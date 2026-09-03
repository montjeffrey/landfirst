package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/engine"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// ---- fakes ----
//
// These implement engine.Engine/engine.WorktreeService directly (no git, no
// real repository) so tree-navigation and worktree-op wiring can be tested
// as pure state transitions - the bubbletea runtime and a real terminal are
// neither needed nor testable here (see the package's own verification
// notes).

type fakeWorktrees struct {
	addReq    protocol.WorktreeAddRequest
	addResult *protocol.ExecuteResult
	addErr    error

	removeReq    protocol.WorktreeRemoveRequest
	removeResult *protocol.ExecuteResult
	removeErr    error
}

func (f *fakeWorktrees) Add(ctx context.Context, req protocol.WorktreeAddRequest) (*protocol.ExecuteResult, error) {
	f.addReq = req
	return f.addResult, f.addErr
}
func (f *fakeWorktrees) Remove(ctx context.Context, req protocol.WorktreeRemoveRequest) (*protocol.ExecuteResult, error) {
	f.removeReq = req
	return f.removeResult, f.removeErr
}
func (f *fakeWorktrees) Prune(ctx context.Context) (*protocol.ExecuteResult, error) { return nil, nil }

type fakeEngine struct {
	wt *fakeWorktrees

	decideUnitID string
	decideTree   *protocol.DecisionTree
	decideErr    error

	executeReq    protocol.ExecuteRequest
	executeResult *protocol.ExecuteResult
	executeErr    error
}

func (f *fakeEngine) Snapshot(ctx context.Context, o engine.SnapshotOptions) (*protocol.Snapshot, error) {
	return nil, nil
}
func (f *fakeEngine) Graph(ctx context.Context, o protocol.GraphOptions) (*protocol.Graph, error) {
	return nil, nil
}
func (f *fakeEngine) Decide(ctx context.Context, unitID string) (*protocol.DecisionTree, error) {
	f.decideUnitID = unitID
	return f.decideTree, f.decideErr
}
func (f *fakeEngine) Execute(ctx context.Context, req protocol.ExecuteRequest) (*protocol.ExecuteResult, error) {
	f.executeReq = req
	return f.executeResult, f.executeErr
}
func (f *fakeEngine) Watch(ctx context.Context) (<-chan protocol.ChangeEvent, error) { return nil, nil }
func (f *fakeEngine) WatchFallback() bool                                            { return false }
func (f *fakeEngine) Worktrees() engine.WorktreeService {
	if f.wt == nil {
		f.wt = &fakeWorktrees{}
	}
	return f.wt
}
func (f *fakeEngine) Close() error { return nil }

func keyRune(r rune) tea.KeyMsg        { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
func keyType(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// divergedFixtureTree mirrors internal/engine/decide.go's diverged() shape:
// a root question, a nested question down the third branch, and leaves of
// every class this package's tree UI has to render differently (manual,
// guarded-and-unblocked, and - built by the caller as needed -
// guarded-but-blocked).
func divergedFixtureTree() (*protocol.DecisionTree, map[string]*protocol.Action) {
	rebaseUp := &protocol.Action{ID: "branch:x/rebase-onto-upstream", Kind: "rebase-onto-upstream",
		Label: "rebase onto upstream", Argv: []string{"rebase", "origin/x"}, Class: protocol.ClassManual}
	mergeUp := &protocol.Action{ID: "branch:x/merge-upstream", Kind: "merge-upstream",
		Label: "merge upstream in", Argv: []string{"merge", "--no-edit", "origin/x"}, Class: protocol.ClassGuarded}
	rebaseBase := &protocol.Action{ID: "branch:x/rebase-onto-base", Kind: "rebase-onto-base",
		Label: "rebase onto base", Argv: []string{"rebase", "origin/main"}, Class: protocol.ClassManual}
	mergeBase := &protocol.Action{ID: "branch:x/merge-into-base", Kind: "merge-into-base",
		Label: "merge into base", Argv: []string{"merge", "--no-ff", "x"}, Class: protocol.ClassGuarded}

	root := protocol.DecisionNode{
		Question: "origin/x and local both moved. Keep local history linear?",
		Options: []protocol.DecisionEdge{
			{Answer: "yes, rebase onto origin/x", Next: protocol.DecisionNode{Action: rebaseUp}},
			{Answer: "no, merge origin/x in", Next: protocol.DecisionNode{Action: mergeUp}},
			{Answer: "I want this on main now", Next: protocol.DecisionNode{
				Question: "How should it land?",
				Options: []protocol.DecisionEdge{
					{Answer: "rebase onto main then push", Next: protocol.DecisionNode{Action: rebaseBase}},
					{Answer: "merge into main", Next: protocol.DecisionNode{Action: mergeBase}},
				},
			}},
		},
	}
	tree := &protocol.DecisionTree{UnitID: "branch:x", Revision: "rev1", Suggested: mergeUp.ID, Root: root}
	byKind := map[string]*protocol.Action{
		"rebase-onto-upstream": rebaseUp, "merge-upstream": mergeUp,
		"rebase-onto-base": rebaseBase, "merge-into-base": mergeBase,
	}
	return tree, byKind
}

func TestTreeNavigationEntersRootWithNoConfirmForAQuestion(t *testing.T) {
	tree, _ := divergedFixtureTree()
	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})

	if a.node != &a.tree.Root {
		t.Fatalf("node = %p, want &tree.Root %p", a.node, &a.tree.Root)
	}
	if len(a.path) != 1 || a.path[0] != &a.tree.Root {
		t.Fatalf("path = %v, want [&tree.Root]", a.path)
	}
	if a.confirm != nil {
		t.Fatalf("a question root must not open a confirm, got %+v", a.confirm)
	}
}

