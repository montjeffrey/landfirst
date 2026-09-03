package git

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gfixture is a minimal real-repo fixture for Open's resolution tests. Kept
// separate from internal/model's fixture because this is a different
// package - the whole value of this tool is reading git correctly, so the
// tests have to read real git.
type gfixture struct {
	t   *testing.T
	dir string
}

// newGfixture creates a repo with a single commit on branch and no remotes.
func newGfixture(t *testing.T, branch string) *gfixture {
	t.Helper()
	f := &gfixture{t: t, dir: filepath.Join(t.TempDir(), "repo")}
	if err := os.MkdirAll(f.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f.run("init", "-q", "-b", branch)
	f.run("config", "user.email", "t@example.invalid")
	f.run("config", "user.name", "Test")
	f.commit("a.txt", "base", "init")
	return f
}

func (f *gfixture) run(args ...string) string {
	f.t.Helper()
	return gitIn(f.t, f.dir, args...)
}

// gitIn runs git in dir, isolated from the developer's own git config so a
// personal setting (e.g. init.defaultBranch) cannot change a test result.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".noglobal"),
		"GIT_CONFIG_SYSTEM="+filepath.Join(dir, ".nosystem"),
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (f *gfixture) commit(name, body, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.dir, name), []byte(body+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.run("add", "-A")
	f.run("commit", "-q", "--no-gpg-sign", "-m", msg)
}

// addRemote creates a bare repo and wires it up as remote name.
func (f *gfixture) addRemote(name string) {
	f.t.Helper()
	bare := filepath.Join(f.t.TempDir(), name+".git")
	gitIn(f.t, filepath.Dir(bare), "init", "-q", "--bare", bare)
	f.run("remote", "add", name, bare)
}

func TestOpenResolvesBaseFromRemoteHEAD(t *testing.T) {
	f := newGfixture(t, "develop")
	f.addRemote("origin")
	f.run("push", "-q", "-u", "origin", "develop")
	f.run("fetch", "-q", "origin") // guarantee refs/remotes/origin/develop exists locally
	f.run("remote", "set-head", "origin", "develop")

	r, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r.Base != "develop" {
		t.Fatalf("Base = %q, want %q", r.Base, "develop")
	}
	if r.Remote != "origin" {
		t.Fatalf("Remote = %q, want %q", r.Remote, "origin")
	}
}

func TestOpenPrefersBranchRemoteConfigOverAlphabeticallyFirstRemote(t *testing.T) {
	f := newGfixture(t, "main")
	f.addRemote("fork") // alphabetically first - must NOT win
	f.addRemote("origin")
	f.run("config", "branch.main.remote", "origin")

	r, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r.Base != "main" {
		t.Fatalf("Base = %q, want %q", r.Base, "main")
	}
	if r.Remote != "origin" {
		t.Fatalf("Remote = %q, want %q (explicit branch.main.remote must win over alphabetical order)", r.Remote, "origin")
	}
}

func TestOpenReturnsErrNoBaseWhenNothingResolves(t *testing.T) {
	f := newGfixture(t, "trunk") // neither main nor master

	_, err := Open(f.dir)
	if !errors.Is(err, ErrNoBase) {
		t.Fatalf("Open err = %v, want errors.Is(err, ErrNoBase)", err)
	}
}

func TestOpenReturnsErrNoRemoteWhenAmbiguous(t *testing.T) {
	f := newGfixture(t, "main")
	f.addRemote("fork")
	f.addRemote("origin") // two remotes, no pushDefault, no branch.main.remote - genuinely ambiguous

	_, err := Open(f.dir)
	if !errors.Is(err, ErrNoRemote) {
		t.Fatalf("Open err = %v, want errors.Is(err, ErrNoRemote)", err)
	}
}

func TestOpenLocalOnlyRepoHasEmptyRemote(t *testing.T) {
	f := newGfixture(t, "main") // no remotes at all

	r, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r.Base != "main" {
		t.Fatalf("Base = %q, want %q", r.Base, "main")
	}
	if r.Remote != "" {
		t.Fatalf("Remote = %q, want empty - a local-only repo is not an error", r.Remote)
	}
}
