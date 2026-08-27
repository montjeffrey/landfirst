package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/montjeffrey/landfirst/internal/model"
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
	m := a.m
	if len(m.Forks) == 0 {
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
	for _, c := range m.Contentions {
		peers[c.A] = append(peers[c.A], peer{c.B, c.Real, c.Files})
		peers[c.B] = append(peers[c.B], peer{c.A, c.Real, c.Files})
	}

	rows := a.topologyRows()
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
			wt := "  "
			if len(u.Worktrees) > 0 {
				wt = lipgloss.NewStyle().Foreground(lipgloss.Color("176")).Render(" ⬖")
			}
			// Bar length is sqrt-scaled: a 195-commit branch should read as
			// bigger than a 6-commit one without being 30x the width.
			bar := strings.Repeat("━", clampInt(isqrt(u.Unlanded)*2, 0, 22))
			if u.Unlanded > 0 {
				bar += "▸"
			}
			name := clip(u.Branch, 38)
			line := "   " + styFaint.Render("│  ") + styFaint.Render(elbow) +
				lipgloss.NewStyle().Foreground(tint).Render(mark+" ") +
				styText.Render(pad(name, 39)) +
				lipgloss.NewStyle().Foreground(tint).Render(pad(bar, 24)) +
				styFaint.Render(fmt.Sprintf("+%-4d", u.Unlanded)) + wt

			if i == a.cursor {
				plain := fmt.Sprintf("   │  %s%s %s%s+%-4d", elbow, mark, pad(name, 39), pad(bar, 24), u.Unlanded)
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
		}
	}
	return b.String()
}

type topoRowKind int

const (
	rowFork topoRowKind = iota
	rowBranch
	rowSpine
)

type topoRow struct {
	kind topoRowKind
	fork model.ForkPoint
	unit model.Unit
	last bool
}

// topologyRows flattens the fork tree into printable rows, so cursor movement
// and rendering agree on what row means.
func (a *App) topologyRows() []topoRow {
	var rows []topoRow
	byName := map[string]model.Unit{}
	for _, u := range a.m.Units {
		if u.Branch != "" {
			byName[u.Branch] = u
		}
	}
	for fi, f := range a.m.Forks {
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
	return rows
}

func verdictMark(v model.Verdict) (string, lipgloss.Color) {
	switch model.Verdicts[v].Rank {
	case 0, 1, 2:
		return "!", cBad
	case 3, 4, 5:
		return "~", cWarn
	case 6:
		return "-", cInfo
	}
	return "·", cOK
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
