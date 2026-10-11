// The yard team's story, part three: the refusals. Starting a sale or a
// quote is refused (the role's counter-till and quote-desk cells are None,
// the role gate's shared Forbidden body), and committing a delivery date is
// refused (the dispatch capability's own sentence). Proved on the demo
// image; this machine cannot build it.
import { expect, test } from "./helpers.js";
import { TRENTON, arriveFromFrontDoor, staff } from "./helpers.js";

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

test("committing a delivery date is refused, in the dispatch capability's own sentence", async ({ page }) => {
  await arriveFromFrontDoor(page);
  const commit = await staff(page, "POST", "/api/v1/dispatch/stops/00000000-0000-4000-9000-000000000001/commit", { scheduled_date: "2026-10-09" });
  expect(commit.status).toBe(403);
  // The capability's own words (core/internal/dispatch/loadboard.go), the
  // module prefix stripped by the envelope.
  expect(commit.body.error).toContain("a salesperson can request a delivery date, only dispatch can commit one");
});
