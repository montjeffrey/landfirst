// Package protocol is the wire format between the engine and every adapter -
// the TUI, the plain report, and (later) serve. It is the only thing an
// adapter imports; engine internals (git, model) stay behind it. Nothing here
// does I/O - these are data shapes, stamped with a revision so a client can
// tell whether what it is looking at is still current.
package protocol

import "time"

// Stage controls how much a Snapshot cost to build. StageFast skips cherry
// comparisons and contention probing; StageFull runs them.
type Stage string

const (
	StageFast Stage = "fast"
	StageFull Stage = "full"
)

// Snapshot is the whole state of a repository at one revision.
type Snapshot struct {
	Revision    string       `json:"revision"` // changes iff derived state changed
	Stage       Stage        `json:"stage"`
	Repo        RepoInfo     `json:"repo"`
	Freshness   Freshness    `json:"freshness"`
	Units       []Unit       `json:"units"`
	Worktrees   []Worktree   `json:"worktrees"`
	Forks       []ForkPoint  `json:"forks"`
	Contentions []Contention `json:"contentions"`
	Plan        []PlanItem   `json:"plan"` // suggested merge order; a hint, not a surface
	Summary     Summary      `json:"summary"`
	BuildMillis int64        `json:"buildMillis"`
}

type RepoInfo struct {
	Root         string `json:"root"`
	Name         string `json:"name"`
	Base         string `json:"base"`
	BaseRef      string `json:"baseRef"` // remote/base when it exists, else base
	Remote       string `json:"remote"`  // "" for a legitimate local-only repo
	GitVersion   string `json:"gitVersion"`
	ConflictMode string `json:"conflictMode"`
}

// Freshness is how stale this repo's view of the remote is. A caller checks
// FetchHeadAt.IsZero() for "never fetched".
type Freshness struct {
	FetchHeadAt time.Time `json:"fetchHeadAt"`
}

type Summary struct {
	StrandedCommits int `json:"strandedCommits"`
	AtRisk          int `json:"atRisk"`
}

type Risk struct {
	Kind string `json:"kind"`
	Note string `json:"note"`
}

type DirtyState struct {
	Staged     int `json:"staged"`
	Unstaged   int `json:"unstaged"`
	Untracked  int `json:"untracked"`
	Conflicted int `json:"conflicted"`
}

// CommitMeta is the sliver of commit data a Unit carries for display - not
// the full Commit (that lives in Graph), just enough for a one-line summary.
type CommitMeta struct {
	Short   string    `json:"short"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
}

// Unit is one piece of in-flight work: a branch, or a detached worktree. ID
// is stable across snapshots so a client can track "the same unit" through a
// revision change - "branch:<name>" or "detached:<sha>".
type Unit struct {
	ID         string     `json:"id"`
	Branch     string     `json:"branch,omitempty"`
	Head       string     `json:"head"`
	Upstream   string     `json:"upstream,omitempty"`
	Ahead      int        `json:"ahead"`
	Behind     int        `json:"behind"`
	Unlanded   int        `json:"unlanded"`
	Equivalent int        `json:"equivalent"`
	Verdict    string     `json:"verdict"`
	Stale      bool       `json:"stale"`
	Detail     string     `json:"detail"`
	ForkSHA    string     `json:"forkSha,omitempty"`
	Worktrees  []string   `json:"worktrees"` // worktree IDs, see Worktree.ID
	Risks      []Risk     `json:"risks"`
	Suggested  *Action    `json:"suggested,omitempty"` // the engine's own pick from this unit's DecisionTree
	Tip        CommitMeta `json:"tip"`
}

type Worktree struct {
	ID         string     `json:"id"` // "wt:" + a stable hash of Path
	Path       string     `json:"path"`
	Branch     string     `json:"branch,omitempty"`
	Head       string     `json:"head"`
	Detached   bool       `json:"detached"`
	IsMain     bool       `json:"isMain"`
	Locked     bool       `json:"locked"`
	Prunable   bool       `json:"prunable"`
	PathExists bool       `json:"pathExists"`
	Dirty      DirtyState `json:"dirty"`
	Op         string     `json:"op"` // "none"|"rebase"|"merge"|"cherry-pick"|"revert"|"bisect"
	Risks      []Risk     `json:"risks"`
}

type ForkPoint struct {
	SHA      string   `json:"sha"`
	Short    string   `json:"short"`
	Subject  string   `json:"subject"`
	Behind   int      `json:"behind"`
	Branches []string `json:"branches"`
}

type Contention struct {
	A          string   `json:"a"`
	B          string   `json:"b"`
	Real       int      `json:"real"`
	Files      []string `json:"files"`
	Structural int      `json:"structural"`
	SameFork   bool     `json:"sameFork"`
}

// PlanItem is a branch's position in the suggested merge order - folded into
// the decision tree as "suggested next" per §2.3; kept here as the ordering
// data a client can render as a hint, not as its own surface.
type PlanItem struct {
	Order     int      `json:"order"`
	Branch    string   `json:"branch"`
	Ahead     int      `json:"ahead"`
	Behind    int      `json:"behind"`
	Conflicts int      `json:"conflicts"` // -1 means not probed
	Why       []string `json:"why"`
}

// ---- graph ----

type GraphOptions struct {
	Limit    int  `json:"limit,omitempty"`
	Branches int  `json:"branches,omitempty"`
	All      bool `json:"all,omitempty"`
}

type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Parents []string  `json:"parents"`
	Refs    []string  `json:"refs"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Row     int       `json:"row"`
	Lane    int       `json:"lane"`
	Color   int       `json:"color"`
}

