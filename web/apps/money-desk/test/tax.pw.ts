// The Financial Controller's tax legs on the Money Desk, walked in a real browser on the stub: the rate table and
// a rate added through its confirm step; the exemption profiles, the review queue and a profile put in force;
// the certificates on file, opened, checked and downloaded, and a new one recorded with its number masked; the
// company wide settings; the reconciliation to the ledger and the recompute; every refusal in words with none of
// the core's Ontario words; the loading, empty and failed states; the phone; and the keyboard.
import { test, expect, openTax, tab, resetStub, controllerHandoff, screenText, OLD_WORDS, priorMonth, olderMonth, dayFromToday, dayWords, ORDER_ONE } from "./tax-helpers.js";
import { receivablesHandoff } from "./helpers.js";
import type { Page } from "@playwright/test";

const rowOf = (page: Page, attr: string, text: string) => page.locator(`[${attr}]`).filter({ hasText: text });

async function noOldWords(page: Page): Promise<void> {
  expect(await screenText(page)).not.toMatch(OLD_WORDS);
}

test("the controller's Tax area lists the rate table in California words, and Payables and Receivables does not have it", async ({ page, request }) => {
  await openTax(page, request);
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Tax");
  const standard = rowOf(page, "data-rate-id", "Standard sales tax");
  await expect(standard.locator("[data-rate-figure]")).toHaveText("7.75%");
  await expect(standard).toContainText("California");
  await expect(standard).toContainText("San Diego County combined rate.");
  await expect(standard).toContainText("In force");
  await expect(rowOf(page, "data-rate-id", "Exempt delivery, proof of delivery required").locator("[data-rate-figure]")).toHaveText("0%");
  await expect(rowOf(page, "data-rate-id", "Held for a person to decide")).toBeVisible();
  await noOldWords(page);
  // The other areas are whole beside it.
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await expect(page.locator("[data-card]")).toHaveCount(4);
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await expect(page.locator("[data-rate-id]")).toHaveCount(3);
  // Payables and Receivables has none of it.
  await resetStub(request);
  const other = await page.context().newPage();
  await other.addInitScript(receivablesHandoff);
  await other.goto("/");
  await expect(other.locator("[data-card]")).toHaveCount(4);
  await expect(other.getByRole("button", { name: "Tax", exact: true })).toHaveCount(0);
  await expect(other.locator("nav.areas")).toHaveCount(0);
});

test("a rate is added through a confirm step that names what changes, and a wrong one sends nothing", async ({ page, request }) => {
  await openTax(page, request);
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/v1/tax/rates")) posts.push(r.postData() ?? "");
  });
  const add = page.getByRole("button", { name: "Add the rate", exact: true });
  // A rate that is not a percent is said before anything is sent.
  await page.getByRole("textbox", { name: "Rate, percent" }).fill("seven");
  await expect(page.locator("[data-rate-hint]")).toContainText("Enter a percent between 0 and 100 with at most two decimals, such as 7.75.");
  await add.click();
  await expect(page.locator("[data-error]")).toContainText("Enter the rate as a percent between 0 and 100");
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
  // Three decimals would round a half hundredth either way: refused, not guessed.
  await page.getByRole("textbox", { name: "Rate, percent" }).fill("1.005");
  await add.click();
  await expect(page.locator("[data-error]")).toContainText("at most two decimals");
  // A day that is not a day.
  await page.getByRole("textbox", { name: "Rate, percent" }).fill("8.25");
  await page.getByRole("textbox", { name: "First day" }).fill("June 4");
  await add.click();
  await expect(page.locator("[data-error]")).toContainText("The first day must be a real day");
  // A last day before the first.
  const start = dayFromToday(1);
  await page.getByRole("textbox", { name: "First day" }).fill(start);
  await page.getByRole("textbox", { name: "Last day, if it ends" }).fill(dayFromToday(0));
  await add.click();
  await expect(page.locator("[data-error]")).toContainText("The last day cannot come before the first day");
  expect(posts).toEqual([]);
  // A good one asks first, and says what it does.
  await page.getByRole("textbox", { name: "Last day, if it ends" }).fill("");
  await page.getByRole("textbox", { name: "Note" }).fill("Raised for the new year.");
  await add.click();
  const confirm = page.locator("[data-confirm='rate']");
  await expect(confirm).toContainText("Add Standard sales tax at 8.25%?");
  await expect(confirm).toContainText(`It starts ${dayWords(start)} and has no end.`);
  await expect(confirm).toContainText("Rows are never edited or removed.");
  await expect(confirm.getByRole("button", { name: "Add the rate" })).toBeFocused();
  expect(posts).toEqual([]);
  // Not yet: nothing was sent and the row count is unchanged.
  await confirm.getByRole("button", { name: "Not yet" }).click();
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
  await expect(page.locator("[data-rate-id]")).toHaveCount(3);
  expect(posts).toEqual([]);
  // The second press sends it, once, in the area of the standard rate in force.
  await add.click();
  await page.locator("[data-confirm='rate']").getByRole("button", { name: "Add the rate" }).click();
  await expect(page.locator("[data-notice]")).toContainText(`Added: Standard sales tax at 8.25% from ${dayWords(start)}. It is in the table.`);
  await expect(page.locator("[data-rate-id]")).toHaveCount(4);
  const created = rowOf(page, "data-rate-id", "Raised for the new year.");
  await expect(created.locator("[data-rate-figure]")).toHaveText("8.25%");
  await expect(created).toContainText("Starts later");
  await expect(created).toContainText("California");
  expect(posts).toHaveLength(1);
  expect(JSON.parse(posts[0] ?? "{}")).toMatchObject({ jurisdiction: "CA", reason_code: "STD_ON_HST_13", rate_bps: 825, effective_from: start });
  // The form is cleared of what was added.
  await expect(page.getByRole("textbox", { name: "Rate, percent" })).toHaveValue("");
  await noOldWords(page);
});

