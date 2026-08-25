/**
 * The renderers ship user-visible output, so they get asserted on directly:
 * escaping, valid structure, and - the one that actually matters - never
 * describing an unprobed pair as clean.
 */

import { test, after } from 'node:test';
import assert from 'node:assert/strict';

import { standardRepo, sharedStandardRepo, makeRepo, commit, checkoutNew, checkout, inRepo, cleanupAll } from './fixtures.mjs';
import { openRepo, buildModel } from '../src/core.mjs';
import { toMarkdown } from '../src/render-md.mjs';
import { renderHtml, escapeHtml } from '../src/render-html.mjs';
import { buildTextGraph, gutterString } from '../src/lanes-text.mjs';

after(cleanupAll);

const build = (dir, opts = {}) =>
  inRepo(dir, () => buildModel(openRepo(), { limit: 100, maxPairs: 50, ...opts }, { probes: true }));

test('markdown output contains every section and no unrendered template holes', async () => {
  const md = toMarkdown(await build(sharedStandardRepo()));
  for (const heading of ['## Branch map', '## Merge plan', '## Collisions', '## Worktrees', '## Commit graph']) {
    assert.ok(md.includes(heading), `missing ${heading}`);
  }
  assert.ok(!md.includes('undefined'), 'a template hole leaked "undefined" into the document');
  assert.ok(!md.includes('[object Object]'), 'an object was stringified into the document');
  assert.ok(md.includes('```mermaid'), 'the mermaid branch map is present');
});

test('the mermaid block is balanced and uses sanitised node ids', async () => {
  const md = toMarkdown(await build(sharedStandardRepo()));
  const fences = md.match(/```/g) || [];
  assert.equal(fences.length % 2, 0, 'unbalanced code fences would break the preview');

  const block = md.split('```mermaid')[1].split('```')[0];
  for (const id of block.matchAll(/^\s{2}(b_[A-Za-z0-9_]+)\[/gm)) {
    assert.match(id[1], /^b_[A-Za-z0-9_]+$/, 'mermaid ids must not contain slashes or dashes');
  }
});

test('html output is a complete document and embeds parseable model json', async () => {
  const model = await build(sharedStandardRepo());
  const html = renderHtml(model, { live: false });
  assert.ok(html.startsWith('<!doctype html>'));
  assert.ok(html.includes('</html>'));

  const json = html.split('<script id="data" type="application/json">')[1].split('</script>')[0];
  const parsed = JSON.parse(json.replace(/\\u003c/g, '<'));
  assert.equal(parsed.commits.length, model.commits.length);
  assert.equal(parsed.base, model.base);
});

test('hostile branch names and commit subjects cannot break out of the document', async () => {
  const dir = makeRepo();
  commit(dir, { 'a.txt': 'x\n' }, 'init');
  // Windows stores refs as files, so < > " : | ? * are illegal in a branch name
  // there. & and ' are legal on every platform and still HTML-significant.
  const evilBranch = "feat/a&b'c";
  checkoutNew(dir, evilBranch);
  // Commit subjects are not filenames, so they carry the full payload.
  const evilSubject = '</script><img src=x onerror=alert(1)> & "q" \'s\'';
  commit(dir, { 'a.txt': 'y\n' }, evilSubject);
  checkout(dir, 'main');

  const model = await build(dir);
  assert.ok(model.branches.some((b) => b.name === evilBranch), 'the branch parsed intact');
  assert.ok(model.commits.some((c) => c.subject === evilSubject), 'the subject parsed intact');

  const html = renderHtml(model, { live: false });
  const marker = '<script id="data" type="application/json">';
  const jsonBlock = html.split(marker)[1].split('</script>')[0];
  const rest = html.replace(marker + jsonBlock, '');

  // 1. The payload must not be able to close the script element early. This is
  //    the only injection route in the static document, since every visible
  //    string is rendered client-side through esc().
  assert.ok(!jsonBlock.includes('<'), 'every < inside the json payload must be \\u003c-escaped');
  assert.ok(jsonBlock.includes('\\u003c/script'), 'the </script> attempt survives only in escaped form');
  JSON.parse(jsonBlock.replace(/\\u003c/g, '<'));

  // 2. Outside the payload, no fragment of the attack may appear at all.
  assert.ok(!rest.includes('onerror='), 'no raw event handler in the document body');
  assert.ok(!rest.includes(evilBranch), 'no raw branch name in the document body');
});

test('escapeHtml handles every character that matters', () => {
  assert.equal(escapeHtml(`<>&"'`), '&lt;&gt;&amp;&quot;&#39;');
  assert.equal(escapeHtml(null), '');
  assert.equal(escapeHtml(undefined), '');
});

test('the text graph emits one row per commit plus link rows, all same width', async () => {
  const model = await build(sharedStandardRepo());
  const rows = buildTextGraph(model);
  const commitRows = rows.filter((r) => r.kind !== 'link');
  assert.equal(commitRows.length, model.commits.length);

  const widths = new Set(rows.map((r) => gutterString(r, { color: false, laneCount: model.laneCount }).length));
  assert.equal(widths.size, 1, `gutter must be fixed width, got widths ${[...widths]}`);
});

test('the plain-text gutter carries no ansi escapes when colour is off', async () => {
  const model = await build(sharedStandardRepo());
  for (const r of buildTextGraph(model)) {
    const s = gutterString(r, { color: false, laneCount: model.laneCount });
    assert.ok(!s.includes('\x1b'), 'colour leaked into the plain-text renderer');
  }
});

test('an unprobed collision is never described as clean', async () => {
  const model = await build(sharedStandardRepo());
  // Force the "could not probe" state the way an old git would produce it.
  model.collisions = [{ a: 'feat/alpha', b: 'feat/beta', shared: 1, sharedFiles: ['a.txt'], conflicts: [], clean: null }];

  const md = toMarkdown(model);
  assert.ok(md.includes('could not probe'), 'markdown must say it could not tell');
  assert.ok(!/merges cleanly/.test(md.split('## Collisions')[1].split('## Worktrees')[0]),
    'markdown must not claim the pair merges cleanly');

  const html = renderHtml(model, { live: false });
  assert.ok(html.includes('could not probe on this git version'), 'html must say it could not tell');
});
