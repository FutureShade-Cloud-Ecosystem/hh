// The drawn category tiles, rendered (demo build): a seeded product with no picture shows its
// category's tile as the img in the hot list and in a search result, and the tile follows the
// document theme (Light is data-theme="light", Dark is the absence of it). The stub's products
// carry no image (has_image false), the same state as a demo product with no photograph. The
// default build's side of this (the glyph, no img) is test/default-build/glyph.pw.ts.
import { expect, test, type Page } from "@playwright/test";

async function signInToItems(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.evaluate((t) => {
    if (t === "light") document.documentElement.setAttribute("data-theme", "light");
    else document.documentElement.removeAttribute("data-theme");
  }, theme);
}

test("the hot list tiles show each group's category tile, in the document theme", async ({ page }) => {
  await signInToItems(page);
  const groups = page.getByTestId("hot-group");
  await expect(groups).toHaveCount(3);
  for (const theme of ["light", "dark"] as const) {
    await setTheme(page, theme);
    await expect(groups.filter({ hasText: "Pavers" }).locator("img")).toHaveAttribute("src", /\.svg$|^data:image\/svg/);
    await expect(groups.locator("img")).toHaveCount(3);
  }
  // A data URI carries the drawing itself: the pavers tile's own path text tells light from dark.
  await setTheme(page, "light");
  const light = await groups.filter({ hasText: "Pavers" }).locator("img").getAttribute("src");
  await setTheme(page, "dark");
  await expect(groups.filter({ hasText: "Pavers" }).locator("img")).not.toHaveAttribute("src", light ?? "");
});

test("a hot list item and a search result show their category tile and keep their alt text", async ({ page }) => {
  await signInToItems(page);
  await page.getByTestId("hot-group").filter({ hasText: "Pavers" }).click();
  await expect(page.getByTestId("hot-item").locator("img")).toHaveAttribute("src", /pavers|data:image\/svg/);

  await page.getByTestId("pos-scan-input").fill("holland");
  const results = page.getByTestId("drop-row");
  await expect(results.first()).toBeVisible();
  const thumb = results.first().locator("hos-product-thumb img");
  await expect(thumb).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);
  // The pavers tile's own drawing (its aria-label rides the data URI), not a request to the image route.
  expect(await thumb.getAttribute("src")).not.toContain("/api/v1/products/");
});

test("the cart line of an added product shows its category tile", async ({ page }) => {
  await signInToItems(page);
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  const line = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(line).toHaveCount(1);
  const img = line.locator("hos-product-thumb img");
  await expect(img).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);
  await expect(img).toHaveAttribute("alt", "Holland Stone 60 mm");
});
