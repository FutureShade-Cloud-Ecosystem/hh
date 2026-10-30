// The Payables board walked in a real browser against the stub: the open AP by payee with its
// bands and its total, a payee's open items with the landed cost line marked as one, and the
// empty state. The stub serves the route the core's W2-9a answers
// (GET /api/v1/purchasing/ap-aging), so the board is walked on the shape the board will keep.
//
// The blank check (TO-PRINCIPAL 386): every leg reads the figures on the screen, so a screen
// that renders nothing, or a route that answers nothing, fails the leg instead of passing it.
//
// The last leg is the contract's own: the board is walked on a response whose keys are the
// contract's, and it fails on any key the board reads that the contract does not name. The walk
// is a runtime walk: the desk and the board's own views are driven on a Proxy over the contract's
// fixture, and every property they read is recorded, so a key with no underscore in it (row.payee)
// is caught as readily as one with.
import { test, expect, openDesk, dayFromToday, dayWords, receivablesHandoff } from "./helpers.js";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { AP_AGING_FIXTURE, AP_AGING_KEYS } from "./ap-aging-contract.js";
import { ArDesk, type DeskHost, type DeskWho } from "../src/ar/ar-desk.js";
import type { ArApi } from "../src/ar/ar-api.js";
import type { Page } from "@playwright/test";

/** The cents a figure reads: "$13,482.50" is 1348250. */
function centsOf(text: string): number {
  const dollars = text.replace(/[^0-9.]/g, "");
  const [whole = "0", fraction = ""] = dollars.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

/** The board's open AP, read from the screen. */
async function openBoard(page: Page): Promise<void> {
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Payables", exact: true })).toBeVisible();
  // The board paints its head before the route answers: wait for the table itself.
  await expect(page.locator("[data-payables-aging], [data-payables-empty]").first()).toBeVisible();
}

/** The board's own source, the files that read the route's shape. */
function boardSource(): string {
  const here = fileURLToPath(new URL(".", import.meta.url));
  return ["ap-logic.ts", "ap-views.ts"].map((file) => readFileSync(`${here}../src/ar/${file}`, "utf8")).join("\n");
}

/** The desk's own source, the controller that reads the route's answer. */
function deskSource(): string {
  const here = fileURLToPath(new URL(".", import.meta.url));
  return readFileSync(`${here}../src/ar/ar-desk.ts`, "utf8");
}

/**
 * One method's body, brace matched from its signature. The desk's open AP read is looked at on
 * its own: the rest of the desk belongs to other routes, whose keys this leg must not catch.
 */
function methodBody(source: string, signature: string): string {
  const start = source.indexOf(signature);
  if (start < 0) return "";
  const open = source.indexOf("{", start);
  let depth = 0;
  for (let index = open; index < source.length; index += 1) {
    if (source[index] === "{") depth += 1;
    if (source[index] === "}") {
      depth -= 1;
      if (depth === 0) return source.slice(open + 1, index);
    }
  }
  return "";
}

/**
 * JavaScript's own members: the ones a value carries whatever the contract says. They are not
 * the contract's keys, so the walk does not hold them against it.
 */
const OBJECT_MEMBERS = new Set([
  "constructor",
  "hasOwnProperty",
  "isPrototypeOf",
  "propertyIsEnumerable",
  "toLocaleString",
  "toString",
  "valueOf",
  "__proto__",
]);

/**
 * A deep Proxy over the fixture. Every property the board reads is recorded, whatever the key's
 * shape: a key with no underscore in it (row.payee) is recorded as readily as one with, so a
 * board that read a key the contract does not name cannot pass the leg. An array's own members
 * are the array's shape, not the contract's, so only a plain object's reads are held against it.
 */
function tracked<T>(value: T, reads: Set<string>): T {
  if (value === null || typeof value !== "object") return value;
  return new Proxy(value as object, {
    get(target, prop, receiver) {
      if (typeof prop === "string" && !Array.isArray(target) && !OBJECT_MEMBERS.has(prop)) reads.add(prop);
      const raw = Reflect.get(target, prop, receiver);
      if (raw !== null && typeof raw === "object") return tracked(raw, reads);
      return raw;
    },
  }) as T;
}

/** A control the desk can press, without a browser: the press is the desk's own code. */
function control(action: string, attrs: Record<string, string>): Element {
  return {
    getAttribute: (name: string) => (name === "data-action" ? action : attrs[name] ?? null),
  } as unknown as Element;
}

/**
 * The desk, held to nothing: the host does nothing and the route answers the contract's own
 * fixture, so the desk's reads are the desk's own and nothing else.
 */
function deskOn(reads: Set<string>): ArDesk {
  const page = tracked(AP_AGING_FIXTURE, reads);
  const api = {
    apAging: async () => ({ ok: true, value: page }),
    office: () => ({}),
  } as unknown as ArApi;
  const host: DeskHost = {
    repaint: () => {},
    reveal: () => {},
    field: () => "",
    setField: () => {},
    download: () => {},
    mountDocument: () => {},
  };
  const who: DeskWho = { userId: "contract-leg", roleCode: "financial_controller", userName: "Contract leg" };
  return new ArDesk(api, host, who);
}

/**
 * The walk: the desk reads the open AP and draws the board, then a payee's open items are opened
 * and drawn too. Every property read on the way is recorded.
 */
async function walkTheBoard(reads: Set<string>): Promise<void> {
  const desk = deskOn(reads);
  desk.openPayables();
  await new Promise((resolve) => setTimeout(resolve, 0));
  desk.renderPayablesArea();
  // A payee's open items: the vendor row's own bills, read and drawn.
  desk.press(control("payables-open", { "data-payee-id": AP_AGING_FIXTURE.items[0].vendor_id ?? "" }));
  desk.renderPayablesArea();
}

test("the Payables area is in the Money Desk's top level switch, beside Receivables", async ({ page, request }) => {
  await openDesk(page, request);
  const areas = page.locator("nav[aria-label='Money Desk areas']");
  await expect(areas.getByRole("button", { name: "Payables", exact: true })).toBeVisible();
  await expect(areas.getByRole("button", { name: "Receivables", exact: true })).toBeVisible();
  // One way in: the receivables desk's own views nav holds no Payables.
  const views = page.locator("nav[aria-label='Money Desk views']");
  await expect(views.getByRole("button", { name: "Payables", exact: true })).toHaveCount(0);
  await expect(page.locator(".tab-later")).toHaveCount(0);
});

test("the Payables area rides the receivables gate: every role that holds the receivables aging holds it", async ({ page, request }) => {
  // Payables and Receivables holds the receivables desk, so it holds the board.
  await openDesk(page, request);
  await expect(page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true })).toBeVisible();
  // The controller holds the receivables desk too, so it holds the board.
  const second = await page.context().newPage();
  await second.addInitScript(receivablesHandoff as (who: string) => void, "controller");
  await second.goto("/");
  await expect(second.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true })).toBeVisible();
  await second.close();
});

