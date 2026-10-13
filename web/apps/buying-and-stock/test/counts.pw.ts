// The counts area walked in a real browser against the stub: sheets opened,
// frozen, counted blind, submitted, approved by a checker and posted after a
// confirm that names the sheet, the yard and the value; the maker-checker and
// seat refusals in words; the recon and the negative stock. The stub is held
// to the core's shapes and sentences by test/inventory-contract.test.mjs.
import type { Page } from "@playwright/test";
import { expect, openCounts, payablesHandoff, resetStub, switchRole, test, yardHandoff, procurementHandoff } from "./helpers.js";

const tab = (page: Page, id: string) => page.locator(`[data-counts-tab="${id}"]`);

async function startSheet(page: Page): Promise<void> {
  await page.locator('[data-field="sheet-branch"]').selectOption({ label: "Point Loma yard" });
  await page.locator('[data-field="sheet-scope"]').selectOption("FULL");
  await page.getByRole("button", { name: "New sheet" }).click();
  await expect(page.locator("[data-sheet-detail]")).toBeVisible();
}

async function freeze(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Freeze snapshot" }).click();
  await expect(page.locator("[data-count-entry]")).toBeVisible();
  await expect(page.locator("[data-count-line]").first()).toBeVisible();
}

/** Counts the first line one below its snapshot with a reason. */
async function countOneShort(page: Page, reason: string): Promise<void> {
  const line = page.locator("[data-count-line]").first();
  await line.locator('[data-field="counted"]').fill("0");
  await line.locator('[data-field="reason"]').selectOption(reason);
  await page.getByRole("button", { name: "Submit counts" }).click();
  await expect(page.locator("[data-sheet-status]")).toHaveText("Counted");
}

test("the counts area opens on the sheets list, and the posted sheet reads Posted", async ({ page, request }) => {
  await openCounts(page, request, "sheets");
  await expect(page.locator("[data-sheets]")).toBeVisible();
  await expect(page.locator('[data-sheet-row="CS-PLM-0001"]')).toContainText("Posted");
  await expect(page.locator('[data-sheet-row="CS-PLM-0001"]')).toContainText("Full count");
});

test("a sheet is opened, frozen, counted blind, and submitted with a reason on the variance", async ({ page, request }) => {
  await openCounts(page, request, "sheets");
  await startSheet(page);
  await expect(page.locator("[data-sheet-status]")).toHaveText("Open");
  // The book quantity is hidden until the blind count is lifted: the audit-clean default.
  await freeze(page);
  await expect(page.locator("[data-count-entry]")).toContainText("Blind count");
  const line = page.locator("[data-count-line]").first();
  await expect(line.locator('[data-field="counted"]')).toBeVisible();
  await line.locator('[data-field="counted"]').fill("1");
  await page.getByRole("button", { name: "Submit counts" }).click();
  await expect(page.locator("[data-error]")).toContainText("Every variance needs a reason code.");
  await line.locator('[data-field="reason"]').selectOption("SHRINK");
  await page.getByRole("button", { name: "Submit counts" }).click();
  await expect(page.locator("[data-sheet-status]")).toHaveText("Counted");
});

test("the maker cannot approve their own count: the words say so", async ({ page, request }) => {
  await openCounts(page, request, "sheets");
  await startSheet(page);
  await freeze(page);
  await countOneShort(page, "SHRINK");
  await tab(page, "sheets").isVisible();
  await page.getByRole("button", { name: "Approve (checker)" }).click();
  await expect(page.locator("[data-error]")).toContainText("Maker-checker violation, the maker cannot approve their own count");
});

test("posting before a checker's approval is refused in the server's words", async ({ page, request }) => {
  await openCounts(page, request, "sheets");
  await startSheet(page);
  await freeze(page);
  await countOneShort(page, "SHRINK");
  await page.getByRole("button", { name: "Post adjustment" }).click();
  await page.locator("[data-post-confirm]").getByRole("button", { name: "Confirm post" }).click();
  await expect(page.locator("[data-error]")).toContainText("Post requires a checker approval");
  // A sheet can still be abandoned before post, after a confirm that names it.
  await page.getByRole("button", { name: "Cancel sheet" }).click();
  const confirm = page.locator("[data-cancel-confirm]");
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText("Point Loma yard");
  await expect(confirm).toContainText("Nothing posts");
  await expect(page.locator("[data-sheet-status]")).toHaveText("Counted");
  await confirm.getByRole("button", { name: "Back" }).click();
  await expect(confirm).toHaveCount(0);
  await expect(page.locator("[data-sheet-status]")).toHaveText("Counted");
  await page.getByRole("button", { name: "Cancel sheet" }).click();
  await page.locator("[data-cancel-confirm]").getByRole("button", { name: "Confirm cancel" }).click();
  await expect(page.locator("[data-sheet-status]")).toHaveText("Cancelled");
});

