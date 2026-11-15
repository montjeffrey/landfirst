// Package model turns a repository into the two answers this tool exists for:
// what landed, and what to merge next.
package model

import "time"

// Verdict is how much trouble a unit of work is in. Lower Rank is worse.
type Verdict string

const (
	InProgress    Verdict = "in-progress"
	Orphaned      Verdict = "orphaned"
	WrongUpstream Verdict = "wrong-upstream"
	NeverPushed   Verdict = "never-pushed"
	Unknown       Verdict = "unknown"
	Diverged      Verdict = "diverged"
	Unpushed      Verdict = "unpushed"
	Detached      Verdict = "detached"
	SafeToDelete  Verdict = "safe-to-delete"
	Behind        Verdict = "behind"
	Landed        Verdict = "landed"
)

type VerdictInfo struct {
	Rank  int
	Label string
	Blurb string
}

// Severity buckets a verdict's rank into how urgently it needs attention.
// internal/model's AtRisk threshold and every internal/tui view that colours
// or marks a verdict (tone, glyph, verdictMark) want the same grouping - this
// used to be three independent hardcoded rank-range switches that a rank
// insertion had to keep in step by hand. One switch, one place to update.
type Severity int

const (
	SeverityBad  Severity = iota // blocks progress or risks losing work outright
	SeverityWarn                 // needs a decision, nothing lost yet
	SeverityInfo                 // routine cleanup
	SeverityDim                  // no local work, just behind
	SeverityOK                   // nothing to do
)

func (v Verdict) Severity() Severity {
	switch Verdicts[v].Rank {
	case 0, 1, 2, 3, 4: // InProgress, Orphaned, WrongUpstream, NeverPushed, Unknown
		return SeverityBad
	case 5, 6, 7: // Diverged, Unpushed, Detached
		return SeverityWarn
	case 8: // SafeToDelete
		return SeverityInfo
	case 9: // Behind
		return SeverityDim
	default: // Landed
		return SeverityOK
	}
}

// Ranks below are load-bearing beyond this map's own sort: Verdict.Severity
// keys off specific rank numbers, not just relative order. Inserting or
// reordering a verdict here means updating that too - grep the repo for
// ".Rank" and "Verdicts[".
var Verdicts = map[Verdict]VerdictInfo{
	InProgress:    {0, "in progress", "a rebase, merge, cherry-pick, revert or bisect is unfinished here"},
	Orphaned:      {1, "orphaned", "remote branch was deleted and the changes are not in the base"},
	WrongUpstream: {2, "wrong upstream", "tracks another branch, so pushing does not publish this work"},
	NeverPushed:   {3, "never pushed", "has changes the remote has never seen"},
	Unknown:       {4, "unknown", "git could not compare this branch with the base, so it is not known to be safe"},
	Diverged:      {5, "diverged", "local and remote both moved; needs a rebase or merge"},
	Unpushed:      {6, "unpushed work", "ahead of its remote"},
	Detached:      {7, "detached", "worktree is on no branch; commits here are easy to lose"},
	SafeToDelete:  {8, "safe to delete", "every change is already in the base"},
	Behind:        {9, "behind", "no local work; just out of date"},
	Landed:        {10, "landed", "in sync with its remote"},
}

// Risk describes why a worktree's location is untrustworthy.
type Risk struct {
	Kind string `json:"kind"`
	Note string `json:"note"`
}

type Worktree struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	Head     string `json:"head"`
	Detached bool   `json:"detached"`
	Locked   bool   `json:"locked"`
	Prunable bool   `json:"prunable"`
	IsMain   bool   `json:"isMain"`
	Risks    []Risk `json:"risks"`

	PathExists bool         `json:"pathExists"` // os.Stat on this machine
	Dirty      DirtyState   `json:"dirty"`
	Op         InProgressOp `json:"op"`
}

// DirtyState is a git status --porcelain=v2 summary. Counts, not a file list -
// a remedy needs to know whether a worktree is safe to touch, not what changed.
type DirtyState struct {
	Staged     int `json:"staged"`
	Unstaged   int `json:"unstaged"`
	Untracked  int `json:"untracked"`
	Conflicted int `json:"conflicted"`
}

// InProgressOp is a string enum (not int) deliberately: it serializes to
// readable JSON ("op":"rebase") and matches Verdict's convention for status
// enums.
type InProgressOp string

