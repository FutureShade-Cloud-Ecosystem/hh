// The till's client: the app's one door to the counter routes, one method per
// route the sale screen speaks, every answer validated field by field before
// the app renders a word of it. Nothing here computes money, holds a price,
// or decides who may do what: the core prices every line, holds every gate,
// and answers the figures; this module reads those figures off the wire and
// refuses everything it cannot prove (a missing field is a refusal, never a
// default), because the one thing a till must never show is a figure nobody
// computed.
import { err, ok, type ApiClient, type Result } from "@hardscapehouse/api-client";
import {
  staffBranches,
  staffCustomer,
  staffPayment,
  staffPos,
  staffProduct,
  staffTill,
} from "@hardscapehouse/api-client";

/** The station the counter's shared drawer sits on in this release. */
export const DRAWER_STATION = "MAIN";

/**
 * The house day (in the dealer's served zone), its conversion and the demo card
 * token live in @hardscapehouse/pos-kit (src/ported/host-facts.ts), which
 * carried them out of this file when the shared pieces moved into that
 * package: the kit's seam file reads them and must not import the app. This
 * module re-exports the same three names, so this file's own callers are
 * unchanged, and the wording of their comments moved with them.
 */
export { DEFAULT_HOUSE_ZONE, houseDay, houseDayInZone, houseZone, serveHouseZoneFrom, DEMO_CARD_TOKEN } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
// This module's own ring path still swaps the card token onto the demo
// constant, so the value rides in beside the re-export above.
import { DEMO_CARD_TOKEN } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { decideSessionBranch, type BranchKeyStore } from "@hardscapehouse/pos-kit/src/session-branches.js";

/**
 * The float a first use opens the drawer with: an invented, constant amount
 * for the demo box (the real till counts denominations; this release does not
 * carry a count screen). Integer cents, and the core's till session owns it
 * the moment it is opened.
 */
export const INVENTED_FLOAT_CENTS = 20000;

export interface BranchRef {
  id: string;
  code: string;
  name: string;
}

/** What a desk does with the branches the login reaches: work the one, or ask the person once which of several. */
export type BranchResolution = { kind: "one"; branch: BranchRef } | { kind: "ask"; choices: BranchRef[] };

export interface DrawerRef {
  id: string;
  station: string;
  floatCents: number;
}

export interface ProductRef {
  productId: string;
  sku: string;
  name: string;
  soldBy: string;
}

export interface CustomerRef {
  id: string;
  accountNo: string;
  name: string;
  customerType: string;
  creditLimitCents: number;
  openBalanceCents: number;
}

export interface StandingRef {
  /** Past due, or null when the answer left it out: rendered as unknown, never zero. */
  pastDueCents: number | null;
  oldestBucket: string | null;
  /** The hold flag, or null when the answer left it out. */
  onHold: boolean | null;
}

/** One line on the ticket as the resolve and the glance take it. */
export interface TicketItem {
  productId: string;
  qty: number;
  uom: string;
}

/** What the till sends to ring one sale: no price anywhere in it. */
export interface SaleRing {
  idempotencyKey: string;
  branchId: string;
  tillSessionId: string;
  customerId: string;
  tender: "CASH" | "CARD" | "ACCOUNT";
  cashTenderedCents: number | null;
  requestedDate: string;
  items: TicketItem[];
}

export interface CommittedSale {
  orderId: string;
  orderNo: string;
  invoiceNo: string;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  tender: string;
  cashTenderedCents: number | null;
  changeDueCents: number | null;
  tenders: Array<{ method: string; amountCents: number }>;
}

/** A parked ticket as the till holds it: quantities and units, no prices. */
export interface ParkedTicket {
  id: string;
  ticketNo: string;
  customerId: string;
  lines: TicketItem[];
}

export interface QuoteRef {
  productId: string;
  priceUom: string;
  /** The customer's rule and tier unit price, or null when the line came back unpriceable. */
  unitCents: number | null;
  basis: string | null;
}

export interface PriceAnswer {
  customerType: string;
  quotes: QuoteRef[];
}

export interface MarginAnswer {
  /** One slot per input line; null where the glance answered nothing for it. */
  marginBps: Array<number | null>;
  partial: boolean;
}

// -- field readers: a response field either proves itself or the answer is
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

/**
 * Parse a typed quantity into the number the wire carries: digits with at
 * most one decimal point, greater than zero. Quantities are fixed-precision
 * decimals with their unit of measure, owned by the core; this only refuses
 * what is not a quantity at all, so the till never sends a guess.
 */
