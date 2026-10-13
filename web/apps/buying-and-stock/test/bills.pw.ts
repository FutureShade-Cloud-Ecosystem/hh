// The bills area walked in a real browser against the stub: the list with the three way match
// beside each bill, a bill line by line, a bill entered against an order, the post behind its
// confirm step, the variance that needs an acceptance and a note, the match rules, and every
// refusal in the core's words. The stub is held to the core's shapes, arithmetic and sentences by
// test/bills-contract.test.mjs.
import type { Page } from "@playwright/test";
import { test, expect, openArea, openAreaAs, AP, SUPER, lowContrast, noSidewaysScroll } from "./helpers.js";

const row = (page: Page, no: string) => page.locator("[data-bill-row]", { hasText: no });
const open = async (page: Page, no: string): Promise<void> => {
  await row(page, no).getByRole("button", { name: no }).click();
  await expect(page.locator("[data-bill-detail] h2")).toHaveText(`Bill ${no}`);
};

test("the procurement seat reads the bills and the match, and is offered no control to enter or post", async ({ page, request }) => {
  await openArea(page, request, "Vendor bills");
  await expect(page.getByRole("heading", { name: "Vendor bills", level: 1 })).toBeVisible();
  await expect(page.locator("[data-bill-row]")).toHaveCount(3);
  await expect(row(page, "INV-7790")).toContainText("Variance");
  await expect(row(page, "INV-7790")).toContainText("Draft");
  await expect(row(page, "INV-7790")).toContainText("$2,620.00");
  await expect(row(page, "INV-7795")).toContainText("Short billed");
  await expect(row(page, "INV-7781")).toContainText("Matched");
  await expect(row(page, "INV-7781")).toContainText("Posted");
  await expect(page.locator("[data-tile='to-post']")).toContainText("2");
  await expect(page.locator("[data-tile='variance']")).toContainText("1");
  await expect(page.locator("[data-tile='posted']")).toContainText("1");
  await expect(page.getByRole("button", { name: "Enter a bill" })).toHaveCount(0);
  await expect(page.locator("[data-bills-note]")).toContainText("Entering and posting a vendor bill is for Payables and Receivables");
  await open(page, "INV-7790");
  await expect(page.getByRole("button", { name: "Post the bill" })).toHaveCount(0);
  await expect(page.locator("[data-bills-note]")).toContainText("Posting a vendor bill is for Payables and Receivables.");
});

test("a bill reads line by line against the order and the receipt, the variance said from the core's figures", async ({ page, request }) => {
  await openArea(page, request, "Vendor bills");
  await open(page, "INV-7790");
  const line = page.locator("[data-match-line]");
  await expect(line).toHaveCount(1);
  await expect(line).toContainText("UNI-WALL-CAP");
  await expect(line.locator("td[data-label='Billed']")).toHaveText("10");
  await expect(line.locator("td[data-label='Received']")).toHaveText("6");
  await expect(line.locator("td[data-label='Order price']")).toHaveText("$240.00");
  await expect(line.locator("td[data-label='Billed price']")).toHaveText("$262.00");
  await expect(line).toContainText("Variance");
  await expect(line.locator("[data-qty-variance]")).toHaveText("4 billed that have not arrived");
  await expect(line.locator("[data-price-variance]")).toHaveText("$220.00 over the order's price");
  await expect(page.locator("[data-total]")).toHaveText("$2,620.00");
  await expect(page.locator("[data-no-instalments]")).toBeVisible();
  await page.getByRole("button", { name: "Back to the list" }).click();
  await open(page, "INV-7795");
  await expect(page.locator("[data-qty-variance]")).toHaveText("2 received and not yet billed");
  await expect(page.locator("[data-price-variance]")).toHaveText("Price matches the order");
});

