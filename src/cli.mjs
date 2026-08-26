#!/usr/bin/env node
/**
 * landfirst - branch/worktree map with merge-order analysis.
 *
 *   landfirst                 one-shot HTML report (written to a temp dir)
 *   landfirst --serve         live page over SSE, never stale
 *   landfirst --tui           live terminal UI, for Zed's terminal dock
 *   landfirst --md            live Markdown, for Zed's Markdown preview
 *
 * --serve, --tui and --md can be combined; they share one watcher and one
 * merge-probe cache, so running all three costs barely more than one.
 */

import { execFileSync, spawn } from 'node:child_process';
import { writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { tmpdir } from 'node:os';
import process from 'node:process';

import { openRepo, buildModel } from './core.mjs';
import { createWatcher } from './watch.mjs';
import { renderHtml } from './render-html.mjs';
import { toMarkdown } from './render-md.mjs';
import { startTui } from './tui.mjs';
import { startServer } from './serve.mjs';

function usage() {
  console.log(`landfirst - branch/worktree map with merge-order analysis

surfaces (combinable; default is one-shot HTML)
  --serve            live page on 127.0.0.1, updates over SSE
  --tui              live terminal UI (use inside Zed's terminal dock)
  --md               live Markdown report (path is printed on start)
  --open             open the HTML surface in a browser
  --app              open it as a chromeless window (implies --serve --open)

options
  --limit N          commits to render (default 400)
  --base BRANCH      integration branch (default main, else master)
  --all              include branches classified as dead
  --max-pairs N      cap on pairwise conflict probes (default 400)
  --port N           port for --serve (default: an ephemeral one)
  --graph-rows N     commit rows in the Markdown graph (default 60)
  --out FILE         output path for the one-shot HTML
`);
}

function parseArgs(argv) {
  const o = {
    limit: 400, base: null, all: false, maxPairs: 400, port: 0, graphRows: 60,
    out: null, serve: false, tui: false, md: false, open: false, app: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === '--limit') o.limit = parseInt(argv[++i], 10);
    else if (a === '--base') o.base = argv[++i];
    else if (a === '--max-pairs') o.maxPairs = parseInt(argv[++i], 10);
    else if (a === '--port') o.port = parseInt(argv[++i], 10);
    else if (a === '--graph-rows') o.graphRows = parseInt(argv[++i], 10);
    else if (a === '--out') o.out = argv[++i];
    else if (a === '--all') o.all = true;
    else if (a === '--serve') o.serve = true;
    else if (a === '--tui') o.tui = true;
    else if (a === '--md') o.md = true;
    else if (a === '--open') o.open = true;
    else if (a === '--app') { o.app = true; o.serve = true; o.open = true; }
    else if (a === '--help' || a === '-h') { usage(); process.exit(0); }
    else { console.error(`landfirst: unknown argument ${a}`); usage(); process.exit(2); }
  }
  if (!Number.isFinite(o.limit) || o.limit < 1) throw new Error('--limit must be a positive integer');
  return o;
}

/** Chromium-family binaries that support --app, per platform. */
function browserCandidates() {
  if (process.platform === 'win32') {
    const local = process.env.LOCALAPPDATA;
    return [
      'C:/Program Files/Google/Chrome/Application/chrome.exe',
      'C:/Program Files (x86)/Google/Chrome/Application/chrome.exe',
      local ? `${local}/Google/Chrome/Application/chrome.exe` : null,
      'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
      'C:/Program Files/Microsoft/Edge/Application/msedge.exe',
    ].filter(Boolean);
  }
  if (process.platform === 'darwin') {
    return [
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
      '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
      '/Applications/Brave Browser.app/Contents/MacOS/Brave Browser',
    ];
  }
  // Linux: prefer PATH lookup, since distro install locations vary too much.
  return ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser', 'microsoft-edge', 'brave-browser']
    .map((bin) => which(bin))
    .filter(Boolean);
}

/** Resolve a bare command name against PATH. */
function which(bin) {
  const dirs = (process.env.PATH || '').split(process.platform === 'win32' ? ';' : ':');
  for (const d of dirs) {
    if (!d) continue;
    const p = join(d, bin);
    try { if (existsSync(p)) return p; } catch { /* unreadable dir */ }
  }
  return null;
}

/** Chromeless window, so it sits beside the editor rather than in a browser. */
function openAppWindow(url) {
  for (const exe of browserCandidates()) {
    if (!existsSync(exe)) continue;
    // A dedicated profile dir keeps this window out of the user's browsing
    // session, so it does not inherit extensions or restore unrelated tabs.
    const prof = join(tmpdir(), 'landfirst-app-profile');
    try { mkdirSync(prof, { recursive: true }); } catch { /* already there */ }
    const child = spawn(exe, [
      `--app=${url}`,
      `--user-data-dir=${prof}`,
      '--no-first-run',
      '--no-default-browser-check',
      '--window-size=1280,900',
    ], { detached: true, stdio: 'ignore' });
    child.unref();
    return true;
  }
  return false;
}

