// The till's client: the app's one door to the counter routes, one method per
// route the sale screen speaks, every answer validated field by field before
// the app renders a word of it. Nothing here computes money, holds a price,
// or decides who may do what: the core prices every line, holds every gate,
// and answers the figures; this module reads those figures off the wire and
// refuses everything it cannot prove (a missing field is a refusal, never a
// default), because the one thing a till must never show is a figure nobody
// computed.
import { err, ok, type ApiClient, type Result } from "@hardscapehouse/api-client";
import { staffBranches, staffProduct, staffTill } from "@hardscapehouse/api-client";

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
}
