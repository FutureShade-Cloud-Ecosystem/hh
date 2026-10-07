// The one sender for deferred writes (src/deferred-send.ts), at the screen: a write that was asked in one person's
// session goes out under that session or not at all, and the layout store keeps the reset and the changes after it in
// order. The subject of every request's bearer is read off the request, so a wrong person is seen on the wire.
import { expect, test, type Page, type Request } from "@playwright/test";
import { hold, open, quiet, seed, settle, signIn } from "./switch-helpers.js";

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

function subOf(request: Request): string {
  const auth = request.headers()["authorization"] ?? "";
  const part = auth.replace(/^Bearer /, "").split(".")[1] ?? "";
  if (part === "") return "(no bearer)";
  return (JSON.parse(Buffer.from(part, "base64url").toString("utf8")) as { sub?: string }).sub ?? "(no sub)";
}

/** Every write matching the pattern, in order, as "METHOD path as subject" and with its body. */
function record(page: Page, pattern: RegExp): { sent: string[]; bodies: string[] } {
  const sent: string[] = [];
  const bodies: string[] = [];
  page.on("request", (request) => {
    const path = new URL(request.url()).pathname;
    if (request.method() !== "GET" && pattern.test(path)) {
      sent.push(`${request.method()} ${path} as ${subOf(request)}`);
      bodies.push(request.postData() ?? "");
    }
  });
  return { sent, bodies };
}

async function signOutThenIn(page: Page, role: string): Promise<void> {
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 10000 });
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

const MANAGER = "demo-account-sales-manager";
const SALES = "demo-account-sales";

test("sender: a terms PUT queued behind a held one is never sent under the next person's session", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page, "Sales");
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const puts = record(page, /\/quotes\/q-1004\/terms$/);
  const put1 = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-FIRST");
  await put1.asked;
  await page.getByTestId("qb-contact").fill("Site contact by Sales");
  // Past the terms debounce: the second save is asked and waits its turn behind the held first.
  await page.waitForTimeout(1500);
  await signOutThenIn(page, "Sales manager");
  await quiet(page);
  await put1.release();
  await settle(page);
  expect(puts.sent.filter((p) => p.endsWith(`as ${MANAGER}`))).toEqual([]);
  expect(puts.sent.every((p) => p.endsWith(`as ${SALES}`))).toBe(true);
});

test("sender: a terms flush on leaving queued behind a held PUT is never sent under the next person's session", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page, "Sales");
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const puts = record(page, /\/quotes\/q-1004\/terms$/);
  const put1 = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-FIRST");
  await put1.asked;
  await page.getByTestId("qb-contact").fill("Site contact by Sales");
  // Leave before the debounce: the edit is flushed to its own quote, in its queue turn behind the held PUT.
  await open(page, "Q-1001");
  await signOutThenIn(page, "Sales manager");
  await quiet(page);
  await put1.release();
  await settle(page);
  expect(puts.sent.filter((p) => p.endsWith(`as ${MANAGER}`))).toEqual([]);
  expect(puts.sent.every((p) => p.endsWith(`as ${SALES}`))).toBe(true);
});

test("sender: a terms PUT queued behind a held one is never sent when the same person signs back in on the same quote", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page, "Sales");
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const puts = record(page, /\/quotes\/q-1004\/terms$/);
  const put1 = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT", times: 1 });
  await page.getByTestId("qb-po").fill("PO-FIRST");
  await put1.asked;
  await page.getByTestId("qb-contact").fill("Site contact by Sales");
  // Past the debounce and away: the debounced save and the flush of the leaving both queue behind the held first.
  await page.waitForTimeout(1500);
  await open(page, "Q-1001");
  // The same person signs back in and opens the very quote the edit was typed on: the queued PUT is refused all the
  // same, and the proof is on the wire, the server never sees it.
  await signOutThenIn(page, "Sales");
  await open(page, "Q-1004");
  await quiet(page);
  const before = puts.sent.length;
  await put1.release();
  await settle(page);
  expect(puts.sent.length).toBe(before);
  expect(puts.sent.every((p) => p.endsWith(`as ${SALES}`))).toBe(true);
});

test("sender: a reset queued behind a held layout save is never sent under the next person's session", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  const ws = record(page, /\/my-workspace$/);
  const first = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "PUT", times: 1 });
  await page.getByRole("button", { name: "Swap panels" }).click();
  await first.asked;
  await page.getByRole("button", { name: "Reset layout" }).click();
  await signOutThenIn(page, "Sales manager");
  await quiet(page);
  await first.release();
  await settle(page);
  expect(ws.sent.filter((p) => p.endsWith(`as ${MANAGER}`))).toEqual([]);
});

