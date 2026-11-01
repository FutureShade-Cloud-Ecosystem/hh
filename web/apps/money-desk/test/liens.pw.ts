// The Money Desk's Liens area (item 1d), walked in a real browser on the stub: the Monday triage, the jobs and the
// new job form, the holdback sync, the holdback aging and the dual gated release, the documents and their versions.
// Each leg is a refusal or a state as much as a happy path: the form is refused in words before anything is sent, a
// co-signer the core will not take is refused in the core's words, a release another hand already made says there is
// nothing outstanding, a load that fails says so and can be tried again, and an empty list says it is empty.
import type { Page } from "@playwright/test";
import { test, expect, openLiens, liensTab, noSidewaysScroll, GM_ID, handoffFor, dayFrom, nb } from "./lien-helpers.js";

const row = (page: Page, job: string, kind = "triage") => page.locator(`[data-row="${kind}"][data-job-no="${job}"]:visible`);

test("the controller opens Liens on the Monday triage: the core's banner and four jobs, oldest first, each with the core's badge and note", async ({ page, request }) => {
  await openLiens(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Liens");
  await expect(page.locator("[data-banner]")).toContainText("DRAFT, statute-derived, pending client validation");
  const rows = page.locator("[data-row='triage']:visible");
  await expect(rows).toHaveCount(4);
  await expect(rows.nth(0)).toContainText("GLS-2102");
  await expect(rows.nth(0)).toContainText("Overdue");
  await expect(rows.nth(0)).toContainText("100 days");
  await expect(rows.nth(0)).toContainText("$6,475.00");
  await expect(rows.nth(0)).toContainText("Past the release window, action overdue.");
  await expect(row(page, "GLS-2101")).toContainText("Release due");
  await expect(row(page, "GLS-2101")).toContainText("$18,600.00");
  await expect(row(page, "GLS-2101")).toContainText("At/over the 45-day window, eligible to release.");
  await expect(row(page, "GLS-2104")).toContainText("Payable, released by hand");
  await expect(row(page, "GLS-2103")).toContainText("Aging");
  await expect(row(page, "GLS-2103")).toContainText("12 days");
  // A word always sits beside the colour: every badge carries its word and the area names its own count.
  await expect(page.locator("[data-triage-count]")).toHaveText("4 jobs hold retention");
});

test("a triage row opens its job on the jobs tab, with the facts the core holds and what is held now", async ({ page, request }) => {
  await openLiens(page, request);
  await row(page, "GLS-2101").getByRole("button", { name: "Open job" }).click();
  await expect(page.locator("[data-tab-id='jobs']")).toHaveAttribute("aria-current", "true");
  const panel = page.locator("[data-job-panel]");
  await expect(panel).toContainText("GLS-2101");
  await expect(panel).toContainText("Pool deck pavers and drainage, Summit Ridge Builders");
  await expect(panel).toContainText("Receivable");
  await expect(panel).toContainText("$186,000.00");
  await expect(panel).toContainText("10%");
  await expect(panel).toContainText("Substantially performed");
  await expect(panel.locator("[data-held-now]")).toContainText("$18,600.00 held, oldest 62 days");
});

test("the jobs tab lists every job newest first and a press on a row opens it", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "jobs");
  const rows = page.locator("[data-row='job']:visible");
  await expect(rows).toHaveCount(5);
  await expect(rows.nth(0)).toContainText("GLS-2103");
  await expect(rows.nth(4)).toContainText("GLS-2100");
  await expect(row(page, "GLS-2100", "job")).toContainText("Released");
  await expect(page.locator("[data-job-panel]")).toHaveCount(0);
  await row(page, "GLS-2104", "job").getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-job-panel]")).toContainText("Payable");
  await expect(page.locator("[data-job-panel] [data-held-now]")).toContainText("$2,900.00 held");
  await expect(page.locator("[data-job-panel]")).toContainText("We owe a subcontractor's retention");
});

