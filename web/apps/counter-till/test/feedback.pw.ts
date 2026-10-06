// The feedback pill on the till (Feature Polish, decision log row 958): the
// pill renders in the lane the ported terminal reserved for it, opens the
// panel, and covers no working control of the counter: the hotkey legend's
// dismiss control stays clear of the corner by its own design, and the pill
// keeps to the bottom-right lane. Written red first: on main no pill exists.
import { expect, test, type Page } from "@playwright/test";

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

test("the pill renders in the reserved lane and opens the panel", async ({ page }) => {
  await signIn(page);
  const pill = page.getByTestId("feedback-pill");
  await expect(pill).toBeVisible();

  // The lane: about 22px off the bottom, and the pill's own height is the
  // 38px the terminal's bottom padding reserves (--hos-fab-size).
  const box = await pill.boundingBox();
  const height = page.viewportSize()?.height ?? 720;
  expect(box).not.toBeNull();
  if (box !== null) {
    const inset = height - (box.y + box.height);
    expect(inset).toBeGreaterThanOrEqual(16);
    expect(inset).toBeLessThanOrEqual(30);
    expect(Math.round(box.height)).toBeGreaterThanOrEqual(36);
    expect(Math.round(box.height)).toBeLessThanOrEqual(40);
  }

  await pill.click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(page.getByTestId("feedback-kind-bug")).toHaveText("Something broke");
  await expect(page.getByTestId("feedback-note")).toBeVisible();
});

test("the pill covers no working control: the legend's dismiss stays clear", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("feedback-pill").click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();

  // The hotkey legend pins across the foot; its one control (dismiss) sits
  // beside the keycaps, never flushed right, so the pill cannot sit on it.
  // Held here as the rule the ported terminal's own comments state.
  const legend = page.locator("hos-pos-hotkey-legend");
  await expect(legend).toBeVisible();
  const dismiss = legend.locator("button").first();
  await expect(dismiss).toBeVisible();
  const dismissBox = await dismiss.boundingBox();
  const pillBox = await page.getByTestId("feedback-pill").boundingBox();
  expect(dismissBox).not.toBeNull();
  expect(pillBox).not.toBeNull();
  if (dismissBox !== null && pillBox !== null) {
    const overlaps =
      dismissBox.x < pillBox.x + pillBox.width &&
      dismissBox.x + dismissBox.width > pillBox.x &&
      dismissBox.y < pillBox.y + pillBox.height &&
      dismissBox.y + dismissBox.height > pillBox.y;
    expect(overlaps).toBe(false);
  }

  // The panel itself, open, leaves the legend's control reachable: the
  // dismiss control is not under the panel either.
  const panelBox = await page.getByTestId("feedback-panel").boundingBox();
  if (panelBox !== null && dismissBox !== null) {
    const panelOverlaps =
      dismissBox.x < panelBox.x + panelBox.width &&
      dismissBox.x + dismissBox.width > panelBox.x &&
      dismissBox.y < panelBox.y + panelBox.height &&
      dismissBox.y + dismissBox.height > panelBox.y;
    expect(panelOverlaps).toBe(false);
  }
});
