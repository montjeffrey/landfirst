/**
 * Markdown for Zed's Markdown preview. Zed re-renders on file change, so
 * rewriting this file is the whole live-update mechanism for that surface.
 *
 * Mermaid gitGraph is not used: it cannot express worktrees, has no notion of
 * conflicts, and falls apart past a handful of branches. A flowchart of the
 * branch relationships carries the actual decision instead, and the commit
 * graph goes in a code fence using the shared box-drawing renderer.
 */

import { buildTextGraph, gutterString } from './lanes-text.mjs';

const esc = (s) => String(s ?? '').replace(/\|/g, '\\|');
const mermaidId = (name) => 'b_' + name.replace(/[^A-Za-z0-9]/g, '_');

export function renderMarkdown(model, { graphRows = 60, mermaidBranches = 10 } = {}) {
  const L = [];
  const stamp = model.generatedAt.replace('T', ' ').slice(0, 19);

  L.push(`# ${model.repo}`);
  L.push('');
  const shown = model.truncatedCommits
    ? `${model.commits.length} of ${model.totalCommits} commits`
    : `${model.commits.length} commits`;
  L.push(`base \`${model.base}\` · ${shown} · ${model.branches.length} branches · ` +
         `${model.worktrees.length} worktrees · updated ${stamp}` +
         (model.probed ? '' : ' · _conflict probes still running_'));
  L.push('');

  // ---- branch relationship map -------------------------------------------
  const active = model.plan.slice(0, mermaidBranches);
  if (active.length) {
    L.push('## Branch map');
    L.push('');
    L.push('```mermaid');
    L.push('flowchart LR');
    L.push(`  base(["${model.base}"])`);
    for (const p of active) {
      const b = model.branches.find((x) => x.name === p.name) || {};
      const wt = (b.worktrees && b.worktrees.length) ? ' ⬖' : '';
      L.push(`  ${mermaidId(p.name)}["${p.order}. ${p.name}${wt}<br/>+${p.ahead} / -${p.behind}"]`);
      L.push(`  base --> ${mermaidId(p.name)}`);
    }
    for (const c of model.collisions.filter((c) => c.conflicts.length)) {
      if (!active.some((p) => p.name === c.a) || !active.some((p) => p.name === c.b)) continue;
      L.push(`  ${mermaidId(c.a)} -.->|"${c.conflicts.length} conflict(s)"| ${mermaidId(c.b)}`);
    }
    for (const p of active) {
      const cls = p.conflicts == null ? 'unknown' : (p.conflicts > 0 ? 'bad' : 'good');
      L.push(`  class ${mermaidId(p.name)} ${cls}`);
    }
    L.push('  classDef good fill:#1e3a24,stroke:#98c379,color:#d7e5d9');
    L.push('  classDef bad fill:#3a1e22,stroke:#e06c75,color:#f0d7da');
    L.push('  classDef unknown fill:#2b2b2b,stroke:#777,color:#ddd');
    L.push('```');
    L.push('');
    L.push('Solid edges are branches off the base. Dotted edges are branch pairs that ' +
           'conflict with **each other** — merging one forces a rebase on the other. ' +
           'Green merges into the base cleanly, red does not. ⬖ marks a branch checked out in a worktree.');
    L.push('');
  }
  return L;
}

