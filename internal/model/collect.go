package model

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
	"golang.org/x/sync/errgroup"
)

const us = "\x1f" // unit separator: safe in git format strings, illegal in ref names

var (
	reAhead  = regexp.MustCompile(`ahead (\d+)`)
	reBehind = regexp.MustCompile(`behind (\d+)`)
	reVer    = regexp.MustCompile(`(\d+)\.(\d+)`)
)

type rawBranch struct {
	Name, Head, Upstream string
	Gone                 bool
	Ahead, Behind        int
	Date                 time.Time
	Author, Subject      string
}

// collectBranches gets every branch, its upstream and its ahead/behind in a
// single process. Asking per branch meant dozens of spawns, which is what made
// this unusably slow on Windows.
func collectBranches(r *git.Repo) []rawBranch {
	format := strings.Join([]string{
		"%(refname:short)", "%(objectname)", "%(upstream:short)",
		"%(upstream:track,nobracket)", "%(committerdate:iso-strict)",
		"%(authorname)", "%(contents:subject)",
	}, us)

	var out []rawBranch
	for _, line := range git.Lines(r.Root, "for-each-ref", "--format="+format, "refs/heads") {
		f := strings.Split(line, us)
		if len(f) < 7 {
			continue
		}
		b := rawBranch{Name: f[0], Head: f[1], Upstream: f[2], Author: f[5], Subject: f[6]}
		b.Gone = strings.Contains(f[3], "gone")
		if m := reAhead.FindStringSubmatch(f[3]); m != nil {
			b.Ahead, _ = strconv.Atoi(m[1])
		}
		if m := reBehind.FindStringSubmatch(f[3]); m != nil {
			b.Behind, _ = strconv.Atoi(m[1])
		}
		if t, err := time.Parse(time.RFC3339, f[4]); err == nil {
			b.Date = t
		}
		out = append(out, b)
	}
	return out
}

// LocationRisk flags a worktree path that sync software or nesting could
// silently corrupt - exported so internal/engine's WorktreeService.Add can
// run the exact same check before creating a worktree, instead of a second,
// independently-drifting copy of "is this path safe" living in engine too.
func LocationRisk(path, root string) []Risk {
	p := strings.ReplaceAll(path, "\\", "/")
	rt := strings.ReplaceAll(root, "\\", "/")
	var risks []Risk
	low := strings.ToLower(p)
	if strings.Contains(low, "/tmp/") || strings.Contains(low, "/temp/") ||
		strings.Contains(low, "/appdata/local/temp/") {
		risks = append(risks, Risk{"volatile", "in a temp directory - the OS can delete this"})
	}
	for _, cloud := range []string{"onedrive", "dropbox", "google drive", "icloud"} {
		if strings.Contains(low, cloud) {
			risks = append(risks, Risk{"cloud", "inside a cloud-synced folder - sync can race git"})
			break
		}
	}
	if p != rt && strings.HasPrefix(p, rt+"/") {
		risks = append(risks, Risk{"nested", "nested inside the main worktree"})
	}
	return risks
}

// collectWorktrees uses -z throughout: a worktree or branch name can contain
// almost anything, and NUL is the one byte git guarantees won't appear.
func collectWorktrees(r *git.Repo) []Worktree {
	var trees []Worktree
	var cur *Worktree
	for _, l := range strings.Split(git.Try(r.Root, "worktree", "list", "--porcelain", "-z"), "\x00") {
		switch {
		case strings.HasPrefix(l, "worktree "):
			if cur != nil {
				trees = append(trees, *cur)
			}
			cur = &Worktree{Path: strings.TrimPrefix(l, "worktree ")}
		case cur == nil:
			continue
		case strings.HasPrefix(l, "HEAD "):
			cur.Head = strings.TrimPrefix(l, "HEAD ")
		case strings.HasPrefix(l, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(l, "branch "), "refs/heads/")
		case l == "detached":
			cur.Detached = true
		case strings.HasPrefix(l, "locked"):
			cur.Locked = true
		case strings.HasPrefix(l, "prunable"):
			cur.Prunable = true
		}
	}
	if cur != nil {
		trees = append(trees, *cur)
	}
	// IsMain/Risks/PathExists are cheap local checks and stay serial; only the
	// two git spawns below (Dirty, Op) are worth running concurrently.
	var g errgroup.Group
	g.SetLimit(runtime.NumCPU())
	for i := range trees {
		trees[i].IsMain = i == 0
		trees[i].Risks = LocationRisk(trees[i].Path, r.Root)

		// A worktree's directory can vanish - deleted by hand, on a drive that
		// unmounted, cleaned up by something other than git. Everything past
		// this point requires spawning git inside it, which would just fail.
		_, err := os.Stat(trees[i].Path)
		trees[i].PathExists = err == nil
		if !trees[i].PathExists {
			trees[i].Op = OpNone
			continue
		}
		g.Go(func() error {
			// Each goroutine only ever writes trees[i]/trees[j] for its own
			// index - distinct slice elements are distinct memory, so this is
			// race-free with no lock, unlike a shared accumulator would be.
			trees[i].Dirty = dirtyState(trees[i].Path)
			trees[i].Op = worktreeOp(trees[i].Path)
			return nil
		})
	}
	g.Wait() // every g.Go func above only ever returns nil
	return trees
}

