// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-searchdrop.test.mjs; adapted for HH.

// _pos-searchdrop.test.mjs: the counter search DROPDOWN and the customer-aware price picture.
//
// TWO CLIENT ASKS MEET IN THIS FILE.
//
// "the name of the game is getting people in and out really quickly" (Ryan Bostock): the
// grid-first lookup became about five rows under the field, Enter takes the top one, and
// browsing moved behind its own disclosure as the exception path it always was.
//
// "one of the biggest things that people always complain about is, how much am I saving?"
// (Ryan Rood): every row shows retail, this account's price, the saving between them, and any
// bulk break stacked on top. Every one of those cents is the SERVER's; the tests below pin what
// may be CLAIMED, never how it is computed, because computing it here would itself be the defect.
//
// node --import./src/_ts-js-loader.mjs --test src/_pos-searchdrop.test.mjs (run from app/)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  DROP_ROWS,
  MIN_TERM,
  dropView,
  shouldQuery,
  rowStep,
  pricePicture,
  basisWord,
} from './_pos-searchdrop.ts';

const dropSrc = readFileSync(new URL('./pos-search-drop.ts', import.meta.url), 'utf8');
const terminalSrc = readFileSync(new URL('./pos-terminal.ts', import.meta.url), 'utf8');
const scanSrc = readFileSync(new URL('./pos-scan-box.ts', import.meta.url), 'utf8');

const hit = (n) => ({ product_id: `p${n}`, sku: `SKU${n}`, name: `Product ${n}`, base_uom: 'EA', has_image: false, match_reason: 'NAME' });
const many = (n) => Array.from({ length: n }, (_, i) => hit(i + 1));

// ── about five rows, and the rest is a count ───────────────────────────────────────────────────

test('drop: the client asked for "about five": five rows, and the overflow is a NUMBER not more rows', () => {
  assert.equal(DROP_ROWS, 5);
  const v = dropView(many(12));
  assert.equal(v.rows.length, 5);
  assert.equal(v.overflow, 7, 'seven more matches exist and are reported as a count');
  assert.equal(v.top.product_id, 'p1');
});

test('drop: fewer matches than the cap report NO overflow: never "+0 more"', () => {
  const v = dropView(many(3));
  assert.equal(v.rows.length, 3);
  assert.equal(v.overflow, 0);
});

test('drop: the top hit is the SERVER\'s first: the list never re-ranks under the Enter key', () => {
 // The server ranks EXACT → SKU_PREFIX → NAME_PREFIX → … → FUZZY. If the dropdown re-sorted, the
 // row a person is looking at and the row Enter takes could disagree: the one thing a
 // commit-on-Enter list may never do.
  const hits = [hit(9), hit(2), hit(5)];
  assert.equal(dropView(hits).top.product_id, 'p9');
  assert.deepEqual(dropView(hits).rows.map((h) => h.product_id), ['p9', 'p2', 'p5']);
});

test('drop: an absent, malformed or empty answer is an EMPTY view, never a crash and never a fake row', () => {
  for (const bad of [null, undefined, 'nope', 42, {}]) {
    const v = dropView(bad);
    assert.deepEqual(v.rows, []);
    assert.equal(v.top, null);
    assert.equal(v.overflow, 0);
  }
  assert.deepEqual(dropView([null, undefined, { sku: 'no id' }]).rows, [], 'rows without a product id are not rows');
});

test('drop: a term shorter than the server answers is not sent: a known-empty round trip delays the real one', () => {
  assert.equal(MIN_TERM, 2);
  assert.equal(shouldQuery(''), false);
  assert.equal(shouldQuery(' '), false);
  assert.equal(shouldQuery('a'), false);
  assert.equal(shouldQuery(' a '), false);
  assert.equal(shouldQuery('mu'), true);
  assert.equal(shouldQuery(' mulch '), true);
});

// ── the list is one tab stop, and it is not a trap ─────────────────────────────────────────────

