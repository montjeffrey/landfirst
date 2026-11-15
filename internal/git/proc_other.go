//go:build !windows

package git

import "os/exec"

// procGuard is the Windows job object's counterpart; here it does nothing.
//
// POSIX has the same shape of gap - CommandContext signals only git itself, so a
// hook or alias's children can outlive a timeout - but the usual fix, a new
// process group, also detaches git from the terminal's foreground group, which
// would break ssh's prompt on the controlling terminal and Ctrl-C reaching git.
// That trade is not made here.
type procGuard struct{}

func newProcGuard(cmd *exec.Cmd) *procGuard { return &procGuard{} }
func (g *procGuard) attach(cmd *exec.Cmd)   {}
func (g *procGuard) close()                 {}
