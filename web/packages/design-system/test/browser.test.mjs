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
          // connectedCallback: give the elements their first update before
          // the snapshot reads them.
          await new Promise((r) => setTimeout(r, 150));
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
          await new Promise((r) => setTimeout(r, 150));
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
          await new Promise((r) => setTimeout(r, 150));
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
}
