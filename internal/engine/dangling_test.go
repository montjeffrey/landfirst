package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

func TestSnapshotRefusesToPublishWhenTheBranchListCannotBeRead(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/real")
	f.commit("x.txt", "x", "real work")
	p := filepath.Join(f.dir, ".git", "refs", "heads", "ghost")
	if err := os.WriteFile(p, []byte(strings.Repeat("ab", 20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	snap, err := eng.Snapshot(context.Background(), SnapshotOptions{Stage: protocol.StageFast})
	if err == nil {
		t.Fatalf("Snapshot must fail, not publish %d units and an all-clear", len(snap.Units))
	}
}