test('drop: arrows walk the list, and ArrowUp at the top walks BACK OUT to the field', () => {
  assert.equal(rowStep(0, 'ArrowDown', 5), 1);
  assert.equal(rowStep(4, 'ArrowDown', 5), 4, 'no wrap: four downs lands at the bottom, not back at the top');
  assert.equal(rowStep(2, 'ArrowUp', 5), 1);
  assert.equal(rowStep(0, 'ArrowUp', 5), -1, 'the caret can always get back to the field it came from');
  assert.equal(rowStep(3, 'Home', 5), 0);
  assert.equal(rowStep(0, 'End', 5), 4);
  assert.equal(rowStep(0, 'Enter', 5), null, 'Enter is the row\'s own business, not navigation');
  assert.equal(rowStep(0, 'ArrowDown', 0), null, 'an empty list moves nothing');
});

test('drop: the rows are a listbox with ONE tab stop, and the exit is wired to the field', () => {
  assert.match(dropSrc, /role="listbox"/);
  assert.match(dropSrc, /role="option"/);
  assert.match(dropSrc, /tabindex=\$\{i === this\.roveIdx \? '0' : '-1'\}/, 'one stop; arrows move inside');
  assert.match(dropSrc, /new CustomEvent\('pos-drop-exit'/, 'ArrowUp out of row 0 tells the host');
  assert.match(terminalSrc, /@pos-drop-exit=\$\{\(\) => this\.focusScanBox\(\)\}/, 'and the host puts the caret back');
  assert.match(scanSrc, /ArrowDown[^]{0,200}pos-term-enter-list/, 'ArrowDown out of the field walks in');
  assert.ok(!/e\.key === 'ArrowUp'/.test(scanSrc), 'ArrowUp inside a text field stays the browser\'s caret gesture');
});

// ── Enter takes the top hit; a SCAN never consults the list ────────────────────────────────────

test('drop: a TYPED Enter that misses the exact resolve commits the top row', () => {
  assert.match(scanSrc, /enterMissOutcome\(gaps, code\.length\) === 'fall-through-to-search'/);
  assert.match(scanSrc, /new CustomEvent\('pos-term-commit-top'/,
    'the box hands the moment to the host: it does not own the list, so it may not pick from it');
  assert.match(terminalSrc, /@pos-term-commit-top=\$\{\(\) => this\.commitTopHit\(\)\}/);
  assert.match(terminalSrc, /if \(!drop \|\| !top \|\| !drop\.commitTop\?\.\(\)\) return;/,
    'no hit means no commit: Enter never invents a product');
  assert.match(scanSrc, /commitDone\(msg: string\): void/, 'and the field ends exactly as it does after a scan');
});

test('drop: a SCANNER BURST bypasses the list entirely: it resolves exactly and clears the term', () => {
 // The safety property, not an optimisation: a list a 200-char-per-second burst could pick from is
 // a list that could pick the WRONG product. The burst path resolves against the server and the
 // successful resolve clears the term, which closes the list on the way past.
  assert.match(scanSrc, /const hit = await resolveScan\(code, this\.branchId\)/);
  assert.match(scanSrc, /this\.msg = `No product for \$\{code\}/, 'a burst MISS is loud, never a fall-through');
  assert.match(scanSrc, /this\.code = '';\s*\n\s*if \(this\.unified\) this\.emitTerm\(''\); \/\/ the commit ends the search/);
  assert.ok(!/topHit|commitTop|dropView/.test(scanSrc), 'the scan box cannot even see the result list');
});

// ── the price picture: what may be CLAIMED ────────────────────────────────────────────────────

const quote = (over = {}) => ({ product_id: 'p1', price_uom: 'EA', retail_cents: 1000, customer_cents: 1000, ...over });

test('price: a walk-in paying retail sees ONE price: no struck-through copy of the same number', () => {
  const p = pricePicture(quote());
  assert.equal(p.unpriceable, false);
  assert.equal(p.customerCents, 1000);
  assert.equal(p.showRetail, false, 'retail equals their price; showing both twice says it twice');
  assert.equal(p.showSaving, false, 'and there is no "you save CA$0.00" state');
});

test('price: an account paying less shows retail struck AND the saving, with the server\'s basis', () => {
  const p = pricePicture(quote({ customer_cents: 850, saving_cents: 150, saving_bps: 1500, basis: 'CONTRACT' }));
  assert.equal(p.customerCents, 850);
  assert.equal(p.showRetail, true);
  assert.equal(p.retailCents, 1000);
  assert.equal(p.showSaving, true);
  assert.equal(p.savingCents, 150);
  assert.equal(p.savingBps, 1500);
  assert.equal(basisWord(p.basis), 'Contract');
});

test('price: the bulk break STACKS on top and is only shown while it is still an OFFER', () => {
 // Ryan Rood: customer type triggers a pricing role, "then it's going to show your savings against
 // retail. If it triggers a bulk pricing break it's going to add that to it as well."
  const p = pricePicture(
    quote({
      customer_cents: 850,
      saving_cents: 150,
      basis: 'ACCOUNT',
      next_break_qty: 20,
      next_break_uom: 'TON',
      next_break_unit_cents: 800,
      next_break_save_cents: 50,
    }),
  );
  assert.equal(p.showSaving, true, 'the account saving stands…');
  assert.equal(p.showBreak, true, '…and the unreached break sits on top of it');
  assert.equal(p.breakQty, 20);
  assert.equal(p.breakUnitCents, 800);
 // A break with no quantity, or one that saves nothing, is not an offer and must not render.
  assert.equal(pricePicture(quote({ next_break_unit_cents: 800, next_break_save_cents: 50 })).showBreak, false);
  assert.equal(pricePicture(quote({ next_break_qty: 20, next_break_unit_cents: 800 })).showBreak, false);
});

test('price: an unpriceable product renders NO price: never CA$0.00, never a zero somebody sells at', () => {
  for (const q of [null, undefined, quote({ unpriceable: true }), quote({ retail_cents: 0, customer_cents: 0 })]) {
    assert.equal(pricePicture(q).unpriceable, true);
  }
  assert.match(dropSrc, /if \(p\.unpriceable\) \{[^]{0,400}prices on add/,
    'the row says what WILL happen instead of showing a number');
});

test('price: a garbage or absent field degrades to zero-and-hidden, never to NaN on the screen', () => {
  const p = pricePicture({ product_id: 'p', retail_cents: 1000, customer_cents: 900, saving_cents: 'lots', saving_bps: null });
  assert.equal(p.savingCents, 0);
  assert.equal(p.savingBps, 0);
  assert.equal(p.showSaving, false);
});

test('price: the basis chip speaks counter language: an unknown enum says NOTHING rather than shouting the wire word', () => {
  assert.equal(basisWord('CONTRACT'), 'Contract');
  assert.equal(basisWord('ACCOUNT'), 'Account');
  assert.equal(basisWord('BULK'), 'Bulk');
  assert.equal(basisWord('PROMO'), 'Promo');
  assert.equal(basisWord('SOME_NEW_RULE'), '', 'a surface never wears the wire\'s vocabulary');
  assert.equal(basisWord(''), '');
});

// ── the money is the server's, and the wiring says so ──────────────────────────────────────────

test('drop: every cent on a row came from the pricing engine: nothing here subtracts anything', () => {
  assert.match(dropSrc, /await counterPrices\(/, 'the row prices come from POST /pos/counter-prices');
  assert.ok(!/retail_cents\s*-\s*/.test(dropSrc), 'no client-side subtraction');
  assert.ok(!/customer_cents\s*[-*/]/.test(dropSrc), 'and no client-side arithmetic of any kind');
  assert.match(dropSrc, /changed\.has\('customerId'\)/, 'a customer arriving RE-prices what is on screen');
});

test('drop: a pricing outage leaves the rows tappable and priceless: never blank, never an error', () => {
  assert.match(dropSrc, /catch \{\s*\n\s*if \(seq === this\.seq\) this\.quotes = \{\};/,
    'the prices go, the products stay');
  assert.match(dropSrc, /role="status" data-testid="drop-empty"/, 'and an empty search is an outcome, not a fault');
  assert.ok(!/role="alert"/.test(dropSrc), 'nothing on this surface is a fault surface');
});

test('drop: browsing is the EXCEPTION path: one disclosure, and the results flip lives with the grid', () => {
  assert.match(terminalSrc, /<details class="browse" data-testid="pos-browse">/);
  const browse = terminalSrc.slice(terminalSrc.indexOf('<details class="browse"'));
  assert.match(browse.slice(0, 2000), /data-testid="lookup-view-cards"/, 'the flip governs the grid, so it sits with it');
  assert.match(browse.slice(0, 2600), /<hos-pos-product-grid/, 'and the full catalogue keeps every capability it had');
  assert.match(terminalSrc, /\.externalTerm=\$\{this\.lookupTerm\}/, 'still ONE field feeding both surfaces');
});
