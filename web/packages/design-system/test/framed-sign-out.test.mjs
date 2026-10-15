// Item 8e: the host asks a framed desk app to run its own sign out, and the app
// says when it has. The two messages, their validators and the app's one
// listener. A hostile page can send the same shapes and gets nothing past
// "the person is signed out": the request is accepted only from the app's own
// parent window at its own origin, and an acknowledgement only from the
// frame the host asked, with the nonce the host made for that ask.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MAX_SIGN_OUT_NONCE_LENGTH,
  SIGNED_OUT_MESSAGE,
  SIGN_OUT_REQUEST_MESSAGE,
  announceSignOut,
  announceSignedOut,
  listenForHostSignOut,
  signOutRequestFrom,
  signedOutFrom,
} from '../src/framed.js';

const ORIGIN = 'http://127.0.0.1:8080';
const NONCE = 'a1b2c3d4e5f60718';

test('the request is the exact type with a short plain nonce, from the app’s own parent at its own origin', () => {
  const parent = {};
  const win = { parent, location: { origin: ORIGIN } };
  const good = { origin: ORIGIN, source: parent, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE } };
  assert.equal(signOutRequestFrom(good, win), NONCE);
  assert.equal(signOutRequestFrom({ ...good, origin: 'https://other.example' }, win), null, 'another origin');
  assert.equal(signOutRequestFrom({ ...good, source: {} }, win), null, 'another window');
  assert.equal(signOutRequestFrom({ ...good, source: null }, win), null, 'no source');
  assert.equal(signOutRequestFrom({ ...good, data: { type: 'other', nonce: NONCE } }, win), null, 'another type');
  assert.equal(signOutRequestFrom({ ...good, data: NONCE }, win), null, 'not an object');
  assert.equal(signOutRequestFrom({ ...good, data: null }, win), null, 'null data');
  assert.equal(signOutRequestFrom({ ...good, data: { type: SIGN_OUT_REQUEST_MESSAGE } }, win), null, 'no nonce');
  assert.equal(signOutRequestFrom({ ...good, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: '' } }, win), null, 'empty nonce');
  assert.equal(signOutRequestFrom({ ...good, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: 7 } }, win), null, 'numeric nonce');
  assert.equal(signOutRequestFrom({ ...good, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: 'has space' } }, win), null, 'nonce outside the plain alphabet');
  assert.equal(
    signOutRequestFrom({ ...good, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: 'a'.repeat(MAX_SIGN_OUT_NONCE_LENGTH + 1) } }, win),
    null,
    'a nonce over the bound',
  );
});

test('the acknowledgement is the exact type with the nonce the host made, from the frame it asked, at its origin', () => {
  const frame = {};
  const host = { origin: ORIGIN, contentWindow: frame };
  const good = { origin: ORIGIN, source: frame, data: { type: SIGNED_OUT_MESSAGE, nonce: NONCE } };
  assert.equal(signedOutFrom(good, host), NONCE);
  assert.equal(signedOutFrom({ ...good, origin: 'https://other.example' }, host), null, 'another origin');
  assert.equal(signedOutFrom({ ...good, source: {} }, host), null, 'a window that is not the asked frame');
  assert.equal(signedOutFrom({ ...good, source: null }, host), null, 'no source');
  assert.equal(signedOutFrom(good, { origin: ORIGIN, contentWindow: null }), null, 'no frame mounted');
  assert.equal(signedOutFrom({ ...good, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE } }, host), null, 'the request type is not an acknowledgement');
  assert.equal(signedOutFrom({ ...good, data: { type: SIGNED_OUT_MESSAGE } }, host), null, 'no nonce');
  assert.equal(signedOutFrom({ ...good, data: { type: SIGNED_OUT_MESSAGE, nonce: 'x'.repeat(MAX_SIGN_OUT_NONCE_LENGTH + 1) } }, host), null, 'a long nonce');
});

