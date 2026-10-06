// The quote edit panel in a real browser, against the stubbed harness: the
// controls the server offers (and their absence for a caller it offers
// nothing), the quantity and discount saves with their field validation, the
// expiry, the add line search by mouse and keyboard, share links (mint, list,
// revoke), a line's price alternatives, the typed below floor refusal on both
// arms (the plain message here and the same qd-floor-refusal event the work
// area fires), and the convert control with the fulfilment choice. The stub's
// world is in memory: the file puts it back at both ends and runs in order.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** Open a row of the list by its text, then the detail disclosure that hosts the panel. */
async function openQuote(page: Page, text: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
}

const panel = (page: Page) => page.locator("qd-quote-edit");

async function captureRefusals(page: Page): Promise<void> {
  await page.evaluate(() => {
    const w = window as unknown as { __refusals: unknown[] };
    w.__refusals = [];
    window.addEventListener("qd-floor-refusal", (event) => w.__refusals.push((event as CustomEvent).detail));
  });
}

async function refusals(page: Page): Promise<Array<{ quoteId: string; lineNo: number; discPct: number }>> {
  return page.evaluate(() => (window as unknown as { __refusals: Array<{ quoteId: string; lineNo: number; discPct: number }> }).__refusals);
}

test.beforeAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the panel shows the quote's lines with the controls the server offers", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  await expect(page.getByTestId("qe-title")).toContainText("Draft");
  await expect(page.getByTestId("qe-sub")).toContainText("Maple Ridge Contracting");
  await expect(page.getByTestId("qe-sub")).toContainText("No expiry");
  await expect(panel(page).getByTestId("qe-line")).toHaveCount(2);
  // The offer: a quantity box, a discount box, Save, Remove and Compare per line.
  await expect(panel(page).locator('[data-field="qty"]')).toHaveCount(2);
  await expect(panel(page).locator('[data-field="disc"]')).toHaveCount(2);
  await expect(panel(page).getByRole("button", { name: "Remove" })).toHaveCount(2);
  await expect(panel(page).getByRole("button", { name: "Compare" })).toHaveCount(2);
  // The line below its floor at the rule price carries the server's mark.
  await expect(panel(page).getByTestId("qe-line").first()).toContainText("Rule price is below the floor");
  // The expiry field, the add form and the customer link are offered.
  await expect(panel(page).locator('[data-field="expiry"]')).toBeVisible();
  await expect(panel(page).getByText("Add a line", { exact: true })).toBeVisible();
  await expect(panel(page).getByRole("button", { name: "Create link to copy" })).toBeVisible();
  // The QD-5 seam rides the detail's panel stack beside this panel (the compose pattern),
  // mounted with both of its panels.
  const seam = page.locator('[data-seam="payment"]');
  await expect(seam).toBeAttached();
  await expect(seam.locator("qd-related-docs")).toBeAttached();
  await expect(seam.locator("qd-order-payment")).toBeAttached();
});

test("a notice or a refusal does not follow the person to another quote", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  const sand = panel(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="qty"]').fill("0");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-error")).toContainText("Quantity must be more than zero");
  await sand.locator('[data-field="qty"]').fill("3");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Saved.");
  await panel(page).locator('[data-field="add-search"]').fill("edge");
  await expect(panel(page).getByRole("option")).toHaveCount(1);
  await page.getByTestId("qb-list-row").filter({ hasText: "Accepted" }).first().click();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1003");
  await expect(page.getByTestId("qe-notice")).toHaveText("");
  await expect(page.getByTestId("qe-error")).toHaveText("");
  // The add box a person left half typed does not follow either.
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await expect(panel(page).locator('[data-field="add-search"]')).toHaveValue("");
  await expect(panel(page).getByRole("option")).toHaveCount(0);
});

test("a quantity save reprices from the server's answer, and bad input is refused before any request", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  const sand = panel(page).getByTestId("qe-line").nth(1);
  // Zero is refused in words and nothing is sent.
  await sand.locator('[data-field="qty"]').fill("0");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-error")).toContainText("Quantity must be more than zero");
  await sand.locator('[data-field="qty"]').fill("many");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-error")).toContainText("Quantity must be a number");
  // A real quantity saves; the line total is the server's figure.
  await sand.locator('[data-field="qty"]').fill("5");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Saved.");
  await expect(sand.locator('[data-field="qty"]')).toHaveValue("5");
  await expect(sand).toContainText("CA$57.40");
});

