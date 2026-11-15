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
	for _, f := range r.Lines("diff", "--name-only", baseRef(r)+"..."+git.HeadRef(branch)) {
		set[f] = true
	}
	return set
}

// buildPlan orders branches by how safe they are to merge next.
//
// Conflict counts come from contentions (buildContentions, forks.go) instead
// of a probe loop of its own: that function already restricts itself to
// same-fork pairs with genuine file overlap - it only ever constructs a
// Contention when len(shared) > 0 - so every entry already represents a real
// collision. Comparing every candidate pair here too, cross-fork included,
// would just re-measure the era drift Contention's doc comment describes and
// duplicate work buildContentions already did more precisely.
//
// A branch is only ever compared with branches from its own fork point, and
// only up to the -max-pairs cap. Every other candidate is counted in
// NotCompared so that a 0 in Conflicts is never mistaken for "checked, clean".
func buildPlan(r *git.Repo, units []Unit, forks []ForkPoint, contentions []Contention, skipped [][2]string, probed bool) []PlanItem {
	type cand struct {
		u     Unit
		files map[string]bool
	}
	var cands []cand
	for _, u := range units {
		if u.Branch == "" || u.Branch == r.Base {
			continue
		}
		// Only branches with work worth merging. InProgress is excluded too:
		// its whole point is that no action on the unit is safe to suggest
		// until the stuck rebase/merge/cherry-pick resolves - recommending a
		// merge order for it would be exactly the wrong kind of suggestion.
		if u.Verdict == SafeToDelete || u.Verdict == Landed || u.Verdict == Behind || u.Verdict == InProgress {
			continue
		}
		cands = append(cands, cand{u, changedFiles(r, u.Branch)})
	}

	forkOf := map[string]string{}
	for _, fp := range forks {
		for _, b := range fp.Branches {
			forkOf[b] = fp.SHA
		}
	}
	notCompared := map[string]int{}
	if probed {
		for i := range cands {
			for j := i + 1; j < len(cands); j++ {
				a, b := cands[i].u.Branch, cands[j].u.Branch
				if fa, fb := forkOf[a], forkOf[b]; fa == "" || fb == "" || fa != fb {
					notCompared[a]++
					notCompared[b]++
				}
			}
		}
		for _, p := range skipped {
			notCompared[p[0]]++
			notCompared[p[1]]++
		}
	}

	collideCount := map[string]int{}
	for _, c := range contentions {
		collideCount[c.A]++
		collideCount[c.B]++
	}

	items := make([]PlanItem, 0, len(cands))
	for _, c := range cands {
		var why []string
		conf := -1
		if probed {
			conf = collideCount[c.u.Branch]
			if conf == 0 && notCompared[c.u.Branch] > 0 {
				why = append(why, "collides with nothing it was compared with")
			} else if conf == 0 {
				why = append(why, "collides with nothing")
			} else {
				why = append(why, strconv.Itoa(conf)+" colliding branch(es)")
			}
		}
		behind := behindBase(r, c.u.Branch)
		if behind > 0 {
			why = append(why, strconv.Itoa(behind)+" behind "+r.Base)
		}
		if n := notCompared[c.u.Branch]; n > 0 {
			why = append(why, "not compared with "+strconv.Itoa(n)+" other branch(es)")
		}
		why = append(why, strconv.Itoa(len(c.files))+" file(s) changed")
		if n := len(c.u.Worktrees); n > 0 {
			why = append(why, "checked out in "+strconv.Itoa(n)+" worktree(s)")
		}
		items = append(items, PlanItem{
			Branch: c.u.Branch, Ahead: c.u.Unlanded, BehindN: behind,
			Conflicts: conf, NotCompared: notCompared[c.u.Branch], Why: why,
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
	return items
}

// behindBase counts the commits on the base that branch does not have: how far
// the branch has drifted from where it will merge. Unit.Behind is something
// else (commits behind the branch's own upstream) and is 0 for a branch with
// no upstream however stale it is.
func behindBase(r *git.Repo, branch string) int {
	n, err := strconv.Atoi(strings.TrimSpace(r.Try("rev-list", "--count", git.HeadRef(branch)+".."+baseRef(r), "--")))
	if err != nil {
		return 0
	}
	return n
}