test("the board opens on the open AP: the five bands, the payee rows and the day it is read as of", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  await expect(page.locator("[data-payables-aging]")).toBeVisible();
  await expect(page.locator("[data-band-head]")).toHaveText(["Current", "1 to 30 days", "31 to 60 days", "61 to 90 days", "Over 90 days"]);
  await expect(page.locator("[data-payee-row]")).toHaveCount(6);
  // The rows are the most overdue first, the desk's own rule for an aging view.
  await expect(page.locator("[data-payee-row]").first()).toContainText("Meadowlark Landscape Lighting (demo)");
  await expect(page.locator("[data-field='payables-asof']")).toHaveValue(dayFromToday(0));
  // The footnote date reads the way the as of control reads it, never as the ISO digits.
  await expect(page.locator("[data-payables-foot]")).toContainText("As of " + dayWords(dayFromToday(0)));
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
  // The landed cost is a type, so it rides the document's own line beside what it was for.
  await expect(line).toContainText("Landed cost");
  await expect(line).toContainText("Freight");
  await expect(line).toContainText("PO-1042");
  await expect(line).toContainText("$780.00");
});

test("a carrier's own bill says so, and the footnote beside it does not contradict it", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  await page.locator("[data-payee-row]", { hasText: "Northwind Freight Lines (demo)" }).getByRole("button").click();
  // The carrier's own invoice is not a vendor's bill: it says it is the carrier's.
  await expect(page.locator("[data-line-row]", { hasText: "B-3301" })).toContainText("Carrier bill");
  // And the page's own words say the same thing, so nothing on it reads as a contradiction.
  await expect(page.locator("section[aria-label='Open items'] .foot")).toContainText("Landed costs are owed to the carrier or broker, not to the supplier.");
  // A plain vendor's bill still says bill.
  await page.getByRole("button", { name: "‹ Payables" }).click();
  await page.locator("[data-payee-row]", { hasText: "Saltmarsh Fasteners (demo)" }).getByRole("button").click();
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).toContainText("Bill");
  await expect(page.locator("[data-line-row]", { hasText: "B-4107" })).not.toContainText("Carrier bill");
});

