package tui

import (
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// L5: the stale-revision check only protects the user if it carries the
// revision they were looking at when the prompt was built. A refresh landing
// while they read the prompt must not upgrade it to the newer revision.
func TestConfirmedActionCarriesTheRevisionOfThePrompt(t *testing.T) {
	eng := &fakeEngine{executeResult: &protocol.ExecuteResult{}}
	a := New(eng)
	defer a.watchCancel()
	a.snap = &protocol.Snapshot{Revision: "r7"}

	a.enterNode(&protocol.DecisionNode{Action: &protocol.Action{ID: "x", Argv: []string{"push", "origin", "b"}, Class: protocol.ClassSafe}})
	if a.confirm == nil {
		t.Fatal("entering an executable leaf must open the confirm bar")
	}
	a.snap = &protocol.Snapshot{Revision: "r8"} // a refresh arrives while the prompt is open
	_, cmd := a.Update(keyRune('y'))
	run(cmd)

	if got := eng.executeReq.Revision; got != "r7" {
		t.Fatalf("Execute got revision %q, want r7 from when the prompt was built", got)
	}
}

// An open decision tree knows which snapshot its actions came from.
func TestConfirmedActionPrefersTheTreesRevision(t *testing.T) {
	eng := &fakeEngine{executeResult: &protocol.ExecuteResult{}}
	a := New(eng)
	defer a.watchCancel()
	a.snap = &protocol.Snapshot{Revision: "r8"}
	a.tree = &protocol.DecisionTree{Revision: "r5"}

	a.enterNode(&protocol.DecisionNode{Action: &protocol.Action{ID: "x", Argv: []string{"push", "origin", "b"}, Class: protocol.ClassSafe}})
	_, cmd := a.Update(keyRune('y'))
	run(cmd)

	if got := eng.executeReq.Revision; got != "r5" {
		t.Fatalf("Execute got revision %q, want the tree's r5", got)
	}
}
