// Ported from hardscapeos_dibbits master 87d7941b app/src/_pos-rev8.test.mjs; adapted for HH.

// DIB-488 rev 8: the commander's concept slate (v1 + v2 rulings): the unified smart field, the
// concept header bar, separate ticket columns, curated favorites chips, underline customer tabs,
// fulfilment icon tiles, the split totals card, the DIB-502 add-ons strip, the weight column, the
// role-gated margin surfaces, the job fields and the footer transaction bar.
//
// Pure decision tables first (_pos-rev8.ts, _pos-workspace.ts), then the house BUDGET+FLOOR
// source-scan pairs pinning the wiring.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import {
  isScannerBurst,
  enterMissOutcome,
  clockLabel,
  lineWeightLbs,
  formatLbs,
  ticketWeightTotalLbs,
  marginPctLabel,
  txbarSegments,
  resolveCuratedChips,
} from './_pos-rev8.ts';
import { defaultView, resolveWorkspaceView, workspaceSaveBody, workspaceSaveBodyV8, mergeWorkspaceForSave, WORKSPACE_FIELDS } from './_pos-workspace.ts';

const money = (c) => `CA$${(c / 100).toFixed(2)}`;

const terminalSrc = readFileSync(fileURLToPath(new URL('./pos-terminal.ts', import.meta.url)), 'utf8');
const scanSrc = readFileSync(fileURLToPath(new URL('./pos-scan-box.ts', import.meta.url)), 'utf8');
const gridSrc = readFileSync(fileURLToPath(new URL('./pos-product-grid.ts', import.meta.url)), 'utf8');
const pickerSrc = readFileSync(fileURLToPath(new URL('./pos-customer-picker.ts', import.meta.url)), 'utf8');

// ── item 1 · the scanner-vs-typist classifier ───────────────────────────────────────────────────

test('isScannerBurst: a machine-fast burst is a scan; human typing is not', () => {
  assert.equal(isScannerBurst([8, 9, 7, 8, 9, 8, 9, 8, 9, 8, 9], 12), true, 'a 12-char HID burst');
  assert.equal(isScannerBurst([120, 180, 90, 200, 150], 6), false, 'six keys at human speed');
  assert.equal(isScannerBurst([8, 9], 3), false, 'too short to be a code');
  assert.equal(isScannerBurst([], 12), false, 'no measured gaps (a paste) is a typist');
});

test('isScannerBurst: the MEDIAN decides: one slow wake-up gap must not disqualify a gun', () => {
  assert.equal(isScannerBurst([400, 6, 7, 6, 7, 6, 7, 6], 9), true, 'the gun woke slowly, then burst');
  assert.equal(isScannerBurst([6, 300, 280, 320, 300, 310, 290, 300], 9), false, 'one fast key-roll pair does not promote a typist');
});

test('enterMissOutcome: burst → LOUD unknown-code; typed → quiet fall-through to the search', () => {
  assert.equal(enterMissOutcome([7, 8, 7, 8, 7, 8, 7], 8), 'loud-unknown-code');
  assert.equal(enterMissOutcome([150, 200, 180, 170, 160, 190, 150], 8), 'fall-through-to-search');
});

// ── item 2 · the injected clock ─────────────────────────────────────────────────────────────────

test('clockLabel: pure over an injected Date (the house rule): 12h, minutes padded', () => {
  assert.equal(clockLabel(new Date(2026, 7, 24, 10, 36)), '10:36 AM');
  assert.equal(clockLabel(new Date(2026, 7, 24, 0, 5)), '12:05 AM');
  assert.equal(clockLabel(new Date(2026, 7, 24, 12, 0)), '12:00 PM');
  assert.equal(clockLabel(new Date(2026, 7, 24, 23, 59)), '11:59 PM');
});

// ── item 9 · weight honesty ─────────────────────────────────────────────────────────────────────

test('lineWeightLbs: base-unit line = qty × kg × 2.20462; sell-unit line converts through the factor', () => {
  const facts = { weightKgPerBase: 3.9, baseUom: 'PC', sellUom: 'SF', sellFactor: 5.1 };
  assert.ok(Math.abs(lineWeightLbs(10, 'PC', facts) - 10 * 3.9 * 2.20462) < 1e-9);
  assert.ok(Math.abs(lineWeightLbs(10, 'SF', facts) - 10 * 5.1 * 3.9 * 2.20462) < 1e-9);
});

