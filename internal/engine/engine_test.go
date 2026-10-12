package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// efixture mirrors internal/model's own fixture helper - real git, real
// fixtures. A different package needs its own copy rather than importing
// internal/model's test-only type.
type efixture struct {
	t   *testing.T
	dir string
}

// realTempDir is t.TempDir with symlinks and short names resolved. git
// reports canonical paths (macOS's /private/var, Windows' long names), so a
// fixture built under an aliased temp dir would never compare equal to them.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return dir
}

func newEfixture(t *testing.T) *efixture {
	t.Helper()
	dir := realTempDir(t)
	f := &efixture{t: t, dir: filepath.Join(dir, "repo")}
	remote := filepath.Join(dir, "remote.git")
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.run("init", "-q", "-b", "main")
	f.run("config", "user.email", "t@example.invalid")
	f.run("config", "user.name", "Test")
	f.git(dir, "init", "-q", "--bare", remote)
	f.run("remote", "add", "origin", remote)
	f.commit("a.txt", "base", "init")
	f.run("push", "-q", "-u", "origin", "main")
	return f
}

func (f *efixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".noglobal"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".nosystem"),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *efixture) run(args ...string) string { return f.git(f.dir, args...) }

func (f *efixture) commit(name, body, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.run("add", "-A")
	f.run("commit", "-q", "--no-gpg-sign", "-m", msg)
}

func TestSnapshotMapsUnitsWorktreesAndForks(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/x")
	f.commit("x.txt", "work", "unpushed work")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	snap, err := eng.Snapshot(context.Background(), SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if snap.Revision == "" {
		t.Fatal("Revision must not be empty")
	}
	if snap.Repo.Base != "main" || snap.Repo.Remote != "origin" {
		t.Fatalf("Repo = %+v, want Base=main Remote=origin", snap.Repo)
	}

	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/x" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatalf("feat/x not found in %d units", len(snap.Units))
	}
	if unit.ID != "branch:feat/x" {
		t.Fatalf("unit.ID = %q, want %q", unit.ID, "branch:feat/x")
	}
	if unit.Verdict != "never-pushed" {
		t.Fatalf("unit.Verdict = %q, want never-pushed", unit.Verdict)
	}
	if len(unit.Worktrees) != 0 {
		t.Fatalf("feat/x has no worktree checked out, got Worktrees=%v", unit.Worktrees)
	}

	// A build with no code change must produce the same revision - the
	// contract every caller of Revision depends on.
	snap2, err := eng.Snapshot(context.Background(), SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("second Snapshot: %v", err)
	}
	if snap2.Revision != snap.Revision {
		t.Fatalf("Revision changed across two builds of the same state: %q vs %q", snap.Revision, snap2.Revision)
	}

	// Changing real state must change the revision.
	f.run("checkout", "-q", "-b", "feat/y")
	f.commit("y.txt", "more", "another branch")
	f.run("checkout", "-q", "main")
	snap3, err := eng.Snapshot(context.Background(), SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("third Snapshot: %v", err)
	}
	if snap3.Revision == snap.Revision {
		t.Fatal("Revision did not change after adding a new branch")
	}
}

func TestSnapshotMapsWorktreeIDsConsistently(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/wt")
	f.commit("w.txt", "work", "work on a worktree")
	f.run("checkout", "-q", "main")

	wtDir := filepath.Join(realTempDir(t), "wt")
	f.run("worktree", "add", "-q", wtDir, "feat/wt")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	snap, err := eng.Snapshot(context.Background(), SnapshotOptions{Stage: protocol.StageFast})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/wt" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/wt not found in units")
	}
	if len(unit.Worktrees) != 1 {
		t.Fatalf("feat/wt should have exactly one worktree ID, got %v", unit.Worktrees)
	}

	var wt *protocol.Worktree
	for i := range snap.Worktrees {
		if snap.Worktrees[i].ID == unit.Worktrees[0] {
			wt = &snap.Worktrees[i]
		}
	}
	if wt == nil {
		t.Fatalf("unit.Worktrees[0] = %q does not match any Worktree.ID in %d worktrees", unit.Worktrees[0], len(snap.Worktrees))
	}
	if wt.Branch != "feat/wt" {
		t.Fatalf("cross-referenced worktree has Branch = %q, want feat/wt", wt.Branch)
	}
}

func TestGraphIncludesEdgesInJSON(t *testing.T) {
	f := newEfixture(t)
	f.commit("b.txt", "second", "second commit")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	g, err := eng.Graph(context.Background(), protocol.GraphOptions{})
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(g.Commits) < 2 {
		t.Fatalf("expected at least 2 commits, got %d", len(g.Commits))
	}
	// The whole point of protocol.Graph vs model.Graph: Edges must actually
	// serialize, not be tagged json:"-".
	if len(g.Edges) == 0 && len(g.Commits) > 1 {
		t.Fatal("expected at least one edge linking the two commits")
	}
}

