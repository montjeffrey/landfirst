# landfirst

[![ci](https://github.com/montjeffrey/landfirst/actions/workflows/ci.yml/badge.svg)](https://github.com/montjeffrey/landfirst/actions/workflows/ci.yml)

**Did every agent's finished work actually land, and what should merge next?**

A terminal panel for repositories with a lot of parallel work — several agents
or people committing in separate worktrees, branches finishing at different
times, and no reliable way to tell which of them actually reached the remote.

```
1 landing    2 merge plan    3 worktrees

ORPHANED  remote branch was deleted and the changes are not in the base
  ! feat/kpi-slice-b-restore      3 changes exist only here (4 already in main)   3w
WRONG UPSTREAM  tracks another branch, so pushing does not publish this work
  ! feat/team-productivity        tracks origin/main, not origin/feat/team-…      3w
NEVER PUSHED  has changes the remote has never seen
  ! feat/orcatec-task5-fanout     30 changes never pushed                    5d ⬖1 [volatile]

j/k move   1-3/tab panel   p fix selected   r refresh   q quit
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
landfirst            # the panel
landfirst -plain     # one-shot text report; pipeable
landfirst -json      # the whole model, for scripts and agents
landfirst -no-probe  # skip conflict probing (much faster on big repos)
```

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

Pressing `p` stages the exact command shown on screen and waits for
confirmation; the confirmation bar swallows every other key, so a stray
keystroke cannot fire an action.

Only operations that *add* to the remote are ever offered as a one-key action —
`git push` and `git branch -d`. Anything that rewrites local history is printed
for you to run yourself and is never executed by the tool, because an agent may
be mid-commit in that worktree. A test asserts that no action marked safe is
anything other than push/delete, and that none of them carries `--force`.

**This is not a read-only tool.** v0.1 was, and said so; v0.2 makes guarded
writes. It never rewrites history, never force-pushes, and never acts without a
confirmation, but it does push and delete branches when you ask it to.

## History

v0.1 was a Node implementation focused on merge ordering and a commit graph. It
is preserved at the [`v0.1.0-node`](https://github.com/montjeffrey/landfirst/tree/v0.1.0-node)
tag. v0.2 is a Go rewrite: a single static binary, landing-safety analysis, and
a real TUI.

## License

MIT
