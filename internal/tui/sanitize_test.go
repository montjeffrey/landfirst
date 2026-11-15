package tui

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// hostile holds the payloads from audit finding H2: OSC title, OSC 52
// clipboard write, CSI clear screen, a C1 CSI, DEL, a bidi override and a
// zero-width character.
const hostile = "fix typo \x1b]0;pwned\x07\x1b]52;c;ZWNobyBwd25lZA==\x07\x1b[2J\u009b31m\x7f\u202eevil\u200b end\r\nnext"

// sgr matches the colour sequences landfirst itself emits (the graph canvas
// colours cells even under test); anything else escape-shaped is hostile.
var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// assertTerminalSafe fails if out still holds anything a terminal could act
// on once landfirst's own colour sequences are set aside. A hostile payload
// that survived as a bare "ESC ]" or "ESC [2J" is caught either way.
func assertTerminalSafe(t *testing.T, label, out string) {
	t.Helper()
	for _, bad := range []string{"\x1b]", "\x1b[2J", "\x07"} {
		if strings.Contains(out, bad) {
			t.Fatalf("%s: hostile sequence %q survived:\n%q", label, bad, out)
		}
	}
	out = sgr.ReplaceAllString(out, "")
	if !utf8.ValidString(out) {
		t.Fatalf("%s: invalid UTF-8 in output", label)
	}
	for _, r := range out {
		if r == '\n' {
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("%s: output holds control or format character U+%04X:\n%q", label, r, out)
		}
	}
}

func TestSanitizeStripsEscapeSequencesAndFormatControls(t *testing.T) {
	got := Sanitize(hostile)
	assertTerminalSafe(t, "Sanitize", got)
	if strings.ContainsRune(got, '\n') {
		t.Fatalf("Sanitize must keep a value on one row, got %q", got)
	}
	if !strings.Contains(got, "fix typo") || !strings.Contains(got, "end") {
		t.Fatalf("Sanitize dropped ordinary text: %q", got)
	}
}

func TestSanitizeLeavesOrdinaryTextAlone(t *testing.T) {
	for _, s := range []string{"", "feat/a", "日本語 branch", "ünï2", "fix: handle a → b", "emoji 🚀"} {
		if got := Sanitize(s); got != s {
			t.Fatalf("Sanitize(%q) = %q, want it unchanged", s, got)
		}
	}
}

func TestSanitizeReplacesInvalidUTF8(t *testing.T) {
	got := Sanitize("a\xffb")
	if got != "a?b" {
		t.Fatalf("got %q", got)
	}
}

func TestPlainReportSanitizesEveryRepoDerivedString(t *testing.T) {
	snap := &protocol.Snapshot{
		Repo: protocol.RepoInfo{Name: "re" + hostile, Base: "ma" + hostile, GitVersion: "2" + hostile, ConflictMode: "full"},
		Units: []protocol.Unit{
			{Branch: "feat/" + hostile, Verdict: "never-pushed", Detail: "git said: " + hostile},
			{Head: "abc" + hostile, Verdict: "detached", Detail: "d" + hostile},
		},
		Plan: []protocol.PlanItem{{Order: 1, Branch: "b" + hostile, Conflicts: 0}},
	}
	snap.Units[0].Suggested = &protocol.Action{Argv: []string{"push", "feat/" + hostile}}
	assertTerminalSafe(t, "PlainReport", PlainReport(snap))
}

func TestViewSanitizesCommitTextStatusAndPrompts(t *testing.T) {
	a := hostileApp()
	a.w, a.h = 120, 40

	for _, tab := range []tab{tabTopology, tabGraph} {
		a.tab = tab
		a.cursor = 0
		assertTerminalSafe(t, tabNames[tab], a.View())
		a.cursor = 1
		assertTerminalSafe(t, tabNames[tab]+" (selected)", a.View())
	}

	a.tab = tabTopology
	a.status = "failed: git push: " + hostile
	assertTerminalSafe(t, "status line", a.View())

	a.confirm = &pendingConfirm{kind: confirmWorktreeRemove, wt: &protocol.Worktree{Path: "C:/wt/" + hostile}}
	assertTerminalSafe(t, "confirm prompt", a.View())

	a.confirm = nil
	a.node = &protocol.DecisionNode{Action: &protocol.Action{
		Label: "l" + hostile, Class: protocol.ClassManual, Cwd: "/w" + hostile, Argv: []string{"rebase", "x" + hostile},
	}}
	assertTerminalSafe(t, "manual leaf", a.View())
	a.node = &protocol.DecisionNode{Question: "q" + hostile, Options: []protocol.DecisionEdge{{Answer: "a" + hostile}}}
	a.tree = &protocol.DecisionTree{}
	assertTerminalSafe(t, "question", a.View())
}

func hostileApp() *App {
	a := &App{density: Comfortable}
	a.snap = &protocol.Snapshot{
		Repo: protocol.RepoInfo{Name: "repo" + hostile, Base: "main" + hostile},
		Units: []protocol.Unit{
			{ID: "branch:x", Branch: "feat/" + hostile, Head: "abc1234", Unlanded: 3, Verdict: "never-pushed",
				Worktrees: []string{"wt:1"}, Tip: protocol.CommitMeta{Subject: hostile, Author: hostile}},
		},
		Worktrees:   []protocol.Worktree{{ID: "wt:1", Path: "C:/wt/" + hostile, Op: "none"}},
		Forks:       []protocol.ForkPoint{{SHA: "abc1234", Short: "abc1234", Subject: hostile, Behind: 2, Branches: []string{"feat/" + hostile}}},
		Contentions: []protocol.Contention{{A: "feat/" + hostile, B: "other" + hostile, Real: 2, Files: []string{"dir/" + hostile}}},
		Summary:     protocol.Summary{AtRisk: 1, StrandedCommits: 3},
	}
	a.graph = &protocol.Graph{
		Commits: []protocol.Commit{
			{Hash: "abc1234", Short: "abc1234", Subject: hostile, Author: hostile, Refs: []string{"HEAD -> main" + hostile, "tag: v1" + hostile, "origin/x" + hostile}},
			{Hash: "def5678", Short: "def5678", Subject: hostile, Author: hostile, Lane: 0, Row: 1},
		},
		LaneCount: 1, Total: 2,
	}
	return a
}

func TestEncodeJSONIsFaithfulButHasNoRawControlBytes(t *testing.T) {
	snap := &protocol.Snapshot{
		Repo:  protocol.RepoInfo{Name: "r", Base: "main"},
		Units: []protocol.Unit{{Branch: "feat/a", Verdict: "landed", Detail: hostile, Tip: protocol.CommitMeta{Subject: hostile}}},
	}
	out, err := EncodeJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range string(out) {
		if r == '\n' || r == ' ' {
			continue
		}
		if unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r) {
			t.Fatalf("raw control or format character U+%04X in -json output:\n%q", r, out)
		}
	}
	var back protocol.Snapshot
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if back.Units[0].Detail != hostile || back.Units[0].Tip.Subject != hostile {
		t.Fatalf("-json must stay faithful data: got %q", back.Units[0].Detail)
	}
}
