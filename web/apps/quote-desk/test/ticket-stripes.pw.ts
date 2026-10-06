// Striped ticket rows (visual review round 1, P3): the ERP stripes its ticket
// lines; the desk drew them all alike.
import { expect, test } from "@playwright/test";

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

for (const width of [1440, 820]) {
  test(`every second ticket row is striped at ${width}`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1180 });
    await page.goto("/");
    await page.getByRole("button", { name: "Sales", exact: true }).click();
    await expect(page.getByTestId("qb-list")).toBeVisible();
    await newQuote(page);
    const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
    await picker.getByPlaceholder("Search name or account #…").fill("Maple");
    await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
    for (const sku of ["PSN-100", "WAT-STD"]) {
      const scan = page.getByTestId("pos-scan-input");
      await scan.fill(sku);
      await scan.press("Enter");
    }
    await expect(page.getByTestId("qb-line")).toHaveCount(2);
    const paint = (index: number) =>
      page
        .getByTestId("qb-line")
        .nth(index)
        .evaluate((row) => {
          const own = getComputedStyle(row).backgroundColor;
          const cell = getComputedStyle(row.querySelector("td")!).backgroundColor;
          return `${own}|${cell}`;
        });
    const first = await paint(0);
    const second = await paint(1);
    expect(second).not.toBe(first);
  });
}