const (
	OpNone       InProgressOp = "none"
	OpRebase     InProgressOp = "rebase"
	OpMerge      InProgressOp = "merge"
	OpCherryPick InProgressOp = "cherry-pick"
	OpRevert     InProgressOp = "revert"
	OpBisect     InProgressOp = "bisect"
)

// Remedy is the exact command that fixes a verdict. Safe means it only adds to
// the remote; anything that rewrites local history is never Safe and is never
// offered as a one-key action.
type Remedy struct {
	Summary string   `json:"summary"`
	Args    []string `json:"args"`
	Safe    bool     `json:"safe"`

	// Dir is the worktree path this must run in; empty means the repo root.
	// Only ever set on advisory (Safe: false) remedies - run() always
	// executes Safe remedies against the repo root, so Dir is never consulted
	// for those and would be misleading to set.
	Dir string `json:"dir,omitempty"`
}

// Unit is one piece of in-flight work: a branch, or a detached worktree.
type Unit struct {
	Branch      string     `json:"branch"`
	Head        string     `json:"head"`
	HeadFull    string     `json:"headFull"` // the whole commit id Head abbreviates
	Upstream    string     `json:"upstream"`
	UpstreamRef string     `json:"upstreamRef,omitempty"` // full refname; what a merge or rebase is given
	Ahead       int        `json:"ahead"`
	Behind      int        `json:"behind"`
	Unlanded    int        `json:"unlanded"`   // patch-id aware: genuinely absent from base
	Equivalent  int        `json:"equivalent"` // already in base under another hash
	Date        time.Time  `json:"date"`
	Author      string     `json:"author"`
	Subject     string     `json:"subject"`
	Worktrees   []Worktree `json:"worktrees"`
	Risks       []Risk     `json:"risks"`
	Verdict     Verdict    `json:"verdict"`
	Detail      string     `json:"detail"`
	Stale       bool       `json:"stale"` // computed in build.go, against Freshness
	Remedy      *Remedy    `json:"remedy,omitempty"`
	Files       []string   `json:"-"`
}

// PlanItem is a branch's position in the merge order.
type PlanItem struct {
	Order     int      `json:"order"`
	Branch    string   `json:"branch"`
	Ahead     int      `json:"ahead"`
	BehindN   int      `json:"behind"`
	Conflicts int      `json:"conflicts"` // -1 means not probed
	Why       []string `json:"why"`

	// NotCompared counts the other branches in the plan this one was never
	// compared with (a different fork point, or past the -max-pairs cap), so
	// Conflicts counts only the compared ones.
	NotCompared int `json:"notCompared,omitempty"`
	// Unreadable: git could not say which files this branch changed, so it was
	// compared with nothing.
	Unreadable bool `json:"unreadable,omitempty"`
}

// Freshness is how stale this repo's view of the remote is. FetchHeadAt alone
// answers "when did we last ask" - a caller checks IsZero for "never".
type Freshness struct {
	FetchHeadAt time.Time `json:"fetchHeadAt"` // mtime of .git/FETCH_HEAD
}

type Model struct {
	Repo            string       `json:"repo"`
	Root            string       `json:"root"`
	Base            string       `json:"base"`
	Remote          string       `json:"remote"`
	GeneratedAt     time.Time    `json:"generatedAt"`
	GitVersion      string       `json:"gitVersion"`
	ConflictMode    string       `json:"conflictMode"`
	Freshness       Freshness    `json:"freshness"`
	Units           []Unit       `json:"units"`
	Worktrees       []Worktree   `json:"worktrees"`
	Plan            []PlanItem   `json:"plan"`
	Graph           *Graph       `json:"graph,omitempty"`
	Forks           []ForkPoint  `json:"forks"`
	Contentions     []Contention `json:"contentions"`
	StrandedCommits int          `json:"strandedCommits"`
	AtRisk          int          `json:"atRisk"`
	BuildMillis     int64        `json:"buildMillis"`

	// ReadErr is set when the repository could not be read (the branch list
	// failed). The rest of the model is then empty and must not be shown as if
	// the repository had no work in it.
	ReadErr error `json:"-"`

	// PairsSkipped is how many overlapping same-fork pairs were left unprobed
	// because MaxPairs was reached; 0 when the cap did not bite.
	PairsSkipped int `json:"pairsSkipped,omitempty"`
	MaxPairs     int `json:"maxPairs,omitempty"`
}
