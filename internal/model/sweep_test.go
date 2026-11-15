package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An interrupted probe never reaches its cleanup, so its throwaway object
// store stays in the temp dir. The next run sweeps stores old enough that no
// live run can still be using them, and leaves recent ones alone.
func TestSweepStaleObjectStoresRemovesOnlyOldOnes(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)

	mk := func(name string, age time.Duration) string {
		p := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Join(p, "ab"), 0o755); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-age)
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := mk("landfirst-objects-111", 48*time.Hour)
	fresh := mk("landfirst-objects-222", time.Minute)
	other := mk("someone-elses-dir", 48*time.Hour)

	if n := SweepStaleObjectStores(time.Hour); n != 1 {
		t.Fatalf("swept %d, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale store should be gone: %v", err)
	}
	for _, p := range []string{fresh, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must survive: %v", p, err)
		}
	}
}
