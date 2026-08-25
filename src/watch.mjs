/**
 * Repo watcher. Watches the git metadata that can change the map - refs, HEAD,
 * the index, worktree registrations - and never the working tree, so editing
 * files does not churn the view (and node_modules cannot drown the watcher).
 *
 * Every change runs a fast pass first (no merge probes, ~1s) so the surface
 * updates immediately, then a probe pass (cached, so usually only the branch
 * that moved is re-probed) which supersedes it.
 */

import { watch } from 'node:fs';
import { join } from 'node:path';
import { buildModel, probeStats } from './core.mjs';

const RELEVANT = /^(HEAD|ORIG_HEAD|packed-refs|index|MERGE_HEAD|REBASE_HEAD|CHERRY_PICK_HEAD)$|^(refs|worktrees|logs)[\\/]/;

export function createWatcher(repo, opts, onModel, { onStatus = () => {} } = {}) {
  let timer = null;
  let probeTimer = null;
  let building = false;
  let queued = false;
  let generation = 0;

  async function build(withProbes) {
    if (building) { queued = true; return; }
    building = true;
    const gen = ++generation;
    try {
      const t0 = Date.now();
      const before = probeStats.misses;
      const model = buildModel(repo, opts, { probes: withProbes });
      if (gen !== generation) return;            // superseded mid-build
      model.buildMs = Date.now() - t0;
      model.probesRun = probeStats.misses - before;
      model.probeHits = probeStats.hits;
      onModel(model);
      onStatus(withProbes
        ? `updated in ${model.buildMs}ms (${model.probesRun} probes, ${model.probeHits} cached)`
        : `graph updated in ${model.buildMs}ms - probing conflicts...`);
    } catch (err) {
      onStatus(`error: ${err.message}`);
    } finally {
      building = false;
      if (queued) { queued = false; schedule(); }
    }
  }

  function schedule() {
    clearTimeout(timer);
    clearTimeout(probeTimer);
    // Fast pass quickly, probe pass once things settle.
    timer = setTimeout(() => build(false), 150);
    probeTimer = setTimeout(() => build(true), 700);
  }

  const watchers = [];
  const dirs = new Set([repo.gitDir, repo.commonDir]);
  for (const dir of dirs) {
    try {
      const w = watch(dir, { recursive: true }, (_evt, file) => {
        if (!file) return schedule();
        const norm = String(file).replace(/\\/g, '/');
        // Ignore git's own lock churn; the real file event follows.
        if (norm.endsWith('.lock') || norm.includes('/objects/')) return;
        if (RELEVANT.test(String(file))) schedule();
      });
      w.on('error', () => {});
      watchers.push(w);
    } catch (err) {
      onStatus(`watch failed for ${dir}: ${err.message}`);
    }
  }

  build(true);

  return {
    refresh: () => build(true),
    close: () => {
      clearTimeout(timer); clearTimeout(probeTimer);
      for (const w of watchers) { try { w.close(); } catch { /* already gone */ } }
    },
  };
}
