package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// The topology view.
//
// A commit graph answers "what happened, in what order". The question this tool
// exists for is different: where does each branch land, and what does it fight
// with. Those are not the same picture, and drawing the second as a prettier
// version of the first is why it kept feeling like git log with nicer glyphs.
//
// Here the base runs down the page and every branch attaches at the commit it
// actually forked from. Distance down the spine is staleness, so branches that
// left together sit together and stale forks fall away below. Contention is
// drawn only between branches sharing a fork point, because only those are
// genuinely fighting - a pair from different eras conflicts on hundreds of
// files simply because one carries months of unrelated history.
func (a *App) viewTopology(w, body int) string {
	snap := a.snap
	// Checked against rows, not snap.Forks directly: a repo whose only
	// in-flight work is detached (no fork points at all, see topologyRows'
	// own detached section below) still has something to show.
	rows := a.topologyRows()
	if len(rows) == 0 {
		return styDim.Render("  every branch is merged or in sync - nothing in flight") + "\n"
	}
	var b strings.Builder

	// Contention lookup, strongest first per branch.
	type peer struct {
		other string
		real  int
		files []string
	}
	peers := map[string][]peer{}
	for _, c := range snap.Contentions {
		peers[c.A] = append(peers[c.A], peer{c.B, c.Real, c.Files})
		peers[c.B] = append(peers[c.B], peer{c.A, c.Real, c.Files})
	}

	for i := a.top; i < len(rows) && i < a.top+body; i++ {
		r := rows[i]
		switch r.kind {
		case rowFork:
			label := "HEAD"
			if r.fork.Behind > 0 {
				label = fmt.Sprintf("%d commits back", r.fork.Behind)
			}
			dot := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("●")
			line := "   " + dot + styFaint.Render("── ") +
				lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render(label) +
				styFaint.Render("  "+r.fork.Short+"  "+clip(r.fork.Subject, maxInt(10, w-46)))
			if n := len(r.fork.Branches); n > 1 {
				line += styFaint.Render(fmt.Sprintf("   %d branches share this fork", n))
			}
			b.WriteString(line + "\n")

		case rowSpine:
			b.WriteString("   " + styFaint.Render("│") + "\n")

		case rowBranch:
			u := r.unit
			elbow := "├"
			if r.last {
				elbow = "╰"
			}
			mark, tint := verdictMark(u.Verdict)
			// Bar length is sqrt-scaled: a 195-commit branch should read as
			// bigger than a 6-commit one without being 30x the width.
			bar := strings.Repeat("━", clampInt(isqrt(u.Unlanded)*2, 0, 22))
			if u.Unlanded > 0 {
				bar += "▸"
			}
			name := clip(u.Branch, 38)

			// Path clipped aggressively to keep the row from wrapping: the
			// fixed prefix (elbow/mark/name/bar/count) already runs to about
			// 78 cells, so what's left goes mostly to the worktree column,
			// minus room for the dirty/op badges that can follow the path.
			pathW := clampInt(w-78-30, 8, 40)
			wtText, wtWarn := worktreeText(&u, snap, pathW)
			wtStyle := styFaint
			if wtWarn {
				wtStyle = lipgloss.NewStyle().Foreground(cWarn)
			}

			line := "   " + styFaint.Render("│  ") + styFaint.Render(elbow) +
				lipgloss.NewStyle().Foreground(tint).Render(mark+" ") +
				styText.Render(pad(name, 39)) +
				lipgloss.NewStyle().Foreground(tint).Render(pad(bar, 24)) +
				styFaint.Render(fmt.Sprintf("+%-4d ", u.Unlanded)) +
				wtStyle.Render(wtText)

			if i == a.cursor {
				plain := fmt.Sprintf("   │  %s%s %s%s+%-4d %s", elbow, mark, pad(name, 39), pad(bar, 24), u.Unlanded, wtText)
				line = lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(pad(plain, w-1))
			}
			b.WriteString(line + "\n")

			// Strongest contention for this branch, inline underneath.
			if ps := peers[u.Branch]; len(ps) > 0 {
				top := ps[0]
				for _, p := range ps {
					if p.real > top.real {
						top = p
					}
				}
				files := ""
				if len(top.files) > 0 {
					names := make([]string, 0, 3)
					for _, f := range top.files[:minInt(3, len(top.files))] {
						parts := strings.Split(f, "/")
						names = append(names, parts[len(parts)-1])
					}
					files = "  " + strings.Join(names, ", ")
				}
				warn := lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
				b.WriteString("   " + styFaint.Render("│   ") +
					warn.Render(fmt.Sprintf("↯ %d file(s) also changed by %s", top.real, clip(top.other, 34))) +
					styFaint.Render(files) + "\n")
			}

		case rowDetachedHeader:
			// No "●" fork dot - this isn't a commit branches forked from,
			// just a flat list, and the plain divider says so rather than
			// reading as one more branch under the fork point above it.
			b.WriteString("   " + lipgloss.NewStyle().Foreground(cWarn).Render("detached") +
				styFaint.Render("  · no branch points here") + "\n")

		case rowDetached:
			u := r.unit
			elbow := "├"
			if r.last {
				elbow = "╰"
			}
			mark, tint := verdictMark(u.Verdict)
			bar := strings.Repeat("━", clampInt(isqrt(u.Unlanded)*2, 0, 22))
			if u.Unlanded > 0 {
				bar += "▸"
			}
			name := clip("(detached "+u.Head+")", 38)

			pathW := clampInt(w-78-30, 8, 40)
			wtText, wtWarn := worktreeText(&u, snap, pathW)
			wtStyle := styFaint
			if wtWarn {
				wtStyle = lipgloss.NewStyle().Foreground(cWarn)
			}

			// Same column widths as rowBranch, but no leading "│": these
			// rows sit under the divider above, not the last fork's spine.
			line := "      " + styFaint.Render(elbow) +
				lipgloss.NewStyle().Foreground(tint).Render(mark+" ") +
				styText.Render(pad(name, 39)) +
				lipgloss.NewStyle().Foreground(tint).Render(pad(bar, 24)) +
				styFaint.Render(fmt.Sprintf("+%-4d ", u.Unlanded)) +
				wtStyle.Render(wtText)

			if i == a.cursor {
				plain := fmt.Sprintf("      %s%s %s%s+%-4d %s", elbow, mark, pad(name, 39), pad(bar, 24), u.Unlanded, wtText)
				line = lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(pad(plain, w-1))
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// worktreeText is the trailing worktree column for a topology branch row:
// the path plus compact dirty/op badges, or "(no worktree)" when the unit
// has none - matching the plan's own mockup for both shapes. attention is
// true when the row deserves the warning colour (dirty, or an op mid-flight).
func worktreeText(u *protocol.Unit, snap *protocol.Snapshot, pathWidth int) (text string, attention bool) {
	wt, ok := unitWorktree(u, snap)
	if !ok {
		return "(no worktree)", false
	}
	dirtyN := wt.Dirty.Staged + wt.Dirty.Unstaged + wt.Dirty.Untracked + wt.Dirty.Conflicted
	state := "clean"
	if dirtyN > 0 {
		state = fmt.Sprintf("dirty:%d", dirtyN)
	}
	opInProgress := wt.Op != "" && wt.Op != "none"
	text = "⬖ " + clip(wt.Path, pathWidth) + " " + state
	if opInProgress {
		text += " " + wt.Op + "-in-progress"
	}
	return text, dirtyN > 0 || opInProgress
}

type topoRowKind int

const (
	rowFork topoRowKind = iota
	rowBranch
	rowSpine
	rowDetachedHeader // one-line divider introducing the section below
	rowDetached
)

type topoRow struct {
	kind topoRowKind
	fork protocol.ForkPoint
	unit protocol.Unit
	last bool
}

// topologyRows flattens the fork tree into printable rows, so cursor movement
// and rendering agree on what row means.
func (a *App) topologyRows() []topoRow {
	var rows []topoRow
	byName := map[string]protocol.Unit{}
	for _, u := range a.snap.Units {
		if u.Branch != "" {
			byName[u.Branch] = u
		}
	}
	for fi, f := range a.snap.Forks {
		if fi > 0 {
			rows = append(rows, topoRow{kind: rowSpine})
		}
		rows = append(rows, topoRow{kind: rowFork, fork: f})
		for bi, name := range f.Branches {
			u, ok := byName[name]
			if !ok {
				continue
			}
			rows = append(rows, topoRow{kind: rowBranch, unit: u, last: bi == len(f.Branches)-1})
		}
	}

	// buildForks (internal/model/forks.go) skips every unit with Branch == ""
	// before it ever becomes part of a ForkPoint - there is no branch name to
	// group by a fork point - so a detached-HEAD unit never appears above.
	// That includes an InProgress unit whose worktree is itself detached (a
	// rebase leaves HEAD detached until it finishes; see landing.go's own
	// comment on buildUnits' second loop). Both are exactly the work most
	// worth surfacing, so they get their own flat section instead of staying
	// unreachable by the cursor.
	//
	// a.snap.Units already arrives sorted worst-first (buildUnits' own final
	// sort: Verdict rank, then Unlanded, descending severity) and that order
	// is preserved index-for-index into protocol.Snapshot.Units (see
	// internal/engine/engine.go's toSnapshot) - so filtering in place keeps
	// the same most-severe-first order without a re-sort here.
	var detached []protocol.Unit
	for _, u := range a.snap.Units {
		if u.Branch == "" {
			detached = append(detached, u)
		}
	}
	if len(detached) > 0 {
		rows = append(rows, topoRow{kind: rowDetachedHeader})
		for i, u := range detached {
			rows = append(rows, topoRow{kind: rowDetached, unit: u, last: i == len(detached)-1})
		}
	}
	return rows
}

func clip(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:maxInt(0, w)])
	}
	return string(r[:w-1]) + "…"
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func isqrt(n int) int {
	if n <= 0 {
		return 0
	}
	x := n
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + n/x) / 2
	}
	return x
}
