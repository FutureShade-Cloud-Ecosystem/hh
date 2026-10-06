// The source scans over the kit's tree, carried over from the Counter Till's
// app tree when the shared pieces moved into this package (the scans ran
// there as test/scans.pw.ts over the whole app; the till's copy keeps its
// claim over its own tree, this one over the kit's):
//
//   1. No money arithmetic: the plan's scan over the ported surface's source
//      prints nothing outside the named display and seam files. The surface
//      formats what the core computed, through the named helpers, and
//      computes nothing. The scan reads both spellings of a cents figure,
//      the snake_case the wire carries and the camelCase the ported classes
//      carry, beside any arithmetic operator, and every binary operator is
//      read. A unary minus passed to a formatter is display shaping, not a
//      figure decided, so the minus wants the binary left operand.
//   2. No card number path: no card number field, no tokenizer script; the
//      card tender sends the fixed demo token.
//   3. No named colour (white, black) standing in for a role in a colour
//      position, in any source file under src/ported (the house gate the
//      workspace test carries over the app's tree; a role the design system
//      does not carry is declared LOCAL in styles/tokens.css instead).
//
// A hit anywhere outside the named helpers for the first scan, or anywhere
// at all for the other two, is a failure naming the file.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const SRC = join(here, '..');

// The named display and parse helper of the ported surface, and the seam
// file the rerouted computations land in: the same two files the app's scan
// named, now in this package. The exclusion is exactly these two and nothing
// else; a third file that divides figures by hand must face the scan.
const NAMED_HELPERS = [join(SRC, 'ported', '_money-format.ts'), join(SRC, 'ported', 'ports.ts')];

function walkSources(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walkSources(full, out);
    else if (entry.endsWith('.ts')) out.push(full);
  }
  return out;
}

test('the money scan prints nothing outside the named display and seam files', () => {
  const scan =
    /[a-zA-Z_$.]*[cC]ents\s*[-+*/]|[a-zA-Z0-9_$)\]]\s*[+*/]\s*[a-zA-Z_$.]*[cC]ents\b|[a-zA-Z0-9_$)\]]\s*-\s*(?![cC]ents\b)[a-zA-Z_$.]*[cC]ents\b\s*[^:]/;
  // String literals hold words, not computations, so quoted spans come off
  // the line before the scan (an attribute named 'total-cents' is a name).

  const offenders = [];
  const scanned = [];
  for (const file of walkSources(SRC)) {
    if (NAMED_HELPERS.includes(file)) continue;
    scanned.push(file);
    const hits = readFileSync(file, 'utf8')
      .split('\n')
      .map((line) => line.replace(/'(?:[^'\\]|\\.)*'|"(?:[^"\\]|\\.)*"/g, '""'))
      .filter((line) => scan.test(line));
    if (hits.length === 0) continue;
    offenders.push(`${file}: ${hits.join(' | ')}`);
  }
  assert.deepEqual(NAMED_HELPERS, NAMED_HELPERS.slice(0, 2), 'the exclusion is exactly the two named files');
  assert.ok(NAMED_HELPERS.some((file) => file.endsWith('_money-format.ts')));
  assert.ok(NAMED_HELPERS.some((file) => file.endsWith('ports.ts')));
  // The walk covers the moved components, the seam file's neighbours and the
  // shared helpers: the claim is about the tree, so the walk must be seen to
  // reach it.
  assert.ok(scanned.some((file) => file.endsWith('pos-scan-box.ts')), 'the scan reaches the moved surface');
  assert.ok(scanned.length > 20, 'the scan reaches the whole ported tree');
  assert.deepEqual(offenders, []);
});

test('the card scan prints nothing: no card number path, no tokenizer, no loader', () => {
  const scan = /card_?number|tokeniz|runner\.js/i;
  const offenders = [];
  for (const file of walkSources(SRC)) {
    const hits = readFileSync(file, 'utf8').split('\n').filter((line) => scan.test(line));
    if (hits.length > 0) offenders.push(`${file}: ${hits.join(' | ')}`);
  }
  assert.deepEqual(offenders, []);
});

test('no named colour stands in for a role in a colour position, in any ported file', () => {
  const files = readdirSync(here).filter((f) => f.endsWith('.ts'));
  assert.ok(files.includes('pos-scan-box.ts') && files.length > 20, 'the scan reaches the whole ported directory');
  const found = [];
  for (const f of files) {
    const src = readFileSync(join(here, f), 'utf8');
    const named = src.match(/(?:color|background|background-color|border(?:-[a-z]+)?|fill|stroke|outline|box-shadow|text-shadow)\s*:[^;{}]*\b(?:white|black)\b/g) ?? [];
    const mixes = src.match(/color-mix\([^)]*,\s*(?:white|black)\s*\)/g) ?? [];
    for (const hit of [...named, ...mixes]) found.push(`${f}: ${hit}`);
  }
  assert.deepEqual(found, [], 'name a LOCAL role in tokens.css, not a colour keyword');
});
