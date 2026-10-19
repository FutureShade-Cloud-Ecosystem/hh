// THE REAL-BROWSER LEG. The source brief's acceptance says the kits run in a
// browser, and until this file existed the custom element path had never
// executed anywhere. This leg registers the five custom elements in headless
// Chromium over the DevTools protocol (zero npm dependencies: Node 22's global
// WebSocket, test/_lib/cdp.mjs), then renders every kit in every state in the
// live DOM, asserts what a person would SEE (the state words and icons painted
// with size, visibility and opaque colour; disabled controls unfaded), and
// holds the harness page to a clean layout at every gate width (1920, 1440,
// 1280, 768, and the 400 the review flagged).
//
// The page's sources are served through Node's own TypeScript stripping
// (module.stripTypeScriptTypes): the same job src/_ts-js-loader.mjs does for
// the Node process, done for a browser page. No build step, no new dependency.
//
// The skip is LOUD, and its accounting depends on where it lands. Under bun
// the leg never runs (bun runs the drift gate; Node 22 is this leg's
// authoritative runner), so one named statement registers in its place, in CI
// or out, exactly as before the CI rule existed. Under Node, a runtime missing
// a prerequisite (a global WebSocket, the stripping API of Node 22.13+, or a
// browser binary) prints the reason and registers a SKIP carrying it, so no
// summary counts the skipped leg as a pass; with CI set the same missing
// prerequisite FAILS instead, naming what is missing, because a skipped gate
// is a failed gate. The bun skip is never a failure: bun is not this leg's
// runner by design, and failing there would go red on every runner.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { existsSync, readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { findBrowserBinary, launchBrowser } from './_lib/cdp.mjs';
import { KIT_STATES, STATE_META } from '../src/state.js';
import { STATE_ICONS } from '../src/state-icons.js';
import { KITS, buildHarnessPage, harnessSheets } from '../src/harness-page.js';

// Guarded, not static: a runtime without this export (bun's node:module) would
// otherwise fail the whole file at link time instead of skipping loudly below.
let stripTypeScriptTypes = null;
try {
  ({ stripTypeScriptTypes } = await import('node:module'));
} catch {
  // no stripping API here: the guard below names it
}

const here = dirname(fileURLToPath(import.meta.url));
const pkgDir = join(here, '..');
const webRoot = join(pkgDir, '..', '..');

const TAGS = KITS.map((k) => k.tag);
const ALL_STATES = ['ready', ...KIT_STATES];
const PATTERNS = {
  ready: 'ready',
  empty: 'prompt',
  loading: 'progress',
  offline: 'cached',
  conflict: 'review',
  error: 'fault',
};
// A ready-state body marker per kit: proof the kit's OWN body renders, not a
// blank shell.
const READY_BODY_MARKER = {
  'hh-scan-and-confirm': 'scan-frame',
  'hh-list-detail': 'detail',
  'hh-guided-flow': 'rail',
  'hh-dashboard': 'bento',
  'hh-inbox': 'msg-main',
};

const underBun = typeof globalThis.Bun !== 'undefined';
const hasWebSocket = typeof globalThis.WebSocket === 'function';
const hasStrip = typeof stripTypeScriptTypes === 'function';
const binary = !underBun && hasWebSocket && hasStrip ? findBrowserBinary() : null;

function skipReason() {
  if (underBun) return 'the authoritative runner for this leg is Node 22 (bun runs the drift gate and skips the real-browser leg)';
  if (!hasWebSocket) return `this runtime has no global WebSocket; Node 22 is required, got ${process.version}`;
  if (!hasStrip) return 'this runtime has no module.stripTypeScriptTypes; Node 22.13+ is required';
  if (!binary) {
    return (
      'no headless Chromium binary exists (looked at $HH_DS_BROWSER, then google-chrome, ' +
      'google-chrome-stable, chromium, chromium-browser, chrome on the PATH, then the Playwright cache); ' +
      'point HH_DS_BROWSER at one to run this leg'
    );
  }
  return null;
}

// ── the element page: registers the real custom elements and renders them ───────

function elementPage() {
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>hh design system browser leg</title>
<link rel="stylesheet" href="/tokens.css">
<script type="importmap">
  {
    "imports": {
      "lit": "/node_modules/lit/index.js",
      "lit/": "/node_modules/lit/",
      "lit-html": "/node_modules/lit-html/lit-html.js",
      "lit-html/": "/node_modules/lit-html/",
      "lit-element": "/node_modules/lit-element/lit-element.js",
      "lit-element/": "/node_modules/lit-element/",
      "@lit/reactive-element": "/node_modules/@lit/reactive-element/reactive-element.js",
      "@lit/reactive-element/": "/node_modules/@lit/reactive-element/"
    }
  }
</script>
</head>
<body>
<main id="grid"></main>
<script type="module">
  import '/src/index.js';
  const main = document.getElementById('grid');
  for (const tag of ${JSON.stringify(TAGS)}) {
    for (const state of ${JSON.stringify(ALL_STATES)}) {
      const el = document.createElement(tag);
      el.setAttribute('state', state);
      el.dataset.cell = tag + '/' + state;
      main.append(el);
    }
  }
</script>
</body>
</html>`;
}

// The header slot leg's page: the shared header with and without slotted
// theme controls, static HTML so the elements upgrade with their light DOM
// children already in place (the shape every adopting app renders).
function headerPage() {
  const themeControls = (choice) => `
  <div slot="theme" class="theme-cycle">
    <button type="button" data-action="theme-cycle" data-choice="${choice}">Theme: ${choice}</button>
  </div>`;
  return `<!doctype html>
<html data-theme="light">
<head>
<meta charset="utf-8">
<title>hh design system header slot leg</title>
<link rel="stylesheet" href="/tokens.css">
<script type="importmap">
  {
    "imports": {
      "lit": "/node_modules/lit/index.js",
      "lit/": "/node_modules/lit/",
      "lit-html": "/node_modules/lit-html/lit-html.js",
      "lit-html/": "/node_modules/lit-html/",
      "lit-element": "/node_modules/lit-element/lit-element.js",
      "lit-element/": "/node_modules/lit-element/",
      "@lit/reactive-element": "/node_modules/@lit/reactive-element/reactive-element.js",
      "@lit/reactive-element/": "/node_modules/@lit/reactive-element/"
    }
  }
</script>
</head>
<body>
<hos-app-header id="bare-unsigned"></hos-app-header>
<hos-app-header id="bare-signed" role-label="Sales" signed-in></hos-app-header>
<hos-app-header id="slotted-signed" role-label="Sales" signed-in>${themeControls('auto')}</hos-app-header>
<hos-app-header id="slotted-unsigned">${themeControls('auto')}</hos-app-header>
<script type="module">
  import '/src/index.js';
</script>
</body>
</html>`;
}

// The icon leg's page: every state mark rendered bare through icon(), the way
// a host renders one, in the light DOM so the nodes are plain to inspect.
function iconPage() {
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>hh design system icon leg</title>
<script type="importmap">
  {
    "imports": {
      "lit": "/node_modules/lit/index.js",
      "lit/": "/node_modules/lit/",
      "lit-html": "/node_modules/lit-html/lit-html.js",
      "lit-html/": "/node_modules/lit-html/",
      "lit-element": "/node_modules/lit-element/lit-element.js",
      "lit-element/": "/node_modules/lit-element/",
      "@lit/reactive-element": "/node_modules/@lit/reactive-element/reactive-element.js",
      "@lit/reactive-element/": "/node_modules/@lit/reactive-element/"
    }
  }
</script>
<style>svg.icon { width: 48px; height: 48px; color: #000; }</style>
</head>
<body>
<main id="marks"></main>
<script type="module">
  import { render, html } from 'lit';
  import { icon } from '/src/state-icons.js';
  render(html${'`'}${'$'}{${JSON.stringify(Object.keys(STATE_ICONS))}.map((n) => html${'`'}<div data-mark="${'$'}{n}">${'$'}{icon(n)}</div>${'`'})}${'`'}, document.getElementById('marks'));
  window.__marksReady = true;
</script>
</body>
</html>`;
}