func TestTreeNavigationSelectsNestedOptionAndTracksPath(t *testing.T) {
	tree, byKind := divergedFixtureTree()
	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})

	// "3" picks "I want this on main now" - a nested question, not a leaf.
	a.onKey(keyRune('3'))
	if a.node.Question != "How should it land?" {
		t.Fatalf("node.Question = %q, want the nested question", a.node.Question)
	}
	if len(a.path) != 2 || a.path[0] != &a.tree.Root || a.path[1] != a.node {
		t.Fatalf("path = %v, want [root, node]", a.path)
	}

	// "2" from there picks merge-into-base - guarded, unblocked, so it should
	// auto-open the confirm bar exactly like a single-leaf tree would.
	a.onKey(keyRune('2'))
	if a.node.Action != byKind["merge-into-base"] {
		t.Fatalf("node.Action = %+v, want merge-into-base", a.node.Action)
	}
	if a.confirm == nil || a.confirm.kind != confirmAction || a.confirm.action != byKind["merge-into-base"] {
		t.Fatalf("expected merge-into-base to auto-open a confirm, got %+v", a.confirm)
	}
}

func TestTreeNavigationOutOfRangeOptionIsIgnored(t *testing.T) {
	tree, _ := divergedFixtureTree()
	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})
	before := a.node

	a.onKey(keyRune('9')) // root only has 3 options
	if a.node != before {
		t.Fatalf("an out-of-range option must not move the cursor, node changed to %+v", a.node)
	}
}

func TestTreeNavigationEscPopsThenExits(t *testing.T) {
	tree, _ := divergedFixtureTree()
	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})
	a.onKey(keyRune('3')) // descend into the nested question

	a.onKey(keyType(tea.KeyEsc))
	if a.node != &a.tree.Root {
		t.Fatalf("first esc should pop back to the root question, got %+v", a.node)
	}
	if a.tree == nil {
		t.Fatal("still mid-navigation after one esc - tree must not be cleared yet")
	}

	a.onKey(keyType(tea.KeyEsc))
	if a.tree != nil || a.node != nil || a.path != nil {
		t.Fatalf("esc at the root must fully exit tree mode, got tree=%v node=%v path=%v", a.tree, a.node, a.path)
	}
}

func TestTreeManualLeafAnyKeyDismissesToNormalBrowsing(t *testing.T) {
	tree, byKind := divergedFixtureTree()
	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})

	a.onKey(keyRune('1')) // rebase-onto-upstream: ClassManual
	if a.node.Action != byKind["rebase-onto-upstream"] {
		t.Fatalf("node.Action = %+v, want rebase-onto-upstream", a.node.Action)
	}
	if a.confirm != nil {
		t.Fatalf("a manual leaf must never open a confirm, got %+v", a.confirm)
	}

	// Any key, esc included, dismisses a leaf's own text straight back to
	// normal browsing - there's nowhere to "back out" to from a leaf.
	a.onKey(keyType(tea.KeyEsc))
	if a.tree != nil || a.node != nil || a.path != nil {
		t.Fatalf("esc on a manual leaf must fully exit tree mode, got tree=%v node=%v", a.tree, a.node)
	}
}

