/**
 * The cases that break git tools in the wild. Each one previously had no
 * coverage at all, and several of them found real bugs.
 */

import { test, after } from 'node:test';
import assert from 'node:assert/strict';

import { makeRepo, commit, checkoutNew, checkout, sh, inRepo, cleanupAll, standardRepo } from './fixtures.mjs';
import { openRepo, buildModel } from '../src/core.mjs';

after(cleanupAll);

const build = (dir, opts = {}) =>
  inRepo(dir, () => buildModel(openRepo(opts.base), { limit: 100, maxPairs: 50, ...opts }, { probes: true }));

test('a repo with a single commit and no branches still produces a model', async () => {
  const dir = makeRepo();
  commit(dir, { 'only.txt': 'x\n' }, 'the only commit');
  const m = await build(dir);
  assert.equal(m.commits.length, 1);
  assert.deepEqual(m.plan, []);
  assert.deepEqual(m.collisions, []);
  assert.equal(m.worktrees.length, 1);
  assert.ok(m.laneCount >= 1, 'lane count is never zero, or the gutter width breaks');
});

test('a branch name that is neither main nor master is accepted as the base', async () => {
  const dir = makeRepo({ initialBranch: 'trunk' });
  commit(dir, { 'a.txt': 'x\n' }, 'init');
  checkoutNew(dir, 'feature');
  commit(dir, { 'a.txt': 'y\n' }, 'work');
  checkout(dir, 'trunk');

  const m = await build(dir, { base: 'trunk' });
  assert.equal(m.base, 'trunk');
  assert.ok(m.plan.some((p) => p.name === 'feature'));
});

test('master is used when main is absent', async () => {
  const dir = makeRepo({ initialBranch: 'master' });
  commit(dir, { 'a.txt': 'x\n' }, 'init');
  const m = await build(dir);
  assert.equal(m.base, 'master');
});

test('detached HEAD does not break the model', async () => {
  const dir = standardRepo();
  const sha = sh(dir, ['rev-parse', 'HEAD~1']).trim();
  sh(dir, ['checkout', '-q', '--detach', sha]);
  const m = await build(dir);
  assert.ok(m.commits.length > 0, 'still renders a graph while detached');
  const detached = m.worktrees.find((w) => w.detached);
  assert.ok(detached, 'the detached worktree is reported as detached');
  assert.equal(detached.branch, null);
});

test('non-ASCII and slash-heavy branch names survive round-tripping', async () => {
  const dir = makeRepo();
  commit(dir, { 'a.txt': 'x\n' }, 'init');
  const odd = 'feat/ünïcode-Ω/nested-deep';
  checkoutNew(dir, odd);
  commit(dir, { 'a.txt': 'y\n' }, 'odd branch work');
  checkout(dir, 'main');

  const m = await build(dir);
  assert.ok(m.branches.some((b) => b.name === odd), `branch ${odd} must survive parsing`);
  const json = JSON.parse(JSON.stringify(m));
  assert.ok(json.branches.some((b) => b.name === odd), 'and survive JSON round-trip');
});

test('a file path with spaces is reported correctly', async () => {
  const dir = makeRepo();
  commit(dir, { 'dir with spaces/some file.txt': 'x\n' }, 'init');
  checkoutNew(dir, 'edits');
  commit(dir, { 'dir with spaces/some file.txt': 'y\n' }, 'edit the spaced file');
  checkout(dir, 'main');

  const m = await build(dir);
  const b = m.branches.find((x) => x.name === 'edits');
  assert.ok(b.files.includes('dir with spaces/some file.txt'),
    `expected the spaced path, got ${JSON.stringify(b.files)}`);
});

test('a shallow clone does not crash the model', async () => {
  const origin = standardRepo();
  const clone = makeRepo();
  sh(clone, ['remote', 'add', 'origin', origin]);
  const ok = sh(clone, ['fetch', '-q', '--depth', '1', 'origin', 'main'], { allowFail: true });
  if (ok === null) return;                       // some git builds refuse local shallow fetch
  sh(clone, ['checkout', '-q', '-B', 'main', 'FETCH_HEAD']);
  const m = await build(clone);
  assert.ok(m.commits.length >= 1, 'a shallow history still renders');
});

test('an empty repo with no commits fails cleanly instead of throwing garbage', async () => {
  const dir = makeRepo();
  await inRepo(dir, () => {
    assert.throws(
      () => buildModel(openRepo(), { limit: 10, maxPairs: 10 }, { probes: false }),
      (err) => err instanceof Error && typeof err.message === 'string' && err.message.length > 0,
      'an unborn HEAD must produce a real Error, not a crash deep in a renderer');
  });
});

test('branches whose tips are identical to the base are classed merged, not active', async () => {
  const dir = makeRepo();
  commit(dir, { 'a.txt': 'x\n' }, 'init');
  sh(dir, ['branch', 'copy-of-main']);
  const m = await build(dir);
  const copy = m.branches.find((b) => b.name === 'copy-of-main');
  assert.equal(copy.ahead, 0);
  assert.equal(copy.state, 'merged');
  assert.ok(!m.plan.some((p) => p.name === 'copy-of-main'));
});

test('the limit option actually bounds how many commits are collected', async () => {
  const dir = makeRepo();
  for (let i = 0; i < 12; i++) commit(dir, { 'a.txt': `line ${i}\n` }, `commit ${i}`);
  const m = await build(dir, { limit: 5 });
  assert.ok(m.commits.length <= 5, `expected at most 5 commits, got ${m.commits.length}`);
  // A capped graph must announce itself, or it reads as the whole history.
  assert.equal(m.truncatedCommits, true, 'the model reports that the graph is capped');
  assert.ok(m.totalCommits >= 12, `totalCommits should count the real history, got ${m.totalCommits}`);
});
