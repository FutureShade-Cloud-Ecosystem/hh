// Check Quote and the floor refusal dialog in a real browser, against the
// stubbed harness: the form at its defaults, a check that clears, a check
// below the floor that opens the dialog (a check only: nothing filed), the
// dialog's focus and closing, filing a request as Sales through a line
// discount below the floor and the requester's view of it, and the server's
// offer to clear the price itself for the Sales manager.
import { expect, test, type Page } from "@playwright/test";
import { shot } from "./shot";
import { showList } from "./switch-helpers.js";

async function signIn(page: Page, role = "Sales"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await showList(page);
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qg-box")).toBeVisible();
}

const guard = (page: Page) => page.locator("qd-quote-guard");
const dialog = (page: Page) => page.locator("qd-floor-refusal");
const edit = (page: Page) => page.locator("qd-quote-edit");

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("the form opens at its defaults with the branch named and no verdict", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(guard(page).getByRole("heading", { name: "Check Quote" })).toBeVisible();
  await expect(guard(page).getByTestId("qg-price")).toHaveValue("100.00");
  await expect(guard(page).getByTestId("qg-cost")).toHaveValue("94.00");
  await expect(guard(page).getByTestId("qg-branch")).not.toBeEmpty();
  await expect(guard(page).getByTestId("qg-result")).toHaveCount(0);
  await expect(dialog(page).getByTestId("fr-dialog")).toHaveCount(0);
  await guard(page).scrollIntoViewIfNeeded();
  await shot(guard(page), "guard-clear");
});

test("a price the server clears says so in percent and opens no dialog", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await guard(page).getByTestId("qg-cost").fill("70.00");
  await guard(page).getByTestId("qg-check").click();
  await expect(guard(page).getByTestId("qg-result")).toHaveText("Approved. The margin is 30%.");
  await expect(guard(page).getByTestId("qg-result")).toHaveAttribute("role", "status");
  await expect(dialog(page).getByTestId("fr-dialog")).toHaveCount(0);
});

test("a price that is not an amount is refused in words before any request", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await guard(page).getByTestId("qg-price").fill("abc");
  await guard(page).getByTestId("qg-check").click();
  await expect(guard(page).getByTestId("qg-result")).toContainText("Enter the price and the cost as amounts");
  await expect(guard(page).getByTestId("qg-result")).toHaveAttribute("role", "alert");
});

test("below the floor opens the refusal dialog as a check only, and it closes by button and by Escape", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openQuote(page, "Q-1001");
  await guard(page).getByTestId("qg-price").fill("90.00");
  await guard(page).getByTestId("qg-check").click();
  await expect(guard(page).getByTestId("qg-result")).toHaveText("Blocked. Margin -4.45% is below the 12% floor");
  const dlg = dialog(page).getByRole("dialog", { name: "Action blocked" });
  await expect(dlg).toBeVisible();
  await expect(dlg).toHaveAttribute("aria-modal", "true");
  await expect(dialog(page).getByTestId("fr-policy")).toHaveText("Below floor");
  // The core's 403 carries the margin in its sentence, not as a figure of its own.
  await expect(dialog(page).getByTestId("fr-reason")).toHaveText("Margin -4.45% is below the 12% floor");
  await expect(dialog(page).getByTestId("fr-check-only")).toContainText("check only");
  await expect(dialog(page).getByTestId("fr-approvers")).toContainText("named no approver");
  // A check only files nothing: no request button, no note field.
  await expect(dialog(page).getByTestId("fr-request")).toHaveCount(0);
  await expect(dialog(page).getByTestId("fr-close")).toBeFocused();
  await shot(page, "guard-blocked");
  await shot(page, "refusal-check-only");
  await dialog(page).getByTestId("fr-close").click();
  await expect(dlg).toHaveCount(0);
  await expect(guard(page).getByTestId("qg-check")).toBeFocused();
  // Again, closed by Escape.
  await guard(page).getByTestId("qg-check").click();
  await expect(dlg).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(dlg).toHaveCount(0);
  await expect(guard(page).getByTestId("qg-check")).toBeFocused();
});

test("Tab stays inside the open dialog", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1001");
  await guard(page).getByTestId("qg-price").fill("90.00");
  await guard(page).getByTestId("qg-check").click();
  await expect(dialog(page).getByTestId("fr-dialog")).toBeVisible();
  for (let i = 0; i < 5; i += 1) await page.keyboard.press("Tab");
  const inside = await page.evaluate(() => {
    let node: Element | null = document.activeElement;
    while (node?.shadowRoot?.activeElement) node = node.shadowRoot.activeElement;
    return node?.closest(".dialog") !== null && node?.closest(".dialog") !== undefined;
  });
  expect(inside).toBe(true);
});

