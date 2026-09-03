package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/montjeffrey/landfirst/internal/protocol"
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
	if a.snap == nil {
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
	case tabTopology:
		b.WriteString(a.viewTopology(w, body))
	case tabGraph:
		b.WriteString(a.viewGraph(w, body))
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
	snap := a.snap
	title := styTitle.Render(snap.Repo.Name)
	meta := styDim.Render(fmt.Sprintf("  base %s  ·  %d units  ·  %d worktrees", snap.Repo.Base, len(snap.Units), len(snap.Worktrees)))

	var banner string
	if snap.Summary.AtRisk > 0 {
		banner = styBanner.Foreground(cBad).Render(fmt.Sprintf(
			"  %d change(s) across %d branch(es) exist only on this machine", snap.Summary.StrandedCommits, snap.Summary.AtRisk))
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

// viewGraph draws the commit graph on a sub-cell canvas, with the commit text
// aligned beside it.
//
// The gutter used to be box-drawing characters, one cell per lane. That is
// legible at three branches and unreadable at ten: every lane change becomes a
// corner glyph the reader has to decode, and diagonals become staircases. The
// canvas addresses 2x4 sub-pixels per cell, so edges are real curves and a
// branch departing its parent is obvious at a glance.
func (a *App) viewGraph(w, body int) string {
	g := a.graph
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

	// How much width the canvas may take before the text pane is squeezed out.
	// The text is the point of the graph, so the canvas yields, not the text.
	const minTextW = 44
	canvasBudget := w - minTextW - 4
	if canvasBudget < 8 {
		canvasBudget = 8
	}
	if a.panX > gc.Width-1 {
		a.panX = maxInt(0, gc.Width-1)
	}
	shown := gc.Width - a.panX
	if shown > canvasBudget {
		shown = canvasBudget
	}
	if gc.Width > canvasBudget {
		b.WriteString(styFaint.Render(fmt.Sprintf(
			"  lanes %d-%d of %d · h/l to pan, H to reset",
			a.panX/laneCells+1, minInt(gc.Width, a.panX+shown)/laneCells, g.LaneCount)) + "\n")
		body--
	}

	// Scroll by text row, so a partially visible commit still shows its curve.
	first := a.top * rpc
	for i := 0; i < body && first+i < len(gc.Lines); i++ {
		rowIdx := first + i
		line := "  " + sliceCells(gc.Lines[rowIdx], a.panX, shown)

		// Attach commit text on the row the node sits on.
		commitIdx := rowIdx / rpc
		if rowIdx%rpc == 0 && commitIdx < len(g.Commits) {
			c := g.Commits[commitIdx]
			line += "  " + a.commitText(c, w-shown-6, commitIdx == a.cursor)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// commitText is the right-hand pane: sha, refs, subject, age.
func (a *App) commitText(c protocol.Commit, width int, selected bool) string {
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

func (a *App) footer(w int) string {
	if a.confirm != nil {
		q := "  " + a.confirm.prompt() + "  "
		return "\n" + lipgloss.NewStyle().Background(lipgloss.Color("58")).Foreground(lipgloss.Color("231")).
			Bold(true).Render(pad(q+"[y] yes   [any other key] no", w-1))
	}
	if a.node != nil {
		return "\n" + a.treeFooter(w)
	}
	help := styFaint.Render("  ") +
		styKey.Render("j/k") + styFaint.Render(" move  ") +
		styKey.Render("1/2/tab") + styFaint.Render(" panel  ") +
		styKey.Render("d") + styFaint.Render(" density  ") +
		styKey.Render("h/l") + styFaint.Render(" pan  ") +
		styKey.Render("p") + styFaint.Render(" decide  ") +
		styKey.Render("w/W") + styFaint.Render(" worktree +/-  ") +
		styKey.Render("r") + styFaint.Render(" refresh  ") +
		styKey.Render("q") + styFaint.Render(" quit")
	return "\n" + styFaint.Render(strings.Repeat("─", w)) + "\n" +
		help + "\n  " + styFaint.Render(a.status)
}

// treeFooter renders the decision tree in place of the help line: a
// question and its numbered options, or a leaf's informational text
// (manual advice, or why a leaf is currently blocked).
func (a *App) treeFooter(w int) string {
	node := a.node
	if node.Action != nil {
		return a.leafFooter(w, node.Action)
	}
	var b strings.Builder
	b.WriteString(styFaint.Render(strings.Repeat("─", w)) + "\n")
	b.WriteString("  " + styText.Render(node.Question) + "\n")
	for i, opt := range node.Options {
		suggested := ""
		if leaf := opt.Next.Action; leaf != nil && a.tree.Suggested != "" && leaf.ID == a.tree.Suggested {
			suggested = styFaint.Render("  (suggested)")
		}
		b.WriteString(fmt.Sprintf("  %s %s%s\n",
			styKey.Render(fmt.Sprintf("[%d]", i+1)), styText.Render(opt.Answer), suggested))
	}
	b.WriteString(styFaint.Render("  esc back   q quit"))
	return b.String()
}

// leafFooter is only reached for a leaf that is not immediately executable -
// Class manual, or blocked - since a runnable leaf opens the y/n confirm bar
// instead (footer's own caller checks a.confirm first).
func (a *App) leafFooter(w int, action *protocol.Action) string {
	var b strings.Builder
	b.WriteString(styFaint.Render(strings.Repeat("─", w)) + "\n")
	if action.Class == protocol.ClassManual {
		// today's exact advisory wording (internal/tui's old propose()),
		// just sourced from Action instead of Remedy.
		msg := "needs a manual decision — do it by hand"
		if action.Cwd != "" {
			msg += " in " + action.Cwd
		}
		msg += ": git " + strings.Join(action.Argv, " ")
		b.WriteString("  " + styText.Render(msg) + "\n")
	} else {
		b.WriteString("  " + styText.Render(action.Label) + " — " +
			lipgloss.NewStyle().Foreground(cWarn).Render("blocked: "+strings.Join(action.Blocked, "; ")) + "\n")
	}
	b.WriteString(styFaint.Render("  any key to continue"))
	return b.String()
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

// sliceCells takes a window of printable cells out of an already-styled line,
// skipping ANSI sequences so the escape bytes are neither counted as width nor
// cut in half. Slicing a styled string by byte offset is the classic way to
// emit a broken escape and corrupt the rest of the screen.
func sliceCells(s string, from, count int) string {
	if count <= 0 {
		return ""
	}
	var out strings.Builder
	col, taken := 0, 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			for j < len(s) && s[j] != 'm' {
				j++
			}
			if j < len(s) {
				j++
			}
			// Keep styling for cells inside the window so colour survives.
			if col >= from && taken < count {
				out.WriteString(s[i:j])
			}
			i = j
			continue
		}
		r, size := decodeRune(s[i:])
		if col >= from && taken < count {
			out.WriteRune(r)
			taken++
		}
		col++
		i += size
	}
	return out.String()
}

func decodeRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			size := 1
			for size < len(s) && (s[size]&0xC0) == 0x80 {
				size++
			}
			return r, size
		}
	}
	return ' ', 1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