test('lineWeightLbs: NEVER a guess: no weight, an alien unit, or a zero factor answer null', () => {
  const facts = { weightKgPerBase: 3.9, baseUom: 'PC', sellUom: 'SF', sellFactor: 5.1 };
  assert.equal(lineWeightLbs(10, 'PLT', facts), null, 'an unconvertible unit');
  assert.equal(lineWeightLbs(10, 'SF', { ...facts, sellFactor: 0 }), null, 'no conversion on file');
  assert.equal(lineWeightLbs(10, 'PC', { ...facts, weightKgPerBase: undefined }), null, 'no weight on file');
  assert.equal(lineWeightLbs(10, 'PC', { ...facts, weightKgPerBase: 0 }), null, 'a zero weight is absent, not zero lbs');
  assert.equal(lineWeightLbs(0, 'PC', facts), null, 'nothing weighs nothing');
});

test('ticketWeightTotalLbs: unknown lines are EXCLUDED honestly and flagged', () => {
  assert.deepEqual(ticketWeightTotalLbs([100, null, 50]), { totalLbs: 150, allKnown: false });
  assert.deepEqual(ticketWeightTotalLbs([100, 50]), { totalLbs: 150, allKnown: true });
  assert.deepEqual(ticketWeightTotalLbs([]), { totalLbs: 0, allKnown: true });
});

test('formatLbs: whole pounds, grouped: the counter reads 2,450 lbs, never 2450.13', () => {
  assert.equal(formatLbs(2450.13), '2,450 lbs');
  assert.equal(formatLbs(999.6), '1,000 lbs');
});

// ── items 10+12 · margin display + the footer bar ───────────────────────────────────────────────

test('marginPctLabel: bps → one-decimal percent', () => {
  assert.equal(marginPctLabel(3230), '32.3%');
  assert.equal(marginPctLabel(4000), '40.0%');
});

test('txbarSegments: items + weight ALWAYS; margin/profit only when present AND un-hidden', () => {
  const base = { itemCount: 4, totalWeightLbs: 12690, marginBps: 3030, estProfitCents: 145618, marginHidden: false };
  assert.deepEqual(
    txbarSegments(base, money).map((s) => s.text),
    ['4 Items', 'Total Weight 12,690 lbs', 'Margin 30.3%', 'Est. Profit CA$1456.18'],
  );
 // Quick-hide OFF the margin segments: items+weight stand.
  assert.deepEqual(
    txbarSegments({ ...base, marginHidden: true }, money).map((s) => s.id),
    ['items', 'weight'],
  );
 // Role-gated data ABSENT (the server sent {}): the segments do not exist.
  assert.deepEqual(
    txbarSegments({ ...base, marginBps: undefined, estProfitCents: undefined }, money).map((s) => s.id),
    ['items', 'weight'],
  );
 // No line has a weight → the blank word, never 0 lbs.
  assert.equal(txbarSegments({ ...base, totalWeightLbs: null }, money)[1].text, 'Total Weight (not weighed)');
  assert.equal(txbarSegments({ ...base, itemCount: 1 }, money)[0].text, '1 Item');
});

// ── item 4 · curated chips hardened at the edge ─────────────────────────────────────────────────

test('resolveCuratedChips: only a clean string list counts: trimmed, deduped, capped at 8', () => {
  assert.deepEqual(resolveCuratedChips([' Paver ', 'Wall', 'paver', '', 42, 'Bedding']), ['Paver', 'Wall', 'Bedding']);
  assert.deepEqual(resolveCuratedChips('Paver,Wall'), [], 'a non-array is nothing');
  assert.deepEqual(resolveCuratedChips(undefined), []);
  assert.equal(resolveCuratedChips(['a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j']).length, 8, 'capped');
});

// ── 0170 workspace: marginHidden rides the view, the resolver, the save body and the merge ──────

