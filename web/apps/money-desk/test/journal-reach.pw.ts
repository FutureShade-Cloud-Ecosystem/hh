// The register's reach legs (W2-10): on a planted world of 136 journals, more
// than the register's first hundred, Show more follows the core's next cursor
// and appends the older page, the posted date range reaches a journal the first
// hundred cannot show, changing the range starts from the first page again, and
// the status filters keep working on the loaded window. W2-10b adds the leg that
// reads the posted date back off a row: a posted journal in the desk's own day
// words, and a draft's plain Not posted.
//
// The blank check (principal item 386): each leg must fail on an empty screen
// and on an empty core, each on its own, so a leg that passes against nothing
// proves nothing. JOURNAL_REACH_BLANK=core clears the stub's books to no
// journals at all; JOURNAL_REACH_BLANK=screen answers every register read with
// an empty page so the screen draws its empty state over a full stub:
//
//   JOURNAL_REACH_BLANK=core npx playwright test journal-reach
//   JOURNAL_REACH_BLANK=screen npx playwright test journal-reach
//
// Both runs go red; the unmarked run goes green with the fix.
import type { APIRequestContext, Page } from "@playwright/test";
import { test, expect, controllerHandoff, resetStub, openTab } from "./gl-helpers.js";

const PLANTED = 130;
const FIRST_PLANTED_ENTRY = 101;
const PAGE = 100;
const blank = process.env.JOURNAL_REACH_BLANK ?? "";

/**
 * THE BLANK SENTINEL: the edge openRegister answers when the books hold nothing
 * at all. It has to be a label no row can carry. JE-0000 would not do: the
 * stub's planted lodgement journal really carries that label. Where the teeth
 * are: in the unmarked run, the leg's own "no button named the sentinel" check
 * holds against a register that really holds the JE-0000 row; a blank leg does
 * not get that far, it fails earlier, in openRegister, when no register table
 * is drawn at all.
 */
const BLANK_SENTINEL = "JE-none";

/** Plants the older books (or empties them, for the blank core), signs in, and stands on the register.
 *  Answers the oldest label the first page holds, derived from the world: the register lists the demo
 *  year's rows (the seeded journals and any another desk plants, such as a lodgement's own) ahead of the
 *  older year's, so the first hundred reach down to the planted entry that many rows short of the newest. */
async function openRegister(page: Page, request: APIRequestContext): Promise<string> {
  let edge = BLANK_SENTINEL;
  await resetStub(request);
  if (blank === "core") {
    const emptied = await request.post("/api/v1/gl/journals/_stub/planter", {
      data: { empty: true },
      headers: { Authorization: "Bearer the-planter-is-a-stub-control" },
    });
    if (!emptied.ok()) throw new Error("the stub refused the empty books");
  } else {
    const planted = await request.post("/api/v1/gl/journals/_stub/planter", {
      data: { count: PLANTED },
      headers: { Authorization: "Bearer the-planter-is-a-stub-control" },
    });
    if (!planted.ok()) throw new Error("the stub refused the older journals");
    const total = ((await planted.json()) as { journals: number }).journals;
    const newestPlanted = FIRST_PLANTED_ENTRY + PLANTED - 1;
    edge = `JE-${String(newestPlanted - (PAGE - (total - PLANTED)) + 1).padStart(4, "0")}`;
  }
  await page.addInitScript(controllerHandoff);
  if (blank === "screen") {
    await page.route("**/api/v1/gl/journals?**", (route) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: [], next_cursor: null, limit: 100 }) }),
    );
  }
  await page.goto("/");
  await expect(page.locator("[data-approval-id]")).toHaveCount(5);
  await openTab(page, "journals");
  await expect(page.getByRole("table", { name: "Journal register" })).toBeVisible();
  return edge;
}
/** The entry just before a label; a label with no number (the blank sentinel) has no neighbour and answers itself. */
const before = (label: string) => {
  const n = Number(label.slice(3));
  return Number.isInteger(n) ? `JE-${String(n - 1).padStart(4, "0")}` : label;
};