test("a discount within the floor saves; a blank clears it; a non-percent is refused", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  const sand = panel(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="disc"]').fill("2.5");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-error")).toContainText("whole percent from 0 to 100");
  await sand.locator('[data-field="disc"]').fill("5");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Saved.");
  await expect(sand.locator('[data-field="disc"]')).toHaveValue("5");
  await sand.locator('[data-field="disc"]').fill("");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Saved.");
  await expect(sand.locator('[data-field="disc"]')).toHaveValue("");
});

test("a discount below the floor shows the plain refusal and fires the dialog event; so does a quantity reprice", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  await captureRefusals(page);
  // The discount arm: the paver line already sits below its floor at the rule price.
  const paver = panel(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("10");
  await paver.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-error")).toContainText("puts the line below the margin floor");
  await expect(page.getByTestId("qe-error")).toContainText("General Manager");
  await expect(paver.locator('[data-field="disc"]')).toHaveValue("");
  const first = await refusals(page);
  expect(first).toHaveLength(1);
  expect(first[0]).toMatchObject({ lineNo: 1, discPct: 10 });
  // The dialog the event opens is a real modal now: close it before the next edit.
  await page.keyboard.press("Escape");

  // The quantity arm: a discount that was inside the floor, then the floor rises.
  const sand = panel(page).getByTestId("qe-line").nth(1);
  await sand.locator('[data-field="disc"]').fill("5");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Saved.");
  await page.request.put("/_stub/floor-raise", { data: { bps: 500 } });
  await sand.locator('[data-field="qty"]').fill("9");
  await sand.getByRole("button", { name: "Save" }).click();
  await expect(page.getByTestId("qe-error")).toContainText("puts the line below the margin floor");
  const second = await refusals(page);
  expect(second).toHaveLength(2);
  expect(second[1]).toMatchObject({ lineNo: 2, discPct: 5 });
  await page.keyboard.press("Escape");
  // The quantity did not change: the box returns to the server's truth.
  await expect(sand.locator('[data-field="qty"]')).toHaveValue("5");
  await page.request.put("/_stub/floor-raise", { data: { bps: 0 } });
});

test("the expiry saves and clears", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  const later = new Date(Date.now() + 40 * 86400000).toISOString().slice(0, 10);
  await panel(page).locator('[data-field="expiry"]').fill(later);
  await panel(page).getByRole("button", { name: "Save expiry" }).click();
  await expect(page.getByTestId("qe-sub")).toContainText(`Valid until ${later}`);
  await panel(page).locator('[data-field="expiry"]').fill("");
  await panel(page).getByRole("button", { name: "Save expiry" }).click();
  await expect(page.getByTestId("qe-sub")).toContainText("No expiry");
});

test("add a line: the catalogue search answers by mouse and by keyboard, and a pick needs a quantity", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  const search = panel(page).locator('[data-field="add-search"]');
  await search.fill("p");
  await expect(panel(page).getByRole("status").filter({ hasText: "Type at least 2 characters" })).toBeAttached();
  await search.fill("edge");
  await expect(panel(page).getByRole("option")).toHaveCount(1);
  // Enter with nothing highlighted only highlights the first row: it never picks.
  await search.press("Enter");
  await expect(panel(page).getByRole("option").first()).toHaveAttribute("aria-selected", "true");
  await expect(panel(page).getByRole("button", { name: "Add", exact: true })).toBeDisabled();
  await search.press("Enter");
  await expect(panel(page).getByRole("option")).toHaveCount(0);
  await expect(panel(page).getByText("EDG-08 · Edge Restraint 8 ft (pc)")).toBeVisible();
  // No quantity yet: refused in words.
  await panel(page).getByRole("button", { name: "Add", exact: true }).click();
  await expect(page.getByTestId("qe-error")).toContainText("Enter a quantity");
  await panel(page).locator('[data-field="add-qty"]').fill("12");
  await panel(page).getByRole("button", { name: "Add", exact: true }).click();
  await expect(panel(page).getByTestId("qe-line")).toHaveCount(3);
  await expect(panel(page).getByTestId("qe-line").nth(2)).toContainText("Edge Restraint 8 ft");
  // Escape closes the list without choosing.
  await search.fill("pave");
  await expect(panel(page).getByRole("option")).toHaveCount(1);
  await search.press("Escape");
  await expect(panel(page).getByRole("option")).toHaveCount(0);
});

