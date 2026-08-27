package tui

import (
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/model"
)

// A gutter of inconsistent width makes the text beside it jitter as the graph
// updates, which is the single most distracting thing a live graph can do.
func TestEveryGutterIsTheSameWidth(t *testing.T) {
	g := &model.Graph{LaneCount: 4}
	rows := []GraphRow{
		{Kind: "commit", Cells: []rune{dot, vert, blank, blank}, Colors: []int{0, 1, -1, -1}},
		{Kind: "link", Cells: []rune{teeR, horiz, cornerDL, blank}, Colors: []int{0, 0, 0, -1}},
		{Kind: "link", Cells: []rune{cornerDR, horiz, teeL, blank}, Colors: []int{2, 2, 2, -1}},
		{Kind: "commit", Cells: []rune{blank, blank, ring, vert}, Colors: []int{-1, -1, 3, 1}},
	}
	want := GutterWidth(g.LaneCount)
	for i, r := range rows {
		got := len([]rune(Gutter(r, g.LaneCount, false)))
		if got != want {
			t.Fatalf("row %d gutter width %d, want %d: %q", i, got, want, Gutter(r, g.LaneCount, false))
		}
	}
}

// A right-to-left merge must draw an unbroken line. This was the bug: the
// bridge test only accepted left-to-right shapes, so these rendered with a gap.
func TestHorizontalRunsAreUnbrokenInBothDirections(t *testing.T) {
	cases := []struct {
		name  string
		cells []rune
	}{
		{"left to right", []rune{teeR, horiz, cornerDL}},
		{"right to left", []rune{cornerDR, horiz, teeL}},
	}
	for _, c := range cases {
		row := GraphRow{Kind: "link", Cells: c.cells, Colors: []int{0, 0, 0}}
		got := Gutter(row, 3, false)
		if strings.Contains(got, " ") {
			t.Fatalf("%s: horizontal run broken by a gap: %q", c.name, got)
		}
	}
}

func TestGutterCarriesNoAnsiWhenColourIsOff(t *testing.T) {
	row := GraphRow{Kind: "commit", Cells: []rune{dot, vert}, Colors: []int{0, 1}}
	if strings.Contains(Gutter(row, 2, false), "\x1b") {
		t.Fatal("colour leaked into the plain renderer")
	}
}

// Slicing a styled line by byte offset is the classic way to emit half an
// escape sequence and corrupt everything after it on screen.
func TestSliceCellsNeverCutsAnEscapeSequence(t *testing.T) {
	line := "\x1b[38;5;39m⠁\x1b[0m\x1b[38;5;78m⠂\x1b[0m⠄"
	for from := 0; from < 4; from++ {
		for count := 1; count <= 4; count++ {
			got := sliceCells(line, from, count)
			// Every escape that starts must also terminate.
			opens := strings.Count(got, "\x1b[")
			closes := strings.Count(got, "m")
			if opens > closes {
				t.Fatalf("from=%d count=%d produced a truncated escape: %q", from, count, got)
			}
		}
	}
}

func TestSliceCellsCountsPrintableCellsNotBytes(t *testing.T) {
	// Three braille cells, each 3 bytes, wrapped in colour.
	line := "\x1b[38;5;39m⠁⠂⠄\x1b[0m"
	got := sliceCells(line, 0, 2)
	printable := 0
	for _, r := range got {
		if r != 0x1b && r != '[' && r != 'm' && !(r >= '0' && r <= '9') && r != ';' {
			printable++
		}
	}
	if printable != 2 {
		t.Fatalf("expected 2 printable cells, got %d from %q", printable, got)
	}
}

func TestSliceCellsHandlesAnEmptyWindow(t *testing.T) {
	if got := sliceCells("\x1b[38;5;39m⠁\x1b[0m", 0, 0); got != "" {
		t.Fatalf("a zero-width window should be empty, got %q", got)
	}
	if got := sliceCells("", 5, 5); got != "" {
		t.Fatalf("slicing an empty line should be empty, got %q", got)
	}
}