// dirtyState summarizes `git status --porcelain=v2 -z` into counts.
//
// Every record is one NUL-terminated field, except a rename/copy ('2'),
// which is followed by a second NUL-terminated field holding the origin
// path - miss that and every record after it reads one field out of phase.
func dirtyState(path string) DirtyState {
	var d DirtyState
	fields := strings.Split(git.Try(path, "status", "--porcelain=v2", "-z"), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case '1', '2':
			if len(f) < 4 {
				continue
			}
			if f[2] != '.' {
				d.Staged++
			}
			if f[3] != '.' {
				d.Unstaged++
			}
			if f[0] == '2' {
				i++ // consume the origPath field this record owns
			}
		case 'u':
			d.Conflicted++
		case '?':
			d.Untracked++
		}
	}
	return d
}

// resolveGitPath runs a `git rev-parse` subcommand that reports a path (a
// gitdir, a common dir, a file under one) and absolutizes it against dir -
// git reports these relative to dir for the main worktree but absolute for a
// linked one, and every caller here needs both handled the same way. "" means
// the command produced nothing (not a worktree, or too old to know the flag).
func resolveGitPath(dir string, args ...string) string {
	p := strings.TrimSpace(git.Try(dir, args...))
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return p
}

// worktreeOp resolves a worktree's own gitdir - .git/worktrees/<name> for a
// linked worktree, .git itself for the main one - and checks it for the marker
// git leaves behind while a rebase/merge/cherry-pick/revert/bisect is
// unfinished. These never live in the shared common dir, so one worktree can
// never be misread as reporting another's in-progress operation.
//
// Git refuses to start a second of these at once, so the precedence below is
// mostly academic - it exists to give a defined answer instead of an
// arbitrary one.
func worktreeOp(path string) InProgressOp {
	gitDir := resolveGitPath(path, "rev-parse", "--git-dir")
	if gitDir == "" {
		return OpNone
	}
	isDir := func(name string) bool {
		fi, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil && fi.IsDir()
	}
	isFile := func(name string) bool {
		fi, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil && !fi.IsDir()
	}
	switch {
	case isDir("rebase-merge") || isDir("rebase-apply"):
		return OpRebase
	case isFile("MERGE_HEAD"):
		return OpMerge
	case isFile("CHERRY_PICK_HEAD"):
		return OpCherryPick
	case isFile("REVERT_HEAD"):
		return OpRevert
	case isFile("BISECT_LOG"):
		return OpBisect
	}
	return OpNone
}

// collectFreshness reads how stale this repo's view of the remote is.
// build.go compares FetchHeadAt against a threshold to flag Units Stale.
func collectFreshness(r *git.Repo) Freshness {
	p := resolveGitPath(r.Root, "rev-parse", "--git-path", "FETCH_HEAD")
	if p == "" {
		return Freshness{}
	}
	fi, err := os.Stat(p)
	if err != nil {
		return Freshness{}
	}
	return Freshness{FetchHeadAt: fi.ModTime()}
}

var reMergeTreePath = regexp.MustCompile(`^\s+(?:base|our|their)\s+\d+\s+[0-9a-f]+\s+(.+)$`)