test('both messages are posted scoped to the page’s own origin', () => {
  const posted = [];
  const target = { postMessage: (message, origin) => posted.push([message, origin]) };
  announceSignOut(target, ORIGIN, NONCE);
  announceSignedOut({ parent: target, location: { origin: ORIGIN } }, NONCE);
  assert.deepEqual(posted, [
    [{ type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE }, ORIGIN],
    [{ type: SIGNED_OUT_MESSAGE, nonce: NONCE }, ORIGIN],
  ]);
  assert.doesNotThrow(() => announceSignOut(null, ORIGIN, NONCE), 'a host with no frame sends nothing');
});

/** A fake window: a parent, an origin, a flag in the address, and the message listeners the helper adds. */
function fakeWindow(search) {
  const parent = { postMessage() {} };
  const listeners = new Set();
  const posted = [];
  parent.postMessage = (message, origin) => posted.push([message, origin]);
  const win = {
    parent,
    location: { origin: ORIGIN, search },
    addEventListener: (type, fn) => type === 'message' && listeners.add(fn),
    removeEventListener: (type, fn) => type === 'message' && listeners.delete(fn),
  };
  // a same origin parent reads as framed
  parent.location = { origin: ORIGIN };
  return { win, parent, listeners, posted };
}

test('a framed app runs its own sign out once per valid request, then acknowledges with the same nonce, after it settles', async () => {
  const { win, parent, listeners, posted } = fakeWindow('?hh-framed=work-shell');
  const order = [];
  let release;
  const gate = new Promise((r) => (release = r));
  const off = listenForHostSignOut(async () => {
    order.push('run');
    await gate;
    order.push('settled');
  }, win);
  assert.equal(listeners.size, 1);
  const [listener] = [...listeners];
  const pending = listener({ origin: ORIGIN, source: parent, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE } });
  assert.deepEqual(posted, [], 'no acknowledgement while the sign out is still running');
  release();
  await pending;
  order.push('acknowledged');
  assert.deepEqual(order, ['run', 'settled', 'acknowledged']);
  assert.deepEqual(posted, [[{ type: SIGNED_OUT_MESSAGE, nonce: NONCE }, ORIGIN]]);
  off();
  assert.equal(listeners.size, 0, 'the disposer removes the listener');
});

test('a sign out that throws is still acknowledged: the host must not wait on a broken app', async () => {
  const { win, parent, listeners, posted } = fakeWindow('?hh-framed=work-shell');
  listenForHostSignOut(() => {
    throw new Error('the app’s sign out failed');
  }, win);
  const [listener] = [...listeners];
  await listener({ origin: ORIGIN, source: parent, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE } });
  assert.deepEqual(posted, [[{ type: SIGNED_OUT_MESSAGE, nonce: NONCE }, ORIGIN]]);
});

test('a hostile message runs nothing and acknowledges nothing', async () => {
  const { win, parent, listeners, posted } = fakeWindow('?hh-framed=work-shell');
  let ran = 0;
  listenForHostSignOut(() => {
    ran += 1;
  }, win);
  const [listener] = [...listeners];
  const hostile = [
    { origin: ORIGIN, source: {}, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE } },
    { origin: 'https://other.example', source: parent, data: { type: SIGN_OUT_REQUEST_MESSAGE, nonce: NONCE } },
    { origin: ORIGIN, source: parent, data: { type: SIGN_OUT_REQUEST_MESSAGE } },
    { origin: ORIGIN, source: parent, data: { type: 'hh-work-shell:chrome-action', action: 'theme' } },
    { origin: ORIGIN, source: parent, data: 'hh-work-shell:sign-out' },
  ];
  for (const event of hostile) await listener(event);
  assert.equal(ran, 0);
  assert.deepEqual(posted, []);
});

test('a page that is not framed by the shell listens for nothing', () => {
  const alone = fakeWindow('');
  assert.doesNotThrow(() => listenForHostSignOut(() => {}, alone.win));
  assert.equal(alone.listeners.size, 0, 'no flag, no listener');
  const flagOnly = fakeWindow('?hh-framed=work-shell');
  flagOnly.win.parent = flagOnly.win; // top level: the flag alone enables nothing
  listenForHostSignOut(() => {}, flagOnly.win);
  assert.equal(flagOnly.listeners.size, 0, 'the flag at the top level enables nothing');
});