func TestTreeBlockedLeafShowsReasonAndDismissesWithoutConfirm(t *testing.T) {
	blocked := &protocol.Action{ID: "branch:y/switch", Label: "give this work a branch",
		Argv: []string{"switch", "-c", "wip/abc"}, Class: protocol.ClassGuarded,
		Blocked: []string{"worktree has no uncommitted changes"}}
	tree := &protocol.DecisionTree{UnitID: "branch:y", Revision: "rev1", Root: protocol.DecisionNode{Action: blocked}}

	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})
	if a.confirm != nil {
		t.Fatalf("a blocked leaf must not open a confirm, got %+v", a.confirm)
	}
	footer := a.leafFooter(80, blocked)
	if !strings.Contains(footer, "blocked: worktree has no uncommitted changes") {
		t.Fatalf("leafFooter did not show the blocked reason:\n%s", footer)
	}

	a.onKey(keyRune('x'))
	if a.tree != nil || a.node != nil {
		t.Fatal("any key must dismiss a blocked leaf back to normal browsing")
	}
}

func TestTreeFooterAnnotatesSuggestedLeaf(t *testing.T) {
	tree, _ := divergedFixtureTree()
	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})

	footer := a.treeFooter(100)
	if !strings.Contains(footer, "no, merge origin/x in") || !strings.Contains(footer, "(suggested)") {
		t.Fatalf("expected the merge-upstream option annotated as suggested:\n%s", footer)
	}
	// The other two top-level options are leaves too, but neither is
	// tree.Suggested, so they must not be annotated.
	for _, line := range strings.Split(footer, "\n") {
		if strings.Contains(line, "yes, rebase") && strings.Contains(line, "(suggested)") {
			t.Fatalf("rebase-onto-upstream must not be annotated suggested:\n%s", footer)
		}
	}
}

func TestConfirmCancelFullyExitsTreeMode(t *testing.T) {
	leaf := &protocol.Action{ID: "branch:x/push", Argv: []string{"push", "-u", "origin", "x:x"}, Class: protocol.ClassSafe}
	tree := &protocol.DecisionTree{UnitID: "branch:x", Revision: "rev1", Root: protocol.DecisionNode{Action: leaf}}

	a := New(&fakeEngine{})
	a.Update(treeMsg{tree: tree})
	if a.confirm == nil {
		t.Fatal("test setup: a safe unblocked leaf should auto-open a confirm")
	}

	// Any key other than y/Y/enter cancels - and per the established,
	// already-shipped UX for a plain action confirm, cancelling goes back to
	// normal browsing entirely rather than back to the tree node the confirm
	// came from.
	a.onKey(keyRune('n'))
	if a.confirm != nil {
		t.Fatal("confirm should be cleared on cancel")
	}
	if a.tree != nil || a.node != nil || a.path != nil {
		t.Fatalf("cancelling must fully exit tree mode, got tree=%v node=%v path=%v", a.tree, a.node, a.path)
	}
	if a.status != "cancelled" {
		t.Fatalf("status = %q, want cancelled", a.status)
	}
}

func TestConfirmYesRunsActionAndExitsTreeMode(t *testing.T) {
	leaf := &protocol.Action{ID: "branch:x/push", Argv: []string{"push", "-u", "origin", "x:x"}, Class: protocol.ClassSafe}
	tree := &protocol.DecisionTree{UnitID: "branch:x", Revision: "rev1", Root: protocol.DecisionNode{Action: leaf}}
	eng := &fakeEngine{executeResult: &protocol.ExecuteResult{Ran: true, ExitCode: 0, Revision: "rev2"}}

	a := New(eng)
	a.snap = &protocol.Snapshot{Revision: "rev1"}
	a.Update(treeMsg{tree: tree})

	_, cmd := a.onKey(keyType(tea.KeyEnter))
	if a.tree != nil || a.node != nil {
		t.Fatal("running the action must exit tree mode - the tree is stale the moment it runs")
	}
	if cmd == nil {
		t.Fatal("expected a Cmd to actually run the action")
	}
	msg := cmd()
	done, ok := msg.(opDoneMsg)
	if !ok {
		t.Fatalf("expected opDoneMsg, got %T", msg)
	}
	if done.err != nil || !done.result.Ran {
		t.Fatalf("unexpected opDoneMsg: %+v", done)
	}
	wantConf := confirmationOf(leaf.Argv)
	if eng.executeReq.ActionID != leaf.ID || eng.executeReq.Revision != "rev1" || eng.executeReq.Confirmation != wantConf {
		t.Fatalf("Execute request = %+v", eng.executeReq)
	}
}

