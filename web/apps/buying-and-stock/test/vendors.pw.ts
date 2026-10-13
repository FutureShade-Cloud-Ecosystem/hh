// The vendors area walked in a real browser against the stub: the list and its search, a new
// vendor, a correction, the ordering contact, the vendor's own codes, the lookup of an order a
// vendor quoted, and every refusal in the core's words. The stub is held to the core's shapes and
// sentences by test/vendors-contract.test.mjs.
import { test, expect, openArea, stubRole, lowContrast, noSidewaysScroll } from "./helpers.js";

const row = (page: import("@playwright/test").Page, name: string) => page.locator("[data-vendor-row]", { hasText: name });

test("the area opens on the vendors, active ones first, and the search narrows them", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await expect(page.getByRole("heading", { name: "Vendors", level: 1 })).toBeVisible();
  await expect(page.locator("[data-vendor-row]")).toHaveCount(4);
  await expect(row(page, "Alcli")).toContainText("V-ALC");
  await expect(row(page, "Alcli")).toContainText("30 days");
  await expect(row(page, "Alcli")).toContainText("Active");
  await expect(page.locator("[data-vendor-empty]")).toContainText("Pick a vendor.");
  await page.locator("[data-v-query]").fill("bloc");
  await expect(page.locator("[data-vendor-row]")).toHaveCount(1);
  await expect(row(page, "Techo Bloc")).toBeVisible();
  await page.locator("[data-v-query]").fill("nothing like it");
  await expect(page.locator("[data-empty]").first()).toContainText("No vendor matches that.");
});

