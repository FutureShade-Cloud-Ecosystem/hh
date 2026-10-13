// The manifest print and the scan trail on the stub: a manifest opens in its own window with the core's
// document and the print is recorded; a blocked window and a refused print are sentences; a trail reads
// by stage per pallet, honestly (two of three delivered is not delivered), on the load board, the day
// board and the schedule; both widths.
import { test, expect, openBoard, stubPost } from "./helpers.js";
import type { Page } from "@playwright/test";

const truck = (page: Page, no: string) => page.locator(`[data-run-no='${no}']`);
const card = (page: Page, order: string) => page.locator("li.load", { hasText: order });

test("Print manifest opens the core's document in its own window and says the print is recorded", async ({ page, request }) => {
  await openBoard(page, request);
  await page.addInitScript(() => {
    (window as unknown as { __printed: number }).__printed = 0;
  });
  const [popup] = await Promise.all([
    page.waitForEvent("popup"),
    truck(page, "RUN-PLM-1").getByRole("button", { name: "Print manifest" }).click(),
  ]);
  await popup.waitForLoadState("domcontentloaded");
  await expect(popup.locator("h1")).toHaveText("Run manifest RUN-PLM-1");
  await expect(popup.locator("table")).toContainText("ORD-0012");
  await expect(popup.locator("table")).toContainText("Fieldstone Pools and Patios");
  await expect(page.locator("[data-notice]")).toHaveText("Manifest for RUN-PLM-1 sent to the printer window, and the print is recorded.");
  await popup.close();
});

test("a manifest is printable from the day board and from the schedule too", async ({ page, request }) => {
  await openBoard(page, request);
  await page.locator("[data-tab-id='day']").first().click();
  const [first] = await Promise.all([page.waitForEvent("popup"), truck(page, "RUN-PLM-4").getByRole("button", { name: "Print manifest" }).click()]);
  await first.waitForLoadState("domcontentloaded");
  await expect(first.locator("h1")).toHaveText("Run manifest RUN-PLM-4");
  await first.close();
  await page.locator("[data-tab-id='schedule']").first().click();
  const [second] = await Promise.all([page.waitForEvent("popup"), page.locator("[data-books-run='RUN-PLM-3']").getByRole("button", { name: "Print manifest" }).click()]);
  await second.waitForLoadState("domcontentloaded");
  await expect(second.locator("h1")).toHaveText("Run manifest RUN-PLM-3");
  await second.close();
});

test("a refused print is the core's word in the lane and no window is left open", async ({ page, request }) => {
  await openBoard(page, request);
  await stubPost(request, "refuse", { status: 403, error: "dispatch: forbidden", path: "/manifest", reads: true });
  const before = page.context().pages().length;
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Print manifest" }).click();
  await expect(page.locator("[data-error]")).toHaveText("That run belongs to another yard. Its manifest is not available here.");
  await expect.poll(() => page.context().pages().length).toBe(before);
});

test("a manifest the server could not record is not printed", async ({ page, request }) => {
  await openBoard(page, request);
  await stubPost(request, "refuse", { status: 500, error: "internal error", path: "/manifest/print" });
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Print manifest" }).click();
  await expect(page.locator("[data-error]")).toContainText("The server could not finish");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
});

test("a blocked window is a sentence that says what to allow, and nothing is asked of the core", async ({ page, request }) => {
  await openBoard(page, request);
  await page.evaluate(() => {
    (window as unknown as { open: () => null }).open = () => null;
  });
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Print manifest" }).click();
  await expect(page.locator("[data-error]")).toContainText("The browser blocked the manifest window. Allow pop-ups");
});

