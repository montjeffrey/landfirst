package tui

import (
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

func TestPlainReportGroupsByVerdictAndOmitsSuggestedWhenNil(t *testing.T) {
	snap := &protocol.Snapshot{
		Repo: protocol.RepoInfo{Name: "myrepo", Base: "main", GitVersion: "2.43.0", ConflictMode: "full"},
		Units: []protocol.Unit{
			{Branch: "feat/a", Verdict: "never-pushed", Detail: "2 changes never pushed"},
			{Branch: "feat/b", Verdict: "never-pushed", Detail: "1 change never pushed", Worktrees: []string{"wt:1"}},
			{Branch: "feat/c", Verdict: "landed", Detail: "in sync"},
		},
		Summary: protocol.Summary{StrandedCommits: 3, AtRisk: 2},
	}
	out := PlainReport(snap)

	if !strings.Contains(out, "NEVER PUSHED - has changes the remote has never seen") {
		t.Fatalf("missing never-pushed group header:\n%s", out)
	}
	if strings.Count(out, "NEVER PUSHED") != 1 {
		t.Fatalf("group header must print once per run of the same verdict, not once per unit:\n%s", out)
	}
	if !strings.Contains(out, "LANDED - in sync with its remote") {
		t.Fatalf("missing landed group header:\n%s", out)
	}
	if !strings.Contains(out, "feat/b") || !strings.Contains(out, "[1 worktree(s)]") {
		t.Fatalf("missing worktree annotation for feat/b:\n%s", out)
	}
	if strings.Contains(out, "\n      git ") {
		t.Fatalf("no unit sets Suggested in this test, so no fix line should print:\n%s", out)
	}
	if !strings.Contains(out, "3 change(s) across 2 branch(es)") {
		t.Fatalf("missing the AtRisk banner:\n%s", out)
	}
}

func TestPlainReportPrintsSuggestedFixWhenPopulated(t *testing.T) {
	// Unit.Suggested is nil in every Snapshot the engine builds today (see
	// internal/engine/engine.go's toSnapshot) - this covers the line for
	// whenever that changes, per this package's own report-back note.
	snap := &protocol.Snapshot{
		Repo: protocol.RepoInfo{Name: "myrepo", Base: "main"},
		Units: []protocol.Unit{
			{Branch: "feat/a", Verdict: "never-pushed", Detail: "1 change never pushed",
				Suggested: &protocol.Action{Argv: []string{"push", "-u", "origin", "feat/a:feat/a"}}},
		},
	}
	out := PlainReport(snap)
	if !strings.Contains(out, "git push -u origin feat/a:feat/a") {
		t.Fatalf("expected the suggested fix line when Suggested is populated:\n%s", out)
	}
}

func TestPlainReportEverythingLandedBanner(t *testing.T) {
	snap := &protocol.Snapshot{Repo: protocol.RepoInfo{Name: "myrepo", Base: "main"}}
	out := PlainReport(snap)
	if !strings.Contains(out, "everything with local work has reached the remote") {
		t.Fatalf("missing the all-clear banner when AtRisk is zero:\n%s", out)
	}
}

func TestPlainReportPrintsMergeOrder(t *testing.T) {
	snap := &protocol.Snapshot{
		Repo: protocol.RepoInfo{Name: "myrepo", Base: "main"},
		Plan: []protocol.PlanItem{
			{Order: 1, Branch: "feat/a", Conflicts: 0},
			{Order: 2, Branch: "feat/b", Conflicts: -1},
		},
	}
	out := PlainReport(snap)
	if !strings.Contains(out, "MERGE ORDER") {
		t.Fatalf("missing MERGE ORDER section:\n%s", out)
	}
	if !strings.Contains(out, "0 conflicting branch(es)") {
		t.Fatalf("expected a probed conflict count for feat/a:\n%s", out)
	}
	if !strings.Contains(out, "not probed") {
		t.Fatalf("expected \"not probed\" for a Conflicts=-1 item (feat/b):\n%s", out)
	}
}
