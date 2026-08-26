package tui

import (
	"fmt"
	"strings"

	"github.com/montjeffrey/landfirst/internal/model"
)

// PlainReport is the non-interactive view: pipeable, greppable, and what CI or
// an agent would read.
func PlainReport(m *model.Model) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s  base %s  ·  %d units  ·  %d worktrees  ·  git %s (%s)\n",
		m.Repo, m.Base, len(m.Units), len(m.Worktrees), m.GitVersion, m.ConflictMode)
	if m.AtRisk > 0 {
		fmt.Fprintf(&b, "  %d change(s) across %d branch(es) exist only on this machine\n\n", m.StrandedCommits, m.AtRisk)
	} else {
		b.WriteString("  everything with local work has reached the remote\n\n")
	}

	last := model.Verdict("")
	for _, u := range m.Units {
		if u.Verdict != last {
			last = u.Verdict
			info := model.Verdicts[u.Verdict]
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
		if u.Remedy != nil && len(u.Remedy.Args) > 0 {
			fmt.Fprintf(&b, "      git %s\n", strings.Join(u.Remedy.Args, " "))
		}
	}

	if len(m.Plan) > 0 {
		b.WriteString("\nMERGE ORDER\n")
		for _, p := range m.Plan {
			conf := "not probed"
			if p.Conflicts >= 0 {
				conf = fmt.Sprintf("%d conflicting branch(es)", p.Conflicts)
			}
			fmt.Fprintf(&b, "  %2d %s %s\n", p.Order, pad(p.Branch, 46), conf)
		}
	}
	return b.String()
}
