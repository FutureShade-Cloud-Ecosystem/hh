// The settings validation message (visual review round 1, P2-4): the refusal
// sentence appeared at the foot of the whole Screen settings block, far from
// Save policy, with nothing marking the field. It now sits beside Save policy
// and the field carries aria-invalid and a red border.
import { expect, test, type Page } from "@playwright/test";
import { showList } from "./switch-helpers.js";

async function openScreenSettings(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await showList(page);
  await row(page, "Q-1001").click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
  await page.getByText("Quote detail & admin").click();
  await page.locator("qd-quote-settings").getByTestId("qs-screen").locator("summary").click();
  await expect(page.getByTestId("qs-policy")).toBeVisible();
}

test.beforeAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a refused validity shows its sentence beside Save policy and marks the field", async ({ page }) => {
  await openScreenSettings(page);
  const days = page.getByTestId("qs-validity-days");
  await expect(days).not.toHaveAttribute("aria-invalid", "true");
  const normalBorder = await days.evaluate((el) => getComputedStyle(el).borderTopColor);
  await days.fill("0");
  await page.getByTestId("qs-policy-save").click();
  const message = page.getByTestId("qs-error");
  await expect(message).toContainText("Default validity must be a whole number of days from 1 to 365");
  // Next to the button: inside the policy box, on its row.
  await expect(page.getByTestId("qs-policy").getByTestId("qs-error")).toHaveCount(1);
  const save = (await page.getByTestId("qs-policy-save").boundingBox())!;
  const said = (await message.boundingBox())!;
  expect(Math.abs(said.y - save.y)).toBeLessThan(60);
  // The field is marked: aria-invalid and a border that is no longer the normal one.
  await expect(days).toHaveAttribute("aria-invalid", "true");
  expect(await days.evaluate((el) => getComputedStyle(el).borderTopColor)).not.toBe(normalBorder);
  // The other fields stay unmarked, and typing a value clears the mark and the sentence.
  await expect(page.getByTestId("qs-cost-visibility")).not.toHaveAttribute("aria-invalid", "true");
  await days.fill("45");
  await expect(days).not.toHaveAttribute("aria-invalid", "true");
  await expect(page.getByTestId("qs-error")).toHaveCount(0);
});
