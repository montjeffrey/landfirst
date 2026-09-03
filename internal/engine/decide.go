package engine

import (
	"context"
	"fmt"

	"github.com/montjeffrey/landfirst/internal/model"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// Precondition text is shared verbatim between Action.Preconditions and
// Action.Blocked so a client can match one against the other by string
// equality instead of parsing prose.
const (
	preNoOpInProgress = "no rebase/merge/cherry-pick in progress"
	preClean          = "worktree has no uncommitted changes"
	preWorktreeExists = "worktree directory exists on disk"
)

// preBaseCheckedOut is merge-into-base's own extra precondition: unlike the
// three above, it names the base branch, so it can't be a package-level
// const and is built per-repo instead. Still shared verbatim between
// Preconditions and Blocked, same contract as the others.
func preBaseCheckedOut(base string) string {
	return base + " is checked out in the main worktree"
}

// Decide builds the decision tree for one unit from the most recent
// snapshot - e.snap if Snapshot has already run, else a fresh StageFull one.
// It never rebuilds a snapshot that is already in hand: a decision tree is
// "for the unit I'm already looking at", so recomputing here would defeat
// the point of the cache.
func (e *engine) Decide(ctx context.Context, unitID string) (*protocol.DecisionTree, error) {
	e.mu.RLock()
	snap := e.snap
	e.mu.RUnlock()
	if snap == nil {
		if _, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull}); err != nil {
			return nil, err
		}
		// Snapshot guarantees e.snap is non-nil now, but another goroutine
		// could have written a newer one in between - re-read rather than
		// assume the pointer we just caused to be written is still current,
		// so this call uses the freshest snapshot, not "the one I caused".
		e.mu.RLock()
		snap = e.snap
		e.mu.RUnlock()
	}

	unit := findUnit(snap, unitID)
	if unit == nil {
		return nil, fmt.Errorf("engine: no unit %q in snapshot revision %s", unitID, snap.Revision)
	}

	dc := &decideCtx{snap: snap, unit: unit}
	tree := &protocol.DecisionTree{UnitID: unitID, Revision: snap.Revision}

	// One case per model.Verdict - see internal/model/landing.go's remedy()
	// for the precedent this replaces (one command per verdict); this
	// mirrors that same verdict switch but grows a tree per verdict, not a
	// single command.
	switch unit.Verdict {
	case string(model.InProgress):
		tree.Root, tree.Suggested = dc.inProgress()
	case string(model.Orphaned):
		tree.Root, tree.Suggested = dc.orphaned()
	case string(model.WrongUpstream):
		tree.Root, tree.Suggested = dc.wrongUpstream()
	case string(model.NeverPushed):
		tree.Root, tree.Suggested = dc.neverPushed()
	case string(model.Unpushed):
		tree.Root, tree.Suggested = dc.unpushed()
	case string(model.SafeToDelete):
		tree.Root, tree.Suggested = dc.safeToDelete()
	case string(model.Detached):
		tree.Root, tree.Suggested = dc.detached()
	case string(model.Diverged):
		tree.Root, tree.Suggested = dc.diverged()
	case string(model.Behind), string(model.Landed):
		// Nothing to suggest for either - Root stays the zero DecisionNode,
		// matching that the old Remedy system never offered anything here.
	default:
		return nil, fmt.Errorf("engine: unit %q has unrecognized verdict %q", unitID, unit.Verdict)
	}
	return tree, nil
}

// decideCtx carries the read-only inputs every verdict builder below needs,
// so each one reads as the tree it builds instead of a parameter list.
type decideCtx struct {
	snap *protocol.Snapshot
	unit *protocol.Unit
}

// act builds one leaf Action. ID follows protocol.Action's own documented
// scheme: "<unitID>/<kind>".
func (dc *decideCtx) act(kind, label string, argv []string, cwd string, class protocol.ActionClass) *protocol.Action {
	return &protocol.Action{
		ID:    dc.unit.ID + "/" + kind,
		Kind:  kind,
		Label: label,
		Argv:  argv,
		Cwd:   cwd,
		Class: class,
	}
}

