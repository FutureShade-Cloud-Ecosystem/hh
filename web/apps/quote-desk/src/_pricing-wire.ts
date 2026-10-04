// _pricing-wire.ts: the field readers for the pricing panel (DOM-free, unit tested on
// the Node runner). Each reader either proves every field it needs or answers null,
// and the client turns a null into a refusal in words: a missing field is a refusal,
// never a default, and one unreadable row refuses the whole page rather than showing
// it short. The shapes are the contract's PricingRule, PricingOverride,
// PricingQuantityBreakHeader, PricingCategoryPolicy and PricingAuditLogRow, and the
// floor warning's PricingFloorPreview.

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function str(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  return typeof value === "string" && value !== "" ? value : null;
}

function optStr(record: Record<string, unknown>, key: string): string | undefined {
  const value = record[key];
  return typeof value === "string" && value !== "" ? value : undefined;
}

function int(record: Record<string, unknown>, key: string): number | null {
  const value = record[key];
  return typeof value === "number" && Number.isInteger(value) ? value : null;
}

/** A nullable integer: absent or null is null; anything else must be an integer or the row is refused (undefined). */
function nullableInt(record: Record<string, unknown>, key: string): number | null | undefined {
  const value = record[key];
  if (value === undefined || value === null) return null;
  return typeof value === "number" && Number.isInteger(value) ? value : undefined;
}

function bool(record: Record<string, unknown>, key: string): boolean | null {
  const value = record[key];
  return typeof value === "boolean" ? value : null;
}

export interface PageOf<T> {
  items: T[];
  nextCursor: string | null;
}

/**
 * A list arrives as the contract's page object ({items, next_cursor}) or as a bare array,
 * which is what the core's pricing handlers write today (they take no cursor). Both are
 * read; an unreadable row refuses the whole list.
 */
function readPage<T>(value: unknown, readRow: (row: unknown) => T | null): PageOf<T> | null {
  const rows = Array.isArray(value) ? value : isRecord(value) && Array.isArray(value["items"]) ? value["items"] : null;
  if (rows === null) return null;
  const items: T[] = [];
  for (const raw of rows) {
    const row = readRow(raw);
    if (row === null) return null;
    items.push(row);
  }
  const cursor = isRecord(value) ? value["next_cursor"] : null;
  return { items, nextCursor: typeof cursor === "string" && cursor !== "" ? cursor : null };
}

// ── category margin policy ───────────────────────────────────────────────────

export interface PolicyRow {
  categoryId: string;
  targetBps: number;
  floorBps: number;
  enforcementOff: boolean;
}

export function readPolicy(value: unknown): PolicyRow | null {
  if (!isRecord(value)) return null;
  const categoryId = str(value, "category_id");
  const targetBps = int(value, "margin_target_bps");
  const floorBps = int(value, "margin_floor_bps");
  const enforcementOff = bool(value, "margin_enforcement_disabled");
  if (categoryId === null || targetBps === null || floorBps === null || enforcementOff === null) return null;
  return { categoryId, targetBps, floorBps, enforcementOff };
}

export function readPolicyPage(value: unknown): PageOf<PolicyRow> | null {
  return readPage(value, readPolicy);
}

// ── rules ────────────────────────────────────────────────────────────────────

export const RULE_SCOPES = ["CUSTOMER_PRODUCT", "CUSTOMER_CATEGORY", "TIER_CATEGORY", "TIER_DEFAULT"] as const;
export const RULE_CALCS = ["ABSOLUTE", "PCT_OFF_LIST", "MARKUP_ON_COST"] as const;

export interface RuleRow {
  id: string;
  scope: (typeof RULE_SCOPES)[number];
  customerId: string | undefined;
  customerType: string | undefined;
  categoryId: string | undefined;
  productId: string | undefined;
  branchId: string | undefined;
  calcType: (typeof RULE_CALCS)[number];
  priceCents: number | null;
  discountBps: number | null;
  markupBps: number | null;
  priceUom: string;
  priority: number;
  validFrom: string | null;
  validTo: string | null;
  detailText: string;
  isActive: boolean;
  version: number;
}