test("the status says how late the document is, and a late document never says open", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  await page.locator("[data-payee-row]", { hasText: "Northwind Freight Lines (demo)" }).getByRole("button").click();
  // B-3301 fell due three days ago: its status is the register's own past due word, not open.
  const bill = page.locator("[data-line-row]", { hasText: "B-3301" });
  await expect(bill).toContainText("3d past due");
  await expect(bill).not.toContainText("open");
  // The landed cost line is late too, and says so in the same words.
  const landed = page.locator("[data-line-row][data-doc-kind='LANDED_COST']");
  await expect(landed).toContainText("33d past due");
  await expect(landed).not.toContainText("open");
});

test("the two oldest bands carry the desk's aging colour, and a zero carries none", async ({ page, request }) => {
  await openDesk(page, request);
  // The desk's own aging tiles, the colours the board borrows.
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  const tileColour = async (title: string): Promise<string> =>
    page.locator("[data-card]").filter({ hasText: title }).locator(".tile-figure").evaluate((el) => getComputedStyle(el).color);
  const escalation = await tileColour("90 days and over");
  const late = await tileColour("61 to 89 days");
  expect(escalation).not.toBe(late);

  await openBoard(page);
  const bandColour = async (band: string): Promise<string> =>
    page.locator(`[data-band-head='${band}']`).evaluate((el) => getComputedStyle(el).color);
  // The headings and the money in them wear the same colour the tiles wear.
  expect(await bandColour("DAYS_90_PLUS")).toBe(escalation);
  expect(await bandColour("DAYS_60_TO_90")).toBe(late);
  const over90 = page.locator("[data-band-cell='DAYS_90_PLUS']");
  expect(await over90.first().evaluate((el) => getComputedStyle(el).color)).toBe(escalation);
  expect(await page.locator("[data-band-cell='DAYS_60_TO_90']").nth(2).evaluate((el) => getComputedStyle(el).color)).toBe(late);
  // A zero keeps the plain ink: a column of red zeros says nothing.
  const plain = await page.locator("[data-band-cell='CURRENT']").first().evaluate((el) => getComputedStyle(el).color);
  expect(await over90.nth(2).evaluate((el) => getComputedStyle(el).color)).toBe(plain);
  expect(await page.locator("[data-band-cell='DAYS_60_TO_90']").first().evaluate((el) => getComputedStyle(el).color)).toBe(plain);
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
  // The part paid bill says what has been paid against it, beside the document kind, and its
  // status says how late it is: the register's past due word, never "open".
  const partPaid = page.locator("[data-line-row]", { hasText: "B-4107" });
  await expect(partPaid).toContainText("$400.00 paid");
  await expect(partPaid).toContainText("$560.00");
  await expect(partPaid).toContainText("20d past due");
  await expect(partPaid).not.toContainText("Part paid");
});

test("the board says so when nothing is open as of that day", async ({ page, request }) => {
  await openDesk(page, request);
  await request.post("/_stub/scenario", { data: { name: "empty" } });
  await openBoard(page);
  await expect(page.locator("[data-payables-empty]")).toContainText("No open AP as of this day.");
  await expect(page.locator("[data-payables-empty]")).toContainText(dayWords(dayFromToday(0)));
  await expect(page.locator("[data-payee-row]")).toHaveCount(0);
  await expect(page.locator("[data-payables-total]")).toHaveCount(0);
  // The footnote under an empty board says nothing worth saying, so it is not there.
  await expect(page.locator("[data-payables-foot]")).toHaveCount(0);
});

test("the as of control reads the books as of the day the operator picks", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  await expect(page.locator("[data-payee-row]")).toHaveCount(6);
  const field = page.locator("[data-field='payables-asof']");
  await field.fill(dayFromToday(-200));
  await expect(page.locator("[data-payables-empty]")).toContainText("No open AP as of this day.");
  await expect(page.locator("[data-payables-empty]")).toContainText(dayWords(dayFromToday(-200)));
  await expect(page.locator("[data-payables-foot]")).toHaveCount(0);
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

test("every road out of the payables board closes the payee whose open items were open", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  // A payee's open items are open.
  await page.locator("[data-payee-row]", { hasText: "Saltmarsh Fasteners (demo)" }).getByRole("button").click();
  await expect(page.locator("[data-payables-lines]")).toBeVisible();

  // The assistant panel names a customer: the desk opens that record on the receivables desk's
  // own route, which leaves the payables board. The panel is in the desk's own shadow root.
  await page.evaluate(() => {
    const app = document.querySelector("money-desk-app");
    const panel = app?.shadowRoot?.querySelector("hh-tanyai-panel");
    panel?.dispatchEvent(new CustomEvent("hh-tanyai-open-ref", { detail: { ref: { kind: "customer", id: "e0000000-0000-4000-8000-000000000004" } } }));
  });
  await expect(page.getByRole("heading", { name: "Summit Patio Co (demo)" })).toBeVisible();

  // And back to the board: the payee's open items were closed on the way out, so the board is
  // what shows, not the payee that was open.
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await expect(page.locator("[data-payables-aging]")).toBeVisible();
  await expect(page.locator("[data-payables-lines]")).toHaveCount(0);
});

