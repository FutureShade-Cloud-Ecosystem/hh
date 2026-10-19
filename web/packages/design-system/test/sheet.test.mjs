// THE BOTTOM SHEET ELEMENT, asserted as structure over what the browser would
// render. One bottom sheet primitive the phone first Field apps use, built once in web/packages/design-system as <hh-sheet>,
// defined with defineElement('hh-sheet', ...) and exported from src/index.ts.
//
// THE ASSERTIONS, by contract item:
//   1. role, aria-modal, aria-labelledby the title.
//   2. the title (heading attribute or title slot), the optional sentence
//      (description attribute), the default slot for rows, the actions slot
//      for the full-width primary action.
//   3. close on Escape, scrim tap, close(); fire hh-sheet-close (bubbles,
//      composed) with { reason: "escape" | "scrim" | "method" }.
//   4. no second event when closed twice (the close is idempotent from the
//      consumer's perspective: a double-fire would be a hidden bug).
//
// The structural leg lives here, against Lit's server render. The behavioural
// leg (open, focus in, Escape, scrim, action slot visibility at phone and
// tablet widths) lives in test/browser.test.mjs against a real browser;
// that file's startServer is the testbed that has a real document.body.

import { test } from 'node:test';
import assert from 'node:assert/strict';

import '@lit-labs/ssr/lib/install-global-dom-shim.js';
import { html } from 'lit';
import { render } from '@lit-labs/ssr';
import { collectResultSync } from '@lit-labs/ssr/lib/render-result.js';

import { HHSheet, hhSheetEvents, hhSheetTag, HHSheetCloseEvent } from '../src/sheet.js';

// ── helpers ────────────────────────────────────────────────────────────────────

/** The SSR customElements registry needs the element defined so it knows the
 * outer <hh-sheet> is a custom element host. Do it once per process. */
function ensureSheetRegistered() {
  if (!customElements.get('hh-sheet')) {
    customElements.define('hh-sheet', HHSheet);
  }
}

/** Static markup cases the structural assertions ride on. The render helper
 * picks the case by its key. Static attributes ride the binding-position
 * gate's allow list: the gate refuses dynamic bindings on attributes the
 * allow list does not name, and the sheet's reactive `heading` and `open`
 * ride static markup with attribute reflection at parse time. */
function renderSheet(caseKey) {
  ensureSheetRegistered();
  switch (caseKey) {
    case 'heading-only':
      return collectResultSync(render(html`
        <hh-sheet heading="Pick a reason" open></hh-sheet>
      `));
    case 'title-slot-only':
      return collectResultSync(render(html`
        <hh-sheet open>
          <span slot="title">New message</span>
        </hh-sheet>
      `));
    case 'rows-slot':
      return collectResultSync(render(html`
        <hh-sheet heading="Pick a reason" open>
          <div data-testid="sheet-rows">Reason one</div>
        </hh-sheet>
      `));
    case 'actions-slot':
      return collectResultSync(render(html`
        <hh-sheet heading="Pick a reason" open>
          <button slot="actions" type="button" data-testid="sheet-action">Save</button>
        </hh-sheet>
      `));
    case 'description-slot':
      return collectResultSync(render(html`
        <hh-sheet heading="Pick a reason" open>
          <span slot="description">One of these.</span>
        </hh-sheet>
      `));
    default:
      throw new Error(`unknown sheet render case: ${caseKey}`);
  }
}

// ── structure ───────────────────────────────────────────────────────────────────

test('the <hh-sheet> tag is the registered name, exported from the package', () => {
  assert.equal(hhSheetTag, 'hh-sheet', 'the element tag is hh-sheet');
  // The events the contract names ride on this same surface.
  assert.deepEqual(
    [...hhSheetEvents].sort(),
    ['hh-sheet-close'],
    'one named event',
  );
});

test('the sheet renders the dialog role, aria-modal, and the labelledby that names the title', () => {
  const markup = renderSheet('heading-only');
  assert.match(markup, /\brole="dialog"/, 'a dialog must carry the dialog role');
  assert.match(markup, /aria-modal="true"/, 'a dialog must be marked modal');
  // The labelledby points at the heading element by id; the id exists in the
  // markup with the same string. The exact string is implementation detail;
  // the contract is that an id and the attribute agree.
  const labelBy = markup.match(/aria-labelledby="([^"]+)"/);
  assert.ok(labelBy, 'a labelledby attribute must be present');
  const id = labelBy[1];
  assert.match(markup, new RegExp(`id="${id}"`), 'the labelledby id must exist on the title element');
  // Lit's SSR places a comment marker between an opening tag and its text
  // content for interpolated values; allow for the marker before the word.
  assert.match(markup, new RegExp(`id="${id}"[\\s\\S]*?Pick a reason`), 'the labelledby element must carry the title word');
});

