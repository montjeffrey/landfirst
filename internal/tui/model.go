package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/engine"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

type tab int

const (
	tabTopology tab = iota
	tabGraph
	tabCount
)

var tabNames = []string{"topology", "graph"}

// App is the whole panel state. It talks only to an engine.Engine and sees
// only protocol types - never internal/model or internal/git directly (see
// internal/tui/boundary_test.go).
type App struct {
	eng   engine.Engine
	snap  *protocol.Snapshot
	graph *protocol.Graph

	// watchCtx/watchCancel bound the background Watch listener (started in
	// Init, alongside the manual "r" refresh - see startWatch/listenForChanges)
	// to this App's own lifetime: canceled wherever a quit key is handled, so
	// the watcher goroutine, its fsnotify handle, and this channel all stop
	// cleanly on exit instead of outliving the bubbletea program. watchCh is
	// the channel Watch handed back, kept so the re-arming listenForChanges
	// Cmd (see the changeEventMsg case in Update) always reads from the same
	// channel rather than needing to thread it through every message.
	watchCtx    context.Context
	watchCancel context.CancelFunc
	watchCh     <-chan protocol.ChangeEvent

	tab      tab
	density  Density
	graphOpt protocol.GraphOptions // from -limit/-branches/-all
	snapOpt  engine.SnapshotOptions
	cursor   int
	top      int // first visible row, for vertical scrolling
	panX     int // first visible canvas column, for horizontal scrolling
	w, h     int

	status  string
	loading bool

	// Decision-tree navigation for the currently selected unit. node == nil
	// means "not currently drilling into a tree" - normal browsing. path is
	// the breadcrumb stack: path[0] is always &tree.Root, and path's last
	// element always equals node - esc pops path and node moves to the new
	// last element, until only the root is left, at which point esc exits
	// tree mode entirely instead of popping further.
	tree *protocol.DecisionTree
	node *protocol.DecisionNode
	path []*protocol.DecisionNode

	confirm *pendingConfirm // an action or worktree op awaiting y/n

	quitting bool
}

// confirmKind says which of pendingConfirm's payload fields is set, so
// onKey's y/n handling can stay one shared code path (the swallow-every-key
// discipline) instead of three copies of it.
type confirmKind int

const (
	confirmAction confirmKind = iota
	confirmWorktreeAdd
	confirmWorktreeRemove
)

// pendingConfirm is an action or worktree op awaiting y/n. Exactly one of
// action/unit/wt is set, per kind.
type pendingConfirm struct {
	kind   confirmKind
	action *protocol.Action   // confirmAction: the leaf about to run
	unit   *protocol.Unit     // confirmWorktreeAdd: the branch getting a new worktree
	wt     *protocol.Worktree // confirmWorktreeRemove: the worktree about to go
}

// prompt is the question shown before "[y] yes   [any other key] no".
func (p *pendingConfirm) prompt() string {
	switch p.kind {
	case confirmWorktreeAdd:
		return fmt.Sprintf("create a worktree for %s at the default location?", p.unit.Branch)
	case confirmWorktreeRemove:
		return fmt.Sprintf("remove the worktree at %s?", p.wt.Path)
	default:
		return fmt.Sprintf("run  git %s   ?", strings.Join(p.action.Argv, " "))
	}
}

// ---- messages ----
//
// Snapshot and Graph are two independent Engine calls now, not one
// model.Build() - each gets its own message so rebuild() can fetch them
// concurrently via tea.Batch instead of serially.

type snapshotMsg struct {
	snap *protocol.Snapshot
	err  error
}

type graphMsg struct {
	graph *protocol.Graph
	err   error
}

// treeMsg is a Decide result - entering tree-navigation mode once it lands.
type treeMsg struct {
	tree *protocol.DecisionTree
	err  error
}

// opDoneMsg is the result of an Execute or a worktree Add/Remove - one
// shared message, since all three just need a status line and whether the
// repo actually changed.
type opDoneMsg struct {
	label  string
	result *protocol.ExecuteResult
	err    error
}

// watchStartedMsg is the result of the one-time eng.Watch call - err is
// non-nil only if Watch itself refused to start (e.g. the common git
// directory couldn't be resolved), which is distinct from "started, but in
// poll-fallback mode" (that's WatchFallback(), checked where it matters).
type watchStartedMsg struct {
	ch  <-chan protocol.ChangeEvent
	err error
}

// changeEventMsg carries one protocol.ChangeEvent off the watch channel, or
// ok=false when the channel has closed (the watcher stopped - context
// canceled, or Watch was never started) and there is nothing left to listen
// to.
type changeEventMsg struct {
	ev protocol.ChangeEvent
	ok bool
}

