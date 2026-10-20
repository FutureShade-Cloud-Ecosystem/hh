// A stop that could not be delivered: the reason in the driver's words, the
// handback sentence on the screen, and the core's record of both.
import { arriveFromFrontDoor, expect, myRuns, shot, staff, test } from "./helpers.js";

test("a stop is marked not delivered with a reason, and the handback sentence reads", async ({ page }, testInfo) => {
  const run = (await myRuns(page))[0];
  const target = run.stops[1];
  await arriveFromFrontDoor(page);
  await page.locator("[data-stop]").nth(1).click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.getByRole("radio", { name: "Nobody on site" }).check();
  await shot(page, "30-could-not-deliver", testInfo);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Not delivered");
  await expect(page.getByRole("status")).toContainText("Recorded. This stop is closed.");
  await expect(page.locator("[data-handback]")).toContainText("Bring the load back to the yard");
  await shot(page, "31-handback", testInfo);

  // The core's own record agrees with the screen, through the run's read (the per stop detail
  // read is a dispatch-board read the Driver is refused).
  const record = await staff(page, "GET", `/api/v1/dispatch/runs/${run.id}`);
  expect(record.status).toBe(200);
  const moved = (record.body.stops as Array<{ id: string; status: string; failure_reason: string }>)
    .find((s) => s.id === target.id);
  expect(moved?.status).toBe("FAILED");
  expect(moved?.failure_reason).toBe("Nobody on site");
});
