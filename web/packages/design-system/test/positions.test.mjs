// THE BINDING-POSITION GATE. The rule the review rounds added to the owned
// renderer stands under the adopted substrate: nothing in this package, and in
// no package that depends on it, binds a value anywhere the owned renderer's
// allow list refused. The scanner is render.ts's, from main's 81e6f689,
// pointed at source text (test/_lib/positions.mjs carries it and states the
// allow list). The gate judges the POSITION and never the value, so every
// fixture below is a small template with one binding and no payload, and each
// one IS its rule's fail proof: it asserts a refusal, so removing the rule
// turns this file red.
//
// Beside it, the IMPORT LIST: the gate names what is allowed and refuses the
// rest. In every walked file of the package, every import, re-export, dynamic
// import and require is refused unless its specifier and its imported names
// are on the list written in the gate (IMPORT_ALLOW_LIST, in
// test/_lib/positions.mjs), and eval, new Function, and a dynamic import or
// require whose argument is not a string literal are refused by the parser.
// The list itself is locked to the package's actual imports by a test below.
//
// The fixtures live here. This file and test/no-raw-markup.test.mjs are
// excluded from the POSITION walk and the raw-markup walk BY NAME (GATE_FILES),
// because those two scans read raw text and would see the fixture strings; the
// import list scan and the two-place scan walk this file too and stay quiet on
// it, because every fixture here is a string, which the parser never reads as
// code. The exclusion is stated in the lock further down.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, readFileSync, rmSync, unlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

import {
  auditSource,
  auditImports,
  auditTagUsages,
  refusedBy,
  extractTemplates,
  importedSpecifiers,
  thisPackageSources,
  everyPackageSource,
  dependentPackages,
  GATE_FILES,
  IMPORT_ALLOW_LIST,
  ALLOWED_PROPERTY_OR_EVENT,
  ADDRESS_HELPER,
} from './_lib/positions.mjs';

const PKG = join(new URL('..', import.meta.url).pathname);
const pkgText = (rel) => readFileSync(join(PKG, rel), 'utf8');

// ── the fixture table: one binding in each refused position, no payload ─────────

const REFUSED_FIXTURES = [
  ['a value in an event attribute', `html\`<button onclick="\${'x'}">go</button>\``, 'a value in the event attribute onclick'],
  ['a value in the style attribute', `html\`<div style="\${'x'}">t</div>\``, 'a value in the style attribute'],
  ['a value in srcdoc', `html\`<iframe srcdoc="\${'x'}"></iframe>\``, 'a value in the srcdoc attribute'],
  ['a value in the object data attribute', `html\`<object data="\${'x'}"></object>\``, 'a value in the data attribute'],
  ['a value in the SVG animation values attribute', `html\`<svg><animate values="\${'x'}"></animate></svg>\``, 'a value in the values attribute'],
  ['a value in the SVG set to attribute', `html\`<svg><set to="\${'x'}"></set></svg>\``, 'a value in the to attribute'],
  ['a value in the SVG animate from attribute', `html\`<svg><animate from="\${'x'}"></animate></svg>\``, 'a value in the from attribute'],
  ['a quoted value in an attribute off the allow list', `html\`<div title="\${'x'}">t</div>\``, 'a value in the title attribute'],
  ['an unquoted value in an attribute off the namespaces', `html\`<div title=\${'x'}>t</div>\``, 'an unquoted value in the title attribute'],
  ['an unquoted value with static text in it, in an allowed namespace', `html\`<div data-x=y\${'z'}>t</div>\``, 'an unquoted value with static text in it'],
  ['an unquoted value in class (allowed name, unquoted is namespaces only)', `html\`<div class=\${'x'}>t</div>\``, 'an unquoted value in the class attribute'],
  ['a binding where an attribute name goes', `html\`<div \${'disabled'}>t</div>\``, 'where an attribute name goes'],
  ['a property binding (the . form)', `html\`<div .value=\${'x'}>t</div>\``, 'a property binding (. form)'],
  ['an event binding (the @ form)', `html\`<button @click=\${'x'}>go</button>\``, 'an event binding (@ form)'],
  ['a boolean binding other than ?disabled', `html\`<div ?hidden=\${'x'}>t</div>\``, 'the boolean binding ?hidden'],
  ['an address attribute not through the helper', `html\`<a href="\${'/x'}">go</a>\``, 'a binding inside the address attribute href'],
  ['an address split over two bindings', `html\`<a href="\${'a'}\${'b'}">go</a>\``, 'not wholly one value', 'any'],
  ['an address with a static prefix, even through the helper', `html\`<a href="/p\${${ADDRESS_HELPER}('/x')}">go</a>\``, 'not wholly one value'],
  ['a binding inside a script tag', `html\`<script src="\${'x'}"></script>\``, 'of a script tag'],
  ['a binding inside a base tag', `html\`<base href="\${'x'}">\``, 'of a base tag'],
  ['a binding of an attribute name inside a script tag', `html\`<script \${'src'}="x"></script>\``, 'inside a script tag'],
  ['a binding of an attribute name inside a base tag', `html\`<base \${'target'}>\``, 'inside a base tag'],
  ['a parenthesis between attributes enters the unknown state', `html\`<div a="1" (\${'x'})>t</div>\``, 'cannot classify'],
  ['a binding inside a script body', `html\`<script>\${'x'}</script>\``, 'raw-text element script'],
  ['a binding inside a style body', `html\`<style>\${'x'}</style>\``, 'raw-text element style'],
  ['a binding inside a textarea body', `html\`<textarea>\${'x'}</textarea>\``, 'raw-text element textarea'],
  ['a binding inside a title body', `html\`<title>\${'x'}</title>\``, 'raw-text element title'],
  ['a binding inside an xmp body', `html\`<xmp>\${'x'}</xmp>\``, 'raw-text element xmp'],
  ['a binding inside a comment', `html\`<!-- \${'x'} -->\``, 'inside a comment'],
  ['a binding the scanner cannot classify (a quote that opens no value)', `html\`<a x' href="\${'x'}">go</a>\``, 'cannot classify'],
  ['a binding after a position the scanner cannot classify (a doctype)', `html\`<!DOCTYPE html><b title="\${'x'}">t</b>\``, 'cannot classify'],
  ['a template the extractor cannot parse into parts', 'html`<div ${\'x\'} ', 'the template body'],
];

