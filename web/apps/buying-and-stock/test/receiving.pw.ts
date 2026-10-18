// The receiving area walked in a real browser against the stub: the queue of orders to receive, a
// receive behind its confirm step, the receipts, a landed cost attached to a receipt, the order's
// document as a file, and the send to the vendor, with every refusal in the core's words and the
// two answers that can be lost on the way back. The stub is held to the core's shapes and sentences
// by test/receiving-contract.test.mjs.
import type { Page } from "@playwright/test";
import { test, expect, openArea, stubRole, lowContrast, noSidewaysScroll } from "./helpers.js";

const P = "/api/v1/purchasing";
const auth = { Authorization: "Bearer x" };

async function openOrder(page: Page, no: string): Promise<void> {
  await page.locator("[data-queue-row]", { hasText: no }).getByRole("button", { name: no }).click();
  await expect(page.locator("[data-order-panel] h2")).toHaveText(no);
}

const recv = (page: Page, n = 0) => page.locator("[data-recv]").nth(n);

test("the area opens on the orders waiting to arrive, soonest first, with the order panel empty", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await expect(page.getByRole("heading", { name: "Receiving", level: 1 })).toBeVisible();
  const rows = page.locator("[data-queue-row]");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("PO-PLM-141");
  await expect(rows.nth(0)).toContainText("in 2 days");
  await expect(rows.nth(0)).toContainText("$9,840.00");
  await expect(rows.nth(1)).toContainText("PO-PLM-136");
  await expect(rows.nth(2)).toContainText("PO-PLM-67");
  await expect(rows.nth(2)).toContainText("no date");
  await expect(page.locator("[data-order-empty]")).toContainText("Pick an order.");
});

test("goods are received behind a confirm step, the order moves to Partial, then Received and leaves the queue", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  await expect(page.locator("[data-order-line]")).toHaveCount(1);
  await expect(page.locator("[data-order-line]")).toContainText("Alcli Step 48in");
  await expect(page.locator("[data-no-receipts]")).toBeVisible();

  await recv(page).fill("4");
  await page.locator("[data-recv-notes]").fill("one pallet short");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Receive 4 Alcli Step 48in against PO-PLM-136.");
  await expect(confirm).toContainText("moves the goods into stock at the order's cost and cannot be undone here");
  // Going back changes nothing and keeps what was typed.
  await page.getByRole("button", { name: "Go back" }).click();
  await expect(recv(page)).toHaveValue("4");
  await expect(page.locator("[data-recv-notes]")).toHaveValue("one pallet short");
  await expect(page.locator("[data-receipt-row]")).toHaveCount(0);
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();

  await expect(page.locator("[data-notice]")).toContainText("Received against PO-PLM-136: receipt PO-PLM-136/R1.");
  await expect(page.locator("[data-notice]")).toContainText("The order reads partial now");
  await expect(page.locator("[data-order-panel] [data-status]").first()).toHaveText("Partial");
  await expect(page.locator("[data-order-line] td[data-label='Received']")).toHaveText("4");
  await expect(page.locator("[data-order-line] td[data-label='Outstanding']")).toHaveText("6");
  await expect(page.locator("[data-receipt-row]")).toHaveCount(1);
  await expect(page.locator("[data-receipt-row]")).toContainText("PO-PLM-136/R1");
  await expect(page.locator("[data-receipt-row]")).toContainText("$100.00");
  await expect(page.locator("[data-receipt-row] td[data-label='Received']")).toHaveText("today");
  await expect(recv(page)).toHaveValue("");
  await expect(page.locator("[data-queue-row]", { hasText: "PO-PLM-136" })).toContainText("Partial");

  await recv(page).fill("6");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await expect(page.locator("[data-order-panel] [data-status]").first()).toHaveText("Received");
  await expect(page.locator("[data-not-receivable]")).toHaveText("This purchase order is already fully received.");
  await expect(page.locator("[data-queue-row]", { hasText: "PO-PLM-136" })).toHaveCount(0);
  await expect(page.locator("[data-receipt-row]")).toHaveCount(2);
});

test("a count that cannot be right is refused in the core's words before anything is sent", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  const ask = page.getByRole("button", { name: "Receive goods", exact: true });
  await ask.click();
  await expect(page.locator("[data-error]")).toHaveText("Write how many arrived on at least one line.");
  await recv(page).fill("abc");
  await ask.click();
  await expect(page.locator("[data-error]")).toContainText("Write how many ALC-STEP-48 arrived");
  await recv(page).fill("0");
  await ask.click();
  await expect(page.locator("[data-error]")).toContainText("Write how many ALC-STEP-48 arrived");
  await recv(page).fill("11");
  await ask.click();
  await expect(page.locator("[data-error]")).toHaveText("Over-receipt on line 1: 11.0000 requested but only 10.0000 outstanding.");
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
  await expect(page.locator("[data-receipt-row]")).toHaveCount(0);
});

