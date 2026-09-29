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
// The fixtures live here and this file is excluded from both gates' walks BY
// NAME (test/no-raw-markup.test.mjs and test/positions.test.mjs, in
// GATE_FILES), stated in the lock further down.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, unlinkSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';

import {
  auditSource,
  auditTagIntroductions,
  auditTagUsages,
  refusedBy,
  extractTemplates,
  thisPackageSources,
  everyPackageSource,
  dependentPackages,
  GATE_FILES,
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

// ── the tag's introduction: the gate cannot be left by a rename ─────────────────
//
// The extractor finds a template only where its tag is spelled html or svg,
// so the gate's second half holds, for every file it walks, that a lit
// template tag cannot enter a file under any other spelling. Each fixture
// below is one of the four introduction rules' fail proofs: it asserts a
// refusal, so removing the rule turns this file red.

const TAG_INTRODUCTION_FIXTURES = [
  ['an aliased import of html', `import { html as h } from 'lit';`, 'an import of html under the name h'],
  ['a namespace import from lit', `import * as everything from 'lit';`, 'a namespace import from lit'],
  ['an import of mathml', `import { mathml } from 'lit';`, 'an import of mathml'],
  ['a re-export of html from lit', `export { html } from 'lit';`, 'a re-export of html from lit'],
];

for (const [name, source, expectedPosition] of TAG_INTRODUCTION_FIXTURES) {
  test(`fixture: ${name}`, () => {
    const refusals = refusedBy(auditTagIntroductions(source, 'fixture.ts'));
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
    assert.ok(
      refusals.every((r) => !/^allowed$/.test(r.rule)),
      'the refusal must be a refusal',
    );
  });
}

// ── the tag's two places: one refusal per spelling the review round ran ─────────
//
// The parser rule (test/_lib/positions.mjs, auditTagUsages): in every file of
// the package, an imported html or svg may appear in exactly two places, its
// import statement and the tag position of a tagged template; any other
// appearance is refused, with the file and the line, and a local declaration
// of either name in any scope is refused, so the name always means the
// import. Each fixture below is one spelling's fail proof: it asserts the
// refusal naming the file and the LINE, so removing the rule turns this file
// red. This file is itself walked by this rule (the walk excludes only
// node_modules, the build output and .git) and holds no fixture of its kind:
// every fixture here is a string, which the parser never sees as code.

// Where a fixture file's relative imports resolve from: the package's own
// root, so the escape fixture's ../ provably lands outside the package and
// the allowed ./ stays inside it.
const TAG_USAGE_FIXTURE_DIR = PKG;

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
  ['a relative import that resolves outside the package',
   "import { html } from '../tag-source.js';\nhtml`<p>x</p>`;",
   'a relative import that leaves the package', 1],
];