test("a rate the gate refuses, a server fault and a lost answer are each said in words, and a lost answer rereads the table", async ({ page, request }) => {
  await openTax(page, request);
  const fill = async () => {
    await page.getByRole("textbox", { name: "Rate, percent" }).fill("5");
    await page.getByRole("button", { name: "Add the rate", exact: true }).click();
    await page.locator("[data-confirm='rate']").getByRole("button", { name: "Add the rate" }).click();
  };
  await page.route("**/api/v1/tax/rates", (route) => (route.request().method() === "POST" ? route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }) : route.continue()));
  await fill();
  await expect(page.locator("[data-error]")).toContainText("Changing the rate table is for the Financial Controller.");
  await expect(page.locator("[data-error]")).not.toContainText("Forbidden");
  await page.unroute("**/api/v1/tax/rates");
  // The core's own sentence for an ungated reason below the standard, in words that name the standard.
  await page.route("**/api/v1/tax/rates", (route) =>
    route.request().method() === "POST"
      ? route.fulfill({
          status: 400,
          contentType: "application/json",
          body: JSON.stringify({ error: 'tax reason "MANUAL_TAX_REVIEW" resolves 500 bps, below a standard of 775 bps, without an evidence gate and without the manual exception: the rate row is misconfigured against the gate map' }),
        })
      : route.continue(),
  );
  await fill();
  await expect(page.locator("[data-error]")).toContainText("Not added: this reason may not charge less than the standard rate (7.75%)");
  await noOldWords(page);
  await page.unroute("**/api/v1/tax/rates");
  // A server fault.
  await page.route("**/api/v1/tax/rates", (route) => (route.request().method() === "POST" ? route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) }) : route.continue()));
  await fill();
  await expect(page.locator("[data-error]")).toContainText("The tax service did not answer. Reload to try again.");
  await page.unroute("**/api/v1/tax/rates");
  // A lost answer: the row may have been written, so the table is read again and the words say so.
  await page.route("**/api/v1/tax/rates", (route) => (route.request().method() === "POST" ? route.abort("failed") : route.continue()));
  await fill();
  await expect(page.locator("[data-error]")).toContainText("Not confirmed: no answer came back, so the rate may or may not have gone through.");
  await expect(page.locator("[data-rate-id]")).toHaveCount(3);
});

