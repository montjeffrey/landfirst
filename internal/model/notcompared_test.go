package model

import "testing"

func planItem(m *Model, branch string) *PlanItem {
	for i := range m.Plan {
		if m.Plan[i].Branch == branch {
			return &m.Plan[i]
		}
	}
	return nil
}

func (f *fixture) buildProbed(maxPairs int) *Model {
	f.t.Helper()
	m := Build(f.openRepo(), Options{Probe: true, MaxPairs: maxPairs})
	return m
}

// three branches fork from the same commit and all edit a.txt: three
// overlapping pairs.
func (f *fixture) threeOverlapping() {
	for _, b := range []string{"b1", "b2", "b3"} {
		f.run("checkout", "-q", "-b", b, "main")
		f.commit("a.txt", "edit by "+b, b)
	}
	f.run("checkout", "-q", "main")
}

// -max-pairs used to stop comparing without a word, so a branch that was never
// compared read as "0 colliding". The model must say how many pairs it left
// out and which branches are affected.
func TestMaxPairsTruncationIsReportedAndBranchesAreMarkedNotCompared(t *testing.T) {
	f := newFixture(t)
	f.threeOverlapping()

	full := f.buildProbed(200)
	if full.PairsSkipped != 0 {
		t.Fatalf("nothing should be skipped under a generous cap, got %d", full.PairsSkipped)
	}
	for _, b := range []string{"b1", "b2", "b3"} {
		if it := planItem(full, b); it == nil || it.NotCompared != 0 {
			t.Fatalf("%s: full comparison must leave NotCompared 0, got %+v", b, it)
		}
	}

	m := f.buildProbed(1)
	if m.PairsSkipped != 2 || m.MaxPairs != 1 {
		t.Fatalf("PairsSkipped=%d MaxPairs=%d, want 2 and 1", m.PairsSkipped, m.MaxPairs)
	}
	marked := 0
	for _, b := range []string{"b1", "b2", "b3"} {
		if it := planItem(m, b); it != nil && it.NotCompared > 0 {
			marked++
		}
	}
	if marked != 3 {
		t.Fatalf("every branch has an uncompared pair, but only %d are marked: %+v", marked, m.Plan)
	}
}

// Branches forked at different commits are deliberately never compared. That
// must not read as "0 colliding".
func TestCrossForkBranchesAreMarkedNotCompared(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "p", "main")
	f.commit("a.txt", "edit by p", "p")
	f.run("checkout", "-q", "main")
	f.commit("m.txt", "main moves", "main moves")
	f.run("push", "-q", "origin", "main")
	f.run("checkout", "-q", "-b", "q", "main")
	f.commit("a.txt", "edit by q", "q")
	f.run("checkout", "-q", "main")

	m := f.buildProbed(200)
	for _, b := range []string{"p", "q"} {
		it := planItem(m, b)
		if it == nil || it.NotCompared != 1 {
			t.Fatalf("%s should be marked as not compared with 1 branch, got %+v", b, it)
		}
	}
	if m.PairsSkipped != 0 {
		t.Fatalf("cross-fork pairs are not a truncation: PairsSkipped=%d", m.PairsSkipped)
	}
}
