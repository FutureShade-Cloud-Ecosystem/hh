// The feedback control on the till (Feature Polish, decision log row 958; round 7 moved the
// control into the bar): the bar carries a Feedback button that opens the shared panel, no pill
// floats over the till's own surface, and the open panel leaves the hotkey legend's dismiss
// control reachable. The no-overlap rule across layouts lives in r7-polish.pw.ts.
import { expect, test, type Page } from "@playwright/test";

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

test("the bar's Feedback button opens the panel and no pill floats over the till", async ({ page }) => {
  await signIn(page);
  await expect(page.getByTestId("feedback-pill")).toBeHidden();
  const button = page.getByTestId("feedback-button");
  await expect(button).toBeVisible();

  // It sits in the bar, above the till's own screen.
  const bar = (await page.locator(".surfacebar").boundingBox())!;
  const box = (await button.boundingBox())!;
  expect(box.y).toBeGreaterThanOrEqual(bar.y - 1);
  expect(box.y + box.height).toBeLessThanOrEqual(bar.y + bar.height + 1);

  await button.click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();
  await expect(page.getByTestId("feedback-kind-bug")).toHaveText("Something broke");
  await expect(page.getByTestId("feedback-note")).toBeVisible();
});

test("the open panel leaves the legend's dismiss control reachable", async ({ page }) => {
  await signIn(page);
  await page.getByTestId("feedback-button").click();
  await expect(page.getByTestId("feedback-panel")).toBeVisible();

  const legend = page.locator("hos-pos-hotkey-legend");
  await expect(legend).toBeVisible();
  const dismiss = legend.locator("button").first();
  await expect(dismiss).toBeVisible();
  const dismissBox = (await dismiss.boundingBox())!;
  const panelBox = (await page.getByTestId("feedback-panel").boundingBox())!;
  const overlaps =
    dismissBox.x < panelBox.x + panelBox.width &&
    dismissBox.x + dismissBox.width > panelBox.x &&
    dismissBox.y < panelBox.y + panelBox.height &&
    dismissBox.y + dismissBox.height > panelBox.y;
  expect(overlaps).toBe(false);
});

for (const size of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
]) {
  test(`the Feedback button is the topmost element at its corners at ${size.width}x${size.height}`, async ({ page }) => {
    await page.setViewportSize(size);
    await signIn(page);
    const button = page.getByTestId("feedback-button");
    await expect(button).toBeVisible();
    const bad = await button.evaluate((el) => {
      const deepHit = (x: number, y: number): Element | null => {
        let hit = document.elementFromPoint(x, y);
        while (hit?.shadowRoot) {
          const inner = hit.shadowRoot.elementFromPoint(x, y);
          if (inner === null || inner === hit) break;
          hit = inner;
        }
        return hit;
      };
      const box = el.getBoundingClientRect();
      const inset = 4;
      const points: Array<[string, number, number]> = [
        ["top-left", box.left + inset, box.top + inset],
        ["top-right", box.right - inset, box.top + inset],
        ["bottom-left", box.left + inset, box.bottom - inset],
        ["bottom-right", box.right - inset, box.bottom - inset],
        ["centre", box.left + box.width / 2, box.top + box.height / 2],
      ];
      const out: string[] = [];
      for (const [name, x, y] of points) {
        const hit = deepHit(x, y);
        if (hit === null || !(hit === el || el.contains(hit))) {
          out.push(`${name} at ${Math.round(x)},${Math.round(y)} is ${hit?.tagName ?? "nothing"}`);
        }
      }
      return out;
    });
    expect(bad).toEqual([]);
  });
}
