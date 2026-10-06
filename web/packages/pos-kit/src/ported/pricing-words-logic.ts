// Ported from hardscapeos_dibbits master 87d7941b app/src/pricing-words-logic.ts; adapted for HH.

// pricing-words-logic.ts: the pricing module's enum→word maps, and nothing else.
//
// Item 3b, session B: a stored enum is wire format. `STAGED`, `BYPASS`, `CUSTOMER_PRODUCT_RULE` and
// `CONTRACTOR` are rows in a CHECK constraint; none of them is a thing a person says, and a screen
// that prints one is showing a database column where a sentence belongs. Every pricing surface that
// lets one of those columns reach RENDERED TEXT comes through here first.
//
// The shape is the house shape (dispatch-logic.ts:47-82, the frontend twin of the server's
// pickStateWord in backend/internal/dispatch/willcall.go): a `Record<string, string>` plus an
// accessor whose DEFAULT ARM DEGRADES HONESTLY: an unmapped value comes back as itself, lowercased
// with its underscores opened out, so a sixth batch status added to the CHECK later shows up on
// the screen as STRANGE rather than as a confident lie. Never `?? 'Unknown'`, which invents a state
// the schema does not have, and never `?? ''`, which deletes the row's only fact.
//
// WHY A FILE OF ITS OWN. The rule is that lifecycle knowledge lives in the matrix and never gets
// branched inside a component: six components read these maps, and a map living in one of them
// would make that component the other five's dependency. pricing-shared.ts is the pricing module's
// existing shared file and already carries SCOPE_LABELS / ENTITY_LABELS in exactly this shape; it is
// where these would otherwise sit, and it belongs to another owner this session, so the maps land
// beside it under the module's own `*-logic.ts` name (the pricing-uom-logic.ts precedent) rather
// than being scattered into the render functions. Fold this into pricing-shared.ts when the two
// files are back in one pair of hands: the exports are named so that is a move, not a rewrite.

/**
 * BATCH_STATUS_LABEL is what a manufacturer price batch's five states are CALLED.
 *
 * `price_batches.status` is DRAFT | STAGED | APPROVED | APPLIED | CANCELLED (migration 0039,
 * price_change_log). The stored words are shouted because a CHECK constraint has no other register;
 * on the batch console they land in a chip in a dense table, and SHOUTING there reads as an alarm on
 * a batch that is merely a draft. The lifecycle itself is unchanged and deliberately so: APPLIED
 * means the batch is written into the retail anchor and the change log, which is not the same claim
 * as "live" or "done", so the word stays the schema's own word with its voice lowered.
 *
 * The batch console, the import picker and the cost-compare picker all render this column, which is
 * the second reason it is not a component's local const.
 */
export const BATCH_STATUS_LABEL: Record<string, string> = {
  DRAFT: 'Draft',
  STAGED: 'Staged',
  APPROVED: 'Approved',
  APPLIED: 'Applied',
  CANCELLED: 'Cancelled',
};

/**
 * The five statuses in the order the batch actually moves through them, for the console's status
 * filter. Kept beside the map so a status can never appear in the filter without a word, or carry a
 * word nothing offers.
 */
export const BATCH_STATUSES: readonly string[] = [
  'DRAFT',
  'STAGED',
  'APPROVED',
  'APPLIED',
  'CANCELLED',
];

/** A batch status for people. An unmapped status degrades honestly: see the header. */
export function batchStatusWord(status: string): string {
  return BATCH_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, ' ');
}

/**
 * DERIVATION_LABEL names WHICH ANCHOR an imported line's retail price was derived from.
 *
 * `PriceChangeLine.derived_from` is TRADE_PLUS_FOB | COST_MARKUP | MFR_RETAIL | NONE
 * (backend/internal/pricing/batch_model.go:33-39). It has no migration number because it is not a
 * persisted column: batch_model.go:115-117 calls it presentation metadata, recomputed on every
 * read: which makes it MORE exposed, not less: it exists purely to be shown, and the import
 * preview's "Derivation" column was showing it in the raw.
 *
 * The words keep the schema's own arithmetic rather than summarising it, because a manager reading
 * this column is deciding whether to trust the retail number: `TRADE_PLUS_FOB` is the GTA trade
 * price plus an ESTIMATED FOB freight band, and the adjacent est.-freight chip is what marks the
 * estimate, so the word says what was added and does not repeat the caveat. NONE is not "nothing":
 * batch_model.go calls it "no usable source column → line cannot be derived", which is a line the
 * import could not price, so it says so.
 */
export const DERIVATION_LABEL: Record<string, string> = {
  TRADE_PLUS_FOB: 'Trade + freight',
  COST_MARKUP: 'Cost + markup',
  MFR_RETAIL: 'Manufacturer retail',
  NONE: 'Not derived',
};

/** A derivation anchor for people. An unmapped anchor degrades honestly: see the header. */
export function derivationWord(source: string): string {
  return DERIVATION_LABEL[source] ?? source.toLowerCase().replace(/_/g, ' ');
}

