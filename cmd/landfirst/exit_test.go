package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/montjeffrey/landfirst/internal/git"
)

// Every "this is not a repository landfirst can read" outcome exits 2; 1 is
// for things that went wrong inside landfirst or git.
func TestExitCodeForOpenErrors(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{git.ErrNotARepo, 2},
		{git.ErrEmptyRepo, 2},
		{git.ErrNoBase, 2},
		{git.ErrNoRemote, 2},
		{fmt.Errorf("wrapped: %w", git.ErrEmptyRepo), 2},
		{errors.New("exec: git not found"), 1},
	} {
		if got := openExitCode(c.err); got != c.want {
			t.Errorf("%v: exit %d, want %d", c.err, got, c.want)
		}
	}
}
