// The end of the run: every other stop of the seeded run is answered through
// the core (the screen's moves were proved in the two legs before), then the
// driver closes the run from the end screen, and the core's record agrees.
import { arriveFromFrontDoor, expect, myRuns, shot, staff, test } from "./helpers.js";

test("the run closes from the end screen once every stop has an answer", async ({ page }, testInfo) => {
  const run = (await myRuns(page))[0];
  // Any stop beyond the two the earlier legs answered is delivered through the core's own routes.
  for (const stop of run.stops.slice(2)) {
    for (const status of ["LOADED", "EN_ROUTE"]) {
      const moved = await staff(page, "POST", `/api/v1/dispatch/stops/${stop.id}/status`, { status, reason: "" });
      expect(moved.status).toBe(200);
    }
    const signed = await staff(page, "POST", `/api/v1/dispatch/stops/${stop.id}/pod`, { received_by: "Receiving clerk", affirmed: true });
    expect(signed.status).toBe(200);
  }
  await arriveFromFrontDoor(page);
  await page.getByRole("button", { name: "End of run" }).click();
  await expect(page.locator("[data-close-run]").first()).toContainText("could not be delivered");
  await shot(page, "40-end-of-run", testInfo);
  await page.getByRole("button", { name: "Complete run" }).click();
  await expect(page.getByRole("status")).toContainText(`${run.run_no} is closed.`);
  await shot(page, "41-closed", testInfo);

  const after = await staff(page, "GET", `/api/v1/dispatch/runs/${run.id}`);
  expect(after.status).toBe(200);
  expect(after.body.status).toBe("COMPLETED");
});