test("show more reaches a journal older than the first hundred and opens it", async ({ page, request }) => {
  const edge = await openRegister(page, request);
  const register = page.getByRole("table", { name: "Journal register" });
  // The first page is the newest hundred: it stops at the edge the world gives (JE-0138 with the lodgement's
  // own journal beside the six seeded ones), and the entry just before it waits on the next page.
  await expect(register.getByRole("button", { name: edge })).toBeVisible();
  await expect(register.getByRole("button", { name: before(edge) })).toHaveCount(0);
  await expect(register.getByRole("button", { name: "JE-0101" })).toHaveCount(0);
  // The blank sentinel is a label no row can carry. The register holds the world's own rows here, the planted
  // lodgement's own JE-0000 among them, so this is where a sentinel that a row really carries would show.
  await expect(register.getByRole("button", { name: BLANK_SENTINEL })).toHaveCount(0);
  const more = page.locator("[data-show-more]");
  await expect(more).toBeVisible();
  await expect(more).toContainText("Show more journals");
  await more.click();
  // The next page appends the older year's rows, down to JE-0101, and the way in is gone at the end.
  await expect(register.getByRole("button", { name: "JE-0101" })).toBeVisible();
  await expect(more).toHaveCount(0);
  await register.getByRole("button", { name: "JE-0101" }).click();
  const panel = page.locator("[data-journal-panel]");
  await expect(panel).toContainText("JE-0101");
  await expect(panel).toContainText("January 2025 pour");
});

test("a posted date range reaches an older journal, and changing it starts from the first page again", async ({ page, request }) => {
  const edge = await openRegister(page, request);
  const register = page.getByRole("table", { name: "Journal register" });
  await expect(register.getByRole("button", { name: edge })).toBeVisible();
  const from = page.locator('[data-field="journal-from"]');
  const to = page.locator('[data-field="journal-to"]');
  await from.fill("2025-06-01");
  await to.fill("2025-06-30");
  await page.getByRole("button", { name: "Apply dates" }).click();
  // June of the older year alone: eleven planted rows, no draft of the demo year (a draft carries no
  // posted date), and one page answers the range, so the way in to a further page is gone.
  await expect(register.locator("tbody tr")).toHaveCount(11);
  await expect(register.getByRole("button", { name: "JE-0106" })).toBeVisible();
  await expect(register.getByRole("button", { name: "JE-0119" })).toHaveCount(0);
  await expect(register.getByRole("button", { name: edge })).toHaveCount(0);
  await expect(register.getByRole("button", { name: "JE-0006" })).toHaveCount(0);
  await expect(page.locator("[data-show-more]")).toHaveCount(0);
  // The reach: an older June journal the first hundred never held, opened.
  await register.getByRole("button", { name: "JE-0106" }).click();
  await expect(page.locator("[data-journal-panel]")).toContainText("June 2025 pour");
  // Changing the range starts from the first page again: July's rows stand where June's were.
  await from.fill("2025-07-01");
  await to.fill("2025-07-31");
  await page.getByRole("button", { name: "Apply dates" }).click();
  await expect(register.locator("tbody tr")).toHaveCount(11);
  await expect(register.getByRole("button", { name: "JE-0119" })).toBeVisible();
  await expect(register.getByRole("button", { name: "JE-0106" })).toHaveCount(0);
});

test("each register row shows when it posted, and a draft shows Not posted", async ({ page, request }) => {
  await openRegister(page, request);
  const register = page.getByRole("table", { name: "Journal register" });
  const rowOf = (label: string) => register.locator("tbody tr").filter({ has: page.getByRole("button", { name: label }) });
  // The stub world's stamps read back in the desk's own day words: the July invoice booked in August
  // carries July 18 and the opening balances August 11, the stamp's own day in the house zone.
  await expect(rowOf("JE-0002").locator("[data-posted]")).toHaveText("Jul 18, 2026");
  await expect(rowOf("JE-0001").locator("[data-posted]")).toHaveText("Aug 11, 2026");
  // A draft carries no posted date: plain words in the muted ink, never null, Invalid Date or a blank cell.
  await expect(rowOf("JE-0005").locator("[data-posted]")).toHaveText("Not posted");
  await expect(rowOf("JE-0006").locator("[data-posted]")).toHaveText("Not posted");
});

