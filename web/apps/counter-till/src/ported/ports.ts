// ports.ts: what the ported POS surface needs from its host, and nothing else.
//
// The port (see CLOSURE.md for the file-by-file map) stops at the host's edge.
// Everything here was, in the reference, the app's own api client, session,
// router, navigation registry, or a neighbouring screen. The wiring item
// implements these seams over the host's own parts: the auth package's custody
// (the bearer lives in memory in the SessionStore, and this file only reads it
// through the provider the app root wires in), the demo adapter's minted
// identity, and the app's one house-day conversion.
//
// Two house rules are baked into the shape of this file:
//
//   1. The core computes money. Where the reference computed a money figure in
//      the browser (a subtotal sum, a tax line, a change-due, a refund
//      estimate, a credit headroom, a balance after deposit, a typed dollar
//      amount parsed through a float), the computation is left out of the
//      ported files entirely and reappears here as a host call. Each such seam
//      carries the reference's signature so the ported call sites stay
//      shaped as they were, and every figure a seam answers is assembled from
//      figures the core stated.
//   2. Every seam fails loudly until wired: calling one before the app root
//      has wired its host throws the port message rather than returning a
//      plausible answer. An honest refusal beats a quiet zero at a counter.

import { DEMO_CARD_TOKEN, HOUSE_ZONE, houseDayInZone } from "../till-client.js";

/**
 * The port seam every unwired host call raises. The message names the seam so
 * the first person to hit it in a browser knows which line of this file to
 * implement, not which ported component to distrust.
 */
function unwired(seam: string): never {
  throw new Error(
    `ported surface: the host seam "${seam}" is not wired yet; see src/ported/ports.ts and CLOSURE.md`,
  );
}

// ---------------------------------------------------------------------------
// The host wiring
// ---------------------------------------------------------------------------

/**
 * The host the app root hands this file once, at construction. Nothing here
 * touches custody directly: the header provider is the app's own closure over
 * its SessionStore, so the bearer never rests in a second place and sign-out
 * is visible here the moment it happens.
 */
export interface PortHost {
  /** Headers for every api() call: the demo org plus the live bearer. */
  headers(): Record<string, string> | Promise<Record<string, string>>;
  /** The dealer's day in the house zone: the ring's requested-date default. */
  houseDay(): string;
  /** The signed-in staff member, in the mint's own IdentityUser shape, or null. */
  user(): AuthUser | null;
  /** The live bearer, only ever to hand a session across a window boundary. */
  token(): string;
}

/** True when the host has been wired and carries a live session. */
function sessionLive(): boolean {
  return host !== null && host.user() !== null && host.token() !== "";
}

let host: PortHost | null = null;

/**
 * Wire the host. Called once by the app root before any ported element mounts;
 * re-wiring replaces the host wholesale (a fresh app root in a test, say).
 */
export function wirePorts(wired: PortHost): void {
  host = wired;
}

// ---------------------------------------------------------------------------
// Session facts (the reference's api.ts session half)
// ---------------------------------------------------------------------------

/** The signed-in staff member, in the reference's shape. */
export interface AuthUser {
  user_id: string;
  email: string;
  display_name: string;
  role_code: string;
  role_name: string;
  tier: number;
  scope: string;
}

/** One yard branch the till can sit behind. */
export interface Branch {
  id: string;
  name: string;
}

/**
 * The branches the dealer runs, as the login reaches them. Empty until the app
 * root loads them from the branches route (the one branch this login works),
 * which happens before the terminal mounts: an honest blank, never a guessed
 * yard. The list is filled in place so a branch default read at element
 * construction time sees what the server answered.
 */
export const BRANCHES: Branch[] = [];

/**
 * Replace the branch list in place: the ported surface holds the array's
 * identity (a module constant read at construction), so the host fills the
 * same array rather than rebinding it.
 */
export function setBranches(list: readonly Branch[]): void {
  BRANCHES.length = 0;
  for (const row of list) BRANCHES.push({ id: row.id, name: row.name });
}

/** The branch's name, or a short form of its id when the list does not know it. */
export function branchName(id: string): string {
  if (!id) return "";
  const b = BRANCHES.find((x) => x.id === id);
  return b ? b.name : id.slice(0, 8);
}

/**
 * The signed-in user, or null between sessions. The shape is the demo mint's
 * own IdentityUser block (user_id, email, display_name, role_code, role_name,
 * tier, scope), handed over unchanged: the ported surface reads the session
 * facts the server stated, never re-derived ones.
 */
export function getCurrentUser(): AuthUser | null {
  if (host === null) return unwired("getCurrentUser");
  return host.user();
}