// Each fixture is its rule's fail proof: the gate must name the position, the
// file, and the line.
for (const [name, source, expectedPosition, mode] of REFUSED_FIXTURES) {
  test(`fixture: ${name}`, () => {
    const refusals = refusedBy(auditSource(source, 'fixture.ts'));
    assert.ok(refusals.length >= 1, `the fixture must be refused, got ${JSON.stringify(refusals)}`);
    if (mode === 'any') {
      // Both bindings of a split address are judged; the whole-value rule must
      // fire among the refusals.
      assert.ok(
        refusals.some((r) => r.position.includes(expectedPosition)),
        `the refusal must name the position: got ${JSON.stringify(refusals.map((r) => r.position))}`,
      );
    } else {
      assert.equal(
        refusals.length,
        1,
        `expected exactly one refusal for the fixture, got ${JSON.stringify(refusals)}`,
      );
      assert.ok(
        refusals[0].position.includes(expectedPosition),
        `the refusal must name the position: got ${JSON.stringify(refusals[0].position)}, expected it to name ${JSON.stringify(expectedPosition)}`,
      );
      assert.equal(refusals[0].file, 'fixture.ts', 'the refusal must name the file');
      assert.equal(refusals[0].line, 1, 'the refusal must name the line');
    }
    assert.ok(
      refusals.every((r) => !/^allowed$/.test(r.rule)),
      'the refusal must be a refusal',
    );
  });
}

// ── the allowed positions keep passing: zero refusals on each ───────────────────

const ALLOWED_FIXTURES = [
  ['text content', `html\`<p>\${'hello'}</p>\``],
  ['a quoted value in class, with a static prefix', `html\`<button class="btn btn-\${'primary'}">go</button>\``],
  ['a quoted value in role', `html\`<div role="\${'status'}">t</div>\``],
  ['a quoted value in d', `html\`<svg><path d="\${'M4 4h16'}"></path></svg>\``],
  ['a quoted value in the data namespace', `html\`<div data-state="\${'ready'}">t</div>\``],
  ['a quoted value in the aria namespace', `html\`<div aria-label="\${'x'}">t</div>\``],
  ['the boolean binding ?disabled', `html\`<button ?disabled=\${true}>go</button>\``],
  ['a whole unquoted value in the data namespace', `html\`<button data-action=\${'confirm'}>go</button>\``],
  ['a whole unquoted value in the aria namespace', `html\`<li aria-current=\${'true'}>t</li>\``],
  ['an address as the whole value through the helper, quoted', `html\`<a href="\${${ADDRESS_HELPER}('/orders/7')}">go</a>\``],
  ['an address as the whole value through the helper, unquoted', `html\`<a href=\${${ADDRESS_HELPER}('/orders/7')}>go</a>\``],
  ['a nested template in text position, its own bindings judged', `html\`<ul>\${['a'].map((r) => html\`<li class="\${'row'}">\${r}</li>\`)}</ul>\``],
];

for (const [name, source] of ALLOWED_FIXTURES) {
  test(`allowed: ${name}`, () => {
    const refusals = refusedBy(auditSource(source, 'allowed-fixture.ts'));
    assert.deepEqual(refusals, [], `the gate refused an allowed position: ${JSON.stringify(refusals)}`);
  });
}

// ── the import list: the gate names what is allowed, and refuses the rest ────────
//
// In every walked file, every import, re-export, dynamic import and require
// is refused unless its specifier AND its imported names are on the gate's
// list (IMPORT_ALLOW_LIST, per folder: src, test, harness). html and svg are
// allowed from lit only, under their own names only. Each fixture below is
// one refusal's fail proof, naming the file, the LINE and the SPECIFIER, so
// removing the rule or widening the list turns this file red. Every fixture
// here is a string, which the parser never reads as code.

