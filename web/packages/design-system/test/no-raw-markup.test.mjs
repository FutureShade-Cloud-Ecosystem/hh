// THE RAW-MARKUP GATE. The rule the review rounds added to the owned renderer
// stands under the adopted substrate, restated for it: no render path in this
// package may offer raw markup. Lit is the renderer (docs/adr/0023, option 2),
// and Lit ships exactly one class of API that turns a string into markup: the
// unsafe directives (unsafeHTML, unsafeSVG, unsafeMathML) and unsafeStatic,
// plus the static-html module that unsafeStatic fronts. This gate refuses any
// import of those, and any mention of their names, anywhere in this package
// and in any package that depends on this one.
//
// The kits bind only what Lit's own rules keep inert: html and css tagged
// templates and the reactive base class. An interpolated value lands as a text
// node or an attribute value, never as markup; there is no second path, and
// this gate is what keeps it that way.
//
// The gates below follow the phantom gate's honesty pattern: each scanner is
// pinned against a planted offence first, so a clean result below is evidence
// and not blindness.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

const here = new URL('.', import.meta.url).pathname;
const pkgDir = join(here, '..');
const webRoot = join(pkgDir, '..', '..');

// ── what is banned, by specifier and by name ─────────────────────────────────────

// Import specifiers that put raw markup on a render path: the unsafe
// directives of lit and lit-html under every spelling they ship, and the
// static-html module.
const BANNED_SPECIFIERS = [
  'lit/directives/unsafe-html',
  'lit/directives/unsafe-svg',
  'lit/directives/unsafe-mathml',
  'lit/directives/static',
  'lit-html/directives/unsafe-html',
  'lit-html/directives/unsafe-svg',
  'lit-html/directives/unsafe-mathml',
  'lit-html/directives/static',
  'lit/static-html',
  'lit-html/static-html',
];

// The directive names themselves. An imported name is an offence wherever it
// appears; a mention in a comment is handled by the same rule, on purpose:
// a comment is how it would be excused.
const BANNED_NAMES = ['unsafeHTML', 'unsafeSVG', 'unsafeMathML', 'unsafeStatic'];

