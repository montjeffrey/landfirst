package tui

import (
	"strings"
	"testing"
)

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
