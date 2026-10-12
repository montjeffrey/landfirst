package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/model"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// Sentinel errors for WorktreeService, matching execute.go's own style.
// ErrStaleRevision (Remove's staleness check) is deliberately not redefined
// here - it is the same concept execute.go's Execute already guards with an
// exported sentinel in this package, so Remove reuses it rather than mint a
// second name for one idea.
var (
	// ErrUnsafeWorktreePath means model.LocationRisk flagged the resolved
	// path - under the repo root, or inside a cloud-synced directory - so
	// Add refused before running anything. This is the validation that
	// stands in for Execute's confirmation hash: a typed request has no
	// per-argv hash to check, so getting this refusal right *is* the safety
	// check for this API.
	ErrUnsafeWorktreePath = errors.New("engine: unsafe worktree path")
	// ErrInvalidBranchName means `git check-ref-format --branch` rejected
	// the name before it ever reached `worktree add -b`.
	ErrInvalidBranchName = errors.New("engine: invalid branch name")
	// ErrWorktreeNotFound means no worktree in the current snapshot has the
	// requested ID.
	ErrWorktreeNotFound = errors.New("engine: no such worktree")
)

// worktreeService is the real WorktreeService, replacing stubWorktreeService.
// It holds the owning engine so it can call back into Snapshot and read the
// cached e.snap (through e.mu, the same locking pattern decide.go/execute.go
// use) the same way they do - a worktree mutation needs the identical
// re-snapshot-after treatment Execute already established, not a parallel
// copy of it.
type worktreeService struct {
	e *engine
}