export function readRule(value: unknown): RuleRow | null {
  if (!isRecord(value)) return null;
  const id = str(value, "id");
  const scope = RULE_SCOPES.find((s) => s === value["scope"]);
  const calcType = RULE_CALCS.find((c) => c === value["calc_type"]);
  const priceUom = str(value, "price_uom");
  const priority = int(value, "priority");
  const isActive = bool(value, "is_active");
  const version = int(value, "version");
  const priceCents = nullableInt(value, "price_cents");
  const discountBps = nullableInt(value, "discount_bps");
  const markupBps = nullableInt(value, "markup_bps");
  if (
    id === null || scope === undefined || calcType === undefined || priceUom === null || priority === null ||
    isActive === null || version === null || priceCents === undefined || discountBps === undefined || markupBps === undefined
  ) {
    return null;
  }
  return {
    id,
    scope,
    customerId: optStr(value, "customer_id"),
    customerType: optStr(value, "customer_type"),
    categoryId: optStr(value, "category_id"),
    productId: optStr(value, "product_id"),
    branchId: optStr(value, "branch_id"),
    calcType,
    priceCents,
    discountBps,
    markupBps,
    priceUom,
    priority,
    validFrom: optStr(value, "valid_from") ?? null,
    validTo: optStr(value, "valid_to") ?? null,
    detailText: typeof value["detail_text"] === "string" ? value["detail_text"] : "",
    isActive,
    version,
  };
}

export function readRulePage(value: unknown): PageOf<RuleRow> | null {
  return readPage(value, readRule);
}

// ── customer prices (category overrides) ─────────────────────────────────────

export const OVERRIDE_CALCS = ["PCT_OFF_LIST", "MARKUP_ON_COST"] as const;

export interface OverrideRow {
  id: string;
  customerId: string;
  categoryId: string;
  calcType: (typeof OVERRIDE_CALCS)[number];
  discountBps: number | null;
  markupBps: number | null;
  priceUom: string;
  notes: string;
  version: number;
}

export function readOverride(value: unknown): OverrideRow | null {
  if (!isRecord(value)) return null;
  const id = str(value, "id");
  const customerId = str(value, "customer_id");
  const categoryId = str(value, "category_id");
  const calcType = OVERRIDE_CALCS.find((c) => c === value["calc_type"]);
  const priceUom = str(value, "price_uom");
  const version = int(value, "version");
  const discountBps = nullableInt(value, "discount_bps");
  const markupBps = nullableInt(value, "markup_bps");
  if (
    id === null || customerId === null || categoryId === null || calcType === undefined || priceUom === null ||
    version === null || discountBps === undefined || markupBps === undefined
  ) {
    return null;
  }
  return { id, customerId, categoryId, calcType, discountBps, markupBps, priceUom, notes: typeof value["notes"] === "string" ? value["notes"] : "", version };
}

export function readOverridePage(value: unknown): PageOf<OverrideRow> | null {
  return readPage(value, readOverride);
}

// ── quantity breaks ──────────────────────────────────────────────────────────

export const BREAK_VALUE_TYPES = ["PCT_OFF", "ABS_OFF", "SURCHARGE_PCT", "SURCHARGE_ABS"] as const;

export interface BreakItemRow {
  fromQty: number;
  toQty: number | null;
  valueType: (typeof BREAK_VALUE_TYPES)[number];
  discountBps: number | null;
  discountCents: number | null;
  surchargeBps: number | null;
  surchargeCents: number | null;
  priceUom: string;
}

export interface BreakRow {
  id: string;
  code: string;
  name: string;
  productId: string | undefined;
  categoryId: string | undefined;
  thresholdUom: string;
  isActive: boolean;
  version: number;
  items: BreakItemRow[];
}

function readBreakItem(value: unknown): BreakItemRow | null {
  if (!isRecord(value)) return null;
  const fromQty = value["from_qty"];
  const toRaw = value["to_qty"];
  const valueType = BREAK_VALUE_TYPES.find((t) => t === value["value_type"]);
  const priceUom = str(value, "price_uom");
  const discountBps = nullableInt(value, "discount_bps");
  const discountCents = nullableInt(value, "discount_cents");
  const surchargeBps = nullableInt(value, "surcharge_bps");
  const surchargeCents = nullableInt(value, "surcharge_cents");
  if (typeof fromQty !== "number" || !Number.isFinite(fromQty)) return null;
  if (toRaw !== undefined && toRaw !== null && (typeof toRaw !== "number" || !Number.isFinite(toRaw))) return null;
  if (
    valueType === undefined || priceUom === null || discountBps === undefined || discountCents === undefined ||
    surchargeBps === undefined || surchargeCents === undefined
  ) {
    return null;
  }
  return {
    fromQty,
    toQty: typeof toRaw === "number" ? toRaw : null,
    valueType,
    discountBps,
    discountCents,
    surchargeBps,
    surchargeCents,
    priceUom,
  };
}

export function readBreak(value: unknown): BreakRow | null {
  if (!isRecord(value) || !Array.isArray(value["items"])) return null;
  const id = str(value, "id");
  const code = str(value, "code");
  const name = str(value, "name");
  const thresholdUom = str(value, "threshold_uom");
  const isActive = bool(value, "is_active");
  const version = int(value, "version");
  if (id === null || code === null || name === null || thresholdUom === null || isActive === null || version === null) return null;
  const items: BreakItemRow[] = [];
  for (const raw of value["items"]) {
    const item = readBreakItem(raw);
    if (item === null) return null;
    items.push(item);
  }
  return {
    id,
    code,
    name,
    productId: optStr(value, "product_id"),
    categoryId: optStr(value, "category_id"),
    thresholdUom,
    isActive,
    version,
    items,
  };
}

