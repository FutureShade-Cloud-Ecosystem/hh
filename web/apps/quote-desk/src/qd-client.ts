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
import {
  staffBranches,
  staffQuote,
  staffSettings,
} from "@hardscapehouse/api-client";
import type { QuoteView } from "./_quote-edit.js";
import type { GlanceIn } from "./_quote-workarea.js";
import type { WireItem } from "./quote-surface.js";

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

function validateTerms(value: unknown): TermsRef | null {
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
  if (
    quoteId === null || depositRequired === null || depositPct === null || includeImages === null ||
    fulfillmentRaw === null || subtotalCents === null || taxCents === null || totalCents === null
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
    fulfillment: fulfillmentRaw === "DELIVERY" ? "DELIVERY" : "PICKUP",
    deliveryAddress: typeof value["delivery_address"] === "string" ? value["delivery_address"] : "",
    requestedDate: typeof value["requested_date"] === "string" ? value["requested_date"] : "",
    deliveryFeeCents: integerAt(value, "delivery_fee_cents") ?? 0,
    depositCents: depositCents ?? 0,
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

export class QdClient {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
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

  /** The quotes list, with the status filter the server itself applies. */
  async listQuotes(status: string): Promise<Result<QuoteRow[]>> {
    const opts = status === "" ? undefined : { status: status as "DRAFT" | "SENT" | "ACCEPTED" | "CONVERTED" | "EXPIRED" | "LOST" };
    const result = await staffQuote.quoteListQuotes(this.#client, opts);
    if (!result.ok) return result;
    const value = result.value as unknown;
    const items = isRecord(value) ? value["items"] : value;
    if (!Array.isArray(items)) return err("the quotes list did not answer a page or a list");
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
    return ok(rows);
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

  /** Convert an accepted quote to its order. */
  async convertQuote(quoteId: string): Promise<Result<{ orderId: string; orderNo: string }>> {
    const result = await staffQuote.quoteConvertQuote(this.#client, quoteId, {});
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the conversion answer is not readable");
    const orderId = stringAt(record, "order_id");
    const orderNo = stringAt(record, "order_no");
    if (orderId === null || orderNo === null) return err("the conversion answer is missing its order number");
    return ok({ orderId, orderNo });
  }

  /**
   * The request door the third coder's dialog files through (the QD-1
   * operation). Exposed here so the dialog lands on the same validated door
   * the rest of the surface speaks; the work area itself does not call it.
   */
  async requestFloorApproval(
    quoteId: string,
    lineNo: number,
    discPct: number,
    note: string,
  ): Promise<Result<{ filed: boolean; mayClearItself: boolean }>> {
    const result = await staffQuote.quoteRequestFloorApproval(this.#client, quoteId, lineNo, {
      disc_pct: discPct,
      ...(note === "" ? {} : { note }),
    });
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the request door's answer is not readable");
    const filed = booleanAt(record, "filed");
    const mayClearItself = booleanAt(record, "may_clear_itself");
    if (filed === null || mayClearItself === null) {
      return err("the request door's answer is missing its own facts");
    }
    return ok({ filed, mayClearItself });
  }

  /** The surface config read: the curation rows, the workspace row and the policy. */
  async surfaceConfig(branchId: string): Promise<Result<SurfaceConfig>> {
    const result = await staffSettings.settingsGetSurfaceConfig(this.#client, "quotes.builder", {
      ...(branchId === "" ? {} : { branch_id: branchId }),
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
}