test("the review queue lists what waits, the tab counts it, and a profile is put in force through a confirm step", async ({ page, request }) => {
  await openTax(page, request);
  await expect(page.locator("[data-tab-count='profiles']")).toHaveText("1");
  await tab(page, "profiles");
  await expect(page.locator("[data-queue-id]")).toHaveCount(1);
  await expect(page.locator("[data-queue-id='FARM']")).toContainText("Agricultural exemption");
  await expect(page.locator("[data-queue-id='FARM']")).toContainText("Waiting for review");
  const rows = page.locator("[data-profile-id]");
  await expect(rows).toHaveCount(3);
  await expect(rowOf(page, "data-profile-id", "Resale certificate")).toContainText("Requires the photo of the certificate, certificate number and the signed statement");
  await expect(rowOf(page, "data-profile-id", "Agricultural exemption")).toContainText("Requires the certificate number and the signed statement");
  // Put in force asks first and names what changes.
  await page.locator("[data-queue-id='FARM']").getByRole("button", { name: "Put in force" }).click();
  const confirm = page.locator("[data-confirm='profile']");
  await expect(confirm).toContainText("Replace the Agricultural exemption profile?");
  await expect(confirm).toContainText("Buyers with this certificate pay 7.75%.");
  await expect(confirm).toContainText("Standing: in force.");
  await expect(confirm).toContainText("saving replaces its settings for every sale from now on");
  await expect(page.locator("[data-profile-form] [data-field='tx-p-code']")).toHaveValue("FARM");
  await confirm.getByRole("button", { name: "Replace the profile" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved: Agricultural exemption, in force.");
  await expect(page.locator("[data-queue-id]")).toHaveCount(0);
  await expect(page.locator("[data-empty='queue']")).toContainText("Nothing is waiting.");
  await expect(page.locator("[data-tab-count]")).toHaveCount(0);
  await expect(rowOf(page, "data-profile-id", "Agricultural exemption")).toContainText("In force");
  await noOldWords(page);
});

test("a new profile is saved, an existing one edited, and a wrong one is held or refused in words without the core's old ones", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "profiles");
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/v1/tax/profiles")) posts.push(r.postData() ?? "");
  });
  const save = page.getByRole("button", { name: "Save the profile", exact: true });
  // Held in the browser: a code that is not a code, no name, a rate that is not a percent.
  await page.getByRole("textbox", { name: "Code" }).fill("lower case");
  await save.click();
  await expect(page.locator("[data-error]")).toContainText("Give the profile a code of capitals, digits and underscores");
  await page.getByRole("textbox", { name: "Code" }).fill("CONTRACTOR");
  await save.click();
  await expect(page.locator("[data-error]")).toContainText("Give the profile a name first");
  await page.getByRole("textbox", { name: "Name" }).fill("Licensed contractor");
  await page.getByRole("textbox", { name: "Profile rate, percent" }).fill("zero");
  await expect(page.locator("[data-profile-rate-hint]")).toBeVisible();
  await save.click();
  await expect(page.locator("[data-error]")).toContainText("Enter the rate as a percent");
  expect(posts).toEqual([]);
  // The server refuses a reduced rate that does not keep the number, and the screen says it in its own words.
  await page.getByRole("textbox", { name: "Profile rate, percent" }).fill("0");
  await save.click();
  await expect(page.locator("[data-confirm='profile']")).toContainText("Save the Licensed contractor profile?");
  await page.locator("[data-confirm='profile']").getByRole("button", { name: "Save the profile" }).click();
  await expect(page.locator("[data-error]")).toContainText("Not saved: this profile must require the certificate number.");
  await noOldWords(page);
  expect(posts).toHaveLength(1);
  // Tick the number and save it as waiting for review: it lands in the queue.
  await page.getByRole("checkbox", { name: "Certificate number" }).check();
  await page.locator("[data-field='tx-p-status']").selectOption({ label: "Waiting for review" });
  await save.click();
  await page.locator("[data-confirm='profile']").getByRole("button", { name: "Save the profile" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved: Licensed contractor, waiting for review.");
  await expect(page.locator("[data-queue-id='CONTRACTOR']")).toBeVisible();
  await expect(page.locator("[data-profile-id]")).toHaveCount(4);
  expect(JSON.parse(posts[1] ?? "{}")).toMatchObject({ code: "CONTRACTOR", rate_bps: 0, requires_number: true, requires_photo: false, status: "PENDING" });
  // Edit it: the form fills from the row, saving replaces it, and the count stays.
  await rowOf(page, "data-profile-id", "Licensed contractor").getByRole("button", { name: "Edit" }).click();
  await expect(page.locator("[data-profile-form]")).toContainText("Edit CONTRACTOR");
  await expect(page.getByRole("textbox", { name: "Name" })).toHaveValue("Licensed contractor");
  await expect(page.getByRole("checkbox", { name: "Certificate number" })).toBeChecked();
  await page.getByRole("checkbox", { name: "Signed statement" }).check();
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await expect(page.locator("[data-confirm='profile']")).toContainText("Replace the Licensed contractor profile?");
  await page.locator("[data-confirm='profile']").getByRole("button", { name: "Replace the profile" }).click();
  await expect(page.locator("[data-profile-id]")).toHaveCount(4);
  await expect(rowOf(page, "data-profile-id", "Licensed contractor")).toContainText("Requires the certificate number and the signed statement");
  // A wall on the write is named.
  await page.route("**/api/v1/tax/profiles", (route) => (route.request().method() === "POST" ? route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }) : route.continue()));
  await rowOf(page, "data-profile-id", "Licensed contractor").getByRole("button", { name: "Edit" }).click();
  await page.getByRole("button", { name: "Save changes", exact: true }).click();
  await page.locator("[data-confirm='profile']").getByRole("button", { name: "Replace the profile" }).click();
  await expect(page.locator("[data-error]")).toContainText("Changing an exemption profile is for the Financial Controller.");
});

