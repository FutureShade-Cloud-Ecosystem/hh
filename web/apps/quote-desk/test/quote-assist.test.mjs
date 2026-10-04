// The assist panel's pure tables, unit tested on the Node runner before the
// panel is written against a screen. Ported from the ERP's
// _quote-assist.test.mjs (hardscapeos_dibbits master 87d7941b), adapted for HH:
// the decisions live in _quote-assist.ts, DOM-free; the roles and the wiring
// of the element are pinned by a source scan of assist-panel.ts, and the
// screens are driven in a browser by assist.pw.ts.
//
// The load-bearing ones: proposals arrive UNTICKED, only ticked and completable
// rows are sent, suggestions are role="status" and never role="alert", and the
// AI helper latches disabled on the server's own deterministic answer.
//
//   node --import ../../packages/api-client/src/_ts-js-loader.mjs --test test/quote-assist.test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

import {
  AI_OFF_HINT,
  CONFIDENCE_HIGH,
  CONFIDENCE_LOW,
  CONFIDENCE_MEDIUM,
  addPayloads,
  addResultText,
  blockedReason,
  canConfirm,
  confidenceLabel,
  confirmButtonLabel,
  editedProposal,
  extractStatusText,
  gapEmptyText,
  gapStatusText,
  aiHelperLatched,
  selectionOf,
  sourceText,
  toggleRef,
  visibleFindings,
  withAlternate,
} from "../src/_quote-assist.js";

const here = dirname(fileURLToPath(import.meta.url));
const raw = (p) => readFileSync(join(here, p), "utf8");
// Comments stripped first: the panel's own header discusses role="alert" at
// length, and a gate that read its own prose would pass vacuously.
const code = (p) =>
  raw(p)
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/^\s*\/\/.*$/gm, "")
    .replace(/<!--[\s\S]*?-->/g, "");

// ── fixtures: the real extractor's own output shape ────────────────────────────────────────────
//
// Copied from what the Go handler actually produces for the walkthrough's paragraph (see
// TestFixture_WriteWalkthroughBodies), so these tests and the screenshots describe the same rows.

const complete = (over = {}) => ({
  ref: 'p1',
  source_text: 'Need 480 sq ft of Techo-Bloc Blu 60 for the back patio',
  product_id: 'prod-blu60',
  sku: 'DBS-TB-BLU60',
  name: 'Techo-Bloc Blu 60 Slab',
  base_uom: 'PC',
  sell_uom: '',
  qty_entered: '480',
  uom_entered: 'SF',
  unit_note: '',
  confidence: CONFIDENCE_MEDIUM,
  match_reason: 'NAME_PREFIX',
  why: ['matched the product name “Techo-Bloc Blu 60 Slab”', 'read the quantity 480 from your text'],
  needs_qty: false,
  needs_uom: false,
  alternates: [],
  ...over,
});

const needsQty = (over = {}) =>
  complete({
    ref: 'p2',
    source_text: 'some limestone screenings',
    product_id: 'prod-screen',
    sku: 'DBS-AGG-SCREEN',
    name: 'Limestone Screenings',
    qty_entered: '',
    uom_entered: 'T',
    needs_qty: true,
    confidence: CONFIDENCE_LOW,
    ...over,
  });

const needsUOM = (over = {}) =>
  complete({
    ref: 'p3',
    source_text: '40 linear feet of Banas coping',
    product_id: 'prod-coping',
    sku: 'DBS-BANAS-COPING',
    name: 'Banas Sandstone Coping',
    qty_entered: '40',
    uom_entered: '',
    needs_uom: true,
    unit_note: 'the catalogue has no LF factor for this product',
    confidence: CONFIDENCE_LOW,
    ...over,
  });

const extraction = (proposals, unmatched = []) => ({
  proposals,
  unmatched,
  source: 'deterministic',
  ai_note: '',
});

const finding = (over = {}) => ({
  rule_code: 'SURFACE_NEEDS_JOINTING',
  title: 'Jointing sand for the paved area',
  advice: 'A paved area is quoted with no jointing sand. Joints left unfilled wash out in the first heavy rain.',
  triggered_by: [{ line_no: 1, name: 'Techo-Bloc Blu 60 Slab' }],
  missing: ['JOINTING'],
  suggest_query: 'polymeric sand',
  ...over,
});

const gaps = (findings, over = {}) => ({
  quote_id: 'q-1',
  findings,
  lines_checked: 2,
  rules_evaluated: 7,
  ...over,
});