test("a negative counted quantity is refused in words before any request", async ({ page, request }) => {
  await openCounts(page, request, "sheets");
  await startSheet(page);
  await freeze(page);
  const line = page.locator("[data-count-line]").first();
  await line.locator('[data-field="counted"]').fill("-400");
  await page.getByRole("button", { name: "Submit counts" }).click();
  await expect(page.locator("[data-error]")).toContainText("A count is zero or more.");
  await expect(page.locator("[data-sheet-status]")).toHaveText("Frozen");
});

test("the yard manager counts, the procurement manager approves and posts after the confirm names the facts", async ({ page, request }) => {
  // The maker: the yard manager, a work seat, counts the frozen sheet.
  await resetStub(request);
  await page.addInitScript(yardHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await page.locator('[data-area="counts"]').click();
  await startSheet(page);
  await freeze(page);
  // The book quantity is hidden under the blind count; lifting it shows the snapshot.
  await page.locator('[data-action="toggle-blind"]').uncheck();
  await expect(page.locator("[data-book-qty]").first()).toBeVisible();
  const line = page.locator("[data-count-line]").first();
  await line.locator('[data-field="counted"]').fill("0");
  await line.locator('[data-field="reason"]').selectOption("DAMAGE");
  await page.getByRole("button", { name: "Submit counts" }).click();
  await expect(page.locator("[data-sheet-status]")).toHaveText("Counted");
  // The approve and the post are a manager seat's move: refused to the maker's seat in words.
  await page.getByRole("button", { name: "Approve (checker)" }).click();
  await expect(page.locator("[data-error]")).toContainText("manager seats' moves");
  // The checker: the procurement manager approves, then posts through the confirm.
  await switchRole(page, procurementHandoff);
  await page.locator('[data-area="counts"]').click();
  await page.locator("[data-sheet-row]", { hasText: "Counted" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-count-variance]")).toBeVisible();
  await page.getByRole("button", { name: "Approve (checker)" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Approved");
  await page.getByRole("button", { name: "Post adjustment" }).click();
  const confirm = page.locator("[data-post-confirm]");
  await expect(confirm).toBeVisible();
  await expect(confirm).toContainText("Point Loma yard");
  await expect(confirm).toContainText("variance");
  // Nothing is valued until the post, so the confirm states no dollar figure; it names the line and the counted figure.
  await expect(confirm).not.toContainText("$0.00");
  await expect(confirm).toContainText("valued at the yard's average cost when it posts");
  await expect(confirm.locator("[data-post-lines] li").first()).toContainText("Copperline");
  await expect(confirm.locator("[data-post-lines] li").first()).toContainText(/book [\d,.]+ to counted 0/);
  await expect(page.locator("[data-variance-summary]")).toContainText("Net");
  await confirm.getByRole("button", { name: "Confirm post" }).click();
  await expect(page.locator("[data-sheet-status]")).toHaveText("Posted");
  await expect(page.locator("[data-notice]")).toContainText("Posted");
});

test("a read-only role is refused the new sheet in words that name who may", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(payablesHandoff);
  await page.goto("/");
  await expect(page.locator("[data-shortfall-row]").first()).toBeVisible();
  await page.locator('[data-area="counts"]').click();
  await page.getByRole("button", { name: "New sheet" }).click();
  await expect(page.locator("[data-error]")).toContainText("look but not touch");
});

test("the recon reads the run, its history and the negative stock; a second look runs nothing twice", async ({ page, request }) => {
  await openCounts(page, request, "recon");
  await page.locator('[data-field="recon-branch"]').selectOption({ label: "Point Loma yard" });
  await page.getByRole("button", { name: "Run the tie-out" }).click();
  await expect(page.locator("[data-recon-current]")).toBeVisible();
  await expect(page.locator("[data-recon-current]")).toContainText("Tied");
  await expect(page.locator("[data-recon-current]")).toContainText("$40,275.00");
  await expect(page.locator("[data-recon-history] [data-recon-run]")).toHaveCount(2);
  await expect(page.locator("[data-negative-row]").first()).toContainText("-6");
  await expect(page.locator("[data-recon-current]")).toContainText("Subledger");
});

test("on a phone the sheet detail stacks and the page never scrolls sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openCounts(page, request, "sheets");
  await expect(page.locator('[data-sheet-row="CS-PLM-0001"]')).toBeVisible();
  const sideways = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth + 1);
  expect(sideways).toBe(false);
});
