// The tablet ticket (visual review round 1, P2-2): at 820 wide the ticket's
// money columns and the remove control stayed off-screen with no hint. The
// checks read the boxes the browser laid out.
import { expect, test, type Page } from "@playwright/test";

async function openTicket(page: Page, role: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill("Maple");
  await picker.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  for (const sku of ["PSN-100", "WAT-STD"]) {
    const scan = page.getByTestId("pos-scan-input");
    await scan.fill(sku);
    await scan.press("Enter");
  }
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
}

for (const role of ["Sales", "Sales manager"]) {
  for (const width of [820, 1440]) {
    test(`${role} at ${width}: the total and the remove control of every ticket line sit inside the panel`, async ({ page }) => {
      await page.setViewportSize({ width, height: 1180 });
      await openTicket(page, role);
      const panel = (await page.locator("section.panel.lines").boundingBox())!;
      const lines = page.getByTestId("qb-line");
      for (let index = 0; index < 2; index += 1) {
        const line = lines.nth(index);
        for (const part of [
          line.getByTestId("qb-line-total"),
          line.getByTestId("qb-line-trash"),
          line.getByTestId("qb-line-disc"),
        ]) {
          await expect(part).toBeVisible();
          const box = (await part.boundingBox())!;
          expect(box.x).toBeGreaterThanOrEqual(panel.x);
          expect(box.x + box.width).toBeLessThanOrEqual(panel.x + panel.width);
        }
      }
    });
  }
}
