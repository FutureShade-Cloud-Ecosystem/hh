// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-api.ts; adapted for HH.

// A-19 POS client helpers. Thin typed wrappers over the staff API (Bearer JWT via api()).
import { api, getSessionToken } from './ports.js';
import { centsAmount } from './_money-format.js';
import { asRows } from './_response-shape.js'; // loader-boundary coercion (DIB-471): no ad-hoc arrays
import type { CounterQuote } from './_pos-searchdrop.js';
import type { PriceBreakdown } from './price-breakdown.js';

export interface PosProduct {
  id: string;
  sku: string;
  name: string;
  color_family: string;
  application: string;
  sold_by: string;
  base_uom: string; // stocking unit: the sell-UoM fallback when sold_by is unset
  is_active: boolean;
  // ── rev 5: the LIST read already enriches every row with the retail anchor + per-branch
 // on-hand (product.Service.List → enrichRows). The browse grid was ignoring both: its price
  // slot rendered the "click to price & add" placeholder on every tile and its stock glances
  // stopped at an arbitrary 24-product cap. All three fields are optional and absent-is-honest:
  // no anchor → no price claim, no stock read → no stock claim.
  list_price_cents?: number;
  list_price_uom?: string;
  on_hand_by_branch?: Record<string, number>;
 // rev 8 (item 4): the LIST read has always emitted the featured flag: the ★ Favorites chip
  // finally consumes it. Optional/absent-is-honest like the rev-5 trio above.
  is_featured?: boolean;
  /**
   * Whether a thumbnail exists for this product. WAVE 2 · finding 20d.
   *
   * The counter SEARCH carries this (CounterHit.has_image), and counter_search.go says why in its
   * own words: "HasImage lets the UI render a thumbnail without first issuing a request that 404s".
 * Quick picks and add-on suggestions carry it too. The product LIST: the read that fills the
 * browse grid: does not emit it yet, which is why the field is optional here and why the browse
   * tiles do not speculate (see pos-product-grid.ts). The day the list read emits it, this
   * consumes it and the browse grid starts showing pictures with no further change.
   */
  has_image?: boolean;
}

export interface CartLine {
  product: PosProduct;
  qty: number;
  uom: string;
  unitPriceCents: number;
  priceUom: string;
  lineTotalCents: number;
  breakdown: PriceBreakdown | null;
 /** rev 5: pricing/resolve failed for this line: render "price pending", never a hard CA$0.00. */
  priceMissing?: boolean;
}

export interface SaleResult {
  order_id: string;
  order_no: string;
  invoice_id: string;
  invoice_no: string;
  payment_id?: string;
  tender: string;
  sale_type: string;
  subtotal_cents: number;
  tax_cents: number;
  total_cents: number;
  deposit_cents?: number; // taken at ring on an unpaid sale; balance = total − deposit
  tax_reason_code: string;
  status: string;
  replayed: boolean;
  // ── DIB-488 rev 6 ── item D facts echoed back (sold panel + audit trail), item F change due.
  po_job_ref?: string;
  salesperson_id?: string;
  salesperson_name?: string;
  cash_tendered_cents?: number;
  change_due_cents?: number;
  // ── DIB-488 rev 8 (item 11) ── the JOB facts echoed back (sold panel + replay-verbatim).
  job_address?: string;
  job_reference?: string;
}

export interface ReliefOutcome {
  purchaser_type: string;
  masked_identifier: string;
  outcome: string;
  reason_code: string;
  rate_bps: number;
  full_hst_cents: number;
  on_credit_cents: number;
  net_collected_cents: number;
  automated: boolean;
  requires_evidence: boolean;
  evidence_satisfied: boolean;
}

export interface StockGlance {
  product_id: string;
  /** branch_id → AVAILABLE (on-hand − reserved), in the product's BASE unit. */
  on_hand: Record<string, number>;
  // ── DIB-488 unit meta: lets the counter speak the number in the item's SELL unit ──
  base_uom?: string;
  sell_uom?: string;
 /** Base units per ONE sell unit (product_uoms). 0/absent = no conversion on file: do not divide. */
  sell_factor?: number;
  /** branch_id → authored low threshold (0117 reorder point), base units. Absent = none authored. */
  low_base?: Record<string, number>;
  /** rev 8 (item 9): kg per BASE unit (products.weight_per_unit_kg). Absent = no weight on file. */
  weight_kg_per_base?: number;
}

