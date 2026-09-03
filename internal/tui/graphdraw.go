package tui

import (
	"github.com/montjeffrey/landfirst/internal/canvas"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// Density controls how much vertical room each commit gets. A docked panel is
// short, so this is the trade between smooth curves and how much history fits.
type Density int

const (
	Compact     Density = iota // 1 text row per commit  - 4 sub-pixels
	Comfortable                // 2 text rows per commit - 8 sub-pixels
	Spacious                   // 3 text rows per commit - 12 sub-pixels
)

func (d Density) rowsPerCommit() int {
	switch d {
	case Comfortable:
		return 2
	case Spacious:
		return 3
	}
	return 1
}

// laneCells is how many terminal cells separate lane centres.
//
// It is not a purely visual choice. A cell holds one foreground colour, so two
// differently coloured tracks sharing a cell means one loses. Two cells of
// separation guarantees each lane owns its own column of cells, which is what
// keeps branch colours truthful.
//
// Three cells (6 sub-pixels) is the minimum that fits a radius-2 node without
// it bleeding into the neighbouring lane.
const laneCells = 3

// lanePalette maps a commit's lane-derived colour index to a 256-colour
// terminal code for the canvas renderer.
var lanePalette = []int{39, 78, 203, 141, 179, 43, 215, 111, 176, 79, 221, 167}

// GraphCanvas is a drawn graph plus the row each commit landed on, so the text
// pane beside it can line up exactly.
type GraphCanvas struct {
	Lines     []string
	CommitRow map[string]int // commit hash -> text row within Lines
	Width     int            // cells
}

// DrawGraph renders the commit graph onto a sub-cell canvas.
//
// Edges are drawn as quadratic beziers rather than box characters: a branch
// leaving its parent becomes a curve that visibly departs and rejoins, instead
// of a staircase of corner glyphs that has to be decoded.
func DrawGraph(g *protocol.Graph, d Density, maxRows int) GraphCanvas {
	if g == nil || len(g.Commits) == 0 {
		return GraphCanvas{}
	}
	rpc := d.rowsPerCommit()
	cols := g.LaneCount*laneCells + 1
	rows := len(g.Commits) * rpc
	if maxRows > 0 && rows > maxRows {
		rows = maxRows
	}
	cv := canvas.New(cols, rows)

	// Sub-pixel centre of a lane, and of a commit's row.
	laneX := func(lane int) int { return lane*laneCells*canvas.SubX + laneCells }
	// Nodes sit in the middle of the commit's FIRST text row, so the text pane
	// lines up with the node and a node never straddles a row boundary.
	rowY := func(row int) int { return row*rpc*canvas.SubY + 1 }

	visible := func(row int) bool { return rowY(row) < cv.Height() }

	byHash := make(map[string]*protocol.Commit, len(g.Commits))
	for i := range g.Commits {
		byHash[g.Commits[i].Hash] = &g.Commits[i]
	}

	// Edges first so nodes paint over them.
	for _, e := range g.Edges {
		if !visible(e.FromRow) {
			continue
		}
		x0, y0 := laneX(e.FromLane), rowY(e.FromRow)

		if e.Stub || e.ToRow < 0 {
			// Parent is outside the window: fade out downward rather than
			// stopping dead, so a truncated history reads as truncated.
			cv.VLine(x0, y0, y0+(rpc*canvas.SubY)/2, e.Color)
			continue
		}
		x1, y1 := laneX(e.ToLane), rowY(e.ToRow)

		if x0 == x1 {
			cv.VLine(x0, y0, y1, e.Color)
			continue
		}
		// Bend near the child so the departure is visible immediately, then run
		// straight down the destination lane. A control point at the corner
		// gives the quarter-turn that makes lane changes readable.
		turn := y0 + (rpc*canvas.SubY)*3/4
		if turn > y1 {
			turn = y1
		}
		cv.Curve(x0, y0, x0, turn, x1, turn, e.Color)
		if turn < y1 {
			cv.VLine(x1, turn, y1, e.Color)
		}
	}

	// Wider than tall: three sub-pixel rows fit inside one text row, and the
	// extra width is what gives a node presence against the lines beside it.
	rx, ry := 2, 1
	for i := range g.Commits {
		c := &g.Commits[i]
		if !visible(c.Row) {
			continue
		}
		x, y := laneX(c.Lane), rowY(c.Row)
		if len(c.Parents) > 1 {
			cv.EllipseRing(x, y, rx, ry, c.Color) // merges read as hollow
		} else {
			cv.Ellipse(x, y, rx, ry, c.Color)
		}
	}

	lines := cv.Render(lanePalette)
	rowOf := make(map[string]int, len(g.Commits))
	for i := range g.Commits {
		if r := g.Commits[i].Row * rpc; r < len(lines) {
			rowOf[g.Commits[i].Hash] = r
		}
	}
	return GraphCanvas{Lines: lines, CommitRow: rowOf, Width: cols}
}
