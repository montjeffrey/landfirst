package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// findWorktreeID resolves a worktree's snapshot ID from its filesystem path -
// tests know the path (they just created it with `git worktree add`), but
// Remove needs the ID, so every Remove-focused test starts here. Mirrors the
// filepath.ToSlash(...) pattern internal/engine/decide_test.go already uses:
// git's own worktree list always reports "/", even on Windows.
func findWorktreeID(t *testing.T, snap *protocol.Snapshot, path string) string {
	t.Helper()
	want := filepath.ToSlash(path)
	for _, w := range snap.Worktrees {
		if w.Path == want {
			return w.ID
		}
	}
	t.Fatalf("no worktree with path %q among %+v", want, snap.Worktrees)
	return ""
}

// TestWorktreeAddCreatesAtDefaultPathAndSetsUpstreamWhenTracked covers the
// happy path spelled out in protocol.WorktreeAddRequest.Path's own doc: an
// empty Path resolves to a sibling ".worktrees" directory next to the repo
// root, never inside it. It also covers the fix the plan calls out by name -
// worktree add -b never sets an upstream on its own, so Track:true has to.
func TestWorktreeAddCreatesAtDefaultPathAndSetsUpstreamWhenTracked(t *testing.T) {
	f := newEfixture(t)

	// Publish feat/tracked on the remote, then drop the local branch - only
	// the remote-tracking ref should exist by the time Add creates it fresh.
	f.run("checkout", "-q", "-b", "feat/tracked")
	f.commit("tr.txt", "tracked", "will be tracked")
	f.run("push", "-q", "-u", "origin", "feat/tracked")
	f.run("checkout", "-q", "main")
	f.run("branch", "-D", "feat/tracked")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	before, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("initial Snapshot: %v", err)
	}

	result, err := eng.Worktrees().Add(ctx, protocol.WorktreeAddRequest{
		Branch:    "feat/tracked",
		NewBranch: true,
		Track:     true,
		Revision:  before.Revision,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !result.Ran || result.ExitCode != 0 {
		t.Fatalf("expected a successful run, got %+v (stderr=%q)", result, result.Stderr)
	}
	if result.Revision == "" {
		t.Fatal("expected a non-empty post-add Revision")
	}

	wantPath := filepath.Join(filepath.Dir(f.dir), "repo.worktrees", "feat/tracked")
	if _, statErr := os.Stat(wantPath); statErr != nil {
		t.Fatalf("expected a worktree at the default path %q: %v", wantPath, statErr)
	}
	rootSlash := filepath.ToSlash(f.dir) + "/"
	if strings.HasPrefix(filepath.ToSlash(wantPath), rootSlash) {
		t.Fatalf("default path %q must be outside the repo root %q", wantPath, f.dir)
	}

	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/tracked" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/tracked not found in units after Add")
	}
	if unit.Upstream != "origin/feat/tracked" {
		t.Fatalf("Upstream = %q, want origin/feat/tracked - Track:true should have set it", unit.Upstream)
	}
}

// TestWorktreeAddForExistingBranchChecksOutThatBranch is a regression test:
// an earlier version of Add's argv builder only ever inserted req.Branch via
// "-b" (the NewBranch case) - when NewBranch was false, the branch name
// never reached argv at all, and the final positional argument was always
// StartAt-or-BaseRef, so a worktree "for" an existing branch silently
// checked out the base branch instead. Caught in review before this shipped.
func TestWorktreeAddForExistingBranchChecksOutThatBranch(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/existing")
	f.commit("e.txt", "existing", "commit only on feat/existing")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	before, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("initial Snapshot: %v", err)
	}

	result, err := eng.Worktrees().Add(ctx, protocol.WorktreeAddRequest{
		Branch: "feat/existing", NewBranch: false, Revision: before.Revision,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !result.Ran || result.ExitCode != 0 {
		t.Fatalf("expected a successful run, got %+v (stderr=%q)", result, result.Stderr)
	}

	wantPath := filepath.Join(filepath.Dir(f.dir), "repo.worktrees", "feat/existing")
	branchAtPath := strings.TrimSpace(f.git(wantPath, "symbolic-ref", "--short", "HEAD"))
	if branchAtPath != "feat/existing" {
		t.Fatalf("worktree at %q is on branch %q, want feat/existing (it must not have checked out main/base instead)", wantPath, branchAtPath)
	}
}

// TestWorktreeAddRefusesUnsafePaths covers the plan's own security note
// verbatim: refuse a path under the repo root, or under a cloud-sync
// directory. The cloud case mirrors internal/model/landing_test.go's own
// TestWorktreeLocationRisksAreDetected fixture shape ("OneDrive - corp"),
// which already solved "how do I fake a cloud path without real cloud
// storage" for this exact detector.
func TestWorktreeAddRefusesUnsafePaths(t *testing.T) {
	f := newEfixture(t)
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	cases := []struct {
		name, path string
	}{
		{"nested", filepath.Join(f.dir, "nested-wt")},
		{"cloud", filepath.Join(filepath.Dir(f.dir), "OneDrive - corp", "wt")},
	}
	for _, c := range cases {
		_, err := eng.Worktrees().Add(ctx, protocol.WorktreeAddRequest{
			Branch: "feat/" + c.name, NewBranch: true, Path: c.path,
		})
		if !errors.Is(err, ErrUnsafeWorktreePath) {
			t.Fatalf("%s: Add into %q: err = %v, want ErrUnsafeWorktreePath", c.name, c.path, err)
		}
		if _, statErr := os.Stat(c.path); statErr == nil {
			t.Fatalf("%s: a refused Add must not have created %q", c.name, c.path)
		}
	}
}

// TestWorktreeAddRefusesMalformedBranchName proves the branch name is
// validated with `git check-ref-format --branch` before anything else runs -
// the argv-injection guard this API substitutes for Execute's confirmation
// hash, since a typed request has no per-argv hash to check.
func TestWorktreeAddRefusesMalformedBranchName(t *testing.T) {
	f := newEfixture(t)
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	before := f.run("worktree", "list", "--porcelain")

	for _, branch := range []string{"has space", "-leading-dash"} {
		_, err := eng.Worktrees().Add(ctx, protocol.WorktreeAddRequest{Branch: branch, NewBranch: true})
		if !errors.Is(err, ErrInvalidBranchName) {
			t.Fatalf("Add with branch %q: err = %v, want ErrInvalidBranchName", branch, err)
		}
		wantPath := filepath.Join(filepath.Dir(f.dir), "repo.worktrees", branch)
		if _, statErr := os.Stat(wantPath); statErr == nil {
			t.Fatalf("branch %q: worktree add must never have run, but %q exists", branch, wantPath)
		}
	}

	after := f.run("worktree", "list", "--porcelain")
	if before != after {
		t.Fatalf("a refused Add must never call worktree add - list changed:\nbefore=%q\nafter=%q", before, after)
	}
}

// TestWorktreeRemoveCleanWorktreeRemovesIt is the guarded case from the
// plan's Phase B spec: PathExists && Dirty=={} && Op=="none" actually runs
// `worktree remove`.
func TestWorktreeRemoveCleanWorktreeRemovesIt(t *testing.T) {
	f := newEfixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-clean")
	f.run("worktree", "add", "-q", "-b", "feat/clean", wt)

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
	if !result.Ran || result.ExitCode != 0 {
		t.Fatalf("expected a successful removal, got %+v (stderr=%q)", result, result.Stderr)
	}
	wantArgv := []string{"worktree", "remove", filepath.ToSlash(wt)}
	if !equalArgv(result.Argv, wantArgv) {
		t.Fatalf("Argv = %v, want %v", result.Argv, wantArgv)
	}

	snap2, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}
	for _, w := range snap2.Worktrees {
		if w.ID == id {
			t.Fatalf("worktree %q should be gone after Remove, still found: %+v", wt, w)
		}
	}
}

