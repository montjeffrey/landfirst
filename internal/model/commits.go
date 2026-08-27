package model

import (
	"sort"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
)

// Field and record separators. Legal in a git format string and illegal in a
// ref name, so they cannot appear inside the data they delimit.
const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// Commit is one node in the graph.
type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Parents []string  `json:"parents"`
	Refs    []string  `json:"refs"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`

	Row   int `json:"row"`
	Lane  int `json:"lane"`
	Color int `json:"color"`
}

// Edge connects a commit to a parent. Stub means the parent lies outside the
// window, so the line ends rather than reaching anything.
type Edge struct {
	FromRow, FromLane int
	ToRow, ToLane     int
	Color             int
	Stub              bool
}

// Graph is the laid-out commit graph.
type Graph struct {
	Commits   []Commit `json:"commits"`
	Edges     []Edge   `json:"-"`
	LaneCount int      `json:"laneCount"`
	Total     int      `json:"totalCommits"`
	Truncated bool     `json:"truncatedCommits"`
	Drawn     []string `json:"drawnBranches"`
	Hidden    int      `json:"hiddenBranches"`
}

func parseLog(r *git.Repo, args ...string) []Commit {
	format := recordSep + strings.Join([]string{"%H", "%P", "%D", "%s", "%an", "%cI"}, fieldSep)
	full := append([]string{"log", "--no-color", "--date-order", "--format=" + format}, args...)

	var commits []Commit
	for _, rec := range strings.Split(git.Try(r.Root, full...), recordSep) {
		rec = strings.TrimLeft(rec, "\r\n")
		if rec == "" {
			continue
		}
		f := strings.SplitN(rec, fieldSep, 6)
		if len(f) < 6 {
			continue
		}
		c := Commit{Hash: f[0], Subject: f[3], Author: f[4]}
		c.Short = short(c.Hash)
		if p := strings.TrimSpace(f[1]); p != "" {
			c.Parents = strings.Fields(p)
		}
		for _, ref := range strings.Split(f[2], ",") {
			if ref = strings.TrimSpace(ref); ref != "" {
				c.Refs = append(c.Refs, ref)
			}
		}
		c.Date, _ = time.Parse(time.RFC3339, strings.TrimSpace(f[5]))
		commits = append(commits, c)
	}
	return commits
}

// topoSort orders children before parents, newest first among what is ready.
//
// Ties break on hash. Without that, commits made in the same second - routine
// in scripted and agent-driven work - can come out in a different order run to
// run, reshuffling rows and lanes for no reason. Ordering must be a pure
// function of the commit set.
func topoSort(commits []Commit) []Commit {
	inSet := make(map[string]*Commit, len(commits))
	for i := range commits {
		inSet[commits[i].Hash] = &commits[i]
	}
	unresolved := make(map[string]int, len(commits))
	for _, c := range commits {
		for _, p := range c.Parents {
			if _, ok := inSet[p]; ok {
				unresolved[p]++
			}
		}
	}

	newerFirst := func(a, b *Commit) bool {
		if !a.Date.Equal(b.Date) {
			return a.Date.After(b.Date)
		}
		return a.Hash < b.Hash
	}

	var ready []*Commit
	for i := range commits {
		if unresolved[commits[i].Hash] == 0 {
			ready = append(ready, &commits[i])
		}
	}

	out := make([]Commit, 0, len(commits))
	done := make(map[string]bool, len(commits))
	for len(ready) > 0 {
		best := 0
		for i := 1; i < len(ready); i++ {
			if newerFirst(ready[i], ready[best]) {
				best = i
			}
		}
		c := ready[best]
		ready = append(ready[:best], ready[best+1:]...)
		out = append(out, *c)
		done[c.Hash] = true
		for _, p := range c.Parents {
			pc, ok := inSet[p]
			if !ok {
				continue
			}
			unresolved[p]--
			if unresolved[p] == 0 {
				ready = append(ready, pc)
			}
		}
	}
	// Cycles are impossible in git, but never silently drop commits.
	if len(out) < len(commits) {
		var rest []Commit
		for _, c := range commits {
			if !done[c.Hash] {
				rest = append(rest, c)
			}
		}
		sort.SliceStable(rest, func(i, j int) bool {
			if !rest[i].Date.Equal(rest[j].Date) {
				return rest[i].Date.After(rest[j].Date)
			}
			return rest[i].Hash < rest[j].Hash
		})
		out = append(out, rest...)
	}
	return out
}