const LIST_REFUSAL_FIXTURES = [
  ['lit-element imported aliased (the family package the list does not name)',
   "import { html as h } from 'lit-element';",
   'an import from lit-element', 'src/fixture.ts'],
  ['lit-element imported as a namespace (the specifier alone refuses it)',
   "import * as litElement from 'lit-element';",
   'an import from lit-element', 'src/fixture.ts'],
  ['lit-element imported by subpath',
   "import { html as h } from 'lit-element/lit-element.js';",
   'an import from lit-element/lit-element.js', 'src/fixture.ts'],
  ['a package the list does not name',
   "import { thing } from 'some-new-package';",
   'an import from some-new-package', 'src/fixture.ts'],
  ['a listed package under a name the list does not carry',
   "import { render } from 'lit';",
   'an import of render from lit', 'src/fixture.ts'],
  ['the mathml tag (a name off the lit entry)',
   "import { mathml } from 'lit';",
   'an import of mathml from lit', 'src/fixture.ts'],
  ['the html tag under another name',
   "import { html as h } from 'lit';",
   'an import of html under the name h', 'src/fixture.ts'],
  ['the svg tag under another name',
   "import { svg as vector } from 'lit';",
   'an import of svg under the name vector', 'src/fixture.ts'],
  ['a namespace import from lit',
   "import * as everything from 'lit';",
   'a namespace import from lit', 'src/fixture.ts'],
  ['a default import from lit',
   "import lit from 'lit';",
   'an import of default from lit', 'src/fixture.ts'],
  ['a star re-export from lit',
   "export * from 'lit';",
   'a star re-export from lit', 'src/fixture.ts'],
  ['a namespace re-export from lit',
   "export * as lit from 'lit';",
   'a namespace re-export from lit', 'src/fixture.ts'],
  ['a re-export of the tag under another name',
   "export { html as h } from 'lit';",
   'an import of html under the name h', 'src/fixture.ts'],
  ['a dynamic import whose specifier is not a string literal',
   'const m = await import(whichLit);',
   'a dynamic import whose specifier is not a string literal', 'src/fixture.ts'],
  ['a dynamic import whose names the source does not show',
   "const m = await import('lit');",
   'a dynamic import of lit', 'src/fixture.ts'],
  ['a require whose specifier is not a string literal',
   'const m = require(whichLit);',
   'a require whose specifier is not a string literal', 'src/fixture.ts'],
  ['a require of a listed specifier (the names arrive where the statement does not show them)',
   "const m = require('lit');",
   'a require of lit', 'src/fixture.ts'],
  ['a relative import that resolves outside the package',
   "import { html } from '../tag-source.js';",
   'an import from ../tag-source.js', 'test/fixture.mjs'],
  ['a require of a module outside the package',
   "const m = require('../../tag-source.js');",
   'a require of ../../tag-source.js', 'test/fixture.mjs'],
  ['a call of eval',
   "const made = eval('makeTag()');",
   'a call of eval', 'src/fixture.ts'],
  ['a call of new Function',
   "const made = new Function('return html');",
   'a call of new Function', 'src/fixture.ts'],
];

for (const [name, source, expectedPosition, fixtureFile] of LIST_REFUSAL_FIXTURES) {
  test(`fixture: ${name}`, () => {
    const refusals = refusedBy(auditImports(source, fixtureFile));
    assert.equal(
      refusals.length,
      1,
      `expected exactly one refusal for the fixture, got ${JSON.stringify(refusals)}`,
    );
    assert.ok(
      refusals[0].position.includes(expectedPosition),
      `the refusal must name the position: got ${JSON.stringify(refusals[0].position)}, expected it to name ${JSON.stringify(expectedPosition)}`,
    );
    assert.equal(refusals[0].file, fixtureFile, 'the refusal must name the file');
    assert.equal(refusals[0].line, 1, 'the refusal must name the line');
    assert.ok(
      refusals.every((r) => !/^allowed$/.test(r.rule)),
      'the refusal must be a refusal',
    );
  });
}

// Allowed fixtures: every import the package makes today, folder by folder,
// stays quiet. The lock further down holds the list to exactly these, entry
// by entry and name by name.
const LIST_ALLOWED_FIXTURES = [
  ["the src folder's imports, at the package root", 'src/fixture.ts',
   [
     "import '@lit-labs/ssr/lib/install-global-dom-shim.js';",
     "import { render } from '@lit-labs/ssr';",
     "import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';",
     "import { CSSResult, type CSSResultGroup, type TemplateResult as Template } from 'lit';",
     "import { readFileSync } from 'node:fs';",
     "import { dirname, join } from 'node:path';",
     "import { fileURLToPath } from 'node:url';",
     "import { hhAddress } from './address.js';",
     "import { HHElement, HHRenderError, defineElement } from './base.js';",
     "import { ButtonSpec, baseStyles, button, chip, row, spinner } from './primitives.js';",
     "import { STATE_ICONS, StateIconName, icon } from './state-icons.js';",
     "import { DesignedState, KIT_STATES, STATE_META, KitState, kitShell, stateIndicator, stateStyles } from './state.js';",
     "export { css } from 'lit';",
     "export type { KitState, DesignedState } from './state.js';",
   ].join('\n')],
  ["the src folder's imports, in a kit subfolder", 'src/kits/fixture.ts',
   [
     "import { css, html, type TemplateResult as Template } from 'lit';",
     "import { defineElement, HHElement } from '../base.js';",
     "import { baseStyles, chip, row } from '../primitives.js';",
     "import { kitShell, stateStyles, ButtonSpec, KitState } from '../state.js';",
     "import { icon } from '../state-icons.js';",
   ].join('\n')],
  ["the test folder's imports", 'test/fixture.mjs',
   [
     "import { test } from 'node:test';",
     "import assert from 'node:assert/strict';",
     "import ts from 'typescript';",
     "import { html } from 'lit';",
     "import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, statSync, unlinkSync, writeFileSync } from 'node:fs';",
     "import { dirname, join, relative } from 'node:path';",
     "import { fileURLToPath } from 'node:url';",
     "import { createServer } from 'node:http';",
     "import { tmpdir } from 'node:os';",
     "import { spawn, spawnSync } from 'node:child_process';",
     "import { setTimeout as delay } from 'node:timers/promises';",
     "import { render } from '@lit-labs/ssr';",
     "import { findBrowserBinary, launchBrowser } from './_lib/cdp.mjs';",
     "import { hexToOklch, oklchToHex, parseOklch, relativeLuminance } from './_lib/color.mjs';",
     "import { contrast, palettes, readPkg } from './_lib/sheet.mjs';",
     "import { renderKit, renderTemplate } from './_lib/render.mjs';",
     "import { GATE_FILES } from './_lib/positions.mjs';",
     "import { KITS, buildHarnessPage, harnessSheets } from '../src/harness-page.js';",
     "import { baseStyles } from '../src/primitives.js';",
     "import { STATE_ICONS, icon } from '../src/state-icons.js';",
     "import { KIT_STATES, STATE_META, kitShell, stateIndicator } from '../src/state.js';",
     "import { DashboardKit } from '../src/kits/dashboard.js';",
     "import { ListDetailKit } from '../src/kits/list-detail.js';",
     "import { GuidedFlowKit } from '../src/kits/guided-flow.js';",
     "import { InboxKit } from '../src/kits/inbox.js';",
     "import { ScanConfirmKit } from '../src/kits/scan-and-confirm.js';",
     "({ stripTypeScriptTypes } = await import('node:module'));",
   ].join('\n')],
  ["the harness folder's imports", 'harness/fixture.mjs',
   [
     "import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';",
     "import { dirname, join } from 'node:path';",
     "import { fileURLToPath } from 'node:url';",
     "import { createServer } from 'node:http';",
     "import net from 'node:net';",
     "import { HARNESS_TITLE, buildHarnessPage, harnessSheets } from '../src/harness-page.js';",
   ].join('\n')],
];

