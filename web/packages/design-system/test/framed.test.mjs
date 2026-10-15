// The framed flag (item 8d, row 1520: one header band). A desk app framed by the
// Work shell host shows no header band of its own; the host says so with an
// explicit query parameter on the frame's address, never a guess from the
// window tree. What is asserted here: the flag is read strictly (one parameter,
// one value), honoured only for a page that really sits in a frame whose parent
// is at its own origin, and that the shared header element, the one band the
// desk apps mount, renders nothing when it is set and everything when it is not.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { renderKit } from './_lib/render.mjs';
import { HosAppHeader } from '../src/header.js';
import {
  CHROME_ACTION_MESSAGE,
  CHROME_CONTROLS_MESSAGE,
  FRAMED_BY_WORK_SHELL,
  FRAMED_PARAM,
  acceptChromeControls,
  announceChromeControls,
  chromeActionFrom,
  framedAddress,
  framedFlagFromSearch,
  readFramedByShell,
} from '../src/framed.js';

const ORIGIN = 'http://127.0.0.1:8080';

/** A window inside a same origin parent. */
const framedWindow = (search) => ({ location: { search, origin: ORIGIN }, parent: { location: { origin: ORIGIN } } });

test('the flag is the one parameter with the one value and nothing else', () => {
  assert.equal(framedFlagFromSearch(`?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`), true);
  assert.equal(framedFlagFromSearch(`?a=1&${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}&b=2`), true);
  assert.equal(framedFlagFromSearch(''), false, 'no query');
  assert.equal(framedFlagFromSearch(`?${FRAMED_PARAM}=`), false, 'empty value');
  assert.equal(framedFlagFromSearch(`?${FRAMED_PARAM}=1`), false, 'another value');
  assert.equal(framedFlagFromSearch(`?${FRAMED_PARAM}=true`), false, 'a truthy word is not the value');
  assert.equal(framedFlagFromSearch(`?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL.toUpperCase()}`), false, 'the value is exact');
  assert.equal(
    framedFlagFromSearch(`?${FRAMED_PARAM}=other&${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`),
    false,
    'a repeated parameter is refused, not resolved by order',
  );
});

test('a page is framed only with the flag, inside a frame, under a parent at its own origin', () => {
  assert.equal(readFramedByShell(framedWindow(`?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`)), true);
  assert.equal(readFramedByShell(framedWindow('')), false, 'framed with no flag keeps its band');
  const top = { location: { search: `?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`, origin: ORIGIN } };
  top.parent = top;
  assert.equal(readFramedByShell(top), false, 'the flag on a top level page enables nothing');
  const foreign = {
    location: { search: `?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`, origin: ORIGIN },
    get parent() {
      throw new DOMException('Blocked a frame from accessing a cross-origin frame.', 'SecurityError');
    },
  };
  // A cross origin parent: reading its location throws, which is "not the shell".
  const crossOrigin = {
    location: foreign.location,
    parent: {
      get location() {
        throw new DOMException('cross origin', 'SecurityError');
      },
    },
  };
  assert.equal(readFramedByShell(crossOrigin), false, 'a parent at another origin cannot enable it');
  const otherOrigin = { location: foreign.location, parent: { location: { origin: 'https://other.example' } } };
  assert.equal(readFramedByShell(otherOrigin), false, 'a readable parent at another origin cannot enable it');
});

test('the host builds a frame address that carries the flag, whatever the path already holds', () => {
  assert.equal(framedAddress('/till/'), `/till/?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`);
  assert.equal(framedAddress('/quote/?x=1'), `/quote/?x=1&${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}`);
  assert.equal(framedAddress('/home/#/a'), `/home/?${FRAMED_PARAM}=${FRAMED_BY_WORK_SHELL}#/a`);
  assert.equal(framedFlagFromSearch(new URL(framedAddress('/till/'), ORIGIN).search), true, 'the app reads back what the host wrote');
});

