package model

import (
	"strings"
	"testing"
)

// A merge-plan line saying "N behind main" must count commits the branch lacks
// from main. It used to carry the branch's behind-its-upstream count, which is
// 0 for a branch with no upstream however stale it is.
func TestPlanBehindIsDistanceToTheBaseNotTheUpstream(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/stale")
	f.commit("s.txt", "work", "stale work")
	f.run("checkout", "-q", "main")
	for _, n := range []string{"m1.txt", "m2.txt", "m3.txt"} {
		f.commit(n, n, "main moves "+n)
	}
	f.run("push", "-q", "origin", "main")

	m := f.build()
	var item *PlanItem
	for i := range m.Plan {
		if m.Plan[i].Branch == "feat/stale" {
			item = &m.Plan[i]
		}
	}
	if item == nil {
		t.Fatalf("feat/stale missing from plan: %+v", m.Plan)
	}
	if item.BehindN != 3 {
		t.Fatalf("plan Behind = %d, want 3 (commits on main it lacks)", item.BehindN)
	}
	if !strings.Contains(strings.Join(item.Why, "; "), "3 behind main") {
		t.Fatalf("plan reason does not say 3 behind main: %v", item.Why)
	}
}