for (const [name, fixtureFile, source] of LIST_ALLOWED_FIXTURES) {
  test(`allowed: ${name}`, () => {
    const refusals = refusedBy(auditImports(source, fixtureFile));
    assert.deepEqual(refusals, [], `the gate refused an allowed import: ${JSON.stringify(refusals)}`);
  });
}

// The list is what the package imports, not what it might: every entry is
// exercised by some walked file today, every name on an entry is imported
// today (svg excused, the pattern's second tag, listed so the door is named
// and not silent), and the tags sit on no entry but lit's.
test('the import list is exactly what the package imports today, and the tags sit on lit alone', () => {
  const actual = new Map();
  for (const { rel, text } of everyPackageSource()) {
    const folder = rel.split('/')[0];
    if (!['src', 'test', 'harness'].includes(folder)) continue;
    for (const { spec, names } of importedSpecifiers(text, rel)) {
      const key = `${folder}\u0000${spec}`;
      const seen = actual.get(key) ?? new Set();
      if (names === null) seen.add(null);
      else for (const n of names) seen.add(n);
      actual.set(key, seen);
    }
  }
  for (const folder of ['src', 'test', 'harness']) {
    for (const spec of Object.keys(IMPORT_ALLOW_LIST[folder])) {
      assert.ok(
        actual.has(`${folder}\u0000${spec}`),
        `the list entry ${folder}: ${spec} is imported by no walked file today: take it off the list or write the import (adding an entry is a change a reviewer sees)`,
      );
    }
  }
  const EXCUSED = ['svg'];
  for (const [folder, table] of Object.entries(IMPORT_ALLOW_LIST)) {
    for (const [spec, names] of Object.entries(table)) {
      for (const name of names) {
        assert.ok(
          EXCUSED.includes(name) || (actual.get(`${folder}\u0000${spec}`) ?? new Set()).has(name),
          `the name ${name} on the ${folder}: ${spec} entry is imported by no walked file today: take it off the entry or write the import`,
        );
      }
    }
  }
  for (const [folder, table] of Object.entries(IMPORT_ALLOW_LIST)) {
    for (const [spec, names] of Object.entries(table)) {
      for (const name of names) {
        if (name === 'html' || name === 'svg') {
          assert.equal(spec, 'lit', `the tag ${name} is allowed from lit only: it is on the ${folder}: ${spec} entry`);
        }
      }
    }
  }
});

