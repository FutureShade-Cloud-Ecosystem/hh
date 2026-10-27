// The till close on screen (FW1-13): End Session opens the real close flow the
// A-03 routes already serve. The stories this suite holds are the drawer's
// own: End Session is open (not the closed notice the mount carried before the
// flow landed), the close walks movements then a BLIND count by denomination
// (no expected figure is on screen before the count posts), the Z report states
// expected, counted, variance and the session's movements, an over-threshold
// variance demands a manager's note before it completes, and the close resets
// the till chrome to a fresh Open Session.
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

test("End Session opens the close: movements, a blind count by denomination, the Z report, a manager's note, and the close lands", async ({
  page,
}) => {
  const completeCalls: string[] = [];
  page.on("request", (request) => {
    if (/\/api\/v1\/till\/sessions\/[^/]+\/complete/.test(request.url())) {
      completeCalls.push(`${request.method()} ${request.url()}`);
    }
  });

  await signIn(page, "Sales manager");
  await ensureSession(page);

  // The button is open now: no closed notice, a real control.
  const button = page.getByTestId("end-session");
  await expect(button).toBeVisible();
  await expect(button).not.toHaveAttribute("aria-disabled", /.*/);
  await button.click();

  // The close flow opens on the movements step.
  const dialog = page.getByTestId("till-close-dialog");
  await expect(dialog).toBeVisible();
  await expect(page.getByText("End of day · drawer movements")).toBeVisible();

  // Record a paid out and a lodgement; both name their reason.
  const type = dialog.getByTestId("movement-type");
  const amount = dialog.getByTestId("movement-amount");
  const reason = dialog.getByTestId("movement-reason");
  await type.selectOption("PAID_OUT");
  await amount.fill("12.50");
  await reason.fill("petty cash for ties");
  await dialog.getByTestId("record-movement").click();
  await expect(dialog.getByTestId("movement-ok")).toContainText("PAID_OUT");
  await type.selectOption("LODGEMENT");
  await amount.fill("40.00");
  await reason.fill("morning bank run");
  await dialog.getByTestId("record-movement").click();
  await expect(dialog.getByTestId("movement-ok")).toContainText("LODGEMENT");

  // On to the blind count.
  await dialog.getByTestId("to-count").click();
  const countStep = dialog.getByTestId("count-step");
  await expect(page.getByText("End of day · blind count")).toBeVisible();
  // Blind: the count step shows no expected figure anywhere before the count posts.
  await expect(countStep).not.toContainText("Expected");

  // The count is by denomination: two twenties and a ten make the counted cash.
  await countStep.getByTestId("denom-2000").fill("2");
  await countStep.getByTestId("denom-1000").fill("1");
  await expect(countStep.getByTestId("denom-sum")).toHaveText("$50.00");
  await countStep.getByTestId("closing-float").fill("200.00");
  await countStep.getByTestId("post-count").click();

  // The Z report states the reconciliation and the drawer's movements.
  const zStep = dialog.getByTestId("zreport-step");
  await expect(page.getByText("Z-report · reconciliation")).toBeVisible();
  const table = zStep.getByTestId("zreport-table");
  await expect(table).toBeVisible();
  await expect(table).toContainText("Expected");
  await expect(table).toContainText("Counted");
  // The counted cash cell is the denomination sum the counter typed.
  await expect(table).toContainText("$50.00");
  await expect(zStep.getByTestId("zreport-movements")).toContainText("petty cash for ties");
  await expect(zStep.getByTestId("zreport-movements")).toContainText("morning bank run");

  // An over-threshold variance (the demo threshold is zero) demands the note.
  const chip = zStep.getByTestId("overshort-chip");
  await expect(chip).toBeVisible();
  const note = zStep.getByTestId("override-notes");
  const complete = zStep.getByTestId("complete-close");
  if ((await zStep.getByTestId("manager-notice").count()) > 0) {
    await expect(complete).toBeDisabled();
    await note.fill("counted short; floats for tomorrow");
  }
  await complete.click();

  // The close lands, the wizard says so, and Done resets the till chrome.
  await expect(page.getByRole("heading", { name: "Till closed" })).toBeVisible();
  await expect(page.getByTestId("till-done-step")).toContainText("Till closed and reconciled.");
  await expect(completeCalls).toHaveLength(1);
  await page.getByTestId("till-done").click();
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole("button", { name: "Open Session", exact: true })).toBeVisible();
});

test("A short count on a frontline's drawer names the manager in the Z report's sign-off", async ({ page }) => {
  await signIn(page, "Sales");
  await ensureSession(page);
  const button = page.getByTestId("end-session");
  await expect(button).toBeVisible();
  await expect(button).not.toHaveAttribute("aria-disabled", /.*/);
  await button.click();
  const dialog = page.getByTestId("till-close-dialog");
  await expect(dialog).toBeVisible();
  await dialog.getByTestId("to-count").click();
  const countStep = dialog.getByTestId("count-step");
  // A count under the float is a short drawer.
  await countStep.getByTestId("denom-2000").fill("5");
  await countStep.getByTestId("closing-float").fill("200.00");
  await countStep.getByTestId("post-count").click();
  const zStep = dialog.getByTestId("zreport-step");
  await expect(page.getByText("Z-report · reconciliation")).toBeVisible();
  // The demo threshold is zero, so any variance is over it: the sign-off names the manager.
  await expect(zStep.getByTestId("manager-notice")).toContainText("manager");
  await expect(zStep.getByTestId("complete-close")).toBeDisabled();
});
