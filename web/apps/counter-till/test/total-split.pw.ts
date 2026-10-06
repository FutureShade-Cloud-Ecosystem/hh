// Row 985, the till's side: Payment and Summary show the Tax and Total the server's sale-preview
// states (pending while it loads, never a guess), refreshed when the lines, the customer or the
// fulfilment change; the split works from that total (Total to split, Left to assign, the last leg
// filled with the remainder, Ring split sale armed only when the legs match), the tenders are named
// Account, Card, Cash, and the server's split_total_mismatch reads in dollars from its fields.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  await page.getByTestId("pos-view").click();
  const radio = page.getByTestId("pos-layout-panels");
  if ((await radio.getAttribute("aria-checked")) !== "true") await radio.click();
  else await page.getByTestId("pos-view").click();
  await expect(page.getByTestId("pos-twopane")).toBeVisible();
}

/** The branch's drawer, found or opened: a ring needs an open till session. */
async function ensureSession(page: Page): Promise<void> {
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (await dot.isVisible()) return;
  await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
}

async function addLine(page: Page, query: string, hit: string): Promise<void> {
  await page.getByTestId("pos-scan-input").fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hit }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hit })).toHaveCount(1);
  await page.getByTestId("pos-scan-input").fill("");
}

async function openPaymentStep(page: Page): Promise<void> {
  await page.locator("hos-pos-flow-steps").first().getByRole("button", { name: /Payment/ }).click();
  await expect(page.getByTestId("pos-totals-card")).toBeVisible();
}

test.afterEach(async ({ page }) => {
  // The stub keeps the workspace's layout pick between legs: leave it on the stepper.
  try {
    if ((await page.getByTestId("pos-view").count()) === 0) return;
    await page.getByTestId("pos-view").click();
    const saved = page.waitForRequest((r) => r.method() === "PUT" && r.url().includes("/api/v1/pos/workspace"));
    await page.getByTestId("pos-layout-stepper").click();
    await saved;
  } catch {
    /* the page never reached the till */
  }
});

test("Payment shows Tax and Total from the server's preview, and Summary reads the same figures", async ({ page }) => {
  await signIn(page);
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals.getByTestId("pos-preview-tax")).toContainText("$0.52");
  await expect(totals.getByTestId("pos-preview-total")).toContainText("$4.52");
  await expect(totals.getByTestId("pos-ring-words")).toHaveCount(0);
  await expect(page.getByTestId("pos-complete-sale")).toContainText("total $4.52");

  await page.locator("hos-pos-flow-steps").first().getByRole("button", { name: /Summary/ }).click();
  const readback = page.getByTestId("pos-readback");
  await expect(readback.getByTestId("readback-preview-tax")).toContainText("$0.52");
  await expect(readback.getByTestId("readback-preview-total")).toContainText("$4.52");
});

test("while the preview loads the rows say pending and no figure is guessed, then the answer lands", async ({
  page,
}) => {
  let release: () => void = () => undefined;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/pos/sale-preview", async (route) => {
    await held;
    await route.continue();
  });
  await signIn(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals.getByTestId("pos-preview-pending")).toContainText("pending");
  await expect(totals.getByTestId("pos-preview-total")).toHaveCount(0);
  await expect(page.getByTestId("pos-complete-sale")).not.toContainText(/·\s*total \$/);
  release();
  await expect(totals.getByTestId("pos-preview-total")).toContainText("$4.52");
  await expect(totals.getByTestId("pos-preview-pending")).toHaveCount(0);
});

test("the preview is asked again when the lines, the customer or the fulfilment change", async ({ page }) => {
  const bodies: Array<Record<string, unknown>> = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/api/v1/pos/sale-preview") && request.method() === "POST") {
      bodies.push(request.postDataJSON() as Record<string, unknown>);
    }
  });
  await signIn(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  const total = page.getByTestId("pos-totals-card").getByTestId("pos-preview-total");
  await expect(total).toContainText("$4.52");
  const afterFirst = bodies.length;
  expect(afterFirst).toBeGreaterThan(0);
  expect((bodies.at(-1)?.lines as unknown[]).length).toBe(1);

  // A second line: the total follows (4.00 + 32.00, 13% tax).
  await addLine(page, "edge", "Edge Restraint 8 ft");
  await expect(total).toContainText("$40.68");
  expect((bodies.at(-1)?.lines as unknown[]).length).toBe(2);

  // The fulfilment: choosing COD asks again, naming the sale type and fulfilment.
  const before = bodies.length;
  await page.getByRole("button", { name: /^COD/ }).click();
  await expect.poll(() => bodies.length).toBeGreaterThan(before);
  expect(bodies.at(-1)?.sale_type).toBe("COD");
  expect(bodies.at(-1)?.fulfilment).toBeTruthy();
  // Nothing in any ask is a tender.
  for (const body of bodies) expect(body).not.toHaveProperty("tender");
});

