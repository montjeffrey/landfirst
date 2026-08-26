/**
 * These lock in the two defects that made the graph unusable in live mode:
 * lanes and colours reshuffling on every recompute, and branches rendering
 * disconnected from the trunk once the commit window filled up.
 */

import { test, after } from 'node:test';
import assert from 'node:assert/strict';

import { standardRepo, commit, checkout, checkoutNew, sh, inRepo, cleanupAll } from './fixtures.mjs';
import { openRepo, buildModel, gitCapabilities, cleanupScratch } from '../src/core.mjs';

after(() => { cleanupScratch(); cleanupAll(); });

function busyRepo() {
  const dir = standardRepo();
  for (const n of ['feat/d', 'feat/e', 'feat/f']) {
    checkout(dir, 'main');
    checkoutNew(dir, n);
    commit(dir, { [n.replace('/', '_') + '.txt']: n }, 'work on ' + n);
  }
  checkout(dir, 'main');
  return dir;
}

const build = (dir, opts = {}) =>
  inRepo(dir, () => buildModel(openRepo(), { limit: 200, maxPairs: 100, ...opts }, { probes: false }));

test('a new commit does not reshuffle lanes or colours of existing commits', async () => {
  const dir = busyRepo();
  const before = await build(dir);

  checkout(dir, 'feat/alpha');
  commit(dir, { 'a.txt': 'alpha again\n' }, 'one more alpha commit');
  checkout(dir, 'main');
  const after = await build(dir);

  const now = new Map(after.commits.map((c) => [c.hash, c]));
  let shared = 0, laneMoved = 0, colourMoved = 0;
  for (const c of before.commits) {
    const o = now.get(c.hash);
    if (!o) continue;
    shared++;
    if (o.lane !== c.lane) laneMoved++;
    if (o.color !== c.color) colourMoved++;
  }
  assert.ok(shared >= 8, 'the two builds should overlap substantially');

  // Colour is pinned to branch identity, so it must never move. This is the
  // dominant visual cue - when colours churned, the graph appeared to flicker.
  assert.equal(colourMoved, 0, `${colourMoved}/${shared} commits changed colour`);

  // Lanes may shift a little: a new commit genuinely re-dates its branch's
  // chain and rows reorder, exactly as `git log --graph` does. What must not
  // happen is the wholesale reshuffle we started with (7/10 on Windows).
  assert.ok(laneMoved / shared <= 0.25,
    `${laneMoved}/${shared} commits jumped lanes - the graph is re-packing itself`);
});

test('rebuilding with no repository change is completely idempotent', async () => {
  const dir = busyRepo();
  const a = await build(dir);
  const b = await build(dir);

  assert.equal(a.commits.length, b.commits.length);
  for (let i = 0; i < a.commits.length; i++) {
    assert.equal(a.commits[i].hash, b.commits[i].hash, `row ${i} changed identity`);
    assert.equal(a.commits[i].lane, b.commits[i].lane, `row ${i} changed lane`);
    assert.equal(a.commits[i].color, b.commits[i].color, `row ${i} changed colour`);
  }
  assert.equal(a.laneCount, b.laneCount);
  // The watcher fires on index and lock churn too, so a no-op rebuild is the
  // single most common case in live mode. It must be a perfect no-op visually.
  assert.deepEqual(a.edges, b.edges, 'edge geometry drifted between identical builds');
});

test('the base branch owns lane 0 and keeps it', async () => {
  const m = await build(busyRepo());
  const tip = m.commits.find((c) => (c.refs || []).some((r) => r === m.base || r.endsWith('-> ' + m.base)));
  assert.ok(tip, 'the base tip is in the graph');
  assert.equal(tip.lane, 0, 'the trunk must be the leftmost lane');

  // and its whole first-parent spine stays there
  const byHash = new Map(m.commits.map((c) => [c.hash, c]));
  let cur = tip;
  while (cur && cur.parents[0] && byHash.has(cur.parents[0])) {
    cur = byHash.get(cur.parents[0]);
    assert.equal(cur.lane, 0, `trunk commit ${cur.short} wandered to lane ${cur.lane}`);
  }
});

test('every planned branch visibly connects to the trunk', async () => {
  const m = await build(busyRepo(), { limit: 12 });   // force the window to bind
  const byHash = new Map(m.commits.map((c) => [c.hash, c]));
  const tip = m.commits.find((c) => (c.refs || []).some((r) => r === m.base || r.endsWith('-> ' + m.base)));
  assert.ok(tip, 'base tip present even under a tight limit');

  const trunk = new Set();
  const stack = [tip.hash];
  while (stack.length) {
    const h = stack.pop();
    if (trunk.has(h) || !byHash.has(h)) continue;
    trunk.add(h);
    for (const p of byHash.get(h).parents) stack.push(p);
  }

  for (const p of m.plan) {
    const br = m.branches.find((b) => b.name === p.name);
    if (!br || !byHash.has(br.head)) continue;
    const seen = new Set();
    const st = [br.head];
    let reached = false;
    while (st.length) {
      const h = st.pop();
      if (seen.has(h) || !byHash.has(h)) continue;
      seen.add(h);
      if (trunk.has(h)) { reached = true; break; }
      for (const par of byHash.get(h).parents) st.push(par);
    }
    assert.ok(reached, `${p.name} renders with no visible path to ${m.base}`);
  }
});

test('probing writes no objects into the repository', async () => {
  const dir = busyRepo();
  const loose = () => parseInt(/count: (\d+)/.exec(sh(dir, ['count-objects', '-v']))[1], 10);

  const before = loose();
  for (let i = 0; i < 3; i++) {
    await inRepo(dir, () => buildModel(openRepo(), { limit: 200, maxPairs: 100 }, { probes: true }));
  }
  const after = loose();

  assert.equal(after, before,
    `conflict probing wrote ${after - before} objects into the repo ` +
    `(mode: ${gitCapabilities().conflictMode}) - probes must go to the scratch store`);
});