// The sheet's own browser page: a host page that mounts <hh-sheet> open with
// a real trigger button (the focus return lands on it) and a slotted actions
// button (the brief's "the action slot visible and not covered" assertion has
// something to find).
function sheetPage() {
  return `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>hh design system sheet leg</title>
<link rel="stylesheet" href="/tokens.css">
<script type="importmap">
  {
    "imports": {
      "lit": "/node_modules/lit/index.js",
      "lit/": "/node_modules/lit/",
      "lit-html": "/node_modules/lit-html/lit-html.js",
      "lit-html/": "/node_modules/lit-html/",
      "lit-element": "/node_modules/lit-element/lit-element.js",
      "lit-element/": "/node_modules/lit-element/",
      "@lit/reactive-element": "/node_modules/@lit/reactive-element/reactive-element.js",
      "@lit/reactive-element/": "/node_modules/@lit/reactive-element/"
    }
  }
</script>
</head>
<body style="margin:0; padding:0; background: var(--hh-page); min-height:100vh;">
<button type="button" id="trigger" data-testid="sheet-trigger" style="margin:24px;">Open the sheet</button>
<hh-sheet id="sheet" heading="Pick a reason" data-testid="hh-sheet">
  <span slot="title">Pick a reason</span>
  <span slot="description">One of these for the change.</span>
  <ul>
    <li><button type="button" data-testid="row-1">Counted wrong</button></li>
    <li><button type="button" data-testid="row-2">Wrong item</button></li>
  </ul>
  <button type="button" slot="actions" data-testid="sheet-action" id="sheet-action">Save</button>
</hh-sheet>
<script type="module">
  import '/src/index.js';
  await new Promise((done) => {
    const t0 = Date.now();
    const poll = () => (customElements.get('hh-sheet') || Date.now() - t0 > 5000 ? done() : setTimeout(poll, 25));
    poll();
  });
  const sheet = document.getElementById('sheet');
  const trigger = document.getElementById('trigger');
  trigger.addEventListener('click', () => { sheet.open = true; });
  window.__sheetReady = true;
</script>
</body>
</html>`;
}

function startServer() {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
    if (url.pathname === '/tokens.css') {
      // The element page must paint the DESIGNED colours: without the sheet,
      // every var() colour is invalid at computed-value time and falls back to
      // inherited black, and a colour assertion would pass vacuously.
      res.writeHead(200, { 'content-type': 'text/css; charset=utf-8' });
      res.end(readFileSync(join(pkgDir, 'src', 'tokens.css'), 'utf8'));
      return;
    }
    if (url.pathname === '/') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(elementPage());
      return;
    }
    if (url.pathname === '/harness') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(buildHarnessPage({ theme: 'dark', brand: 'base' }));
      return;
    }
    if (url.pathname === '/icons') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(iconPage());
      return;
    }
    if (url.pathname === '/header') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(headerPage());
      return;
    }
    if (url.pathname === '/sheet') {
      res.writeHead(200, { 'content-type': 'text/html; charset=utf-8' });
      res.end(sheetPage());
      return;
    }
    // The per-kit sheets the harness page's declarative shadow roots link.
    const sheet = url.pathname.match(/^\/sheets\/([a-z0-9-]+)\.css$/);
    if (sheet) {
      const kit = harnessSheets().find((s) => s.tag === sheet[1]);
      if (kit) {
        res.writeHead(200, { 'content-type': 'text/css; charset=utf-8' });
        res.end(kit.css);
        return;
      }
    }
    if (url.pathname.startsWith('/node_modules/')) {
      // The sources import from 'lit' by bare specifier; the import map above
      // points it here, and lit's own imports are relative from there.
      const abs = join(webRoot, url.pathname.slice(1));
      if (existsSync(abs) && statSync(abs).isFile()) {
        res.writeHead(200, { 'content-type': 'text/javascript; charset=utf-8' });
        res.end(readFileSync(abs, 'utf8'));
        return;
      }
    }
    if (url.pathname.startsWith('/src/')) {
      // Map a .js specifier onto its .ts source, stripped for the browser.
      const abs = join(pkgDir, 'src', url.pathname.slice('/src/'.length));
      const candidates = abs.endsWith('.js') ? [abs.slice(0, -3) + '.ts', abs] : [abs];
      for (const c of candidates) {
        if (existsSync(c) && statSync(c).isFile()) {
          let code = readFileSync(c, 'utf8');
          if (c.endsWith('.ts')) code = stripTypeScriptTypes(code, { mode: 'strip' });
          res.writeHead(200, { 'content-type': 'text/javascript; charset=utf-8' });
          res.end(code);
          return;
        }
      }
    }
    res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' });
    res.end('not found');
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      resolve({ port: server.address().port, close: () => new Promise((r) => server.close(r)) });
    });
  });
}

// ── the leg itself ───────────────────────────────────────────────────────────────

// The leg's registration, by runner and by CI. Under bun: the one named
// statement, as before, in CI or out (a skip there is by design, and bun's
// node:test layer ignores a { skip } option, so the statement stays a plain
// one). Under Node with CI set, a missing prerequisite is a FAILURE naming
// what is missing. Under Node without CI, the same statement registers as a
// SKIP carrying the reason, so the tally counts it under skipped and never
// under pass. The registration shape matches test/harness.test.mjs: bun
// interleaves async tests across files (oven-sh/bun#5090), so the async legs
// register on node only.
const reason = skipReason();
const inCI = process.env.CI !== undefined && process.env.CI !== '';

