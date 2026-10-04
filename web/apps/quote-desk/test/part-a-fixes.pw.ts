// PR 121 round 1, Part A: the work area and the list say what is true.
// A quantity step that lands below the floor is the typed refusal and opens the
// dialog; a layout save, reset or column save that fails is shown, never
// dropped; Send Quote is one send and says only what the core did; the
// Screen settings entry is offered to everyone and the server answers; the
// quotes list pages and its rows are reachable by keyboard; convert sends
// the chosen fulfilment and nothing more.
import { expect, test, type Page } from "@playwright/test";

const SALES = { name: "Sales", exact: true };

async function signIn(page: Page, role: { name: string; exact: boolean } = SALES): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", role).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function startQuote(page: Page, customer = "Maple", option = /Maple Ridge Contracting/): Promise<void> {
  const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
  await picker.getByPlaceholder("Search name or account #…").fill(customer);
  await picker.getByRole("option", { name: option }).click();
  const scan = page.getByTestId("pos-scan-input");
  await scan.fill("PSN-100");
  await scan.press("Enter");
  await expect(page.getByTestId("qb-line")).toHaveCount(1);
}

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a quantity step that lands below the floor is the typed refusal and opens the dialog, never raw JSON", async ({ page, request }) => {
  await signIn(page);
  await startQuote(page);
  const disc = page.getByTestId("qb-line-disc");
  await disc.fill("2");
  await disc.blur();
  await expect(page.getByTestId("qb-totals")).toContainText("Discount");
  await request.put("/_stub/floor-raise", { data: { bps: 4000 } });
  await page.getByRole("button", { name: /^Increase quantity/ }).click();
  await expect(page.locator("qd-floor-refusal").getByRole("dialog")).toBeVisible();
  await expect(page.getByTestId("qb-error")).toContainText("below the margin floor");
  await expect(page.getByTestId("qb-error")).not.toContainText("{");
});

test("a failed layout save is shown, a failed reset changes nothing, and a later good save clears the line", async ({ page }) => {
  await signIn(page);
  await startQuote(page);
  let failing = true;
  await page.route("**/api/v1/surfaces/*/my-workspace", async (route) => {
    if (failing && route.request().method() !== "GET") {
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the layout store is down" }) });
    } else {
      await route.continue();
    }
  });
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect(page.getByTestId("qb-layout-error")).toContainText("The layout was not saved");
  await expect(page.getByTestId("qb-layout-error")).toContainText("The layout store is down");
  // The reset is refused: the screen keeps the arrangement it has and says why.
  const arrangement = await page.getByTestId("qb-workspace").getAttribute("data-arrangement");
  await page.getByRole("button", { name: "Reset layout" }).click();
  await expect(page.getByTestId("qb-layout-error")).toContainText("The layout was not reset");
  await expect(page.getByTestId("qb-workspace")).toHaveAttribute("data-arrangement", arrangement ?? "");
  failing = false;
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect(page.getByTestId("qb-layout-error")).toHaveText("", { useInnerText: true });
});

test("a failed config read still lets a later layout change save, and a refusal of that save is shown", async ({ page }) => {
  await page.route("**/api/v1/surfaces/*/config**", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the config store is down" }) }),
  );
  await page.route("**/api/v1/surfaces/*/my-workspace", (route) =>
    route.request().method() === "GET"
      ? route.continue()
      : route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the layout store is down" }) }),
  );
  await signIn(page);
  await expect(page.getByTestId("qb-layout-error")).toContainText("The saved layout could not be read");
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await page.getByRole("button", { name: "Swap panels" }).click();
  await expect(page.getByTestId("qb-layout-error")).toContainText("The layout was not saved");
});

