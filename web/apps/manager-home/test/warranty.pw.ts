// Warranty, in a real browser against the stub: the landing link opens the
// desk, the board carries the world's claims, the claim detail walks the
// lifecycle (tick the checklist item, move the phase, settle through the
// server), the recovery log re-reads the server's subtraction, the service
// levels show the breach, the registry refuses a manager's write in the
// walled words, and the reports answer. Every state: loading, empty, refusal,
// success.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

async function openWarranty(page: import("@playwright/test").Page): Promise<void> {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator(".view-link", { hasText: "Warranty" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Warranty");
}

test("the landing link opens the warranty desk on its board, the world's claims in their columns", async ({ page }) => {
  await openWarranty(page);
  const board = page.locator("[data-test='warranty-board']");
  await expect(board).toBeVisible();
  await expect(board.locator("[data-phase='INTAKE'] .claim-card")).toHaveCount(2);
  await expect(board.locator("[data-phase='TRIAGE'] .claim-card")).toHaveCount(1);
  await expect(board.locator("[data-phase='CLOSED'] .claim-card")).toHaveCount(1);
  await expect(board.locator(".claim-card", { hasText: "WTY-1041" })).toContainText("Northland Stone Supply (demo)");
  await expect(board.locator(".claim-card", { hasText: "WTY-1039" })).toContainText("$240.00");
  // The empty column says so, never a blank.
  await expect(board.locator("[data-phase='REJECTED']")).toContainText("-");
});

test("the board's empty world says so in words, with the capture invitation", async ({ page, request }) => {
  await useScenario(request, "no-warranty");
  await openWarranty(page);
  await expect(page.locator("[data-test='warranty-board-empty']")).toContainText("No warranty claims on the board yet");
});

test("the claim detail walks the lifecycle: tick the required item, move the phase, see the timeline move", async ({ page }) => {
  await openWarranty(page);
  await page.locator(".claim-card", { hasText: "WTY-1041" }).click();
  await expect(page.locator("[data-test='warranty-claim-no']")).toHaveText("WTY-1041");
  // The checklist header counts the required item still open.
  await expect(page.locator(".check-list")).toContainText("Customer statement recorded");
  await expect(page.locator(".box", { hasText: "Checklist" }).locator(".badge")).toHaveText("1 required item still open");

  // The gate: the move is refused until the required item is ticked, in the core's own words.
  await page.getByRole("button", { name: "Triage", exact: true }).click();
  await expect(page.locator("[data-test='warranty-refusal']")).toContainText("required checklist items incomplete");

  // Tick it (the row is a checkbox button), and the move goes through.
  await page.getByRole("checkbox", { name: /Customer statement recorded/ }).click();
  await expect(page.locator(".box", { hasText: "Checklist" }).locator(".badge")).toHaveText("Checklist complete");
  await page.getByRole("button", { name: "Triage", exact: true }).click();
  await expect(page.locator("[data-test='warranty-detail-notice']")).toHaveText("Moved to Triage.");
  await expect(page.locator("[data-test='warranty-claim-no']").locator("..").locator(".badge")).toHaveText("Triage");
  // The timeline carries the move.
  await expect(page.locator(".timeline")).toContainText("Moved from intake to triage");
});

test("settlement goes through the server and the answer's own figures land on the claim", async ({ page }) => {
  await openWarranty(page);
  await page.locator(".claim-card", { hasText: "WTY-1039" }).click();
  await expect(page.locator("[data-test='warranty-claim-no']")).toHaveText("WTY-1039");

  // Advance to SETTLEMENT (the decision item is ticked in the world), then settle.
  await page.getByRole("button", { name: "Settlement", exact: true }).click();
  await expect(page.locator("[data-test='warranty-detail-notice']")).toHaveText("Moved to Settlement.");
  await page.getByRole("button", { name: "Record settlement" }).click();
  await page.locator("select[data-field='w-settle-outcome']").selectOption({ index: 0 });
  await page.locator("input[data-field='w-settle-realized']").fill("240.00");
  await page.locator("input[data-field='w-settle-note']").fill("Credit approved in full");
  await page.locator("[data-action='w-settle']").click();
  await expect(page.locator("[data-test='warranty-detail-notice']")).toContainText("Settlement recorded and posted to the ledger");
  // The claim now carries the server's outcome and figures.
  await expect(page.locator(".kv", { hasText: "Outcome" }).first()).toContainText("Vendor credit");
  await expect(page.locator(".kv", { hasText: "Realized" }).first()).toContainText("$240.00");
  // And the postings box answers.
  await expect(page.locator(".box").filter({ hasText: "Postings" }).last()).toContainText("Vendor recovery");
});

test("the settle wall: a refused settle keeps the sheet standing with the words that name who settles", async ({ page, request }) => {
  await useScenario(request, "refused-settle");
  await openWarranty(page);
  await page.locator(".claim-card", { hasText: "WTY-1039" }).click();
  await page.getByRole("button", { name: "Settlement", exact: true }).click();
  await page.getByRole("button", { name: "Record settlement" }).click();
  await page.locator("input[data-field='w-settle-realized']").fill("240.00");
  await page.locator("[data-action='w-settle']").click();
  await expect(page.locator("[data-test='warranty-refusal']")).toContainText("You may not record this settlement.");
  await expect(page.locator("[data-test='warranty-refusal']")).toContainText("manager roles (tier 1 and tier 2)");
});

test("the recovery worklist: outstanding is the server's figure, and a receipt moves it", async ({ page }) => {
  await openWarranty(page);
  await page.getByRole("tab", { name: "Recovery" }).click();
  const stats = page.locator("[data-test='warranty-recovery']");
  await expect(stats.locator(".stat.headline .n")).toHaveText("$300.00");
  // The claim's row carries the same arithmetic per claim, and the chase log opens.
  await page.getByRole("button", { name: "Chase" }).click();
  await expect(page.locator(".chase-log li")).toHaveCount(2);
  await page.locator("select[data-field='w-recovery-action']").selectOption({ index: 2 });
  await page.locator("input[data-field='w-recovery-amount']").fill("150.00");
  await page.locator("input[data-field='w-recovery-ref']").fill("CN-8820");
  await page.getByRole("button", { name: "Log", exact: true }).click();
  // The board re-reads the server's answer, not an optimistic guess.
  await expect(stats.locator(".stat.headline .n")).toHaveText("$150.00");
  await expect(page.locator(".stat", { hasText: "Received" }).locator(".n")).toHaveText("$150.00");
  await expect(page.locator(".chase-log")).toContainText("CN-8820");
});

test("the recovery empty world: every agreed recovery received", async ({ page, request }) => {
  await useScenario(request, "no-warranty");
  await openWarranty(page);
  await page.getByRole("tab", { name: "Recovery" }).click();
  await expect(page.locator("[data-test='warranty-recovery-empty']")).toContainText("Nothing outstanding");
});

test("the service levels show the breach, the notification status, and the configured targets", async ({ page }) => {
  await openWarranty(page);
  await page.getByRole("tab", { name: "Service levels" }).click();
  const board = page.locator("[data-test='warranty-sla']");
  await expect(board.locator("[data-test='warranty-sla-breached']")).toHaveText("1");
  // The alert carries the claim, its overdue hours, and the notification's own status.
  const alertRow = page.locator("tr[data-alert]");
  await expect(alertRow).toContainText("WTY-1042");
  await expect(alertRow).toContainText("12h");
  await expect(alertRow).toContainText("Sent");
  // Acknowledging closes the alert: it leaves the open list, and the include
  // switch brings it back with its acknowledged words.
  await alertRow.getByRole("button", { name: "Acknowledge" }).click();
  await expect(page.getByText("No open alerts.")).toBeVisible();
  await page.locator(".check-row", { hasText: "Include acknowledged" }).click();
  const acknowledgedRow = page.locator("tr[data-alert]");
  await expect(acknowledgedRow).toContainText("acknowledged");
  // The targets name the per-vendor override.
  const overrideRow = page.locator("tr[data-target]", { hasText: "Bay Aggregate (demo)" });
  await expect(overrideRow).toContainText("72h");
});

test("the registry shows the explicit undocumented state, and a manager's write is refused in the walled words", async ({ page }) => {
  await openWarranty(page);
  await page.getByRole("tab", { name: "Vendor registry" }).click();
  const table = page.locator("[data-test='warranty-vendors']");
  await expect(table).toContainText("Undocumented");
  await expect(table).toContainText("Documented");
  // The expansion reads the whole profile: terms, documents, overrides.
  await page.getByRole("button", { name: "Northland Stone Supply (demo)" }).click();
  await expect(page.locator(".vendor-panel")).toContainText("Boundary course paver, 60 mm");
  await expect(page.locator(".vendor-panel")).toContainText("docs://warranty/northland-terms");
  // The write: a tier 2 manager holds no admin role, and the wall names who does.
  await page.getByRole("button", { name: "Terms", exact: true }).first().click();
  await page.locator("input[data-field='w-vendor-ref']").fill("warranty@northlandstone.example");
  await page.getByRole("button", { name: "Save terms" }).click();
  await expect(page.locator("[data-test='warranty-refusal']")).toContainText("You may not change the vendor warranty registry");
  await expect(page.locator("[data-test='warranty-refusal']")).toContainText("tier 1");
});

test("the reports answer with the funnel, the win rate and the exposure", async ({ page }) => {
  await openWarranty(page);
  await page.getByRole("tab", { name: "Reports" }).click();
  const stats = page.locator("[data-test='warranty-reports']");
  await expect(stats.locator(".stat", { hasText: "Open claims" }).locator(".n")).toHaveText("4");
  await expect(stats.locator(".stat", { hasText: "Total exposure" }).locator(".n")).toHaveText("$840.00");
  await expect(stats.locator(".stat", { hasText: "Average days" }).locator(".n")).toHaveText("30");
  // The funnel's bars carry their counts as text.
  await expect(page.locator(".funnel .bar-row", { hasText: "Intake" }).locator(".value")).toHaveText("2");
  await expect(page.locator(".report-grid").first()).toContainText("Northland Stone Supply (demo)");
});

test("capture a claim from the board's sheet, and the detail opens on the answer", async ({ page }) => {
  await openWarranty(page);
  await page.getByRole("button", { name: "+ Capture a claim" }).click();
  await page.locator("textarea[data-field='w-capture-desc']").fill("Corner units cracked across two pallets");
  await page.locator("input[data-field='w-capture-product']").fill("Corner paver, 60 mm");
  await page.locator("input[data-field='w-capture-qty']").fill("24");
  await page.locator("input[data-field='w-capture-uom']").fill("PC");
  await page.locator("input[data-field='w-capture-expected']").fill("180");
  await page.getByRole("button", { name: "Create claim" }).click();
  await expect(page.locator("[data-test='warranty-claim-no']")).toHaveText("WTY-1045");
  await expect(page.locator(".kv", { hasText: "Expected" })).toContainText("$180.00");
});

test("the public intake's photos ride the claim detail", async ({ page }) => {
  await openWarranty(page);
  await page.locator(".claim-card", { hasText: "WTY-1044" }).click();
  await expect(page.locator(".box", { hasText: "Photos from the intake" })).toBeVisible();
  const img = page.locator("img[data-w-photo]");
  await expect(img).toHaveCount(1);
  await expect(img).toHaveAttribute("alt", "Patio steps, hollow edge");
  await expect(img).toHaveAttribute("src", /blob:/);
});
