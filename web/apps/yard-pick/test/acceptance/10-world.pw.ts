// L-card, L-branch and L-wall for the Yard Team (the role goes live
// checklist's common legs, ROLE-GOES-LIVE.md section 6). Written against
// PR 274's mint row (the yard-team flow, Point Loma grant) and proved on the
// demo image; this machine cannot build the image.
import { expect, test } from "./helpers.js";
import { POINT_LOMA, arriveFromFrontDoor, queue, shot, staff } from "./helpers.js";

test("L-card: the yard team signs in from its card and lands on the Point Loma queue", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  await expect(page.locator("[data-branch]")).toHaveText(/Point Loma/);
  await expect(page.getByRole("heading", { name: "Pick queue" })).toBeVisible();
  await expect(page.locator("[data-pick-card]").first()).toBeVisible();
  await shot(page, "world-point-loma-queue", testInfo);
});

test("the card's own door offers the Yard Team's card and no other role's", async ({ page }) => {
  await page.goto("/yard/");
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toHaveText("Yard Team");
});

test("L-branch: the yard reads its own branch only", async ({ page }) => {
  const mine = await queue(page, POINT_LOMA);
  expect(mine.status).toBe(200);
  // Julian, the seed's other branch: out of the yard's reach.
  const other = await queue(page, "5d1b3f6e-0000-4000-9000-000000000002");
  expect(other.status).toBe(403);
  expect(other.body.error).toContain("branch out of scope");
  // No branch named at all: a branch-scoped caller must name one.
  const none = await staff(page, "GET", "/api/v1/yard/pick-queue");
  expect(none.status).toBe(403);
  expect(none.body.error).toContain("a branch-scoped caller must name a reachable branch_id");
});

test("L-wall: a surface at None for the role is refused by the server", async ({ page }) => {
  // The counter till's sale door (the yard team's counter-till cell is None).
  const sale = await staff(page, "POST", "/api/v1/pos/sales", { lines: [] });
  expect(sale.status).toBe(403);
  expect(sale.body).toEqual({ error: "Forbidden" });
  // The Quote Desk's create door (the yard team's quote-desk cell is None).
  const quote = await staff(page, "POST", "/api/v1/quotes", {});
  expect(quote.status).toBe(403);
  expect(quote.body).toEqual({ error: "Forbidden" });
});

test("L-cost: cost is hidden for the role as the decided matrix says (PR 275)", async ({ page }) => {
  // The stock reads the role holds at read level answer with no cost key at
  // all, byte absent rather than a zero that reads as free. The browse read
  // is branch shaped; the on-hand read is product scoped (DIB-443 names
  // product_id), so the leg takes one product id off the browse answer and
  // reads that product's on-hand rows. The browse asks for stock only (in_stock_only=1), so the
  // product the leg takes holds stock somewhere; without it the first product may hold none.
  const browse = await staff(page, "GET", "/api/v1/inventory/browse?in_stock_only=1&branch_id=" + POINT_LOMA);
  expect(browse.status).toBe(200);
  expect(JSON.stringify(browse.body).toLowerCase()).not.toMatch(/avg_cost_cents|unit_cost_cents|suggested_value_cents/);
  const rows = (browse.body.rows ?? []) as Array<{ product_id?: string }>;
  const productId = rows.map((row) => row.product_id).find((id) => typeof id === "string" && id !== "");
  expect(productId, "the branch's browse answers at least one product").toBeTruthy();
  const onHand = await staff(page, "GET", `/api/v1/inventory/on-hand?product_id=${productId}`);
  expect(onHand.status).toBe(200);
  const onHandRows = (onHand.body.on_hand ?? []) as unknown[];
  expect(onHandRows.length, "on-hand answers at least one row for the browsed product").toBeGreaterThan(0);
  const seen = JSON.stringify(onHand.body).toLowerCase();
  expect(seen).not.toMatch(/avg_cost_cents|unit_cost_cents|suggested_value_cents/);
});