test("the core's own refusal is shown when the order moved while the screen was open", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  // The yard team receives it all in Yard Pick and Receive while this screen still shows 10 outstanding.
  const po = (await (await request.get(`${P}/pos`, { headers: auth })).json()).items.find((p: { po_no: string }) => p.po_no === "PO-PLM-136");
  const detail = await (await request.get(`${P}/pos/${po.id}`, { headers: auth })).json();
  await request.post(`${P}/pos/${po.id}/receive`, { headers: auth, data: { lines: [{ po_line_id: detail.lines[0].id, qty_base: 10 }] } });
  await recv(page).fill("5");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Purchase order is already fully received.");
  await expect(page.locator("[data-confirm]")).toHaveCount(0);
});

test("an answer lost on the way back is not retried: the order is read again and the person told what it holds", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  await recv(page).fill("2");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await request.post("/_stub/flag", { data: { name: "dropNext", value: true } });
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await expect(page.locator("[data-error]")).toContainText("it is not known whether the goods were received. The order has been read again");
  await expect(page.locator("[data-order-line] td[data-label='Received']")).toHaveText("2");
  await expect(page.locator("[data-receipt-row]")).toHaveCount(1);
  await expect(recv(page)).toHaveValue("");
});

test("a landed cost is attached to a receipt behind a confirm step, once, and shows in Landed cost", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  await recv(page).fill("10");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await page.locator("[data-receipt-row]").getByRole("button", { name: "Open" }).click();
  const panel = page.locator("[data-receipt-panel]");
  await expect(panel).toContainText("Receipt PO-PLM-136/R1");
  await expect(panel.locator("tbody tr")).toHaveCount(1);
  await page.getByRole("button", { name: "Apply a landed cost" }).click();
  await expect(page.locator("[data-error]")).toContainText("Write at least one of freight, duty or handling");
  await page.locator("[data-at-freight]").fill("480.00");
  await page.locator("[data-at-handling]").fill("35.5");
  await page.getByRole("button", { name: "Apply a landed cost" }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Apply freight $480.00 and handling $35.50 to the goods on receipt PO-PLM-136/R1, by value.");
  await expect(confirm).toContainText("It is applied at once");
  await page.getByRole("button", { name: "Go back" }).click();
  await expect(page.locator("[data-at-freight]")).toHaveValue("480.00");
  await page.getByRole("button", { name: "Apply a landed cost" }).click();
  await page.getByRole("button", { name: "Apply the landed cost" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Landed cost applied to receipt PO-PLM-136/R1.");
  await expect(page.locator("[data-landed-applied]")).toBeVisible();
  await expect(page.locator("[data-receipt-row]")).toContainText("Applied");
  await expect(page.locator("[data-attach]")).toHaveCount(0);
  // It is in the landed costs, applied, against the receipt.
  await page.locator("nav.areas").getByRole("button", { name: "Landed cost" }).click();
  const row = page.locator("[data-landed-row]", { hasText: "PO-PLM-136/R1" });
  await expect(row).toContainText("Applied");
  await expect(row).toContainText("$480.00");
});

test("the order document downloads as a spreadsheet and to print, under the order's own name", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-141");
  const csv = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download as a spreadsheet" }).click();
  const file = await csv;
  expect(file.suggestedFilename()).toBe("PO-PLM-141.csv");
  const text = await (await import("node:fs/promises")).readFile((await file.path()) ?? "", "utf8");
  expect(text).toContain("Purchase order,PO-PLM-141");
  expect(text).toContain("Unit cost");
  const printed = page.waitForEvent("download");
  await page.getByRole("button", { name: "Download to print" }).click();
  expect((await printed).suggestedFilename()).toBe("PO-PLM-141.html");
});

test("a send goes to the tagged desk behind a confirm step and the history keeps it", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  const po = (await (await request.get(`${P}/pos`, { headers: auth })).json()).items.find((p: { po_no: string }) => p.po_no === "PO-PLM-141");
  await request.put(`${P}/vendors/${po.vendor_id}/contact`, { headers: auth, data: { contact_name: "Orders desk", email: "po@techo.test" } });
  await openOrder(page, "PO-PLM-141");
  await expect(page.locator("[data-no-sends]")).toBeVisible();
  await page.getByRole("button", { name: "Send to the vendor" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("Send PO-PLM-141 to Orders desk, po@techo.test.");
  await page.getByRole("button", { name: "Go back" }).click();
  await expect(page.locator("[data-send-row]")).toHaveCount(0);
  await page.getByRole("button", { name: "Send to the vendor" }).click();
  await page.getByRole("button", { name: "Send the order" }).click();
  await expect(page.locator("[data-send-result]")).toHaveText("Sent to po@techo.test.");
  await expect(page.locator("[data-send-row]")).toHaveCount(1);
  await expect(page.locator("[data-send-row]")).toContainText("Tagged ordering contact");
  await expect(page.locator("[data-send-row]")).toContainText("Delivered");
  await expect(page.locator("[data-send-row] td[data-label='Sent']")).toHaveText("today");
});

test("with nobody tagged the fallback address is asked for, and a delivery that fails is said plainly", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  const po = (await (await request.get(`${P}/pos`, { headers: auth })).json()).items.find((p: { po_no: string }) => p.po_no === "PO-PLM-141");
  await request.put(`${P}/vendors/${po.vendor_id}`, { headers: auth, data: { order_email: "orders@techo.test" } });
  await request.post("/_stub/flag", { data: { name: "sendFails", value: true } });
  await openOrder(page, "PO-PLM-141");
  await page.getByRole("button", { name: "Send to the vendor" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("Nobody is tagged for ordering at Techo Bloc (demo).");
  await page.getByRole("button", { name: "Send the order" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("The vendor record has one address. Send PO-PLM-141 to it?");
  await page.getByRole("button", { name: "Send to the address on file" }).click();
  await expect(page.locator("[data-send-result]")).toHaveText("Not delivered to orders@techo.test: no mail transport is wired. The attempt is on the record as failed.");
  await expect(page.locator("[data-send-row]")).toContainText("Failed");
  await expect(page.locator("[data-send-row]")).toContainText("Address on the vendor record");
});

test("a send whose answer was lost is the same attempt when pressed again, so the order goes once", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  const po = (await (await request.get(`${P}/pos`, { headers: auth })).json()).items.find((p: { po_no: string }) => p.po_no === "PO-PLM-141");
  await request.put(`${P}/vendors/${po.vendor_id}/contact`, { headers: auth, data: { email: "po@techo.test" } });
  await openOrder(page, "PO-PLM-141");
  await page.getByRole("button", { name: "Send to the vendor" }).click();
  await request.post("/_stub/flag", { data: { name: "dropNext", value: true } });
  await page.getByRole("button", { name: "Send the order" }).click();
  await expect(page.locator("[data-error]")).toContainText("Press Send again: it is the same attempt, so it cannot go twice.");
  await page.getByRole("button", { name: "Send to the vendor" }).click();
  await page.getByRole("button", { name: "Send the order" }).click();
  await expect(page.locator("[data-send-result]")).toHaveText("Sent to po@techo.test.");
  await expect(page.locator("[data-send-row]")).toHaveCount(1);
});

test("the manager's queue keeps the Value open column, as the ruling kept it", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  // Change 5, R5: the queue's Value open column is the managers'; where cost is visible it stays.
  const heads = (await page.locator("[data-queue] th").allTextContents()).map((t) => t.trim());
  expect(heads).toContain("Value open");
  await expect(page.locator("[data-queue-row]").first().locator("td[data-label='Value open']")).toContainText("$");
});

test("where the policy hides cost the screen says so, shows no price or value, and draws no value column", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  // Change 5, R5: where the policy hides cost the column is not drawn at all, never a stub cell.
  await request.post("/_stub/flag", { data: { name: "costsHidden", value: true } });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(page.locator("[data-queue] th", { hasText: "Value open" })).toHaveCount(0);
  await expect(page.locator("[data-queue-row]").first().locator("td[data-label='Value open']")).toHaveCount(0);
  await openOrder(page, "PO-PLM-141");
  await expect(page.locator("[data-costs-hidden]")).toContainText("Cost is hidden for your role by the purchasing policy");
  await expect(page.locator("[data-order-value]")).toHaveCount(0);
  await expect(page.locator("[data-order-lines] th", { hasText: "Unit cost" })).toHaveCount(0);
});

test("a role the wall does not admit is refused in words naming who may receive", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  await stubRole(request, "payables-receivables");
  await recv(page).fill("1");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await expect(page.locator("[data-error]")).toContainText("Receiving goods is for the Yard Manager");
  await expect(page.locator("[data-receipt-row]")).toHaveCount(0);
});

test("a caller with no branch reach reads the core's refusal and can try again", async ({ page, request }) => {
  await request.post("/_stub/reset");
  await request.post("/_stub/fence");
  await page.goto("/");
  await page.locator('[data-flow="procurement-manager"]').click();
  await page.locator("nav.areas").getByRole("button", { name: "Receiving" }).click();
  await expect(page.locator("[data-error]")).toContainText("Caller cannot access this branch.");
  await request.post("/_stub/reset");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-queue-row]")).toHaveCount(3);
});