// ── 1 · THE PROPOSAL-ONLY INVARIANT ────────────────────────────────────────────────────────────

test('a fresh extraction has NOTHING selected, the review list never pre-ticks itself', () => {
  const ex = extraction([complete(), needsQty(), needsUOM()]);
  // The whole invariant, in one assertion. A list that arrives pre-ticked is not a review list; it is
  // an auto-add with a cancel button, and the first rep who scrolls past it has quoted a customer
  // something nobody read.
  assert.deepEqual(selectionOf(ex, []), []);
  assert.deepEqual(addPayloads(ex, []), []);
  assert.equal(confirmButtonLabel(ex, []), 'Add the ticked lines');
});

test('only rows the rep TICKED are sent', () => {
  const ex = extraction([complete(), complete({ ref: 'p9', product_id: 'prod-gator', sku: 'DBS-GATOR-SAND', name: 'Alliance Gator Polymeric Sand', qty_entered: '6', uom_entered: 'BAG' })]);
  const payloads = addPayloads(ex, ['p9']);
  assert.equal(payloads.length, 1, 'one tick, one payload');
  assert.equal(payloads[0].product_id, 'prod-gator');
  assert.equal(payloads[0].ref, 'p9');
});

test('a ticked row that is not completable is still not sent', () => {
  // A rep can tick a row and then clear the quantity. The button count and the payload must agree
  // with each other, and both must agree with what the server will accept.
  const ex = extraction([complete(), needsQty(), needsUOM()]);
  const payloads = addPayloads(ex, ['p1', 'p2', 'p3']);
  assert.equal(payloads.length, 1, `expected only the complete row, got ${JSON.stringify(payloads)}`);
  assert.equal(payloads[0].ref, 'p1');
  assert.equal(confirmButtonLabel(ex, ['p1', 'p2', 'p3']), 'Add 1 line to the quote');
});

test('a stale tick from a previous paste cannot resurrect a row', () => {
  const ex = extraction([complete()]);
  assert.deepEqual(selectionOf(ex, ['p1', 'p2', 'p77']).map((p) => p.ref), ['p1']);
  assert.deepEqual(addPayloads(ex, ['p77']), []);
});

test('the payload description is the CATALOGUE name, not the rep’s phrase', () => {
  // A quote is a document a customer reads. "Need 480 sq ft of Blu 60 for the back patio" is not a
  // line description.
  const payloads = addPayloads(extraction([complete()]), ['p1']);
  assert.equal(payloads[0].description, 'Techo-Bloc Blu 60 Slab');
  assert.ok(!payloads[0].description.includes('back patio'));
});

test('null and empty extractions produce no payloads and never throw', () => {
  for (const ex of [null, undefined, {}, { proposals: null }, { proposals: [] }]) {
    assert.deepEqual(addPayloads(ex, ['p1']), []);
    assert.deepEqual(selectionOf(ex, ['p1']), []);
  }
});

test('toggleRef adds, removes, and returns a NEW array each time', () => {
  const a = toggleRef([], 'p1');
  assert.deepEqual(a, ['p1']);
  const b = toggleRef(a, 'p2');
  assert.deepEqual(b, ['p1', 'p2']);
  assert.notEqual(a, b, 'a mutated array is why a checkbox appears not to respond to the first click');
  assert.deepEqual(toggleRef(b, 'p1'), ['p2']);
});

// ── 2 · what is confirmable, and why not ───────────────────────────────────────────────────────

test('canConfirm needs a product, a quantity and a unit', () => {
  assert.equal(canConfirm(complete()), true);
  assert.equal(canConfirm(needsQty()), false);
  assert.equal(canConfirm(needsUOM()), false);
  assert.equal(canConfirm(complete({ product_id: '' })), false, 'no product, nothing to add');
  assert.equal(
    canConfirm(complete({ qty_entered: '   ' })),
    false,
    'whitespace is not a quantity',
  );
});

test('blockedReason names WHICH of the two things to type', () => {
  assert.equal(blockedReason(complete()), '', 'a confirmable row explains nothing');
  assert.match(blockedReason(needsQty()), /quantity/);
  assert.match(blockedReason(needsUOM()), /unit/);
  const both = needsQty({ uom_entered: '', needs_uom: true });
  assert.match(blockedReason(both), /quantity and pick a unit/);
  assert.match(blockedReason(complete({ product_id: '' })), /nothing to add/);
});

