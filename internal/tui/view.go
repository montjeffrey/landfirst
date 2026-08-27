package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/montjeffrey/landfirst/internal/model"
)

// pad truncates or pads to an exact display width so columns line up even when
// a branch name is absurdly long.
func pad(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		if w <= 1 {
			return string(r[:w])
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}

func (a *App) View() string {
	if a.quitting {
		return ""
	}
	if a.m == nil {
		return "\n  " + styDim.Render(a.status) + "\n"
	}
	w := a.w
	if w < 40 {
		w = 100
	}

	var b strings.Builder
	b.WriteString(a.header(w))
	b.WriteString("\n")

	body := a.h - 6 // header(2) + tabs(1) + rule(1) + footer(2)
	if body < 3 {
		body = 12
	}
	a.scrollTo(body)

	switch a.tab {
	case tabGraph:
		b.WriteString(a.viewGraph(w, body))
	case tabLanding:
		b.WriteString(a.viewLanding(w, body))
	case tabPlan:
		b.WriteString(a.viewPlan(w, body))
	case tabWorktrees:
		b.WriteString(a.viewWorktrees(w, body))
	}
	b.WriteString(a.footer(w))
	return b.String()
}

func (a *App) scrollTo(body int) {
	if a.cursor < a.top {
		a.top = a.cursor
	}
	if a.cursor >= a.top+body {
		a.top = a.cursor - body + 1
	}
	if a.top < 0 {
		a.top = 0
	}
}

func (a *App) header(w int) string {
	m := a.m
	title := styTitle.Render(m.Repo)
	meta := styDim.Render(fmt.Sprintf("  base %s  ·  %d units  ·  %d worktrees", m.Base, len(m.Units), len(m.Worktrees)))

	var banner string
	if m.AtRisk > 0 {
		banner = styBanner.Foreground(cBad).Render(fmt.Sprintf(
			"  %d change(s) across %d branch(es) exist only on this machine", m.StrandedCommits, m.AtRisk))
	} else {
		banner = styBanner.Foreground(cOK).Render("  everything with local work has reached the remote")
	}

	tabs := make([]string, len(tabNames))
	for i, n := range tabNames {
		label := fmt.Sprintf(" %d %s ", i+1, n)
		if tab(i) == a.tab {
			tabs[i] = styTabOn.Render(label)
		} else {
			tabs[i] = styTabOff.Render(label)
		}
	}
	return title + meta + "\n" + banner + "\n" + strings.Join(tabs, "") + "\n" +
		styFaint.Render(strings.Repeat("─", w)) + "\n"
}

func (a *App) viewLanding(w, body int) string {
	var b strings.Builder
	nameW := clamp(w-58, 18, 46)
	lastVerdict := model.Verdict("")

	for i := a.top; i < len(a.m.Units) && i < a.top+body; i++ {
		u := a.m.Units[i]
		if u.Verdict != lastVerdict {
			lastVerdict = u.Verdict
			info := model.Verdicts[u.Verdict]
			b.WriteString(lipgloss.NewStyle().Foreground(tone(u.Verdict)).Bold(true).
				Render(strings.ToUpper(info.Label)) + " " + styFaint.Render(info.Blurb) + "\n")
		}
		name := u.Branch
		if name == "" {
			name = "(detached " + u.Head + ")"
		}
		risk := ""
		if len(u.Risks) > 0 {
			kinds := map[string]bool{}
			var ks []string
			for _, r := range u.Risks {
				if !kinds[r.Kind] {
					kinds[r.Kind] = true
					ks = append(ks, r.Kind)
				}
			}
			risk = " " + lipgloss.NewStyle().Foreground(cWarn).Render("["+strings.Join(ks, ",")+"]")
		}
		wt := ""
		if n := len(u.Worktrees); n > 0 {
			wt = lipgloss.NewStyle().Foreground(lipgloss.Color("176")).Render(fmt.Sprintf(" ⬖%d", n))
		}
		row := fmt.Sprintf("  %s %s %s %s%s%s",
			lipgloss.NewStyle().Foreground(tone(u.Verdict)).Render(glyph(u.Verdict)),
			styText.Render(pad(name, nameW)),
			styDim.Render(pad(u.Detail, 34)),
			styFaint.Render(pad(ago(u.Date), 4)),
			wt, risk)
		if i == a.cursor {
			row = stySelected.Render(pad(stripANSI(row), w-1))
			row = lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(
				fmt.Sprintf("  %s %s %s %s",
					lipgloss.NewStyle().Foreground(tone(u.Verdict)).Render(glyph(u.Verdict)),
					styText.Render(pad(name, nameW)),
					styDim.Render(pad(u.Detail, 34)),
					styFaint.Render(pad(ago(u.Date), 4))))
		}
		b.WriteString(row + "\n")
	}
	return b.String()
}

// viewGraph draws the commit graph on a sub-cell canvas, with the commit text
// aligned beside it.
//
// The gutter used to be box-drawing characters, one cell per lane. That is
// legible at three branches and unreadable at ten: every lane change becomes a
// corner glyph the reader has to decode, and diagonals become staircases. The
// canvas addresses 2x4 sub-pixels per cell, so edges are real curves and a
// branch departing its parent is obvious at a glance.
func (a *App) viewGraph(w, body int) string {
	g := a.m.Graph
	if g == nil || len(g.Commits) == 0 {
		return styDim.Render("  no commits to draw") + "\n"
	}
	var b strings.Builder

	if g.Truncated || g.Hidden > 0 {
		note := fmt.Sprintf("  showing %d of %d commits", len(g.Commits), g.Total)
		if g.Hidden > 0 {
			note += fmt.Sprintf(" · %d branch(es) not drawn", g.Hidden)
		}
		b.WriteString(styFaint.Render(note) + "\n")
		body--
	}

	gc := DrawGraph(g, a.density, 0)
	rpc := a.density.rowsPerCommit()

	// Scroll by text row, so a partially visible commit still shows its curve.
	first := a.top * rpc
	for i := 0; i < body && first+i < len(gc.Lines); i++ {
		rowIdx := first + i
		line := "  " + gc.Lines[rowIdx]

		// Attach commit text on the row the node sits on.
		commitIdx := rowIdx / rpc
		if rowIdx%rpc == 0 && commitIdx < len(g.Commits) {
			c := g.Commits[commitIdx]
			line += "  " + a.commitText(c, w-gc.Width-4, commitIdx == a.cursor)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// commitText is the right-hand pane: sha, refs, subject, age.
func (a *App) commitText(c model.Commit, width int, selected bool) string {
	if width < 20 {
		width = 20
	}
	var refs string
	for _, r := range c.Refs {
		label := strings.TrimPrefix(r, "HEAD -> ")
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
		switch {
		case strings.HasPrefix(r, "HEAD -> "), r == "HEAD":
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
		case strings.HasPrefix(r, "tag: "):
			label = strings.TrimPrefix(r, "tag: ")
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("179"))
		}
		refs += style.Render(label) + " "
	}

	const ageW = 5
	subjW := maxInt(8, width-9-ageW-lipgloss.Width(refs))
	age := ago(c.Date)

	if selected {
		plain := fmt.Sprintf("%s %s%s %s", c.Short, stripANSI(refs), pad(c.Subject, subjW), pad(age, ageW))
		return lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(pad(plain, width))
	}
	return styFaint.Render(c.Short) + " " + refs +
		styText.Render(pad(c.Subject, subjW)) + " " + styFaint.Render(pad(age, ageW))
}

func (a *App) viewPlan(w, body int) string {
	var b strings.Builder
	if len(a.m.Plan) == 0 {
		return styDim.Render("  nothing waiting to merge") + "\n"
	}
	b.WriteString(styFaint.Render(fmt.Sprintf("  %-3s %-42s %8s %8s %6s  %s",
		"#", "branch", "unlanded", "behind", "confl", "reasoning")) + "\n")
	for i := a.top; i < len(a.m.Plan) && i < a.top+body; i++ {
		p := a.m.Plan[i]
		conf := "—"
		if p.Conflicts >= 0 {
			conf = fmt.Sprint(p.Conflicts)
		}
		confStyle := styDim
		if p.Conflicts > 0 {
			confStyle = lipgloss.NewStyle().Foreground(cBad)
		} else if p.Conflicts == 0 {
			confStyle = lipgloss.NewStyle().Foreground(cOK)
		}
		row := fmt.Sprintf("  %-3d %s %8d %8d %s  %s",
			p.Order, styText.Render(pad(p.Branch, 42)), p.Ahead, p.BehindN,
			confStyle.Render(pad(conf, 6)), styFaint.Render(strings.Join(p.Why, " · ")))
		if i == a.cursor {
			row = lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(pad(stripANSI(row), w-1))
		}
		b.WriteString(row + "\n")
	}
	return b.String()
}

func (a *App) viewWorktrees(w, body int) string {
	var b strings.Builder
	for i := a.top; i < len(a.m.Worktrees) && i < a.top+body; i++ {
		t := a.m.Worktrees[i]
		br := t.Branch
		if br == "" {
			br = "(detached)"
		}
		var flags []string
		if t.IsMain {
			flags = append(flags, "main")
		}
		if t.Prunable {
			flags = append(flags, "prunable")
		}
		for _, r := range t.Risks {
			flags = append(flags, r.Kind)
		}
		fl := ""
		if len(flags) > 0 {
			fl = lipgloss.NewStyle().Foreground(cWarn).Render("[" + strings.Join(flags, ",") + "]")
		}
		row := fmt.Sprintf("  %s %s %s",
			styText.Render(pad(br, 38)), styFaint.Render(pad(t.Path, clamp(w-52, 20, 70))), fl)
		if i == a.cursor {
			row = lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(pad(stripANSI(row), w-1))
		}
		b.WriteString(row + "\n")
	}
	return b.String()
}

func (a *App) footer(w int) string {
	if a.confirm != nil {
		q := fmt.Sprintf("  run  git %s   ?  ", strings.Join(a.confirm.args, " "))
		return "\n" + lipgloss.NewStyle().Background(lipgloss.Color("58")).Foreground(lipgloss.Color("231")).
			Bold(true).Render(pad(q+"[y] yes   [any other key] no", w-1))
	}
	help := styFaint.Render("  ") +
		styKey.Render("j/k") + styFaint.Render(" move  ") +
		styKey.Render("1-4/tab") + styFaint.Render(" panel  ") +
		styKey.Render("d") + styFaint.Render(" density  ") +
		styKey.Render("p") + styFaint.Render(" fix selected  ") +
		styKey.Render("r") + styFaint.Render(" refresh  ") +
		styKey.Render("q") + styFaint.Render(" quit")
	return "\n" + styFaint.Render(strings.Repeat("─", w)) + "\n" +
		help + "\n  " + styFaint.Render(a.status)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// stripANSI removes styling so a highlighted row can be padded to an exact
// width without counting escape sequences as visible characters.
func stripANSI(s string) string {
	var out strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && r == 'm':
			inEsc = false
		case !inEsc:
			out.WriteRune(r)
		}
	}
	return out.String()
}
