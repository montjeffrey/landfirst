/** Self-contained HTML page. With `live`, it subscribes to SSE and re-renders in place. */

export function escapeHtml(s) {
  return String(s ?? '').replace(/[&<>"']/g, (c) =>
    ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}

const CSS = `
:root{--bg:#181c24;--bg2:#1f242e;--bg3:#262c38;--line:#333b49;--fg:#c3cbd8;
  --fg-dim:#7b869a;--fg-bright:#e6ecf5;--ok:#98c379;--warn:#e5c07b;--bad:#e06c75;
  --accent:#61afef;--row:30px}
*{box-sizing:border-box}
html,body{height:100%}
body{margin:0;background:var(--bg);color:var(--fg);font:13px/1.5 "Cascadia Code","JetBrains Mono",Consolas,ui-monospace,monospace}
header{display:flex;align-items:center;gap:16px;padding:10px 16px;background:var(--bg2);border-bottom:1px solid var(--line);position:sticky;top:0;z-index:20;flex-wrap:wrap}
h1{font-size:14px;margin:0;font-weight:600;color:var(--fg-bright)}
.meta{color:var(--fg-dim);font-size:11.5px}
#live{font-size:11px;padding:2px 9px;border-radius:9px;border:1px solid #3d5a37;color:var(--ok)}
#live.stale{border-color:#5c4f2a;color:var(--warn)}
#live.off{border-color:#5e3236;color:var(--bad)}
nav{display:flex;gap:4px;margin-left:auto}
nav button{background:transparent;border:1px solid transparent;color:var(--fg-dim);padding:5px 12px;border-radius:6px;cursor:pointer;font:inherit;font-size:12px}
nav button:hover{color:var(--fg-bright);background:var(--bg3)}
nav button[aria-selected="true"]{background:var(--bg3);color:var(--fg-bright);border-color:var(--line)}
#search{background:var(--bg);border:1px solid var(--line);color:var(--fg);padding:5px 10px;border-radius:6px;font:inherit;font-size:12px;width:200px}
#search:focus{outline:none;border-color:var(--accent)}
main{height:calc(100vh - 49px);overflow:hidden}
.pane{height:100%;display:none}
.pane.on{display:flex}
#graphScroll{flex:1;overflow:auto;position:relative}
#graphInner{position:relative}
#lanes{position:absolute;top:0;left:0;pointer-events:none;overflow:visible}
#rows{position:relative}
.r{height:var(--row);display:flex;align-items:center;gap:8px;padding-right:16px;overflow:hidden;white-space:nowrap;cursor:pointer;border-left:2px solid transparent}
.r:hover{background:var(--bg2)}
.r.sel{background:var(--bg3);border-left-color:var(--accent)}
.r.dim{opacity:.22}
.r.lit{background:rgba(97,175,239,.08)}
.sha{color:var(--fg-dim);font-size:11.5px;flex:none}
.subj{overflow:hidden;text-overflow:ellipsis;flex:1 1 auto;min-width:60px}
.who{color:var(--fg-dim);font-size:11.5px;flex:none;max-width:110px;overflow:hidden;text-overflow:ellipsis}
.when{color:var(--fg-dim);font-size:11.5px;flex:none;width:88px;text-align:right}
.badge{font-size:10.5px;padding:1px 7px;border-radius:9px;flex:none;border:1px solid;background:var(--bg3);white-space:nowrap}
.b-head{color:#fff;border-color:var(--accent);background:var(--accent)}
.b-local{color:var(--accent);border-color:#2f4c68}
.b-remote{color:var(--fg-dim);border-color:#3a4250}
.b-tag{color:var(--warn);border-color:#5c4f2a}
.b-wt{color:#c678dd;border-color:#553a63}
#detail{width:360px;flex:none;border-left:1px solid var(--line);background:var(--bg2);overflow:auto;padding:16px}
#detail h2{font-size:12px;text-transform:uppercase;letter-spacing:.8px;color:var(--fg-dim);margin:0 0 10px;font-weight:600}
#detail .msg{white-space:pre-wrap;color:var(--fg-bright);margin:0 0 14px;font-size:12.5px}
#detail dl{display:grid;grid-template-columns:auto 1fr;gap:4px 12px;margin:0 0 16px;font-size:12px}
#detail dt{color:var(--fg-dim)}
#detail dd{margin:0;word-break:break-all}
.files{list-style:none;margin:0;padding:0;font-size:12px}
.files li{display:flex;gap:8px;padding:2px 0}
.st{width:14px;flex:none;text-align:center;font-weight:700}
.st.A{color:var(--ok)}.st.M{color:var(--warn)}.st.D{color:var(--bad)}
.fp{overflow:hidden;text-overflow:ellipsis;direction:rtl;text-align:left}
.tablewrap{flex:1;overflow:auto;padding:16px}
table{border-collapse:collapse;width:100%;font-size:12px}
th{text-align:left;color:var(--fg-dim);font-weight:600;font-size:11px;text-transform:uppercase;letter-spacing:.6px;padding:8px 10px;border-bottom:1px solid var(--line);position:sticky;top:0;background:var(--bg);z-index:2}
td{padding:7px 10px;border-bottom:1px solid #232935;vertical-align:top}
tr:hover td{background:var(--bg2)}
.num{text-align:right;font-variant-numeric:tabular-nums}
.pill{display:inline-block;font-size:10.5px;padding:1px 8px;border-radius:9px;border:1px solid}
.s-active{color:var(--ok);border-color:#3d5a37}
.s-stale{color:var(--warn);border-color:#5c4f2a}
.s-merged{color:var(--fg-dim);border-color:#3a4250}
.s-dead{color:var(--bad);border-color:#5e3236}
.why{color:var(--fg-dim);font-size:11.5px}
.why b{color:var(--bad);font-weight:600}
.why i{color:var(--ok);font-style:normal}
.rank{font-size:16px;font-weight:700;color:var(--fg-bright);width:34px}
a{color:var(--accent);text-decoration:none;border-bottom:1px solid transparent}
a:visited{color:var(--accent)}
a:hover{border-bottom-color:var(--accent)}
code{background:var(--bg3);padding:1px 5px;border-radius:4px;font-size:11.5px}
.empty{color:var(--fg-dim);padding:24px;text-align:center}
`;

export function renderHtml(model, { live = false } = {}) {
  const json = JSON.stringify(model).replace(/</g, '\\u003c');
  return `<!doctype html>
<html lang="en" data-theme="dark">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>landfirst - ${escapeHtml(model.repo)}</title>
<style>${CSS}</style>
</head>
<body>
<header>
  <h1>${escapeHtml(model.repo)}</h1>
  <span class="meta" id="meta"></span>
  <span class="meta" id="caps" style="color:var(--warn);display:none"></span>
  ${live ? '<span id="live">live</span>' : ''}
  <input id="search" type="search" placeholder="filter commits...">
  <nav>
    <button data-tab="graph" aria-selected="true">Graph</button>
    <button data-tab="plan" aria-selected="false">Merge plan</button>
    <button data-tab="collisions" aria-selected="false">Collisions</button>
    <button data-tab="worktrees" aria-selected="false">Worktrees</button>
  </nav>
</header>
<main>
  <section class="pane on" id="tab-graph">
    <div id="graphScroll"><div id="graphInner"><svg id="lanes"></svg><div id="rows"></div></div></div>
    <aside id="detail"></aside>
  </section>
  <section class="pane" id="tab-plan"><div class="tablewrap" id="planBody"></div></section>
  <section class="pane" id="tab-collisions"><div class="tablewrap" id="colBody"></div></section>
  <section class="pane" id="tab-worktrees"><div class="tablewrap" id="wtBody"></div></section>
</main>
<script id="data" type="application/json">${json}</script>
<script>
var LIVE = ${live ? 'true' : 'false'};
${client()}
</script>
</body>
</html>`;
}

// Client code. Kept free of backticks and ${} so it survives being embedded in
// the template literal above without escaping games.
function clientA() {
  return `
var D = JSON.parse(document.getElementById('data').textContent);
var ROW = 30, LANE = 20, PAD = 16, DOT = 4.5;
var COLORS = ['#61afef','#98c379','#e06c75','#c678dd','#e5c07b','#56b6c2',
              '#d19a66','#7f9fd4','#b294bb','#8abeb7','#f0c674','#cc6666'];
var GUTTER = 0, byHash = {}, rowEls = [], wtByBranch = {}, branchNames = {};
var curTab = 'graph';

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
    return { '&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;' }[c];
  });
}
function laneX(l) { return PAD + l * LANE; }
function rowY(r) { return r * ROW + ROW / 2; }
function col(i) { return COLORS[i % COLORS.length]; }

function index() {
  GUTTER = PAD * 2 + D.laneCount * LANE;
  byHash = {}; wtByBranch = {}; branchNames = {};
  D.commits.forEach(function (c) { byHash[c.hash] = c; });
  D.worktrees.forEach(function (w) {
    if (!w.branch) return;
    (wtByBranch[w.branch] = wtByBranch[w.branch] || []).push(w.path);
  });
  D.branches.forEach(function (b) { branchNames[b.name] = b; });
  branchNames[D.base] = branchNames[D.base] || { name: D.base, state: 'active' };
}

/* ---------- lane graph: quarter-turn bezier then a straight run ---------- */
function edgePath(e) {
  var x1 = laneX(e.fromLane), y1 = rowY(e.fromRow);
  if (e.stub) return 'M' + x1 + ',' + y1 + 'L' + x1 + ',' + (y1 + ROW * 0.8);
  var x2 = laneX(e.toLane), y2 = rowY(e.toRow);
  if (x1 === x2) return 'M' + x1 + ',' + y1 + 'L' + x2 + ',' + y2;
  var dy = y2 - y1, k = ROW * 0.85;
  if (dy > 2.2 * k) {
    return 'M' + x1 + ',' + y1 +
           'C' + x1 + ',' + (y1 + k) + ' ' + x2 + ',' + (y1 + k) + ' ' + x2 + ',' + (y1 + 2 * k) +
           'L' + x2 + ',' + y2;
  }
  return 'M' + x1 + ',' + y1 +
         'C' + x1 + ',' + (y1 + dy * 0.5) + ' ' + x2 + ',' + (y2 - dy * 0.5) + ' ' + x2 + ',' + y2;
}

function drawGraph() {
  var svg = document.getElementById('lanes');
  var h = D.commits.length * ROW;
  svg.setAttribute('width', GUTTER);
  svg.setAttribute('height', h);
  svg.setAttribute('viewBox', '0 0 ' + GUTTER + ' ' + h);
  var out = [];
  D.edges.forEach(function (e) {
    out.push('<path d="' + edgePath(e) + '" fill="none" stroke="' + col(e.color) +
             '" stroke-width="2" stroke-linecap="round" opacity="' + (e.stub ? 0.35 : 0.85) + '"/>');
  });
  D.commits.forEach(function (c) {
    var x = laneX(c.lane), y = rowY(c.row), merge = c.parents.length > 1;
    out.push('<circle cx="' + x + '" cy="' + y + '" r="' + (merge ? DOT + 1.5 : DOT) +
             '" fill="' + (merge ? 'var(--bg)' : col(c.color)) + '" stroke="' + col(c.color) +
             '" stroke-width="2.5"/>');
  });
  svg.innerHTML = out.join('');
  document.getElementById('rows').style.marginLeft = GUTTER + 'px';
}
`;
}

function clientB() {
  return `
/* ---------- commit rows ---------- */
function refBadges(c) {
  var refs = c.refs || [];
  // origin/X next to X on the same commit is noise; collapse it into one badge
  // with an arrow so the subject line keeps its width.
  var locals = {}, remoteTails = {};
  refs.forEach(function (r) {
    var n = r.indexOf('HEAD -> ') === 0 ? r.slice(8) : r;
    if (branchNames[n]) locals[n] = 1;
    else if (n.indexOf('/') > -1 && n.indexOf('tag: ') !== 0) remoteTails[n.slice(n.indexOf('/') + 1)] = 1;
  });
  var cut = function (s) { return s.length > 26 ? s.slice(0, 25) + '\\u2026' : s; };
  var parts = [];
  refs.forEach(function (r) {
    var cls = 'b-local', label = r;
    if (r.indexOf('HEAD -> ') === 0) { cls = 'b-head'; label = r.slice(8); }
    else if (r === 'HEAD') { cls = 'b-head'; }
    else if (r.indexOf('tag: ') === 0) { cls = 'b-tag'; label = r.slice(5); }
    else if (!branchNames[r] && r.indexOf('/') > -1) {
      if (locals[r.slice(r.indexOf('/') + 1)]) return;
      cls = 'b-remote';
    }
    var tracked = (cls === 'b-local' || cls === 'b-head') && remoteTails[label] ? ' \\u2191' : '';
    parts.push('<span class="badge ' + cls + '" data-branch="' + esc(label) + '" title="' + esc(label) + '">' +
               esc(cut(label)) + tracked + '</span>');
    var wts = wtByBranch[label];
    if (wts) wts.forEach(function (p) {
      var leaf = p.split(/[\\\\/]/).filter(Boolean).pop();
      parts.push('<span class="badge b-wt" title="' + esc(p) + '">' + esc(cut(leaf)) + '</span>');
    });
  });
  if (parts.length > 4) parts = parts.slice(0, 3).concat(['<span class="badge b-remote">+' + (parts.length - 3) + '</span>']);
  return parts.join('');
}

function drawRows() {
  var host = document.getElementById('rows');
  host.textContent = '';
  rowEls = [];
  var frag = document.createDocumentFragment();
  D.commits.forEach(function (c) {
    var d = document.createElement('div');
    d.className = 'r';
    d.dataset.hash = c.hash;
    d.innerHTML = refBadges(c) +
      '<span class="subj">' + esc(c.subject) + '</span>' +
      '<span class="who">' + esc(c.author) + '</span>' +
      '<span class="sha">' + esc(c.short) + '</span>' +
      '<span class="when">' + esc(c.date.slice(0, 10)) + '</span>';
    frag.appendChild(d);
    rowEls.push(d);
  });
  host.appendChild(frag);
  document.getElementById('graphInner').style.height = (D.commits.length * ROW) + 'px';
}

function showCommit(c) {
  if (!c) { document.getElementById('detail').innerHTML = ''; return; }
  var wt = [];
  (c.refs || []).forEach(function (r) {
    var n = r.indexOf('HEAD -> ') === 0 ? r.slice(8) : r;
    if (wtByBranch[n]) wtByBranch[n].forEach(function (p) { wt.push(p); });
  });
  var h = '<h2>Commit</h2><p class="msg">' + esc(c.subject) + (c.body ? '\\n\\n' + esc(c.body) : '') + '</p><dl>' +
          '<dt>hash</dt><dd>' + esc(c.hash) + '</dd>' +
          '<dt>author</dt><dd>' + esc(c.author) + '</dd>' +
          '<dt>date</dt><dd>' + esc(c.date.replace('T', ' ').slice(0, 19)) + '</dd>' +
          '<dt>parents</dt><dd>' + (c.parents.map(function (p) { return esc(p.slice(0, 7)); }).join(', ') || 'root') + '</dd>' +
          (c.refs.length ? '<dt>refs</dt><dd>' + esc(c.refs.join(', ')) + '</dd>' : '') +
          (wt.length ? '<dt>worktree</dt><dd>' + wt.map(esc).join('<br>') + '</dd>' : '') +
          '</dl><h2>Files (' + c.fileCount + ')</h2><ul class="files">';
  c.files.forEach(function (f) {
    h += '<li><span class="st ' + esc(f.status[0]) + '">' + esc(f.status) + '</span>' +
         '<span class="fp" title="' + esc(f.path) + '">' + esc(f.path) + '</span></li>';
  });
  if (c.fileCount > c.files.length) h += '<li class="why">... ' + (c.fileCount - c.files.length) + ' more</li>';
  document.getElementById('detail').innerHTML = h + '</ul>';
}

function highlightFrom(hash) {
  var seen = {}, stack = [hash];
  while (stack.length) {
    var hsh = stack.pop();
    if (seen[hsh] || !byHash[hsh]) continue;
    seen[hsh] = 1;
    byHash[hsh].parents.forEach(function (p) { stack.push(p); });
  }
  rowEls.forEach(function (el) {
    var on = seen[el.dataset.hash];
    el.classList.toggle('lit', !!on);
    el.classList.toggle('dim', !on);
  });
}
function clearHighlight() { rowEls.forEach(function (el) { el.classList.remove('lit', 'dim'); }); }
`;
}

function clientC() {
  return `
/* ---------- tables ---------- */
function whyHtml(list) {
  return list.map(function (w) {
    if (/conflict|collide|behind|ago/.test(w)) return '<b>' + esc(w) + '</b>';
    if (/cleanly/.test(w)) return '<i>' + esc(w) + '</i>';
    return esc(w);
  }).join(' &middot; ');
}

function drawPlan() {
  var h = '<table><thead><tr><th></th><th>Branch</th><th class="num">Ahead</th><th class="num">Behind</th>' +
          '<th class="num">Conflicts</th><th>State</th><th>Reasoning</th></tr></thead><tbody>';
  if (!D.plan.length) h += '<tr><td colspan="7" class="empty">Nothing to merge.</td></tr>';
  D.plan.forEach(function (p) {
    var wts = wtByBranch[p.name] || [];
    h += '<tr><td class="rank">' + p.order + '</td>' +
         '<td><a href="#" data-goto="' + esc(p.name) + '">' + esc(p.name) + '</a>' +
         (wts.length ? ' <span class="badge b-wt">wt</span>' : '') + '</td>' +
         '<td class="num">' + p.ahead + '</td><td class="num">' + p.behind + '</td>' +
         '<td class="num">' + (p.conflicts === null ? '?' : p.conflicts) + '</td>' +
         '<td><span class="pill s-' + esc(p.state) + '">' + esc(p.state) + '</span></td>' +
         '<td class="why">' + whyHtml(p.why) + '</td></tr>';
  });
  h += '</tbody></table>';
  var rest = D.branches.filter(function (b) { return b.state === 'merged' || b.state === 'dead'; });
  if (rest.length) {
    h += '<h2 style="font-size:12px;text-transform:uppercase;letter-spacing:.8px;color:var(--fg-dim);margin:26px 0 10px">' +
         'Not in the plan (' + rest.length + ')</h2>' +
         '<table><thead><tr><th>Branch</th><th class="num">Ahead</th><th class="num">Behind</th>' +
         '<th class="num">Age</th><th>State</th><th>Note</th></tr></thead><tbody>';
    rest.forEach(function (b) {
      var note = b.state === 'merged' ? 'already contained in ' + D.base + ' - safe to delete'
                                      : b.behind + ' commits behind; forked before a history rewrite';
      h += '<tr><td>' + esc(b.name) + '</td><td class="num">' + b.ahead + '</td><td class="num">' + b.behind +
           '</td><td class="num">' + b.ageDays + 'd</td>' +
           '<td><span class="pill s-' + esc(b.state) + '">' + esc(b.state) + '</span></td>' +
           '<td class="why">' + esc(note) + '</td></tr>';
    });
    h += '</tbody></table>';
  }
  document.getElementById('planBody').innerHTML = h;
}

function drawCollisions() {
  var h = '';
  if (!D.collisions.length) {
    h = '<div class="empty">No branch pairs share changed files, so nothing can collide.</div>';
  } else {
    h += '<p class="why" style="margin:0 0 14px;max-width:74ch">Overlap is the pre-filter: pairs sharing at least ' +
         'one changed file are dry-run merged with <code>git merge-tree</code>. The conflict count is the full ' +
         'result of merging the two tips, so it can exceed the overlap &mdash; each branch also carries its own ' +
         'drift from ' + esc(D.base) + '. Pairs sharing no files are never probed.</p>';
    h += '<table><thead><tr><th>Branch A</th><th>Branch B</th><th class="num">Overlap</th>' +
         '<th class="num">Conflicts</th><th>Conflicting paths</th></tr></thead><tbody>';
    var bad = D.collisions.filter(function (c) { return c.conflicts.length > 0; });
    var ok = D.collisions.filter(function (c) { return c.conflicts.length === 0; });
    bad.concat(ok).forEach(function (c) {
      var isBad = c.conflicts.length > 0;
      var unknown = c.clean === null;
      // "could not probe" must never be painted as "clean".
      var colr = isBad ? 'var(--bad)' : (unknown ? 'var(--fg-dim)' : 'var(--ok)');
      var cell = isBad ? c.conflicts.length : (unknown ? '?' : 'clean');
      var note = isBad
        ? c.conflicts.slice(0, 6).map(esc).join('<br>') +
          (c.conflicts.length > 6 ? '<br>... ' + (c.conflicts.length - 6) + ' more' : '')
        : (unknown ? 'could not probe on this git version' : 'overlap merges cleanly');
      h += '<tr><td>' + esc(c.a) + '</td><td>' + esc(c.b) + '</td><td class="num">' + c.shared + '</td>' +
           '<td class="num" style="color:' + colr + '">' + cell + '</td>' +
           '<td class="why">' + note + '</td></tr>';
    });
    h += '</tbody></table>';
  }
  document.getElementById('colBody').innerHTML = h;
}

function drawWorktrees() {
  var h = '<table><thead><tr><th>Path</th><th>Branch</th><th>HEAD</th><th>Flags</th></tr></thead><tbody>';
  D.worktrees.forEach(function (w) {
    var f = [];
    if (w.isMain) f.push('main');
    if (w.detached) f.push('<span style="color:var(--warn)">detached</span>');
    if (w.locked) f.push('locked');
    if (w.prunable) f.push('<span style="color:var(--bad)">prunable</span>');
    h += '<tr><td style="word-break:break-all">' + esc(w.path) + '</td><td>' +
         (w.branch ? '<a href="#" data-goto="' + esc(w.branch) + '">' + esc(w.branch) + '</a>' : '<span class="why">-</span>') +
         '</td><td><code>' + esc(w.head || '') + '</code></td><td class="why">' + (f.join(', ') || '-') + '</td></tr>';
  });
  document.getElementById('wtBody').innerHTML = h + '</tbody></table>';
}
`;
}

function clientD() {
  return `
/* ---------- wiring ---------- */
function selectRow(el) {
  rowEls.forEach(function (r) { r.classList.remove('sel'); });
  if (!el) return;
  el.classList.add('sel');
  showCommit(byHash[el.dataset.hash]);
}

function gotoBranch(name) {
  var b = branchNames[name];
  var head = b && b.head ? b.head : null;
  if (!head || !byHash[head]) {
    for (var i = 0; i < D.commits.length; i++) {
      var refs = D.commits[i].refs || [];
      for (var j = 0; j < refs.length; j++) {
        var r = refs[j].indexOf('HEAD -> ') === 0 ? refs[j].slice(8) : refs[j];
        if (r === name) { head = D.commits[i].hash; break; }
      }
      if (head && byHash[head]) break;
    }
  }
  if (!head || !byHash[head]) return false;
  showTab('graph');
  highlightFrom(head);
  var el = rowEls[byHash[head].row];
  selectRow(el);
  el.scrollIntoView({ block: 'center' });
  return true;
}

function showTab(name) {
  curTab = name;
  ['graph', 'plan', 'collisions', 'worktrees'].forEach(function (t) {
    document.getElementById('tab-' + t).classList.toggle('on', t === name);
  });
  document.querySelectorAll('nav button').forEach(function (b) {
    b.setAttribute('aria-selected', String(b.dataset.tab === name));
  });
}

function applyFilter() {
  var q = document.getElementById('search').value.trim().toLowerCase();
  if (!q) { clearHighlight(); return; }
  rowEls.forEach(function (el) {
    var c = byHash[el.dataset.hash];
    var hay = (c.subject + ' ' + c.author + ' ' + c.short + ' ' + c.refs.join(' ')).toLowerCase();
    var on = hay.indexOf(q) > -1;
    el.classList.toggle('lit', on);
    el.classList.toggle('dim', !on);
  });
}

function setMeta() {
  var warn = document.getElementById('caps');
  if (warn) {
    var legacy = D.capabilities && D.capabilities.conflictMode === 'legacy';
    warn.style.display = legacy ? '' : 'none';
    warn.textContent = legacy
      ? 'git ' + D.capabilities.version + ' - conflict probing uses the pre-2.38 merge-tree; upgrade to git 2.38+ for exact results'
      : '';
  }
  var shown = D.truncatedCommits
    ? D.commits.length + ' of ' + D.totalCommits + ' commits'
    : D.commits.length + ' commits';
  document.getElementById('meta').textContent =
    'base ' + D.base + '  \\u00b7  ' + shown + '  \\u00b7  ' +
    D.branches.length + ' branches  \\u00b7  ' + D.worktrees.length + ' worktrees  \\u00b7  ' +
    D.generatedAt.replace('T', ' ').slice(11, 19);
}

function rebuild() {
  index(); setMeta(); drawGraph(); drawRows(); drawPlan(); drawCollisions(); drawWorktrees();
}

/* Re-render without losing where the user was. */
function applyModel(next) {
  var scroller = document.getElementById('graphScroll');
  var top = scroller.scrollTop;
  var sel = document.querySelector('.r.sel');
  var selHash = sel ? sel.dataset.hash : null;
  D = next;
  rebuild();
  scroller.scrollTop = top;
  var keep = selHash && byHash[selHash] ? rowEls[byHash[selHash].row] : rowEls[0];
  selectRow(keep);
  applyFilter();
  showTab(curTab);
}
`;
}

function clientE() {
  return `
function connect() {
  var badge = document.getElementById('live');
  var es = new EventSource('/events');
  es.addEventListener('model', function (ev) {
    applyModel(JSON.parse(ev.data));
    if (badge) { badge.textContent = D.probed ? 'live' : 'probing'; badge.className = D.probed ? '' : 'stale'; }
  });
  es.onopen = function () { if (badge) { badge.textContent = 'live'; badge.className = ''; } };
  es.onerror = function () {
    if (badge) { badge.textContent = 'disconnected'; badge.className = 'off'; }
    // EventSource retries on its own; this only reports it.
  };
}

function init() {
  rebuild();
  if (D.commits.length) selectRow(rowEls[0]);

  document.getElementById('rows').addEventListener('click', function (ev) {
    var badge = ev.target.closest('.badge[data-branch]');
    if (badge) { ev.stopPropagation(); gotoBranch(badge.dataset.branch); return; }
    var row = ev.target.closest('.r');
    if (row) selectRow(row);
  });
  document.addEventListener('click', function (ev) {
    var a = ev.target.closest('[data-goto]');
    if (a) { ev.preventDefault(); gotoBranch(a.dataset.goto); }
  });
  document.querySelectorAll('nav button').forEach(function (b) {
    b.addEventListener('click', function () { showTab(b.dataset.tab); });
  });
  var s = document.getElementById('search');
  s.addEventListener('input', applyFilter);
  document.addEventListener('keydown', function (ev) {
    if (ev.key === 'Escape') { s.value = ''; clearHighlight(); }
    if (ev.key === '/' && document.activeElement !== s) { ev.preventDefault(); s.focus(); }
  });
  if (LIVE) connect();
}
init();
`;
}

function client() { return clientA() + clientB() + clientC() + clientD() + clientE(); }