test("the scan trail reads by pallet and stage, signed for where it was, and never rounds up", async ({ page, request }) => {
  await openBoard(page, request);
  await card(page, "ORD-0012").getByRole("button", { name: "Scan trail" }).click();
  const sheet = page.locator("[data-sheet='scan-trail']");
  await expect(sheet).toContainText("Scan trail for ORD-0012");
  await expect(sheet.locator("[data-trail-summary]")).toHaveText("1 of 2 loads delivered");
  await expect(sheet.locator("[data-trail-load='PLT-0012-A'] [data-stage='PICKED']")).toHaveAttribute("data-reached", "true");
  await expect(sheet.locator("[data-trail-load='PLT-0012-A'] [data-stage='PICKED']")).toHaveAttribute("data-furthest", "true");
  await expect(sheet.locator("[data-trail-load='PLT-0012-A'] [data-stage='IN_TRANSIT']")).toHaveAttribute("data-reached", "false");
  await expect(sheet.locator("[data-trail-load='PLT-0012-A']")).toContainText("PLT-0012-A: picked, last scanned");
  await expect(sheet.locator("[data-trail-load='PLT-0012-B']")).toContainText("delivered, signed for");
  await expect(sheet.locator("[data-trail-load='PLT-0012-B'] [data-stage='DELIVERED']")).toHaveAttribute("data-furthest", "true");
  await expect(sheet.locator("[data-stage='YARD']").first()).toHaveText("In the yard");
  await sheet.getByRole("button", { name: "Close" }).click();
  await expect(page.locator("[data-sheet='scan-trail']")).toHaveCount(0);
});

test("an order nobody has scanned says so, and a refused trail is the core's word in the sheet", async ({ page, request }) => {
  await openBoard(page, request);
  await card(page, "ORD-0029").getByRole("button", { name: "Scan trail" }).click();
  await expect(page.locator("[data-sheet='scan-trail'] [data-trail-summary]")).toHaveText("No scans yet");
  await expect(page.locator("[data-sheet='scan-trail']")).toContainText("No pallets scanned against this order yet.");
  await page.locator("[data-sheet='scan-trail']").getByRole("button", { name: "Close" }).click();
  await stubPost(request, "refuse", { status: 403, error: "Forbidden", path: "scan-trail", reads: true });
  await card(page, "ORD-0030").getByRole("button", { name: "Scan trail" }).click();
  await expect(page.locator("[data-sheet='scan-trail'] [data-sheet-error]")).toHaveText("That is not yours to do. The Logistics Manager or a Branch Manager can.");
});

test("the trail is on the day board and the schedule's books too", async ({ page, request }) => {
  await openBoard(page, request);
  await page.locator("[data-tab-id='day']").first().click();
  await card(page, "ORD-0012").getByRole("button", { name: "Scan trail" }).click();
  await expect(page.locator("[data-sheet='scan-trail'] [data-trail-summary]")).toHaveText("1 of 2 loads delivered");
  await page.locator("[data-sheet='scan-trail']").getByRole("button", { name: "Close" }).click();
  await page.locator("[data-tab-id='schedule']").first().click();
  await card(page, "ORD-0012").getByRole("button", { name: "Scan trail" }).click();
  await expect(page.locator("[data-sheet='scan-trail'] [data-trail-summary]")).toHaveText("1 of 2 loads delivered");
});

for (const size of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
  test(`at ${size.width} px the trail's five stages stay on one row and every control is at least 44 tall`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await openBoard(page, request);
    await card(page, "ORD-0012").getByRole("button", { name: "Scan trail" }).click();
    await expect(page.locator("[data-trail-load='PLT-0012-A']")).toBeVisible();
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
    const rows = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      const tops = [...(root?.querySelectorAll("[data-trail-load='PLT-0012-A'] [data-stage]") ?? [])].map((el) => Math.round(el.getBoundingClientRect().top));
      return new Set(tops).size;
    });
    expect(rows, "five stages, one row: a spine that folds is a list").toBe(1);
    const small = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      return [...(root?.querySelectorAll("[data-sheet='scan-trail'] button, [data-run-no='RUN-PLM-1'] [data-action='print-manifest']") ?? [])]
        .filter((el) => el.getBoundingClientRect().height < 44)
        .map((el) => el.textContent?.trim() ?? "?");
    });
    expect(small).toEqual([]);
  });
}
