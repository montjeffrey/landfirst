package model

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/montjeffrey/landfirst/internal/engine/state"
	"github.com/montjeffrey/landfirst/internal/git"
	"golang.org/x/sync/errgroup"
)

// baseRef prefers the remote-tracking base, because "did this land" means
// "is it on the remote", not "is it in my local copy of main".
func baseRef(r *git.Repo) string {
	remote := r.Remote + "/" + r.Base
	if git.Try(r.Root, "rev-parse", "--verify", "--quiet", remote) != "" {
		return remote
	}
	return r.Base
}

type cherryResult struct{ Unlanded, Equivalent int }

// unlanded counts changes genuinely absent from the base.
//
// rev-list --count compares commit hashes and is wrong for this question: a
// squash-merged branch, or one forked before a history rewrite, reports every
// post-fork commit as missing. One backup branch scored 424 that way when the
// true answer was 1. git cherry compares patch ids, so '+' means genuinely
// absent and '-' means an equivalent change is already there. That difference
// is what separates "an agent's work never landed" from "this is an old fork".
//
// mu guards cache. Build() hands unlanded() a fresh map every call and every
// branch has a unique name, so - confirmed by reading Build() and this
// function together, not assumed - this cache structurally never produces a
// hit within one build; it does no useful memoizing today. But buildUnits()
// now calls this (via judge()) from multiple goroutines at once, and an
// unsynchronized map write from concurrent goroutines is a real data race
// the instant it happens, hit or no hit. The lock is cheap insurance against
// the day two branches do share a ref, or a future caller reuses one cache
// across builds.
func unlanded(r *git.Repo, ref string, cache map[string]cherryResult, mu *sync.Mutex) cherryResult {
	mu.Lock()
	v, ok := cache[ref]
	mu.Unlock()
	if ok {
		return v
	}
	var res cherryResult
	for _, l := range git.Lines(r.Root, "cherry", baseRef(r), ref) {
		switch {
		case strings.HasPrefix(l, "+"):
			res.Unlanded++
		case strings.HasPrefix(l, "-"):
			res.Equivalent++
		}
	}
	mu.Lock()
	cache[ref] = res
	mu.Unlock()
	return res
}

func rawDistance(r *git.Repo, ref string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(git.Try(r.Root, "rev-list", "--count", baseRef(r)+".."+ref)))
	return n
}

// upstreamIsWrong reports a branch tracking something other than its own remote
// counterpart. Pushing from one of these either fails or publishes nothing,
// while the agent reports success - the most common way work silently vanishes.
func upstreamIsWrong(r *git.Repo, b rawBranch) bool {
	if b.Upstream == "" || b.Gone {
		return false
	}
	return b.Upstream != r.Remote+"/"+b.Name
}

// judge derives a branch's verdict via state.Derive from one already-known
// tuple of facts, then reconstructs the detail text from those same facts -
// Derive itself returns only a Verdict, no text, by design.
//
// unlanded() (a git cherry spawn) only runs when the verdict actually depends
// on it - Gone or no-upstream, state.Derive's rows 2/3/5/6 - mirroring the
// switch this replaced. A first version called it unconditionally for every
// branch, reasoning that Observation should just read the whole tuple once;
// a review caught that this reintroduces the exact "dozens of spawns" cost
// collectBranches's own doc comment says made the tool unusably slow on
// Windows, just moved from for-each-ref onto cherry. Diverged/Unpushed
// branches still report Unlanded/Equivalent as 0 (a spawn-saving stand-in,
// not a real cherry comparison) - unchanged from before this file's rewrite.
func judge(r *git.Repo, b rawBranch, worktrees []Worktree, cache map[string]cherryResult, mu *sync.Mutex) (Verdict, string, int, int) {
	op := OpNone
	if len(worktrees) > 0 {
		op = worktrees[0].Op // a branch has at most one worktree checked out; git enforces that
	}
	var c cherryResult
	if op == OpNone && (b.Gone || b.Upstream == "") {
		c = unlanded(r, b.Name, cache, mu)
	}

	v := Verdict(state.Derive(state.Observation{
		Op:                 state.InProgressOp(op),
		UpstreamConfigured: b.Upstream != "",
		UpstreamGone:       b.Gone,
		UpstreamMismatch:   upstreamIsWrong(r, b),
		Ahead:              b.Ahead,
		Behind:             b.Behind,
		Unlanded:           c.Unlanded,
		Equivalent:         c.Equivalent,
	}))

	var detail string
	switch v {
	case InProgress:
		detail = string(op) + " in progress"
	case Orphaned:
		detail = plural(c.Unlanded, "change") + verb(c.Unlanded, " exists", " exist") + " only here"
		if c.Equivalent > 0 {
			detail += " (" + strconv.Itoa(c.Equivalent) + " already in " + r.Base + ")"
		}
	case WrongUpstream:
		detail = "tracks " + b.Upstream + ", not " + r.Remote + "/" + b.Name
	case NeverPushed:
		detail = plural(c.Unlanded, "change") + " never pushed"
		if c.Equivalent > 0 {
			detail += " (" + strconv.Itoa(c.Equivalent) + " of " + strconv.Itoa(rawDistance(r, b.Name)) + " already in " + r.Base + ")"
		}
	case SafeToDelete:
		detail = "every change is already in " + r.Base
	case Diverged:
		detail = strconv.Itoa(b.Ahead) + " ahead, " + strconv.Itoa(b.Behind) + " behind"
	case Unpushed:
		detail = plural(b.Ahead, "commit") + " ahead of its remote"
	case Behind:
		detail = strconv.Itoa(b.Behind) + " behind"
	default: // Landed
		detail = "in sync"
	}
	return v, detail, c.Unlanded, c.Equivalent
}

