// The Pricing surface's one door to the core: every read and write the
// twelve tabs make, over the generated api-client, answering a Result and
// never throwing.
//
// THE READ PROJECTION. Every wire object is copied through a whitelist into
// a narrow type that names only what the screens use. A field the projection
// does not name is dropped at this door and never reaches a screen. Nothing
// here sums, converts or derives a figure: a money figure is a server
// figure, and the door carries it as it came.
//
// THE COVERAGE. The pricing governance family (the staff-pricing tag) is
// wired whole; the one operation this door does not carry is pricingResolve,
// whose contract access names the counter till and the quote desk, not this
// app. The category names come from the product categories read, which the
// contract serves "for the pricing screens" at this app.
//
// Port provenance: the shapes are the original app's pricing-shared.ts,
// pricing-audit.ts, pricing-rebates.ts and pricing-uom.ts reads (repository
// hardscapeos_dibbits, read only; see PROVENANCE.md), rebuilt on HH's
// generated client and narrowed to what the twelve tabs use.
import {
  staffProduct,
  staffPricing,
  type PricingClearanceWrite,
  type PricingFloorConfirmation,
  type PricingFloorPreviewRequest,
  type PricingOverrideWrite,
  type PricingPromotionWrite,
  type PricingRulePatch,
  type PricingRuleWrite,
  type Result,
} from "@hardscapehouse/api-client";
import type { CategoryRow } from "./pricing-logic.js";

type Bag = Record<string, unknown>;

const bag = (value: unknown): Bag => (typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Bag) : {});
const text = (value: unknown): string => (typeof value === "string" ? value : "");
const num = (value: unknown): number => (typeof value === "number" && Number.isFinite(value) ? value : 0);
const maybeNum = (value: unknown): number | null => (typeof value === "number" && Number.isFinite(value) ? value : null);
const bool = (value: unknown): boolean => value === true;
const list = (value: unknown): readonly unknown[] => (Array.isArray(value) ? value : []);
const page = (value: unknown): readonly unknown[] => list(bag(value).items);

// -- the rows ---------------------------------------------------------------------------------------

/** One category as the catalog read projects it (the pricing screens' names). */
export function toCategoryRow(raw: unknown): CategoryRow {
  const r = bag(raw);
  return { id: text(r.id), code: text(r.code), name: text(r.name), is_active: bool(r.is_active) };
}

/** One category's margin policy: the target, the floor, and whether the gate is off. */
export interface PolicyRow {
  readonly category_id: string;
  readonly margin_target_bps: number;
  readonly margin_floor_bps: number;
  readonly margin_enforcement_disabled: boolean;
}

export function toPolicyRow(raw: unknown): PolicyRow {
  const r = bag(raw);
  return {
    category_id: text(r.category_id),
    margin_target_bps: num(r.margin_target_bps),
    margin_floor_bps: num(r.margin_floor_bps),
    margin_enforcement_disabled: bool(r.margin_enforcement_disabled),
  };
}

/** What a policy write carries on the wire. */
export interface PolicyWrite {
  readonly margin_target_bps: number;
  readonly margin_floor_bps: number;
  readonly margin_enforcement_disabled: boolean;
}

/** One product the proposed price leaves under the floor, as the server graded it. */
export interface FloorItem {
  readonly product_id: string;
  readonly sku: string;
  readonly name: string;
  readonly category_id: string;
  readonly customer_type: string;
  readonly branch_code: string;
  readonly branch_name: string;
  readonly final_cents: number;
  readonly margin_bps: number;
  readonly floor_bps: number;
  readonly price_uom: string;
  readonly source: string;
  readonly previous_final_cents: number;
  readonly previous_margin_bps: number;
  readonly already_under_floor: boolean;
}

export function toFloorItem(raw: unknown): FloorItem {
  const r = bag(raw);
  return {
    product_id: text(r.product_id),
    sku: text(r.sku),
    name: text(r.name),
    category_id: text(r.category_id),
    customer_type: text(r.customer_type),
    branch_code: text(r.branch_code),
    branch_name: text(r.branch_name),
    final_cents: num(r.final_cents),
    margin_bps: num(r.margin_bps),
    floor_bps: num(r.floor_bps),
    price_uom: text(r.price_uom),
    source: text(r.source),
    previous_final_cents: num(r.previous_final_cents),
    previous_margin_bps: num(r.previous_margin_bps),
    already_under_floor: bool(r.already_under_floor),
  };
}

