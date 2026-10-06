// The Quote Desk's client: the app's one door to the quote routes, one
// method per route the work area and the list speak, every answer validated
// field by field before the app renders a word of it (the till's client
// pattern). Nothing here computes money, holds a price, or decides who may
// do what: the core prices every line, holds every gate, and answers the
// figures; this module reads those figures off the wire and refuses
// everything it cannot prove (a missing field is a refusal, never a
// default).
//
// The one refusal this surface reads as DATA is the typed 409 below-floor
// refusal: the transport flattens every refusal to its error string, and the
// typed body carries no error field, so for that refusal the string IS the
// raw body. The client returns that string unchanged on the error leg and
// the work area parses it through floor-refusal.ts, which validates it field
// by field like every other read.
import { err, ok, type ApiClient, type Result } from "@hardscapehouse/api-client";
import type {
  IdentityApproval,
  IdentityPolicyCheckResponse,
  QuoteAssistExtraction,
  QuoteAssistScopeGaps,
  QuoteBranchStock,
  QuoteFloorApprovalAnswer,
  QuoteFollowUpHistory,
  QuoteHoldExpiryView,
  QuoteLineAvailability,
} from "@hardscapehouse/api-client";
import {
  staffCustomer,
  staffIdentity,
  staffPos,
  staffBranches,
  staffProduct,
  staffQuote,
  staffQuoteassist,
  staffSettings,
} from "@hardscapehouse/api-client";
import { convertBody, type QuoteView } from "./_quote-edit.js";
import type { QuoteFieldChange } from "./_quote-history.js";
import {
  readAcceptance,
  readCatalogHits,
  readComparison,
  readHistory,
  readPolicy,
  readScopeRuleOne,
  readScopeRules,
  readShareLinks,
  type AcceptanceView,
  type CatalogHit,
  type ComparisonRow,
  type QuotePolicyWire,
  type ScopeRuleRow,
  type ShareLinkRow,
} from "./_panel-wire.js";
import {
  readApprovalsPage,
  readAvailability,
  readBranchStocks,
  readExtraction,
  readFloorAnswer,
  readFollowUps,
  readHoldExpiry,
  readHoldsApplied,
  readPolicyCheck,
  readScopeGaps,
} from "./_seam-wire.js";
import type { CurationProfile } from "./_quote-settings.js";
import type { GlanceIn } from "./_quote-workarea.js";
import type { WireItem } from "./quote-surface.js";

/** One page of the quotes list: its rows, and the cursor of the next page or null at the end. */
export interface QuotePage {
  rows: QuoteRow[];
  nextCursor: string | null;
}

export interface BranchRef {
  id: string;
  code: string;
  name: string;
}

/** One row of the quotes list, every field proven. */
export interface QuoteRow {
  id: string;
  quoteNo: string;
  customerName: string;
  branchId: string;
  status: string;
  validUntil: string | null;
  expired: boolean;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  ownerName: string | null;
}

/** The terms panel's read and write shape, field for field the contract's. */
export interface TermsRef {
  quoteId: string;
  depositRequired: boolean;
  depositPct: number;
  includeImages: boolean;
  customerNotes: string;
  contactName: string;
  projectJob: string;
  customerPo: string;
  salespersonId: string;
  salespersonName: string | null;
  fulfillment: "PICKUP" | "DELIVERY";
  deliveryAddress: string;
  requestedDate: string;
  deliveryFeeCents: number;
  depositCents: number;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  lines: Array<{ lineNo: number; discPct: number; preDiscountTotalCents: number; hasImage: boolean; sku: string | null }>;
}

export interface TermsInput {
  depositRequired: boolean;
  depositPct: number;
  includeImages: boolean;
  customerNotes: string;
  contactName: string;
  projectJob: string;
  customerPo: string;
  salespersonId: string;
  fulfillment: "PICKUP" | "DELIVERY";
  deliveryAddress: string;
  requestedDate: string;
  deliveryFeeCents: number;
}

/** The surface config as the work area reads it. */
export interface SurfaceConfig {
  profile: string;
  roleItems: WireItem[];
  userItems: WireItem[];
  workspace: {
    splitPct: number;
    arrangement: "PRIMARY_LEFT" | "PRIMARY_RIGHT";
    primaryCollapsed: boolean;
    secondaryCollapsed: boolean;
    costHidden: boolean;
    openRef: string;
    source: string;
  };
  policy: {
    costVisibility: string;
    defaultValidityDays: number;
    defaultDepositPct: number;
    lineDiscountsEnabled: boolean;
    sendEmailEnabled: boolean;
    sendSmsEnabled: boolean;
    includeProductImages: boolean;
    quickChipSet: string[];
    effectiveChips: string[];
    workspaceSplitPct: number;
    workspaceArrangement: "PRIMARY_LEFT" | "PRIMARY_RIGHT";
  } | null;
}

// ── field readers: a response field either proves itself or the answer is
// refused as data, like every other refusal in this app.

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function stringAt(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  return typeof value === "string" && value !== "" ? value : null;
}

function integerAt(record: Record<string, unknown>, key: string): number | null {
  const value = record[key];
  return typeof value === "number" && Number.isInteger(value) ? value : null;
}

function booleanAt(record: Record<string, unknown>, key: string): boolean | null {
  const value = record[key];
  return typeof value === "boolean" ? value : null;
}

function stringArrayAt(record: Record<string, unknown>, key: string): string[] | null {
  const value = record[key];
  if (!Array.isArray(value)) return null;
  const out: string[] = [];
  for (const item of value) {
    if (typeof item !== "string" || item === "") return null;
    out.push(item);
  }
  return out;
}

