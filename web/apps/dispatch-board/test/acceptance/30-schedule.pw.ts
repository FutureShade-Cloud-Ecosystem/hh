// The Logistics Manager's schedule tab on the image: the day's books agree with the core's day board,
// the week strip agrees with its calendar, and an order number that names nothing is refused in words.
// Reads only, plus one refused write: the world the walk found is the world it leaves.
import { expect, myBranch, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("the schedule tab shows the day the core holds, and the week the core counts", async ({ page }, testInfo) => {
  const branch = await myBranch(page);
  await arriveFromFrontDoor(page);
  await page.locator("[data-tab-id='schedule']").first().click();
  await expect(page.locator("[data-schedule]")).toBeVisible();
  const date = await page.locator("input[data-field='date']").inputValue();
  const day = await staff(page, "GET", `/api/v1/dispatch/board?branch_id=${branch}&date=${date}`);
  expect(day.status).toBe(200);
  const expected = (day.body.unassigned as unknown[]).length + (day.body.stops as unknown[]).length;
  await expect(page.locator("[data-books] li[data-stop-id]")).toHaveCount(expected);
  const end = new Date(`${date}T12:00:00Z`);
  end.setUTCDate(end.getUTCDate() + 6);
  const week = await staff(page, "GET", `/api/v1/dispatch/loadboard/calendar?branch_id=${branch}&from=${date}&to=${end.toISOString().slice(0, 10)}`);
  expect(week.status).toBe(200);
  await expect(page.locator("[data-week] .day")).toHaveCount((week.body.items as unknown[]).length);
  await shot(page, "30-schedule", testInfo);
});

test("an order number that names nothing is refused in words and nothing is scheduled", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  await page.locator("[data-tab-id='schedule']").first().click();
  const before = await page.locator("[data-books] li[data-stop-id]").count();
  await page.locator("[data-schedule] [data-field='order']").fill("ZZ-999999");
  await page.getByRole("button", { name: "Schedule delivery" }).click();
  await expect(page.locator("[data-schedule-error]")).toContainText("No order matches that number at this yard");
  await expect(page.locator("[data-books] li[data-stop-id]")).toHaveCount(before);
  await shot(page, "31-schedule-refused", testInfo);
});
