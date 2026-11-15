package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// TestUnassessableBranchOffersNoCommand is M1 end to end: when git cherry
// fails for a branch, Decide must hand back a manual, command-less leaf - not
// the safe "git branch -d" it offered when the failure read as "nothing
// unlanded".
func TestUnassessableBranchOffersNoCommand(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/broken")
	f.commit("one.txt", "one", "one")
	parent := strings.TrimSpace(f.run("rev-parse", "HEAD"))
	f.commit("two.txt", "two", "two")
	f.run("switch", "-q", "main")
	obj := filepath.Join(f.dir, ".git", "objects", parent[:2], parent[2:])
	if err := os.Chmod(obj, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(obj); err != nil {
		t.Fatal(err)
	}

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/broken" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil || unit.Verdict != "unknown" {
		t.Fatalf("verdict = %+v, want unknown", unit)
	}
	tree, err := eng.Decide(ctx, unit.ID)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	walkActions(tree.Root, func(a *protocol.Action) {
		if a.Class != protocol.ClassManual || len(a.Argv) != 0 {
			t.Fatalf("an unassessed branch may only get manual, command-less actions, got %+v", a)
		}
	})
}
