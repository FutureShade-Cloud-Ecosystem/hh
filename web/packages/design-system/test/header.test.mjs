// The shared header element's API (HH-RD2 polish round 2 item 5, decision log
// row 938): the front door's chrome as one custom element. What is asserted
// here is the element's own contract, over the server render the kit tests
// use: the brand lockup is always there, the right cluster rides the
// signed-in flag, the demo badge rides the demo flag, and the centred word
// defaults to the unsigned word the moment either input says nobody is
// signed in. The click contract (data-action names, no handlers of the
// element's own) is asserted structurally: the buttons carry the names and
// the markup carries no listener spelling at all.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderKit } from './_lib/render.mjs';
import { HosAppHeader } from '../src/header.js';
import { readPkg } from './_lib/sheet.mjs';

/** The rendered markup with markers stripped, the text the browser shows. */
const visible = (markup) => markup.replace(/<!--.*?-->/g, '');

/** The centred word the browser shows, the front door's own testid hook. */
const word = (markup) => {
  const m = visible(markup).match(/<p class="who" data-testid="header-word">(.*?)<\/p>/s);
  return m === null ? null : m[1].trim();
};

/** The button markup carrying an action name, if the cluster rendered one. */
const actionButton = (markup, action) => {
  const m = markup.match(new RegExp(`<button[^>]*data-action="${action}"[^>]*>[\\s\\S]*?</button>`));
  return m === null ? null : m[0];
};

test('the header renders the brand lockup: the HH mark, the product name with its accent word, the house line', () => {
  const markup = visible(renderKit(HosAppHeader));
  assert.ok(markup.includes('class="brand"'), 'the lockup div is present');
  assert.ok(markup.includes('role="img"'), 'the lockup is one image');
  assert.ok(markup.includes('aria-label="HH Yard, a Hardscape House product"'), 'the lockup names itself');
  assert.ok(markup.includes('aria-hidden="true"'), 'the mark is hidden from assistive tech');
  assert.ok(markup.includes('>HH<'), 'the mark is the plain text HH');
  assert.ok(markup.includes('HH Yard'), 'the product name reads HH Yard');
  assert.ok(markup.includes('a Hardscape House product'), 'the house line is present');
  assert.ok(markup.includes('class="accent"'), 'the accent word carries the gold-as-ink class');
});

test('unsigned (the default) the right cluster is gone: no Switch role, no Sign out', () => {
  const markup = renderKit(HosAppHeader);
  assert.ok(!markup.includes('Switch role'), 'no Switch role button');
  assert.ok(!markup.includes('Sign out'), 'no Sign out button');
  assert.ok(!markup.includes('data-action'), 'no action names at all');
});

test('signed in, the right cluster carries Switch role and Sign out under their action names', () => {
  const markup = renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales' });
  const switchRole = actionButton(markup, 'switch-role');
  const signOut = actionButton(markup, 'sign-out');
  assert.ok(switchRole !== null, 'the switch-role button is present');
  assert.ok(switchRole.includes('>Switch role</button>'), 'the switch-role button is labelled');
  assert.ok(switchRole.includes('class="btn-secondary"'), 'the switch-role button is a secondary control');
  assert.ok(signOut !== null, 'the sign-out button is present');
  assert.ok(signOut.includes('>Sign out</button>'), 'the sign-out button is labelled');
  assert.ok(signOut.includes('class="btn-secondary"'), 'the sign-out button is a secondary control');
});

test('the demo badge is off by default: the production build carries no banner', () => {
  const markup = renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales' });
  assert.ok(!markup.includes('role="note"'), 'no note anywhere');
  assert.ok(!markup.includes('invented demo data'), 'no demo copy');
});

test('demo on, the badge is present with its word and its copy', () => {
  const markup = visible(renderKit(HosAppHeader, { demo: true }));
  assert.ok(markup.includes('class="demo-banner"'), 'the badge div is present');
  assert.ok(markup.includes('role="note"'), 'the badge is a note');
  assert.ok(markup.includes('>Demo<'), 'the badge word is Demo');
  assert.ok(
    markup.includes('Everything on this box is invented demo data: nothing here is real.'),
    'the badge copy is the front door copy',
  );
});