test("the list narrows by state and by match", async ({ page, request }) => {
  await openArea(page, request, "Vendor bills");
  await page.getByRole("button", { name: "Drafts" }).click();
  await expect(page.locator("[data-bill-row]")).toHaveCount(2);
  await page.getByRole("button", { name: "Variance", exact: true }).click();
  await expect(page.locator("[data-bill-row]")).toHaveCount(1);
  await expect(row(page, "INV-7790")).toBeVisible();
  await page.getByRole("button", { name: "Posted", exact: true }).click();
  await expect(page.locator("[data-empty]")).toContainText("No bill matches that.");
});

test("a bill inside the tolerance is posted behind a confirm step that names it, and then it is owed", async ({ page, request }) => {
  await openAreaAs(page, request, AP, "Vendor bills");
  await open(page, "INV-7795");
  await page.getByRole("button", { name: "Post the bill" }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Post bill INV-7795 from Unilock (demo) for $960.00 against PO-TRT-67.");
  await expect(confirm).toContainText("books the amount owed to the vendor");
  await page.getByRole("button", { name: "Go back" }).click();
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Draft");
  await page.getByRole("button", { name: "Post the bill" }).click();
  await page.getByRole("button", { name: "Post the bill" }).last().click();
  await expect(page.locator("[data-notice]")).toContainText("Posted bill INV-7795 for $960.00.");
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Posted");
  await expect(page.getByRole("button", { name: "Post the bill" })).toHaveCount(0);
  await page.getByRole("button", { name: "Back to the list" }).click();
  await expect(row(page, "INV-7795")).toContainText("Posted");
  await expect(page.locator("[data-tile='posted']")).toContainText("2");
});

test("a variance bill is refused by the core, then posted only with an acceptance and a note", async ({ page, request }) => {
  await openAreaAs(page, request, AP, "Vendor bills");
  await open(page, "INV-7790");
  await page.getByRole("button", { name: "Post the bill" }).click();
  await page.getByRole("button", { name: "Post the bill" }).last().click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("This bill is outside the tolerance.");
  await expect(confirm).toContainText("Bill INV-7790 is outside the match tolerance: the core puts the difference at $1,180.00 against the purchase order.");
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Draft");
  // An acceptance without a reason is not sent.
  await page.getByRole("button", { name: "Accept the variance and post" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Write why you are accepting the variance: the note is kept with the bill.");
  await page.locator("[data-accept-note]").fill("Vendor agreed to credit the freight on the next invoice.");
  await page.getByRole("button", { name: "Accept the variance and post" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Posted bill INV-7790 for $2,620.00.");
  await expect(page.locator("[data-notice]")).toContainText("with the variance accepted and your note kept");
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Posted");
  await expect(page.locator("[data-accepted]")).toContainText("Vendor agreed to credit the freight on the next invoice.");
});

test("a post whose answer was lost is settled by reading the bill, never sent twice", async ({ page, request }) => {
  await openAreaAs(page, request, AP, "Vendor bills");
  await open(page, "INV-7795");
  await page.getByRole("button", { name: "Post the bill" }).click();
  await request.post("/_stub/flag", { data: { name: "dropNext", value: true } });
  await page.getByRole("button", { name: "Post the bill" }).last().click();
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Posted");
  await expect(page.locator("[data-notice]")).toContainText("Posted bill INV-7795");
  await expect(page.getByRole("button", { name: "Post the bill" })).toHaveCount(0);
});

test("a bill that changed while it was open is not pressed on", async ({ page, request }) => {
  await openAreaAs(page, request, AP, "Vendor bills");
  await open(page, "INV-7795");
  await page.getByRole("button", { name: "Post the bill" }).click();
  const list = await (await request.get("/api/v1/purchasing/bills?branch_id=b0000000-0000-4000-8000-000000000001", { headers: { Authorization: "Bearer x" } })).json();
  const bill = list.items.find((b: { bill_no: string }) => b.bill_no === "INV-7795");
  await request.post(`/api/v1/purchasing/bills/${bill.id}/post`, { headers: { Authorization: "Bearer x" }, data: {} });
  await page.getByRole("button", { name: "Post the bill" }).last().click();
  await expect(page.locator("[data-error]")).toContainText("This bill changed while you had it open: it reads posted now. Nothing was sent.");
});

test("a bill is entered against an order, starting from what arrived and the order's price, matched on entry", async ({ page, request }) => {
  await openAreaAs(page, request, AP, "Vendor bills");
  await page.getByRole("button", { name: "Enter a bill" }).click();
  await page.getByRole("button", { name: "Enter the bill" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Write the vendor's invoice number.");
  await page.locator("[data-bill-no-field]").fill("INV-9001");
  await page.getByRole("button", { name: "Enter the bill" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Pick the purchase order the bill is against.");
  await page.locator("[data-bill-po]").selectOption({ label: "PO-TRT-67 · Unilock (demo)" });
  await expect(page.locator("[data-entry-line]")).toHaveCount(1);
  await expect(page.locator("[data-bq]")).toHaveValue("6");
  await expect(page.locator("[data-bc]")).toHaveValue("240.00");
  await page.locator("[data-bc]").fill("240.001");
  await page.getByRole("button", { name: "Enter the bill" }).click();
  await expect(page.locator("[data-error]")).toContainText("Write the unit price of UNI-WALL-CAP as dollars and cents");
  await page.locator("[data-bc]").fill("240.00");
  await page.locator("[data-bill-tax]").fill("31.2");
  await page.locator("[data-bill-date]").fill("2031-03-04");
  await page.locator("[data-bill-due]").fill("2031-04-03");
  await page.getByRole("button", { name: "Enter the bill" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Entered bill INV-9001 as a draft.");
  await expect(page.locator("[data-notice]")).toContainText("It matched within the tolerance. Nothing is owed until it is posted.");
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Draft");
  await expect(page.locator("[data-subtotal]")).toHaveText("$1,440.00");
  await expect(page.locator("[data-total]")).toHaveText("$1,471.20");
  await expect(page.locator("[data-facts]")).toContainText("2031-04-03");
  await page.getByRole("button", { name: "Back to the list" }).click();
  await expect(page.locator("[data-bill-row]")).toHaveCount(4);
});

test("a bill billed ahead of the goods shows as a variance as soon as it is entered", async ({ page, request }) => {
  await openAreaAs(page, request, AP, "Vendor bills");
  await page.getByRole("button", { name: "Enter a bill" }).click();
  await page.locator("[data-bill-no-field]").fill("INV-9002");
  await page.locator("[data-bill-po]").selectOption({ label: "PO-TRT-67 · Unilock (demo)" });
  await expect(page.locator("[data-bq]")).toHaveValue("6");
  await page.locator("[data-bq]").fill("10");
  await page.getByRole("button", { name: "Enter the bill" }).click();
  await expect(page.locator("[data-notice]")).toContainText("It matched outside the tolerance.");
  await expect(page.locator("[data-bill-detail] [data-status]").nth(1)).toHaveText("Match: Variance");
});

test("a seat the wall does not admit is refused in words naming who posts bills", async ({ page, request }) => {
  await openAreaAs(page, request, { code: "payables_receivables", stub: "procurement-manager" }, "Vendor bills");
  await open(page, "INV-7795");
  await page.getByRole("button", { name: "Post the bill" }).click();
  await page.getByRole("button", { name: "Post the bill" }).last().click();
  await expect(page.locator("[data-error]")).toContainText("Entering and posting a vendor bill is for Payables and Receivables");
  await expect(page.locator("[data-bill-detail] [data-status]").first()).toHaveText("Draft");
});

test("the match rules read in words, and only the super admin is offered the controls to change them", async ({ page, request }) => {
  await openArea(page, request, "Vendor bills");
  await expect(page.locator("[data-tolerance-words]")).toContainText("within 2% of what was received and its price within 1% of the order's price or $0.50, whichever allows more. This is the dealer default.");
  await expect(page.locator("[data-policy-words]")).toContainText("Cost is visible to managers and up.");
  await expect(page.locator("[data-rules-note]")).toHaveText("The rules are set by the super admin only.");
  await expect(page.locator("[data-tol-qty]")).toHaveCount(0);
});

test("the super admin changes the tolerance and the policy, each behind a confirm step", async ({ page, request }) => {
  await openAreaAs(page, request, SUPER, "Vendor bills");
  await expect(page.locator("[data-tol-qty]")).toHaveValue("2");
  await expect(page.locator("[data-tol-price]")).toHaveValue("1");
  await expect(page.locator("[data-tol-abs]")).toHaveValue("0.50");
  await page.locator("[data-tol-qty]").fill("26");
  await page.getByRole("button", { name: "Change the tolerance" }).click();
  await expect(page.locator("[data-error]")).toHaveText("A match tolerance above 25% is not a tolerance, it is an off switch.");
  await page.locator("[data-tol-qty]").fill("5");
  await page.locator("[data-tol-abs]").fill("1.00");
  await page.getByRole("button", { name: "Change the tolerance" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("Change the match tolerance to quantity 5%, price 1% and a price allowance of $1.00.");
  await page.getByRole("button", { name: "Go back" }).click();
  await page.getByRole("button", { name: "Change the tolerance" }).click();
  await page.getByRole("button", { name: "Change the tolerance" }).last().click();
  await expect(page.locator("[data-notice]")).toContainText("The match tolerance is changed.");
  await expect(page.locator("[data-tolerance-words]")).toContainText("within 5% of what was received and its price within 1% of the order's price or $1.00");

  await page.getByRole("button", { name: "Hidden from everyone" }).click();
  await page.getByRole("button", { name: "Change the policy" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("cost is visible to hidden from everyone");
  await page.getByRole("button", { name: "Change the policy" }).last().click();
  await expect(page.locator("[data-notice]")).toContainText("The purchasing policy is changed.");
  await expect(page.locator("[data-policy-words]")).toContainText("Cost is visible to hidden from everyone.");
});

test("every text is 4.5 to 1 or better in both themes: the list, a bill, the entry form, the accept step", async ({ page, request }) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openAreaAs(page, request, AP, "Vendor bills");
  for (const theme of ["Theme: Light", "Theme: Brand"]) {
    if (theme === "Theme: Brand") await page.getByRole("button", { name: "Theme: Light" }).click();
    expect(await lowContrast(page), `${theme}, list`).toEqual([]);
    await open(page, "INV-7790");
    expect(await lowContrast(page), `${theme}, bill`).toEqual([]);
    await page.getByRole("button", { name: "Post the bill" }).click();
    await page.getByRole("button", { name: "Post the bill" }).last().click();
    await expect(page.locator("[data-accept-note]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, accept`).toEqual([]);
    await page.getByRole("button", { name: "Go back" }).click();
    await page.getByRole("button", { name: "Back to the list" }).click();
    await page.getByRole("button", { name: "Enter a bill" }).click();
    await page.locator("[data-bill-po]").selectOption({ label: "PO-TRT-67 · Unilock (demo)" });
    await expect(page.locator("[data-entry-line]")).toHaveCount(1);
    expect(await lowContrast(page), `${theme}, entry`).toEqual([]);
    await page.getByRole("button", { name: "Back to the list" }).first().click();
  }
});

test("at 390 px the page never scrolls sideways: the list and the match are cards, the entry form stacks", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openAreaAs(page, request, AP, "Vendor bills");
  await noSidewaysScroll(page);
  await open(page, "INV-7790");
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "Back to the list" }).click();
  await page.getByRole("button", { name: "Enter a bill" }).click();
  await page.locator("[data-bill-po]").selectOption({ label: "PO-TRT-67 · Unilock (demo)" });
  await expect(page.locator("[data-entry-line]")).toHaveCount(1);
  await noSidewaysScroll(page);
});

test("a caller with no branch reach reads the core's refusal and can try again", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await request.post("/_stub/fence");
  await page.goto("/");
  await page.locator("[data-flow]").click();
  await page.locator("nav.areas").getByRole("button", { name: "Vendor bills" }).click();
  await expect(page.locator("[data-error]")).toContainText("Caller cannot access this branch.");
  await request.post("/_stub/reset");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-bill-row]")).toHaveCount(3);
});