export function readBreakPage(value: unknown): PageOf<BreakRow> | null {
  return readPage(value, readBreak);
}

// ── the pricing log ──────────────────────────────────────────────────────────

export const AUDIT_ROW_ACTIONS = ["CREATE", "UPDATE", "DELETE", "BYPASS"] as const;

export interface AuditRow {
  id: string;
  entityType: string;
  entityId: string;
  action: (typeof AUDIT_ROW_ACTIONS)[number];
  fieldName: string | undefined;
  oldValue: string | undefined;
  newValue: string | undefined;
  userId: string | undefined;
  createdAt: string;
  refEntityType: string | undefined;
  refEntityId: string | undefined;
  /** Why a BYPASS row bypassed the floor; only on those rows. */
  bypassReason: string | undefined;
}

export function readAuditRow(value: unknown): AuditRow | null {
  if (!isRecord(value)) return null;
  const id = str(value, "id");
  const entityType = str(value, "entity_type");
  const entityId = str(value, "entity_id");
  const action = AUDIT_ROW_ACTIONS.find((a) => a === value["action"]);
  const createdAt = str(value, "created_at");
  if (id === null || entityType === null || entityId === null || action === undefined || createdAt === null) return null;
  return {
    id,
    entityType,
    entityId,
    action,
    fieldName: optStr(value, "field_name"),
    oldValue: optStr(value, "old_value"),
    newValue: optStr(value, "new_value"),
    userId: optStr(value, "user_id"),
    createdAt,
    refEntityType: optStr(value, "ref_entity_type"),
    refEntityId: optStr(value, "ref_entity_id"),
    bypassReason: optStr(value, "bypass_reason"),
  };
}

export function readAuditPage(value: unknown): PageOf<AuditRow> | null {
  return readPage(value, readAuditRow);
}

// ── writes ───────────────────────────────────────────────────────────────────

export interface CreatedRef {
  id: string;
  version: number;
}

export function readCreatedRef(value: unknown): CreatedRef | null {
  if (!isRecord(value)) return null;
  const id = str(value, "id");
  const version = int(value, "version");
  return id === null || version === null ? null : { id, version };
}

// ── the floor warning ────────────────────────────────────────────────────────

export interface FloorItem {
  productId: string;
  sku: string;
  name: string;
  customerType: string;
  finalCents: number;
  marginBps: number;
  floorBps: number;
  priceUom: string;
  previousFinalCents: number;
  previousMarginBps: number;
  alreadyUnderFloor: boolean;
}

export interface FloorPreview {
  items: FloorItem[];
  evaluated: number;
  /** Products the resolver had no price to grade in full. */
  notGraded: number;
  /** Products with no cost on file, whose margin cannot be told. */
  noCost: number;
  truncated: boolean;
}

function readFloorItem(value: unknown): FloorItem | null {
  if (!isRecord(value)) return null;
  const productId = str(value, "product_id");
  const sku = str(value, "sku");
  const name = str(value, "name");
  const finalCents = int(value, "final_cents");
  const marginBps = int(value, "margin_bps");
  const floorBps = int(value, "floor_bps");
  const priceUom = str(value, "price_uom");
  const previousFinalCents = int(value, "previous_final_cents");
  const previousMarginBps = int(value, "previous_margin_bps");
  const alreadyUnderFloor = bool(value, "already_under_floor");
  if (
    productId === null || sku === null || name === null || finalCents === null || marginBps === null || floorBps === null ||
    priceUom === null || previousFinalCents === null || previousMarginBps === null || alreadyUnderFloor === null
  ) {
    return null;
  }
  return { productId, sku, name, customerType: optStr(value, "customer_type") ?? "", finalCents, marginBps, floorBps, priceUom, previousFinalCents, previousMarginBps, alreadyUnderFloor };
}

export function readFloorPreview(value: unknown): FloorPreview | null {
  if (!isRecord(value) || !Array.isArray(value["items"])) return null;
  const evaluated = int(value, "evaluated");
  const notGraded = int(value, "not_graded");
  const noCost = int(value, "no_cost");
  const truncated = bool(value, "truncated");
  if (evaluated === null || notGraded === null || noCost === null || truncated === null) return null;
  const items: FloorItem[] = [];
  for (const raw of value["items"]) {
    const item = readFloorItem(raw);
    if (item === null) return null;
    items.push(item);
  }
  return { items, evaluated, notGraded, noCost, truncated };
}
