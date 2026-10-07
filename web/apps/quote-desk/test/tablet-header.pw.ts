// The panel header at the tablet width (visual review round 1, P2-3): the
// manager's Screen settings button sat on the Customer & terms title. The
// checks read the boxes the browser laid out.
import { expect, test, type Page } from "@playwright/test";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function openTicket(page: Page, role: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  for (const [at, sku] of ["PSN-100", "WAT-STD"].entries()) {
    const scan = page.getByRole("combobox", { name: "Search products by name, code or category" });
    await scan.fill(sku);
    await scan.press("Enter");
    await expect(page.getByTestId("qb-line")).toHaveCount(at + 1);
  }
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
}

for (const width of [820, 1440]) {
  test(`the manager's panel header at ${width}: no child of the header overlaps another`, async ({ page }) => {
    await page.setViewportSize({ width, height: 1180 });
    await openTicket(page, "Sales manager");
    const panel = page.locator("section.panel.summary");
    await expect(panel.getByTestId("qb-settings-btn")).toBeVisible();
    const boxes = [];
    for (const target of [
      panel.locator(".panelbar h2"),
      panel.getByTestId("qb-settings-btn"),
      panel.getByRole("button", { name: "Reset layout" }),
      panel.getByRole("button", { name: "Collapse this panel" }),
    ]) {
      boxes.push((await target.boundingBox())!);
    }
    for (let a = 0; a < boxes.length; a += 1) {
      for (let b = a + 1; b < boxes.length; b += 1) {
        const x = Math.min(boxes[a].x + boxes[a].width, boxes[b].x + boxes[b].width) - Math.max(boxes[a].x, boxes[b].x);
        const y = Math.min(boxes[a].y + boxes[a].height, boxes[b].y + boxes[b].height) - Math.max(boxes[a].y, boxes[b].y);
        expect(x > 0 && y > 0, `header children ${a} and ${b} overlap`).toBe(false);
      }
    }
    // The title is still readable: it keeps a visible width.
    expect(boxes[0].width).toBeGreaterThan(60);
  });
}
