// The Pricing surface's pure half: the words the twelve tabs say, the money
// and percent encoders, the floor warning's reading of the server's own
// grading, and the empty and refusal sentences. No DOM, no Lit, no client.
// views-pricing.ts holds the element side; pricing-controller.ts owns every
// call and every state change.
//
// Port provenance: the enum to word maps, the money and percent formats and
// the floor warning's reading are the original app's pricing-shared.ts,
// pricing-words-logic.ts, pricing-uom-logic.ts and (via the quote desk's own
// pricing panel) _pricing-floor.ts (repository hardscapeos_dibbits, read
// only; see PROVENANCE.md), rebuilt on HH's tokens and vocabulary. The maps
// keep the words the original had already ratified; an unmapped value
// degrades honestly, as itself, never as a state the schema does not have.
//
// THE MONEY RULE, restated where it can be seen: a money figure on any
// pricing screen is a server figure. The encoders here only turn what a
// person typed into the wire's integer cents, and turn the server's cents
// into text; nothing in this file sums, converts or derives a figure.

// -- the tabs ---------------------------------------------------------------------------------------

/**
 * The twelve tabs, in the order the work reasons about them: the policy the
 * floors hang from, then the rules that ride it, the customer's own prices,
 * the quantity ladder, the two timed discounts, the vendor money, the unit
 * data, the two import roads, the report, and the two records of what
 * happened. One row per tab; the list, the strip and the router name them.
 */
export interface PricingTab {
  /** The id the strip button, the router line and the tests name. */
  readonly id: string;
  /** The strip's word for the tab. */
  readonly title: string;
  /** One line under the title, what the tab decides. */
  readonly blurb: string;
}

export const PRICING_TABS: readonly PricingTab[] = [
  { id: "margin", title: "Margin policy", blurb: "The target and floor each category's prices are graded against." },
  { id: "rules", title: "Rules", blurb: "The priced rules: scope, calc, priority, validity." },
  { id: "customers", title: "Customer prices", blurb: "The category overrides one customer is given." },
  { id: "breaks", title: "Quantity breaks", blurb: "The tier ladders that stack on the resolved base." },
  { id: "promos", title: "Promotions & clearance", blurb: "The timed discounts and the marked down items." },
  { id: "rebates", title: "Rebates", blurb: "Vendor programs, the accrual ledger, the accrual write." },
  { id: "uom", title: "Units of measure", blurb: "The conversion edges and what a quantity becomes at the pick face." },
  { id: "batches", title: "Batches & imports", blurb: "Staged manufacturer updates, maker checked, applied on their date." },
  { id: "pricefile", title: "Price file", blurb: "Preview the diff, then stage it: the import road with a digest." },
  { id: "lowmargin", title: "Low margin", blurb: "The committed lines under target or floor, graded at cost." },
  { id: "audit", title: "Pricing audit", blurb: "The pricing log: every write, and every bypass, named." },
  { id: "changelog", title: "Change log", blurb: "Who moved a cost or a retail, from what to what." },
];

export function isPricingTab(value: string): value is PricingTab["id"] {
  return PRICING_TABS.some((t) => t.id === value);
}

// -- the words a stored enum carries -----------------------------------------------------------------
//
// A stored enum is wire format. ABSOLUTE, BYPASS, CUSTOMER_PRODUCT_RULE and
// CONTRACTOR are rows in a CHECK constraint; none of them is a thing a
// person says, and a screen that prints one is showing a database column
// where a sentence belongs. Every surface that lets one reach rendered text
// comes through these maps first, and an unmapped value degrades honestly:
// lowercased, separators opened out, never invented, never blanked.

/** The plain fallback for any unknown closed set value. */
function plainly(value: string): string {
  return value.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
}

const SCOPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  TIER_DEFAULT: "Tier default",
  TIER_CATEGORY: "Tier + category",
  CUSTOMER_CATEGORY: "Customer + category",
  CUSTOMER_PRODUCT: "Customer + product",
});

