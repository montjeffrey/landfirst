# landfirst

[![ci](https://github.com/montjeffrey/landfirst/actions/workflows/ci.yml/badge.svg)](https://github.com/montjeffrey/landfirst/actions/workflows/ci.yml)

**Did every agent's finished work actually land, and what should merge next?**

A terminal panel for repositories with a lot of parallel work — several agents
or people committing in separate worktrees, branches finishing at different
times, and no reliable way to tell which of them actually reached the remote.

```
1 topology    2 graph

   ●── HEAD  a61b733  merge alpha        8 branches share this fork
   │  ├! feat/orcatec-task5-fanout    ━━━━━━━━━━━▸ +33 ⬖ C:\tmp\wt5 dirty:2 rebase-in-progress
   │   ↯ 14 file(s) also changed by fix/kpi-compliance-role   loader.ts, seed.sql
   │  ├! fix/kpi-compliance-role      ━━━━▸ +6 ⬖ .claude/worktrees/kpi clean
   │  ╰! feat/self-hosted-redis-worker ━━▸ +1 (no worktree)

j/k move   1/2/tab panel   p decide   w/W worktree +/-   r refresh   q quit
```

## Install

Download a binary from [releases](https://github.com/montjeffrey/landfirst/releases)
and put it on your `PATH`. One static file, no runtime.

```bash
go install github.com/montjeffrey/landfirst/cmd/landfirst@latest   # if you have Go
```

Needs `git` on the `PATH`. Conflict probing uses `git merge-tree --write-tree`
(git 2.38+); on older git it falls back to the pre-2.38 form, and any pair it
cannot probe is reported as **unknown** rather than quietly reported as clean.

## Use

```bash
landfirst            # the panel, opening on topology
landfirst -all       # the entire topology: every branch, every commit
landfirst -plain     # one-shot text report; pipeable
landfirst -json      # the whole snapshot, for scripts and agents
landfirst -no-probe  # skip conflict probing (much faster on big repos)
```

## The topology view

The panel opens on **topology**, not on the commit graph, because a commit graph
answers "what happened, in what order" and that is not the question this tool
exists for. The question is where each branch lands and what it fights with.

The base runs down the page. Every branch attaches at the commit it actually
forked from, so distance down the spine is staleness and branches that left the
base together sit together:

```
   ●── HEAD  a61b733  merge alpha        8 branches share this fork
   │  ├! feat/orcatec-task5-fanout    ━━━━━━━━━━━▸ +33 ⬖ C:\tmp\wt5 dirty:2 rebase-in-progress
   │   ↯ 14 file(s) also changed by fix/kpi-compliance-role   loader.ts, seed.sql
   │  ├! fix/kpi-compliance-role      ━━━━▸ +6 (no worktree)
   │  ╰! feat/self-hosted-redis-worker ━━▸ +1 ⬖ .claude/worktrees/redis clean
   │
   ●── 232 commits back  9f21c0e  initial import
   │  ╰! feat/orcatec-phase3-transform-load ━▸ +0 (no worktree)
```

Every branch also shows *where its worktree is*, if it has one: the path, a
`dirty:N` count when there are uncommitted changes (omitted when clean), and
`<op>-in-progress` when a rebase, merge, cherry-pick, revert or bisect is
mid-flight there. `w` gives the selected branch a worktree at the default
location; `W` removes one, refusing (and printing the `--force` command
instead of running it) rather than silently discarding uncommitted work.

### What "contention" means here

A raw conflict count conflates two unrelated things, and on a real repository
the useless one drowns out the useful one — measured median 14 files against a
maximum of 203.

Two branches that forked from **different eras** conflict on hundreds of files
because one carries months of unrelated history. That is drift, not contention,
and it tells you to rebase rather than anything about the code.

Two branches that forked **together** and both edited the same file are
genuinely fighting over it. That is what gets drawn: files both branches changed
since their shared fork point. Pairs from different fork points are not compared
at all, so the signal stays clean.

## The graph

The commit graph is drawn on a **sub-cell canvas**, not with box-drawing
characters. A terminal cell is the smallest thing most TUIs can colour, which is
why character-drawn graphs go blocky: every lane change becomes a corner glyph
the reader has to decode, and diagonals become staircases. It stops being
legible somewhere around three branches. Braille addresses 2x4 dots inside a
single cell, so edges are drawn as real quadratic curves at eight times the
resolution and a branch departing its parent is obvious at a glance.

```
   ⠐⢺⠒         716a9a2 feat/gamma  gamma work            2m
  ⢀⣀⠤⠃
  ⢸       ⠐⢺⠒   a3a83e8 feat/beta   feat/beta work 3      2m
  ⢸        ⢸
  ⢸       ⠐⢺⠒   4cb0881             feat/beta work 2      2m
  ⢸⣀⣀⣀⡠⠤⠔⠊⠁
 ⠐⢺⠒            a61b733 main        merge alpha           2m
```

Filled nodes are ordinary commits, hollow ones are merges, so the distinction
survives without colour. `-density compact|comfortable|spacious` trades curve
smoothness against how much history fits, and `d` cycles it live — a docked
panel is short, which makes that a real choice rather than a preference.

`-all` draws the **entire topology** — every branch gets its own lane and every
commit is present, including history reachable only through a merge. The default
window is capped for a fast first screen, and it says so; `-limit` and
`-branches` set the caps individually, and `-1` means no cap. At full topology
the canvas is wider than any dock, so `h`/`l` pan it and `H` snaps back.

Lanes sit three cells apart for a reason that is not aesthetic: a cell holds
exactly one foreground colour, so two differently coloured tracks sharing a cell
means one of them loses. Three cells guarantees each lane owns its own column of
cells and branch colours stay truthful.

## What it detects

| verdict | meaning |
|---|---|
| **orphaned** | the remote branch was deleted and the changes are *not* in the base |
| **wrong upstream** | tracks another branch, so `git push` does not publish this work |
| **never pushed** | has changes the remote has never seen |
| **diverged** | local and remote both moved |
| **unpushed** | ahead of its remote |
| **detached** | a worktree on no branch; no branch-oriented tool will show it |
| **safe to delete** | every change is already in the base |

Worktrees are also flagged by *where they live*: `volatile` (a temp directory
the OS can clear), `cloud` (inside OneDrive/Dropbox, where sync races git), and
`nested` (inside the main worktree).

## Why the counts differ from `git log`

Counting with `rev-list base..branch` compares commit **hashes**, and that gives
the wrong answer for the question this tool asks. A squash-merged branch, or one
forked before a history rewrite, reports every post-fork commit as missing. On a
real repository that read as *2073 unlanded commits across 17 branches*.

`git cherry` compares **patch ids** instead: `+` means a change is genuinely
absent from the base, `-` means an equivalent change is already there under a
different hash. The true figure for that same repository was **45 across 8** —
and three of the branches that looked like lost agent work were ordinary
squash-merged pull requests.

That distinction is the whole point. A board that cries wolf gets ignored.

## Acting on it

Pressing `p` on a selected branch (topology view only) opens that branch's
decision tree in the bottom pane. For most verdicts there is only one
sensible next step, so this goes straight to one of two things:

- an **action** — the exact command, with a confirmation bar that swallows
  every other key, so a stray keystroke can't fire it; or
- **advice** — the command printed for you to run yourself, because it
  rewrites history or nothing here is safe to guess at.

For a verdict where both sides genuinely moved (`diverged`), it's a real
question instead: numbered options (`[1]`, `[2]`, …), the engine's own pick
marked `(suggested)`, and picking one can lead to another question before it
bottoms out at an action or a piece of advice. `esc` steps back up the tree
one level at a time; at the top level it leaves the tree entirely.

Only actions that *add* to the remote or create a ref/worktree are ever
offered as a one-key action after confirmation — `push`, `worktree add`,
`branch -d`, `switch -c`, and a non-rewriting `merge`. Anything that rewrites
local history is printed for you to run yourself and is never executed by the
tool, because an agent may be mid-commit in that worktree. A test asserts
that no such action's command lies outside that set, and that none of them
carries `--force`.

`w` and `W` work the same way for worktrees directly: `w` gives the selected
branch's own worktree at the default location (confirmed first); `W` removes
it, or — if it's dirty or mid-operation — refuses and prints the `--force`
command instead of silently discarding work.

**This is not a read-only tool.** v0.1 was, and said so; v0.2 makes guarded
writes. It never rewrites history, never force-pushes, and never acts without a
confirmation, but it does push, merge, branch, and manage worktrees when you
ask it to.

## History

v0.1 was a Node implementation focused on merge ordering and a commit graph. It
is preserved at the [`v0.1.0-node`](https://github.com/montjeffrey/landfirst/tree/v0.1.0-node)
tag. v0.2 is a Go rewrite: a single static binary, landing-safety analysis, and
a real TUI.

## License

MIT
