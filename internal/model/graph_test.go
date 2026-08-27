package model

import (
	"strconv"
	"testing"

	"github.com/montjeffrey/landfirst/internal/git"
)

// deepFixture builds a graph big enough that one extra commit is a small
// perturbation. Churn ratios measured on a 10-commit graph are noise: the same
// code that moves 3 of 10 there moves 0 of 534 on a real repository.
func deepFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	for i := 0; i < 8; i++ {
		// Content must differ each time or git has nothing to commit.
		f.commit("trunk.txt", "trunk line "+strconv.Itoa(i), "trunk commit "+strconv.Itoa(i))
	}
	f.run("push", "-q", "origin", "main")
	for _, n := range []string{"feat/a", "feat/b", "feat/c"} {
		f.run("checkout", "-q", "main")
		f.run("checkout", "-q", "-b", n)
		for i := 0; i < 5; i++ {
			f.commit(n[5:]+".txt", n+" work "+strconv.Itoa(i), n+" commit "+strconv.Itoa(i))
		}
	}
	f.run("checkout", "-q", "main")
	return f
}

func graphOf(t *testing.T, f *fixture) *Graph {
	t.Helper()
	repo, err := git.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	m := Build(repo, Options{Probe: false})
	if m.Graph == nil {
		t.Fatal("no graph in model")
	}
	return m.Graph
}

func TestGraphIsIdenticalAcrossIdenticalBuilds(t *testing.T) {
	f := deepFixture(t)
	a, b := graphOf(t, f), graphOf(t, f)

	if len(a.Commits) != len(b.Commits) {
		t.Fatalf("commit count drifted: %d vs %d", len(a.Commits), len(b.Commits))
	}
	for i := range a.Commits {
		x, y := a.Commits[i], b.Commits[i]
		if x.Hash != y.Hash || x.Lane != y.Lane || x.Color != y.Color {
			t.Fatalf("row %d drifted between identical builds: %+v vs %+v", i, x, y)
		}
	}
	// The watcher rebuilds on index and lock churn too, so a no-op rebuild is
	// the most common case in live use. It must be a perfect visual no-op.
	if len(a.Edges) != len(b.Edges) {
		t.Fatalf("edge count drifted: %d vs %d", len(a.Edges), len(b.Edges))
	}
}

func TestNewCommitDoesNotRecolourTheGraph(t *testing.T) {
	f := deepFixture(t)
	before := graphOf(t, f)

	f.run("checkout", "-q", "feat/a")
	f.commit("a.txt", "one more", "one more on feat/a")
	f.run("checkout", "-q", "main")
	after := graphOf(t, f)

	now := make(map[string]Commit, len(after.Commits))
	for _, c := range after.Commits {
		now[c.Hash] = c
	}
	var shared, laneMoved, colourMoved int
	for _, c := range before.Commits {
		o, ok := now[c.Hash]
		if !ok {
			continue
		}
		shared++
		if o.Lane != c.Lane {
			laneMoved++
		}
		if o.Color != c.Color {
			colourMoved++
		}
	}
	if shared < 15 {
		t.Fatalf("expected a deep graph to compare, got %d shared commits", shared)
	}
	// Colour is pinned to branch identity and must never move: it is the cue
	// the eye tracks, and churn made the graph appear to flicker.
	if colourMoved != 0 {
		t.Fatalf("%d/%d commits changed colour", colourMoved, shared)
	}
	if ratio := float64(laneMoved) / float64(shared); ratio > 0.10 {
		t.Fatalf("%d/%d commits jumped lanes - the graph is re-packing itself", laneMoved, shared)
	}
}

func TestBaseOwnsLaneZeroForItsWholeSpine(t *testing.T) {
	f := deepFixture(t)
	g := graphOf(t, f)

	byHash := make(map[string]Commit, len(g.Commits))
	for _, c := range g.Commits {
		byHash[c.Hash] = c
	}
	var tip *Commit
	for i, c := range g.Commits {
		for _, r := range c.Refs {
			if r == "main" || r == "HEAD -> main" {
				tip = &g.Commits[i]
			}
		}
		if tip != nil {
			break
		}
	}
	if tip == nil {
		t.Fatal("base tip is not in the graph")
	}
	if tip.Lane != 0 {
		t.Fatalf("the trunk must be the leftmost lane, got %d", tip.Lane)
	}
	for cur := *tip; len(cur.Parents) > 0; {
		next, ok := byHash[cur.Parents[0]]
		if !ok {
			break
		}
		if next.Lane != 0 {
			t.Fatalf("trunk commit %s wandered to lane %d", next.Short, next.Lane)
		}
		cur = next
	}
}

