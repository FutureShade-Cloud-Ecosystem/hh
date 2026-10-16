// The Logistics Manager's will call tab on the image: it opens quiet, an order number that names
// nothing is refused in words, and nothing is printed. Reads only: no code is minted on the shared image.
import { expect, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("the will call tab opens quiet and refuses an order that is not there in words", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  await page.locator("[data-tab-id='willcall']").first().click();
  await expect(page.locator("[data-willcall]")).toBeVisible();
  await expect(page.locator("[data-wc-idle]")).toContainText("Name an order to see its pickup codes");
  await page.locator("[data-willcall] [data-field='wc-order']").fill("ZZ-999999");
  await page.getByRole("button", { name: "Look up" }).click();
  await expect(page.locator("[data-wc-error]")).toContainText("No order matches that number at this yard");
  await expect(page.locator("[data-tag]")).toHaveCount(0);
  await shot(page, "50-willcall", testInfo);
});
