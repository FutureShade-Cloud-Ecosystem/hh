// The register's reach logic (W2-10): the query the register builds from its
// applied posted date range and the core's cursor, the way a page's next
// cursor reads, the window's own words, and the stub world's journal list
// held to the core's pagination, field by field, the way
// test/gl-stub-contract.test.mjs holds the rest of the stub to the core. The
// world is driven through its own handle, no server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createGlWorld, IDS } from "./stub/gl-world.mjs";
import { journalListParams, nextCursorOf, rangeWords } from "../src/gl/gl-logic.ts";

// ---- the query the register builds -----------------------------------------------------------------

test("the register's query is the newest hundred, and an empty bound sends no key at all", () => {
  assert.deepEqual(journalListParams({ dateFrom: "", dateTo: "" }), { limit: 100 });
  assert.deepEqual(journalListParams({ dateFrom: "2025-06-01", dateTo: "2025-06-30" }), {
    limit: 100,
    date_from: "2025-06-01",
    date_to: "2025-06-30",
  });
  assert.deepEqual(journalListParams({ dateFrom: "", dateTo: "2025-06-30" }), { limit: 100, date_to: "2025-06-30" });
  assert.deepEqual(journalListParams({ dateFrom: "2025-06-01", dateTo: "", cursor: "djE6MjAyNS8xMzc" }), {
    limit: 100,
    date_from: "2025-06-01",
    cursor: "djE6MjAyNS8xMzc",
  });
});

test("a page's next cursor reads as the cursor string, and the end reads as empty", () => {
  assert.equal(nextCursorOf({ next_cursor: "djE6MjAyNS8xMzc" }), "djE6MjAyNS8xMzc");
  assert.equal(nextCursorOf({ next_cursor: null }), "");
  assert.equal(nextCursorOf({ next_cursor: undefined }), "");
  assert.equal(nextCursorOf({ next_cursor: "" }), "");
});

test("the window's words say what is loaded in the desk's own date words, and no range says nothing", () => {
  assert.equal(rangeWords("2025-06-01", "2025-06-30"), "Showing journals posted Jun 1, 2025 through Jun 30, 2025.");
  assert.equal(rangeWords("2025-06-01", ""), "Showing journals posted Jun 1, 2025 or later.");
  assert.equal(rangeWords("", "2025-06-30"), "Showing journals posted through Jun 30, 2025.");
  assert.equal(rangeWords("", ""), "");
});

// ---- the stub world's list, held to the core's pagination -------------------------------------------

const PLANTED = 130;

function world() {
  const w = createGlWorld();
  const part = (v) => Buffer.from(JSON.stringify(v), "utf8").toString("base64url");
  const token = `${part({ alg: "ES256" })}.${part({ sub: IDS.controller, exp: 4102444800 })}.c3R1Yg`;
  const call = (method, path, bodyText = "") => w.handle({ method, headers: { authorization: `Bearer ${token}` } }, new URL(`http://stub${path}`), bodyText);
  return { call, plant: () => call("POST", "/api/v1/gl/journals/_stub/planter", JSON.stringify({ count: PLANTED })) };
}

const decode = (cursor) => Buffer.from(cursor, "base64url").toString("utf8");

test("the planted books list a first page of one hundred, and the minted cursor names the pair the list orders by", () => {
  const w = world();
  assert.equal(w.plant().body.journals, PLANTED + 6);
  const page = w.call("GET", "/api/v1/gl/journals?limit=100");
  assert.equal(page.status, 200);
  assert.equal(page.body.items.length, 100);
  assert.equal(page.body.limit, 100);
  // The newest row of the older year that fits the first page is the resume key: fy/en, the core's shape.
  assert.equal(decode(page.body.next_cursor), "v1:2025/137");
  assert.equal(page.body.items[0].entry_no, 6, "the newest first");
  assert.equal(page.body.items[99].entry_no, 137);
});

