// The Logistics Manager's scan trail on the image: a card's trail on screen is the trail the core serves
// for that order, and the manifest the core renders names the run. Reads only: no print is recorded on
// the shared image.
import { expect, myBranch, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("a card's scan trail agrees with the core's, and the core's manifest names its run", async ({ page }, testInfo) => {
  const branch = await myBranch(page);
  const board = await staff(page, "GET", `/api/v1/dispatch/loadboard?branch_id=${branch}`);
  expect(board.status).toBe(200);
  const date = board.body.date as string;
  const stop = ((board.body.loads as any[]).flatMap((r) => r.stops) as any[])[0] ?? (board.body.staging as any[])[0];
  expect(stop, "the seeded day holds a load to look at").toBeTruthy();
  const trail = await staff(page, "GET", `/api/v1/dispatch/scan-trail?order_id=${stop.order_id}`);
  expect(trail.status).toBe(200);
  await arriveFromFrontDoor(page);
  await expect(page.locator("input[data-field='date']")).toHaveValue(date);
  await page.locator("li.load", { hasText: stop.order_no }).getByRole("button", { name: "Scan trail" }).click();
  const loads = trail.body.loads as unknown[];
  await expect(page.locator("[data-sheet='scan-trail'] [data-trail-summary]")).toHaveText(
    loads.length === 0 ? "No scans yet" : trail.body.complete ? `${loads.length} ${loads.length === 1 ? "load" : "loads"}, all delivered` : /\d+ of \d+ loads? delivered/,
  );
  await expect(page.locator("[data-sheet='scan-trail'] [data-trail-load]")).toHaveCount(loads.length);
  await shot(page, "70-trail", testInfo);

  const run = (board.body.loads as any[])[0]?.run;
  if (run !== undefined) {
    const manifest = await staff(page, "GET", `/api/v1/dispatch/runs/${run.id}/manifest`);
    expect(manifest.status).toBe(200);
    expect(manifest.text).toContain(`Run manifest ${run.run_no}`);
  }
});
