// Split freight on the stub: an ordinary split applies and says the shares the core sent, a raise above
// the quote parks (nothing changes) and reads back, an identical re-ask converges, a different one is
// refused in the core's words, a figure the core did not send is not drawn, a lost reply is safe to
// ask again, both widths.
import { test, expect, openBoard, stubPost } from "./helpers.js";
import type { Page } from "@playwright/test";

const card = (page: Page, order: string) => page.locator("li.load", { hasText: order });
const field = (page: Page, name: string) => page.locator(`[data-sheet='split-freight'] [data-field='${name}']`);
const submit = (page: Page) => page.locator("[data-sheet='split-freight']").getByRole("button", { name: "Split it" });

async function openSplit(page: Page, order: string): Promise<void> {
  await card(page, order).first().getByRole("button", { name: "Split across trucks" }).click();
  await expect(page.locator("[data-sheet='split-freight']")).toBeVisible();
}

test("an ordinary split applies: the notice names the freight and each truck's share, and the new load is on the board", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0012");
  await expect(field(page, "loads")).toHaveValue("2");
  await submit(page).click();
  await expect(page.locator("[data-notice]")).toHaveText("Freight $300.00 split across 2 trucks: $150.00, $150.00.");
  await expect(card(page, "ORD-0012")).toHaveCount(2);
  await expect(page.locator("[data-raise]")).toHaveCount(0);
});

test("a share set by hand is the core's to check: a sum that does not match keeps the typing and the core's words", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0012");
  await field(page, "loads").fill("3");
  await field(page, "amounts").fill("100, 50, 50");
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("the split comes to $200.00 and the order's freight is $300.00, they have to match");
  await expect(field(page, "amounts")).toHaveValue("100, 50, 50");
  await field(page, "amounts").fill("200, 50, 50");
  await submit(page).click();
  await expect(page.locator("[data-notice]")).toHaveText("Freight $300.00 split across 3 trucks: $200.00, $50.00, $50.00.");
});

test("amounts that are not dollars are answered before the core is asked", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0012");
  await field(page, "freight").fill("12.345");
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("The charge is dollars and cents, like 150.00.");
  await field(page, "freight").fill("");
  await field(page, "loads").fill("two");
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("Trucks is a whole number, like 2.");
});

test("a raise above the quote parks: nothing changes, the card says what is asked, and an identical re-ask is the same card", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0014");
  await field(page, "freight").fill("150.00");
  await submit(page).click();
  await expect(page.locator("[data-notice]")).toContainText("Nothing changed. The extra charge is waiting for approval");
  const raise = page.locator("[data-raise]");
  await expect(raise).toContainText("Waiting for approval");
  await expect(raise).toContainText("ORD-0014 for Summit Ridge Builders");
  await expect(raise).toContainText("Quoted $100.00, charged now $100.00, asked for $150.00");
  await expect(raise).toContainText("$50.00 above the quote");
  await expect(raise).toContainText("asked by a dispatch manager");
  await expect(card(page, "ORD-0014")).toHaveCount(1);
  const id = await raise.getAttribute("data-raise-id");
  await openSplit(page, "ORD-0014");
  await field(page, "freight").fill("150.00");
  await submit(page).click();
  await expect(page.locator("[data-raise]")).toHaveAttribute("data-raise-id", id as string);
  await openSplit(page, "ORD-0014");
  await field(page, "freight").fill("160.00");
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("a freight raise on this order is already waiting for approval, that one has to be decided before another can be asked for");
});

test("checking on a parked ask reads the core's state again; a decided ask says so", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0014");
  await field(page, "freight").fill("150.00");
  await submit(page).click();
  const id = (await page.locator("[data-raise]").getAttribute("data-raise-id")) as string;
  await stubPost(request, "raise", { id, state: "APPLIED" });
  await page.locator("[data-raise]").getByRole("button", { name: "Check on it" }).click();
  await expect(page.locator("[data-raise]")).toContainText("Approved and applied");
  await page.locator("[data-raise]").getByRole("button", { name: "Close" }).click();
  await expect(page.locator("[data-raise]")).toHaveCount(0);
});

test("a figure the core did not send is not drawn: a caller who may not see value gets no money on the card", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0014");
  await field(page, "freight").fill("150.00");
  await submit(page).click();
  await expect(page.locator("[data-raise]")).toContainText("Quoted $100.00");
  await stubPost(request, "caller", { can_see_value: false });
  await page.locator("[data-raise]").getByRole("button", { name: "Check on it" }).click();
  await expect(page.locator("[data-raise]")).toContainText("ORD-0014 for Summit Ridge Builders");
  await expect(page.locator("[data-raise]")).not.toContainText("$");
  await expect(page.locator("[data-raise]")).not.toContainText("Quoted");
});

test("a caller the value policy refuses may not divide freight, and reads the refusal in words", async ({ page, request }) => {
  await openBoard(page, request);
  await stubPost(request, "caller", { can_see_value: false });
  await openSplit(page, "ORD-0012");
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("That is not yours to do. The Logistics Manager or a Branch Manager can.");
});

test("an order with no freight says so in the core's words", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0029");
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("this order carries no freight to split");
});

test("a lost reply is not called done, says asking again is safe, and asking again lands once", async ({ page, request }) => {
  await openBoard(page, request);
  await openSplit(page, "ORD-0012");
  await page.route("**/api/v1/dispatch/freight-split", (route) => route.abort("failed"), { times: 1 });
  await submit(page).click();
  await expect(page.locator("[data-sheet-error]")).toContainText("asking again is safe");
  await expect(page.locator("[data-notice]")).toHaveCount(0);
  await submit(page).click();
  await expect(page.locator("[data-notice]")).toHaveText("Freight $300.00 split across 2 trucks: $150.00, $150.00.");
  await expect(card(page, "ORD-0012")).toHaveCount(2);
});

for (const size of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
  test(`at ${size.width} px the sheet and the parked card never scroll sideways and every control is at least 44 tall`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await openBoard(page, request);
    await openSplit(page, "ORD-0014");
    await field(page, "freight").fill("150.00");
    await submit(page).click();
    await expect(page.locator("[data-raise]")).toBeVisible();
    await openSplit(page, "ORD-0014");
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
    const small = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      return [...(root?.querySelectorAll("[data-sheet='split-freight'] button, [data-sheet='split-freight'] input, [data-raise] button") ?? [])]
        .filter((el) => el.getBoundingClientRect().height < 44)
        .map((el) => el.textContent?.trim() || el.getAttribute("data-field") || "?");
    });
    expect(small).toEqual([]);
  });
}
