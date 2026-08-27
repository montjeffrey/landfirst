// Package model turns a repository into the two answers this tool exists for:
// what landed, and what to merge next.
package model

import "time"

// Verdict is how much trouble a unit of work is in. Lower Rank is worse.
type Verdict string

const (
	Orphaned      Verdict = "orphaned"
	WrongUpstream Verdict = "wrong-upstream"
	NeverPushed   Verdict = "never-pushed"
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

var Verdicts = map[Verdict]VerdictInfo{
	Orphaned:      {0, "orphaned", "remote branch was deleted and the changes are not in the base"},
	WrongUpstream: {1, "wrong upstream", "tracks another branch, so pushing does not publish this work"},
	NeverPushed:   {2, "never pushed", "has changes the remote has never seen"},
	Diverged:      {3, "diverged", "local and remote both moved; needs a rebase or merge"},
	Unpushed:      {4, "unpushed work", "ahead of its remote"},
	Detached:      {5, "detached", "worktree is on no branch; commits here are easy to lose"},
	SafeToDelete:  {6, "safe to delete", "every change is already in the base"},
	Behind:        {7, "behind", "no local work; just out of date"},
	Landed:        {8, "landed", "in sync with its remote"},
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
}

// Remedy is the exact command that fixes a verdict. Safe means it only adds to
// the remote; anything that rewrites local history is never Safe and is never
// offered as a one-key action.
type Remedy struct {
	Summary string   `json:"summary"`
	Args    []string `json:"args"`
	Safe    bool     `json:"safe"`
}

// Unit is one piece of in-flight work: a branch, or a detached worktree.
type Unit struct {
	Branch     string     `json:"branch"`
	Head       string     `json:"head"`
	Upstream   string     `json:"upstream"`
	Ahead      int        `json:"ahead"`
	Behind     int        `json:"behind"`
	Unlanded   int        `json:"unlanded"`   // patch-id aware: genuinely absent from base
	Equivalent int        `json:"equivalent"` // already in base under another hash
	Date       time.Time  `json:"date"`
	Author     string     `json:"author"`
	Subject    string     `json:"subject"`
	Worktrees  []Worktree `json:"worktrees"`
	Risks      []Risk     `json:"risks"`
	Verdict    Verdict    `json:"verdict"`
	Detail     string     `json:"detail"`
	Remedy     *Remedy    `json:"remedy,omitempty"`
	Files      []string   `json:"-"`
}

// PlanItem is a branch's position in the merge order.
type PlanItem struct {
	Order     int      `json:"order"`
	Branch    string   `json:"branch"`
	Ahead     int      `json:"ahead"`
	BehindN   int      `json:"behind"`
	Conflicts int      `json:"conflicts"` // -1 means not probed
	Why       []string `json:"why"`
}

type Collision struct {
	A         string   `json:"a"`
	B         string   `json:"b"`
	Shared    int      `json:"shared"`
	Conflicts []string `json:"conflicts"`
	Unknown   bool     `json:"unknown"`
}

type Model struct {
	Repo            string      `json:"repo"`
	Root            string      `json:"root"`
	Base            string      `json:"base"`
	Remote          string      `json:"remote"`
	GeneratedAt     time.Time   `json:"generatedAt"`
	GitVersion      string      `json:"gitVersion"`
	ConflictMode    string      `json:"conflictMode"`
	Units           []Unit      `json:"units"`
	Worktrees       []Worktree  `json:"worktrees"`
	Plan            []PlanItem  `json:"plan"`
	Collisions      []Collision `json:"collisions"`
	Graph           *Graph       `json:"graph,omitempty"`
	Forks           []ForkPoint  `json:"forks"`
	Contentions     []Contention `json:"contentions"`
	StrandedCommits int         `json:"strandedCommits"`
	AtRisk          int         `json:"atRisk"`
	BuildMillis     int64       `json:"buildMillis"`
}