for (const [name, source, expectedPosition, expectedLine] of TAG_USAGE_REFUSAL_FIXTURES) {
  test(`fixture: ${name}`, () => {
    const refusals = refusedBy(auditTagUsages(source, 'fixture.ts', TAG_USAGE_FIXTURE_DIR));
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
// that are only member labels.
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
  ['a relative import that stays inside the package',
   "import { html } from './local-tags.js';\nconst t = html`<p>x</p>`;"],
];

for (const [name, source] of TAG_USAGE_ALLOWED_FIXTURES) {
  test(`two places allowed: ${name}`, () => {
    const refusals = refusedBy(auditTagUsages(source, 'allowed-fixture.ts', TAG_USAGE_FIXTURE_DIR));
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
    ['a require of a module outside the package', "const m = require('../../tag-source.js');", 'a relative import that leaves the package'],
  ];
  for (const [name, source, expected] of planted) {
    const refusals = refusedBy(auditTagUsages(source, 'plant.ts', TAG_USAGE_FIXTURE_DIR));
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
    const refusals = refusedBy(auditTagUsages(quiet, 'fine.ts', TAG_USAGE_FIXTURE_DIR));
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

test('the tag introduction scanner sees every spelling that hides a tag (guards the gate)', () => {
  // The four fixtures above are one spelling each; the scanner must refuse
  // the whole family, so every spelling is planted here.
  const planted = [
    [`import { html as h } from 'lit';`, 'under the name h'],
    [`import { svg as vector } from 'lit';`, 'under the name vector'],
    [`import { html as h } from 'lit-html';`, 'under the name h'],
    [`import { svg as v } from 'lit/html.js';`, 'under the name v'],
    [`import { html as h } from 'lit-html/template.js';`, 'under the name h'],
    [`import { mathml as m } from 'lit';`, 'an import of mathml'],
    [`import * as lit from 'lit';`, 'a namespace import'],
    [`import lit from 'lit';`, 'a default import'],
    [`import lit, { html } from 'lit';`, 'a default import'],
    [`const m = await import('lit');`, 'a dynamic import of lit'],
    [`const m = await import('lit-html');`, 'a dynamic import of lit-html'],
    [`const m = require('lit');`, 'a require of lit'],
    [`const m = require(whichLit);`, 'not a string literal'],
    [`export { html } from 'lit';`, 'a re-export of html from lit'],
    [`export { html as h } from 'lit';`, 'a re-export of html from lit'],
    [`export { h as svg } from 'lit';`, 'a re-export of svg from lit'],
    [`export { mathml } from 'lit';`, 'a re-export of mathml from lit'],
    [`export * from 'lit';`, 'a star re-export'],
    [`export * as lit from 'lit';`, 'a star re-export'],
    [`import { html } from 'lit'; export { html as h };`, 'a re-export of the local html binding'],
    [`import { html } from 'lit'; export default html;`, 'a default re-export of html'],
  ];
  for (const [p, expected] of planted) {
    const refusals = refusedBy(auditTagIntroductions(p, 'plant.ts'));
    assert.ok(refusals.length >= 1, `the scanner missed the planted offence: ${p}`);
    assert.ok(
      refusals.some((r) => r.position.includes(expected)),
      `the refusal must name the position (${expected}): got ${JSON.stringify(refusals.map((r) => r.position))} for ${p}`,
    );
  }
  // And it stays quiet on the statements the kits are allowed: the tags under
  // their own names, the library's non tag exports, type only imports, and
  // specifiers that are not the lit packages.
  for (const allowed of [
    `import { html, css } from 'lit';`,
    `import { svg } from 'lit';`,
    `import { LitElement, type PropertyValues } from 'lit';`,
    `import { html, type TemplateResult as Template } from 'lit';`,
    `import { nothing } from 'lit';`,
    `import 'lit';`,
    `import { render } from '@lit-labs/ssr';`,
    `import { html } from './local.js';`,
    `import { join } from 'node:path';`,
    `const m = await import('./local.js');`,
    `export { css } from 'lit';`,
    `export * from './local.js';`,
    `export { css };`,
    `export type { KitState } from './state.js';`,
  ]) {
    const refusals = refusedBy(auditTagIntroductions(allowed, 'fine.ts'));
    assert.deepEqual(refusals, [], `the scanner flagged an allowed statement: ${allowed}: ${JSON.stringify(refusals)}`);
  }
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

test('no file of this package introduces a lit template tag the template pattern cannot see', () => {
  const offences = [];
  for (const { rel, text } of thisPackageSources()) {
    for (const o of refusedBy(auditTagIntroductions(text, rel))) offences.push(o);
  }
  assert.deepEqual(
    offences,
    [],
    `a file introduces an html, svg or mathml tag outside the spellings the template pattern sees` +
      ` (the tags under their own names, from lit or lit-html, are the only allowed introduction):\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

test('no file of any package that depends on this one introduces a tag the template pattern cannot see', () => {
  const offences = [];
  for (const dependent of dependentPackages()) {
    for (const { rel, text } of dependent.sources) {
      for (const o of refusedBy(auditTagIntroductions(text, rel))) {
        offences.push({ ...o, file: `${dependent.name} (${o.file})` });
      }
    }
  }
  assert.deepEqual(
    offences,
    [],
    `a dependent package introduces a tag outside the spellings the template pattern sees.` +
      ` The kits' rule binds its dependents too:\n  ` +
      offences.map((o) => `${o.file}:${o.line}: ${o.position} (${o.rule})`).join('\n  '),
  );
});

// ── the tag's two places, over every file of the package ────────────────────────

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

test('the walk reaches the package root: a re-export file there is named and refused', () => {
  // The hole the review round ran: a file at the package root, outside the
  // walked folders, re-exporting the library, imported by a kit through a
  // relative path. The root file is planted for real, walked, refused, and
  // removed, so the gate proves its own reach.
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
    const refusals = refusedBy(auditTagIntroductions(body, rootFileRel));
    assert.ok(
      refusals.some((r) => r.position.includes('a star re-export')),
      `the introduction scan must refuse the root file's re-export: got ${JSON.stringify(refusals.map((r) => r.position))}`,
    );
    assert.deepEqual(
      refusedBy(auditTagUsages(body, rootFileRel, PKG)),
      [],
      'the root file carries no tag identifier, so the two-place scan stays quiet on it',
    );
  } finally {
    unlinkSync(rootFile);
  }
});

test('no file of this package uses an imported html or svg anywhere but its import and its tag position', () => {
  const offences = [];
  for (const { rel, abs, text } of everyPackageSource()) {
    for (const o of refusedBy(auditTagUsages(text, rel, dirname(abs)))) offences.push(o);
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
    for (const { rel, abs, text } of dependent.sources) {
      for (const o of refusedBy(auditTagUsages(text, rel, dirname(abs), dependent.dir))) {
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

test('the gate files are excluded from the walks by name, and by no other exclusion', () => {
  // The fixtures are bindings in refused positions BY DESIGN; the exclusion is
  // the two gate files' names, nothing broader.
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