function openDefault(target) {
  const plat = process.platform;
  const cmd = plat === 'win32' ? ['cmd', ['/c', 'start', '', target]]
            : plat === 'darwin' ? ['open', [target]]
            : ['xdg-open', [target]];
  try { execFileSync(cmd[0], cmd[1], { windowsHide: true }); } catch { /* non-fatal */ }
}

/**
 * Where reports go. Not inside .git: a 600 kB HTML rewritten on every recompute
 * is noise in the object store's directory, and on a cloud-synced repo it makes
 * the sync client churn over .git continuously. Temp is disposable and private.
 */
function reportDir(repo) {
  let h = 0x811c9dc5;
  const key = repo.root || repo.name;
  for (let i = 0; i < key.length; i++) { h ^= key.charCodeAt(i); h = Math.imul(h, 0x01000193) >>> 0; }
  const slug = (repo.name || 'repo').replace(/[^A-Za-z0-9._-]/g, '-').slice(0, 40);
  const dir = join(tmpdir(), 'landfirst', `${slug}-${h.toString(16)}`);
  mkdirSync(dir, { recursive: true });
  return dir;
}

function main() {
  const opts = parseArgs(process.argv.slice(2));
  const repo = openRepo(opts.base);
  const live = opts.serve || opts.tui || opts.md;

  // ---------------- one-shot: build, write, optionally open ----------------
  if (!live) {
    process.stderr.write(`landfirst: reading ${repo.name} (base=${repo.base}) ... `);
    const model = buildModel(repo, opts, { probes: true });
    const out = resolve(opts.out || join(reportDir(repo), 'landfirst.html'));
    writeFileSync(out, renderHtml(model, { live: false }), 'utf8');
    process.stderr.write(`${model.commits.length} commits, ${model.branches.length} branches\n`);
    console.log(out);
    if (opts.open) openDefault(out);
    return;
  }

  // ---------------- live: one watcher feeding every surface ----------------
  const surfaces = [];
  let tui = null;
  let server = null;
  let mdPath = null;

  if (opts.md) {
    mdPath = resolve(join(reportDir(repo), 'landfirst.md'));
    surfaces.push(`markdown  ${mdPath}`);
  }

  const log = (msg) => {
    if (tui) tui.setStatus(msg);
    else process.stderr.write(`landfirst: ${msg}\n`);
  };

  let mdOpened = false;
  const onModel = (model) => {
    if (mdPath) {
      try {
        writeFileSync(mdPath, toMarkdown(model, { graphRows: opts.graphRows }), 'utf8');
        // Open only after the file exists, otherwise the editor opens a blank buffer.
        if (opts.open && !mdOpened) {
          mdOpened = true;
          try { execFileSync('zed', [mdPath], { windowsHide: true }); }
          catch { openDefault(mdPath); }
        }
      } catch (err) { log(`markdown write failed: ${err.message}`); }
    }
    if (server) server.push(model);
    if (tui) tui.setModel(model);
  };

  if (opts.tui) {
    tui = startTui(repo, { onQuit: () => shutdown(0) });
  }

  const watcher = createWatcher(repo, opts, onModel, { onStatus: log });
  if (tui) tui.state.onRefresh = watcher.refresh;

  if (opts.serve) {
    server = startServer({
      port: opts.port,
      onListen: (port) => {
        const url = `http://127.0.0.1:${port}/`;
        surfaces.push(`web       ${url}`);
        if (!tui) process.stderr.write(`landfirst: serving ${url}\n`);
        if (opts.open) {
          // Give the first model a moment so the page does not land on the 503.
          setTimeout(() => {
            if (!opts.app || !openAppWindow(url)) openDefault(url);
          }, 400);
        }
      },
    });
  }

  if (!tui) {
    process.stderr.write('landfirst: watching for changes, ctrl-c to stop\n');
    if (mdPath) process.stderr.write(`landfirst: markdown at ${mdPath}\n`);
  }

  let stopping = false;
  function shutdown(code) {
    if (stopping) return;
    stopping = true;
    watcher.close();
    if (server) server.close();
    if (tui) tui.quit();
    process.exit(code);
  }
  for (const sig of ['SIGINT', 'SIGTERM']) process.on(sig, () => shutdown(0));
}

try {
  main();
} catch (err) {
  console.error(`landfirst: ${err && err.message ? err.message : err}`);
  process.exit(1);
}