/** The floor preview answer, narrowed: the items plus the coverage counts. */
export interface FloorPreview {
  readonly items: readonly FloorItem[];
  readonly evaluated: number;
  readonly not_graded: number;
  readonly no_cost: number;
  readonly truncated: boolean;
  readonly no_cost_branches: readonly { readonly branch_name: string; readonly products: number }[];
}

export function toFloorPreview(raw: unknown): FloorPreview {
  const r = bag(raw);
  return {
    items: list(r.items).map(toFloorItem),
    evaluated: num(r.evaluated),
    not_graded: num(r.not_graded),
    no_cost: num(r.no_cost),
    truncated: bool(r.truncated),
    no_cost_branches: list(r.no_cost_branches).map((b) => {
      const bb = bag(b);
      return { branch_name: text(bb.branch_name), products: num(bb.products) };
    }),
  };
}

/** The single status word the writes and the confirmation answer carry. */
export interface StatusResult {
  readonly status: string;
}

function toStatus(raw: unknown): StatusResult {
  return { status: text(bag(raw).status) };
}

/** The id and starting version a create answers with. */
export interface CreatedRef {
  readonly id: string;
  readonly version: number;
}

function toCreatedRef(raw: unknown): CreatedRef {
  const r = bag(raw);
  return { id: text(r.id), version: num(r.version) };
}

// -- the rules -----------------------------------------------------------------------------------------

/** One pricing rule as the screens use it. */
export interface RuleRow {
  readonly id: string;
  readonly scope: string;
  readonly customer_id: string;
  readonly customer_type: string;
  readonly category_id: string;
  readonly product_id: string;
  readonly branch_id: string;
  readonly calc_type: string;
  readonly price_cents: number | null;
  readonly discount_bps: number | null;
  readonly markup_bps: number | null;
  readonly price_uom: string;
  readonly priority: number;
  readonly valid_from: string;
  readonly valid_to: string;
  readonly detail_text: string;
  readonly is_active: boolean;
  readonly version: number;
}

export function toRuleRow(raw: unknown): RuleRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    scope: text(r.scope),
    customer_id: text(r.customer_id),
    customer_type: text(r.customer_type),
    category_id: text(r.category_id),
    product_id: text(r.product_id),
    branch_id: text(r.branch_id),
    calc_type: text(r.calc_type),
    price_cents: maybeNum(r.price_cents),
    discount_bps: maybeNum(r.discount_bps),
    markup_bps: maybeNum(r.markup_bps),
    price_uom: text(r.price_uom),
    priority: num(r.priority),
    valid_from: text(r.valid_from),
    valid_to: text(r.valid_to),
    detail_text: text(r.detail_text),
    is_active: bool(r.is_active),
    version: num(r.version),
  };
}

export interface RuleQuery {
  readonly scope?: string;
  readonly customer_type?: string;
}

// -- the promotions --------------------------------------------------------------------------------------

/** One promotion as the screens use it. */
export interface PromotionRow {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly product_id: string;
  readonly category_id: string;
  readonly branch_id: string;
  readonly starts_at: string;
  readonly ends_at: string;
  readonly calc_type: string;
  readonly discount_bps: number | null;
  readonly discount_cents: number | null;
  readonly price_cents: number | null;
  readonly price_uom: string;
  readonly priority: number;
  readonly detail_text: string;
  readonly is_active: boolean;
  readonly version: number;
}

export function toPromotionRow(raw: unknown): PromotionRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    code: text(r.code),
    name: text(r.name),
    product_id: text(r.product_id),
    category_id: text(r.category_id),
    branch_id: text(r.branch_id),
    starts_at: text(r.starts_at),
    ends_at: text(r.ends_at),
    calc_type: text(r.calc_type),
    discount_bps: maybeNum(r.discount_bps),
    discount_cents: maybeNum(r.discount_cents),
    price_cents: maybeNum(r.price_cents),
    price_uom: text(r.price_uom),
    priority: num(r.priority),
    detail_text: text(r.detail_text),
    is_active: bool(r.is_active),
    version: num(r.version),
  };
}

