// The demo harness and the house gates, in one suite: the committed demo
// pages and every sheet are held byte-fresh to a build; the package carries
// no em or en dashes, no calendar dates, no person names, and no hex colour
// literals in its element sources; the sources import only what the gate
// names; and the demo page carries every state the fidelity pass is asked to
// render.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { buildDemoPage, demoSheets, DEMO_TITLE } from '../demo/demo-page.ts';

const pkgDir = fileURLToPath(new URL('..', import.meta.url));

const TEXT_FILES = [
  'README.md',
  'package.json',
  'tsconfig.json',
  'src/shell-state.ts',
  'src/shell-header.ts',
  'src/shell-rail.ts',
  'src/shell-banner.ts',
  'src/shell-update.ts',
  'src/index.ts',
  'demo/demo-page.ts',
  'demo/build.mjs',
  'demo/serve.mjs',
  'demo/respond.mjs',
  'test/shell-state.test.mjs',
  'test/header.test.mjs',
  'test/rail.test.mjs',
  'test/banner-update.test.mjs',
  'test/lib/render.mjs',
  'test/_ts-js-loader.mjs',
];

const read = (rel) => readFileSync(join(pkgDir, rel), 'utf8');

// ── the demo pages and sheets are byte-fresh ─────────────────────────────────────────────────────

test('the committed demo pages are byte-equal to a fresh build', () => {
  assert.equal(read('demo/index.html'), buildDemoPage({ theme: 'dark' }), 'index.html (brand mode) is stale: run npm run build:demo');
  assert.equal(read('demo/index-light.html'), buildDemoPage({ theme: 'light' }), 'index-light.html is stale: run npm run build:demo');
});

test('the committed element sheets are byte-equal to a fresh build', () => {
  for (const { tag, css } of demoSheets()) {
    assert.equal(read(`demo/sheets/${tag}.css`), css, `demo/sheets/${tag}.css is stale: run npm run build:demo`);
  }
});

// ── the house rules, held by scan ────────────────────────────────────────────────────────────────

test('no em or en dashes anywhere in the package\u2019s own text', () => {
  const offences = TEXT_FILES.filter((rel) => /[\u2013\u2014]/.test(read(rel)));
  assert.deepEqual(offences, [], 'an em or en dash lives in these files; use the plain hyphen or the middle dot');
});

test('no calendar dates in the package\u2019s own text', () => {
  const offences = [];
  for (const rel of TEXT_FILES) {
    // The badge mark's path data carries four digit geometry coordinates
    // (V1940.1 and friends): geometry, not dates. Strip the path constants
    // before the scan looks.
    const text = read(rel).replace(/M[0-9A-Za-z. ]{40,}Z/g, ' ');
    const m = /\b(19|20)\d{2}\b/.exec(text);
    if (m !== null) offences.push(`${rel}: ${m[0]}`);
  }
  assert.deepEqual(offences, [], 'a four digit year reads as a date; carry no dates');
});

test('no person names: the committed text carries role labels only', () => {
  const offences = TEXT_FILES.filter((rel) => /Colton|Trenton/.test(read(rel)));
  assert.deepEqual(offences, [], 'a person name is in committed text; use role labels');
});

