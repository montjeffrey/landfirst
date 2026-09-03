package model

import (
	"bytes"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/montjeffrey/landfirst/internal/git"
)

// countObjects parses `git count-objects -v` into (count, size) so a test can
// assert the real object store is untouched by a probing build.
func countObjects(t *testing.T, dir string) (count, size int) {
	t.Helper()
	out, err := git.Run(dir, "count-objects", "-v")
	if err != nil {
		t.Fatalf("count-objects: %v", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "count:"); ok {
			count, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		if v, ok := strings.CutPrefix(line, "size:"); ok {
			size, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return count, size
}

// P0-2: merge-tree --write-tree writes a real merge-tree object into
// whatever object store it's pointed at, as a side effect of just checking
// "would this conflict". A build that probes must never leave that behind in
// the repo's real object store - see isolatedObjectEnv in forks.go.
func TestProbingLeavesNoLooseObjectsInTheRealRepo(t *testing.T) {
	f := forkFixture(t)
	repo, err := git.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}

	// feat/a and feat/b share a fork point and both edit shared.py, so a
	// probing build is guaranteed to actually invoke merge-tree --write-tree
	// here - asserting "nothing changed" is worthless if nothing ran.
	before, sizeBefore := countObjects(t, f.dir)

	var m *Model
	for i := 0; i < 3; i++ {
		m = Build(repo, Options{Probe: true, MaxPairs: 50})
	}
	if len(m.Contentions) == 0 {
		t.Fatal("fixture produced no contentions - this test would pass vacuously")
	}

	after, sizeAfter := countObjects(t, f.dir)
	if after != before || sizeAfter != sizeBefore {
		t.Fatalf("probing polluted the real object store: before count=%d size=%d, after count=%d size=%d",
			before, sizeBefore, after, sizeAfter)
	}
}

// captureGitTrace runs fn with LANDFIRST_TRACE on and returns everything git
// wrote to stderr, so a test can count real git invocations by command
// instead of trusting an internal call count.
func captureGitTrace(t *testing.T, fn func()) string {
	t.Helper()
	t.Setenv("LANDFIRST_TRACE", "1")

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	os.Stderr = old
	w.Close()
	return <-done
}

// P0-3: buildPlan must not re-probe every candidate pair itself. That
// duplicated buildContentions's work and, cross-fork included, measured era
// drift rather than contention (see the Contention doc comment). The only
// merge-tree spawns in a build should be buildContentions's: one per
// same-fork pair that shares at least one file.
//
// In forkFixture: feat/a and feat/b share a fork and both edit shared.py -
// the one genuine same-fork collision. feat/c shares that fork too but
// touches no common file, so it never pairs. feat/stale forked alone at a
// different, later point, so it can never pair with anyone either.
func TestContentionProbingIsScopedToSameForkPairs(t *testing.T) {
	f := forkFixture(t)
	repo, err := git.Open(f.dir)
	if err != nil {
		t.Fatal(err)
	}

	var m *Model
	trace := captureGitTrace(t, func() {
		m = Build(repo, Options{Probe: true, MaxPairs: 50})
	})

	var mergeTreeCalls []string
	for _, line := range strings.Split(trace, "\n") {
		if strings.Contains(line, "git merge-tree") {
			mergeTreeCalls = append(mergeTreeCalls, line)
		}
	}
	if len(mergeTreeCalls) != 1 {
		t.Fatalf("want exactly 1 merge-tree probe (feat/a vs feat/b, the only same-fork pair "+
			"sharing a file), got %d:\n%s", len(mergeTreeCalls), strings.Join(mergeTreeCalls, "\n"))
	}
	if strings.Contains(mergeTreeCalls[0], "feat/stale") {
		t.Fatalf("feat/stale forked alone and must never be probed: %s", mergeTreeCalls[0])
	}
	if !strings.Contains(mergeTreeCalls[0], "feat/a") || !strings.Contains(mergeTreeCalls[0], "feat/b") {
		t.Fatalf("expected the one probe to be feat/a vs feat/b, got: %s", mergeTreeCalls[0])
	}

	// PlanItem.Conflicts must come from Contentions, not a separate probe.
	collideCount := map[string]int{}
	for _, c := range m.Contentions {
		collideCount[c.A]++
		collideCount[c.B]++
	}
	if len(m.Plan) == 0 {
		t.Fatal("fixture produced an empty plan")
	}
	for _, p := range m.Plan {
		if want := collideCount[p.Branch]; p.Conflicts != want {
			t.Fatalf("PlanItem.Conflicts for %s = %d, want %d (count of contentions naming it)",
				p.Branch, p.Conflicts, want)
		}
	}
}