// -- the clearance ---------------------------------------------------------------------------------------

/** One clearance item as the screens use it. */
export interface ClearanceRow {
  readonly product_id: string;
  readonly sku: string;
  readonly name: string;
  readonly reason: string;
  readonly clearance_price_cents: number | null;
  readonly price_uom: string;
  readonly started_at: string;
  readonly ends_at: string;
  readonly is_active: boolean;
  readonly version: number;
}

export function toClearanceRow(raw: unknown): ClearanceRow {
  const r = bag(raw);
  return {
    product_id: text(r.product_id),
    sku: text(r.sku),
    name: text(r.name),
    reason: text(r.reason),
    clearance_price_cents: maybeNum(r.clearance_price_cents),
    price_uom: text(r.price_uom),
    started_at: text(r.started_at),
    ends_at: text(r.ends_at),
    is_active: bool(r.is_active),
    version: num(r.version),
  };
}

  // -- the rebates ---------------------------------------------------------------------------------------

/** One rebate program as the screens use it. */
export interface RebateRow {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly category_id: string;
  readonly product_id: string;
  readonly basis: string;
  readonly rate_bps: number | null;
  readonly rate_cents_per_unit: number | null;
  readonly starts_at: string;
  readonly ends_at: string;
  readonly notes: string;
  readonly is_active: boolean;
  readonly version: number;
}

export function toRebateRow(raw: unknown): RebateRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    code: text(r.code),
    name: text(r.name),
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    category_id: text(r.category_id),
    product_id: text(r.product_id),
    basis: text(r.basis),
    rate_bps: maybeNum(r.rate_bps),
    rate_cents_per_unit: maybeNum(r.rate_cents_per_unit),
    starts_at: text(r.starts_at),
    ends_at: text(r.ends_at),
    notes: text(r.notes),
    is_active: bool(r.is_active),
    version: num(r.version),
  };
}

/** One accrual row as the ledger shows it. */
export interface AccrualRow {
  readonly id: string;
  readonly program_id: string;
  readonly program_code: string;
  readonly vendor_id: string;
  readonly product_id: string;
  readonly product_sku: string;
  readonly source_type: string;
  readonly source_id: string;
  readonly qty_base: number;
  readonly basis_cents: number;
  readonly accrual_cents: number;
  readonly rate_note: string;
  readonly accrued_at: string;
}

function toAccrualRow(raw: unknown): AccrualRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    program_id: text(r.program_id),
    program_code: text(r.program_code),
    vendor_id: text(r.vendor_id),
    product_id: text(r.product_id),
    product_sku: text(r.product_sku),
    source_type: text(r.source_type),
    source_id: text(r.source_id),
    qty_base: num(r.qty_base),
    basis_cents: num(r.basis_cents),
    accrual_cents: num(r.accrual_cents),
    rate_note: text(r.rate_note),
    accrued_at: text(r.accrued_at),
  };
}

/** One vendor's ledger: the claim side, with its totals. */
export interface LedgerRow {
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly total_cents: number;
  readonly accrual_count: number;
  readonly rows: readonly AccrualRow[];
}

function toLedgerRow(raw: unknown): LedgerRow {
  const r = bag(raw);
  return {
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    total_cents: num(r.total_cents),
    accrual_count: num(r.accrual_count),
    rows: list(r.rows).map(toAccrualRow),
  };
}

/** What an accrual run wrote, as the server reports it. */
export interface AccrualResult {
  readonly accrued: readonly AccrualRow[];
  readonly skipped_count: number;
  readonly total_cents: number;
}

function toAccrualResult(raw: unknown): AccrualResult {
  const r = bag(raw);
  return {
    accrued: list(r.accrued).map(toAccrualRow),
    skipped_count: num(r.skipped_count),
    total_cents: num(r.total_cents),
  };
}

// -- the units of measure --------------------------------------------------------------------------------

