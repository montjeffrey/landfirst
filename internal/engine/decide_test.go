package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// tryGit is efixture's own git without the fatal-on-error, for commands set
// up to fail on purpose - a rebase or merge meant to conflict, so a worktree
// ends up genuinely mid-operation. Mirrors internal/model's fixture.tryGit.
func (f *efixture) tryGit(dir string, args ...string) string {
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

// assertNeverUnsafe is the generalized form of
// internal/model/landing_test.go:145's TestRemedyIsNeverUnsafeForAutomaticActions
// (its own doc: no ClassSafe leaf's Argv[0] outside {push, worktree, branch,
// switch}, and no leaf of any class carries a force/-D/reset flag). The old
// test checked one flat Remedy per unit; this one walks a whole DecisionTree
// per unit via walkActions, so it also covers every branch a multi-level
// tree (Diverged) can reach, not just the unit's single top-level command.
func assertNeverUnsafe(t *testing.T, tree *protocol.DecisionTree) {
	t.Helper()
	walkActions(tree.Root, func(a *protocol.Action) {
		if a.Class == protocol.ClassSafe {
			if len(a.Argv) == 0 {
				t.Fatalf("unit %s: safe action %s (%s) has empty Argv", tree.UnitID, a.ID, a.Kind)
			}
			switch a.Argv[0] {
			case "push", "worktree", "branch", "switch":
			default:
				t.Fatalf("unit %s: safe action %s runs %q, not push/worktree/branch/switch: %v",
					tree.UnitID, a.ID, a.Argv[0], a.Argv)
			}
		}
		for _, arg := range a.Argv {
			switch arg {
			case "--force", "-f", "--force-with-lease", "-D", "reset":
				t.Fatalf("unit %s: action %s (class %s) carries a forbidden arg %q: %v",
					tree.UnitID, a.ID, a.Class, arg, a.Argv)
			}
		}
	})
}

// decideAllUnits calls Decide for every unit in a fresh StageFull snapshot
// and runs assertNeverUnsafe on each tree - the sweep every test below uses
// so a fixture only has to set up branches/worktrees, never hand-enumerate
// which units resulted.
func decideAllUnits(t *testing.T, eng Engine) map[string]*protocol.DecisionTree {
	t.Helper()
	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	trees := make(map[string]*protocol.DecisionTree, len(snap.Units))
	for _, u := range snap.Units {
		tree, err := eng.Decide(ctx, u.ID)
		if err != nil {
			t.Fatalf("Decide(%q): %v", u.ID, err)
		}
		if tree.UnitID != u.ID {
			t.Fatalf("tree.UnitID = %q, want %q", tree.UnitID, u.ID)
		}
		if tree.Revision != snap.Revision {
			t.Fatalf("unit %s: tree.Revision = %q, want the snapshot's %q", u.ID, tree.Revision, snap.Revision)
		}
		assertNeverUnsafe(t, tree)
		trees[u.ID] = tree
	}
	return trees
}

func singleLeaf(t *testing.T, tree *protocol.DecisionTree) *protocol.Action {
	t.Helper()
	if tree.Root.Action == nil {
		t.Fatalf("unit %s: expected a single-leaf tree, got %+v", tree.UnitID, tree.Root)
	}
	if len(tree.Root.Options) != 0 {
		t.Fatalf("unit %s: single-leaf tree must have no Options, got %v", tree.UnitID, tree.Root.Options)
	}
	return tree.Root.Action
}

func TestDecisionTreeOrphaned(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/orphan")
	f.commit("o.txt", "orphan", "orphan work")
	f.run("push", "-q", "-u", "origin", "feat/orphan")
	f.run("push", "-q", "origin", "--delete", "feat/orphan")
	f.run("fetch", "-q", "--prune")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	trees := decideAllUnits(t, eng)
	tree := trees["branch:feat/orphan"]
	if tree == nil {
		t.Fatal("branch:feat/orphan not decided")
	}
	a := singleLeaf(t, tree)
	if a.Kind != "push" || a.Class != protocol.ClassSafe {
		t.Fatalf("orphaned leaf = %+v, want Kind=push Class=safe", a)
	}
	want := []string{"push", "-u", "origin", "feat/orphan:feat/orphan"}
	if !equalArgv(a.Argv, want) {
		t.Fatalf("orphaned Argv = %v, want %v", a.Argv, want)
	}
	if tree.Suggested != a.ID {
		t.Fatalf("Suggested = %q, want the single leaf %q", tree.Suggested, a.ID)
	}
}

func TestDecisionTreeWrongUpstream(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/mistracked")
	f.commit("w.txt", "work", "work")
	f.run("branch", "--set-upstream-to=origin/main", "feat/mistracked")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	trees := decideAllUnits(t, eng)
	tree := trees["branch:feat/mistracked"]
	if tree == nil {
		t.Fatal("branch:feat/mistracked not decided")
	}
	a := singleLeaf(t, tree)
	if a.Kind != "push" || a.Class != protocol.ClassSafe {
		t.Fatalf("wrong-upstream leaf = %+v, want Kind=push Class=safe", a)
	}
	want := []string{"push", "-u", "origin", "feat/mistracked:feat/mistracked"}
	if !equalArgv(a.Argv, want) {
		t.Fatalf("wrong-upstream Argv = %v, want %v", a.Argv, want)
	}
}

func TestDecisionTreeNeverPushed(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/local-only")
	f.commit("n.txt", "local", "never pushed")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	trees := decideAllUnits(t, eng)
	tree := trees["branch:feat/local-only"]
	if tree == nil {
		t.Fatal("branch:feat/local-only not decided")
	}
	a := singleLeaf(t, tree)
	if a.Kind != "push" || a.Class != protocol.ClassSafe {
		t.Fatalf("never-pushed leaf = %+v, want Kind=push Class=safe", a)
	}
	want := []string{"push", "-u", "origin", "feat/local-only:feat/local-only"}
	if !equalArgv(a.Argv, want) {
		t.Fatalf("never-pushed Argv = %v, want %v", a.Argv, want)
	}
}

// TestDecisionTreeNeverPushedNoRemote generalizes
// internal/model/landing_test.go's TestNoRemoteConfiguredOffersNoAutoPushRemedy:
// a legitimate local-only repo (git.Open leaves Remote == "", see
// internal/git/open.go's ErrNoRemote doc) must not get an executable push
// leaf built from an empty remote.
func TestDecisionTreeNeverPushedNoRemote(t *testing.T) {
	dir := t.TempDir()
	f := &efixture{t: t, dir: filepath.Join(dir, "repo")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.run("init", "-q", "-b", "main")
	f.run("config", "user.email", "t@example.invalid")
	f.run("config", "user.name", "Test")
	f.commit("a.txt", "base", "init") // no remote added anywhere
	f.run("checkout", "-q", "-b", "feat/local-only")
	f.commit("n.txt", "local", "never pushed, no remote configured at all")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Repo.Remote != "" {
		t.Fatalf("test setup: expected an empty Remote, got %q", snap.Repo.Remote)
	}

	trees := decideAllUnits(t, eng)
	tree := trees["branch:feat/local-only"]
	if tree == nil {
		t.Fatal("branch:feat/local-only not decided")
	}
	a := singleLeaf(t, tree)
	if a.Kind != "no-remote" || a.Class != protocol.ClassManual {
		t.Fatalf("no-remote leaf = %+v, want Kind=no-remote Class=manual", a)
	}
	if len(a.Argv) != 0 {
		t.Fatalf("no-remote leaf must not carry a push argv, got %v", a.Argv)
	}
	if tree.Suggested != "" {
		t.Fatalf("Suggested = %q, want \"\" - nothing to suggest with no remote", tree.Suggested)
	}
}

func TestDecisionTreeUnpushed(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/unpushed")
	f.commit("u1.txt", "one", "first")
	f.run("push", "-q", "-u", "origin", "feat/unpushed")
	f.commit("u2.txt", "two", "second, not yet pushed")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	trees := decideAllUnits(t, eng)
	tree := trees["branch:feat/unpushed"]
	if tree == nil {
		t.Fatal("branch:feat/unpushed not decided")
	}
	a := singleLeaf(t, tree)
	if a.Kind != "push" || a.Class != protocol.ClassSafe {
		t.Fatalf("unpushed leaf = %+v, want Kind=push Class=safe", a)
	}
	want := []string{"push", "origin", "feat/unpushed:feat/unpushed"}
	if !equalArgv(a.Argv, want) {
		t.Fatalf("unpushed Argv = %v, want %v", a.Argv, want)
	}
}

func TestDecisionTreeSafeToDelete(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/squashed")
	f.commit("s.txt", "squashed", "squashed work")
	f.run("push", "-q", "-u", "origin", "feat/squashed")
	f.run("checkout", "-q", "main")
	f.run("merge", "-q", "--squash", "feat/squashed")
	f.run("commit", "-q", "--no-gpg-sign", "-m", "squash merge")
	f.run("push", "-q", "origin", "main")
	f.run("push", "-q", "origin", "--delete", "feat/squashed")
	f.run("fetch", "-q", "--prune")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	trees := decideAllUnits(t, eng)
	tree := trees["branch:feat/squashed"]
	if tree == nil {
		t.Fatal("branch:feat/squashed not decided")
	}
	a := singleLeaf(t, tree)
	if a.Kind != "delete-branch" || a.Class != protocol.ClassSafe {
		t.Fatalf("safe-to-delete leaf = %+v, want Kind=delete-branch Class=safe", a)
	}
	want := []string{"branch", "-d", "feat/squashed"}
	if !equalArgv(a.Argv, want) {
		t.Fatalf("safe-to-delete Argv = %v, want %v", a.Argv, want)
	}
}

// TestDecisionTreeDetached covers the normal (and structurally only
// reachable) shape for this verdict: a Detached *unit* exists only because
// some worktree is genuinely on no branch - see buildUnits's second loop in
// landing.go - so it always has that one worktree associated. The !ok /
// no-worktree branch of attachWorktree is unreachable for this verdict via a
// real fixture; TestDetachedLeafBlocksWithNoAssociatedWorktree below proves
// it directly instead, against decideCtx.detached() itself.
func TestDecisionTreeDetached(t *testing.T) {
	f := newEfixture(t)
	wtDir := filepath.Join(filepath.Dir(f.dir), "wt-detached")
	f.run("worktree", "add", "-q", "--detach", wtDir, "main")
	wantCwd := filepath.ToSlash(wtDir) // git's own worktree list reports "/", even on Windows

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var detachedID string
	for _, u := range snap.Units {
		if u.Verdict == "detached" {
			detachedID = u.ID
		}
	}
	if detachedID == "" {
		t.Fatalf("no detached unit found among %d units", len(snap.Units))
	}

	trees := decideAllUnits(t, eng)
	tree := trees[detachedID]
	a := singleLeaf(t, tree)
	if a.Kind != "branch-from-detached" || a.Class != protocol.ClassGuarded {
		t.Fatalf("detached leaf = %+v, want Kind=branch-from-detached Class=guarded", a)
	}
	if a.Argv[0] != "switch" {
		t.Fatalf("detached Argv = %v, want switch ...", a.Argv)
	}
	if a.Cwd != wantCwd {
		t.Fatalf("detached Cwd = %q, want the worktree path %q", a.Cwd, wantCwd)
	}
	if len(a.Blocked) != 0 {
		t.Fatalf("a checked-out, clean, idle worktree must not be blocked, got %v", a.Blocked)
	}
	if len(a.Preconditions) == 0 {
		t.Fatal("a guarded leaf must document its preconditions")
	}
	if tree.Suggested != a.ID {
		t.Fatalf("Suggested = %q, want the single leaf %q", tree.Suggested, a.ID)
	}
}

// TestDetachedLeafBlocksWithNoAssociatedWorktree exercises attachWorktree's
// !ok branch directly against decideCtx (no git involved - detached() is a
// pure function of its inputs), since a real Detached unit always has its
// one worktree associated and cannot produce this shape through Decide.
func TestDetachedLeafBlocksWithNoAssociatedWorktree(t *testing.T) {
	dc := &decideCtx{
		snap: &protocol.Snapshot{Repo: protocol.RepoInfo{Root: "/repo"}},
		unit: &protocol.Unit{ID: "detached:abc1234", Head: "abc1234"},
	}
	node, suggested := dc.detached()
	a := node.Action
	if a == nil {
		t.Fatal("expected a leaf action")
	}
	if a.Cwd != "" {
		t.Fatalf("Cwd = %q, want \"\" - must not default to the repo root", a.Cwd)
	}
	if len(a.Blocked) != 1 || a.Blocked[0] != "no worktree checked out for this branch" {
		t.Fatalf("Blocked = %v, want exactly the no-worktree message", a.Blocked)
	}
	if suggested != a.ID {
		t.Fatalf("Suggested = %q, want the single leaf %q even though it is blocked", suggested, a.ID)
	}
}

func TestDecisionTreeInProgress(t *testing.T) {
	f := newEfixture(t)
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
	f.tryGit(wt, "rebase", "main")  // conflicts on purpose, leaves wt mid-rebase
	wantCwd := filepath.ToSlash(wt) // git's own worktree list reports "/", even on Windows

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	// A worktree mid-rebase is detached until the rebase finishes - the
	// branch ref itself doesn't move (see buildUnits's own comment on this
	// in landing.go) - so this shows up as its own "detached:<sha>" unit,
	// never as "branch:feat/stuck": that ID still exists too, judged
	// independently (and harmlessly) from the branch ref's unmoved tip.
	var inProgressID string
	for _, u := range snap.Units {
		if u.Verdict == "in-progress" {
			inProgressID = u.ID
		}
	}
	if inProgressID == "" {
		t.Fatalf("no in-progress unit found among %d units", len(snap.Units))
	}

	trees := decideAllUnits(t, eng)
	tree := trees[inProgressID]
	if tree == nil {
		t.Fatalf("%s not decided", inProgressID)
	}
	a := singleLeaf(t, tree)
	if a.Kind != "resolve-in-progress" || a.Class != protocol.ClassManual {
		t.Fatalf("in-progress leaf = %+v, want Kind=resolve-in-progress Class=manual", a)
	}
	if a.Argv != nil {
		t.Fatalf("in-progress leaf must carry no Argv, got %v", a.Argv)
	}
	if a.Cwd != wantCwd {
		t.Fatalf("in-progress Cwd = %q, want the stuck worktree %q", a.Cwd, wantCwd)
	}
	if !strings.Contains(a.Label, "rebase") {
		t.Fatalf("in-progress Label = %q, want it to name the actual op (rebase)", a.Label)
	}
	if tree.Suggested != "" {
		t.Fatalf("Suggested = %q, want \"\" - nothing is safe to suggest here", tree.Suggested)
	}
}

func TestDecisionTreeBehindAndLandedAreEmpty(t *testing.T) {
	f := newEfixture(t)

	// feat/landed: pushed, no divergence either way.
	f.run("checkout", "-q", "-b", "feat/landed")
	f.commit("l.txt", "landed", "in sync")
	f.run("push", "-q", "-u", "origin", "feat/landed")
	f.run("checkout", "-q", "main")

	// feat/behind: pushed, then the remote moves ahead without this branch,
	// via a second clone acting as another contributor.
	f.run("checkout", "-q", "-b", "feat/behind")
	f.commit("b.txt", "behind", "starting point")
	f.run("push", "-q", "-u", "origin", "feat/behind")
	f.run("checkout", "-q", "main")

	remote := filepath.Join(filepath.Dir(f.dir), "remote.git")
	clone := filepath.Join(filepath.Dir(f.dir), "clone")
	f.git(filepath.Dir(f.dir), "clone", "-q", remote, clone)
	f.git(clone, "checkout", "-q", "feat/behind")
	if err := os.WriteFile(filepath.Join(clone, "b2.txt"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(clone, "add", "-A")
	f.git(clone, "-c", "user.email=t@example.invalid", "-c", "user.name=Test", "commit", "-q", "--no-gpg-sign", "-m", "someone else's commit")
	f.git(clone, "push", "-q", "origin", "feat/behind")
	f.run("fetch", "-q", "origin")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	trees := decideAllUnits(t, eng)

	landed := trees["branch:feat/landed"]
	if landed == nil {
		t.Fatal("branch:feat/landed not decided")
	}
	if landed.Root.Action != nil || landed.Root.Question != "" || len(landed.Root.Options) != 0 {
		t.Fatalf("landed tree must be the zero DecisionNode, got %+v", landed.Root)
	}
	if landed.Suggested != "" {
		t.Fatalf("landed Suggested = %q, want \"\"", landed.Suggested)
	}

	behind := trees["branch:feat/behind"]
	if behind == nil {
		t.Fatal("branch:feat/behind not decided")
	}
	if behind.Root.Action != nil || behind.Root.Question != "" || len(behind.Root.Options) != 0 {
		t.Fatalf("behind tree must be the zero DecisionNode, got %+v", behind.Root)
	}
	if behind.Suggested != "" {
		t.Fatalf("behind Suggested = %q, want \"\"", behind.Suggested)
	}
}

// TestDecisionTreeDiverged is the plan's rich worked example: both sides
// moved, so the tree has two levels instead of one leaf. Covers both the
// no-worktree-checked-out shape (every leaf but merge-into-base blocks) and,
// separately, the case where a worktree is checked out (Cwd actually
// populated, blockedFor reading real state).
func TestDecisionTreeDiverged(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/diverge")
	f.commit("d.txt", "local", "local side")
	f.run("push", "-q", "-u", "origin", "feat/diverge")
	f.run("commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "another local commit")
	f.run("checkout", "-q", "main")
	// Move the remote out from under it too, so both Ahead and Behind are
	// nonzero - Diverged, not merely Unpushed.
	clone := filepath.Join(filepath.Dir(f.dir), "clone-diverge")
	remote := filepath.Join(filepath.Dir(f.dir), "remote.git")
	f.git(filepath.Dir(f.dir), "clone", "-q", remote, clone)
	f.git(clone, "checkout", "-q", "feat/diverge")
	f.git(clone, "-c", "user.email=t@example.invalid", "-c", "user.name=Test", "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "someone else's commit")
	f.git(clone, "push", "-q", "origin", "feat/diverge")
	f.run("fetch", "-q", "origin")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/diverge" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/diverge not found in units")
	}
	if unit.Verdict != "diverged" {
		t.Fatalf("test setup: verdict = %q, want diverged", unit.Verdict)
	}

	tree, err := eng.Decide(ctx, unit.ID)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	assertNeverUnsafe(t, tree)

	if tree.Root.Action != nil {
		t.Fatal("diverged root must be a question, not a leaf")
	}
	if len(tree.Root.Options) != 3 {
		t.Fatalf("diverged root must have 3 options, got %d: %+v", len(tree.Root.Options), tree.Root.Options)
	}
	land := tree.Root.Options[2].Next
	if land.Action != nil {
		t.Fatal("the third branch must itself be a question (how should it land), not a leaf")
	}
	if len(land.Options) != 2 {
		t.Fatalf("the landing sub-question must have 2 options, got %d", len(land.Options))
	}

	byKind := map[string]*protocol.Action{}
	walkActions(tree.Root, func(a *protocol.Action) { byKind[a.Kind] = a })
	for _, kind := range []string{"rebase-onto-upstream", "merge-upstream", "rebase-onto-base", "merge-into-base"} {
		if byKind[kind] == nil {
			t.Fatalf("missing leaf %q in diverged tree", kind)
		}
	}

	rebaseUp := byKind["rebase-onto-upstream"]
	if rebaseUp.Class != protocol.ClassManual || !equalArgv(rebaseUp.Argv, []string{"rebase", "origin/feat/diverge"}) {
		t.Fatalf("rebase-onto-upstream = %+v", rebaseUp)
	}
	mergeUp := byKind["merge-upstream"]
	if mergeUp.Class != protocol.ClassGuarded || !equalArgv(mergeUp.Argv, []string{"merge", "--no-edit", "origin/feat/diverge"}) {
		t.Fatalf("merge-upstream = %+v", mergeUp)
	}
	rebaseBase := byKind["rebase-onto-base"]
	if rebaseBase.Class != protocol.ClassManual || rebaseBase.Argv[0] != "rebase" {
		t.Fatalf("rebase-onto-base = %+v", rebaseBase)
	}
	mergeBase := byKind["merge-into-base"]
	if mergeBase.Class != protocol.ClassGuarded || !equalArgv(mergeBase.Argv, []string{"merge", "--no-ff", "feat/diverge"}) {
		t.Fatalf("merge-into-base = %+v", mergeBase)
	}
	// merge-into-base runs where base is checked out (the main worktree),
	// never in this branch's own worktree.
	if mergeBase.Cwd != snap.Repo.Root {
		t.Fatalf("merge-into-base.Cwd = %q, want the repo root %q", mergeBase.Cwd, snap.Repo.Root)
	}

	// feat/diverge has no worktree checked out anywhere in this fixture, so
	// every leaf but merge-into-base (which uses the main worktree instead)
	// must report itself blocked rather than guess a Cwd.
	for _, kind := range []string{"rebase-onto-upstream", "merge-upstream", "rebase-onto-base"} {
		a := byKind[kind]
		if a.Cwd != "" {
			t.Fatalf("%s: Cwd = %q, want \"\" - no worktree checked out for feat/diverge", kind, a.Cwd)
		}
		if len(a.Blocked) == 0 {
			t.Fatalf("%s: expected Blocked to report the missing worktree, got none", kind)
		}
	}
	// The main worktree is checked out, clean and idle, so merge-into-base
	// itself must not be blocked.
	if len(mergeBase.Blocked) != 0 {
		t.Fatalf("merge-into-base.Blocked = %v, want none - the main worktree is idle and clean", mergeBase.Blocked)
	}

	if tree.Suggested != mergeUp.ID {
		t.Fatalf("Suggested = %q, want the merge-upstream leaf %q (guarded, least destructive default)", tree.Suggested, mergeUp.ID)
	}
}

// TestDecisionTreeDivergedMergeIntoBaseBlockedWhenMainNotOnBase is the
// regression test for the gap attachWorktree alone left open: it only ever
// confirmed the main worktree is clean/idle/exists, never that the branch
// actually checked out there is base. Same fixture as TestDecisionTreeDiverged
// (feat/diverge, genuinely diverged), except the repo root ends up on a third,
// unrelated branch instead of main - an operator mid-errand elsewhere in the
// repo. merge-into-base must refuse rather than silently offer to merge
// feat/diverge into whatever that third branch happens to be.
func TestDecisionTreeDivergedMergeIntoBaseBlockedWhenMainNotOnBase(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/diverge")
	f.commit("d.txt", "local", "local side")
	f.run("push", "-q", "-u", "origin", "feat/diverge")
	f.run("commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "another local commit")
	f.run("checkout", "-q", "main")
	clone := filepath.Join(filepath.Dir(f.dir), "clone-diverge-badmain")
	remote := filepath.Join(filepath.Dir(f.dir), "remote.git")
	f.git(filepath.Dir(f.dir), "clone", "-q", remote, clone)
	f.git(clone, "checkout", "-q", "feat/diverge")
	f.git(clone, "-c", "user.email=t@example.invalid", "-c", "user.name=Test", "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "someone else's commit")
	f.git(clone, "push", "-q", "origin", "feat/diverge")
	f.run("fetch", "-q", "origin")

	// The one difference from TestDecisionTreeDiverged: the main worktree
	// (repo root) is left on a third branch, not main/base, right before
	// Decide runs.
	f.run("checkout", "-q", "-b", "operator/unrelated-errand")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/diverge" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/diverge not found in units")
	}
	if unit.Verdict != "diverged" {
		t.Fatalf("test setup: verdict = %q, want diverged", unit.Verdict)
	}
	var main *protocol.Worktree
	for i := range snap.Worktrees {
		if snap.Worktrees[i].IsMain {
			main = &snap.Worktrees[i]
		}
	}
	if main == nil {
		t.Fatal("test setup: no main worktree found")
	}
	if main.Branch != "operator/unrelated-errand" {
		t.Fatalf("test setup: main worktree Branch = %q, want operator/unrelated-errand (not base)", main.Branch)
	}

	tree, err := eng.Decide(ctx, unit.ID)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	assertNeverUnsafe(t, tree)

	var mergeBase *protocol.Action
	walkActions(tree.Root, func(a *protocol.Action) {
		if a.Kind == "merge-into-base" {
			mergeBase = a
		}
	})
	if mergeBase == nil {
		t.Fatal("merge-into-base leaf not found")
	}
	// It still runs where base is checked out - or would, if it weren't
	// blocked - so Cwd stays the main worktree's path; what must change is
	// that this leaf is no longer safe to execute.
	if mergeBase.Cwd != snap.Repo.Root {
		t.Fatalf("merge-into-base.Cwd = %q, want the repo root %q", mergeBase.Cwd, snap.Repo.Root)
	}
	if len(mergeBase.Blocked) == 0 {
		t.Fatal("merge-into-base must be blocked when the main worktree is not on base, got no Blocked entries")
	}
	found := false
	for _, b := range mergeBase.Blocked {
		if strings.Contains(b, "main") && strings.Contains(b, "checked out") {
			found = true
		}
	}
	if !found {
		t.Fatalf("merge-into-base.Blocked = %v, want an entry naming base (%q) as not checked out", mergeBase.Blocked, snap.Repo.Base)
	}
	// The same text must also appear in Preconditions - Blocked entries are
	// always a subset of Preconditions (protocol.Action's own doc), so a
	// client can match one against the other by plain string equality.
	preFound := false
	for _, p := range mergeBase.Preconditions {
		if p == mergeBase.Blocked[len(mergeBase.Blocked)-1] {
			preFound = true
		}
	}
	if !preFound {
		t.Fatalf("Blocked entry %q must also appear verbatim in Preconditions %v", mergeBase.Blocked[len(mergeBase.Blocked)-1], mergeBase.Preconditions)
	}
}

// TestDecisionTreeDivergedWithWorktreeChecksOutCwd exercises the populated
// path of attachWorktree - a worktree IS checked out on the diverged branch,
// so the first three leaves get a real Cwd instead of being blocked.
func TestDecisionTreeDivergedWithWorktreeChecksOutCwd(t *testing.T) {
	f := newEfixture(t)
	wt := filepath.Join(filepath.Dir(f.dir), "wt-diverge")
	wantCwd := filepath.ToSlash(wt) // git's own worktree list reports "/", even on Windows
	f.run("worktree", "add", "-q", "-b", "feat/diverge-wt", wt)
	f.git(wt, "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "wt first commit")
	f.run("push", "-q", "-u", "origin", "feat/diverge-wt:feat/diverge-wt")
	// A second, local-only commit so this branch is genuinely Ahead too, not
	// just Behind once the remote-side commit below lands.
	f.git(wt, "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "wt local-only commit")

	clone := filepath.Join(filepath.Dir(f.dir), "clone-diverge-wt")
	remote := filepath.Join(filepath.Dir(f.dir), "remote.git")
	f.git(filepath.Dir(f.dir), "clone", "-q", remote, clone)
	f.git(clone, "checkout", "-q", "feat/diverge-wt")
	f.git(clone, "-c", "user.email=t@example.invalid", "-c", "user.name=Test", "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "someone else's commit")
	f.git(clone, "push", "-q", "origin", "feat/diverge-wt")
	f.git(wt, "fetch", "-q", "origin")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/diverge-wt" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/diverge-wt not found in units")
	}
	if unit.Verdict != "diverged" {
		t.Fatalf("test setup: verdict = %q, want diverged", unit.Verdict)
	}

	tree, err := eng.Decide(ctx, unit.ID)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	assertNeverUnsafe(t, tree)

	var mergeUp *protocol.Action
	walkActions(tree.Root, func(a *protocol.Action) {
		if a.Kind == "merge-upstream" {
			mergeUp = a
		}
	})
	if mergeUp == nil {
		t.Fatal("merge-upstream leaf not found")
	}
	if mergeUp.Cwd != wantCwd {
		t.Fatalf("merge-upstream.Cwd = %q, want the checked-out worktree %q", mergeUp.Cwd, wantCwd)
	}
	if len(mergeUp.Blocked) != 0 {
		t.Fatalf("a clean, idle worktree must not block merge-upstream, got %v", mergeUp.Blocked)
	}
}

func equalArgv(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestExecuteRejectsWrongConfirmationAndDoesNothing proves Execute's
// confirmation check actually gates execution, not just that it returns the
// right error: after a rejected call, re-reading state via a fresh Snapshot
// shows the branch exactly as unpushed as before - if the push had run
// despite the wrong confirmation, Verdict/Upstream here would have changed.
func TestExecuteRejectsWrongConfirmationAndDoesNothing(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/guard")
	f.commit("g.txt", "work", "unpushed work")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	tree, err := eng.Decide(ctx, "branch:feat/guard")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	a := singleLeaf(t, tree)
	if a.Class != protocol.ClassSafe || a.Kind != "push" {
		t.Fatalf("test setup: expected a safe push leaf, got %+v", a)
	}

	_, err = eng.Execute(ctx, protocol.ExecuteRequest{
		ActionID:     a.ID,
		Revision:     tree.Revision,
		Confirmation: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if !errors.Is(err, ErrConfirmationMismatch) {
		t.Fatalf("Execute with a wrong confirmation: err = %v, want ErrConfirmationMismatch", err)
	}

	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == "feat/guard" {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatal("feat/guard not found after the rejected Execute")
	}
	if unit.Verdict != "never-pushed" || unit.Upstream != "" || unit.Ahead != 0 {
		t.Fatalf("a rejected Execute must not have pushed anything: verdict=%q upstream=%q ahead=%d",
			unit.Verdict, unit.Upstream, unit.Ahead)
	}
}

// TestExecuteRunsASafeActionAndAdvancesRevision is the corresponding
// positive path: the right confirmation (computed independently here, via
// the exact recipe protocol.ExecuteRequest.Confirmation documents - sha256
// of argv joined by \x00 - rather than by calling this package's own
// confirmationOf, so the test does not just check the code against itself)
// actually runs the command and reports a new revision.
func TestExecuteRunsASafeActionAndAdvancesRevision(t *testing.T) {
	f := newEfixture(t)
	f.run("checkout", "-q", "-b", "feat/run-me")
	f.commit("r.txt", "work", "never pushed")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	tree, err := eng.Decide(ctx, "branch:feat/run-me")
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	a := singleLeaf(t, tree)

	sum := sha256.Sum256([]byte(strings.Join(a.Argv, "\x00")))
	confirmation := hex.EncodeToString(sum[:])

	result, err := eng.Execute(ctx, protocol.ExecuteRequest{
		ActionID:     a.ID,
		Revision:     tree.Revision,
		Confirmation: confirmation,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Ran {
		t.Fatalf("expected Ran=true for a safe, unblocked action, got %+v", result)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected the push to succeed, got exit %d, stderr=%q", result.ExitCode, result.Stderr)
	}
	if result.Revision == tree.Revision {
		t.Fatal("Revision must advance after a real run - the repo state changed")
	}

	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Revision != result.Revision {
		t.Fatalf("Snapshot's revision after Execute = %q, want Execute's own result.Revision %q", snap.Revision, result.Revision)
	}
	for _, u := range snap.Units {
		if u.Branch == "feat/run-me" && u.Upstream == "" {
			t.Fatalf("feat/run-me should have an upstream after the push, got %+v", u)
		}
	}
}