/** A rule's scope for people. An unknown value passes through, opened out. */
export function scopeWord(scope: string): string {
  return SCOPE_LABELS[scope] ?? plainly(scope);
}

/** The four scopes the rules list filters and creates, in list order. */
export const RULE_SCOPES: readonly string[] = Object.freeze([
  "CUSTOMER_PRODUCT",
  "CUSTOMER_CATEGORY",
  "TIER_CATEGORY",
  "TIER_DEFAULT",
]);

const CALC_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  ABSOLUTE: "Fixed price",
  PCT_OFF_LIST: "Percent off list",
  MARKUP_ON_COST: "Markup on cost",
});

/** A rule or override calc for people. */
export function calcTypeWord(calc: string): string {
  return CALC_TYPE_LABELS[calc] ?? plainly(calc);
}

/** The calc types a customer override may hold: the store CHECK forbids ABSOLUTE there. */
export const OVERRIDE_CALC_TYPES: readonly string[] = Object.freeze(["PCT_OFF_LIST", "MARKUP_ON_COST"]);

const AUDIT_ACTION_LABELS: Readonly<Record<string, string>> = Object.freeze({
  CREATE: "Created",
  UPDATE: "Updated",
  DELETE: "Deleted",
  BYPASS: "Bypassed",
});

export const AUDIT_ACTIONS: readonly string[] = Object.freeze(["CREATE", "UPDATE", "DELETE", "BYPASS"]);

/** A pricing log verb for people; BYPASS keeps its hard word on purpose. */
export function auditActionWord(action: string): string {
  return AUDIT_ACTION_LABELS[action] ?? plainly(action);
}

/**
 * PRICE_SOURCE names which anchor a resolved price came from, and the words
 * are borrowed, not invented: the four rule levels keep the scope column's
 * wording, the override keeps the audit filter's, and LIST and PROMOTION
 * keep their own. A second word for one state would be worse than the enum.
 */
const PRICE_SOURCE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  CUSTOMER_PRODUCT_RULE: "Customer + product",
  CUSTOMER_CATEGORY_RULE: "Customer + category",
  CUSTOMER_OVERRIDE: "Customer override",
  QUANTITY_BREAK: "Quantity break",
  TIER_CATEGORY: "Tier + category",
  TIER_DEFAULT: "Tier default",
  PROMOTION: "Promotion",
  LIST: "List price",
});

/** A resolved price's source level for people. */
export function priceSourceWord(source: string): string {
  return PRICE_SOURCE_LABELS[source] ?? plainly(source);
}

const BATCH_STATUS_LABELS: Readonly<Record<string, string>> = Object.freeze({
  DRAFT: "Draft",
  STAGED: "Staged",
  APPROVED: "Approved",
  APPLIED: "Applied",
  CANCELLED: "Cancelled",
});

export const BATCH_STATUSES: readonly string[] = Object.freeze([
  "DRAFT",
  "STAGED",
  "APPROVED",
  "APPLIED",
  "CANCELLED",
]);

/** A batch status for people; the lifecycle's own words, voice lowered. */
export function batchStatusWord(status: string): string {
  return BATCH_STATUS_LABELS[status] ?? plainly(status);
}

const DERIVATION_LABELS: Readonly<Record<string, string>> = Object.freeze({
  TRADE_PLUS_FOB: "Trade + freight",
  COST_MARKUP: "Cost + markup",
  MFR_RETAIL: "Manufacturer retail",
  NONE: "Not derived",
});

/** A derived retail's anchor for people; NONE is a line that could not be priced. */
export function derivationWord(source: string): string {
  return DERIVATION_LABELS[source] ?? plainly(source);
}

const ENTITY_LABELS: Readonly<Record<string, string>> = Object.freeze({
  pricing_rule: "Pricing rule",
  category_policy: "Category policy",
  customer_override: "Customer override",
  margin_enforcement: "Margin enforcement",
  quantity_break: "Quantity break",
});

