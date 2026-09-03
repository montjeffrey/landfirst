package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/montjeffrey/landfirst/internal/git"
)

// Fixtures are real repositories. The whole value of this tool is reading git
// correctly, so the tests have to read real git.
type fixture struct {
	t      *testing.T
	dir    string
	remote string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, dir: filepath.Join(dir, "repo"), remote: filepath.Join(dir, "remote.git")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.run("init", "-q", "-b", "main")
	f.run("config", "user.email", "t@example.invalid")
	f.run("config", "user.name", "Test")
	f.git(dir, "init", "-q", "--bare", f.remote)
	f.run("remote", "add", "origin", f.remote)
	f.commit("a.txt", "base", "init")
	f.run("push", "-q", "-u", "origin", "main")
	return f
}

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Isolate from the developer's own git config so a personal setting cannot
	// change a test result.
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".noglobal"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".nosystem"),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *fixture) run(args ...string) string { return f.git(f.dir, args...) }

func (f *fixture) commit(name, body, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.run("add", "-A")
	f.run("commit", "-q", "--no-gpg-sign", "-m", msg)
}

func (f *fixture) build() *Model {
	f.t.Helper()
	repo, err := git.Open(f.dir)
	if err != nil {
		f.t.Fatal(err)
	}
	return Build(repo, Options{Probe: false})
}

func verdictOf(m *Model, branch string) Verdict {
	for _, u := range m.Units {
		if u.Branch == branch {
			return u.Verdict
		}
	}
	return Verdict("<missing>")
}

func TestSquashMergedBranchReadsAsLandedNotOrphaned(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/squashed")
	f.commit("s.txt", "squashed", "squashed work")
	f.run("push", "-q", "-u", "origin", "feat/squashed")
	f.run("checkout", "-q", "main")
	f.run("merge", "-q", "--squash", "feat/squashed")
	f.run("commit", "-q", "--no-gpg-sign", "-m", "squash merge")
	f.run("push", "-q", "origin", "main")
	f.run("push", "-q", "origin", "--delete", "feat/squashed")
	f.run("fetch", "-q", "--prune")

	// The squash changed the hash, so comparing hashes says "15 commits lost".
	// Comparing patch ids says "already there" - which is the truth, and the
	// difference between a useful board and one that cries wolf.
	if got := verdictOf(f.build(), "feat/squashed"); got != SafeToDelete {
		t.Fatalf("squash-merged branch should be %q, got %q", SafeToDelete, got)
	}
}

func TestTrulyOrphanedBranchIsFlagged(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/orphan")
	f.commit("o.txt", "orphan", "orphan work")
	f.run("push", "-q", "-u", "origin", "feat/orphan")
	f.run("push", "-q", "origin", "--delete", "feat/orphan")
	f.run("fetch", "-q", "--prune")
	f.run("checkout", "-q", "main")

	m := f.build()
	if got := verdictOf(m, "feat/orphan"); got != Orphaned {
		t.Fatalf("expected %q, got %q", Orphaned, got)
	}
	if m.AtRisk == 0 {
		t.Fatal("an orphaned branch must count toward the at-risk headline")
	}
}

func TestBranchTrackingTheWrongRemoteIsFlagged(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/mistracked")
	f.commit("w.txt", "work", "work")
	f.run("branch", "--set-upstream-to=origin/main", "feat/mistracked")
	f.run("checkout", "-q", "main")

	if got := verdictOf(f.build(), "feat/mistracked"); got != WrongUpstream {
		t.Fatalf("expected %q, got %q - pushing from this branch publishes nothing", WrongUpstream, got)
	}
}

func TestNeverPushedBranchIsFlagged(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/local-only")
	f.commit("n.txt", "local", "never pushed")
	f.run("checkout", "-q", "main")

	if got := verdictOf(f.build(), "feat/local-only"); got != NeverPushed {
		t.Fatalf("expected %q, got %q", NeverPushed, got)
	}
}

func TestRemedyIsNeverUnsafeForAutomaticActions(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/diverge")
	f.commit("d.txt", "local", "local side")
	f.run("push", "-q", "-u", "origin", "feat/diverge")
	f.run("commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "another local")
	f.run("checkout", "-q", "main")

	for _, u := range f.build().Units {
		if u.Remedy == nil || !u.Remedy.Safe {
			continue
		}
		// Anything offered as a one-key action must only add to the remote.
		switch u.Remedy.Args[0] {
		case "push", "branch":
		default:
			t.Fatalf("%s offers a safe action that is not push/branch: %v", u.Branch, u.Remedy.Args)
		}
		for _, a := range u.Remedy.Args {
			if a == "--force" || a == "-f" || a == "--force-with-lease" {
				t.Fatalf("%s offers a force operation as a safe action: %v", u.Branch, u.Remedy.Args)
			}
		}
	}
}

// TestNoRemoteConfiguredOffersNoAutoPushRemedy guards a bug a review caught:
// git.Open leaves Remote == "" for a legitimate local-only repo rather than
// guessing "origin" - but remedy() built its push args unconditionally, so a
// NeverPushed branch there got a Safe:true remedy whose argv was literally
// "push -u ” branch:branch". One keypress away from actually running it.
func TestNoRemoteConfiguredOffersNoAutoPushRemedy(t *testing.T) {
	dir := t.TempDir()
	f := &fixture{t: t, dir: filepath.Join(dir, "repo")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.run("init", "-q", "-b", "main")
	f.run("config", "user.email", "t@example.invalid")
	f.run("config", "user.name", "Test")
	f.commit("a.txt", "base", "init") // no remote added anywhere
	f.run("checkout", "-q", "-b", "feat/local-only")
	f.commit("n.txt", "local", "never pushed, no remote configured at all")
	f.run("checkout", "-q", "main")

	m := f.build()
	if m.Remote != "" {
		t.Fatalf("test setup: expected an empty Remote, got %q", m.Remote)
	}
	for _, u := range m.Units {
		if u.Branch != "feat/local-only" {
			continue
		}
		if u.Verdict != NeverPushed {
			t.Fatalf("verdict = %q, want %q", u.Verdict, NeverPushed)
		}
		if u.Remedy != nil {
			t.Fatalf("a repo with no remote must not offer a push remedy, got %+v", u.Remedy)
		}
		return
	}
	t.Fatal("feat/local-only not found in units")
}

func TestWorktreeLocationRisksAreDetected(t *testing.T) {
	cases := []struct{ path, want string }{
		{"C:/tmp/des-crm-thing", "volatile"},
		{"/home/u/OneDrive - corp/repo", "cloud"},
	}
	for _, c := range cases {
		risks := LocationRisk(c.path, "/some/root")
		found := false
		for _, r := range risks {
			if r.Kind == c.want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s should be flagged %q, got %v", c.path, c.want, risks)
		}
	}
	nested := LocationRisk("/repo/.claude/worktrees/x", "/repo")
	if len(nested) == 0 || nested[len(nested)-1].Kind != "nested" {
		t.Fatalf("a worktree inside the main worktree should be flagged nested, got %v", nested)
	}
}
