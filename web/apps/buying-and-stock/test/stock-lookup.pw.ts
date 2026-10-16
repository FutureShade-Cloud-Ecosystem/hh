// The Sales stock lookup's browser legs (change 5, board bs-stock-lookup-sales-search-a-product):
// the sales seat lands on the Stock lookup with its two pills, a search lists the products with
// their availability, the picked product reads its stock at each branch and what is coming in, no
// cell draws cost, the buying writes are refused in the core's words, and the buying seats keep
// the app they had.
import { expect, test, resetStub, roleHandoff, procurementHandoff, openInventory, INSIDE_SEAT } from "./helpers.js";

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the sales seat lands on the stock lookup, with two pills and no buying area", async ({ page }) => {
  await page.addInitScript(roleHandoff, INSIDE_SEAT);
  await page.goto("/");
  await expect(page.locator("[data-lookup-sales]")).toBeVisible();
  await expect(page.locator("h1", { hasText: "Stock lookup" })).toBeVisible();
  const pills = page.locator("nav.areas button");
  await expect(pills).toHaveCount(2);
  await expect(pills.filter({ hasText: "Stock lookup" })).toHaveCount(1);
  await expect(pills.filter({ hasText: "Incoming" })).toHaveCount(1);
  await expect(page.locator("nav.areas")).not.toContainText("Purchase orders");
  await expect(page.locator("nav.areas")).not.toContainText("Receiving");
  await expect(page.locator("nav.areas")).not.toContainText("Vendors");
});

test("a search lists the products, the chips sort them, and the availability line names the branch that is out", async ({ page }) => {
  await page.addInitScript(roleHandoff, INSIDE_SEAT);
  await page.goto("/");
  await page.locator("[data-lookup-text]").fill("GLS-");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  const rows = page.locator("[data-lookup-row]");
  await expect(rows).toHaveCount(4);
  await expect(page.locator("h3", { hasText: "4 products" })).toBeVisible();
  const paver = rows.filter({ hasText: "Copperline Paver 60mm" });
  await expect(paver.locator("[data-lookup-avail]")).toHaveText("340 available");
  const sand = rows.filter({ hasText: "Polymeric Sand" });
  await expect(sand.locator("[data-lookup-avail]")).toContainText("14 BG available");
  await expect(rows.filter({ hasText: "Decomposed Granite" }).locator("[data-lookup-avail]")).toHaveText("10 T available");
  // The chips: In stock keeps everything that has some, Low keeps what a branch is out of, All
  // brings the whole page back.
  await page.locator('[data-lookup-chips] [data-choice="in-stock"]').click();
  await expect(rows).toHaveCount(4);
  await page.locator('[data-lookup-chips] [data-choice="low"]').click();
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ hasText: "Copperline Paver 60mm" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "Heronfield Wall Cap" })).toHaveCount(0);
  await page.locator('[data-lookup-chips] [data-choice="all"]').click();
  await expect(rows).toHaveCount(4);
});

test("the picked product reads its stock at each branch and what is coming in, and no cell draws cost", async ({ page }) => {
  await page.addInitScript(roleHandoff, INSIDE_SEAT);
  await page.goto("/");
  await page.locator("[data-lookup-text]").fill("GLS-");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await page.locator("[data-lookup-row]").filter({ hasText: "Copperline Paver 60mm" }).click();
  const detail = page.locator("[data-lookup-detail]");
  await expect(detail.locator("h2")).toHaveText("Copperline Paver 60mm");
  await expect(detail).toContainText("GLS-PAV-60");
  await expect(detail).toContainText("Sold by the piece · $119.00 list");
  const branchRows = detail.locator("[data-branch-stock-row]");
  await expect(branchRows).toHaveCount(2);
  await expect(branchRows.filter({ hasText: "Point Loma yard" })).toContainText("340");
  await expect(branchRows.filter({ hasText: "Julian yard" })).toContainText("0");
  for (const header of ["On hand", "Reserved", "Available", "In transit"]) {
    await expect(detail.locator("th", { hasText: header })).toHaveCount(1);
  }
  const coming = detail.locator("[data-coming-row]");
  await expect(coming).toHaveCount(1);
  await expect(coming).toContainText("Point Loma yard");
  await expect(coming).toContainText("PO-PLM-0151 · Copperline Pavers (demo)");
  await expect(coming).toContainText("240");
  await expect(coming).toContainText("in 4 days");
  // The board draws no cost: not the branch average, not the order's figures, not the word.
  const words = (await detail.innerText()).toLowerCase();
  expect(words.includes("cost"), "the detail draws cost").toBe(false);
  expect(words.includes("$8.20") || words.includes("82.00"), "the detail draws the cost figure").toBe(false);
});

