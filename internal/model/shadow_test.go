package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file in the working tree whose path equals a refname ("refs/heads/B") makes
// `git diff <rev> refs/heads/B` fail as ambiguous. The failure was swallowed,
// B looked like it changed nothing, and it rose to the top of MERGE ORDER.
func TestWorktreeFileNamedLikeARefDoesNotHideABranchsChanges(t *testing.T) {
	f := newFixture(t)
	for _, b := range []string{"B", "C"} {
		f.run("checkout", "-q", "-b", b, "main")
		f.commit("a.txt", "edit by "+b, b)
	}
	f.run("checkout", "-q", "main")
	shadow := filepath.Join(f.dir, "refs", "heads", "B")
	if err := os.MkdirAll(filepath.Dir(shadow), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shadow, []byte("not a ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := f.buildProbed(200)
	if len(m.Contentions) != 1 {
		t.Fatalf("B and C both edit a.txt; got contentions %+v", m.Contentions)
	}
	if it := planItem(m, "B"); it == nil || it.Conflicts != 1 || it.Unreadable {
		t.Fatalf("B must show its collision with C, got %+v", it)
	}
}

// When git cannot say what a branch changed, that is "not compared", never
// "collides with nothing".
func TestUnreadableBranchChangesAreNotRenderedAsNoConflicts(t *testing.T) {
	f := newFixture(t)
	for _, b := range []string{"good", "bad"} {
		f.run("checkout", "-q", "-b", b, "main")
		f.commit("a.txt", "edit by "+b, b)
	}
	f.run("checkout", "-q", "main")
	tree := strings.TrimSpace(f.run("rev-parse", "bad^{tree}"))
	obj := filepath.Join(f.dir, ".git", "objects", tree[:2], tree[2:])
	if err := os.Chmod(obj, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(obj); err != nil {
		t.Fatal(err)
	}

	m := f.buildProbed(200)
	bad, good := planItem(m, "bad"), planItem(m, "good")
	if bad == nil || good == nil {
		t.Fatalf("plan lacks a branch: %+v", m.Plan)
	}
	if !bad.Unreadable || bad.NotCompared == 0 {
		t.Fatalf("bad could not be read and must say so, got %+v", bad)
	}
	if good.NotCompared == 0 {
		t.Fatalf("good was not compared with bad and must say so, got %+v", good)
	}
	if m.Plan[len(m.Plan)-1].Branch != "bad" {
		t.Fatalf("an unreadable branch must not lead the merge order: %+v", m.Plan)
	}
	for _, w := range bad.Why {
		if strings.Contains(w, "collides with nothing") {
			t.Fatalf("unreadable branch described as collision-free: %v", bad.Why)
		}
	}
}
