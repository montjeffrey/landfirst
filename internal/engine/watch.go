package engine

// watch.go is the real-time half of Watch (P1-3): a single fsnotify watcher
// over the repository's *common* git directory (never the working tree),
// debounced and coalesced into at most one "did anything actually change"
// check every 150ms, which itself only pays for a Snapshot when refs really
// moved. When fsnotify can't start at all, it falls back to polling the same
// check every 2s instead - see startFSNotify/pollLoop.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

const (
	// debounceWindow is how long notifyLoop waits after the *last* fsnotify
	// event before treating a burst as settled - the plan's own number.
	debounceWindow = 150 * time.Millisecond
	// fallbackPollInterval is how often pollLoop re-checks refsRevision when
	// fsnotify itself could not start.
	fallbackPollInterval = 2 * time.Second
)

// startFSNotify creates a real fsnotify.Watcher and adds every path in
// paths, closing the watcher again if any single Add fails - a partially-
// watched tree would silently miss changes under whatever didn't get added,
// which is worse than a clean, visible fallback to polling.
//
// A package-level func var, not a hardcoded call, so a test can substitute a
// forced-failure stand-in for "fsnotify couldn't start at all" (e.g. a
// resource limit) without needing to actually exhaust a real OS watch-handle
// limit, which is impractical to fake reliably.
var startFSNotify = func(paths []string) (*fsnotify.Watcher, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := w.Add(p); err != nil {
			w.Close()
			return nil, fmt.Errorf("engine: watching %s: %w", p, err)
		}
	}
	return w, nil
}

// Watch resolves the repository's common git directory, starts a real
// fsnotify watcher over it (falling back to polling if that fails - see
// startFSNotify/pollLoop/WatchFallback), and returns a channel of coalesced
// change events. Canceling ctx stops the background goroutine, closes the
// fsnotify watcher (if any), and closes the returned channel; nothing sends
// on it afterward (sendEvent guards the send side itself, so this holds even
// if ctx is canceled mid-tick, not just after the loop has already stopped).
func (e *engine) Watch(ctx context.Context) (<-chan protocol.ChangeEvent, error) {
	commonDir := resolveGitPath(e.repo.Root, "rev-parse", "--git-common-dir")
	if commonDir == "" {
		return nil, fmt.Errorf("engine: could not resolve the common git directory for %s", e.repo.Root)
	}
	refsRoot := filepath.Join(commonDir, "refs")

	ch := make(chan protocol.ChangeEvent)
	w, err := startFSNotify(watchPaths(commonDir, refsRoot))
	if err != nil {
		e.watchFallback.Store(true)
		go e.pollLoop(ctx, commonDir, ch)
		return ch, nil
	}
	e.watchFallback.Store(false)
	go e.notifyLoop(ctx, w, commonDir, refsRoot, ch)
	return ch, nil
}

// WatchFallback reports whether the most recently started Watch is running
// in 2s-poll fallback mode rather than event-driven fsnotify. False before
// Watch has ever been called.
func (e *engine) WatchFallback() bool { return e.watchFallback.Load() }

// resolveGitPath mirrors internal/model/collect.go's own unexported
// resolveGitPath exactly (same behavior: absolutizes a `git rev-parse`
// path-reporting subcommand's output against dir, since git reports these
// relative to dir for the main worktree but absolute for a linked one).
// Duplicated rather than exported from model for one string - the same
// tradeoff engine.go's own baseRefOf already makes and documents.
func resolveGitPath(dir string, args ...string) string {
	p := strings.TrimSpace(git.Try(dir, args...))
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p
}

