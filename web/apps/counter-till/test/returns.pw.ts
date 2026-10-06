// The Counter Till's W8 exit test: the returns story end to end against the
// stubbed harness (the stub serves the demo routes and the till's return
// paths in the contract's shapes; the vite dev server proxies /api to it).
// The story, as the plan's screens 8 to 10 spell it: the sale found by its
// number, one line returned in cash within the approval amount and the
// drawer's Z report dropping by it, a return above the amount refused with
// the amount and the approvers named, the manager's card through the dialog
// completing it with the receipt naming both and the header still reading the
// clerk, the same return completing with no dialog when the approver role is
// the one signed in, and the card rail saying in words that nothing reached a
// card. The review round adds the second find's clean slate, the plain 409's
// own words, the changed draft's dropped key, the simulated flag's false
// side, and the credential's ledger checks. The stub's world carries no tax,
// so every figure is a line's own total: 1 of the Holland paver line is
// $60.00, the full Wall block line is $360.00 over the $250.00 approval
// amount, the second cash sale's line the same again, the first card sale's
// edger $89.00, and the second card sale's sandstone edger $75.00.
import { expect, test, type Locator, type Page } from "@playwright/test";

// The stub's open drawer at the demo branch, by its own id (the uuid helper's
// spelling of 90001): the Z report is read through the same /api proxy the
// app uses, under a bearer the stub's own demo mint issued, exactly as the
// real gate would ask one.

/** Row 955: Returns is its own surface; open it from the bar (a no-op when it is already up). */
async function openReturns(page: Page): Promise<void> {
  if (!(await page.getByTestId("returns").isVisible())) await page.getByTestId("till-returns-entry").click();
  await expect(page.getByTestId("returns")).toBeVisible();
}

const DRAWER_SESSION_ID = "00000000-0000-4000-8000-000000090001";
const HOLLAND_LINE = "Quantity to return: Holland paver, bevelled, each";
const WALLBLOCK_LINE = "Quantity to return: Wall block, charcoal, each";
const EDGER_LINE = "Quantity to return: Garden edger, grey, each";
const SANDSTONE_LINE = "Quantity to return: Garden edger, sandstone, each";

async function findSale(page: Page, orderNo: string): Promise<void> {
  await openReturns(page);
  await page.getByLabel("Sale number").fill(orderNo);
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-sale")).toContainText(orderNo);
}

async function signIn(page: Page, role: "Sales" | "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText(role);
}

async function drawerExpectedCash(page: Page): Promise<number> {
  const mint = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const minted = (await mint.json()) as { token?: string };
  const answer = await page.request.get(`/api/v1/till/sessions/${DRAWER_SESSION_ID}/zreport`, {
    headers: { Authorization: `Bearer ${minted.token ?? ""}` },
  });
  const report = (await answer.json()) as { cash?: { expected_cents?: number } };
  if (typeof report.cash?.expected_cents !== "number") {
    throw new Error(`the Z report did not answer an expected cash figure: ${JSON.stringify(report)}`);
  }
  return report.cash.expected_cents;
}

async function stubState(page: Page): Promise<{
  keys: string[];
  lastReturnBody: Record<string, unknown>;
  sightings: { path: string; approverHeader: boolean }[];
}> {
  const answer = await page.request.get("/_stub/state");
  return (await answer.json()) as {
    keys: string[];
    lastReturnBody: Record<string, unknown>;
    sightings: { path: string; approverHeader: boolean }[];
  };
}

test("a return of one line in cash within 250.00 commits, and the drawer's Z report drops by it", async ({ page }) => {
  await signIn(page, "Sales");
  await findSale(page, "CT-1001");
  await expect(page.getByText("Holland paver, bevelled, each")).toBeVisible();
  await expect(page.getByText("sold 2, returned 0")).toBeVisible();
  // A cash sale has no card capture to reverse: the card method is greyed,
  // and its reason is in words beside it.
  await expect(page.getByRole("button", { name: "Card", exact: true })).toBeDisabled();
  await expect(page.getByText("No card refund: this sale was not paid by card", { exact: false })).toBeVisible();

  const before = await drawerExpectedCash(page);
  await page.getByLabel(HOLLAND_LINE).fill("1");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();
  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$60.00");
  await expect(receipt).toContainText("cash, from the drawer");
  await expect(receipt).toContainText("Rung by Sales (demo).");

  const after = await drawerExpectedCash(page);
  // The Holland paver line at 1 of 2: 6000 cents, the refund the receipt
  // shows; the drawer's expected cash drops by exactly it.
  expect(after).toBe(before - 6000);
});