test("every text is 4.5 to 1 or better in both themes: the queue, an order, a receipt, the confirm step", async ({ page, request }) => {
  test.setTimeout(90_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openArea(page, request, "Receiving");
  for (const theme of ["Theme: Light", "Theme: Brand"]) {
    if (theme === "Theme: Brand") await page.getByRole("button", { name: "Theme: Light" }).click();
    expect(await lowContrast(page), `${theme}, queue`).toEqual([]);
    await openOrder(page, "PO-PLM-136");
    expect(await lowContrast(page), `${theme}, order`).toEqual([]);
    await recv(page).fill("2");
    await page.getByRole("button", { name: "Receive goods", exact: true }).click();
    expect(await lowContrast(page), `${theme}, confirm`).toEqual([]);
    await page.getByRole("button", { name: "Receive the goods" }).click();
    await expect(page.locator("[data-receipt-row]")).toHaveCount(1);
    await page.locator("[data-receipt-row]").getByRole("button", { name: "Open" }).click();
    await expect(page.locator("[data-receipt-panel]")).toBeVisible();
    expect(await lowContrast(page), `${theme}, receipt`).toEqual([]);
    await request.post("/_stub/reset");
    await page.reload();
    await page.locator("nav.areas").getByRole("button", { name: "Receiving" }).click();
    await expect(page.locator("[data-queue-row]").first()).toBeVisible();
  }
});

test("at 390 px the page never scrolls sideways: the queue is cards, the order and the receipt stack", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openArea(page, request, "Receiving");
  await noSidewaysScroll(page);
  await openOrder(page, "PO-PLM-136");
  await noSidewaysScroll(page);
  await recv(page).fill("2");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await page.locator("[data-receipt-row]").getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-receipt-panel]")).toBeVisible();
  await noSidewaysScroll(page);
});