// watchPaths resolves the directories to hand to fsnotify, all rooted under
// commonDir - never anything under the working tree, which is what makes
// writing to the working tree produce zero events regardless of how many
// files change there.
//
// These are all *directory* watches, deliberately, even though the plan
// frames the target as a handful of individual files (HEAD, packed-refs,
// index, FETCH_HEAD): git updates every one of those via a lockfile-then-
// rename (files-backend.c's lock_ref/commit_ref_update; the index goes
// through the same dance via index.lock, hence the well-known "Unable to
// create '.git/index.lock'" error) - and fsnotify's own doc on Watcher.Add
// explains why that matters: watching an individual file is fragile because
// a rename-over abandons the watch on the now-orphaned original inode/handle;
// it explicitly recommends watching the parent directory and filtering by
// name instead. Watching commonDir itself (non-recursive - it only reports
// changes to commonDir's own direct children, never into objects/ or the
// working tree) covers HEAD/packed-refs/index/FETCH_HEAD robustly across
// renames in one watch; the same reasoning is why each worktree's own
// private gitdir is added as a directory below, not just its HEAD file.
func watchPaths(commonDir, refsRoot string) []string {
	paths := []string{commonDir}

	_ = filepath.WalkDir(refsRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // best-effort: skip whatever couldn't be read, don't abort the whole walk
		}
		if d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})

	// Every currently-registered linked worktree gets its own private gitdir
	// watched (commonDir/worktrees/<name>/, holding that worktree's own
	// HEAD) - each subdirectory's name is arbitrary internal bookkeeping, so
	// this reads them directly rather than spawning `git worktree list` just
	// to recover the same names. A worktree added *after* Watch starts is a
	// known gap (see this file's report / doc): commonDir/worktrees itself
	// isn't watched, so a brand new subdirectory there two levels down from
	// commonDir is invisible to any watch already in place.
	if entries, err := os.ReadDir(filepath.Join(commonDir, "worktrees")); err == nil {
		for _, ent := range entries {
			if ent.IsDir() {
				paths = append(paths, filepath.Join(commonDir, "worktrees", ent.Name()))
			}
		}
	}
	return paths
}

// refsRevision hashes the on-disk state that decides whether a coalesced
// tick is worth paying for a Snapshot at all: refs/heads and refs/remotes
// (for-each-ref), plus every worktree's current HEAD sha (worktree list
// --porcelain reports it directly, including for a detached worktree, which
// for-each-ref alone would never see since a detached HEAD is not a ref
// under refs/). Two git spawns - cheap enough for every debounce tick and
// every 2s fallback poll.
func refsRevision(root string) string {
	refs := git.Try(root, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/remotes")
	worktrees := git.Try(root, "worktree", "list", "--porcelain")
	sum := sha256.Sum256([]byte(refs + "\x00" + worktrees))
	return hex.EncodeToString(sum[:])
}

// watchState is owned by exactly one goroutine (notifyLoop or pollLoop) for
// its whole lifetime, so it needs no lock of its own. This is not a second
// lock competing with engine.mu's documented "guards last/snap" contract -
// it's private data nothing else ever touches.
type watchState struct {
	lastRefsRev string
	// baseline is the last snapshot Changed/Removed get computed against -
	// rolls forward to whichever snapshot was just emitted, so each event
	// reports what changed since the *previous event*, not since watch
	// start (the alternative would be diffing every event against a single
	// fixed starting point, which is also defensible but reports strictly
	// more as the watch session goes on rather than "what's new").
	baseline *protocol.Snapshot
}

// newWatchState seeds lastRefsRev from current disk state (so the first
// real change is comparable against something) and baseline from whatever
// the engine already has cached, if anything - not a fresh Snapshot call,
// since starting a watcher shouldn't itself force a potentially-expensive
// build the caller hasn't asked for.
func (e *engine) newWatchState(root string) *watchState {
	e.mu.RLock()
	snap := e.snap
	e.mu.RUnlock()
	return &watchState{lastRefsRev: refsRevision(root), baseline: snap}
}

// notifyLoop is the event-driven path. Every fsnotify event (whatever kind)
// restarts the debounce timer; a Create under refsRoot additionally gets
// added as its own watch before the timer logic runs, so a freshly-created
// nested ref directory (a branch name containing "/") is covered before
// anything can be written inside it.
func (e *engine) notifyLoop(ctx context.Context, w *fsnotify.Watcher, root, refsRoot string, ch chan protocol.ChangeEvent) {
	defer close(ch)
	defer w.Close()

	st := e.newWatchState(root)

	var timer *time.Timer
	var timerC <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return

		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Has(fsnotify.Create) && underDir(ev.Name, refsRoot) {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = w.Add(ev.Name) // best-effort; nothing better to do if this fails
				}
			}
			// Always a fresh timer rather than Stop+Reset on the old one -
			// sidesteps the classic Timer.Reset race entirely. The old
			// timer's channel is simply abandoned: an unread, already-fired
			// timer doesn't block or leak anything, so there's nothing to
			// drain.
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounceWindow)
			timerC = timer.C

		case _, ok := <-w.Errors:
			if !ok {
				return
			}
			// Nothing actionable per-error - the debounce/coalesce logic
			// above still runs off Events regardless.

		case <-timerC:
			timerC = nil
			if !e.tick(ctx, ch, root, st) {
				return
			}
		}
	}
}

// pollLoop is the fallback path when fsnotify itself could not start - same
// coalesce-on-no-change / two-stage-emit-on-change contract as notifyLoop,
// just triggered by a fixed interval instead of real filesystem events.
func (e *engine) pollLoop(ctx context.Context, root string, ch chan protocol.ChangeEvent) {
	defer close(ch)

	st := e.newWatchState(root)
	ticker := time.NewTicker(fallbackPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !e.tick(ctx, ch, root, st) {
				return
			}
		}
	}
}

