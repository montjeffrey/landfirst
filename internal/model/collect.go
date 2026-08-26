package model

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
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

func locationRisk(path, root string) []Risk {
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

func collectWorktrees(r *git.Repo) []Worktree {
	var trees []Worktree
	var cur *Worktree
	for _, line := range strings.Split(git.Try(r.Root, "worktree", "list", "--porcelain"), "\n") {
		l := strings.TrimRight(line, "\r")
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
	for i := range trees {
		trees[i].IsMain = i == 0
		trees[i].Risks = locationRisk(trees[i].Path, r.Root)
	}
	return trees
}

var reMergeTreePath = regexp.MustCompile(`^\s+(?:base|our|their)\s+\d+\s+[0-9a-f]+\s+(.+)$`)
