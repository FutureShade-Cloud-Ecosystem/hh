// Shared helpers for the pricing panel's browser suites: sign in as a role card, open the
// pricing screen from the desk's tab strip, and move between its tabs.
import { expect, type Locator, type Page } from "@playwright/test";

export async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

export async function openPricing(page: Page): Promise<void> {
  await page.getByTestId("desk-tab-pricing").click();
  await expect(page.getByTestId("pricing-panel")).toBeVisible();
}

export async function openTab(page: Page, tab: "margin" | "rules" | "customers" | "breaks" | "audit"): Promise<void> {
  await page.getByTestId(`pricing-tab-${tab}`).click();
  await expect(page.getByTestId(`pricing-tab-${tab}`)).toHaveAttribute("aria-selected", "true");
}

export async function setMode(
  page: Page,
  body: { drop_policies?: string[]; mode?: string; audit_allowed?: boolean; shape?: string; fail_next?: { kind: string; status?: number; message?: string } | null; fail_confirm?: boolean; force_preview?: Record<string, unknown> | null; product_names?: boolean },
): Promise<void> {
  await page.request.put("/_stub/pricing-mode", { data: body });
}

export async function pricingState(page: Page): Promise<{
  rules: Array<Record<string, unknown>>;
  policies: Array<Record<string, unknown>>;
  overrides: Array<Record<string, unknown>>;
  breaks: Array<Record<string, unknown>>;
  audit: Array<Record<string, unknown>>;
  confirmations: Array<Record<string, unknown>>;
  previews: Array<Record<string, unknown>>;
  writes: Array<Record<string, unknown>>;
}> {
  const response = await page.request.get("/_stub/pricing-state");
  return response.json();
}

/** A select inside a locator, set by its visible value. */
export async function choose(select: Locator, value: string): Promise<void> {
  await select.selectOption(value);
}
