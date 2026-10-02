// Ported from hardscapeos_dibbits master 87d7941b app/src/pricing-words-logic.test.mjs; adapted for HH.

// pricing-words-logic.test.mjs: item 3b (session B), the pricing group.
//
// Two things are being pinned here, and the second one is the one that matters in a year.
//
// 1. Every value the SCHEMA defines has a word, and it is the word we meant. The schema lists are
// copied out of the migration / Go file named above each block, so a value added there and not
// here fails the coverage test rather than reaching a counter as SHOUTING.
// 2. AN UNMAPPED VALUE DEGRADES HONESTLY. This is the whole argument for the shape: a sixth batch
// status added to the 0039 CHECK next year must arrive on the screen looking STRANGE, so
// somebody fixes it: never as 'Unknown' (a state the schema does not have) and never as the
// empty string (which deletes the row's only fact).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  AUDIT_ACTIONS,
  AUDIT_ACTION_LABEL,
  BATCH_STATUSES,
  BATCH_STATUS_LABEL,
  CUSTOMER_TYPE_LABEL,
  DERIVATION_LABEL,
  PRICE_SOURCE_LABEL,
  auditActionWord,
  batchStatusWord,
  customerTypeWord,
  derivationWord,
  priceSourceWord,
} from './pricing-words-logic.ts';

// ── the schema, copied from the source of truth ────────────────────────────────────────────────

// migration 0039_price_change_log.sql:44-45: price_batches.status CHECK
const SCHEMA_BATCH_STATUSES = ['DRAFT', 'STAGED', 'APPROVED', 'APPLIED', 'CANCELLED'];
// migration 0023_pricing.sql:176: pricing_audit_log.action CHECK
const SCHEMA_AUDIT_ACTIONS = ['CREATE', 'UPDATE', 'DELETE', 'BYPASS'];
// backend/internal/pricing/model.go:41-50 + promo.go:60: the Breakdown.Source constants
const SCHEMA_PRICE_SOURCES = [
  'CUSTOMER_PRODUCT_RULE',
  'QUANTITY_BREAK',
  'CUSTOMER_CATEGORY_RULE',
  'CUSTOMER_OVERRIDE',
  'TIER_CATEGORY',
  'TIER_DEFAULT',
  'LIST',
  'PROMOTION',
];
// migration 0006_customers.sql:8 (mirrored by 0023_pricing.sql:57): customer_type CHECK
const SCHEMA_CUSTOMER_TYPES = ['CONTRACTOR', 'RETAIL', 'BUILDER', 'GOVERNMENT', 'INTERNAL'];
// backend/internal/pricing/batch_model.go:33-39: the retail-derivation anchor constants
const SCHEMA_DERIVATIONS = ['TRADE_PLUS_FOB', 'COST_MARKUP', 'MFR_RETAIL', 'NONE'];

// ── batch status (price_batches.status, 0039) ──────────────────────────────────────────────────

test('every batch status renders its intended word', () => {
  assert.equal(batchStatusWord('DRAFT'), 'Draft');
  assert.equal(batchStatusWord('STAGED'), 'Staged');
  assert.equal(batchStatusWord('APPROVED'), 'Approved');
 // APPLIED means written into the retail anchor + the change log. It is NOT "done" and NOT "live",
 // and calling it either would be the meaning change the item's acceptance clause forbids.
  assert.equal(batchStatusWord('APPLIED'), 'Applied');
  assert.equal(batchStatusWord('CANCELLED'), 'Cancelled');
});

test('the batch-status map covers every status the 0039 CHECK defines', () => {
  for (const s of SCHEMA_BATCH_STATUSES) {
    assert.ok(s in BATCH_STATUS_LABEL, `${s} is in the CHECK and has no word`);
  }
  assert.deepEqual(Object.keys(BATCH_STATUS_LABEL).sort(), [...SCHEMA_BATCH_STATUSES].sort());
});

test('the console filter offers exactly the statuses that have words, in lifecycle order', () => {
 // A filter option is a rendered word too. If the two lists drifted, the filter would either offer
 // a status with no word or hide one the table can show.
  assert.deepEqual([...BATCH_STATUSES], SCHEMA_BATCH_STATUSES);
});

// ── audit action (pricing_audit_log.action, 0023) ──────────────────────────────────────────────

test('every audit action renders its intended word, in the past tense a log is written in', () => {
  assert.equal(auditActionWord('CREATE'), 'Created');
  assert.equal(auditActionWord('UPDATE'), 'Updated');
  assert.equal(auditActionWord('DELETE'), 'Deleted');
 // 0023:173-176: BYPASS records that the margin gate was SKIPPED. "NO SILENT BYPASS" is the reason
 // the row exists at all, so the word has to keep saying that something was got around.
  assert.equal(auditActionWord('BYPASS'), 'Bypassed');
});

test('the audit-action map covers every action the 0023 CHECK defines', () => {
  assert.deepEqual(Object.keys(AUDIT_ACTION_LABEL).sort(), [...SCHEMA_AUDIT_ACTIONS].sort());
  assert.deepEqual([...AUDIT_ACTIONS], SCHEMA_AUDIT_ACTIONS);
});

// ── price source (Breakdown.source / LowMarginEntry.source) ────────────────────────────────────

