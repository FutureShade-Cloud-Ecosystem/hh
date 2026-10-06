// The default build keeps today's picture for a product with no photograph: the stone-stack glyph,
// no img at all. The drawn category tiles are a demo seam for now (decision log row 893), so this
// runs against a default mode server (test/default-build.config.ts), not the demo one.
import { expect, test } from "@playwright/test";

test("a default build shows the glyph, not a tile, in the hot list and in a search result", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  const groups = page.getByTestId("hot-group");
  await expect(groups).toHaveCount(3);
  await expect(groups.locator("hos-product-thumb svg")).toHaveCount(3);
  await expect(groups.locator("hos-product-thumb img")).toHaveCount(0);

  await page.getByTestId("pos-scan-input").fill("holland");
  const results = page.getByTestId("drop-row");
  await expect(results.first()).toBeVisible();
  await expect(results.first().locator("hos-product-thumb svg")).toBeVisible();
  await expect(results.locator("hos-product-thumb img")).toHaveCount(0);
});
