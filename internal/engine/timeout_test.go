package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// TestSnapshotDoesNotPublishAPartialModelWhenGitTimesOut is M5: the model reads
// a failed git call as "nothing there", so a wedged git used to produce a
// snapshot of an empty, healthy-looking repository - with no error.
func TestSnapshotDoesNotPublishAPartialModelWhenGitTimesOut(t *testing.T) {
	f := newEfixture(t)
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	old := git.DefaultTimeout
	git.DefaultTimeout = time.Nanosecond // every git call misses its deadline
	defer func() { git.DefaultTimeout = old }()

	snap, err := eng.Snapshot(context.Background(), SnapshotOptions{Stage: protocol.StageFast})
	if err == nil {
		t.Fatalf("Snapshot returned %d units with no error although every git call timed out", len(snap.Units))
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error should say git timed out, got %v", err)
	}
}

func TestSnapshotStopsWhenItsContextIsCanceled(t *testing.T) {
	f := newEfixture(t)
	eng, err := Open(f.dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Snapshot(ctx, SnapshotOptions{Stage: protocol.StageFast}); !errors.Is(err, context.Canceled) {
		t.Fatalf("a canceled Snapshot must return context.Canceled, got %v", err)
	}
}
