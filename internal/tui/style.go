// Package tui is the app panel. It is built to live docked inside an editor,
// so it assumes a short, wide viewport and never depends on colour alone to
// carry meaning.
package tui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	cBad   = lipgloss.Color("203")
	cWarn  = lipgloss.Color("179")
	cOK    = lipgloss.Color("78")
	cInfo  = lipgloss.Color("39")
	cDim   = lipgloss.Color("244")
	cFaint = lipgloss.Color("240")
	cText  = lipgloss.Color("252")

	styTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	styDim    = lipgloss.NewStyle().Foreground(cDim)
	styFaint  = lipgloss.NewStyle().Foreground(cFaint)
	styText   = lipgloss.NewStyle().Foreground(cText)
	styTabOn  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("238")).Padding(0, 1)
	styTabOff = lipgloss.NewStyle().Foreground(cDim).Padding(0, 1)
	styKey    = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	styBanner = lipgloss.NewStyle().Bold(true)
)

// verdictSeverity buckets a wire verdict string (protocol.Unit.Verdict) into
// how urgently it needs attention. Mirrors internal/model.Verdict.Severity()'s
// own bucketing (internal/model/types.go) - duplicated rather than imported,
// because tui sits behind the protocol boundary and only ever sees the
// string protocol.Unit.Verdict already carries (internal/protocol's own doc:
// "the only thing an adapter imports"). Same tradeoff internal/engine's own
// baseRefOf makes against internal/model's baseRef: one small duplicated
// mapping is cheaper than opening a seam across the boundary for it.
type verdictSeverity int

const (
	sevBad  verdictSeverity = iota // blocks progress or risks losing work outright
	sevWarn                        // needs a decision, nothing lost yet
	sevInfo                        // routine cleanup
	sevDim                         // no local work, just behind
	sevOK                          // nothing to do
)

var severityByVerdict = map[string]verdictSeverity{
	"in-progress":    sevBad,
	"orphaned":       sevBad,
	"wrong-upstream": sevBad,
	"never-pushed":   sevBad,
	"diverged":       sevWarn,
	"unpushed":       sevWarn,
	"detached":       sevWarn,
	"safe-to-delete": sevInfo,
	"behind":         sevDim,
	"landed":         sevOK,
}

func severityOf(verdict string) verdictSeverity {
	if s, ok := severityByVerdict[verdict]; ok {
		return s
	}
	return sevOK
}

// verdictMark is the glyph and colour used for a verdict everywhere in the
// app - a terminal without colour still reads the glyph, so meaning never
// depends on colour alone.
func verdictMark(verdict string) (string, lipgloss.Color) {
	switch severityOf(verdict) {
	case sevBad:
		return "!", cBad
	case sevWarn:
		return "~", cWarn
	case sevInfo:
		return "-", cInfo
	case sevDim:
		return ".", cDim
	}
	return "·", cOK
}
