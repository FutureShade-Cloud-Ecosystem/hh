// The live demo server answers the page AND its element sheets. The shadow roots
// link ./sheets/<tag>.css, and a stylesheet request answered with HTML is refused
// by the browser, so the page rendered unstyled. The request handler is a pure
// function over a URL (demo/respond.mjs), so this suite can hold it without
// starting a server that never ends.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { respond } from '../demo/respond.mjs';

const demoDir = fileURLToPath(new URL('../demo/', import.meta.url));
const SHEETS = readdirSync(`${demoDir}sheets`).filter((name) => name.endsWith('.css'));

test('there are element sheets to serve', () => {
  // One sheet per element the page mounts: the header, the rail, the offline
  // banner, the update prompt, the conflict card list, and the shared Field
  // phone frame (phone-header, tab-bar, action-bar).
  assert.equal(SHEETS.length, 8, 'one sheet per element');
});

test('every committed sheet is served as text/css with the committed bytes', () => {
  for (const name of SHEETS) {
    const answer = respond(`/sheets/${name}`);
    assert.equal(answer.status, 200, `${name} answers`);
    assert.match(answer.headers['content-type'], /^text\/css\b/, `${name} is text/css, not html`);
    assert.deepEqual(Buffer.from(answer.body), readFileSync(`${demoDir}sheets/${name}`), `${name} is the committed file`);
  }
});

test('the page links exactly the sheets the server answers', () => {
  const page = readFileSync(`${demoDir}index.html`, 'utf8');
  const linked = new Set([...page.matchAll(/href="\.\/sheets\/([^"]+)"/g)].map((m) => m[1]));
  assert.ok(linked.size > 0, 'the page links sheets');
  for (const name of linked) assert.equal(respond(`/sheets/${name}`).status, 200, `${name} is served`);
});

test('the page still answers on the root, light on ?theme=light', () => {
  const brand = respond('/');
  const light = respond('/?theme=light');
  assert.equal(brand.status, 200);
  assert.match(brand.headers['content-type'], /^text\/html\b/);
  assert.deepEqual(Buffer.from(brand.body), readFileSync(`${demoDir}index.html`));
  assert.deepEqual(Buffer.from(light.body), readFileSync(`${demoDir}index-light.html`));
});

test('the sheets guard holds: no traversal, no other file, no unknown sheet', () => {
  for (const bad of [
    '/sheets/../serve.mjs',
    '/sheets/%2e%2e/serve.mjs',
    '/sheets/..%2fserve.mjs',
    '/sheets/hh-shell-header.css/../../serve.mjs',
    '/sheets/serve.mjs',
    '/sheets/index.html',
    '/sheets/nope.css',
    '/sheets/',
    '/sheets/%',
  ]) {
    const answer = respond(bad);
    // The URL parser folds a literal ../ out of the path before the handler sees it, which lands on the
    // page like any other path; what must never happen is any other file, or a css answer, for these.
    const isPage = answer.status === 200 && Buffer.from(answer.body).equals(readFileSync(`${demoDir}index.html`));
    assert.ok(answer.status === 404 || isPage, `${bad} is refused or answers the page, got ${answer.status}`);
    assert.doesNotMatch(String(answer.headers['content-type']), /css/, `${bad} is not served as css`);
    assert.ok(!Buffer.from(answer.body).toString('utf8').includes('createServer'), `${bad} never serves the server source`);
  }
});
