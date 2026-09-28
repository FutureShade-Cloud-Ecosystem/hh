// The pagination gate: the cursor walker walks a synthetic multi-page cursor
// sequence of the document's envelope shape, terminates on the documented end
// condition (next_cursor null), and surfaces the yard pending-tickets feed's
// position cursor opaquely: the token the server minted is the token the
// walker hands back, byte for byte, never inspected or re-derived (the
// position-cursor convention, core/api/CONVENTIONS.md, Pagination).
import { test } from "node:test";
import assert from "node:assert/strict";
import { resumeCursor, walkPages } from "../src/pagination.ts";

// A synthetic feed shaped like the generated TaskPendingTicketPage: items,
// next_cursor, limit. The tokens are merge-position style opaque strings with
// separators and non-numeric padding, so any attempt to parse or re-derive
// them would corrupt the walk.
function pageOf(items, cursor) {
  return { items, next_cursor: cursor, limit: 2 };
}

const FEED = [
  pageOf(["ticket-1", "ticket-2"], "v1:pos:2:3###a"),
  pageOf(["ticket-3", "ticket-4"], "v1:pos:4:3###b"),
  pageOf(["ticket-5"], null), // the documented end condition
];

test("the walker collects every page's items and stops at next_cursor null", async (t) => {
  const seen = [];
  const result = await walkPages(async (cursor) => {
    seen.push(cursor);
    return { ok: true, value: FEED[seen.length - 1] };
  });
  assert.equal(result.ok, true);
  if (result.ok) assert.deepEqual(result.value, ["ticket-1", "ticket-2", "ticket-3", "ticket-4", "ticket-5"]);
  assert.deepEqual(seen, [undefined, "v1:pos:2:3###a", "v1:pos:4:3###b"], "the first page takes no cursor");
});

test("the position cursor passes through verbatim, whatever its contents", async (t) => {
  const nasty = "v1:pos:-7:0###///not a number###%2F";
  let calls = 0;
  let received = "<the second page never saw a cursor>";
  const result = await walkPages(async (cursor) => {
    calls++;
    if (calls === 1) return { ok: true, value: pageOf([], nasty) };
    received = cursor;
    return { ok: true, value: pageOf(["later"], null) };
  });
  assert.equal(result.ok, true);
  assert.equal(calls, 2);
  assert.equal(received, nasty, "the walker re-sent the token byte for byte");
});

test("resumeCursor surfaces the next cursor opaquely", (t) => {
  assert.equal(resumeCursor(pageOf([], "v1:pos:9:1")), "v1:pos:9:1");
  assert.equal(resumeCursor(pageOf([], null)), null);
});

test("a refused page ends the walk with that exact refusal", async (t) => {
  const result = await walkPages(async (cursor) => {
    if (cursor === undefined) return { ok: true, value: FEED[0] };
    return { ok: false, error: "cursor refused" };
  });
  assert.deepEqual(result, { ok: false, error: "cursor refused" });
});

test("a feed that repeats a cursor is refused, not walked forever", async (t) => {
  const same = "v1:pos:2:3###a";
  const result = await walkPages(async () => ({ ok: true, value: pageOf(["x"], same) }));
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /repeated the same cursor/);
});

test("maxPages stops a feed that never ends", async (t) => {
  let pages = 0;
  const result = await walkPages(
    async (cursor) => {
      pages++;
      return { ok: true, value: pageOf(["x"], cursor === undefined ? "a" : `${cursor}b`) };
    },
    { maxPages: 3 },
  );
  assert.equal(pages, 3);
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /3 pages/);
});

test("an aborted signal cancels the walk before the next page", async (t) => {
  const controller = new AbortController();
  let pages = 0;
  let received = "<the page never ran>";
  const result = await walkPages(
    async (cursor, pageOptions) => {
      pages++;
      received = pageOptions;
      return { ok: true, value: pageOf(["x"], null) };
    },
    { signal: controller.signal, maxPages: 1 },
  );
  assert.equal(pages, 1);
  assert.deepEqual(received, { signal: controller.signal }, "the walk's signal rides to the page fetcher");
  controller.abort();
  const cancelled = await walkPages(
    async (cursor) => ({ ok: true, value: pageOf(["x"], cursor === undefined ? "a" : `${cursor}b`) }),
    { signal: controller.signal },
  );
  assert.equal(cancelled.ok, false);
  if (!cancelled.ok) assert.match(cancelled.error, /aborted before page 1/);
});

test("an abort between pages ends the walk with an error Result, nothing fetched after", async (t) => {
  const controller = new AbortController();
  let pages = 0;
  const result = await walkPages(
    async (cursor) => {
      pages++;
      if (pages === 2) controller.abort();
      return { ok: true, value: pageOf(["x"], cursor === undefined ? "a" : `${cursor}b`) };
    },
    { signal: controller.signal },
  );
  assert.equal(result.ok, false);
  if (!result.ok) assert.match(result.error, /aborted before page 3/);
  assert.equal(pages, 2, "no page is fetched after the abort");
});
