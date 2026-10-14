// The cached routes the desk apps share (the Quote Desk and the Counter Till
// read the same catalogue, customers and parked tickets through the ported
// surface). Each answers from the device's copy in the shape of the contract's
// route, so the ported surface's own parsers take it unchanged; the read
// through fetch decides when to use it.
//
// What a copy cannot know is left out or null, never invented: a hit carries
// no stock figure, a customer carries no terms and no credit figures (null,
// which the picker words as "Terms unknown"), and a product the price band
// holds no row for carries no list price.
import type { CachedAnswer, CachedRoute } from "./read-through.js";
import type { CatalogEntry, WorkReader } from "./read-cache.js";

type ReaderOf = () => WorkReader | null;

const positive = (value: string | null, fallback: number): number => {
  const n = Number(value ?? "");
  return Number.isInteger(n) && n > 0 ? n : fallback;
};

function counterHit(entry: CatalogEntry): Record<string, unknown> {
  return {
    product_id: entry.id,
    sku: entry.sku,
    name: entry.name,
    ...(entry.description !== "" ? { description: entry.description } : {}),
    ...(entry.collection !== "" ? { collection: entry.collection } : {}),
    base_uom: entry.baseUom,
    sold_by: entry.soldBy,
    has_image: false,
    match_reason: "NAME",
    ...(entry.listPriceCents !== null ? { list_price_cents: entry.listPriceCents } : {}),
  };
}

/** The catalogue reads: the staff search, the counter's instant search and the POS product list. */
export function productRoutes(reader: ReaderOf): CachedRoute[] {
  return [
    {
      match: (url) => url.pathname === "/api/v1/products/search",
      async answer(url): Promise<CachedAnswer | null> {
        const r = reader();
        if (r === null) return null;
        const found = await r.catalogue(url.searchParams.get("q") ?? "", positive(url.searchParams.get("limit"), 10));
        return {
          body: { items: found.hits.map((h) => ({ product_id: h.id, sku: h.sku, name: h.name, category: "", base_uom: h.baseUom })) },
          words: found.words,
        };
      },
    },
    {
      match: (url) => url.pathname === "/api/v1/products/counter-search",
      async answer(url): Promise<CachedAnswer | null> {
        const r = reader();
        if (r === null) return null;
        const q = url.searchParams.get("q") ?? "";
        // The server answers nothing for a term under two characters; so does the copy.
        if (q.trim().length < 2) return { body: { items: [] }, words: (await r.catalogue("zzzzzzzz", 1)).words };
        const found = await r.catalogue(q, positive(url.searchParams.get("limit"), 20));
        return { body: { items: found.hits.map(counterHit) }, words: found.words };
      },
    },
    {
      match: (url) => url.pathname === "/api/v1/products",
      async answer(): Promise<CachedAnswer | null> {
        const r = reader();
        if (r === null) return null;
        const found = await r.catalogue("", Number.MAX_SAFE_INTEGER);
        return {
          body: found.hits.map((h) => ({
            id: h.id,
            sku: h.sku,
            name: h.name,
            color_family: "",
            application: "",
            sold_by: h.soldBy,
            base_uom: h.baseUom,
            is_active: true,
            ...(h.listPriceCents !== null ? { list_price_cents: h.listPriceCents } : {}),
            ...(h.priceUom !== null ? { list_price_uom: h.priceUom } : {}),
          })),
          words: found.words,
        };
      },
    },
  ];
}

/** The customer type-ahead. */
export function customerRoutes(reader: ReaderOf): CachedRoute[] {
  return [
    {
      match: (url) => url.pathname === "/api/v1/customers/search",
      async answer(url): Promise<CachedAnswer | null> {
        const r = reader();
        if (r === null) return null;
        const found = await r.customers(url.searchParams.get("q") ?? "", positive(url.searchParams.get("limit"), 20));
        return {
          body: found.hits.map((c) => ({
            id: c.id,
            account_no: c.accountNo,
            name: c.name,
            customer_type: c.customerType,
            payment_terms_days: null,
            credit_limit_cents: null,
            open_balance_cents: null,
          })),
          words: found.words,
        };
      },
    },
  ];
}

/** The branch's parked tickets, as the POS list route pages them (the ported surface unwraps the page). */
export function parkedRoute(reader: ReaderOf): CachedRoute {
  return {
    match: (url) => url.pathname === "/api/v1/pos/parked",
    async answer(url): Promise<CachedAnswer | null> {
      const r = reader();
      if (r === null) return null;
      const branch = url.searchParams.get("branch_id");
      const found = await r.parkedTickets(branch === null || branch === "" ? undefined : branch);
      const items = found.rows.map((t) => {
        const source = found.sources.get(t.id);
        return {
          id: t.id,
          ticket_no: t.ticketNo,
          branch_id: t.branchId,
          customer_id: t.customerId,
          sale_type: t.saleType,
          notes: t.notes,
          status: t.status,
          lines: Array.isArray(source?.["lines"]) ? source["lines"] : [],
          created_at: typeof source?.["created_at"] === "string" ? source["created_at"] : t.updatedAt,
        };
      });
      return { body: { items, limit: items.length }, words: found.words };
    },
  };
}