test("a line can be removed", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  await panel(page).getByTestId("qe-line").nth(2).getByRole("button", { name: "Remove" }).click();
  await expect(panel(page).getByTestId("qe-line")).toHaveCount(2);
});

test("share links: mint once with the token shown once, list without it, revoke", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Draft");
  await panel(page).getByRole("button", { name: "Create link to copy" }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Nothing was emailed");
  const url = panel(page).locator('[data-field="link-url"]');
  await expect(url).toHaveValue(/\/quote\.html\?t=qd-demo-/);
  await expect(panel(page).getByText("it is not shown again")).toBeVisible();
  // Minting is the send: the quote reads Sent now.
  await expect(page.getByTestId("qe-title")).toContainText("Sent");
  await expect(panel(page).getByTestId("qe-link")).toHaveCount(1);
  await expect(panel(page).getByTestId("qe-link")).toContainText("Active");
  // A fresh load lists the link and never shows its token again.
  await page.reload();
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await openQuote(page, "Sent");
  await expect(panel(page).getByTestId("qe-link")).toHaveCount(1);
  await expect(panel(page).locator('[data-field="link-url"]')).toHaveCount(0);
  await panel(page).getByRole("button", { name: "Revoke" }).click();
  await expect(panel(page).getByTestId("qe-link")).toContainText("Revoked");
  await expect(panel(page).getByRole("button", { name: "Revoke" })).toHaveCount(0);
});

// Visual review round 1, P3: the link area. The created notice sits beside
// its button, the URL box has room for a link, and the Issued column says
// when, not a raw id.
test("the link area: the created notice beside its button, a roomy URL box, Issued as a date", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Sent");
  const create = panel(page).getByRole("button", { name: "Create link to copy" });
  await create.click();
  const notice = page.getByTestId("qe-notice");
  await expect(notice).toContainText("Nothing was emailed");
  const buttonBox = (await create.boundingBox())!;
  const noticeBox = (await notice.boundingBox())!;
  // The notice is inside the Customer link area, within a short reach of the button.
  expect(Math.abs(noticeBox.y - buttonBox.y)).toBeLessThan(80);
  const url = panel(page).locator('[data-field="link-url"]');
  const urlBox = (await url.boundingBox())!;
  expect(urlBox.width).toBeGreaterThanOrEqual(300);
  const issued = await panel(page).getByTestId("qe-link").first().locator("td").first().innerText();
  expect(issued).toMatch(/^\d{4}-\d{2}-\d{2}$/);
});

test("compare scores alternates read only: the current line, a priced alternate, an unavailable one", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Sent");
  await panel(page).getByTestId("qe-line").first().getByRole("button", { name: "Compare" }).click();
  await expect(page.getByTestId("qe-compare")).toBeVisible();
  await expect(page.getByTestId("qe-compare")).toContainText("Current line");
  const search = page.getByTestId("qe-compare").locator('[data-field="cmp-search"]');
  await search.fill("holland");
  await page.getByTestId("qe-compare").getByRole("option").first().click();
  await search.fill("edge");
  await page.getByTestId("qe-compare").getByRole("option").first().click();
  await page.getByTestId("qe-compare").getByRole("button", { name: "Score" }).click();
  await expect(page.getByTestId("qe-compare")).toContainText("HPV-60 · Holland Stone 60 mm");
  await expect(page.getByTestId("qe-compare")).toContainText("cannot be priced in this unit");
  await expect(page.getByTestId("qe-compare")).toContainText("Comparison only");
  await page.getByTestId("qe-compare").getByRole("button", { name: "Close" }).click();
  await expect(page.getByTestId("qe-compare")).toHaveCount(0);
});

