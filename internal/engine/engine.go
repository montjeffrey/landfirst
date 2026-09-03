// Package engine is the boundary the plan calls for: everything downstream
// (the TUI, and later serve) talks to an Engine and sees only
// internal/protocol types. It never imports lipgloss, bubbletea, or canvas.
//
// This implementation is a thin, well-tested adapter over the existing
// internal/model package rather than a physical relocation of that logic.
// internal/model's derivation, worktree collection, and graph layout are
// already correct and covered by real-git fixture tests from the P0 pass -
// moving ~1500 lines of that into this package would risk regressing all of
// it for zero externally-visible benefit. What actually matters for the
// boundary the plan describes is that nothing outside this package reaches
// past protocol into model or git directly - Engine is where that happens.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/model"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// Engine is the operations a UI or a wire protocol needs from a repository.
type Engine interface {
	// Snapshot reads the tuple, derives state, and returns a revisioned model.
	Snapshot(ctx context.Context, o SnapshotOptions) (*protocol.Snapshot, error)
	// Graph lays out the commit window.
	Graph(ctx context.Context, o protocol.GraphOptions) (*protocol.Graph, error)
	// Decide returns the decision tree for one unit (branch or detached worktree).
	Decide(ctx context.Context, unitID string) (*protocol.DecisionTree, error)
	// Execute runs exactly one leaf action. Refuses unless the action's class
	// permits execution and the caller's revision matches the current one.
	Execute(ctx context.Context, req protocol.ExecuteRequest) (*protocol.ExecuteResult, error)
	// Watch emits a change event per coalesced ref/index/worktree mutation.
	// See watch.go for the implementation.
	Watch(ctx context.Context) (<-chan protocol.ChangeEvent, error)
	// WatchFallback reports whether the most recently started Watch is
	// running in 2s-poll fallback mode rather than event-driven fsnotify -
	// see watch.go's own doc for when/why this happens. A separate getter
	// rather than a third Watch return value or a field on ChangeEvent
	// (both considered - see watch.go) so every other Engine method, and
	// every existing Watch caller that only wants the channel, is
	// unaffected. False before Watch has ever been called.
	WatchFallback() bool
	Worktrees() WorktreeService
	Close() error
}

// WorktreeService is worktree add/remove/prune. Declared here because Engine
// embeds it; implemented (for now, as a stub) at the bottom of this file.
type WorktreeService interface {
	Add(ctx context.Context, req protocol.WorktreeAddRequest) (*protocol.ExecuteResult, error)
	Remove(ctx context.Context, req protocol.WorktreeRemoveRequest) (*protocol.ExecuteResult, error)
	Prune(ctx context.Context) (*protocol.ExecuteResult, error)
}

type SnapshotOptions struct {
	Stage    protocol.Stage
	MaxPairs int
}

// engine is the concrete Engine. One per opened repository.
type engine struct {
	repo *git.Repo

	// mu guards last/snap below. The TUI drives an Engine through bubbletea,
	// which runs every tea.Cmd in its own goroutine (see internal/tui/model.go's
	// fetchSnapshot/decide/runAction/runWorktreeAdd/runWorktreeRemove) - so
	// nothing stops two of Snapshot/Decide/Execute/Worktrees().Add/Remove/Prune
	// from being in flight on the same *engine at once (a double keypress is
	// enough). Every read or write of last/snap goes through mu instead of
	// touching the fields directly.
	mu sync.RWMutex
	// last is the most recently built model, kept so Decide/Execute can derive
	// a DecisionTree for a unit without re-running Build - both are driven by
	// the same snapshot revision a client already has in hand.
	last *model.Model
	// snap is the converted form of last - what Snapshot actually handed the
	// caller. Decide builds a unit's tree from this (it has the ID/Worktrees/
	// ForkSHA cross-references a tree needs; last is pre-conversion) instead
	// of reconverting or rebuilding, and Execute's revision check compares
	// against snap.Revision - the same value the caller got back from
	// Snapshot/Decide, not a freshly recomputed one.
	//
	// last/snap are only ever swapped as whole pointers, never mutated in
	// place after construction - so once a caller has copied the pointer out
	// under mu (the pattern every method below follows), it can read through
	// that local copy as many times as it needs for the rest of its call
	// without re-locking, and sees one consistent view even if another
	// goroutine swaps in a newer snapshot midway through.
	snap *protocol.Snapshot

	// watchFallback records whether the watcher started by Watch (watch.go)
	// is running in poll-fallback mode. Deliberately its own atomic instead
	// of being folded under mu above: mu's documented contract is "guards
	// last/snap", a pair that's read/written together; watchFallback is
	// unrelated state written by exactly one watcher goroutine and read by
	// anyone polling WatchFallback(), so a dedicated atomic.Bool is simpler
	// than stretching mu's contract to cover a third, unrelated field.
	watchFallback atomic.Bool
}

// Open resolves dir's repository and returns an Engine over it. Mirrors
// git.Open's own error contract (ErrNoBase/ErrNoRemote): Open never guesses.
func Open(dir string) (Engine, error) {
	repo, err := git.Open(dir)
	if err != nil {
		return nil, err
	}
	return &engine{repo: repo}, nil
}

