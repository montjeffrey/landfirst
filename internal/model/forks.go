package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		// InProgress is deliberately NOT excluded here (an earlier version of
		// this filter did exclude it): a unit stuck mid-rebase is exactly the
		// state most worth surfacing, and forks.go feeds the topology view's
		// row list, not just the merge plan - excluding it there made the
		// tool's single most urgent state invisible, with the header banner
		// actively claiming "nothing in flight." buildPlan (plan.go) still
		// excludes it from the *merge-order suggestion*, which is the part
		// that's actually wrong to offer for a unit mid-operation.
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

// isolatedObjectEnv creates a throwaway object store for merge-tree
// --write-tree probes, so a build never leaves loose objects in the real
// repo. Reads still see every real object via GIT_ALTERNATE_OBJECT_DIRECTORIES;
// only writes are diverted. Verified empirically: an empty temp dir needs no
// setup - git creates the two-hex-char loose-object subdirs on demand - and
// `git count-objects -v` on the real repo is unchanged after a --write-tree
// probe run this way.
//
// git rev-parse --git-common-dir (not a hardcoded ".git/objects") finds the
// store shared across every worktree, which is the right target regardless
// of whether r.Root itself is a linked worktree.
func isolatedObjectEnv(r *git.Repo) (env []string, cleanup func(), err error) {
	common := resolveGitPath(r.Root, "rev-parse", "--git-common-dir")
	if common == "" {
		return nil, func() {}, fmt.Errorf("resolve git-common-dir for %s", r.Root)
	}
	realObjects := filepath.Join(common, "objects")

	tmp, err := os.MkdirTemp("", "landfirst-objects-*")
	if err != nil {
		return nil, func() {}, err
	}
	env = []string{
		"GIT_OBJECT_DIRECTORY=" + tmp,
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + realObjects,
	}
	cleanup = func() { os.RemoveAll(tmp) }
	return env, cleanup, nil
}

// probeMerge asks git whether a and b would conflict. mode "full" uses
// merge-tree --write-tree, which commits a real merge-tree object into
// whatever object store it's pointed at - isolatedEnv (see isolatedObjectEnv)
// diverts that write away from the repo's real .git/objects. The legacy
// 3-arg form is read-only and needs no isolation.
func probeMerge(r *git.Repo, mode, a, b string, isolatedEnv []string) (conflicts []string, unknown bool) {
	if mode == "full" {
		out, err := git.RunCtx(context.Background(), r.Root, isolatedEnv, "merge-tree", "--write-tree", "--name-only", a, b)
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

// buildContentions measures genuine overlap between branches that share a fork
// point. Pairs from different forks are not compared: their conflict count
// reports era drift, not contention, and including them buries the real signal.
func buildContentions(r *git.Repo, forks []ForkPoint, mode string, maxPairs int) []Contention {
	// Isolation is only needed for the "full" --write-tree path (the legacy
	// path is read-only), and is set up once for the whole probing phase -
	// not once per pair, which would be wasteful.
	var isolatedEnv []string
	canProbe := true
	if mode == "full" {
		env, cleanup, err := isolatedObjectEnv(r)
		if err != nil {
			// Can't guarantee a --write-tree probe won't touch the real
			// object store, so skip probing this build rather than risk it.
			// Real/Files below do not depend on probing at all.
			canProbe = false
		} else {
			isolatedEnv = env
			defer cleanup()
		}
	}

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
				if canProbe {
					if cf, unknown := probeMerge(r, mode, a, b, isolatedEnv); !unknown {
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
				}
				out = append(out, c)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Real > out[j].Real })
	return out
}
