package model

import (
	"sort"
	"strconv"
	"strings"

	"github.com/montjeffrey/landfirst/internal/git"
)

// changedFiles is the pre-filter for collisions: two branches that never touch
// the same file cannot conflict, so they are never probed.
func changedFiles(r *git.Repo, branch string) map[string]bool {
	set := map[string]bool{}
	for _, f := range git.Lines(r.Root, "diff", "--name-only", baseRef(r)+"..."+branch) {
		set[f] = true
	}
	return set
}

func probeMerge(r *git.Repo, mode, a, b string) (conflicts []string, unknown bool) {
	if mode == "full" {
		out, err := git.Run(r.Root, "merge-tree", "--write-tree", "--name-only", a, b)
		if err == nil {
			return nil, false // clean
		}
		// Exit 1 means conflicts, and stdout lists them. Any other code is a
		// failure to probe, which must never be reported as "clean".
		if git.ExitCode(err) != 1 {
			return nil, true
		}
		lines := strings.Split(out, "\n")
		for i := 1; i < len(lines); i++ {
			l := strings.TrimSpace(lines[i])
			if l == "" {
				break
			}
			conflicts = append(conflicts, l)
		}
		return conflicts, false
	}

	base := strings.TrimSpace(git.Try(r.Root, "merge-base", a, b))
	if base == "" {
		return nil, true
	}
	out := git.Try(r.Root, "merge-tree", base, a, b)
	if out == "" {
		return nil, true
	}
	return parseLegacyMergeTree(out), false
}

// parseLegacyMergeTree reads pre-2.38 merge-tree output, which describes each
// changed path and shows conflict markers inside the hunk.
func parseLegacyMergeTree(out string) []string {
	seen := map[string]bool{}
	var order []string
	var path string
	for _, raw := range strings.Split(out, "\n") {
		l := strings.TrimRight(raw, "\r")
		if l == "" {
			continue
		}
		if !strings.ContainsAny(l[:1], " \t@+-") {
			path = ""
			continue
		}
		if f := reMergeTreePath.FindStringSubmatch(l); f != nil {
			path = f[1]
			continue
		}
		if path != "" && strings.HasPrefix(l, "+<<<<<<<") && !seen[path] {
			seen[path] = true
			order = append(order, path)
		}
	}
	return order
}

func buildPlan(r *git.Repo, units []Unit, opt Options, mode string) ([]PlanItem, []Collision) {
	type cand struct {
		u     Unit
		files map[string]bool
	}
	var cands []cand
	for _, u := range units {
		if u.Branch == "" || u.Branch == r.Base {
			continue
		}
		// Only branches with work worth merging.
		if u.Verdict == SafeToDelete || u.Verdict == Landed || u.Verdict == Behind {
			continue
		}
		cands = append(cands, cand{u, changedFiles(r, u.Branch)})
	}

	var collisions []Collision
	if opt.Probe {
		pairs := 0
		for i := 0; i < len(cands); i++ {
			for j := i + 1; j < len(cands); j++ {
				if opt.MaxPairs > 0 && pairs >= opt.MaxPairs {
					break
				}
				shared := 0
				for f := range cands[i].files {
					if cands[j].files[f] {
						shared++
					}
				}
				if shared == 0 {
					continue
				}
				pairs++
				cf, unknown := probeMerge(r, mode, cands[i].u.Branch, cands[j].u.Branch)
				collisions = append(collisions, Collision{
					A: cands[i].u.Branch, B: cands[j].u.Branch,
					Shared: shared, Conflicts: cf, Unknown: unknown,
				})
			}
		}
	}

	collideCount := map[string]int{}
	for _, c := range collisions {
		if len(c.Conflicts) > 0 {
			collideCount[c.A]++
			collideCount[c.B]++
		}
	}

	items := make([]PlanItem, 0, len(cands))
	for _, c := range cands {
		var why []string
		conf := -1
		if opt.Probe {
			conf = collideCount[c.u.Branch]
			if conf == 0 {
				why = append(why, "collides with nothing")
			} else {
				why = append(why, strconv.Itoa(conf)+" colliding branch(es)")
			}
		}
		if c.u.Behind > 0 {
			why = append(why, strconv.Itoa(c.u.Behind)+" behind "+r.Base)
		}
		why = append(why, strconv.Itoa(len(c.files))+" file(s) changed")
		if n := len(c.u.Worktrees); n > 0 {
			why = append(why, "checked out in "+strconv.Itoa(n)+" worktree(s)")
		}
		items = append(items, PlanItem{
			Branch: c.u.Branch, Ahead: c.u.Unlanded, BehindN: c.u.Behind,
			Conflicts: conf, Why: why,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		ci, cj := items[i].Conflicts, items[j].Conflicts
		if ci < 0 {
			ci = 0
		}
		if cj < 0 {
			cj = 0
		}
		if ci != cj {
			return ci < cj // fewest collisions first
		}
		if items[i].BehindN != items[j].BehindN {
			return items[i].BehindN < items[j].BehindN
		}
		return items[i].Ahead < items[j].Ahead // smallest diff first
	})
	for i := range items {
		items[i].Order = i + 1
	}
	return items, collisions
}
