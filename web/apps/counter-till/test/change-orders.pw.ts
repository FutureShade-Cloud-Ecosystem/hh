// The change order surface, stage 1: the one "Change order" action on the
// sold card, before the pick (the line edit, DIB-175) and after it (the
// SCOPE:F10 change request), held against the ported panels and the stub's
// change routes, which serve the core's shapes and refusal words field by
// field.
//
// The stories this suite holds are the brief's own legs:
//   before the pick, the line edit (the manager edits a quantity, the trail
//     groups the audit rows, the clerk is offered no line controls);
//   after the pick, raise a change and see its state (the delta the server
//     priced, the chip and the stage sentence, the buttons the server offers);
//   the yard's acknowledgement walled on the till answers plain words naming
//     who may act (ROLE-APP-OBLIGATIONS 1, with a leg proving the words show);
//   cancel by the requester (Withdraw);
//   apply once approved (the yard acknowledged out of band, the way stage 2's
//     app will), and a second apply refused, driven through the API because
//     the server offers no button on an applied change (next_actions is
//     empty), which is the re-entrancy ruling made visible.
import { expect, test, type Page } from "@playwright/test";
import { closeAccountMenu, openAccountMenu } from "./account-menu";

/** The screen-side rendering of integer cents, for comparing against the app. */
function dollars(cents: number): string {
  const negative = cents < 0;
  const magnitude = Math.abs(cents);
  const whole = Math.floor(magnitude / 100);
  const remainder = magnitude - whole * 100;
  return `${negative ? "-" : ""}$${whole.toLocaleString("en-CA")}.${String(remainder).padStart(2, "0")}`;
}

/** Mint a demo session for one flow and return the bearer header's value. */
async function bearer(page: Page, flow: "sales" | "sales-manager"): Promise<string> {
  const minted = await page.request.post("/api/v1/demo/session", { data: { flow } });
  expect(minted.ok()).toBe(true);
  const body = (await minted.json()) as { token?: string };
  expect(typeof body.token).toBe("string");
  return body.token as string;
}

/** Sign in through the entry cards and land on the ported terminal. */
async function signIn(page: Page, role: "Sales" | "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

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

/** Open one step of the linear flow by its head (a no-op when it is already open). */
async function openStep(page: Page, step: "customer" | "items" | "payment" | "summary"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      await page.waitForTimeout(250);
      if ((await head.getAttribute("aria-expanded")) === "true") return;
    } catch {
      // closed again by the advance: press once more
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

/** Search from the counter's own field and add the hit: the drop row's click adds. */
async function searchAndAdd(page: Page, query: string, hitName: string): Promise<void> {
  await openStep(page, "items");
  const field = page.getByTestId("pos-scan-input");
  await field.fill(query);
  const row = page.getByTestId("drop-row").filter({ hasText: hitName }).first();
  await expect(row).toBeVisible();
  await row.click();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: hitName })).toHaveCount(1);
}

/** Ring one cash walk-in sale of Charcoal Stone and return the committed sale's order id. */
async function ringCharcoalSale(page: Page): Promise<{ orderId: string; orderNo: string }> {
  await ensureSession(page);
  await searchAndAdd(page, "charcoal", "Charcoal Stone 60 mm");
  const qty = page.getByRole("textbox", { name: "Quantity: Charcoal Stone 60 mm" });
  await qty.fill("3");
  await qty.blur();
  await expect(page.getByTestId("pos-line-row").filter({ hasText: "Charcoal Stone 60 mm" }).locator(".ltot")).toHaveText(
    "$12.00",
  );
  await openStep(page, "payment");
  const ringPost = page.waitForResponse(
    (response) => response.url().endsWith("/api/v1/pos/sales") && response.request().method() === "POST",
  );
  await page.getByTestId("pos-complete-sale").click();
  const sale = (await (await ringPost).json()) as { order_id: string; order_no: string };
  await expect(page.getByTestId("sold-panel")).toBeVisible();
  return { orderId: sale.order_id, orderNo: sale.order_no };
}