/**
 * Whether the signed-in user clears the platform-admin gate. The same shape
 * the core's own gate reads (a Tier-one global scope): the demo's two flow
 * accounts are branch-scoped staff, so this answers false for both, and the
 * counter-settings entry the gate holds stays closed on the demo box exactly
 * as the reviewed rule keeps it.
 */
export function isAdmin(): boolean {
  const me = getCurrentUser();
  return me !== null && me.tier === 1 && me.scope === "GLOBAL";
}

/**
 * The raw bearer token, only ever to hand a session across a window boundary
 * (the pop-out). Never placed in a URL.
 */
export function getSessionToken(): string {
  if (host === null) return unwired("getSessionToken");
  return host.token();
}

// ---------------------------------------------------------------------------
// The data adapter: the ported surface's one request door
// ---------------------------------------------------------------------------

// The demo card token: the fixed demo token this app sends on a card tender.
// The reference carried its own simulator's token spelling; the host rewrites
// any card token the ported surface sends onto the app's own constant, so the
// wire carries the one token the reviewed rule pins (till-client.ts names it).

/** The paths whose request bodies the adapter enriches to the contract's shape. */
const RING_PATH = "/api/v1/pos/sales";
const RESOLVE_PATH = "/api/v1/pricing/resolve";

/**
 * Fill the ring body's contract-required fields the reference's caller did not
 * know: cash_sale, the card token, the signature and notes strings, the
 * offline flag, and the requested date, which is the dealer's day in the house
 * zone when the surface left it unset (the daily-means-the-dealer's-day rule;
 * the binding date is still validated server-side). A card token the surface
 * sends is swapped for the app's own fixed demo token.
 */
function enrichRingBody(body: Record<string, unknown>, houseDay: string): Record<string, unknown> {
  const tender = typeof body["tender"] === "string" ? body["tender"] : "";
  const deposit = typeof body["deposit_cents"] === "number" ? body["deposit_cents"] : 0;
  const cardAtRing =
    (tender === "CARD" && deposit <= 0) ||
    (deposit > 0 && body["deposit_method"] === "CARD");
  const token = typeof body["payment_token"] === "string" ? body["payment_token"] : "";
  const legs = Array.isArray(body["tenders"]) ? body["tenders"] : null;
  return {
    ...body,
    cash_sale: tender === "CASH" && deposit <= 0,
    payment_token: cardAtRing && token !== "" ? DEMO_CARD_TOKEN : token,
    signature_ref: typeof body["signature_ref"] === "string" ? body["signature_ref"] : "",
    notes: typeof body["notes"] === "string" ? body["notes"] : "",
    offline: body["offline"] === true,
    requested_date:
      typeof body["requested_date"] === "string" && body["requested_date"] !== ""
        ? body["requested_date"]
        : houseDay,
    // A split's CARD legs each carry their own token; the same swap applies.
    ...(legs !== null
      ? {
          tenders: legs.map((leg) => {
            if (leg === null || typeof leg !== "object") return leg;
            const row = leg as Record<string, unknown>;
            const legToken = typeof row["payment_token"] === "string" ? row["payment_token"] : "";
            return legToken !== "" ? { ...row, payment_token: DEMO_CARD_TOKEN } : row;
          }),
        }
      : {}),
  };
}

/**
 * Fill the resolve body's contract-required fields the reference's caller did
 * not know: the product's category id (the catalogue resolve reads it from the
 * product server-side when the caller cannot name it), the override flag and
 * the document reference the quote flow names.
 */
function enrichResolveBody(body: Record<string, unknown>): Record<string, unknown> {
  return {
    category_id: typeof body["category_id"] === "string" ? body["category_id"] : "",
    has_approved_override: body["has_approved_override"] === true,
    doc_ref_type: typeof body["doc_ref_type"] === "string" ? body["doc_ref_type"] : "order",
    doc_ref_id: typeof body["doc_ref_id"] === "string" ? body["doc_ref_id"] : "",
    ...body,
  };
}

/**
 * The authed request helper the whole ported surface talks through: relative
 * path in, the demo org and bearer attached, Response out.
 *
 * Two shape adaptations live here, each named where it runs below: the ring
 * and resolve bodies gain the contract-required fields the reference's callers
 * did not know, and the parked list is unwrapped from the contract's page
 * shape to the bare rows the ported panel reads. Everything else is the
 * contract's own path and shape, byte for byte.
 */
