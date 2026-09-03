package model

import (
	"sort"
	"strconv"

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

// buildPlan orders branches by how safe they are to merge next.
//
// Conflict counts come from contentions (buildContentions, forks.go) instead
// of a probe loop of its own: that function already restricts itself to
// same-fork pairs with genuine file overlap - it only ever constructs a
// Contention when len(shared) > 0 - so every entry already represents a real
// collision. Comparing every candidate pair here too, cross-fork included,
// would just re-measure the era drift Contention's doc comment describes and
// duplicate work buildContentions already did more precisely.
func buildPlan(r *git.Repo, units []Unit, contentions []Contention, probed bool) []PlanItem {
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
	return items
}
