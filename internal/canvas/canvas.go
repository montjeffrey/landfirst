// Package canvas is a sub-cell drawing surface for the terminal.
//
// A terminal cell is the smallest thing most TUIs can colour, which is why
// character-drawn graphs look blocky: a diagonal has to become a staircase of
// box characters. Braille gives 2x4 addressable dots inside one cell, so the
// same cell can carry eight distinct points and a diagonal reads as a line.
//
// The catch is colour: a cell has exactly one foreground, so two differently
// coloured strokes cannot share a cell without one of them losing. Callers are
// expected to space distinct-coloured tracks at least one cell apart; Set
// records the first colour written to a cell and keeps it, so a collision
// degrades predictably instead of flickering between frames.
package canvas

import (
	"strconv"
	"strings"
)

// Braille dot bits, by (x, y) within a cell. The Unicode order is not the
// reading order, which is the usual source of bugs here:
//
//	dot1 dot4     (0,0) (1,0)
//	dot2 dot5     (0,1) (1,1)
//	dot3 dot6     (0,2) (1,2)
//	dot7 dot8     (0,3) (1,3)
var dotBit = [2][4]byte{
	{0x01, 0x02, 0x04, 0x40}, // x=0: dots 1,2,3,7
	{0x08, 0x10, 0x20, 0x80}, // x=1: dots 4,5,6,8
}

const brailleBase = 0x2800

// SubX and SubY are how many sub-pixels fit in one terminal cell.
const (
	SubX = 2
	SubY = 4
)

// Canvas is a fixed-size grid of cells, each holding a braille bitmask and a
// colour.
type Canvas struct {
	cols, rows int
	bits       []byte
	color      []int // -1 = unset
}

// New makes a canvas measured in terminal cells.
func New(cols, rows int) *Canvas {
	if cols < 0 {
		cols = 0
	}
	if rows < 0 {
		rows = 0
	}
	c := &Canvas{cols: cols, rows: rows,
		bits:  make([]byte, cols*rows),
		color: make([]int, cols*rows)}
	for i := range c.color {
		c.color[i] = -1
	}
	return c
}

// Width and Height are in sub-pixels, which is what every drawing call uses.
func (c *Canvas) Width() int  { return c.cols * SubX }
func (c *Canvas) Height() int { return c.rows * SubY }

// Cols and Rows are in terminal cells.
func (c *Canvas) Cols() int { return c.cols }
func (c *Canvas) Rows() int { return c.rows }

// Set lights one sub-pixel. Out-of-range writes are dropped rather than
// panicking: the graph is laid out against a live repository and a viewport
// that can be resized mid-draw, so clipping is the normal case.
func (c *Canvas) Set(x, y, colour int) {
	if x < 0 || y < 0 || x >= c.Width() || y >= c.Height() {
		return
	}
	cell := (y/SubY)*c.cols + (x / SubX)
	c.bits[cell] |= dotBit[x%SubX][y%SubY]
	if c.color[cell] == -1 {
		c.color[cell] = colour
	}
}

// Line draws a straight line in sub-pixel space (Bresenham).
func (c *Canvas) Line(x0, y0, x1, y1, colour int) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		c.Set(x0, y0, colour)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// VLine is the common case and avoids Bresenham's per-step work.
func (c *Canvas) VLine(x, y0, y1, colour int) {
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	for y := y0; y <= y1; y++ {
		c.Set(x, y, colour)
	}
}

// Curve draws a quadratic bezier from (x0,y0) to (x1,y1) bending through the
// control point. This is what makes a branch read as a curve instead of a
// staircase, and it is the whole reason for drawing at sub-cell resolution.
func (c *Canvas) Curve(x0, y0, cx, cy, x1, y1, colour int) {
	// Step count from the control polygon length, so long curves stay smooth
	// and short ones do not waste work.
	steps := (abs(x1-x0) + abs(y1-y0) + abs(cx-x0) + abs(cy-y0)) / 2
	if steps < 8 {
		steps = 8
	}
	if steps > 512 {
		steps = 512
	}
	px, py := x0, y0
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		mt := 1 - t
		fx := mt*mt*float64(x0) + 2*mt*t*float64(cx) + t*t*float64(x1)
		fy := mt*mt*float64(y0) + 2*mt*t*float64(cy) + t*t*float64(y1)
		nx, ny := int(fx+0.5), int(fy+0.5)
		// Join successive samples so a fast-moving curve leaves no gaps.
		c.Line(px, py, nx, ny, colour)
		px, py = nx, ny
	}
}