/** One conversion edge as the screens use it. */
export interface UomConversionRow {
  readonly id: string;
  readonly product_id: string;
  readonly product_sku: string;
  readonly category_id: string;
  readonly from_uom: string;
  readonly to_uom: string;
  readonly factor: number;
  readonly round_up_at_pick: boolean;
  readonly source: string;
  readonly source_note: string;
  readonly is_active: boolean;
  readonly version: number;
}

export function toUomConversionRow(raw: unknown): UomConversionRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    product_id: text(r.product_id),
    product_sku: text(r.product_sku),
    category_id: text(r.category_id),
    from_uom: text(r.from_uom),
    to_uom: text(r.to_uom),
    factor: num(r.factor),
    round_up_at_pick: bool(r.round_up_at_pick),
    source: text(r.source),
    source_note: text(r.source_note),
    is_active: bool(r.is_active),
    version: num(r.version),
  };
}

/** The plan the engine answers, narrowed. */
export interface UomPlanRow {
  readonly product_id: string;
  readonly qty_entered: number;
  readonly uom_entered: string;
  readonly pick_uom: string;
  readonly pick_qty_exact: number;
  readonly pick_qty: number;
  readonly rounded_up: boolean;
  readonly overage_qty: number;
  readonly pick_steps: readonly { readonly from_uom: string; readonly to_uom: string; readonly factor: number; readonly source: string }[];
  readonly weight_uom: string;
  readonly weight_qty: number;
  readonly weight_steps: readonly { readonly from_uom: string; readonly to_uom: string; readonly factor: number; readonly source: string }[];
  readonly unconfirmed: boolean;
}

function toUomPlanRow(raw: unknown): UomPlanRow {
  const r = bag(raw);
  const step = (x: unknown): { readonly from_uom: string; readonly to_uom: string; readonly factor: number; readonly source: string } => {
    const b = bag(x);
    return { from_uom: text(b.from_uom), to_uom: text(b.to_uom), factor: num(b.factor), source: text(b.source) };
  };
  return {
    product_id: text(r.product_id),
    qty_entered: num(r.qty_entered),
    uom_entered: text(r.uom_entered),
    pick_uom: text(r.pick_uom),
    pick_qty_exact: num(r.pick_qty_exact),
    pick_qty: num(r.pick_qty),
    rounded_up: bool(r.rounded_up),
    overage_qty: num(r.overage_qty),
    pick_steps: list(r.pick_steps).map(step),
    weight_uom: text(r.weight_uom),
    weight_qty: num(r.weight_qty),
    weight_steps: list(r.weight_steps).map(step),
    unconfirmed: bool(r.unconfirmed),
  };
}

// -- the customer overrides --------------------------------------------------------------------------

/** One customer category override as the screens use it. */
export interface OverrideRow {
  readonly id: string;
  readonly customer_id: string;
  readonly category_id: string;
  readonly calc_type: string;
  readonly price_cents: number | null;
  readonly discount_bps: number | null;
  readonly markup_bps: number | null;
  readonly price_uom: string;
  readonly notes: string;
  readonly version: number;
}

export function toOverrideRow(raw: unknown): OverrideRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    customer_id: text(r.customer_id),
    category_id: text(r.category_id),
    calc_type: text(r.calc_type),
    price_cents: maybeNum(r.price_cents),
    discount_bps: maybeNum(r.discount_bps),
    markup_bps: maybeNum(r.markup_bps),
    price_uom: text(r.price_uom),
    notes: text(r.notes),
    version: num(r.version),
  };
}

// -- the quantity breaks --------------------------------------------------------------------------------

/** One tier of a schedule: a band with an adjustment of a named unit. */
export interface BreakItem {
  readonly id: string;
  readonly from_qty: number;
  readonly to_qty: number | null;
  readonly value_type: string;
  readonly discount_bps: number | null;
  readonly discount_cents: number | null;
  readonly surcharge_bps: number | null;
  readonly surcharge_cents: number | null;
  readonly price_uom: string;
}

