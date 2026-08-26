// Package git is the only place that shells out. Every command is a fixed
// argv slice - nothing is ever concatenated into a shell string, so a branch
// name containing a quote or a semicolon cannot become a command.
package git

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
)

// Repo is an opened repository.
type Repo struct {
	Root   string
	Base   string
	Remote string
	Name   string
}

// Run executes git and returns stdout. Stderr is captured, never inherited:
// a branch deleted mid-run makes git print to stderr, and inheriting it would
// corrupt the alternate screen the TUI draws on.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
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

// Open locates the repository containing dir and picks an integration branch.
func Open(dir string) (*Repo, error) {
	root, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: strings.TrimSpace(root)}
	r.Root = strings.TrimSpace(r.Root)
	for _, cand := range []string{"main", "master"} {
		if Try(r.Root, "rev-parse", "--verify", "--quiet", "refs/heads/"+cand) != "" {
			r.Base = cand
			break
		}
	}
	if r.Base == "" {
		r.Base = "main"
	}
	if remotes := Lines(r.Root, "remote"); len(remotes) > 0 {
		r.Remote = remotes[0]
	} else {
		r.Remote = "origin"
	}
	parts := strings.FieldsFunc(r.Root, func(c rune) bool { return c == '/' || c == '\\' })
	if len(parts) > 0 {
		r.Name = parts[len(parts)-1]
	}
	return r, nil
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
