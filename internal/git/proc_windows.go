//go:build windows

package git

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// procGuard puts a git process and everything it starts in one job object, so a
// timeout or cancellation ends the whole tree. exec.CommandContext alone kills
// only git.exe on Windows: the shell and the command an alias or hook runs keep
// going, holding their working directory open.
//
// The job is terminated explicitly on cancellation and is NOT kill-on-close:
// git legitimately leaves detached helpers behind after a normal run (gc --auto,
// fsmonitor), and those must not be killed when landfirst's call returns.
type procGuard struct{ job windows.Handle }

func newProcGuard(cmd *exec.Cmd) *procGuard {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return &procGuard{} // degrade to killing only git itself
	}
	g := &procGuard{job: job}
	cmd.Cancel = func() error {
		windows.TerminateJobObject(g.job, 1)
		return cmd.Process.Kill()
	}
	return g
}

// attach is called right after Start. A child git spawned before this runs is
// not covered; git does its own startup first, so in practice none is.
func (g *procGuard) attach(cmd *exec.Cmd) {
	if g.job == 0 || cmd.Process == nil {
		return
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	if windows.AssignProcessToJobObject(g.job, h) != nil {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

func (g *procGuard) close() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}