test("the sales seat's incoming is the arrivals, read only", async ({ page }) => {
  await page.addInitScript(roleHandoff, INSIDE_SEAT);
  await page.goto("/");
  await page.locator('nav.areas [data-area-id="incoming"]').click();
  await expect(page.locator("[data-sales-incoming]")).toBeVisible();
  const rows = page.locator("[data-sales-incoming-row]");
  await expect(rows).toHaveCount(3);
  await expect(rows.filter({ hasText: "PO-PLM-141" })).toContainText("in 2 days");
  await expect(page.locator("[data-sales-incoming]")).not.toContainText("Place order");
  await expect(page.locator("[data-sales-incoming]")).not.toContainText("Receive goods");
});

test("the buying writes are refused for the sales seat in the core's words", async ({ page, request }) => {
  await page.addInitScript(roleHandoff, INSIDE_SEAT);
  await page.goto("/");
  await expect(page.locator("[data-lookup-sales]")).toBeVisible();
  // The handoff record is cleared once adopted (the app's own rule), so the leg mints the seat's
  // token the way the demo session route does: the stub's wall reads the sub, as the core reads
  // the grants behind the mint.
  const part = (value: object): string => Buffer.from(JSON.stringify(value), "utf8").toString("base64url");
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: INSIDE_SEAT.userId })}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
  const headers = { Authorization: `Bearer ${token}`, "X-Org": "hh-demo", "Content-Type": "application/json" };
  const draft = await request.post("/api/v1/purchasing/pos", { data: { vendor_id: "", branch_id: "", lines: [] }, headers });
  expect(draft.status()).toBe(403);
  expect(await draft.json()).toEqual({ error: "Forbidden" });
  const receive = await request.post("/api/v1/purchasing/pos/not-an-id/receive", { data: { lines: [] }, headers });
  expect(receive.status()).toBe(403);
  expect(await receive.json()).toEqual({ error: "Forbidden" });
  // The reads the lookup lives on stay open.
  const browse = await request.get("/api/v1/inventory/browse?text=paver", { headers });
  expect(browse.status()).toBe(200);
});

test("the buying seats keep their areas, and the inventory lookup says Reserved", async ({ page, request }) => {
  await page.addInitScript(procurementHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  const pills = page.locator("nav.areas button");
  await expect(pills).toHaveCount(7);
  await expect(page.locator("nav.areas")).not.toContainText("Stock lookup");
  // Ruling R6, one vocabulary: the manager's lookup pane reads Reserved, as every other surface does.
  await openInventory(page, request, "lookup");
  await page.locator('[data-field="lookup-product"] option').nth(1).waitFor({ state: "attached" });
  await page.locator('[data-field="lookup-product"]').selectOption({ label: "GLS-PAV-60 Copperline Paver 60mm" });
  await page.getByRole("button", { name: "Look up" }).click();
  await expect(page.locator("[data-lookup] th", { hasText: "Reserved" })).toHaveCount(1);
  await expect(page.locator("[data-lookup] th", { hasText: "Allocated" })).toHaveCount(0);
});
