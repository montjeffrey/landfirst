package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMidRebaseWorktreeReportsInProgress is the end-to-end check for the new
// InProgress verdict: a real second worktree, a real rebase that genuinely
// conflicts, left unresolved on purpose.
//
// A rebase detaches HEAD (it replays commits with HEAD off any branch, and
// the branch ref itself doesn't move until the rebase finishes), so this
// exercises buildUnits' detached-worktree loop and its Op != OpNone
// extension - not judge()'s row 1, which only ever sees a worktree still on
// its branch (true for a stuck merge/cherry-pick/revert, never for a rebase).
func TestMidRebaseWorktreeReportsInProgress(t *testing.T) {
	f := newFixture(t)

	// Diverge the same line on two branches so replaying one onto the other
	// is guaranteed to conflict, not just apply cleanly.
	f.run("checkout", "-q", "-b", "feat/rebase-conflict")
	f.commit("a.txt", "branch-line", "branch change")
	f.run("checkout", "-q", "main")
	f.commit("a.txt", "main-line", "main change")

	wtDir := filepath.Join(t.TempDir(), "wt")
	f.run("worktree", "add", "-q", wtDir, "feat/rebase-conflict")

	// Expected to fail with a conflict - do not finish or abort it, the
	// point is to leave rebase-merge/rebase-apply behind unresolved.
	if out, err := runGitAllowFailure(t, wtDir, "rebase", "main"); err == nil {
		t.Fatalf("expected the rebase to stop on a conflict, it completed cleanly:\n%s", out)
	}

	m := f.build()

	var found *Unit
	for i, u := range m.Units {
		for _, w := range u.Worktrees {
			if w.Op != OpNone {
				found = &m.Units[i]
			}
		}
	}
	if found == nil {
		t.Fatalf("no unit found with an in-progress worktree op among %d units", len(m.Units))
	}
	if found.Worktrees[0].Op != OpRebase {
		t.Fatalf("expected the worktree's Op to be %q, got %q", OpRebase, found.Worktrees[0].Op)
	}
	if found.Verdict != InProgress {
		t.Fatalf("mid-rebase worktree should be %q, got %q (detail: %s)", InProgress, found.Verdict, found.Detail)
	}
}

// TestInProgressBranchExcludedFromPlanNotForks guards two bugs from two
// separate reviews, in opposite directions. First: InProgress was added as a
// new verdict but buildPlan's candidate filter (written before it existed)
// was never updated to exclude it, so a branch stuck mid-operation could be
// recommended in the merge order. Second, found later: an initial fix to the
// first bug also excluded InProgress from buildForks, which feeds the
// topology view's row list, not just the merge plan - that made the tool's
// single most urgent state invisible in the UI entirely, with the header
// banner actively claiming "nothing in flight." InProgress must be excluded
// from Plan but must still appear in Forks.
//
// Uses a stuck merge, not a rebase: a merge conflict leaves HEAD on the
// branch (unlike rebase, which detaches it - see TestMidRebaseWorktree...
// above), so this exercises judge()'s row 1 for a named branch, the path the
// rebase test above cannot reach.
func TestInProgressBranchExcludedFromPlanNotForks(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/a")
	f.commit("shared.txt", "line-from-a", "a's change")
	f.run("checkout", "-q", "-b", "feat/b", "main")
	f.commit("shared.txt", "line-from-b", "b's change") // same line as feat/a - guaranteed conflict
	f.run("checkout", "-q", "feat/a")

	if out, err := runGitAllowFailure(t, f.dir, "merge", "--no-edit", "feat/b"); err == nil {
		t.Fatalf("expected the merge to stop on a conflict, it completed cleanly:\n%s", out)
	}

	m := buildFork(t, f) // forks_test.go: git.Open + Build(Options{Probe: true, MaxPairs: 50})

	var unit *Unit
	for i, u := range m.Units {
		if u.Branch == "feat/a" {
			unit = &m.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/a not found in units")
	}
	if unit.Verdict != InProgress {
		t.Fatalf("feat/a verdict = %q, want %q (merge conflict left unresolved, HEAD stays on the branch)", unit.Verdict, InProgress)
	}
	for _, p := range m.Plan {
		if p.Branch == "feat/a" {
			t.Fatalf("feat/a is InProgress but still appears in the merge plan: %+v", p)
		}
	}
	found := false
	for _, fp := range m.Forks {
		for _, b := range fp.Branches {
			if b == "feat/a" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("feat/a is InProgress but missing from Forks - it must still be visible in the topology view, just not recommended in the merge plan")
	}
}

// runGitAllowFailure runs git in dir and returns its output and error instead
// of failing the test - for the one place a non-zero exit (a rebase stopping
// on conflict) is the correct, expected outcome. Mirrors fixture.git's own
// config isolation.
func runGitAllowFailure(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".noglobal"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".nosystem"),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	return string(out), err
}