test("the certificates on file: masked numbers only, newest first, opened, checked against their dates and downloaded", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "certificates");
  const rows = page.locator("[data-evidence-id]");
  await expect(rows).toHaveCount(4);
  await expect(rows.first()).toContainText("•••• ••••");
  await expect(rows.last()).toContainText("•••• 4821");
  await expect(rows.last()).toContainText("Resale certificate");
  await expect(rowOf(page, "data-evidence-id", "•••• 1177")).toContainText("Not signed");
  // Open the first: only the mask, the banner for the signed statement and the logged access.
  await rowOf(page, "data-evidence-id", "•••• 4821").getByRole("button", { name: "Open the record" }).click();
  const panel = page.locator("[data-evidence-panel]");
  await expect(panel).toContainText("Certificate •••• 4821");
  await expect(panel.locator("[data-mask]")).toHaveText("•••• 4821");
  await expect(panel.locator("[data-attestation-note]")).toHaveText("The signed statement's wording is waiting for legal review.");
  await expect(panel).toContainText("Only the last four digits are kept.");
  await expect(panel).toContainText("evidence/resale-1.jpg");
  await panel.getByRole("button", { name: "Check the certificate" }).click();
  await expect(panel.locator("[data-check-result]")).toContainText("Can back a reduced rate");
  // The expired one cannot, and checked as of an earlier day it can.
  await rowOf(page, "data-evidence-id", "•••• 9034").getByRole("button", { name: "Open the record" }).click();
  await expect(panel).toContainText("Certificate •••• 9034");
  await panel.getByRole("button", { name: "Check the certificate" }).click();
  await expect(panel.locator("[data-check-result]")).toContainText("Cannot back a reduced rate");
  await expect(panel.locator("[data-check-result]")).toContainText("The certificate has expired.");
  await panel.getByRole("textbox", { name: "Check it as of" }).fill(dayFromToday(-400));
  await panel.getByRole("button", { name: "Check the certificate" }).click();
  await expect(panel.locator("[data-check-result]")).toContainText("Can back a reduced rate");
  await panel.getByRole("textbox", { name: "Check it as of" }).fill("last week");
  await panel.getByRole("button", { name: "Check the certificate" }).click();
  await expect(page.locator("[data-error]")).toContainText("The check date must be a real day");
  // Unsigned: the statement is missing.
  await rowOf(page, "data-evidence-id", "•••• 1177").getByRole("button", { name: "Open the record" }).click();
  await expect(panel).toContainText("Certificate •••• 1177");
  await panel.getByRole("button", { name: "Check the certificate" }).click();
  await expect(panel.locator("[data-check-result]")).toContainText("The signed statement is missing.");
  // Download: logged, the masked record only, and the photo's place.
  await panel.getByRole("button", { name: "Download the record" }).click();
  await expect(panel.locator("[data-download-result]")).toContainText("Download logged. The record carries the masked number only, and the photo is kept at evidence/org-1.jpg.");
  await panel.getByRole("button", { name: "Close" }).click();
  await expect(page.locator("[data-evidence-panel]")).toHaveCount(0);
  await noOldWords(page);
});