func TestDecideWithNoSelectionShowsNothingToDo(t *testing.T) {
	eng := &fakeEngine{}
	a := New(eng)
	a.snap = &protocol.Snapshot{Forks: []protocol.ForkPoint{{SHA: "abc", Branches: nil}}}
	a.tab, a.cursor = tabTopology, 0 // the fork header row, not a branch

	_, cmd := a.onKey(keyRune('p'))
	if cmd != nil {
		t.Fatal("no selection should never dispatch a Decide call")
	}
	if a.status != "nothing to do for this one" {
		t.Fatalf("status = %q", a.status)
	}
	if eng.decideUnitID != "" {
		t.Fatalf("Decide must not have been called, got unitID %q", eng.decideUnitID)
	}
}

func TestDecideDispatchesToTheSelectedUnitAndEmptyTreeShowsNothingToDo(t *testing.T) {
	tree := &protocol.DecisionTree{UnitID: "branch:feat/landed", Revision: "rev1", Root: protocol.DecisionNode{}}
	eng := &fakeEngine{decideTree: tree}
	a := New(eng)
	a.snap = &protocol.Snapshot{
		Units: []protocol.Unit{{ID: "branch:feat/landed", Branch: "feat/landed"}},
		Forks: []protocol.ForkPoint{{SHA: "abc", Branches: []string{"feat/landed"}}},
	}
	a.tab, a.cursor = tabTopology, 1 // row 0 = fork header, row 1 = the branch

	_, cmd := a.onKey(keyRune('p'))
	if cmd == nil {
		t.Fatal("expected a Cmd to fetch the decision tree")
	}
	msg := cmd()
	if eng.decideUnitID != "branch:feat/landed" {
		t.Fatalf("Decide called with %q, want branch:feat/landed", eng.decideUnitID)
	}
	treeM := msg.(treeMsg)
	a.Update(treeM)
	if a.status != "nothing to do for this one" {
		t.Fatalf("status = %q", a.status)
	}
	if a.tree != nil || a.node != nil {
		t.Fatal("an empty tree (Behind/Landed shape) must not enter tree-navigation mode")
	}
}

func topologySnapForOneBranch(branch string, worktreeIDs []string) *protocol.Snapshot {
	return &protocol.Snapshot{
		Revision: "rev1",
		Units:    []protocol.Unit{{ID: "branch:" + branch, Branch: branch, Worktrees: worktreeIDs}},
		Forks:    []protocol.ForkPoint{{SHA: "abc", Branches: []string{branch}}},
	}
}

func TestWtAddSendsExistingBranchRequestAndRunsOnConfirm(t *testing.T) {
	wt := &fakeWorktrees{addResult: &protocol.ExecuteResult{Ran: true, ExitCode: 0, Revision: "rev2"}}
	eng := &fakeEngine{wt: wt}
	a := New(eng)
	a.snap = topologySnapForOneBranch("feat/x", nil)
	a.tab, a.cursor = tabTopology, 1

	a.onKey(keyRune('w'))
	if a.confirm == nil || a.confirm.kind != confirmWorktreeAdd || a.confirm.unit.Branch != "feat/x" {
		t.Fatalf("expected a worktree-add confirm for feat/x, got %+v", a.confirm)
	}

	_, cmd := a.onKey(keyRune('y'))
	if a.confirm != nil {
		t.Fatal("confirm should be cleared after y")
	}
	if cmd == nil {
		t.Fatal("expected a Cmd to run the worktree add")
	}
	msg := cmd()
	done := msg.(opDoneMsg)
	if done.err != nil || !done.result.Ran {
		t.Fatalf("unexpected opDoneMsg: %+v", done)
	}
	// NewBranch must be false: the branch already exists, so this is "give
	// an existing branch its own worktree", not "create a new branch" - see
	// internal/engine/worktree_test.go's TestWorktreeAddForExistingBranchChecksOutThatBranch.
	if wt.addReq.Branch != "feat/x" || wt.addReq.NewBranch {
		t.Fatalf("Add request = %+v, want Branch=feat/x NewBranch=false", wt.addReq)
	}
	if !wt.addReq.Track || wt.addReq.Revision != "rev1" {
		t.Fatalf("Add request = %+v, want Track=true Revision=rev1", wt.addReq)
	}
}

