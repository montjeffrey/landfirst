package git

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRunKillsACallThatOutlivesItsDeadline is M5: a wedged git process (a
// credential helper, a dead mount, a hook) blocked the panel forever because
// every call used a background context with no deadline.
func TestRunKillsACallThatOutlivesItsDeadline(t *testing.T) {
	old := DefaultTimeout
	DefaultTimeout = 300 * time.Millisecond
	defer func() { DefaultTimeout = old }()

	start := time.Now()
	_, err := Run(t.TempDir(), "-c", "alias.hang=!sleep 30", "hang")
	if err == nil {
		t.Fatal("a hung git call must fail, not succeed")
	}
	if !IsTimeout(err) {
		t.Fatalf("error should report the timeout, got %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("call was not killed at its deadline, took %v", took)
	}
}

func TestRunHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	_, err := RunCtx(ctx, t.TempDir(), nil, "-c", "alias.hang=!sleep 30", "hang")
	if err == nil || IsTimeout(err) {
		t.Fatalf("a canceled call must fail as canceled, got %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("cancellation did not stop the call, took %v", took)
	}
}

// TestRunNeverPromptsOnTheTerminal checks the environment git is started with.
func TestRunNeverPromptsOnTheTerminal(t *testing.T) {
	out, err := Run(t.TempDir(), "-c", `alias.env=!echo "$GIT_TERMINAL_PROMPT $GIT_OPTIONAL_LOCKS"`, "env")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out); got != "0 0" {
		t.Fatalf("GIT_TERMINAL_PROMPT and GIT_OPTIONAL_LOCKS = %q, want \"0 0\"", got)
	}
}

func TestRepoCountsTimedOutCalls(t *testing.T) {
	old := DefaultTimeout
	DefaultTimeout = 300 * time.Millisecond
	defer func() { DefaultTimeout = old }()

	r := (&Repo{Root: t.TempDir()}).WithContext(context.Background())
	if got := r.Try("-c", "alias.hang=!sleep 30", "hang"); got != "" {
		t.Fatalf("Try must hide the failure as empty output, got %q", got)
	}
	if r.TimedOut() != 1 {
		t.Fatalf("TimedOut() = %d, want 1: an empty Try() result after a timeout must be detectable", r.TimedOut())
	}
}

// A killed call must not leave descendants holding its working directory: on
// Windows only git.exe died, the alias's shell and sleep kept the directory open
// and it could not be removed.
func TestTimedOutCallLeavesNothingHoldingItsDirectory(t *testing.T) {
	old := DefaultTimeout
	DefaultTimeout = 300 * time.Millisecond
	defer func() { DefaultTimeout = old }()

	dir := t.TempDir()
	if _, err := Run(dir, "-c", "alias.hang=!sleep 30", "hang"); err == nil {
		t.Fatal("the call must time out")
	}
	var err error
	for i := 0; i < 20; i++ { // allow the OS a moment to release handles after the kill
		if err = os.Remove(dir); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("directory still in use after the timed-out call returned: %v", err)
}
