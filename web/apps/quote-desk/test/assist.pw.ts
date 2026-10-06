// The assist panel in a real browser, against the stubbed harness: a pasted
// description read into proposals that arrive unticked, a confirm that adds
// only the ticked and completable rows through the audited add line door, a
// row that cannot be confirmed until it is completed, the Not this removal,
// the could not match list, the scope suggestions for q-1001 that shrink once
// a base line lands, the AI helper latching disabled on the server's own
// deterministic answer (and reading ai-assisted when a key is configured),
// and a refused request in words under the one role=alert. The screenshots
// are headless and prefixed assist-.
import { expect, test, type Page } from "@playwright/test";
import { openTab } from "./surface-tabs";
import { shot } from "./shot";

const TEXT = "3 yd clear stone, 20 sf holland stone, jointing sand, a bobcat for the day";

async function openDraft(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByTestId("qb-list-row").filter({ hasText: "Q-1001" }).first().click();
  await openTab(page, "activity");
  await expect(page.locator("qd-quote-assist").getByTestId("qa-extract")).toBeVisible();
}

const assist = (page: Page) => page.locator("qd-quote-assist");
const proposals = (page: Page) => assist(page).getByTestId("qa-proposal");
const rowOf = (page: Page, name: string) => proposals(page).filter({ has: page.locator(".pname", { hasText: name }) });

async function paste(page: Page, text: string): Promise<void> {
  await assist(page).locator("#jobtext").fill(text);
  await assist(page).getByRole("button", { name: "Suggest lines from this text" }).click();
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the panel mounts under the detail disclosure with the quote's suggestions, and no alert", async ({ page }) => {
  await openDraft(page);
  await expect(assist(page).getByRole("heading", { name: "Read a job description" })).toBeVisible();
  await expect(assist(page).getByRole("heading", { name: "Suggestions for this quote" })).toBeVisible();
  // Polymeric sand is on q-1001, so the base, bedding and edging advice shows and jointing does not.
  await expect(assist(page).getByTestId("qa-finding")).toHaveCount(3);
  await expect(assist(page).getByTestId("qa-gaps")).toContainText("Granular base for the paved area");
  await expect(assist(page).getByTestId("qa-gaps")).not.toContainText("Jointing sand for the paved area");
  await expect(assist(page).locator('[role="alert"]')).toHaveCount(0);
  // The helper starts available and unticked.
  await expect(assist(page).getByTestId("qa-ai")).toBeEnabled();
  await expect(assist(page).getByTestId("qa-ai")).not.toBeChecked();
});

test("proposals arrive unticked and the could not match list shows", async ({ page }) => {
  await openDraft(page);
  await paste(page, TEXT);
  await expect(proposals(page)).toHaveCount(3);
  for (let index = 0; index < 3; index += 1) {
    await expect(proposals(page).nth(index).locator('input[type="checkbox"]')).not.toBeChecked();
  }
  await expect(assist(page).getByText("0 of 3 ticked")).toBeVisible();
  await expect(assist(page).getByTestId("qa-confirm")).toBeDisabled();
  await expect(assist(page).getByTestId("qa-unmatched")).toContainText("a bobcat for the day");
  await expect(assist(page).getByTestId("qa-source")).toHaveText("Read by: deterministic");
  await expect(assist(page).locator('[role="status"]').first()).toBeAttached();
});

test("ticking and confirming adds only the ticked lines, and the suggestions shrink", async ({ page }) => {
  await openDraft(page);
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
  await paste(page, TEXT);
  await rowOf(page, "Clear Stone").locator('input[type="checkbox"]').check();
  await expect(assist(page).getByTestId("qa-confirm")).toHaveText("Add 1 line to the quote");
  await assist(page).getByTestId("qa-confirm").click();
  await expect(assist(page).getByTestId("qa-add-note")).toHaveText("Added 1 line.");
  // The work area re-read through quote-changed: exactly one new line, the clear stone.
  await expect(page.getByTestId("qb-line")).toHaveCount(3);
  await expect(page.getByTestId("qb-line").nth(2)).toContainText("Clear Stone");
  // The untouched proposals are still waiting, unticked.
  await expect(proposals(page)).toHaveCount(2);
  await expect(rowOf(page, "Holland Stone").locator('input[type="checkbox"]')).not.toBeChecked();
  // A base line now stands, so the base suggestion is gone and the others remain.
  await expect(assist(page).getByTestId("qa-finding")).toHaveCount(2);
  await expect(assist(page).getByTestId("qa-gaps")).not.toContainText("Granular base for the paved area");
});

test("a row needing a quantity or a unit cannot be confirmed until it is completed", async ({ page }) => {
  await openDraft(page);
  await paste(page, TEXT);
  const sand = rowOf(page, "Polymeric Locking Sand");
  await expect(sand).toHaveAttribute("data-confirmable", "false");
  await expect(sand.locator('input[type="checkbox"]')).toBeDisabled();
  await expect(sand).toContainText("Add a quantity and pick a unit before you add this line.");
  await sand.locator('[data-field="qty"]').fill("4");
  await expect(sand).toContainText("Pick a unit before you add this line.");
  await expect(sand.locator('input[type="checkbox"]')).toBeDisabled();
  await sand.locator('[data-field="uom"]').fill("bag");
  await expect(sand).toHaveAttribute("data-confirmable", "true");
  await sand.locator('input[type="checkbox"]').check();
  await expect(assist(page).getByTestId("qa-confirm")).toHaveText("Add 1 line to the quote");
  // Clearing the quantity drops it out of the confirm set again.
  await sand.locator('[data-field="qty"]').fill("");
  await expect(assist(page).getByTestId("qa-confirm")).toBeDisabled();
  await expect(assist(page).getByTestId("qa-confirm")).toHaveText("Add the ticked lines");
});

