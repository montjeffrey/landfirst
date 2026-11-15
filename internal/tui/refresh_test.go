package tui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/montjeffrey/landfirst/internal/engine"
	"github.com/montjeffrey/landfirst/internal/protocol"
)

// countingEngine records Snapshot and Graph calls and the context they got.
type countingEngine struct {
	fakeEngine
	snapshots, graphs atomic.Int64
	lastCtx           atomic.Value // context.Context
	hold              bool         // Snapshot blocks until its context ends
}

func (c *countingEngine) Snapshot(ctx context.Context, o engine.SnapshotOptions) (*protocol.Snapshot, error) {
	c.snapshots.Add(1)
	c.lastCtx.Store(ctx)
	if c.hold {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &protocol.Snapshot{}, nil
}
func (c *countingEngine) Graph(ctx context.Context, o protocol.GraphOptions) (*protocol.Graph, error) {
	c.graphs.Add(1)
	return &protocol.Graph{}, nil
}

// run executes a Cmd the way bubbletea would, descending into batches, and
// returns the messages the leaf commands produced.
func run(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, run(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// TestRefreshRequestsDoNotPileUpConcurrentBuilds is M5: every "r" press and
// change event started its own full build. While one is running, any number of
// further requests must collapse into a single follow-up.
func TestRefreshRequestsDoNotPileUpConcurrentBuilds(t *testing.T) {
	eng := &countingEngine{}
	a := New(eng)
	defer a.watchCancel()

	first := a.rebuild() // Init's refresh: a Snapshot and a Graph, now in flight
	var fetches []tea.Msg
	for i := 0; i < 25; i++ {
		_, cmd := a.Update(keyRune('r'))
		fetches = append(fetches, run(cmd)...)
	}
	if len(fetches) != 0 || eng.snapshots.Load() != 0 {
		t.Fatalf("a refresh started while one was still running (%d msgs, %d snapshots)", len(fetches), eng.snapshots.Load())
	}

	// The first build finishes: its two results arrive, and exactly one more
	// build - not 25 - starts.
	var follow []tea.Msg
	for _, m := range run(first) {
		_, cmd := a.Update(m)
		follow = append(follow, run(cmd)...)
	}
	if got := eng.snapshots.Load(); got != 2 {
		t.Fatalf("Snapshot called %d times, want 2 (the first build plus one coalesced refresh)", got)
	}
	if got := eng.graphs.Load(); got != 2 {
		t.Fatalf("Graph called %d times, want 2", got)
	}

	// And once those finish with nothing queued, nothing more starts.
	for _, m := range follow {
		_, cmd := a.Update(m)
		if len(run(cmd)) != 0 {
			t.Fatal("a refresh started with nothing queued")
		}
	}
	if a.inflight != 0 {
		t.Fatalf("inflight = %d after every fetch finished, want 0", a.inflight)
	}
}

// TestQuitCancelsARunningFetch: a build the user quit on must not keep
// spawning git, and no fetch may run without a deadline.
func TestQuitCancelsARunningFetch(t *testing.T) {
	eng := &countingEngine{hold: true}
	a := New(eng)

	done := make(chan []tea.Msg)
	go func() { done <- run(a.fetchSnapshot()) }()

	var ctx context.Context
	for ctx == nil {
		ctx, _ = eng.lastCtx.Load().(context.Context)
		time.Sleep(time.Millisecond)
	}
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("a Snapshot fetch must carry a deadline")
	}
	a.watchCancel() // what every quit key does

	select {
	case msgs := <-done:
		if m, ok := msgs[0].(snapshotMsg); !ok || m.err == nil {
			t.Fatalf("a canceled fetch should report an error, got %+v", msgs)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("quitting did not cancel the running fetch")
	}
}