// Ellipse draws a filled node with independent radii. Nodes are usually wider
// than tall: four sub-pixel rows make up one text row, so a node taller than
// three straddles a row boundary and splits into two visually separate halves.
func (c *Canvas) Ellipse(cx, cy, rx, ry, colour int) {
	if rx < 1 && ry < 1 {
		c.Set(cx, cy, colour)
		return
	}
	for dy := -ry; dy <= ry; dy++ {
		for dx := -rx; dx <= rx; dx++ {
			if dx*dx*ry*ry+dy*dy*rx*rx <= rx*rx*ry*ry {
				c.Set(cx+dx, cy+dy, colour)
			}
		}
	}
}

// EllipseRing is the outlined form, used for merge commits so they are
// distinguishable without relying on colour.
func (c *Canvas) EllipseRing(cx, cy, rx, ry, colour int) {
	if rx < 2 || ry < 1 {
		c.Ellipse(cx, cy, rx, ry, colour)
		return
	}
	for dy := -ry; dy <= ry; dy++ {
		for dx := -rx; dx <= rx; dx++ {
			out := dx*dx*ry*ry + dy*dy*rx*rx
			in := dx*dx*(ry-1)*(ry-1) + dy*dy*(rx-1)*(rx-1)
			if out <= rx*rx*ry*ry && in >= (rx-1)*(rx-1)*(ry-1)*(ry-1) {
				c.Set(cx+dx, cy+dy, colour)
			}
		}
	}
}

// Dot draws a filled node of the given sub-pixel radius, the visual weight that
// distinguishes a commit from the lines running past it.
func (c *Canvas) Dot(cx, cy, r, colour int) {
	if r < 1 {
		c.Set(cx, cy, colour)
		return
	}
	// A terminal cell is roughly twice as tall as it is wide, and braille
	// splits it 2 across by 4 down - so a braille sub-pixel is very nearly
	// square and needs no aspect correction. Weighting y here (an easy
	// assumption to carry over from half-block rendering) stretches every
	// node into a vertical ellipse.
	r2 := r * r
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if dx*dx+dy*dy <= r2 {
				c.Set(cx+dx, cy+dy, colour)
			}
		}
	}
}

// Ring draws an outlined node, used for merge commits so they are
// distinguishable from ordinary ones without relying on colour.
func (c *Canvas) Ring(cx, cy, r, colour int) {
	if r < 1 {
		c.Set(cx, cy, colour)
		return
	}
	r2 := r * r
	inner := (r - 1) * (r - 1)
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			d := dx*dx + dy*dy
			if d <= r2 && d >= inner {
				c.Set(cx+dx, cy+dy, colour)
			}
		}
	}
}

// Render turns the canvas into printable lines. Palette maps a colour index to
// a 256-colour terminal code; a nil palette renders uncoloured.
//
// The escape is written directly rather than through a styling library. Those
// libraries detect terminal capability from the environment and silently drop
// colour when stdout is not a tty, which makes a drawing primitive behave
// differently under test than in use. A canvas should render the same bytes
// wherever it runs; deciding whether colour is wanted belongs to the caller.
func (c *Canvas) Render(palette []int) []string {
	out := make([]string, c.rows)
	var b strings.Builder
	for row := 0; row < c.rows; row++ {
		b.Reset()
		for col := 0; col < c.cols; col++ {
			i := row*c.cols + col
			if c.bits[i] == 0 {
				b.WriteRune(' ')
				continue
			}
			ch := string(rune(brailleBase + int(c.bits[i])))
			if palette == nil || len(palette) == 0 || c.color[i] < 0 {
				b.WriteString(ch)
				continue
			}
			b.WriteString("\x1b[38;5;")
			b.WriteString(strconv.Itoa(palette[c.color[i]%len(palette)]))
			b.WriteString("m")
			b.WriteString(ch)
			b.WriteString("\x1b[0m")
		}
		out[row] = b.String()
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