/**
 * AUDIT_ACTION_LABEL is the pricing audit log's verb, told as something that happened.
 *
 * `pricing_audit_log.action` is CREATE | UPDATE | DELETE | BYPASS (migration 0023). A log row is a
 * record of the past, so the past tense is the honest reading: the column says CREATE, the row
 * means somebody created a rule at 10:31. BYPASS is the one that has to survive the translation
 * intact: 0023 defines it as the margin gate having been SKIPPED at resolve time because
 * margin_enforcement_disabled was set on the category or product, and "NO SILENT BYPASS" is the
 * whole point of writing the row. 'Bypassed' keeps that; anything softer would quietly undo it.
 */
export const AUDIT_ACTION_LABEL: Record<string, string> = {
  CREATE: 'Created',
  UPDATE: 'Updated',
  DELETE: 'Deleted',
  BYPASS: 'Bypassed',
};

/** The four actions the 0023 CHECK admits, in the order the audit filter offers them. */
export const AUDIT_ACTIONS: readonly string[] = ['CREATE', 'UPDATE', 'DELETE', 'BYPASS'];

/** An audit action for people. An unmapped action degrades honestly: see the header. */
export function auditActionWord(action: string): string {
  return AUDIT_ACTION_LABEL[action] ?? action.toLowerCase().replace(/_/g, ' ');
}

/**
 * PRICE_SOURCE_LABEL names WHICH RULE SET a resolved price came from.
 *
 * `Breakdown.source` (backend/internal/pricing/model.go:41-50, plus SourcePromotion in promo.go:60)
 * is the precedence level the waterfall stopped at, and the same value rides out on every
 * LowMarginEntry.source. It is the answer to the only question a clerk is ever asked about a price:
 * "why is it that?": and answering it with `CUSTOMER_PRODUCT_RULE` answers nothing.
 *
 * THE WORDS ARE BORROWED, NOT INVENTED. Four of these levels already have a word on the Rules screen
 * and in the audit filter (pricing-shared.ts SCOPE_LABELS / ENTITY_LABELS), and a second word for
 * the same state would be worse than the enum: the reader would have to learn that 'Tier default'
 * and something else were one thing. So TIER_DEFAULT and TIER_CATEGORY keep the scope column's
 * wording, CUSTOMER_PRODUCT_RULE and CUSTOMER_CATEGORY_RULE take their scope's wording (the _RULE
 * suffix marks which table the row came from, which is not a fact the screen is reporting), and
 * CUSTOMER_OVERRIDE keeps the audit filter's 'Customer override'. LIST is the pricing_list_prices
 * retail anchor with nothing applied to it, so it is the list price; PROMOTION is a distinct level
 * on purpose (promo.go:56-60: a counter clerk has to be able to say "that's the June sale price"),
 * and it keeps its own word.
 */
export const PRICE_SOURCE_LABEL: Record<string, string> = {
  CUSTOMER_PRODUCT_RULE: 'Customer + product',
  CUSTOMER_CATEGORY_RULE: 'Customer + category',
  CUSTOMER_OVERRIDE: 'Customer override',
  QUANTITY_BREAK: 'Quantity break',
  TIER_CATEGORY: 'Tier + category',
  TIER_DEFAULT: 'Tier default',
  PROMOTION: 'Promotion',
  LIST: 'List price',
};

/** A price source for people. An unmapped source degrades honestly: see the header. */
export function priceSourceWord(source: string): string {
  return PRICE_SOURCE_LABEL[source] ?? source.toLowerCase().replace(/_/g, ' ');
}

/**
 * CUSTOMER_TYPE_LABEL is the account class, spelled the way the rest of the product spells it.
 *
 * `customers.customer_type` is CONTRACTOR | RETAIL | BUILDER | GOVERNMENT | INTERNAL (migration
 * 0006, mirrored by the pricing_rules.customer_type CHECK in 0023). These are the mildest offenders
 * on the list: the stored value is a real English noun under the shouting: which is exactly why
 * they had been left alone, and why the low-margin report was printing GOVERNMENT in a table column
 * beside sentence-cased everything else.
 *
 * The words are the ones the POS account picker already offers (pos-customer-picker.ts:475-479) and
 * the ones the quote work area already derives (_quote-workarea.ts customerTypeTag), so nothing here
 * teaches the reader a new vocabulary. INTERNAL is in the CHECK and has never had a picker entry;
 * it gets its word here rather than being left to the default arm.
 */
export const CUSTOMER_TYPE_LABEL: Record<string, string> = {
  CONTRACTOR: 'Contractor',
  RETAIL: 'Retail',
  BUILDER: 'Builder',
  GOVERNMENT: 'Government',
  INTERNAL: 'Internal',
};

/** An account class for people. An unmapped class degrades honestly: see the header. */
export function customerTypeWord(customerType: string): string {
  return CUSTOMER_TYPE_LABEL[customerType] ?? customerType.toLowerCase().replace(/_/g, ' ');
}