test("an accepted quote offers convert and no edit; the fulfilment choice rides the conversion", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Accepted");
  await expect(page.getByTestId("qe-frozen")).toHaveText("This quote is accepted and can no longer be changed.");
  await expect(panel(page).locator('[data-field="qty"]')).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Remove" })).toHaveCount(0);
  await expect(panel(page).getByText("Add a line", { exact: true })).toHaveCount(0);
  await expect(page.getByTestId("qe-share-note")).toHaveText("This quote is accepted and cannot be shared.");
  await page.getByTestId("qe-fulfilment").selectOption("WILL_CALL");
  await page.getByTestId("qe-convert").click();
  await expect(page.getByTestId("qe-converted")).toContainText("Converted to order SO-");
  await expect(page.getByTestId("qe-convert")).toHaveCount(0);
  const state = await (await page.request.get("/_stub/state")).json();
  expect(state.conversions).toHaveLength(1);
  expect(state.conversions[0].fulfillment).toBe("WILL_CALL");
});

test("a caller the server offers nothing sees no edit control, only the reasons", async ({ page }) => {
  await signIn(page);
  // The manager's Sent quote is not in the Sales list; open it by id the way the list's open event does.
  await page.evaluate(() => {
    const root = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("qd-quote-builder")?.shadowRoot;
    root?.dispatchEvent(new CustomEvent("quote-open", { detail: { quoteId: "q-1002" } }));
  });
  await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qe-box")).toBeVisible();
  await expect(page.getByTestId("qe-title")).toContainText("Q-1002");
  await expect(page.getByTestId("qe-frozen")).toHaveText("No changes are offered to you on this quote. It belongs to Sales manager (demo).");
  await expect(panel(page).locator('[data-field="qty"]')).toHaveCount(0);
  await expect(panel(page).locator('[data-field="disc"]')).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Remove" })).toHaveCount(0);
  await expect(panel(page).locator('[data-field="expiry"]')).toHaveCount(0);
  await expect(panel(page).getByText("Add a line", { exact: true })).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Create link to copy" })).toHaveCount(0);
  await expect(page.getByTestId("qe-share-note")).toHaveText("Sharing is not offered to you on this quote.");
  await expect(panel(page).getByTestId("qe-convert")).toHaveCount(0);
  // Reading is still allowed: Compare stays, read only.
  await expect(panel(page).getByRole("button", { name: "Compare" })).toHaveCount(1);
});

test("the Sales manager edits any quote in its branch", async ({ page }) => {
  await signIn(page, "Sales manager");
  await page.getByTestId("qb-list-row").filter({ hasText: "Fenwick" }).first().click();
  await page.getByText("Quote detail & admin").click();
  await expect(panel(page).locator('[data-field="qty"]')).toHaveCount(1);
  await panel(page).locator('[data-field="qty"]').fill("11");
  await panel(page).getByRole("button", { name: "Save", exact: true }).click();
  await expect(page.getByTestId("qe-notice")).toContainText("Saved.");
  await expect(panel(page).locator('[data-field="qty"]')).toHaveValue("11");
  await expect(panel(page).getByTestId("qe-line")).toContainText("CA$514.80");
});

// Visual review round 1, P3: the compare table's Basis column words the pricing
// source as the ERP does, not the raw enum.
test("the compare table words its basis, not the raw enum", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Sent");
  await panel(page).getByTestId("qe-line").first().getByRole("button", { name: "Compare" }).click();
  const search = page.getByTestId("qe-compare").locator('[data-field="cmp-search"]');
  await search.fill("holland");
  await page.getByTestId("qe-compare").getByRole("option").first().click();
  await page.getByTestId("qe-compare").getByRole("button", { name: "Score" }).click();
  await expect(page.getByTestId("qe-compare")).toContainText("HPV-60 · Holland Stone 60 mm");
  const text = await page.getByTestId("qe-compare").innerText();
  expect(text).not.toMatch(/\b[A-Z]+_[A-Z_]+\b/);
});