/** Open the sold card's order admin disclosure and the one Change order action inside it. */
async function openChangeAction(page: Page): Promise<void> {
  const admin = page.getByTestId("pos-order-admin");
  if ((await admin.getAttribute("open")) === null) {
    await admin.locator("summary", { hasText: "Order admin" }).click();
  }
  await expect(admin).toHaveAttribute("open", "");
  const change = page.getByTestId("order-change");
  if ((await change.getAttribute("open")) === null) {
    await change.locator("summary", { hasText: "Change order" }).click();
  }
  await expect(change).toHaveAttribute("open", "");
}

test("before the pick, the Change order action opens the line edit, and the trail groups the edit", async ({
  page,
}) => {
  await signIn(page, "Sales manager");
  await ringCharcoalSale(page);
  await openChangeAction(page);

  // The one action opened the pre pick flow: the mode note says why.
  await expect(page.getByTestId("change-mode-note")).toContainText("the pick has not started");
  // The line controls the SERVER offered (editable_fields: the manager holds
  // the line cells on a confirmed order), the quantity prefilled from the read.
  const qty = page.getByTestId("order-edit-qty");
  await expect(qty).toBeVisible();
  // The read serves the quantity as the DECIMAL string the column stores.
  await expect(qty).toHaveValue("3.0000");
  await qty.fill("5");
  await qty.blur();
  await expect(page.getByTestId("order-edit-saved")).toContainText("Saved.");
  // The re-read shows the server's arithmetic: 5 at $4.00 is a $20.00 line,
  // and the recomputed total follows it (13% HST, the stub's Ontario rate).
  const edit = page.getByTestId("order-edit");
  await expect(edit).toContainText("$20.00");
  await expect(edit).toContainText("Total $22.60");
  // The trail reads as ONE action with its consequences nested, not five rows.
  const trail = page.getByTestId("order-edits");
  await expect(trail).toContainText("Quantity");
  await expect(trail).toContainText("3.0000");
  await expect(trail).toContainText("5.0000");
  await expect(trail.locator("tbody tr")).toHaveCount(1);
  await expect(trail).toContainText("also");
});

test("before the pick, the clerk's Change order action offers no line controls", async ({ page }) => {
  await signIn(page, "Sales");
  await ringCharcoalSale(page);
  await openChangeAction(page);
  await expect(page.getByTestId("change-mode-note")).toContainText("the pick has not started");
  // inside_sales holds no manage cell: editable_fields carries the date alone,
  // so no quantity input, no Remove, no Add line renders.
  await expect(page.getByTestId("order-edit-qty")).toHaveCount(0);
  await expect(page.getByTestId("order-edit-remove")).toHaveCount(0);
  await expect(page.getByTestId("order-edit-add")).toHaveCount(0);
  await expect(page.getByTestId("order-edit-date")).toBeVisible();
});

test("after the pick, a raised change shows its server-priced delta, its state and its offered actions", async ({
  page,
}) => {
  await signIn(page, "Sales manager");
  const sale = await ringCharcoalSale(page);

  // The raise on an order whose pick has not started is refused in the
  // core's own words, naming the line-edit path (the API leg: the surface
  // itself already opened the line edit for this state).
  const manager = await bearer(page, "sales-manager");
  const early = await page.request.post(`/api/v1/orders/${sale.orderId}/changes`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
    data: { reason: "too early", lines: [{ op: "QTY", line_no: 1, qty_entered: "5" }] },
  });
  expect(early.status()).toBe(409);
  expect(((await early.json()) as { error: string }).error).toContain("pick has not started");

  // The pick starts (the transition the lifecycle panel will carry): opening
  // the action re-reads the order, and the flow is the change request now.
  const picking = await page.request.post(`/api/v1/orders/${sale.orderId}/picking`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
  });
  expect(picking.ok()).toBe(true);
  await openChangeAction(page);
  await expect(page.getByTestId("change-mode-note")).toContainText("The yard has this order");

  await page.getByTestId("change-raise-line").fill("1");
  await page.getByTestId("change-raise-qty").fill("5");
  await page.getByTestId("change-raise-reason").fill("Customer wants two more");
  await page.getByTestId("change-raise").click();
  const card = page.locator("[data-change-id]");
  await expect(card).toHaveCount(1);
  await expect(card.locator("[data-change-status]")).toHaveText("With yard");
  // The delta the server priced: 3 to 5 at $4.00 is $8.00, and the money
  // never rode from the till.
  await expect(card.locator("[data-change-delta]")).toHaveText(dollars(800));
  await expect(card.locator("[data-finance-required]")).toHaveCount(0);
  await expect(card.locator("[data-change-stage]")).toContainText("Waiting on the yard.");
  await expect(card).toContainText("\"Customer wants two more\"");
  await expect(card.locator("td").first()).toContainText("Line 1 Charcoal Stone 60 mm: 3 to 5");
  // The buttons are the server's next_actions for this caller, nothing else.
  await expect(card.locator("button[data-change-action=yard_ack]")).toBeVisible();
  await expect(card.locator("button[data-change-action=reject]")).toBeVisible();
  await expect(card.locator("button[data-change-action=cancel]")).toBeVisible();
  await expect(card.locator("button[data-change-action=apply]")).toHaveCount(0);
});

