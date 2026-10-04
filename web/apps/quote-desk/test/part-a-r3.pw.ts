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

test("P2-2 a terms edit still being saved is waited for when the person returns to the quote, and is not lost", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const seen = writes(page);
  const put = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT" });
  await page.getByTestId("qb-po").fill("PO-TYPED-ON-1004");
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1001");
  await put.asked;
  await row(page, "Q-1004").click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1004");
  // The save is out: the read waits for it, so no empty box is shown as the saved value.
  await settle(page);
  await expect(page.getByTestId("qb-po")).toHaveCount(0);
  put.open();
  await expect(page.getByTestId("qb-po")).toHaveValue("PO-TYPED-ON-1004");
  // The next edit carries the PO, so nothing blanks it.
  await page.getByTestId("qb-contact").fill("Site contact");
  await expect.poll(() => seen.filter((w) => w.startsWith("PUT") && w.includes("Site contact")).length).toBeGreaterThan(0);
  const last = seen.filter((w) => w.startsWith("PUT") && w.includes("Site contact")).pop() ?? "";
  expect(last).toContain('"customer_po":"PO-TYPED-ON-1004"');
});

test("P2-2 a return to a quote whose terms save never answers reads after a bound and says so", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const put = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT" });
  await page.getByTestId("qb-po").fill("PO-NEVER-ANSWERED");
  await row(page, "Q-1001").click();
  await put.asked;
  await row(page, "Q-1004").click();
  await expect(page.getByTestId("qb-po")).toBeVisible({ timeout: 10000 });
  await expect(page.getByTestId("qb-error")).toContainText("still being saved");
});

test("P2-2 sign out waits for the terms save of the quote just left, and sends it while the session stands", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const statuses: string[] = [];
  page.on("response", (r) => {
    if (r.request().method() === "PUT" && /\/quotes\/q-1004\/terms$/.test(r.url())) statuses.push(String(r.status()));
  });
  const put = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT" });
  await page.getByTestId("qb-po").fill("PO-AT-SIGN-OUT");
  await row(page, "Q-1001").click();
  await put.asked;
  await page.getByRole("button", { name: "Sign out" }).click();
  await settle(page);
  // The save is out: the session still stands.
  await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
  put.open();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  expect(statuses.length).toBeGreaterThan(0);
  expect(statuses.every((status) => status === "200")).toBe(true);
});

test("P2-2 sign out is bounded: a save that never answers does not hold it, and the entry page says it timed out", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await expect(page.getByTestId("qb-po")).toBeVisible();
  const put = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "PUT" });
  await page.getByTestId("qb-po").fill("PO-NEVER-ANSWERED");
  await row(page, "Q-1001").click();
  await put.asked;
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible({ timeout: 10000 });
  await expect(page.getByRole("status").filter({ hasText: "timed out" })).toBeVisible();
});

test("P2-3 the identity card shows nothing of the previous customer's account while the new customer's facts are read", async ({ page }) => {
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-identity-credit")).toBeVisible();
  await expect(page.getByTestId("qb-identity")).toContainText("C01");
  const held = await hold(page, /\/quotes\/q-1004\/terms$/, { method: "GET" });
  await row(page, "Q-1004").click();
  await held.asked;
  await expect(page.getByTestId("qb-identity")).toContainText("Fenwick");
  await expect(page.getByTestId("qb-identity-credit")).toHaveCount(0);
  await expect(page.getByTestId("qb-identity")).not.toContainText("C01");
  held.open();
  await expect(page.getByTestId("qb-identity")).toContainText("C02");
});

test("P2-4 the desk resumes the quote that was open when the person signed out, not the one open at the last layout change", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await page.getByRole("button", { name: "Swap panels" }).click();
  await open(page, "Q-1003");
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-context")).toContainText("Q-1003");
});

test("P3 Reset columns takes: the box a failed re-read held on the draft goes back to the default", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const box = page.locator('[data-field="column"][data-column="line.margin_pct"]');
  await page.getByTestId("qb-columns-btn").click();
  const before = await box.isChecked();
  const refuse = (route: import("@playwright/test").Route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the config store is down" }) });
  await page.route("**/api/v1/surfaces/*/config**", refuse);
  const reread = page.waitForResponse((r) => r.request().method() === "GET" && r.url().includes("/config"));
  await box.click();
  await reread;
  await settle(page);
  expect(await box.isChecked()).toBe(!before);
  await page.unroute("**/api/v1/surfaces/*/config**", refuse);
  const deleted = page.waitForResponse((r) => r.request().method() === "DELETE" && r.url().includes("/my-items"));
  await page.getByTestId("qb-columns-reset").click();
  await deleted;
  await settle(page);
  await page.getByTestId("qb-po").fill("any later render");
  await settle(page);
  expect(await box.isChecked()).toBe(before);
});

test("P3 Reset columns with the config re-read refused still shows the default columns", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  const box = page.locator('[data-field="column"][data-column="line.margin_pct"]');
  await page.getByTestId("qb-columns-btn").click();
  const before = await box.isChecked();
  await box.click();
  await expect.poll(() => box.isChecked()).toBe(!before);
  await settle(page);
  await page.route("**/api/v1/surfaces/*/config**", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the config store is down" }) }),
  );
  const deleted = page.waitForResponse((r) => r.request().method() === "DELETE" && r.url().includes("/my-items"));
  await page.getByTestId("qb-columns-reset").click();
  await deleted;
  await settle(page);
  await page.getByTestId("qb-po").fill("any later render");
  await settle(page);
  expect(await box.isChecked()).toBe(before);
});