test("the certificates filter by order, hold an id that is not an id, and say when none is on file", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "certificates");
  await page.getByRole("textbox", { name: "Only this order" }).fill("order 12");
  await page.getByRole("button", { name: "Filter", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("That is not an order id. Nothing was sent.");
  await expect(page.locator("[data-evidence-id]")).toHaveCount(4);
  await page.getByRole("textbox", { name: "Only this order" }).fill(ORDER_ONE);
  await page.getByRole("button", { name: "Filter", exact: true }).click();
  await expect(page.locator("[data-evidence-id]")).toHaveCount(1);
  await expect(page.locator("[data-evidence-id]")).toContainText("•••• 4821");
  await page.getByRole("textbox", { name: "Only this order" }).fill("7d000000-0000-4000-8000-0000000000aa");
  await page.getByRole("button", { name: "Filter", exact: true }).click();
  await expect(page.locator("[data-empty='evidence']")).toContainText("No certificate is on file.");
  await expect(page.locator("[data-empty='evidence']")).toContainText("None is on file for that order. Clear the filter to see them all.");
  await page.getByRole("button", { name: "Clear", exact: true }).click();
  await expect(page.locator("[data-evidence-id]")).toHaveCount(4);
  await expect(page.getByRole("textbox", { name: "Only this order" })).toHaveValue("");
});

test("a certificate is recorded with its number masked and thrown away, and what the profile forces is held before anything is sent", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "certificates");
  const bodies: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && /\/api\/v1\/tax\/evidence$/.test(r.url())) bodies.push(r.postData() ?? "");
  });
  const record = page.getByRole("button", { name: "Record the certificate", exact: true });
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("Choose the profile this certificate is for. Nothing was sent.");
  await page.getByLabel("Profile for the certificate").selectOption({ label: "Resale certificate" });
  await expect(page.locator("[data-capture-hint]")).toContainText("Requires the photo of the certificate, certificate number and the signed statement");
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("the Resale certificate profile requires the photo of the certificate");
  await page.getByRole("textbox", { name: "Photo kept at" }).fill("evidence/new-resale.jpg");
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("requires the certificate number");
  await page.getByRole("textbox", { name: "Certificate number" }).fill("SR-5550-7316");
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("requires the signed statement");
  await page.getByRole("textbox", { name: "Order", exact: true }).fill("not an order");
  await page.getByRole("checkbox", { name: "The buyer signed the statement" }).check();
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("That is not an order id.");
  await page.getByRole("textbox", { name: "Order", exact: true }).fill("");
  await page.getByRole("textbox", { name: "Certificate expires" }).fill("soon");
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("The expiry must be a real day");
  expect(bodies).toEqual([]);
  await page.getByRole("textbox", { name: "Certificate expires" }).fill(dayFromToday(300));
  await record.click();
  await expect(page.locator("[data-notice]")).toContainText("Recorded. The number is kept as •••• 7316; the full number is not stored.");
  // It went once, with the number, and the number is nowhere on the screen afterwards.
  expect(bodies).toHaveLength(1);
  expect(JSON.parse(bodies[0] ?? "{}")).toMatchObject({ card_profile_code: "RESALE", raw_number: "SR-5550-7316", attestation: true, photo_ref: "evidence/new-resale.jpg" });
  await expect(page.getByRole("textbox", { name: "Certificate number" })).toHaveValue("");
  expect(await screenText(page)).not.toContain("5550");
  expect(await screenText(page)).not.toContain("SR-5550");
  // The new record is first on the list and open.
  await expect(page.locator("[data-evidence-id]")).toHaveCount(5);
  await expect(page.locator("[data-evidence-id]").first()).toContainText("•••• 7316");
  await expect(page.locator("[data-evidence-panel]")).toContainText("Certificate •••• 7316");
  // The core's own refusal comes back in our words.
  await page.route("**/api/v1/tax/evidence", (route) =>
    route.request().method() === "POST" ? route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "band-card photo is required for this profile" }) }) : route.continue(),
  );
  await page.getByLabel("Profile for the certificate").selectOption({ label: "Resale certificate" });
  await page.getByRole("textbox", { name: "Certificate number" }).fill("SR-1");
  await page.getByRole("textbox", { name: "Photo kept at" }).fill("evidence/x.jpg");
  await page.getByRole("checkbox", { name: "The buyer signed the statement" }).check();
  await record.click();
  await expect(page.locator("[data-error]")).toContainText("Not recorded: this profile requires the photo of the certificate.");
  await noOldWords(page);
  await expect(page.getByRole("textbox", { name: "Certificate number" })).toHaveValue("");
});