test("the yard's acknowledgement is walled on the till, and the refusal names who may act", async ({ page }) => {
  await signIn(page, "Sales manager");
  const sale = await ringCharcoalSale(page);
  const manager = await bearer(page, "sales-manager");
  const picking = await page.request.post(`/api/v1/orders/${sale.orderId}/picking`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
  });
  expect(picking.ok()).toBe(true);
  await openChangeAction(page);
  await page.getByTestId("change-raise-line").fill("1");
  await page.getByTestId("change-raise-qty").fill("5");
  await page.getByTestId("change-raise").click();
  await expect(page.locator("[data-change-status]")).toHaveText("With yard");

  // The server offered the ack in next_actions (PENDING_YARD is not manager
  // gated), but the route's census row walls it to yard-pick work, and the
  // till's logins hold none of it: the bare Forbidden word arrives and the
  // surface answers it in plain words naming who may act.
  await page.locator("button[data-change-action=yard_ack]").click();
  await expect(page.getByTestId("change-error")).toContainText(
    "Only the yard team or the yard manager may acknowledge a change for the yard.",
  );
});

test("the requester withdraws a raised change, and the state says withdrawn", async ({ page }) => {
  await signIn(page, "Sales manager");
  const sale = await ringCharcoalSale(page);
  const manager = await bearer(page, "sales-manager");
  await page.request.post(`/api/v1/orders/${sale.orderId}/picking`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
  });
  await openChangeAction(page);
  await page.getByTestId("change-raise-line").fill("1");
  await page.getByTestId("change-raise-qty").fill("5");
  await page.getByTestId("change-raise").click();
  await expect(page.locator("[data-change-status]")).toHaveText("With yard");

  await page.locator("button[data-change-action=cancel]").click();
  await expect(page.locator("[data-change-status]")).toHaveText("Withdrawn");
  await expect(page.locator("[data-change-stage]")).toHaveText("Withdrawn.");
  // A decided change offers nothing.
  await expect(page.locator("[data-change-id] button")).toHaveCount(0);
});

/** Close and reopen the one Change order action: reopening re-reads, which is how the legs see a
 *  decision the yard or finance made out of band (through the API, the way stages 2 and 3 will). */
async function reopenChange(page: Page): Promise<void> {
  const change = page.getByTestId("order-change");
  await change.locator("summary").click(); // close
  await change.locator("summary").click(); // reopen: the toggle re-reads
}

