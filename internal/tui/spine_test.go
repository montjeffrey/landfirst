package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/engine"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

func TestWorktreeTextFormatsPathDirtyAndOp(t *testing.T) {
	snap := &protocol.Snapshot{
		Worktrees: []protocol.Worktree{
			{ID: "wt:1", Path: "C:/tmp/wt5", Dirty: protocol.DirtyState{Unstaged: 2}, Op: "rebase"},
			{ID: "wt:2", Path: ".claude/worktrees/kpi", Op: "none"},
		},
	}
	tests := []struct {
		name     string
		unit     protocol.Unit
		wantText string
		wantAttn bool
	}{
		{"no worktree", protocol.Unit{}, "(no worktree)", false},
		{"dirty and mid-rebase", protocol.Unit{Worktrees: []string{"wt:1"}},
			"⬖ C:/tmp/wt5 dirty:2 rebase-in-progress", true},
		{"clean and idle", protocol.Unit{Worktrees: []string{"wt:2"}},
			"⬖ .claude/worktrees/kpi clean", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, attn := worktreeText(&tt.unit, snap, 60)
			if got != tt.wantText {
				t.Fatalf("worktreeText = %q, want %q", got, tt.wantText)
			}
			if attn != tt.wantAttn {
				t.Fatalf("attention = %v, want %v", attn, tt.wantAttn)
			}
		})
	}
}

func TestWorktreeTextClipsLongPathsRatherThanOverflow(t *testing.T) {
	snap := &protocol.Snapshot{Worktrees: []protocol.Worktree{
		{ID: "wt:1", Path: "C:/very/long/path/that/should/get/clipped/aggressively/so/the/row/never/wraps"},
	}}
	u := protocol.Unit{Worktrees: []string{"wt:1"}}

	got, _ := worktreeText(&u, snap, 10)
	if !strings.Contains(got, "…") {
		t.Fatalf("expected the clipped path to carry an ellipsis, got %q", got)
	}
	// "⬖ " (2) + a 10-rune clipped path + " clean" (6).
	if want := 2 + 10 + 6; len([]rune(got)) != want {
		t.Fatalf("worktreeText length = %d, want %d for a %d-rune path budget: %q", len([]rune(got)), want, 10, got)
	}
}

func TestWorktreeTextUnknownWorktreeIDIsTreatedAsNoWorktree(t *testing.T) {
	// A defensive case: Unit.Worktrees names an ID that isn't in
	// Snapshot.Worktrees (should not happen from a real engine, but this
	// function must not panic or fabricate a path if it ever does).
	snap := &protocol.Snapshot{}
	u := protocol.Unit{Worktrees: []string{"wt:missing"}}
	got, attn := worktreeText(&u, snap, 20)
	if got != "(no worktree)" || attn {
		t.Fatalf("worktreeText = (%q, %v), want (\"(no worktree)\", false)", got, attn)
	}
}

// ---- detached/branchless units in the topology view ----
//
// buildForks (internal/model/forks.go) skips every unit with Branch == ""
// before it ever becomes part of a ForkPoint, so topologyRows() used to build
// every row exclusively from snap.Forks and a detached-HEAD unit - the single
// most easily lost kind of work - was unreachable by the cursor entirely.
// These tests cover the section that fixes that: present, selectable, and
// wired into the same Decide path a branch row uses.

func TestTopologyRowsIncludesDetachedSection(t *testing.T) {
	snap := &protocol.Snapshot{
		Units: []protocol.Unit{
			{ID: "branch:feat/x", Branch: "feat/x", Verdict: "unpushed"},
			{ID: "detached:abc1234", Head: "abc1234", Verdict: "detached", Unlanded: 2, Worktrees: []string{"wt:1"}},
		},
		Worktrees: []protocol.Worktree{
			{ID: "wt:1", Path: "C:/tmp/wt-detached", Op: "none"},
		},
		Forks: []protocol.ForkPoint{{SHA: "base1", Short: "base1", Branches: []string{"feat/x"}}},
	}
	a := New(&fakeEngine{})
	a.snap = snap
	a.tab = tabTopology

	rows := a.topologyRows()
	// fork header, its one branch, the detached-section divider, one detached row.
	if len(rows) != 4 {
		t.Fatalf("topologyRows() = %d rows, want 4: %+v", len(rows), rows)
	}
	if rows[2].kind != rowDetachedHeader {
		t.Fatalf("row 2 kind = %v, want rowDetachedHeader", rows[2].kind)
	}
	if rows[3].kind != rowDetached || rows[3].unit.ID != "detached:abc1234" {
		t.Fatalf("row 3 = %+v, want the detached unit", rows[3])
	}

	// rowCount is what cursor movement (j/k/g/G) is bounded by - the new rows
	// must be included there too, or the cursor can never actually land on one.
	if got := a.rowCount(); got != len(rows) {
		t.Fatalf("rowCount() = %d, want %d to match topologyRows()", got, len(rows))
	}

	a.cursor = 3
	u, ok := a.selectedUnit()
	if !ok || u.ID != "detached:abc1234" {
		t.Fatalf("selectedUnit() at the detached row = (%+v, %v), want the detached unit", u, ok)
	}
}

