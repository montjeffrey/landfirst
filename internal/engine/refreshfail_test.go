package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// A push that happened must be reported as having happened even when the
// snapshot taken afterwards fails.
func TestExecuteReportsTheActionWhenTheRefreshAfterwardsFails(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/pushme")
	f.commit("one.txt", "one", "first")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	ctx := context.Background()
	tree, err := eng.Decide(ctx, "branch:feat/pushme")
	if err != nil {
		t.Fatal(err)
	}
	a := singleLeaf(t, tree)

	// After the user confirmed, a ref that points nowhere appears: the push still
	// works, the re-snapshot cannot read the branch list.
	ghost := filepath.Join(f.dir, ".git", "refs", "heads", "ghost")
	if err := os.WriteFile(ghost, []byte(strings.Repeat("ab", 20)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(strings.Join(a.Argv, "\x00")))
	res, err := eng.Execute(ctx, protocol.ExecuteRequest{ActionID: a.ID, Revision: tree.Revision, Confirmation: hex.EncodeToString(sum[:])})
	if err != nil {
		t.Fatalf("the push ran; Execute must not return an error for the refresh: %v", err)
	}
	if !res.Ran || res.ExitCode != 0 {
		t.Fatalf("result = %+v, want the push reported as run and successful", res)
	}
	if res.RefreshError == "" {
		t.Fatal("RefreshError must say the refresh failed")
	}
	if out := f.git(f.dir, "ls-remote", "origin", "refs/heads/feat/pushme"); strings.TrimSpace(out) == "" {
		t.Fatal("the push should have happened")
	}
}
