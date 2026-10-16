// The Logistics Manager's registry on the image: the trucks and drivers on screen are the core's own
// lists (in use and out of use), and a truck code that is already taken is refused in the core's words
// with the typing kept. Reads only plus one refused write: the world the walk found is the world it leaves.
import { expect, myBranch, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("the registry shows the trucks and drivers the core holds, out of use ones included", async ({ page }, testInfo) => {
  const branch = await myBranch(page);
  await arriveFromFrontDoor(page);
  await page.locator("[data-tab-id='fleet']").first().click();
  await expect(page.locator("[data-trucks]")).toBeVisible();
  const trucks = await staff(page, "GET", `/api/v1/dispatch/trucks?branch_id=${branch}&include_inactive=true`);
  const drivers = await staff(page, "GET", `/api/v1/dispatch/drivers?branch_id=${branch}&include_inactive=true`);
  expect(trucks.status).toBe(200);
  expect(drivers.status).toBe(200);
  await expect(page.locator("[data-trucks] li[data-truck-id]")).toHaveCount((trucks.body.items as unknown[]).length);
  await expect(page.locator("[data-drivers] li[data-driver-id]")).toHaveCount((drivers.body.items as unknown[]).length);
  await shot(page, "40-fleet", testInfo);
});

test("a truck code that is already taken is refused in the core's words and the typing stays", async ({ page }, testInfo) => {
  const branch = await myBranch(page);
  const trucks = await staff(page, "GET", `/api/v1/dispatch/trucks?branch_id=${branch}&include_inactive=true`);
  const taken = (trucks.body.items as Array<{ code: string }>)[0]?.code;
  expect(taken, "the seeded yard holds at least one truck").toBeTruthy();
  await arriveFromFrontDoor(page);
  await page.locator("[data-tab-id='fleet']").first().click();
  await page.locator("[data-trucks]").getByRole("button", { name: "Add truck" }).click();
  await page.locator("[data-sheet] [data-field='code']").fill(taken as string);
  await page.locator("[data-sheet] [data-field='name']").fill("Acceptance walk, not saved");
  await page.locator("[data-sheet]").getByRole("button", { name: "Add truck" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("a truck with that code already exists at this branch");
  await expect(page.locator("[data-sheet] [data-field='name']")).toHaveValue("Acceptance walk, not saved");
  await shot(page, "41-fleet-refused", testInfo);
});
