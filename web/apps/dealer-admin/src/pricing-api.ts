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
  type PricingFloorConfirmation,
  type PricingFloorPreviewRequest,
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

/** The one status word the writes and the confirmation answer carry. */
export interface StatusResult {
  readonly status: string;
}

function toStatus(raw: unknown): StatusResult {
  return { status: text(bag(raw).status) };
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
