// Package git is the only place that shells out. Every command is a fixed
// argv slice - nothing is ever concatenated into a shell string, so a branch
// name containing a quote or a semicolon cannot become a command.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
)

// Repo is an opened repository.
type Repo struct {
	Root   string
	Base   string
	Remote string
	Name   string
}

var spawnCount atomic.Int64

// SpawnCount reports how many git processes have been spawned since start
// (or since the last ResetSpawnCount). Tests assert on it to catch an
// accidental N+1 of git invocations.
func SpawnCount() int64 { return spawnCount.Load() }

// ResetSpawnCount zeroes the counter, for isolating spawn counts between
// test cases.
func ResetSpawnCount() { spawnCount.Store(0) }

// RunCtx is the actual executor; Run delegates to it with a background
// context and no extra env. extraEnv lets a later stage isolate the object
// store (GIT_OBJECT_DIRECTORY etc.) during merge-tree probes without
// touching the process-wide environment.
func RunCtx(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	spawnCount.Add(1)
	if os.Getenv("LANDFIRST_TRACE") != "" {
		fmt.Fprintln(os.Stderr, "git trace:", dir, "git", strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	if extraEnv != nil {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return out.String(), &Error{Args: args, Stderr: strings.TrimSpace(errb.String()), ExitCode: code, Err: err}
	}
	return out.String(), nil
}

// Run executes git and returns stdout. Stderr is captured, never inherited:
// a branch deleted mid-run makes git print to stderr, and inheriting it would
// corrupt the alternate screen the TUI draws on.
func Run(dir string, args ...string) (string, error) {
	return RunCtx(context.Background(), dir, nil, args...)
}

// Try runs git and reports failure as empty output rather than an error, for
// probes where "this ref does not exist" is a normal answer.
func Try(dir string, args ...string) string {
	out, err := Run(dir, args...)
	if err != nil {
		return ""
	}
	return out
}

// Lines returns non-empty trimmed lines of output.
func Lines(dir string, args ...string) []string {
	var res []string
	for _, l := range strings.Split(Try(dir, args...), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			res = append(res, s)
		}
	}
	return res
}

type Error struct {
	Args     []string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *Error) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = e.Err.Error()
	}
	return "git " + strings.Join(e.Args, " ") + ": " + msg
}

// ExitCode reports git's exit status, or -1 if it never ran. merge-tree uses
// exit 1 to mean "conflicts found", which is a result rather than a failure,
// so callers need the code and not just an error.
func ExitCode(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.ExitCode
	}
	return -1
}
