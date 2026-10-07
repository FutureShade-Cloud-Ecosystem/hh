// Review P3-4 of round 7: the front door's Feedback is a button in its bar like the till's and the
// Quote Desk's, with no floating pill. It is there on the picker and on a role's home, sits inside
// the bar, and opens the shared panel.
import { expect, test } from "@playwright/test";

test.beforeEach(async ({ page }) => {
  const reset = await page.request.post("/_stub/reset");
  expect(reset.ok()).toBe(true);
});

for (const where of ["picker", "home"] as const) {
  test(`the bar carries a Feedback button on the ${where}, no pill floats, and it opens the panel`, async ({ page }) => {
    await page.goto("/");
    if (where === "home") {
      await page.getByRole("button", { name: "Sales", exact: true }).click();
      await expect(page.getByTestId("header-word")).toHaveText("Sales");
    }
    await expect(page.getByTestId("feedback-pill")).toBeHidden();
    const button = page.getByTestId("feedback-button");
    await expect(button).toBeVisible();
    const header = (await page.locator("fd-app-root header").boundingBox())!;
    const box = (await button.boundingBox())!;
    expect(box.y).toBeGreaterThanOrEqual(header.y - 1);
    expect(box.y + box.height).toBeLessThanOrEqual(header.y + header.height + 1);
    await button.click();
    await expect(page.getByTestId("feedback-panel")).toBeVisible();
  });
}

test("the button says whether the panel is open, and closing the panel gives focus back to it", async ({ page }) => {
  await page.goto("/");
  const button = page.getByTestId("feedback-button");
  await expect(button).toHaveAttribute("aria-expanded", "false");
  await button.click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(button).toHaveAttribute("aria-expanded", "true");
  // Escape, heard from inside the panel, closes it and the button takes focus back.
  await page.getByTestId("feedback-note").focus();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("feedback-panel")).toHaveCount(0);
  await expect(button).toHaveAttribute("aria-expanded", "false");
  await expect(button).toBeFocused();
  // The panel's own Close does the same.
  await button.click();
  await expect(button).toHaveAttribute("aria-expanded", "true");
  await page.getByRole("button", { name: "Close feedback" }).click();
  await expect(page.getByTestId("feedback-panel")).toHaveCount(0);
  await expect(button).toHaveAttribute("aria-expanded", "false");
  await expect(button).toBeFocused();
});