func New(eng engine.Engine) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		eng:         eng,
		density:     Comfortable,
		loading:     true,
		status:      "reading repository…",
		snapOpt:     engine.SnapshotOptions{Stage: protocol.StageFull},
		watchCtx:    ctx,
		watchCancel: cancel,
	}
}

// SetDensity chooses vertical spacing before the program starts.
func (a *App) SetDensity(d Density) { a.density = d }

// SetGraphOptions sets the -limit/-branches/-all draw window - same timing
// contract as SetDensity, called before the program starts.
func (a *App) SetGraphOptions(o protocol.GraphOptions) { a.graphOpt = o }

// SetSnapshotOptions sets the -no-probe/-max-pairs derived Stage/MaxPairs
// used for every Snapshot fetch this App makes.
func (a *App) SetSnapshotOptions(o engine.SnapshotOptions) { a.snapOpt = o }

// ParseDensity maps the flag value. Unknown values fall back to comfortable
// rather than failing: this is a display preference, not a correctness one.
func ParseDensity(s string) Density {
	switch s {
	case "compact":
		return Compact
	case "spacious":
		return Spacious
	}
	return Comfortable
}

// Init kicks off the initial rebuild and, alongside it, starts listening for
// live change events - a background enhancement, never a replacement for
// the manual "r" refresh (see startWatch's own doc).
func (a *App) Init() tea.Cmd { return tea.Batch(a.rebuild(), a.startWatch()) }

// rebuild re-fetches Snapshot and Graph concurrently - independent Engine
// calls, so there is no reason to make one wait on the other.
func (a *App) rebuild() tea.Cmd {
	return tea.Batch(a.fetchSnapshot(), a.fetchGraph())
}

// startWatch begins listening for live change events. Live updates are a
// background enhancement on top of the manual "r" refresh, not a
// replacement for it - internal/engine/watch.go's own fallback story (poll
// every 2s when it can't get a real filesystem watcher) is the same bet:
// this works fine without ever landing too. A Cmd, per bubbletea's own
// discipline (see rebuild/fetchSnapshot above): it only computes a message,
// it never touches App fields directly - all mutation happens in Update.
func (a *App) startWatch() tea.Cmd {
	return func() tea.Msg {
		ch, err := a.eng.Watch(a.watchCtx)
		return watchStartedMsg{ch: ch, err: err}
	}
}

// listenForChanges reads exactly one event off ch and returns it as a
// message - the standard bubbletea "re-arm after every read" idiom: the
// changeEventMsg case in Update calls this again each time, so listening
// keeps going instead of firing once.
func listenForChanges(ch <-chan protocol.ChangeEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		return changeEventMsg{ev: ev, ok: ok}
	}
}

func (a *App) fetchSnapshot() tea.Cmd {
	return func() tea.Msg {
		snap, err := a.eng.Snapshot(context.Background(), a.snapOpt)
		return snapshotMsg{snap: snap, err: err}
	}
}

func (a *App) fetchGraph() tea.Cmd {
	return func() tea.Msg {
		g, err := a.eng.Graph(context.Background(), a.graphOpt)
		return graphMsg{graph: g, err: err}
	}
}

// rows is what the current tab shows, so cursor movement and rendering always
// agree on the length.
func (a *App) rowCount() int {
	switch a.tab {
	case tabTopology:
		if a.snap == nil {
			return 0
		}
		return len(a.topologyRows())
	case tabGraph:
		if a.graph == nil {
			return 0
		}
		return len(a.graph.Commits)
	}
	return 0
}

// selectedUnit resolves the topology row under the cursor to its unit, or
// false if the cursor is on a fork/spine/divider row, off a unit entirely, or
// the panel isn't on Topology - Graph has no "selected unit" concept. Both
// rowBranch and rowDetached carry a real unit; every other kind is structural
// (a fork header, a spine filler, the detached-section divider) and has none.
func (a *App) selectedUnit() (*protocol.Unit, bool) {
	if a.snap == nil || a.tab != tabTopology {
		return nil, false
	}
	rows := a.topologyRows()
	if a.cursor < 0 || a.cursor >= len(rows) {
		return nil, false
	}
	r := rows[a.cursor]
	if r.kind != rowBranch && r.kind != rowDetached {
		return nil, false
	}
	u := r.unit
	return &u, true
}