func TestViewTopologyRendersDetachedRowNameAndWorktree(t *testing.T) {
	snap := &protocol.Snapshot{
		Units: []protocol.Unit{
			{ID: "detached:abc1234", Head: "abc1234", Verdict: "detached", Worktrees: []string{"wt:1"}},
		},
		Worktrees: []protocol.Worktree{
			{ID: "wt:1", Path: "C:/tmp/wt-detached", Op: "none"},
		},
		// No Forks at all - only detached work in flight - which also
		// exercises viewTopology's empty-state guard: it must key off the
		// actual row count, not len(snap.Forks) directly, or this section
		// would never render for a repo whose only in-flight work is detached.
	}
	a := New(&fakeEngine{})
	a.snap = snap
	a.tab = tabTopology

	out := a.viewTopology(200, 20)
	if !strings.Contains(out, "(detached abc1234)") {
		t.Fatalf("expected the detached unit's display name in the render, got:\n%s", out)
	}
	if !strings.Contains(out, "C:/tmp/wt-detached") {
		t.Fatalf("expected the detached unit's worktree path in the render, got:\n%s", out)
	}
	if !strings.Contains(out, "no branch points here") {
		t.Fatalf("expected the section divider, got:\n%s", out)
	}
}

// tfixture mirrors internal/engine's own efixture helper (itself a copy of
// internal/model's) - real git, real fixtures. A different package needs its
// own copy rather than importing another package's test-only type.
type tfixture struct {
	t   *testing.T
	dir string
}

func newTfixture(t *testing.T) *tfixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	f := &tfixture{t: t, dir: filepath.Join(dir, "repo")}
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