// TestConcurrentSnapshotsRaceFree is the plainest shape of the bug this
// covers: nothing but engine.mu stops two goroutines' Snapshot calls from
// writing e.last/e.snap at the same time. bubbletea runs every tea.Cmd in
// its own goroutine (internal/tui/model.go's fetchSnapshot), so a double
// keypress on refresh does exactly this against a real *engine. Run under
// -race, not just for its own pass/fail: see
// TestConcurrentEngineCallsWouldRaceWithoutTheMutex below for the proof that
// this (and the mixed test after it) actually trips the detector without
// engine.mu - this test alone is a write/write race on e.last/e.snap, the
// mixed test below adds the read/write shape Decide/Execute contribute.
func TestConcurrentSnapshotsRaceFree(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/concurrent")
	f.commit("c.txt", "work", "concurrent snapshot fixture work")
	f.run("push", "-q", "-u", "origin", "feat/concurrent")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stage := protocol.StageFast
			if i%2 == 0 {
				stage = protocol.StageFull
			}
			_, err := eng.Snapshot(ctx, SnapshotOptions{Stage: stage})
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: Snapshot returned an error: %v", i, err)
		}
	}
}

// TestConcurrentMixedEngineCallsRaceFree fires a realistic mix at the same
// *engine at once, not just N of the same call: plain re-snapshots, Decide
// calls against a unit nothing here ever mutates, and Decide-then-Execute
// against a dedicated branch. It doesn't probe business-logic correctness
// (decide_test.go/worktree_test.go already cover that in depth) - the point
// is putting Snapshot/Decide/Execute in flight on one engine at the same
// time, the shape -race needs to see a race on e.snap/e.last.
//
// ErrStaleRevision from the concurrent Execute calls is expected and
// tolerated, not a bug: Revision is a whole-snapshot fingerprint, so two
// goroutines racing Decide-then-Execute against a shared *engine can
// legitimately have one lose to the other's mutation and see its own
// revision go stale between the two calls - that's the staleness guard
// (execute.go) doing its job. Every other outcome (any other error, or a
// panic - which nothing here recovers from, so it fails the whole test
// binary) is treated as a real failure.
func TestConcurrentMixedEngineCallsRaceFree(t *testing.T) {
	f := newEfixture(t)
	// feat/stable: decided repeatedly below but never executed against, so
	// it is present, unchanged, in every snapshot for the test's duration -
	// nothing about deciding it should ever legitimately fail.
	f.run("checkout", "-q", "-b", "feat/stable")
	f.commit("s.txt", "work", "never mutated by this test")
	f.run("checkout", "-q", "main")
	// feat/push-me: the target of the concurrent Decide-then-Execute calls.
	f.run("checkout", "-q", "-b", "feat/push-me")
	f.commit("p.txt", "work", "push target")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()

	var wg sync.WaitGroup
	fail := make(chan string, 64)

	// Plain re-snapshots - the read-heavy majority of real traffic.
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stage := protocol.StageFast
			if i%2 == 0 {
				stage = protocol.StageFull
			}
			if _, err := eng.Snapshot(ctx, SnapshotOptions{Stage: stage}); err != nil {
				fail <- fmt.Sprintf("Snapshot: %v", err)
			}
		}(i)
	}

	// Decide against a unit nothing in this test ever mutates - must always
	// succeed regardless of how it interleaves with everything else.
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := eng.Decide(ctx, "branch:feat/stable"); err != nil {
				fail <- fmt.Sprintf("Decide(feat/stable): %v", err)
			}
		}()
	}

	// Decide-then-Execute a real, safe leaf concurrently against a shared
	// target - see the staleness note in the doc comment above for why
	// ErrStaleRevision alone is tolerated here.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tree, err := eng.Decide(ctx, "branch:feat/push-me")
			if err != nil {
				fail <- fmt.Sprintf("Decide(feat/push-me): %v", err)
				return
			}
			a := tree.Root.Action
			if a == nil || a.Kind != "push" {
				fail <- fmt.Sprintf("feat/push-me: expected a single push leaf, got %+v", tree.Root)
				return
			}
			sum := sha256.Sum256([]byte(strings.Join(a.Argv, "\x00")))
			_, err = eng.Execute(ctx, protocol.ExecuteRequest{
				ActionID:     a.ID,
				Revision:     tree.Revision,
				Confirmation: hex.EncodeToString(sum[:]),
			})
			if err != nil && !errors.Is(err, ErrStaleRevision) {
				fail <- fmt.Sprintf("Execute(feat/push-me): %v", err)
			}
		}()
	}

	wg.Wait()
	close(fail)
	for msg := range fail {
		t.Error(msg)
	}
}