test('the centred word defaults to Pick a role and takes the role label only when signed in', () => {
  assert.equal(word(renderKit(HosAppHeader)), 'Pick a role');
  assert.equal(
    word(renderKit(HosAppHeader, { signedIn: true })),
    'Pick a role',
    'a signed-in header with no label keeps the default word',
  );
  assert.equal(
    word(renderKit(HosAppHeader, { roleLabel: 'Sales' })),
    'Pick a role',
    'a role label without the signed-in flag does not name a role',
  );
  assert.equal(word(renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales' })), 'Sales');
  assert.equal(
    word(renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales manager' })),
    'Sales manager',
  );
});

// ---- the product name input (HH Drive's review fix) ----

test('a header given no product words says what it always said: HH Yard, a Hardscape House product', () => {
  const markup = visible(renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales', demo: true }));
  assert.ok(markup.includes('aria-label="HH Yard, a Hardscape House product"'));
  assert.ok(markup.includes('<span class="brand-product">HH <span class="accent">Yard</span></span>'.replace(/\s+/g, ' ')) || markup.includes('HH <span class="accent">Yard</span>'));
  assert.ok(markup.includes('<span class="brand-sub">a Hardscape House product</span>'));
  assert.equal(new HosAppHeader().compact, false, 'compact is off unless an app asks');
});

test('a header given a product name and a sub line wears them, accent word and all', () => {
  const markup = visible(renderKit(HosAppHeader, { productName: 'Drive', productSub: 'HH Drive · HH Field shell' }));
  assert.ok(markup.includes('aria-label="HH Drive, HH Drive · HH Field shell"'));
  assert.ok(markup.includes('HH <span class="accent">Drive</span>'));
  assert.ok(markup.includes('<span class="brand-sub">HH Drive · HH Field shell</span>'));
  assert.ok(!markup.includes('Yard'), 'no trace of the default words');
});

test('an empty product input falls back to the default words rather than draw a blank lockup', () => {
  const markup = visible(renderKit(HosAppHeader, { productName: '', productSub: '' }));
  assert.ok(markup.includes('aria-label="HH Yard, a Hardscape House product"'));
});

test('the compact layout is opt in, narrow only, and its controls are at least 44 px tall', () => {
  const css = HosAppHeader.styles.map((s) => s.cssText ?? String(s)).join('\n');
  const at = css.indexOf(':host([compact])');
  assert.ok(at !== -1, 'a compact rule exists');
  const compactRules = css.slice(at);
  assert.ok(/max-width:\s*480px/.test(css), 'it rides the narrow breakpoint');
  assert.ok(/min-height:\s*max\(44px,\s*var\(--hh-header-control-min,\s*0px\)\)/.test(compactRules), 'compact controls are 44 px tall, or the host floor when higher');
  // Nothing outside the opt in changed: the plain control rule keeps its small face.
  assert.ok(/\.controls button \{\s*min-height: var\(--hh-header-control-min, 0px\);/.test(css));
  assert.match(readPkg('src/tokens.css'), /--hh-header-control-min:\s*0px;/, 'the knob defaults to zero, so an app that sets nothing keeps its own small face');
});

test('every read of the header floor knob carries a zero fallback, so a page without tokens.css computes the old rules', () => {
  const css = HosAppHeader.styles.map((s) => s.cssText ?? String(s)).join('\n');
  const reads = css.match(/var\(--hh-header-control-min[^)]*\)/g) ?? [];
  assert.equal(reads.length, 2, 'the knob is read in the compact and the plain control rules, and nowhere else');
  for (const read of reads) assert.equal(read, 'var(--hh-header-control-min, 0px)', 'each read names its own zero fallback');
  // With the token undefined the reads collapse to main's rules: max(44px, 0px) is 44px, and the plain rule is 0px.
});
