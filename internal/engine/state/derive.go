// Package state derives a branch's verdict from a single already-observed
// tuple of facts about it. Derive performs no I/O and imports nothing from
// this repo but the standard library, so it is reproducible and testable
// with plain structs - no git fixtures needed.
package state

import "time"

// Observation is everything Derive needs about one branch to produce its
// verdict. Every field is read from git exactly once upstream; Derive itself
// performs no I/O - that is what makes it reproducible and testable without
// fixtures.
type Observation struct {
	Op                 InProgressOp // the associated worktree's in-progress op, if any
	UpstreamConfigured bool         // branch.<n>.remote/merge is set - true even if the ref it names is now gone
	UpstreamGone       bool         // configured, but the tracked ref no longer exists (pruned/deleted)
	UpstreamMismatch   bool         // configured, ref exists, but isn't remote/<thisBranchName> - pushing here publishes nothing
	Ahead, Behind      int          // vs the tracked upstream; meaningless when UpstreamGone
	Unlanded           int          // git cherry: '+' lines, genuinely absent from base
	Equivalent         int          // git cherry: '-' lines, already in base under another hash
}

// InProgressOp and Verdict duplicate internal/model's types of the same
// name, deliberately: internal/model calls Derive, so this package importing
// internal/model back would be an import cycle. Both are string enums with
// identical literal values, so internal/model converts with a plain type
// conversion (model.Verdict(stateVerdict)) - safe and trivial. Do not "fix"
// this by importing internal/model; that is the cycle this duplication
// exists to avoid.
type InProgressOp string

const (
	OpNone       InProgressOp = "none"
	OpRebase     InProgressOp = "rebase"
	OpMerge      InProgressOp = "merge"
	OpCherryPick InProgressOp = "cherry-pick"
	OpRevert     InProgressOp = "revert"
	OpBisect     InProgressOp = "bisect"
)

type Verdict string

const (
	InProgress    Verdict = "in-progress"
	Orphaned      Verdict = "orphaned"
	WrongUpstream Verdict = "wrong-upstream"
	NeverPushed   Verdict = "never-pushed"
	Diverged      Verdict = "diverged"
	Unpushed      Verdict = "unpushed"
	SafeToDelete  Verdict = "safe-to-delete"
	Behind        Verdict = "behind"
	Landed        Verdict = "landed"
)

// Derive applies the precedence table below - first match wins. This is the
// state model: every branch's fate reduces to exactly one of these rows.
//
//  1. Op != OpNone                              -> InProgress   (blocks every action on this unit)
//  2. UpstreamGone && Unlanded > 0               -> Orphaned
//  3. UpstreamGone                               -> SafeToDelete
//  4. UpstreamMismatch                           -> WrongUpstream
//  5. !UpstreamConfigured && Unlanded > 0        -> NeverPushed
//  6. !UpstreamConfigured                        -> SafeToDelete
//  7. Ahead > 0 && Behind > 0                    -> Diverged
//  8. Ahead > 0                                  -> Unpushed
//  9. Behind > 0                                 -> Behind
//
// 10. else                                       -> Landed
func Derive(o Observation) Verdict {
	switch {
	case o.Op != OpNone:
		return InProgress
	case o.UpstreamGone && o.Unlanded > 0:
		return Orphaned
	case o.UpstreamGone:
		return SafeToDelete
	case o.UpstreamMismatch:
		return WrongUpstream
	case !o.UpstreamConfigured && o.Unlanded > 0:
		return NeverPushed
	case !o.UpstreamConfigured:
		return SafeToDelete
	case o.Ahead > 0 && o.Behind > 0:
		return Diverged
	case o.Ahead > 0:
		return Unpushed
	case o.Behind > 0:
		return Behind
	default:
		return Landed
	}
}

// IsStale reports whether a repo-wide fetch is old enough that ahead/behind
// numbers derived from it should carry a staleness badge - a fact about the
// repo, not about any one branch, so it's independent of Derive.
func IsStale(fetchHeadAt time.Time, threshold time.Duration) bool {
	return time.Since(fetchHeadAt) > threshold
}
