// The cursor page walker, over the document's cursor envelope.
//
// The contract's every list endpoint answers a page with items, next_cursor
// (a string or null) and the effective limit; the cursor parameter is the
// previous page's next_cursor verbatim. One documented cursor is a position
// token rather than a row key: GET /api/v1/yard/pending-tickets (the yard
// pending-tickets feed) resumes an in-memory merge whose token carries the
// merge position, opaque under the shared v1 encoding, malformed and negative
// positions refused server side (core/api/CONVENTIONS.md, Pagination). This
// helper honors that as data and nothing more: a cursor is passed through
// verbatim, never inspected, decoded, or re-derived, and the walk ends when
// the document says it ends, at next_cursor null.
import { err, ok, type Result } from "./result.js";

/** The shape of the document's cursor envelope, structurally. */
export interface CursorPage<T> {
  readonly items: readonly T[];
  readonly next_cursor: string | null;
}

export interface WalkOptions {
  /**
   * A safety ceiling, since a feed that kept answering the same cursor would
   * otherwise walk forever. Default 1000 pages.
   */
  maxPages?: number;
  /**
   * Cancels the walk: once aborted, the walk resolves an error Result naming
   * the page it stopped at and fetches nothing further. The signal also rides
   * to every fetchPage call, so a page fetched through the transport aborts
   * its request with the same signal.
   */
  signal?: AbortSignal;
}

/**
 * The resume token of a page, opaquely: the caller may store it, log it, and
 * hand it back, and nothing else. It is the document's next_cursor verbatim.
 */
export function resumeCursor<T>(page: CursorPage<T>): string | null {
  return page.next_cursor;
}

/**
 * Walk a cursor-paginated feed to its end, collecting every page's items.
 * fetchPage receives the cursor to resume from (undefined for the first page)
 * and the walk's page options (the signal, when the walk was given one), and
 * returns the page as a Result. The first refused page ends the walk with
 * that same Result, so a refused page surfaces exactly what the operation
 * resolved. Never throws on a refusal.
 */
export async function walkPages<T, Page extends CursorPage<T>>(
  fetchPage: (cursor: string | undefined, pageOptions: { signal?: AbortSignal }) => Promise<Result<Page>>,
  options: WalkOptions = {},
): Promise<Result<T[]>> {
  const maxPages = options.maxPages ?? 1000;
  const pageOptions = { signal: options.signal };
  const items: T[] = [];
  let cursor: string | undefined;
  for (let page = 1; page <= maxPages; page++) {
    if (options.signal?.aborted) return err(`walkPages: aborted before page ${page}`);
    const result = await fetchPage(cursor, pageOptions);
    if (!result.ok) return result;
    items.push(...result.value.items);
    const next = resumeCursor(result.value);
    if (next === null) return ok(items); // the document's end condition
    if (next === cursor) return err("walkPages: the feed repeated the same cursor");
    cursor = next;
  }
  return err(`walkPages: stopped after ${maxPages} pages without reaching the end`);
}