test('every price source renders its intended word', () => {
  assert.equal(priceSourceWord('CUSTOMER_PRODUCT_RULE'), 'Customer + product');
  assert.equal(priceSourceWord('CUSTOMER_CATEGORY_RULE'), 'Customer + category');
  assert.equal(priceSourceWord('CUSTOMER_OVERRIDE'), 'Customer override');
  assert.equal(priceSourceWord('QUANTITY_BREAK'), 'Quantity break');
  assert.equal(priceSourceWord('TIER_CATEGORY'), 'Tier + category');
  assert.equal(priceSourceWord('TIER_DEFAULT'), 'Tier default');
 // promo.go:56-60 keeps PROMOTION a DISTINCT level so a clerk can say "that's the June sale
 // price". Collapsing it into the tier word here would undo that on the only screen that shows it.
  assert.equal(priceSourceWord('PROMOTION'), 'Promotion');
  assert.equal(priceSourceWord('LIST'), 'List price');
});

test('the price-source map covers every Source constant the resolver can return', () => {
  assert.deepEqual(Object.keys(PRICE_SOURCE_LABEL).sort(), [...SCHEMA_PRICE_SOURCES].sort());
});

test('the tier sources keep the words the Rules screen already uses', () => {
 // pricing-shared.ts SCOPE_LABELS renders these same two values in the rules table. One state, one
 // word: a second wording here would make a reader think they were two different things.
  assert.equal(priceSourceWord('TIER_DEFAULT'), 'Tier default');
  assert.equal(priceSourceWord('TIER_CATEGORY'), 'Tier + category');
});

// ── customer type (customers.customer_type, 0006) ──────────────────────────────────────────────

test('every account class renders its intended word', () => {
  assert.equal(customerTypeWord('CONTRACTOR'), 'Contractor');
  assert.equal(customerTypeWord('RETAIL'), 'Retail');
  assert.equal(customerTypeWord('BUILDER'), 'Builder');
  assert.equal(customerTypeWord('GOVERNMENT'), 'Government');
  assert.equal(customerTypeWord('INTERNAL'), 'Internal');
});

test('the customer-type map covers every class the 0006 CHECK defines', () => {
  assert.deepEqual(Object.keys(CUSTOMER_TYPE_LABEL).sort(), [...SCHEMA_CUSTOMER_TYPES].sort());
});

test('the account words match the POS picker, which already offers four of the five', () => {
 // pos-customer-picker.ts:475-478. INTERNAL has no picker entry and gets its word here.
  for (const [stored, word] of [
    ['CONTRACTOR', 'Contractor'],
    ['RETAIL', 'Retail'],
    ['BUILDER', 'Builder'],
    ['GOVERNMENT', 'Government'],
  ]) {
    assert.equal(customerTypeWord(stored), word);
  }
});

// ── derivation anchor (PriceChangeLine.derived_from) ───────────────────────────────────────────

test('every derivation anchor renders its intended word', () => {
  assert.equal(derivationWord('TRADE_PLUS_FOB'), 'Trade + freight');
  assert.equal(derivationWord('COST_MARKUP'), 'Cost + markup');
  assert.equal(derivationWord('MFR_RETAIL'), 'Manufacturer retail');
 // NONE is not "nothing": it is a line the import could not price. The word has to say that,
 // because the retail cell beside it is empty for exactly this reason.
  assert.equal(derivationWord('NONE'), 'Not derived');
});

test('the derivation map covers every anchor batch_model.go defines', () => {
  assert.deepEqual(Object.keys(DERIVATION_LABEL).sort(), [...SCHEMA_DERIVATIONS].sort());
});

// ── the arm that keeps a future schema change visible ──────────────────────────────────────────

test('an unmapped value degrades honestly: it comes back as itself, opened out', () => {
  assert.equal(batchStatusWord('SOME_NEW_STATE'), 'some new state');
  assert.equal(auditActionWord('SOME_NEW_STATE'), 'some new state');
  assert.equal(priceSourceWord('SOME_NEW_STATE'), 'some new state');
  assert.equal(customerTypeWord('SOME_NEW_STATE'), 'some new state');
  assert.equal(derivationWord('SOME_NEW_STATE'), 'some new state');
});

test('the default arm never invents a state and never deletes one', () => {
 // The two failure modes the shape exists to rule out. 'Unknown' claims the schema has a state it
 // does not; '' loses the only thing the row was telling anybody.
  for (const word of [
    batchStatusWord('SUPERSEDED'),
    auditActionWord('RESTORE'),
    priceSourceWord('CONTRACT_MATRIX'),
    customerTypeWord('WHOLESALE'),
    derivationWord('VENDOR_FEED'),
  ]) {
    assert.notEqual(word, 'Unknown');
    assert.notEqual(word, '');
    assert.ok(word.length > 0);
  }
  assert.equal(batchStatusWord('SUPERSEDED'), 'superseded');
  assert.equal(priceSourceWord('CONTRACT_MATRIX'), 'contract matrix');
});

test('a strange value stays strange: it is not dressed up as a sentence', () => {
 // The lowercased passthrough is deliberately NOT sentence-cased. Beside 'Draft' and 'Applied', a
 // lowercase chip reads as unfinished, which is the signal.
  assert.equal(batchStatusWord('PENDING_VENDOR'), 'pending vendor');
  assert.notEqual(batchStatusWord('PENDING_VENDOR'), 'Pending vendor');
});

test('the empty string is not a state, and is not turned into one', () => {
 // derived_from is `omitempty` on the wire (batch_model.go:117), so an absent anchor arrives as ''.
 // The word map must not manufacture a word for it: the import preview renders an em dash instead,
 // and that decision belongs to the component, not to the map.
  assert.equal(derivationWord(''), '');
  assert.equal(batchStatusWord(''), '');
});
