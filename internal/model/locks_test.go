package model

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBuildDoesNotRewriteTheIndex is M3: git status opportunistically refreshes
// and rewrites .git/index (taking index.lock) unless GIT_OPTIONAL_LOCKS=0. A
// read-only inspector must not write to the repository it inspects - an
// agent's concurrent commit can fail on the lock landfirst holds.
func TestBuildDoesNotRewriteTheIndex(t *testing.T) {
	f := newFixture(t)
	// Make the stat information of a tracked file stale without changing its
	// content: exactly the state in which git status wants to refresh the index.
	file := filepath.Join(f.dir, "a.txt")
	later := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(f.dir, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}

	f.build()

	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("Build rewrote .git/index; read-only git calls must run with GIT_OPTIONAL_LOCKS=0")
	}
}