test("the cursor is followed one page at a time, every row answered once, oldest first at the end, and then the cursor is gone", () => {
  const w = world();
  w.plant();
  const seen = [];
  const keys = [];
  let cursor = "";
  let last = null;
  for (let pages = 0; pages < 10; pages += 1) {
    last = w.call("GET", `/api/v1/gl/journals?limit=100${cursor === "" ? "" : `&cursor=${cursor}`}`);
    assert.equal(last.status, 200);
    seen.push(...last.body.items);
    for (const j of last.body.items) keys.push(`${j.fiscal_year}/${j.entry_no}`);
    if (last.body.next_cursor === null) break;
    cursor = last.body.next_cursor;
  }
  assert.equal(seen.length, PLANTED + 6);
  assert.equal(new Set(seen.map((j) => j.id)).size, seen.length, "the walk repeats no row");
  const ordered = [...keys].sort((a, b) => {
    const [ay, an] = a.split("/").map(Number);
    const [by, bn] = b.split("/").map(Number);
    return by - ay || bn - an;
  });
  assert.deepEqual(keys, ordered, "the list walks fiscal_year DESC, entry_no DESC");
  assert.equal(last.body.next_cursor, null, "the last page carries no way on");
  assert.equal(last.body.items[0].entry_no, 136, "the last page begins where the first left off");
  assert.equal(last.body.items[last.body.items.length - 1].entry_no, 101, "the oldest planted journal ends the walk");
});

test("a posted date range narrows the books to the days it bounds, and a draft (no posted date) is outside every range", () => {
  const w = world();
  w.plant();
  const june = w.call("GET", "/api/v1/gl/journals?date_from=2025-06-01&date_to=2025-06-30&limit=100");
  assert.equal(june.status, 200);
  assert.deepEqual(
    june.body.items.map((j) => j.entry_no),
    [226, 214, 202, 190, 178, 166, 154, 142, 130, 118, 106],
    "June of the older year alone, newest first",
  );
  assert.equal(june.body.next_cursor, null, "one page answers the range");
  const bounded = w.call("GET", "/api/v1/gl/journals?date_from=2025-01-01&date_to=2025-12-31&status=PENDING_APPROVAL");
  assert.equal(bounded.body.items.length, 0, "the seeded drafts carry no posted date");
  const unbounded = w.call("GET", "/api/v1/gl/journals?status=PENDING_APPROVAL");
  assert.deepEqual(unbounded.body.items.map((j) => j.entry_no), [6, 5], "without a range the drafts are there");
});

test("a malformed date, an impossible day and a cursor this world never minted are refused in the core's words", () => {
  const w = world();
  w.plant();
  const refused = (answer, fragment) => {
    assert.equal(answer.status, 400);
    assert.equal(answer.body.error, fragment);
  };
  refused(w.call("GET", "/api/v1/gl/journals?date_from=June"), "invalid date_from (want YYYY-MM-DD)");
  refused(w.call("GET", "/api/v1/gl/journals?date_from=2025-02-30"), "invalid date_from (want YYYY-MM-DD)");
  refused(w.call("GET", "/api/v1/gl/journals?date_to=2025-13-01"), "invalid date_to (want YYYY-MM-DD)");
  refused(w.call("GET", "/api/v1/gl/journals?cursor=plainly-not-ours"), "cursor is malformed");
  refused(w.call("GET", `/api/v1/gl/journals?cursor=${Buffer.from("v1:not-a-pair", "utf8").toString("base64url")}`), "cursor is malformed");
});

test("the limit defaults to fifty, clamps at two hundred, and echoes the page size", () => {
  const w = world();
  w.plant();
  assert.equal(w.call("GET", "/api/v1/gl/journals").body.limit, 50);
  assert.equal(w.call("GET", "/api/v1/gl/journals?limit=0").body.limit, 50);
  assert.equal(w.call("GET", "/api/v1/gl/journals?limit=words").body.limit, 50);
  assert.equal(w.call("GET", "/api/v1/gl/journals?limit=500").body.limit, 200);
  const page = w.call("GET", "/api/v1/gl/journals?limit=10");
  assert.equal(page.body.items.length, 10);
  assert.equal(decode(page.body.next_cursor), "v1:2025/227", "the page's own last row mints the cursor");
});

test("the empty core answers an empty page with no way on, and the demo books come back on a reset", () => {
  const w = world();
  w.plant();
  assert.equal(w.call("POST", "/api/v1/gl/journals/_stub/planter", JSON.stringify({ empty: true })).body.journals, 0);
  const empty = w.call("GET", "/api/v1/gl/journals?limit=100");
  assert.deepEqual(empty.body.items, []);
  assert.equal(empty.body.next_cursor, null);
  w.call("POST", "/_stub/reset");
  assert.equal(w.call("GET", "/api/v1/gl/journals?limit=100").body.items.length, 6);
});