test("a return above 250.00 is refused with the amount and the approvers named, and completes through the Sales manager's card with the header still reading Sales", async ({ page }) => {
  await signIn(page, "Sales");
  await findSale(page, "CT-1001");
  await page.getByLabel(WALLBLOCK_LINE).fill("6");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();

  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toContainText("$250.00");
  await expect(refusal).toContainText("Sales manager");
  await expect(refusal).toContainText("Branch manager");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await refusal.getByRole("button", { name: "Manager approval" }).click();
  const dialog = page.getByRole("dialog", { name: "Manager approval" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("the till stays the clerk's");
  await dialog.getByRole("button", { name: "Sales manager" }).click();

  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$360.00");
  await expect(receipt).toContainText("Approved by Sales manager (demo).");
  await expect(receipt).toContainText("Rung by Sales (demo).");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // The till is still the clerk's: the manager's credential rode the one
  // call and nothing else, and the chrome never changed hands.
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");

  // The credential's ledger (the review round's P2-3): the send that carried
  // the header is this story's approval send alone, and no other request the
  // app made, the staff lookups included, ever carried it.
  await findSale(page, "CT-1002");
  // The chrome is read from custody again after the next find: an adoption
  // that landed late, after the receipt's own assertion, would still show
  // here (the re-read's P2-3, the trailing adoption shape).
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  const state = await stubState(page);
  const posts = state.sightings.filter((sighting) => sighting.path === "/api/v1/pos/returns");
  expect(posts.length).toBeGreaterThanOrEqual(2);
  expect(posts[posts.length - 1].approverHeader).toBe(true);
  expect(posts[posts.length - 2].approverHeader).toBe(false);
  for (const sighting of state.sightings) {
    if (sighting.path !== "/api/v1/pos/returns") {
      expect(sighting.approverHeader).toBe(false);
    }
  }
});

test("a sign out under an in flight approval leaves the desk blank for the next person", async ({ page }) => {
  // The re-read's P3, the receipt half: the send's answer landing after the
  // sign out writes nothing, the receipt included. The harness holds the
  // approval send's response until the till has changed hands.
  let releaseApproval: (() => void) | undefined;
  const held = new Promise<void>((resolve) => {
    releaseApproval = resolve;
  });
  await page.route("**/api/v1/pos/returns", async (route) => {
    const response = await route.fetch();
    if (await route.request().headerValue("x-approver-authorization")) {
      await held;
    }
    await route.fulfill({ response });
  });

  await signIn(page, "Sales");
  await findSale(page, "CT-1004");
  await page.getByLabel(WALLBLOCK_LINE).fill("6");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();
  const refusal = page.getByTestId("refund-refusal");
  await expect(refusal).toBeVisible();
  await refusal.getByRole("button", { name: "Manager approval" }).click();
  await page
    .getByRole("dialog", { name: "Manager approval" })
    .getByRole("button", { name: "Sales manager" })
    .click();

  // The send is in the harness's hand: the till changes sides under it.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  releaseApproval?.();

  // Whoever signs in next finds a blank desk: no receipt from the send that
  // landed after they took the till.
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
  await expect(page.getByTestId("refund-receipt")).toHaveCount(0);
  await expect(page.getByTestId("returns-sale")).toHaveCount(0);
});

test("as Sales manager the same return completes with no refusal and no dialog, and the receipt names the manager as the one who approved it", async ({ page }) => {
  await signIn(page, "Sales manager");
  await findSale(page, "CT-1002");
  await page.getByLabel(WALLBLOCK_LINE).fill("6");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();

  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$360.00");
  // The approver line on the caller's own path (the review round's ruling on
  // the plan's screen 9): the caller's own display name and role, beside the
  // clerk line naming the same till.
  await expect(receipt).toContainText("Approved by the Sales manager on this till: Sales manager (demo).");
  await expect(receipt).toContainText("Rung by Sales manager (demo).");
  // The approver role's own commit never earns a refusal, so no dialog and
  // no refusal block render at any point of the path.
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales manager");
});

test("with the status route answering simulated false, the card rail makes no simulated claim", async ({ page }) => {
  // The false side of the simulator's flag (the review round's P2-4): the
  // harness answers the status route itself, and the desk must not say
  // nothing reached a card when the flag does not say so.
  await page.route("**/api/v1/payments/config", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        configured: true,
        merchant_id: "hh-demo-merchant",
        processor: "stub",
        currency: "USD",
        simulated: false,
      }),
    }),
  );
  await signIn(page, "Sales");
  await findSale(page, "CT-2001");
  await page.getByRole("button", { name: "Card", exact: true }).click();
  await expect(page.getByTestId("simulated-notice")).toHaveCount(0);

  await page.getByLabel(EDGER_LINE).fill("1");
  await page.getByRole("button", { name: "Refund card" }).click();
  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$89.00");
  await expect(receipt).toContainText("card");
  await expect(page.getByTestId("simulated-notice")).toHaveCount(0);
});