export async function api(path: string, init: RequestInit = {}): Promise<Response> {
  if (host === null) return unwired("api");
  const headers = new Headers(init.headers);
  for (const [name, value] of Object.entries(await host.headers())) {
    headers.set(name, value);
  }
  // Default to JSON, but leave FormData bodies alone so the browser sets the
  // multipart content type with its generated boundary.
  let body = init.body;
  if (body !== undefined && !(body instanceof FormData) && typeof body === "string") {
    headers.set("Content-Type", "application/json");
    // The body adaptations, parsed and rewritten before anything dials.
    const pathOnly = path.split("?")[0] ?? path;
    try {
      const parsed: unknown = JSON.parse(body);
      if (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed)) {
        const record = parsed as Record<string, unknown>;
        const adapted =
          pathOnly === RING_PATH
            ? enrichRingBody(record, host.houseDay())
            : pathOnly === RESOLVE_PATH
              ? enrichResolveBody(record)
              : record;
        body = JSON.stringify(adapted);
      }
    } catch {
      // not JSON: send it as it arrived and let the server refuse it
    }
  }
  const response = await fetch(path, { ...init, body, headers });
  // The parked list arrives in the contract's page shape ({items, limit}); the
  // ported panel reads the bare rows. Unwrapped here, once, so every ported
  // call site keeps the reference's own shape: the rows themselves are
  // untouched, and an answer of any other shape passes through as it came.
  const pathOnly = path.split("?")[0] ?? path;
  if (pathOnly === "/api/v1/pos/parked" && (init.method ?? "GET") === "GET" && response.ok) {
    try {
      const parsed: unknown = await response.json();
      return new Response(JSON.stringify(parsed), {
        status: response.status,
        statusText: response.statusText,
        headers: { "Content-Type": "application/json" },
      });
    } catch {
      // a body that cannot be re-read is returned as it is
    }
  }
  return response;
}

// ---------------------------------------------------------------------------
// Navigation (the reference's hash-route.ts, dashboard-nav.ts and nav-access.ts)
// ---------------------------------------------------------------------------

/**
 * The hash a navigation target produces: `#home`, `#apps`, `#unit` or
 * `#unit/module`, in the reference's own spelling. This app is one surface
 * (the till), so the hashes it produces are well-formed addresses of the one
 * module the host carries; nothing here writes location state.
 */
export function hashTargetFor(active: string, module?: string): string {
  if (active === "home") return "#home";
  if (active === "apps") return "#apps";
  return module && module !== "overview" ? `#${active}/${module}` : `#${active}`;
}

/**
 * The shell's unit and module keys, as the reference's frozen registry named
 * them. The host's own registry re-derives the live set; these unions are the
 * reference's shapes so the ported icon and hotkey tables keep compiling as
 * complete records.
 */
export type NavUnitKey =
  | 'sales'
  | 'finance'
  | 'inventory'
  | 'compliance'
  | 'messaging'
  | 'warranty'
  | 'dispatch'
  | 'yard'
  | 'purchasing'
  | 'reporting'
  | 'admin';

export type NavModuleKey =
  | 'catalogue'
  | 'calculator'
  | 'quote'
  | 'quotes'
  | 'pos'
  | 'invoices'
  | 'returns'
  | 'pricing'
  | 'gl'
  | 'bankrec'
  | 'statements'
  | 'ar'
  | 'inventory'
  | 'lien'
  | 'tax'
  | 'warranty'
  | 'messaging'
  | 'dispatch'
  | 'yard'
  | 'purchasing'
  | 'reporting'
  | 'staff'
  | 'approvals'
  | 'audit'
  | 'assist'
  | 'assist-admin'
  | 'hotkeys'
  | 'account';

const UNIT_KEYS: readonly NavUnitKey[] = [
  "sales",
  "finance",
  "inventory",
  "compliance",
  "messaging",
  "warranty",
  "dispatch",
  "yard",
  "purchasing",
  "reporting",
  "admin",
];

const MODULE_KEYS: readonly NavModuleKey[] = [
  "catalogue",
  "calculator",
  "quote",
  "quotes",
  "pos",
  "invoices",
  "returns",
  "pricing",
  "gl",
  "bankrec",
  "statements",
  "ar",
  "inventory",
  "lien",
  "tax",
  "warranty",
  "messaging",
  "dispatch",
  "yard",
  "purchasing",
  "reporting",
  "staff",
  "approvals",
  "audit",
  "assist",
  "assist-admin",
  "hotkeys",
  "account",
];

export function isNavUnitKey(v: string): v is NavUnitKey {
  return (UNIT_KEYS as readonly string[]).includes(v);
}