test("an approved change applies once from the till, and a second apply is refused", async ({ page }) => {
  await signIn(page, "Sales manager");
  const sale = await ringCharcoalSale(page);
  const manager = await bearer(page, "sales-manager");
  await page.request.post(`/api/v1/orders/${sale.orderId}/picking`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
  });
  await openChangeAction(page);
  await page.getByTestId("change-raise-line").fill("1");
  await page.getByTestId("change-raise-qty").fill("5");
  await page.getByTestId("change-raise").click();
  const changeId = await page.locator("[data-change-id]").getAttribute("data-change-id");
  expect(typeof changeId).toBe("string");

  // A second raise while one is live is refused at the door, the core's words.
  const secondRaise = await page.request.post(`/api/v1/orders/${sale.orderId}/changes`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
    data: { reason: "another", lines: [{ op: "QTY", line_no: 1, qty_entered: "6" }] },
  });
  expect(secondRaise.status()).toBe(409);
  expect(((await secondRaise.json()) as { error: string }).error).toContain("already has an open change request");

  // The yard acknowledged out of band (stage 2's app): an $8.00 delta is
  // under the threshold, so the ladder lands on APPROVED. Reopening the
  // action re-reads the change.
  const ack = await page.request.post("/_stub/change-decide", {
    data: { change_id: changeId, decide: "yard-ack", note: "can still do it" },
  });
  expect(ack.ok()).toBe(true);
  await reopenChange(page);
  await expect(page.locator("[data-change-status]")).toHaveText("Approved");
  await expect(page.locator("[data-change-stage]")).toContainText("Applying writes the change");

  // Apply once: the manager holds the apply cell, the state flips, and the
  // sold card's money is the server's recomputed arithmetic.
  await page.locator("button[data-change-action=apply]").click();
  await expect(page.locator("[data-change-status]")).toHaveText("Applied");
  await expect(page.locator("[data-change-stage]")).toHaveText("Applied to the order.");
  await expect(page.locator("[data-change-id] button")).toHaveCount(0);

  // A second apply is refused by the closed row. The surface offers no button
  // (the server's next_actions on an applied change are empty), so the
  // re-entrancy proof rides the API: the refusal is the core's own words.
  const again = await page.request.post(`/api/v1/order-changes/${changeId}/apply`, {
    headers: { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" },
  });
  expect(again.status()).toBe(409);
  expect(((await again.json()) as { error: string }).error).toContain("already closed");
});

