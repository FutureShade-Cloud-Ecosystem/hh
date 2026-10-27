// The till close's deposit slip (FW1-13, FROM-PRINCIPAL 166 P1-1): the close lists the
// drawer's unlodged cheques, each ticked by default, and the lodgement rides the ticked slip.
// A cheque lodgement names its cheques because a cheque bounces: without the slip the deposit
// would not say which payments it carries, and the bounce would credit the wrong account.
//
// This file runs BEFORE till-close.pw.ts on purpose (the name sorts first): the demo drawer
// holds one slip's worth of cheques, and a lodged cheque leaves the list, so this file takes
// the fresh drawer and the close spec that follows finds the drawer as it left it.
import { expect, test, type Page } from "@playwright/test";

import { closeAccountMenu, openAccountMenu } from "./account-menu";

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page, role: "Sales" | "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

/** Make sure the counter's drawer is open before working the close. */
async function ensureSession(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (await dot.isVisible()) {
    await expect(dot).toContainText("Open");
    return;
  }
  await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

// The review screens: the close's slip at 1440 and 390, light and charcoal. Written only when
// asked, the discount suite's convention (an env var names the directory; without it this block
// skips).
const shots = process.env.TILL_CLOSE_SHOTS_DIR ?? "";
test.describe("review screens", () => {
  test.skip(shots === "", "TILL_CLOSE_SHOTS_DIR is not set");

  for (const theme of ["light", "charcoal"] as const) {
    for (const viewport of [1440, 390] as const) {
      test(`${theme} ${viewport}: the close's slip`, async ({ page }) => {
        await page.setViewportSize({ width: viewport, height: viewport === 1440 ? 900 : 844 });
        await signIn(page, "Sales");
        await ensureSession(page);
        if (theme === "charcoal") {
          await openAccountMenu(page);
          await page.getByTestId("theme-control").click();
          await closeAccountMenu(page);
        }
        await page.getByTestId("end-session").click();
        const dialog = page.getByTestId("till-close-dialog");
        await expect(dialog).toBeVisible();
        await dialog.getByTestId("movement-type").selectOption("LODGEMENT");
        await dialog.getByTestId("movement-method").selectOption("CHEQUE");
        await expect(dialog.getByTestId("movement-slip")).toBeVisible();
        await page.screenshot({
          path: `${shots}/till-close-slip-${viewport}x${viewport === 1440 ? 900 : 844}-${theme}.png`,
          fullPage: true,
        });
      });
    }
  }
});

// The lodgement's slip (FROM-PRINCIPAL 166 P1-1): the close lists the drawer's unlodged cheques,
// each ticked by default, and the lodgement rides the ticked slip. A cheque lodgement names its
// cheques because a cheque bounces: without the slip the deposit would not say which payments it
// carries, and the bounce would credit the wrong account.
test("the close lists the unlodged cheques, each ticked by default, and the lodgement carries them", async ({
  page,
}) => {
  const slipCalls: { payment_ids: string[]; method: string }[] = [];
  page.on("request", (request) => {
    if (/\/api\/v1\/till\/sessions\/[^/]+\/movements$/.test(request.url()) && request.method() === "POST") {
      const body = JSON.parse(request.postData() ?? "{}") as { payment_ids?: string[]; method?: string };
      slipCalls.push({ payment_ids: body.payment_ids ?? [], method: body.method ?? "" });
    }
  });

  await signIn(page, "Sales");
  await ensureSession(page);
  await page.getByTestId("end-session").click();
  const dialog = page.getByTestId("till-close-dialog");
  await expect(dialog).toBeVisible();

  // A lodgement of cheques: the slip shows the drawer's unlodged cheques.
  await dialog.getByTestId("movement-type").selectOption("LODGEMENT");
  await dialog.getByTestId("movement-method").selectOption("CHEQUE");
  const slip = dialog.getByTestId("movement-slip");
  await expect(slip).toBeVisible();
  const ticks = slip.getByTestId("slip-tick");
  await expect(ticks).toHaveCount(2);

  // The slip names each cheque: the number is on the row, whole, not squeezed out by the tick.
  const firstNo = slip.locator(".slip-no").first();
  await expect(firstNo).toHaveText("CHQ-1042");
  await expect(firstNo).toBeVisible();
  const clipped = await firstNo.evaluate((el) => el.scrollWidth > el.clientWidth + 1);
  expect(clipped).toBe(false);

  // Every cheque starts ticked: the drawer unticks the ones this lodgement does not carry.
  for (const tick of await ticks.all()) {
    await expect(tick).toBeChecked();
  }
  await expect(slip.getByTestId("slip-sum")).toHaveText("$312.50");

  // Untick one, and the slip's sum follows.
  await ticks.first().uncheck();
  await expect(slip.getByTestId("slip-sum")).toHaveText("$12.50");
  await ticks.first().check();
  await expect(slip.getByTestId("slip-sum")).toHaveText("$312.50");

  // The lodgement rides the slip: the ticked cheques go with it.
  await dialog.getByTestId("movement-reason").fill("bank run");
  await dialog.getByTestId("record-movement").click();
  await expect(dialog.getByTestId("movement-ok")).toContainText("LODGEMENT");
  expect(slipCalls.length).toBeGreaterThan(0);
  const last = slipCalls[slipCalls.length - 1];
  expect(last.method).toBe("CHEQUE");
  expect(last.payment_ids.length).toBe(2);
});
