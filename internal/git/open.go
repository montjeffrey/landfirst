package git

import (
	"errors"
	"strings"
)

// ErrNoBase means Open could not find an integration branch without
// guessing. Every downstream verdict depends on this ref, so a wrong guess
// is worse than stopping and asking.
var ErrNoBase = errors.New("no resolvable base branch - set init.defaultBranch or ensure a remote HEAD/main/master ref exists")

// ErrNoRemote means the remote is ambiguous: 2+ remotes, no pushDefault, and
// no branch.<base>.remote to settle it.
var ErrNoRemote = errors.New("ambiguous remote - set branch.<base>.remote or remote.pushDefault")

// Open locates the repository containing dir and picks an integration
// branch and remote - resolved, never guessed. A candidate remote comes
// first (pushDefault, or a sole remote); the base comes from that remote's
// HEAD, then init.defaultBranch, then main/master; the remote is then
// finalized against branch.<base>.remote, which can override the candidate.
func Open(dir string) (*Repo, error) {
	root, err := Run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	r := &Repo{Root: strings.TrimSpace(root)}

	candidate := candidateRemote(r.Root)
	base, err := resolveBase(r.Root, candidate)
	if err != nil {
		return nil, err
	}
	r.Base = base

	remote, err := resolveRemote(r.Root, r.Base, candidate)
	if err != nil {
		return nil, err
	}
	r.Remote = remote

	parts := strings.FieldsFunc(r.Root, func(c rune) bool { return c == '/' || c == '\\' })
	if len(parts) > 0 {
		r.Name = parts[len(parts)-1]
	}
	return r, nil
}

// candidateRemote picks a remote to consult for HEAD before the base branch
// is known. Empty means ambiguous - resolveRemote refines or accepts that
// once the base makes branch.<base>.remote available.
func candidateRemote(root string) string {
	if v := strings.TrimSpace(Try(root, "config", "--get", "remote.pushDefault")); v != "" {
		return v
	}
	if remotes := Lines(root, "remote"); len(remotes) == 1 {
		return remotes[0]
	}
	return ""
}

// resolveBase finds the integration branch without guessing: the candidate
// remote's HEAD, then init.defaultBranch, then main/master, stopping at the
// first that actually exists.
func resolveBase(root, candidate string) (string, error) {
	if candidate != "" {
		prefix := "refs/remotes/" + candidate + "/"
		ref := strings.TrimSpace(Try(root, "symbolic-ref", "-q", prefix+"HEAD"))
		if strings.HasPrefix(ref, prefix) {
			return strings.TrimPrefix(ref, prefix), nil
		}
	}
	if def := strings.TrimSpace(Try(root, "config", "--get", "init.defaultBranch")); def != "" {
		if Try(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+def) != "" {
			return def, nil
		}
	}
	for _, cand := range []string{"main", "master"} {
		if Try(root, "rev-parse", "--verify", "--quiet", "refs/heads/"+cand) != "" {
			return cand, nil
		}
	}
	return "", ErrNoBase
}

// resolveRemote finalizes the remote now that base is known. An explicit
// branch.<base>.remote always overrides the step-1 candidate - the fix for
// picking an unrelated fork just because it sorts first alphabetically.
func resolveRemote(root, base, candidate string) (string, error) {
	if v := strings.TrimSpace(Try(root, "config", "--get", "branch."+base+".remote")); v != "" {
		return v, nil
	}
	if candidate != "" {
		return candidate, nil
	}
	if len(Lines(root, "remote")) == 0 {
		return "", nil // legitimate local-only repo, not an error
	}
	return "", ErrNoRemote
}
