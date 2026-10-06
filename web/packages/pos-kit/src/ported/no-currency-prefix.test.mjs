// The dealer's money reads as dollars: `$1,234.00`, never `CA$1,234.00` or `US$1,234.00`.
// The currency code lives in the data and the API; the display never restates it. The one
// formatter is formatDollars in _money-format.ts, and this suite holds every site to it.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { formatDollars } from './_money-format.ts';

test('formatDollars renders dollars with grouping and no currency letters', () => {
  assert.equal(formatDollars(123400), '$1,234.00');
  assert.equal(formatDollars(0), '$0.00');
  assert.equal(formatDollars(150), '$1.50');
  assert.equal(formatDollars(5), '$0.05');
  assert.equal(formatDollars(104864000), '$1,048,640.00');
  assert.equal(formatDollars(-123400), '-$1,234.00');
  assert.equal(formatDollars(Number.NaN), '$0.00');
});

const REPO = fileURLToPath(new URL('../../../../../', import.meta.url));
const SKIP_DIRS = new Set(['node_modules', 'dist', '.git', 'testdata', 'migrations']);
// Other lanes carry their own sites and are skipped here until they land:
//   web/apps/counter-till: the Counter Till coder on demo/till-fill
//   web/apps/quote-desk: the Quote Desk lane
const SKIP_PATHS = ['web/apps/counter-till', 'web/apps/quote-desk'];
const SOURCE = /\.(ts|mjs|js|go|html|css|tmpl|txt)$/;
const TESTS = /(\.test\.[a-z]+|_test\.go)$/;
const LITERAL = /\b(CA|US)\$/;

function walk(dir, out) {
  for (const name of readdirSync(dir)) {
    if (SKIP_DIRS.has(name)) continue;
    const full = join(dir, name);
    const rel = relative(REPO, full).split(sep).join('/');
    if (SKIP_PATHS.includes(rel)) continue;
    if (statSync(full).isDirectory()) walk(full, out);
    else if (SOURCE.test(name) && !TESTS.test(name)) out.push(full);
  }
}

test('no CA$ or US$ literal in app, package or server source outside tests', () => {
  const files = [];
  for (const root of ['web/apps', 'web/packages', 'core']) {
    walk(join(REPO, root), files);
  }
  assert.ok(files.length > 50, 'the walk found the source tree');
  const hits = [];
  for (const file of files) {
    readFileSync(file, 'utf8').split('\n').forEach((line, i) => {
      if (LITERAL.test(line)) hits.push(`${relative(REPO, file)}:${i + 1}: ${line.trim()}`);
    });
  }
  assert.deepEqual(hits, [], `currency letters in display text, use formatDollars:\n${hits.join('\n')}`);
});