func TestEveryBranchConnectsToTheTrunk(t *testing.T) {
	f := deepFixture(t)
	g := graphOf(t, f)

	byHash := make(map[string]Commit, len(g.Commits))
	for _, c := range g.Commits {
		byHash[c.Hash] = c
	}
	var tipHash string
	for _, c := range g.Commits {
		for _, r := range c.Refs {
			if r == "main" || r == "HEAD -> main" {
				tipHash = c.Hash
			}
		}
	}
	trunk := map[string]bool{}
	for stack := []string{tipHash}; len(stack) > 0; {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if trunk[h] {
			continue
		}
		c, ok := byHash[h]
		if !ok {
			continue
		}
		trunk[h] = true
		stack = append(stack, c.Parents...)
	}

	// Every drawn branch tip must have a visible path to the trunk, or it
	// renders floating with no connection to the base.
	for _, name := range g.Drawn {
		if name == "main" {
			continue
		}
		head := ""
		for _, c := range g.Commits {
			for _, r := range c.Refs {
				if r == name {
					head = c.Hash
				}
			}
		}
		if head == "" {
			continue
		}
		seen := map[string]bool{}
		reached := false
		for stack := []string{head}; len(stack) > 0 && !reached; {
			h := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[h] {
				continue
			}
			seen[h] = true
			if trunk[h] {
				reached = true
				break
			}
			if c, ok := byHash[h]; ok {
				stack = append(stack, c.Parents...)
			}
		}
		if !reached {
			t.Fatalf("%s renders with no visible path to main", name)
		}
	}
}

func TestNoTwoCommitsOccupyTheSameCell(t *testing.T) {
	f := deepFixture(t)
	g := graphOf(t, f)
	seen := map[[2]int]string{}
	for _, c := range g.Commits {
		cell := [2]int{c.Row, c.Lane}
		if prev, clash := seen[cell]; clash {
			t.Fatalf("%s and %s collide at row %d lane %d", prev, c.Short, c.Row, c.Lane)
		}
		seen[cell] = c.Short
		if c.Lane < 0 || c.Lane >= g.LaneCount {
			t.Fatalf("%s has lane %d outside 0..%d", c.Short, c.Lane, g.LaneCount-1)
		}
	}
}

// Full topology must mean full. The windowed construction cannot produce it:
// --first-parent skips commits that arrived through a merge, and base..branch
// is empty for a branch already merged in, so merged history goes invisible -
// the exact opposite of what this mode is for.
func TestUnlimitedDrawsTheEntireTopology(t *testing.T) {
	f := deepFixture(t)
	// Merge one branch in, so there is history only reachable through a merge.
	f.run("checkout", "-q", "main")
	f.run("merge", "-q", "--no-ff", "--no-edit", "-m", "merge feat/a", "feat/a")

	repo, err := git.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}

	full := Build(repo, Options{Probe: false, Limit: -1, MaxBranches: -1}).Graph
	if full.Truncated {
		t.Fatal("the unlimited graph reports itself truncated")
	}
	if len(full.Commits) != full.Total {
		t.Fatalf("drew %d commits of %d - unlimited must draw every one", len(full.Commits), full.Total)
	}
	if full.Hidden != 0 {
		t.Fatalf("%d branches hidden in unlimited mode", full.Hidden)
	}

	// Every local branch must have its own lane.
	want := map[string]bool{}
	for _, b := range collectBranches(repo) {
		want[b.Name] = true
	}
	got := map[string]bool{}
	for _, n := range full.Drawn {
		got[n] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("branch %q has no lane in the full topology", name)
		}
	}

	// A commit that exists only on the merged-in side must be present.
	merged := 0
	for _, c := range full.Commits {
		if len(c.Parents) > 1 {
			merged++
		}
	}
	if merged == 0 {
		t.Fatal("no merge commit in the full topology; merged history is missing")
	}
}

func TestBranchCapIsRespectedByDefault(t *testing.T) {
	f := deepFixture(t)
	repo, err := git.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	g := Build(repo, Options{Probe: false, MaxBranches: 2}).Graph
	// base plus the cap; the rest are counted, not drawn.
	if len(g.Drawn) > 3 {
		t.Fatalf("cap of 2 drew %d branches: %v", len(g.Drawn), g.Drawn)
	}
	if g.Hidden == 0 {
		t.Fatal("branches were dropped without being reported as hidden")
	}
}
