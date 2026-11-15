package tui

import (
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// L5: the stale-revision check only protects the user if it carries the
// revision they saw when they confirmed. A refresh landing between the confirm
// and the command running must not upgrade it to the newer revision.
func TestConfirmedActionCarriesTheRevisionTheUserSaw(t *testing.T) {
	eng := &fakeEngine{executeResult: &protocol.ExecuteResult{}}
	a := New(eng)
	defer a.watchCancel()
	a.snap = &protocol.Snapshot{Revision: "r7"}

	cmd := a.runAction(&protocol.Action{ID: "x", Argv: []string{"push", "origin", "b"}})
	a.snap = &protocol.Snapshot{Revision: "r8"} // a refresh arrives before the command runs
	cmd()

	if got := eng.executeReq.Revision; got != "r7" {
		t.Fatalf("Execute got revision %q, want the confirmed r7", got)
	}
}
