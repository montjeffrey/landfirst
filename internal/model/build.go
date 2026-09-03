package model

import (
	"strconv"
	"strings"
	"time"

	"github.com/montjeffrey/landfirst/internal/engine/state"
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
		Freshness: collectFreshness(r),
		Units:     units, Worktrees: trees,
	}

	// A zero FetchHeadAt (never fetched) makes time.Since huge rather than
	// overflowing - Duration.Sub clamps to its max representable value
	// instead of wrapping - so it reads as stale with no IsZero special case.
	const staleThreshold = time.Hour
	for i := range m.Units {
		m.Units[i].Stale = state.IsStale(m.Freshness.FetchHeadAt, staleThreshold)
	}

	// SeverityBad covers InProgress/Orphaned/WrongUpstream/NeverPushed - the
	// same set the topology view marks "!" - stuck mid-rebase work is exactly
	// the "might get lost" signal this metric exists for, same as the other
	// three.
	for _, u := range units {
		if u.Verdict.Severity() == SeverityBad && u.Unlanded > 0 {
			m.StrandedCommits += u.Unlanded
			m.AtRisk++
		}
	}

	// Forks and contentions must exist before the plan is built: buildPlan
	// derives PlanItem.Conflicts from contentions rather than probing itself.
	m.Forks = buildForks(r, units)
	if opt.Probe {
		m.Contentions = buildContentions(r, m.Forks, mode, opt.MaxPairs)
	}
	m.Plan = buildPlan(r, units, m.Contentions, opt.Probe)
	m.Graph = buildGraph(r, units, opt)
	m.BuildMillis = time.Since(start).Milliseconds()
	return m
}
