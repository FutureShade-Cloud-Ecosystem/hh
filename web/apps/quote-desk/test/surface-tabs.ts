// Shared by the legs that reach a quote's surfaces or the desk surfaces: the walk is the tab or the route, never a
// disclosure (the "Quote detail & admin" disclosure is gone, decision log row 955).
import { expect, type Page } from "@playwright/test";

/** Open one of the open quote's tabs by its strip. */
export async function openTab(page: Page, tab: "build" | "activity" | "stock" | "admin"): Promise<void> {
  const strip = page.getByTestId(`qt-tab-${tab}`);
  await strip.click();
  await expect(strip).toHaveAttribute("aria-current", "page");
}

/** Open a desk surface by its tab in the navigation bar (the Sales manager's tabs). */
export async function openDesk(page: Page, tab: "quotes" | "approvals" | "pricing" | "settings"): Promise<void> {
  const link = page.getByTestId(`desk-tab-${tab}`);
  await link.click();
  await expect(link).toHaveAttribute("aria-current", "page");
}
