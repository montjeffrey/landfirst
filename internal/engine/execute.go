package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// Sentinel errors so a caller can distinguish why Execute refused to run,
// via errors.Is. The JSON-RPC code for staleness (-32010 in the protocol
// doc) is serve's problem, not this package's - serve does not exist yet;
// here these only need to be distinguishable at the Go level.
var (
	// ErrActionNotFound means no leaf in the action ID's owning unit's
	// current decision tree has this ID.
	ErrActionNotFound = errors.New("engine: no such action")
	// ErrStaleRevision means the caller's Revision does not match the
	// engine's current snapshot - something changed since they last looked.
	ErrStaleRevision = errors.New("engine: stale revision")
	// ErrConfirmationMismatch means Confirmation does not equal
	// sha256(argv joined by \x00) - the proof, per protocol.ExecuteRequest's
	// own doc, that the caller actually displayed the exact argv about to run.
	ErrConfirmationMismatch = errors.New("engine: confirmation does not match this action's argv")
	// ErrBlocked means the action's own preconditions currently fail.
	ErrBlocked = errors.New("engine: action is blocked")
)

// Execute runs exactly one leaf action, after re-deriving and re-checking
// everything that makes it safe to run: that the action still exists, that
// the caller's revision is the engine's current one (not stale), that the
// caller's confirmation proves it displayed this exact argv, and - for
// anything but ClassManual - that none of the action's preconditions
// currently fail.
func (e *engine) Execute(ctx context.Context, req protocol.ExecuteRequest) (*protocol.ExecuteResult, error) {
	unitID, ok := ownerUnit(req.ActionID)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrActionNotFound, req.ActionID)
	}
	// Re-deriving via Decide rather than trusting a caller-supplied Action is
	// the whole point: the argv that runs is the argv this engine just built
	// from current state, never one the wire carried in.
	tree, err := e.Decide(ctx, unitID)
	if err != nil {
		return nil, err
	}
	action := findAction(tree.Root, req.ActionID)
	if action == nil {
		return nil, fmt.Errorf("%w: %q", ErrActionNotFound, req.ActionID)
	}

	// Decide (above) guarantees e.snap is non-nil by the time it returns
	// successfully - it either found one already set or just set one itself.
	// Another goroutine could still have written a newer snapshot since, so
	// this re-reads rather than assumes "the snapshot Decide used" - and
	// takes one local copy so the rest of this call sees a single consistent
	// revision instead of re-touching (and re-racing on) e.snap below.
	e.mu.RLock()
	snap := e.snap
	e.mu.RUnlock()
	if snap == nil {
		// Unreachable today - Decide's guarantee above means this is never
		// nil here - but checked explicitly, with its own message, rather
		// than assumed: dereferencing snap.Revision in this branch (instead
		// of guarding it separately, as this used to) is exactly the
		// nil-pointer panic a future change to Decide could reintroduce.
		return nil, fmt.Errorf("%w: no snapshot available", ErrStaleRevision)
	}
	if req.Revision != snap.Revision {
		return nil, fmt.Errorf("%w: have %q, current is %q", ErrStaleRevision, req.Revision, snap.Revision)
	}
	if req.Confirmation != confirmationOf(action.Argv) {
		return nil, ErrConfirmationMismatch
	}

	if action.Class == protocol.ClassManual {
		// By design, not an error: rewrites history or discards work, so it
		// is printed for the operator to run themselves, never executed here.
		return &protocol.ExecuteResult{Ran: false, Argv: action.Argv, Cwd: action.Cwd, Revision: req.Revision}, nil
	}
	if len(action.Blocked) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, strings.Join(action.Blocked, "; "))
	}

	out, runErr := git.RunCtx(ctx, action.Cwd, nil, action.Argv...)
	result := &protocol.ExecuteResult{Ran: true, Argv: action.Argv, Cwd: action.Cwd, Stdout: out}
	if runErr != nil {
		result.ExitCode = git.ExitCode(runErr)
		var gerr *git.Error
		if errors.As(runErr, &gerr) {
			result.Stderr = gerr.Stderr
		}
	}
	// A failed run is still a real outcome (ExitCode/Stderr carry it) -
	// Execute's own error return is for "we could not attempt or account for
	// this", not "git declined".

	// Re-snapshot whether or not the command succeeded: even a rejected push
	// is a no-op, but a rebase/merge that started and then conflicted leaves
	// the worktree mid-operation - itself a state change (Verdict flips to
	// InProgress) the caller needs reflected. This is also what keeps
	// e.snap current for the next Execute's revision check, instead of
	// silently serving pre-action data.
	newSnap, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		return nil, err
	}
	result.Revision = newSnap.Revision
	return result, nil
}

// ownerUnit recovers the unit ID an action ID was minted from. Action IDs
// are always "<unitID>/<kind>" (protocol.Action's own doc), and a unit ID
// can itself contain "/" (a branch name can), so this splits on the last
// one, not the first.
func ownerUnit(actionID string) (string, bool) {
	i := strings.LastIndex(actionID, "/")
	if i < 0 {
		return "", false
	}
	return actionID[:i], true
}

// confirmationOf is the protocol doc's own spec for ExecuteRequest.Confirmation:
// sha256 of argv joined by NUL, hex-encoded.
func confirmationOf(argv []string) string {
	sum := sha256.Sum256([]byte(strings.Join(argv, "\x00")))
	return hex.EncodeToString(sum[:])
}
