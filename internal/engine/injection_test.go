package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/protocol"
)

// optionBranch creates a local branch whose name git would read as an option
// if it ever reached argv bare, tracks a remote branch of the same name, and
// leaves it one commit ahead - the exact state that makes landfirst offer a
// "safe" push. git branch refuses such a name, so it is made with update-ref.
func (f *efixture) optionBranch(name string) {
	f.t.Helper()
	ref := "refs/heads/" + name
	f.run("push", "-q", "origin", "main:"+ref)
	f.run("fetch", "-q", "origin")
	f.run("update-ref", ref, "main")
	f.run("config", "branch."+name+".remote", "origin")
	f.run("config", "branch."+name+".merge", ref)
	f.run("symbolic-ref", "HEAD", ref)
	f.commit("opt.txt", "local work", "local commit on the option-named branch")
	f.run("switch", "-q", "main")
}

// TestOptionLikeBranchNameNeverBecomesAnExecutableCommand reproduces audit
// finding H1: the branch below is a legal ref name, and the push landfirst
// built for it started with "--receive-pack=<cmd>", which git runs through a
// shell. Before the fix, confirming the "safe" push created the marker file.
func TestOptionLikeBranchNameNeverBecomesAnExecutableCommand(t *testing.T) {
	f := newEfixture(t)
	const marker = "PWNED_E2E"
	name := "--receive-pack=touch${IFS}" + marker + ";#"
	f.optionBranch(name)

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx := context.Background()
	snap, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFull})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var unit *protocol.Unit
	for i := range snap.Units {
		if snap.Units[i].Branch == name {
			unit = &snap.Units[i]
		}
	}
	if unit == nil {
		t.Fatalf("option-named branch missing from snapshot: %+v", snap.Units)
	}
	if unit.Verdict != "unpushed" {
		t.Fatalf("test setup: verdict = %q, want unpushed", unit.Verdict)
	}
	tree, err := eng.Decide(ctx, unit.ID)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	var offered []*protocol.Action
	walkActions(tree.Root, func(a *protocol.Action) {
		offered = append(offered, a)
		// Press "y" on whatever is offered, exactly as the TUI would.
		sum := sha256.Sum256([]byte(strings.Join(a.Argv, "\x00")))
		if _, err := eng.Execute(ctx, protocol.ExecuteRequest{
			ActionID: a.ID, Revision: tree.Revision, Confirmation: hex.EncodeToString(sum[:]),
		}); err != nil {
			t.Fatalf("Execute(%s): %v", a.ID, err)
		}
	})
	if _, err := os.Stat(filepath.Join(f.dir, marker)); err == nil {
		t.Fatalf("%s exists: the branch name was executed as a command", marker)
	}
	for _, a := range offered {
		if a.Class != protocol.ClassManual {
			t.Fatalf("action %s for an option-like branch name must not be executable, got class %s: %v", a.ID, a.Class, a.Argv)
		}
		for _, arg := range a.Argv {
			if strings.HasPrefix(arg, "-") {
				t.Fatalf("action %s offers the option-like argument %q: %v", a.ID, arg, a.Argv)
			}
		}
	}
	flagged := false
	for _, r := range unit.Risks {
		if r.Kind == "name" {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("a branch name that looks like an option must carry a name risk, got %+v", unit.Risks)
	}
}

// TestPushUsesFullRefnamesAndSucceedsForOddButValidNames guards the other
// half: the fix must not break names git allows, such as ones with $ ; #.
func TestPushUsesFullRefnamesAndSucceedsForOddButValidNames(t *testing.T) {
	f := newEfixture(t)
	name := "feat/odd$name;x#1"
	f.run("checkout", "-q", "-b", name)
	f.commit("odd.txt", "work", "odd name")
	f.run("checkout", "-q", "main")

	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()
	ctx := context.Background()
	tree, err := eng.Decide(ctx, "branch:"+name)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	a := singleLeaf(t, tree)
	want := []string{"push", "-u", "--", "origin", "refs/heads/" + name + ":refs/heads/" + name}
	if !equalArgv(a.Argv, want) {
		t.Fatalf("Argv = %v, want %v", a.Argv, want)
	}
	sum := sha256.Sum256([]byte(strings.Join(a.Argv, "\x00")))
	res, err := eng.Execute(ctx, protocol.ExecuteRequest{ActionID: a.ID, Revision: tree.Revision, Confirmation: hex.EncodeToString(sum[:])})
	if err != nil || !res.Ran || res.ExitCode != 0 {
		t.Fatalf("push of a valid but unusual name failed: res=%+v err=%v", res, err)
	}
}