/** A pricing log entity for people; unknown values pass through opened out. */
export function entityWord(entity: string): string {
  return ENTITY_LABELS[entity] ?? plainly(entity);
}

const UOM_SOURCE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  SEEDED_DEFAULT: "Seeded default",
  CLIENT_CONFIRMED: "Client confirmed",
  VENDOR_SHEET: "Vendor sheet",
  MEASURED: "Measured",
});

/** A conversion factor's provenance for people. */
export function uomSourceWord(source: string): string {
  return UOM_SOURCE_LABELS[source] ?? plainly(source);
}

/** A factor is CONFIRMED only when someone outside this codebase said so. */
export function isUomConfirmed(source: string): boolean {
  return source !== "SEEDED_DEFAULT";
}

const CUSTOMER_TYPE_LABELS: Readonly<Record<string, string>> = Object.freeze({
  CONTRACTOR: "Contractor",
  RETAIL: "Retail",
  BUILDER: "Builder",
  GOVERNMENT: "Government",
  INTERNAL: "Internal",
});

export const CUSTOMER_TYPES: readonly string[] = Object.freeze([
  "CONTRACTOR",
  "RETAIL",
  "BUILDER",
  "GOVERNMENT",
  "INTERNAL",
]);

/** An account class for people. */
export function customerTypeWord(customerType: string): string {
  return CUSTOMER_TYPE_LABELS[customerType] ?? plainly(customerType);
}

const CLEARANCE_OUTCOME_LABELS: Readonly<Record<string, string>> = Object.freeze({
  NEW: "New",
  CHANGED: "Changed",
  UNCHANGED: "Unchanged",
  UNKNOWN: "No catalog product",
});

/** A price file row's outcome for people; UNKNOWN is an unmatched SKU, said plainly. */
export function outcomeWord(outcome: string): string {
  return CLEARANCE_OUTCOME_LABELS[outcome] ?? plainly(outcome);
}

// -- the figures -------------------------------------------------------------------------------------
//
// Money renders from the server's cents and never the other way round: the
// only arithmetic here turns what a person typed into the integer the wire
// carries, and refuses anything it cannot turn cleanly. Both encoders
// refuse negatives: a negative price or a negative margin is the server's
// refusal to give, not a form the client can second guess.

/** Dollars a person typed to integer cents, or null when it is not one. */
export function centsFromMoneyInput(input: string): number | null {
  const t = input.trim();
  if (t === "") return null;
  const n = Number(t);
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * 100);
}

/** A percent a person typed to integer basis points, or null when it is not one. */
export function bpsFromPctInput(input: string): number | null {
  const t = input.trim();
  if (t === "") return null;
  const n = Number(t);
  if (!Number.isFinite(n) || n < 0) return null;
  return Math.round(n * 100);
}

/** Integer cents to the display text, the dollar sign included. */
export function moneyWords(cents: number | null | undefined): string {
  if (cents === null || cents === undefined || !Number.isFinite(cents)) return "";
  const sign = cents < 0 ? "-" : "";
  const abs = Math.abs(Math.round(cents));
  const dollars = Math.floor(abs / 100).toString();
  const rem = (abs % 100).toString().padStart(2, "0");
  return `CA$${sign}${dollars}.${rem}`;
}

/** Integer basis points to a two decimal percent, the sign included. */
export function pctWords(bps: number | null | undefined): string {
  if (bps === null || bps === undefined || !Number.isFinite(bps)) return "";
  return `${(bps / 100).toFixed(2)}%`;
}

/**
 * A float without trailing zero noise: 7 not 7.0000, 6.314 not 6.31400000.
 * The original's own trim, kept for the UoM factor and plan quantities.
 */
export function trimNumber(n: number, dp = 3): string {
  if (!Number.isFinite(n)) return "";
  return String(Number(n.toFixed(dp)));
}

// -- the categories -----------------------------------------------------------------------------------

