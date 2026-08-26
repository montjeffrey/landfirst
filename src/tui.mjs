/**
 * Live TUI for Zed's terminal dock. Redraws whenever the watcher reports a new
 * model, so it is never stale.
 *
 *   1..4 / tab   switch view      j k up down   scroll     g G  top / bottom
 *   pgup pgdn    page             r  force refresh          q   quit
 */

import { buildTextGraph, gutterString, LANE_COLORS } from './lanes-text.mjs';

const ESC = '\x1b';
const alt = (on) => `${ESC}[?1049${on ? 'h' : 'l'}`;
const cursor = (on) => `${ESC}[?25${on ? 'h' : 'l'}`;
const home = `${ESC}[H`;
const clearDown = `${ESC}[J`;
const RESET = `${ESC}[0m`;

const fg = (c, s) => `${ESC}[38;5;${c}m${s}${RESET}`;
const bg = (c, s) => `${ESC}[48;5;${c}m${s}${RESET}`;
const bold = (s) => `${ESC}[1m${s}${RESET}`;
const dim = (s) => `${ESC}[2m${s}${RESET}`;

const C = { dim: 244, accent: 39, ok: 78, warn: 179, bad: 203, head: 213, chrome: 240 };

/** Visible width, ignoring ANSI sequences. */
function vlen(s) {
  return s.replace(/\x1b\[[0-9;]*m/g, '').length;
}
function pad(s, w) {
  const l = vlen(s);
  return l >= w ? clip(s, w) : s + ' '.repeat(w - l);
}
/** Clip to width without cutting an escape sequence in half. */
function clip(s, w) {
  let out = '', n = 0, i = 0;
  while (i < s.length && n < w) {
    if (s[i] === '\x1b') {
      const m = /^\x1b\[[0-9;]*m/.exec(s.slice(i));
      if (m) { out += m[0]; i += m[0].length; continue; }
    }
    out += s[i]; i++; n++;
  }
  return out + (n >= w ? RESET : '');
}

const VIEWS = ['graph', 'plan', 'collisions', 'worktrees'];

export function startTui(repo, { onQuit } = {}) {
  const state = { model: null, view: 'graph', scroll: 0, status: 'starting...', rows: [] };
  const out = process.stdout;

  function size() {
    return { w: out.columns || 120, h: out.rows || 40 };
  }

  function setModel(model) {
    state.model = model;
    state.rows = buildTextGraph(model);
    draw();
  }
  function setStatus(s) { state.status = s; draw(); }

  // ------------------------------------------------------------------ views

  function viewGraph(w) {
    const m = state.model;
    const lines = [];
    for (const r of state.rows) {
      const gutter = gutterString(r, { color: true, laneCount: m.laneCount });
      if (r.kind === 'link') { lines.push(gutter); continue; }
      const c = r.commit;
      let refs = '';
      for (const ref of c.refs || []) {
        const isHead = ref.startsWith('HEAD -> ');
        const label = isHead ? ref.slice(8) : ref;
        if (!isHead && label.includes('/') && m.branches.some((b) => label.endsWith('/' + b.name))) continue;
        refs += ' ' + fg(isHead ? C.head : C.accent, label);
      }
      const left = `${gutter} ${fg(C.dim, c.short)}${refs} `;
      lines.push(pad(left + clip(c.subject, Math.max(10, w - vlen(left) - 14)), w - 12) +
                 fg(C.dim, c.date.slice(0, 10)));
    }
    return lines;
  }

  function stateColor(s) {
    return s === 'active' ? C.ok : s === 'stale' ? C.warn : s === 'dead' ? C.bad : C.dim;
  }

  function viewPlan(w) {
    const m = state.model;
    const lines = [];
    lines.push(dim(pad('  #  branch', 44) + pad('ahead', 7) + pad('behind', 8) + pad('cf', 5) + 'reasoning'));
    lines.push(fg(C.chrome, '─'.repeat(w)));
    if (!m.plan.length) lines.push(dim('  nothing to merge'));
    for (const p of m.plan) {
      const b = m.branches.find((x) => x.name === p.name) || {};
      const wt = (b.worktrees && b.worktrees.length) ? fg(C.head, '⬖') : ' ';
      const cf = p.conflicts == null ? dim('  —') : (p.conflicts ? fg(C.bad, String(p.conflicts).padStart(3))
                                                                : fg(C.ok, '  0'));
      const name = fg(stateColor(p.state), clip(p.name, 34));
      lines.push(pad(` ${String(p.order).padStart(2)} ${wt} ${name}`, 44) +
                 pad(String(p.ahead), 7) + pad(String(p.behind), 8) + pad(cf, 5) +
                 dim(clip(p.why.join(' · '), Math.max(10, w - 64))));
    }
    const rest = m.branches.filter((b) => b.state === 'merged' || b.state === 'dead');
    if (rest.length) {
      lines.push('');
      lines.push(fg(C.chrome, `  not in the plan (${rest.length})`));
      for (const b of rest) {
        const note = b.state === 'merged' ? `already in ${m.base} — safe to delete`
                                          : `${b.behind} behind; pre-rewrite`;
        lines.push(pad('   ' + fg(stateColor(b.state), clip(b.name, 40)), 46) + dim(note));
      }
    }
    return lines;
  }

  function viewCollisions(w) {
    const m = state.model;
    const lines = [];
    if (!m.collisions.length) {
      lines.push(dim('  No branch pairs share changed files, so nothing can collide.'));
      return lines;
    }
    lines.push(dim('  Overlap is the pre-filter. Conflicts come from merging the two tips, so they'));
    lines.push(dim(`  can exceed the overlap — each branch also carries its own drift from ${m.base}.`));
    lines.push('');
    lines.push(dim(pad('  branch a', 34) + pad('branch b', 34) + pad('ovl', 5) + pad('cf', 5) + 'paths'));
    lines.push(fg(C.chrome, '─'.repeat(w)));
    const sorted = m.collisions.filter((c) => c.conflicts.length)
                     .concat(m.collisions.filter((c) => !c.conflicts.length));
    for (const c of sorted) {
      const bad = c.conflicts.length > 0;
      const unknown = c.clean === null;
      const cell = bad ? fg(C.bad, String(c.conflicts.length)) : (unknown ? dim('?') : fg(C.ok, 'ok'));
      const note = bad ? c.conflicts.join(', ')
                       : (unknown ? 'could not probe on this git version' : 'overlap merges cleanly');
      lines.push(pad('  ' + clip(c.a, 32), 34) + pad(clip(c.b, 32), 34) +
                 pad(String(c.shared), 5) + pad(cell, 5) +
                 dim(clip(note, Math.max(10, w - 80))));
    }
    return lines;
  }

  function viewWorktrees(w) {
    const m = state.model;
    const lines = [];
    lines.push(dim(pad('  branch', 40) + pad('head', 10) + pad('flags', 22) + 'path'));
    lines.push(fg(C.chrome, '─'.repeat(w)));
    for (const t of m.worktrees) {
      const flags = [t.isMain && 'main', t.detached && fg(C.warn, 'detached'),
                     t.locked && 'locked', t.prunable && fg(C.bad, 'prunable')].filter(Boolean).join(' ');
      lines.push(pad('  ' + (t.branch ? fg(C.accent, clip(t.branch, 36)) : dim('(detached)')), 40) +
                 pad(fg(C.dim, t.head || ''), 10) + pad(flags || dim('—'), 22) +
                 dim(clip(t.path, Math.max(10, w - 72))));
    }
    return lines;
  }

  // ------------------------------------------------------------------- draw

  function body(w) {
    if (!state.model) return [dim('  reading repository...')];
    if (state.view === 'plan') return viewPlan(w);
    if (state.view === 'collisions') return viewCollisions(w);
    if (state.view === 'worktrees') return viewWorktrees(w);
    return viewGraph(w);
  }

  let drawScheduled = false;
  function draw() {
    if (drawScheduled) return;
    drawScheduled = true;
    setImmediate(() => { drawScheduled = false; render(); });
  }

  function render() {
    const { w, h } = size();
    const m = state.model;
    const lines = body(w);
    const viewport = Math.max(1, h - 3);
    const maxScroll = Math.max(0, lines.length - viewport);
    if (state.scroll > maxScroll) state.scroll = maxScroll;

    const head = m
      ? bold(fg(C.accent, m.repo)) + dim(`  base ${m.base}  ·  ` +
          (m.truncatedCommits ? `${m.commits.length} of ${m.totalCommits} commits` : `${m.commits.length} commits`) + `  ·  ` +
          `${m.branches.length} branches  ·  ${m.worktrees.length} worktrees` +
          (m.hiddenFromGraph > 0 ? `  ·  ${m.hiddenFromGraph} not drawn` : '')) +
        (m.probed ? '' : fg(C.warn, '  · probing…'))
      : bold('landfirst');

    const tabs = VIEWS.map((v, i) => {
      const label = ` ${i + 1} ${v} `;
      return v === state.view ? bg(236, fg(15, bold(label))) : dim(label);
    }).join('');

    const frame = [pad(head, w), pad(tabs, w), fg(C.chrome, '─'.repeat(w))];
    for (let i = 0; i < viewport - 1; i++) {
      frame.push(pad(lines[state.scroll + i] ?? '', w));
    }
    const pos = lines.length ? `${Math.min(state.scroll + viewport - 1, lines.length)}/${lines.length}` : '0/0';
    frame.push(pad(dim(`  ${state.status}`), w - 22) + dim(pos.padStart(12)) + dim('  q quit '));

    out.write(home + frame.join('\n') + clearDown);
  }

  // ------------------------------------------------------------------ input

  function onKey(buf) {
    const k = buf.toString();
    const { h } = size();
    const page = Math.max(1, h - 5);
    const lines = state.model ? body(size().w).length : 0;
    const maxScroll = Math.max(0, lines - (h - 3));

    if (k === 'q' || k === '\x03') return quit();
    else if (k === 'j' || k === '\x1b[B') state.scroll = Math.min(maxScroll, state.scroll + 1);
    else if (k === 'k' || k === '\x1b[A') state.scroll = Math.max(0, state.scroll - 1);
    else if (k === '\x1b[6~' || k === ' ') state.scroll = Math.min(maxScroll, state.scroll + page);
    else if (k === '\x1b[5~') state.scroll = Math.max(0, state.scroll - page);
    else if (k === 'g' || k === '\x1b[H') state.scroll = 0;
    else if (k === 'G' || k === '\x1b[F') state.scroll = maxScroll;
    else if (k === '\t') { state.view = VIEWS[(VIEWS.indexOf(state.view) + 1) % VIEWS.length]; state.scroll = 0; }
    else if (k >= '1' && k <= '4') { state.view = VIEWS[+k - 1]; state.scroll = 0; }
    else if (k === 'r') { state.status = 'refreshing...'; state.onRefresh?.(); }
    else return;
    draw();
  }

  let closed = false;
  function quit() {
    if (closed) return;
    closed = true;
    try { process.stdin.setRawMode?.(false); } catch { /* not a tty */ }
    process.stdin.pause();
    process.stdin.off('data', onKey);
    out.write(cursor(true) + alt(false));
    onQuit?.();
  }

  out.write(alt(true) + cursor(false) + home + clearDown);
  if (process.stdin.isTTY) {
    process.stdin.setRawMode(true);
    process.stdin.resume();
    process.stdin.on('data', onKey);
  }
  out.on('resize', draw);
  for (const sig of ['SIGINT', 'SIGTERM']) process.on(sig, quit);
  process.on('exit', () => { if (!closed) out.write(cursor(true) + alt(false)); });
  draw();

  return { setModel, setStatus, quit, state };
}
