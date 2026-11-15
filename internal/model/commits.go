package model

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
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
	format := strings.Join([]string{"%H", "%P", "%D", "%s", "%an", "%cI"}, "%x00")
	full := append([]string{"log", "-z", "--no-color", "--date-order", "--format=" + format}, args...)
	// "--" ends the revisions: a file in the working tree named like a ref must
	// not make git refuse the whole log as ambiguous.
	full = append(full, "--")
	return parseLogRecords(r.Try(full...))
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

// reHash is a full object name: 40 hex digits (SHA-1) or 64 (SHA-256).
var reHash = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// logFields is how many NUL-terminated fields one record of parseLog's format holds.
const logFields = 6

// parseLogRecords reads "git log -z" output whose format is six NUL-separated
// fields per commit. NUL cannot occur in a commit subject or author name as git
// reports them, so - unlike the 0x1e/0x1f bytes this replaced, which a commit
// message may contain - no message can end its own record and start a forged
// one. The hash and parent fields are still checked: a record whose hash is not
// an object name is dropped rather than drawn.
func parseLogRecords(out string) []Commit {
	fields := strings.Split(out, "\x00")
	var commits []Commit
	for i := 0; i+logFields <= len(fields); i += logFields {
		f := fields[i : i+logFields]
		hash := strings.TrimLeft(f[0], "\r\n")
		if !reHash.MatchString(hash) {
			continue
		}
		var parents []string
		valid := true
		for _, p := range strings.Fields(f[1]) {
			if !reHash.MatchString(p) {
				valid = false
				break
			}
			parents = append(parents, p)
		}
		if !valid {
			continue
		}
		c := Commit{Hash: hash, Short: short(hash), Parents: parents, Subject: f[3], Author: f[4]}
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