test("a new job is refused in words before anything is sent, then it lands first in the list with its deadline registered", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "jobs");
  await page.getByRole("button", { name: "New job" }).click();
  const form = page.locator("[data-job-form]");
  await expect(form).toBeVisible();
  let sent = 0;
  await page.route("**/api/v1/lien/jobs", (route) => {
    if (route.request().method() === "POST") sent += 1;
    return route.continue();
  });
  await form.getByRole("button", { name: "Create job" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Give the job a number.");
  await form.getByLabel("Job number").fill("GLS-2201");
  await form.getByRole("button", { name: "Create job" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Pick the branch the job belongs to.");
  await form.getByLabel("Branch").selectOption({ index: 1 });
  await form.getByLabel("Contract amount").fill("12.345");
  await form.getByRole("button", { name: "Create job" }).click();
  await expect(page.locator("[data-error]")).toHaveText("The contract amount is dollars and cents, like 125,000.00.");
  await form.getByLabel("Contract amount").fill("64,000.00");
  await form.getByLabel("Holdback percent").fill("150");
  await form.getByRole("button", { name: "Create job" }).click();
  await expect(page.locator("[data-error]")).toContainText("The holdback percent is a number from 0.01 to 100");
  await form.getByLabel("Holdback percent").fill("");
  await form.getByLabel("Substantial performance date").fill(dayFrom(-3));
  await form.getByRole("button", { name: "Create job" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Job GLS-2201 created."));
  expect(sent).toBe(1);
  await expect(page.locator("[data-row='job']:visible").first()).toContainText("GLS-2201");
  await expect(page.locator("[data-row='job']:visible").first()).toContainText("$64,000.00");
  // The job's release deadline reached the calendar through the core.
  await page.getByRole("button", { name: "Compliance", exact: true }).click();
  await page.locator("[data-compliance] [data-tab-id='calendar']").click();
  await expect(page.locator("[data-row='deadline']:visible")).toHaveCount(8);
});

test("syncing a job's holdbacks books the held invoice once and a second press books nothing", async ({ page, request }) => {
  await openLiens(page, request);
  await row(page, "GLS-2101").getByRole("button", { name: "Open job" }).click();
  await page.getByRole("button", { name: "Sync holdbacks" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Synced: 1 invoice booked for $4,200.00, 2 already booked.");
  await expect(page.locator("[data-held-now]")).toContainText("$22,800.00 held");
  await page.getByRole("button", { name: "Sync holdbacks" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Synced: nothing new to book, 3 already booked.");
  await expect(page.locator("[data-held-now]")).toContainText("$22,800.00 held");
});

test("the holdbacks tab ages the retention; a payable holdback is released by hand and has no release control", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "holdbacks");
  const rows = page.locator("[data-row='aging']:visible");
  await expect(rows).toHaveCount(4);
  await expect(rows.nth(0)).toContainText("GLS-2102");
  await expect(rows.nth(0)).toContainText("Over 90 days");
  await expect(row(page, "GLS-2101", "aging")).toContainText("61 to 90 days");
  await expect(row(page, "GLS-2101", "aging")).toContainText("Release due");
  await expect(row(page, "GLS-2103", "aging")).toContainText("Not yet");
  await expect(row(page, "GLS-2104", "aging")).toContainText("Released by hand");
  await expect(row(page, "GLS-2104", "aging").getByRole("button", { name: "Release" })).toHaveCount(0);
  await expect(row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" })).toHaveCount(1);
});

test("the controller can read a release but not make one: the screen says who releases, and offers no confirm", async ({ page, request }) => {
  await openLiens(page, request, "controller");
  await liensTab(page, "holdbacks");
  await row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-release-panel]");
  await expect(panel).toBeFocused();
  await expect(panel).toContainText("GLS-2101");
  await expect(panel).toContainText("$18,600.00");
  await expect(panel).toContainText("Releasing a holdback is for the General Manager, co-signed by a Financial Controller or Payables and Receivables.");
  await expect(panel.getByRole("button", { name: "Confirm release" })).toHaveCount(0);
  await panel.getByRole("button", { name: "Close" }).click();
  await expect(page.locator("[data-release-panel]")).toHaveCount(0);
});

test("the General Manager releases a holdback: the co-signer is picked from the roster, the confirm names the job and the amount, and the job leaves the aging", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Liens");
  await liensTab(page, "holdbacks");
  await row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-release-panel]");
  await expect(panel).toBeFocused();
  const options = panel.getByLabel("Co-signer").locator("option");
  await expect(options).toHaveText(["Pick a co-signer", "Financial Controller (demo)", "Payables and Receivables (demo)", "Payables and Receivables (former, demo)"]);
  await expect(panel.locator("[data-confirm-words]")).toHaveText("Release $18,600.00 held on job GLS-2101. This posts a journal to the ledger.");
  // No co-signer yet: the press is refused in words and nothing is sent.
  await panel.getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Pick the co-signer who reviewed this release.");
  await expect(row(page, "GLS-2101", "aging")).toHaveCount(1);
  await panel.getByLabel("Co-signer").selectOption({ label: "Financial Controller (demo)" });
  await panel.getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $18,600.00 on job GLS-2101, co-signed by Financial Controller (demo)."));
  await expect(page.locator("[data-release-panel]")).toHaveCount(0);
  await expect(row(page, "GLS-2101", "aging")).toHaveCount(0);
  await liensTab(page, "triage");
  await expect(page.locator("[data-triage-count]")).toHaveText("3 jobs hold retention");
});

test("a co-signer the core will not take is refused in the core's words and nothing is released", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  await liensTab(page, "holdbacks");
  await row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-release-panel]");
  await panel.getByLabel("Co-signer").selectOption({ label: "Payables and Receivables (former, demo)" });
  await panel.getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Holdback release co-signer must be an active staff member.");
  await expect(page.locator("[data-release-panel]")).toBeVisible();
  await expect(row(page, "GLS-2101", "aging")).toHaveCount(1);
  // The same press with a good co-signer then goes through: the refusal left everything as it was.
  await panel.getByLabel("Co-signer").selectOption({ label: "Payables and Receivables (demo)" });
  await panel.getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-notice]")).toContainText(nb("Released $18,600.00 on job GLS-2101"));
});

test("a holdback released by another hand meanwhile is refused as nothing outstanding, and the list is read again", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  await liensTab(page, "holdbacks");
  await row(page, "GLS-2102", "aging").getByRole("button", { name: "Release" }).click();
  const panel = page.locator("[data-release-panel]");
  await panel.getByLabel("Co-signer").selectOption({ label: "Financial Controller (demo)" });
  // Someone else releases the same holdback while this screen is open.
  const token = await page.evaluate((id) => {
    const part = (value: unknown): string => btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
    return `${part({ alg: "ES256" })}.${part({ sub: id, exp: 4102444800 })}.c3R1Yg`;
  }, GM_ID);
  const other = await request.post("/api/v1/lien/holdback/10b00000-0000-4000-8000-000000000002/release", {
    headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" },
    data: { approver_id: "de110000-0000-4000-9000-000000000008", approver_role: "FINANCIAL_CONTROLLER" },
  });
  expect(other.status()).toBe(200);
  await panel.getByRole("button", { name: "Confirm release" }).click();
  await expect(page.locator("[data-error]")).toHaveText("No outstanding retention to release for this job.");
  await expect(row(page, "GLS-2102", "aging")).toHaveCount(0);
});

