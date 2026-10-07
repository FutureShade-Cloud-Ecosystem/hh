// The default build shows a product's category tile where the product has no photograph (decision
// log row 1057: every dealer's system, not the demo only). A product whose category the tiles do not
// know keeps the stone-stack glyph. This runs against a default mode server
// (test/default-build.config.ts), not the demo one, so it proves the tile needs no demo flag.
import { expect, test } from "@playwright/test";

test("a default build shows the category tile, not the glyph, in the hot list and in a search result", async ({
  page,
}) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  const groups = page.getByTestId("hot-group");
  await expect(groups).toHaveCount(3);
  await expect(groups.locator("hos-product-thumb img")).toHaveCount(3);
  await expect(groups.locator("hos-product-thumb svg")).toHaveCount(0);
  await expect(groups.filter({ hasText: "Pavers" }).locator("img")).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);

  await page.getByTestId("pos-scan-input").fill("holland");
  const results = page.getByTestId("drop-row");
  await expect(results.first()).toBeVisible();
  await expect(results.first().locator("hos-product-thumb img")).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);
  await expect(results.locator("hos-product-thumb svg")).toHaveCount(0);
});

test("a default build keeps the glyph for a product whose category the tiles do not know", async ({ page }) => {
  await page.route("**/api/v1/pos/quick-picks**", async (route) => {
    const real = await route.fetch();
    const body = (await real.json()) as { items?: Array<Record<string, unknown>> };
    for (const row of body.items ?? []) {
      row["category"] = "Gift cards";
      row["subcategory"] = "Gift cards";
    }
    await route.fulfill({ response: real, json: body });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  const groups = page.getByTestId("hot-group");
  await expect(groups.first()).toBeVisible();
  await expect(groups.locator("hos-product-thumb svg")).not.toHaveCount(0);
  await expect(groups.locator("hos-product-thumb img")).toHaveCount(0);
});