export function renderMarkdownFull(model, opts = {}) {
  const L = renderMarkdown(model, opts);
  const graphRows = opts.graphRows ?? 60;

  // ---- merge plan ---------------------------------------------------------
  L.push('## Merge plan');
  L.push('');
  if (!model.plan.length) {
    L.push('_Nothing to merge._');
  } else {
    L.push('| # | Branch | Ahead | Behind | Conflicts | Worktree | Reasoning |');
    L.push('|--:|---|--:|--:|--:|:-:|---|');
    for (const p of model.plan) {
      const b = model.branches.find((x) => x.name === p.name) || {};
      const wt = (b.worktrees && b.worktrees.length) ? '⬖' : '';
      const cf = p.conflicts == null ? '—' : (p.conflicts ? `**${p.conflicts}**` : '0');
      L.push(`| ${p.order} | \`${esc(p.name)}\` | ${p.ahead} | ${p.behind} | ${cf} | ${wt} | ${esc(p.why.join(' · '))} |`);
    }
  }
  L.push('');

  // ---- collisions ---------------------------------------------------------
  const conflicting = model.collisions.filter((c) => c.conflicts.length);
  L.push('## Collisions');
  L.push('');
  if (!model.collisions.length) {
    L.push('_No branch pairs share changed files, so nothing can collide._');
  } else {
    L.push('Overlap is the pre-filter: pairs sharing at least one changed file are dry-run merged with ' +
           '`git merge-tree`. The conflict count is the full result of merging the two tips, so it can ' +
           `exceed the overlap — each branch also carries its own drift from \`${model.base}\`. Pairs ` +
           'sharing no files are never probed.');
    L.push('');
    L.push('| Branch A | Branch B | Overlap | Conflicts | Paths |');
    L.push('|---|---|--:|--:|---|');
    for (const c of conflicting.concat(model.collisions.filter((x) => !x.conflicts.length))) {
      const unknown = c.clean === null;
      const paths = c.conflicts.length
        ? c.conflicts.slice(0, 4).map((p) => `\`${esc(p)}\``).join('<br/>') +
          (c.conflicts.length > 4 ? `<br/>… ${c.conflicts.length - 4} more` : '')
        : (unknown ? '_could not probe_' : '_merges cleanly_');
      L.push(`| \`${esc(c.a)}\` | \`${esc(c.b)}\` | ${c.shared} | ` +
             `${c.conflicts.length ? `**${c.conflicts.length}**` : (unknown ? '?' : '0')} | ${paths} |`);
    }
  }
  L.push('');

  // ---- worktrees ----------------------------------------------------------
  L.push('## Worktrees');
  L.push('');
  L.push('| Path | Branch | HEAD | Flags |');
  L.push('|---|---|---|---|');
  for (const w of model.worktrees) {
    const flags = [w.isMain && 'main', w.detached && '**detached**', w.locked && 'locked',
                   w.prunable && '**prunable**'].filter(Boolean).join(', ') || '—';
    L.push(`| \`${esc(w.path)}\` | ${w.branch ? `\`${esc(w.branch)}\`` : '_detached_'} | \`${w.head ?? ''}\` | ${flags} |`);
  }
  L.push('');
  return L;
}

/** The whole document. Written to disk on every recompute. */
export function toMarkdown(model, opts = {}) {
  const L = renderMarkdownFull(model, opts);
  const graphRows = opts.graphRows ?? 60;

  // ---- commit graph -------------------------------------------------------
  L.push('## Commit graph');
  L.push('');
  const rows = buildTextGraph(model);
  const shown = rows.slice(0, graphRows);
  L.push('```text');
  for (const r of shown) {
    const gutter = gutterString(r, { color: false, laneCount: model.laneCount });
    if (r.kind === 'link') { L.push(gutter); continue; }
    const c = r.commit;
    const refs = (c.refs || []).length ? ` (${c.refs.join(', ')})` : '';
    const subj = c.subject.length > 68 ? c.subject.slice(0, 67) + '…' : c.subject;
    L.push(`${gutter}  ${c.short} ${subj}${refs}`);
  }
  L.push('```');
  if (rows.length > shown.length) {
    L.push('');
    L.push(`_${rows.length - shown.length} more rows — raise \`--graph-rows\`._`);
  }
  L.push('');

  // ---- excluded branches --------------------------------------------------
  const rest = model.branches.filter((b) => b.state === 'merged' || b.state === 'dead');
  if (rest.length) {
    L.push(`## Not in the plan (${rest.length})`);
    L.push('');
    L.push('| Branch | Ahead | Behind | Age | State | Note |');
    L.push('|---|--:|--:|--:|---|---|');
    for (const b of rest) {
      const note = b.state === 'merged'
        ? `already contained in \`${model.base}\` — safe to delete`
        : `${b.behind} behind; forked before a history rewrite`;
      L.push(`| \`${esc(b.name)}\` | ${b.ahead} | ${b.behind} | ${b.ageDays}d | ${b.state} | ${note} |`);
    }
    L.push('');
  }

  L.push('---');
  L.push('');
  L.push(`_Regenerated automatically by \`landfirst --watch\`. Buffer reloads in place; ` +
         `keep the Markdown preview open beside your code._`);
  L.push('');
  return L.join('\n');
}
