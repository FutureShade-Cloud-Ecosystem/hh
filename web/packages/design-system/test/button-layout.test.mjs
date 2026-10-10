// The shared button rule must not re-centre buttons that align their own text. Main's plain `button` is a
// block level box with the author's `text-align` honoured (the counter till's role cards, the returns approver
// cards and the Coming roles list set `text-align: left` on a bare button); the flex centring the Quote Desk's
// icon buttons need belongs to `.btn` alone. This leg renders the real base styles in headless Chromium and
// measures where the label starts inside each button: a left aligned plain button keeps its label at the left
// padding, a plain button's default stays centred by the UA, and `.btn` centres label and icon.
//
// Same runner rules as browser.test.mjs: Node 22, a headless Chromium, loud skip, FAIL under CI. The skip is
// registered as a separate statement in each runtime, the way browser.test.mjs does: bun's `node:test` does
// not honor the `skip` option, so the body would run under bun and `launchBrowser(null)` would throw. The
// branch below registers the right statement for each case (loud under CI; per-runtime skip under Node; the
// real leg where the binary exists).

import { test } from 'node:test';
import assert from 'node:assert/strict';

import { findBrowserBinary, launchBrowser } from './_lib/cdp.mjs';
import { baseStyles } from '../src/primitives.js';

const underBun = typeof globalThis.Bun !== 'undefined';
const binary = !underBun && typeof globalThis.WebSocket === 'function' ? findBrowserBinary() : null;
const reason = underBun
  ? 'the authoritative runner for this leg is Node 22 (bun runs the drift gate and skips the real-browser leg)'
  : !binary
    ? 'no headless Chromium binary exists; point HH_DS_BROWSER at one to run this leg'
    : null;
const ci = process.env.CI === 'true';

const page = (css) => `<!doctype html><meta charset="utf-8"><style>${css}
  .wide { width: 300px; } .lefty { text-align: left; }</style>
  <button id="plain" class="wide">Label</button>
  <button id="lefty" class="wide lefty">Label</button>
  <button id="btn" class="btn wide">Label</button>`;

if (reason && !underBun && ci) {
  // missing browser FAILS the job and names what is missing, it never passes
  // with a warning.
  test('the button layout leg refuses to skip when CI is set', () => {
    assert.fail(reason);
  });
} else if (reason && underBun) {
  // The by-design bun statement, registered as before, in CI or out.
  test('the button layout leg has a loud, named reason for not running here', () => {
    console.warn(`SKIPPING the button layout leg: ${reason}`);
    assert.ok(reason.length > 0, 'the skip reason must be stated, never silent');
  });
} else if (reason) {
  // Node without CI: the leg is skipped, and a skip is not a pass.
  console.warn(`SKIPPING the button layout leg: ${reason}`);
  test(
    'the button layout leg has a loud, named reason for not running here',
    { skip: reason },
    () => assert.fail('the button layout leg must not run where it declared a named skip'),
  );
} else {
  test('a plain button keeps its own text alignment; only .btn is centred by the flex rule', async () => {
    const cdp = await launchBrowser(binary);
    try {
      const doc = page(baseStyles.cssText);
      await cdp.navigate(`data:text/html;base64,${Buffer.from(doc).toString('base64')}`);
      const out = await cdp.evaluate(`(() => {
        const labelOffset = (id) => {
          const b = document.getElementById(id);
          const r = document.createRange();
          r.selectNodeContents(b.firstChild);
          return Math.round(r.getBoundingClientRect().left - b.getBoundingClientRect().left);
        };
        const display = (id) => getComputedStyle(document.getElementById(id)).display;
        return { plain: labelOffset('plain'), lefty: labelOffset('lefty'), btn: labelOffset('btn'),
          plainDisplay: display('plain'), leftyDisplay: display('lefty'), btnDisplay: display('btn') };
      })()`);
      assert.ok(out.lefty < 40, `a left aligned plain button starts its label at the left padding, got ${out.lefty}px in`);
      assert.notEqual(out.leftyDisplay, 'inline-flex', 'a plain button is not a flex container');
      assert.notEqual(out.plainDisplay, 'inline-flex', 'a plain button is not a flex container');
      assert.ok(out.plain > 100, `a plain button's default is the UA centre, got ${out.plain}px in`);
      assert.equal(out.btnDisplay, 'inline-flex', '.btn is the flex row that draws an icon beside its label');
      assert.ok(out.btn > 100, `.btn centres its label, got ${out.btn}px in`);
    } finally {
      await cdp.close();
    }
  });
}
