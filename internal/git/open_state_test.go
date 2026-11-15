package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenOutsideARepoReturnsErrNotARepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	_, err := Open(dir)
	if !errors.Is(err, ErrNotARepo) {
		t.Fatalf("got %v, want ErrNotARepo", err)
	}
	if strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("the message should be landfirst's own, not raw git text: %v", err)
	}
}

func TestOpenInARepoWithNoCommitsReturnsErrEmptyRepo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "init", "-q", "-b", "main")
	_, err := Open(dir)
	if !errors.Is(err, ErrEmptyRepo) {
		t.Fatalf("got %v, want ErrEmptyRepo", err)
	}
	if errors.Is(err, ErrNoBase) {
		t.Fatalf("an empty repo is not a missing-base problem: %v", err)
	}
}