const IMPORT_RE = /(?:import|export)[^'"`]*['"]([^'"]+)['"]|import\(\s*['"]([^'"]+)['"]\s*\)/g;

const offence = (src) => {
  for (const m of src.matchAll(IMPORT_RE)) {
    const spec = m[1] ?? m[2];
    if (spec && BANNED_SPECIFIERS.some((b) => spec === b || spec === `${b}.js`)) return `import of ${spec}`;
  }
  for (const name of BANNED_NAMES) {
    if (src.includes(name)) return `a mention of ${name}`;
  }
  return null;
};

// Raw DOM markup paths: there is no render path that hands a string to the
// parser in this package, and this is what holds that true alongside the
// directive ban.
const RAW_DOM_RE = /\.innerHTML\s*=|\.outerHTML\s*=|insertAdjacentHTML|document\.write/;

const offenceDom = (src) => {
  const m = src.match(RAW_DOM_RE);
  return m ? `a raw DOM markup path (${m[0].trim()})` : null;
};

// ── the walk: this package, plus every package that depends on it ────────────────

const SOURCE_RE = /\.(ts|tsx|mts|cts|js|mjs|cjs|jsx|html)$/;

const walkSources = (dir, prefix, out) => {
  for (const entry of readdirSync(dir)) {
    const rel = prefix ? `${prefix}/${entry}` : entry;
    const abs = join(dir, entry);
    if (statSync(abs).isDirectory()) {
      if (entry === 'node_modules' || entry === 'test' || entry === '.git') continue;
      walkSources(abs, rel, out);
    } else if (SOURCE_RE.test(entry)) {
      out.push({ rel, abs, text: readFileSync(abs, 'utf8') });
    }
  }
  return out;
};

const manifestOf = (dir) => {
  try {
    return JSON.parse(readFileSync(join(dir, 'package.json'), 'utf8'));
  } catch {
    return null;
  }
};

/** This package's shippable sources (src and harness; the gates scan
 * themselves too, but test/ is excluded as machinery, as in the phantom
 * gate). */
const thisPackageSources = () => {
  const out = [];
  for (const root of ['src', 'harness']) out.push(...walkSources(join(pkgDir, root), root, []));
  return out;
};

/** Every workspace package that depends on @hh/design-system, by manifest or
 * by import: where this gate's rule must hold just as hard. */
const dependentPackages = () => {
  const out = [];
  for (const group of ['packages', 'apps']) {
    const dir = join(webRoot, group);
    let entries = [];
    try {
      entries = readdirSync(dir);
    } catch {
      continue;
    }
    for (const name of entries) {
      const abs = join(dir, name);
      if (!statSync(abs).isDirectory()) continue;
      const manifest = manifestOf(abs);
      if (!manifest || manifest.name === '@hh/design-system') continue;
      const deps = { ...(manifest.dependencies ?? {}), ...(manifest.devDependencies ?? {}), ...(manifest.peerDependencies ?? {}) };
      const sources = walkSources(abs, `${group}/${name}`, []);
      const imports = sources.some((s) => s.text.includes('@hh/design-system'));
      if (deps['@hh/design-system'] || imports) out.push({ name: manifest.name ?? `${group}/${name}`, sources });
    }
  }
  return out;
};

// ── the scanner guards: each proves it sees the thing it refuses ────────────────

test('the specifier scanner sees every banned raw-markup import (guards the gate)', () => {
  const planted = [
    `import { unsafeHTML } from 'lit/directives/unsafe-html.js';`,
    `import { unsafeSVG } from 'lit/directives/unsafe-svg.js';`,
    `import { unsafeMathML } from 'lit/directives/unsafe-mathml.js';`,
    `import { unsafeStatic } from 'lit/directives/static.js';`,
    `import { html } from 'lit-html/directives/unsafe-html.js';`,
    `import 'lit/static-html.js';`,
    `const u = await import('lit/directives/unsafe-html.js');`,
  ];
  for (const p of planted) {
    assert.ok(offence(p) !== null, `the scanner missed the planted offence: ${p}`);
  }
  // And it stays quiet on the imports the kits are allowed.
  for (const allowed of [
    `import { html, css } from 'lit';`,
    `import { LitElement } from 'lit';`,
    `import '../base.js';`,
    `import { render } from '@lit-labs/ssr';`,
  ]) {
    assert.equal(offence(allowed), null, `the scanner flagged an allowed import: ${allowed}`);
  }
});

test('the raw DOM scanner sees the parser paths it refuses (guards the gate)', () => {
  for (const planted of [`el.innerHTML = x;`, `el.insertAdjacentHTML('beforeend', x);`, `document.write(x);`, `el.outerHTML = x;`]) {
    assert.ok(offenceDom(planted) !== null, `the DOM scanner missed: ${planted}`);
  }
  assert.equal(offenceDom(`const innerHTML = readStdin(); return innerHTML.length;`), null, 'the DOM scanner flagged a non-assignment mention');
});

test('the walk finds this package and names the workspace packages it must also scan', () => {
  // Without the walk the gate below would pass while scanning nothing.
  const files = thisPackageSources().map((s) => s.rel);
  for (const anchor of [
    'src/base.ts',
    'src/index.ts',
    'src/state.ts',
    'src/primitives.ts',
    'src/state-icons.ts',
    'src/harness-page.ts',
    'src/kits/scan-and-confirm.ts',
    'src/kits/list-detail.ts',
    'src/kits/guided-flow.ts',
    'src/kits/dashboard.ts',
    'src/kits/inbox.ts',
    'harness/build.mjs',
    'harness/serve.mjs',
  ]) {
    assert.ok(files.includes(anchor), `walker missed ${anchor}: its scan is not package-wide`);
  }
  // The dependents are computed, not hard-coded: this records what the gate
  // covers beyond this package, so an empty list is a stated fact, not a
  // silence.
  assert.deepEqual(
    dependentPackages().map((d) => d.name),
    [],
    'a package now depends on @hh/design-system; its sources are scanned below, and this lock must name it deliberately',
  );
});

// ── the gates themselves ─────────────────────────────────────────────────────────

test('no shipped source of this package imports or names a raw-markup escape hatch', () => {
  const offenders = [];
  for (const { rel, text } of thisPackageSources()) {
    for (const what of [offence(text), offenceDom(text)]) {
      if (what) offenders.push(`${rel}: ${what}`);
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a shipped source offers raw markup. The rule from the review rounds stands under Lit: no render ` +
      `path in this package may offer it, and the library's unsafe directives and static-html module ` +
      `are exactly that:\n  ${offenders.join('\n  ')}`,
  );
});

test('no source of any package that depends on this one imports or names a raw-markup escape hatch', () => {
  const offenders = [];
  for (const dependent of dependentPackages()) {
    for (const { rel, text } of dependent.sources) {
      for (const what of [offence(text), offenceDom(text)]) {
        if (what) offenders.push(`${dependent.name} (${rel}): ${what}`);
      }
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a package that builds on the kits offers raw markup. The kits' rule binds its dependents too:\n  ` +
      `${offenders.join('\n  ')}`,
  );
});
