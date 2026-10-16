// The will call tab on the stub: an order named by its number, its pickup codes in the counter's words,
// a code printed once and gone when the tab is left, a code cancelled behind a confirm, the core's words
// on every refusal, the role gate below the manage level, and both widths.
import { test, expect, openBoard, stubPost } from "./helpers.js";
import type { Page } from "@playwright/test";

const box = (page: Page) => page.locator("[data-willcall] [data-field='wc-order']");
const lookup = (page: Page) => page.locator("[data-willcall]").getByRole("button", { name: "Look up" });
const mint = (page: Page) => page.locator("[data-willcall]").getByRole("button", { name: "Print a pickup code" });

async function openWillCall(page: Page, request: Parameters<typeof openBoard>[1]): Promise<void> {
  await openBoard(page, request);
  await page.locator("[data-tab-id='willcall']").first().click();
  await expect(page.locator("[data-willcall]")).toBeVisible();
}

test("the tab opens quiet: no statement about an order nobody named", async ({ page, request }) => {
  await openWillCall(page, request);
  await expect(page.locator("[data-tab-id='willcall']").first()).toHaveAttribute("aria-current", "true");
  await expect(page.locator("[data-wc-idle]")).toContainText("Name an order to see its pickup codes");
  await expect(page.locator("[data-wc-empty]")).toHaveCount(0);
  await expect(page.locator("[data-action='new-run']")).toHaveCount(0);
});

test("an order's codes read in the counter's words with who collected", async ({ page, request }) => {
  await openWillCall(page, request);
  await box(page).fill("ORD-0047");
  await lookup(page).click();
  await expect(page.locator("[data-wc-list] li")).toHaveCount(2);
  await expect(page.locator("[data-code-status='CONFIRMED']")).toContainText("Collected");
  await expect(page.locator("[data-code-status='CONFIRMED']")).toContainText("Collected by Pickup contact (demo)");
  await expect(page.locator("[data-code-status='REVOKED']")).toContainText("Cancelled before it was used");
  await expect(page.getByRole("button", { name: "Cancel code" })).toHaveCount(0);
});

test("an order with no codes says so only after the lookup, and Enter in the box looks it up", async ({ page, request }) => {
  await openWillCall(page, request);
  await box(page).fill("ORD-0044");
  await box(page).press("Enter");
  await expect(page.locator("[data-wc-empty]")).toHaveText("No pickup codes for ORD-0044 yet.");
});

test("a code is printed once: shown on the tag, listed as live, gone from the screen when the tab is left", async ({ page, request }) => {
  await openWillCall(page, request);
  await box(page).fill("ORD-0044");
  await mint(page).click();
  await expect(page.locator("[data-notice]")).toContainText("Pickup code made for ORD-0044, good until");
  await expect(page.locator("[data-once]")).toContainText("Shown once.");
  const token = await page.locator("[data-tag-code]").innerText();
  expect(token.length).toBeGreaterThanOrEqual(32);
  await expect(page.locator("[data-code-status='ACTIVE']")).toContainText("Live");
  await expect(page.locator("[data-code-status='ACTIVE']")).toContainText("Good until");
  // The list the core serves never carries the token, so the screen cannot show it again.
  await expect(page.locator("[data-wc-codes]")).not.toContainText(token);
  await page.locator("[data-tab-id='load']").first().click();
  await page.locator("[data-tab-id='willcall']").first().click();
  await expect(page.locator("[data-tag]")).toHaveCount(0);
  await expect(page.locator("main")).not.toContainText(token);
  await expect(box(page)).toHaveValue("");
});

test("a second code for the same order is refused in the core's words, and the one live code is cancelled behind a confirm", async ({ page, request }) => {
  await openWillCall(page, request);
  await box(page).fill("ORD-0044");
  await mint(page).click();
  await expect(page.locator("[data-tag]")).toBeVisible();
  await mint(page).click();
  await expect(page.locator("[data-wc-error]")).toHaveText("this order already has a live pickup code, revoke it before printing another");
  await page.getByRole("button", { name: "Cancel code" }).click();
  await expect(page.locator("[data-sheet]")).toContainText("The tag already printed stops working.");
  await page.locator("[data-sheet]").getByRole("button", { name: "Keep it" }).click();
  await expect(page.locator("[data-code-status='ACTIVE']")).toBeVisible();
  await page.getByRole("button", { name: "Cancel code" }).click();
  await page.locator("[data-sheet]").getByRole("button", { name: "Cancel the code" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("The pickup code is cancelled. The tag already printed no longer works.");
  await expect(page.locator("[data-code-status='REVOKED']")).toContainText("Cancelled before it was used");
  await expect(page.locator("[data-tag]")).toHaveCount(0);
});

test("every refusal is the core's sentence: not a will call order, a draft, not picked, held, not there", async ({ page, request }) => {
  await openWillCall(page, request);
  const refused = async (order: string, words: string | RegExp): Promise<void> => {
    await box(page).fill(order);
    await mint(page).click();
    await expect(page.locator("[data-wc-error]")).toContainText(words);
  };
  await refused("ORD-0043", "not will-call, a rack tag prints only for a will-call order");
  await refused("ORD-0041", "confirm the order before printing a pickup code");
  await refused("ORD-0042", "this order was cancelled");
  await refused("ORD-0045", "nothing has been picked for this order yet");
  await refused("ORD-0046", "This order cannot reach picking until the balance is paid.");
  await refused("ORD-9999", "No order matches that number at this yard");
  await refused("banana", "is not an order number");
  await expect(page.locator("[data-tag]")).toHaveCount(0);
});

test("a blank box is answered before the core is asked", async ({ page, request }) => {
  await openWillCall(page, request);
  await mint(page).click();
  await expect(page.locator("[data-wc-error]")).toContainText("Enter an order number");
  await lookup(page).click();
  await expect(page.locator("[data-wc-error]")).toContainText("Enter an order number");
});

test("below the manage level a cancel is refused by the gate in words, and minting still works", async ({ page, request }) => {
  await openWillCall(page, request);
  await box(page).fill("ORD-0044");
  await mint(page).click();
  await stubPost(request, "caller", { can_manage: false });
  await page.getByRole("button", { name: "Cancel code" }).click();
  await page.locator("[data-sheet]").getByRole("button", { name: "Cancel the code" }).click();
  await expect(page.locator("[data-sheet-error]")).toHaveText("That is not yours to do. The Logistics Manager or a Branch Manager can.");
  await expect(page.locator("[data-code-status='ACTIVE']")).toBeVisible();
});

for (const size of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
  test(`at ${size.width} px the tab never scrolls sideways, the tag fits, and every control is at least 44 tall`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await openWillCall(page, request);
    await box(page).fill("ORD-0044");
    await mint(page).click();
    await expect(page.locator("[data-tag]")).toBeVisible();
    const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(wide).toBe(false);
    const small = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      return [...(root?.querySelectorAll("[data-willcall] button, [data-willcall] input, [data-tag] button, [data-wc-codes] button") ?? [])]
        .filter((el) => el.getBoundingClientRect().height < 44)
        .map((el) => el.textContent?.trim() || el.getAttribute("data-field") || "?");
    });
    expect(small).toEqual([]);
    const tagFits = await page.evaluate(() => {
      const root = document.querySelector("dispatch-board-app")?.shadowRoot;
      const tag = root?.querySelector("[data-tag-code]")?.getBoundingClientRect();
      return tag !== undefined && tag.right <= window.innerWidth && tag.left >= 0;
    });
    expect(tagFits).toBe(true);
  });
}