export function isNavModuleKey(v: string): v is NavModuleKey {
  return (MODULE_KEYS as readonly string[]).includes(v);
}

/** Who may see an entry point: the reference's gate vocabulary. */
export type Gate = "always" | "authed" | "manager" | "admin";

/** The caller's grants as the three predicates the shell computes. */
export interface AccessRole {
  authed: boolean;
  manager: boolean;
  admin: boolean;
}

/**
 * The per-screen gate table, answered from the host's role model. This micro
 * app carries one surface: the till (`pos`), which any signed-in counter role
 * works (the entry cards are the gate's front door). The other modules name
 * screens this app does not carry, so their gates hold at `admin`: an action
 * that named one refuses as data (the registry's own words: a visible dead
 * click, never a silent parallel navigation path), and nobody on the demo box
 * clears the gate that would open it.
 */
export const SCREEN_GATE: Record<NavModuleKey, Gate> = MODULE_KEYS.reduce(
  (table, key) => {
    table[key] = key === "pos" ? "authed" : "admin";
    return table;
  },
  {} as Record<NavModuleKey, Gate>,
);

/** Does this role clear this gate? The host answers from its own role model. */
export function canSeeGate(gate: Gate, role: AccessRole): boolean {
  if (gate === "always") return true;
  if (gate === "authed") return role.authed;
  if (gate === "manager") return role.manager;
  return role.admin;
}

/** The modules of one unit this role may see, in rail order. */
export function visibleModuleKeys(unit: NavUnitKey, role: AccessRole): NavModuleKey[] {
  // The host's live registry: the sales unit carries the till; the other units
  // name surfaces this app does not carry, so they have no visible modules
  // here for any role.
  if (unit !== "sales") return [];
  return canSeeGate(SCREEN_GATE["pos"], role) ? ["pos"] : [];
}

// ---------------------------------------------------------------------------
// The neighbouring screens the surface opens (left unported, see CLOSURE.md)
// ---------------------------------------------------------------------------

/**
 * One document in an order's family (quote, order, invoice), as the surface's
 * related-documents chip renders it. The drawer itself is a host screen.
 */
export interface RelatedDoc {
  kind: string; // QUOTE | ORDER | INVOICE
  id: string;
  number: string;
  status: string;
  /** ISO timestamp: when the document came to exist. */
  date: string;
  total_cents: number;
  parent_id?: string;
  /** PORTAL | COUNTER: how an order was minted (absent on older orders). */
  origin?: string;
  /** Which numbered acceptance minted it (2+ means a repeat acceptance). */
  accept_seq?: number;
  /** The document the caller asked from. */
  self?: boolean;
}

// ---------------------------------------------------------------------------
// Money: the core computes, the host answers (every reference-side computation
// behind these seams is named in CLOSURE.md and in the item's report)
// ---------------------------------------------------------------------------

/** One parsed answer for the optional cash-tendered quick entry. */
export interface CashTenderedPlan {
  /** The parsed tendered amount in cents (0 when the box is empty or refused). */
  tenderedCents: number;
  /** Tendered less total, when sendable; 0 otherwise. */
  changeDueCents: number;
  /** True only when the entry covers the total. */
  sendable: boolean;
}

/**
 * The quick entry's plan: parse what the cashier typed (through the decimal
 * string, never a float), compare it with the total and derive the change due.
 * The host owns all three steps.
 */
export function cashTenderedPlan(totalCents: number, rawDollars: string): CashTenderedPlan {
  return unwired('cashTenderedPlan');
}

/**
 * Parse what the cashier typed into cents, or NaN when it is not an amount.
 * NaN rather than 0 on purpose: an empty box and a deliberate zero are
 * different mistakes, and the split tender panel words them differently.
 */
export function parseAmountCents(raw: string): number {
  return unwired('parseAmountCents');
}

/** One split leg the cashier is composing: the method and what they typed, parsed to cents. */
export interface SplitLeg {
  method: 'CASH' | 'CARD' | 'CHEQUE' | 'ACCOUNT';
  amountCents: number;
}

/** The live state of a split as the cashier reads it. */
export interface SplitReadiness {
  /** The sum of the legs, in cents. */
  tenderedCents: number;
  /** The total less the tendered amount: positive is still owed, negative is over-tendered. */
  remainingCents: number;
  /** True only when the legs settle the sale exactly and the shape is legal. */
  ready: boolean;
  /** Why it is not ready, in words a cashier can act on; empty when ready. */
  problem: string;
}

/**
 * The split panel's live verdict: the host sums the typed legs, compares them
 * with the ticket total and words the gap, so the panel enables Ring only on
 * the core's own arithmetic.
 */