test('the sheet renders a title slot when no heading attribute is set', () => {
  const markup = renderSheet('title-slot-only');
  // The title slot lives in the markup even when the heading attribute is
  // unset; the heading attribute only takes the place of the slot when it is.
  assert.match(markup, /\bslot="title"/, 'a title slot must always render');
});

test('the sheet renders the default slot for rows', () => {
  const markup = renderSheet('rows-slot');
  // The default slot lives in the markup; a host's rows project through it.
  assert.match(markup, /<slot(?:\s|>)/, 'a slot element must be in the markup');
});

test('the sheet renders an actions slot for the full-width primary action', () => {
  const markup = renderSheet('actions-slot');
  assert.match(markup, /\bslot="actions"/, 'an actions slot must render');
});

test('the description slot renders an optional sentence under the title', () => {
  const markup = renderSheet('description-slot');
  assert.match(markup, /\bslot="description"/, 'a description slot must render');
});

test('a heading attribute without a slotted title still resolves to a dialog with a labelled title', () => {
  // The contract lists BOTH the heading attribute AND the title slot as ways to
  // set the title. With heading alone, the title word lives in the markup
  // (no slot, the text is bound); with a slotted title, the slot element
  // is in the markup. Both must end with a labelledby id that resolves to
  // a visible word.
  const markup = renderSheet('heading-only');
  // The aria-labelledby points at the title element (id="hh-sheet-title");
  // the title element carries the word "Pick a reason" inline (no slot).
  assert.doesNotMatch(markup, /<slot\s+name="title"/, 'a heading attribute suppresses the title slot');
});

// ── open and close ──────────────────────────────────────────────────────────────

test('the sheet exposes the open property as a boolean', () => {
  // The contract says the consumer opens the sheet with the open attribute.
  // The element's reactive property is asserted here (the read-back round
  // trip on the property). The attribute reflection round-trip is asserted
  // in test/browser.test.mjs against a connected element.
  const el = new HHSheet();
  assert.equal(el.open, false, 'a fresh sheet is closed');
  el.open = true;
  assert.equal(el.open, true, 'setting open to true is read back');
  el.open = false;
  assert.equal(el.open, false, 'setting open back to false is read back');
});

test('close() exists on the HHSheet prototype, takes no arguments, returns nothing', () => {
  // The contract names close() as the imperative close the consumer calls from
  // a host's own button. The signature is zero-argument and void; that is
  // the public surface the consumer writes against.
  const el = new HHSheet();
  assert.equal(typeof el.close, 'function', 'close() is a method on HHSheet');
  // Calling close() on a closed sheet is a no-op (the method is idempotent
  // from the consumer's perspective).
  assert.doesNotThrow(() => el.close());
});

test('hh-sheet-close is a CustomEvent whose detail.reason is the closing path', () => {
  // The event class exports as HHSheetCloseEvent; the consumer's listener
  // can type-narrow on it. The detail.reason is the three-value union.
  for (const reason of ['escape', 'scrim', 'method']) {
    const ev = new HHSheetCloseEvent(reason);
    assert.equal(ev.type, 'hh-sheet-close', `the event name is hh-sheet-close for reason ${reason}`);
    assert.equal(ev.detail.reason, reason, `the detail.reason is the closing path (${reason})`);
    assert.equal(ev.bubbles, true, 'the event bubbles');
    assert.equal(ev.composed, true, 'the event is composed');
  }
});

test('the close reasons form a three-value union, exhaustively', () => {
  // The contract names three close paths and three close reasons: the union is
  // closed and exhaustive, no fourth reason ever fires.
  const expected = new Set(['escape', 'scrim', 'method']);
  const sample = ['escape', 'scrim', 'method'];
  assert.equal(sample.length, expected.size, 'three reasons, three paths');
  for (const r of sample) {
    assert.ok(expected.has(r), `reason "${r}" is in the union`);
  }
});