export function toBreakItem(raw: unknown): BreakItem {
  const r = bag(raw);
  return {
    id: text(r.id),
    from_qty: num(r.from_qty),
    to_qty: r.to_qty === null || r.to_qty === undefined ? null : num(r.to_qty),
    value_type: text(r.value_type),
    discount_bps: maybeNum(r.discount_bps),
    discount_cents: maybeNum(r.discount_cents),
    surcharge_bps: maybeNum(r.surcharge_bps),
    surcharge_cents: maybeNum(r.surcharge_cents),
    price_uom: text(r.price_uom),
  };
}

/** One quantity break schedule plus its tiers, under an optimistic version. */
export interface BreakRow {
  readonly id: string;
  readonly code: string;
  readonly name: string;
  readonly product_id: string;
  readonly category_id: string;
  readonly threshold_uom: string;
  readonly is_active: boolean;
  readonly version: number;
  readonly items: readonly BreakItem[];
}

export function toBreakRow(raw: unknown): BreakRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    code: text(r.code),
    name: text(r.name),
    product_id: text(r.product_id),
    category_id: text(r.category_id),
    threshold_uom: text(r.threshold_uom),
    is_active: bool(r.is_active),
    version: num(r.version),
    items: list(r.items).map(toBreakItem),
  };
}

/**
 * Every call the pricing tabs make. The generated client's own functions do
 * the wire; this class narrows the answers and gives each tab one named
 * method, so a screen never sees a wire object.
 */
export class PricingApi {
  readonly #client: import("@hardscapehouse/api-client").ApiClient;

  constructor(client: import("@hardscapehouse/api-client").ApiClient) {
    this.#client = client;
  }

  // -- the categories (the pricing screens' names) -----------------------------------------------

  /** The catalog's categories, in code order: the pickers' names. */
  async categories(): Promise<Result<readonly CategoryRow[]>> {
    const answer = await staffProduct.productListCategories(this.#client, { limit: 500 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toCategoryRow) };
  }

  // -- the margin policies -------------------------------------------------------------------------

  /** Every category's margin policy. */
  async policies(): Promise<Result<readonly PolicyRow[]>> {
    const answer = await staffPricing.pricingListCategoryPolicies(this.#client, { limit: 500 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toPolicyRow) };
  }

  /** Set one category's policy whole (the route is an upsert). */
  async upsertPolicy(categoryId: string, body: PolicyWrite): Promise<Result<PolicyRow>> {
    const answer = await staffPricing.pricingUpsertCategoryPolicy(this.#client, categoryId, { ...body });
    if (!answer.ok) return answer;
    return { ok: true, value: toPolicyRow(answer.value) };
  }

  // -- the rules -------------------------------------------------------------------------------------

  /** The pricing rules, filtered by scope and customer type. */
  async rules(query: RuleQuery = {}): Promise<Result<readonly RuleRow[]>> {
    const answer = await staffPricing.pricingListRules(this.#client, {
      limit: 200,
      scope: query.scope === "" ? undefined : (query.scope as "CUSTOMER_PRODUCT" | "CUSTOMER_CATEGORY" | "TIER_CATEGORY" | "TIER_DEFAULT"),
      customer_type: query.customer_type === "" ? undefined : query.customer_type,
    });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toRuleRow) };
  }

