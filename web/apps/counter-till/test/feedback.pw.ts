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

  // The lane: lifted 48px off the bottom (22px inset plus the 26px lift that
  // clears the 35px legend), and the pill's own height is the 38px the
  // terminal's bottom padding reserves (--hos-fab-size).
  const box = await pill.boundingBox();
  const height = page.viewportSize()?.height ?? 720;
  expect(box).not.toBeNull();
  if (box !== null) {
    const inset = height - (box.y + box.height);
    expect(inset).toBeGreaterThanOrEqual(44);
    expect(inset).toBeLessThanOrEqual(52);
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

for (const size of [
  { width: 1440, height: 900 },
  { width: 1024, height: 768 },
]) {
  test(`the pill is the topmost element at its corners at ${size.width}x${size.height}, legend shown`, async ({ page }) => {
    await page.setViewportSize(size);
    await signIn(page);
    await expect(page.locator("hos-pos-hotkey-legend")).toBeVisible();
    const pill = page.getByTestId("feedback-pill");
    await expect(pill).toBeVisible();
    // The deep hit walks every shadow root the way a pointer does.
    const bad = await pill.evaluate((button) => {
      const deepHit = (x: number, y: number): Element | null => {
        let hit = document.elementFromPoint(x, y);
        while (hit?.shadowRoot) {
          const inner = hit.shadowRoot.elementFromPoint(x, y);
          if (inner === null || inner === hit) break;
          hit = inner;
        }
        return hit;
      };
      const box = button.getBoundingClientRect();
      // The corner probes sit 8px in, inside the pill's rounding.
      const inset = 8;
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
        if (hit === null || !(hit === button || button.contains(hit))) {
          out.push(`${name} at ${Math.round(x)},${Math.round(y)} is ${hit?.tagName ?? "nothing"}`);
        }
      }
      // A pinned bar can paint over the pill while passing every pointer
      // through (the legend is pointer-events: none), so the hit test alone
      // cannot see it: no other fixed element's box may meet the pill's box.
      const fixed: Element[] = [];
      const walk = (root: Document | ShadowRoot): void => {
        for (const el of Array.from(root.querySelectorAll("*"))) {
          if (getComputedStyle(el).position === "fixed") fixed.push(el);
          if (el.shadowRoot !== null) walk(el.shadowRoot);
        }
      };
      walk(document);
      for (const el of fixed) {
        if (el === button || el.contains(button) || button.contains(el)) continue;
        if (el.closest("hh-feedback") !== null) continue;
        const other = el.getBoundingClientRect();
        if (other.width === 0 || other.height === 0) continue;
        const meets =
          other.left < box.right && other.right > box.left && other.top < box.bottom && other.bottom > box.top;
        if (meets) out.push(`a fixed ${el.tagName.toLowerCase()} (${Math.round(other.height)}px tall) meets the pill's box`);
      }
      return out;
    });
    expect(bad).toEqual([]);
  });
}