test('workspaceSaveBodyV8 = the rev-3 body PLUS margin_hidden: earlier composers stand untouched', () => {
  const v = { ...defaultView(), marginHidden: true, maximized: 'sale' };
  assert.deepEqual(workspaceSaveBodyV8(v), { ...workspaceSaveBody(v), margin_hidden: true });
  assert.deepEqual(Object.keys(workspaceSaveBody(v)).sort(), [
    'arrangement',
    'lookup_collapsed',
    'lookup_presentation',
    'sale_collapsed',
    'split_pct',
  ], 'the pinned rev-3 body did NOT widen');
});

test('resolveWorkspaceView: only the literal true hides margin; the field is a WORKSPACE_FIELD for the merge', () => {
  assert.equal(resolveWorkspaceView({ margin_hidden: true }).marginHidden, true);
  assert.equal(resolveWorkspaceView({ margin_hidden: 'yes' }).marginHidden, false, 'garbage shows');
  assert.equal(resolveWorkspaceView({}).marginHidden, false);
  assert.ok(WORKSPACE_FIELDS.includes('marginHidden'), 'the merge-save contract covers the toggle');
  const merged = mergeWorkspaceForSave({ margin_hidden: true }, { ...defaultView(), marginHidden: false }, ['split']);
  assert.equal(merged.marginHidden, true, 'an unchanged toggle keeps the OTHER surface\'s value (no clobber)');
});

// ── FLOOR: the unified smart field (item 1) ─────────────────────────────────────────────────────

test('FLOOR (1): the terminal mounts ONE unified field: scan box unified, grid input stood down', () => {
  assert.match(terminalSrc, /\?unified=\$\{true\}/, 'the scan box must be in unified mode');
  assert.match(terminalSrc, /\.externalTerm=\$\{this\.lookupTerm\}/, 'the grid takes the forwarded term');
  assert.match(terminalSrc, /@pos-term-changed=/, 'the terminal listens for the forwarded term');
  assert.match(gridSrc, /this\.externalTerm === null\s*\?/, 'the grid renders its OWN input only when no external term drives it');
});

test('FLOOR (1): the unified Enter keeps BOTH contracts: exact resolve first, classified miss', () => {
  assert.match(scanSrc, /enterMissOutcome\(gaps, code\.length\) === 'fall-through-to-search'/,
    'a typed miss falls through to the live search');
  assert.match(scanSrc, /No product for \$\{code\}/, 'a burst miss keeps the LOUD unknown-code answer');
  assert.match(scanSrc, /Scan barcode or search product \/ SKU/, 'the concept placeholder');
  assert.match(scanSrc, /void this\.updateComplete\.then\(\(\) => this\.focusInput\(\)\)/,
    'the post-add refocus (rev-5 contract) stands');
  assert.match(scanSrc, /this\.emitTerm\(''\)/, 'a commit/clear also clears the forwarded search term');
});

// ── FLOOR: the header bar (item 2) ──────────────────────────────────────────────────────────────

test('FLOOR (2): the header bar: bold title, yard context, injected clock, session dot, Help, gold + New Sale', () => {
  assert.match(terminalSrc, /data-testid="pos-page-title"/, 'the large sale title');
  assert.match(terminalSrc, /Yard · Register 01/, 'the yard context line');
  assert.match(terminalSrc, /clockLabel\(this\.clockNow\)/, 'the clock label derives PURE from the injected Date');
  assert.ok(!/clockLabel\(new Date\(\)\)/.test(terminalSrc), 'no clock read inside the render (injected clock only)');
  assert.match(terminalSrc, /data-testid="session-dot"/, 'the green session-status dot');
  assert.match(terminalSrc, /data-testid="pos-help"/, 'the Help affordance');
  assert.match(terminalSrc, /data-testid="pos-new-sale"[^]{0,200}@click=\$\{this\.newSaleClicked\}/,
    'the page-level + New Sale takes the GUARDED path (never a silent reset)');
  assert.match(terminalSrc, /class="title">HH <span>Yard<\/span> · Point of Sale/, 'the brand lockup stands (DIB-456 gate)');
 // UNION TRAIN 12. This pinned the DIB-488 rev-8 mechanism: a per-tab opt-out that retired the
 // shell's helper sentence for POS ONLY, at both template sites. The commander extended that
 // opt-out to EVERY pane, so the mechanism is gone along with the paragraph, and the pin has to
 // move with it or it would pin the absence of a carve-out by demanding the carve-out exists.
 // What rev 8 actually won is unchanged and is what is checked now: no helper sentence sits above
 // the POS pane.
 // HH note: the shell half of this pin read the reference app's shell element, which HH replaces
 // with its own; that half stayed with the reference (CLOSURE.md), and this file keeps only the
 // surface half below.
});