function validateView(value: unknown): QuoteView | null {
  if (!isRecord(value)) return null;
  const linesRaw = value["lines"];
  if (!Array.isArray(linesRaw)) return null;
  const lines: QuoteView["lines"] = [];
  for (const line of linesRaw) {
    if (!isRecord(line)) return null;
    const lineNo = integerAt(line, "line_no");
    const productId = stringAt(line, "product_id");
    const description = stringAt(line, "description");
    const qtyEntered = stringAt(line, "qty_entered");
    const uomEntered = stringAt(line, "uom_entered");
    const qtyBase = stringAt(line, "qty_base");
    const unitPriceCents = integerAt(line, "unit_price_cents");
    const priceUom = stringAt(line, "price_uom");
    const lineTotalCents = integerAt(line, "line_total_cents");
    if (
      lineNo === null || productId === null || description === null || qtyEntered === null || uomEntered === null ||
      qtyBase === null || unitPriceCents === null || priceUom === null || lineTotalCents === null
    ) {
      return null;
    }
    lines.push({
      line_no: lineNo,
      product_id: productId,
      description,
      qty_entered: qtyEntered,
      uom_entered: uomEntered,
      qty_base: qtyBase,
      unit_price_cents: unitPriceCents,
      price_uom: priceUom,
      line_total_cents: lineTotalCents,
      ...(line["rule_price_below_floor"] === true ? { rule_price_below_floor: true } : {}),
      ...(typeof line["floor_cleared_by"] === "string" ? { floor_cleared_by: line["floor_cleared_by"] } : {}),
    });
  }
  const editableRaw = value["editable_fields"];
  if (!Array.isArray(editableRaw)) return null;
  const editable: string[] = [];
  for (const field of editableRaw) {
    if (typeof field !== "string") return null;
    editable.push(field);
  }
  const id = stringAt(value, "id");
  const quoteNo = stringAt(value, "quote_no");
  const customerId = stringAt(value, "customer_id");
  const customerName = stringAt(value, "customer_name");
  const branchId = stringAt(value, "branch_id");
  const status = stringAt(value, "status");
  const subtotalCents = integerAt(value, "subtotal_cents");
  const taxCents = integerAt(value, "tax_cents");
  const totalCents = integerAt(value, "total_cents");
  const updatedAt = stringAt(value, "updated_at");
  if (
    id === null || quoteNo === null || customerId === null || customerName === null || branchId === null ||
    status === null || subtotalCents === null || taxCents === null || totalCents === null || updatedAt === null
  ) {
    return null;
  }
  const canConvert = booleanAt(value, "can_convert");
  return {
    id,
    quote_no: quoteNo,
    customer_id: customerId,
    customer_name: customerName,
    branch_id: branchId,
    status,
    valid_until: stringAt(value, "valid_until") ?? undefined,
    expired: value["expired"] === true,
    editable_fields: editable,
    can_convert: canConvert === true,
    owner_id: stringAt(value, "owner_id") ?? undefined,
    owner_name: stringAt(value, "owner_name") ?? undefined,
    lines,
    subtotal_cents: subtotalCents,
    tax_cents: taxCents,
    total_cents: totalCents,
    updated_at: updatedAt,
  };
}

/**
 * The terms answer, field by field. delivery_fee_cents, deposit_cents and
 * fulfillment are required in the contract: a missing one refuses the answer
 * rather than defaulting to zero or to PICKUP.
 */
export function validateTerms(value: unknown): TermsRef | null {
  if (!isRecord(value)) return null;
  const linesRaw = value["lines"];
  if (!Array.isArray(linesRaw)) return null;
  const lines: TermsRef["lines"] = [];
  for (const line of linesRaw) {
    if (!isRecord(line)) return null;
    const lineNo = integerAt(line, "line_no");
    const discPct = integerAt(line, "disc_pct");
    const preDiscountTotalCents = integerAt(line, "pre_discount_total_cents");
    if (lineNo === null || discPct === null || preDiscountTotalCents === null) return null;
    lines.push({
      lineNo,
      discPct,
      preDiscountTotalCents,
      hasImage: line["has_image"] === true,
      sku: stringAt(line, "sku"),
    });
  }
  const quoteId = stringAt(value, "quote_id");
  const depositRequired = booleanAt(value, "deposit_required");
  const depositPct = integerAt(value, "deposit_pct");
  const includeImages = booleanAt(value, "include_images");
  const fulfillmentRaw = stringAt(value, "fulfillment");
  const subtotalCents = integerAt(value, "subtotal_cents");
  const taxCents = integerAt(value, "tax_cents");
  const totalCents = integerAt(value, "total_cents");
  const depositCents = integerAt(value, "deposit_cents");
  const deliveryFeeCents = integerAt(value, "delivery_fee_cents");
  if (
    quoteId === null || depositRequired === null || depositPct === null || includeImages === null ||
    fulfillmentRaw === null || (fulfillmentRaw !== "PICKUP" && fulfillmentRaw !== "DELIVERY") ||
    deliveryFeeCents === null || depositCents === null ||
    subtotalCents === null || taxCents === null || totalCents === null
  ) {
    return null;
  }
  return {
    quoteId,
    depositRequired,
    depositPct,
    includeImages,
    customerNotes: typeof value["customer_notes"] === "string" ? value["customer_notes"] : "",
    contactName: typeof value["contact_name"] === "string" ? value["contact_name"] : "",
    projectJob: typeof value["project_job"] === "string" ? value["project_job"] : "",
    customerPo: typeof value["customer_po"] === "string" ? value["customer_po"] : "",
    salespersonId: typeof value["salesperson_id"] === "string" ? value["salesperson_id"] : "",
    salespersonName: stringAt(value, "salesperson_name"),
    fulfillment: fulfillmentRaw,
    deliveryAddress: typeof value["delivery_address"] === "string" ? value["delivery_address"] : "",
    requestedDate: typeof value["requested_date"] === "string" ? value["requested_date"] : "",
    deliveryFeeCents,
    depositCents,
    subtotalCents,
    taxCents,
    totalCents,
    lines,
  };
}