test("an unreadable preview says the ring states the total, and shows no figure", async ({ page }) => {
  await page.route("**/api/v1/pos/sale-preview", (route) => route.fulfill({ status: 500, body: "{}" }));
  await signIn(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  const totals = page.getByTestId("pos-totals-card");
  await expect(totals.getByTestId("pos-ring-words")).toContainText("added at the ring");
  await expect(totals.getByTestId("pos-preview-total")).toHaveCount(0);
});

test("the split works from the server's total: Total to split, Left to assign, the last leg takes the remainder", async ({
  page,
}) => {
  await signIn(page);
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $4.52");
  // Tenders are named for people: Account, Card, Cash.
  const options = await panel.getByLabel("Tender 1 method").locator("option").allTextContents();
  expect(options.slice(0, 3)).toEqual(["Account", "Card", "Cash"]);
  // The last leg opens holding everything; the first is empty, so all of it is left until typed.
  const leg0 = panel.getByTestId("split-amount-0");
  const leg1 = panel.getByTestId("split-amount-1");
  await expect(leg1).toHaveValue("4.52");
  await leg0.fill("1.00");
  await expect(leg1).toHaveValue("3.52");
  await expect(panel.getByTestId("split-remaining")).toHaveText("Left to assign $0.00");
  await expect(panel.getByTestId("split-ring")).toBeEnabled();
  // The last leg is still an ordinary box: edit it and the legs stop matching.
  await leg1.fill("1.00");
  await expect(panel.getByTestId("split-remaining")).toHaveText("Left to assign $2.52");
  await expect(panel.getByTestId("split-ring")).toBeDisabled();
  await leg1.fill("3.52");
  await expect(panel.getByTestId("split-ring")).toBeEnabled();

  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await panel.getByTestId("split-ring").click();
  const sale = (await (await ringPost).json()) as Record<string, unknown>;
  expect(sale.total_cents).toBe(452);
  await expect(page.getByTestId("sold-panel")).toBeVisible();
});

test("the server's split_total_mismatch reads in dollars from its fields, never raw cents", async ({ page }) => {
  await signIn(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await panel.getByTestId("split-amount-0").fill("1.00");
  await expect(panel.getByTestId("split-ring")).toBeEnabled();
  // The server answers its refusal (the legs the till armed are overridden by a changed total).
  await page.route("**/api/v1/pos/sales", (route) =>
    route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: "split_total_mismatch",
        tendered_cents: 26600,
        total_cents: 30058,
        message: "The server's own sentence, which the till must not print.",
      }),
    }),
  );
  await panel.getByTestId("split-ring").click();
  const banner = page.getByRole("alert");
  // Read from the fields (26600 and 30058), not from the server's message, which differs here.
  await expect(banner).toContainText("The tenders add up to $266.00, but the sale total is $300.58.");
  await expect(banner).not.toContainText("The server's own sentence");
  await expect(banner).not.toContainText("cents");
  await expect(banner).not.toContainText("30058");
});

test("Ring split sale stays disabled while the total is pending, and arms when it arrives", async ({ page }) => {
  let release: () => void = () => undefined;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/v1/pos/sale-preview", async (route) => {
    await held;
    await route.continue();
  });
  await signIn(page);
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await panel.getByTestId("split-amount-0").fill("1.00");
  await panel.getByTestId("split-amount-1").fill("1.00");
  await expect(panel.getByTestId("split-total-pending")).toHaveText("Total to split: pending");
  await expect(panel.getByTestId("split-typed")).toHaveCount(0);
  await expect(panel.getByTestId("split-ring")).toBeDisabled();
  release();
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $4.52");
  await panel.getByTestId("split-amount-1").fill("3.52");
  await expect(panel.getByTestId("split-ring")).toBeEnabled();
});