// unitWorktree resolves the single worktree checked out on u, if any - the
// same cross-reference internal/engine/decide.go's decideCtx.unitWorktree
// makes, duplicated here because tui only ever sees the wire Snapshot, not
// the engine's own internals.
func unitWorktree(u *protocol.Unit, snap *protocol.Snapshot) (*protocol.Worktree, bool) {
	if len(u.Worktrees) == 0 {
		return nil, false
	}
	id := u.Worktrees[0]
	for i := range snap.Worktrees {
		if snap.Worktrees[i].ID == id {
			return &snap.Worktrees[i], true
		}
	}
	return nil, false
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.w, a.h = msg.Width, msg.Height
		return a, nil

	case snapshotMsg:
		a.loading = false
		if msg.err != nil {
			a.status = "failed: " + msg.err.Error()
			return a, nil
		}
		a.snap = msg.snap
		fetched := "never fetched"
		if t := msg.snap.Freshness.FetchHeadAt; !t.IsZero() {
			fetched = "fetched " + ago(t) + " ago"
		}
		a.status = fmt.Sprintf("read in %dms · git %s · conflict probing: %s · %s",
			msg.snap.BuildMillis, msg.snap.Repo.GitVersion, msg.snap.Repo.ConflictMode, fetched)
		if a.eng.WatchFallback() {
			// Durable, not transient like the "updated: fast/full" status
			// below - this stays visible on every refresh for as long as
			// fallback polling is active, which is the actual "say so in the
			// status line" ask for this mode.
			a.status += " · watch: poll fallback"
		}
		if a.cursor >= a.rowCount() {
			a.cursor = maxInt(0, a.rowCount()-1)
		}
		return a, nil

	case graphMsg:
		if msg.err != nil {
			// Graph is secondary to Snapshot for status purposes - don't
			// clobber a Snapshot-derived status with this one unless nothing
			// has loaded at all yet.
			if a.snap == nil {
				a.status = "failed: " + msg.err.Error()
			}
			return a, nil
		}
		a.graph = msg.graph
		if a.tab == tabGraph && a.cursor >= a.rowCount() {
			a.cursor = maxInt(0, a.rowCount()-1)
		}
		return a, nil

	case treeMsg:
		a.loading = false
		if msg.err != nil {
			a.status = "failed: " + msg.err.Error()
			return a, nil
		}
		root := msg.tree.Root
		if root.Question == "" && root.Action == nil {
			// The Behind/Landed empty-tree case (decide.go's own doc: no
			// case builds anything for either) - matches today's exact
			// message for the equivalent case.
			a.status = "nothing to do for this one"
			return a, nil
		}
		a.tree = msg.tree
		a.path = []*protocol.DecisionNode{&a.tree.Root}
		a.status = ""
		a.enterNode(&a.tree.Root)
		return a, nil

	case opDoneMsg:
		a.loading = false
		if msg.err != nil {
			a.status = "failed: " + msg.err.Error()
			return a, nil
		}
		r := msg.result
		switch {
		case !r.Ran:
			// Printed, not executed - a manual-class leaf's own contract, and
			// also how Worktrees().Remove reports a safe refusal (dirty or
			// mid-op). Not a failure: show the command for the operator to
			// run by hand.
			a.status = msg.label + " — needs a manual decision — do it by hand: git " + strings.Join(r.Argv, " ")
			return a, nil
		case r.ExitCode != 0:
			a.status = "failed: git " + strings.Join(r.Argv, " ") + ": " + strings.TrimSpace(r.Stderr)
			return a, nil
		default:
			a.status = msg.label + " — done"
			a.loading = true
			return a, a.rebuild()
		}

	case watchStartedMsg:
		if msg.err != nil {
			// Not fatal - live updates are a background enhancement (see
			// startWatch's own doc), so this stays silent rather than
			// clobbering whatever the initial rebuild's status says. The
			// manual "r" refresh keeps working either way.
			return a, nil
		}
		a.watchCh = msg.ch
		return a, listenForChanges(a.watchCh)

	case changeEventMsg:
		if !msg.ok {
			return a, nil // the watcher stopped (context canceled, or it never started) - nothing left to listen to
		}
		// StageFull re-fetches Snapshot and Graph (rebuild, same as "r");
		// StageFast only re-fetches Snapshot - Graph's own doc (engine.go)
		// notes it always re-runs a full model.Build with no Stage of its
		// own, so re-deriving it on every fast tick would pay full build
		// cost far more often than the topology actually needs redrawing.
		if msg.ev.Stage == protocol.StageFull {
			a.status = "updated: full"
			return a, tea.Batch(a.rebuild(), listenForChanges(a.watchCh))
		}
		a.status = "updated: fast"
		return a, tea.Batch(a.fetchSnapshot(), listenForChanges(a.watchCh))

	case tea.KeyMsg:
		return a.onKey(msg)
	}
	return a, nil
}

