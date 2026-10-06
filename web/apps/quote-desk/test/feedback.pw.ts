// The feedback pill on the Quote Desk (Feature Polish, decision log row 958):
// the shared widget's third mount, in the bottom-right lane, clear of the
// desk's chrome and its bottom-anchored panels. The desk's own stub answers
// the bridge's route in the contract's shape, so this spec never reaches a
// real board. Written red first: on the app branch without the mount no pill
// exists to find.
import { expect, test, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

test("the pill renders in the reserved lane, opens the panel, and stays off the chrome", async ({ page }) => {
  await signIn(page);

  const pill = page.getByTestId("feedback-pill");
  await expect(pill).toBeVisible();
  const box = await pill.boundingBox();
  const height = page.viewportSize()?.height ?? 720;
  expect(box).not.toBeNull();
  if (box !== null) {
    const inset = height - (box.y + box.height);
    expect(inset).toBeGreaterThanOrEqual(16);
    expect(inset).toBeLessThanOrEqual(30);
  }

  await pill.click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(page.getByTestId("feedback-kind-bug")).toHaveText("Something broke");
  await expect(page.getByTestId("feedback-kind-feedback")).toHaveText("Polish idea");
  await expect(page.getByTestId("feedback-kind-flag")).toHaveText("Flag for later");

  // The bar stays whole: the pill and the open panel sit under the chrome,
  // never over it.
  const bar = page.getByTestId("qd-bar");
  const barBox = (await bar.boundingBox())!;
  if (box !== null) {
    expect(box.y).toBeGreaterThanOrEqual(barBox.y + barBox.height);
  }
  const panelBox = (await page.getByTestId("feedback-panel").boundingBox())!;
  expect(panelBox.y).toBeGreaterThanOrEqual(barBox.y + barBox.height);
});