  /** One rule, read whole: the editor opens on the version the server holds now. */
  async rule(id: string): Promise<Result<RuleRow>> {
    const answer = await staffPricing.pricingGetRule(this.#client, id);
    if (!answer.ok) return answer;
    return { ok: true, value: toRuleRow(answer.value) };
  }

  /** Create a rule; the write carries version zero. */
  async createRule(body: PricingRuleWrite): Promise<Result<CreatedRef>> {
    const answer = await staffPricing.pricingCreateRule(this.#client, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toCreatedRef(answer.value) };
  }

  /** Save a rule as a sparse patch over the version the editor read. */
  async updateRule(id: string, body: PricingRulePatch): Promise<Result<RuleRow>> {
    const answer = await staffPricing.pricingUpdateRule(this.#client, id, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toRuleRow(answer.value) };
  }

  /** Retire a rule; the version the row showed rides the delete. */
  async deleteRule(id: string, opts: { version: number }): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeleteRule(this.#client, id, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

// -- the customer overrides ---------------------------------------------------------------------------

  /** One customer's category overrides, the prices that ride over their tier. */
  async overrides(customerId: string): Promise<Result<readonly OverrideRow[]>> {
    const answer = await staffPricing.pricingListOverrides(this.#client, customerId, { limit: 200 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toOverrideRow) };
  }

  /** Give one customer a category price; the write carries version zero. */
  async createOverride(customerId: string, body: PricingOverrideWrite): Promise<Result<CreatedRef>> {
    const answer = await staffPricing.pricingCreateOverride(this.#client, customerId, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toCreatedRef(answer.value) };
  }

  /** Save one override as the write over the version the row showed. */
  async updateOverride(id: string, body: PricingOverrideWrite): Promise<Result<OverrideRow>> {
    const answer = await staffPricing.pricingUpdateOverride(this.#client, id, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toOverrideRow(answer.value) };
  }

  /** Delete one override; the version the row showed rides the delete. */
  async deleteOverride(id: string, opts: { version: number }): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeleteOverride(this.#client, id, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

  // -- the quantity breaks ---------------------------------------------------------------------------

  /** The quantity break schedules, each with its tiers. */
  async breaks(): Promise<Result<readonly BreakRow[]>> {
    const answer = await staffPricing.pricingListQuantityBreaks(this.#client, { limit: 200 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toBreakRow) };
  }

  /** One schedule, read whole: the editor opens on the version the server holds now. */
  async break(id: string): Promise<Result<BreakRow>> {
    const answer = await staffPricing.pricingGetQuantityBreak(this.#client, id);
    if (!answer.ok) return answer;
    return { ok: true, value: toBreakRow(answer.value) };
  }

  /** Create a schedule; the write carries version zero. */
  async createBreak(body: unknown): Promise<Result<CreatedRef>> {
    const answer = await staffPricing.pricingCreateQuantityBreak(this.#client, body as Parameters<typeof staffPricing.pricingCreateQuantityBreak>[1]);
    if (!answer.ok) return answer;
    return { ok: true, value: toCreatedRef(answer.value) };
  }

  /** Save a schedule whole over the version the editor read. */
  async updateBreak(id: string, body: unknown): Promise<Result<BreakRow>> {
    const answer = await staffPricing.pricingUpdateQuantityBreak(this.#client, id, body as Parameters<typeof staffPricing.pricingUpdateQuantityBreak>[2]);
    if (!answer.ok) return answer;
    return { ok: true, value: toBreakRow(answer.value) };
  }

  /** Retire a schedule; the version the row showed rides the delete. */
  async deleteBreak(id: string, opts: { version: number }): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeleteQuantityBreak(this.#client, id, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

  // -- the promotions and clearance --------------------------------------------------------------------

  /** The promotions, active and retired. */
  async promotions(): Promise<Result<readonly PromotionRow[]>> {
    const answer = await staffPricing.pricingListPromotions(this.#client, { limit: 200, include_inactive: true });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toPromotionRow) };
  }

  async createPromotion(body: PricingPromotionWrite): Promise<Result<PromotionRow>> {
    const answer = await staffPricing.pricingCreatePromotion(this.#client, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toPromotionRow(answer.value) };
  }

  async updatePromotion(id: string, body: PricingPromotionWrite): Promise<Result<PromotionRow>> {
    const answer = await staffPricing.pricingUpdatePromotion(this.#client, id, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toPromotionRow(answer.value) };
  }

  async deletePromotion(id: string, opts: { version: number }): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeletePromotion(this.#client, id, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

  /** The clearance items, active and ended. */
  async clearance(): Promise<Result<readonly ClearanceRow[]>> {
    const answer = await staffPricing.pricingListClearance(this.#client, { limit: 200, include_inactive: true });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toClearanceRow) };
  }

  /** Mark one product clearance, or rewrite its mark (the route is an upsert). */
  async upsertClearance(productId: string, body: PricingClearanceWrite): Promise<Result<ClearanceRow>> {
    const answer = await staffPricing.pricingUpsertClearance(this.#client, productId, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toClearanceRow(answer.value) };
  }

  /** Take one product's clearance mark off. */
  async deleteClearance(productId: string): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeleteClearance(this.#client, productId);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

  // -- the rebates -------------------------------------------------------------------------------------

  /** The rebate programs, active and retired. */
  async rebatePrograms(): Promise<Result<readonly RebateRow[]>> {
    const answer = await staffPricing.pricingListRebatePrograms(this.#client, { limit: 200, include_inactive: true });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toRebateRow) };
  }

  async createRebate(body: unknown): Promise<Result<RebateRow>> {
    const answer = await staffPricing.pricingCreateRebateProgram(this.#client, body as Parameters<typeof staffPricing.pricingCreateRebateProgram>[1]);
    if (!answer.ok) return answer;
    return { ok: true, value: toRebateRow(answer.value) };
  }

  async updateRebate(id: string, body: unknown): Promise<Result<RebateRow>> {
    const answer = await staffPricing.pricingUpdateRebateProgram(this.#client, id, body as Parameters<typeof staffPricing.pricingUpdateRebateProgram>[2]);
    if (!answer.ok) return answer;
    return { ok: true, value: toRebateRow(answer.value) };
  }

  async deleteRebate(id: string, opts: { version: number }): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeleteRebateProgram(this.#client, id, opts);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

  /** One vendor's accrual ledger: the claim side, with its totals. */
  async vendorLedger(vendorId: string): Promise<Result<LedgerRow>> {
    const answer = await staffPricing.pricingVendorRebateLedger(this.#client, vendorId);
    if (!answer.ok) return answer;
    return { ok: true, value: toLedgerRow(answer.value) };
  }

  /** Offer one sale event to the accrual engine; the answer is the server's counts. */
  async accrueRebates(body: unknown): Promise<Result<AccrualResult>> {
    const answer = await staffPricing.pricingAccrueRebates(this.#client, body as Parameters<typeof staffPricing.pricingAccrueRebates>[1]);
    if (!answer.ok) return answer;
    return { ok: true, value: toAccrualResult(answer.value) };
  }

  // -- the units of measure ------------------------------------------------------------------------------

  /** The conversion edges, active ones. */
  async uomConversions(): Promise<Result<readonly UomConversionRow[]>> {
    const answer = await staffPricing.pricingListUOMConversions(this.#client, { limit: 500 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toUomConversionRow) };
  }

  /** Set one edge whole (the route is an upsert by id; an empty id creates). */
  async upsertUom(body: unknown): Promise<Result<UomConversionRow>> {
    const answer = await staffPricing.pricingUpsertUOMConversion(this.#client, body as Parameters<typeof staffPricing.pricingUpsertUOMConversion>[1]);
    if (!answer.ok) return answer;
    return { ok: true, value: toUomConversionRow(answer.value) };
  }

  /** Take one edge off. */
  async deleteUom(id: string): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingDeleteUOMConversion(this.#client, id);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }

  /** Ask the engine what a quantity becomes at the pick face. */
  async planUom(body: unknown): Promise<Result<UomPlanRow>> {
    const answer = await staffPricing.pricingPlanUOM(this.#client, body as Parameters<typeof staffPricing.pricingPlanUOM>[1]);
    if (!answer.ok) return answer;
    return { ok: true, value: toUomPlanRow(answer.value) };
  }

  // -- the floor warning ------------------------------------------------------------------------------

  /** Ask the server what the proposed write would put under the floor. The grading is the server's. */
  async previewFloor(body: PricingFloorPreviewRequest): Promise<Result<FloorPreview>> {
    const answer = await staffPricing.pricingPreviewFloor(this.#client, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toFloorPreview(answer.value) };
  }

  /**
   * Log the editor's confirmation of a floor warning, after the confirmed
   * save, against the record that save wrote. A failure here is an error
   * answer, never a silent drop, and the screen shows the server's verdict.
   */
  async confirmFloor(body: PricingFloorConfirmation): Promise<Result<StatusResult>> {
    const answer = await staffPricing.pricingConfirmFloorWarning(this.#client, body);
    if (!answer.ok) return answer;
    return { ok: true, value: toStatus(answer.value) };
  }
}
