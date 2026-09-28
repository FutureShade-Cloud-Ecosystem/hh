// THE REAL-BROWSER LEG. The source brief's acceptance says the kits run in a
// browser, and until this file existed the custom element path had never
// executed anywhere. This leg registers the five custom elements in headless
// Chromium over the DevTools protocol (zero npm dependencies: Node 22's global
// WebSocket, test/_lib/cdp.mjs), then renders every kit in every state in the
// live DOM and asserts the page contents per kit and state, and holds the
// harness page to a clean layout at all three widths (1440, 1920, and the 400
// the review flagged).
//
// The page's sources are served through Node's own TypeScript stripping
// (module.stripTypeScriptTypes): the same job src/_ts-js-loader.mjs does for
// the Node process, done for a browser page. No build step, no new dependency.
//
// The skip is LOUD on purpose: a silent skip would read as a pass, so when no
// global WebSocket, no stripping API (Node 22.13+), or no browser binary
// exists, the reason is printed and a visible test registers in its place.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { existsSync, readFileSync, statSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { findBrowserBinary, launchBrowser } from './_lib/cdp.mjs';
import { KIT_STATES, STATE_META } from '../src/state.js';
import { KITS, buildHarnessPage } from '../src/harness-page.js';

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
<head><meta charset="utf-8"><title>hh design system browser leg</title></head>
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

function startServer() {
  const server = createServer((req, res) => {
    const url = new URL(req.url ?? '/', 'http://localhost');
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

// One statement test under bun (and under any runtime missing a prerequisite),
// so the skip is visible in the count rather than silent. Under Node 22 the
// real tests register in its place. The registration shape matches
// test/harness.test.mjs: bun interleaves async tests across files
// (oven-sh/bun#5090), so the async legs register on node only.
const reason = skipReason();

if (reason) {
  test('the headless-browser leg has a loud, named reason for not running here', () => {
    console.warn(`SKIPPING the headless-browser leg: ${reason}`);
    assert.ok(reason.length > 0, 'the skip reason must be stated, never silent');
  });
} else {
  test('the five custom elements register and render every kit in every state in a real page', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/`);
      const snapshot = await cdp.evaluate(
        `(() => {
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

  test('the harness page lays out at 1440, 1920 and 400 without sideways scroll or escaping cells', async () => {
    const { port, close } = await startServer();
    const cdp = await launchBrowser(binary);
    try {
      await cdp.navigate(`http://127.0.0.1:${port}/harness`);
      for (const width of [1440, 1920, 400]) {
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
}
