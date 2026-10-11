// L-card, L-branch and L-wall for the Yard Team (the role goes live
// checklist's common legs, ROLE-GOES-LIVE.md section 6). Written against
// PR 274's mint row (the yard-team flow, Trenton grant) and proved on the
// demo image; this machine cannot build the image.
import { expect, test } from "./helpers.js";
import { TRENTON, arriveFromFrontDoor, queue, shot, staff } from "./helpers.js";

test("L-card: the yard team signs in from its card and lands on the Trenton queue", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  await expect(page.locator("[data-branch]")).toHaveText(/Trenton/);
  await expect(page.getByRole("heading", { name: "Pick queue" })).toBeVisible();
  await expect(page.locator("[data-pick-card]").first()).toBeVisible();
  await shot(page, "world-trenton-queue", testInfo);
});

test("the card's own door offers the Yard Team's card and no other role's", async ({ page }) => {
  await page.goto("/yard/");
  const cards = page.locator("[data-flow]");
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toHaveText("Yard Team");
});

test("L-branch: the yard reads its own branch only", async ({ page }) => {
  const mine = await queue(page, TRENTON);
  expect(mine.status).toBe(200);
  // Kingston, the seed's other branch: out of the yard's reach.
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
  // The stock read the role holds at read level answers with no cost key at
  // all, byte absent rather than a zero that reads as free.
  const onHand = await staff(page, "GET", "/api/v1/inventory/on-hand?branch_id=" + TRENTON);
  expect(onHand.status).toBe(200);
  const seen = JSON.stringify(onHand.body).toLowerCase();
  expect(seen).not.toMatch(/avg_cost_cents|unit_cost_cents|suggested_value_cents/);
  const browse = await staff(page, "GET", "/api/v1/inventory/browse?branch_id=" + TRENTON);
  expect(browse.status).toBe(200);
  expect(JSON.stringify(browse.body).toLowerCase()).not.toMatch(/avg_cost_cents|unit_cost_cents|suggested_value_cents/);
});