/**
 * The desk's own pick of the day the books are read as of, driven through the app's own change
 * event. The board takes its as-of control off the screen while a read is in flight, so a person
 * cannot make a second pick while the first is on the way; this drives the desk's own change path
 * so that the two picks overlap, the way the desk has to be ready for.
 */
async function pickAsOf(page: Page, day: string): Promise<void> {
  await page.evaluate((value) => {
    const app = document.querySelector("money-desk-app");
    if (app === null || app.shadowRoot === null) return;
    const field = document.createElement("input");
    field.setAttribute("data-field", "payables-asof");
    field.value = value;
    const event = new Event("change", { bubbles: true });
    Object.defineProperty(event, "target", { value: field });
    app.shadowRoot.dispatchEvent(event);
  }, day);
}

test("a slow answer for an earlier day never replaces the newer pick", async ({ page, request }) => {
  const earlier = dayFromToday(-45);
  const later = dayFromToday(-5);
  // The answer for the earlier day is held back; every other answer is let through at once.
  const held: { route: import("@playwright/test").Route; body: string }[] = [];
  await page.route("**/api/v1/purchasing/ap-aging*", async (route) => {
    const asOf = new URL(route.request().url()).searchParams.get("as_of") ?? "";
    const answer = await request.get(`/api/v1/purchasing/ap-aging${asOf === "" ? "" : `?as_of=${asOf}`}`, { headers: { authorization: "Bearer contract-leg" } });
    const body = await answer.text();
    if (asOf === earlier) {
      held.push({ route, body });
      return;
    }
    await route.fulfill({ status: 200, contentType: "application/json", body });
  });

  await openDesk(page, request);
  await openBoard(page);
  // Two picks, the first one's answer still on the way.
  await pickAsOf(page, earlier);
  await pickAsOf(page, later);
  // The newer pick is what the board shows.
  await expect(page.locator("[data-payables-foot]")).toContainText(`As of ${dayWords(later)}`);

  // Now the slow answer for the earlier day lands. It answers for a day the operator has already
  // moved on from, so the board does not move back to it.
  expect(held.length).toBe(1);
  await held[0]!.route.fulfill({ status: 200, contentType: "application/json", body: held[0]!.body });
  await page.waitForLoadState("networkidle");
  await expect(page.locator("[data-payables-foot]")).toContainText(`As of ${dayWords(later)}`);
  await expect(page.locator("[data-payables-foot]")).not.toContainText(dayWords(earlier));
  await expect(page.locator("[data-field='payables-asof']")).toHaveValue(later);
});

test("the day the books are read as of reads in the desk's one words, never as digits", async ({ page, request }) => {
  await openDesk(page, request);
  await openBoard(page);
  const foot = page.locator("[data-payables-foot]");
  await expect(foot).toContainText(`As of ${dayWords(dayFromToday(0))}`);
  // The old shape is gone: the board does not write a day as M/D/YYYY, the way the rest of the
  // desk does not either.
  await expect(foot).not.toContainText(/\d{1,2}\/\d{1,2}\/\d{4}/);
  // And the empty state says the same day the same way.
  await request.post("/_stub/scenario", { data: { name: "empty" } });
  await page.locator("[data-field='payables-asof']").fill(dayFromToday(-200));
  await expect(page.locator("[data-payables-empty]")).toContainText(dayWords(dayFromToday(-200)));
  await expect(page.locator("[data-payables-empty]")).not.toContainText(/\d{1,2}\/\d{1,2}\/\d{4}/);
});

