package model

import "testing"

// The base branch is by definition fully contained in the base. With no
// upstream it used to come out as "safe to delete", under a heading inviting
// the user to delete the branch everything else merges into.
func TestBaseBranchIsNeverSafeToDelete(t *testing.T) {
	f := newFixture(t)
	f.run("branch", "--unset-upstream", "main")
	f.run("checkout", "-q", "-b", "feat/x")

	m := f.build()
	for _, u := range m.Units {
		if u.Branch != "main" {
			continue
		}
		if u.Verdict == SafeToDelete {
			t.Fatalf("the base branch must never be %q", SafeToDelete)
		}
		if u.Remedy != nil {
			t.Fatalf("no remedy may be offered for the base branch, got %+v", u.Remedy)
		}
		return
	}
	t.Fatal("base branch unit missing")
}
