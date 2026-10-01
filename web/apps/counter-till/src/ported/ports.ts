// ports.ts: what the ported POS surface needs from its host, and nothing else.
//
// The port (see CLOSURE.md for the file-by-file map) stops at the host's edge.
// Everything here was, in the reference, the app's own api client, session,
// router, navigation registry, or a neighbouring screen. In HH the host owns
// those; the next work item implements these seams against the HH api-client,
// the auth package and the shell. The ported code compiles against these
// declarations only, and is not mounted yet, so nothing here runs until the
// wiring item lands.
//
// Two house rules are baked into the shape of this file:
//
//   1. The core computes money. Where the reference computed a money figure in
//      the browser (a subtotal sum, a tax line, a change-due, a refund
//      estimate, a credit headroom, a balance after deposit, a typed dollar
//      amount parsed through a float), the computation is left out of the
//      ported files entirely and reappears here as a host call. Each such seam
//      carries the reference's signature so the ported call sites stay
//      shaped as they were.
//   2. Every seam fails loudly until wired: calling one throws the port
//      message rather than returning a plausible answer. An honest refusal
//      beats a quiet zero at a counter.

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
 * The authed request helper the whole ported surface talks through: relative
 * path in, bearer token attached, Response out. The host implements it over
 * the HH api-client and its header provider.
 */
export function api(path: string, init?: RequestInit): Promise<Response> {
  return unwired('api');
}

/** The branches the dealer runs. Empty until wired: an honest blank, never a guessed yard. */
export const BRANCHES: Branch[] = [];

/** The branch's name, or a short form of its id when the list does not know it. */
export function branchName(id: string): string {
  return unwired('branchName');
}

/** The signed-in user, or null between sessions. */
export function getCurrentUser(): AuthUser | null {
  return unwired('getCurrentUser');
}

/** Whether the signed-in user clears the platform-admin gate. */
export function isAdmin(): boolean {
  return unwired('isAdmin');
}

/**
 * The raw bearer token, only ever to hand a session across a window boundary
 * (the reference's pop-out handshake). Never placed in a URL.
 */
export function getSessionToken(): string {
  return unwired('getSessionToken');
}

// ---------------------------------------------------------------------------
// Navigation (the reference's hash-route.ts, dashboard-nav.ts and nav-access.ts)
// ---------------------------------------------------------------------------

/**
 * The hash a navigation target produces: `#home`, `#apps`, `#unit` or
 * `#unit/module`. The host shell owns the real route table; the reference's
 * shapes are kept so the ported quick-action registry reads as it did.
 */
export function hashTargetFor(active: string, module?: string): string {
  return unwired('hashTargetFor');
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

export function isNavUnitKey(v: string): v is NavUnitKey {
  return unwired('isNavUnitKey');
}

export function isNavModuleKey(v: string): v is NavModuleKey {
  return unwired('isNavModuleKey');
}

/** Who may see an entry point: the reference's gate vocabulary. */
export type Gate = 'always' | 'authed' | 'manager' | 'admin';

/** The caller's grants as the three predicates the shell computes. */
export interface AccessRole {
  authed: boolean;
  manager: boolean;
  admin: boolean;
}

/**
 * The per-screen gate table. Declared empty and cast: the host's RBAC (the
 * per-dealer role defaults HH decided) supplies the real table, and every
 * read goes through canSeeGate, which refuses until wired.
 */
export const SCREEN_GATE: Record<NavModuleKey, Gate> = {} as Record<NavModuleKey, Gate>;

/** Does this role clear this gate? The host answers from its own role model. */
export function canSeeGate(gate: Gate, role: AccessRole): boolean {
  return unwired('canSeeGate');
}

/** The modules of one unit this role may see, in rail order. */
export function visibleModuleKeys(unit: NavUnitKey, role: AccessRole): NavModuleKey[] {
  return unwired('visibleModuleKeys');
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
 * server priced it: the per-line saving times the quantity, summed by the
 * host because the multiplication is money arithmetic.
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