func (e *engine) Close() error { return nil }

func (e *engine) Worktrees() WorktreeService { return &worktreeService{e: e} }

// Decide and Execute are implemented in decide.go and execute.go
// respectively; WorktreeService in worktree.go; Watch and WatchFallback in
// watch.go.

// Snapshot builds the model and converts it to the wire shape. Stage maps
// onto model.Options.Probe: StageFull additionally runs contention/merge-plan
// probing (isolated object store, see internal/model/forks.go); StageFast
// skips it. Both stages already skip the per-branch git-cherry spawn for any
// branch whose verdict doesn't need it - that fix (a P0 review finding, see
// internal/model/landing.go's judge()) already gives StageFast most of the
// spawn savings the original plan asked Stage to provide.
func (e *engine) Snapshot(ctx context.Context, o SnapshotOptions) (*protocol.Snapshot, error) {
	opt := model.Options{
		Probe:       o.Stage == protocol.StageFull,
		MaxPairs:    o.MaxPairs,
		Limit:       400,
		MaxBranches: 10,
	}
	if opt.MaxPairs == 0 {
		opt.MaxPairs = 200
	}
	// model.Build spawns real git and can run for a while - StageFull's
	// contention probing especially - so it stays outside the lock, along
	// with the pure conversion below, so a slow build never blocks a
	// concurrent Decide/Execute from reading the last-known last/snap while
	// this one is still in flight. Only the brief pointer swap needs mu.
	m := model.Build(e.repo, opt)
	s := toSnapshot(m, o.Stage)
	s.Repo.BaseRef = baseRefOf(m)

	e.mu.Lock()
	e.last = m
	e.snap = s
	e.mu.Unlock()
	return s, nil
}

// baseRefOf mirrors internal/model/landing.go's own unexported baseRef():
// the remote-tracking ref when it resolves, else the local base - "did this
// land" means "is it on the remote", not "is it in my local copy of main".
// Duplicated rather than exported from model, the same tradeoff
// internal/engine/state's own doc comment makes for a couple of small enums:
// one more git spawn per Snapshot is cheaper than opening a seam into model
// for a single string.
func baseRefOf(m *model.Model) string {
	if m.Remote == "" {
		return m.Base
	}
	ref := m.Remote + "/" + m.Base
	if git.Try(m.Root, "rev-parse", "--verify", "--quiet", ref) != "" {
		return ref
	}
	return m.Base
}

func (e *engine) Graph(ctx context.Context, o protocol.GraphOptions) (*protocol.Graph, error) {
	opt := model.Options{Limit: o.Limit, MaxBranches: o.Branches}
	if o.All {
		opt.Limit, opt.MaxBranches = -1, -1
	}
	if opt.Limit == 0 {
		opt.Limit = 400
	}
	if opt.MaxBranches == 0 {
		opt.MaxBranches = 10
	}
	// buildGraph is unexported; the cheapest correct path to it without
	// exporting model internals is a full Build. Graph() is not on any hot
	// path yet (no caller re-requests it standalone today - the TUI gets its
	// graph from Snapshot's own Build call), so the extra cost is deferred
	// until that's untrue, not paid down here speculatively.
	m := model.Build(e.repo, opt)
	return toGraph(m.Graph), nil
}

// unitID and worktreeID are the two stable-identity schemes the whole
// protocol package depends on - every cross-reference (Unit.Worktrees,
// Unit.ForkSHA lookups) is built from these, so a unit or worktree can be
// re-identified across snapshots even though nothing in model carries an ID.
func unitID(u model.Unit) string {
	if u.Branch != "" {
		return "branch:" + u.Branch
	}
	return "detached:" + u.Head
}

func worktreeID(path string) string {
	sum := sha256.Sum256([]byte(path))
	return "wt:" + hex.EncodeToString(sum[:])[:12]
}

