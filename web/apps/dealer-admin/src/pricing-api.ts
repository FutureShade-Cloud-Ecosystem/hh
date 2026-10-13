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

// -- the price batches --------------------------------------------------------------------------------

/** One price batch: a staged manufacturer update under its effective date. */
export interface BatchRow {
  readonly id: string;
  readonly batch_no: string;
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly profile_id: string;
  readonly status: string;
  readonly effective_date: string;
  readonly source_ref: string;
  readonly line_count: number;
  readonly outlier_count: number;
  readonly created_at: string;
  readonly approved_at: string;
  readonly applied_at: string;
}

export function toBatchRow(raw: unknown): BatchRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    batch_no: text(r.batch_no),
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    profile_id: text(r.profile_id),
    status: text(r.status),
    effective_date: text(r.effective_date),
    source_ref: text(r.source_ref),
    line_count: num(r.line_count),
    outlier_count: num(r.outlier_count),
    created_at: text(r.created_at),
    approved_at: text(r.approved_at),
    applied_at: text(r.applied_at),
  };
}

/** One change line: before and after, with the flags stamped at derivation. */
export interface BatchLineRow {
  readonly id: string;
  readonly product_id: string;
  readonly sku: string;
  readonly before_cost_cents: number | null;
  readonly after_cost_cents: number | null;
  readonly before_retail_cents: number | null;
  readonly after_retail_cents: number | null;
  readonly variance_bps: number | null;
  readonly is_outlier: boolean;
  readonly derived_from: string;
  readonly unmatched: boolean;
}

export function toBatchLineRow(raw: unknown): BatchLineRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    product_id: text(r.product_id),
    sku: text(r.sku),
    before_cost_cents: maybeNum(r.before_cost_cents),
    after_cost_cents: maybeNum(r.after_cost_cents),
    before_retail_cents: maybeNum(r.before_retail_cents),
    after_retail_cents: maybeNum(r.after_retail_cents),
    variance_bps: maybeNum(r.variance_bps),
    is_outlier: bool(r.is_outlier),
    derived_from: text(r.derived_from),
    unmatched: bool(r.unmatched),
  };
}

/** What an import answers: the batch plus the derived preview aids and counts. */
export interface BatchImportResult {
  readonly batch: BatchRow;
  readonly total_lines: number;
  readonly matched_lines: number;
  readonly unmatched: readonly BatchLineRow[];
  readonly outliers: readonly BatchLineRow[];
}

function toBatchImportResult(raw: unknown): BatchImportResult {
  const r = bag(raw);
  return {
    batch: toBatchRow(r.batch),
    total_lines: num(r.total_lines),
    matched_lines: num(r.matched_lines),
    unmatched: list(r.unmatched).map(toBatchLineRow),
    outliers: list(r.outliers).map(toBatchLineRow),
  };
}

/** One import profile: the CSV column mapping for a manufacturer. */
export interface ImportProfileRow {
  readonly id: string;
  readonly vendor_id: string;
  readonly vendor_name: string;
  readonly name: string;
  readonly mapping: Readonly<Record<string, string>>;
  readonly markup_bps: number;
  readonly is_active: boolean;
}

function toImportProfileRow(raw: unknown): ImportProfileRow {
  const r = bag(raw);
  const mapping: Record<string, string> = {};
  const rawMapping = bag(r.mapping);
  for (const key of Object.keys(rawMapping)) {
    if (typeof rawMapping[key] === "string") mapping[key] = rawMapping[key] as string;
  }
  return {
    id: text(r.id),
    vendor_id: text(r.vendor_id),
    vendor_name: text(r.vendor_name),
    name: text(r.name),
    mapping,
    markup_bps: num(r.markup_bps),
    is_active: bool(r.is_active),
  };
}

// -- the price file ------------------------------------------------------------------------------------

/** One manufacturer the price file surface can run for. */
export interface PriceFileVendorRow {
  readonly vendor_id: string;
  readonly vendor_no: string;
  readonly name: string;
  readonly profile_id: string;
  readonly origin: string;
  readonly last_import_at: string;
}