test("Not this removes a proposal", async ({ page }) => {
  await openDraft(page);
  await paste(page, TEXT);
  await expect(proposals(page)).toHaveCount(3);
  await rowOf(page, "Holland Stone").getByRole("button", { name: "Not this" }).click();
  await expect(proposals(page)).toHaveCount(2);
  await expect(assist(page).getByText("0 of 2 ticked")).toBeVisible();
  await expect(rowOf(page, "Holland Stone")).toHaveCount(0);
});

test("a gap can be dismissed and brought back, and Find reads its query into the review list", async ({ page }) => {
  await openDraft(page);
  const base = assist(page).getByTestId("qa-finding").filter({ hasText: "Granular base" });
  await base.getByRole("button", { name: "Dismiss the suggestion: Granular base for the paved area" }).click();
  await expect(assist(page).getByTestId("qa-finding")).toHaveCount(2);
  // Find puts the rule's own query in the paste box and reads it, adding nothing.
  const edging = assist(page).getByTestId("qa-finding").filter({ hasText: "Edge restraint" });
  await edging.getByRole("button", { name: /^Find/ }).click();
  await expect(assist(page).locator("#jobtext")).toHaveValue("edging");
  await expect(proposals(page)).toHaveCount(1);
  await expect(proposals(page).first()).toContainText("Edge Restraint 8 ft");
  await expect(proposals(page).first().locator('input[type="checkbox"]')).not.toBeChecked();
  await expect(page.getByTestId("qb-line")).toHaveCount(2);
});

test("with no key configured the AI helper latches disabled, grey, with the server's note", async ({ page }) => {
  await openDraft(page);
  const helper = assist(page).getByTestId("qa-ai");
  const label = assist(page).locator("label.switch");
  const enabledInk = await label.evaluate((el) => getComputedStyle(el).color);
  await helper.check();
  await paste(page, TEXT);
  await expect(proposals(page)).toHaveCount(3);
  await expect(assist(page).getByTestId("qa-ai-note")).toHaveText("AI-assisted reading isn't switched on here, so I used the plain parser.");
  await expect(helper).toBeDisabled();
  await expect(helper).not.toBeChecked();
  const latchedInk = await label.evaluate((el) => getComputedStyle(el).color);
  expect(latchedInk).not.toBe(enabledInk);
  expect(await label.evaluate((el) => getComputedStyle(el).opacity)).toBe("1");
  await helper.click({ force: true });
  await expect(helper).not.toBeChecked();
  // The plain parser's proposals are the real answer and still arrive unticked.
  await expect(assist(page).getByTestId("qa-source")).toHaveText("Read by: deterministic");
  for (let index = 0; index < 3; index += 1) {
    await expect(proposals(page).nth(index).locator('input[type="checkbox"]')).not.toBeChecked();
  }
});

test("with a key configured the source reads ai-assisted and the helper stays available", async ({ page, request }) => {
  const set = await request.put("/_stub/ai-key", { data: { configured: true } });
  expect(set.ok()).toBe(true);
  await openDraft(page);
  await assist(page).getByTestId("qa-ai").check();
  await paste(page, TEXT);
  await expect(proposals(page)).toHaveCount(3);
  await expect(assist(page).getByTestId("qa-source")).toHaveText("Read by: ai-assisted");
  await expect(assist(page).getByTestId("qa-ai")).toBeEnabled();
  await expect(assist(page).getByTestId("qa-ai-note")).toHaveCount(0);
  await expect(proposals(page).first().locator('input[type="checkbox"]')).not.toBeChecked();
});

test("a refused request shows its words under the one role=alert", async ({ page }) => {
  await openDraft(page);
  await page.route("**/api/v1/quote-assist/extract", (route) =>
    route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "quoteassist: this role may not read descriptions" }) }),
  );
  await paste(page, TEXT);
  const alert = assist(page).locator('[role="alert"]');
  await expect(alert).toHaveCount(1);
  await expect(alert).toHaveText("This role may not read descriptions");
  await expect(proposals(page)).toHaveCount(0);
  // The suggestions beside it stay advice.
  await expect(assist(page).getByTestId("qa-gaps").locator('[role="alert"]')).toHaveCount(0);
});

test("screenshots: the state of the reference shot", async ({ page }) => {
  await page.setViewportSize({ width: 1340, height: 900 });
  await openDraft(page);
  await assist(page).scrollIntoViewIfNeeded();
  await shot(assist(page), "assist-initial");
  await paste(page, "Back patio about 400 sq ft of pavers with a 30 ft border, jointing sand for the joints");
  await expect(proposals(page)).toHaveCount(1);
  await shot(assist(page), "assist-proposals");
  await assist(page).getByTestId("qa-ai").check();
  await assist(page).getByRole("button", { name: "Suggest lines from this text" }).click();
  await expect(assist(page).getByTestId("qa-ai-note")).toBeVisible();
  await shot(assist(page), "assist-latched");
  const row = proposals(page).first();
  await row.locator('[data-field="qty"]').fill("4");
  await row.locator('[data-field="uom"]').fill("bag");
  await row.locator('input[type="checkbox"]').check();
  await shot(assist(page), "assist-ticked");
});
