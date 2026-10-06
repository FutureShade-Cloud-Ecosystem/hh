// PR 121 round 2 follow-up: the four race fixes of the previous round now each have a test that fails without
// them. Every one holds an answer back behind a gate the test opens (a delayed route), so the order of events is
// exact and the result does not depend on how fast or how starved the machine is.
import { expect, test } from "@playwright/test";
import { gate, open, seed, settle, signIn, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a terms read that was out when the person edited does not land over the edit", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const po = page.getByTestId("qb-po");
  await expect(po).toBeVisible();
  // The save of the edit and the re-read are both held, so the edit is still unsaved when the read lands.
  const savePut = gate();
  const reread = gate();
  await page.route(/\/api\/v1\/quotes\/q-1001\/terms$/, async (route) => {
    if (route.request().method() === "PUT") await savePut.opened;
    else await reread.opened;
    await route.continue();
  });
  const putSeen = page.waitForRequest((r) => r.method() === "PUT" && r.url().endsWith("/quotes/q-1001/terms"));
  await page.getByTestId("qb-save-draft").click();
  await po.fill("PO-TYPED-1");
  await po.blur();
  await putSeen;
  const landed = page.waitForResponse((r) => r.request().method() === "GET" && r.url().endsWith("/quotes/q-1001/terms"));
  reread.open();
  await landed;
  await settle(page);
  await expect(po).toHaveValue("PO-TYPED-1");
  savePut.open();
});

test("a terms save answer that was out when the person kept typing does not land over the newer edit", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const po = page.getByTestId("qb-po");
  await expect(po).toBeVisible();
  const first = gate();
  const later = gate();
  // The re-read that follows a save is held too, so only the save's own answer can speak on the box.
  const reread = gate();
  let puts = 0;
  await page.route(/\/api\/v1\/quotes\/q-1001\/terms$/, async (route) => {
    if (route.request().method() === "PUT") {
      puts += 1;
      await (puts === 1 ? first.opened : later.opened);
    } else {
      await reread.opened;
    }
    await route.continue();
  });
  await po.fill("PO-A");
  await expect.poll(() => puts).toBe(1);
  await po.fill("PO-AB");
  await po.blur();
  const answered = page.waitForResponse((r) => r.request().method() === "PUT" && r.url().endsWith("/quotes/q-1001/terms"));
  first.open();
  await answered;
  await settle(page);
  await expect(po).toHaveValue("PO-AB");
  later.open();
  reread.open();
});

test("a scan right after the customer is picked waits for the quote's read and adds the line", async ({ page }) => {
  await signIn(page);
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1004$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const readSeen = page.waitForRequest((r) => /\/quotes\/q-1004$/.test(r.url()));
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await readSeen;
  await settle(page);
  await expect(page.getByTestId("qb-line")).toHaveCount(0);
  held.open();
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("a late terms read never rewrites the delivery fee being typed", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick", terms: { fulfillment: "DELIVERY" } });
  await signIn(page);
  await open(page, "Q-1004");
  const fee = page.getByTestId("qb-delivery-fee");
  await expect(fee).toBeVisible();
  const held = gate();
  await page.route(/\/api\/v1\/quotes\/q-1004\/terms$/, async (route) => {
    await held.opened;
    await route.continue();
  });
  await page.getByTestId("qb-save-draft").click();
  await fee.click();
  await fee.pressSequentially("12.50");
  const seen = writes(page);
  const landed = page.waitForResponse((r) => r.request().method() === "GET" && r.url().endsWith("/quotes/q-1004/terms"));
  held.open();
  await landed;
  await settle(page);
  await expect(fee).toHaveValue("12.50");
  expect(seen).toHaveLength(0);
});
