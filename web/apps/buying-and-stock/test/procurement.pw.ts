// The procurement slice walked in a real browser against the stub: the card
// lands on the Buying and Stock home and shows the K08 gap, a PO is drafted from
// the shortfall, placed, and seen incoming, and the walled moves are refused in
// words. The stub is held to the core's shapes and sentences by
// test/stub-contract.test.mjs.
import { test, expect, openHome, resetStub } from "./helpers.js";

test("the way in with no handoff offers this role's own card", async ({ page, request }) => {
  await resetStub(request);
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(1);
  await expect(page.locator("[data-flow]")).toContainText("Procurement manager");
});

test("the card lands on the home and the K08 gap is the hot row of the worklist", async ({ page, request }) => {
  await openHome(page, request);
  await expect(page.getByRole("heading", { name: "Buying and Stock", level: 1 })).toBeVisible();
  await expect(page.locator("[data-shortfall-row]")).toHaveCount(3);
  const gap = page.locator("[data-shortfall-row][data-hot]");
  await expect(gap).toHaveCount(1);
  await expect(gap).toContainText("DBS-BRUS-DIM");
  await expect(gap).toContainText("-40");
  await expect(gap).toContainText("240");
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
  await expect(page.locator("[data-po-row]")).toHaveCount(4);
});

test("Draft PO stays grey until a worklist row is picked", async ({ page, request }) => {
  await openHome(page, request);
  const draft = page.getByRole("button", { name: "Draft PO (0)" });
  await expect(draft).toBeDisabled();
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await expect(page.getByRole("button", { name: "Draft PO (1)" })).toBeEnabled();
});

test("shortfall to a draft: the vendor, the line at the suggested quantity and the server's line total", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  const panel = page.locator("[data-draft]");
  await expect(panel).toContainText("New purchase order from the shortfall");
  await expect(panel).toContainText("Alcli (demo)");
  const line = panel.locator("[data-draft-line]");
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("DBS-BRUS-DIM");
  await expect(line).toContainText("240");
  await expect(line).toContainText("$8.40");
  await expect(line).toContainText("$2,016.00");
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-142" })).toContainText("Draft");
});

test("a draft is placed and seen incoming, and the gap leaves the worklist", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await page.locator("[data-field='expected']").fill("2031-03-04");
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Placed: PO-TRT-142 to Alcli (demo)");
  await expect(page.locator("[data-notice]")).toContainText("240");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  const register = page.locator("[data-po-row]", { hasText: "PO-TRT-142" });
  await expect(register).toContainText("Ordered");
  await expect(register).toContainText("$2,016.00");
  const incoming = page.locator("[data-incoming-row]", { hasText: "PO-TRT-142" });
  await expect(incoming).toContainText("2031-03-04");
  await expect(incoming).toContainText("Alcli (demo)");
  // The gap is closed on paper: the core counts the 240 on order, so the line leaves the worklist.
  await expect(page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" })).toHaveCount(0);
  await expect(page.locator("[data-tile='below-point']")).toContainText("2");
});

test("an order placed without a promised date shows no date, never an invented one", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Placed: PO-TRT-142");
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-142" })).toContainText("no date");
});

test("the delivery and the promised date of a draft are saved with Save draft", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await page.locator("[data-field='expected']").fill("2031-03-04");
  await page.locator("[data-field='delivery']").selectOption("PICKUP");
  await page.getByRole("button", { name: "Save draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Draft PO-TRT-142 saved");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-142" })).toContainText("Draft");
});

test("a draft with its line removed is refused by the server in its own words", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await page.locator("[data-draft-line]").getByRole("button", { name: "Remove" }).click();
  await expect(page.locator("[data-draft-line]")).toHaveCount(0);
  await page.getByRole("button", { name: "Place order" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Cannot order an empty purchase order, add at least one line.");
});

test("Cancel draft cancels the order it drafted", async ({ page, request }) => {
  await openHome(page, request);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await page.getByRole("button", { name: "Cancel draft" }).click();
  await expect(page.locator("[data-draft]")).toHaveCount(0);
  await expect(page.locator("[data-po-row]", { hasText: "PO-TRT-142" })).toContainText("Cancelled");
  // The gap is open again: a cancelled draft is not supply.
  await expect(page.locator("[data-tile='below-point']")).toContainText("3");
});

test("a role the gate walls out of ordering is refused in words naming who may", async ({ page, request }) => {
  await openHome(page, request);
  await request.post("/_stub/wall");
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await expect(page.locator("[data-error]")).toContainText("is for the Procurement Manager");
  await expect(page.locator("[data-error]")).toContainText("Other roles can read what is on order");
  await expect(page.locator("[data-draft]")).toHaveCount(0);
});

test("there is no vendor bill control, and the words say who posts a bill", async ({ page, request }) => {
  await openHome(page, request);
  await expect(page.getByRole("button", { name: /bill/i })).toHaveCount(0);
  await expect(page.getByRole("link", { name: /bill/i })).toHaveCount(0);
  await expect(page.locator("[data-bills-note]")).toContainText("Posting a vendor bill is for Payables and Receivables");
});

test("an order the yard has received reads Received, as an outline word and never a button", async ({ page, request }) => {
  await openHome(page, request);
  await request.post("/_stub/receive", { data: { po_no: "PO-TRT-141" } });
  await page.getByRole("button", { name: "Refresh" }).first().click();
  const row = page.locator("[data-po-row]", { hasText: "PO-TRT-141" });
  await expect(row).toContainText("Received");
  await expect(row.getByRole("button")).toHaveCount(0);
  await expect(page.locator("[data-incoming-row]", { hasText: "PO-TRT-141" })).toHaveCount(0);
});

test.skip("PENDING: the yard team receives the placed order in Yard Pick and Receive (waits for Yard receiving)", async () => {
  // The cross role receive leg of roadmap 2.7. It needs the Yard receiving app and the yard roles'
  // cards on one running core; it is named here so it cannot be forgotten.
});

test("on a phone the page does not scroll sideways, on the home or on a draft", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openHome(page, request);
  const sideways = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(await sideways()).toBeLessThanOrEqual(0);
  await page.locator("[data-shortfall-row]", { hasText: "DBS-BRUS-DIM" }).getByRole("checkbox").click();
  await page.getByRole("button", { name: "Draft PO (1)" }).click();
  await expect(page.locator("[data-draft]")).toBeVisible();
  expect(await sideways()).toBeLessThanOrEqual(0);
});