// Add resolves the target path (refusing anything model.LocationRisk
// flags), validates the branch name before it ever reaches `worktree add
// -b`, creates the worktree, and - when asked to track a branch that is
// already published - sets its upstream in a second call, since `worktree
// add -b` never does that on its own.
func (w *worktreeService) Add(ctx context.Context, req protocol.WorktreeAddRequest) (*protocol.ExecuteResult, error) {
	e := w.e

	path := req.Path
	if path == "" {
		// Doc comment on protocol.WorktreeAddRequest.Path spells this default
		// out exactly: never inside root, never a cloud-sync dir - a sibling
		// ".worktrees" directory next to the repo satisfies both by construction.
		path = filepath.Join(filepath.Dir(e.repo.Root), e.repo.Name+".worktrees", req.Branch)
	}

	// git's option parser intermixes flags with positional arguments
	// regardless of where they appear in argv - a Path or StartAt of
	// "--force" is consumed as a flag, not a location, shifting every
	// argument after it out of position. Verified empirically: this silently
	// creates the worktree nested inside the repo root (defeating the
	// LocationRisk check above, which only pattern-matches the *string*, not
	// how git's parser will tokenize it) checked out at the wrong commit,
	// while still reporting exit 0. Refusing any positional value that could
	// be mistaken for a flag closes this regardless of git's own `--`
	// end-of-options handling for this subcommand, which this code does not
	// depend on or assume. req.Branch is deliberately not checked here - a
	// leading-dash branch name is check-ref-format's job below, which gives
	// the more specific ErrInvalidBranchName instead of this generic one.
	for _, v := range []string{path, req.StartAt} {
		if strings.HasPrefix(v, "-") {
			return nil, fmt.Errorf("%w: %q looks like a flag, not a value", ErrUnsafeWorktreePath, v)
		}
	}

	// git worktree add runs from the repo root (below), so it resolves a
	// relative path against the root: "nested-wt" lands inside the repo.
	// Resolve it the same way here, so the location check judges the path
	// git will actually create, and hand git that same absolute path.
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.repo.Root, path)
	}

	// Only cloud/nested are the plan's own refusal criteria ("refuse a path
	// under the repo root or under any cloud-sync directory") - "volatile"
	// (a temp dir) is informational elsewhere (collectWorktrees surfaces it
	// for display on worktrees that already exist) but not a reason to
	// refuse *creating* one, and every test fixture in this module lives
	// under a temp dir, so gating on it here would make Add unusable in
	// tests as well as unrelated to the stated risk.
	var unsafe []model.Risk
	for _, r := range model.LocationRisk(path, e.repo.Root) {
		if r.Kind == "cloud" || r.Kind == "nested" {
			unsafe = append(unsafe, r)
		}
	}
	if len(unsafe) > 0 {
		notes := make([]string, len(unsafe))
		for i, r := range unsafe {
			notes[i] = r.Note
		}
		return nil, fmt.Errorf("%w: %s (%s)", ErrUnsafeWorktreePath, path, strings.Join(notes, "; "))
	}

	// Checked here, before argv is built, instead of left for git to reject
	// inside worktree add -b: git.Try's contract (internal/git/git.go) is
	// that "" means "didn't work", the same signal every other caller in
	// this codebase already relies on.
	if git.Try(e.repo.Root, "check-ref-format", "--branch", req.Branch) == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidBranchName, req.Branch)
	}

	e.mu.RLock()
	snap := e.snap
	e.mu.RUnlock()
	if snap == nil {
		// Only BaseRef/Remote are needed below - StageFast skips contention
		// probing this call has no use for.
		if _, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast}); err != nil {
			return nil, err
		}
		// Snapshot guarantees e.snap is non-nil now, but another goroutine
		// could have written a newer one in between - use the freshest
		// value, not necessarily the one this call just caused.
		e.mu.RLock()
		snap = e.snap
		e.mu.RUnlock()
	}
	// Same staleness contract Remove and Execute already enforce - a review
	// caught that Add silently ignored this field despite every caller
	// (the TUI included) populating it expecting the protection it implies.
	if req.Revision != snap.Revision {
		return nil, fmt.Errorf("%w: have %q, current is %q", ErrStaleRevision, req.Revision, snap.Revision)
	}

	// NewBranch means req.Branch doesn't exist yet: `-b <branch>` creates it,
	// starting from StartAt (or BaseRef by default) as the third positional
	// arg. Otherwise req.Branch already exists and *is* the commit-ish to
	// check out - it belongs in the position StartAt/BaseRef occupies above,
	// not alongside it. Conflating the two used to mean the branch name never
	// reached argv at all for an existing branch, and the worktree silently
	// checked out BaseRef instead - caught in review before this shipped.
	argv := []string{"worktree", "add"}
	if req.NewBranch {
		startAt := req.StartAt
		if startAt == "" {
			startAt = snap.Repo.BaseRef
		}
		argv = append(argv, "-b", req.Branch, path, startAt)
	} else {
		argv = append(argv, path, req.Branch)
	}

	// Runs from the repo root - the target path doesn't exist yet, so there
	// is nowhere else it could run.
	out, runErr := git.RunCtx(ctx, e.repo.Root, nil, argv...)
	result := runResult(argv, e.repo.Root, out, runErr)
	if runErr != nil {
		result.Revision = snap.Revision // nothing changed
		return result, nil
	}

	if req.Track {
		if remote := snap.Repo.Remote; remote != "" {
			remoteBranch := remote + "/" + req.Branch
			if git.Try(e.repo.Root, "rev-parse", "--verify", "--quiet", remoteBranch) != "" {
				// The fix the plan calls out by name: worktree add -b never
				// decides an upstream on its own, so a tracked branch would
				// otherwise sit unpublished-looking despite already existing
				// on the remote.
				if _, err := git.RunCtx(ctx, path, nil, "branch", "--set-upstream-to="+remoteBranch); err != nil {
					return nil, fmt.Errorf("engine: worktree created but failed to set upstream to %s: %w", remoteBranch, err)
				}
			}
		}
	}

	// A client's next read must see the new worktree.
	newSnap, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		return nil, err
	}
	result.Revision = newSnap.Revision
	return result, nil
}

