// The related documents panel in a real browser, against the stubbed
// harness: a converted quote's family (the quote, the order it became with
// its provenance, the invoice once a payment raises one), the empty family of
// a quote that became nothing, a refused read said in words and never shown
// as empty, and the two context guards (a family read for the quote left
// never lands on the one open now; an older read of the same quote never
// lands over a newer one).
import { expect, test, type Page } from "@playwright/test";
import { armedHold, hold, settle, signIn } from "./switch-helpers.js";
import { shot } from "./shot";

const panel = (page: Page) => page.locator("qd-related-docs");

/** The builder's own fan-out, dispatched from a panel inside it, so the detail panels re-read as they would after an edit. */
async function dispatchReload(page: Page): Promise<void> {
  await page.locator("qd-related-docs").evaluate((el) =>
    el.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: "q-2101" }, bubbles: true, composed: true })),
  );
}

async function openQuote(page: Page, quoteNo: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
  await expect(panel(page).getByTestId("rd-box")).toBeVisible();
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a converted quote's family shows itself, the order it became and the door that minted it", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
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
  await shot(page, "related-docs-viewport-mine");
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
  // The gate is armed before the read it holds is asked for: a reload of the open quote's panels.
  // Both panels' family reads are held (the document family's and the payment's), so whichever
  // lands late is covered and the catch does not depend on which one asked first.
  const gated = await armedHold(page, new RegExp("/api/v1/quotes/q-2101/related-documents$"), "GET", { times: 2, snapshot: true });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(2);
  // The family reads of the open quote are out; the person opens the cash customer's accepted quote.
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-2102" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-2102");
  await gated.held.release();
  await settle(page);
  // Q-2102's own family is itself alone: no order chip ever lands on it, and its figures stay its own.
  await expect(panel(page).getByTestId("rd-empty")).toBeVisible();
  await expect(panel(page).locator(".chip").filter({ hasText: "SO-2001" })).toHaveCount(0);
  await expect(page.locator("qd-order-payment").getByTestId("op-figures")).toContainText("Q-2102");
});

test("guard related documents order: an older read of the quote answered late never lands over a newer one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-2101");
  // The first family read is held and will answer as a refusal; a newer read of the same quote is asked for after it.
  const gated = await armedHold(page, new RegExp("/api/v1/quotes/q-2101/related-documents$"), "GET", {
    times: 2,
    refuse: { status: 500, error: "internal" },
  });
  gated.arm();
  await dispatchReload(page);
  await expect.poll(() => gated.held.seen.length).toBe(2);
  // A newer read of the same quote begins and succeeds while the older ones are still out.
  await dispatchReload(page);
  await expect(panel(page).locator(".chip").filter({ hasText: "SO-2001" })).toBeVisible();
  await gated.held.release();
  await settle(page);
  await expect(panel(page).getByTestId("rd-error")).toHaveCount(0);
  await expect(panel(page).locator(".chip").filter({ hasText: "SO-2001" })).toBeVisible();
  await expect(page.locator("qd-order-payment").getByTestId("op-unread")).toHaveCount(0);
});

test("a refused read is said in words, never shown as an empty family", async ({ page }) => {
  await signIn(page);
  const refused = await hold(page, new RegExp("/api/v1/quotes/q-2101/related-documents$"), {
    method: "GET",
    refuse: { status: 403, error: "quote: forbidden" },
  });
  await openQuote(page, "Q-2101");
  await refused.release();
  await expect(panel(page).getByTestId("rd-error")).toBeVisible();
  await expect(panel(page).getByTestId("rd-error")).toContainText("Forbidden");
  await expect(panel(page).getByTestId("rd-empty")).toHaveCount(0);
  await shot(panel(page).getByTestId("rd-box"), "related-docs-refused");
});
