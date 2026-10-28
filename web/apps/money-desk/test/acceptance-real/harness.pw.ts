// The acceptance-real harness spec (W2-4 (a)), red first. One spec, two
// configs: run under the app's default config (the desk against the Node
// stub) it must fail, the stub's world is not the core's; run against
// acceptance-real.config.ts it reads the real core through the real authz.
//
// THE SEED'S TRUE FIGURES, PINNED. Today's finance seed (core finance-seed,
// dibbits_seed.sql) holds a minimal fixture world: the GL's journals and
// the subledger disagree by construction (the W2-1b world ties by
// construction; this seed does not). The figures below were read from the
// real core on that seed and are pinned here, not recomputed at run time,
// so a stub world cannot satisfy them by accident.
//
// WORLD SEAM (fin2 W2-1b): when the world driver lands, the block below is
// the one place that changes: swap SEED_WORLD for the world's pinned
// figures (the world's month end 1100 equals its AR aging, and the bank
// month's assertion switches from the seed's honest empty state to the
// world's signed off month at zero difference). The exact assertions that
// switch are named in the bank month test.
const SEED_WORLD = {
  /** The whole fiscal year's trial balance, read from the real core: in balance at these totals. */
  trialInBalance: true,
  trialTotalDebitCents: 3_835_000,
  /** The 1100 Accounts Receivable movement columns on the trial balance, in cents ($17,000.00 by $16,300.00, a $700.00 balance). */
  gl1100DebitCents: 1_700_000,
  gl1100CreditCents: 1_630_000,
  /** The AR aging's open total, in cents ($10,870.00): 1,017,000 open invoice plus 70,000 of scrubbed party invoices. */
  arOpenTotalCents: 1_087_000,
  /** The seeded open invoice the receivables desk must show, and its open balance in cents ($10,170.00). */
  openInvoiceNo: "INV-2026-0002",
  openInvoiceCustomer: "Frontenac Home Builders",
  openInvoiceCents: 1_017_000,
  /** Today's seed holds no signed off bank month: the desk offers the starting point. */
  signedOffBankMonth: false,
} as const;

import { test, expect, type Page } from "@playwright/test";

const real = (): boolean => process.env.MDREAL_CONFIG === "1";

/**
 * Signs in as the Financial Controller. On the real box the desk at
 * /money-desk/ recovers its own session on load: the console double
 * answers the platform directory, the real exchange runs every gate and
 * mints the core's own staff token, and every later request carries it
 * into the real core's authz. On the stub run the desk's demo mint (the
 * default config serves the app against the stub) lands the controller on
 * the approvals queue.
 */
async function signInAsController(page: Page): Promise<void> {
  if (real()) {
    // "./" stays under this config's baseURL (/money-desk/ on the real box);
    // "/" would escape to the front door at the origin root.
    await page.goto("./");
  } else {
    await page.goto("/");
    await page.locator("[data-flow='financial-controller']").click();
  }
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals", { timeout: 30_000 });
}

test("the harness signs in as the Financial Controller through the real core's authz", async ({ page }) => {
  await signInAsController(page);
  // The role the exchange named, on the desk's chrome.
  await expect(page.getByText("Financial Controller").first()).toBeVisible();
  // The desk loaded through the staff token: the controller's whole area
  // switch and the approvals panel the seed honestly empties.
  await expect(page.getByRole("button", { name: "Bank reconciliation", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Receivables", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Approvals", exact: true })).toBeVisible();
});

test("the world's month end 1100 balance, read from the real core", async ({ page }) => {
  await signInAsController(page);
  await page.locator("[data-tab-id='trial']").click();
  await expect(page.locator("[data-trial]")).toBeVisible();
  if (SEED_WORLD.trialInBalance) {
    await expect(page.locator("[data-trial]")).toContainText("In balance");
  }
  const dollars = (cents: number): string => `$${(cents / 100).toLocaleString("en-CA", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
  await expect(page.locator("[data-total-debit]")).toHaveText(dollars(SEED_WORLD.trialTotalDebitCents));
  const row1100 = page.getByRole("row", { name: /1100 Accounts Receivable/ });
  await expect(row1100).toBeVisible();
  await expect(row1100).toContainText(dollars(SEED_WORLD.gl1100DebitCents));
  await expect(row1100).toContainText(dollars(SEED_WORLD.gl1100CreditCents));

  // The same world's receivables, through the same core, on the statements
  // side: the aging's open total and the seeded open invoice. (The
  // Receivables desk area itself crashes on the real core's empty bounced
  // list today, {"items": null} on /api/v1/ar/bounced; that bug is written
  // up in the W2-4a report, and the register assertions move there when it
  // is fixed.)
  await page.getByRole("button", { name: "Statements", exact: true }).click();
  await page.getByRole("button", { name: "Receivables aging", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Receivables aging", exact: true })).toBeVisible();
  await expect(page.locator("main")).toContainText(dollars(SEED_WORLD.arOpenTotalCents));
  const agingRow = page.getByRole("row", { name: new RegExp(SEED_WORLD.openInvoiceCustomer) });
  await expect(agingRow.first()).toBeVisible();
  await expect(agingRow.first()).toContainText(dollars(SEED_WORLD.openInvoiceCents));
});

test("a signed off bank month: what today's seed truly holds", async ({ page }) => {
  await signInAsController(page);
  await page.getByRole("button", { name: "Bank reconciliation", exact: true }).click();
  if (SEED_WORLD.signedOffBankMonth) {
    // WORLD SEAM (fin2 W2-1b): when the world lands this branch is the one
    // that runs. Assert the world's signed off month here: the
    // reconciliation of the month before the anchor, completed at zero
    // difference (the reconciliations list names it COMPLETED and the
    // desk's signed off sentence replaces the starting point form).
    throw new Error("the W2-1b world seam: assert the world's signed off bank month here");
  }
  // Today's seed: no month has signed off, so the desk offers the starting
  // point. When W2-1b lands, this assertion is replaced by the world's
  // signed off month above.
  await expect(page.locator("[data-cutover-form]")).toBeVisible();
});
