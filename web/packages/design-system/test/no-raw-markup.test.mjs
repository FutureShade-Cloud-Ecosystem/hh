// THE RAW-MARKUP GATE. The rule the review rounds added to the owned renderer
// stands under the adopted substrate, restated for it: no render path in this
// package may offer raw markup. Lit is the renderer (docs/adr/0023, option 2),
// and Lit ships exactly one class of API that turns a string into markup: the
// unsafe directives (unsafeHTML, unsafeSVG, unsafeMathML) and unsafeStatic,
// plus the static-html module that unsafeStatic fronts. This gate refuses any
// import of those, and any mention of their names in shipped source, anywhere
// in this package (src, harness, and test too) and in any package that depends
// on this one. Where a binding GOES is the position gate's job
// (test/positions.test.mjs); this gate holds that no raw-markup SOURCE exists
// at all.
//
// The kits bind only what Lit's own rules keep inert: html and css tagged
// templates and the reactive base class. An interpolated value lands as a text
// node or an attribute value, never as markup; there is no second path, and
// this gate is what keeps it that way.
//
// The gates below follow the phantom gate's honesty pattern: each scanner is
// pinned against planted offences first, so a clean result below is evidence
// and not blindness.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';

import { GATE_FILES } from './_lib/positions.mjs';

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

// The library's development subpaths ship the same directives again (and the
// whole unminified source beside them): every one of them is refused, whatever
// it carries, so a directive cannot sneak in dressed as dev tooling.
const BANNED_SPECIFIER_PREFIXES = [
  'lit/development/',
  'lit-html/development/',
  'lit-element/development/',
  '@lit/reactive-element/development/',
  '@lit-labs/ssr-client/development/',
];

// The directive names themselves. In SHIPPED source an imported name is an
// offence wherever it appears; a mention in a comment is handled by the same
// rule, on purpose: a comment is how it would be excused. Under test/ a test
// may NAME the directive it refuses (an assertion about the public surface
// spells the name); what no file may do anywhere is import it, and the
// specifier, subpath and dynamic-import scans run on test/ too.
const BANNED_NAMES = ['unsafeHTML', 'unsafeSVG', 'unsafeMathML', 'unsafeStatic'];

