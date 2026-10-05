// PR 121 round 2 fixes, the work area: what belongs to one quote never lands on
// another when the person switches quotes. Each test holds one answer back with a
// delayed route (a gate the test opens), so the order of events is exact and does
// not depend on the machine's speed.
import { expect, test } from "@playwright/test";
import { SALES, gate, open, row, seed, settle, signIn, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a terms edit typed on one quote is saved to that quote, never to the next one opened", async ({ page }) => {
  const q4 = await seed(page, { customer_id: "c-fenwick" });
  expect(q4).toBe("q-1004");
  await signIn(page);
  const seen = writes(page);
  const termsRead = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1004/terms") && r.request().method() === "GET");
  await open(page, "Q-1004");
  await termsRead;
  await page.getByTestId("qb-po").fill("PO-OF-Q1004");
  // At once, before the save has fired: open the other quote.
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect.poll(() => seen.filter((w) => w.startsWith("PUT")).length).toBeGreaterThan(0);
  await page.waitForTimeout(900);
  const puts = seen.filter((w) => w.startsWith("PUT"));
  expect(puts.filter((w) => w.includes("/quotes/q-1004/terms") && w.includes("PO-OF-Q1004"))).toHaveLength(1);
  expect(puts.filter((w) => w.includes("/quotes/q-1001/"))).toHaveLength(0);
  await expect(page.getByTestId("qb-po")).toHaveValue("");
});

test("the terms boxes show the server's values for the open quote, and another quote's value never shows", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", terms: { fulfillment: "DELIVERY", customer_po: "PO-SAVED-7", contact_name: "Site contact A", customer_notes: "Gate code at the side", delivery_address: "12 Pine Rd", requested_date: "2031-05-01", project_job: "Back patio" } });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toHaveValue("PO-SAVED-7");
  for (const [field, value] of [
    ["contact", "Site contact A"],
    ["project", "Back patio"],
    ["notes", "Gate code at the side"],
    ["delivery-address", "12 Pine Rd"],
    ["requested-date", "2031-05-01"],
  ] as const) {
    await expect(page.locator(`[data-terms-field="${field}"]`)).toHaveValue(value);
  }
  // The other quote's terms read is held back: while it is out its boxes hold nothing of Q-1004's.
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/terms$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  const shown = async (): Promise<string[]> =>
    page.locator("[data-terms-field]").evaluateAll((boxes) => boxes.map((box) => (box as HTMLInputElement).value).filter((value) => value !== ""));
  await settle(page);
  expect(await shown()).toEqual([]);
  const late = page.waitForResponse((r) => /\/quotes\/q-1001\/terms$/.test(r.url()));
  held.open();
  await late;
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveValue("");
  expect(await shown()).toEqual([]);
});

test("a slow read of the quote just left never lands over the quote now open", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1004$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  await row(page, "Q-1004").click();
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  const late = page.waitForResponse((r) => /\/quotes\/q-1004$/.test(r.url()));
  held.open();
  await late;
  await settle(page);
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  const seen = writes(page);
  await page.getByRole("button", { name: /^Increase quantity/ }).first().click();
  await expect.poll(() => seen.length).toBeGreaterThan(0);
  expect(seen[0]).toContain("PATCH /api/v1/quotes/q-1001/lines/1");
  expect(seen[0]).toContain('"qty_entered":"121"');
});

test("a send in flight when another quote opens texts and links the quote it began on, and leaves nothing on the next", async ({ page }) => {
  await page.addInitScript(() => {
    (window as unknown as { __copied: string[] }).__copied = [];
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: (text: string) => Promise.resolve((window as unknown as { __copied: string[] }).__copied.push(text)) },
      configurable: true,
    });
  });
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const seen = writes(page);
  await open(page, "Q-1004");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1004\/share-links$/, async (route) => {
    if (route.request().method() === "POST") await held.opened;
    await route.continue();
  });
  await page.getByTestId("qb-send").click();
  await expect.poll(() => seen.filter((w) => w.includes("q-1004/share-links")).length).toBe(1);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  const minted = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1004/share-links") && r.request().method() === "POST");
  held.open();
  await minted;
  await settle(page);
  await page.waitForTimeout(300);
  expect(seen.filter((w) => w.includes("/q-1001/share-links") || w.includes("/q-1001/sms-share"))).toHaveLength(0);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
  await expect(page.getByTestId("qb-error")).toHaveText("");
  // Copy link on Q-1001 mints and hands out Q-1001's own link, never the one Q-1004 made.
  await page.getByTestId("qb-copy-link").click();
  await expect(page.getByTestId("qb-notice")).toContainText("Link copied");
  const copied = await page.evaluate(() => (window as unknown as { __copied: string[] }).__copied);
  expect(copied).toHaveLength(1);
  expect(copied[0]).toContain("sl-q-1001-1");
  expect(copied[0]).not.toContain("q-1004");
});

test("a text answer that lands after another quote opened says nothing on that quote", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1004\/sms-share$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const asked = page.waitForRequest((r) => r.url().endsWith("/quotes/q-1004/sms-share"));
  await page.getByTestId("qb-send").click();
  await asked;
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  const answered = page.waitForResponse((r) => r.url().endsWith("/quotes/q-1004/sms-share"));
  held.open();
  await answered;
  await settle(page);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
  await expect(page.getByTestId("qb-error")).toHaveText("");
  await expect(page.getByTestId("qb-send")).toBeEnabled();
});

test("signing out sends the unsaved terms edit and the layout while the session still stands", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const po = page.getByTestId("qb-po");
  await expect(po).toBeVisible();
  const statuses: string[] = [];
  page.on("response", (r) => {
    const m = r.request().method();
    if (m === "PUT" && /\/quotes\/q-1001\/terms$|\/my-workspace$/.test(r.url())) statuses.push(`${r.url().split("/").slice(-1)[0]} ${r.status()}`);
  });
  await page.getByRole("button", { name: "Swap panels" }).click();
  await po.fill("PO-BEFORE-SIGN-OUT");
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", SALES)).toBeVisible();
  await settle(page);
  expect(statuses.sort()).toEqual(["my-workspace 200", "terms 200"]);
});
