package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// status.showUntrackedFiles=no (a common personal setting) makes plain
// "git status" report no untracked or ignored files, and git's own check before
// "worktree remove" is blind in the same way. landfirst must ask for them
// explicitly, or W deletes notes.txt and .env without a word.
func TestWorktreeRemoveRefusesUntrackedAndIgnoredFilesEvenWhenGitConfigHidesThem(t *testing.T) {
	f := newEfixture(t)
	f.commit(".gitignore", ".env", "ignore .env")
	f.run("config", "status.showUntrackedFiles", "no")

	for _, c := range []struct{ name, file string }{
		{"untracked", "notes.txt"},
		{"ignored", ".env"},
	} {
		t.Run(c.name, func(t *testing.T) {
			wt := filepath.Join(filepath.Dir(f.dir), "wt-"+c.name)
			f.run("worktree", "add", "-q", "-b", "feat/"+c.name, wt)
			path := filepath.Join(wt, c.file)
			if err := os.WriteFile(path, []byte("only copy\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			eng, err := Open(f.dir)
			if err != nil {
				t.Fatal(err)
			}
			defer eng.Close()
			ctx := context.Background()
			snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
			if err != nil {
				t.Fatal(err)
			}
			id := findWorktreeID(t, snap, wt)
			if c.name == "untracked" {
				if w := findWorktree(snap, id); w.Dirty.Untracked == 0 {
					t.Fatalf("the worktree holds an untracked file but is reported clean: %+v", w.Dirty)
				}
			}
			res, err := eng.Worktrees().Remove(ctx, protocol.WorktreeRemoveRequest{WorktreeID: id, Revision: snap.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if _, statErr := os.Stat(path); statErr != nil {
				t.Fatalf("%s was deleted by Remove: %v (%+v)", c.file, statErr, res)
			}
			if res.Ran {
				t.Fatalf("Remove ran on a worktree holding %s: %+v", c.file, res)
			}
		})
	}
}
