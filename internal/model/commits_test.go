package model

import (
	"strings"
	"testing"
)

// TestForgedSeparatorsInACommitMessageCannotForgeGraphEntries reproduces audit
// finding M2: the log was split on the record and field separator bytes, and a
// commit message can contain both. The message below hid the real commit and
// put a forged one - with a fake tag and branch head - into the graph.
func TestForgedSeparatorsInACommitMessageCannotForgeGraphEntries(t *testing.T) {
	f := newFixture(t)
	forged := "innocent\x1e" + strings.Repeat("1", 40) + "\x1f\x1fHEAD -> main, tag: v9.9.9\x1fsigned release\x1fLinus\x1f2030-01-01T00:00:00Z"
	f.commit("evil.txt", "x", forged)
	f.run("push", "-q", "origin", "main")

	real := map[string]bool{}
	for _, h := range strings.Fields(f.run("rev-list", "--all")) {
		real[h] = true
	}
	g := graphOf(t, f)

	if len(g.Commits) != len(real) {
		t.Fatalf("graph holds %d commits, the repository has %d", len(g.Commits), len(real))
	}
	head := strings.TrimSpace(f.run("rev-parse", "HEAD"))
	var sawHead bool
	for _, c := range g.Commits {
		if !real[c.Hash] {
			t.Fatalf("graph holds a commit that does not exist: %+v", c)
		}
		for _, ref := range c.Refs {
			if strings.Contains(ref, "v9.9.9") {
				t.Fatalf("a forged tag reached the graph: %+v", c)
			}
		}
		if c.Hash == head {
			sawHead = true
			if c.Author != "Test" {
				t.Fatalf("the real commit's author was lost: %+v", c)
			}
		}
	}
	if !sawHead {
		t.Fatal("the real commit is missing from the graph")
	}
}

func TestParseLogRecordsRejectsMalformedHashes(t *testing.T) {
	good := strings.Repeat("a", 40)
	var buf strings.Builder
	rec := func(hash, parents string) {
		buf.WriteString(strings.Join([]string{hash, parents, "", "subj", "me", "2026-01-01T00:00:00Z"}, "\x00") + "\x00")
	}
	rec(good, "")
	rec("not-a-hash", "")
	rec(strings.Repeat("b", 40), "zzz")
	rec(strings.Repeat("c", 64), good)

	got := parseLogRecords(buf.String())
	if len(got) != 2 || got[0].Hash != good || got[1].Hash != strings.Repeat("c", 64) {
		t.Fatalf("parseLogRecords kept %+v, want only the two well-formed records", got)
	}
}