test('no hex colour literals in the element and demo sources: tokens only', () => {
  const sources = [
    'src/shell-state.ts',
    'src/shell-header.ts',
    'src/shell-rail.ts',
    'src/shell-banner.ts',
    'src/shell-update.ts',
    'demo/demo-page.ts',
  ];
  const offences = [];
  for (const rel of sources) {
    for (const m of read(rel).matchAll(/#[0-9a-fA-F]{3,8}\b/g)) offences.push(`${rel}: ${m[0]}`);
  }
  assert.deepEqual(offences, [], 'a hex literal is a colour the sheet does not own');
});

// ── the import gate, over the package's own sources ──────────────────────────────────────────────

test('the element sources import only lit, the design system, and their siblings', () => {
  const sources = ['src/shell-state.ts', 'src/shell-header.ts', 'src/shell-rail.ts', 'src/shell-banner.ts', 'src/shell-update.ts', 'src/index.ts'];
  const allowed = [/^lit$/, /^@hardscapehouse\/design-system\/src\/[a-z-]+\.js$/, /^\.\/[a-z-]+\.js$/];
  const offences = [];
  const statementRes = [
    /^\s*import\s[^'"\n]*?\bfrom\s*['"]([^'"]+)['"]/,
    /^\s*import\s*['"]([^'"]+)['"]/,
    /^\s*export\s[^'"\n]*?\bfrom\s*['"]([^'"]+)['"]/,
  ];
  for (const rel of sources) {
    const src = read(rel);
    if (/\brequire\s*\(|\bimport\s*\(/.test(src)) offences.push(`${rel}: a dynamic import or require`);
    for (const line of src.split('\n')) {
      for (const re of statementRes) {
        const hit = re.exec(line);
        if (hit !== null && !allowed.some((allowedRe) => allowedRe.test(hit[1]))) {
          offences.push(`${rel}: ${hit[1]}`);
        }
      }
    }
  }
  assert.deepEqual(offences, [], 'a specifier the gate does not name; adding one is a change a reviewer sees');
});

test('the element sources never spell a template tag outside lit, and never alias it', () => {
  const sources = ['src/shell-header.ts', 'src/shell-rail.ts', 'src/shell-banner.ts', 'src/shell-update.ts'];
  const offences = [];
  for (const rel of sources) {
    const src = read(rel);
    for (const m of src.matchAll(/import\s*\{([^}]*)\}\s*from\s*['"]lit['"]/g)) {
      for (const raw of m[1].split(',')) {
        const trimmed = raw.trim().replace(/^type\s+/, '');
        if (trimmed === '') continue;
        const [base, aliased] = trimmed.split(/\s+as\s+/);
        if (base === 'html' || base === 'svg') {
          if (aliased !== undefined) offences.push(`${rel}: the template tag ${base} under the name ${aliased}`);
          continue;
        }
        if (!/^(css|nothing|PropertyValues|TemplateResult|CSSResult|CSSResultGroup)$/.test(base)) {
          offences.push(`${rel}: ${base} from lit`);
        }
      }
    }
    for (const m of src.matchAll(/html\s+as\s+|[a-zA-Z]+(?:\s*=\s*html)\b/g)) {
      offences.push(`${rel}: an aliased or rebound template tag`);
    }
  }
  assert.deepEqual(offences, [], 'the tag rides under its own name only');
});

// ── the demo page carries everything the fidelity pass is asked to render ─────────────────────────

test('the demo page carries the whole state list the fidelity pass renders', () => {
  // The server render escapes apostrophes as entities; decode before matching.
  const decode = (s) => s.replace(/&#39;/g, "'").replace(/&quot;/g, '"').replace(/&amp;/g, '&');
  const page = decode(read('demo/index.html'));
  assert.ok(page.includes(DEMO_TITLE), 'the page names itself');
  assert.ok(page.includes('Gable Landscape Supply'), 'the demo tenant');
  assert.ok(page.includes('Yard Team'), 'role labels, never person names');
  for (const word of ['Synced', 'Pulling', 'Pushing', 'Offline', 'Reconnecting', '1 to review', 'Sign in expired', 'retrying']) {
    assert.ok(page.includes(word), `the sync state word: ${word}`);
  }
  for (const word of ["You're offline", "can't be reached", 'read only', 'Sign in expired']) {
    assert.ok(page.includes(word), `the banner word: ${word}`);
  }
  for (const word of ['Pinned', 'Pick queue', 'My run', 'House Messages', 'Receiving', 'Inbox']) {
    assert.ok(page.includes(word), `the rail and selector words: ${word}`);
  }
  assert.ok(page.includes('HH Field update ready'), 'the update prompt');
  assert.ok(page.includes('Update now'), 'and its primary action');
  assert.ok(!/<html[^>]*data-theme/.test(page), 'the default page is the brand mode');
  const light = read('demo/index-light.html');
  assert.ok(/<html[^>]*data-theme="light"/.test(light), 'the light build carries the theme attribute');
});

test('the sheets travel with the pages', () => {
  const sheetsDir = join(pkgDir, 'demo', 'sheets');
  const files = readdirSync(sheetsDir).filter((f) => statSync(join(sheetsDir, f)).isFile());
  assert.equal(files.length, demoSheets().length, 'one sheet per element, no strays');
});
