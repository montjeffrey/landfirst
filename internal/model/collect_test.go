package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
)

func (f *fixture) openRepo() *git.Repo {
	f.t.Helper()
	repo, err := git.Open(f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return repo
}

// tryGit is fixture.git without the fatal-on-error, for commands that are
// expected to fail on purpose - a merge or rebase set up to conflict.
func (f *fixture) tryGit(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".noglobal"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".nosystem"),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func findByBranch(t *testing.T, trees []Worktree, branch string) Worktree {
	t.Helper()
	for _, w := range trees {
		if w.Branch == branch {
			return w
		}
	}
	t.Fatalf("no worktree on branch %q among %+v", branch, trees)
	return Worktree{}
}

// conflictFixture returns a fixture with a linked worktree on branch
// "feat-<name>" and a colliding commit on main - both edit a.txt, so merging
// or rebasing one against the other is guaranteed to conflict.
func conflictFixture(t *testing.T, name string) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-"+name)
	f.run("worktree", "add", "-q", "-b", "feat-"+name, wt)

	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("wt side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(wt, "commit", "-q", "--no-gpg-sign", "-am", "wt edits a.txt")

	if err := os.WriteFile(filepath.Join(f.dir, "a.txt"), []byte("main side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run("commit", "-q", "--no-gpg-sign", "-am", "main edits a.txt")

	return f, wt
}

func TestCollectWorktreesFieldsSurviveDashZ(t *testing.T) {
	f := newFixture(t)
	wtA := filepath.Join(filepath.Dir(f.dir), "wt-a")
	f.run("worktree", "add", "-q", "-b", "feat-a", wtA)

	wtLocked := filepath.Join(filepath.Dir(f.dir), "wt-locked")
	f.run("worktree", "add", "-q", "-b", "feat-locked", wtLocked)
	f.run("worktree", "lock", wtLocked, "--reason", "testing")

	wtDetached := filepath.Join(filepath.Dir(f.dir), "wt-detached")
	f.run("worktree", "add", "-q", "--detach", wtDetached, "main")

	trees := collectWorktrees(f.openRepo())
	if len(trees) != 4 {
		t.Fatalf("expected 4 worktrees, got %d: %+v", len(trees), trees)
	}
	if !trees[0].IsMain || trees[0].Branch != "main" || trees[0].Detached {
		t.Fatalf("expected the first entry to be the main worktree on main, got %+v", trees[0])
	}

	a := findByBranch(t, trees, "feat-a")
	if a.IsMain || a.Detached || a.Locked || !a.PathExists {
		t.Fatalf("unexpected fields for the feat-a worktree: %+v", a)
	}

	locked := findByBranch(t, trees, "feat-locked")
	if !locked.Locked {
		t.Fatalf("expected the feat-locked worktree to report Locked, got %+v", locked)
	}

	var detachedCount int
	for _, w := range trees {
		if w.Detached {
			detachedCount++
			if w.Branch != "" || w.Head == "" {
				t.Fatalf("detached worktree should have no branch and a head, got %+v", w)
			}
		}
	}
	if detachedCount != 1 {
		t.Fatalf("expected exactly 1 detached worktree, found %d", detachedCount)
	}
}

func TestWorktreePathGoneSkipsOpAndDirty(t *testing.T) {
	f := newFixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-gone")
	f.run("worktree", "add", "-q", "-b", "feat-gone", wt)

	// Deleted by hand, not via `git worktree remove` - git's own admin files
	// for it are left dangling, same as an OS crash or a stray rm -rf would do.
	if err := os.RemoveAll(wt); err != nil {
		t.Fatal(err)
	}

	w := findByBranch(t, collectWorktrees(f.openRepo()), "feat-gone")
	if w.PathExists {
		t.Fatalf("expected PathExists=false for a deleted worktree directory, got %+v", w)
	}
	if w.Op != OpNone {
		t.Fatalf("expected Op=%q when the path doesn't exist, got %q", OpNone, w.Op)
	}
	if w.Dirty != (DirtyState{}) {
		t.Fatalf("expected a zero DirtyState when the path doesn't exist, got %+v", w.Dirty)
	}
}

func TestWorktreeWithSpaceInPathDashZAndStatusDashZ(t *testing.T) {
	f := newFixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt spaced name")
	f.run("worktree", "add", "-q", "-b", "feat-spaced", wt)

	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := findByBranch(t, collectWorktrees(f.openRepo()), "feat-spaced")
	if !w.PathExists {
		t.Fatalf("expected the spaced-path worktree to be found and exist, got %+v", w)
	}
	want := DirtyState{Unstaged: 1, Untracked: 1}
	if w.Dirty != want {
		t.Fatalf("dirty state for the spaced-path worktree = %+v, want %+v", w.Dirty, want)
	}
}

func TestDirtyStateCleanWorktreeIsAllZero(t *testing.T) {
	f := newFixture(t)
	if d := dirtyState(f.dir); d != (DirtyState{}) {
		t.Fatalf("expected a clean worktree to report a zero DirtyState, got %+v", d)
	}
}

func TestDirtyStateCountsEachKind(t *testing.T) {
	f := newFixture(t)

	if err := os.WriteFile(filepath.Join(f.dir, "staged.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run("add", "staged.txt")

	if err := os.WriteFile(filepath.Join(f.dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(f.dir, "untracked.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := DirtyState{Staged: 1, Unstaged: 1, Untracked: 1}
	if d := dirtyState(f.dir); d != want {
		t.Fatalf("dirtyState = %+v, want %+v", d, want)
	}
}

// TestDirtyStateRenameDoesNotDesyncFollowingRecords is the classic -z bug: a
// rename ('2') record is followed by an extra NUL-terminated origPath field.
// The origin name here deliberately starts with 'u' - if that field is not
// consumed, it leaks into the next iteration and gets misread as a bogus
// unmerged record, and the true count of what follows goes uninspected.
func TestDirtyStateRenameDoesNotDesyncFollowingRecords(t *testing.T) {
	f := newFixture(t)
	f.commit("uold.txt", "content", "add uold")
	f.run("mv", "uold.txt", "renamed.txt")

	// A genuine later change, so a desync would corrupt real data, not just
	// leave phantom counts.
	if err := os.WriteFile(filepath.Join(f.dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	want := DirtyState{Staged: 1, Unstaged: 1}
	if d := dirtyState(f.dir); d != want {
		t.Fatalf("dirtyState after a rename = %+v, want %+v (a wrong Conflicted or Untracked count means the origPath field desynced parsing)", d, want)
	}
}

func TestWorktreeOpIsNoneWhenClean(t *testing.T) {
	f := newFixture(t)
	w := findByBranch(t, collectWorktrees(f.openRepo()), "main")
	if w.Op != OpNone {
		t.Fatalf("expected Op=%q for a clean repo, got %q", OpNone, w.Op)
	}
}

func TestWorktreeOpDetectsMerge(t *testing.T) {
	f, wt := conflictFixture(t, "merge")
	f.tryGit(wt, "merge", "main") // conflicts on purpose

	trees := collectWorktrees(f.openRepo())
	var mergeCount int
	for _, w := range trees {
		switch w.Op {
		case OpMerge:
			mergeCount++
			if w.Dirty.Conflicted == 0 {
				t.Fatalf("worktree mid-merge should report a conflicted entry, got %+v", w.Dirty)
			}
		case OpNone:
		default:
			t.Fatalf("unexpected Op on a worktree not mid-merge: %+v", w)
		}
	}
	if mergeCount != 1 {
		t.Fatalf("expected exactly 1 worktree with Op=%q, found %d among %+v", OpMerge, mergeCount, trees)
	}
}

func TestWorktreeOpDetectsRebase(t *testing.T) {
	f, wt := conflictFixture(t, "rebase")
	f.tryGit(wt, "rebase", "main") // conflicts on purpose

	trees := collectWorktrees(f.openRepo())
	var rebaseCount int
	for _, w := range trees {
		switch w.Op {
		case OpRebase:
			rebaseCount++
		case OpNone:
		default:
			t.Fatalf("unexpected Op on a worktree not mid-rebase: %+v", w)
		}
	}
	if rebaseCount != 1 {
		t.Fatalf("expected exactly 1 worktree with Op=%q, found %d among %+v", OpRebase, rebaseCount, trees)
	}
}

func TestCollectFreshnessNeverFetchedIsZero(t *testing.T) {
	f := newFixture(t)
	if fr := collectFreshness(f.openRepo()); !fr.FetchHeadAt.IsZero() {
		t.Fatalf("expected a zero FetchHeadAt before any fetch, got %v", fr.FetchHeadAt)
	}
}

func TestCollectFreshnessAfterFetchIsRecent(t *testing.T) {
	f := newFixture(t)
	f.run("fetch", "-q", "origin")

	fr := collectFreshness(f.openRepo())
	if fr.FetchHeadAt.IsZero() {
		t.Fatal("expected a non-zero FetchHeadAt after a fetch")
	}
	if age := time.Since(fr.FetchHeadAt); age < 0 || age > time.Minute {
		t.Fatalf("expected FetchHeadAt to be just now, got %v ago", age)
	}
}
