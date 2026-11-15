package model

import (
	"testing"
)

// optionBranch makes a local branch named like a git option with one commit
// that exists nowhere else. git branch refuses the name; update-ref does not.
func (f *fixture) optionBranch(name string) {
	f.t.Helper()
	f.run("checkout", "-q", "-b", "tmp-opt")
	f.commit("opt.txt", "unmerged work", "unmerged work")
	f.run("update-ref", "refs/heads/"+name, "refs/heads/tmp-opt")
	f.run("checkout", "-q", "main")
	f.run("branch", "-q", "-D", "tmp-opt")
}

// TestOptionNamedBranchIsReadAsARefNotAnOption reproduces H1's read path (and
// the audit's M1 scenario (a)): "git cherry main --output=pwned2" fails, the
// failure was read as "0 unlanded", and a branch holding unmerged work was
// listed as safe to delete.
func TestOptionNamedBranchIsReadAsARefNotAnOption(t *testing.T) {
	f := newFixture(t)
	f.optionBranch("--output=pwned2")

	m := f.build()
	if got := verdictOf(m, "--output=pwned2"); got != NeverPushed {
		t.Fatalf("a branch with unmerged work must be %q, got %q", NeverPushed, got)
	}
	for _, u := range m.Units {
		if u.Branch != "--output=pwned2" {
			continue
		}
		if u.Unlanded != 1 {
			t.Fatalf("Unlanded = %d, want 1", u.Unlanded)
		}
		if u.Remedy != nil {
			t.Fatalf("no remedy may be offered for an option-like name, got %+v", u.Remedy)
		}
		var flagged bool
		for _, r := range u.Risks {
			flagged = flagged || r.Kind == "name"
		}
		if !flagged {
			t.Fatalf("expected a name risk, got %+v", u.Risks)
		}
	}
}

// TestBranchNamedLikeACommitIsNotConfusedWithTheCommit is M1 scenario (b): a
// branch named with the full SHA of a commit on main made "git cherry main
// <sha>" resolve the object, not the branch, so unique work read as landed.
func TestBranchNamedLikeACommitIsNotConfusedWithTheCommit(t *testing.T) {
	f := newFixture(t)
	sha := f.run("rev-parse", "main")[:40]
	f.run("checkout", "-q", "-b", "tmp-sha")
	f.commit("sha.txt", "unique work", "unique work")
	f.run("update-ref", "refs/heads/"+sha, "refs/heads/tmp-sha")
	f.run("checkout", "-q", "main")
	f.run("branch", "-q", "-D", "tmp-sha")

	if got := verdictOf(f.build(), sha); got != NeverPushed {
		t.Fatalf("a SHA-named branch holding unique work must be %q, got %q", NeverPushed, got)
	}
}