test("the contract's own leg: the route answers the contract's keys, and the board reads no key the contract does not name", async ({ page, request }) => {
  // The route's answer names nothing the contract does not, at any level.
  const answer = await request.get("/api/v1/purchasing/ap-aging", { headers: { authorization: "Bearer contract-leg" } });
  expect(answer.ok()).toBe(true);
  const served = (await answer.json()) as { items: { open_items: unknown[] }[] } & Record<string, unknown>;
  const stray = (object: Record<string, unknown>): string[] => Object.keys(object).filter((key) => !AP_AGING_KEYS.has(key));
  expect(stray(served)).toEqual([]);
  for (const row of served.items) {
    expect(stray(row as unknown as Record<string, unknown>)).toEqual([]);
    for (const item of row.open_items) expect(stray(item as Record<string, unknown>)).toEqual([]);
  }

  // The board is walked on a response whose keys are the contract's: the fixture, not the stub.
  await page.route("**/api/v1/purchasing/ap-aging*", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(AP_AGING_FIXTURE) });
  });
  await openDesk(page, request);
  await openBoard(page);
  await expect(page.locator("[data-payee-row]")).toHaveCount(4);
  // The rows are the most overdue first: the carrier's landed cost is the oldest money here.
  await expect(page.locator("[data-payee-row]").first()).toContainText("Northwind Freight Lines (demo)");
  await expect(page.locator("[data-payables-total]")).toContainText("$11,322.50");

  // The payee that holds a vendor row and a landed cost row holds two, and they open apart:
  // the core gives the landed cost its own row, and never folds it into the carrier's bill.
  const landed = page.locator("[data-payee-row][data-payee-id='c0000000-0000-4000-8000-000000000005']");
  await expect(landed).toContainText("Landed cost carrier");
  const carrier = page.locator("[data-payee-row][data-payee-id='p0000000-0000-4000-8000-000000000007']");
  await expect(carrier).toContainText("Net 15");
  await carrier.getByRole("button").click();
  await expect(page.locator("[data-line-row]")).toHaveCount(1);
  await expect(page.locator("[data-line-row]")).toContainText("B-3301");
  await page.getByRole("button", { name: "‹ Payables" }).click();
  await landed.getByRole("button").click();
  await expect(page.locator("[data-line-row]")).toHaveCount(1);
  await expect(page.locator("[data-line-row]")).toContainText("PO-1042");
  await expect(page.locator("[data-line-row]")).toContainText("Freight");

  // And the board's own reads name nothing the contract does not: the desk and the board's views
  // are walked on a Proxy over the contract's fixture, and every property they read is a key the
  // schemas name. A key with no underscore in it is caught as readily as one with, so a board
  // that read row.payee would fail here instead of failing in the browser.
  const reads = new Set<string>();
  await walkTheBoard(reads);
  expect([...reads].filter((key) => !AP_AGING_KEYS.has(key)).sort()).toEqual([]);
  // The walk really read the shape: the band it reads is the contract's 61 to 90 band.
  expect(reads.has("days_60_to_90_cents")).toBe(true);

  // The board's own source, read for the keys it names: the two files that draw the route's
  // shape. The desk's own reads are caught by the walk above, but the source of the two files
  // that draw it is read too, so a key named in a path the walk did not take is still held.
  const sourceReads = new Set<string>();
  const source = boardSource();
  for (const match of source.matchAll(/\.([a-z][a-z0-9]*_[a-z0-9_]+)/g)) sourceReads.add(match[1]);
  for (const match of source.matchAll(/field: "([a-z0-9_]+)"/g)) sourceReads.add(match[1]);
  expect([...sourceReads].filter((key) => !AP_AGING_KEYS.has(key)).sort()).toEqual([]);
  expect(sourceReads.has("days_60_to_90_cents")).toBe(true);

  // And the desk's source is read for the same reason, over the open AP read alone: the page
  // the route answered is read either straight off the answer or off the field it is kept in,
  // and every key read off it is a key the contract names. The rest of the desk belongs to other
  // routes, whose keys this leg must not catch.
  const deskReads = new Set<string>();
  const desk = deskSource();
  const apRead = methodBody(desk, "async #loadPayables(");
  for (const match of apRead.matchAll(/\.value\.([a-z][a-z0-9_]+)/g)) deskReads.add(match[1]);
  for (const match of desk.matchAll(/#payablesPage\.([a-z][a-z0-9_]+)/g)) deskReads.add(match[1]);
  expect([...deskReads].filter((key) => !AP_AGING_KEYS.has(key)).sort()).toEqual([]);
  // The read is really found: the desk reads the day it is read as of.
  expect(deskReads.has("as_of")).toBe(true);
});
