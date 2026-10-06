// _panel-wire.ts: the field readers for the second coder's panels (the change
// history, share links, the price comparison, the catalogue search, the quote
// policy, the acceptance switch and the scope rules). Each reader either
// proves every field it needs or answers null, and the client turns a null
// into a refusal in words: a missing field is a refusal, never a default.
// DOM-free so the Node runner can drive it.
import type { QuoteFieldChange } from "./_quote-history.js";

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function str(record: Record<string, unknown>, key: string): string | null {
  const value = record[key];
  return typeof value === "string" && value !== "" ? value : null;
}

function int(record: Record<string, unknown>, key: string): number | null {
  const value = record[key];
  return typeof value === "number" && Number.isInteger(value) ? value : null;
}

function bool(record: Record<string, unknown>, key: string): boolean | null {
  const value = record[key];
  return typeof value === "boolean" ? value : null;
}

function strings(record: Record<string, unknown>, key: string): string[] | null {
  const value = record[key];
  if (!Array.isArray(value)) return null;
  const out: string[] = [];
  for (const item of value) {
    if (typeof item !== "string") return null;
    out.push(item);
  }
  return out;
}

/** The change trail: one unreadable row refuses the whole list rather than showing it short. */
export function readHistory(value: unknown): QuoteFieldChange[] | null {
  if (!isRecord(value) || !Array.isArray(value["changes"])) return null;
  const out: QuoteFieldChange[] = [];
  for (const raw of value["changes"]) {
    if (!isRecord(raw)) return null;
    const field = str(raw, "field");
    const lineNo = int(raw, "line_no");
    const occurredAt = str(raw, "occurred_at");
    if (field === null || lineNo === null || occurredAt === null) return null;
    const row: QuoteFieldChange = { field, line_no: lineNo, occurred_at: occurredAt };
    for (const key of ["before", "after", "actor_id", "actor_name", "request_id"] as const) {
      const text = raw[key];
      if (typeof text === "string") row[key] = text;
    }
    out.push(row);
  }
  return out;
}

export interface ShareLinkRow {
  id: string;
  expiresAt: string;
  revokedAt: string | null;
  createdAt: string;
  live: boolean;
}

/** The share link list. The raw token is the mint's answer alone and is never read from a list. */
export function readShareLinks(value: unknown): ShareLinkRow[] | null {
  if (!isRecord(value) || !Array.isArray(value["items"])) return null;
  const out: ShareLinkRow[] = [];
  for (const raw of value["items"]) {
    if (!isRecord(raw)) return null;
    const id = str(raw, "id");
    const expiresAt = str(raw, "expires_at");
    const live = bool(raw, "live");
    if (id === null || expiresAt === null || live === null) return null;
    out.push({
      id,
      expiresAt,
      revokedAt: str(raw, "revoked_at"),
      createdAt: str(raw, "created_at") ?? "",
      live,
    });
  }
  return out;
}

export interface ComparisonRow {
  product_id: string;
  is_current: boolean;
  unit_price_cents: number;
  line_total_cents: number;
  list_price_cents: number;
  delta_cents: number;
  source: string;
  blocked: boolean;
  block_policy?: string;
  unavailable?: string;
}

/** A line's price alternatives. The money fields are proven integers or the list is refused. */
export function readComparison(value: unknown): ComparisonRow[] | null {
  if (!isRecord(value) || !Array.isArray(value["options"])) return null;
  const out: ComparisonRow[] = [];
  for (const raw of value["options"]) {
    if (!isRecord(raw)) return null;
    const productId = str(raw, "product_id");
    const isCurrent = bool(raw, "is_current");
    const unit = int(raw, "unit_price_cents");
    const total = int(raw, "line_total_cents");
    const list = int(raw, "list_price_cents");
    const delta = int(raw, "delta_cents");
    const source = typeof raw["source"] === "string" ? raw["source"] : null;
    const blocked = bool(raw, "blocked");
    if (productId === null || isCurrent === null || unit === null || total === null || list === null || delta === null || source === null || blocked === null) {
      return null;
    }
    const row: ComparisonRow = {
      product_id: productId,
      is_current: isCurrent,
      unit_price_cents: unit,
      line_total_cents: total,
      list_price_cents: list,
      delta_cents: delta,
      source,
      blocked,
    };
    const policy = str(raw, "block_policy");
    if (policy !== null) row.block_policy = policy;
    const unavailable = str(raw, "unavailable");
    if (unavailable !== null) row.unavailable = unavailable;
    out.push(row);
  }
  return out;
}

export interface CatalogHit {
  product_id: string;
  sku: string;
  name: string;
  base_uom: string;
}