test("the settings read the company wide row, ask before a change, and keep the two switches that are not available off", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "settings");
  await expect(page.getByRole("textbox", { name: "Seller's permit number" })).toHaveValue("SR-DEMO-0001");
  await expect(page.getByRole("radio", { name: /Round once, on the invoice total/ })).toHaveAttribute("aria-checked", "true");
  await expect(page.getByRole("radio", { name: /Round each line/ })).toHaveAttribute("aria-checked", "false");
  await expect(page.getByText("Automatic tax lookup")).toBeVisible();
  await expect(page.getByText("Postal code routing")).toBeVisible();
  expect(await page.locator(".tx-later").count()).toBeGreaterThanOrEqual(2);
  const bodies: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/v1/tax/config")) bodies.push(r.postData() ?? "");
  });
  await page.getByRole("textbox", { name: "Seller's permit number" }).fill("SR-DEMO-0002");
  await page.getByRole("radio", { name: /Round each line/ }).click();
  await expect(page.getByRole("radio", { name: /Round each line/ })).toHaveAttribute("aria-checked", "true");
  await page.getByRole("button", { name: "Save the settings", exact: true }).click();
  const confirm = page.locator("[data-confirm='config']");
  await expect(confirm).toContainText("Change the company wide tax settings?");
  await expect(confirm).toContainText("Seller's permit number: SR-DEMO-0002. Rounding: round each line.");
  await expect(confirm).toContainText("Past sales are not recomputed by it.");
  expect(bodies).toEqual([]);
  await confirm.getByRole("button", { name: "Save the settings" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved: the company wide tax settings.");
  expect(JSON.parse(bodies[0] ?? "{}")).toMatchObject({ hst_registration_no: "SR-DEMO-0002", round_mode: "PER_LINE", avalara_enabled: false, postal_routing_enabled: false });
  // Read back from the server on a reload.
  await page.reload();
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await tab(page, "settings");
  await expect(page.getByRole("textbox", { name: "Seller's permit number" })).toHaveValue("SR-DEMO-0002");
  await expect(page.getByRole("radio", { name: /Round each line/ })).toHaveAttribute("aria-checked", "true");
  // The core's refusal of a switch, and a wall.
  await page.route("**/api/v1/tax/config", (route) =>
    route.request().method() === "POST" ? route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "avalara/postal routing are dormant at W6 and cannot be enabled" }) }) : route.continue(),
  );
  await page.getByRole("button", { name: "Save the settings", exact: true }).click();
  await page.locator("[data-confirm='config']").getByRole("button", { name: "Save the settings" }).click();
  await expect(page.locator("[data-error]")).toContainText("Automatic tax lookup and postal routing are not available yet, so they cannot be turned on.");
  await noOldWords(page);
  await page.unroute("**/api/v1/tax/config");
  await page.route("**/api/v1/tax/config", (route) => (route.request().method() === "POST" ? route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }) : route.continue()));
  await page.getByRole("button", { name: "Save the settings", exact: true }).click();
  await page.locator("[data-confirm='config']").getByRole("button", { name: "Save the settings" }).click();
  await expect(page.locator("[data-error]")).toContainText("Changing the tax settings is for the Financial Controller.");
});

test("the reconciliation ties out one month, is off in another in words, and holds a year that is not a year", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "checks");
  const prior = priorMonth();
  const older = olderMonth();
  await expect(page.getByRole("textbox", { name: "Year", exact: true })).toHaveValue(String(prior.year));
  await expect(page.locator("[data-field='tx-r-month'] option:checked")).toHaveText(prior.words.split(" ")[0] ?? "");
  await page.getByRole("button", { name: "Reconcile", exact: true }).click();
  const result = page.locator("[data-recon-result]");
  await expect(result.locator("[data-collected]")).toHaveText("$4,823.75");
  await expect(result.locator("[data-ledger]")).toHaveText("$4,823.75");
  await expect(result).toContainText("Tied out");
  await expect(page.locator("[data-recon-words]")).toContainText(`${prior.words}: The ledger holds the tax the sales collected.`);
  // The month before that is off, and the direction is in words.
  await page.getByRole("textbox", { name: "Year", exact: true }).fill(String(older.year));
  await page.locator("[data-field='tx-r-month']").selectOption({ index: older.month });
  await page.getByRole("button", { name: "Reconcile", exact: true }).click();
  await expect(result.locator("[data-collected]")).toHaveText("$3,912.50");
  await expect(result.locator("[data-ledger]")).toHaveText("$3,899.00");
  await expect(result).toContainText("Off by $13.50");
  await expect(page.locator("[data-recon-words]")).toContainText(`${older.words}: The ledger holds less tax than the sales collected.`);
  // A whole year with nothing in it is tied out at zero, not a blank.
  await page.getByRole("textbox", { name: "Year", exact: true }).fill("1999");
  await page.locator("[data-field='tx-r-month']").selectOption({ label: "Whole year" });
  await page.getByRole("button", { name: "Reconcile", exact: true }).click();
  await expect(page.locator("[data-recon-words]")).toContainText("1999: The ledger holds the tax the sales collected.");
  await expect(result.locator("[data-collected]")).toHaveText("$0.00");
  // A year that is not a year is held.
  await page.getByRole("textbox", { name: "Year", exact: true }).fill("99");
  await expect(page.locator("[data-year-hint]")).toContainText("Enter a four digit year.");
  await page.getByRole("button", { name: "Reconcile", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("Enter a four digit year. Nothing was sent.");
  await noOldWords(page);
});