test("the documents tab renders a form from its own merge fields, a second render is version 2, and the history reads both", async ({ page, request }) => {
  await openLiens(page, request);
  await liensTab(page, "documents");
  await page.getByLabel("Job", { exact: true }).selectOption({ label: "GLS-2101" });
  await expect(page.locator("[data-doc-empty]")).toContainText("No documents for this job yet.");
  await page.getByLabel("Form", { exact: true }).selectOption({ label: "Conditional release" });
  await expect(page.locator("[data-merge-field]")).toHaveCount(6);
  await expect(page.getByLabel("Contractor / Claimant")).toBeVisible();
  await page.getByLabel("Owner", { exact: true }).fill("Summit Ridge Builders");
  await page.getByLabel("Job / Improvement").fill("GLS-2101");
  await page.getByRole("button", { name: "Render draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Rendered version 1 of Conditional release.");
  const view = page.locator("[data-doc-view]");
  await expect(view).toContainText("*** DRAFT, statute-derived, pending client validation ***");
  await expect(view).toContainText("Owner: Summit Ridge Builders");
  await expect(view).toContainText("Contractor / Claimant: ____");
  await expect(page.locator("[data-row='doc']:visible")).toHaveCount(1);
  await page.getByLabel("Owner", { exact: true }).fill("Summit Ridge Builders (revised)");
  await page.getByRole("button", { name: "Render draft" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Rendered version 2 of Conditional release.");
  await expect(page.locator("[data-row='doc']:visible")).toHaveCount(1);
  await expect(page.locator("[data-row='doc']:visible")).toContainText("Version 2");
  await page.getByRole("button", { name: "History" }).click();
  const versions = page.locator("[data-row='version']:visible");
  await expect(versions).toHaveCount(2);
  await expect(versions.nth(0)).toContainText("Version 2");
  await versions.nth(1).getByRole("button", { name: "View" }).click();
  await expect(view).toContainText("(v1)");
  await expect(view).toContainText("Owner: Summit Ridge Builders\n");
});

test("a load that fails says so in words and Try again reads the area again", async ({ page, request }) => {
  await page.route("**/api/v1/lien/triage*", (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) }));
  await page.addInitScript(handoffFor, "controller");
  await request.post("/_stub/reset");
  await page.goto("/");
  await page.getByRole("button", { name: "Liens", exact: true }).click();
  await expect(page.locator("[data-load-error]")).toContainText("Internal error.");
  await page.unroute("**/api/v1/lien/triage*");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-row='triage']:visible")).toHaveCount(4);
});