// tick recomputes refsRevision and, only if it actually changed, snapshots
// and emits the fast/full ChangeEvent pair - the coalesce point both loops
// share. Returns false if ctx was canceled while trying to send (the caller
// should stop); true otherwise, including when nothing changed, or when a
// Snapshot call itself errored (Snapshot has no real error path in this
// codebase today - engine.go's own Snapshot always returns a nil error - but
// the signature allows one, and skipping just that stage's emission is
// simpler and safer than retry/teardown logic for something that doesn't
// currently happen).
func (e *engine) tick(ctx context.Context, ch chan<- protocol.ChangeEvent, root string, st *watchState) bool {
	rev := refsRevision(root)
	if rev == st.lastRefsRev {
		return true // the exact noise this scheme exists to drop - e.g. index churn, lockfiles
	}
	st.lastRefsRev = rev

	if fast, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast}); err == nil {
		if !sendEvent(ctx, ch, changeEventFor(fast, protocol.StageFast, st.baseline)) {
			return false
		}
		st.baseline = fast
	}
	if full, err := e.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull}); err == nil {
		if !sendEvent(ctx, ch, changeEventFor(full, protocol.StageFull, st.baseline)) {
			return false
		}
		st.baseline = full
	}
	return true
}

// sendEvent selects on ctx.Done() alongside the send so a canceled watcher
// never blocks forever handing an event to a caller that stopped reading -
// the "guard the send side" half of clean shutdown; close(ch) in
// notifyLoop/pollLoop's own defer is the other half. Since exactly one
// goroutine ever sends on ch (whichever of notifyLoop/pollLoop owns it) and
// that same goroutine is the only one that ever closes it, afterward, a send
// can never race a close.
func sendEvent(ctx context.Context, ch chan<- protocol.ChangeEvent, ev protocol.ChangeEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// changeEventFor builds the ChangeEvent for snap, diffing baseline's
// Units/Worktrees (by ID) against snap's: an ID present in both whose own
// JSON representation differs goes in Changed - deliberately coarse (whole-
// struct, not a hand-picked field list) so a field added to Unit/Worktree
// later is covered automatically instead of silently falling outside the
// comparison. An ID present in baseline but missing from snap goes in
// Removed. A brand new ID (in snap only) is left out of both: protocol.
// ChangeEvent's own doc scopes Changed to *existing* things changing, and a
// new ID is already visible to any caller reading Units/Worktrees off the
// new snapshot directly.
func changeEventFor(snap *protocol.Snapshot, stage protocol.Stage, baseline *protocol.Snapshot) protocol.ChangeEvent {
	ev := protocol.ChangeEvent{Revision: snap.Revision, Stage: stage}
	if baseline == nil {
		return ev // nothing to diff against yet - first tick since the watcher started
	}
	old := fingerprints(baseline)
	cur := fingerprints(snap)
	for id, newFP := range cur {
		if oldFP, ok := old[id]; ok && oldFP != newFP {
			ev.Changed = append(ev.Changed, id)
		}
	}
	for id := range old {
		if _, ok := cur[id]; !ok {
			ev.Removed = append(ev.Removed, id)
		}
	}
	sort.Strings(ev.Changed)
	sort.Strings(ev.Removed)
	return ev
}

// fingerprints maps every unit/worktree ID in s to a hash of its own JSON
// encoding. Unit and Worktree IDs safely share one map - unitID's "branch:"/
// "detached:" prefixes and worktreeID's "wt:" prefix never collide - which
// is exactly what protocol.ChangeEvent.Changed/Removed's own doc describes:
// "unit/worktree IDs" together, not two separate lists.
func fingerprints(s *protocol.Snapshot) map[string]string {
	out := make(map[string]string, len(s.Units)+len(s.Worktrees))
	for _, u := range s.Units {
		out[u.ID] = hashOf(u)
	}
	for _, w := range s.Worktrees {
		out[w.ID] = hashOf(w)
	}
	return out
}

// hashOf mirrors engine.go's own revisionOf: marshal to JSON, sha256, hex.
// The panic on a Marshal error is unreachable for the same reason
// revisionOf's is - protocol.Unit/Worktree hold nothing that could ever put
// a channel/func/unsupported type there.
func hashOf(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("engine: value is not JSON-encodable: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// underDir reports whether path is dir itself or lies under it, comparing
// path components rather than a raw string prefix (so ".git/refs-old" for
// example is never mistaken for lying under ".git/refs").
func underDir(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}
