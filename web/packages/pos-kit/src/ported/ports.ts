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
//      the browser (a subtotal sum, a change-due, a refund
//      estimate, a credit headroom, a balance after deposit, a typed dollar
//      amount parsed through a float), the computation is left out of the
//      ported files entirely and reappears here as a host call. Each such seam
//      carries the reference's signature so the ported call sites stay
//      shaped as they were, and every figure a seam answers is assembled from
//      figures the core stated. One pair of the reference's assemblies is not
//      rerouted but REMOVED (decision log row 661): the ticket's tax line and
//      total were browser guesses at a rate the contract never serves, so the
//      seam carries neither, and the ring states both figures.
//   2. Every seam fails loudly until wired: calling one before the app root
//      has wired its host throws the port message rather than returning a
//      plausible answer. An honest refusal beats a quiet zero at a counter.

import { formatDollars } from "./_money-format.js";
import { DEMO_CARD_TOKEN, HOUSE_ZONE, houseDayInZone } from "./host-facts.js";

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
  /**
   * The drawn product tile for a category in a theme (the design system's product tiles), or
   * null when the category has none. Optional: a host that does not wire it leaves a product
   * with no real picture on the stacked-stone glyph.
   */
  productTile?(category: string, theme: 'light' | 'dark'): string | null;
  /**
   * The core's base URL when it is not the page's own origin (a console copy points the app at the
   * dealer's core); absent or empty keeps every call relative, as the demo's always were.
   */
  baseUrl?: string;
  /**
   * The fetch the request door sends through. Optional: a host that does not
   * supply one uses the platform's. The Work shell supplies one that refuses a
   * write with no signal (money never queues) and answers a cached read from
   * the device when the network does not.
   */
  fetch?(input: string, init?: RequestInit): Promise<Response>;
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