test('a chrome action is accepted only from the parent window, at the page’s own origin, for a control the shell carries', () => {
  const parent = {};
  const win = { parent, location: { origin: ORIGIN } };
  const good = { origin: ORIGIN, source: parent, data: { type: CHROME_ACTION_MESSAGE, action: 'theme' } };
  assert.equal(chromeActionFrom(good, win), 'theme');
  assert.equal(chromeActionFrom({ ...good, data: { type: CHROME_ACTION_MESSAGE, action: 'feedback' } }, win), 'feedback');
  assert.equal(chromeActionFrom({ ...good, data: { type: CHROME_ACTION_MESSAGE, action: 'sign-out' } }, win), null, 'the shell carries Theme and Feedback only');
  assert.equal(chromeActionFrom({ ...good, data: { type: CHROME_ACTION_MESSAGE, action: 'switch-role' } }, win), null, 'Switch role stays the host’s own');
  assert.equal(chromeActionFrom({ ...good, origin: 'https://other.example' }, win), null, 'another origin');
  assert.equal(chromeActionFrom({ ...good, source: {} }, win), null, 'another window');
  assert.equal(chromeActionFrom({ ...good, source: null }, win), null, 'no source');
  assert.equal(chromeActionFrom({ ...good, data: { type: 'other', action: 'theme' } }, win), null, 'another type');
  assert.equal(chromeActionFrom({ ...good, data: 'theme' }, win), null, 'not an object');
});

const visible = (markup) => markup.replace(/<!--.*?-->/g, '');

test('framed, the shared header band renders nothing: no brand lockup, no Demo badge, no controls', () => {
  const markup = visible(renderKit(HosAppHeader, { framed: true, signedIn: true, demo: true, roleLabel: 'Sales' }));
  assert.ok(!markup.includes('<header'), 'no band');
  assert.ok(!markup.includes('class="brand"'), 'no brand lockup');
  assert.ok(!markup.includes('Demo'), 'no Demo badge');
  assert.ok(!markup.includes('Switch role'), 'no Switch role');
  assert.ok(!markup.includes('Sign out'), 'no Sign out');
  assert.ok(!markup.includes('header-word'), 'no centred word');
});

test('standalone (not framed, the default), the same inputs render the whole band', () => {
  const markup = visible(renderKit(HosAppHeader, { signedIn: true, demo: true, roleLabel: 'Sales' }));
  assert.ok(markup.includes('<header'));
  assert.ok(markup.includes('class="brand"'));
  assert.ok(markup.includes('Demo'));
  assert.ok(markup.includes('Switch role'));
  assert.ok(markup.includes('Sign out'));
});

test('a framed app tells its parent which of the shell\u2019s controls it can act on, scoped to its own origin', () => {
  const posted = [];
  const parent = { postMessage: (message, origin) => posted.push([message, origin]) };
  announceChromeControls({ parent, location: { origin: ORIGIN } }, ['theme', 'feedback']);
  assert.deepEqual(posted, [[{ type: CHROME_CONTROLS_MESSAGE, actions: ['theme', 'feedback'] }, ORIGIN]]);
});

test('the host accepts the list only from its content frame at its own origin, keeping only the controls the shell carries', () => {
  const frame = {};
  const host = { origin: ORIGIN, contentWindow: frame };
  const good = { origin: ORIGIN, source: frame, data: { type: CHROME_CONTROLS_MESSAGE, actions: ['theme', 'feedback'] } };
  assert.deepEqual(acceptChromeControls(good, host), ['theme', 'feedback']);
  assert.deepEqual(
    acceptChromeControls({ ...good, data: { type: CHROME_CONTROLS_MESSAGE, actions: ['theme', 'sign-out', 'theme', 7] } }, host),
    ['theme'],
    'unknown names and repeats are dropped',
  );
  assert.equal(acceptChromeControls({ ...good, origin: 'https://other.example' }, host), null);
  assert.equal(acceptChromeControls({ ...good, source: {} }, host), null);
  assert.equal(acceptChromeControls({ ...good, data: { type: 'other', actions: ['theme'] } }, host), null);
  assert.equal(acceptChromeControls({ ...good, data: { type: CHROME_CONTROLS_MESSAGE, actions: 'theme' } }, host), null);
  assert.equal(acceptChromeControls(good, { origin: ORIGIN, contentWindow: null }), null);
});