export function parseQuantity(text: string): number | null {
  const trimmed = text.trim();
  if (!/^[0-9]+(\.[0-9]+)?$/.test(trimmed)) return null;
  const value = Number(trimmed);
  return value > 0 ? value : null;
}

/**
 * Field-by-field validation for a parked ticket, never a cast. Strict by the
 * file's own rule: a missing field is a refusal, so an unreadable line
 * refuses the whole ticket rather than coming back as a short ticket whose
 * line count lies.
 */
function validateParkedTicket(value: unknown): ParkedTicket | null {
  const record = value as unknown;
  if (!isRecord(record)) return null;
  const id = stringAt(record, "id");
  const ticketNo = stringAt(record, "ticket_no");
  const customerId = record["customer_id"];
  const rawLines = record["lines"];
  if (id === null || ticketNo === null || !Array.isArray(rawLines)) return null;
  const lines: TicketItem[] = [];
  for (const line of rawLines) {
    const row = line as unknown;
    if (!isRecord(row)) return null;
    const productId = stringAt(row, "product_id");
    const uom = stringAt(row, "uom_entered");
    const qty = row["qty_entered"];
    if (productId === null || uom === null || typeof qty !== "number" || !(qty > 0)) return null;
    lines.push({ productId, qty, uom });
  }
  if (lines.length === 0) return null;
  return {
    id,
    ticketNo,
    customerId: typeof customerId === "string" ? customerId : "",
    lines,
  };
}