function validateConfig(value: unknown): SurfaceConfig | null {
  if (!isRecord(value)) return null;
  const profile = stringAt(value, "profile");
  const roleItemsRaw = value["role_items"];
  const userItemsRaw = value["user_items"];
  const workspaceRaw = value["workspace"];
  if (profile === null || !Array.isArray(roleItemsRaw) || !Array.isArray(userItemsRaw) || !isRecord(workspaceRaw)) {
    return null;
  }
  const readItems = (rows: unknown[]): WireItem[] | null => {
    const out: WireItem[] = [];
    for (const row of rows) {
      if (!isRecord(row)) return null;
      const kind = stringAt(row, "kind");
      const id = stringAt(row, "id");
      const position = integerAt(row, "position");
      if (kind === null || id === null || position === null) return null;
      out.push({ kind: kind as WireItem["kind"], id, position, visible: row["visible"] !== false });
    }
    return out;
  };
  const roleItems = readItems(roleItemsRaw);
  const userItems = readItems(userItemsRaw);
  const splitPct = integerAt(workspaceRaw, "split_pct");
  const arrangementRaw = stringAt(workspaceRaw, "arrangement");
  if (roleItems === null || userItems === null || splitPct === null || arrangementRaw === null) return null;
  let policy: SurfaceConfig["policy"] = null;
  const policyRaw = value["policy"];
  if (isRecord(policyRaw)) {
    const chips = stringArrayAt(policyRaw, "quick_chip_set") ?? [];
    const effective = stringArrayAt(policyRaw, "effective_chips") ?? chips;
    const defaultValidityDays = integerAt(policyRaw, "default_validity_days");
    const defaultDepositPct = integerAt(policyRaw, "default_deposit_pct");
    const workspaceSplitPct = integerAt(policyRaw, "workspace_split_pct");
    if (defaultValidityDays === null || defaultDepositPct === null || workspaceSplitPct === null) return null;
    policy = {
      costVisibility: typeof policyRaw["cost_visibility"] === "string" ? policyRaw["cost_visibility"] : "",
      defaultValidityDays,
      defaultDepositPct,
      lineDiscountsEnabled: policyRaw["line_discounts_enabled"] !== false,
      sendEmailEnabled: policyRaw["send_email_enabled"] !== false,
      sendSmsEnabled: policyRaw["send_sms_enabled"] === true,
      includeProductImages: policyRaw["include_product_images"] !== false,
      quickChipSet: chips,
      effectiveChips: effective,
      workspaceSplitPct,
      workspaceArrangement: policyRaw["workspace_arrangement"] === "PRIMARY_RIGHT" ? "PRIMARY_RIGHT" : "PRIMARY_LEFT",
    };
  }
  return {
    profile,
    roleItems,
    userItems,
    workspace: {
      splitPct,
      arrangement: arrangementRaw === "PRIMARY_RIGHT" ? "PRIMARY_RIGHT" : "PRIMARY_LEFT",
      primaryCollapsed: workspaceRaw["primary_collapsed"] === true,
      secondaryCollapsed: workspaceRaw["secondary_collapsed"] === true,
      costHidden: workspaceRaw["cost_hidden"] === true,
      openRef: typeof workspaceRaw["open_ref"] === "string" ? workspaceRaw["open_ref"] : "",
      source: typeof workspaceRaw["source"] === "string" ? workspaceRaw["source"] : "dealer",
    },
    policy,
  };
}

/** The write outcome the line edits share: the totals, always, and the line when one answers. */
export interface EditOutcome {
  subtotalCents: number;
  totalCents: number;
  status: string;
}

function validateEditOutcome(value: unknown): EditOutcome | null {
  if (!isRecord(value)) return null;
  const totals = value["totals"];
  const status = stringAt(value, "status");
  if (!isRecord(totals) || status === null) return null;
  const subtotalCents = integerAt(totals, "SubtotalCents");
  const totalCents = integerAt(totals, "TotalCents");
  if (subtotalCents === null || totalCents === null) return null;
  return { subtotalCents, totalCents, status };
}

const MAX_APPROVAL_PAGES = 40;

/** The account facts the standing step reads off the customer search. */
export interface StandingRead {
  name: string;
  paymentTermsDays: number;
  creditLimitCents: number;
  openBalanceCents: number;
}

/** The payment door's answer: the payment, the balance now on the invoice, and the order (made by this very action when converted). */
export interface OrderPaymentAnswer {
  paymentNo: string;
  amountCents: number;
  balanceCents: number;
  orderId: string;
  orderNo: string;
  converted: boolean;
  replayed: boolean;
}

/** The counter's account standing read: the past due position and the hold flag, as the server answers them. */
export interface AccountStandingRead {
  onHold: boolean;
  pastDueCents: number | null;
  oldestBucket: string | null;
}

export class QdClient {
  readonly #client: ApiClient;
  readonly #callerId: () => string;

  /** callerId reads the signed in person's own user id from the session, "" when none is carried. */
  constructor(client: ApiClient, callerId: () => string = () => "") {
    this.#client = client;
    this.#callerId = callerId;
  }

  /** The caller's own user id as the session carries it; "" when it does not. */
  callerId(): string {
    return this.#callerId();
  }