test("Sales files a request through a discount below the floor and sees the requester state", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openQuote(page, "Q-1001");
  const paver = edit(page).getByTestId("qe-line").first();
  await paver.locator('[data-field="disc"]').fill("10");
  await paver.getByRole("button", { name: "Save" }).click();
  const dlg = dialog(page).getByRole("dialog", { name: "Action blocked" });
  await expect(dlg).toBeVisible();
  await expect(dialog(page).getByTestId("fr-policy")).toHaveText("Category margin floor");
  // The approvers exactly as the server names them, and the margin figures the server sent.
  await expect(dialog(page).getByTestId("fr-approvers")).toContainText("the General Manager, the Financial Controller or the Sales Manager");
  await expect(dialog(page).getByTestId("fr-figures")).toContainText("against a floor of");
  await expect(dialog(page).getByTestId("fr-request-state")).toHaveCount(0);
  await dialog(page).locator('[data-field="note"]').fill("Contractor is waiting on the price");
  await shot(page, "refusal-before-filing");
  await dialog(page).getByTestId("fr-request").click();
  await expect(dialog(page).getByTestId("fr-request-state")).toContainText("Your request is pending");
  await expect(dialog(page).getByTestId("fr-request-state")).toHaveAttribute("data-status", "PENDING");
  await expect(dialog(page).getByTestId("fr-notice")).toContainText("Sent.");
  await expect(dialog(page).getByTestId("fr-request")).toHaveCount(0);
  await shot(page, "refusal-filed");
  await dialog(page).getByTestId("fr-close").click();
  await expect(dlg).toHaveCount(0);
  // The standing request rides the next refusal as existing_request: shown, not refiled.
  await paver.locator('[data-field="disc"]').fill("11");
  await paver.getByRole("button", { name: "Save" }).click();
  await expect(dialog(page).getByTestId("fr-request-state")).toContainText("Your request is pending");
  await expect(dialog(page).getByTestId("fr-request")).toHaveCount(0);
});

test("the Sales manager is offered the price itself and nothing is filed", async ({ page }) => {
  await signIn(page, "Sales manager");
  await openQuote(page, "Q-1001");
  // The work area does not raise a refusal for this caller (the server lets the write through), so the
  // seam event is fired the way the work area fires it, to see the dialog's answer to the filing door.
  await page.evaluate(() => {
    window.dispatchEvent(
      new CustomEvent("qd-floor-refusal", {
        detail: {
          refusal: { policy: "category_margin_floor", approverRoles: ["General Manager", "Financial Controller", "Sales Manager"], managerMaySet: true, requestId: null, requestStatus: null, marginBps: null, floorBps: null },
          quoteId: "q-1001",
          lineNo: 1,
          discPct: 10,
        },
      }),
    );
  });
  await expect(dialog(page).getByTestId("fr-dialog")).toBeVisible();
  await dialog(page).getByTestId("fr-request").click();
  await expect(dialog(page).getByTestId("fr-may-clear")).toContainText("You may set this price yourself");
  await expect(dialog(page).getByTestId("fr-request-state")).toHaveCount(0);
  await expect(dialog(page).getByTestId("fr-request")).toHaveCount(0);
  await shot(page, "refusal-may-clear");
  await dialog(page).getByTestId("fr-close").click();
  await expect(dialog(page).getByTestId("fr-dialog")).toHaveCount(0);
});

// Visual review round 1, P3: the Check Quote form sat 350 px wide and left
// aligned in a full-width card. The three fields now share the card's width.
test("the Check Quote fields share a row across the card at desk width", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await signIn(page);
  await openQuote(page, "Q-1001");
  const branch = (await guard(page).getByTestId("qg-branch").boundingBox())!;
  const price = (await guard(page).getByTestId("qg-price").boundingBox())!;
  const cost = (await guard(page).getByTestId("qg-cost").boundingBox())!;
  const card = (await guard(page).getByTestId("qg-box").boundingBox())!;
  expect(Math.abs(branch.y - price.y)).toBeLessThan(8);
  expect(Math.abs(price.y - cost.y)).toBeLessThan(8);
  expect(price.width).toBeGreaterThan(180);
  // Together the fields reach across most of the card, not a third of it.
  expect(cost.x + cost.width - branch.x).toBeGreaterThan(card.width * 0.6);
});