test("Ring split sale stays disabled when the preview cannot be read", async ({ page }) => {
  await page.route("**/api/v1/pos/sale-preview", (route) => route.fulfill({ status: 500, body: "{}" }));
  await signIn(page);
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await panel.getByTestId("split-amount-0").fill("1.00");
  await panel.getByTestId("split-amount-1").fill("1.00");
  await expect(panel.getByTestId("split-total-pending")).toBeVisible();
  await expect(panel.getByTestId("split-total")).toHaveCount(0);
  await expect(panel.getByTestId("split-ring")).toBeDisabled();
});

test("a ticket change after the legs were typed disarms Ring split sale until the new total arrives", async ({
  page,
}) => {
  let hold = false;
  let release: () => void = () => undefined;
  await page.route("**/api/v1/pos/sale-preview", async (route) => {
    if (hold) {
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    }
    await route.continue();
  });
  await signIn(page);
  await ensureSession(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await openPaymentStep(page);
  await page.getByRole("button", { name: "Split" }).click();
  const panel = page.getByTestId("pos-split-panel");
  await panel.getByTestId("split-amount-0").fill("1.00");
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $4.52");
  await expect(panel.getByTestId("split-ring")).toBeEnabled();

  hold = true;
  await addLine(page, "edge", "Edge Restraint 8 ft");
  // The legs typed against $4.52 are stale the moment the ticket changes.
  await expect(panel.getByTestId("split-total-pending")).toBeVisible();
  await expect(panel.getByTestId("split-ring")).toBeDisabled();
  await expect.poll(() => hold).toBe(true);
  await page.waitForTimeout(500);
  release();
  await expect(panel.getByTestId("split-total")).toHaveText("Total to split $40.68");
  // The untouched last leg followed the remainder, so the legs match the new total.
  await expect(panel.getByTestId("split-amount-1")).toHaveValue("39.68");
  await expect(panel.getByTestId("split-ring")).toBeEnabled();
});

test("a late answer for an older ticket is dropped, and quick edits make one ask", async ({ page }) => {
  const asked: Array<{ qty: number; at: number }> = [];
  let holdFirst = true;
  let releaseFirst: () => void = () => undefined;
  await page.route("**/api/v1/pos/sale-preview", async (route) => {
    const body = route.request().postDataJSON() as { lines: Array<{ qty_entered: number }> };
    asked.push({ qty: body.lines[0]?.qty_entered ?? 0, at: Date.now() });
    if (holdFirst && asked.length === 1) {
      holdFirst = false;
      // The first answer is held, then released AFTER a newer ticket has answered, with a figure
      // that would be wrong for it.
      await new Promise<void>((resolve) => {
        releaseFirst = resolve;
      });
      const real = await route.fetch();
      const answer = (await real.json()) as Record<string, unknown>;
      await route.fulfill({ response: real, json: { ...answer, tax_cents: 1, total_cents: 101 } });
      return;
    }
    await route.continue();
  });
  await signIn(page);
  await addLine(page, "holland", "Holland Stone 60 mm");
  await expect.poll(() => asked.length).toBe(1);

  // Three quick quantity steps inside the debounce window: the ticket is now four sf.
  await page.evaluate(async () => {
    const walk = (root: ParentNode): Element[] => {
      const out: Element[] = [];
      for (const el of root.querySelectorAll("*")) {
        out.push(el);
        if (el.shadowRoot) out.push(...walk(el.shadowRoot));
      }
      return out;
    };
    const plus = walk(document).find((el) => el.matches("button[aria-label^='Increase quantity']")) as HTMLElement;
    for (let i = 0; i < 3; i += 1) {
      plus.click();
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
  });
  await openPaymentStep(page);
  const total = page.getByTestId("pos-totals-card").getByTestId("pos-preview-total");
  await expect(total).toContainText("$18.08");
  // One ask for the settled ticket, not one per step (the first, held ask is the other).
  expect(asked.length).toBe(2);
  expect(asked[1]?.qty).toBe(4);
  // The held answer for the one-line ticket lands now and changes nothing on screen.
  releaseFirst();
  await page.waitForTimeout(500);
  await expect(total).toContainText("$18.08");
  await expect(total).not.toContainText("$1.01");
});
