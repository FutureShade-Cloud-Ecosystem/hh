// The related documents panel in a real browser, against the stubbed
// harness: a converted quote's family (the quote, the order it became with
// its provenance, the invoice once a payment raises one), the empty family of
// a quote that became nothing, a refused read said in words and never shown
// as empty, and the two context guards (a family read for the quote left
// never lands on the one open now; an older read of the same quote never
// lands over a newer one).
import { expect, test, type Page } from "@playwright/test";
import { hold, settle, signIn } from "./switch-helpers.js";
import { shot } from "./shot";

const panel = (page: Page) => page.locator("qd-related-docs");

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
  await expect(panel(page).getByTestId("rd-box")).toBeVisible();
}

/** The family read of one quote, held back: whatever else the page does, this answer waits. */
async function holdFamily(
  page: Page,
  quoteId: string,
  options: { times?: number; refuse?: { status: number; error: string }; snapshot?: boolean } = {},
) {
  return hold(page, new RegExp(`/api/v1/quotes/${quoteId}/related-documents$`), { method: "GET", ...options });
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a converted quote's family shows itself, the order it became and the door that minted it", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-2101");
  const chips = panel(page).locator(".chip");
  await expect(chips).toHaveCount(2);
  await expect(chips.nth(0)).toContainText("Quote Q-2101 · Converted");
  await expect(chips.nth(0)).toContainText("(this document)");
  await expect(chips.nth(1)).toContainText("Order SO-2001 · Draft");
  await expect(chips.nth(1)).toContainText("created at the counter");
  // This desk opens no document from the family: the chips are labels, not dead buttons.
  await expect(panel(page).locator("button.chip")).toHaveCount(0);
  await shot(panel(page).getByTestId("rd-box"), "related-docs-converted");
});

test("a quote that became nothing says the empty sentence, which explains itself", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(panel(page).getByTestId("rd-empty")).toHaveText("No related documents yet: converting this quote to an order links them here.");
  await expect(panel(page).locator(".chip")).toHaveCount(0);
});

test("guard related documents read: a family read for the quote left never lands on the one open now", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-2101");
  const held = await holdFamily(page, "q-2101", { times: 1, snapshot: true });
  // The list read of the open quote is out; the person opens a quote whose family is one row.
  await held.asked;
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1002" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1002");
  await held.release();
  await settle(page);
  // Q-1002's own family is the SENT quote alone: no order chip ever lands on it, and the SENT
  // quote never grows the payment controls of the converted one.
  await expect(panel(page).getByTestId("rd-empty")).toBeVisible();
  await expect(panel(page).locator(".chip")).toHaveCount(0);
  await expect(panel(page).locator(".chip").filter({ hasText: "SO-2001" })).toHaveCount(0);
  await expect(page.locator("qd-order-payment").getByTestId("op-box")).toHaveCount(0);
});

test("guard related documents order: an older read of the quote answered late never lands over a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-2101");
  // The first family read is held and will answer as a refusal; a newer read of the same quote succeeds first.
  const held = await holdFamily(page, "q-2101", { times: 1, refuse: { status: 500, error: "internal" } });
  await held.asked;
  await page.locator("qd-related-docs").evaluate((el) =>
    el.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-2101" }, bubbles: true, composed: true })),
  );
  await expect(panel(page).locator(".chip").filter({ hasText: "SO-2001" })).toBeVisible();
  await held.release();
  await settle(page);
  await expect(panel(page).getByTestId("rd-error")).toHaveCount(0);
  await expect(panel(page).locator(".chip").filter({ hasText: "SO-2001" })).toBeVisible();
});

test("a refused read is said in words, never shown as an empty family", async ({ page }) => {
  await signIn(page);
  const refused = await holdFamily(page, "q-2101", { refuse: { status: 403, error: "quote: forbidden" } });
  await openQuote(page, "Q-2101");
  await refused.release();
  await expect(panel(page).getByTestId("rd-error")).toBeVisible();
  await expect(panel(page).getByTestId("rd-error")).toContainText("forbidden");
  await expect(panel(page).getByTestId("rd-empty")).toHaveCount(0);
  await shot(panel(page).getByTestId("rd-box"), "related-docs-refused");
});