export function evaluateSplit(
  legs: readonly SplitLeg[],
  totalCents: number,
  hasCustomer: boolean,
): SplitReadiness {
  return unwired('evaluateSplit');
}

/**
 * What is left for the leg being filled: the total less the other legs, and
 * never an over-tender (0 once the sale is covered).
 */
export function autoFillRemainder(
  legs: readonly SplitLeg[],
  totalCents: number,
  indexBeingFilled: number,
): number {
  return unwired('autoFillRemainder');
}

/** The cart's subtotal, over the line totals the core already priced. */
export function cartSubtotalCents(lines: readonly { lineTotalCents: number }[]): number {
  return unwired('cartSubtotalCents');
}

/** The ticket's tax line at the given rate in basis points. The core owns tax. */
export function ticketTaxCents(subtotalCents: number, rateBps: number): number {
  return unwired('ticketTaxCents');
}

/** The ticket's total: subtotal plus the tax line the core computed. */
export function ticketTotalCents(subtotalCents: number, taxCents: number): number {
  return unwired('ticketTotalCents');
}

/** Parse the typed deposit box into cents through the decimal string; 0 when refused. */
export function depositCentsFromTyped(raw: string): number {
  return unwired('depositCentsFromTyped');
}

/**
 * One line's contribution to what the ticket saves against retail, as the
 * server priced it: the per-line saving times the quantity, summed by the host
 * because the multiplication is money arithmetic.
 */
export function sumLineSavingCents(
  entries: readonly { saving_cents: number; qty: number }[],
): number {
  return unwired('sumLineSavingCents');
}

/** A sale's balance once its deposit is taken, clamped by the host's rule. */
export function balanceAfterDepositCents(totalCents: number, depositCents: number): number {
  return unwired('balanceAfterDepositCents');
}

/** One returnable order line, as the refund estimate reads it. */
export interface ReturnEstimateLine {
  order_line_id: string;
  unit_price_cents: number;
  tax_cents: number;
  qty_sold: number;
}

/**
 * The returns desk's display estimate for the picked quantities. Display
 * only: the server derives the binding refund inside its own transaction.
 */
export function refundEstimateCents(
  lines: readonly ReturnEstimateLine[],
  qty: Record<string, number>,
): { subtotal: number; tax: number; total: number } {
  return unwired('refundEstimateCents');
}

/**
 * The account's available headroom for the strip and the picker to display,
 * already clamped by the host's rule: the core computes credit, never the till.
 */
export function availableCreditCents(customer: {
  credit_limit_cents: number;
  open_balance_cents: number;
}): number {
  return unwired('availableCreditCents');
}

// ---------------------------------------------------------------------------
// The detached panel layout's persisted store
// ---------------------------------------------------------------------------
//
// The reference kept the multi-card surface's box positions in the browser's
// local store, per device, under a versioned key. In HH the host owns
// persistence (this app's credential sink rule reserves the browser's stores),
// so the ported panel module asks these seams with the same key and the same
// raw-string contract; the wiring item picks the backing store.

/** Whatever the device remembers under this key, as the stored string, or null. */
export function readPanelLayoutRaw(key: string): string | null {
  return unwired('readPanelLayoutRaw');
}

/** Best effort persist of the stored string under this key. A refusing store just forgets. */
export function writePanelLayoutRaw(key: string, value: string): void {
  return unwired('writePanelLayoutRaw');
}

/** Forget the layout under this key; the next read returns the default tiling. */
export function clearPanelLayoutRaw(key: string): void {
  return unwired('clearPanelLayoutRaw');
}

// ---------------------------------------------------------------------------
// Printing
// ---------------------------------------------------------------------------
//
// The reference printed a receipt by writing the server-rendered document into
// a fresh blank window with the browser's document writer, then calling
// print. This app's fence on raw markup paths reserves that write for the
// host, and each shell (a browser tab, a Tauri window) prints through its own
// chrome, so the choreography is the host's.

/** The receipt window the flow opens: injectable, so the reference kept it testable. */
export type { ReceiptWindow } from './receipt-print-logic.js';

/**
 * Write a rendered receipt into a freshly opened window and print it. A
 * blocked pop-up is a real and common counter failure (browsers block
 * window.open outside a user gesture); it must surface as the operator
 * message the reference worded, never a silent no-op.
 */
export function printReceiptInto(
  win: import('./receipt-print-logic.js').ReceiptWindow | null,
  receiptHtml: string,
): import('./receipt-print-logic.js').PrintOutcome {
  return unwired('printReceiptInto');
}
