package model

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
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
func unlanded(r *git.Repo, ref string, cache map[string]cherryResult) cherryResult {
	if v, ok := cache[ref]; ok {
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
	cache[ref] = res
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

func judge(r *git.Repo, b rawBranch, cache map[string]cherryResult) (Verdict, string, int, int) {
	switch {
	case b.Gone:
		c := unlanded(r, b.Name, cache)
		if c.Unlanded > 0 {
			d := plural(c.Unlanded, "change") + verb(c.Unlanded, " exists", " exist") + " only here"
			if c.Equivalent > 0 {
				d += " (" + strconv.Itoa(c.Equivalent) + " already in " + r.Base + ")"
			}
			return Orphaned, d, c.Unlanded, c.Equivalent
		}
		return SafeToDelete, "every change is already in " + r.Base, 0, c.Equivalent

	case upstreamIsWrong(r, b):
		c := unlanded(r, b.Name, cache)
		return WrongUpstream, "tracks " + b.Upstream + ", not " + r.Remote + "/" + b.Name, c.Unlanded, c.Equivalent

	case b.Upstream == "":
		c := unlanded(r, b.Name, cache)
		if c.Unlanded > 0 {
			d := plural(c.Unlanded, "change") + " never pushed"
			if c.Equivalent > 0 {
				d += " (" + strconv.Itoa(c.Equivalent) + " of " + strconv.Itoa(rawDistance(r, b.Name)) + " already in " + r.Base + ")"
			}
			return NeverPushed, d, c.Unlanded, c.Equivalent
		}
		return SafeToDelete, "every change is already in " + r.Base, 0, c.Equivalent

	case b.Ahead > 0 && b.Behind > 0:
		return Diverged, strconv.Itoa(b.Ahead) + " ahead, " + strconv.Itoa(b.Behind) + " behind", b.Ahead, 0

	case b.Ahead > 0:
		return Unpushed, plural(b.Ahead, "commit") + " ahead of its remote", b.Ahead, 0

	case b.Behind > 0:
		return Behind, strconv.Itoa(b.Behind) + " behind", 0, 0
	}
	return Landed, "in sync", 0, 0
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
// same slice, so what you approve is literally what executes.
func remedy(r *git.Repo, u *Unit) *Remedy {
	push := []string{"push", "-u", r.Remote, u.Branch + ":" + u.Branch}
	switch u.Verdict {
	case Orphaned:
		return &Remedy{"publish it again before the work is lost", push, true}
	case WrongUpstream:
		return &Remedy{"point it at " + r.Remote + "/" + u.Branch + " and publish", push, true}
	case NeverPushed:
		return &Remedy{"publish for the first time", push, true}
	case Unpushed:
		return &Remedy{"push the outstanding commits", []string{"push", r.Remote, u.Branch + ":" + u.Branch}, true}
	case Diverged:
		// Never Safe: rewrites local history, and an agent may be mid-commit.
		return &Remedy{"reconcile by hand - both sides moved", []string{"rebase", r.Remote + "/" + u.Branch}, false}
	case SafeToDelete:
		return &Remedy{"already in " + r.Base + "; delete the local branch", []string{"branch", "-d", u.Branch}, true}
	case Detached:
		return &Remedy{"give this work a branch before it is lost", nil, false}
	}
	return nil
}

func buildUnits(r *git.Repo, trees []Worktree, cache map[string]cherryResult) []Unit {
	byBranch := map[string][]Worktree{}
	for _, w := range trees {
		if w.Branch != "" {
			byBranch[w.Branch] = append(byBranch[w.Branch], w)
		}
	}

	var units []Unit
	for _, b := range collectBranches(r) {
		v, detail, un, eq := judge(r, b, cache)
		u := Unit{
			Branch: b.Name, Head: short(b.Head), Upstream: b.Upstream,
			Ahead: b.Ahead, Behind: b.Behind, Unlanded: un, Equivalent: eq,
			Date: b.Date, Author: b.Author, Subject: b.Subject,
			Worktrees: byBranch[b.Name], Verdict: v, Detail: detail,
		}
		for _, w := range u.Worktrees {
			u.Risks = append(u.Risks, w.Risks...)
		}
		u.Remedy = remedy(r, &u)
		units = append(units, u)
	}

	// Detached worktrees hold commits no branch points at, so no branch-oriented
	// view will ever show them. They get their own units.
	for _, w := range trees {
		if !w.Detached {
			continue
		}
		c := unlanded(r, w.Head, cache)
		date, _ := time.Parse(time.RFC3339, strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%cI", w.Head)))
		u := Unit{
			Head: short(w.Head), Date: date,
			Author:    strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%an", w.Head)),
			Subject:   strings.TrimSpace(git.Try(r.Root, "log", "-1", "--format=%s", w.Head)),
			Worktrees: []Worktree{w}, Risks: w.Risks,
			Verdict: Detached, Detail: "no branch points here", Unlanded: c.Unlanded,
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
