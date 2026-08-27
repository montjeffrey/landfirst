package model

import (
	"strconv"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/git"
)

// Options control how much work Build does.
type Options struct {
	Probe       bool // run conflict probes for the merge plan
	MaxPairs    int
	Limit       int // commits to draw (0 = default)
	MaxBranches int // branches given their own lane (0 = default)
}

// gitCapability decides how conflicts can be probed.
//
// merge-tree --write-tree arrived in git 2.38. Older git exits 129, which is
// not a merge result - reporting those pairs as clean would be confidently
// wrong, so they are reported as unknown instead.
func gitCapability(r *git.Repo) (version string, mode string) {
	raw := strings.TrimSpace(git.Try(r.Root, "--version"))
	m := reVer.FindStringSubmatch(raw)
	if m == nil {
		return raw, "none"
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	if maj > 2 || (maj == 2 && min >= 38) {
		return m[0], "full"
	}
	return m[0], "legacy"
}

// Build produces the whole model.
func Build(r *git.Repo, opt Options) *Model {
	start := time.Now()
	ver, mode := gitCapability(r)

	trees := collectWorktrees(r)
	cache := map[string]cherryResult{}
	units := buildUnits(r, trees, cache)

	m := &Model{
		Repo: r.Name, Root: r.Root, Base: r.Base, Remote: r.Remote,
		GeneratedAt: time.Now(), GitVersion: ver, ConflictMode: mode,
		Units: units, Worktrees: trees,
	}

	for _, u := range units {
		if Verdicts[u.Verdict].Rank <= 2 && u.Unlanded > 0 {
			m.StrandedCommits += u.Unlanded
			m.AtRisk++
		}
	}

	m.Plan, m.Collisions = buildPlan(r, units, opt, mode)
	m.Graph = buildGraph(r, units, opt)
	m.BuildMillis = time.Since(start).Milliseconds()
	return m
}