test('confidence labels are plain words, and an unknown band is not a blank chip', () => {
  assert.equal(confidenceLabel(CONFIDENCE_HIGH), 'high confidence');
  assert.equal(confidenceLabel(CONFIDENCE_MEDIUM), 'worth a look');
  assert.equal(confidenceLabel(CONFIDENCE_LOW), 'check this one');
  assert.equal(confidenceLabel('WAT'), 'unrated');
  assert.equal(confidenceLabel(undefined), 'unrated');
});

// ── 3 · correcting a wrong pick ────────────────────────────────────────────────────────────────

test('swapping to an alternate keeps the quantity and re-asks the unit', () => {
  const withAlts = complete({
    alternates: [
      { product_id: 'prod-step', sku: 'DBS-TB-BOREALIS', name: 'Techo-Bloc Borealis Step', match_reason: 'NAME', base_uom: 'PC' },
    ],
  });
  const [swapped] = withAlternate([withAlts], 'p1', 'prod-step');
  // The 480 was about the area of the patio, not about the product.
  assert.equal(swapped.qty_entered, '480');
  assert.equal(swapped.product_id, 'prod-step');
  assert.equal(swapped.sku, 'DBS-TB-BOREALIS');
  // The unit belonged to the OLD product and this module cannot know whether the catalogue keeps a
  // factor for the new one, that is a server question, so the row honestly asks.
  assert.equal(swapped.uom_entered, '');
  assert.equal(swapped.needs_uom, true);
  assert.equal(canConfirm(swapped), false, 'a swapped row is not silently confirmable');
  assert.equal(swapped.confidence, CONFIDENCE_LOW);
  assert.match(swapped.unit_note, /confirm the unit/i);
});

test('swapping on a stale ref or an unknown product is a no-op, not a half-change', () => {
  const list = [complete({ alternates: [] })];
  assert.deepEqual(withAlternate(list, 'p1', 'nope')[0], list[0]);
  assert.deepEqual(withAlternate(list, 'p404', 'prod-step')[0], list[0]);
});

// ── 4 · what a screen reader is told ───────────────────────────────────────────────────────────

