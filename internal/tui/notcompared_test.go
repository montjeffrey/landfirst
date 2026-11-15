package tui

import (
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

func TestPlainReportSaysWhatWasNotCompared(t *testing.T) {
	snap := &protocol.Snapshot{
		Repo:    protocol.RepoInfo{Name: "r", Base: "main", ConflictMode: "full"},
		Summary: protocol.Summary{PairsSkipped: 7, MaxPairs: 5},
		Plan: []protocol.PlanItem{
			{Order: 1, Branch: "b1", Conflicts: 0, NotCompared: 2},
			{Order: 2, Branch: "b2", Conflicts: 1},
		},
	}
	out := PlainReport(snap)
	for _, want := range []string{"-max-pairs 5", "7 pair(s)", "not compared with 2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "not compared with") != 1 {
		t.Fatalf("b2 was fully compared and must not carry the marker:\n%s", out)
	}
}

func TestStatusLineWarnsWhenMaxPairsTruncated(t *testing.T) {
	if got := truncationNote(protocol.Summary{PairsSkipped: 7, MaxPairs: 5}); !strings.Contains(got, "7") || !strings.Contains(got, "-max-pairs 5") {
		t.Fatalf("got %q", got)
	}
	if got := truncationNote(protocol.Summary{}); got != "" {
		t.Fatalf("no truncation, no note: got %q", got)
	}
}

func TestEveryBranchThatWasNotComparedCarriesAMarker(t *testing.T) {
	snap := &protocol.Snapshot{
		Repo:  protocol.RepoInfo{Name: "r", Base: "main", ConflictMode: "full"},
		Units: []protocol.Unit{{Branch: "b1", Verdict: "unpushed"}, {Branch: "b2", Verdict: "unpushed"}, {Branch: "b3", Verdict: "unpushed"}},
		Plan: []protocol.PlanItem{
			{Order: 1, Branch: "b1", Conflicts: 0, NotCompared: 2},
			{Order: 2, Branch: "b2", Conflicts: 0},
			{Order: 3, Branch: "b3", Conflicts: 0, NotCompared: 2, Unreadable: true},
		},
	}
	if got := notComparedText(snap, "b1"); !strings.Contains(got, "not compared with 2") {
		t.Fatalf("b1 marker = %q", got)
	}
	if got := notComparedText(snap, "b2"); got != "" {
		t.Fatalf("b2 was compared with everything, marker = %q", got)
	}
	if got := notComparedText(snap, "b3"); !strings.Contains(got, "could not read") {
		t.Fatalf("b3 marker = %q", got)
	}
	out := PlainReport(snap)
	if strings.Count(out, "      ? ") != 2 {
		t.Fatalf("want a marker line under b1 and b3 in the branch list:\n%s", out)
	}
}