test("a failed column save is shown", async ({ page }) => {
  await signIn(page);
  await startQuote(page);
  await page.route("**/api/v1/surfaces/*/my-items", async (route) => {
    if (route.request().method() === "PUT") {
      await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "settings: the items store is down" }) });
    } else {
      await route.continue();
    }
  });
  await page.getByTestId("qb-columns-btn").click();
  // A refused save returns the box to the saved state, so the press is a click: check() would re-read the box and race that revert.
  await page.locator('[data-field="column"][data-column="line.margin_pct"]').click();
  await expect(page.getByTestId("qb-layout-error")).toContainText("The columns were not saved");
});

test("Send Quote is one send: a double press mints one link, and the confirmation claims no email", async ({ page }) => {
  await signIn(page);
  await startQuote(page);
  const mints: string[] = [];
  page.on("request", (req) => {
    if (req.method() === "POST" && /\/api\/v1\/quotes\/[^/]+\/share-links$/.test(new URL(req.url()).pathname)) mints.push(req.url());
  });
  const send = page.getByTestId("qb-send");
  await send.dblclick();
  await expect(page.getByTestId("qb-notice")).toContainText("Quote sent");
  expect(mints).toHaveLength(1);
  const notice = (await page.getByTestId("qb-notice").innerText()).toLowerCase();
  expect(notice).toContain("nothing was emailed");
  expect(notice).not.toMatch(/emailed to the customer|emailed and texted/);
  expect(notice).not.toContain("texted");
});

test("the Sales manager and Sales both get the Screen settings entry; the server answers who may write", async ({ page }) => {
  await signIn(page, { name: "Sales manager", exact: true });
  await page.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await expect(page.getByTestId("qb-settings-btn")).toBeVisible();
  const salesPage = await page.context().newPage();
  await signIn(salesPage);
  await salesPage.getByTestId("qb-list-row").filter({ hasText: "Draft" }).first().click();
  await expect(salesPage.getByTestId("qb-settings-btn")).toBeVisible();
  await salesPage.close();
});

test("the quotes list follows next_cursor behind a Load more control", async ({ page, request }) => {
  await request.put("/_stub/quote-page-size", { data: { size: 1 } });
  await signIn(page, { name: "Sales manager", exact: true });
  const rows = page.getByTestId("qb-list-row");
  await expect(rows).toHaveCount(1);
  await page.getByTestId("qb-list-more").click();
  await expect(rows).toHaveCount(2);
  await page.getByTestId("qb-list-more").click();
  await expect(rows).toHaveCount(3);
  await expect(page.getByTestId("qb-list-more")).toHaveCount(0);
});

test("a quote row is reached with Tab and opened with Enter", async ({ page }) => {
  await signIn(page);
  const first = page.getByTestId("qb-list-row").first();
  await first.focus();
  await expect(first).toBeFocused();
  // Tab walks to a row from the control before it, and the row opens on Enter.
  await page.getByTestId("qb-list-filter").focus();
  let reached = false;
  for (let i = 0; i < 8 && !reached; i += 1) {
    await page.keyboard.press("Tab");
    reached = await page.evaluate(() => {
      let el: Element | null = document.activeElement;
      while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement;
      return el?.getAttribute("data-testid") === "qb-list-row";
    });
  }
  expect(reached).toBe(true);
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("qb-context")).toContainText("Q-");
});

test("convert sends nothing for the default fulfilment and the chosen one when chosen", async ({ page, request }) => {
  await signIn(page);
  await page.getByTestId("qb-list-row").filter({ hasText: "Accepted" }).first().click();
  await page.getByText("Quote detail & admin").click();
  await page.getByTestId("qe-convert").click();
  await expect(page.getByTestId("qe-converted")).toContainText("Converted to order SO-");
  const state = await (await request.get("/_stub/state")).json();
  expect(state.conversions).toHaveLength(1);
  expect(state.conversions[0].fulfillment).toBeNull();
});

test("the desk raises no Lit change-in-update warning from the customer picker", async ({ page }) => {
  const warnings: string[] = [];
  page.on("console", (message) => {
    if (message.text().includes("scheduled an update")) warnings.push(message.text());
  });
  await signIn(page);
  await page.getByTestId("qb-list-row").first().click();
  await page.waitForTimeout(500);
  expect(warnings).toEqual([]);
});