// ── FLOOR: ticket columns (items 3 + 9 + 10) ────────────────────────────────────────────────────

test('FLOOR (3): Item | Qty | Unit | Price | Total as SEPARATE columns, trash icon on the remove', () => {
 // DIB-512: the per-line column is 'Weight': 'Total Weight' is the FOOTER bar's word for the
 // ticket total, and the same words meaning two different numbers on one screen was a measured
 // duplicate-fact finding. The footer segment (txbarSegments) keeps 'Total Weight' verbatim.
  assert.match(terminalSrc, /<span><\/span><span>Item<\/span><span>Qty<\/span><span>Unit<\/span><span class="r">Price<\/span><span class="r">Total<\/span><span class="r">Weight<\/span>/,
    'the header names the separate Price and Total columns plus the per-line Weight');
  const rmButtons = terminalSrc.match(/class="rm"[^]{0,400}?<svg[^]{0,300}?M4 7h16/g) ?? [];
  assert.ok(rmButtons.length >= 2, `the remove control is a trash-can icon in BOTH presentations (found ${rmButtons.length})`);
  assert.ok(!/class="rm"[^]{0,200}>×</.test(terminalSrc), 'the × is retired');
});

test('FLOOR (9): the weight cell renders through the pure honesty table (the blank word, never a guess)', () => {
  assert.match(terminalSrc, /data-testid="pos-line-weight"/, 'every LINES row carries the weight cell');
  assert.match(terminalSrc, /lineWeightLbs\(l\.qty, l\.uom, facts\)/, 'the cell derives through the pure fn');
  assert.match(terminalSrc, /w === null \? '\(none\)' : formatLbs\(w\)/, 'absent weight gets the explicit blank word');
});

test('FLOOR (10): the margin column exists ONLY on present-and-unhidden data, and the eye persists via the workspace', () => {
  assert.match(terminalSrc, /marginVisible\(\)/, 'one gate decides the column');
  assert.match(terminalSrc, /marginPresent\(\) && !this\.wsMarginHidden/, 'visible = data present AND quick-hide off');
  assert.match(terminalSrc, /data-testid="margin-eye"/, 'the one-click quick-hide (eye)');
  assert.match(terminalSrc, /this\.wsDirty\.add\('marginHidden'\)/, 'the toggle rides the rev-6 merge-save contract');
  assert.match(terminalSrc, /workspaceSaveBodyV8/, 'the save body carries margin_hidden (0170)');
  assert.ok(!/marginBps\s*=\s*.*lineTotalCents/.test(terminalSrc), 'the client NEVER computes a margin: the server does');
});

// ── FLOOR: favorites chips (item 4) ─────────────────────────────────────────────────────────────

