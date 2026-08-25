/**
 * Real throwaway git repositories. No mocking - the whole point of this tool is
 * that it reads git correctly, so the tests have to read real git.
 *
 * Every repo is isolated from the developer's own git config (GIT_CONFIG_GLOBAL
 * and GIT_CONFIG_SYSTEM point at paths that do not exist), so a user's
 * init.defaultBranch, merge driver, or hooks cannot change a test result.
 */

import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync, mkdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { tmpdir } from 'node:os';

const created = [];

function env(dir) {
  return {
    ...process.env,
    GIT_CONFIG_GLOBAL: join(dir, '.no-global-config'),
    GIT_CONFIG_SYSTEM: join(dir, '.no-system-config'),
    GIT_AUTHOR_NAME: 'Test', GIT_AUTHOR_EMAIL: 'test@example.invalid',
    GIT_COMMITTER_NAME: 'Test', GIT_COMMITTER_EMAIL: 'test@example.invalid',
    GIT_TERMINAL_PROMPT: '0',
  };
}

export function sh(dir, args, { allowFail = false } = {}) {
  try {
    return execFileSync('git', args, {
      cwd: dir, encoding: 'utf8', env: env(dir), stdio: ['ignore', 'pipe', 'pipe'],
    });
  } catch (err) {
    if (allowFail) return null;
    throw new Error(`git ${args.join(' ')} failed in ${dir}: ${err.stderr || err.message}`);
  }
}

export function makeRepo({ initialBranch = 'main' } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'landfirst-test-'));
  created.push(dir);
  sh(dir, ['init', '-q', '-b', initialBranch]);
  return dir;
}

/** Write files and commit them. Returns the new SHA. */
export function commit(dir, files, message) {
  for (const [rel, content] of Object.entries(files)) {
    const full = join(dir, rel);
    mkdirSync(dirname(full), { recursive: true });
    writeFileSync(full, content);
  }
  sh(dir, ['add', '-A']);
  sh(dir, ['commit', '-q', '--no-gpg-sign', '-m', message]);
  return sh(dir, ['rev-parse', 'HEAD']).trim();
}

export function checkoutNew(dir, name, from) {
  sh(dir, from ? ['checkout', '-q', '-b', name, from] : ['checkout', '-q', '-b', name]);
}
export function checkout(dir, name) { sh(dir, ['checkout', '-q', name]); }

export function addWorktree(dir, path, branchName) {
  sh(dir, ['worktree', 'add', '-q', path, branchName]);
  created.push(path);
  return path;
}

/** Run fn with cwd set to the repo, always restoring the previous cwd. */
export async function inRepo(dir, fn) {
  const prev = process.cwd();
  process.chdir(dir);
  try { return await fn(); } finally { process.chdir(prev); }
}

export function cleanupAll() {
  for (const d of created.splice(0)) {
    try { rmSync(d, { recursive: true, force: true, maxRetries: 3 }); } catch { /* windows lock */ }
  }
}

/**
 * The standard scenario used by most tests:
 *   main            init -> shared.txt, a.txt, b.txt
 *   feat/alpha      edits a.txt
 *   feat/beta       edits a.txt (conflicts with alpha) and b.txt
 *   feat/solo       edits solo.txt (collides with nobody)
 *   chore/merged    already merged into main
 */
export function standardRepo() {
  const dir = makeRepo();
  commit(dir, { 'a.txt': 'base\n', 'b.txt': 'base\n', 'solo.txt': 'base\n' }, 'init');

  checkoutNew(dir, 'feat/alpha');
  commit(dir, { 'a.txt': 'alpha rewrites this line\n' }, 'alpha edits a');

  checkout(dir, 'main');
  checkoutNew(dir, 'feat/beta');
  commit(dir, { 'a.txt': 'beta rewrites the same line differently\n' }, 'beta edits a');
  commit(dir, { 'b.txt': 'beta also edits b\n' }, 'beta edits b');

  checkout(dir, 'main');
  checkoutNew(dir, 'feat/solo');
  commit(dir, { 'solo.txt': 'only solo touches this\n' }, 'solo edits solo');

  checkout(dir, 'main');
  checkoutNew(dir, 'chore/merged');
  commit(dir, { 'merged.txt': 'merged work\n' }, 'chore work');
  checkout(dir, 'main');
  sh(dir, ['merge', '-q', '--no-ff', '--no-gpg-sign', '-m', 'merge chore', 'chore/merged']);

  return dir;
}
let shared = null;
/**
 * The standard repo, built once per test file. Use for read-only assertions.
 * Anything that mutates the repo (worktrees, checkouts, new refs) must call
 * standardRepo() for a private one instead.
 */
export function sharedStandardRepo() {
  if (!shared) shared = standardRepo();
  return shared;
}
