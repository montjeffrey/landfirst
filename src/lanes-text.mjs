/**
 * Text-grid lane graph, shared by the TUI and the Markdown output.
 *
 * git's own --graph draws lane changes with `\` and `/` on the commit rows,
 * which is where the blocky look comes from. This emits a dedicated link line
 * for topology changes and draws them with box-drawing corners instead, so a
 * branch reads as a curve:
 *
 *   ● commit on lane 0
 *   ├─╮            <- link line: lane 0 branches right
 *   │ ● commit on lane 1
 *   ● │ commit on lane 0
 *   ├─╯            <- link line: lane 1 merges back
 */

const V = '│';       // │
const H = '─';       // ─
const TEE_R = '├';   // ├
const TEE_L = '┤';   // ┤
const CORNER_DL = '╮'; // ╮  arriving from the left, turning down
const CORNER_DR = '╭'; // ╭  arriving from the right, turning down
const CROSS = '┼';   // ┼
const DOT = '●';     // ●
const RING = '○';    // ○  merge commit
const TIP = '◉';     // ◉  branch tip

export const LANE_COLORS = [39, 78, 203, 141, 179, 43, 215, 111, 176, 79, 221, 167];

export function ansi(code, s, on = true) {
  return on ? `\x1b[38;5;${code}m${s}\x1b[0m` : s;
}

/**
 * @returns array of rows: { kind: 'commit'|'link', cells: string[], colors: (number|null)[], commit }
 * `cells` is one character per lane; the caller joins them with a space.
 */
export function buildTextGraph(model) {
  const { commits, edges, laneCount } = model;
  const nRows = commits.length;

  // Which lanes have a line passing vertically through each commit row.
  const open = Array.from({ length: nRows }, () => new Map());
  for (const e of edges) {
    if (e.stub || e.toRow == null) continue;
    const lane = e.toLane;
    for (let r = e.fromRow + 1; r < e.toRow; r++) open[r].set(lane, e.color);
  }

  const tipLanes = new Set();
  for (const c of commits) if (c.refs && c.refs.length) tipLanes.add(c.row);

  const out = [];
  for (let r = 0; r < nRows; r++) {
    const c = commits[r];
    const cells = new Array(laneCount).fill(' ');
    const colors = new Array(laneCount).fill(null);

    for (const [lane, col] of open[r]) { cells[lane] = V; colors[lane] = col; }
    cells[c.lane] = c.parents.length > 1 ? RING : (tipLanes.has(r) ? TIP : DOT);
    colors[c.lane] = c.color;
    out.push({ kind: 'commit', cells, colors, commit: c });

    const jogs = edges.filter((e) => !e.stub && e.fromRow === r && e.fromLane !== e.toLane);
    if (!jogs.length) continue;

    const lc = new Array(laneCount).fill(' ');
    const lcol = new Array(laneCount).fill(null);
    const nextOpen = r + 1 < nRows ? open[r + 1] : new Map();
    for (const [lane, col] of nextOpen) { lc[lane] = V; lcol[lane] = col; }

    for (const e of jogs) {
      const from = e.fromLane, to = e.toLane;
      const lo = Math.min(from, to), hi = Math.max(from, to);
      for (let x = lo + 1; x < hi; x++) {
        lc[x] = (lc[x] === V || lc[x] === CROSS) ? CROSS : H;
        if (lcol[x] == null) lcol[x] = e.color;
      }
      lc[from] = to > from ? TEE_R : TEE_L;
      lcol[from] = e.color;
      lc[to] = to > from ? CORNER_DL : CORNER_DR;
      lcol[to] = e.color;
    }
    out.push({ kind: 'link', cells: lc, colors: lcol, commit: null });
  }
  return out;
}

/** Join one row's cells into a printable gutter, optionally coloured. */
export function gutterString(row, { color = true, laneCount } = {}) {
  const n = laneCount ?? row.cells.length;
  let s = '';
  for (let i = 0; i < n; i++) {
    const ch = row.cells[i] ?? ' ';
    const col = row.colors[i];
    s += (color && col != null && ch !== ' ')
      ? ansi(LANE_COLORS[col % LANE_COLORS.length], ch)
      : ch;
    // A horizontal run must not be broken by the inter-lane gap.
    if (i < n - 1) {
      const nxt = row.cells[i + 1] ?? ' ';
      const bridge = (ch === H || ch === TEE_R || ch === CROSS) &&
                     (nxt === H || nxt === CROSS || nxt === CORNER_DL || nxt === TEE_L);
      const c2 = bridge ? H : ' ';
      s += (color && bridge && col != null)
        ? ansi(LANE_COLORS[col % LANE_COLORS.length], c2)
        : c2;
    }
  }
  return s;
}