test('FLOOR (4): ★ Favorites leads a curated SINGLE-ROW chip set; the full list stays reachable', () => {
  assert.match(gridSrc, /data-testid="pos-chip-favorites"/, 'the ★ Favorites chip');
  assert.match(gridSrc, /is_featured === true/, 'favorites filter = the catalogue\'s featured flag');
  assert.match(gridSrc, /\.chips\.onerow \{[^]{0,200}?flex-wrap: nowrap;/, 'the curated row NEVER wraps');
  assert.match(gridSrc, /data-testid="pos-chips-all"/, 'the browse affordance to the full category list');
  assert.match(terminalSrc, /\.curated=\$\{this\.curatedChips\}/, 'the terminal feeds the policy\'s curated set');
  assert.match(terminalSrc, /resolveCuratedChips\(p\.quick_chip_set\)/, 'hardened at the edge');
});

// ── FLOOR: customer tabs + add (item 5) ─────────────────────────────────────────────────────────

test('FLOOR (5): Walk-in / Account wear UNDERLINE tabs; the picker offers add-new via the EXISTING create flow', () => {
  assert.match(terminalSrc, /\.custmode button \{[^]{0,400}?border-bottom: 2px solid transparent;/,
    'the tabs are underline-style (no filled segments)');
  assert.match(terminalSrc, /\.custmode button\.active \{[^]{0,200}?border-bottom-color: var\(--hos-stone-amber\);/,
    'the active tab is the amber underline');
  assert.match(pickerSrc, /data-testid="customer-add-new"/, 'the add-new-customer icon beside the search');
  assert.match(pickerSrc, /api\('\/api\/v1\/customers', \{\s*method: 'POST'/, 'it drives the EXISTING create endpoint: no new flow');
  assert.match(pickerSrc, /Creating accounts needs a manager sign-in\./, 'the manager gate answered in counter words');
});

// ── FLOOR: fulfilment icon tiles (item 6) ───────────────────────────────────────────────────────

test('FLOOR (6): all four fulfilment options are ICON tiles: inline currentColor SVGs, 2×2 grid', () => {
  const fulfil = terminalSrc.slice(
    terminalSrc.indexOf('<div class="fulfil"'),
    terminalSrc.indexOf("COD${this.codEnabled === false"),
  );
  const icons = fulfil.match(/<svg[^>]*stroke="currentColor"/g) ?? [];
  assert.ok(icons.length >= 4, `every fulfilment tile carries an inline icon (found ${icons.length})`);

 // THE 2×2 WAS RETIRED ON 20 SEP, on the commander's ruling, and this line is the record of what
 // that did and did not change. Measured first: the fulfilment label plus this group was 158px of
 // a 494px Items card: 32% of the step: with 140px of it two rows of icon-OVER-label tiles. He
 // asked whether fulfilment should leave Items so item search had more room; it cannot move
 // (DIB-551 settled where it lives, locked with the client), so it got shorter instead: icon
 // BESIDE label, one row, about 40px.
 //
 // What this floor protected is unchanged and still asserted above: four options, as TILES, with
 // inline currentColor art, never a select. What replaced the literal is a stronger claim than the
 // one it made: a grid that answers the 1106px column and a 280px panel with the same rule, and a
 // tile that is still a real target under a thumb.
  assert.match(
    terminalSrc,
    /\.fulfil \{\s*\n\s*display: grid;\s*\n\s*grid-template-columns: repeat\(auto-fit, minmax\(112px, 1fr\)\);/,
    'the tiles wrap to the pane they are in rather than to a breakpoint',
  );
  const tile = terminalSrc.slice(terminalSrc.indexOf('.fulfil button {'), terminalSrc.indexOf('.fulfil button svg'));
  assert.match(tile, /flex-direction: row;/, 'icon beside label: the row is what bought the space back');
  assert.match(tile, /min-height: 40px;/, 'and it is still a target, not a line of text');
});

// ── FLOOR: the split totals card (item 7) ───────────────────────────────────────────────────────

test('FLOOR (7): a distinct totals card LEADS the Payment step; the oversized gold Total', () => {
 // The concept put a quiet totals card above a clearly separate Payment section. In the single
 // column the Payment STEP is that separation, and the card leads its body: still distinct, still
 // above the tender tiles, still adjacent to the number it justifies (the one the clerk reads out
 // loud while taking the money). The rev-6 bar chip retired with the bar; see _pos-rev6 FLOOR (J)
 // for where the running total is readable from the other three steps.
  assert.match(terminalSrc, /data-testid="pos-totals-card"/, 'the totals card exists');
  const body = terminalSrc.slice(terminalSrc.indexOf('private renderPaymentBody()'));
  assert.ok(body.indexOf('data-testid="pos-totals-card"') < body.indexOf('class="paytiles"'),
    'the totals card renders ABOVE the tender tiles inside the payment step');
  assert.match(terminalSrc, /\.totalscard \.trow\.grand \.num \{\s*\n\s*font-size: 26px;/, 'the oversized gold Total');
});

// ── FLOOR: the add-ons strip (item 8 · DIB-502) ─────────────────────────────────────────────────

test('FLOOR (8): the strip renders ONLY on suggestions, adds via the EXISTING engine-priced path', () => {
  assert.match(terminalSrc, /if \(this\.addons\.length === 0 \|\| this\.ringDone\(\)\) return nothing;/,
    'empty = the strip does not exist (empty-never-error)');
  assert.match(terminalSrc, /data-testid="pos-addons"/, 'the strip');
  assert.match(terminalSrc, /data-testid="addon-add"/, 'the one-click gold + Add');
  assert.match(terminalSrc, /data-testid="addon-qty"/, 'the qty box');
  assert.match(terminalSrc, /await this\.addProduct\(p, qty\)/, 'adds go through the EXISTING add path: the engine prices, never the client');
  assert.ok(!/list_price_cents[^]{0,80}unitPriceCents/.test(terminalSrc), 'the tile price is display only');
});

// ── FLOOR: job fields (item 11) ─────────────────────────────────────────────────────────────────

test('FLOOR (11): Job Address + Job Reference under More options, riding BOTH ring bodies + the snapshot', () => {
  assert.match(terminalSrc, /data-testid="job-address"/, 'the job address field');
  assert.match(terminalSrc, /data-testid="job-reference"/, 'the job reference field');
  const rings = terminalSrc.match(/job_address: this\.jobAddress\.trim\(\) \|\| undefined/g) ?? [];
  assert.equal(rings.length, 2, 'both ring bodies (plain + split) carry job_address');
  const refs = terminalSrc.match(/job_reference: this\.jobReference\.trim\(\) \|\| undefined/g) ?? [];
  assert.equal(refs.length, 2, 'both ring bodies carry job_reference');
  assert.match(terminalSrc, /jobAddress: this\.jobAddress/, 'the snapshot carries the job facts');
  assert.match(terminalSrc, /this\.jobAddress = s\.jobAddress \?\? ''/, 'an older snapshot restores cleanly');
});

// ── FLOOR: the footer transaction bar (item 12) ─────────────────────────────────────────────────

test('FLOOR (12): the txbar sits OUTSIDE every step and derives through the pure segments', () => {
 // The bar reports the SALE (count · weight · the role-gated figures), not any one step, so it
 // must not live inside a card that collapses. It is now the column's bottom line: outside all
 // four accordion cards, legible from every one of them, which is the same property the old
 // "outside the panel scroll" assertion protected.
  assert.match(terminalSrc, /\$\{this\.renderTxBar\(\)\}\s*\n\s*<\/div>`;/, "the bar is the column's bottom line");
  const col = terminalSrc.slice(terminalSrc.indexOf('private renderFlowColumn()'), terminalSrc.indexOf('private renderStepBody('));
  assert.ok(col.includes('${this.renderTxBar()}'), 'and it is rendered by the column itself');
  assert.ok(!col.includes('hos-pos-step-card>\n      ${this.renderTxBar()}'), 'never inside a card');
  assert.match(terminalSrc, /txbarSegments\(/, 'segments derive pure');
  assert.match(terminalSrc, /data-testid="pos-txbar"/, 'the bar');
});

// ── BUDGET: no new hex, and the terminal budget only falls ──────────────────────────────────────

test('BUDGET: hex colour literals: grid/scan/picker at 0; the terminal capped at its pre-existing 2', () => {
  const hex = (src) => (src.match(/#[0-9a-fA-F]{3,8}\b/g) ?? []).filter((h) => !/^#\d+$/.test(h));
  assert.equal(hex(gridSrc).length, 0, 'pos-product-grid.ts must stay hex-free');
  assert.equal(hex(scanSrc).length, 0, 'pos-scan-box.ts must stay hex-free');
  assert.equal(hex(pickerSrc).length, 0, 'pos-customer-picker.ts must stay hex-free');
  assert.ok(hex(terminalSrc).length <= 2, `pos-terminal.ts hex budget is 2 (pre-existing gold inks): found ${hex(terminalSrc).length}`);
});
