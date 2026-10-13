// The inventory area walked in a real browser against the stub: the lookup,
// browse, branch stock, pending fulfilment, reservations, transfers, receiving
// and the replenishment list, with the walled moves refused in words. The stub
// is held to the core's shapes and sentences by test/inventory-contract.test.mjs.
import type { Page } from "@playwright/test";
import { expect, openInventory, PAYABLES_SEAT, resetStub, roleHandoff, showArea, test, YARD_SEAT, procurementHandoff } from "./helpers.js";

const tab = (page: Page, id: string) => page.locator(`[data-inv-tab="${id}"]`);

async function runLookup(page: Page, label: string): Promise<void> {
  await page.locator('[data-field="lookup-product"]').selectOption({ label });
  await page.getByRole("button", { name: "Look up" }).click();
}

test("the areas bar holds every purchasing area and Inventory and Cycle counts, and the card still lands on purchase orders", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  const bar = page.locator("nav.areas");
  for (const [id, label] of [["orders", "Purchase orders"], ["receiving", "Receiving"], ["vendors", "Vendors"], ["bills", "Vendor bills"], ["landed", "Landed cost"], ["inventory", "Inventory"], ["counts", "Cycle counts"]]) {
    await expect(bar.locator(`[data-area-id="${id}"]`)).toHaveText(label);
  }
  await expect(bar.locator('[data-area-id="orders"][aria-current="true"]')).toBeVisible();
});

test("the stock lookup answers the per-branch buckets and the ledger for the paver", async ({ page, request }) => {
  await openInventory(page, request, "lookup");
  await expect(page.locator("[data-lookup]")).toContainText("Search for a product by name or SKU");
  await runLookup(page, "GLS-PAV-60 Copperline Paver 60mm");
  await expect(page.locator("[data-onhand-row]").first()).toBeVisible();
  const plmRow = page.locator("[data-onhand-row]", { hasText: "Point Loma" });
  await expect(plmRow).toContainText("400");
  await expect(plmRow).toContainText("340");
  await expect(page.locator("[data-move-row]").first()).toBeVisible();
  await expect(page.locator("[data-lookup]").locator("[data-move-row]").first()).toContainText("Receipt");
});

test("a product that has never moved says so plainly, never as a fault", async ({ page, request }) => {
  await openInventory(page, request, "lookup");
  await runLookup(page, "DBS-RYMAR-TURF Rymar Artificial Turf 40mm");
  await expect(page.locator("[data-lookup-empty]")).toBeVisible();
  await expect(page.locator("[data-lookup-empty]")).toContainText("No stock has moved for this product yet.");
});

test("browse filters by text, names the total, and the facets count the rows", async ({ page, request }) => {
  await openInventory(page, request, "browse");
  await page.locator('[data-field="browse-text"]').fill("paver");
  await page.getByRole("button", { name: "Search" }).click();
  await expect(page.locator("[data-browse-row]").first()).toBeVisible();
  await expect(page.locator("[data-browse-total]")).toContainText("1 of 1");
  await expect(page.locator('[data-facet="manufacturer"]')).toContainText("Copperline Pavers");
  await page.locator('[data-field="browse-text"]').fill("");
  await page.getByRole("button", { name: "Search" }).click();
  await expect(page.locator("[data-browse-total]")).toContainText("of 4");
});

test("branch stock reads the four buckets and names the negative on hand", async ({ page, request }) => {
  await openInventory(page, request, "branch-stock");
  await page.locator('[data-field="bs-product"]').selectOption({ label: "GLS-SAND-POLY Polymeric Sand" });
  await page.getByRole("button", { name: "Show stock" }).click();
  await expect(page.locator("[data-bs-row]").first()).toBeVisible();
  const julian = page.locator("[data-bs-row]", { hasText: "Julian" });
  await expect(julian.locator("[data-bs-negative]")).toContainText("-6");
});

test("pending fulfilment lists the sold lines that have not moved stock", async ({ page, request }) => {
  await openInventory(page, request, "pending");
  await expect(page.locator("[data-pending-row]")).toHaveCount(2);
  await expect(page.locator("[data-pending-row]").first()).toContainText("Delivered");
});