// Money via a clearly-flagged LOCAL STOPGAP: swap for M8 fmt.ts the moment it lands (mirrors
// price-breakdown.ts:34 / D-A07-7).
export function money(cents: number): string {
  return `CA$${centsAmount(cents)}`; // LOCAL STOPGAP
}

// HH note: the reference fetched the catalogue here with a bare browser call, no bearer and no
// organisation header and silent when the boundary is unwired. The list rides the host seam now
// (ports.ts, named in CLOSURE.md), same signature, same parsed shape.
export { listProducts } from './ports.js';

export async function resolveLine(input: {
  product_id: string;
  customer_id: string;
  customer_type: string;
  branch_id: string;
  qty_entered: number;
  uom_entered: string;
  cost_cents: number;
}): Promise<PriceBreakdown> {
  const res = await api('/api/v1/pricing/resolve', { method: 'POST', body: JSON.stringify(input) });
  if (!res.ok) throw new Error(`resolve ${res.status}`);
  return (await res.json()) as PriceBreakdown;
}

export async function stockGlance(productId: string): Promise<StockGlance> {
  const res = await api(`/api/v1/pos/stock-glance?product_id=${encodeURIComponent(productId)}`);
  if (!res.ok) throw new Error(`glance ${res.status}`);
  return (await res.json()) as StockGlance;
}

// PosCustomer is the account projection the customer strip + picker share (see
// pos-customer-strip.ts); GET /api/v1/customers/search returns an array of exactly it.
import type { PosCustomer } from './pos-customer-strip.js';

// searchCustomers backs the POS customer picker. Staff-gated (Bearer JWT via api()).
// The walk-in house account is excluded server-side, so results are chargeable accounts.
// DIB-506 fix pass: optional branchId: the U-park E4 fence 403s a branch-SCOPED caller that
// names no branch, so callers that know their branch must say it. Omitted = the old URL, byte
// for byte (GLOBAL callers still get the unfiltered sweep).
export async function searchCustomers(q: string, limit = 20, branchId = ''): Promise<PosCustomer[]> {
  const branch = branchId ? `&branch_id=${encodeURIComponent(branchId)}` : '';
  const res = await api(`/api/v1/customers/search?q=${encodeURIComponent(q)}&limit=${limit}${branch}`);
  if (!res.ok) throw new Error(`customer search ${res.status}`);
  return (await res.json()) as PosCustomer[];
}

export interface SaleLineInput {
  product_id: string;
  qty_entered: number;
  uom_entered: string;
  cost_cents: number;
}

export interface SalePost {
  idempotency_key: string;
  branch_id: string;
  till_session_id: string;
  customer_id: string;
  sale_type: string;
  fulfilment?: string; // COD only: PICKUP | WILL_CALL | DELIVERY
  tender: string;
  payment_token?: string;
  card_type?: string;
  signature_ref?: string;
  requested_date?: string;
  notes?: string;
  offline?: boolean;
  deposit_cents?: number; // unpaid-at-ring only (Charge Account / COD)
  deposit_method?: string;
  // ── DIB-488 rev 6 ── item D (optional More-options facts) + item F (cash tendered, only sent
  // when it covers the total). All three ride the ring BODY, so stableRingKey's fingerprint
  // (JSON.stringify of the payload) re-mints the idempotency key naturally when any of them change.
  po_job_ref?: string;
  salesperson_id?: string;
  cash_tendered_cents?: number;
  // ── DIB-488 rev 8 (item 11) ── the JOB fields ride the body the same way, so stableRingKey's
  // fingerprint re-mints the idempotency key naturally when either changes.
  job_address?: string;
  job_reference?: string;
  lines: SaleLineInput[];
}

export interface SaleError {
  status: number;
  error: string;
  policy?: string;
  line?: number;
}

// createSale returns either a SaleResult or a structured SaleError (409 credit/price, 402 decline).
export async function createSale(body: SalePost): Promise<{ ok: true; sale: SaleResult } | { ok: false; err: SaleError }> {
  const res = await api('/api/v1/pos/sales', { method: 'POST', body: JSON.stringify(body) });
  if (res.ok) return { ok: true, sale: (await res.json()) as SaleResult };
  let parsed: { error?: string; policy?: string; line?: number } = {};
  try {
    parsed = (await res.json()) as typeof parsed;
  } catch {
    /* non-JSON body */
  }
  return { ok: false, err: { status: res.status, error: parsed.error ?? 'sale_failed', policy: parsed.policy, line: parsed.line } };
}