test('the import list scanner refuses every spelling that could carry a tag (guards the gate)', () => {
  const planted = [
    [`import { svg as vector } from 'lit';`, 'under the name vector'],
    [`import { html as h } from 'lit-html';`, 'an import from lit-html'],
    [`import { svg as v } from 'lit/html.js';`, 'an import from lit/html.js'],
    [`import { html as h } from 'lit-element';`, 'an import from lit-element'],
    [`import * as litElement from 'lit-element/lit-element.js';`, 'an import from lit-element/lit-element.js'],
    [`import lit from 'lit';`, 'an import of default from lit'],
    [`import lit, { html } from 'lit';`, 'an import of default from lit'],
    [`import * as everything from 'lit';`, 'a namespace import from lit'],
    [`const m = await import('lit-html');`, 'a dynamic import of lit-html'],
    ['const m = await import(whichLit);', 'not a string literal'],
    ['const m = require(whichLit);', 'not a string literal'],
    [`export { html as h } from 'lit';`, 'under the name h'],
    [`export { h as svg } from 'lit';`, 'an import of h from lit'],
    [`export * from 'lit';`, 'a star re-export from lit'],
    [`export * as lit from 'lit';`, 'a namespace re-export from lit'],
    ["const made = eval('makeTag()');", 'a call of eval'],
    ["const made = new Function('return html');", 'a call of new Function'],
    [`import { thing } from 'some-new-package';`, 'an import from some-new-package'],
    [`import { render } from '@lit-labs/ssr/lib/render-result.js';`, 'an import of render from @lit-labs/ssr/lib/render-result.js'],
  ];
  // The planted spellings are judged as src files; the last one proves the
  // name rule on a listed subpath: the specifier is on the folder's list and
  // the name is not, so the refusal names the name.
  for (const [p, expected] of planted) {
    const refusals = refusedBy(auditImports(p, 'src/plant.ts'));
    assert.ok(refusals.length >= 1, `the scanner missed the planted offence: ${p}`);
    assert.ok(
      refusals.some((r) => r.position.includes(expected)),
      `the refusal must name the position (${expected}): got ${JSON.stringify(refusals.map((r) => r.position))} for ${p}`,
    );
  }
  // And it stays quiet on the statements the package is allowed: the tags
  // under their own names, type only names, side effect imports, re-exports
  // of listed names, and a dynamic import whose names the source shows.
  const quiet = [
    [`import { html, svg } from 'lit';`, 'src/quiet.ts'],
    [`import { LitElement, type PropertyValues } from 'lit';`, 'src/quiet.ts'],
    [`import { html, type TemplateResult as Template } from 'lit';`, 'src/quiet.ts'],
    [`import 'lit';`, 'src/quiet.ts'],
    [`import { kitShell } from './state.js';`, 'src/quiet.ts'],
    [`export { css } from 'lit';`, 'src/quiet.ts'],
    [`export type { KitState, DesignedState } from './state.js';`, 'src/quiet.ts'],
    [`import { test } from 'node:test';`, 'test/quiet.mjs'],
    [`import assert from 'node:assert/strict';`, 'test/quiet.mjs'],
    [`import ts from 'typescript';`, 'test/quiet.mjs'],
    [`const { stripTypeScriptTypes } = await import('node:module');`, 'test/quiet.mjs'],
    [`import net from 'node:net';`, 'harness/quiet.mjs'],
    [`import { buildHarnessPage } from '../src/harness-page.js';`, 'harness/quiet.mjs'],
  ];
  for (const [p, file] of quiet) {
    const refusals = refusedBy(auditImports(p, file));
    assert.deepEqual(refusals, [], `the scanner flagged an allowed statement: ${p}: ${JSON.stringify(refusals)}`);
  }
});

// ── the tag's two places: one refusal per spelling the review round ran ─────────
//
// The parser rule (test/_lib/positions.mjs, auditTagUsages): in every file of
// the package, an imported html or svg may appear in exactly two places, its
// import statement and the tag position of a tagged template; any other
// appearance is refused, with the file and the line, and a local declaration
// of either name in any scope is refused, so the name always means the
// import. Each fixture below is one spelling's fail proof: it asserts the
// refusal naming the file and the LINE, so removing the rule turns this file
// red. This file is itself walked by this rule (the walk leaves out only
// node_modules and the build output at the package's root, by their full
// path, and .git) and holds no fixture of its kind: every fixture here is a
// string, which the parser never sees as code.

const TAG_USAGE_REFUSAL_FIXTURES = [
  ['the tag bound to another local name, then a template under that name',
   "import { html } from 'lit';\nconst h = html;\nh`<p>x</p>`;",
   'the tag bound to a local name', 2],
  ['a function that wraps the tag',
   "import { html } from 'lit';\nfunction wrap() {\n  return html;\n}\nconst h = wrap();\nh`<p>x</p>`;",
   'the tag returned from a function', 3],
  ['the tag passed as an argument',
   "import { html } from 'lit';\nconst h = makeTag(html);\nh`<p>x</p>`;",
   'the tag passed as an argument', 2],
  ['a local rebinding that is then exported',
   "import { html } from 'lit';\nconst h = html;\nexport { h };",
   'the tag bound to a local name', 2],
  ['a local declaration of the name in a function scope (the shadowing case)',
   "function render(label) {\n  const html = `<b>${label}</b>`;\n  return label;\n}",
   'a local declaration of the name html', 2],
];

for (const [name, source, expectedPosition, expectedLine] of TAG_USAGE_REFUSAL_FIXTURES) {
  test(`fixture: ${name}`, () => {
    const refusals = refusedBy(auditTagUsages(source, 'fixture.ts'));
    assert.equal(
      refusals.length,
      1,
      `expected exactly one refusal for the fixture, got ${JSON.stringify(refusals)}`,
    );
    assert.ok(
      refusals[0].position.includes(expectedPosition),
      `the refusal must name the position: got ${JSON.stringify(refusals[0].position)}, expected it to name ${JSON.stringify(expectedPosition)}`,
    );
    assert.equal(refusals[0].file, 'fixture.ts', 'the refusal must name the file');
    assert.equal(refusals[0].line, expectedLine, `the refusal must name the line (${expectedLine})`);
    assert.ok(
      refusals[0].rule.includes('two places') || refusals[0].rule.includes('fails closed'),
      `the refusal must carry the rule: got ${JSON.stringify(refusals[0].rule)}`,
    );
  });
}

