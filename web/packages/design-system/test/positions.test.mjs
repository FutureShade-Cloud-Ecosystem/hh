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
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

import {
  auditSource,
  refusedBy,
  extractTemplates,
  thisPackageSources,
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
