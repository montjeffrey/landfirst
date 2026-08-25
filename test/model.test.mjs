import { test, after } from 'node:test';
import assert from 'node:assert/strict';

import { standardRepo, sharedStandardRepo, addWorktree, inRepo, cleanupAll, commit, checkout, checkoutNew } from './fixtures.mjs';
import { openRepo, buildModel, gitCapabilities, parseLegacyMergeTree } from '../src/core.mjs';

after(cleanupAll);

const build = (dir, opts = {}) =>
  inRepo(dir, () => buildModel(openRepo(opts.base), { limit: 200, maxPairs: 100, ...opts }, { probes: true }));

test('finds every local branch and measures ahead/behind against the base', async () => {
  const m = await build(sharedStandardRepo());
  assert.equal(m.base, 'main');
  const names = m.branches.map((b) => b.name).sort();
  // The base is the merge target, not a candidate, so it is deliberately absent.
  assert.deepEqual(names, ['chore/merged', 'feat/alpha', 'feat/beta', 'feat/solo']);
  assert.ok(!names.includes(m.base), 'the base branch is never listed as a branch to merge');

  const alpha = m.branches.find((b) => b.name === 'feat/alpha');
  assert.equal(alpha.ahead, 1, 'alpha has one commit main does not');
  // --no-ff puts two commits on main after alpha forked: the chore work and the
  // merge commit itself. Both count as "behind".
  assert.equal(alpha.behind, 2, 'main gained the chore work and its merge commit');

  const beta = m.branches.find((b) => b.name === 'feat/beta');
  assert.equal(beta.ahead, 2, 'beta has two commits');
});

test('classifies an already-merged branch as merged, not as work to do', async () => {
  const m = await build(sharedStandardRepo());
  const merged = m.branches.find((b) => b.name === 'chore/merged');
  assert.equal(merged.state, 'merged');
  assert.equal(merged.ahead, 0);
  assert.ok(!m.plan.some((p) => p.name === 'chore/merged'), 'merged branches stay out of the plan');
});

test('detects the real conflict between alpha and beta and nothing else', async () => {
  const m = await build(sharedStandardRepo());
  if (gitCapabilities().conflictMode !== 'full') return; // legacy path covered separately

  const pair = m.collisions.find(
    (c) => [c.a, c.b].sort().join() === ['feat/alpha', 'feat/beta'].sort().join());
  assert.ok(pair, 'alpha and beta both touch a.txt so the pair must be probed');
  assert.deepEqual(pair.conflicts, ['a.txt'], 'a.txt is the only conflicting file');

  assert.ok(!m.collisions.some((c) => c.a === 'feat/solo' || c.b === 'feat/solo'),
    'solo shares no files with anyone, so it is never probed');
});

test('a branch that conflicts with nobody is ranked above one that does', async () => {
  const m = await build(sharedStandardRepo());
  const order = m.plan.map((p) => p.name);
  assert.ok(order.includes('feat/solo'));
  const solo = m.plan.find((p) => p.name === 'feat/solo');
  const beta = m.plan.find((p) => p.name === 'feat/beta');
  assert.ok(solo.order < beta.order,
    `solo (no collisions) should rank before beta (collides); got ${JSON.stringify(order)}`);
});

test('every planned branch carries human-readable reasoning', async () => {
  const m = await build(sharedStandardRepo());
  for (const p of m.plan) {
    assert.ok(Array.isArray(p.why) && p.why.length > 0, `${p.name} has no reasoning`);
    assert.ok(p.why.every((w) => typeof w === 'string' && w.length), `${p.name} has an empty reason`);
  }
});

test('worktrees are discovered and attributed to their branch', async () => {
  const dir = standardRepo();
  const wt = addWorktree(dir, `${dir}-wt-alpha`, 'feat/alpha');
  const m = await build(dir);

  assert.equal(m.worktrees.length, 2, 'main worktree plus the added one');
  const added = m.worktrees.find((w) => w.branch === 'feat/alpha');
  assert.ok(added, 'the alpha worktree is listed');
  assert.ok(!added.isMain);
  assert.ok(added.path.replace(/\\/g, '/').endsWith('-wt-alpha'));

  const alpha = m.branches.find((b) => b.name === 'feat/alpha');
  assert.equal(alpha.worktrees.length, 1, 'the branch knows it is checked out somewhere');
});

test('the commit graph is acyclic and every parent edge resolves', async () => {
  const m = await build(sharedStandardRepo());
  const rows = new Map(m.commits.map((c) => [c.hash, c.row]));
  for (const c of m.commits) {
    for (const p of c.parents) {
      if (!rows.has(p)) continue;             // parent outside the window is fine
      assert.ok(rows.get(p) > c.row, `parent ${p.slice(0, 7)} must render below its child`);
    }
  }
});

test('lane assignment never puts two commits in the same cell', async () => {
  const m = await build(sharedStandardRepo());
  const seen = new Set();
  for (const c of m.commits) {
    const cell = `${c.row}:${c.lane}`;
    assert.ok(!seen.has(cell), `two commits collide at ${cell}`);
    seen.add(cell);
    assert.ok(c.lane >= 0 && c.lane < m.laneCount, `${c.short} has lane ${c.lane} outside 0..${m.laneCount - 1}`);
  }
});

test('the model is JSON-serialisable, because the web surface ships it as JSON', async () => {
  const m = await build(sharedStandardRepo());
  const round = JSON.parse(JSON.stringify(m));
  assert.equal(round.commits.length, m.commits.length);
  assert.equal(round.plan.length, m.plan.length);
});

test('parseLegacyMergeTree finds conflicts in pre-2.38 output', () => {
  const sample = [
    'changed in both',
    '  base   100644 5626abf0f72e58d7a153368ba57db4c673c0e171 a.txt',
    '  our    100644 587be6b4c3f93f93c489c0111bba5596147a26cb a.txt',
    '  their  100644 975fbec8256d3e8a3797e7a3611380f27c49f4ac a.txt',
    '@@ -1 +1,5 @@',
    '+<<<<<<< .our',
    ' x',
    '+=======',
    '+y',
    '+>>>>>>> .their',
  ].join('\n');
  assert.deepEqual(parseLegacyMergeTree(sample), ['a.txt']);
});

test('parseLegacyMergeTree reports nothing for a clean auto-merge', () => {
  const sample = [
    'merged',
    '  result 100644 aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa b.txt',
    '  our    100644 bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb b.txt',
  ].join('\n');
  assert.deepEqual(parseLegacyMergeTree(sample), []);
});

test('conflict probing reports a mode, and never claims clean when it cannot tell', async () => {
  const m = await build(sharedStandardRepo());
  assert.ok(['full', 'legacy'].includes(m.capabilities.conflictMode));
  for (const c of m.collisions) {
    if (c.clean === null) {
      assert.equal(c.conflicts.length, 0);   // unknown carries no false detail
    } else {
      assert.equal(typeof c.clean, 'boolean');
    }
  }
});