// Ordinary kit code stays quiet: the tags under their own names, in tag
// position, beside css, nested in maps, inside a component class, and names
// that are only member labels. What a relative specifier may BE is the import
// list's decision, not this rule's.
const TAG_USAGE_ALLOWED_FIXTURES = [
  ['the tags under their own names, in tag position only',
   "import { html, svg } from 'lit';\nconst t = html`<p>${'x'}</p>`;\nconst s = svg`<circle r=\"2\"></circle>`;"],
  ['css beside the tags',
   "import { css, html } from 'lit';\nconst t = html`<p>x</p>`;\nconst c = css`.p { color: red; }`;"],
  ['a nested template inside a map',
   "import { html } from 'lit';\nconst t = html`<ul>${['a'].map((r) => html`<li>${r}</li>`)}</ul>`;"],
  ['a component class rendering through the tag',
   "import { LitElement, html } from 'lit';\nclass X extends LitElement {\n  render() {\n    return html`<div></div>`;\n  }\n}"],
  ['names that are only member labels',
   "const box = { html: 1, svg: 2 };\nconst n = box.html;\nfunction f(p) { return p.svg; }"],
  ['a relative import judged only for its identifiers here',
   "import { html } from './local-tags.js';\nconst t = html`<p>x</p>`;"],
];

for (const [name, source] of TAG_USAGE_ALLOWED_FIXTURES) {
  test(`two places allowed: ${name}`, () => {
    const refusals = refusedBy(auditTagUsages(source, 'allowed-fixture.ts'));
    assert.deepEqual(refusals, [], `the gate refused an allowed use: ${JSON.stringify(refusals)}`);
  });
}

test('the two-place scanner refuses every shape the rule names (guards the gate)', () => {
  const planted = [
    ['an assignment', "import { html } from 'lit';\nlet h = null;\nh = html;", 'the tag assigned to another name'],
    ['an argument', "import { html } from 'lit';\nregister(html);", 'the tag passed as an argument'],
    ['a return', "import { html } from 'lit';\nfunction w() {\n  return html;\n}", 'the tag returned from a function'],
    ['a property value', "import { html } from 'lit';\nconst box = { tag: html };", 'the tag as a property value'],
    ['an export', "import { html } from 'lit';\nexport { html };", 'the tag exported from this file'],
    ['a spread', "import { html } from 'lit';\nconst pair = [...html];", 'the tag spread into an expression'],
    ['a typeof', "import { html } from 'lit';\nconst t = { f: typeof html };", 'the tag under a typeof'],
    ['a comma expression', "import { html } from 'lit';\nconst x = (0, html);", 'the tag riding a comma expression'],
    ['a shorthand property', "import { html } from 'lit';\nconst box = { html };", 'shorthand property'],
    ['a destructuring capture of the name', "const { html } = readTags();", 'a local declaration of the name html (a destructuring)'],
    ['a default import under the name', "import html from 'lit';", 'a declaration of the name html (a default or namespace import)'],
    ['a namespace import under the name', "import * as html from 'lit';", 'a declaration of the name html (a default or namespace import)'],
  ];
  for (const [name, source, expected] of planted) {
    const refusals = refusedBy(auditTagUsages(source, 'plant.ts'));
    assert.ok(refusals.length >= 1, `the scanner missed the planted ${name}: ${source}`);
    assert.ok(
      refusals.some((r) => r.position.includes(expected)),
      `the refusal must name the position (${expected}): got ${JSON.stringify(refusals.map((r) => r.position))} for ${source}`,
    );
  }
  // And it stays quiet where the names are only labels or the two places.
  for (const quiet of [
    "import { html } from 'lit';\nconst t = html`<p>x</p>`;",
    'const box = { html: 1, svg: 2 };\nbox.html; box.svg;',
    "import { html } from './local-tags.js';\nconst t = html`<p>x</p>`;",
  ]) {
    const refusals = refusedBy(auditTagUsages(quiet, 'fine.ts'));
    assert.deepEqual(refusals, [], `the scanner flagged a quiet use: ${quiet}: ${JSON.stringify(refusals)}`);
  }
});

// ── the scanner and the extractor guard themselves, so a clean gate is evidence ──

test('the extractor sees the package (guards the gate below)', () => {
  const files = thisPackageSources().map((s) => s.rel);
  for (const anchor of [
    'src/primitives.ts',
    'src/state.ts',
    'src/state-icons.ts',
    'src/kits/scan-and-confirm.ts',
    'src/kits/list-detail.ts',
    'src/kits/guided-flow.ts',
    'src/kits/dashboard.ts',
    'src/kits/inbox.ts',
  ]) {
    assert.ok(files.includes(anchor), `walker missed ${anchor}: its scan is not package-wide`);
  }
  const templateFiles = files.filter((f) => extractTemplates(pkgText(f)).length > 0);
  assert.ok(
    templateFiles.length >= 8,
    `expected the walk to find templates across the kit sources; found them in ${templateFiles.length} files`,
  );
});

test('the extractor survives nesting, strings and escapes (guards the classification)', () => {
  const src = [
    'const a = html`<ul>${rows.map((r) => html`<li class="${k}">${r}</li>`)}</ul>`;',
    'const b = html`<p>an escaped backtick: \\` and a ${value}</p>`;',
    "const c = html`<div data-x=\"it is fine\">${v}</div>`;",
  ].join('\n');
  const templates = extractTemplates(src);
  assert.equal(templates.length, 3, 'three tagged templates expected');
  const bindingsOf = (t) => t.chunks.filter((c) => c.type === 'binding').map((c) => c.text.trim());
  assert.deepEqual(bindingsOf(templates[0]), ['rows.map((r) => html`<li class="${k}">${r}</li>`)'], 'the nested template stays inside its expression');
  assert.deepEqual(bindingsOf(templates[1]), ['value'], 'an escaped backtick is text, and the expression after it still parses');
  assert.deepEqual(bindingsOf(templates[2]), ['v']);
});