function toPriceFileVendorRow(raw: unknown): PriceFileVendorRow {
  const r = bag(raw);
  return {
    vendor_id: text(r.vendor_id),
    vendor_no: text(r.vendor_no),
    name: text(r.name),
    profile_id: text(r.profile_id),
    origin: text(r.origin),
    last_import_at: text(r.last_import_at),
  };
}

/** The counts over the whole file, as the preview and the apply both answer. */
export interface PriceFileCounts {
  readonly rows: number;
  readonly new: number;
  readonly changed: number;
  readonly unchanged: number;
  readonly unknown: number;
  readonly outliers: number;
  readonly unpriced: number;
}

function toPriceFileCounts(raw: unknown): PriceFileCounts {
  const r = bag(raw);
  return {
    rows: num(r.rows),
    new: num(r.new),
    changed: num(r.changed),
    unchanged: num(r.unchanged),
    unknown: num(r.unknown),
    outliers: num(r.outliers),
    unpriced: num(r.unpriced),
  };
}

/** One row of the file, diffed against the retail anchor. */
export interface PriceFileLine {
  readonly row: number;
  readonly sku: string;
  readonly outcome: string;
  readonly product_name: string;
  readonly current_retail_cents: number | null;
  readonly new_retail_cents: number | null;
  readonly variance_bps: number | null;
  readonly is_outlier: boolean;
  readonly derived_from: string;
  readonly freight_estimated: boolean;
}

function toPriceFileLine(raw: unknown): PriceFileLine {
  const r = bag(raw);
  return {
    row: num(r.row),
    sku: text(r.sku),
    outcome: text(r.outcome),
    product_name: text(r.product_name),
    current_retail_cents: maybeNum(r.current_retail_cents),
    new_retail_cents: maybeNum(r.new_retail_cents),
    variance_bps: maybeNum(r.variance_bps),
    is_outlier: bool(r.is_outlier),
    derived_from: text(r.derived_from),
    freight_estimated: bool(r.freight_estimated),
  };
}

/** The preview: the whole answer to what this file would do; it writes nothing. */
export interface PriceFilePreview {
  readonly vendor_id: string;
  readonly format: string;
  readonly mapping_source: string;
  readonly counts: PriceFileCounts;
  readonly lines: readonly PriceFileLine[];
  readonly truncated: boolean;
  readonly catalog_checked: boolean;
  readonly digest: string;
  readonly costs_visible: boolean;
}

function toPriceFilePreview(raw: unknown): PriceFilePreview {
  const r = bag(raw);
  return {
    vendor_id: text(r.vendor_id),
    format: text(r.format),
    mapping_source: text(r.mapping_source),
    counts: toPriceFileCounts(r.counts),
    lines: list(r.lines).map(toPriceFileLine),
    truncated: bool(r.truncated),
    catalog_checked: bool(r.catalog_checked),
    digest: text(r.digest),
    costs_visible: bool(r.costs_visible),
  };
}

/** What the apply produced: the run, the batch, and whether a profile was minted. */
export interface PriceFileApplyResult {
  readonly run_id: string;
  readonly batch_no: string;
  readonly counts: PriceFileCounts;
  readonly staged: number;
  readonly profile_created: boolean;
}

function toPriceFileApplyResult(raw: unknown): PriceFileApplyResult {
  const r = bag(raw);
  return {
    run_id: text(r.run_id),
    batch_no: text(bag(r.batch).batch_no),
    counts: toPriceFileCounts(r.counts),
    staged: num(r.staged),
    profile_created: bool(r.profile_created),
  };
}

/** One import run of the price file road. */
export interface PriceFileRunRow {
  readonly id: string;
  readonly batch_no: string;
  readonly source_name: string;
  readonly source_format: string;
  readonly source_bytes: number;
  readonly imported_by_name: string;
  readonly created_at: string;
  readonly counts: PriceFileCounts;
}

function toPriceFileRunRow(raw: unknown): PriceFileRunRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    batch_no: text(r.batch_no),
    source_name: text(r.source_name),
    source_format: text(r.source_format),
    source_bytes: num(r.source_bytes),
    imported_by_name: text(r.imported_by_name),
    created_at: text(r.created_at),
    counts: toPriceFileCounts(r.counts),
  };
}

// -- the low margin report ---------------------------------------------------------------------------------