test("the acceptance walk: a $600 change waits for the manager, a $40 change does not", async ({ page }) => {
  await signIn(page, "Sales manager");
  const sale = await ringCharcoalSale(page);
  const manager = await bearer(page, "sales-manager");
  const auth = { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" };
  await page.request.post(`/api/v1/orders/${sale.orderId}/picking`, { headers: auth });
  await openChangeAction(page);

  // A $600.00 change: 3 to 153 at $4.00 the unit, priced by the server. At the
  // $500 threshold, so the finance chip and the stage sentence both say so.
  await page.getByTestId("change-raise-line").fill("1");
  await page.getByTestId("change-raise-qty").fill("153");
  await page.getByTestId("change-raise-reason").fill("Customer added two more pallets");
  await page.getByTestId("change-raise").click();
  const bigCard = page.locator("[data-change-id]").first();
  await expect(bigCard.locator("[data-change-status]")).toHaveText("With yard");
  await expect(bigCard.locator("[data-change-delta]")).toHaveText(dollars(60000));
  await expect(bigCard.locator("[data-finance-required]")).toBeVisible();
  await expect(bigCard.locator("[data-change-stage]")).toContainText("over the approval threshold");

  // The yard acknowledges (stage 2's app drives this): a material delta waits
  // for the manager, and the till says so without offering Apply.
  const bigId = await bigCard.getAttribute("data-change-id");
  expect(typeof bigId).toBe("string");
  const bigAck = await page.request.post("/_stub/change-decide", {
    data: { change_id: bigId, decide: "yard-ack" },
  });
  expect(bigAck.ok()).toBe(true);
  await reopenChange(page);
  await expect(bigCard.locator("[data-change-status]")).toHaveText("With finance");
  await expect(bigCard.locator("[data-change-stage]")).toContainText("needs a finance approval");
  await expect(bigCard.locator("button[data-change-action=apply]")).toHaveCount(0);

  // The manager approves (stage 3's app drives this), and the change applies once.
  const sign = await page.request.post("/_stub/change-decide", {
    data: { change_id: bigId, decide: "finance-approve", note: "customer signed the revision" },
  });
  expect(sign.ok()).toBe(true);
  await reopenChange(page);
  await expect(bigCard.locator("[data-change-status]")).toHaveText("Approved");
  await bigCard.locator("button[data-change-action=apply]").click();
  await expect(bigCard.locator("[data-change-status]")).toHaveText("Applied");
  await expect(bigCard.locator("[data-change-stage]")).toHaveText("Applied to the order.");

  // A $40.00 change on the same order (153 to 163 at $4.00): under the
  // threshold, so the yard's acknowledgement alone lands it on APPROVED and
  // no finance signature is asked for or waited on.
  await page.getByTestId("change-raise-line").fill("1");
  await page.getByTestId("change-raise-qty").fill("163");
  await page.getByTestId("change-raise-reason").fill("One bundle more on the same load");
  await page.getByTestId("change-raise").click();
  const smallCard = page.locator("[data-change-id]").first();
  await expect(smallCard.locator("[data-change-delta]")).toHaveText(dollars(4000));
  await expect(smallCard.locator("[data-finance-required]")).toHaveCount(0);
  const smallId = await smallCard.getAttribute("data-change-id");
  expect(typeof smallId).toBe("string");
  const smallAck = await page.request.post("/_stub/change-decide", {
    data: { change_id: smallId, decide: "yard-ack" },
  });
  expect(smallAck.ok()).toBe(true);
  await reopenChange(page);
  await expect(smallCard.locator("[data-change-status]")).toHaveText("Approved");
  await smallCard.locator("button[data-change-action=apply]").click();
  await expect(smallCard.locator("[data-change-status]")).toHaveText("Applied");
  await expect(page.locator("[data-change-stage]").filter({ hasText: "Applied to the order." })).toHaveCount(2);
});

// The review screens: the change surface at 1440 and 390, light and brand. Written only when asked,
// the discount suite's convention (an env var names the directory; without it this block skips).
const shots = process.env.CHANGE_SHOTS_DIR ?? "";
test.describe("review screens", () => {
  test.skip(shots === "", "CHANGE_SHOTS_DIR is not set");

  for (const theme of ["light", "brand"] as const) {
    for (const viewport of [1440, 390] as const) {
      test(`${theme} ${viewport}: the line edit, then the change request`, async ({ page }) => {
        await page.setViewportSize({ width: viewport, height: viewport === 1440 ? 900 : 844 });
        await signIn(page, "Sales manager");
        if (theme === "brand") {
          await openAccountMenu(page);
          await page.getByTestId("theme-control").click();
          await closeAccountMenu(page);
        }
        const sale = await ringCharcoalSale(page);
        const manager = await bearer(page, "sales-manager");
        const auth = { Authorization: `Bearer ${manager}`, "X-Org": "hh-demo" };

        // The pre pick half: the line edit with a saved edit beneath it.
        await openChangeAction(page);
        const qty = page.getByTestId("order-edit-qty");
        await qty.fill("5");
        await qty.blur();
        await expect(page.getByTestId("order-edit-saved")).toContainText("Saved.");
        await page.screenshot({ path: `${shots}/change-edit-${theme}-${viewport}.png`, fullPage: true });

        // The post pick half: the raise form, a raised change with its priced
        // delta, and the stock the order holds.
        await page.request.post(`/api/v1/orders/${sale.orderId}/picking`, { headers: auth });
        await reopenChange(page);
        await page.getByTestId("change-raise-line").fill("1");
        await page.getByTestId("change-raise-qty").fill("7");
        await page.getByTestId("change-raise-reason").fill("Customer wants more on the same load");
        await page.getByTestId("change-raise").click();
        // The line was edited to 5 above, so 5 to 7 at $4.00 is +$8.00.
        await expect(page.locator("[data-change-delta]")).toHaveText(dollars(800));
        await page.screenshot({ path: `${shots}/change-raise-${theme}-${viewport}.png`, fullPage: true });
      });
    }
  }
});
