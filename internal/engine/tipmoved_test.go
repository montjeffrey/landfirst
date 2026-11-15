package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// The snapshot a push was offered from can be minutes old. A branch that moved
// since (an agent committed again) must not be pushed at whatever it points at
// now: the user confirmed a specific commit.
func TestPushRefusesWhenTheBranchMovedSinceTheSnapshot(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/moving")
	f.commit("one.txt", "one", "first")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	ctx := context.Background()
	tree, err := eng.Decide(ctx, "branch:feat/moving")
	if err != nil {
		t.Fatal(err)
	}
	a := singleLeaf(t, tree)

	// The branch moves after the snapshot, before the user answers y.
	f.run("checkout", "-q", "feat/moving")
	f.commit("two.txt", "two", "second")
	f.run("checkout", "-q", "main")

	sum := sha256.Sum256([]byte(strings.Join(a.Argv, "\x00")))
	_, err = eng.Execute(ctx, protocol.ExecuteRequest{ActionID: a.ID, Revision: tree.Revision, Confirmation: hex.EncodeToString(sum[:])})
	if !errors.Is(err, ErrBranchMoved) {
		t.Fatalf("Execute = %v, want ErrBranchMoved", err)
	}
	if out := f.git(f.dir, "ls-remote", "origin", "refs/heads/feat/moving"); strings.TrimSpace(out) != "" {
		t.Fatalf("nothing may be pushed when the branch moved, remote has: %s", out)
	}
}
