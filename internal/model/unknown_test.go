package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// breakBranch leaves feat/broken with unmerged work whose parent commit object
// has been removed from the object store, so "git cherry main feat/broken"
// exits non-zero while for-each-ref still lists the branch.
func (f *fixture) breakBranch() {
	f.t.Helper()
	f.run("checkout", "-q", "-b", "feat/broken")
	f.commit("one.txt", "one", "one")
	parent := strings.TrimSpace(f.run("rev-parse", "HEAD"))
	f.commit("two.txt", "two", "two")
	f.run("checkout", "-q", "main")

	obj := filepath.Join(f.dir, ".git", "objects", parent[:2], parent[2:])
	if err := os.Chmod(obj, 0o644); err != nil {
		f.t.Fatalf("loose object %s: %v", obj, err)
	}
	if err := os.Remove(obj); err != nil {
		f.t.Fatal(err)
	}
}

// TestFailedCherryIsUnknownNotSafeToDelete is M1: a failed "git cherry" was
// read as "no unlanded changes", so a branch landfirst could not assess got
// the verdict "safe to delete" and a Safe remedy that deletes it.
func TestFailedCherryIsUnknownNotSafeToDelete(t *testing.T) {
	f := newFixture(t)
	f.breakBranch()

	m := f.build()
	if got := verdictOf(m, "feat/broken"); got != Unknown {
		t.Fatalf("a branch git cherry could not assess must be %q, got %q", Unknown, got)
	}
	for _, u := range m.Units {
		if u.Branch != "feat/broken" {
			continue
		}
		if u.Remedy != nil {
			t.Fatalf("no remedy may be offered for an unassessed branch, got %+v", u.Remedy)
		}
		if !strings.Contains(u.Detail, "could not") {
			t.Fatalf("detail should say the comparison failed, got %q", u.Detail)
		}
		if u.Verdict.Severity() != SeverityBad {
			t.Fatalf("an unassessed branch is not routine cleanup, severity = %v", u.Verdict.Severity())
		}
	}
}