test("the status filters keep working on the loaded window", async ({ page, request }) => {
  const edge = await openRegister(page, request);
  const register = page.getByRole("table", { name: "Journal register" });
  await expect(register.getByRole("button", { name: edge })).toBeVisible();
  // On the first hundred the pending ones are the demo year's two seeded drafts.
  await page.getByRole("button", { name: /^Pending/ }).click();
  await expect(register.locator("tbody tr")).toHaveCount(2);
  await expect(register.getByRole("button", { name: "JE-0005" })).toBeVisible();
  await expect(register.getByRole("button", { name: "JE-0006" })).toBeVisible();
  await page.getByRole("button", { name: "All", exact: true }).click();
  await expect(register.getByRole("button", { name: edge })).toBeVisible();
  await page.locator("[data-show-more]").click();
  await expect(register.getByRole("button", { name: "JE-0101" })).toBeVisible();
  // The whole loaded window: still the two seeded drafts, the older year adds none.
  await page.getByRole("button", { name: /^Pending/ }).click();
  await expect(register.locator("tbody tr")).toHaveCount(2);
  await expect(register.getByRole("button", { name: "JE-0005" })).toBeVisible();
  await expect(register.getByRole("button", { name: "JE-0101" })).toHaveCount(0);
});

test("a refused range read keeps the shown range, and the same dates can be tried again", async ({ page, request }) => {
  const edge = await openRegister(page, request);
  const register = page.getByRole("table", { name: "Journal register" });
  await expect(register.getByRole("button", { name: edge })).toBeVisible();
  // The first June read is refused, as a lost or failing core would answer it.
  let refused = false;
  await page.route("**/api/v1/gl/journals?**date_from=2025-06-01**", (route) => {
    if (refused) return route.fallback();
    refused = true;
    return route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "unavailable" }) });
  });
  await page.locator('[data-field="journal-from"]').fill("2025-06-01");
  await page.locator('[data-field="journal-to"]').fill("2025-06-30");
  await page.getByRole("button", { name: "Apply dates" }).click();
  await expect.poll(() => refused).toBe(true);
  // The old window stays, and nothing on screen claims it is June's.
  await expect(register.getByRole("button", { name: edge })).toBeVisible();
  await expect(page.locator("[data-range-words]")).toHaveCount(0);
  // The same dates, pressed again, are read again and land.
  await page.getByRole("button", { name: "Apply dates" }).click();
  await expect(register.locator("tbody tr")).toHaveCount(11);
  await expect(register.getByRole("button", { name: "JE-0106" })).toBeVisible();
});

test("at 390 the register keeps its status in view: the posted date moves under the memo", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openRegister(page, request);
  const register = page.getByRole("table", { name: "Journal register" });
  const rowOf = (label: string) => register.locator("tbody tr").filter({ has: page.getByRole("button", { name: label }) });
  await expect(rowOf("JE-0002")).toBeVisible();
  // No sideways scroll hides a status: the register's scroller is no wider than its card.
  const overflow = await page.locator(".gl-scroll").first().evaluate((el) => el.scrollWidth - el.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  // The date still reads on a phone, under the memo, and a draft still says Not posted.
  await expect(rowOf("JE-0002").locator("[data-posted-narrow]")).toBeVisible();
  await expect(rowOf("JE-0002").locator("[data-posted-narrow]")).toHaveText("Jul 18, 2026");
  await expect(rowOf("JE-0005").locator("[data-posted-narrow]")).toHaveText("Not posted");
});