func TestWtAddNoOpWhenAlreadyHasWorktree(t *testing.T) {
	eng := &fakeEngine{}
	a := New(eng)
	a.snap = topologySnapForOneBranch("feat/x", []string{"wt:1"})
	a.tab, a.cursor = tabTopology, 1

	a.onKey(keyRune('w'))
	if a.confirm != nil {
		t.Fatalf("expected no confirm when a worktree already exists, got %+v", a.confirm)
	}
	if a.status != "already has a worktree" {
		t.Fatalf("status = %q", a.status)
	}
}

func TestWtRemoveSendsWorktreeIDAndRevision(t *testing.T) {
	wt := &fakeWorktrees{removeResult: &protocol.ExecuteResult{Ran: true, ExitCode: 0, Revision: "rev2"}}
	eng := &fakeEngine{wt: wt}
	a := New(eng)
	a.snap = topologySnapForOneBranch("feat/x", []string{"wt:1"})
	a.snap.Worktrees = []protocol.Worktree{{ID: "wt:1", Path: "C:/tmp/wt-x"}}
	a.tab, a.cursor = tabTopology, 1

	a.onKey(keyRune('W'))
	if a.confirm == nil || a.confirm.kind != confirmWorktreeRemove || a.confirm.wt.Path != "C:/tmp/wt-x" {
		t.Fatalf("expected a worktree-remove confirm for C:/tmp/wt-x, got %+v", a.confirm)
	}

	_, cmd := a.onKey(keyType(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("expected a Cmd to run the worktree removal")
	}
	msg := cmd()
	done := msg.(opDoneMsg)
	if done.err != nil || !done.result.Ran {
		t.Fatalf("unexpected opDoneMsg: %+v", done)
	}
	if wt.removeReq.WorktreeID != "wt:1" || wt.removeReq.Revision != "rev1" {
		t.Fatalf("Remove request = %+v, want WorktreeID=wt:1 Revision=rev1", wt.removeReq)
	}
}

func TestWtRemoveRefusalIsShownAsInformationalNotAFailure(t *testing.T) {
	wt := &fakeWorktrees{removeResult: &protocol.ExecuteResult{
		Ran: false, Argv: []string{"worktree", "remove", "--force", "C:/tmp/wt-x"}, Revision: "rev1",
	}}
	eng := &fakeEngine{wt: wt}
	a := New(eng)
	a.snap = topologySnapForOneBranch("feat/x", []string{"wt:1"})
	a.snap.Worktrees = []protocol.Worktree{{ID: "wt:1", Path: "C:/tmp/wt-x"}}
	a.tab, a.cursor = tabTopology, 1

	a.onKey(keyRune('W'))
	_, cmd := a.onKey(keyRune('y'))
	msg := cmd()
	a.Update(msg)
	if !strings.Contains(a.status, "worktree remove --force C:/tmp/wt-x") {
		t.Fatalf("status = %q, want the printed --force argv (a safe refusal, not a failure)", a.status)
	}
	if strings.Contains(strings.ToLower(a.status), "failed") {
		t.Fatalf("a Ran=false worktree-remove refusal must not be reported as a failure: %q", a.status)
	}
}

func TestWtRemoveNoOpWhenNoWorktree(t *testing.T) {
	eng := &fakeEngine{}
	a := New(eng)
	a.snap = topologySnapForOneBranch("feat/x", nil)
	a.tab, a.cursor = tabTopology, 1

	a.onKey(keyRune('W'))
	if a.confirm != nil {
		t.Fatalf("expected no confirm when there is no worktree, got %+v", a.confirm)
	}
	if a.status != "no worktree for this branch" {
		t.Fatalf("status = %q", a.status)
	}
}

func TestConfirmationOfMatchesProtocolsDocumentedRecipe(t *testing.T) {
	// Recomputed independently here (protocol.ExecuteRequest.Confirmation's
	// own doc: sha256 of argv joined by \x00, hex-encoded) rather than by
	// calling confirmationOf a second time, so this checks the function
	// against the spec instead of against itself - the same discipline
	// internal/engine/engine_test.go's own Execute test uses.
	argv := []string{"push", "-u", "origin", "feat/x:feat/x"}
	sum := sha256.Sum256([]byte(strings.Join(argv, "\x00")))
	want := hex.EncodeToString(sum[:])
	if got := confirmationOf(argv); got != want {
		t.Fatalf("confirmationOf(%v) = %q, want %q", argv, got, want)
	}
}
