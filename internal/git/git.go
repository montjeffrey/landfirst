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
	"time"
)

// Repo is an opened repository.
type Repo struct {
	Root   string
	Base   string
	Remote string
	Name   string

	// Ctx, when set, governs every call made through the Repo methods below, so
	// canceling it kills the git processes of a build the user no longer wants.
	Ctx context.Context
	// Timeouts, when set, counts calls that timed out. Try reports failure as
	// empty output, so without this a build whose git calls timed out would look
	// like a repository with nothing in it.
	Timeouts *atomic.Int64
}

var spawnCount atomic.Int64

// SpawnCount reports how many git processes have been spawned since start
// (or since the last ResetSpawnCount). Tests assert on it to catch an
// accidental N+1 of git invocations.
func SpawnCount() int64 { return spawnCount.Load() }

// ResetSpawnCount zeroes the counter, for isolating spawn counts between
// test cases.
func ResetSpawnCount() { spawnCount.Store(0) }

// baseEnv is added to every git process. GIT_OPTIONAL_LOCKS=0 stops read-only
// commands (status above all) from refreshing and rewriting .git/index under
// index.lock: landfirst inspects repositories other tools are committing to,
// and must never be the reason one of their writes fails or the index changes.
//
// LC_ALL=C pins git's messages to English: open.go and the push/ref error
// handling recognise git's own wording, which a translated git would defeat.
// LANGUAGE outranks LC_ALL for message catalogs under GNU gettext, so it is
// removed from the inherited environment (see gitEnv).
var baseEnv = []string{"GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C"}

// gitEnv is the environment for one git process: the inherited one without
// LANGUAGE, then baseEnv, then extra (later entries win).
func gitEnv(extra []string) []string {
	env := make([]string, 0, len(os.Environ())+len(baseEnv)+len(extra))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(strings.ToUpper(kv), "LANGUAGE=") {
			continue
		}
		env = append(env, kv)
	}
	return append(append(env, baseEnv...), extra...)
}

// DefaultTimeout bounds any git call whose context carries no deadline of its
// own, so one wedged process (a credential helper, a dead network mount, a
// hook) cannot freeze the panel forever. GIT_TERMINAL_PROMPT=0 above makes git
// fail instead of asking for a password it can never receive. ssh may still
// prompt on the controlling terminal; this timeout is what bounds that case.
var DefaultTimeout = 30 * time.Second

// killGrace is how long Wait may keep waiting for output pipes after a
// timed-out or canceled git is killed: a grandchild (git-remote-https, a
// credential helper) can hold them open.
const killGrace = 2 * time.Second

// RunCtx is the actual executor; Run delegates to it with a background
// context and no extra env. extraEnv lets a later stage isolate the object
// store (GIT_OBJECT_DIRECTORY etc.) during merge-tree probes without
// touching the process-wide environment.
func RunCtx(ctx context.Context, dir string, extraEnv []string, args ...string) (string, error) {
	spawnCount.Add(1)
	if os.Getenv("LANDFIRST_TRACE") != "" {
		fmt.Fprintln(os.Stderr, "git trace:", dir, "git", strings.Join(args, " "))
	}
	if _, ok := ctx.Deadline(); !ok && DefaultTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv(extraEnv)
	cmd.WaitDelay = killGrace
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	guard := newProcGuard(cmd)
	defer guard.close()
	err := cmd.Start()
	if err == nil {
		guard.attach(cmd)
		err = cmd.Wait()
	}
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		stderr := strings.TrimSpace(errb.String())
		if cerr := ctx.Err(); cerr != nil {
			// The kill is what the caller needs to hear about, not git's
			// "signal: killed".
			err = cerr
			if errors.Is(cerr, context.DeadlineExceeded) {
				stderr = "timed out"
			} else {
				stderr = "canceled"
			}
		}
		return out.String(), &Error{Args: args, Stderr: stderr, ExitCode: code, Err: err}
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

// WithContext returns a copy of r whose git calls use ctx and whose timeouts
// are counted. The original is untouched, so concurrent builds do not share
// either.
func (r *Repo) WithContext(ctx context.Context) *Repo {
	c := *r
	c.Ctx = ctx
	c.Timeouts = new(atomic.Int64)
	return &c
}

// TimedOut reports how many calls made through r hit their deadline.
func (r *Repo) TimedOut() int64 {
	if r.Timeouts == nil {
		return 0
	}
	return r.Timeouts.Load()
}

// IsTimeout reports whether err is a git call that was killed at its deadline.
func IsTimeout(err error) bool {
	var e *Error
	return errors.As(err, &e) && errors.Is(e.Err, context.DeadlineExceeded)
}

func (r *Repo) ctx() context.Context {
	if r.Ctx != nil {
		return r.Ctx
	}
	return context.Background()
}

// RunEnv runs git in the repository root with extra environment.
func (r *Repo) RunEnv(extraEnv []string, args ...string) (string, error) {
	return r.runIn(r.Root, extraEnv, args)
}

// Run runs git in the repository root.
func (r *Repo) Run(args ...string) (string, error) { return r.runIn(r.Root, nil, args) }

func (r *Repo) runIn(dir string, extraEnv []string, args []string) (string, error) {
	out, err := RunCtx(r.ctx(), dir, extraEnv, args...)
	if err != nil && r.Timeouts != nil && IsTimeout(err) {
		r.Timeouts.Add(1)
	}
	return out, err
}

// Try is the package-level Try bound to r's context.
func (r *Repo) Try(args ...string) string { return r.TryIn(r.Root, args...) }

// TryIn is Try in another directory (a linked worktree).
func (r *Repo) TryIn(dir string, args ...string) string {
	out, err := r.runIn(dir, nil, args)
	if err != nil {
		return ""
	}
	return out
}

// Lines is the package-level Lines bound to r's context.
func (r *Repo) Lines(args ...string) []string {
	var res []string
	for _, l := range strings.Split(r.Try(args...), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			res = append(res, s)
		}
	}
	return res
}