export async function assessRelief(body: {
  profile_id?: string;
  order_id?: string;
  purchaser_type?: string;
  declared_use: string;
  fulfilment: string;
  subtotal_cents: number;
  evidence_attached: boolean;
  purpose_ack: boolean;
}): Promise<ReliefOutcome> {
  const res = await api('/api/v1/pos/assess-relief', { method: 'POST', body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`assess-relief ${res.status}`);
  return (await res.json()) as ReliefOutcome;
}

export async function captureSignature(purpose: string, refType: string, refId: string): Promise<string> {
  const res = await api('/api/v1/pos/signatures', {
    method: 'POST',
    body: JSON.stringify({ purpose, ref_type: refType, ref_id: refId }),
  });
  if (!res.ok) throw new Error(`signature ${res.status}`);
  const body = (await res.json()) as { signature_ref: string };
  return body.signature_ref;
}

/** The OPEN drawer at a branch/station, when there is one (rev 5: GET /api/v1/till/sessions/open). */
export interface OpenTillSession {
  id: string;
  opened_at: string;
}

/**
 * findOpenTillSession asks the server whether the caller's branch/station already has an OPEN till
 * (rev 5). The terminal held tillSessionId in memory only, so a mid-shift reload showed "Open
 * Session" and rang 409 while the drawer WAS open. Returns null when no session is open: an
 * ordinary state, never an error.
 */
export async function findOpenTillSession(branchId: string, station = 'MAIN'): Promise<OpenTillSession | null> {
  const params = new URLSearchParams({ branch_id: branchId, station });
  const res = await api(`/api/v1/till/sessions/open?${params.toString()}`);
  if (!res.ok) throw new Error(`till session lookup ${res.status}`);
  const body = (await res.json()) as { open: boolean; id?: string; opened_at?: string };
  return body.open && body.id ? { id: body.id, opened_at: body.opened_at ?? '' } : null;
}

/** One row of the counter's salesperson selector (DIB-488 rev 6, item D). */
export interface Salesperson {
  id: string;
  name: string;
}

/**
 * listSalespeople: the active staff (id + display name only) for the More-options salesperson
 * selector. Staff-gated; the terminal degrades to "just the signed-in operator" on any failure
 * (empty-never-error: the selector is a convenience, never a gate).
 */
export async function listSalespeople(): Promise<Salesperson[]> {
  const res = await api('/api/v1/pos/salespeople');
  if (!res.ok) throw new Error(`salespeople ${res.status}`);
  const body = (await res.json()) as { items: Salesperson[] };
  return body.items ?? [];
}

// newIdempotencyKey mints a client UUID v4: retried VERBATIM on a network error → idempotent replay.
export function newIdempotencyKey(): string {
  return crypto.randomUUID();
}

// ── W7 counter-v2 helpers ────────────────────────────────────────────────────

export interface PosPolicy {
  branch_id: string;
  cod_enabled: boolean;
  notes: string;
  // ── DIB-488 / D-096 dealer-level workspace preferences (0165) ──
  /** Default items presentation: 'LINES' (compact thumbnail rows) | 'CARDS' (tap-tile grid). */
  line_presentation?: string;
  /** Whether the quick category chip row renders at all. */
  quick_chips_enabled?: boolean;
 // ── DIB-488 rev 2 dealer-level workspace DEFAULTS (0166): written via the admin policy PUT ──
  /** Default share (%) of the workspace given to the inventory-lookup panel (20-80). */
  workspace_split_pct?: number;
  /** Default panel arrangement: 'LOOKUP_LEFT' | 'SALE_LEFT'. */
  workspace_arrangement?: string;
  // ── DIB-488 rev 3 (0167): the lookup-results presentation default ──
  /** How the inventory-lookup results paint: 'CARDS' (product-card grid) | 'ROWS' (dense list). */
  lookup_presentation?: string;
  // ── DIB-488 rev 8 (0170) ──
  /** Who receives margin/profit in POS payloads: 'MANAGER_UP' | 'ALL_STAFF' | 'HIDDEN'. */
  margin_visibility?: string;
  /** The curated single-row chip categories (item 4); derived from top sellers when un-curated. */
  quick_chip_set?: string[];
}

export async function getPosPolicy(branchId: string): Promise<PosPolicy> {
  const res = await api(`/api/v1/pos/policy?branch_id=${encodeURIComponent(branchId)}`);
  if (!res.ok) throw new Error(`policy ${res.status}`);
  return (await res.json()) as PosPolicy;
}

// ── DIB-488 rev 2: the caller's own workspace ergonomics (0166; see _pos-workspace.ts for the
// persistence ruling: dealer defaults on pos_policy, per-user split/collapse/swap here, maximize
// never persisted). GET returns the server-resolved answer (user row when present, else dealer).
export interface WorkspacePrefsRow {
  split_pct: number;
  arrangement: string;
  lookup_collapsed: boolean;
  sale_collapsed: boolean;
 /** DIB-488 rev 3 (0167): 'CARDS' | 'ROWS': rides the same whole-or-absent resolution. */
  lookup_presentation?: string;
 /** DIB-488 rev 8 (0170): the margin quick-hide (eye) toggle: rides the same row. */
  margin_hidden?: boolean;
  source: 'user' | 'dealer';
}

export async function getWorkspacePrefs(branchId: string): Promise<WorkspacePrefsRow> {
  const res = await api(`/api/v1/pos/workspace?branch_id=${encodeURIComponent(branchId)}`);
  if (!res.ok) throw new Error(`workspace ${res.status}`);
  return (await res.json()) as WorkspacePrefsRow;
}

/** Saves the caller's own workspace. The idempotency key is minted ONCE per save and retried verbatim. */
export async function putWorkspacePrefs(body: {
  idempotency_key: string;
  split_pct: number;
  arrangement: string;
  lookup_collapsed: boolean;
  sale_collapsed: boolean;
  lookup_presentation: string;
  /** rev 8 (0170): optional so every earlier caller still typechecks; the server defaults false. */
  margin_hidden?: boolean;
}): Promise<WorkspacePrefsRow> {
  const res = await api('/api/v1/pos/workspace', { method: 'PUT', body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`workspace save ${res.status}`);
  return (await res.json()) as WorkspacePrefsRow;
}

/** Reset to the dealer defaults: reset IS a delete of the per-user row (the dashboard ruling). */
export async function resetWorkspacePrefs(): Promise<void> {
  const res = await api('/api/v1/pos/workspace', { method: 'DELETE' });
  if (!res.ok) throw new Error(`workspace reset ${res.status}`);
}

export interface ParkedLine {
  product_id: string;
  qty_entered: number;
  uom_entered: string;
}

export interface ParkedTicket {
  id: string;
  ticket_no: string;
  branch_id: string;
  customer_id?: string;
  sale_type: string;
  notes: string;
  status: string;
  lines: ParkedLine[];
  created_at: string;
}

export async function parkTicket(body: {
  branch_id: string;
  customer_id?: string;
  sale_type: string;
  notes?: string;
  lines: ParkedLine[];
}): Promise<ParkedTicket> {
  const res = await api('/api/v1/pos/parked', { method: 'POST', body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`park ${res.status}`);
  return (await res.json()) as ParkedTicket;
}

export async function listParked(branchId: string): Promise<ParkedTicket[]> {
  const res = await api(`/api/v1/pos/parked?branch_id=${encodeURIComponent(branchId)}`);
  if (!res.ok) throw new Error(`parked ${res.status}`);
  return (await res.json()) as ParkedTicket[];
}

export async function resumeParked(id: string): Promise<ParkedTicket> {
  const res = await api(`/api/v1/pos/parked/${encodeURIComponent(id)}/resume`, { method: 'POST', body: '{}' });
  if (!res.ok) throw new Error(`resume ${res.status}`);
  return (await res.json()) as ParkedTicket;
}

export async function discardParked(id: string): Promise<void> {
  const res = await api(`/api/v1/pos/parked/${encodeURIComponent(id)}`, { method: 'DELETE' });
  if (!res.ok) throw new Error(`discard ${res.status}`);
}

export interface ReturnableLine {
  order_line_id: string;
  product_id: string;
  description: string;
  qty_sold: number;
  qty_returned: number;
  unit_price_cents: number;
  line_total_cents: number;
  tax_cents: number;
}

export interface ReturnLookup {
  order_id: string;
  order_no: string;
  branch_id: string;
  customer_id: string;
  invoice_id: string;
  sale_tender: string;
  card_refundable: boolean;
  lines: ReturnableLine[];
}

export async function lookupReturn(orderNo: string): Promise<ReturnLookup> {
  const res = await api(`/api/v1/pos/returns/lookup?order_no=${encodeURIComponent(orderNo)}`);
  if (!res.ok) throw new Error(res.status === 404 ? 'No committed POS sale found for that order.' : `lookup ${res.status}`);
  return (await res.json()) as ReturnLookup;
}

export interface ReturnResult {
  return_id: string;
  return_no: string;
  order_id: string;
  invoice_id: string;
  refund_method: string;
  subtotal_cents: number;
  tax_cents: number;
  total_cents: number;
  refund_payment_id?: string;
  pickup_status: string;
  status: string;
  replayed: boolean;
}

export async function createReturn(body: {
  idempotency_key: string;
  order_id: string;
  lines: { order_line_id: string; qty_base: number; reason_code?: string }[];
  refund_method: string;
  reason?: string;
  restock: boolean;
  till_session_id?: string;
  pickup?: { required: boolean; site: string; requested_date: string };
}): Promise<ReturnResult> {
  const res = await api('/api/v1/pos/returns', { method: 'POST', body: JSON.stringify(body) });
  if (!res.ok) {
    let msg = `return failed (${res.status})`;
    try {
      const b = (await res.json()) as { error?: string };
      if (b.error) msg = b.error;
    } catch {
      /* non-JSON body */
    }
    throw new Error(msg);
  }
  return (await res.json()) as ReturnResult;
}

export interface PricingOrderLine {
  line_no: number;
  product_id: string;
  sku: string;
  name: string;
  qty_entered: number;
  uom_entered: string;
  unit_price_cents: number;
  list_price_cents: number;
  line_total_cents: number;
}

export interface PricingOrder {
  id: string;
  doc_no: string;
  branch_id: string;
  customer_id?: string;
  customer_name: string;
  notes: string;
  valid_until?: string;
  subtotal_cents: number;
  tax_cents: number;
  total_cents: number;
  created_at: string;
  lines: PricingOrderLine[];
}

export async function createPricingOrder(body: {
  branch_id: string;
  customer_id?: string;
  customer_name: string;
  notes?: string;
  valid_until?: string;
  lines: { product_id: string; qty_entered: number; uom_entered: string }[];
}): Promise<PricingOrder> {
  const res = await api('/api/v1/pos/pricing-orders', { method: 'POST', body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`pricing order ${res.status}`);
  return (await res.json()) as PricingOrder;
}

// ── A-06: portal return requests (staff approval queue) ─────────────────────

export interface ReturnRequestLine {
  order_line_id: string;
  product_id: string;
  description: string;
  qty_base: number;
}

// Mirrors internal/pos ReturnRequest: one portal-submitted return request on the staff queue.
export interface PortalReturnRequest {
  id: string;
  request_no: string;
  order_id: string;
  order_no: string;
  customer_id: string;
  customer_name?: string;
  branch_id: string;
  status: 'PENDING' | 'APPROVED' | 'REJECTED' | (string & {});
  reason_code: string;
  note: string;
  requested_by?: string;
  decided_by?: string;
  decided_at?: string;
  decision_reason?: string;
  fee_cents: number;
  return_id?: string;
  return_no?: string;
  created_at: string;
  lines: ReturnRequestLine[];
}

async function requestJson<T>(res: Response, what: string): Promise<T> {
  if (!res.ok) {
    let msg = `${what} failed (${res.status})`;
    try {
      const b = (await res.json()) as { error?: string };
      if (b.error) msg = b.error;
    } catch {
      /* non-JSON body */
    }
    throw new Error(msg);
  }
  return (await res.json()) as T;
}

export async function listReturnRequests(status = 'PENDING'): Promise<PortalReturnRequest[]> {
  const res = await api(`/api/v1/pos/return-requests?status=${encodeURIComponent(status)}`);
  return requestJson(res, 'return-request list');
}

// Approve executes the EXISTING POS return rail server-side: ACCOUNT_CREDIT + restock + the branch's
// restocking-fee policy, attributed to the signed-in staff member.
export async function approveReturnRequest(id: string): Promise<PortalReturnRequest> {
  const res = await api(`/api/v1/pos/return-requests/${encodeURIComponent(id)}/approve`, {
    method: 'POST',
    body: JSON.stringify({}),
  });
  return requestJson(res, 'approve');
}

export async function rejectReturnRequest(id: string, reason: string): Promise<PortalReturnRequest> {
  const res = await api(`/api/v1/pos/return-requests/${encodeURIComponent(id)}/reject`, {
    method: 'POST',
    body: JSON.stringify({ reason }),
  });
  return requestJson(res, 'reject');
}

// ── W3-A: counter search, scan, past-sale lookup, split tender ───────────────

// CounterHit mirrors internal/product CounterHit: one ranked search result for the counter.
export interface CounterHit {
  product_id: string;
  sku: string;
  name: string;
  description?: string;
  collection?: string;
  application?: string;
  color_family?: string;
  base_uom: string;
  sold_by?: string;
  has_image: boolean;
  /** EXACT | SKU_PREFIX | NAME_PREFIX | NAME | DESCRIPTION | ALIAS | FUZZY */
  match_reason: string;
  on_hand_branch?: number;
  on_hand_by_branch?: Record<string, number>;
  list_price_cents?: number;
}

// counterHitToProduct adapts a search hit onto the PosProduct shape the cart already speaks, so the
// server-side search drops into the existing add-line path with no change to CartLine or reprice().
export function counterHitToProduct(h: CounterHit): PosProduct {
  return {
    id: h.product_id,
    sku: h.sku,
    name: h.name,
    color_family: h.color_family ?? '',
    application: h.application ?? '',
    sold_by: h.sold_by ?? '',
    base_uom: h.base_uom,
    is_active: true,
  };
}

/**
 * counterSearch is the debounced server-side instant search (the pos-search ticket). It replaces the old
 * "download the whole catalogue once and filter it in the browser" path, which had no notion of
 * relevance and could not see barcodes, aliases or branch stock at all.
 *
 * A term shorter than two characters returns [] server-side; calling anyway would be a round trip
 * for a guaranteed-empty answer, so the caller debounces AND the server clamps.
 */
export async function counterSearch(q: string, branchId: string, limit = 20): Promise<CounterHit[]> {
  const params = new URLSearchParams({ q, limit: String(limit) });
  if (branchId) params.set('branch_id', branchId);
  const res = await api(`/api/v1/products/counter-search?${params.toString()}`);
  if (!res.ok) throw new Error(`search ${res.status}`);
  const body = (await res.json()) as { items: CounterHit[] };
  return body.items ?? [];
}

/**
 * counterPrices: the CUSTOMER-AWARE price picture for a screen's worth of products, in one call.
 *
 * Every cent in the answer was decided by the pricing waterfall server-side (POST
 * /api/v1/pos/counter-prices → pricing.CounterQuotes, the same authority the ring prices through).
 * The counter formats these numbers and never derives one: the house rule that money is never
 * computed client-side is why this read exists at all rather than the browser subtracting a
 * discount off a list price it happens to be holding.
 *
 * An empty customer id is the ordinary WALK-IN case, not a missing argument: retail stands alone
 * and every row comes back with no saving and no basis. The counter asks the same question either
 * way.
 */
export async function counterPrices(
  branchId: string,
  customerId: string,
  items: { product_id: string; qty_entered?: number; uom_entered?: string }[],
): Promise<{ customer_type: string; prices: CounterQuote[] }> {
  const res = await api('/api/v1/pos/counter-prices', {
    method: 'POST',
    body: JSON.stringify({
      branch_id: branchId,
      customer_id: customerId,
      items: items.map((i) => ({
        product_id: i.product_id,
        qty_entered: i.qty_entered ?? 1,
        uom_entered: i.uom_entered ?? '',
      })),
    }),
  });
  if (!res.ok) throw new Error(`counter-prices ${res.status}`);
  const body = (await res.json()) as { customer_type?: string; prices?: CounterQuote[] };
  return { customer_type: body.customer_type ?? '', prices: asRows<CounterQuote>(body, ['prices']) };
}

/**
 * AccountStanding: the Customer step's past-due FACT, role-gated SERVER-SIDE.
 *
 * Every field is optional because the whole answer is optional: a walk-in, a current account, a
 * caller the dealer's balance_visibility policy does not allow, and an AR read that failed all
 * return `{}`. The client renders nothing for `{}` and asks no questions about which of those it
 * was: that indistinguishability is deliberate (see backend/internal/pos/standing.go): a counter
 * must never be told an account is current because a query timed out, and must never see a fault
 * banner over a fact nobody asked for.
 */
export interface AccountStanding {
  past_due_cents?: number;
  oldest_bucket?: string;
  on_hold?: boolean;
}

/** GET /api/v1/pos/account-standing. A failure is an empty standing, never a thrown error. */
export async function accountStanding(branchId: string, customerId: string): Promise<AccountStanding> {
  if (!customerId) return {};
  const params = new URLSearchParams({ branch_id: branchId, customer_id: customerId });
  const res = await api(`/api/v1/pos/account-standing?${params.toString()}`);
  if (!res.ok) return {};
  return (await res.json()) as AccountStanding;
}

/**
 * posHotkeys: the dealer's counter key roster (GET /api/v1/pos/hotkeys).
 *
 * The bindings are configuration, resolved user-over-dealer-over-default server-side. A failed read
 * is not an error here: the caller falls back to the shipped roster, because a till whose keys
 * stopped working over a settings timeout is a till nobody trusts again.
 */
export async function posHotkeys(branchId: string): Promise<unknown> {
  const params = new URLSearchParams();
  if (branchId) params.set('branch_id', branchId);
  const res = await api(`/api/v1/pos/hotkeys?${params.toString()}`);
  if (!res.ok) throw new Error(`hotkeys ${res.status}`);
  return (await res.json()) as unknown;
}

/** One tile of the drillable hot-list, or one of its category/subcategory nodes. */
export interface HotPick {
  product_id: string;
  sku: string;
  name: string;
  size_line?: string;
  uom: string;
  has_image: boolean;
  category: string;
  subcategory: string;
  price?: CounterQuote;
}

/**
 * quickPicks: the dealer-curated hot list (GET /api/v1/pos/quick-picks).
 *
 * The SET is curation (settings kit, surface `pos.counter`); the STRUCTURE is the client's drill:
 * category → subcategory → item. The server sends flat rows carrying their two group names and the
 * frontend builds the tree, because which grouping reads well is a presentation decision and the
 * substrate must not have to migrate when it changes.
 */
export async function quickPicks(branchId: string, customerId: string): Promise<{ source: string; items: HotPick[] }> {
  const params = new URLSearchParams();
  if (branchId) params.set('branch_id', branchId);
  if (customerId) params.set('customer_id', customerId);
  const res = await api(`/api/v1/pos/quick-picks?${params.toString()}`);
  if (!res.ok) throw new Error(`quick-picks ${res.status}`);
  const body = (await res.json()) as { source?: string };
  return { source: typeof body.source === 'string' ? body.source : '', items: asRows<HotPick>(body, ['items']) };
}

/**
 * resolveScan turns one scanned barcode (or a typed SKU) into a product: the pos-barcode ticket.
 *
 * Returns null for an unknown code rather than throwing, because "no such code" is an ordinary
 * counter event (a torn label, a product with no barcode on file) that the scan box reports inline.
 * Any OTHER failure throws, because a network or auth problem must not read as "unknown barcode".
 */
export async function resolveScan(code: string, branchId: string): Promise<CounterHit | null> {
  const params = new URLSearchParams({ code });
  if (branchId) params.set('branch_id', branchId);
  const res = await api(`/api/v1/products/scan?${params.toString()}`);
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`scan ${res.status}`);
  return (await res.json()) as CounterHit;
}

// SaleSummary mirrors internal/pos SaleSummary: one row of the past-sale lookup.
export interface SaleSummary {
  sale_id: string;
  order_id: string;
  order_no: string;
  invoice_id?: string;
  invoice_no?: string;
  customer_id: string;
  customer_name: string;
  branch_id: string;
  till_session_id?: string;
  sale_type: string;
  tender: string;
  split_tender: boolean;
  total_cents: number;
  status: string;
  created_at: string;
  line_count: number;
}

export interface SaleLookupQuery {
  branch_id: string;
  till_session_id?: string;
  q?: string;
  from?: string;
  to?: string;
  limit?: number;
}

/** lookupSales is the branch-fenced past-sale / till-session search (DIB-165). */
export async function lookupSales(query: SaleLookupQuery): Promise<SaleSummary[]> {
  const params = new URLSearchParams({ branch_id: query.branch_id });
  if (query.till_session_id) params.set('till_session_id', query.till_session_id);
  if (query.q) params.set('q', query.q);
  if (query.from) params.set('from', query.from);
  if (query.to) params.set('to', query.to);
  if (query.limit) params.set('limit', String(query.limit));
  const res = await api(`/api/v1/pos/sales?${params.toString()}`);
  if (!res.ok) {
    if (res.status === 403) throw new Error('You can only look up sales for your own branch.');
    throw new Error(`sale lookup ${res.status}`);
  }
  const body = (await res.json()) as { items: SaleSummary[] };
  return body.items ?? [];
}

// TenderLeg is one settled leg on a reopened sale.
export interface TenderLeg {
  seq: number;
  method: string;
  amount_cents: number;
  payment_id?: string;
  gateway_ref?: string;
}

export interface SaleViewLine {
  line_no: number;
  product_id: string;
  description: string;
  qty_entered: number;
  uom_entered: string;
  unit_price_cents: number;
  price_uom: string;
  line_total_cents: number;
}

export interface SaleView {
  order_id: string;
  order_no: string;
  customer_id: string;
  branch_id: string;
  sale_type: string;
  tender: string;
  status: string;
  subtotal_cents: number;
  tax_cents: number;
  total_cents: number;
  tax_reason_code: string;
  invoice_id: string;
  invoice_no: string;
  payment_id?: string;
  lines: SaleViewLine[];
  split_tender?: boolean;
  tenders?: TenderLeg[];
}

/** getSale reopens one committed sale: branch-fenced server-side (403 for another branch). */
export async function getSale(id: string): Promise<SaleView> {
  const res = await api(`/api/v1/pos/sales/${encodeURIComponent(id)}`);
  if (!res.ok) {
    if (res.status === 403) throw new Error('That sale belongs to another branch.');
    if (res.status === 404) throw new Error('Sale not found.');
    throw new Error(`sale ${res.status}`);
  }
  return (await res.json()) as SaleView;
}

// NOTE: there is deliberately no receiptUrl() helper here. The receipt route is staff-gated, so a
// URL is not usable as an <img>/<a>/window.open target: it needs the Bearer token. Reprint goes
// through printReceipt() in receipt-print.ts (DIB-410), which fetches with the token and writes the
// document into a blank window. A URL helper in this file was exactly the trap that produced a
// customer-facing `{"error":"unauthorized"}` window during the W3-A walkthrough.

/**
 * putProductImage / deleteProductImage: the PHOTO PER TILE (ruling R2(a): an admin curates
 * "the photo for each tile"). Manager-gated server-side; the refusal arrives in words.
 *
 * WHY RAW fetch AND NOT api(). api() stamps application/json on every non-FormData body, and this
 * route reads the RAW image bytes and validates the declared content type against an allowlist:
 * a JSON header would make every upload a 415. The Authorization header is added the same way
 * api() adds it, from the same stored token.
 */
// HH note: the reference composed this multipart PUT by hand and attached the raw session token
// itself. The host composes the upload now (ports.ts, named in CLOSURE.md), same signature.
export { putProductImage } from './ports.js';

export async function deleteProductImage(productId: string): Promise<Response> {
  return api(`/api/v1/products/${encodeURIComponent(productId)}/image`, { method: 'DELETE' });
}

/** productImageUrl is the thumbnail byte route (the product-images-ui ticket). */
export function productImageUrl(productId: string): string {
  return `/api/v1/products/${encodeURIComponent(productId)}/image`;
}

// ── DIB-488 rev 8 (item 10): the role-gated margin glance ───────────────────────────────────────
// The server computes margins from ITS OWN numbers (engine revenue × the WAC cost source) and the
// marshal layer gates the payload by role: a non-allowed caller receives `{}`: every field below
// is optional BECAUSE it may be byte-absent. The UI renders the column/footers only when the data
// is present; it never computes a margin itself.

export interface MarginGlanceLine {
  margin_bps?: number;
  est_profit_cents?: number;
}

export interface MarginGlanceResult {
  lines?: MarginGlanceLine[];
  margin_bps?: number;
  est_profit_cents?: number;
  partial?: boolean;
}

export async function marginGlance(body: {
  branch_id: string;
  customer_id: string;
  customer_type: string;
  lines: { product_id: string; qty_entered: number; uom_entered: string }[];
}): Promise<MarginGlanceResult> {
  const res = await api('/api/v1/pos/margin-glance', { method: 'POST', body: JSON.stringify(body) });
  if (!res.ok) throw new Error(`margin ${res.status}`);
  return (await res.json()) as MarginGlanceResult;
}

// ── DIB-502 (rev-8 item 8): the suggested add-ons strip ─────────────────────────────────────────

export interface AddonSuggestion {
  product_id: string;
  sku: string;
  name: string;
  size_line?: string;
  uom: string;
  default_qty: number;
  list_price_cents?: number;
  price_uom?: string;
  has_image: boolean;
  application?: string;
}

/** Empty is an ordinary answer (the strip hides entirely): never an error surface. */
export async function addonSuggestions(productIds: string[]): Promise<AddonSuggestion[]> {
  const q = encodeURIComponent(productIds.join(','));
  const res = await api(`/api/v1/pos/addon-suggestions?product_ids=${q}`);
  if (!res.ok) throw new Error(`addons ${res.status}`);
  const body = (await res.json()) as { suggestions?: AddonSuggestion[] };
  return Array.isArray(body.suggestions) ? body.suggestions : [];
}