/** The staff catalogue search: the four facts a pick needs, or the list is refused. */
export function readCatalogHits(value: unknown): CatalogHit[] | null {
  if (!isRecord(value) || !Array.isArray(value["items"])) return null;
  const out: CatalogHit[] = [];
  for (const raw of value["items"]) {
    if (!isRecord(raw)) return null;
    const productId = str(raw, "product_id");
    const sku = str(raw, "sku");
    const name = str(raw, "name");
    const baseUom = str(raw, "base_uom");
    if (productId === null || sku === null || name === null || baseUom === null) return null;
    out.push({ product_id: productId, sku, name, base_uom: baseUom });
  }
  return out;
}

export const COST_VISIBILITIES = ["MANAGER_UP", "ALL_STAFF", "HIDDEN"] as const;
export const ARRANGEMENTS = ["PRIMARY_LEFT", "PRIMARY_RIGHT"] as const;

/** The typed quote policy, field for field the contract's QuotePolicy. */
export interface QuotePolicyWire {
  branch_id?: string;
  cost_visibility: (typeof COST_VISIBILITIES)[number];
  default_validity_days: number;
  default_deposit_pct: number;
  line_discounts_enabled: boolean;
  send_email_enabled: boolean;
  send_sms_enabled: boolean;
  include_product_images: boolean;
  quick_chip_set: string[];
  effective_chips?: string[];
  workspace_split_pct: number;
  workspace_arrangement: (typeof ARRANGEMENTS)[number];
}

export function readPolicy(value: unknown): QuotePolicyWire | null {
  if (!isRecord(value)) return null;
  const visibility = value["cost_visibility"];
  const arrangement = value["workspace_arrangement"];
  const validity = int(value, "default_validity_days");
  const deposit = int(value, "default_deposit_pct");
  const split = int(value, "workspace_split_pct");
  const discounts = bool(value, "line_discounts_enabled");
  const email = bool(value, "send_email_enabled");
  const sms = bool(value, "send_sms_enabled");
  const images = bool(value, "include_product_images");
  const chips = strings(value, "quick_chip_set");
  if (
    !(COST_VISIBILITIES as readonly unknown[]).includes(visibility) ||
    !(ARRANGEMENTS as readonly unknown[]).includes(arrangement) ||
    validity === null || deposit === null || split === null ||
    discounts === null || email === null || sms === null || images === null || chips === null
  ) {
    return null;
  }
  const out: QuotePolicyWire = {
    cost_visibility: visibility as QuotePolicyWire["cost_visibility"],
    default_validity_days: validity,
    default_deposit_pct: deposit,
    line_discounts_enabled: discounts,
    send_email_enabled: email,
    send_sms_enabled: sms,
    include_product_images: images,
    quick_chip_set: chips,
    workspace_split_pct: split,
    workspace_arrangement: arrangement as QuotePolicyWire["workspace_arrangement"],
  };
  const branch = str(value, "branch_id");
  if (branch !== null) out.branch_id = branch;
  const effective = strings(value, "effective_chips");
  if (effective !== null) out.effective_chips = effective;
  return out;
}

export interface AcceptanceView {
  allowMultiAccept: boolean;
  switchWired: boolean;
}

export function readAcceptance(value: unknown): AcceptanceView | null {
  if (!isRecord(value)) return null;
  const allow = bool(value, "allow_multi_accept");
  const wired = bool(value, "switch_wired");
  return allow === null || wired === null ? null : { allowMultiAccept: allow, switchWired: wired };
}

export interface ScopeRuleRow {
  code: string;
  title: string;
  advice: string;
  when_apps: string[];
  expect_apps: string[];
  suggest_query: string;
  enabled: boolean;
  sort_order: number;
}

function readScopeRule(raw: unknown): ScopeRuleRow | null {
  if (!isRecord(raw)) return null;
  const code = str(raw, "code");
  const title = str(raw, "title");
  const enabled = bool(raw, "enabled");
  const whenApps = strings(raw, "when_apps");
  const expectApps = strings(raw, "expect_apps");
  if (code === null || title === null || enabled === null || whenApps === null || expectApps === null) return null;
  return {
    code,
    title,
    advice: typeof raw["advice"] === "string" ? raw["advice"] : "",
    when_apps: whenApps,
    expect_apps: expectApps,
    suggest_query: typeof raw["suggest_query"] === "string" ? raw["suggest_query"] : "",
    enabled,
    sort_order: int(raw, "sort_order") ?? 0,
  };
}

export function readScopeRules(value: unknown): ScopeRuleRow[] | null {
  if (!isRecord(value) || !Array.isArray(value["rules"])) return null;
  const out: ScopeRuleRow[] = [];
  for (const raw of value["rules"]) {
    const rule = readScopeRule(raw);
    if (rule === null) return null;
    out.push(rule);
  }
  return out;
}

/** One rule as the switch write answers it. */
export function readScopeRuleOne(value: unknown): ScopeRuleRow | null {
  return readScopeRule(value);
}