/** One scanned line: a committed document line graded at cost. */
export interface LowMarginScanRow {
  readonly doc_type: string;
  readonly doc_id: string;
  readonly doc_no: string;
  readonly doc_at: string;
  readonly sku: string;
  readonly product_name: string;
  readonly qty_entered: number;
  readonly uom_entered: string;
  readonly unit_price_cents: number;
  readonly margin_bps: number;
  readonly target_bps: number;
  readonly floor_bps: number;
  readonly level: string;
  readonly reason: string;
  readonly salesperson: string;
  readonly cleared_below_floor: boolean;
  readonly cleared_by: string;
}

function toLowMarginScanRow(raw: unknown): LowMarginScanRow {
  const r = bag(raw);
  return {
    doc_type: text(r.doc_type),
    doc_id: text(r.doc_id),
    doc_no: text(r.doc_no),
    doc_at: text(r.doc_at),
    sku: text(r.sku),
    product_name: text(r.product_name),
    qty_entered: num(r.qty_entered),
    uom_entered: text(r.uom_entered),
    unit_price_cents: num(r.unit_price_cents),
    margin_bps: num(r.margin_bps),
    target_bps: num(r.target_bps),
    floor_bps: num(r.floor_bps),
    level: text(r.level),
    reason: text(r.reason),
    salesperson: text(r.salesperson),
    cleared_below_floor: bool(r.cleared_below_floor),
    cleared_by: text(r.cleared_by),
  };
}

/** One graded line of a proposed batch: clean lines are omitted by the server. */
export interface LowMarginEntry {
  readonly product_id: string;
  readonly final_cents: number;
  readonly margin_bps: number;
  readonly target_bps: number;
  readonly floor_bps: number;
  readonly level: string;
  readonly source: string;
  readonly price_uom: string;
  readonly invalid: boolean;
  readonly reason: string;
}

function toLowMarginEntry(raw: unknown): LowMarginEntry {
  const r = bag(raw);
  return {
    product_id: text(r.product_id),
    final_cents: num(r.final_cents),
    margin_bps: num(r.margin_bps),
    target_bps: num(r.target_bps),
    floor_bps: num(r.floor_bps),
    level: text(r.level),
    source: text(r.source),
    price_uom: text(r.price_uom),
    invalid: bool(r.invalid),
    reason: text(r.reason),
  };
}

export interface LowMarginScanQuery {
  readonly from?: string;
  readonly to?: string;
  readonly doc_type?: string;
  readonly cleared_only?: boolean;
  readonly sort?: string;
}

// -- the pricing audit log ---------------------------------------------------------------------------------

/** One pricing log row as the viewer shows it. */
export interface PricingAuditRow {
  readonly id: string;
  readonly entity_type: string;
  readonly entity_id: string;
  readonly action: string;
  readonly field_name: string;
  readonly old_value: string;
  readonly new_value: string;
  readonly user_id: string;
  readonly created_at: string;
  readonly bypass_reason: string;
}

export function toPricingAuditRow(raw: unknown): PricingAuditRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    entity_type: text(r.entity_type),
    entity_id: text(r.entity_id),
    action: text(r.action),
    field_name: text(r.field_name),
    old_value: text(r.old_value),
    new_value: text(r.new_value),
    user_id: text(r.user_id),
    created_at: text(r.created_at),
    bypass_reason: text(r.bypass_reason),
  };
}

export interface PricingAuditQuery {
  readonly entity_type?: string;
  readonly action?: string;
}

// -- the change log --------------------------------------------------------------------------------------

/** One append only change log row: who moved a cost or a retail, from what to what. */
export interface ChangeLogRow {
  readonly id: string;
  readonly product_id: string;
  readonly batch_id: string;
  readonly field: string;
  readonly before_cents: number | null;
  readonly after_cents: number | null;
  readonly effective_date: string;
  readonly changed_by: string;
  readonly created_at: string;
}

export function toChangeLogRow(raw: unknown): ChangeLogRow {
  const r = bag(raw);
  return {
    id: text(r.id),
    product_id: text(r.product_id),
    batch_id: text(r.batch_id),
    field: text(r.field),
    before_cents: maybeNum(r.before_cents),
    after_cents: maybeNum(r.after_cents),
    effective_date: text(r.effective_date),
    changed_by: text(r.changed_by),
    created_at: text(r.created_at),
  };
}

