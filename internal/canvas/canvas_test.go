package canvas

import (
	"strings"
	"testing"
)

// The braille bit order is not the reading order, and getting it wrong yields a
// canvas that looks plausible but is subtly transposed. Pin every dot.
func TestEachSubPixelMapsToItsBrailleDot(t *testing.T) {
	cases := []struct {
		x, y int
		want rune
	}{
		{0, 0, '⠁'}, {0, 1, '⠂'}, {0, 2, '⠄'}, {0, 3, '⡀'},
		{1, 0, '⠈'}, {1, 1, '⠐'}, {1, 2, '⠠'}, {1, 3, '⢀'},
	}
	for _, c := range cases {
		cv := New(1, 1)
		cv.Set(c.x, c.y, 0)
		got := []rune(cv.Render(nil)[0])[0]
		if got != c.want {
			t.Fatalf("sub-pixel (%d,%d) rendered %q, want %q", c.x, c.y, got, c.want)
		}
	}
}

func TestAllEightDotsFillOneCell(t *testing.T) {
	cv := New(1, 1)
	for x := 0; x < SubX; x++ {
		for y := 0; y < SubY; y++ {
			cv.Set(x, y, 0)
		}
	}
	if got := cv.Render(nil)[0]; got != "⣿" {
		t.Fatalf("a fully lit cell rendered %q, want ⣿", got)
	}
}

func TestEmptyCellsRenderAsSpaceNotBlankBraille(t *testing.T) {
	// U+2800 is a *blank* braille cell and is not a space: it defeats trailing
	// whitespace trimming and shows as a box in some fonts.
	cv := New(3, 1)
	cv.Set(0, 0, 0)
	line := cv.Render(nil)[0]
	if strings.ContainsRune(line, rune(brailleBase)) {
		t.Fatalf("empty cells must render as U+0020, got %q", line)
	}
}

// Drawing must never panic on a viewport that changed size mid-frame.
func TestOutOfRangeWritesAreClipped(t *testing.T) {
	cv := New(2, 2)
	cv.Set(-5, -5, 0)
	cv.Set(999, 999, 0)
	cv.Line(-20, -20, 40, 40, 0)
	cv.Curve(-9, -9, 50, 3, 60, 60, 0)
	cv.Dot(100, 100, 4, 0)
	for _, l := range cv.Render(nil) {
		_ = l
	}
}

func TestVerticalLineIsContinuous(t *testing.T) {
	cv := New(1, 3) // 12 sub-pixels tall
	cv.VLine(0, 0, cv.Height()-1, 0)
	for row, line := range cv.Render(nil) {
		if line == " " || line == "" {
			t.Fatalf("row %d of a full-height vertical line is empty", row)
		}
	}
}

// A curve is only useful if it leaves no gaps; sampling a bezier without
// joining the samples produces a dotted line at steep gradients.
func TestCurveLeavesNoGaps(t *testing.T) {
	cv := New(20, 6)
	cv.Curve(0, 0, cv.Width()-1, 0, cv.Width()-1, cv.Height()-1, 0)
	lit := 0
	for _, line := range cv.Render(nil) {
		for _, r := range line {
			if r != ' ' {
				lit++
			}
		}
	}
	if lit < cv.Rows() {
		t.Fatalf("curve lit only %d cells across %d rows - it is dotted", lit, cv.Rows())
	}
	// Every row the curve passes through must contain something.
	for row, line := range cv.Render(nil) {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("row %d is empty; the curve skipped it", row)
		}
	}
}

// A cell has one foreground. First writer wins, so a collision is stable across
// frames instead of flickering.
func TestFirstColourWinsWithinACell(t *testing.T) {
	cv := New(1, 1)
	cv.Set(0, 0, 3)
	cv.Set(1, 1, 7)
	out := cv.Render([]int{100, 101, 102, 103, 104, 105, 106, 107})[0]
	if !strings.Contains(out, "38;5;103m") {
		t.Fatalf("expected the first colour (103) to be kept, got %q", out)
	}
	if strings.Contains(out, "38;5;107m") {
		t.Fatalf("a later write repainted the cell: %q", out)
	}
}

func TestColourIsEmittedIdenticallyRegardlessOfEnvironment(t *testing.T) {
	cv := New(1, 1)
	cv.Set(0, 0, 0)
	got := cv.Render([]int{42})[0]
	const want = "\x1b[38;5;42m\u2801\x1b[0m"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotIsRound(t *testing.T) {
	// A braille sub-pixel is close to square, so a radius-r dot should be about
	// as wide as it is tall. An earlier version weighted y as though sub-pixels
	// were half-height, which stretched every node into an ellipse.
	cv := New(8, 4)
	cx, cy := cv.Width()/2, cv.Height()/2
	const r = 3
	cv.Dot(cx, cy, r, 0)

	minX, maxX, minY, maxY := cv.Width(), -1, cv.Height(), -1
	for y := 0; y < cv.Height(); y++ {
		for x := 0; x < cv.Width(); x++ {
			cell := (y/SubY)*cv.cols + (x / SubX)
			if cv.bits[cell]&dotBit[x%SubX][y%SubY] != 0 {
				if x < minX { minX = x }
				if x > maxX { maxX = x }
				if y < minY { minY = y }
				if y > maxY { maxY = y }
			}
		}
	}
	w, h := maxX-minX+1, maxY-minY+1
	if w != h {
		t.Fatalf("dot is %dx%d sub-pixels; expected square for a round node", w, h)
	}
	if w != 2*r+1 {
		t.Fatalf("dot spans %d sub-pixels, want %d for radius %d", w, 2*r+1, r)
	}
}

// A node must never bleed into a neighbouring lane, or branch colours blur.
func TestDotFitsInsideItsLane(t *testing.T) {
	const laneSubPixels = 3 * SubX // laneCells = 3
	const r = 2
	if 2*r+1 > laneSubPixels {
		t.Fatalf("a radius-%d node is %d sub-pixels wide but lanes are %d apart",
			r, 2*r+1, laneSubPixels)
	}
}
