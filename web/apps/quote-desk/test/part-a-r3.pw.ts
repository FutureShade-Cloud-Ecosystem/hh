// PR 121 round 3, Part A findings: each test holds one answer back behind a delayed route, so the order of events
// is exact and does not depend on the machine's speed.
import { expect, test } from "@playwright/test";
import { hold, open, row, seed, settle, signIn, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("P2-1 a scan on an open quote while a new quote is being created goes to the open quote, and the screen stays", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  const seen = writes(page);
  const create = await hold(page, /\/api\/v1\/quotes$/, { method: "POST" });
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await picker.getByRole("option", { name: /Fenwick/ }).click();
  await create.asked;
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  // The scan lands on the open quote at once; it does not wait for a create it has no part in.
  await expect(page.getByTestId("qb-line")).toHaveCount(3);
  create.open();
  await settle(page);
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(3);
  const lineWrites = seen.filter((w) => w.includes("/lines"));
  expect(lineWrites).toHaveLength(1);
  expect(lineWrites[0]).toContain("POST /api/v1/quotes/q-1001/lines");
  // The quote that was created is in the list, untouched.
  await expect(row(page, "Q-1005")).toBeVisible();
});

test("P2-1 a scan made in compose while the quote is being created is not added to a quote opened meanwhile", async ({ page }) => {
  await signIn(page);
  const seen = writes(page);
  const create = await hold(page, /\/api\/v1\/quotes$/, { method: "POST" });
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Fenwick");
  await picker.getByRole("option", { name: /Fenwick/ }).click();
  await create.asked;
  // A scan reaches the work area as the scan box's own event: compose has no box, so the event is sent as the gun's would be.
  await page.locator("qd-quote-workarea").evaluate((el) => {
    el.shadowRoot?.dispatchEvent(
      new CustomEvent("pos-add-product", { detail: { product: { id: "p-polymer-sand", base_uom: "bag", name: "Polymeric Locking Sand" } }, bubbles: true, composed: true }),
    );
  });
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  create.open();
  await settle(page);
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  expect(seen.filter((w) => w.includes("/lines"))).toHaveLength(0);
});
