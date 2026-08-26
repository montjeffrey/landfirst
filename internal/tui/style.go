// Package tui is the app panel. It is built to live docked inside an editor,
// so it assumes a short, wide viewport and never depends on colour alone to
// carry meaning.
package tui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/montjeffrey/landfirst/internal/model"
)

var (
	cBad   = lipgloss.Color("203")
	cWarn  = lipgloss.Color("179")
	cOK    = lipgloss.Color("78")
	cInfo  = lipgloss.Color("39")
	cDim   = lipgloss.Color("244")
	cFaint = lipgloss.Color("240")
	cText  = lipgloss.Color("252")

	styTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styDim      = lipgloss.NewStyle().Foreground(cDim)
	styFaint    = lipgloss.NewStyle().Foreground(cFaint)
	styText     = lipgloss.NewStyle().Foreground(cText)
	styTabOn    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("238")).Padding(0, 1)
	styTabOff   = lipgloss.NewStyle().Foreground(cDim).Padding(0, 1)
	stySelected = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	styKey      = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	styBanner   = lipgloss.NewStyle().Bold(true)
)

// tone maps a verdict to the colour used for it everywhere in the app.
func tone(v model.Verdict) lipgloss.Color {
	switch model.Verdicts[v].Rank {
	case 0, 1, 2:
		return cBad
	case 3, 4, 5:
		return cWarn
	case 6:
		return cInfo
	case 7:
		return cDim
	}
	return cOK
}

// glyph carries the same information as the colour, for terminals and readers
// where colour is not available.
func glyph(v model.Verdict) string {
	switch model.Verdicts[v].Rank {
	case 0, 1, 2:
		return "!"
	case 3, 4, 5:
		return "~"
	case 6:
		return "-"
	case 7:
		return "."
	}
	return "+"
}