/** One category as the catalog read projects it. */
export interface CategoryRow {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly is_active: boolean;
}

/**
 * A category id as a person reads it: the catalog's name when the read
 * knows the id, else the id's own first block. The original mapped its
 * seed ids in a table; HH asks the catalog instead, because a hardcoded
 * id table is one tenant's facts wearing another tenant's screen.
 */
export function categoryName(id: string, categories: readonly CategoryRow[]): string {
  if (id === "") return "";
  const hit = categories.find((c) => c.id === id);
  return hit !== undefined ? hit.name : id.slice(0, 8);
}

/** The category picker's options, active first then retired, each by code order. */
export function categoryOptions(categories: readonly CategoryRow[]): readonly CategoryRow[] {
  return [...categories].sort((a, b) => (a.code < b.code ? -1 : a.code > b.code ? 1 : 0));
}

// -- the states ---------------------------------------------------------------------------------------

export type LoadPhase = "idle" | "loading" | "ready" | "error" | "refused";

/** The margin tab's empty words. */
export function marginEmptyWords(): string {
  return "No margin policies yet. A category prices without a floor until one is set; the resolve grades against zero.";
}

/** An unwired tab's one plain fact, until its group lands. */
export function pricingEmptyWords(title: string): string {
  return `The ${title} tab is not wired yet.`;
}

/** A clearing write's status word, shown whole or not at all. */
export function clearWords(status: { readonly status: string } | null): string {
  return status === null ? "" : status.status;
}

/** A shared empty sentence for list tabs whose rows simply have not been made. */
export function listEmptyWords(what: string): string {
  return `No ${what} yet. Nothing here is hidden: the list is the whole list.`;
}

// -- the refusals --------------------------------------------------------------------------------------
//
// The core's gates: every pricing read is requireStaff, every pricing write
// is requireManager over the pricing editors list. The shared Forbidden
// body is one word; the screen adds the sentence naming who, the same rule
// the staff register carries. Every other status keeps the server's own
// words verbatim, never a paraphrase.

export function pricingRefusalWords(error: string, status: number | undefined, kind: "read" | "write"): string {
  if (status === 403) {
    return kind === "write"
      ? "You may not change pricing. Pricing writes are managers' and above; the Forbidden gate named the caller, and the core's pricing editors list holds who may act. (server: Forbidden)"
      : "You may not open this. The pricing reads are staff's; the Forbidden gate named the caller. (server: Forbidden)";
  }
  return error !== "" ? error : "Try again in a moment.";
}

/**
 * A write's inline refusal: the server's own words, with the shared Forbidden
 * body expanded the one way the gate allows.
 */
export function pricingWriteError(error: string, status: number | undefined): string {
  return pricingRefusalWords(error, status, "write");
}

// -- the floor warning ---------------------------------------------------------------------------------
//
// THE SERVER GRADED, THE CLIENT ONLY READS THE GRADE. Before a save the
// floor preview names the products the new price would put under their
// margin floor; the editor confirms; the save runs; and the confirmation is
// logged against the record the save wrote. Nothing here computes a margin,
// picks a floor or decides a verdict: the reading below only sorts what the
// server already graded into what the editor must see.

/** One product the proposed price leaves under the floor, as the server graded it. */
export interface FloorItemLike {
  readonly product_id: string;
  readonly sku: string;
  readonly name: string;
  readonly final_cents: number;
  readonly margin_bps: number;
  readonly floor_bps: number;
  readonly previous_final_cents: number;
  readonly previous_margin_bps: number;
  readonly already_under_floor: boolean;
}

export interface FloorPreviewLike {
  readonly items: readonly FloorItemLike[];
  readonly evaluated: number;
  readonly not_graded: number;
  readonly no_cost: number;
  readonly truncated: boolean;
  readonly no_cost_branches?: readonly { readonly branch_name?: string; readonly products: number }[];
}