func toSnapshot(m *model.Model, stage protocol.Stage) *protocol.Snapshot {
	wtID := make(map[string]string, len(m.Worktrees)) // path -> id, for cross-referencing from units
	worktrees := make([]protocol.Worktree, len(m.Worktrees))
	for i, w := range m.Worktrees {
		id := worktreeID(w.Path)
		wtID[w.Path] = id
		worktrees[i] = protocol.Worktree{
			ID:         id,
			Path:       w.Path,
			Branch:     w.Branch,
			Head:       w.Head,
			Detached:   w.Detached,
			IsMain:     w.IsMain,
			Locked:     w.Locked,
			Prunable:   w.Prunable,
			PathExists: w.PathExists,
			Dirty: protocol.DirtyState{
				Staged: w.Dirty.Staged, Unstaged: w.Dirty.Unstaged,
				Untracked: w.Dirty.Untracked, Conflicted: w.Dirty.Conflicted,
			},
			Op:    string(w.Op),
			Risks: toRisks(w.Risks),
		}
	}

	forkOf := make(map[string]string, len(m.Forks)) // branch -> fork SHA
	for _, fp := range m.Forks {
		for _, b := range fp.Branches {
			forkOf[b] = fp.SHA
		}
	}

	units := make([]protocol.Unit, len(m.Units))
	for i, u := range m.Units {
		var wts []string
		for _, w := range u.Worktrees {
			wts = append(wts, wtID[w.Path])
		}
		units[i] = protocol.Unit{
			ID: unitID(u), Branch: u.Branch, Head: u.Head, Upstream: u.Upstream,
			Ahead: u.Ahead, Behind: u.Behind, Unlanded: u.Unlanded, Equivalent: u.Equivalent,
			Verdict: string(u.Verdict), Stale: u.Stale, Detail: u.Detail,
			ForkSHA: forkOf[u.Branch], Worktrees: wts, Risks: toRisks(u.Risks),
			Tip: protocol.CommitMeta{Short: u.Head, Subject: u.Subject, Author: u.Author, Date: u.Date},
		}
	}

	forks := make([]protocol.ForkPoint, len(m.Forks))
	for i, fp := range m.Forks {
		forks[i] = protocol.ForkPoint{SHA: fp.SHA, Short: fp.Short, Subject: fp.Subject, Behind: fp.Behind, Branches: fp.Branches}
	}
	contentions := make([]protocol.Contention, len(m.Contentions))
	for i, c := range m.Contentions {
		contentions[i] = protocol.Contention{A: c.A, B: c.B, Real: c.Real, Files: c.Files, Structural: c.Structural, SameFork: c.SameFork}
	}
	plan := make([]protocol.PlanItem, len(m.Plan))
	for i, p := range m.Plan {
		plan[i] = protocol.PlanItem{Order: p.Order, Branch: p.Branch, Ahead: p.Ahead, Behind: p.BehindN, Conflicts: p.Conflicts, Why: p.Why}
	}

	s := &protocol.Snapshot{
		Stage: stage,
		Repo: protocol.RepoInfo{
			Root: m.Root, Name: m.Repo, Base: m.Base, Remote: m.Remote,
			GitVersion: m.GitVersion, ConflictMode: m.ConflictMode,
		},
		Freshness:   protocol.Freshness{FetchHeadAt: m.Freshness.FetchHeadAt},
		Units:       units,
		Worktrees:   worktrees,
		Forks:       forks,
		Contentions: contentions,
		Plan:        plan,
		Summary:     protocol.Summary{StrandedCommits: m.StrandedCommits, AtRisk: m.AtRisk},
		BuildMillis: m.BuildMillis,
	}
	s.Revision = revisionOf(s)
	return s
}

// revisionOf hashes the client-visible, derived parts of a snapshot - not the
// input tuple the plan's own design describes (that belongs to the watcher's
// incremental-recompute work, P1-3, deferred), but it delivers the same
// contract a client actually needs: this string changes if and only if
// something the client would see differently changed. BuildMillis is the one
// field deliberately excluded - it is real, but it is timing noise, not state.
func revisionOf(s *protocol.Snapshot) string {
	stable := struct {
		Repo        protocol.RepoInfo
		Freshness   protocol.Freshness
		Units       []protocol.Unit
		Worktrees   []protocol.Worktree
		Forks       []protocol.ForkPoint
		Contentions []protocol.Contention
		Plan        []protocol.PlanItem
	}{s.Repo, s.Freshness, s.Units, s.Worktrees, s.Forks, s.Contentions, s.Plan}
	b, err := json.Marshal(stable)
	if err != nil {
		// json.Marshal only fails on channels/funcs/unsupported types, none of
		// which appear in these struct definitions - unreachable in practice.
		panic(fmt.Sprintf("engine: snapshot is not JSON-encodable: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func toRisks(rs []model.Risk) []protocol.Risk {
	if rs == nil {
		return nil
	}
	out := make([]protocol.Risk, len(rs))
	for i, r := range rs {
		out[i] = protocol.Risk{Kind: r.Kind, Note: r.Note}
	}
	return out
}

func toGraph(g *model.Graph) *protocol.Graph {
	if g == nil {
		return &protocol.Graph{}
	}
	commits := make([]protocol.Commit, len(g.Commits))
	for i, c := range g.Commits {
		commits[i] = protocol.Commit{
			Hash: c.Hash, Short: c.Short, Parents: c.Parents, Refs: c.Refs,
			Subject: c.Subject, Author: c.Author, Date: c.Date,
			Row: c.Row, Lane: c.Lane, Color: c.Color,
		}
	}
	edges := make([]protocol.Edge, len(g.Edges))
	for i, e := range g.Edges {
		edges[i] = protocol.Edge{FromRow: e.FromRow, FromLane: e.FromLane, ToRow: e.ToRow, ToLane: e.ToLane, Color: e.Color, Stub: e.Stub}
	}
	return &protocol.Graph{
		Commits: commits, Edges: edges, LaneCount: g.LaneCount,
		Total: g.Total, Truncated: g.Truncated, Drawn: g.Drawn, Hidden: g.Hidden,
	}
}