  /**
   * The branch the login works: the branches route is fenced to the caller's
   * own grants server-side, so the desk takes the one row that comes back.
   * An empty list or a row this app cannot read is a refusal in words, never
   * an invented branch.
   */
  async resolveBranch(): Promise<Result<BranchRef>> {
    const result = await staffBranches.branchesList(this.#client);
    if (!result.ok) return result;
    const rows = result.value as unknown;
    if (!Array.isArray(rows) || rows.length !== 1) {
      return err("the branch list did not answer exactly the one branch this login reaches");
    }
    const row = rows[0] as unknown;
    if (!isRecord(row)) return err("the branch row is not readable");
    const id = stringAt(row, "id");
    const code = stringAt(row, "code");
    const name = stringAt(row, "name");
    if (id === null || code === null || name === null) {
      return err("the branch row is missing its id, code or name");
    }
    return ok({ id, code, name });
  }

  /**
   * One page of the quotes list, with the status filter the server itself
   * applies. The core pages (newest first, fifty by default): next_cursor,
   * when the server sends one, is handed back so the list can offer more.
   */
  async listQuotes(status: string, cursor = ""): Promise<Result<QuotePage>> {
    const opts: { status?: "DRAFT" | "SENT" | "ACCEPTED" | "CONVERTED" | "EXPIRED" | "LOST"; cursor?: string } = {};
    if (status !== "") opts.status = status as "DRAFT" | "SENT" | "ACCEPTED" | "CONVERTED" | "EXPIRED" | "LOST";
    if (cursor !== "") opts.cursor = cursor;
    const result = await staffQuote.quoteListQuotes(this.#client, Object.keys(opts).length === 0 ? undefined : opts);
    if (!result.ok) return result;
    const value = result.value as unknown;
    const items = isRecord(value) ? value["items"] : value;
    if (!Array.isArray(items)) return err("the quotes list did not answer a page or a list");
    const nextRaw = isRecord(value) ? value["next_cursor"] : null;
    if (nextRaw !== undefined && nextRaw !== null && typeof nextRaw !== "string") return err("the quotes list's next page cursor is not readable");
    const nextCursor = typeof nextRaw === "string" && nextRaw !== "" ? nextRaw : null;
    const rows: QuoteRow[] = [];
    for (const item of items) {
      if (!isRecord(item)) return err("a quote row is not readable: the list is refused whole rather than shown short");
      const id = stringAt(item, "id");
      const quoteNo = stringAt(item, "quote_no");
      const customerName = stringAt(item, "customer_name");
      const branchId = stringAt(item, "branch_id");
      const statusWord = stringAt(item, "status");
      const subtotalCents = integerAt(item, "subtotal_cents");
      const taxCents = integerAt(item, "tax_cents");
      const totalCents = integerAt(item, "total_cents");
      if (
        id === null || quoteNo === null || customerName === null || branchId === null || statusWord === null ||
        subtotalCents === null || taxCents === null || totalCents === null
      ) {
        return err("a quote row is missing its number, customer or figures: the list is refused whole");
      }
      rows.push({
        id,
        quoteNo,
        customerName,
        branchId,
        status: statusWord,
        validUntil: stringAt(item, "valid_until"),
        expired: item["expired"] === true,
        subtotalCents,
        taxCents,
        totalCents,
        ownerName: stringAt(item, "owner_name"),
      });
    }
    return ok({ rows, nextCursor });
  }

  /** One quote's read: the offer itself (editable_fields, can_convert) rides it. */
  async readQuote(id: string): Promise<Result<QuoteView>> {
    const result = await staffQuote.quoteReadQuote(this.#client, id);
    if (!result.ok) return result;
    const view = validateView(result.value);
    return view === null ? err("the quote read is not readable") : ok(view);
  }

  /** Create a quote: the server sets the salesperson to the caller. */
  async createQuote(customerId: string, branchId: string, validUntil: string): Promise<Result<string>> {
    const result = await staffQuote.quoteCreateQuote(this.#client, {
      customer_id: customerId,
      branch_id: branchId,
      ...(validUntil === "" ? {} : { valid_until: validUntil }),
    });
    if (!result.ok) return result;
    const view = validateView(result.value);
    if (view === null) return err("the created quote is not readable");
    return ok(view.id);
  }

  /** Add a line: quantities and units only, the engine prices server-side. */
  async addLine(quoteId: string, productId: string, qty: string, uom: string): Promise<Result<EditOutcome>> {
    const result = await staffQuote.quoteAddQuoteLine(this.#client, quoteId, {
      product_id: productId,
      qty_entered: qty,
      uom_entered: uom,
    });
    if (!result.ok) return result;
    const outcome = validateEditOutcome(result.value);
    return outcome === null ? err("the added line's answer is not readable") : ok(outcome);
  }

  /**
   * Edit a line: the quantity arm or the discount arm. The discount arm is
   * where the floor decision runs, so a 409 may arrive carrying the typed
   * refusal body as the error string; the caller parses it through
   * floor-refusal.ts.
   */
  async editLine(
    quoteId: string,
    lineNo: number,
    patch: { qty?: string; discPct?: number },
  ): Promise<Result<EditOutcome>> {
    const body: Record<string, unknown> = {};
    if (patch.qty !== undefined) body["qty_entered"] = patch.qty;
    if (patch.discPct !== undefined) body["disc_pct"] = patch.discPct;
    const result = await staffQuote.quoteEditQuoteLine(this.#client, quoteId, lineNo, body);
    if (!result.ok) return result;
    const outcome = validateEditOutcome(result.value);
    return outcome === null ? err("the edited line's answer is not readable") : ok(outcome);
  }

  /** Remove a line. */
  async removeLine(quoteId: string, lineNo: number): Promise<Result<EditOutcome>> {
    const result = await staffQuote.quoteRemoveQuoteLine(this.#client, quoteId, lineNo);
    if (!result.ok) return result;
    const outcome = validateEditOutcome(result.value);
    return outcome === null ? err("the removed line's answer is not readable") : ok(outcome);
  }

  /** The terms read: the right panel's every field, and the per-line discount figures. */
  async readTerms(quoteId: string): Promise<Result<TermsRef>> {
    const result = await staffQuote.quoteReadQuoteTerms(this.#client, quoteId);
    if (!result.ok) return result;
    const terms = validateTerms(result.value);
    return terms === null ? err("the terms read is not readable") : ok(terms);
  }

  /** The terms write: one PUT carrying every field, the server restamps the totals. */
  async putTerms(quoteId: string, input: TermsInput): Promise<Result<TermsRef>> {
    const result = await staffQuote.quotePutQuoteTerms(this.#client, quoteId, {
      deposit_required: input.depositRequired,
      deposit_pct: input.depositPct,
      include_images: input.includeImages,
      customer_notes: input.customerNotes,
      contact_name: input.contactName,
      project_job: input.projectJob,
      customer_po: input.customerPo,
      salesperson_id: input.salespersonId,
      fulfillment: input.fulfillment,
      delivery_address: input.deliveryAddress,
      requested_date: input.requestedDate,
      delivery_fee_cents: input.deliveryFeeCents,
    });
    if (!result.ok) return result;
    const terms = validateTerms(result.value);
    return terms === null ? err("the saved terms are not readable") : ok(terms);
  }

  /** The expiry write. */
  async setValidUntil(quoteId: string, validUntil: string): Promise<Result<EditOutcome>> {
    const result = await staffQuote.quoteSetValidUntil(this.#client, quoteId, {
      ...(validUntil === "" ? {} : { valid_until: validUntil }),
    });
    if (!result.ok) return result;
    const outcome = validateEditOutcome(result.value);
    return outcome === null ? err("the expiry write's answer is not readable") : ok(outcome);
  }

  /**
   * The margin glance. The glance owns the gate: when it answers nothing at
   * all (the empty object the server sends a caller it does not answer),
   * every field stays byte absent and the margin surface vanishes, value
   * driven, whatever the configuration decides. That is a real answer, not
   * an error.
   */
  async marginGlance(quoteId: string): Promise<Result<GlanceIn>> {
    const result = await staffQuote.quoteMarginGlance(this.#client, quoteId);
    if (!result.ok) return result;
    const value = result.value as unknown;
    if (!isRecord(value)) return err("the margin glance did not answer an object");
    if (value["margin_bps"] === undefined) return ok({});
    const bps = value["margin_bps"];
    if (typeof bps !== "number" || !Number.isInteger(bps)) {
      return err("the margin glance's own figure is not readable");
    }
    const linesRaw = value["lines"];
    const lines: GlanceIn["lines"] = Array.isArray(linesRaw)
      ? linesRaw.map((line) => {
          if (!isRecord(line)) return {};
          const row: { line_no?: number; cost_cents?: number; margin_bps?: number; est_profit_cents?: number } = {};
          const lineNo = integerAt(line, "line_no");
          if (lineNo !== null) row.line_no = lineNo;
          const cost = integerAt(line, "cost_cents");
          if (cost !== null) row.cost_cents = cost;
          const margin = integerAt(line, "margin_bps");
          if (margin !== null) row.margin_bps = margin;
          const profit = integerAt(line, "est_profit_cents");
          if (profit !== null) row.est_profit_cents = profit;
          return row;
        })
      : undefined;
    return ok({ lines, margin_bps: bps, partial: value["partial"] === true });
  }

  /** Mint the share link: minting IS the email send, DRAFT moves to SENT. */
  async mintShareLink(quoteId: string): Promise<Result<string>> {
    const result = await staffQuote.quoteMintShareLink(this.#client, quoteId, {});
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the minted link is not readable");
    const token = stringAt(record, "token");
    return token === null ? err("the minted link carried no token") : ok(token);
  }

  /**
   * The text leg. A refusal is data: the outcome sentence names it with the
   * server's own words, so the caller keeps the ok leg and the error string
   * in one answer.
   */
  async shareBySMS(quoteId: string): Promise<Result<true>> {
    const result = await staffQuote.quoteShareBySMS(this.#client, quoteId, {});
    return result.ok ? ok(true) : result;
  }

  /**
   * Convert an accepted quote to its order. The fulfilment choice rides only
   * when one was made (convertBody), so the default request says nothing and
   * the server's own default applies.
   */
  async convertQuote(quoteId: string, fulfilment = ""): Promise<Result<{ orderId: string; orderNo: string }>> {
    const result = await staffQuote.quoteConvertQuote(this.#client, quoteId, convertBody(fulfilment));
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the conversion answer is not readable");
    const orderId = stringAt(record, "order_id");
    const orderNo = stringAt(record, "order_no");
    if (orderId === null || orderNo === null) return err("the conversion answer is missing its order number");
    return ok({ orderId, orderNo });
  }

  /**
   * The guided surface's conversion: the chosen fulfilment rides explicitly, both of the contract's
   * two values (the bare button's convertBody sends only Will-call and leaves Pickup to the default).
   */
  async convertWithFulfilment(quoteId: string, fulfillment: "PICKUP" | "WILL_CALL"): Promise<Result<{ orderId: string; orderNo: string }>> {
    const result = await staffQuote.quoteConvertQuote(this.#client, quoteId, { fulfillment });
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the conversion answer is not readable");
    const orderId = stringAt(record, "order_id");
    const orderNo = stringAt(record, "order_no");
    if (orderId === null || orderNo === null) return err("the conversion answer is missing its order number");
    return ok({ orderId, orderNo });
  }

  /**
   * One payment against the converted quote's order. The contract's key rides the body, and the same
   * key rides the Idempotency-Key header, so a retry of this one payment posts nothing twice.
   */
  async takeOrderPayment(
    quoteId: string,
    input: { method: "CASH" | "CARD" | "CHEQUE" | "EFT"; amountCents: number; reference: string; idempotencyKey: string },
  ): Promise<Result<OrderPaymentAnswer>> {
    const result = await this.#client.request<unknown>({
      method: "POST",
      path: "/api/v1/quotes/{id}/order-payment",
      pathParams: { id: quoteId },
      body: { method: input.method, amount_cents: input.amountCents, reference: input.reference, idempotency_key: input.idempotencyKey },
      idempotencyKey: input.idempotencyKey,
      operationId: "quoteTakeOrderPayment",
    });
    if (!result.ok) return result;
    const record = result.value;
    if (!isRecord(record)) return err("the payment answer is not readable");
    const paymentNo = stringAt(record, "payment_no");
    const amountCents = integerAt(record, "amount_cents");
    const balanceCents = integerAt(record, "balance_cents");
    const orderId = stringAt(record, "order_id");
    const orderNo = stringAt(record, "order_no");
    if (paymentNo === null || amountCents === null || balanceCents === null || orderId === null || orderNo === null) {
      return err("the payment answer is missing its figures");
    }
    return ok({ paymentNo, amountCents, balanceCents, orderId, orderNo, converted: record["converted"] === true, replayed: record["replayed"] === true });
  }

  /**
   * The customer's credit posture for the standing step, from the customer search the desk already
   * speaks (the account's terms, limit and open balance). The row is matched by id; an answer with no
   * such row is null, never a guess.
   */
  async customerStanding(customerId: string, customerName: string): Promise<Result<StandingRead | null>> {
    const result = await staffCustomer.customerSearch(this.#client, { q: customerName, limit: 20 });
    if (!result.ok) return result;
    const rows = result.value as unknown;
    if (!Array.isArray(rows)) return err("the customer search answer is not readable");
    for (const row of rows) {
      if (!isRecord(row) || stringAt(row, "id") !== customerId) continue;
      const terms = integerAt(row, "payment_terms_days");
      const limit = integerAt(row, "credit_limit_cents");
      const open = integerAt(row, "open_balance_cents");
      if (terms === null || limit === null || open === null) return err("the customer's credit facts are not readable");
      return ok({ name: customerName, paymentTermsDays: terms, creditLimitCents: limit, openBalanceCents: open });
    }
    return ok(null);
  }

  /**
   * The counter's account standing for one customer: the past due amount, the oldest bucket and the
   * hold flag. The contract answers the empty object for a walk-in, a current account, a caller it
   * will not tell and an unreadable read alike, so an absent hold is never a promise of none.
   */
  async accountStanding(branchId: string, customerId: string): Promise<Result<AccountStandingRead>> {
    const result = await staffPos.posAccountStanding(this.#client, { branch_id: branchId, customer_id: customerId });
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the account standing answer is not readable");
    return ok({
      onHold: record["on_hold"] === true,
      pastDueCents: integerAt(record, "past_due_cents"),
      oldestBucket: stringAt(record, "oldest_bucket"),
    });
  }

  /**
   * The request door the refusal dialog files through (the QD-1 operation).
   * The answer is the contract's own: whether a request was filed, whether
   * the caller may clear its own price, and the request's state as the
   * server holds it. The app never decides either fact.
   */
  async requestFloorApproval(
    quoteId: string,
    lineNo: number,
    discPct: number,
    note: string,
  ): Promise<Result<QuoteFloorApprovalAnswer>> {
    const result = await staffQuote.quoteRequestFloorApproval(this.#client, quoteId, lineNo, {
      disc_pct: discPct,
      ...(note === "" ? {} : { note }),
    });
    if (!result.ok) return result;
    const answer = readFloorAnswer(result.value);
    return answer === null ? err("the request door's answer is not readable") : ok(answer);
  }

  /** The surface config read: the curation rows, the workspace row and the policy. */
  async surfaceConfig(branchId: string, profile?: CurationProfile): Promise<Result<SurfaceConfig>> {
    const result = await staffSettings.settingsGetSurfaceConfig(this.#client, "quotes.builder", {
      ...(branchId === "" ? {} : { branch_id: branchId }),
      ...(profile === undefined ? {} : { profile }),
    });
    if (!result.ok) return result;
    const config = validateConfig(result.value);
    return config === null ? err("the surface config is not readable") : ok(config);
  }

  /** Save this person's workspace row (the layout store's debounced write). */
  async putMyWorkspace(input: {
    splitPct: number;
    arrangement: "PRIMARY_LEFT" | "PRIMARY_RIGHT";
    primaryCollapsed: boolean;
    secondaryCollapsed: boolean;
    costHidden: boolean;
    openRef: string;
  }): Promise<Result<true>> {
    const result = await staffSettings.settingsPutMyWorkspace(this.#client, "quotes.builder", {
      split_pct: input.splitPct,
      arrangement: input.arrangement,
      primary_collapsed: input.primaryCollapsed,
      secondary_collapsed: input.secondaryCollapsed,
      cost_hidden: input.costHidden,
      ...(input.openRef === "" ? {} : { open_ref: input.openRef }),
      idempotency_key: crypto.randomUUID(),
    });
    return result.ok ? ok(true) : result;
  }

  /** Reset this person's workspace row to the dealer default. */
  async deleteMyWorkspace(): Promise<Result<true>> {
    const result = await staffSettings.settingsDeleteMyWorkspace(this.#client, "quotes.builder");
    return result.ok ? ok(true) : result;
  }

  /** Save this person's column curation (whole or absent, per item kind). */
  async putMyItems(items: WireItem[]): Promise<Result<true>> {
    const result = await staffSettings.settingsPutMyItems(this.#client, "quotes.builder", {
      items: items.map((item, index) => ({ kind: item.kind, id: item.id, position: index, visible: item.visible })),
    });
    return result.ok ? ok(true) : result;
  }

  /** Reset this person's column curation. */
  async deleteMyItems(): Promise<Result<true>> {
    const result = await staffSettings.settingsDeleteMyItems(this.#client, "quotes.builder");
    return result.ok ? ok(true) : result;
  }

  // ── the edit panel's reads and writes ──────────────────────────────────────

  /** The staff catalogue search behind the add line box and the compare box. */
  async searchProducts(query: string, limit: number): Promise<Result<CatalogHit[]>> {
    const result = await staffProduct.productSearch(this.#client, { q: query, limit });
    if (!result.ok) return result;
    const hits = readCatalogHits(result.value);
    return hits === null ? err("the catalogue search is not readable") : ok(hits);
  }

  /** The quote's share links. A refusal here is data; the list simply stays empty. */
  async listShareLinks(quoteId: string): Promise<Result<ShareLinkRow[]>> {
    const result = await staffQuote.quoteListShareLinks(this.#client, quoteId);
    if (!result.ok) return result;
    const rows = readShareLinks(result.value);
    return rows === null ? err("the share link list is not readable") : ok(rows);
  }

  /** Revoke one share link. */
  async revokeShareLink(quoteId: string, linkId: string): Promise<Result<true>> {
    const result = await staffQuote.quoteRevokeShareLink(this.#client, quoteId, linkId);
    return result.ok ? ok(true) : result;
  }

  /** Score alternate products against one line. Read only: the server writes nothing. */
  async compareLine(quoteId: string, lineNo: number, productIds: string[]): Promise<Result<ComparisonRow[]>> {
    const result = await staffQuote.quoteCompareQuoteLine(this.#client, quoteId, lineNo, {
      alternates: productIds.map((id) => ({ product_id: id })),
    });
    if (!result.ok) return result;
    const rows = readComparison(result.value);
    return rows === null ? err("the price comparison is not readable") : ok(rows);
  }

  /** The change trail, oldest first, exactly as the server sends it. */
  async editHistory(quoteId: string): Promise<Result<QuoteFieldChange[]>> {
    const result = await staffQuote.quoteQuoteEditHistory(this.#client, quoteId);
    if (!result.ok) return result;
    const rows = readHistory(result.value);
    return rows === null ? err("the change history is not readable") : ok(rows);
  }

  // ── the settings screens ───────────────────────────────────────────────────

  /** The effective quote policy for a branch (an empty branch resolves the dealer default). */
  async readPolicy(branchId: string): Promise<Result<QuotePolicyWire>> {
    const result = await staffQuote.quoteReadQuotePolicy(this.#client, branchId === "" ? undefined : { branch_id: branchId });
    if (!result.ok) return result;
    const policy = readPolicy(result.value);
    return policy === null ? err("the quote policy is not readable") : ok(policy);
  }

  /** Write one policy row. The server decides whose row this caller may write. */
  async putPolicy(body: QuotePolicyWire): Promise<Result<QuotePolicyWire>> {
    const result = await staffQuote.quotePutQuotePolicy(this.#client, body);
    if (!result.ok) return result;
    const policy = readPolicy(result.value);
    return policy === null ? err("the saved quote policy is not readable") : ok(policy);
  }

  /** The dealer's one acceptance switch. */
  async getAcceptance(): Promise<Result<AcceptanceView>> {
    const result = await staffQuote.quoteGetAcceptanceSettings(this.#client);
    if (!result.ok) return result;
    const view = readAcceptance(result.value);
    return view === null ? err("the acceptance settings are not readable") : ok(view);
  }

  async setAcceptance(allowMultiAccept: boolean): Promise<Result<AcceptanceView>> {
    const result = await staffQuote.quoteSetAcceptanceSettings(this.#client, { allow_multi_accept: allowMultiAccept });
    if (!result.ok) return result;
    const view = readAcceptance(result.value);
    return view === null ? err("the saved acceptance settings are not readable") : ok(view);
  }

  /** The scope rule table, switched-off rules included. */
  async listScopeRules(): Promise<Result<ScopeRuleRow[]>> {
    const result = await staffQuoteassist.quoteAssistListScopeRules(this.#client);
    if (!result.ok) return result;
    const rules = readScopeRules(result.value);
    return rules === null ? err("the scope rules are not readable") : ok(rules);
  }

  /** Switch one rule. Only the switch: the wording, categories and order are not editable here. */
  async setScopeRule(code: string, enabled: boolean): Promise<Result<ScopeRuleRow>> {
    const result = await staffQuoteassist.quoteAssistPatchScopeRule(this.#client, code, { enabled });
    if (!result.ok) return result;
    const rule = readScopeRuleOne(result.value);
    return rule === null ? err("the saved scope rule is not readable") : ok(rule);
  }

  /** Save the dealer's curation for one role profile. */
  async putRoleItems(profile: CurationProfile, items: WireItem[]): Promise<Result<true>> {
    const result = await staffSettings.settingsPutRoleItems(this.#client, "quotes.builder", {
      profile,
      items: items.map((item) => ({ kind: item.kind, id: item.id, position: item.position, visible: item.visible })),
    });
    return result.ok ? ok(true) : result;
  }

  // ── the third coder's panels: assist, follow-ups, holds, availability, the
  // floor check and the approvals list ────────────────────────────────────────

  /** Read a pasted job description into proposed lines. Nothing is written. */
  async assistExtract(text: string, branchId: string, useAi: boolean): Promise<Result<QuoteAssistExtraction>> {
    const result = await staffQuoteassist.quoteAssistExtract(this.#client, { text, branch_id: branchId, use_ai: useAi });
    if (!result.ok) return result;
    const extraction = readExtraction(result.value);
    return extraction === null ? err("the assist answer is not readable") : ok(extraction);
  }

  /** What the yard's scope rules say the quote is missing. */
  async assistScopeGaps(quoteId: string): Promise<Result<QuoteAssistScopeGaps>> {
    const result = await staffQuoteassist.quoteAssistScopeGaps(this.#client, { quote_id: quoteId });
    if (!result.ok) return result;
    const gaps = readScopeGaps(result.value);
    return gaps === null ? err("the scope gaps are not readable") : ok(gaps);
  }

  /** Has this customer heard from us, and when next. */
  async followUps(quoteId: string): Promise<Result<QuoteFollowUpHistory>> {
    return this.followUpAnswer(await staffQuote.quoteFollowUpHistory(this.#client, quoteId));
  }

  async stopFollowUps(quoteId: string): Promise<Result<QuoteFollowUpHistory>> {
    return this.followUpAnswer(await staffQuote.quoteDisableFollowUps(this.#client, quoteId));
  }

  async resumeFollowUps(quoteId: string): Promise<Result<QuoteFollowUpHistory>> {
    return this.followUpAnswer(await staffQuote.quoteEnableFollowUps(this.#client, quoteId));
  }

  private followUpAnswer(result: Result<unknown>): Result<QuoteFollowUpHistory> {
    if (!result.ok) return result;
    const history = readFollowUps(result.value);
    return history === null ? err("the follow-up history is not readable") : ok(history);
  }

  /** When the quote's soft stock holds lapse with its validity. */
  async holdExpiry(quoteId: string): Promise<Result<QuoteHoldExpiryView>> {
    const result = await staffQuote.quoteQuoteHoldExpiry(this.#client, quoteId);
    if (!result.ok) return result;
    const view = readHoldExpiry(result.value);
    return view === null ? err("the hold expiry status is not readable") : ok(view);
  }

  /** Re-hold the stock where the server allows it; answers the holds applied. */
  async resyncHolds(quoteId: string): Promise<Result<number>> {
    const result = await staffQuote.quoteResyncQuoteHolds(this.#client, quoteId);
    if (!result.ok) return result;
    const applied = readHoldsApplied(result.value);
    return applied === null ? err("the re-hold answer is not readable") : ok(applied);
  }

  /** A chip per line, graded against what is available at the branch. */
  async availability(quoteId: string): Promise<Result<{ lines: QuoteLineAvailability[]; seamWired: boolean }>> {
    const result = await staffQuote.quoteQuoteAvailability(this.#client, quoteId);
    if (!result.ok) return result;
    const view = readAvailability(result.value);
    return view === null ? err("the availability is not readable") : ok(view);
  }

  /** The cross branch peek behind a short line. */
  async availabilityBranches(productId: string): Promise<Result<QuoteBranchStock[]>> {
    const result = await staffQuote.quoteAvailabilityBranches(this.#client, productId);
    if (!result.ok) return result;
    const rows = readBranchStocks(result.value);
    return rows === null ? err("the branch stock is not readable") : ok(rows);
  }

  /** Check a price and cost against the margin policy. The server owns the arithmetic and the verdict. */
  async checkPolicy(priceCents: number, costCents: number, branchId: string): Promise<Result<IdentityPolicyCheckResponse>> {
    const result = await staffIdentity.identityCheckPolicy(this.#client, {
      price_cents: priceCents,
      cost_cents: costCents,
      branch_id: branchId,
    });
    if (!result.ok) return result;
    const verdict = readPolicyCheck(result.value);
    return verdict === null ? err("the policy check is not readable") : ok(verdict);
  }

  /** The approval requests the server returns for this caller, optionally by status. */
  async listApprovals(status: "PENDING" | "APPROVED" | "REJECTED" | ""): Promise<Result<IdentityApproval[]>> {
    // A page object carries next_cursor and is followed to its end; the core's bare array has none (see readApprovalsPage).
    const rows: IdentityApproval[] = [];
    const seen = new Set<string>();
    let cursor: string | undefined;
    for (let pages = 0; pages < MAX_APPROVAL_PAGES; pages += 1) {
      const query: { status?: "PENDING" | "APPROVED" | "REJECTED"; cursor?: string } = {};
      if (status !== "") query.status = status;
      if (cursor !== undefined) query.cursor = cursor;
      const result = await staffIdentity.identityListApprovals(this.#client, Object.keys(query).length === 0 ? undefined : query);
      if (!result.ok) return result;
      const page = readApprovalsPage(result.value);
      if (page === null) return err("the approvals list is not readable");
      rows.push(...page.items);
      if (page.nextCursor === undefined) return ok(rows);
      if (seen.has(page.nextCursor)) return err("the approvals list repeated a page cursor");
      seen.add(page.nextCursor);
      cursor = page.nextCursor;
    }
    return err("the approvals list has more pages than this screen reads");
  }

  /** Decide one approval. The server decides whether this caller may; its refusal comes back verbatim. */
  async decideApproval(id: string, approve: boolean, reason: string): Promise<Result<true>> {
    const result = await staffIdentity.identityDecideApproval(this.#client, id, { approve, reason });
    return result.ok ? ok(true) : result;
  }
}
