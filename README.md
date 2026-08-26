# landfirst

[![ci](https://github.com/montjeffrey/landfirst/actions/workflows/ci.yml/badge.svg)](https://github.com/montjeffrey/landfirst/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/landfirst.svg)](https://www.npmjs.com/package/landfirst)

**Which branch do I merge first?**

`landfirst` reads a repository with a lot of parallel local work and answers one
question: given everything you have in flight, what order should you land it in,
and what breaks if you pick wrong.

It is not another commit-graph viewer. The graph is there because you need it to
read the answer — the answer is the merge plan.

```
 1  feat/checkout-flow   +12  -0   0 conflicts  ⬖   merges cleanly · collides with nobody
 2  feat/pricing         +4   -2   0 conflicts      merges cleanly · collides with 1 branch
 3  feat/tax-rules       +7   -2   3 conflicts  ⬖   conflicts with feat/pricing on 3 files
```

## Why it exists

Merge queues ([Mergify](https://mergify.com/product/merge-queue),
[Graphite](https://graphite.com/docs/graphite-merge-queue)) sequence work well,
but they act on pull requests, server-side, gated on CI. Conflict predictors
(GitKraken, various VS Code extensions) tell you a pair will conflict, one pair
at a time.

Neither helps at the point where the decision is actually made: your machine,
before anything is pushed, with a dozen branches and worktrees in flight. That
gap is what this fills — and it is a gap that keeps widening as more work happens
in parallel worktrees.

## Install

```bash
npm install -g landfirst
```

Requires Node 18+ and git. Zero runtime dependencies — the whole tool is Node
standard library plus the `git` binary you already have.

Conflict detection uses `git merge-tree --write-tree`, which needs **git 2.38+**.
On older git it falls back to the pre-2.38 `merge-tree` form; results are
slightly coarser, and any pair it cannot probe is reported as *unknown* rather
than quietly reported as clean.

## Use

```bash
landfirst              # one-shot HTML report
landfirst --tui        # live terminal UI
landfirst --md         # live Markdown, for an editor preview pane
landfirst --app        # live page in a chromeless window
landfirst --tui --md   # combine: one watcher, one probe cache
```

| Flag | Meaning |
|---|---|
| `--serve` | live page on 127.0.0.1, updates over SSE |
| `--tui` | live terminal UI |
| `--md` | live Markdown at `<repo>/.git/landfirst.md` |
| `--app` | chromeless window (implies `--serve --open`) |
| `--base BRANCH` | integration branch (default `main`, else `master`) |
| `--limit N` | commits to render (default 400) |
| `--all` | include branches classified as dead |
| `--max-pairs N` | cap on pairwise conflict probes (default 400) |
| `--max-branches N` | branches drawn in the graph (default 10) |

## How the plan is computed

1. **Measure** every local branch against the base — ahead, behind, changed files, age.
2. **Classify** it: `active`, `stale`, `merged` (already contained in the base — safe to delete), or `dead` (forked before a history rewrite).
3. **Probe the base merge** with `git merge-tree`, a dry run that touches neither the index nor the working tree.
4. **Find collisions.** Only branch pairs sharing at least one changed file are probed against each other; the rest cannot collide, so they are skipped.
5. **Order** the survivors: dependencies first, then clean-merging branches, then fewest collisions, then smallest diff.

The score is a weighted sum, not a truth. Every branch shows its reasoning so
you can disagree with it.

## Live updates

The watcher follows `refs`, `HEAD`, the index, and worktree registrations under
`.git` — never the working tree, so editing files does not churn the view and
`node_modules` cannot drown the watcher. Each change runs a fast pass so the
graph moves immediately, then a probe pass that supersedes it.

Three SHA-keyed caches make that affordable, so only what actually moved is
recomputed. On a repo with 29 branches, 14 worktrees and 1315 commits:

| | cold | warm |
|---|--:|--:|
| branch measurement | 6805 ms | 128 ms |
| fast pass (graph only) | — | 460 ms |
| full pass (probes cached) | 12364 ms | 527 ms |

## Notes

- `--serve` binds to `127.0.0.1` only. The page exposes commit messages, file
  paths, and branch names; it has no authentication and is not meant to leave
  the machine.
- **Your repository is never written to.** No checkouts, no merges, no index
  changes — and no objects. `merge-tree --write-tree` does write the merged tree
  it computes, so probes run with `GIT_OBJECT_DIRECTORY` pointed at a temp store
  (the real store as an alternate, so reads still resolve). Reports go to a temp
  directory rather than into `.git`. There is a test asserting the loose-object
  count is unchanged after repeated probing.

## Reading the graph

The layout optimises for stability over compactness, because it redraws live:

- **Lanes come from branch identity, not row position.** Each drawn branch gets
  its own lane, assigned in a fixed order, so it keeps that lane and colour as
  you work. An earlier version assigned lanes in commit order and reused them
  between non-overlapping branches — tighter, but one new commit could move 7 of
  10 commits to different lanes and colours. Fine in a screenshot, unusable when
  it redraws every few seconds.
- **Only the branches you're working in get a line.** Branches with worktrees
  first, then merge-plan order, capped by `--max-branches`. The rest are still
  measured, ranked and listed — they just don't add a lane. This is what keeps
  one-lane-per-branch affordable.
- **The base branch always owns lane 0**, and its first-parent spine stays there.
- **Ordering is a pure function of the commit set.** Commits sharing a timestamp
  are broken on hash, so identical repositories always render identically.
- **The trunk is never truncated.** The window always contains a continuous
  first-parent spine plus every branch's merge-base, so a branch can't render
  floating with no visible connection to the base.

## License

MIT