if (reason && inCI && !underBun) {
  // A skipped gate is a failed gate: on a runner (CI set, under Node) a
  // missing browser FAILS the job and names what is missing, it never passes
  // with a warning. The bun runtime is excluded: there the skip is by design
  // (bun runs the drift gate, Node 22 runs this leg), and the statement below
  // registers with CI set too, so the workflow's bun step stays green.
  test('the headless-browser leg refuses to skip when CI is set', () => {
    assert.fail(
      `CI is set and the headless-browser leg cannot run; it fails instead of skipping: ${reason}. ` +
        `Install the browser the leg uses (google-chrome) or point HH_DS_BROWSER at one.`,
    );
  });
} else if (reason && underBun) {
  // The by-design bun statement, registered as before, in CI or out.
  test('the headless-browser leg has a loud, named reason for not running here', () => {
    console.warn(`SKIPPING the headless-browser leg: ${reason}`);
    assert.ok(reason.length > 0, 'the skip reason must be stated, never silent');
  });
} else if (reason) {
  // Node without CI: the leg is skipped, and a skip is not a pass. The same
  // statement registers as a SKIP carrying the named reason, so every tally
  // the suite prints counts it under skipped, never under pass.
  console.warn(`SKIPPING the headless-browser leg: ${reason}`);
  test(
    'the headless-browser leg has a loud, named reason for not running here',
    { skip: reason },
    () => assert.fail('the headless-browser leg must not run where it declared a named skip'),
  );
} else {
  test('the five custom elements register and render every kit in every state in a real page', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const snapshot = await cdp.evaluate(
        `(async () => {
          // Lit renders on a microtask after connect, not inside
          // connectedCallback, and each test drives a cold browser: wait for
          // the tags to register and the elements to upgrade instead of a
          // fixed sleep, which loses that race under load.
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              const els = [...document.querySelectorAll('main > *')];
              const ok = ${JSON.stringify(TAGS)}.every((tag) => !!customElements.get(tag)) &&
                els.length > 0 && els.every((el) => el.shadowRoot !== null);
              if (ok || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const cells = [];
          for (const el of document.querySelectorAll('main > *')) {
            const root = el.shadowRoot;
            const kitRoot = root ? root.querySelector('.kit-root') : null;
            cells.push({
              cell: el.dataset.cell,
              upgraded: !!kitRoot,
              state: kitRoot ? kitRoot.getAttribute('data-state') : null,
              pattern: kitRoot ? kitRoot.getAttribute('data-pattern') : null,
              html: root ? root.innerHTML : '',
            });
          }
          return {
            registered: ${JSON.stringify(TAGS)}.map((t) => [t, !!customElements.get(t)]),
            cells,
          };
        })()`,
      );

      const unregistered = snapshot.registered.filter(([, ok]) => !ok).map(([t]) => t);
      assert.deepEqual(unregistered, [], 'these tags never registered as custom elements');

      const missing = [];
      for (const tag of TAGS) {
        for (const state of ALL_STATES) {
          const cell = snapshot.cells.find((c) => c.cell === `${tag}/${state}`);
          if (!cell) {
            missing.push(`${tag}/${state}: the page has no such element`);
            continue;
          }
          assert.ok(cell.upgraded, `${tag}/${state}: the element never upgraded, so the browser render path did not run`);
          assert.equal(cell.state, state, `${tag}/${state}: wrong data-state in the live DOM`);
          assert.equal(cell.pattern, PATTERNS[state], `${tag}/${state}: wrong data-pattern in the live DOM`);
          if (state === 'ready') {
            // The ready state carries no state icon by design: its body and
            // its live actions are the render.
            assert.match(cell.html, /data-live="true"/, `${tag}/${state}: ready actions must be live in the live DOM`);
            assert.ok(
              cell.html.includes(READY_BODY_MARKER[tag]),
              `${tag}/${state}: the ready body is missing this kit's own body marker (${READY_BODY_MARKER[tag]})`,
            );
          } else {
            assert.match(cell.html, /<svg[\s\S]*<path d="/, `${tag}/${state}: no icon geometry in the live DOM`);
            assert.ok(
              cell.html.includes(STATE_META[state].label),
              `${tag}/${state}: the live DOM does not show the state word`,
            );
          }
        }
      }
      assert.deepEqual(missing, [], missing.join('\n'));
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('the harness page lays out at every gate width without sideways scroll or escaping cells', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/harness`);
      // The widths the two-pane kits must survive: at 1280 and 768 the frames
      // are narrow enough that a fixed-width pane pushes the page sideways,
      // which the reviewer proved the 1440/1920/400 list let pass.
      for (const width of [1920, 1440, 1280, 768, 400]) {
        await cdp.setViewport(width, 1000);
        const m = await cdp.evaluate(
          `(() => {
            const doc = document.scrollingElement;
            const overflowing = [];
            for (const fig of document.querySelectorAll('figure.cell')) {
              const frame = fig.closest('.width-frame').getBoundingClientRect();
              const r = fig.getBoundingClientRect();
              if (r.left < frame.left - 0.5 || r.right > frame.right + 0.5) overflowing.push(fig.dataset.cell);
            }
            return { scrollWidth: doc.scrollWidth, clientWidth: doc.clientWidth, overflowing };
          })()`,
        );
        assert.ok(
          m.scrollWidth <= m.clientWidth + 0.5,
          `at a ${width} pixel window the page scrolls sideways (${m.scrollWidth} wide in a ${m.clientWidth} viewport)`,
        );
        assert.deepEqual(m.overflowing, [], `at a ${width} pixel window these cells run past their width frame`);
      }
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('the leg asserts what a person would see: state words and icons painted, disabled controls unfaded', async () => {
    // Source-regex gates can always be spelled around; this asserts the PAINT:
    // per designed state, the state's word and icon have a real box, are
    // visible, are at least 12 pixels, and are drawn in a colour with its
    // alpha intact; every disabled button keeps computed opacity 1 and an
    // opaque colour (a fade is not a state, in the browser too).
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const { tokensLoaded, out: cells } = await cdp.evaluate(
        `(async () => {
          // Lit renders on a microtask after connect, not inside
          // connectedCallback, and each test drives a cold browser: wait for
          // the tags to register and the elements to upgrade instead of a
          // fixed sleep, which loses that race under load.
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              const els = [...document.querySelectorAll('main > *')];
              const ok = ${JSON.stringify(TAGS)}.every((tag) => !!customElements.get(tag)) &&
                els.length > 0 && els.every((el) => el.shadowRoot !== null);
              if (ok || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const tokensLoaded = document.styleSheets.length > 0;
          const alphaOf = (c) => {
            const m = String(c).match(/rgba?\\(([^)]+)\\)/);
            if (!m) return null;
            const parts = m[1].replace(/\\//g, ' ').trim().split(/[\\s,]+/).map(Number);
            return parts.length >= 4 ? parts[3] : 1;
          };
          const paint = (el, colourProp) => {
            const cs = getComputedStyle(el);
            const box = el.getBoundingClientRect();
            const colour = colourProp ? cs[colourProp] : cs.color;
            return {
              display: cs.display,
              visibility: cs.visibility,
              fontSize: parseFloat(cs.fontSize),
              opacity: parseFloat(cs.opacity),
              colour,
              alpha: alphaOf(colour),
              width: box.width,
              height: box.height,
            };
          };
          const out = [];
          for (const el of document.querySelectorAll('main > *')) {
            const root = el.shadowRoot;
            const kitRoot = root ? root.querySelector('.kit-root') : null;
            if (!kitRoot) { out.push({ cell: el.dataset.cell, upgraded: false }); continue; }
            const state = kitRoot.getAttribute('data-state');
            const wordEl = kitRoot.querySelector('.state-label, .banner-text, .review-title');
            out.push({
              cell: el.dataset.cell,
              upgraded: true,
              state,
              word: wordEl ? { text: wordEl.textContent.trim(), ...paint(wordEl) } : null,
              icons: [...kitRoot.querySelectorAll('svg.icon')].map((i) => paint(i, 'stroke')),
              disabled: [...kitRoot.querySelectorAll('button[disabled]')].map((b) => paint(b)),
              selected: kitRoot.querySelectorAll('[aria-current="true"]').length,
            });
          }
          return { tokensLoaded, out };
        })()`,
      );

      // The colour assertions below are only honest against the designed
      // palette: without the sheet every var() colour falls back to inherited
      // black and an alpha check would pass vacuously.
      assert.ok(tokensLoaded, 'the element page loaded no token sheet; colour assertions would be vacuous');

      const ROW_KITS = ['hh-list-detail', 'hh-inbox'];
      const BODY_STATES = ['ready', 'offline', 'conflict'];
      for (const cell of cells) {
        assert.ok(cell.upgraded, `${cell.cell}: the element never upgraded`);
        const [tag, state] = cell.cell.split('/');
        if (state !== 'ready') {
          // The word: present, visible, readable, opaque.
          assert.ok(cell.word, `${cell.cell}: no state word element at all`);
          assert.ok(cell.word.text.length > 0, `${cell.cell}: the state word is empty`);
          assert.equal(cell.word.visibility, 'visible', `${cell.cell}: the state word is not visible`);
          assert.notEqual(cell.word.display, 'none', `${cell.cell}: the state word is display none`);
          assert.ok(cell.word.fontSize >= 12, `${cell.cell}: the state word renders under 12 pixels (${cell.word.fontSize})`);
          assert.ok(cell.word.width > 0 && cell.word.height > 0, `${cell.cell}: the state word has an empty box`);
          assert.ok(
            cell.word.alpha !== null && cell.word.alpha >= 0.999,
            `${cell.cell}: the state word colour is not opaque (${cell.word.colour})`,
          );
        }
        // Every icon on the cell (the state's mark, the offline banner's, the
        // review head's, the done check): painted, visible, opaque.
        for (const icon of cell.icons) {
          assert.notEqual(icon.display, 'none', `${cell.cell}: an icon is display none`);
          assert.equal(icon.visibility, 'visible', `${cell.cell}: an icon is not visible`);
          assert.ok(icon.width >= 1 && icon.height >= 1, `${cell.cell}: an icon has an empty box`);
          assert.match(String(icon.colour), /rgb/, `${cell.cell}: an icon stroke did not resolve to a colour`);
          assert.ok(
            icon.alpha !== null && icon.alpha >= 0.999,
            `${cell.cell}: an icon colour is not opaque (${icon.colour})`,
          );
        }
        // Disabled changes colour, not opacity, in the paint as in the sheet.
        for (const b of cell.disabled) {
          assert.equal(b.opacity, 1, `${cell.cell}: a disabled control is faded (opacity ${b.opacity})`);
          assert.ok(
            b.alpha !== null && b.alpha >= 0.999,
            `${cell.cell}: a disabled control's ink is not opaque (${b.colour})`,
          );
        }
        // The ARIA half of the selected row, in the live DOM.
        const expectSelected = ROW_KITS.includes(tag) && BODY_STATES.includes(state) ? 1 : 0;
        assert.equal(cell.selected, expectSelected, `${cell.cell}: wrong count of aria-current="true" rows`);
      }
      // The word assertions must be exercised: every kit in every designed
      // state had a word to check.
      const designedCells = cells.filter((c) => c.state !== 'ready');
      assert.equal(designedCells.length, KITS.length * KIT_STATES.length);
      assert.ok(designedCells.every((c) => c.word), 'a designed state rendered without a word to assert on');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('an unknown state fails loudly in the browser: a named error and no empty element', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const out = await cdp.evaluate(
        `(async () => {
          const failures = [];
          const seen = new Set();
          const record = (name, message) => {
            if (!seen.has(message)) { seen.add(message); failures.push({ name, message }); }
          };
          const onErr = (ev) => record(ev.error ? ev.error.name : null, String(ev.error ? ev.error.message : ev.message));
          const onRejection = (ev) => {
            const r = ev.reason;
            record(r instanceof Error ? r.name : null, String(r instanceof Error ? r.message : r));
          };
          window.addEventListener('error', onErr);
          window.addEventListener('unhandledrejection', onRejection);
          const settle = () => new Promise((r) => setTimeout(r, 80));
          // Each test drives a cold browser: the module that registers the
          // tags may still be loading. Wait for the tag before creating an
          // element, so append upgrades it synchronously.
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              if (customElements.get('hh-scan-and-confirm') || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const stateOf = (el) => el.shadowRoot ? (el.shadowRoot.querySelector('.kit-root')?.getAttribute('data-state') ?? null) : null;
          const result = {};
          // A FAILED FIRST RENDER: the state is bad before the element ever
          // connects. Lit reports an exception thrown out of an update as an
          // unhandled rejection naming the element, and commits nothing: the
          // element must end up with a shadow root that paints nothing, never
          // a wrong paint and never a quietly blank kit that looks rendered.
          // Distinct refusals are counted, not events: Lit's updateComplete
          // contract re-surfaces the same rejected update once on the next
          // update (an awaiter-less rejected promise re-rejected), which is
          // the same refusal echoing, not a new one.
          const bad = document.createElement('hh-scan-and-confirm');
          bad.setAttribute('state', 'nonsense');
          document.body.append(bad);
          await settle();
          result.errorCount = failures.length;
          result.name = failures[0] ? failures[0].name : null;
          result.message = failures[0] ? failures[0].message : null;
          result.shadowAfterRefusal = bad.shadowRoot === null ? 'none' : bad.shadowRoot.innerHTML.length;
          bad.removeAttribute('state');
          await settle();
          result.stateAfterRemoval = stateOf(bad);
          result.errorsAfterRemoval = failures.length;
          bad.setAttribute('state', 'error');
          await settle();
          result.stateAfterValid = stateOf(bad);
          result.errorsAfterValid = failures.length;
          // A FAILED RE-RENDER of an element already painted: Lit never
          // commits a partial render, so the last good paint stays (it is
          // never blanked), and the failed cycle's own promise carries the
          // named error. (Its message is identical to the first element's, so
          // the distinct-event listener above cannot count it; the failed
          // updateComplete promise is the precise observation.)
          const painted = document.createElement('hh-scan-and-confirm');
          document.body.append(painted);
          await settle();
          result.paintedState = stateOf(painted);
          result.paintedLength = painted.shadowRoot.innerHTML.length;
          painted.setAttribute('state', 'nonsense');
          const failedCycle = painted.updateComplete;
          result.badRerenderSettled = await new Promise((done) => {
            failedCycle.then(
              () => done('resolved'),
              (e) => {
                result.badRerenderName = e instanceof Error ? e.name : null;
                result.badRerenderMessage = String(e instanceof Error ? e.message : e);
                done('rejected');
              },
            );
          });
          await settle();
          result.paintedStateAfterRerender = stateOf(painted);
          result.paintedLengthAfterRerender = painted.shadowRoot.innerHTML.length;
          window.removeEventListener('error', onErr);
          window.removeEventListener('unhandledrejection', onRejection);
          return result;
        })()`,
      );
      assert.equal(out.errorCount, 1, 'an unknown state must fail loudly at render with exactly one distinct reported error');
      assert.equal(out.name, 'HHRenderError', 'the failure must carry the named error');
      assert.match(out.message, /hh-scan-and-confirm/, 'the error must name the kit');
      assert.match(out.message, /nonsense/, 'the error must name the bad state');
      assert.equal(
        out.shadowAfterRefusal,
        0,
        'a refused first render must commit nothing: the element stays unpainted, never a wrong paint',
      );
      assert.equal(
        out.stateAfterRemoval,
        'ready',
        'removing the attribute must fall back to the default, not a stale render',
      );
      assert.equal(out.errorsAfterRemoval, 1, 'the fallback render must succeed without a second distinct error');
      assert.equal(out.stateAfterValid, 'error', 'a valid state after the refusal must render again');
      assert.equal(out.errorsAfterValid, 1, 'the valid render must succeed without a second distinct error');
      assert.equal(out.paintedState, 'ready', 'the painted element must render before the bad re-render');
      assert.equal(
        out.badRerenderSettled,
        'rejected',
        'a failed re-render must reject its update cycle loudly, not quietly skip',
      );
      assert.equal(out.badRerenderName, 'HHRenderError', 'the failed re-render must carry the named error');
      assert.match(out.badRerenderMessage, /hh-scan-and-confirm/, 'the failed re-render must name the kit');
      assert.match(out.badRerenderMessage, /nonsense/, 'the failed re-render must name the bad state');
      assert.equal(
        out.paintedStateAfterRerender,
        'ready',
        'a failed re-render must keep the last good paint, never an empty element',
      );
      assert.equal(out.paintedLengthAfterRerender, out.paintedLength, 'the kept paint must be the previous render, byte for byte');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('a consumer listener written as the README says receives the action: composedPath, not event.target', async () => {
    // The README's delegation pattern is the one integration shape every shell
    // writes against these kits, so the leg proves it in the browser, both
    // halves: at the host the shadow boundary has already RETARGETED
    // event.target (it arrives as the host itself, and closest finds nothing),
    // and the action arrives off event.composedPath().
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const out = await cdp.evaluate(
        `(async () => {
          // Lit renders on a microtask after connect, not inside
          // connectedCallback, and each test drives a cold browser: wait for
          // the tags to register and the elements to upgrade instead of a
          // fixed sleep, which loses that race under load.
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              const els = [...document.querySelectorAll('main > *')];
              const ok = ${JSON.stringify(TAGS)}.every((tag) => !!customElements.get(tag)) &&
                els.length > 0 && els.every((el) => el.shadowRoot !== null);
              if (ok || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const host = document.querySelector('hh-scan-and-confirm[state="ready"]');
          const received = [];
          // The README's pattern, in shape: one listener on the host element,
          // the control read off the composed path.
          host.addEventListener('click', (event) => {
            const control = event.composedPath().find((n) => n instanceof Element && n.hasAttribute('data-action'));
            received.push(control ? control.getAttribute('data-action') : null);
          });
          // The measurement: what event.target is at the host, and what the
          // old advice (event.target.closest) finds there.
          let targetIsHost = false;
          let targetTag = null;
          let targetClosest = 'not measured';
          host.addEventListener('click', (event) => {
            targetIsHost = event.target === host;
            targetTag = event.target.tagName;
            targetClosest = event.target.closest('[data-action]');
          });
          const button = host.shadowRoot.querySelector('button[data-action]');
          button.click();
          return {
            action: button.getAttribute('data-action'),
            received,
            targetIsHost,
            targetTag,
            targetClosestFound: targetClosest !== null,
          };
        })()`,
      );
      assert.ok(out.action, 'the ready kit must render a live data-action control to click');
      assert.equal(out.received.length, 1, 'the host listener must fire exactly once for the click');
      assert.equal(
        out.received[0],
        out.action,
        'a listener written as the README says (composedPath) must receive the action',
      );
      assert.equal(
        out.targetIsHost,
        true,
        `event.target at the host must be the host itself (got ${out.targetTag}): the shadow boundary retargets it`,
      );
      assert.equal(
        out.targetClosestFound,
        false,
        "event.target.closest('[data-action]') at the host must find nothing, which is why the README says composedPath",
      );
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('a property set re-renders the kit: the reactive property drives the render, not only the attribute', async () => {
    // The reviewer checked this by hand in Chrome; the suite holds it now. The
    // state is set as a PROPERTY (host.state = ...), never as an attribute, so
    // a render that followed the attribute would leave the kit stale and this
    // test red.
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const out = await cdp.evaluate(
        `(async () => {
          // Each test drives a cold browser: wait for the tag to register
          // before creating an element, so append upgrades it synchronously.
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              if (customElements.get('hh-scan-and-confirm') || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const host = document.createElement('hh-scan-and-confirm');
          document.body.append(host);
          await host.updateComplete;
          const stateOf = () => host.shadowRoot.querySelector('.kit-root')?.getAttribute('data-state') ?? null;
          const before = stateOf();
          host.state = 'error';
          await host.updateComplete;
          const word = host.shadowRoot.querySelector('.kit-root .state-label, .kit-root .banner-text, .kit-root .review-title');
          return {
            before,
            after: stateOf(),
            attributeUnset: host.getAttribute('state') === null,
            word: word ? word.textContent.trim() : null,
          };
        })()`,
      );
      assert.equal(out.before, 'ready', 'the kit must render its default state before the property set');
      assert.equal(
        out.after,
        'error',
        'a property set must re-render: data-state must follow the property the test set',
      );
      assert.equal(
        out.attributeUnset,
        true,
        'the render must have followed the property: the state attribute was never set',
      );
      assert.equal(
        out.word,
        'Something went wrong',
        'the re-render must paint the designed error state, not only move the marker',
      );
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('a same-state update keeps its nodes: Lit diffs the parts, it does not redraw the root', async () => {
    // The reviewer checked this by hand in Chrome; the suite holds it now. A
    // regression that renders by wiping the shadow root would replace every
    // node on an update and fail the identity check.
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const out = await cdp.evaluate(
        `(async () => {
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              if (customElements.get('hh-scan-and-confirm') || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const host = document.createElement('hh-scan-and-confirm');
          document.body.append(host);
          await host.updateComplete;
          const before = host.shadowRoot.querySelector('button[data-action]');
          host.requestUpdate();
          await host.updateComplete;
          const after = host.shadowRoot.querySelector('button[data-action]');
          return {
            hadControl: before !== null,
            sameNode: before === after,
            stillConnected: before !== null && before.isConnected,
          };
        })()`,
      );
      assert.equal(out.hadControl, true, 'the ready kit must render a live control to hold on to');
      assert.equal(
        out.sameNode,
        true,
        'a same-state update must keep the node it rendered: the control must be the same element, not a fresh copy',
      );
      assert.equal(out.stillConnected, true, 'the kept node must still sit in the shadow tree');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('a host listener survives a state change; a listener on content inside the kit does not', async () => {
    // The README's rule for consumers, held by the leg: a state change
    // replaces a kit's content nodes, so a listener that outside code
    // attached to a node inside a kit is lost across one, and a listener on
    // the host element survives.
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const out = await cdp.evaluate(
        `(async () => {
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              if (customElements.get('hh-scan-and-confirm') || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const host = document.createElement('hh-scan-and-confirm');
          document.body.append(host);
          await host.updateComplete;
          const received = [];
          host.addEventListener('click', (event) => {
            const control = event.composedPath().find((n) => n instanceof Element && n.hasAttribute('data-action'));
            received.push(control ? control.getAttribute('data-action') : null);
          });
          const first = host.shadowRoot.querySelector('button[data-action]');
          let innerCalls = 0;
          first.addEventListener('click', () => { innerCalls++; });
          const action = first.getAttribute('data-action');
          // One state change swaps the body; the design's own rule.
          host.state = 'error';
          await host.updateComplete;
          const firstGone = !first.isConnected;
          host.state = 'ready';
          await host.updateComplete;
          const second = host.shadowRoot.querySelector('button[data-action]');
          second.addEventListener('click', () => { innerCalls++; });
          // A second round trip, so the listener measured at the click is one
          // that sat on content across a full swap.
          host.state = 'error';
          await host.updateComplete;
          host.state = 'ready';
          await host.updateComplete;
          const third = host.shadowRoot.querySelector('button[data-action]');
          third.click();
          return {
            action,
            firstGone,
            replaced: third !== first && third !== second,
            received,
            innerCalls,
          };
        })()`,
      );
      assert.ok(out.action, 'the ready kit must render a live data-action control to click');
      assert.equal(
        out.firstGone,
        true,
        'a state change must replace the kit content nodes: the node the content listener sat on is gone',
      );
      assert.equal(out.replaced, true, 'each return to ready must be a fresh body, not the old nodes');
      assert.equal(out.received.length, 1, 'the host listener must fire exactly once for the click after two state changes');
      assert.equal(
        out.received[0],
        out.action,
        'the host listener must survive a state change and receive the action',
      );
      assert.equal(
        out.innerCalls,
        0,
        'a listener that outside code attached to a node inside the kit must be lost across a state change',
      );
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('the shared header without slotted theme content keeps a fixed right cluster: two buttons, no separator, no empty slot element', async () => {
    // The item 5 followup's WITHOUT half: a host that never slots theme
    // controls must see exactly the cluster the element shipped before the
    // slot existed. An always-rendered empty slot, or a separator with
    // nothing behind it, fails here.
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/header`);
      const out = await cdp.evaluate(
        `(async () => {
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              const ok = !!customElements.get('hos-app-header') &&
                [...document.querySelectorAll('hos-app-header')].every((el) => el.shadowRoot !== null);
              if (ok || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const shape = (el) => {
            const cluster = el.shadowRoot.querySelector('.controls');
            if (cluster === null) return null;
            return [...cluster.children].map((n) => {
              const tag = n.tagName.toLowerCase();
              const action = n.getAttribute('data-action');
              const name = n.getAttribute('name');
              return tag + (action ? '[data-action=' + action + ']' : '') + (name ? '[' + name + ']' : '');
            });
          };
          return {
            signed: shape(document.getElementById('bare-signed')),
            unsigned: shape(document.getElementById('bare-unsigned')),
          };
        })()`,
      );
      assert.deepEqual(
        out.signed,
        ['button[data-action=switch-role]', 'button[data-action=sign-out]'],
        'the bare signed-in cluster is exactly Switch role then Sign out',
      );
      assert.deepEqual(out.unsigned, [], 'the bare unsigned cluster renders nothing');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('slotted theme controls render inside the right cluster behind a thin separator, and stay live', async () => {
    // The WITH half: the host's controls land inside the element's right
    // cluster, after Switch role and Sign out, with a thin separator between
    // the buttons and them; unsigned, the controls still render (the front
    // door's theme switcher is an unsigned control) with no separator and no
    // buttons; and a click on a slotted control reaches the host's listener
    // off composedPath, the delegation pattern every adopting app rides.
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/header`);
      const out = await cdp.evaluate(
        `(async () => {
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              const ok = !!customElements.get('hos-app-header') &&
                [...document.querySelectorAll('hos-app-header')].every((el) => el.shadowRoot !== null);
              if (ok || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          const shape = (el) => {
            const cluster = el.shadowRoot.querySelector('.controls');
            if (cluster === null) return null;
            return [...cluster.children].map((n) => {
              const tag = n.tagName.toLowerCase();
              const action = n.getAttribute('data-action');
              const name = n.getAttribute('name');
              const cls = n.className || '';
              return tag + (action ? '[data-action=' + action + ']' : '') + (name ? '[' + name + ']' : '') + (cls ? '.' + cls : '');
            });
          };
          const signed = document.getElementById('slotted-signed');
          const unsigned = document.getElementById('slotted-unsigned');
          const slot = signed.shadowRoot.querySelector('.controls slot[name="theme"]');
          const assigned = slot ? slot.assignedElements() : [];
          const themeButton = assigned.length > 0 ? assigned[0].querySelector('button') : null;
          const signOut = signed.shadowRoot.querySelector('button[data-action="sign-out"]');
          const sep = signed.shadowRoot.querySelector('.controls .theme-sep');
          const rect = (el) => {
            const r = el.getBoundingClientRect();
            return { left: r.left, right: r.right, top: r.top, bottom: r.bottom, cx: (r.left + r.right) / 2, cy: (r.top + r.bottom) / 2 };
          };
          const headerBox = rect(signed);
          const buttonBox = themeButton ? rect(themeButton) : null;
          const signOutBox = signOut ? rect(signOut) : null;
          const sepBox = sep ? rect(sep) : null;
          const unsignedSlot = unsigned.shadowRoot.querySelector('.controls slot[name="theme"]');
          const unsignedButton = unsignedSlot && unsignedSlot.assignedElements().length > 0
            ? unsignedSlot.assignedElements()[0].querySelector('button')
            : null;
          // The delegation pattern: one listener on the host, the control read
          // off the composed path, exactly as the README tells adopters.
          const received = [];
          signed.addEventListener('click', (event) => {
            const control = event.composedPath().find((n) => n instanceof Element && n.hasAttribute('data-action'));
            received.push(control ? control.getAttribute('data-action') + ':' + control.getAttribute('data-choice') : null);
          });
          if (themeButton) themeButton.click();
          return {
            signedShape: shape(signed),
            unsignedShape: shape(unsigned),
            assignedCount: assigned.length,
            themeButtonVisible: themeButton !== null && themeButton.isConnected,
            headerBox,
            buttonBox,
            signOutBox,
            sepBox,
            unsignedButtonVisible: unsignedButton !== null && unsignedButton.isConnected,
            received,
          };
        })()`,
      );
      assert.deepEqual(
        out.signedShape,
        [
          'button[data-action=switch-role].btn-secondary',
          'button[data-action=sign-out].btn-secondary',
          'span.theme-sep',
          'slot[theme]',
        ],
        'the slotted signed-in cluster reads Switch role, Sign out, a thin separator, then the theme slot',
      );
      assert.deepEqual(
        out.unsignedShape,
        ['slot[theme]'],
        'unsigned with slotted controls renders the slot alone: no buttons, no separator with nothing to separate',
      );
      assert.equal(out.assignedCount, 1, 'the theme slot has the host control assigned');
      assert.ok(out.themeButtonVisible, 'the slotted theme control is live in the element');
      assert.ok(out.unsignedButtonVisible, 'the unsigned header still shows the slotted theme control');
      assert.ok(out.sepBox !== null, 'the separator rendered');
      assert.ok(out.sepBox.right - out.sepBox.left <= 2, `the separator is a hairline (${out.sepBox.right - out.sepBox.left}px)`);
      assert.ok(out.sepBox.bottom - out.sepBox.top >= 10, 'the separator stretches with the cluster');
      assert.ok(
        out.signOutBox !== null && out.signOutBox.right <= out.sepBox.left + 0.5,
        'the separator sits after Sign out',
      );
      assert.ok(
        out.buttonBox !== null && out.sepBox.right <= out.buttonBox.left + 0.5,
        'the slotted control sits after the separator',
      );
      assert.ok(
        out.buttonBox !== null && out.signOutBox !== null && Math.abs(out.buttonBox.cy - out.signOutBox.cy) < 8,
        'the slotted control rides the same band as Sign out, inside the right cluster',
      );
      assert.ok(
        out.buttonBox !== null &&
          out.buttonBox.left >= out.headerBox.left && out.buttonBox.right <= out.headerBox.right + 0.5 &&
          out.buttonBox.top >= out.headerBox.top - 0.5 && out.buttonBox.bottom <= out.headerBox.bottom + 0.5,
        'the slotted control paints inside the header bar',
      );
      assert.deepEqual(out.received, ['theme-cycle:auto'], 'a click on the slotted control reaches the host listener off composedPath');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('every state mark paints: each path is an SVG element with a box, not an HTML unknown element', async () => {
    // icon() once built its paths with the html tag inside the svg, so the
    // browser made them HTMLUnknownElements in the XHTML namespace and no mark
    // painted. Source gates cannot see a namespace; the live DOM can.
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/icons`);
      const marks = await cdp.evaluate(
        `(async () => {
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => (window.__marksReady || Date.now() - t0 > 5000 ? done() : setTimeout(poll, 25));
            poll();
          });
          return [...document.querySelectorAll('[data-mark]')].map((cell) => ({
            mark: cell.dataset.mark,
            svgNs: cell.querySelector('svg') ? cell.querySelector('svg').namespaceURI : null,
            paths: [...cell.querySelectorAll('svg > *')].map((p) => {
              const b = p.getBBox ? p.getBBox() : { width: 0, height: 0 };
              return { tag: p.localName, ns: p.namespaceURI, ctor: p.constructor.name, width: b.width, height: b.height };
            }),
          }));
        })()`,
      );
      const SVG_NS = 'http://www.w3.org/2000/svg';
      assert.deepEqual(marks.map((m) => m.mark), Object.keys(STATE_ICONS), 'every mark rendered');
      for (const m of marks) {
        assert.equal(m.svgNs, SVG_NS, `${m.mark}: the svg root is in the SVG namespace`);
        assert.equal(m.paths.length, STATE_ICONS[m.mark].paths.length, `${m.mark}: one path per geometry string`);
        for (const [i, p] of m.paths.entries()) {
          assert.equal(p.ns, SVG_NS, `${m.mark} path ${i}: namespaceURI is ${p.ns}, ${p.ctor}, not the SVG namespace`);
          assert.ok(p.width > 0 || p.height > 0, `${m.mark} path ${i}: the bounding box is empty (${p.width} x ${p.height})`);
        }
      }
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('at a phone width every mark keeps its square: a long word beside it does not squeeze it', async () => {
    // Once the marks paint, a flex row with no flex: none on the mark lets the
    // word beside it shrink the mark to a sliver (13 by 18 at 390).
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.setViewport(390, 900);
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const squeezed = await cdp.evaluate(
        `(async () => {
          await new Promise((done) => {
            const t0 = Date.now();
            const poll = () => {
              const els = [...document.querySelectorAll('main > *')];
              const ok = ${JSON.stringify(TAGS)}.every((tag) => !!customElements.get(tag)) &&
                els.length > 0 && els.every((el) => el.shadowRoot !== null);
              if (ok || Date.now() - t0 > 5000) done();
              else setTimeout(poll, 25);
            };
            poll();
          });
          await new Promise((r) => setTimeout(r, 200));
          const bad = [];
          let seen = 0;
          for (const el of document.querySelectorAll('main > *')) {
            for (const i of el.shadowRoot.querySelectorAll('svg.icon')) {
              seen += 1;
              const r = i.getBoundingClientRect();
              if (Math.abs(r.width - r.height) > 0.5 || r.width < 12) bad.push(el.dataset.cell + ' ' + r.width + 'x' + r.height);
            }
          }
          return { seen, bad };
        })()`,
      );
      assert.ok(squeezed.seen > 0, 'no marks were found to measure');
      assert.deepEqual(squeezed.bad, [], 'these marks are not square at 390');
    } finally {
      await cdp.close();
      await close();
    }
  });

  // ── THE BOTTOM SHEET, in the browser ──────────────────────────────────────
  // The brief (T2-C13-S2-SHEET-M3) demands a live check that the sheet opens,
  // takes focus, traps Escape, accepts the scrim, and that the actions slot is
  // visible and not covered at the two widths the kits paint at: phone
  // (390 x 844, the iPhone Pro width) and tablet portrait (834 x 1194, the
  // iPad Pro 11 portrait). The element page mounts the trigger and a slotted
  // actions button; this test exercises the whole path the consumer rides.

  async function openSheetAndSettle(cdp, port) {
    await cdp.navigate(`http://127.0.0.1:${port}/sheet`);
    return await cdp.evaluate(
      `(async () => {
        await new Promise((done) => {
          const t0 = Date.now();
          const poll = () => (window.__sheetReady && customElements.get('hh-sheet') && document.getElementById('sheet').shadowRoot ? done() : (Date.now() - t0 > 5000 ? done() : setTimeout(poll, 25)));
          poll();
        });
        await new Promise((r) => setTimeout(r, 120));
        const sheet = document.getElementById('sheet');
        const trigger = document.getElementById('trigger');
        trigger.focus();
        trigger.click();
        await new Promise((r) => setTimeout(r, 120));
        const tag = sheet.tagName.toLowerCase();
        const root = sheet.shadowRoot;
        const scrim = root.querySelector('[data-action="close-scrim"]');
        const dialog = root.querySelector('[role="dialog"]');
        const actionSlot = root.querySelector('slot[name="actions"]');
        // The slot itself doesn't have a box; the assigned node (the slotted
        // button) does. The action lives where the slot assigns its content.
        const actionAssigned = actionSlot ? actionSlot.assignedElements({ flatten: true })[0] : null;
        const actionEl = actionAssigned || document.getElementById('sheet-action');
        // The labelledby id resolves in the shadow root (the title element
        // lives there, not in the light DOM).
        const labelledBy = dialog ? dialog.getAttribute('aria-labelledby') : null;
        const labelledByEl = labelledBy ? root.getElementById(labelledBy) : null;
        const box = (el) => el ? (() => { const r = el.getBoundingClientRect(); return { top: r.top, bottom: r.bottom, left: r.left, right: r.right, width: r.width, height: r.height }; })() : null;
        return {
          tag,
          hasShadow: root !== null,
          hasScrim: scrim !== null,
          hasDialog: dialog !== null,
          ariaModal: dialog ? dialog.getAttribute('aria-modal') : null,
          labelledBy,
          labelledByPointsAtElement: labelledByEl !== null,
          hasActionSlot: actionSlot !== null,
          actionElTag: actionEl ? actionEl.tagName.toLowerCase() : null,
          openAttr: sheet.hasAttribute('open'),
          opened: sheet.open === true,
          actionBox: box(actionEl),
          sheetBox: box(dialog),
          activeIsInSheet: sheet.contains(document.activeElement) || document.activeElement === sheet,
          triggerId: trigger.id,
        };
      })()`,
    );
  }

  test('hh-sheet opens, takes the dialog role, and moves focus into the sheet', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      const out = await openSheetAndSettle(cdp, port);
      assert.equal(out.tag, 'hh-sheet', 'the host is an hh-sheet');
      assert.equal(out.opened, true, 'the sheet is open after the trigger');
      assert.equal(out.openAttr, true, 'the open attribute reflects');
      assert.equal(out.hasScrim, true, 'a scrim element with data-action="close-scrim" is in the shadow root');
      assert.equal(out.hasDialog, true, 'a dialog with role="dialog" is in the shadow root');
      assert.equal(out.ariaModal, 'true', 'the dialog is aria-modal="true"');
      assert.ok(out.labelledBy, 'a labelledby attribute is present');
      assert.equal(out.labelledByPointsAtElement, true, 'the labelledby id resolves to an element');
      assert.equal(out.hasActionSlot, true, 'the actions slot is in the shadow root');
      assert.ok(out.activeIsInSheet, 'focus is inside the sheet after open');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('hh-sheet closes on Escape and returns focus to the trigger that opened it', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await openSheetAndSettle(cdp, port);
      const out = await cdp.evaluate(
        `(async () => {
          const sheet = document.getElementById('sheet');
          const trigger = document.getElementById('trigger');
          const events = [];
          sheet.addEventListener('hh-sheet-close', (e) => events.push({ reason: e.detail.reason, bubbles: e.bubbles, composed: e.composed }));
          // Escape from a focusable element inside the sheet; the keydown
          // bubbles up and the element's own listener handles it.
          const action = document.getElementById('sheet-action');
          action.focus();
          await new Promise((r) => setTimeout(r, 40));
          action.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, composed: true }));
          await new Promise((r) => setTimeout(r, 80));
          return {
            opened: sheet.open === true,
            hasOpenAttr: sheet.hasAttribute('open'),
            activeId: document.activeElement ? document.activeElement.id : null,
            events,
          };
        })()`,
      );
      assert.equal(out.opened, false, 'Escape closes the sheet');
      assert.equal(out.hasOpenAttr, false, 'the open attribute is removed on close');
      assert.equal(out.activeId, 'trigger', `focus returns to the trigger (was ${out.activeId})`);
      assert.equal(out.events.length, 1, `Escape fires the close event exactly once (got ${out.events.length})`);
      assert.equal(out.events[0].reason, 'escape', 'the reason is "escape"');
      assert.equal(out.events[0].bubbles, true, 'the event bubbles');
      assert.equal(out.events[0].composed, true, 'the event is composed');
    } finally {
      await cdp.close();
      await close();
    }
  });

  test('hh-sheet closes on a scrim tap and fires the event with reason "scrim"', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await openSheetAndSettle(cdp, port);
      const out = await cdp.evaluate(
        `(async () => {
          const sheet = document.getElementById('sheet');
          const trigger = document.getElementById('trigger');
          trigger.focus();
          const events = [];
          sheet.addEventListener('hh-sheet-close', (e) => events.push({ reason: e.detail.reason }));
          const scrim = sheet.shadowRoot.querySelector('[data-action="close-scrim"]');
          scrim.dispatchEvent(new MouseEvent('click', { bubbles: true, composed: true }));
          await new Promise((r) => setTimeout(r, 80));
          return { opened: sheet.open === true, events };
        })()`,
      );
      assert.equal(out.opened, false, 'a scrim tap closes the sheet');
      assert.equal(out.events.length, 1, `a scrim tap fires the event exactly once (got ${out.events.length})`);
      assert.equal(out.events[0].reason, 'scrim', 'the reason is "scrim"');
    } finally {
      await cdp.close();
      await close();
    }
  });

  for (const viewport of [
    { name: 'phone 390 x 844', width: 390, height: 844 },
    { name: 'tablet 834 x 1194', width: 834, height: 1194 },
  ]) {
    test(`hh-sheet action slot is visible and not covered at ${viewport.name}`, async () => {
      const { port, close } = await startServer();
      const cdp = await launchBrowser(binary);
      try {
        await cdp.setViewport(viewport.width, viewport.height);
        const out = await openSheetAndSettle(cdp, port);
        assert.equal(out.opened, true, `the sheet opens at ${viewport.name}`);
        const action = out.actionBox;
        const sheet = out.sheetBox;
        assert.ok(action && sheet, `the slot is positioned at ${viewport.name} (action=${!!action}, sheet=${!!sheet})`);
        // The action lives INSIDE the dialog's painted box: top is below the
        // dialog's top, bottom is above the dialog's bottom. It cannot poke
        // out beyond the sheet (a slot "outside" would mean the consumer's
        // primary action is hidden behind the dimmed page).
        assert.ok(
          action.top >= sheet.top - 0.5,
          `action top (${action.top.toFixed(1)}) must be inside the sheet top (${sheet.top.toFixed(1)}) at ${viewport.name}`,
        );
        assert.ok(
          action.bottom <= sheet.bottom + 0.5,
          `action bottom (${action.bottom.toFixed(1)}) must be inside the sheet bottom (${sheet.bottom.toFixed(1)}) at ${viewport.name}`,
        );
        // The action is laid out: width follows the dialog's cross-axis
        // stretch, height follows the button's own min-height (the test
        // mounts an unstyled button so the height rides whatever the sheet
        // declares via ::slotted(); width: 100% lands either way because
        // the flex container stretches). A non-zero box is the assertion:
        // a zero-dimensioned element is not visible.
        assert.ok(action.width > 0, `the action has width (got ${action.width}) at ${viewport.name}`);
        assert.ok(action.height > 0, `the action has height (got ${action.height}) at ${viewport.name}`);
        // The dialog sits inside the viewport: it is not pushed off the bottom
        // edge (the brief's "not covered" is the visible-bits the user can hit).
        assert.ok(
          sheet.bottom <= viewport.height + 0.5,
          `the sheet bottom (${sheet.bottom.toFixed(1)}) fits inside the viewport (${viewport.height}) at ${viewport.name}`,
        );
      } finally {
        await cdp.close();
        await close();
      }
    });
  }
}