/** The products the change makes worse: newly under, or under and cheaper still. */
export function affectedFloorItems(preview: FloorPreviewLike): readonly FloorItemLike[] {
  return preview.items.filter((i) => !i.already_under_floor || i.final_cents < i.previous_final_cents);
}

/** Products already under the floor that the change does not make worse. */
export function alreadyUnderFloorCount(preview: FloorPreviewLike): number {
  return preview.items.length - affectedFloorItems(preview).length;
}

/**
 * Whether the editor is asked to confirm before the save runs. Never a
 * silent save past a check that could not grade: a product the change makes
 * worse, a product that could not be priced or has no cost, and a check
 * that stopped at its limit all open the notice.
 */
export function needsFloorConfirm(preview: FloorPreviewLike): boolean {
  return affectedFloorItems(preview).length > 0 || preview.not_graded > 0 || preview.no_cost > 0 || preview.truncated;
}

/** The grading's coverage, as lines beside the warning; a clean grading says nothing. */
export function floorNoteLines(preview: FloorPreviewLike): readonly string[] {
  const lines: string[] = [];
  if (preview.truncated) lines.push("The grading stopped at its cap; some combinations were not graded.");
  if (preview.not_graded > 0) lines.push(`${preview.not_graded} products could not be graded (no price to grade).`);
  if (preview.no_cost > 0) {
    const branches = preview.no_cost_branches ?? [];
    const where = branches.length > 0 ? ` at ${branches.map((b) => b.branch_name ?? "a yard").join(", ")}` : "";
    lines.push(`${preview.no_cost} product${preview.no_cost === 1 ? " has" : "s have"} no cost on file${where}; their margin cannot be told.`);
  }
  return lines;
}

/** The ids of the products the editor was shown, which the confirmation names. */
export function shownProductIds(preview: FloorPreviewLike): readonly string[] {
  return affectedFloorItems(preview).map((i) => i.product_id);
}

/** The confirmation's verdict: the server's own status word, or the fact it did not land. */
export function floorConfirmVerdict(status: { readonly status: string } | null): string {
  if (status === null) return "The confirmation was not recorded.";
  return `The confirmation is in the pricing log (${status.status}).`;
}

// -- the rules tab: a rule read as a person reads it ---------------------------------------------------

/** The rules tab's empty words. */
export function ruleEmptyWords(): string {
  return "No pricing rules yet. Every price resolves through the tier defaults until a rule is written.";
}

/**
 * A rule's value in its calc's own unit. The figure is the server's; the
 * words only name what the calc does with it.
 */
export function ruleValueWords(
  rule: {
    readonly calc_type: string;
    readonly price_cents?: number | null;
    readonly discount_bps?: number | null;
    readonly markup_bps?: number | null;
    readonly price_uom: string;
  },
): string {
  switch (rule.calc_type) {
    case "ABSOLUTE":
      return `${moneyWords(rule.price_cents ?? null)} ${ruleCalcWords(rule.price_uom)}`.trim();
    case "PCT_OFF_LIST":
      return `${pctWords(rule.discount_bps ?? null)} off list`;
    case "MARKUP_ON_COST":
      return `${pctWords(rule.markup_bps ?? null)} on cost`;
    default:
      return "";
  }
}

/** The unit a fixed price is carried in, as a sentence tail. */
export function ruleCalcWords(priceUom: string): string {
  return priceUom === "" ? "" : `a ${priceUom}`;
}

/**
 * A rule's validity window, as the row shows it: the date each bound names,
 * and nothing when the rule is open ended. An unparsable bound is left out
 * rather than guessed at.
 */
export function validityWords(validFrom: string, validTo: string): string {
  const from = dayWords(validFrom);
  const to = dayWords(validTo);
  if (from !== "" && to !== "") return `${from} to ${to}`;
  if (from !== "") return `from ${from}`;
  if (to !== "") return `to ${to}`;
  return "";
}

function dayWords(iso: string): string {
  if (iso.trim() === "") return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return iso.slice(0, 10);
}
