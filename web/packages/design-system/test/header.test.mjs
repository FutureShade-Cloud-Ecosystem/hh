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
