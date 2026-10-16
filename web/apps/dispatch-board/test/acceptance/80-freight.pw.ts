// Split freight on the image, read only: a load card offers the sheet and it opens. Nothing is submitted,
// so no raise is parked and no load is split on the shared image.
import { expect, myBranch, staff, test, arriveFromFrontDoor } from "./helpers.js";

test("a load card offers the split sheet and it opens, nothing is sent", async ({ page }) => {
  const branch = await myBranch(page);
  const board = await staff(page, "GET", `/api/v1/dispatch/loadboard?branch_id=${branch}`);
  expect(board.status).toBe(200);
  const stop = ((board.body.loads as any[]).flatMap((r) => r.stops) as any[])[0] ?? (board.body.staging as any[])[0];
  expect(stop, "the seeded day holds a load to look at").toBeTruthy();
  await arriveFromFrontDoor(page);
  await page.locator("li.load", { hasText: stop.order_no }).first().getByRole("button", { name: "Split across trucks" }).click();
  await expect(page.locator("[data-sheet='split-freight']")).toBeVisible();
});
