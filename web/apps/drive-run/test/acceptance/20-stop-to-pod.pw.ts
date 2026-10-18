// A stop, loaded, en route and delivered with proof: the Driver's main job,
// on the demo image. The first stop of the seeded run goes to proof of
// delivery with a typed name and a photo, and the core's own record is read
// back to hold the screen to it.
import { arriveFromFrontDoor, expect, myRuns, shot, staff, test } from "./helpers.js";

const TINY_PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

test("a stop goes loaded, en route, and to proof of delivery with a name and a photo", async ({ page }, testInfo) => {
  const before = (await myRuns(page))[0];
  const target = before.stops[0];
  await arriveFromFrontDoor(page);
  await page.locator("[data-stop]").first().click();
  await expect(page.getByRole("heading", { name: `Stop ${target.stop_no}` })).toBeVisible();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Loaded");
  await page.getByRole("button", { name: "Mark en route" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("En route");
  await shot(page, "20-en-route", testInfo);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByLabel("Full name of the person receiving").fill("Receiving clerk");
  await page.locator("input[type=file]").setInputFiles({ name: "drop.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.locator("[data-photo-sent]")).toHaveCount(1);
  await page.getByRole("checkbox").check();
  await shot(page, "21-pod-ready", testInfo);
  await page.getByRole("button", { name: "Delivered" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
  await expect(page.locator(".signed")).toContainText("Signed by Receiving clerk");
  await shot(page, "22-delivered", testInfo);

  // The core's own record agrees with the screen. The per stop detail read is a dispatch-board
  // read the Driver is refused (the census gates it to that surface alone), so the record is the
  // run's own read, whose stops carry the same facts.
  const record = await staff(page, "GET", `/api/v1/dispatch/runs/${before.id}`);
  expect(record.status).toBe(200);
  const moved = (record.body.stops as Array<{ id: string; status: string; pod_received_by: string; photo_count: number }>)
    .find((s) => s.id === target.id);
  expect(moved?.status).toBe("DELIVERED");
  expect(moved?.pod_received_by).toBe("Receiving clerk");
  expect(moved?.photo_count).toBe(1);
});