// verb picks agreement for the count that precedes it.
func verb(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func plural(n int, word string) string {
	s := strconv.Itoa(n) + " " + word
	if n != 1 {
		s += "s"
	}
	return s
}

// remedy returns the exact command shown on screen. The action layer runs this
// same slice, so what you approve is literally what executes - always against
// r.Root (run(), in internal/tui/model.go, never cd's into a worktree). That
// is exactly why every advisory (Safe: false) remedy below with an associated
// worktree elsewhere gets Dir set: the command is correct only in that
// directory, and nothing here ever auto-executes it there - Dir is purely
// informational until something Safe:true needs a non-root cwd, which
// nothing does yet.
//
// InProgress gets no case, deliberately: there is no single command that is
// a safe or even correct suggestion without knowing whether the user wants
// to continue or abort the stuck operation - continue can commit unresolved
// conflict markers, abort can discard conflict-resolution work in progress.
// Falling through to nil matches the precedence table's own framing: this
// verdict blocks every action on the unit, offered ones included.
func remedy(r *git.Repo, u *Unit) *Remedy {
	if r.Remote == "" {
		switch u.Verdict {
		case Orphaned, WrongUpstream, NeverPushed, Unpushed:
			// git.Open leaves Remote empty rather than guessing one, for a
			// legitimate local-only repo (see internal/git/open.go). Every
			// case below this point assumes a remote to push to; offering one
			// here would build "git push -u '' branch:branch" and, worse,
			// mark it Safe - one keypress away from actually running it.
			return nil
		}
	}
	push := []string{"push", "-u", r.Remote, u.Branch + ":" + u.Branch}
	var rem *Remedy
	switch u.Verdict {
	case Orphaned:
		rem = &Remedy{Summary: "publish it again before the work is lost", Args: push, Safe: true}
	case WrongUpstream:
		rem = &Remedy{Summary: "point it at " + r.Remote + "/" + u.Branch + " and publish", Args: push, Safe: true}
	case NeverPushed:
		rem = &Remedy{Summary: "publish for the first time", Args: push, Safe: true}
	case Unpushed:
		rem = &Remedy{Summary: "push the outstanding commits", Args: []string{"push", r.Remote, u.Branch + ":" + u.Branch}, Safe: true}
	case Diverged:
		// Never Safe: rewrites local history, and an agent may be mid-commit.
		rem = &Remedy{Summary: "reconcile by hand - both sides moved", Args: []string{"rebase", r.Remote + "/" + u.Branch}, Safe: false}
	case SafeToDelete:
		rem = &Remedy{Summary: "already in " + r.Base + "; delete the local branch", Args: []string{"branch", "-d", u.Branch}, Safe: true}
	case Detached:
		// switch -c creates a branch at the worktree's own current HEAD - the
		// correct fix, but only in the worktree HEAD actually refers to (see
		// the Dir assignment below). Never Safe: auto-running this against
		// r.Root's HEAD when the detached worktree is a different directory
		// would create the branch at the wrong commit, silently.
		rem = &Remedy{Summary: "give this work a branch before it is lost", Args: []string{"switch", "-c", "wip/" + u.Head}, Safe: false}
	}
	if rem != nil && !rem.Safe && len(u.Worktrees) > 0 && u.Worktrees[0].Path != r.Root {
		rem.Dir = u.Worktrees[0].Path
	}
	return rem
}

func buildUnits(r *git.Repo, trees []Worktree, cache map[string]cherryResult) []Unit {
	byBranch := map[string][]Worktree{}
	for _, w := range trees {
		if w.Branch != "" {
			byBranch[w.Branch] = append(byBranch[w.Branch], w)
		}
	}
	// byBranch is fully built above and never written again - concurrent reads
	// of it from the goroutines below are safe with no lock.

	branches := collectBranches(r)
	// Pre-sized and written to units[i] by the goroutine that owns index i,
	// never append()'d: two detached units (below, still serial) can tie on
	// every sort key, and append from concurrent goroutines would make
	// sort.SliceStable's tie-break - and so the final order - depend on
	// goroutine finish timing instead of being reproducible. Branch units
	// can't actually tie (names are unique) but writing by index keeps one
	// discipline for the whole slice instead of a special case for half of it.
	units := make([]Unit, len(branches))
	var mu sync.Mutex // guards cache; see unlanded()'s doc for why this is needed despite the cache being a structural no-op today
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for i, b := range branches {
		g.Go(func() error {
			// Worktree lookup happens before judge() now: row 1 of the precedence
			// table needs the branch's checked-out worktree (if any) and its Op
			// before a verdict can be derived, not after.
			wts := byBranch[b.Name]
			v, detail, un, eq := judge(r, b, wts, cache, &mu)
			u := Unit{
				Branch: b.Name, Head: short(b.Head), Upstream: b.Upstream,
				Ahead: b.Ahead, Behind: b.Behind, Unlanded: un, Equivalent: eq,
				Date: b.Date, Author: b.Author, Subject: b.Subject,
				Worktrees: wts, Verdict: v, Detail: detail,
			}
			for _, w := range u.Worktrees {
				u.Risks = append(u.Risks, w.Risks...)
			}
			u.Remedy = remedy(r, &u)
			units[i] = u
			return nil
		})
	}
	g.Wait() // every g.Go func above only ever returns nil

	// Detached worktrees hold commits no branch points at, so no branch-oriented
	// view will ever show them. They get their own units.
	//
	// A worktree stuck mid-rebase is detached too - rebase replays commits
	// with HEAD off any branch, and the branch ref itself doesn't move until
	// it finishes - so in practice this loop, not judge()'s row 1, is what
	// catches a mid-rebase worktree. Op != OpNone here means InProgress
	// instead of Detached: a detached worktree mid-rebase is more precarious
	// than a merely detached one, not less.
	for _, w := range trees {
		if !w.Detached {
			continue
		}
		c := unlanded(r, w.Head, cache, &mu)
		date, _ := time.Parse(time.RFC3339, strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%cI", w.Head)))
		v, detail := Detached, "no branch points here"
		if w.Op != OpNone {
			v, detail = InProgress, string(w.Op)+" in progress"
		}
		u := Unit{
			Head: short(w.Head), Date: date,
			Author:    strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%an", w.Head)),
			Subject:   strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%s", w.Head)),
			Worktrees: []Worktree{w}, Risks: w.Risks,
			Verdict: v, Detail: detail, Unlanded: c.Unlanded,
		}
		u.Remedy = remedy(r, &u)
		units = append(units, u)
	}

	sort.SliceStable(units, func(i, j int) bool {
		ri, rj := Verdicts[units[i].Verdict].Rank, Verdicts[units[j].Verdict].Rank
		if ri != rj {
			return ri < rj
		}
		if units[i].Unlanded != units[j].Unlanded {
			return units[i].Unlanded > units[j].Unlanded
		}
		return units[i].Branch < units[j].Branch
	})
	return units
}

func short(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}