// quit is every quit key's shared path: stop the background watch listener
// (canceling watchCtx stops watch.go's goroutine, which closes the fsnotify
// watcher and the channel on its own) before handing control back to
// bubbletea, so nothing outlives the program.
func (a *App) quit() (tea.Model, tea.Cmd) {
	a.quitting = true
	a.watchCancel()
	return a, tea.Quit
}

func (a *App) onKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A confirmation swallows every key except the answer, so an action can
	// never be triggered by a keystroke meant for the list (or the tree)
	// underneath. Shared by all three pendingConfirm kinds.
	if a.confirm != nil {
		switch msg.String() {
		case "y", "Y", "enter":
			p := a.confirm
			a.confirm = nil
			switch p.kind {
			case confirmWorktreeAdd:
				a.status = "creating worktree for " + p.unit.Branch + " …"
				return a, a.runWorktreeAdd(p.unit)
			case confirmWorktreeRemove:
				a.status = "removing worktree at " + p.wt.Path + " …"
				return a, a.runWorktreeRemove(p.wt)
			default:
				a.status = "running git " + strings.Join(p.action.Argv, " ") + " …"
				// The tree this leaf came from is now stale the moment it
				// runs - exit tree mode rather than leave a menu pointing at
				// a decision that no longer reflects post-action state.
				a.tree, a.node, a.path = nil, nil, nil
				return a, a.runAction(p.action)
			}
		default:
			a.confirm = nil
			a.status = "cancelled"
			// Matches the pre-existing behavior for a plain action confirm:
			// cancelling goes back to normal browsing entirely, not to the
			// tree node the confirm came from.
			a.tree, a.node, a.path = nil, nil, nil
			return a, nil
		}
	}

	// Tree-navigation mode owns the keyboard except for the universal quit
	// keys, until the operator backs all the way out.
	if a.node != nil {
		s := msg.String()
		if s == "q" || s == "ctrl+c" {
			return a.quit()
		}
		if a.node.Action != nil {
			// A leaf shown as text (manual info, or a blocked reason) - an
			// executable leaf would already have a.confirm set and be
			// handled above. Nothing to navigate back through from a leaf's
			// own text, so any key - esc included - dismisses straight to
			// normal browsing.
			a.tree, a.node, a.path = nil, nil, nil
			a.status = ""
			return a, nil
		}
		if s == "esc" {
			return a, a.backOut()
		}
		return a.onTreeKey(msg)
	}

	switch msg.String() {
	case "q", "ctrl+c", "esc":
		return a.quit()
	case "tab":
		a.tab = (a.tab + 1) % tabCount
		a.cursor, a.top, a.panX = 0, 0, 0
	case "shift+tab":
		a.tab = (a.tab + tabCount - 1) % tabCount
		a.cursor, a.top, a.panX = 0, 0, 0
	case "1":
		a.tab, a.cursor, a.top = tabTopology, 0, 0
	case "2":
		a.tab, a.cursor, a.top = tabGraph, 0, 0
	case "h", "left":
		// Pan the canvas rather than changing tab: at full topology the graph
		// is far wider than any dock, so horizontal movement is the common
		// need and tab-switching has the number keys.
		if a.tab == tabGraph {
			a.panX = maxInt(0, a.panX-4)
		} else {
			a.tab = (a.tab + tabCount - 1) % tabCount
			a.cursor, a.top = 0, 0
		}
	case "l", "right":
		if a.tab == tabGraph {
			a.panX += 4
		} else {
			a.tab = (a.tab + 1) % tabCount
			a.cursor, a.top = 0, 0
		}
	case "H":
		a.panX = 0
	case "j", "down":
		if a.cursor < a.rowCount()-1 {
			a.cursor++
		}
	case "k", "up":
		if a.cursor > 0 {
			a.cursor--
		}
	case "g", "home":
		a.cursor = 0
	case "G", "end":
		a.cursor = maxInt(0, a.rowCount()-1)
	case "d":
		// Cycle how much vertical room each commit gets. A docked panel is
		// short, so this is the trade between smooth curves and visible history.
		a.density = (a.density + 1) % 3
		a.status = []string{"density: compact", "density: comfortable", "density: spacious"}[a.density]
	case "r":
		a.loading = true
		a.status = "refreshing…"
		return a, a.rebuild()
	case "p", "enter":
		return a, a.decide()
	case "w":
		a.wtAdd()
	case "W":
		a.wtRemove()
	}
	return a, nil
}

