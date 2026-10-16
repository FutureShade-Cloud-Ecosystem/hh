// The yard team's story, part three: the refusals. Starting a sale or a
// quote is refused (the role's counter-till and quote-desk cells are None,
// the role gate's shared Forbidden body), and committing a delivery date is
// refused the same way (the role's dispatch-board cell is read, the commit
// door is work level, so the shared gate answers before the dispatch
// capability is reached). Proved on the demo image; this machine cannot
// build it.
import { expect, test } from "./helpers.js";
import { POINT_LOMA, arriveFromFrontDoor, staff } from "./helpers.js";

test("starting a sale is refused, in the role gate's shared body", async ({ page }) => {
  await arriveFromFrontDoor(page);
  const sale = await staff(page, "POST", "/api/v1/pos/sales", { lines: [] });
  expect(sale.status).toBe(403);
  expect(sale.body).toEqual({ error: "Forbidden" });
});

test("starting a quote is refused, in the same shared body", async ({ page }) => {
  await arriveFromFrontDoor(page);
  const quote = await staff(page, "POST", "/api/v1/quotes", {});
  expect(quote.status).toBe(403);
  expect(quote.body).toEqual({ error: "Forbidden" });
});

test("committing a delivery date is refused, in the role gate's shared sentence", async ({ page }) => {
  await arriveFromFrontDoor(page);
  const commit = await staff(page, "POST", "/api/v1/dispatch/stops/00000000-0000-4000-9000-000000000001/commit", { scheduled_date: "2026-10-09" });
  expect(commit.status).toBe(403);
  // The decided matrix (ROLE-ACCESS-r3) holds the Yard Team's dispatch-board
  // cell at read, and the commit route is a work-level door, so the shared
  // gate refuses before the dispatch capability is reached, in the same
  // body the role's other refused doors carry (core/internal/platform/authz).
  expect(commit.body).toEqual({ error: "Forbidden" });
});
