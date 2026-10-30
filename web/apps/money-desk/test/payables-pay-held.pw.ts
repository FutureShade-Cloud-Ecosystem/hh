// S6 on 701, P2-2: a held key never swallows a later, different payment.
//
// The stub answers the route the core answers (POST /api/v1/purchasing/bills/{id}/payments): the
// key rides the Idempotency-Key header, one key per bill while its outcome is unknown, a key spent
// on this bill is a replay that writes nothing, a key spent on another bill is a conflict, a press
// with no key is refused, and the role gate is the core's own (a manage level route on the money
// desk, so the Payables and Receivables role is turned away by it).
//
// Every leg reads the figures on the screen, so a screen that renders nothing, or a route that
// answers nothing, fails the leg instead of passing it.
import { test, expect, openDesk, resetStub, dayFromToday, dayWords, receivablesHandoff } from "./helpers.js";
import type { Page } from "@playwright/test";

/** The cents a figure reads: "$13,482.50" is 1348250. */
function centsOf(text: string): number {
  const dollars = text.replace(/[^0-9.]/g, "");
  const [whole = "0", fraction = ""] = dollars.split(".");
  return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
}

/** The demo's own token for a role, minted the way the front door's handoff mints it. */
function demoToken(who: "controller" | "clerk"): string {
  const part = (value: unknown): string => Buffer.from(JSON.stringify(value)).toString("base64url");
  const id = who === "controller" ? "de110000-0000-4000-9000-000000000008" : "de110000-0000-4000-9000-000000000007";
  const exp = Math.floor(Date.now() / 1000) + 3600;
  return `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: id, exp })}.c2ln`;
}

/** The bill the world's B-4107 is: the one the part paid bill belongs to. */
const B4107 = "b0000000-0000-4000-8000-000000000006";
/** The bill the world's B-4108 is. */
const B4108 = "b0000000-0000-4000-8000-000000000007";

/** The board's open AP, read from the screen. */
async function openBoard(page: Page): Promise<void> {
  await page.locator("nav[aria-label='Money Desk areas']").getByRole("button", { name: "Payables", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Payables", exact: true })).toBeVisible();
  await expect(page.locator("[data-payables-aging], [data-payables-empty]").first()).toBeVisible();
}

/** A payee's open items, read from the screen. */
async function openPayee(page: Page, payee: string): Promise<void> {
  await page.locator("[data-payee-row]", { hasText: payee }).getByRole("button").click();
  await expect(page.locator("[data-payables-lines]")).toBeVisible();
}

/** The Pay form on one bill, opened from the row's own Pay control. */
async function openPayForm(page: Page, billNo: string): Promise<void> {
  await page.locator("[data-line-row]", { hasText: billNo }).getByRole("button", { name: "Pay" }).click();
  await expect(page.locator("[data-pay-form]")).toBeVisible();
}

test("a payment that landed but lost its answer ends its key, so a later different payment is written", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  await openPayForm(page, "B-4107");
  let drop = true;
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    if (drop) { drop = false; await route.fetch(); await route.abort("connectionreset"); return; }
    await route.continue();
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  // The books show the $100.00 landed, so the key ends and the form says so until the person closes it.
  await expect(page.locator("[data-payee-lines-total]")).toContainText("$775.00");
  await expect(page.locator("[data-pay-landed]")).toBeVisible();
  await page.locator("[data-action='payables-pay-cancel']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  // A second, different payment of $50.00 is a new payment, and it is written.
  await openPayForm(page, "B-4107");
  await expect(page.locator("[data-field='pay-amount']")).not.toHaveValue("100.00");
  await page.locator("[data-field='pay-amount']").fill("50.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  await expect(page.locator("[data-payee-lines-total]")).toContainText("$725.00");
  const after = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  expect(before - after).toBe(15_000);
});

test("a payment that did not land holds its key and its amount: a different amount is refused, the same one pays once", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  await openPayForm(page, "B-4107");
  let drop = true;
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    if (drop) { drop = false; await route.abort("connectionreset"); return; }
    await route.continue();
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-error]")).toContainText("may or may not have been recorded");
  await page.locator("[data-action='payables-pay-cancel']").click();
  await openPayForm(page, "B-4107");
  await expect(page.locator("[data-field='pay-amount']")).toHaveValue("100.00");
  await page.locator("[data-field='pay-amount']").fill("50.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-error]")).toContainText("A different amount waits");
  expect(centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(before);
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  await expect(page.locator("[data-payee-lines-total]")).toContainText("$775.00");
  expect(before - centsOf(await page.locator("[data-payee-lines-total]").innerText())).toBe(10_000);
});

// Sonnet's visual read of 701 (round 2): the words of a lost answer, the amount field's alignment
// and the phone's touch height.
test("a lost answer says to press Record the payment again, in the desk's words and with no engine word", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await openPayForm(page, "B-4107");
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    await route.abort("connectionreset");
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  const error = page.locator("[data-pay-error]");
  await expect(error).toContainText("Press Record the payment again to send the same payment; the books will say if the first press was already recorded.");
  await expect(error).not.toContainText(/\bkey\b/i);
});

test("the Pay form's amount is right aligned in tabular figures", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  await openPayForm(page, "B-4107");
  const style = await page.locator("[data-field='pay-amount']").evaluate((el) => {
    const css = getComputedStyle(el);
    return { align: css.textAlign, figures: css.fontVariantNumeric };
  });
  expect(["right", "end"]).toContain(style.align);
  expect(style.figures).toContain("tabular-nums");
});

test("the Pay control is at least 44 px high on a phone", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const box = await page.locator("[data-line-row]", { hasText: "B-4107" }).getByRole("button", { name: "Pay" }).boundingBox();
  expect(box?.height ?? 0).toBeGreaterThanOrEqual(44);
});

// S6 on 701, P2-3: the payment landed but its answer was lost, and the form's words had just
// invited a second press. When the books show the payment, the form says it is on the bill, offers
// nothing to send, and waits for the person's own Close: the money moves once.
test("a lost answer on a payment that landed says it is on the bill and sends nothing more", async ({ page, request }) => {
  await openDesk(page, request, "controller");
  await openBoard(page);
  await openPayee(page, "Saltmarsh Fasteners (demo)");
  const before = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  await openPayForm(page, "B-4107");
  let drop = true;
  await page.route("**/api/v1/purchasing/bills/*/payments", async (route) => {
    if (drop) {
      drop = false;
      await route.fetch();
      await route.abort("connectionreset");
      return;
    }
    await route.continue();
  });
  await page.locator("[data-field='pay-amount']").fill("100.00");
  await page.locator("[data-action='payables-pay-send']").click();
  await expect(page.locator("[data-pay-landed]")).toContainText("The earlier payment of $100.00 is on the bill. Nothing more to send.");
  await expect(page.locator("[data-action='payables-pay-send']")).toHaveCount(0);
  await expect(page.locator("[data-action='payables-pay-cancel']")).toHaveText("Close");
  await page.locator("[data-action='payables-pay-cancel']").click();
  await expect(page.locator("[data-pay-form]")).toHaveCount(0);
  const after = centsOf(await page.locator("[data-payee-lines-total]").innerText());
  expect(before - after).toBe(10_000);
});