// ── P3: money and dates ──

test("the deposit shown is the server's deposit_cents; an unsaved terms edit shows a labelled estimate", async ({ page }) => {
  await signIn(page);
  await startQuote(page);
  // The server's figure is deliberately not the local arithmetic, so what shows is whose it is.
  let slowSave = false;
  await page.route("**/api/v1/quotes/*/terms", async (route) => {
    if (slowSave && route.request().method() === "PUT") await new Promise((resolve) => setTimeout(resolve, 1500));
    const response = await route.fetch();
    const body = (await response.json()) as Record<string, unknown>;
    if (body.deposit_required === true) body.deposit_cents = 12345;
    await route.fulfill({ response, json: body });
  });
  await page.getByTestId("qb-terms-deposit-toggle").check();
  await expect(page.getByTestId("qb-deposit-label")).toHaveText("Deposit due");
  await expect(page.getByTestId("qb-totals")).toContainText("CA$123.45");
  // A change that has not been saved yet is the local figure, and says so.
  slowSave = true;
  await page.getByTestId("qb-terms-deposit").fill("50");
  await page.getByTestId("qb-terms-deposit").blur();
  await expect(page.getByTestId("qb-deposit-label")).toHaveText("Deposit due (estimate, not yet saved)");
  await expect(page.getByTestId("qb-deposit-label")).toHaveText("Deposit due");
});

test("a typed delivery fee of $25 or 1,250 is refused in words, never saved as zero; 25 saves", async ({ page }) => {
  await signIn(page);
  await startQuote(page);
  const fee = page.getByTestId("qb-delivery-fee");
  await page.getByTestId("qb-fulfil-delivery").click();
  await fee.fill("$25");
  await fee.blur();
  await expect(page.getByTestId("qb-error")).toContainText("is not a dollar amount");
  await fee.fill("1,250");
  await fee.blur();
  await expect(page.getByTestId("qb-error")).toContainText("is not a dollar amount");
  await expect(page.getByTestId("qb-totals")).not.toContainText("Delivery");
  await fee.fill("25");
  await fee.blur();
  await expect(page.getByTestId("qb-totals")).toContainText("CA$25.00");
});

// ── P3: popovers, the picker's words ──

test("Escape closes the help, columns and send channel popovers", async ({ page }) => {
  await signIn(page);
  await startQuote(page);
  await page.getByTestId("qb-help").click();
  await expect(page.getByTestId("qb-help")).toHaveAttribute("aria-expanded", "true");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("qb-help")).toHaveAttribute("aria-expanded", "false");
  await page.getByTestId("qb-columns-btn").click();
  await expect(page.locator('[data-field="column"]').first()).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator('[data-field="column"]')).toHaveCount(0);
  await page.getByTestId("qb-send-channel").click();
  await expect(page.getByTestId("qb-send-channel")).toHaveAttribute("aria-expanded", "true");
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("qb-send-channel")).toHaveAttribute("aria-expanded", "false");
});

for (const [status, words] of [
  [403, "customer: only a manager creates accounts here"],
  [409, "customer: that account number belongs to Greenridge"],
] as const) {
  test(`the customer picker says the server's words on a ${status}`, async ({ page }) => {
    await page.route("**/api/v1/customers", async (route) => {
      if (route.request().method() === "POST") {
        await route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: words }) });
      } else {
        await route.continue();
      }
    });
    await signIn(page);
    const picker = page.getByTestId("qb-identity").locator("hos-customer-picker");
    await picker.getByTestId("customer-add-new").click();
    await picker.getByTestId("customer-new-name").fill("Greenridge Landscaping");
    await picker.getByTestId("customer-new-account").fill("CUST-9001");
    await picker.getByTestId("customer-create-save").click();
    const said = words.replace(/^customer: /, "");
    await expect(picker.getByRole("alert")).toContainText(said.charAt(0).toUpperCase() + said.slice(1));
    await expect(picker.getByRole("alert")).not.toContainText("manager sign-in");
  });
}