test("a landed cost attach whose answer was lost never advises applying again", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  await openOrder(page, "PO-PLM-136");
  await recv(page).fill("10");
  await page.getByRole("button", { name: "Receive goods", exact: true }).click();
  await page.getByRole("button", { name: "Receive the goods" }).click();
  await page.locator("[data-receipt-row]").getByRole("button", { name: "Open" }).click();
  await page.locator("[data-at-freight]").fill("480.00");
  await page.getByRole("button", { name: "Apply a landed cost" }).click();
  await request.post("/_stub/flag", { data: { name: "dropNext", value: true } });
  await page.getByRole("button", { name: "Apply the landed cost" }).click();
  const error = page.locator("[data-error]");
  await expect(error).toContainText("check Landed cost for one on PO-PLM-136/R1");
  await expect(error).not.toContainText("apply it again");
});

test("the queue's Expected cell never truncates: the relative day word reads whole, on a nowrap floor", async ({ page, request }) => {
  await openArea(page, request, "Receiving");
  // PO-PLM-141 ships "in 2 days", PO-PLM-67 has no date: the first row must carry the full word.
  const cell = page.locator("[data-queue-row]").first().locator("td[data-expected-col]");
  await expect(cell).toHaveText("in 2 days");
  // The CSS floor: nowrap + min-width 6.5rem, so the cell cannot be crushed past its longest word.
  const floor = await cell.evaluate((el) => {
    const cs = window.getComputedStyle(el);
    return { wrap: cs.whiteSpace, min: cs.minWidth, scrollW: el.scrollWidth, clientW: el.clientWidth };
  });
  expect(floor.wrap).toBe("nowrap");
  expect(floor.min).toMatch(/^\d+(?:\.\d+)?(?:px|rem|em)$/);
  const minPx = parseFloat(floor.min);
  expect(minPx).toBeGreaterThanOrEqual(6.5 * 12);
  // The full cell width holds the whole phrase without horizontal overflow inside the cell.
  expect(floor.scrollW).toBeLessThanOrEqual(floor.clientW + 1);
});
