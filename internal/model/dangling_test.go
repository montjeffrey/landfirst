package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// danglingRef points refs/heads/ghost at an object that does not exist. git
// for-each-ref then dies with exit 128 and prints nothing useful.
func (f *fixture) danglingRef() {
	f.t.Helper()
	p := filepath.Join(f.dir, ".git", "refs", "heads", "ghost")
	if err := os.WriteFile(p, []byte(strings.Repeat("ab", 20)+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// One bad ref used to hide every branch: for-each-ref failed, the error was
// dropped, zero units came out and the banner said all work had landed.
func TestUnreadableBranchListIsAnErrorNotAnEmptyRepository(t *testing.T) {
	f := newFixture(t)
	f.run("checkout", "-q", "-b", "feat/real")
	f.commit("x.txt", "x", "real work")
	f.danglingRef()

	m := f.build()
	if m.ReadErr == nil {
		t.Fatalf("the branch list could not be read, but the model reports no error (units=%d)", len(m.Units))
	}
}
