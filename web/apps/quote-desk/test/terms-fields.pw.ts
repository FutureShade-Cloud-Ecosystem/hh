// The summary panel's terms fields (Contact, Project / Job, Customer PO,
// Salesperson) show whole in the sent state: the demo polish round 2 shot
// set found them cut mid input inside the panel, the fields card riding the
// scroll zone's visible area while the pinned foot (the fulfilment tiles,
// the totals card, the actions) filled the panel to its fixed row height.
// The ERP reference shows the fields whole.
//
// Two bounds hold per field input, both against the design system's input
// height token read live off the document root (no arbitrary figure):
//   - the input's VISIBLE height (the part of its box inside the scroll
//     zone's visible area) carries its whole box: a cut input fails it;
//   - the visible height is at least the design system's input height
//     token: the field shows at the control face's own height.
// The pickup pick is the state the shot set shipped; the delivery pick adds
// the delivery row to the pinned foot and is the control state.
import { expect, test, type Page } from "@playwright/test";

test.use({ viewport: { width: 1440, height: 900 } });

const SALES = { name: "Sales", exact: true };

const TERM_FIELDS: Array<[string, string]> = [
  ["qb-contact", "Contact"],
  ["qb-project", "Project / Job"],
  ["qb-po", "Customer PO"],
  ["qb-salesperson", "Salesperson"],
];

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", SALES).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** The compose page under the split-list navigation: the New quote button walks the desk to #/quotes/new. */
async function newQuote(page: Page): Promise<void> {
  await page.getByRole("button", { name: "New quote", exact: true }).click();
  await expect(page.getByTestId("qb-identity")).toBeVisible();
}

/** A sent quote with one line, on the fulfilment pick the test names. */
async function sendWithPick(page: Page, pick: "pickup" | "delivery"): Promise<void> {
  await signIn(page);
  await newQuote(page);
  const pickerHost = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await pickerHost.getByPlaceholder("Search name or account #…").fill("Maple");
  await pickerHost.getByRole("option", { name: /Maple Ridge Contracting/ }).click();
  await expect(page.getByTestId("qb-fulfil-pickup")).toBeVisible();
  if (pick === "delivery") {
    await page.getByTestId("qb-fulfil-delivery").click();
    await expect(page.getByTestId("qb-fulfil-delivery")).toHaveAttribute("aria-pressed", "true");
  }
  const scanInput = page.getByRole("combobox", { name: "Search products by name, code or category" });
  await scanInput.fill("PSN-100");
  await scanInput.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
  await page.getByTestId("qb-send").click();
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
}

/**
 * The design system's input height token, resolved live off the root. The
 * caller runs the field bounds first and asserts the token's presence last,
 * so a tree without the token still reports the clip itself.
 */
async function inputHeightToken(page: Page): Promise<number> {
  return page.evaluate(() => {
    const raw = getComputedStyle(document.documentElement).getPropertyValue("--hh-h-input").trim();
    return parseFloat(raw) || 0;
  });
}

/** The token's presence leg: the bounds mean nothing without it. */
async function expectTokenResolves(page: Page, tokenPx: number): Promise<void> {
  expect(tokenPx, "the design system's input height token --hh-h-input resolves live on the document root").toBeGreaterThan(0);
}

/**
 * The field's whole input box shows inside the scroll zone's visible area,
 * and the visible height clears the input height token: nothing of the
 * field is cut mid input, and the field stands at the control face height.
 */
async function expectFieldWhole(page: Page, testId: string, label: string, tokenPx: number): Promise<void> {
  const field = page.getByTestId(testId);
  await expect(field).toBeVisible();
  const zoneBottom = await page
    .locator("qd-quote-workarea .summary .scrollzone")
    .evaluate((el) => el.getBoundingClientRect().bottom);
  const box = await field.boundingBox();
  expect(box).toBeTruthy();
  const visible = Math.max(0, Math.min(box!.y + box!.height, zoneBottom) - box!.y);
  expect(
    visible,
    `the ${label} input's whole box (${box!.height.toFixed(2)}px tall) shows above the scroll zone's edge: visible height ${visible.toFixed(2)}px`,
  ).toBeGreaterThanOrEqual(box!.height - 0.5);
  expect(
    visible,
    `the ${label} input's visible height (${visible.toFixed(2)}px) clears the design system's input height token (${tokenPx}px, live)`,
  ).toBeGreaterThanOrEqual(tokenPx);
}

test("sent-pickup-terms-fields-not-clipped", async ({ page }) => {
  await sendWithPick(page, "pickup");
  const tokenPx = await inputHeightToken(page);
  for (const [testId, label] of TERM_FIELDS) {
    await expectFieldWhole(page, testId, label, tokenPx);
  }
  await expectTokenResolves(page, tokenPx);
});

test("sent-delivery-terms-fields-not-clipped", async ({ page }) => {
  await sendWithPick(page, "delivery");
  const tokenPx = await inputHeightToken(page);
  for (const [testId, label] of TERM_FIELDS) {
    await expectFieldWhole(page, testId, label, tokenPx);
  }
  await expectTokenResolves(page, tokenPx);
});
