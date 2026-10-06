// Round 7 (the visual review of the box): the page has no light frame around it, on the picker
// and on a role's home, and the Demo badge stays beside the brand lockup in one header row on
// Manager Home, where the longer role word and the two controls used to push it under the logo.
import { expect, test } from "@playwright/test";

const SIZES: Array<[number, number]> = [
  [1440, 900],
  [1920, 1080],
];

test.beforeEach(async ({ page }) => {
  const reset = await page.request.post("/_stub/reset");
  expect(reset.ok()).toBe(true);
});

for (const [width, height] of SIZES) {
  test(`no frame around the picker at ${width}x${height}: the page touches every edge`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await page.goto("/");
    const edges = await page.evaluate(() => {
      const header = document.querySelector("fd-app-root")?.shadowRoot?.querySelector("header");
      const box = header?.getBoundingClientRect();
      const root = document.querySelector("fd-app-root")?.getBoundingClientRect();
      return {
        margin: getComputedStyle(document.body).margin,
        headerLeft: box?.left ?? -1,
        headerTop: box?.top ?? -1,
        headerRight: box?.right ?? -1,
        rootBottom: root?.bottom ?? -1,
        innerWidth: window.innerWidth,
        innerHeight: window.innerHeight,
      };
    });
    expect(edges.margin).toBe("0px");
    expect(edges.headerLeft).toBe(0);
    expect(edges.headerTop).toBe(0);
    expect(edges.headerRight).toBe(edges.innerWidth);
    expect(edges.rootBottom).toBeGreaterThanOrEqual(edges.innerHeight);
  });

  test(`no frame around a role's home at ${width}x${height}, and the Demo badge stays beside the logo`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await page.goto("/");
    for (const role of ["Sales", "Sales manager"]) {
      await page.getByRole("button", { name: role, exact: true }).click();
      await expect(page.getByTestId("header-word")).toHaveText(role);
      const read = await page.evaluate(() => {
        const root = document.querySelector("fd-app-root")?.shadowRoot;
        const header = root?.querySelector("header")?.getBoundingClientRect();
        const brand = root?.querySelector(".brand")?.getBoundingClientRect();
        const demo = root?.querySelector(".demo-banner")?.getBoundingClientRect();
        return {
          margin: getComputedStyle(document.body).margin,
          headerLeft: header?.left ?? -1,
          headerTop: header?.top ?? -1,
          headerHeight: header?.height ?? -1,
          brandRight: brand?.right ?? -1,
          brandTop: brand?.top ?? -1,
          brandBottom: brand?.bottom ?? -1,
          demoLeft: demo?.left ?? -1,
          demoTop: demo?.top ?? -1,
          demoBottom: demo?.bottom ?? -1,
        };
      });
      expect(read.margin).toBe("0px");
      expect(read.headerLeft).toBe(0);
      expect(read.headerTop).toBe(0);
      // One row: the badge sits to the right of the lockup, vertically within its band, and the bar is one row tall.
      expect(read.demoLeft).toBeGreaterThanOrEqual(read.brandRight);
      expect(read.demoTop).toBeLessThan(read.brandBottom);
      expect(read.demoBottom).toBeGreaterThan(read.brandTop);
      expect(read.headerHeight).toBeLessThanOrEqual(72);
      await page.getByRole("button", { name: "Switch role" }).click();
    }
  });
}

for (const [width, height] of [
  [1024, 768],
  [900, 700],
  [390, 844],
] as Array<[number, number]>) {
  test(`the bar never pushes the page sideways at ${width}x${height} on Manager Home`, async ({ page }) => {
    await page.setViewportSize({ width, height });
    await page.goto("/");
    await page.getByRole("button", { name: "Sales manager", exact: true }).click();
    await expect(page.getByTestId("header-word")).toHaveText("Sales manager");
    const wide = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(wide).toBeLessThanOrEqual(0);
  });
}