// pushAction builds a ClassSafe leaf that runs against the repo root. Push
// (and branch -d, see safeToDelete) touch only the ref database, never a
// worktree's checkout, so - unlike every leaf attachWorktree covers - they
// carry no Preconditions: there is nothing about worktree state that governs
// whether they are safe to run.
func (dc *decideCtx) pushAction(kind, label string, argv []string) *protocol.Action {
	return dc.act(kind, label, argv, dc.snap.Repo.Root, protocol.ClassSafe)
}

// noRemoteAction is the informational leaf substituted for a push whenever
// the repo has no remote configured - see neverPushed/unpushed.
func (dc *decideCtx) noRemoteAction() *protocol.Action {
	return dc.act("no-remote", "no remote configured - add one before publishing this", nil, "", protocol.ClassManual)
}

// unitWorktree resolves the single worktree checked out on this unit, if
// any. A branch has at most one worktree checked out (git enforces that),
// and a detached unit's own Worktrees slice always holds exactly the one
// worktree it came from - so index 0 is always the right (and only)
// candidate, never a pick among several.
func (dc *decideCtx) unitWorktree() (protocol.Worktree, bool) {
	if len(dc.unit.Worktrees) == 0 {
		return protocol.Worktree{}, false
	}
	id := dc.unit.Worktrees[0]
	for i := range dc.snap.Worktrees {
		if dc.snap.Worktrees[i].ID == id {
			return dc.snap.Worktrees[i], true
		}
	}
	return protocol.Worktree{}, false
}

// mainWorktree finds the repo's main worktree - every repo has exactly one,
// always first in `git worktree list` (see internal/model/collect.go), so
// !ok here should not happen structurally; it is still handled rather than
// assumed.
func (dc *decideCtx) mainWorktree() (protocol.Worktree, bool) {
	for i := range dc.snap.Worktrees {
		if dc.snap.Worktrees[i].IsMain {
			return dc.snap.Worktrees[i], true
		}
	}
	return protocol.Worktree{}, false
}

// attachWorktree fills in Cwd/Preconditions/Blocked for a leaf that runs
// inside a specific worktree's checkout - anything that moves HEAD there
// (switch/rebase/merge). Every such leaf needs the same three preconditions:
// no rebase/merge/cherry-pick already running there, nothing uncommitted to
// clobber, and a directory that still exists to run git in at all. Filled in
// even for a ClassManual leaf (for display), though Blocked is moot there -
// Execute never reaches the Blocked check for a manual action.
func (dc *decideCtx) attachWorktree(a *protocol.Action, wt protocol.Worktree, ok bool) {
	a.Preconditions = []string{preNoOpInProgress, preClean, preWorktreeExists}
	if !ok {
		// Don't default Cwd to the repo root: e.g. running switch -c there
		// would create the branch at the *wrong* commit when the repo root
		// isn't this unit's own checkout. Leave Cwd empty and block instead.
		a.Blocked = append(a.Blocked, "no worktree checked out for this branch")
		return
	}
	a.Cwd = wt.Path
	a.Blocked = blockedFor(wt)
}

// blockedFor reports which of a worktree-based leaf's preconditions
// currently fail, read straight from the worktree's own snapshot state.
func blockedFor(wt protocol.Worktree) []string {
	if !wt.PathExists {
		// Nothing past this point is checkable in a directory that isn't there.
		return []string{preWorktreeExists}
	}
	var blocked []string
	if wt.Op != "none" {
		blocked = append(blocked, preNoOpInProgress)
	}
	if wt.Dirty.Staged > 0 || wt.Dirty.Unstaged > 0 || wt.Dirty.Untracked > 0 || wt.Dirty.Conflicted > 0 {
		blocked = append(blocked, preClean)
	}
	return blocked
}

// inProgress: no real action is possible - nothing is safe to suggest
// without knowing whether the operator wants to continue or abort the stuck
// operation. Mirrors why remedy() in landing.go deliberately has no case for
// InProgress either.
func (dc *decideCtx) inProgress() (protocol.DecisionNode, string) {
	wt, ok := dc.unitWorktree()
	op, cwd := "operation", ""
	if ok {
		cwd = wt.Path
		if wt.Op != "" && wt.Op != "none" {
			op = wt.Op
		}
	}
	a := dc.act("resolve-in-progress", "finish or abort the "+op+" in progress first", nil, cwd, protocol.ClassManual)
	return protocol.DecisionNode{Action: a}, ""
}