export interface ChangeLogQuery {
  readonly product_id?: string;
  readonly batch_id?: string;
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

  // -- the price batches and import profiles --------------------------------------------------------------

  /** The price batches, filtered by status. */
  async batches(status?: string): Promise<Result<readonly BatchRow[]>> {
    const answer = await staffPricing.pricingListBatches(this.#client, {
      limit: 200,
      status: status === "" || status === undefined ? undefined : (status as "DRAFT" | "STAGED" | "APPROVED" | "APPLIED" | "CANCELLED"),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toBatchRow) };
  }

  async createBatch(body: unknown): Promise<Result<BatchRow>> {
    const answer = await staffPricing.pricingCreateBatch(this.#client, body as Parameters<typeof staffPricing.pricingCreateBatch>[1]);
    if (!answer.ok) return answer;
    return { ok: true, value: toBatchRow(answer.value) };
  }

  /** One batch's change lines. */
  async batchLines(id: string): Promise<Result<readonly BatchLineRow[]>> {
    const answer = await staffPricing.pricingListBatchLines(this.#client, id, { limit: 500 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toBatchLineRow) };
  }

  /** Import a manufacturer CSV into a draft or staged batch. The body is the file's bytes. */
  async importBatch(id: string, csv: string): Promise<Result<BatchImportResult>> {
    const answer = await staffPricing.pricingImportBatch(this.#client, id, csv, "text/csv");
    if (!answer.ok) return answer;
    return { ok: true, value: toBatchImportResult(answer.value) };
  }

  /** The maker check: a second pair of hands approves a staged batch. */
  async approveBatch(id: string): Promise<Result<BatchRow>> {
    const answer = await staffPricing.pricingApproveBatch(this.#client, id);
    if (!answer.ok) return answer;
    return { ok: true, value: toBatchRow(answer.value) };
  }

  /** Write the batch's prices into the retail anchor on or after its date. */
  async applyBatch(id: string): Promise<Result<StatusResult & { readonly applied: number }>> {
    const answer = await staffPricing.pricingApplyBatch(this.#client, id);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    const status = toStatus(r.batch ?? answer.value);
    return { ok: true, value: { status: status.status, applied: num(r.applied) } };
  }

  /** Apply every batch whose effective date has come. */
  async applyDueBatches(): Promise<Result<StatusResult & { readonly applied: number }>> {
    const answer = await staffPricing.pricingApplyDueBatches(this.#client);
    if (!answer.ok) return answer;
    const r = bag(answer.value);
    return { ok: true, value: { status: "applied", applied: num(r.applied) } };
  }

  /** The import profiles: each manufacturer's column mapping. */
  async importProfiles(): Promise<Result<readonly ImportProfileRow[]>> {
    const answer = await staffPricing.pricingListImportProfiles(this.#client, { limit: 200 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toImportProfileRow) };
  }

  /** Rewrite one profile's mapping. */
  async updateImportProfile(id: string, mapping: Readonly<Record<string, string>>): Promise<Result<ImportProfileRow>> {
    const answer = await staffPricing.pricingUpdateImportProfileMapping(this.#client, id, { mapping: { ...mapping } });
    if (!answer.ok) return answer;
    return { ok: true, value: toImportProfileRow(answer.value) };
  }

  // -- the price file ---------------------------------------------------------------------------------

  /** The manufacturers this surface can run for, with their mapping origin. */
  async priceFileVendors(): Promise<Result<readonly PriceFileVendorRow[]>> {
    const answer = await staffPricing.pricingListPriceFileVendors(this.#client, { limit: 200 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toPriceFileVendorRow) };
  }

  /** Preview the diff: writes nothing, answers the digest the apply must echo. */
  async previewPriceFile(vendorId: string, filename: string, file: string, effective?: string): Promise<Result<PriceFilePreview>> {
    const answer = await staffPricing.pricingPreviewPriceFile(
      this.#client,
      { vendor_id: vendorId, file: new Blob([file], { type: "text/csv" }), ...(effective !== undefined && effective !== "" ? { effective_date: effective } : {}) },
      { limit: 100 },
      { fileFilename: filename === "" ? "price-file.csv" : filename },
    );
    if (!answer.ok) return answer;
    return { ok: true, value: toPriceFilePreview(answer.value) };
  }

  /** Stage the file: the digest must echo the preview's, a mismatch is the server's refusal. */
  async applyPriceFile(vendorId: string, filename: string, file: string, digest: string, effective?: string): Promise<Result<PriceFileApplyResult>> {
    const answer = await staffPricing.pricingApplyPriceFile(
      this.#client,
      {
        vendor_id: vendorId,
        file: new Blob([file], { type: "text/csv" }),
        digest,
        ...(effective !== undefined && effective !== "" ? { effective_date: effective } : {}),
      },
      { fileFilename: filename === "" ? "price-file.csv" : filename },
    );
    if (!answer.ok) return answer;
    return { ok: true, value: toPriceFileApplyResult(answer.value) };
  }

  /** The import runs of this road, newest first by the server. */
  async priceFileRuns(): Promise<Result<readonly PriceFileRunRow[]>> {
    const answer = await staffPricing.pricingListPriceFileRuns(this.#client, { limit: 100 });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toPriceFileRunRow) };
  }

  // -- the low margin report --------------------------------------------------------------------------------

  /** The committed lines under target or floor, graded at cost, newest window first. */
  async lowMarginScan(query: LowMarginScanQuery = {}): Promise<Result<readonly LowMarginScanRow[]>> {
    const answer = await staffPricing.pricingLowMarginScan(this.#client, {
      limit: 200,
      from: query.from === "" ? undefined : query.from,
      to: query.to === "" ? undefined : query.to,
      doc_type: query.doc_type === "" || query.doc_type === undefined ? undefined : (query.doc_type as "quotes" | "orders" | "both"),
      cleared_only: query.cleared_only === undefined ? undefined : query.cleared_only,
      sort: query.sort === "" || query.sort === undefined ? undefined : (query.sort as "margin_asc" | "margin_desc"),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toLowMarginScanRow) };
  }

  /** Grade proposed lines through the resolver: the server names the breaches. */
  async lowMarginReport(lines: readonly { customer_id: string; customer_type: string; product_id: string; category_id: string; branch_id: string; qty_entered: number; uom_entered: string; cost_cents: number }[]): Promise<Result<readonly LowMarginEntry[]>> {
    const answer = await staffPricing.pricingLowMarginReport(this.#client, {
      lines: lines.map((l) => ({
        customer_id: l.customer_id,
        customer_type: l.customer_type,
        product_id: l.product_id,
        category_id: l.category_id,
        branch_id: l.branch_id,
        qty_entered: l.qty_entered,
        uom_entered: l.uom_entered,
        cost_cents: l.cost_cents,
        has_approved_override: false,
        doc_ref_type: "quote",
        doc_ref_id: "",
      })),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: (Array.isArray(answer.value) ? answer.value : []).map(toLowMarginEntry) };
  }

  // -- the pricing audit log and the change log -------------------------------------------------------------

  /** The pricing log, filtered by entity and action. The server applies authority. */
  async pricingAudit(query: PricingAuditQuery = {}): Promise<Result<readonly PricingAuditRow[]>> {
    const answer = await staffPricing.pricingListAudit(this.#client, {
      limit: 200,
      entity_type: query.entity_type === "" ? undefined : query.entity_type,
      action: query.action === "" || query.action === undefined ? undefined : (query.action as "CREATE" | "UPDATE" | "DELETE" | "BYPASS"),
    });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toPricingAuditRow) };
  }

  /** The change log, filtered by product or batch. */
  async changeLog(query: ChangeLogQuery = {}): Promise<Result<readonly ChangeLogRow[]>> {
    const answer = await staffPricing.pricingListChangeLog(this.#client, {
      limit: 200,
      product_id: query.product_id === "" ? undefined : query.product_id,
      batch_id: query.batch_id === "" ? undefined : query.batch_id,
    });
    if (!answer.ok) return answer;
    return { ok: true, value: page(answer.value).map(toChangeLogRow) };
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