export class TillClient {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /**
   * The branch the login works: the branches route is fenced to the caller's own grants server-side. One granted
   * branch is taken as it is. With several granted, the front door's session branch key is read through the granted
   * list (a forged or stale id is dropped): exactly one chosen and granted lands on it; several chosen, or none, is
   * an "ask" for the desk's own chooser, never an invented branch. No granted branch at all, or a row this app
   * cannot read, is a refusal in words. The store is a test seam; it defaults to this tab's own key store.
   */
  async resolveBranchChoice(store?: BranchKeyStore): Promise<Result<BranchResolution>> {
    const result = await staffBranches.branchesList(this.#client);
    if (!result.ok) return result;
    const rows = result.value as unknown;
    if (!Array.isArray(rows) || rows.length === 0) {
      return err("the branch list did not answer exactly the one branch this login reaches");
    }
    const granted: BranchRef[] = [];
    for (const row of rows as unknown[]) {
      if (!isRecord(row)) return err("the branch row is not readable");
      const id = stringAt(row, "id");
      const code = stringAt(row, "code");
      const name = stringAt(row, "name");
      if (id === null || code === null || name === null) {
        return err("the branch row is missing its id, code or name");
      }
      granted.push({ id, code, name });
    }
    const decided = decideSessionBranch(granted, store);
    if (decided.kind === "ask") return ok({ kind: "ask", choices: granted });
    const branch = granted.find((row) => row.id === (decided.kind === "one" ? decided.id : ""));
    if (branch === undefined) return err("the branch list did not answer exactly the one branch this login reaches");
    return ok({ kind: "one", branch });
  }

  /** The one branch this tab works, for the panels that read it after the start: refused while the desk still has to ask. */
  async resolveBranch(store?: BranchKeyStore): Promise<Result<BranchRef>> {
    const resolved = await this.resolveBranchChoice(store);
    if (!resolved.ok) return resolved;
    if (resolved.value.kind === "ask") return err("choose which branch you are working first");
    return ok(resolved.value.branch);
  }

  /**
   * Find the branch's open drawer on the counter's station, or open one: a
   * first use opens the MAIN drawer with the invented float above. The
   * drawer's detail is read either way, so the strip shows the float the
   * drawer actually holds.
   */
  async ensureDrawer(branchId: string): Promise<Result<{ how: "found" | "opened"; drawer: DrawerRef }>> {
    const lookup = await staffTill.tillLookupOpenSession(this.#client, { branch_id: branchId, station: DRAWER_STATION });
    if (!lookup.ok) return lookup;
    const open = lookup.value.open;
    let sessionId: string | null = null;
    if (open) {
      sessionId = stringAt(lookup.value as unknown as Record<string, unknown>, "id");
      if (sessionId === null) return err("the open drawer's lookup carried no id");
    } else {
      const opened = await staffTill.tillOpenSession(this.#client, {
        branch_id: branchId,
        station: DRAWER_STATION,
        float_cents: INVENTED_FLOAT_CENTS,
        opening_denominations: [],
      });
      if (!opened.ok) return opened;
      sessionId = stringAt(opened.value as unknown as Record<string, unknown>, "id");
      if (sessionId === null) return err("the opened drawer carried no id");
    }
    const detail = await staffTill.tillGetSession(this.#client, sessionId);
    if (!detail.ok) return detail;
    const row = detail.value as unknown as Record<string, unknown>;
    const id = stringAt(row, "id");
    const station = stringAt(row, "station");
    const floatCents = integerAt(row, "float_cents");
    if (id === null || station === null || floatCents === null) {
      return err("the drawer's detail is missing its id, station or float");
    }
    return ok({ how: open ? "found" : "opened", drawer: { id, station, floatCents } });
  }

  /**
   * The product read route: the counter's search over the catalogue. A blank
   * or refused search is no rows, and a row this app cannot read is dropped
   * rather than rendered half-known. The unit a line is sold by is the hit's
   * own sold_by word when the server names one, and the required base_uom
   * otherwise: the contract makes sold_by optional and base_uom required, so
   * the base unit is the served answer, never a guess.
   */
  async searchProducts(query: string): Promise<Result<ProductRef[]>> {
    const result = await staffProduct.productCounterSearch(this.#client, { q: query });
    if (!result.ok) return result;
    const items = (result.value as unknown as Record<string, unknown> | undefined)?.items;
    if (!Array.isArray(items)) return err("the product search did not answer a list");
    const products: ProductRef[] = [];
    for (const item of items) {
      if (!isRecord(item)) continue;
      const productId = stringAt(item, "product_id");
      const sku = stringAt(item, "sku");
      const name = stringAt(item, "name");
      const soldBy = stringAt(item, "sold_by") ?? stringAt(item, "base_uom");
      if (productId === null || sku === null || name === null || soldBy === null) continue;
      products.push({ productId, sku, name, soldBy });
    }
    return ok(products);
  }

  /**
   * The account picker's read, named for one branch: the search is fenced to
   * the caller's own grants server-side, and a scoped caller must say which
   * branch it is acting for, so the counter always names the branch the till
   * resolved. The walk-in never comes back (the route excludes it
   * server-side), so every row here is an account the till can bill. A row
   * this app cannot read is dropped, not rendered half-known.
   */
  async searchCustomers(query: string, branchId: string): Promise<Result<CustomerRef[]>> {
    const result = await staffCustomer.customerSearch(this.#client, { q: query, branch_id: branchId });
    if (!result.ok) return result;
    const rows = result.value;
    if (!Array.isArray(rows)) return err("the customer search did not answer a list");
    const customers: CustomerRef[] = [];
    for (const row of rows) {
      const record = row as unknown;
      if (!isRecord(record)) continue;
      const id = stringAt(record, "id");
      const accountNo = stringAt(record, "account_no");
      const name = stringAt(record, "name");
      const customerType = stringAt(record, "customer_type");
      const creditLimitCents = integerAt(record, "credit_limit_cents");
      const openBalanceCents = integerAt(record, "open_balance_cents");
      if (
        id === null ||
        accountNo === null ||
        name === null ||
        customerType === null ||
        creditLimitCents === null ||
        openBalanceCents === null
      ) {
        continue;
      }
      customers.push({ id, accountNo, name, customerType, creditLimitCents, openBalanceCents });
    }
    return ok(customers);
  }

  /**
   * The standing read for a picked account: the balance facts the counter
   * shows beside the name. Every field is optional in the answer, so every
   * field stays optional here: what the read leaves out the screen renders as
   * unknown, never as zero.
   */
  async accountStanding(branchId: string, customerId: string): Promise<Result<StandingRef>> {
    const result = await staffPos.posAccountStanding(this.#client, { branch_id: branchId, customer_id: customerId });
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the standing read did not answer an object");
    const pastDueRaw = record["past_due_cents"];
    return ok({
      pastDueCents: typeof pastDueRaw === "number" && Number.isInteger(pastDueRaw) ? pastDueRaw : null,
      oldestBucket: stringAt(record, "oldest_bucket"),
      onHold: booleanAt(record, "on_hold"),
    });
  }

  /**
   * The counter price picture: every ticket line priced by the engine in one
   * read, with no price sent and no price field anywhere in this app. The
   * answer's customer type is the server's own resolution of whose prices
   * these are; the quotes ride index-aligned with the input, and an answer of
   * any other shape is refused whole rather than realigned by guesswork. An
   * unpriceable line comes back flagged, and its unit price stays unknown
   * rather than zero.
   */
  async priceTicket(branchId: string, customerId: string, items: TicketItem[]): Promise<Result<PriceAnswer>> {
    const result = await staffPos.posCounterPrices(this.#client, {
      branch_id: branchId,
      customer_id: customerId,
      items: items.map((item) => ({ product_id: item.productId, qty_entered: item.qty, uom_entered: item.uom })),
    });
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the price read did not answer an object");
    const customerType = stringAt(record, "customer_type");
    const prices = record["prices"];
    if (customerType === null || !Array.isArray(prices)) {
      return err("the price read is missing its customer type or its quotes");
    }
    if (prices.length !== items.length) {
      return err("the price read answered a different number of quotes than the ticket asked about");
    }
    const quotes: QuoteRef[] = [];
    for (const price of prices) {
      const row = price as unknown;
      if (!isRecord(row)) return err("a price quote is not readable");
      const productId = stringAt(row, "product_id");
      const priceUom = stringAt(row, "price_uom");
      const unpriceable = row["unpriceable"] === true;
      const unitCents = integerAt(row, "customer_cents");
      if (productId === null || priceUom === null) return err("a price quote is missing its product or its unit");
      quotes.push({
        productId,
        priceUom,
        unitCents: unpriceable || unitCents === null ? null : unitCents,
        basis: stringAt(row, "basis"),
      });
    }
    return ok({ customerType, quotes });
  }

  /**
   * The margin glance: one ask per ticket, one answer slot per line. The
   * glance owns the gate: when it answers nothing at all (the empty object
   * the server sends a caller it does not answer), every slot is null and the
   * column renders the explicit placeholder, value driven, whatever the
   * configuration decides. A line slot left out (no cost at this branch)
   * reads null exactly the same way.
   */
  async glanceMargins(
    branchId: string,
    customerId: string,
    customerType: string,
    items: TicketItem[],
  ): Promise<Result<MarginAnswer>> {
    const result = await staffPos.posMarginGlance(this.#client, {
      branch_id: branchId,
      customer_id: customerId,
      customer_type: customerType,
      lines: items.map((item) => ({ product_id: item.productId, qty_entered: item.qty, uom_entered: item.uom })),
    });
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the margin glance did not answer an object");
    const lines = record["lines"];
    // The glance's own refusal is the empty object: margin byte-absent for a
    // caller it does not answer. That is a real answer, not an error: every
    // slot reads unknown, value driven, whatever the configuration decides.
    // The surface holds its own gate besides (P3-10): a caller the policy
    // word refuses sees no margin column at all, placeholder included.
    if (lines === undefined) {
      return ok({ marginBps: items.map(() => null), partial: false });
    }
    if (!Array.isArray(lines)) return err("the margin glance answered no per-line slots");
    if (lines.length !== items.length) {
      return err("the margin glance answered a different number of slots than the ticket asked about");
    }
    const marginBps = lines.map((line) => {
      const row = line as unknown;
      if (!isRecord(row)) return null;
      const bps = row["margin_bps"];
      return typeof bps === "number" && Number.isInteger(bps) ? bps : null;
    });
    const partialRaw = record["partial"];
    return ok({ marginBps, partial: partialRaw === true });
  }

  /**
   * The card rail's own report of what it is. True is the demo's simulator
   * and turns the simulated notices on; false means the rail reports itself
   * live; a refusal means unknown, and the notices stay off either way
   * because a notice must render exactly when the value is true.
   */
  async paymentStatus(): Promise<Result<boolean | null>> {
    const result = await staffPayment.paymentGetConfig(this.#client);
    if (!result.ok) return result;
    const record = result.value as unknown;
    if (!isRecord(record)) return err("the payment status did not answer an object");
    return ok(booleanAt(record, "simulated"));
  }

  /**
   * Ring the sale: one atomic call, the core composing order, invoice and
   * tender. The body carries no price: the lines are quantities and units,
   * and the engine prices them server-side. A refusal comes back as data,
   * with the server's status and word, so the till can speak the refusal in
   * words (a credit hold above all) without this module pretending to know
   * the gate's mind.
   */
  async commitSale(ring: SaleRing): Promise<Result<CommittedSale>> {
    const result = await staffPos.posCreateSale(this.#client, {
      idempotency_key: ring.idempotencyKey,
      branch_id: ring.branchId,
      till_session_id: ring.tillSessionId,
      customer_id: ring.customerId,
      sale_type: "COLLECT_NOW",
      cash_sale: ring.tender === "CASH",
      tender: ring.tender,
      payment_token: ring.tender === "CARD" ? DEMO_CARD_TOKEN : "",
      signature_ref: "",
      requested_date: ring.requestedDate,
      notes: "",
      offline: false,
      lines: ring.items.map((item) => ({ product_id: item.productId, qty_entered: item.qty, uom_entered: item.uom })),
      cash_tendered_cents: ring.cashTenderedCents === null ? 0 : ring.cashTenderedCents,
    });
    if (!result.ok) return result;
    const sale = result.value as unknown;
    if (!isRecord(sale)) return err("the committed sale did not answer an object");
    const orderId = stringAt(sale, "order_id");
    const orderNo = stringAt(sale, "order_no");
    const invoiceNo = stringAt(sale, "invoice_no");
    const subtotalCents = integerAt(sale, "subtotal_cents");
    const taxCents = integerAt(sale, "tax_cents");
    const totalCents = integerAt(sale, "total_cents");
    const tender = stringAt(sale, "tender");
    if (
      orderId === null ||
      orderNo === null ||
      invoiceNo === null ||
      subtotalCents === null ||
      taxCents === null ||
      totalCents === null ||
      tender === null
    ) {
      return err("the committed sale is missing its numbers");
    }
    const cashTenderedRaw = sale["cash_tendered_cents"];
    const changeDueRaw = sale["change_due_cents"];
    const legsRaw = sale["tenders"];
    const tenders: Array<{ method: string; amountCents: number }> = [];
    if (Array.isArray(legsRaw)) {
      for (const leg of legsRaw) {
        const row = leg as unknown;
        if (!isRecord(row)) return err("a tender leg on the committed sale is not readable");
        const method = stringAt(row, "method");
        const amountCents = integerAt(row, "amount_cents");
        if (method === null || amountCents === null) {
          return err("a tender leg on the committed sale is missing its method or its amount");
        }
        tenders.push({ method, amountCents });
      }
    }
    return ok({
      orderId,
      orderNo,
      invoiceNo,
      subtotalCents,
      taxCents,
      totalCents,
      tender,
      cashTenderedCents: typeof cashTenderedRaw === "number" && Number.isInteger(cashTenderedRaw) ? cashTenderedRaw : null,
      changeDueCents: typeof changeDueRaw === "number" && Number.isInteger(changeDueRaw) ? changeDueRaw : null,
      tenders,
    });
  }

  /**
   * Park the current ticket: quantities and units only, no prices, per the
   * contract's own rule that prices re-derive from the engine on resume.
   */
  async parkTicket(
    branchId: string,
    customerId: string,
    items: TicketItem[],
  ): Promise<Result<ParkedTicket>> {
    const result = await staffPos.posParkTicket(this.#client, {
      branch_id: branchId,
      customer_id: customerId,
      sale_type: "COLLECT_NOW",
      notes: "",
      lines: items.map((item) => ({ product_id: item.productId, qty_entered: item.qty, uom_entered: item.uom })),
    });
    if (!result.ok) return result;
    const validated = validateParkedTicket(result.value);
    return validated === null ? err("the parked ticket the server answered is not readable") : ok(validated);
  }

  /**
   * This branch's parked tickets: the fence is the server's, this read names
   * the branch it acts for. The answer arrives in the contract's page shape
   * and, on the served core today, as the bare array the page shape wraps;
   * both carry the same rows, so both are read here and anything else is a
   * refusal, never a half list.
   */
  async listParked(branchId: string): Promise<Result<ParkedTicket[]>> {
    const result = await staffPos.posListParked(this.#client, { branch_id: branchId });
    if (!result.ok) return result;
    const value = result.value as unknown;
    let rows: unknown[] | null = null;
    if (isRecord(value) && Array.isArray(value["items"])) {
      rows = value["items"];
    } else if (Array.isArray(value)) {
      rows = value;
    }
    if (rows === null) {
      return err("the parked list did not answer a page or a list");
    }
    const tickets: ParkedTicket[] = [];
    for (const item of rows) {
      const validated = validateParkedTicket(item);
      if (validated === null) {
        return err("a parked ticket row is not readable: the list is refused whole rather than shown short");
      }
      tickets.push(validated);
    }
    return ok(tickets);
  }

  /** Put a parked ticket back on the counter. Its lines carry no prices: the till re-prices at once. */
  async resumeTicket(id: string): Promise<Result<ParkedTicket>> {
    const result = await staffPos.posResumeParked(this.#client, id);
    if (!result.ok) return result;
    const validated = validateParkedTicket(result.value);
    return validated === null ? err("the resumed ticket is not readable") : ok(validated);
  }
}