// orphaned, wrongUpstream, neverPushed and unpushed all share the same
// no-remote guard: git.Open leaves Remote == "" for a legitimate local-only
// repo rather than guessing one (internal/git/open.go's ErrNoRemote doc).
// Building a push with an empty remote is exactly the bug fixed earlier this
// session in landing.go's remedy() (TestNoRemoteConfiguredOffersNoAutoPushRemedy)
// - offer the informational leaf instead of reintroducing it at this layer.
// A prior version of this file only guarded the latter two; Orphaned and
// WrongUpstream are rarer paths to Remote == "" (an upstream configured
// against a remote later removed) but genuinely reachable, and a review
// caught that gap before it shipped.

func (dc *decideCtx) orphaned() (protocol.DecisionNode, string) {
	if dc.snap.Repo.Remote == "" {
		return protocol.DecisionNode{Action: dc.noRemoteAction()}, ""
	}
	branch := dc.unit.Branch
	a := dc.pushAction("push", "publish it again before the work is lost",
		[]string{"push", "-u", dc.snap.Repo.Remote, branch + ":" + branch})
	return protocol.DecisionNode{Action: a}, a.ID
}

func (dc *decideCtx) wrongUpstream() (protocol.DecisionNode, string) {
	if dc.snap.Repo.Remote == "" {
		return protocol.DecisionNode{Action: dc.noRemoteAction()}, ""
	}
	remote, branch := dc.snap.Repo.Remote, dc.unit.Branch
	a := dc.pushAction("push", "point it at "+remote+"/"+branch+" and publish",
		[]string{"push", "-u", remote, branch + ":" + branch})
	return protocol.DecisionNode{Action: a}, a.ID
}

func (dc *decideCtx) neverPushed() (protocol.DecisionNode, string) {
	if dc.snap.Repo.Remote == "" {
		return protocol.DecisionNode{Action: dc.noRemoteAction()}, ""
	}
	branch := dc.unit.Branch
	a := dc.pushAction("push", "publish for the first time",
		[]string{"push", "-u", dc.snap.Repo.Remote, branch + ":" + branch})
	return protocol.DecisionNode{Action: a}, a.ID
}

func (dc *decideCtx) unpushed() (protocol.DecisionNode, string) {
	if dc.snap.Repo.Remote == "" {
		return protocol.DecisionNode{Action: dc.noRemoteAction()}, ""
	}
	branch := dc.unit.Branch
	a := dc.pushAction("push", "push the outstanding commits",
		[]string{"push", dc.snap.Repo.Remote, branch + ":" + branch})
	return protocol.DecisionNode{Action: a}, a.ID
}

func (dc *decideCtx) safeToDelete() (protocol.DecisionNode, string) {
	a := dc.act("delete-branch", "already in "+dc.snap.Repo.Base+"; delete the local branch",
		[]string{"branch", "-d", dc.unit.Branch}, dc.snap.Repo.Root, protocol.ClassSafe)
	return protocol.DecisionNode{Action: a}, a.ID
}

// detached is now ClassGuarded rather than the advisory-only fix from
// earlier this session - that fix existed because the old Remedy had no
// per-action Cwd, so an auto-run against the repo root's HEAD could create
// the branch at the wrong commit. protocol.Action.Cwd exists specifically to
// fix that here: run in the unit's own worktree, or block rather than guess.
func (dc *decideCtx) detached() (protocol.DecisionNode, string) {
	a := dc.act("branch-from-detached", "give this work a branch before it is lost",
		[]string{"switch", "-c", "wip/" + dc.unit.Head}, "", protocol.ClassGuarded)
	wt, ok := dc.unitWorktree()
	dc.attachWorktree(a, wt, ok)
	return protocol.DecisionNode{Action: a}, a.ID
}