test("a new vendor is added with the number the core minted, and shows in the list", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await page.getByRole("button", { name: "New vendor" }).click();
  await page.getByRole("button", { name: "Add the vendor" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Vendor name is required.");
  await page.locator("[data-v-name]").fill("Rymar Turf (demo)");
  await page.locator("[data-v-terms]").fill("45");
  await page.locator("[data-v-email]").fill("orders@rymar.test");
  await page.getByRole("button", { name: "Add the vendor" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Added Rymar Turf (demo).");
  await expect(page.locator("[data-notice]")).toContainText("Its number is V-0101.");
  await expect(page.locator("[data-vendor-row]")).toHaveCount(5);
  await expect(row(page, "Rymar Turf")).toContainText("45 days");
  await expect(page.locator("[data-vendor-panel] h2")).toHaveText("Rymar Turf (demo)");
  await expect(page.locator("[data-email-note]")).toHaveText("On file: orders@rymar.test");
});

test("a correction sends only what changed, says when nothing did, and an inactive vendor leaves the list", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await row(page, "Alcli").getByRole("button", { name: "Alcli (demo)" }).click();
  await expect(page.locator("[data-v-name]")).toHaveValue("Alcli (demo)");
  await expect(page.locator("[data-v-terms]")).toHaveValue("30");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Nothing changed on this vendor yet.");
  await page.locator("[data-v-terms]").fill("366");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator("[data-error]")).toContainText("a whole number of days from 0 to 365");
  await page.locator("[data-v-terms]").fill("45");
  await page.getByRole("button", { name: "Save changes" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Saved changes to Alcli (demo).");
  await expect(row(page, "Alcli")).toContainText("45 days");
  await page.getByRole("button", { name: "Mark inactive" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Alcli (demo) is inactive: it stays on its past orders.");
  await expect(page.locator("[data-vendor-row]")).toHaveCount(3);
  await page.getByRole("checkbox", { name: "Show inactive vendors" }).click();
  await expect(page.locator("[data-vendor-row]")).toHaveCount(4);
  await expect(row(page, "Alcli")).toContainText("Inactive");
});

test("the ordering contact is tagged, needs a way to reach, and is removed behind a confirm step", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await row(page, "Unilock").getByRole("button", { name: "Unilock (demo)" }).click();
  await page.getByRole("button", { name: "Ordering contact" }).click();
  await expect(page.locator("[data-contact]")).toContainText("Nobody is tagged for ordering at this vendor yet.");
  await page.locator("[data-c-name]").fill("Orders desk");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-error]")).toContainText("needs an email or a phone");
  await page.locator("[data-c-email]").fill("no at sign");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-error]")).toContainText("not an address");
  await page.locator("[data-c-email]").fill("po@unilock.test");
  await page.getByRole("button", { name: "Tag the contact" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The ordering contact is tagged.");
  await expect(page.locator("[data-contact-on-file]")).toContainText("po@unilock.test");
  await page.getByRole("button", { name: "Remove the contact" }).first().click();
  await expect(page.locator("[data-confirm]")).toContainText("Remove the ordering contact for this vendor?");
  await page.getByRole("button", { name: "Go back" }).click();
  await expect(page.locator("[data-contact-on-file]")).toBeVisible();
  await page.getByRole("button", { name: "Remove the contact" }).first().click();
  await page.getByRole("button", { name: "Remove the contact" }).last().click();
  await expect(page.locator("[data-notice]")).toContainText("The ordering contact is removed.");
  await expect(page.locator("[data-contact-on-file]")).toHaveCount(0);
});

test("a vendor's own codes: add, correct, refuse a half filled unit, and remove behind a confirm step", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await row(page, "Alcli").getByRole("button", { name: "Alcli (demo)" }).click();
  await page.getByRole("button", { name: "Vendor codes" }).click();
  await expect(page.locator("[data-code-row]", { hasText: "DBS-BRUS-DIM" })).toContainText("ALC-BRU-DIM-60");
  const rows = page.locator("[data-code-row]");
  const before = await rows.count();
  await page.getByRole("button", { name: "Add a code" }).click();
  await page.locator("[data-m-product]").selectOption({ label: "TB-PAVER-60 Techo Bloc Paver 60mm" });
  await page.locator("[data-m-code]").fill("ALC-PAV-9");
  await page.locator("[data-m-unit]").fill("bdl");
  await page.getByRole("button", { name: "Save the code" }).click();
  await expect(page.locator("[data-error]")).toContainText("A vendor unit needs the number of our units it holds");
  await page.locator("[data-m-factor]").fill("12.5");
  await page.getByRole("button", { name: "Save the code" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The vendor code is saved.");
  await expect(rows).toHaveCount(before + 1);
  const added = page.locator("[data-code-row]", { hasText: "TB-PAVER-60" });
  await expect(added).toContainText("ALC-PAV-9");
  await expect(added).toContainText("BDL, 12.5 of ours");
  await added.getByRole("button", { name: "Change" }).click();
  await expect(page.locator("[data-m-code]")).toHaveValue("ALC-PAV-9");
  await page.locator("[data-m-code]").fill("ALC-PAV-10");
  await page.getByRole("button", { name: "Save the code" }).click();
  await expect(page.locator("[data-code-row]", { hasText: "TB-PAVER-60" })).toContainText("ALC-PAV-10");
  await expect(rows).toHaveCount(before + 1);
  await page.locator("[data-code-row]", { hasText: "TB-PAVER-60" }).getByRole("button", { name: "Remove" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("Remove the code ALC-PAV-10 for TB-PAVER-60?");
  await page.getByRole("button", { name: "Remove the code" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The vendor code is removed.");
  await expect(rows).toHaveCount(before);
});

test("the order a vendor quoted is found however it was written, and a miss reads as one", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await page.getByRole("button", { name: "Find the order" }).click();
  await expect(page.locator("[data-lookup-error]")).toHaveText("Type the order number the vendor quoted.");
  await page.locator("[data-v-ref]").fill("#po trt 66");
  await page.getByRole("button", { name: "Find the order" }).click();
  const found = page.locator("[data-order-found]");
  await expect(found).toContainText("PO-TRT-66");
  await expect(found).toContainText("Alcli (demo)");
  await expect(found).toContainText("Received");
  await page.locator("[data-v-ref]").fill("PO-9999");
  await page.getByRole("button", { name: "Find the order" }).click();
  await expect(page.locator("[data-lookup-error]")).toHaveText("Not found.");
  await expect(found).toHaveCount(0);
});

test("a role the wall does not admit is refused in words naming who may, and nothing changes", async ({ page, request }) => {
  await openArea(page, request, "Vendors");
  await stubRole(request, "payables-receivables");
  await page.getByRole("button", { name: "New vendor" }).click();
  await page.locator("[data-v-name]").fill("Nobody Yet");
  await page.getByRole("button", { name: "Add the vendor" }).click();
  await expect(page.locator("[data-error]")).toContainText("Adding and changing vendors, their contacts and their codes is for the Procurement Manager");
  await expect(page.locator("[data-vendor-row]")).toHaveCount(4);
});

test("every text is 4.5 to 1 or better in both themes: the list, a vendor, its contact, its codes", async ({ page, request }) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openArea(page, request, "Vendors");
  for (const theme of ["Theme: Light", "Theme: Brand"]) {
    if (theme === "Theme: Brand") await page.getByRole("button", { name: "Theme: Light" }).click();
    expect(await lowContrast(page), `${theme}, list`).toEqual([]);
    await row(page, "Alcli").getByRole("button", { name: "Alcli (demo)" }).click();
    expect(await lowContrast(page), `${theme}, details`).toEqual([]);
    await page.getByRole("button", { name: "Ordering contact" }).click();
    await expect(page.locator("[data-contact]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, contact`).toEqual([]);
    await page.getByRole("button", { name: "Vendor codes" }).click();
    await expect(page.locator("[data-codes]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, codes`).toEqual([]);
    await page.getByRole("button", { name: "Close" }).click();
  }
});

test("at 390 px the page never scrolls sideways: the list is cards and the vendor stacks beneath it", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openArea(page, request, "Vendors");
  await noSidewaysScroll(page);
  await row(page, "Alcli").getByRole("button", { name: "Alcli (demo)" }).click();
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "Vendor codes" }).click();
  await expect(page.locator("[data-codes]")).toBeVisible();
  await noSidewaysScroll(page);
});
