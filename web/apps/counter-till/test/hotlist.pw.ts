// The filled quick-pick and category chip state: the demo stub curates a
// three-tile hot list (grouped by the catalogue's category word) and the
// catalogue rows carry their category, so the tiles, the drill into a group,
// the add from a tile, the curated chip row and the Favorites chip are all
// exercised, not just the empty note. The stub's shapes are the contract's
// own (PosQuickPicks, the catalogue list's application word); no contract
// change rides this.
import { expect, test, type Page } from "@playwright/test";

async function signInToItems(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

test("the hot list shows its curated groups, drills into one and adds from a tile", async ({ page }) => {
  await signInToItems(page);
  const groups = page.getByTestId("hot-group");
  await expect(groups).toHaveCount(3);
  await expect(groups.filter({ hasText: "Pavers" })).toHaveCount(1);
  await expect(groups.filter({ hasText: "Sand and base" })).toHaveCount(1);
  await expect(groups.filter({ hasText: "Walls" })).toHaveCount(1);
  await groups.filter({ hasText: "Pavers" }).click();
  const tile = page.getByTestId("hot-item");
  await expect(tile).toHaveCount(1);
  await expect(tile).toContainText("Holland Stone 60 mm");
  await expect(page.getByTestId("hot-price")).toHaveText("CA$4.00");
  await tile.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" })).toHaveCount(1);
});

test("the browse grid carries the curated category chips and the Favorites chip", async ({ page }) => {
  await signInToItems(page);
  await page.getByText("Browse products").first().click();
  const favorites = page.getByTestId("pos-chip-favorites");
  await expect(favorites).toBeVisible();
  await expect(page.getByRole("button", { name: "Sand and base", exact: true })).toBeVisible();
  await expect(page.getByTestId("pos-chips-all")).toBeVisible();
  await favorites.click();
  await expect(favorites).toHaveAttribute("aria-pressed", "true");
  await page.getByRole("button", { name: "Walls", exact: true }).click();
  await expect(favorites).toHaveAttribute("aria-pressed", "false");
  await expect(page.getByText("Retaining Wall Stone, Standard").first()).toBeVisible();
  await expect(page.getByText("Clear Stone").first()).toHaveCount(0);
});
