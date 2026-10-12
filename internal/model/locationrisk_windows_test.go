//go:build windows

package model

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mklinkJunction makes a directory junction. Unlike a symlink it needs no
// administrator rights or Developer Mode, so any Windows user can make one.
func mklinkJunction(t *testing.T, link, target string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v: %s", err, out)
	}
}

// TestNestedRiskSeesThroughJunction: a junction is the Windows alias that
// filepath.EvalSymlinks stopped following in Go 1.23. A nested path reached
// through one must still be flagged.
func TestNestedRiskSeesThroughJunction(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	mklinkJunction(t, alias, root)

	if got, want := canonical(alias), canonical(root); got != want {
		t.Fatalf("canonical(junction) = %q, want its target %q", got, want)
	}
	for _, r := range LocationRisk(filepath.Join(alias, "wt"), root) {
		if r.Kind == "nested" {
			return
		}
	}
	t.Fatal("a nested path reached through a junction to the root was not flagged")
}