test("sender: the same person signing back in saves layout again", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  await signOutThenIn(page, "Sales");
  await quiet(page);
  // The sign in lands on the list page now: walk back to the quote before
  // the layout change (the work area is the quote page's own screen).
  await open(page, "Q-1001");
  await quiet(page);
  const ws = record(page, /\/my-workspace$/);
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect.poll(() => ws.sent.length, { timeout: 5000 }).toBeGreaterThan(0);
  expect(ws.sent.every((p) => p.endsWith(`as ${SALES}`))).toBe(true);
});

test("sender: the work area taken off the page and put back saves layout again", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  await page.locator("qd-quote-workarea").evaluate((el) => {
    const parent = el.parentNode as Node;
    const next = el.nextSibling;
    parent.removeChild(el);
    parent.insertBefore(el, next);
  });
  await quiet(page);
  const ws = record(page, /\/my-workspace$/);
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect.poll(() => ws.sent.length, { timeout: 5000 }).toBeGreaterThan(0);
});

test("layout order: a reset and the layout change made after it both survive, in order", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  const ws = record(page, /\/my-workspace$/);
  const first = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "PUT", times: 1 });
  await page.getByRole("button", { name: "Swap panels" }).click();
  await first.asked;
  await page.getByRole("button", { name: "Reset layout" }).click();
  // A different change while the reset waits behind the held save: its debounce fires, and it queues behind the reset.
  await page.locator('[data-action="collapse-primary"]').click();
  await page.waitForTimeout(900);
  await first.release();
  await settle(page);
  await expect.poll(() => ws.sent.length, { timeout: 5000 }).toBe(3);
  expect(ws.sent.map((w) => w.split(" ")[0])).toEqual(["PUT", "DELETE", "PUT"]);
  const last = JSON.parse(ws.bodies[2] as string) as { arrangement: string; primary_collapsed: boolean };
  expect(last.primary_collapsed).toBe(true);
  expect(last.arrangement).toBe("PRIMARY_LEFT");
});

test("layout order: a layout change made while the reset's delete is out survives the delete", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  const ws = record(page, /\/my-workspace$/);
  const del = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "DELETE", times: 1 });
  await page.getByRole("button", { name: "Reset layout" }).click();
  await del.asked;
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect(page.getByTestId("qb-workspace")).toHaveAttribute("data-arrangement", "PRIMARY_RIGHT");
  await page.waitForTimeout(900);
  await del.release();
  await settle(page);
  await expect.poll(() => ws.sent.length, { timeout: 5000 }).toBe(2);
  expect(ws.sent.map((w) => w.split(" ")[0])).toEqual(["DELETE", "PUT"]);
  expect((JSON.parse(ws.bodies[1] as string) as { arrangement: string }).arrangement).toBe("PRIMARY_RIGHT");
  await expect(page.getByTestId("qb-workspace")).toHaveAttribute("data-arrangement", "PRIMARY_RIGHT");
});

test("layout store: a store stopped by the work area leaving sends nothing more of what it had queued, even when the work area is put back", async ({ page }) => {
  await signIn(page, "Sales");
  await open(page, "Q-1001");
  await quiet(page);
  const ws = record(page, /\/my-workspace$/);
  const first = await hold(page, /\/surfaces\/quotes\.builder\/my-workspace$/, { method: "PUT", times: 1 });
  await page.getByRole("button", { name: "Swap panels" }).click();
  await first.asked;
  // A second change waits in the store's queue behind the held save.
  await page.locator('[data-action="collapse-primary"]').click();
  await page.waitForTimeout(900);
  await page.locator("qd-quote-workarea").evaluate((el) => {
    const parent = el.parentNode as Node;
    const next = el.nextSibling;
    parent.removeChild(el);
    parent.insertBefore(el, next);
  });
  await quiet(page);
  await first.release();
  await settle(page);
  // The same person is back on the page, so the sender would let it go: only the store's own stop keeps it unsent.
  expect(ws.bodies.filter((body) => body.includes('"primary_collapsed":true'))).toEqual([]);
});

// The compose create (the one write the census rules immediate although a later turn sends it) is disposed by the
// picker listener's teardown: the pick that asked for it left the page with the person, so an answer that lands
// after a sign out makes nothing, and the one send on the wire is the one the person who picked asked for.
test("sender: the compose create held across a sign out is never re-sent and opens no quote under the next person", async ({ page }) => {
  await signIn(page, "Sales");
  await newQuote(page);
  const creates = record(page, /\/api\/v1\/quotes$/);
  const create = await hold(page, /\/api\/v1\/quotes$/, { method: "POST", times: 1 });
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await picker.getByRole("option", { name: /Fenwick/ }).click();
  const box = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await box.fill("PSN-100");
  await box.press("Enter");
  await create.asked;
  await signOutThenIn(page, "Sales manager");
  await quiet(page);
  await create.release();
  await settle(page);
  expect(creates.sent).toEqual([`POST /api/v1/quotes as ${SALES}`]);
  await expect(page.getByTestId("qb-list")).toBeVisible();
});
