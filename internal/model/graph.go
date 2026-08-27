package model

import (
	"sort"
	"strconv"
	"strings"

	"github.com/montjeffrey/landfirst/internal/git"
)

// Palette size. Lane colours index into this.
const Palette = 12

// stableHash is FNV-1a: small, and identical across runs, platforms and Go
// versions. Lane colour depends on it, so it must never drift.
func stableHash(key string) uint32 {
	var h uint32 = 0x811c9dc5
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 0x01000193
	}
	return h
}

type track struct {
	Name string
	Head string
}

// collectCommits builds the window of history to draw.
//
// Order matters. The first-parent spine of the base goes in first and is never
// sacrificed to the budget: without an unbroken trunk, branches have nothing
// visible to attach to and the graph reads as disconnected fragments. Filling
// the window by recency across all refs does not guarantee that, which is why
// the graph looked broken on any repository larger than the limit.
func collectCommits(r *git.Repo, tracks []track, limit int) ([]Commit, bool) {
	seen := make(map[string]Commit)
	var order []string
	add := func(list []Commit) {
		for _, c := range list {
			if _, ok := seen[c.Hash]; !ok {
				seen[c.Hash] = c
				order = append(order, c.Hash)
			}
		}
	}

	// 1. continuous first-parent trunk
	add(parseLog(r, "--max-count="+strconv.Itoa(limit), "--first-parent", r.Base))

	// 2. each branch's own commits, plus its merge-base so it visibly joins
	perBranch := limit
	if perBranch > 250 {
		perBranch = 250
	}
	ceiling := limit * 3
	var wantBases []string
	for _, t := range tracks {
		if t.Name == r.Base || len(seen) >= ceiling {
			continue
		}
		add(parseLog(r, "--max-count="+strconv.Itoa(perBranch), r.Base+".."+t.Name))
		if mb := strings.TrimSpace(git.Try(r.Root, "merge-base", r.Base, t.Name)); mb != "" {
			wantBases = append(wantBases, mb)
		}
	}
	var missing []string
	for _, h := range wantBases {
		if _, ok := seen[h]; !ok {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		add(parseLog(r, append([]string{"--no-walk"}, missing...)...))
	}

	list := make([]Commit, 0, len(seen))
	for _, h := range order {
		list = append(list, seen[h])
	}
	list = topoSort(list)

	total := 0
	if n := strings.TrimSpace(git.Try(r.Root, "rev-list", "--count", "--all")); n != "" {
		total, _ = strconv.Atoi(n)
	}
	return list, total > len(list)
}

// layout assigns every commit a lane and a colour.
//
// Lanes come from BRANCH IDENTITY, not row position. A greedy allocator that
// walks commits in date order and reuses lanes between branches whose rows do
// not overlap is more compact, but one new commit then reorders rows and
// cascades new lanes and colours through the graph - 7 of 10 commits moved in a
// measured case. Invisible in a screenshot, unusable when it redraws live.
//
//  1. Every commit is attributed to a track (a branch's first-parent spine),
//     resolved in a fixed order so the answer never depends on row order.
//  2. Each track gets its own lane, by position in that order. Interval packing
//     was tried and rejected: extending one branch changed the overlaps and
//     shifted every later track sideways, and it let a feature branch share
//     lane 0 with the trunk.
//  3. Colour is a hash of the track name, so it is pinned to the branch.
//
// The base resolves first and therefore always owns lane 0.
func layout(commits []Commit, tracks []track) ([]Commit, []Edge, int) {
	byHash := make(map[string]*Commit, len(commits))
	for i := range commits {
		commits[i].Row = i
		byHash[commits[i].Hash] = &commits[i]
	}

	owner := make(map[string]string, len(commits))
	claim := func(start, key string, firstParentOnly bool) {
		stack := []string{start}
		for len(stack) > 0 {
			h := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			c, ok := byHash[h]
			if !ok {
				continue
			}
			if _, taken := owner[h]; taken {
				continue
			}
			owner[h] = key
			if firstParentOnly {
				if len(c.Parents) > 0 {
					stack = append(stack, c.Parents[0])
				}
				continue
			}
			stack = append(stack, c.Parents...)
		}
	}
	for _, t := range tracks {
		if t.Head != "" {
			claim(t.Head, t.Name, true)
		}
	}
	for _, t := range tracks {
		if t.Head != "" {
			claim(t.Head, t.Name, false)
		}
	}
	for i := range commits {
		if _, ok := owner[commits[i].Hash]; !ok {
			owner[commits[i].Hash] = commits[i].Hash
		}
	}

	present := map[string]bool{}
	for _, c := range commits {
		present[owner[c.Hash]] = true
	}
	var ordered []string
	seenKey := map[string]bool{}
	for _, t := range tracks {
		if present[t.Name] && !seenKey[t.Name] {
			ordered = append(ordered, t.Name)
			seenKey[t.Name] = true
		}
	}
	var overflow []string
	for k := range present {
		if !seenKey[k] {
			overflow = append(overflow, k)
		}
	}
	sort.Strings(overflow)

	laneOf := make(map[string]int, len(ordered)+len(overflow))
	for i, k := range ordered {
		laneOf[k] = i
	}
	overflowLane := len(ordered)
	for _, k := range overflow {
		laneOf[k] = overflowLane // everything without a track shares the rightmost lane
	}

	laneCount := 0
	for i := range commits {
		key := owner[commits[i].Hash]
		commits[i].Lane = laneOf[key]
		commits[i].Color = int(stableHash(key) % Palette)
		if commits[i].Lane+1 > laneCount {
			laneCount = commits[i].Lane + 1
		}
	}

	var edges []Edge
	for _, c := range commits {
		for _, p := range c.Parents {
			pc, ok := byHash[p]
			if !ok {
				edges = append(edges, Edge{FromRow: c.Row, FromLane: c.Lane, ToRow: -1, ToLane: c.Lane, Color: c.Color, Stub: true})
				continue
			}
			col := pc.Color
			if c.Lane == pc.Lane {
				col = c.Color
			}
			edges = append(edges, Edge{FromRow: c.Row, FromLane: c.Lane, ToRow: pc.Row, ToLane: pc.Lane, Color: col})
		}
	}
	return commits, edges, laneCount
}

// buildGraph decides which branches get drawn and lays them out.
//
// One lane per branch is only affordable with a budget. Priority is: the base,
// then anything checked out in a worktree (that is where work is actually
// happening), then merge-plan order. Everything else is still measured and
// ranked elsewhere - it just does not get a line.
func buildGraph(r *git.Repo, units []Unit, opt Options) *Graph {
	limit := opt.Limit
	if limit <= 0 {
		limit = 400
	}
	maxBranches := opt.MaxBranches
	if maxBranches <= 0 {
		maxBranches = 10
	}

	baseTip := strings.TrimSpace(git.Try(r.Root, "rev-parse", r.Base))
	tracks := []track{{Name: r.Base, Head: baseTip}}
	taken := map[string]bool{r.Base: true}

	add := func(u Unit) {
		if u.Branch == "" || taken[u.Branch] {
			return
		}
		head := strings.TrimSpace(git.Try(r.Root, "rev-parse", u.Branch))
		if head == "" {
			return
		}
		tracks = append(tracks, track{Name: u.Branch, Head: head})
		taken[u.Branch] = true
	}

	eligible := 0
	for _, u := range units {
		if u.Branch != "" && u.Branch != r.Base {
			eligible++
		}
	}
	for _, u := range units { // worktrees first
		if len(u.Worktrees) > 0 {
			add(u)
		}
	}
	for _, u := range units { // then by verdict severity, which units are already sorted by
		if len(tracks) > maxBranches {
			break
		}
		add(u)
	}

	commits, truncated := collectCommits(r, tracks, limit)
	commits, edges, laneCount := layout(commits, tracks)

	total := 0
	if n := strings.TrimSpace(git.Try(r.Root, "rev-list", "--count", "--all")); n != "" {
		total, _ = strconv.Atoi(n)
	}
	drawn := make([]string, 0, len(tracks))
	for _, t := range tracks {
		drawn = append(drawn, t.Name)
	}
	hidden := eligible - (len(tracks) - 1)
	if hidden < 0 {
		hidden = 0
	}
	return &Graph{
		Commits: commits, Edges: edges, LaneCount: laneCount,
		Total: total, Truncated: truncated, Drawn: drawn, Hidden: hidden,
	}
}
