package model

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNestedRiskSeesThroughAliases: a nested path must be caught however it
// is spelled. Before LocationRisk canonicalised both sides, a symlinked or
// junctioned spelling of the root (macOS's /var -> /private/var, a Windows
// 8.3 short name), a dot-dot detour, or different case on a case-insensitive
// filesystem all slipped past the prefix test.
func TestNestedRiskSeesThroughAliases(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := func(path string) bool {
		for _, r := range LocationRisk(path, root) {
			if r.Kind == "nested" {
				return true
			}
		}
		return false
	}

	if nested(filepath.Join(base, "sibling")) {
		t.Error("a sibling of the root was flagged as nested")
	}
	if nested(root) {
		t.Error("the root itself was flagged as nested inside itself")
	}
	if !nested(filepath.Join(root, "sub", "..", "wt")) {
		t.Error("a dot-dot spelling of a nested path was not flagged")
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		if !nested(filepath.Join(strings.ToUpper(root), "wt")) {
			t.Error("an upper-cased spelling of a nested path was not flagged")
		}
	}

	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("cannot create a symlink here (%v); the alias case is not exercised", err)
	}
	if !nested(filepath.Join(alias, "wt")) {
		t.Error("a nested path reached through a symlink to the root was not flagged")
	}
}