test("a card return on the simulator's true side shows the simulated notice", async ({ page }) => {
  await signIn(page, "Sales");
  await findSale(page, "CT-2002");
  const card = page.getByRole("button", { name: "Card", exact: true });
  await expect(card).toBeEnabled();
  await card.click();
  await expect(page.getByTestId("simulated-notice")).toContainText("Simulated refund: nothing reached a card.");

  await page.getByLabel(SANDSTONE_LINE).fill("1");
  await page.getByRole("button", { name: "Refund card" }).click();
  const receipt = page.getByTestId("refund-receipt");
  await expect(receipt).toContainText("$75.00");
  await expect(receipt).toContainText("Simulated refund: nothing reached a card.");
});

test("a second find clears the last draft's fields on the screen and in the body", async ({ page }) => {
  // The review round's P1: the fields are uncontrolled, so a find that resets
  // the model must rebuild the screen too, or the till sends one return while
  // showing another.
  await signIn(page, "Sales");
  await findSale(page, "CT-1001");
  await page.getByLabel(HOLLAND_LINE).fill("2");
  await page.getByLabel("Reason").selectOption("DAMAGED");
  await page.getByLabel("Note").fill("corner chipped");
  await page.getByLabel("Restock the goods").uncheck();

  await findSale(page, "CT-2002");
  await expect(page.getByLabel(SANDSTONE_LINE)).toHaveValue("");
  await expect(page.getByLabel("Reason")).toHaveValue("OTHER");
  await expect(page.getByLabel("Note")).toHaveValue("");
  await expect(page.getByLabel("Restock the goods")).toBeChecked();

  await page.getByRole("button", { name: "Card", exact: true }).click();
  await page.getByLabel(SANDSTONE_LINE).fill("1");
  await page.getByRole("button", { name: "Refund card" }).click();
  await expect(page.getByTestId("refund-receipt")).toContainText("$75.00");
  const state = await stubState(page);
  expect(state.lastReturnBody["order_no"]).toBe("CT-2002");
  expect(state.lastReturnBody["refund_method"]).toBe("CARD");
  expect(state.lastReturnBody["reason"]).toBe("");
  expect(state.lastReturnBody["restock"]).toBe(true);
  const lines = state.lastReturnBody["lines"] as { reason_code: string }[];
  expect(lines[0].reason_code).toBe("OTHER");
});

test("a changed draft drops the refusal it earned and the idempotency key it rode in on", async ({ page }) => {
  // The review round's P2-5: a kept key on a changed draft would replay the
  // old return's answer for a different return. The third cash sale carries
  // this test's own line, untouched by the tests before it.
  await signIn(page, "Sales");
  await findSale(page, "CT-1003");
  await page.getByLabel(WALLBLOCK_LINE).fill("6");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();
  await expect(page.getByTestId("refund-refusal")).toBeVisible();
  const before = await stubState(page);
  const keyBefore = before.keys[before.keys.length - 1];

  await page.getByLabel(WALLBLOCK_LINE).fill("5");
  // The refusal belongs to the draft that earned it: a changed field drops it
  // at once, and the next send carries a fresh key.
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await page.getByRole("button", { name: "Refund cash" }).click();
  await expect(page.getByTestId("refund-refusal")).toBeVisible();
  const after = await stubState(page);
  expect(after.keys.length).toBe(before.keys.length + 1);
  expect(after.keys[after.keys.length - 1]).not.toBe(keyBefore);
});

