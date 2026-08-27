package model

import (
	"sort"
	"strconv"
	"strings"

	"github.com/montjeffrey/landfirst/internal/git"
)

// ForkPoint is a commit on the base that one or more branches diverged from.
//
// Grouping by fork point is what makes a pile of branches legible: branches
// that left the base together are peers working on the same code at the same
// time, and branches that left long ago are stale forks whose diff against the
// base is mostly drift. Those two need different handling, and a commit graph
// ordered by date does not distinguish them.
type ForkPoint struct {
	SHA      string   `json:"sha"`
	Short    string   `json:"short"`
	Subject  string   `json:"subject"`
	Behind   int      `json:"behind"` // commits from here to the base tip
	Branches []string `json:"branches"`
}

// Contention is what two branches genuinely fight over.
//
// A raw conflict count conflates two unrelated things. Two branches that forked
// from different eras conflict on hundreds of files simply because one carries
// months of unrelated history; two branches that forked together and both edited
// the same handful of files conflict on those files. Only the second is
// contention in any useful sense, and on a real repository the first drowns it
// out - measured median 14 files against a maximum of 203.
type Contention struct {
	A          string   `json:"a"`
	B          string   `json:"b"`
	Real       int      `json:"real"`       // files BOTH changed since their common ancestor
	Files      []string `json:"files"`      // those files, capped
	Structural int      `json:"structural"` // conflicts outside that set: era drift
	SameFork   bool     `json:"sameFork"`
}

// changedSince returns the files a ref changed relative to an ancestor.
func changedSince(r *git.Repo, ancestor, ref string) map[string]bool {
	set := map[string]bool{}
	for _, f := range git.Lines(r.Root, "diff", "--name-only", ancestor, ref) {
		set[f] = true
	}
	return set
}

// buildForks groups branches by the commit they forked from.
func buildForks(r *git.Repo, units []Unit) []ForkPoint {
	bySHA := map[string]*ForkPoint{}
	for _, u := range units {
		if u.Branch == "" || u.Branch == r.Base {
			continue
		}
		if u.Verdict == Landed || u.Verdict == SafeToDelete {
			continue
		}
		mb := strings.TrimSpace(git.Try(r.Root, "merge-base", baseRef(r), u.Branch))
		if mb == "" {
			continue
		}
		fp, ok := bySHA[mb]
		if !ok {
			n, _ := strconv.Atoi(strings.TrimSpace(
				git.Try(r.Root, "rev-list", "--count", mb+".."+baseRef(r))))
			fp = &ForkPoint{
				SHA:     mb,
				Short:   short(mb),
				Subject: strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%s", mb)),
				Behind:  n,
			}
			bySHA[mb] = fp
		}
		fp.Branches = append(fp.Branches, u.Branch)
	}

	out := make([]ForkPoint, 0, len(bySHA))
	for _, fp := range bySHA {
		sort.Strings(fp.Branches)
		out = append(out, *fp)
	}
	// Nearest fork first: those are the branches in play right now.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Behind != out[j].Behind {
			return out[i].Behind < out[j].Behind
		}
		return out[i].SHA < out[j].SHA
	})
	return out
}

// buildContentions measures genuine overlap between branches that share a fork
// point. Pairs from different forks are not compared: their conflict count
// reports era drift, not contention, and including them buries the real signal.
func buildContentions(r *git.Repo, forks []ForkPoint, mode string, maxPairs int) []Contention {
	var out []Contention
	pairs := 0
	for _, fp := range forks {
		if len(fp.Branches) < 2 {
			continue
		}
		// Files each branch changed since the shared fork point.
		changed := make(map[string]map[string]bool, len(fp.Branches))
		for _, b := range fp.Branches {
			changed[b] = changedSince(r, fp.SHA, b)
		}
		for i := 0; i < len(fp.Branches); i++ {
			for j := i + 1; j < len(fp.Branches); j++ {
				if maxPairs > 0 && pairs >= maxPairs {
					return out
				}
				a, b := fp.Branches[i], fp.Branches[j]
				var shared []string
				for f := range changed[a] {
					if changed[b][f] {
						shared = append(shared, f)
					}
				}
				if len(shared) == 0 {
					continue
				}
				sort.Strings(shared)
				pairs++

				c := Contention{A: a, B: b, Real: len(shared), SameFork: true}
				if len(shared) > 12 {
					c.Files = shared[:12]
				} else {
					c.Files = shared
				}
				// Anything git reports beyond the genuinely shared set is drift.
				if cf, unknown := probeMerge(r, mode, a, b); !unknown {
					inShared := map[string]bool{}
					for _, f := range shared {
						inShared[f] = true
					}
					for _, f := range cf {
						if !inShared[f] {
							c.Structural++
						}
					}
				}
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Real > out[j].Real })
	return out
}