// diverged is the plan's own worked example: both sides moved, so there is
// no single obvious fix - the operator picks a direction, and "land it now"
// branches again on how. Implemented close to verbatim, down to the third
// answer's "I want this on main now" - every other templated value in the
// spec uses <angle-bracket> placeholders, but that phrase names "main"
// outright even where <base> would fit, so it is kept as fixed UI copy
// (a colloquial "get it on main") rather than substituted with repo.Base.
func (dc *decideCtx) diverged() (protocol.DecisionNode, string) {
	repo := dc.snap.Repo
	upstream, branch, base := dc.unit.Upstream, dc.unit.Branch, repo.Base

	wt, wtOK := dc.unitWorktree()
	main, mainOK := dc.mainWorktree()

	// Rewrites local history - never auto-run, printed only.
	rebaseUpstream := dc.act("rebase-onto-upstream", "rebase onto "+upstream,
		[]string{"rebase", upstream}, "", protocol.ClassManual)
	dc.attachWorktree(rebaseUpstream, wt, wtOK)

	mergeUpstream := dc.act("merge-upstream", "merge "+upstream+" in",
		[]string{"merge", "--no-edit", upstream}, "", protocol.ClassGuarded)
	dc.attachWorktree(mergeUpstream, wt, wtOK)

	// Rewrites local history - never auto-run, printed only.
	rebaseBase := dc.act("rebase-onto-base", "rebase onto "+base+" then push",
		[]string{"rebase", repo.BaseRef}, "", protocol.ClassManual)
	dc.attachWorktree(rebaseBase, wt, wtOK)

	// Merging *into* base has to happen where base is checked out - the main
	// worktree, not this branch's own - so this leaf's preconditions are
	// read from main, not wt.
	mergeBase := dc.act("merge-into-base", "merge into "+base,
		[]string{"merge", "--no-ff", branch}, "", protocol.ClassGuarded)
	dc.attachWorktree(mergeBase, main, mainOK)
	// attachWorktree only confirms *some* worktree is clean/idle/exists at
	// main's path - it never checks that worktree's checked-out branch is
	// actually base. An operator can have anything checked out in the repo
	// root; without this, the leaf would run `git merge --no-ff <branch>`
	// against whatever's actually there and report success while silently
	// merging into the wrong target. mainOK is checked here too: when it's
	// false, attachWorktree already blocked with "no worktree checked out"
	// and main is the zero value, so checking its (nonexistent) Branch would
	// only add a second, redundant Blocked entry.
	basePre := preBaseCheckedOut(base)
	mergeBase.Preconditions = append(mergeBase.Preconditions, basePre)
	if mainOK && main.Branch != base {
		mergeBase.Blocked = append(mergeBase.Blocked, basePre)
	}

	root := protocol.DecisionNode{
		Question: upstream + " and local both moved. Keep local history linear?",
		Options: []protocol.DecisionEdge{
			{Answer: "yes, rebase onto " + upstream, Next: protocol.DecisionNode{Action: rebaseUpstream}},
			{Answer: "no, merge " + upstream + " in", Next: protocol.DecisionNode{Action: mergeUpstream}},
			{Answer: "I want this on main now", Next: protocol.DecisionNode{
				Question: "How should it land?",
				Options: []protocol.DecisionEdge{
					{Answer: "rebase onto " + base + " then push", Next: protocol.DecisionNode{Action: rebaseBase}},
					{Answer: "merge into " + base, Next: protocol.DecisionNode{Action: mergeBase}},
				},
			}},
		},
	}
	// merge-upstream is the suggested default: of the four leaves it is the
	// only one that is both non-destructive (Guarded, not Manual - never
	// rewrites history) and keeps the operator's own work exactly where it
	// is (unlike the "land it now" pair, which moves this branch's commits
	// onto base). A cautious default prefers the leaf that forecloses the
	// fewest options, not the one that finishes the job fastest.
	return root, mergeUpstream.ID
}

func findUnit(snap *protocol.Snapshot, unitID string) *protocol.Unit {
	for i := range snap.Units {
		if snap.Units[i].ID == unitID {
			return &snap.Units[i]
		}
	}
	return nil
}

// walkActions visits every leaf Action reachable from node, depth-first.
// Shared by Execute's own lookup and the safety tests, so neither has to
// hand-enumerate a tree's branches.
func walkActions(node protocol.DecisionNode, fn func(*protocol.Action)) {
	if node.Action != nil {
		fn(node.Action)
		return
	}
	for _, opt := range node.Options {
		walkActions(opt.Next, fn)
	}
}

// findAction returns the leaf in root whose Action.ID == id, or nil.
func findAction(root protocol.DecisionNode, id string) *protocol.Action {
	var found *protocol.Action
	walkActions(root, func(a *protocol.Action) {
		if a.ID == id {
			found = a
		}
	})
	return found
}