// onTreeKey is only reached for a question node (Action == nil) - leaf
// dismissal and the quit/esc keys are handled by onKey before this.
func (a *App) onTreeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return a, nil
	}
	idx := int(s[0] - '1')
	if idx >= len(a.node.Options) {
		return a, nil
	}
	next := &a.node.Options[idx].Next
	a.path = append(a.path, next)
	a.enterNode(next)
	return a, nil
}

// enterNode moves tree-navigation onto node. A question just becomes the new
// current node; an executable leaf (not manual, nothing blocking it) opens
// the same y/n confirm bar a plain action always has, immediately - no extra
// keypress to "enter" a leaf that is ready to run.
func (a *App) enterNode(node *protocol.DecisionNode) {
	a.node = node
	if node.Action == nil {
		return
	}
	action := node.Action
	if action.Class != protocol.ClassManual && len(action.Blocked) == 0 {
		a.confirm = &pendingConfirm{kind: confirmAction, action: action}
	}
}

// backOut pops one level of tree navigation (esc on a question node), or
// fully exits tree mode when already at the root.
func (a *App) backOut() tea.Cmd {
	if len(a.path) <= 1 {
		a.tree, a.node, a.path = nil, nil, nil
		a.status = ""
		return nil
	}
	a.path = a.path[:len(a.path)-1]
	a.node = a.path[len(a.path)-1]
	return nil
}

// decide starts a Decide call for the selected unit - Topology tab only.
// Entering tree-navigation mode happens once treeMsg lands, not here.
func (a *App) decide() tea.Cmd {
	u, ok := a.selectedUnit()
	if !ok {
		a.status = "nothing to do for this one"
		return nil
	}
	unitID := u.ID
	a.loading = true
	a.status = "deciding…"
	return func() tea.Msg {
		tree, err := a.eng.Decide(context.Background(), unitID)
		return treeMsg{tree: tree, err: err}
	}
}

// wtAdd stages a confirm for creating a worktree for the selected unit's
// branch. A no-op (with a status explaining why) if there's no selection or
// the unit already has one.
func (a *App) wtAdd() {
	u, ok := a.selectedUnit()
	if !ok {
		a.status = "nothing to do for this one"
		return
	}
	if len(u.Worktrees) > 0 {
		a.status = "already has a worktree"
		return
	}
	a.confirm = &pendingConfirm{kind: confirmWorktreeAdd, unit: u}
}

// wtRemove stages a confirm for removing the selected unit's worktree. A
// no-op if there's no selection or it has none.
func (a *App) wtRemove() {
	u, ok := a.selectedUnit()
	if !ok {
		a.status = "nothing to do for this one"
		return
	}
	wt, ok := unitWorktree(u, a.snap)
	if !ok {
		a.status = "no worktree for this branch"
		return
	}
	a.confirm = &pendingConfirm{kind: confirmWorktreeRemove, wt: wt}
}

func (a *App) runAction(action *protocol.Action) tea.Cmd {
	return func() tea.Msg {
		res, err := a.eng.Execute(context.Background(), protocol.ExecuteRequest{
			ActionID:     action.ID,
			Revision:     a.snap.Revision,
			Confirmation: confirmationOf(action.Argv),
		})
		return opDoneMsg{label: "git " + strings.Join(action.Argv, " "), result: res, err: err}
	}
}

func (a *App) runWorktreeAdd(u *protocol.Unit) tea.Cmd {
	return func() tea.Msg {
		res, err := a.eng.Worktrees().Add(context.Background(), protocol.WorktreeAddRequest{
			Branch:    u.Branch,
			NewBranch: false, // the branch already exists - give it its own worktree, don't create a new one
			Track:     true,
			Revision:  a.snap.Revision,
		})
		return opDoneMsg{label: "worktree for " + u.Branch, result: res, err: err}
	}
}

func (a *App) runWorktreeRemove(wt *protocol.Worktree) tea.Cmd {
	return func() tea.Msg {
		res, err := a.eng.Worktrees().Remove(context.Background(), protocol.WorktreeRemoveRequest{
			WorktreeID: wt.ID,
			Revision:   a.snap.Revision,
		})
		return opDoneMsg{label: "remove worktree at " + wt.Path, result: res, err: err}
	}
}

// confirmationOf mirrors internal/engine/execute.go's own unexported
// confirmationOf exactly (protocol.ExecuteRequest.Confirmation's documented
// contract: sha256 of argv joined by \x00, hex-encoded) - copied rather than
// exported, since a UI computing its own confirmation from the same argv it
// displays is the whole point of the hash (proof the caller actually showed
// this exact command), not something the engine should hand it pre-computed.
func confirmationOf(argv []string) string {
	sum := sha256.Sum256([]byte(strings.Join(argv, "\x00")))
	return hex.EncodeToString(sum[:])
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}