func (f *tfixture) git(dir string, args ...string) string {
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

func (f *tfixture) run(args ...string) string { return f.git(f.dir, args...) }

// tryGit is run's own version without the fatal-on-error, for a command set
// up to fail on purpose (a rebase meant to conflict) so a worktree ends up
// genuinely mid-operation.
func (f *tfixture) tryGit(dir string, args ...string) string {
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

func (f *tfixture) commit(name, body, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.run("add", "-A")
	f.run("commit", "-q", "--no-gpg-sign", "-m", msg)
}

// TestDecideOnDetachedRowReachesDetachedDecisionTree is an integration test
// through a real engine.Engine, not fakeEngine: it proves the actual wiring
// this feature exists for - pressing p with the cursor on a detached row
// reaches decideCtx.detached() (internal/engine/decide.go). Fixture mirrors
// internal/engine/decide_test.go's own TestDecisionTreeDetached (git worktree
// add --detach), reused rather than reinvented.
func TestDecideOnDetachedRowReachesDetachedDecisionTree(t *testing.T) {
	f := newTfixture(t)
	wtDir := filepath.Join(filepath.Dir(f.dir), "wt-detached")
	f.run("worktree", "add", "-q", "--detach", wtDir, "main")
	wantCwd := filepath.ToSlash(wtDir) // git's own worktree list reports "/", even on Windows

	eng, err := engine.Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	snap, err := eng.Snapshot(context.Background(), engine.SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	a := New(eng)
	a.snap = snap
	a.tab = tabTopology

	rows := a.topologyRows()
	cursor := -1
	for i, r := range rows {
		if r.kind == rowDetached {
			cursor = i
		}
	}
	if cursor == -1 {
		t.Fatalf("no rowDetached found among %d rows: %+v", len(rows), rows)
	}
	a.cursor = cursor

	_, cmd := a.onKey(keyRune('p'))
	if cmd == nil {
		t.Fatal("expected a Cmd to fetch the decision tree for the detached unit")
	}
	msg := cmd()
	treeM, ok := msg.(treeMsg)
	if !ok {
		t.Fatalf("expected treeMsg, got %T", msg)
	}
	if treeM.err != nil {
		t.Fatalf("Decide: %v", treeM.err)
	}
	a.Update(treeM)

	if a.node == nil || a.node.Action == nil {
		t.Fatalf("expected the single detached leaf, got node=%+v", a.node)
	}
	leaf := a.node.Action
	if leaf.Kind != "branch-from-detached" || leaf.Class != protocol.ClassGuarded {
		t.Fatalf("detached leaf = %+v, want Kind=branch-from-detached Class=guarded", leaf)
	}
	if leaf.Cwd != wantCwd {
		t.Fatalf("detached leaf Cwd = %q, want the worktree path %q", leaf.Cwd, wantCwd)
	}
	// A clean, idle, checked-out worktree leaves this leaf unblocked, so
	// enterNode should have opened the confirm bar exactly like a plain
	// branch action does - the p/y wiring doesn't get a special case for
	// where the unit came from.
	if a.confirm == nil || a.confirm.action != leaf {
		t.Fatalf("an unblocked guarded leaf should auto-open the confirm bar, got %+v", a.confirm)
	}
}

// TestDetachedSectionIncludesInProgressMidRebaseUnit covers the same root
// cause as the plain-detached case above: a worktree stuck mid-rebase is
// detached until the rebase finishes - the branch ref itself doesn't move
// (see internal/model/landing.go's own comment on buildUnits' second loop) -
// so it must be just as reachable. Fixture mirrors
// internal/engine/decide_test.go's own TestDecisionTreeInProgress, which (unlike
// internal/model/derive_test.go's TestMidRebaseWorktreeReportsInProgress) already
// carries this all the way to a protocol.Snapshot.
func TestDetachedSectionIncludesInProgressMidRebaseUnit(t *testing.T) {
	f := newTfixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-rebase")
	f.run("worktree", "add", "-q", "-b", "feat/stuck", wt)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("wt side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(wt, "commit", "-q", "--no-gpg-sign", "-am", "wt edits a.txt")
	if err := os.WriteFile(filepath.Join(f.dir, "a.txt"), []byte("main side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.run("commit", "-q", "--no-gpg-sign", "-am", "main edits a.txt")
	f.tryGit(wt, "rebase", "main") // conflicts on purpose, leaves wt mid-rebase

	eng, err := engine.Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	snap, err := eng.Snapshot(context.Background(), engine.SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	a := New(eng)
	a.snap = snap
	a.tab = tabTopology

	rows := a.topologyRows()
	cursor := -1
	for i, r := range rows {
		if r.kind == rowDetached && r.unit.Verdict == "in-progress" {
			cursor = i
		}
	}
	if cursor == -1 {
		t.Fatalf("no in-progress detached row found among %d rows: %+v", len(rows), rows)
	}
	a.cursor = cursor

	u, ok := a.selectedUnit()
	if !ok || u.Verdict != "in-progress" || u.Branch != "" {
		t.Fatalf("selectedUnit() at the mid-rebase row = (%+v, %v), want an in-progress branchless unit", u, ok)
	}

	// p must still reach a real tree for this row too - dc.inProgress()'s
	// manual "resolve it yourself" leaf, matching what TestDecisionTreeInProgress
	// checks at the engine layer.
	_, cmd := a.onKey(keyRune('p'))
	if cmd == nil {
		t.Fatal("expected a Cmd to fetch the decision tree for the mid-rebase unit")
	}
	msg := cmd()
	treeM, ok := msg.(treeMsg)
	if !ok {
		t.Fatalf("expected treeMsg, got %T", msg)
	}
	if treeM.err != nil {
		t.Fatalf("Decide: %v", treeM.err)
	}
	a.Update(treeM)

	if a.node == nil || a.node.Action == nil {
		t.Fatalf("expected the single in-progress leaf, got node=%+v", a.node)
	}
	if a.node.Action.Kind != "resolve-in-progress" || a.node.Action.Class != protocol.ClassManual {
		t.Fatalf("in-progress leaf = %+v, want Kind=resolve-in-progress Class=manual", a.node.Action)
	}
}
