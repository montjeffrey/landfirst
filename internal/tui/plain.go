package tui

import (
	"fmt"
	"strings"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// verdictLabel mirrors internal/model.Verdicts' Label/Blurb text
// (internal/model/types.go) for the group headers below - duplicated for the
// same reason style.go's severityByVerdict is: protocol.Unit.Verdict is a
// wire string, and internal/model is behind the boundary tui no longer
// crosses.
var verdictLabel = map[string]struct{ Label, Blurb string }{
	"in-progress":    {"in progress", "a rebase, merge, cherry-pick, revert or bisect is unfinished here"},
	"orphaned":       {"orphaned", "remote branch was deleted and the changes are not in the base"},
	"wrong-upstream": {"wrong upstream", "tracks another branch, so pushing does not publish this work"},
	"never-pushed":   {"never pushed", "has changes the remote has never seen"},
	"diverged":       {"diverged", "local and remote both moved; needs a rebase or merge"},
	"unpushed":       {"unpushed work", "ahead of its remote"},
	"detached":       {"detached", "worktree is on no branch; commits here are easy to lose"},
	"safe-to-delete": {"safe to delete", "every change is already in the base"},
	"behind":         {"behind", "no local work; just out of date"},
	"landed":         {"landed", "in sync with its remote"},
}

// PlainReport is the non-interactive view: pipeable, greppable, and what CI or
// an agent would read.
func PlainReport(s *protocol.Snapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  base %s  ·  %d units  ·  %d worktrees  ·  git %s (%s)\n",
		s.Repo.Name, s.Repo.Base, len(s.Units), len(s.Worktrees), s.Repo.GitVersion, s.Repo.ConflictMode)
	if s.Summary.AtRisk > 0 {
		fmt.Fprintf(&b, "  %d change(s) across %d branch(es) exist only on this machine\n\n", s.Summary.StrandedCommits, s.Summary.AtRisk)
	} else {
		b.WriteString("  everything with local work has reached the remote\n\n")
	}

	last := ""
	for _, u := range s.Units {
		if u.Verdict != last {
			last = u.Verdict
			info := verdictLabel[u.Verdict]
			fmt.Fprintf(&b, "%s - %s\n", strings.ToUpper(info.Label), info.Blurb)
		}
		name := u.Branch
		if name == "" {
			name = "(detached " + u.Head + ")"
		}
		wt := ""
		if n := len(u.Worktrees); n > 0 {
			wt = fmt.Sprintf(" [%d worktree(s)]", n)
		}
		fmt.Fprintf(&b, "   %s %s%s\n", pad(name, 52), u.Detail, wt)
		// u.Suggested is not populated by the engine today - decision trees
		// are built per unit on demand via Decide, not attached eagerly to
		// every unit in a Snapshot (see internal/engine/engine.go's
		// toSnapshot). Print it when a future engine does start filling it
		// in; nothing to show otherwise.
		if u.Suggested != nil && len(u.Suggested.Argv) > 0 {
			fmt.Fprintf(&b, "      git %s\n", strings.Join(u.Suggested.Argv, " "))
		}
	}

	if len(s.Plan) > 0 {
		b.WriteString("\nMERGE ORDER\n")
		for _, p := range s.Plan {
			conf := "not probed"
			if p.Conflicts >= 0 {
				conf = fmt.Sprintf("%d conflicting branch(es)", p.Conflicts)
			}
			fmt.Fprintf(&b, "  %2d %s %s\n", p.Order, pad(p.Branch, 46), conf)
		}
	}
	return b.String()
}
