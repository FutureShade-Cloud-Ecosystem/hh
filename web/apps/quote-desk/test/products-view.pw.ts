// The Cards and Rows toggle beside Products (visual review round 1, P3): the
// ERP's products panel carries a two-way view control, and the shared grid
// already renders both presentations; the desk gave no way to reach Rows.
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

test("Cards and Rows beside Products flip the grid's presentation", async ({ page }) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await newQuote(page);
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  const cards = page.getByTestId("qb-view-cards");
  const rows = page.getByTestId("qb-view-rows");
  await expect(cards).toHaveAttribute("aria-pressed", "true");
  await expect(rows).toHaveAttribute("aria-pressed", "false");
  const presentation = () =>
    page.evaluate(() => {
      const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
      const grid = root?.querySelector("qd-quote-workarea")?.shadowRoot?.querySelector("hos-pos-product-grid") as
        | (HTMLElement & { presentation?: string })
        | null
        | undefined;
      return grid?.presentation ?? "";
    });
  expect(await presentation()).toBe("CARDS");
  await rows.click();
  await expect(rows).toHaveAttribute("aria-pressed", "true");
  await expect(cards).toHaveAttribute("aria-pressed", "false");
  await expect.poll(presentation).toBe("ROWS");
  await cards.click();
  await expect.poll(presentation).toBe("CARDS");
  // The toggle sits on the Products panel's header row, beside the title.
  const title = (await page.locator("section.panel.lines .panelbar h2").boundingBox())!;
  const flip = (await page.locator("section.panel.lines .viewflip").boundingBox())!;
  expect(Math.abs(flip.y - title.y)).toBeLessThan(20);
});
