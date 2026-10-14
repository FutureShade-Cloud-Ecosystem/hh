// The Quote Desk's cached reads. When the network does not answer, the desk
// reads its world from the Work shell's copy on the device: the quotes list,
// the catalogue search and the customer search. Each answer has the exact
// shape the server's route answers, so the desk's own parsers take it
// unchanged; the read through fetch (work-offline) decides when to use it.
//
// What a copy cannot know is said as not known, never as a number: a customer
// found offline carries no terms and no credit figures (null, which the
// picker words as "Terms unknown"), and a draft that is only waiting in the
// outbox is not a row of the list at all (it shows in its own panel, with no
// total). The standing, the lines and every money leg of a quote are read
// online.
import { customerRoutes, productRoutes, type CachedAnswer, type CachedRoute, type WorkReader } from "@hardscapehouse/work-offline";

/** The date the desk shows for the projection's timestamp, or undefined when there is none. */
export function validUntilDate(value: unknown): string | undefined {
  if (typeof value !== "string") return undefined;
  const match = /^(\d{4}-\d{2}-\d{2})(?:$|T)/.exec(value);
  return match === null ? undefined : match[1];
}

const QUOTES_PATH = "/api/v1/quotes";

export function quoteDeskRoutes(reader: () => WorkReader | null, branchId: () => string): CachedRoute[] {
  return [
    {
      match: (url) => url.pathname === QUOTES_PATH,
      async answer(url): Promise<CachedAnswer | null> {
        const r = reader();
        if (r === null) return null;
        const status = url.searchParams.get("status") ?? "";
        const cursor = url.searchParams.get("cursor") ?? "";
        const docs = await r.openDocuments();
        // The copy answers whole; a later page has nothing more.
        const items =
          cursor !== ""
            ? []
            : docs.rows
                .filter((row) => !row.waiting && (status === "" || row.status === status))
                .filter((row) => row.subtotalCents !== null && row.taxCents !== null && row.totalCents !== null)
                .map((row) => ({
                  id: row.id,
                  quote_no: row.quoteNo,
                  customer_id: row.customerId,
                  customer_name: row.customerName,
                  branch_id: branchId(),
                  status: row.status,
                  valid_until: validUntilDate(row.validUntil),
                  expired: false,
                  subtotal_cents: row.subtotalCents,
                  tax_cents: row.taxCents,
                  total_cents: row.totalCents,
                  owner_name: "",
                }));
        return { body: { items, next_cursor: null, limit: items.length }, words: docs.words };
      },
    },
    ...productRoutes(reader),
    ...customerRoutes(reader),
  ];
}
