package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// TestWorktreeRemoveDoesNotSilentlyDeleteIgnoredFiles is L3: "git worktree
// remove" without --force deletes ignored files, and a .env in an otherwise
// clean worktree is exactly the kind of file nobody has a copy of. Remove must
// refuse, name the file, and leave the choice to a command run by hand.
func TestWorktreeRemoveDoesNotSilentlyDeleteIgnoredFiles(t *testing.T) {
	f := newEfixture(t)
	f.commit(".gitignore", ".env", "ignore .env")
	wt := filepath.Join(filepath.Dir(f.dir), "wt-ignored")
	f.run("worktree", "add", "-q", "-b", "feat/ignored", wt)
	secret := filepath.Join(wt, ".env")
	if err := os.WriteFile(secret, []byte("TOKEN=only-copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	id := findWorktreeID(t, snap, wt)

	result, err := eng.Worktrees().Remove(ctx, protocol.WorktreeRemoveRequest{WorktreeID: id, Revision: snap.Revision})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, statErr := os.Stat(secret); statErr != nil {
		t.Fatalf("the ignored .env was deleted by Remove (%v); result=%+v", statErr, result)
	}
	if result.Ran {
		t.Fatalf("a worktree holding ignored files must not be removed, got %+v", result)
	}
	if want := []string{"worktree", "remove", "--force", filepath.ToSlash(wt)}; !equalArgv(result.Argv, want) {
		t.Fatalf("Argv = %v, want the printed --force command %v", result.Argv, want)
	}
	if !strings.Contains(result.Note, ".env") {
		t.Fatalf("the refusal must name the ignored file, got note %q", result.Note)
	}
}
