package git

import (
	"errors"
	"fmt"
	"strings"
)

// ErrUnsafeBranchName means a branch name could be read by git as an option
// (it begins with "-") or is not a valid branch name at all. Git allows
// "--receive-pack=<cmd>" as a ref name, and a bare name like that in argv
// becomes an option that runs <cmd>, so landfirst offers no command for it.
var ErrUnsafeBranchName = errors.New("branch name is not safe to pass to git")

// HeadRef is the fully qualified ref of a local branch. Every command that
// names a branch uses this instead of the short name: a full refname begins
// with "refs/", so git can never parse it as an option, and it cannot be
// confused with a tag, a remote or an abbreviated object id of the same name.
func HeadRef(branch string) string { return "refs/heads/" + branch }

// RemoteRef is the fully qualified remote-tracking ref for branch on remote.
func RemoteRef(remote, branch string) string {
	return "refs/remotes/" + remote + "/" + branch
}

// OptionLike reports whether s would be read as an option if it were passed to
// git as a bare argument. It is a cheap, pure check for rendering paths that
// cannot afford a spawn per branch; CheckBranchName is the full test.
func OptionLike(s string) bool { return strings.HasPrefix(s, "-") }

// CheckBranchName is the gate in front of every command landfirst builds
// from a branch name: it refuses a leading "-" and anything
// `git check-ref-format --branch` rejects.
func CheckBranchName(dir, name string) error {
	if name == "" || OptionLike(name) {
		return fmt.Errorf("%w: %q", ErrUnsafeBranchName, name)
	}
	if _, err := Run(dir, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("%w: %q", ErrUnsafeBranchName, name)
	}
	return nil
}