// TestWorktreeRemoveDirtyWorktreeRefusesAndShowsForceArgv is the "otherwise"
// case: dirty (or mid-operation) never runs anything, and shows the --force
// command as the "printed, never executed" leaf, same contract ClassManual
// already carries in protocol.Action.
func TestWorktreeRemoveDirtyWorktreeRefusesAndShowsForceArgv(t *testing.T) {
	f := newEfixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-dirty")
	f.run("worktree", "add", "-q", "-b", "feat/dirty", wt)
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("x\n"), 0o644); err != nil {
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
	if result.Ran {
		t.Fatalf("a dirty worktree must not be removed, got %+v", result)
	}
	wantArgv := []string{"worktree", "remove", "--force", filepath.ToSlash(wt)}
	if !equalArgv(result.Argv, wantArgv) {
		t.Fatalf("Argv = %v, want %v", result.Argv, wantArgv)
	}
	if result.ExitCode != 0 || result.Stdout != "" || result.Stderr != "" {
		t.Fatalf("a printed-not-executed result must carry no process output, got %+v", result)
	}

	snap2, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}
	found := false
	for _, w := range snap2.Worktrees {
		if w.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("dirty worktree should still exist after a refused Remove")
	}
}

// TestWorktreeRemoveGoneDirectoryPrunes is the third Phase B case: the
// directory vanished (deleted by hand, same setup internal/model's own
// TestWorktreePathGoneSkipsOpAndDirty uses), so there is nothing left to
// discard - prune git's bookkeeping instead of erroring.
func TestWorktreeRemoveGoneDirectoryPrunes(t *testing.T) {
	f := newEfixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-gone")
	f.run("worktree", "add", "-q", "-b", "feat/gone", wt)

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

	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	snap2, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("Snapshot after deleting the directory by hand: %v", err)
	}

	result, err := eng.Worktrees().Remove(ctx, protocol.WorktreeRemoveRequest{WorktreeID: id, Revision: snap2.Revision})
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !result.Ran || result.ExitCode != 0 {
		t.Fatalf("expected prune to run and succeed, got %+v (stderr=%q)", result, result.Stderr)
	}
	if !equalArgv(result.Argv, []string{"worktree", "prune"}) {
		t.Fatalf("Argv = %v, want [worktree prune]", result.Argv)
	}

	snap3, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("final Snapshot: %v", err)
	}
	for _, w := range snap3.Worktrees {
		if w.ID == id {
			t.Fatalf("worktree %q should be pruned away, still found: %+v", id, w)
		}
	}
}

// TestWorktreePruneSucceedsOnCleanRepo is Prune's own contract: always runs,
// always safe, even with nothing to do.
func TestWorktreePruneSucceedsOnCleanRepo(t *testing.T) {
	f := newEfixture(t)
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	result, err := eng.Worktrees().Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if !result.Ran || result.ExitCode != 0 {
		t.Fatalf("expected Prune to run and succeed with nothing to prune, got %+v (stderr=%q)", result, result.Stderr)
	}
	if !equalArgv(result.Argv, []string{"worktree", "prune"}) {
		t.Fatalf("Argv = %v, want [worktree prune]", result.Argv)
	}
	if result.Revision == "" {
		t.Fatal("expected a non-empty Revision after Prune")
	}
}