test("an empty worklist, an empty aging and an empty job list say they are empty", async ({ page, request }) => {
  for (const route of ["triage", "holdback/aging", "jobs"]) {
    await page.route(`**/api/v1/lien/${route}*`, (r) => r.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ items: null }) }));
  }
  await page.addInitScript(handoffFor, "controller");
  await request.post("/_stub/reset");
  await page.goto("/");
  await page.getByRole("button", { name: "Liens", exact: true }).click();
  await expect(page.locator("[data-empty='triage']")).toContainText("Nothing on the worklist");
  await liensTab(page, "holdbacks");
  await expect(page.locator("[data-empty='aging']")).toContainText("No holdback is outstanding");
  await liensTab(page, "jobs");
  await expect(page.locator("[data-empty='jobs']")).toContainText("No jobs yet");
});

test("Payables and Receivables reads the jobs and the calendar and is offered no write", async ({ page, request }) => {
  await openLiens(page, request, "payrec");
  await expect(page.getByRole("button", { name: "Receivables", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Approvals and ledger" })).toHaveCount(0);
  await expect(page.locator("[data-row='triage']:visible")).toHaveCount(4);
  await liensTab(page, "jobs");
  await expect(page.getByRole("button", { name: "New job" })).toHaveCount(0);
  await expect(page.locator("[data-read-only]")).toContainText("Payables and Receivables reads the liens here. Creating a job, syncing holdbacks and rendering a document are for the Financial Controller or the General Manager.");
  await row(page, "GLS-2103", "job").getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-job-panel]")).toContainText("GLS-2103");
  await expect(page.getByRole("button", { name: "Sync holdbacks" })).toHaveCount(0);
  await liensTab(page, "documents");
  await expect(page.getByRole("button", { name: "Render draft" })).toHaveCount(0);
  await liensTab(page, "holdbacks");
  await row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" }).click();
  await expect(page.locator("[data-release-panel]")).toContainText("Releasing a holdback is for the General Manager");
  await page.getByRole("button", { name: "Compliance", exact: true }).click();
  await expect(page.locator("[data-row='deadline']:visible")).toHaveCount(5);
  await page.locator("[data-compliance] [data-tab-id='review']").click();
  await expect(page.getByRole("button", { name: "Save review status" })).toHaveCount(0);
  await expect(page.locator("[data-read-only]")).toContainText("Setting the review status is for the Financial Controller or the General Manager.");
});

test("the General Manager's area switch holds Liens and Compliance, and the controller's holds all eight", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  const names = page.locator("nav[aria-label='Money Desk areas'] button");
  await expect(names).toHaveText(["Liens", "Compliance"]);
  const second = await page.context().newPage();
  await second.addInitScript(handoffFor, "controller");
  await second.goto("/");
  await expect(second.locator("nav[aria-label='Money Desk areas'] button")).toHaveText(["Approvals and ledger", "Receivables", "Payables", "Statements", "Bank reconciliation", "Tax", "Liens", "Compliance"]);
  await second.close();
});

test("the keys reach every tab and the release confirm, and focus lands where the screen changed", async ({ page, request }) => {
  await openLiens(page, request, "general_manager");
  await page.locator("[data-liens] [data-tab-id='holdbacks']").focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-tab-id='holdbacks']")).toHaveAttribute("aria-current", "true");
  await row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-release-panel]")).toBeFocused();
  await expect(page.getByLabel("Co-signer")).toBeVisible();
  await page.keyboard.press("Tab");
  await expect(page.getByLabel("Co-signer")).toBeFocused();
  await page.getByLabel("Co-signer").selectOption({ label: "Financial Controller (demo)" });
  await page.getByRole("button", { name: "Confirm release" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-notice]")).toContainText("Released $18,600.00");
});

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("every tab fits the screen with cards in place of tables", async ({ page, request }) => {
    await openLiens(page, request, "general_manager");
    await expect(page.locator("[data-row='triage']:visible")).toHaveCount(4);
    await expect(page.locator("table:visible")).toHaveCount(0);
    await noSidewaysScroll(page);
    for (const id of ["jobs", "holdbacks", "documents"] as const) {
      await liensTab(page, id);
      await noSidewaysScroll(page);
    }
    await liensTab(page, "holdbacks");
    await expect(page.locator("[data-row='aging']:visible")).toHaveCount(4);
    await row(page, "GLS-2101", "aging").getByRole("button", { name: "Release" }).click();
    await expect(page.locator("[data-release-panel]")).toBeInViewport();
    await noSidewaysScroll(page);
  });

  test("a job is created and a document rendered from the phone", async ({ page, request }) => {
    await openLiens(page, request, "controller");
    await liensTab(page, "jobs");
    await page.getByRole("button", { name: "New job" }).click();
    const form = page.locator("[data-job-form]");
    await form.getByLabel("Job number").fill("GLS-2301");
    await form.getByLabel("Branch").selectOption({ index: 1 });
    await form.getByRole("button", { name: "Create job" }).click();
    await expect(page.locator("[data-notice]")).toContainText(nb("Job GLS-2301 created."));
    await liensTab(page, "documents");
    await page.getByLabel("Job", { exact: true }).selectOption({ label: "GLS-2301" });
    await page.getByLabel("Form", { exact: true }).selectOption({ label: "Notice" });
    await page.getByLabel("Subject", { exact: true }).fill("Notice of holdback release");
    await page.getByRole("button", { name: "Render draft" }).click();
    await expect(page.locator("[data-doc-view]")).toContainText("Subject: Notice of holdback release");
    await noSidewaysScroll(page);
  });
});

