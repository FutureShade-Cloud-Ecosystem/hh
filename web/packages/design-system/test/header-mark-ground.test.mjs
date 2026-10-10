// The shared header's badge mark wears the front door's lockup exactly: the
// measured H in the near black ink (--hh-accent-ink) on the yellow square
// ground (--hh-accent), square cornered, 30px, the 10px gap to the product
// name. The Quote Desk's header drew the H alone in gold ink with no ground;
// this compares the mark box (size, ground, corner radius, ink) with the
// front door's values. The front door's source is read when it carries the
// badge lockup; the pinned values are the front door's own (its .brand-mark
// and .brand rules) either way, so the check holds on a branch cut before
// the front door took the badge.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

const FRONT_DOOR_VALUES = {
  width: '30px',
  height: '30px',
  background: 'var(--hh-accent)',
  color: 'var(--hh-accent-ink)',
  radius: 'none',
  gap: '10px',
};

/** The declarations of the first rule whose selector is exactly `selector`. */
const rule = (source, selector) => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const m = source.replace(/\/\*[\s\S]*?\*\//g, '').match(new RegExp(`(?:^|[\\s}])${escaped}\\s*\\{([^}]*)\\}`));
  assert.ok(m !== null, `${selector} rule is present`);
  const decls = {};
  for (const d of m[1].split(';')) {
    const i = d.indexOf(':');
    if (i > 0) decls[d.slice(0, i).trim()] = d.slice(i + 1).trim();
  }
  return decls;
};

/** The mark box: size, ground, ink and corner radius ('none' when unset). */
const markBox = (source) => {
  const d = rule(source, '.brand-mark');
  return {
    width: d.width,
    height: d.height,
    background: d.background ?? d['background-color'] ?? 'none',
    color: d.color,
    radius: d['border-radius'] ?? 'none',
    gap: rule(source, '.brand').gap,
  };
};

const read = (rel) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');

test('the header mark box matches the front door lockup: size, yellow ground, ink, square corners, gap', () => {
  assert.deepEqual(markBox(read('../src/header.ts')), FRONT_DOOR_VALUES);
});

test('the pinned values are the front door source values when it carries the badge', () => {
  const rel = '../../../apps/front-door/src/app-root.ts';
  if (!existsSync(fileURLToPath(new URL(rel, import.meta.url)))) return;
  const source = read(rel);
  if (!source.includes('viewBox="0 0 3000 3000"')) return;
  assert.deepEqual(markBox(source), FRONT_DOOR_VALUES);
});
