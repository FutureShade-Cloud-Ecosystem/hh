// The Payables board walked in a real browser against the stub: the open AP by payee with its
// bands and its total, a payee's open items with the landed cost line marked as one, and the
// empty state. The stub serves the route the core's W2-9a will answer
// (GET /api/v1/purchasing/ap-aging), so the board is walked on the shape the board will keep.
//
// The blank check (TO-PRINCIPAL 386): every leg reads the figures on the screen, so a screen
// that renders nothing, or a route that answers nothing, fails the leg instead of passing it.
import { test, expect, openDesk, dayFromToday } from "./helpers.js";
import type { Page } from "@playwright/test";

/** The cents a figure reads: "$13,482.50" is 1348250. */
function centsOf(text: string): number {
  const dollars = text.replace(/[^0-9.]/g, "");
  const [whole = "0", fraction = ""] = dollars.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

/** The board's open AP, read from the screen. */
async function openBoard(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Payables", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Payables", exact: true })).toBeVisible();
  // The board paints its head before the route answers: wait for the table itself.
  await expect(page.locator("[data-payables-aging], [data-payables-empty]").first()).toBeVisible();
}

test("the Payables tab is live in the receivables desk, beside the views it always held", async ({ page, request }) => {
  await openDesk(page, request);
  const nav = page.locator("nav.tabs", { hasText: "Receivables" }).first();
  await expect(nav.getByRole("button", { name: "Payables", exact: true })).toBeVisible();
  await expect(page.locator(".tab-later")).toHaveCount(0);
});

test("the board opens on the open AP: the five bands, the payee rows and the day it is read as of", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  await expect(page.locator("[data-payables-aging]")).toBeVisible();
  await expect(page.locator("[data-band-head]")).toHaveText(["Current", "1 to 30 days", "31 to 60 days", "61 to 90 days", "Over 90 days"]);
  await expect(page.locator("[data-payee-row]")).toHaveCount(6);
  await expect(page.locator("[data-payee-row]").first()).toContainText("Meadowlark Landscape Lighting (demo)");
  await expect(page.locator("[data-field='payables-asof']")).toHaveValue(dayFromToday(0));
  await expect(page.locator("[data-payables-foot]")).toContainText("As of " + dayFromToday(0));
});

test("the bucket totals sum to the total, on every payee row and on the whole book", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  const rows = page.locator("[data-payee-row]");
  const count = await rows.count();
  expect(count).toBe(6);
  for (let index = 0; index < count; index += 1) {
    const row = rows.nth(index);
    const bands = await row.locator("[data-band-cell]").allTextContents();
    expect(bands).toHaveLength(5);
    const sum = bands.reduce((running, figure) => running + centsOf(figure), 0);
    const total = await row.locator("[data-payee-total]").innerText();
    expect(sum, `the bands of ${await row.innerText()} add up to its total`).toBe(centsOf(total));
  }
  const bookBands = await page.locator("[data-band-total]").allTextContents();
  const bookSum = bookBands.reduce((running, figure) => running + centsOf(figure), 0);
  const bookTotal = await page.locator("[data-payables-total]").innerText();
  expect(bookSum).toBe(centsOf(bookTotal));
  // The book total is every payee's total: the board adds nothing up, the route does.
  const rowTotals = await page.locator("[data-payee-total]").allTextContents();
  const rowSum = rowTotals.reduce((running, figure) => running + centsOf(figure), 0);
  expect(rowSum).toBe(centsOf(bookTotal));
});

test("every band carries money, so the aging is not a row of zeros", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  const bands = await page.locator("[data-band-total]").allTextContents();
  expect(bands).toHaveLength(5);
  for (const figure of bands) {
    expect(centsOf(figure)).toBeGreaterThan(0);
  }
});

test("a landed cost line is marked as landed cost, on its own payee row", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  const carrier = page.locator("[data-payee-row]", { hasText: "Northwind Freight Lines (demo)" });
  await expect(carrier).toContainText("Landed cost carrier");
  await carrier.getByRole("button").click();
  await expect(page.getByRole("heading", { name: "Northwind Freight Lines (demo)" })).toBeVisible();
  // The open items hold an open bill with its due date and open amount, and the landed cost
  // line beside it, marked as the landed cost it is.
  const lines = page.locator("[data-line-row]");
  await expect(lines).toHaveCount(2);
  await expect(page.locator("[data-line-row]", { hasText: "B-3301" })).toContainText("$420.00");
  await expect(page.locator("[data-line-row]", { hasText: "B-3301" })).toContainText("3 days ago");
  const line = page.locator("[data-line-row][data-doc-kind='LANDED_COST']");
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("Landed cost");
  await expect(line).toContainText("Freight");
  await expect(line).toContainText("LC-2201");
  await expect(line).toContainText("$780.00");
});

test("a bill and a landed cost line are never folded into one another", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  const vendor = page.locator("[data-payee-row]", { hasText: "Saltmarsh Fasteners (demo)" });
  await vendor.getByRole("button").click();
  await expect(page.getByRole("heading", { name: "Saltmarsh Fasteners (demo)" })).toBeVisible();
  const lines = page.locator("[data-line-row]");
  await expect(lines).toHaveCount(2);
  await expect(lines.first()).toContainText("Bill");
  // The part paid bill says so, and says what has been paid against it.
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("Part paid");
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$400.00 paid");
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("$560.00");
});

test("the board says so when nothing is open as of that day", async ({ page, request }) => {
  await openDesk(page, request);
  await request.post("/_stub/scenario", { data: { name: "empty" } });
  await openBoard(page);
  await expect(page.locator("[data-payables-empty]")).toContainText("No open AP as of this day.");
  await expect(page.locator("[data-payee-row]")).toHaveCount(0);
  await expect(page.locator("[data-payables-total]")).toHaveCount(0);
});

test("the as of control reads the books as of the day the operator picks", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  await expect(page.locator("[data-payee-row]")).toHaveCount(6);
  const field = page.locator("[data-field='payables-asof']");
  await field.fill(dayFromToday(-200));
  await expect(page.locator("[data-payables-empty]")).toContainText("No open AP as of this day.");
  await expect(page.locator("[data-payables-foot]")).toContainText("As of " + dayFromToday(-200));
});

test("the blank check: every board state says what it is, and none of them renders blank", async ({ page, request }) => {
  await openDesk(page, request);
  // The aging list.
  await openBoard(page);
  await expect(page.locator("h1")).toHaveText("Payables");
  await expect(page.locator("section[aria-label='Open AP aging']")).toContainText("Open AP aging");
  expect((await page.locator("main").innerText()).trim()).not.toBe("");
  // A payee's open items.
  await page.locator("[data-payee-row]").first().getByRole("button").click();
  await expect(page.locator("section[aria-label='Open items']")).toContainText("Open items");
  expect((await page.locator("main").innerText()).trim()).not.toBe("");
  await page.getByRole("button", { name: "‹ Payables" }).click();
  await expect(page.locator("[data-payables-aging]")).toBeVisible();
  // The empty state.
  await request.post("/_stub/scenario", { data: { name: "empty" } });
  await page.locator("[data-field='payables-asof']").fill(dayFromToday(0));
  await expect(page.locator("[data-payables-empty]")).toContainText("No open AP as of this day.");
  expect((await page.locator("main").innerText()).trim()).not.toBe("");
});