// Edge connects a commit to a parent. Stub means the parent lies outside the
// drawn window, so the line ends rather than reaching anything.
type Edge struct {
	FromRow  int  `json:"fromRow"`
	FromLane int  `json:"fromLane"`
	ToRow    int  `json:"toRow"`
	ToLane   int  `json:"toLane"`
	Color    int  `json:"color"`
	Stub     bool `json:"stub"`
}

// Graph is the laid-out commit window. Edges are included here (unlike the
// model package's own json:"-") because a remote client has no other way to
// draw the topology - only the in-process TUI can fall back to recomputing.
type Graph struct {
	Commits   []Commit `json:"commits"`
	Edges     []Edge   `json:"edges"`
	LaneCount int      `json:"laneCount"`
	Total     int      `json:"totalCommits"`
	Truncated bool     `json:"truncatedCommits"`
	Drawn     []string `json:"drawnBranches"`
	Hidden    int      `json:"hiddenBranches"`
}

// ---- decision trees (§2.3) - replace the old single-command Remedy ----

// ActionClass is the safety contract. TestRemedyIsNeverUnsafeForAutomaticActions's
// rule generalises directly: no ClassSafe leaf's Argv[0] is outside
// {push, worktree, branch, switch}, and none carries a force/-D/reset flag.
type ActionClass string

const (
	ClassSafe    ActionClass = "safe"    // adds to the remote or creates refs/worktrees; one-key after confirm
	ClassGuarded ActionClass = "guarded" // mutates local refs without rewriting history
	ClassManual  ActionClass = "manual"  // rewrites history or discards work; printed, never executed
)

// Action is one leaf: the exact command that runs if this is where the
// operator's answers lead. Argv is what is shown and exactly what executes -
// they can never drift apart, because there is nowhere else for the argv to
// come from.
type Action struct {
	ID            string      `json:"id"` // stable: "<unitID>/<kind>"
	Kind          string      `json:"kind"`
	Label         string      `json:"label"`
	Argv          []string    `json:"argv"`
	Cwd           string      `json:"cwd"` // repo root or a worktree path
	Class         ActionClass `json:"class"`
	Preconditions []string    `json:"preconditions"`     // human-readable; all must be empty for Class != manual to execute
	Blocked       []string    `json:"blocked,omitempty"` // which preconditions currently fail
}

// DecisionNode is either a question (Options populated, Action nil) or a leaf
// (Action populated, Options nil).
type DecisionNode struct {
	Question string         `json:"question,omitempty"`
	Options  []DecisionEdge `json:"options,omitempty"`
	Action   *Action        `json:"action,omitempty"`
}

type DecisionEdge struct {
	Answer string       `json:"answer"`
	Next   DecisionNode `json:"next"`
}

type DecisionTree struct {
	UnitID    string       `json:"unitId"`
	Revision  string       `json:"revision"`
	Suggested string       `json:"suggested"` // an Action ID the engine would pick, "" if none
	Root      DecisionNode `json:"root"`
}

// ---- execute ----

type ExecuteRequest struct {
	ActionID     string `json:"actionId"`
	Revision     string `json:"revision"`     // must equal the snapshot's current revision, else stale
	Confirmation string `json:"confirmation"` // sha256 of the action's own argv, joined by \x00 - proves the caller displayed the exact argv
}

type ExecuteResult struct {
	Ran      bool     `json:"ran"` // false for ClassManual: printed, not executed
	Argv     []string `json:"argv"`
	Cwd      string   `json:"cwd"`
	ExitCode int      `json:"exitCode"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	Revision string   `json:"revision"` // post-action
}

// ---- worktrees ----

type WorktreeAddRequest struct {
	Branch    string `json:"branch"`
	NewBranch bool   `json:"newBranch"` // -b
	StartAt   string `json:"startAt"`   // "" means the repo's BaseRef
	Path      string `json:"path"`      // "" means the engine's own default, never inside root or a cloud-sync dir
	Track     bool   `json:"track"`     // set upstream immediately when the branch already exists on the remote
	Revision  string `json:"revision"`
}

type WorktreeRemoveRequest struct {
	WorktreeID string `json:"worktreeId"`
	Revision   string `json:"revision"`
	// No Force field, on purpose: a dirty tree is ClassManual and is printed,
	// never silently discarded by a flag a caller could set once and forget.
}

// ---- change stream (P1-3; Watch is a stub until the watcher lands) ----

type ChangeEvent struct {
	Revision string   `json:"revision"`
	Stage    Stage    `json:"stage"`
	Changed  []string `json:"changed"` // unit/worktree IDs whose derived state differs from the previous revision
	Removed  []string `json:"removed"`
}