const IMPORT_RE = /(?:import|export)[^'"`]*['"]([^'"]+)['"]|import\(\s*['"]([^'"]+)['"]\s*\)/g;

const isTestFile = (rel) => rel.startsWith('test/');

// A dynamic import whose specifier is not a string literal cannot be judged,
// so it is refused: the specifier could be assembled to name anything,
// including the directives this gate bans. The argument scan is bracket
// balanced and string aware; an argument it cannot read is refused the same
// way, because the gate fails closed.
const offenceDynamicImport = (src) => {
  for (const m of src.matchAll(/import\s*\(/g)) {
    const openAt = m.index + m[0].length - 1;
    let depth = 0;
    let i = openAt + 1; // past the opening paren
    let arg = null;
    for (;;) {
      if (i >= src.length) break;
      const c = src[i];
      if (c === "'" || c === '"') {
        const q = c;
        i++;
        while (i < src.length && src[i] !== q) {
          if (src[i] === '\\') i++;
          i++;
        }
        i++;
        continue;
      }
      if (c === '`') {
        // A template literal in the argument: not a string literal, and it
        // ends the question.
        arg = 'template';
        break;
      }
      if (c === '(' || c === '{' || c === '[') depth++;
      if (c === ')' || c === '}' || c === ']') {
        if (depth === 0) {
          arg = src.slice(openAt + 1, i);
          break;
        }
        depth--;
      }
      if (c === ';' && depth === 0) break;
      i++;
    }
    if (arg === null) return 'a dynamic import whose argument list cannot be parsed, so it is refused (the gate fails closed)';
    if (!/^\s*(['"])([^'"]*)\1\s*$/.test(arg)) {
      return 'a dynamic import whose specifier is not a string literal';
    }
  }
  return null;
};

const offence = (src, rel) => {
  for (const m of src.matchAll(IMPORT_RE)) {
    const spec = m[1] ?? m[2];
    if (spec && BANNED_SPECIFIERS.some((b) => spec === b || spec === `${b}.js`)) return `import of ${spec}`;
    if (spec && BANNED_SPECIFIER_PREFIXES.some((p) => spec === p || spec.startsWith(p) || spec === p.slice(0, -1))) {
      return `an import of the lit family's development subpath (${spec})`;
    }
  }
  const dynamic = offenceDynamicImport(src);
  if (dynamic) return dynamic;
  if (!isTestFile(rel)) {
    for (const name of BANNED_NAMES) {
      if (src.includes(name)) return `a mention of ${name}`;
    }
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

// ── the walk: this package (src, harness, AND test minus the gates), plus dependents

const SOURCE_RE = /\.(ts|tsx|mts|cts|js|mjs|cjs|jsx|html)$/;

const walkSources = (dir, prefix, out) => {
  for (const entry of readdirSync(dir)) {
    const rel = prefix ? `${prefix}/${entry}` : entry;
    const abs = join(dir, entry);
    if (statSync(abs).isDirectory()) {
      if (entry === 'node_modules' || entry === '.git') continue;
      walkSources(abs, rel, out);
    } else if (SOURCE_RE.test(entry) && !GATE_FILES.includes(rel)) {
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

/** This package's sources: src and harness and test, the two gate files
 * excluded BY NAME (they hold the fixtures, which are offences by design). */
const thisPackageSources = () => {
  const out = [];
  for (const root of ['src', 'harness', 'test']) out.push(...walkSources(join(pkgDir, root), root, []));
  return out;
};

/** Every workspace package that depends on @hardscapehouse/design-system, by manifest or
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
      if (!manifest || manifest.name === '@hardscapehouse/design-system') continue;
      const deps = { ...(manifest.dependencies ?? {}), ...(manifest.devDependencies ?? {}), ...(manifest.peerDependencies ?? {}) };
      const sources = walkSources(abs, `${group}/${name}`, []);
      const imports = sources.some((s) => s.text.includes('@hardscapehouse/design-system'));
      if (deps['@hardscapehouse/design-system'] || imports) out.push({ name: manifest.name ?? `${group}/${name}`, sources });
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
    assert.ok(offence(p, 'src/plant.ts') !== null, `the scanner missed the planted offence: ${p}`);
  }
  // And it stays quiet on the imports the kits are allowed.
  for (const allowed of [
    `import { html, css } from 'lit';`,
    `import { LitElement } from 'lit';`,
    `import '../base.js';`,
    `import { render } from '@lit-labs/ssr';`,
  ]) {
    assert.equal(offence(allowed, 'src/fine.ts'), null, `the scanner flagged an allowed import: ${allowed}`);
  }
});

test('the development subpaths are banned specifiers too (guards gap two)', () => {
  const planted = [
    `import { unsafeHTML } from 'lit/development/directives/unsafe-html.js';`,
    `import { html } from 'lit/development/html.js';`,
    `import 'lit-html/development/lit-html.js';`,
    `import 'lit-element/development/lit-element.js';`,
    `import '@lit/reactive-element/development/reactive-element.js';`,
    `import 'lit-html/development/directives/unsafe-svg.js';`,
  ];
  for (const p of planted) {
    assert.match(
      String(offence(p, 'src/plant.ts')),
      /development subpath/,
      `the scanner missed the planted development subpath: ${p}`,
    );
  }
  // The sanctioned server render's own lib path is not a development subpath.
  assert.equal(offence(`import { render } from '@lit-labs/ssr/lib/render.js';`, 'test/_lib/render.mjs'), null);
});

test('a dynamic import whose specifier is not a string literal is refused (guards gap one)', () => {
  const planted = [
    `const m = await import(name);`,
    `const m = await import(['lit', '/directives/unsafe-html.js'].join(''));`,
    'const m = await import(`lit/directives/${which}.js`);',
  ];
  for (const p of planted) {
    assert.match(
      String(offence(p, 'src/plant.ts')),
      /not a string literal|fails closed/,
      `the scanner missed the planted computed import: ${p}`,
    );
  }
  // A literal dynamic import stays judgeable by the specifier scan.
  assert.equal(offence(`const m = await import('lit/directives/unsafe-html.js');`, 'src/plant.ts'), 'import of lit/directives/unsafe-html.js');
  assert.equal(offence(`const m = await import('lit');`, 'src/fine.ts'), null);
});

test('the raw DOM scanner sees the parser paths it refuses (guards the gate)', () => {
  for (const planted of [`el.innerHTML = x;`, `el.insertAdjacentHTML('beforeend', x);`, `document.write(x);`, `el.outerHTML = x;`]) {
    assert.ok(offenceDom(planted) !== null, `the DOM scanner missed: ${planted}`);
  }
  assert.equal(offenceDom(`const innerHTML = readStdin(); return innerHTML.length;`), null, 'the DOM scanner flagged a non-assignment mention');
});

test('the walk covers test/ as well, minus the two gate files by name (guards gap three)', () => {
  const files = thisPackageSources().map((s) => s.rel);
  for (const underTest of ['test/kits.test.mjs', 'test/_lib/render.mjs', 'test/_lib/cdp.mjs']) {
    assert.ok(files.includes(underTest), `walker missed ${underTest}: test/ is not scanned`);
  }
  for (const gate of GATE_FILES) {
    assert.ok(!files.includes(gate), `${gate} must be excluded from the walk by name`);
  }
});

test('the walk finds this package and names the workspace packages it must also scan', () => {
  // Without the walk the gate below would pass while scanning nothing.
  const files = thisPackageSources().map((s) => s.rel);
  for (const anchor of [
    'src/base.ts',
    'src/index.ts',
    'src/address.ts',
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
  // silence. The Counter Till app is the workspace's first dependent
  // (web/apps/counter-till): its sources are scanned by the gates below.
  assert.deepEqual(
    dependentPackages().map((d) => d.name),
    ['@hardscapehouse/counter-till'],
    'a package now depends on @hardscapehouse/design-system; its sources are scanned below, and this lock must name it deliberately',
  );
});

// ── the gates themselves ─────────────────────────────────────────────────────────

test('no source of this package, test included, imports or names a raw-markup escape hatch', () => {
  const offenders = [];
  for (const { rel, text } of thisPackageSources()) {
    for (const what of [offence(text, rel), offenceDom(text)]) {
      if (what) offenders.push(`${rel}: ${what}`);
    }
  }
  assert.deepEqual(
    offenders.sort(),
    [],
    `a source offers raw markup. The rule from the review rounds stands under Lit: no render ` +
      `path in this package may offer it, and the library's unsafe directives and static-html module ` +
      `are exactly that:\n  ${offenders.join('\n  ')}`,
  );
});

test('no source of any package that depends on this one imports or names a raw-markup escape hatch', () => {
  const offenders = [];
  for (const dependent of dependentPackages()) {
    for (const { rel, text } of dependent.sources) {
      for (const what of [offence(text, rel), offenceDom(text)]) {
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