test('the extraction status names BOTH counts, including zero unmatched', () => {
  const s = extractStatusText({
    loading: false,
    error: '',
    extraction: extraction([complete(), needsQty()], [{ source_text: 'a bobcat for the day', reason: 'nothing matched' }]),
  });
  assert.match(s, /2 suggested lines/);
  // The whole point of the second list is that a screen-reader user cannot see it. "2 proposals" with
  // no mention of the one that failed is exactly the silent drop this feature exists not to have.
  assert.match(s, /1 that I couldn't match/);
  assert.match(s, /Nothing has been added/);

  const clean = extractStatusText({ loading: false, error: '', extraction: extraction([complete()]) });
  assert.match(clean, /nothing left unmatched/);
});

test('the status says what state it is in before, during and after', () => {
  assert.match(extractStatusText({ loading: false, error: '', extraction: null }), /Paste a job description/);
  assert.match(extractStatusText({ loading: true, error: '', extraction: null }), /Reading/);
  assert.equal(extractStatusText({ loading: true, error: 'Could not reach the server.', extraction: null }), 'Could not reach the server.');
  assert.match(extractStatusText({ loading: false, error: '', extraction: extraction([]) }), /Nothing to suggest/);
});

test('the confirm button never overstates what it will add', () => {
  const ex = extraction([complete(), needsQty()]);
  assert.equal(confirmButtonLabel(ex, ['p1']), 'Add 1 line to the quote');
  assert.equal(
    confirmButtonLabel(ex, ['p1', 'p2']),
    'Add 1 line to the quote',
    'ticking an incompletable row must not inflate the count',
  );
  const two = extraction([complete(), complete({ ref: 'pX' })]);
  assert.equal(confirmButtonLabel(two, ['p1', 'pX']), 'Add 2 lines to the quote');
});

test('a partial add names the lines that failed', () => {
  assert.equal(addResultText(3, []), 'Added 3 lines.');
  assert.equal(addResultText(1, []), 'Added 1 line.');
  const partial = addResultText(3, [{ ref: 'p4', message: 'Banas Sandstone Coping: refused (409).' }]);
  // A single boolean would lose this case, and a rep who re-ran the batch would duplicate three lines.
  assert.match(partial, /Added 3 lines/);
  assert.match(partial, /Banas Sandstone Coping/);
  assert.match(addResultText(0, [{ ref: 'p1', message: 'x' }]), /No lines were added/);
});

test('the AI switch explains the cost before it is flipped', () => {
  assert.match(AI_OFF_HINT, /no AI and no cost/);
  assert.match(AI_OFF_HINT, /monthly allowance/);
});

// ── 5 · SUGGESTIONS ARE DISMISSIBLE, AND SILENT WHEN THERE IS NOTHING TO SAY ───────────

test('every finding renders until it is dismissed, and dismissal is BY RULE CODE', () => {
  const g = gaps([finding(), finding({ rule_code: 'SURFACE_NEEDS_BASE', title: 'Granular base for the paved area' })]);
  assert.equal(visibleFindings(g, []).length, 2);
  const after = visibleFindings(g, ['SURFACE_NEEDS_JOINTING']);
  assert.equal(after.length, 1);
  assert.equal(after[0].rule_code, 'SURFACE_NEEDS_BASE');
  // By code and not by index: the panel re-reads after every line the rep adds, and an index-based
  // dismissal would resurrect what they just dismissed and silence something else.
  const reordered = gaps([finding({ rule_code: 'SURFACE_NEEDS_BASE' }), finding()]);
  assert.deepEqual(
    visibleFindings(reordered, ['SURFACE_NEEDS_JOINTING']).map((f) => f.rule_code),
    ['SURFACE_NEEDS_BASE'],
  );
});

test('the live region is SILENT when there is nothing to suggest', () => {
  // Not "0 suggestions", not "the quote looks complete". A reassurance announced on every quote is a
  // thing people stop listening to, and the day it matters they will not listen either.
  assert.equal(gapStatusText(gaps([]), []), '');
  assert.equal(gapStatusText(null, []), '');
  assert.equal(gapStatusText(gaps([finding()]), ['SURFACE_NEEDS_JOINTING']), '', 'dismissing the last one goes quiet');
});

test('the live region names the suggestions when there ARE some', () => {
  const s = gapStatusText(gaps([finding()]), []);
  assert.match(s, /1 suggestion for this quote/);
  assert.match(s, /Jointing sand for the paved area/);
  assert.match(gapStatusText(gaps([finding(), finding({ rule_code: 'X' })]), []), /2 suggestions/);
});

test('the quiet empty line distinguishes “nothing missing” from “rules switched off”', () => {
  // Two silences the backend is careful to separate, and they mean completely different things: one is
  // a clean quote, the other is a panel an administrator turned off.
  assert.match(gapEmptyText(gaps([], { rules_evaluated: 0 })), /switched off/);
  assert.match(gapEmptyText(gaps([], { lines_checked: 0 })), /Nothing to check yet/);
  assert.match(gapEmptyText(gaps([])), /nothing obviously missing/);
  assert.equal(gapEmptyText(null), '');
});

test('a finding carries the rule’s own words and the lines that triggered it', () => {
  const [f] = visibleFindings(gaps([finding()]), []);
  // The advice is the ROW's, verbatim, the admin listing would otherwise be a lie about what reps see.
  assert.match(f.advice, /wash out in the first heavy rain/);
  assert.deepEqual(f.triggered_by, [{ line_no: 1, name: 'Techo-Bloc Blu 60 Slab' }]);
  assert.equal(f.suggest_query, 'polymeric sand');
});

// ── 7 · the AI helper latch, source and the edit recompute (HH additions) ──────────────────────

test("the AI helper latches disabled only on a deterministic answer to an AI request, with a note", () => {
  const note = "AI-assisted reading is not switched on here, so I used the plain parser.";
  assert.equal(aiHelperLatched(true, { ...extraction([complete()]), ai_note: note }), true);
  assert.equal(aiHelperLatched(false, { ...extraction([complete()]), ai_note: note }), false, "not asked, nothing to latch");
  assert.equal(aiHelperLatched(true, extraction([complete()])), false, "no note, no latch");
  assert.equal(aiHelperLatched(true, { ...extraction([complete()]), source: "ai-assisted", ai_note: note }), false, "an ai answer never latches");
  assert.equal(aiHelperLatched(true, null), false);
});

test("the source reads as the server said it, and nothing is invented for a missing extraction", () => {
  assert.equal(sourceText(extraction([])), "Read by: deterministic");
  assert.equal(sourceText({ ...extraction([]), source: "ai-assisted" }), "Read by: ai-assisted");
  assert.equal(sourceText(null), "");
});

test("editing a proposal recomputes both needs flags from the fields", () => {
  const blocked = needsQty();
  const typed = editedProposal(blocked, { qty_entered: "12" });
  assert.equal(typed.needs_qty, false);
  assert.equal(canConfirm(typed), true);
  const cleared = editedProposal(typed, { qty_entered: "  " });
  assert.equal(cleared.needs_qty, true);
  assert.equal(editedProposal(complete(), { uom_entered: "" }).needs_uom, true);
});

// ── 8 · the element, scanned (roles, wiring, no auto-tick) ───────────────────────────────────────

const PANEL = "../src/assist-panel.ts";

test("suggestions are a polite status region and no finding or gap block carries role=alert", () => {
  const src = code(PANEL);
  assert.ok(src.length > 4000, "assist-panel.ts is missing or empty, this gate measures nothing");
  assert.match(src, /customElement\("qd-quote-assist"\)/);
  assert.match(src, /<div role="status" aria-live="polite">/);
  const findingBlock = src.slice(src.indexOf("renderFinding"), src.indexOf("renderGaps"));
  assert.ok(findingBlock.length > 200, "renderFinding was not found, this gate has drifted");
  assert.doesNotMatch(findingBlock, /role="alert"/);
  const gapsBlock = src.slice(src.indexOf("private renderGaps"), src.indexOf("  override render()"));
  assert.ok(gapsBlock.length > 200, "renderGaps was not found, this gate has drifted");
  assert.doesNotMatch(gapsBlock, /role="alert"/);
});

test("the one role=alert on the element is the request failure", () => {
  const src = code(PANEL);
  const alerts = src.match(/role="alert"/g) ?? [];
  assert.equal(alerts.length, 1, `expected exactly one role="alert", found ${alerts.length}`);
  assert.match(src, /class="err" role="alert">\$\{this\.readError\}/);
});

test("every finding has a Dismiss control named for its rule, and a dismissal can be undone", () => {
  const src = code(PANEL);
  assert.match(src, /data-action="dismiss"/);
  assert.match(src, /aria-label=\$\{`Dismiss the suggestion: \$\{f\.title\}`\}/);
  assert.match(src, /Show them again/);
});

test("the proposal list is real checkboxes and buttons, labelled, with no div click handlers", () => {
  const src = code(PANEL);
  assert.match(src, /<input\s+type="checkbox"/);
  assert.doesNotMatch(src, /<div[^>]*@click=/);
  assert.doesNotMatch(src, /\s@(click|change|input)=/, "no event bindings in the template, host delegation only");
  assert.match(src, /<label class="inline"><span class="sr">Quantity for/);
  assert.match(src, /<label class="inline"><span class="sr">Unit for/);
  assert.match(src, /<label class="sr" for="jobtext">/);
});

test("the panel never auto-ticks: the confirm set is emptied on a read and never derived from proposals", () => {
  const src = code(PANEL);
  assert.match(src, /this\.selected = \[\];/);
  const assignments = src.match(/this\.selected\s*=\s*[^;]+;/g) ?? [];
  assert.ok(assignments.length > 0, "no assignment to the confirm set at all, this gate has drifted");
  for (const a of assignments) assert.doesNotMatch(a, /proposals/, `the confirm set must never be derived from the proposal list: ${a}`);
  assert.doesNotMatch(src, /\.checked=\$\{true\}/);
});

test("confirming goes through the client's audited addLine, one line at a time, then announces the change", () => {
  const src = code(PANEL);
  assert.match(src, /client\.addLine\(quoteId, p\.product_id, p\.qty_entered, p\.uom_entered\)/);
  assert.match(src, /for \(const p of payloads\)/);
  assert.match(src, /addPayloads\(/);
  assert.match(src, /this\.announceChanged\(\)/);
});

test("the extract call sends use_ai from the helper and the helper starts unticked and available", () => {
  const src = code(PANEL);
  assert.match(src, /assistExtract\(this\.text, this\.branchId, asked\)/);
  assert.match(src, /const asked = this\.useAi && !this\.aiLatched;/);
  assert.match(src, /@state\(\) private useAi = false;/);
  assert.match(src, /@state\(\) private aiLatched = false;/);
});

test("a gap's Find puts its query in the paste box and reads it, and never adds a line by itself", () => {
  const src = code(PANEL);
  const block = src.slice(src.indexOf("private suggestAdd"), src.indexOf("private renderProposal"));
  assert.ok(block.length > 100, "suggestAdd was not found, this gate has drifted");
  assert.match(block, /this\.text = f\.suggest_query/);
  assert.match(block, /this\.readDescription\(\)/);
  assert.doesNotMatch(block, /addLine/);
});