test("a reservation is held through the policy, listed, and released; the expiry sweep runs", async ({ page, request }) => {
  await openInventory(page, request, "reservations");
  await expect(page.locator("[data-res-row]").first()).toContainText("Hard hold");
  await page.locator('[data-field="res-product"]').selectOption({ label: "GLS-WALL-CAP Heronfield Wall Cap" });
  await page.locator('[data-field="res-branch"]').selectOption({ label: "Point Loma yard" });
  await page.locator('[data-field="res-qty"]').fill("10");
  await page.getByRole("button", { name: "Hold stock" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Held 10 GLS-WALL-CAP");
  await expect(page.locator("[data-res-row]")).toHaveCount(2);
  const row = page.locator("[data-res-row]").first();
  await row.getByRole("button", { name: "Release" }).click();
  await page.locator('[data-confirm="release"]').getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText("released");
  await page.getByRole("button", { name: "Run the expiry sweep" }).click();
  await page.locator('[data-confirm="expire"]').getByRole("button", { name: "Confirm sweep" }).click();
  await expect(page.locator("[data-notice]")).toContainText("no reservation had expired");
  await expect(page.locator("[data-policy]")).toContainText("Hard");
});

test("a read-only role is refused the hold in words that name who may", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(roleHandoff, PAYABLES_SEAT);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await showArea(page, "inventory");
  await tab(page, "reservations").click();
  await page.locator('[data-field="res-product"]').selectOption({ label: "GLS-PAV-60 Copperline Paver 60mm" });
  await page.locator('[data-field="res-qty"]').fill("5");
  await page.getByRole("button", { name: "Hold stock" }).click();
  await expect(page.locator("[data-error]")).toContainText("look but not touch");
});

test("a transfer is raised, shipped with the route's day pre-filled, and received at Julian", async ({ page, request }) => {
  await openInventory(page, request, "transfers");
  const card = page.locator('[data-transfer="TRF-PLM-0001"]');
  await expect(card).toContainText("In transit");
  await expect(card).toContainText("40 in transit");
  // The draft ships with the configured day pre-filled and editable.
  const draft = page.locator('[data-transfer="TRF-PLM-0002"]');
  await draft.getByRole("button", { name: "Ship", exact: true }).click();
  await expect(draft.locator("[data-ship-row]")).toBeVisible();
  await expect(draft.locator("[data-ship-row]")).toContainText("Point Loma yard to Julian yard is set to 1 day");
  await draft.locator('[data-field="ship-eta"]').fill("2031-03-09");
  await draft.getByRole("button", { name: "Confirm ship" }).click();
  await expect(page.locator('[data-transfer="TRF-PLM-0002"]')).toContainText("In transit");
  const arrived = page.locator('[data-transfer="TRF-PLM-0002"]');
  await arrived.getByRole("button", { name: "Receive" }).click();
  // The truck lands only after a confirm that names the transfer, the yard and the quantity in transit.
  const unload = arrived.locator('[data-confirm="receive-transfer"]');
  await expect(unload).toContainText("TRF-PLM-0002");
  await expect(unload).toContainText("Julian yard");
  await expect(unload).toContainText("in transit");
  await expect(arrived).toContainText("In transit");
  await unload.getByRole("button", { name: "Back" }).click();
  await expect(unload).toHaveCount(0);
  await expect(arrived).toContainText("In transit");
  await arrived.getByRole("button", { name: "Receive" }).click();
  await arrived.locator('[data-confirm="receive-transfer"]').getByRole("button", { name: "Confirm receive" }).click();
  await expect(page.locator('[data-transfer="TRF-PLM-0002"]')).toContainText("Received");
  // A shipped transfer offers no cancel: the correction is a transfer back.
  await expect(page.locator('[data-transfer="TRF-PLM-0002"]')).not.toContainText("Cancel");
  // The truck's contents read against one product.
  await expect(page.locator("[data-incoming-row]").first()).toContainText("TRF-PLM-0001");
  await expect(page.locator("[data-transit-row]").first()).toContainText("1 day");
});

test("a transfer is cancelled while it is only a draft, and a same-branch raise is refused in words", async ({ page, request }) => {
  await openInventory(page, request, "transfers");
  await page.locator('[data-field="tr-from"]').selectOption({ label: "Point Loma yard" });
  await page.locator('[data-field="tr-to"]').selectOption({ label: "Point Loma yard" });
  await page.locator('[data-field="tr-product"]').selectOption({ label: "GLS-PAV-60 Copperline Paver 60mm" });
  await page.locator('[data-field="tr-qty"]').fill("4");
  await page.getByRole("button", { name: "Raise draft" }).click();
  await expect(page.locator("[data-error]")).toContainText("A transfer must cross branches.");
  await page.locator('[data-field="tr-to"]').selectOption({ label: "Julian yard" });
  await page.getByRole("button", { name: "Raise draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("raised as a draft");
  const raised = page.locator("[data-transfer]").first();
  await raised.getByRole("button", { name: "Cancel" }).click();
  const confirm = raised.locator('[data-confirm="cancel-transfer"]');
  await expect(confirm).toContainText("Nothing moves");
  await expect(raised).toContainText("Draft");
  await confirm.getByRole("button", { name: "Confirm cancel" }).click();
  await expect(raised).toContainText("Cancelled");
});

test("shipping more than the yard holds is refused in the server's own words", async ({ page, request }) => {
  await openInventory(page, request, "transfers");
  await page.locator('[data-field="tr-from"]').selectOption({ label: "Julian yard" });
  await page.locator('[data-field="tr-to"]').selectOption({ label: "Point Loma yard" });
  await page.locator('[data-field="tr-product"]').selectOption({ label: "GLS-PAV-60 Copperline Paver 60mm" });
  await page.locator('[data-field="tr-qty"]').fill("10000");
  await page.getByRole("button", { name: "Raise draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("raised as a draft");
  const raised = page.locator("[data-transfer]").first();
  await raised.getByRole("button", { name: "Ship", exact: true }).click();
  await raised.getByRole("button", { name: "Confirm ship" }).click();
  await expect(page.locator("[data-error]")).toContainText("would overdraw the source branch");
});

test("receiving asks first, names the order, the yard and the quantity, then the stock lands", async ({ page, request }) => {
  await openInventory(page, request, "receive");
  const order = page.locator('[data-receive-order="PO-TRT-136"]');
  await expect(order).toBeVisible();
  await order.getByRole("button", { name: "Receive" }).click();
  const confirm = page.locator("[data-receive-confirm]");
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText("PO-TRT-136");
  await expect(confirm).toContainText("Trenton yard");
  await expect(confirm).toContainText("10 pc");
  await confirm.getByRole("button", { name: "Confirm receive" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PO-TRT-136 received");
  await expect(page.locator('[data-receive-order="PO-TRT-136"]')).toHaveCount(0);
});

test("a read-only role is refused the receive in words that name who may", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(roleHandoff, PAYABLES_SEAT);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await showArea(page, "inventory");
  await tab(page, "receive").click();
  const order = page.locator('[data-receive-order="PO-TRT-136"]');
  await order.getByRole("button", { name: "Receive" }).click();
  await page.locator("[data-receive-confirm]").getByRole("button", { name: "Confirm receive" }).click();
  await expect(page.locator("[data-error]")).toContainText("Receiving an order into stock is a work move");
});

test("the replenishment list reads, an edit confirms the facts, and the nudge posts once", async ({ page, request }) => {
  await openInventory(page, request, "replenish");
  await expect(page.locator("[data-point-row]").first()).toBeVisible();
  const row = page.locator("[data-point-row]", { hasText: "GLS-AGG-DG" });
  await row.getByRole("button", { name: "Change" }).click();
  const editor = page.locator("[data-point-editor]");
  await expect(editor).toBeVisible();
  // A cleared field is refused in words, never saved as a zero.
  await editor.locator('[data-field="point"]').fill("");
  await editor.getByRole("button", { name: "Save point" }).click();
  await expect(page.locator("[data-error]")).toContainText("A blank field is not a zero");
  await editor.locator('[data-field="point"]').fill("12");
  await editor.getByRole("button", { name: "Save point" }).click();
  await expect(page.locator("[data-notice]")).toContainText("a reorder point of 12 and orders 10 at a time");
  await row.getByRole("button", { name: "Nudge procurement" }).click();
  await expect(page.locator("[data-notice]")).toContainText("sent to procurement");
  await row.getByRole("button", { name: "Nudge procurement" }).click();
  await expect(page.locator("[data-notice]")).toContainText("already sent this cycle");
  await page.locator('[data-field="bootstrap-branch"]').selectOption({ label: "Julian yard" });
  await page.getByRole("button", { name: "Open a line for every stocked product" }).click();
  const confirm = page.locator('[data-confirm="bootstrap"]');
  await expect(confirm).toContainText("Julian yard");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await confirm.getByRole("button", { name: "Back" }).click();
  await expect(confirm).toHaveCount(0);
  await page.getByRole("button", { name: "Open a line for every stocked product" }).click();
  await page.locator('[data-confirm="bootstrap"]').getByRole("button", { name: "Confirm open lines" }).click();
  await expect(page.locator("[data-notice]")).toContainText("reorder point row");
});

test("releasing a hold and the expiry sweep ask first, naming the hold and the sweep's reach", async ({ page, request }) => {
  await openInventory(page, request, "reservations");
  const row = page.locator("[data-res-row]", { hasText: "Active" }).first();
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "Release" }).click();
  const confirm = page.locator('[data-confirm="release"]');
  await expect(confirm).toContainText("Release the hold on");
  await expect(confirm).toContainText("yard");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await confirm.getByRole("button", { name: "Back" }).click();
  await expect(confirm).toHaveCount(0);
  await page.getByRole("button", { name: "Run the expiry sweep" }).click();
  const sweep = page.locator('[data-confirm="expire"]');
  await expect(sweep).toContainText("every expired hold");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await sweep.getByRole("button", { name: "Confirm sweep" }).click();
  await expect(page.locator("[data-notice]")).toContainText("sweep ran");
  await row.getByRole("button", { name: "Release" }).click();
  await page.locator('[data-confirm="release"]').getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText("released");
});

test("the transit table lists every ordered pair and names the admin wall in words, with no editor to press", async ({ page, request }) => {
  await openInventory(page, request, "transfers");
  await expect(page.locator("[data-transit-row]")).toHaveCount(6);
  await expect(page.locator("[data-transit-note]")).toContainText("tech admin");
  await expect(page.locator("[data-transit]")).not.toContainText("Save transit");
});

test("a yard manager receives the buyer's order and is refused the truck's papers in words", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(roleHandoff, YARD_SEAT);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await showArea(page, "inventory");
  // Receiving is the yard manager's work: the confirm names the facts, the stock lands.
  await tab(page, "receive").click();
  const order = page.locator('[data-receive-order="PO-TRT-136"]');
  await order.getByRole("button", { name: "Receive" }).click();
  await page.locator("[data-receive-confirm]").getByRole("button", { name: "Confirm receive" }).click();
  await expect(page.locator("[data-notice]")).toContainText("PO-TRT-136 received");
  // Raising a transfer is a manage seat's move: refused in the ruled words.
  await tab(page, "transfers").click();
  await page.locator('[data-field="tr-from"]').selectOption({ label: "Point Loma yard" });
  await page.locator('[data-field="tr-to"]').selectOption({ label: "Julian yard" });
  await page.locator('[data-field="tr-product"]').selectOption({ label: "GLS-PAV-60 Copperline Paver 60mm" });
  await page.locator('[data-field="tr-qty"]').fill("4");
  await page.getByRole("button", { name: "Raise draft" }).click();
  await expect(page.locator("[data-error]")).toContainText("manage seats' moves");
});

test("on a phone the inventory tables stack with their labels and the page never scrolls sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openInventory(page, request, "lookup");
  await runLookup(page, "GLS-PAV-60 Copperline Paver 60mm");
  await expect(page.locator("[data-onhand-row]").first()).toBeVisible();
  const sideways = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
  expect(sideways).toBe(false);
});