// Remove looks up the worktree, checks staleness the same way Execute does,
// then picks one of three fates - verbatim per the plan's Phase B spec:
// prune if the directory is already gone, actually remove it if it is clean
// and idle, or refuse and only print the --force command if there is
// anything `worktree remove` would otherwise silently discard.
func (w *worktreeService) Remove(ctx context.Context, req protocol.WorktreeRemoveRequest) (*protocol.ExecuteResult, error) {
	e := w.e
	e.mu.RLock()
	snap := e.snap
	e.mu.RUnlock()
	if snap == nil {
		if _, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull}); err != nil {
			return nil, err
		}
		// Use the freshest snapshot, not necessarily the one this call just
		// caused - another goroutine may have written a newer one since.
		e.mu.RLock()
		snap = e.snap
		e.mu.RUnlock()
	}

	wt := findWorktree(snap, req.WorktreeID)
	if wt == nil {
		return nil, fmt.Errorf("%w: %q", ErrWorktreeNotFound, req.WorktreeID)
	}
	if req.Revision != snap.Revision {
		return nil, fmt.Errorf("%w: have %q, current is %q", ErrStaleRevision, req.Revision, snap.Revision)
	}

	switch {
	case !wt.PathExists:
		return w.runAndSnapshot(ctx, []string{"worktree", "prune"})
	case wt.Dirty == (protocol.DirtyState{}) && wt.Op == "none":
		return w.runAndSnapshot(ctx, []string{"worktree", "remove", wt.Path})
	default:
		// Dirty, or an operation in progress: never silently discard work.
		// Show the command that would be needed and let the operator run it
		// by hand - the same "printed, never executed" contract ClassManual
		// leaves already carry (protocol.Action doc). No re-snapshot: nothing
		// changed.
		return &protocol.ExecuteResult{
			Ran:      false,
			Argv:     []string{"worktree", "remove", "--force", wt.Path},
			Cwd:      e.repo.Root,
			Revision: snap.Revision,
		}, nil
	}
}

// Prune is always safe - it only forgets bookkeeping for worktrees git
// already knows are gone, never touches a checkout that still exists.
func (w *worktreeService) Prune(ctx context.Context) (*protocol.ExecuteResult, error) {
	return w.runAndSnapshot(ctx, []string{"worktree", "prune"})
}

// runAndSnapshot runs a worktree-admin argv from the repo root - never from
// inside a worktree the command might be about to delete - and re-snapshots
// on success so the caller's next read reflects the mutation. Shared by
// Remove's prune/remove branches and by Prune itself: all three are "run one
// command, then the state changed" in exactly the same shape.
func (w *worktreeService) runAndSnapshot(ctx context.Context, argv []string) (*protocol.ExecuteResult, error) {
	e := w.e
	out, runErr := git.RunCtx(ctx, e.repo.Root, nil, argv...)
	result := runResult(argv, e.repo.Root, out, runErr)
	if runErr != nil {
		e.mu.RLock()
		snap := e.snap
		e.mu.RUnlock()
		if snap != nil {
			result.Revision = snap.Revision
		}
		return result, nil
	}
	snap, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		return nil, err
	}
	result.Revision = snap.Revision
	return result, nil
}

// runResult builds an ExecuteResult from a just-attempted git.RunCtx call.
// Ran is always true here: this is only for commands actually invoked - the
// same meaning execute.go's own Execute gives it. False is reserved for the
// printed-not-executed case Remove builds directly, mirroring ClassManual.
// ExitCode/Stderr are pulled from *git.Error the same way execute.go does.
func runResult(argv []string, cwd, out string, runErr error) *protocol.ExecuteResult {
	result := &protocol.ExecuteResult{Ran: true, Argv: argv, Cwd: cwd, Stdout: out}
	if runErr != nil {
		result.ExitCode = git.ExitCode(runErr)
		var gerr *git.Error
		if errors.As(runErr, &gerr) {
			result.Stderr = gerr.Stderr
		}
	}
	return result
}

func findWorktree(snap *protocol.Snapshot, id string) *protocol.Worktree {
	for i := range snap.Worktrees {
		if snap.Worktrees[i].ID == id {
			return &snap.Worktrees[i]
		}
	}
	return nil
}
