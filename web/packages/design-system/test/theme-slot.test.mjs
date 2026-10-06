// The shared header element's theme slot (the item 5 followup): the host's
// own theme controls ride a NAMED slot inside the element's right cluster,
// after Switch role and Sign out, behind a thin separator. The element does
// not define the controls; it accepts them, the same hands-off shape the
// buttons' data-action contract keeps. What this file holds on the Node side
// is the WITHOUT half: a server render of an element with no slotted content
// carries no separator and no slot element at all, so the cluster is exactly
// Switch role and Sign out (and nothing, unsigned). The WITH half is the
// browser leg's (test/browser.test.mjs): slot assignment is a live-DOM
// question, and only a real page answers it.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderKit } from './_lib/render.mjs';
import { HosAppHeader } from '../src/header.js';

/** The rendered markup with markers stripped, the text the browser shows. */
const visible = (markup) => markup.replace(/<!--.*?-->/g, '');

test('without slotted theme content the signed-in cluster is exactly Switch role and Sign out', () => {
  const markup = visible(renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales' }));
  assert.ok(markup.includes('Switch role'), 'Switch role is present');
  assert.ok(markup.includes('Sign out'), 'Sign out is present');
  assert.ok(!markup.includes('<slot'), 'no slot element: an unused slot renders no empty placeholder');
  assert.ok(!markup.includes('name="theme"'), 'no theme slot attribute anywhere');
  assert.ok(!markup.includes('theme-sep'), 'no separator: the divider renders only between the buttons and real slotted content');
});

test('without slotted theme content the unsigned cluster renders nothing at all', () => {
  const markup = visible(renderKit(HosAppHeader));
  assert.ok(!markup.includes('<button'), 'unsigned renders no controls');
  assert.ok(!markup.includes('<slot'), 'unsigned renders no slot element');
  assert.ok(!markup.includes('theme-sep'), 'unsigned renders no separator');
});

test('the controls cluster keeps its shape in the server render the apps build against', () => {
  // The DOM-order contract the adoption rides: inside .controls the buttons
  // come first and the theme slot, when it renders, comes after them. In the
  // server render (no light DOM) the cluster is the two buttons alone; this
  // locks the order so a slot rendered ahead of the buttons is caught here
  // even before the browser leg runs.
  const markup = visible(renderKit(HosAppHeader, { signedIn: true, roleLabel: 'Sales' }));
  const controls = markup.match(/<div class="controls">([\s\S]*?)<\/div>/);
  assert.ok(controls !== null, 'the controls cluster rendered');
  const inner = controls[1];
  const switchAt = inner.indexOf('data-action="switch-role"');
  const signOutAt = inner.indexOf('data-action="sign-out"');
  assert.ok(switchAt !== -1 && signOutAt !== -1, 'both controls render inside the cluster');
  assert.ok(switchAt < signOutAt, 'Switch role comes before Sign out');
  assert.equal(inner.indexOf('<slot'), -1, 'no slot inside the cluster without slotted content');
});