/** The host's drawn tile for a category, or null: unwired, or a category the tiles do not know. */
export function productTile(category: string, theme: 'light' | 'dark'): string | null {
  return host?.productTile?.(category, theme) ?? null;
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
// wire carries the one token the reviewed rule pins (host-facts.ts names it: the constant moved here from the app's till-client.ts with this package).
// DEMO ONLY, DELIBERATELY: this whole app is the demo's till (the served
// phrase gate holds the demo box), and the rewrite below is part of that
// demo's wiring. The day a real rail serves this surface, this rewrite is
// what must go with it: a real terminal's token must ride verbatim, never be
// masked by the demo's constant.

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
 * The error a failed read throws. It names the route and the status, as it
 * always has, except for one case: a refusal the HOST made before anything
 * dialed (the Work shell's no signal guard marks it with a header). That
 * refusal carries its own honest words in the shared error envelope, and those
 * words are what the person reads, never a bare status.
 */
export async function refusal(response: Response, what: string): Promise<Error> {
  if (response.headers.get('X-HH-Host-Refusal') === '1') {
    try {
      const body: unknown = await response.clone().json();
      const words = body !== null && typeof body === 'object' ? (body as Record<string, unknown>)['error'] : undefined;
      if (typeof words === 'string' && words !== '') return new Error(words);
    } catch {
      // an unreadable refusal falls back to the status below
    }
  }
  return new Error(`${what} ${response.status}`);
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
  const base = (host.baseUrl ?? '').replace(/\/+$/, '');
  const response = await (host.fetch ?? fetch)(`${base}${path}`, { ...init, body, headers });
  // The parked list arrives in the contract's page shape ({items, limit}); the
  // ported panel reads the bare rows. Unwrapped here, once, so every ported
  // call site keeps the reference's own shape: the rows themselves are
  // untouched, and an answer of any other shape passes through as it came.
  const pathOnly = path.split("?")[0] ?? path;
  if (pathOnly === "/api/v1/pos/parked" && (init.method ?? "GET") === "GET" && response.ok) {
    try {
      const parsed: unknown = await response.json();
      const rows =
        parsed !== null &&
        typeof parsed === "object" &&
        !Array.isArray(parsed) &&
        Array.isArray((parsed as Record<string, unknown>)["items"])
          ? ((parsed as Record<string, unknown>)["items"] as unknown[])
          : parsed;
      return new Response(JSON.stringify(rows), {
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
 * Parse what the cashier typed into cents, through the decimal string, never a
 * float multiply: "40" becomes 4000, "$1,234.56" becomes 123456. Currency
 * symbols, spaces and thousands separators are stripped because people paste
 * from invoices; anything else remaining is refused. NaN rather than 0 on
 * purpose: an empty box and a deliberate zero are different mistakes, and the
 * split tender panel words them differently.
 */
export function parseAmountCents(raw: string): number {
  const cleaned = raw.replace(/[$,\s]/g, "");
  if (cleaned === "" || !/^\d*\.?\d*$/.test(cleaned)) return NaN;
  const point = cleaned.indexOf(".");
  if (point === -1) return Number(`${cleaned}00`);
  const whole = cleaned.slice(0, point);
  const fraction = cleaned.slice(point + 1);
  if (fraction.length > 2) return NaN;
  return Number(`${whole}${fraction.padEnd(2, "0")}`);
}

/** Parse the typed deposit box into cents through the decimal string; 0 when refused. */
export function depositCentsFromTyped(raw: string): number {
  const parsed = parseAmountCents(raw);
  return Number.isNaN(parsed) ? 0 : parsed;
}

/**
 * The quick entry's plan: parse what the cashier typed, compare it with the
 * total and answer the change due. The tendered figure is the typed string's
 * boundary conversion; the difference is the same subtraction the core
 * performs at the ring, where the binding change due is stated and the
 * receipt renders it. CALL IT ONLY WITH A SERVER-STATED TOTAL: before the
 * ring the till has none (row 661), so the cash tile words the ring instead
 * of calling this, and the typed amount rides the ring body for the server to
 * judge.
 */
export function cashTenderedPlan(totalCents: number, rawDollars: string): CashTenderedPlan {
  const parsed = parseAmountCents(rawDollars);
  const tendered = Number.isNaN(parsed) ? 0 : parsed;
  const sendable = tendered >= totalCents && totalCents > 0;
  const changeDueCents = sendable ? tendered - totalCents : 0;
  return { tenderedCents: tendered, changeDueCents, sendable };
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

/** The bound on split legs, mirrored from the contract's own ring shape (tenders, 2 to 6). */
export const MAX_SPLIT_LEGS = 6;

/**
 * The split panel's money word: the shared dollars spelling (the one place
 * integer cents become a customer facing amount): `$1,234.00`, with a minus.
 */
function splitFmt(cents: number): string {
  return formatDollars(cents);
}

/**
 * The split panel's live verdict: the typed legs are summed, compared with the
 * ticket total and the gap is worded, so the panel enables Ring only when the
 * legs settle the sale exactly and the shape is legal (two or more legs, at
 * most the contract's bound, one account leg at most, an account leg only with
 * a customer picked). The ring itself re-validates every one of these
 * server-side; these words are how the cashier learns the shape before the
 * refusal does. CALL IT ONLY WITH A SERVER-STATED TOTAL (row 661): before the
 * ring the till has none, so the panel reads its draft through
 * `splitDraftRead` instead, which compares the legs against nothing.
 */
export function evaluateSplit(
  legs: readonly SplitLeg[],
  totalCents: number,
  hasCustomer: boolean,
): SplitReadiness {
  let sum = 0;
  for (const leg of legs) sum += Number.isFinite(leg.amountCents) ? leg.amountCents : 0;
  const remainingCents = totalCents - sum;
  const problem = splitProblem(legs, totalCents, remainingCents, hasCustomer);
  return { tenderedCents: sum, remainingCents, ready: problem === "", problem };
}

function splitProblem(
  legs: readonly SplitLeg[],
  totalCents: number,
  remainingCents: number,
  hasCustomer: boolean,
): string {
  if (totalCents <= 0) return "Add a line before splitting the tender.";
  const shape = splitShapeProblem(legs, hasCustomer);
  if (shape !== "") return shape;
  if (remainingCents > 0) return `${splitFmt(remainingCents)} still to tender.`;
  if (remainingCents < 0) return `${splitFmt(-remainingCents)} over-tendered.`;
  return "";
}

/** The shape refusals both split reads share: the rules the server would also refuse, worded. */
function splitShapeProblem(legs: readonly SplitLeg[], hasCustomer: boolean): string {
  if (legs.length < 2) return "A split needs at least two tenders.";
  if (legs.length > MAX_SPLIT_LEGS) return `A split takes at most ${MAX_SPLIT_LEGS} tenders.`;
  if (legs.some((l) => !Number.isFinite(l.amountCents) || l.amountCents <= 0)) {
    return "Every tender needs an amount greater than zero.";
  }
  if (legs.filter((l) => l.method === "ACCOUNT").length > 1) {
    return "Only one tender can go on account.";
  }
  if (legs.some((l) => l.method === "ACCOUNT") && !hasCustomer) {
    return "Pick a customer account before putting part of the sale on account.";
  }
  return "";
}

/** The split panel's pre-ring read: the typed sum and the shape's verdict. */
export interface SplitDraftRead {
  /** The sum of what the operator has typed across the legs, in cents. */
  typedCents: number;
  /** Why the ring stays disarmed, in words a cashier can act on; empty when armed. */
  problem: string;
}

/**
 * The split panel's read before the ring: the legs' own typed sum (arithmetic
 * over the operator's own entries, hosted here so no component sums money) and
 * the shape refusals the server would also refuse. It compares the legs
 * against NOTHING: the total they must settle is the server's, stated at the
 * ring, and a browser guess at it is exactly what the till no longer computes
 * (decision log row 661). `evaluateSplit`, which does compare against a total,
 * is for a server-stated total only.
 */
export function splitDraftRead(
  legs: readonly SplitLeg[],
  hasCustomer: boolean,
  hasLines: boolean,
): SplitDraftRead {
  let typed = 0;
  for (const leg of legs) typed += Number.isFinite(leg.amountCents) ? leg.amountCents : 0;
  const problem = !hasLines ? "Add a line before splitting the tender." : splitShapeProblem(legs, hasCustomer);
  return { typedCents: typed, problem };
}

/**
 * What is left for the leg being filled: the total less the other legs, and
 * never an over-tender (0 once the sale is covered). CALL IT ONLY WITH A
 * SERVER-STATED TOTAL (row 661): before the ring the till has none, so the
 * panel's Rest affordance does not render and no leg fills.
 */
export function autoFillRemainder(
  legs: readonly SplitLeg[],
  totalCents: number,
  indexBeingFilled: number,
): number {
  let others = 0;
  for (const [index, leg] of legs.entries()) {
    if (index === indexBeingFilled) continue;
    others += Number.isFinite(leg.amountCents) ? leg.amountCents : 0;
  }
  const remainder = totalCents - others;
  return remainder > 0 ? remainder : 0;
}

/** The cart's subtotal, over the line totals the pricing resolve already priced. */
export function cartSubtotalCents(lines: readonly { lineTotalCents: number }[]): number {
  let sum = 0;
  for (const line of lines) {
    if (Number.isFinite(line.lineTotalCents)) sum += line.lineTotalCents;
  }
  return sum;
}

/**
 * One line's contribution to what the ticket saves against retail, as the
 * server priced it: the per-line saving times that line's quantity, rounded
 * to the whole cent. The multiplication is money arithmetic, so the host owns
 * it, exactly as it owns the footer's summed figure (sumLineSavingCents,
 * below). A value the engine did not answer contributes nothing rather than a
 * guess.
 */
export function perLineSavingCents(savingCents: number, qty: number): number {
  if (!Number.isFinite(savingCents) || !Number.isFinite(qty)) return 0;
  return Math.round(savingCents * qty);
}

/**
 * One line's contribution to what the ticket saves against retail, as the
 * server priced it: the per-line saving times the quantity, each line's
 * product rounded to the whole cent as it sums (the reference's per-line
 * discipline). A line the engine could not answer contributes nothing: it
 * does not contribute a guess, and it does not blank the total for the lines
 * that DID answer.
 */
export function sumLineSavingCents(
  entries: readonly { saving_cents: number; qty: number }[],
): number {
  let sum = 0;
  for (const entry of entries) {
    const per = entry.saving_cents;
    if (Number.isFinite(per) && Number.isFinite(entry.qty)) sum += Math.round(per * entry.qty);
  }
  return sum;
}

/**
 * A sale's balance once its deposit is taken, never below zero: the same
 * subtraction the core states on an unpaid-at-ring sale's own answer, here on
 * the confirmation card's display figures.
 */
export function balanceAfterDepositCents(totalCents: number, depositCents: number): number {
  const balance = totalCents - depositCents;
  return balance > 0 ? balance : 0;
}

/** One returnable order line, as the refund estimate reads it. */
export interface ReturnEstimateLine {
  order_line_id: string;
  unit_price_cents: number;
  tax_cents: number;
  qty_sold: number;
}

/**
 * The returns desk's display estimate for the picked quantities, in the
 * reference's own arithmetic (pos-return.ts's estimate, ported exactly): per
 * line, the picked quantity times the unit price, rounded to the whole cent
 * as it sums, and the tax leg the same discipline over the line's tax spread
 * across its sold quantity. unit_price_cents prices ONE unit (the contract's
 * PosReturnableLine carries line_total_cents separately), so the picked
 * quantity multiplies it directly; nothing divides by the quantity sold.
 * Display only: the server derives the binding refund inside its own
 * transaction, and the committed return's own totals are what the receipt
 * renders.
 */
export function refundEstimateCents(
  lines: readonly ReturnEstimateLine[],
  qty: Record<string, number>,
): { subtotal: number; tax: number; total: number } {
  let subtotal = 0;
  let tax = 0;
  for (const line of lines) {
    const picked = qty[line.order_line_id];
    if (typeof picked !== "number" || !Number.isFinite(picked) || picked <= 0) continue;
    subtotal += Math.round(picked * line.unit_price_cents);
    if (line.qty_sold > 0) tax += Math.round((line.tax_cents * picked) / line.qty_sold);
  }
  return { subtotal, tax, total: subtotal + tax };
}

/**
 * The account's available headroom for the strip and the picker to display:
 * the limit and the balance are the customer read's own figures, already
 * server-side facts; the seam clamps the difference at zero. The ring's credit
 * gate is the enforcement; this is the display beside the name.
 */
export function availableCreditCents(customer: {
  credit_limit_cents: number;
  open_balance_cents: number;
}): number {
  const limit = customer.credit_limit_cents;
  const balance = customer.open_balance_cents;
  if (!Number.isFinite(limit) || !Number.isFinite(balance)) return 0;
  const available = limit - balance;
  return available > 0 ? available : 0;
}

// ---------------------------------------------------------------------------
// The detached panel layout's persisted store
// ---------------------------------------------------------------------------
//
// The reference kept the multi-card surface's box positions in the browser's
// local store, per device. This app's committed gate on credential sinks
// refuses the browser's storage accessors outright (test/sinks.test.mjs: no
// source may carry a place a credential could rest in), so the backing store
// here is tab memory: the layout survives every gesture inside the tab and
// forgets on a reload, which is the same posture the app's custody already
// takes. The per-person half of the arrangement (the layout choice, the
// collapse states) rides the server's workspace row through the ported
// surface's own saves; only the pixel boxes degrade to tab memory.

const panelStore = new Map<string, string>();

/** Whatever this tab remembers under this key, as the stored string, or null. */
export function readPanelLayoutRaw(key: string): string | null {
  return panelStore.get(key) ?? null;
}

/** Best effort persist of the stored string under this key. A refusing store just forgets. */
export function writePanelLayoutRaw(key: string, value: string): void {
  panelStore.set(key, value);
}

/** Forget the layout under this key; the next read returns the default tiling. */
export function clearPanelLayoutRaw(key: string): void {
  panelStore.delete(key);
}

/**
 * Sign-out's own clear: the module state a SESSION owns goes with the
 * session. The panel boxes are the last person's arrangement of the counter
 * they just left; the branch facts are what their login reached. Both are
 * re-earned by whoever signs in next (the workspace row and the branches
 * route), so wiping them is the honest hand-over, and a shared till never
 * opens on the last person's boxes or answers a yard name their login never
 * reached.
 */
export function clearSessionState(): void {
  panelStore.clear();
  BRANCHES.length = 0;
}

/**
 * The catalogue's active products, as the POS module's list route answers.
 * The reference fetched this route with a bare browser call that carried no
 * bearer and no organisation header; in HH every ported request rides the
 * host's one authed helper (api, above), so the seam carries the list whole.
 */
export async function listProducts(): Promise<import('./pos-api.js').PosProduct[]> {
  const res = await api('/api/v1/products?active=true');
  if (!res.ok) throw new Error(`products ${res.status}`);
  return (await res.json()) as import('./pos-api.js').PosProduct[];
}

/**
 * Put one product's photo bytes. Not the json door: this route reads the raw
 * image bytes and validates the declared content type against an allowlist,
 * so a JSON content type would make every upload a 415; the session token and
 * the organisation header travel the same way api() sends them, from the
 * host's own header provider.
 */
export async function putProductImage(productId: string, file: File): Promise<Response> {
  if (host === null) return unwired('putProductImage');
  const headers = new Headers(await host.headers());
  headers.set('Content-Type', file.type || 'image/png');
  return (host.fetch ?? fetch)(`${(host.baseUrl ?? '').replace(/\/+$/, '')}/api/v1/products/${encodeURIComponent(productId)}/image?alt=${encodeURIComponent(file.name)}`, {
    method: 'PUT',
    headers,
    body: file,
  });
}

/**
 * The receipt window opener. The reference opened a blank same-origin window
 * here by hand; window surgery around printing is the host's, like the write
 * itself (printReceiptInto).
 */
export function openBlankWindow(): import('./receipt-print-logic.js').ReceiptWindow | null {
  if (typeof window === 'undefined') return null;
  return window.open('', '_blank') as unknown as import('./receipt-print-logic.js').ReceiptWindow | null;
}

// ---------------------------------------------------------------------------
// Printing
// ---------------------------------------------------------------------------
//
// The reference printed a receipt by writing the server-rendered document
// into a fresh blank window with the browser's document writer, then calling
// print. This app's fence refuses raw DOM markup paths outright (the design
// system's raw-markup gate scans every dependent's source), so the host's
// carriage is a navigation instead of a write: the fetched document rides a
// same-origin blob URL into the window, the browser's own document channel
// parses it, and the print dialog is that window's own, called on its load.
// The document is the core's own render either way; nothing here composes
// markup, and the blob carries no credential (the fetch already presented
// the bearer; the URL names bytes, not a route).

/** The receipt window the flow opens: injectable, so the reference kept it testable. */
export type { ReceiptWindow } from './receipt-print-logic.js';

/**
 * Carry a rendered receipt into a freshly opened window and print it. A
 * blocked pop-up is a real and common counter failure (browsers block
 * window.open outside a user gesture); it surfaces as the operator message
 * the reference worded, never a silent no-op.
 */
export function printReceiptInto(
  win: import('./receipt-print-logic.js').ReceiptWindow | null,
  receiptHtml: string,
): import('./receipt-print-logic.js').PrintOutcome {
  if (win === null) {
    return {
      ok: false,
      reason: 'popup_blocked',
      message: 'The browser blocked the receipt window: allow pop-ups for this till and print again.',
    };
  }
  try {
    const blob = new Blob([receiptHtml], { type: 'text/html;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    win.addEventListener('load', () => {
      if (typeof win.focus === 'function') win.focus();
      if (typeof win.print === 'function') win.print();
      // The document is in the window's own session history from here; the
      // URL's bytes are spent. The delay keeps the revoke behind the print
      // dialog's own read on every engine observed.
      window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
    });
    win.location.assign(url);
    return { ok: true };
  } catch {
    return {
      ok: false,
      reason: 'popup_blocked',
      message: 'The receipt window could not be opened: try again, or call the desk lead.',
    };
  }
}