test("the recompute asks first, adds once, and a second run says nothing new; a bad range is held", async ({ page, request }) => {
  await openTax(page, request);
  await tab(page, "checks");
  const prior = priorMonth();
  const posts: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/api/v1/tax/recompute")) posts.push(r.postData() ?? "");
  });
  await expect(page.getByRole("textbox", { name: "Recompute from" })).toHaveValue(prior.first);
  await expect(page.getByRole("textbox", { name: "Recompute to" })).toHaveValue(prior.last);
  const go = page.getByRole("button", { name: "Recompute", exact: true });
  await page.getByRole("textbox", { name: "Recompute to" }).fill("next week");
  await go.click();
  await expect(page.locator("[data-error]")).toContainText("Give both ends of the range as real days");
  await page.getByRole("textbox", { name: "Recompute to" }).fill(prior.first);
  await page.getByRole("textbox", { name: "Recompute from" }).fill(prior.last);
  await go.click();
  await expect(page.locator("[data-error]")).toContainText("The range cannot end before it starts. Nothing was sent.");
  expect(posts).toEqual([]);
  await page.getByRole("textbox", { name: "Recompute from" }).fill(prior.first);
  await page.getByRole("textbox", { name: "Recompute to" }).fill(prior.last);
  await go.click();
  const confirm = page.locator("[data-confirm='recompute']");
  await expect(confirm).toContainText(`Recompute sales from ${dayWords(prior.first)} to ${dayWords(prior.last)}?`);
  await expect(confirm).toContainText("Nothing is rewritten or removed, and the ledger is not corrected.");
  await expect(confirm).toContainText("Running it again adds nothing new.");
  expect(posts).toEqual([]);
  await confirm.getByRole("button", { name: "Not yet" }).click();
  expect(posts).toEqual([]);
  await go.click();
  await page.locator("[data-confirm='recompute']").getByRole("button", { name: "Recompute" }).click();
  const result = page.locator("[data-recompute-result]");
  await expect(result).toContainText("Recomputed.");
  await expect(result.locator("[data-scanned]")).toHaveText("6");
  await expect(result.locator("[data-inserted]")).toHaveText("6");
  await expect(result.locator("[data-skipped]")).toHaveText("0");
  expect(JSON.parse(posts[0] ?? "{}")).toEqual({ from_date: prior.first, to_date: prior.last });
  await go.click();
  await page.locator("[data-confirm='recompute']").getByRole("button", { name: "Recompute" }).click();
  await expect(page.locator("[data-recompute-result]")).toContainText("Nothing new: every sale in range was already recomputed.");
  await expect(page.locator("[data-recompute-result] [data-inserted]")).toHaveText("0");
  await expect(page.locator("[data-recompute-result] [data-skipped]")).toHaveText("6");
  // A wall and a server fault are each in words.
  await page.route("**/api/v1/tax/recompute", (route) => route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }));
  await go.click();
  await page.locator("[data-confirm='recompute']").getByRole("button", { name: "Recompute" }).click();
  await expect(page.locator("[data-error]")).toContainText("Recomputing past sales is for the Financial Controller.");
  await page.unroute("**/api/v1/tax/recompute");
  await page.route("**/api/v1/tax/recompute", (route) => route.abort("failed"));
  await go.click();
  await page.locator("[data-confirm='recompute']").getByRole("button", { name: "Recompute" }).click();
  await expect(page.locator("[data-error]")).toContainText("Running it again is safe: it adds nothing that is already there.");
});

test("while the tables load the screen says so, a failed load offers Try again, and a wall on the read is named", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  let release: () => void = () => undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/tax/rates", async (route) => {
    await gate;
    await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal error" }) });
  });
  await page.goto("/");
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await expect(page.locator("[data-loading]")).toContainText("Loading the tax tables.");
  release();
  await expect(page.locator("[data-error]")).toContainText("The tax service did not answer. Reload to try again.");
  await page.unroute("**/api/v1/tax/rates");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-rate-id]")).toHaveCount(3);
  // A wall on the read.
  await page.route("**/api/v1/tax/config", (route) => route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "Forbidden" }) }));
  await page.reload();
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("Tax administration is for a manager or the Financial Controller.");
});