test("a plain refusal without the approval key shows its own words and opens no dialog", async ({ page }) => {
  // The review round's P2-6: the contract's 409 covers more than the approval
  // refusal, and a client discriminating on the key's presence instead of the
  // error string would show a manager dialog for a plain rail refusal.
  await signIn(page, "Sales");
  await findSale(page, "CT-1001");
  await page.getByLabel(WALLBLOCK_LINE).fill("99");
  await page.getByRole("button", { name: "Cash", exact: true }).click();
  await page.getByRole("button", { name: "Refund cash" }).click();

  await expect(page.getByTestId("returns-notice")).toContainText(
    "The refund was refused: the return asks for more than the line still has to return",
  );
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("an unknown sale number is refused in words and lays out nothing", async ({ page }) => {
  await signIn(page, "Sales");
  await openReturns(page);
  await page.getByLabel("Sale number").fill("CT-9999");
  await page.getByRole("button", { name: "Find sale" }).click();
  await expect(page.getByTestId("returns-notice")).toContainText(
    "No sale numbered CT-9999 answers at this counter: check the number and try again.",
  );
  await expect(page.getByTestId("returns-sale")).toHaveCount(0);
  await expect(page.getByTestId("refund-refusal")).toHaveCount(0);
});

// WCAG 1.4.11: a non-filled control's boundary clears 3:1 against what it sits on, in both
// themes. The hairline (--hh-line) does not; the design system's control line does.
for (const scheme of ["dark", "light"] as const) {
  test(`the desk's find input keeps the control boundary at 3:1 (${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await signIn(page, "Sales");
    await openReturns(page);
    const input = page.getByLabel("Sale number");
    await expect(input).toBeVisible();
    const read = await input.evaluate((el) => {
      const rgb = (css: string): number[] => (css.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
      const lum = (c: number[]): number => {
        const [r, g, b] = c.map((v) => {
          const s = v / 255;
          return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
        });
        return 0.2126 * r + 0.7152 * g + 0.0722 * b;
      };
      const ratio = (a: number[], b: number[]): number => {
        const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
        return (hi + 0.05) / (lo + 0.05);
      };
      const resolve = (token: string): number[] => {
        const probe = document.createElement("div");
        probe.style.color = `var(${token})`;
        document.body.appendChild(probe);
        const out = rgb(getComputedStyle(probe).color);
        probe.remove();
        return out;
      };
      const border = rgb(getComputedStyle(el).borderTopColor);
      return {
        isControlLine: border.join() === resolve("--hh-control-line").join(),
        onCard: ratio(border, resolve("--hh-card")),
      };
    });
    expect(read.isControlLine).toBe(true);
    expect(read.onCard).toBeGreaterThanOrEqual(3);
  });
}

// The one disabled rule in both themes: no fill, no fade (grey ink on a transparent button), on the
// returns desk's Find sale and the Items step's Add, the two controls that wore a fill or a fade.
for (const scheme of ["dark", "light"] as const) {
  test(`disabled Find sale and Add share the till's disabled rule (${scheme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: scheme });
    await signIn(page, "Sales");
    const head = page.getByTestId("step-head-items");
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    const add = page.getByRole("button", { name: "Add", exact: true });
    await expect(add).toBeDisabled();
    // Add lives on the sale screen and Find sale on the Returns surface (row 955): read each where it is.
    const disabledShape = (button: Locator) =>
      button.evaluate((node) => {
        const style = getComputedStyle(node);
        return { background: style.backgroundColor, opacity: style.opacity, cursor: style.cursor };
      });
    const rule = { background: "rgba(0, 0, 0, 0)", opacity: "1", cursor: "not-allowed" };
    expect(await disabledShape(add)).toEqual(rule);
    await openReturns(page);
    const find = page.getByRole("button", { name: "Find sale" });
    await expect(find).toBeDisabled();
    expect(await disabledShape(find)).toEqual(rule);
  });
}
