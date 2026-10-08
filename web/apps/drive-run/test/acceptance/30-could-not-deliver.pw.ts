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
  await page.getByLabel("What stopped this delivery?").fill("Nobody on site");
  await shot(page, "30-could-not-deliver", testInfo);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Not delivered");
  await expect(page.getByRole("status")).toContainText("Recorded. This stop is closed.");
  await expect(page.locator("[data-handback]")).toContainText("Bring the load back to the yard");
  await shot(page, "31-handback", testInfo);

  const record = await staff(page, "GET", `/api/v1/dispatch/stops/${target.id}`);
  expect(record.body.status).toBe("FAILED");
  expect(record.body.failure_reason).toBe("Nobody on site");
});
