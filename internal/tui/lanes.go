package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/montjeffrey/landfirst/internal/model"
)

// git's own --graph draws lane changes with \ and / on the commit rows, which
// is where the blocky look comes from. This emits a dedicated link line for
// topology changes and draws them with box-drawing corners, so a branch reads
// as a curve:
//
//	● commit on lane 0
//	├─╮        link line: lane 0 branches right
//	│ ● commit on lane 1
//	● │ commit on lane 0
//	├─╯        link line: lane 1 merges back
const (
	vert     = '│'
	horiz    = '─'
	teeR     = '├'
	teeL     = '┤'
	cornerDL = '╮'
	cornerDR = '╭'
	cross    = '┼'
	dot      = '●'
	ring     = '○' // merge commit
	tip      = '◉' // carries a ref
	blank    = ' '
)

// laneColors are 256-colour indices, chosen to stay distinguishable on both
// light and dark terminals.
var laneColors = []string{"39", "78", "203", "141", "179", "43", "215", "111", "176", "79", "221", "167"}

// GraphRow is one printable line: either a commit or a link line between them.
type GraphRow struct {
	Kind   string // "commit" | "link"
	Cells  []rune
	Colors []int // -1 for none
	Commit *model.Commit
}

// BuildGraphRows turns the laid-out graph into printable rows.
func BuildGraphRows(g *model.Graph) []GraphRow {
	n := len(g.Commits)
	if n == 0 {
		return nil
	}

	// Which lanes have a line passing vertically through each row.
	open := make([]map[int]int, n)
	for i := range open {
		open[i] = map[int]int{}
	}
	for _, e := range g.Edges {
		if e.Stub || e.ToRow < 0 {
			continue
		}
		for r := e.FromRow + 1; r < e.ToRow && r < n; r++ {
			open[r][e.ToLane] = e.Color
		}
	}

	rows := make([]GraphRow, 0, n+n/4)
	for r := 0; r < n; r++ {
		c := &g.Commits[r]
		cells := make([]rune, g.LaneCount)
		colors := make([]int, g.LaneCount)
		for i := range cells {
			cells[i] = blank
			colors[i] = -1
		}
		for lane, col := range open[r] {
			if lane < len(cells) {
				cells[lane] = vert
				colors[lane] = col
			}
		}
		glyph := dot
		switch {
		case len(c.Parents) > 1:
			glyph = ring
		case len(c.Refs) > 0:
			glyph = tip
		}
		if c.Lane < len(cells) {
			cells[c.Lane] = glyph
			colors[c.Lane] = c.Color
		}
		rows = append(rows, GraphRow{Kind: "commit", Cells: cells, Colors: colors, Commit: c})

		// A link line only when topology actually changes on this row.
		var jogs []model.Edge
		for _, e := range g.Edges {
			if !e.Stub && e.FromRow == r && e.FromLane != e.ToLane {
				jogs = append(jogs, e)
			}
		}
		if len(jogs) == 0 {
			continue
		}
		lc := make([]rune, g.LaneCount)
		lcol := make([]int, g.LaneCount)
		for i := range lc {
			lc[i] = blank
			lcol[i] = -1
		}
		if r+1 < n {
			for lane, col := range open[r+1] {
				if lane < len(lc) {
					lc[lane] = vert
					lcol[lane] = col
				}
			}
		}
		for _, e := range jogs {
			lo, hi := e.FromLane, e.ToLane
			if lo > hi {
				lo, hi = hi, lo
			}
			for x := lo + 1; x < hi && x < len(lc); x++ {
				if lc[x] == vert || lc[x] == cross {
					lc[x] = cross
				} else {
					lc[x] = horiz
				}
				if lcol[x] < 0 {
					lcol[x] = e.Color
				}
			}
			if e.FromLane < len(lc) {
				if e.ToLane > e.FromLane {
					lc[e.FromLane] = teeR
				} else {
					lc[e.FromLane] = teeL
				}
				lcol[e.FromLane] = e.Color
			}
			if e.ToLane < len(lc) {
				if e.ToLane > e.FromLane {
					lc[e.ToLane] = cornerDL
				} else {
					lc[e.ToLane] = cornerDR
				}
				lcol[e.ToLane] = e.Color
			}
		}
		rows = append(rows, GraphRow{Kind: "link", Cells: lc, Colors: lcol})
	}
	return rows
}

// Gutter renders one row. Every gutter is the same width, so the text beside it
// lines up regardless of topology.
func Gutter(row GraphRow, laneCount int, colour bool) string {
	var b strings.Builder
	for i := 0; i < laneCount; i++ {
		ch := blank
		col := -1
		if i < len(row.Cells) {
			ch, col = row.Cells[i], row.Colors[i]
		}
		b.WriteString(paintRune(ch, col, colour))

		if i < laneCount-1 {
			nxt := blank
			if i+1 < len(row.Cells) {
				nxt = row.Cells[i+1]
			}
			// A horizontal run must not be broken by the inter-lane gap. The
			// test is symmetric: the left cell must continue rightwards and the
			// right cell must continue leftwards. Omitting cornerDR from the
			// left set left a visible gap on every right-to-left merge.
			continuesRight := ch == horiz || ch == teeR || ch == cross || ch == cornerDR
			continuesLeft := nxt == horiz || nxt == cross || nxt == cornerDL || nxt == teeL
			bridge := continuesRight && continuesLeft
			if bridge {
				b.WriteString(paintRune(horiz, col, colour))
			} else {
				b.WriteRune(blank)
			}
		}
	}
	return b.String()
}

func paintRune(ch rune, col int, colour bool) string {
	if !colour || col < 0 || ch == blank {
		return string(ch)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(laneColors[col%len(laneColors)])).
		Render(string(ch))
}

// GutterWidth is the printable width of any gutter for this lane count.
func GutterWidth(laneCount int) int {
	if laneCount <= 0 {
		return 0
	}
	return laneCount*2 - 1
}