test("an empty world says so on each tab, and a list that is not the shape the core sends is refused, never half read", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  const empty = (key: string) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ [key]: null }) });
  await page.route("**/api/v1/tax/rates", (route) => route.fulfill(empty("rates")));
  await page.route("**/api/v1/tax/profiles", (route) => route.fulfill(empty("profiles")));
  await page.route("**/api/v1/tax/review-queue", (route) => route.fulfill(empty("pending_profiles")));
  await page.route("**/api/v1/tax/evidence", (route) => (route.request().method() === "GET" ? route.fulfill(empty("evidence")) : route.continue()));
  await page.goto("/");
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await expect(page.locator("[data-empty='rates']")).toContainText("No rate is on file.");
  await expect(page.locator("[data-empty='rates']")).toContainText("Without a standard rate in force the till refuses to ring a taxable sale.");
  await tab(page, "profiles");
  await expect(page.locator("[data-empty='queue']")).toContainText("Nothing is waiting.");
  await expect(page.locator("[data-empty='profiles']")).toContainText("No exemption profile is on file.");
  await tab(page, "certificates");
  await expect(page.locator("[data-empty='evidence']")).toContainText("No certificate is on file.");
  // A reply that is neither a list nor a named list.
  await page.unroute("**/api/v1/tax/rates");
  await page.route("**/api/v1/tax/rates", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ surprise: [1] }) }));
  await page.getByRole("button", { name: "Receivables", exact: true }).click();
  await page.reload();
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await expect(page.locator("[data-error]")).toContainText("The rates were not readable.");
  await expect(page.locator("[data-rate-id]")).toHaveCount(0);
});

test("on a phone every tab becomes cards and none scrolls the page sideways", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openTax(page, request);
  const sideways = () => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(await sideways()).toBe(false);
  await expect(page.locator("[data-rate-card]")).toHaveCount(3);
  await expect(page.locator("table[aria-label='Rate table']")).toBeHidden();
  await expect(page.locator("[data-rate-card]").first()).toBeVisible();
  for (const id of ["profiles", "certificates", "settings", "checks"] as const) {
    await tab(page, id);
    await expect(page.locator(`[data-tab-id='${id}']`)).toHaveAttribute("aria-current", "true");
    expect(await sideways(), `${id} scrolls sideways`).toBe(false);
  }
  await tab(page, "profiles");
  await expect(page.locator("[data-profile-card]")).toHaveCount(3);
  await tab(page, "certificates");
  await expect(page.locator("[data-evidence-card]")).toHaveCount(4);
  // A chosen record brings its panel into view.
  await page.locator("[data-evidence-card]").last().getByRole("button", { name: "Open the record" }).click();
  await expect(page.locator("[data-evidence-panel]")).toBeInViewport();
  expect(await sideways()).toBe(false);
  // The confirm step is in view when it asks.
  await tab(page, "checks");
  await page.getByRole("button", { name: "Recompute", exact: true }).click();
  await expect(page.locator("[data-confirm='recompute']")).toBeInViewport();
});

test("every control works from the keyboard and focus lands on the confirm step", async ({ page, request }) => {
  await openTax(page, request);
  await page.getByRole("textbox", { name: "Rate, percent" }).fill("8");
  await page.getByRole("button", { name: "Add the rate", exact: true }).focus();
  await page.keyboard.press("Enter");
  const go = page.locator("[data-confirm='rate']").getByRole("button", { name: "Add the rate" });
  await expect(go).toBeFocused();
  await page.keyboard.press("Tab");
  await expect(page.locator("[data-confirm='rate']").getByRole("button", { name: "Not yet" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
  // The tabs are buttons reached in order, and Enter opens one.
  await page.locator("[data-tab-id='settings']").focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("textbox", { name: "Seller's permit number" })).toBeVisible();
  await page.getByRole("radio", { name: /Round each line/ }).focus();
  await page.keyboard.press("Space");
  await expect(page.getByRole("radio", { name: /Round each line/ })).toHaveAttribute("aria-checked", "true");
  // Focus is visible on a control.
  const outline = await page.getByRole("button", { name: "Save the settings", exact: true }).evaluate((el) => {
    (el as HTMLElement).focus();
    return getComputedStyle(el).outlineStyle;
  });
  expect(outline).not.toBe("none");
});
