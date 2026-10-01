// The till's client: the app's one door to the counter routes, one method per
// route the sale screen speaks, every answer validated field by field before
// the app renders a word of it. Nothing here computes money, holds a price,
// or decides who may do what: the core prices every line, holds every gate,
// and answers the figures; this module reads those figures off the wire and
// refuses everything it cannot prove (a missing field is a refusal, never a
// default), because the one thing a till must never show is a figure nobody
// computed.
import { err, ok, type ApiClient, type Result } from "@hardscapehouse/api-client";
import { staffBranches, staffCustomer, staffPos, staffProduct, staffTill } from "@hardscapehouse/api-client";

/** The station the counter's shared drawer sits on in this release. */
export const DRAWER_STATION = "MAIN";

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

/** One ticket line as the resolve and the glance take it. */
export interface TicketItem {
  productId: string;
  qty: number;
  uom: string;
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

export class TillClient {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /**
   * The branch the login works: the branches route is fenced to the caller's
   * own grants server-side, so the demo till takes the one row that comes
   * back. An empty list or a row this app cannot read is a refusal in words,
   * never an invented branch.
   */
  async resolveBranch(): Promise<Result<BranchRef>> {
    const result = await staffBranches.branchesList(this.#client);
    if (!result.ok) return result;
    const rows = result.value;
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
   * rather than rendered half-known.
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
      const soldBy = stringAt(item, "sold_by");
      if (productId === null || sku === null || name === null || soldBy === null) continue;
      products.push({ productId, sku, name, soldBy });
    }
    return ok(products);
  }

  /**
   * The account picker's read: the walk-in never comes back (the route
   * excludes it server-side), so every row here is an account the till can
   * bill. A row this app cannot read is dropped, not rendered half-known.
   */
  async searchCustomers(query: string): Promise<Result<CustomerRef[]>> {
    const result = await staffCustomer.customerSearch(this.#client, { q: query });
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
    // slot reads unknown and the column renders its placeholder, value
    // driven, whatever the configuration decides.
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
}