// ── the gates themselves: the package and its dependents ────────────────────────

test('every html and svg template in this package sits in an allowed position', () => {
  const offences = [];
  for (const { rel, text } of thisPackageSources()) {
    for (const o of refusedBy(auditSource(text, rel))) offences.push(o);
  }
  assert.deepEqual(
    offences,
    [],
    `a template binds a value in a refused position. The owned renderer's allow list stands at source:\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

test('every html and svg template in any package that depends on this one sits in an allowed position', () => {
  const offences = [];
  for (const dependent of dependentPackages()) {
    for (const { rel, text } of dependent.sources) {
      for (const o of refusedBy(auditSource(text, rel))) offences.push({ ...o, file: `${dependent.name} (${o.file})` });
    }
  }
  assert.deepEqual(
    offences,
    [],
    `a dependent package binds a value in a refused position. The kits' rule binds its dependents too:\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

// ── the ported surface's allowance, proven narrow ────────────────────────────────
//
// The allowance is the lead's parity ruling (docs/BLOCKERS.md): the ported POS
// surface keeps the reference's Lit idioms, and the gate names the directory
// and the exact binding positions it uses rather than opening a wildcard. The
// guards below hold each edge of that shape, so a clean dependents scan above
// is evidence and not blindness.

const PORTED_FILE = 'apps/counter-till/src/ported/pos-terminal.ts';
const HH_FILE = 'apps/counter-till/src/views/till.ts';

test('the ported surface allowance is narrow: the directory, the names, and nothing beyond', () => {
  const refusalsOf = (source, file) => refusedBy(auditSource(source, file));

  // The same event binding: refused outside the prefix, allowed inside it.
  const click = `html\`<button @click=\${'x'}>go</button>\``;
  assert.ok(refusalsOf(click, HH_FILE).length > 0, 'an @click outside the ported prefix must stay refused');
  assert.deepEqual(refusalsOf(click, PORTED_FILE), [], 'an @click inside the ported prefix is allowed');

  // A binding name the ported files do not use is refused even inside the prefix.
  const planted = `html\`<button @frobnicate=\${'x'}>go</button>\``;
  assert.ok(refusalsOf(planted, PORTED_FILE).length > 0, 'an unlisted binding name must stay refused inside the prefix');
  const plantedBool = `html\`<button ?frobnicate=\${true}>go</button>\``;
  assert.ok(refusalsOf(plantedBool, PORTED_FILE).length > 0, 'an unlisted boolean must stay refused inside the prefix');

  // The raw-markup refusals keep biting inside the prefix.
  const srcdoc = `html\`<iframe srcdoc=\${'x'}></iframe>\``;
  assert.ok(refusalsOf(srcdoc, PORTED_FILE).length > 0, 'srcdoc must stay refused inside the prefix');

  // Bound values on the ported attributes: quoted or whole-unquoted inside,
  // refused outside; a partial unquoted value stays refused inside.
  const tab = `html\`<button tabindex=\${'0'}>go</button>\``;
  assert.ok(refusalsOf(tab, HH_FILE).length > 0, 'a tabindex binding outside the prefix must stay refused');
  assert.deepEqual(refusalsOf(tab, PORTED_FILE), [], 'a whole unquoted tabindex inside the prefix is allowed');
  const partial = `html\`<div tabindex=row-\${'0'}>t</div>\``;
  assert.ok(refusalsOf(partial, PORTED_FILE).length > 0, 'a partial unquoted value must stay refused inside the prefix');

  // style is the one deliberate admission past the gate's own refusal, inside
  // the prefix only.
  const style = `html\`<div style=\${'left: 4px'}>t</div>\``;
  assert.ok(refusalsOf(style, HH_FILE).length > 0, 'a style binding outside the prefix must stay refused');
  assert.deepEqual(refusalsOf(style, PORTED_FILE), [], "a style binding inside the prefix is the ruling's allowance");
});

test('no walked file of this package imports anything the import list does not name', () => {
  const offences = [];
  for (const { rel, text } of everyPackageSource()) {
    for (const o of refusedBy(auditImports(text, rel))) offences.push(o);
  }
  assert.deepEqual(
    offences,
    [],
    `a file imports a specifier or a name the gate's list does not carry` +
      ` (the list is IMPORT_ALLOW_LIST in test/_lib/positions.mjs; adding an entry is a change a reviewer sees):\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

// The import list is written for this package's folders. The rules it
// replaced ran on dependent packages too; when a package comes to depend on
// this one, the other gates' locks name it below, and what it may import
// must be argued into the gate the same way, entry by entry.

test('the two-place walk covers every source file of the package (guards the gate)', () => {
  const walked = everyPackageSource().map((s) => s.rel);
  for (const anchor of [
    'src/index.ts',
    'src/kits/inbox.ts',
    'harness/serve.mjs',
    'test/_lib/positions.mjs',
    'test/positions.test.mjs',
    'test/no-raw-markup.test.mjs',
    'src/_ts-js-loader.mjs',
  ]) {
    assert.ok(walked.includes(anchor), `the two-place walk missed ${anchor}`);
  }
});

test('the import list walk covers every source file of the package (guards the gate)', () => {
  const walked = everyPackageSource().map((s) => s.rel);
  for (const anchor of [
    'src/index.ts',
    'src/kits/inbox.ts',
    'src/_ts-js-loader.mjs',
    'harness/build.mjs',
    'test/_lib/cdp.mjs',
  ]) {
    assert.ok(walked.includes(anchor), `the import list walk missed ${anchor}`);
  }
});

test('the walk reaches the package root: a re-export file there is named and refused', () => {
  // The hole the review round ran: a file at the package root, outside the
  // walked folders, re-exporting the library, imported by a kit through a
  // relative path. The root file is planted for real, walked, refused, and
  // removed, so the gate proves its own reach. The root has no folder list,
  // so nothing it imports can be allowed.
  const rootFileRel = 'gate-root-fixture.mjs';
  const rootFile = join(PKG, rootFileRel);
  const body = "export * from 'lit';\n";
  writeFileSync(rootFile, body);
  try {
    const walked = everyPackageSource().map((s) => s.rel);
    assert.ok(
      walked.includes(rootFileRel),
      `the package walk must name a file at the package root: got ${JSON.stringify(walked)}`,
    );
    const refusals = refusedBy(auditImports(body, rootFileRel));
    assert.ok(
      refusals.some((r) => r.position.includes('a re-export from lit')),
      `the import list must refuse the root file's re-export: got ${JSON.stringify(refusals.map((r) => r.position))}`,
    );
    assert.deepEqual(
      refusedBy(auditTagUsages(body, rootFileRel)),
      [],
      'the root file carries no tag identifier, so the two-place scan stays quiet on it',
    );
  } finally {
    unlinkSync(rootFile);
  }
});

test('fixture: a source file inside a nested folder named like an exclusion is walked and refused', () => {
  // node_modules and the build output (dist) are excluded at the package's
  // root only, by their full path: a folder of either name anywhere else is
  // walked, so a source file cannot hide inside one. The file is planted for
  // real under a nested dist, walked, refused by the import list, and
  // removed.
  const nestedDir = join(PKG, 'src', 'dist');
  const nestedRel = 'src/dist/nested-fixture.mjs';
  const nestedFile = join(nestedDir, 'nested-fixture.mjs');
  mkdirSync(nestedDir, { recursive: true });
  const body = "import { html as h } from 'lit-element';\n";
  writeFileSync(nestedFile, body);
  try {
    const walked = everyPackageSource().map((s) => s.rel);
    assert.ok(
      walked.includes(nestedRel),
      `the walk must enter a nested folder named like an exclusion: got ${JSON.stringify(walked)}`,
    );
    const refusals = refusedBy(auditImports(body, nestedRel));
    assert.equal(refusals.length, 1, `expected exactly one refusal, got ${JSON.stringify(refusals)}`);
    assert.ok(
      refusals[0].position.includes('lit-element'),
      `the refusal must name the specifier: got ${JSON.stringify(refusals[0].position)}`,
    );
    assert.equal(refusals[0].file, nestedRel, 'the refusal must name the file');
  } finally {
    rmSync(nestedDir, { recursive: true, force: true });
  }
});

test('no file of this package uses an imported html or svg anywhere but its import and its tag position', () => {
  const offences = [];
  for (const { rel, text } of everyPackageSource()) {
    for (const o of refusedBy(auditTagUsages(text, rel))) offences.push(o);
  }
  assert.deepEqual(
    offences,
    [],
    `a file of this package uses an html or svg name outside its two places` +
      ` (the import statement and the tag position of a tagged template):\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

test('no file of any package that depends on this one uses a tag outside its two places', () => {
  const offences = [];
  for (const dependent of dependentPackages()) {
    for (const { rel, text } of dependent.sources) {
      for (const o of refusedBy(auditTagUsages(text, rel))) {
        offences.push({ ...o, file: `${dependent.name} (${o.file})` });
      }
    }
  }
  assert.deepEqual(
    offences,
    [],
    `a dependent package uses a tag outside its two places.` +
      ` The kits' rule binds its dependents too:\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

test('the property and event allow table is exactly what the kits need today, which is nothing', () => {
  // The allow list must name every . or @ binding that exists and no other.
  // None exists today: the table is empty, and this lock makes that a stated
  // fact, so a future binding must be added here, by name, or refused.
  assert.deepEqual([...ALLOWED_PROPERTY_OR_EVENT], []);
});

test('the gate files are excluded from the raw-text walks by name, and by no other exclusion', () => {
  // The fixtures are bindings in refused positions BY DESIGN; the two
  // raw-text scans (the position gate and the raw-markup gate) read source
  // as text and would see the fixture strings, so the exclusion names
  // exactly the two gate files. The parser scans (the import list and the
  // two-place rule) walk this file too: their exclusion is none, and they
  // stay quiet because every fixture here is a string.
  assert.deepEqual(
    [...GATE_FILES].sort(),
    ['test/no-raw-markup.test.mjs', 'test/positions.test.mjs'],
    'the by-name exclusion must name exactly the two gate files',
  );
  const scanned = thisPackageSources().map((s) => s.rel);
  for (const gate of GATE_FILES) {
    assert.ok(!scanned.includes(gate), `${gate} must be excluded from the walk by name`);
  }
  for (const mustScan of ['test/kits.test.mjs', 'test/_lib/render.mjs', 'src/primitives.ts']) {
    assert.ok(scanned.includes(mustScan), `${mustScan} must stay in the walk`);
  }
});
