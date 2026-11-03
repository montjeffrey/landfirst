//go:build windows

package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// TestWorktreeAddRefusesNestedPathThroughJunction: the end-to-end form of
// model's junction test. A junction to the repo is a different spelling of
// the repo; a worktree "beside" it is really inside it, and must be refused.
func TestWorktreeAddRefusesNestedPathThroughJunction(t *testing.T) {
	f := newEfixture(t)
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	alias := filepath.Join(filepath.Dir(f.dir), "repo-junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, f.dir).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
	_, err = eng.Worktrees().Add(context.Background(), protocol.WorktreeAddRequest{
		Branch: "feat/junction", NewBranch: true, Path: filepath.Join(alias, "nested-j"),
	})
	if !errors.Is(err, ErrUnsafeWorktreePath) {
		t.Fatalf("Add through a junction: err = %v, want ErrUnsafeWorktreePath", err)
	}
	if _, statErr := os.Stat(filepath.Join(f.dir, "nested-j")); statErr == nil {
		t.Fatal("a refused Add must not have created the nested worktree")
	}
}
