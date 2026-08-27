package model

import (
	"testing"

	"github.com/montjeffrey/landfirst/internal/git"
)

// forkFixture: three branches off one fork (two fighting over a file, one not),
// plus a stale branch that forked from an older commit on the base.
func forkFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.commit("shared.py", "one\ntwo\nthree", "add shared")
	f.run("push", "-q", "origin", "main")

	for _, b := range []string{"feat/a", "feat/b", "feat/c"} {
		f.run("checkout", "-q", "main")
		f.run("checkout", "-q", "-b", b)
	}
	f.run("checkout", "-q", "feat/a")
	f.commit("shared.py", "one\nA\nthree", "a edits shared")
	f.run("checkout", "-q", "feat/b")
	f.commit("shared.py", "one\nB\nthree", "b edits shared")
	f.run("checkout", "-q", "feat/c")
	f.commit("other.py", "unrelated", "c edits something else")

	// Move the base on, then fork a stale branch from before that.
	f.run("checkout", "-q", "main")
	for i := 0; i < 4; i++ {
		f.commit("drift.txt", "drift "+string(rune('a'+i)), "base moves on")
	}
	f.run("checkout", "-q", "-b", "feat/stale", "HEAD~2")
	f.commit("shared.py", "one\nSTALE\nthree", "stale edits shared")
	f.run("checkout", "-q", "main")
	f.run("push", "-q", "origin", "main")
	return f
}

func buildFork(t *testing.T, f *fixture) *Model {
	t.Helper()
	repo, err := git.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	return Build(repo, Options{Probe: true, MaxPairs: 50})
}

func TestBranchesGroupByTheCommitTheyForkedFrom(t *testing.T) {
	m := buildFork(t, forkFixture(t))
	if len(m.Forks) < 2 {
		t.Fatalf("expected at least two distinct fork points, got %d", len(m.Forks))
	}
	where := map[string]int{}
	for _, fp := range m.Forks {
		for _, b := range fp.Branches {
			where[b] = fp.Behind
		}
	}
	// The three peers share a fork; the stale branch left later, so it sits
	// closer to the base tip.
	if where["feat/a"] != where["feat/b"] || where["feat/b"] != where["feat/c"] {
		t.Fatalf("peers should share a fork point, got %v", where)
	}
	if where["feat/stale"] == where["feat/a"] {
		t.Fatalf("the stale branch forked elsewhere but grouped with the peers: %v", where)
	}
}

// Forks must be ordered nearest-first: those are the branches in play now.
func TestForksAreOrderedNearestFirst(t *testing.T) {
	m := buildFork(t, forkFixture(t))
	for i := 1; i < len(m.Forks); i++ {
		if m.Forks[i-1].Behind > m.Forks[i].Behind {
			t.Fatalf("forks out of order: %d before %d", m.Forks[i-1].Behind, m.Forks[i].Behind)
		}
	}
}

// The whole point of the metric: contention means both branches actually
// changed the same file since their shared fork, not that their diffs overlap
// because one carries months of unrelated history.
func TestContentionCountsOnlyFilesBothBranchesChanged(t *testing.T) {
	m := buildFork(t, forkFixture(t))

	var ab *Contention
	for i := range m.Contentions {
		c := &m.Contentions[i]
		if (c.A == "feat/a" && c.B == "feat/b") || (c.A == "feat/b" && c.B == "feat/a") {
			ab = c
		}
	}
	if ab == nil {
		t.Fatal("feat/a and feat/b both edited shared.py and must be reported as contending")
	}
	if ab.Real != 1 || len(ab.Files) != 1 || ab.Files[0] != "shared.py" {
		t.Fatalf("expected exactly shared.py, got real=%d files=%v", ab.Real, ab.Files)
	}

	// feat/c touched only its own file and must not appear at all.
	for _, c := range m.Contentions {
		if c.A == "feat/c" || c.B == "feat/c" {
			t.Fatalf("feat/c shares no files but was reported contending: %+v", c)
		}
	}
}

// Pairs from different fork points are deliberately not compared: their
// conflict count reports era drift and buries the real signal.
func TestContentionIsNotComputedAcrossForkPoints(t *testing.T) {
	m := buildFork(t, forkFixture(t))
	for _, c := range m.Contentions {
		if !c.SameFork {
			t.Fatalf("contention reported across fork points: %+v", c)
		}
		if (c.A == "feat/stale") != (c.B == "feat/stale") {
			// stale is alone at its fork, so it can never legitimately pair
			t.Fatalf("the stale branch was compared across forks: %+v", c)
		}
	}
}
