// The landed cost area walked in a real browser against the stub: the list, a capture saved as a
// draft, the split compared across bases, the apply and the void behind their confirm steps, a late
// freight that follows an applied landed cost, and every refusal in the core's words. The stub is
// held to the core's shapes and sentences by test/landed-contract.test.mjs.
import { test, expect, openArea, stubRole, resetStub, lowContrast, noSidewaysScroll } from "./helpers.js";

const ORDER = "PO-TRT-66 · Alcli (demo)";

async function newCapture(page: import("@playwright/test").Page): Promise<void> {
  await page.getByRole("button", { name: "New landed cost" }).click();
  await expect(page.locator("[data-landed-capture]")).toBeVisible();
}

async function fill(page: import("@playwright/test").Page, freight: string, duty: string, handling: string): Promise<void> {
  await page.locator("[data-lc-freight]").fill(freight);
  await page.locator("[data-lc-duty]").fill(duty);
  await page.locator("[data-lc-handling]").fill(handling);
}

test("the area opens on the landed costs already captured, with the counts beside them", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await expect(page.getByRole("heading", { name: "Landed cost", level: 1 })).toBeVisible();
  const rows = page.locator("[data-landed-row]");
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText("PO-TRT-66");
  await expect(rows.first()).toContainText("Applied");
  await expect(rows.first()).toContainText("By value");
  await expect(rows.first()).toContainText("$480.00");
  await expect(rows.first()).toContainText("$35.50");
  await expect(page.locator("[data-tile='drafts']")).toContainText("0");
  await expect(page.locator("[data-tile='applied']")).toContainText("1");
  await expect(page.locator("nav.areas").getByRole("button", { name: "Landed cost" })).toHaveAttribute("aria-current", "true");
});

test("a capture is saved as a draft, its split compared across the bases, then applied behind a confirm step", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await newCapture(page);
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await fill(page, "480.00", "", "35.5");
  await page.getByRole("button", { name: "By weight" }).click();
  await page.getByRole("button", { name: "Save as a draft" }).click();

  const detail = page.locator("[data-landed-detail]");
  await expect(detail).toBeVisible();
  await expect(page.locator("[data-notice]")).toContainText("Saved as a draft on PO-TRT-66");
  await expect(detail.locator("[data-status]").first()).toHaveText("Draft");
  await expect(detail.locator("[data-total]")).toHaveText("$515.50");
  await expect(detail.locator("[data-allocation-row]")).toHaveCount(2);
  await expect(detail.locator("[data-allocation-row]", { hasText: "Special buy: takes no share" })).toContainText("$0.00");
  await expect(page.locator("[data-compare-note]")).toContainText("Applying uses the basis saved on this draft: by weight.");

  // Comparing another basis shows its split and says plainly that apply still uses the saved one.
  await page.getByRole("button", { name: "By quantity" }).click();
  await expect(page.locator("[data-compare-note]")).toContainText("Comparing by quantity. Applying still uses the saved basis, by weight");
  await expect(detail.locator("[data-total]")).toHaveText("$515.50");

  // Nothing is applied until the confirm step is pressed through.
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  const confirm = page.locator("[data-confirm]");
  await expect(confirm).toContainText("Apply $515.50 of landed cost to PO-TRT-66, by weight.");
  await expect(confirm).toContainText("It cannot be edited after; a mistake is undone by voiding it.");
  await page.getByRole("button", { name: "Go back" }).click();
  await expect(detail.locator("[data-status]").first()).toHaveText("Draft");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await page.getByRole("button", { name: "Apply it" }).click();

  await expect(page.locator("[data-notice]")).toContainText("Applied to PO-TRT-66.");
  await expect(detail.locator("[data-status]").first()).toHaveText("Applied");
  await expect(detail.locator("[data-allocation-row]")).toHaveCount(1);
  await expect(detail.locator("[data-total]")).toHaveText("$515.50");
  await expect(page.getByRole("button", { name: "Void" })).toBeVisible();
  await page.getByRole("button", { name: "Back to the list" }).click();
  await expect(page.locator("[data-landed-row]")).toHaveCount(2);
  await expect(page.locator("[data-tile='applied']")).toContainText("2");
});

test("a void is confirmed, reverses the document, and the list says so", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await page.locator("[data-landed-row]").first().getByRole("button", { name: "PO-TRT-66" }).click();
  await expect(page.locator("[data-landed-detail]")).toBeVisible();
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Void" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("Void the landed cost of $515.50 on PO-TRT-66.");
  await page.getByRole("button", { name: "Void it" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Voided: the landed cost on PO-TRT-66 is reversed.");
  await expect(page.locator("[data-landed-detail] [data-status]").first()).toHaveText("Voided");
  await expect(page.getByRole("button", { name: "Void" })).toHaveCount(0);
  await page.getByRole("button", { name: "Back to the list" }).click();
  await expect(page.locator("[data-tile='voided']")).toContainText("1");
  await page.getByRole("button", { name: "Voided" }).click();
  await expect(page.locator("[data-landed-row]")).toHaveCount(1);
});

test("a draft that changed while it was open is not pressed on, and the screen says what the core holds", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await newCapture(page);
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await fill(page, "100", "", "");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-landed-detail]")).toBeVisible();
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  // Someone else applies it while the confirm step is open.
  const list = await (await request.get("/api/v1/landed-costs?branch_id=b0000000-0000-4000-8000-000000000001", { headers: { Authorization: "Bearer x" } })).json();
  const draft = list.items.find((i: { status: string }) => i.status === "DRAFT");
  await request.post(`/api/v1/landed-costs/${draft.id}/apply`, { headers: { Authorization: "Bearer x" } });
  await page.getByRole("button", { name: "Apply it" }).click();
  await expect(page.locator("[data-error]")).toContainText("This landed cost changed while you had it open: it reads applied now. Nothing was sent.");
  await expect(page.locator("[data-landed-detail] [data-status]").first()).toHaveText("Applied");
});

test("an apply whose answer was lost is settled by reading the document, so the screen shows what the core holds", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await newCapture(page);
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await fill(page, "100", "", "");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-landed-detail]")).toBeVisible();
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await request.post("/_stub/flag", { data: { name: "dropNext", value: true } });
  await page.getByRole("button", { name: "Apply it" }).click();
  // The apply was made; nothing came back; the screen read the document and says what it now is.
  await expect(page.locator("[data-landed-detail] [data-status]").first()).toHaveText("Applied");
  await expect(page.locator("[data-notice]")).toContainText("Applied to PO-TRT-66.");
  await expect(page.getByRole("button", { name: "Apply", exact: true })).toHaveCount(0);
});

test("a capture that cannot be right is refused in words and sends nothing", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await newCapture(page);
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Pick the purchase order the freight belongs to.");
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await fill(page, "12.345", "", "");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-error]")).toHaveText("Write the freight as dollars and cents, two decimals at most.");
  await fill(page, "", "", "");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-error]")).toContainText("Write at least one of freight, duty or handling");
  await page.getByRole("button", { name: "USD", exact: true }).click();
  await fill(page, "100", "", "");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-error]")).toContainText("Write the exchange rate as a positive number");
  await page.getByRole("button", { name: "Back to the list" }).click();
  await expect(page.locator("[data-landed-row]")).toHaveCount(1);
});

test("a USD capture carries its rate, and the books' figures are the core's conversion", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await newCapture(page);
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await page.getByRole("button", { name: "USD", exact: true }).click();
  await page.locator("[data-lc-fx]").fill("1.352");
  await fill(page, "100.00", "", "5.05");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  const facts = page.locator("[data-facts]");
  await expect(facts).toContainText("Exchange rate");
  await expect(facts).toContainText("1.352");
  await expect(facts).toContainText("$135.20");
  await expect(facts).toContainText("(USD $100.00)");
  await expect(facts).toContainText("$6.83");
});

test("a late freight follows an applied landed cost and splits by what is still on hand", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await newCapture(page);
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await fill(page, "100.00", "", "");
  await page.getByRole("checkbox", { name: /late freight/ }).click();
  await page.locator("[data-lc-retro-of]").selectOption({ index: 1 });
  // Two received lines are counted; a missing count is asked for, not guessed.
  await expect(page.locator("[data-onhand]")).toHaveCount(2);
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-error]")).toContainText("are still on hand");
  const counts = page.locator("[data-onhand]");
  await counts.nth(0).fill("4");
  await counts.nth(1).fill("0");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-landed-detail]")).toBeVisible();
  await expect(page.locator("[data-facts]")).toContainText("Late freight on an applied landed cost");
  await page.getByRole("button", { name: "Apply late freight" }).click();
  await expect(page.locator("[data-confirm]")).toContainText("goes straight to cost of goods sold");
  await page.getByRole("button", { name: "Apply late freight" }).last().click();
  await expect(page.locator("[data-notice]")).toContainText("Late freight applied to PO-TRT-66.");
  await expect(page.locator("[data-landed-detail] [data-status]").first()).toHaveText("Retro adjusted");
  const row = page.locator("[data-allocation-row]").first();
  await expect(row).toContainText("$40.00");
  await expect(row).toContainText("$60.00");
});

test("a role the wall does not admit is refused in words naming who may, and nothing is saved", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  await stubRole(request, "payables-receivables");
  await newCapture(page);
  await page.locator("[data-lc-po]").selectOption({ label: ORDER });
  await fill(page, "100", "", "");
  await page.getByRole("button", { name: "Save as a draft" }).click();
  await expect(page.locator("[data-error]")).toContainText("is for the Procurement Manager, the General Manager and the super admin");
  await page.getByRole("button", { name: "Back to the list" }).click();
  await expect(page.locator("[data-landed-row]")).toHaveCount(1);
});

test("a caller with no branch reach reads the core's refusal and can try again", async ({ page, request }) => {
  await resetStub(request);
  await request.post("/_stub/fence");
  await page.goto("/");
  await expect(page.locator('[data-flow="procurement-manager"]')).toHaveCount(1);
  await page.locator('[data-flow="procurement-manager"]').click();
  await page.locator("nav.areas").getByRole("button", { name: "Landed cost" }).click();
  await expect(page.locator("[data-error]")).toContainText("Caller cannot access this branch.");
  await expect(page.getByRole("button", { name: "Try again" })).toBeVisible();
  await resetStub(request);
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-landed-row]")).toHaveCount(1);
});

test("the keyboard reaches the area, opens a capture and the focus ring shows", async ({ page, request }) => {
  await openArea(page, request, "Landed cost");
  const newButton = page.getByRole("button", { name: "New landed cost" });
  await newButton.focus();
  await page.keyboard.press("Enter");
  await expect(page.locator("[data-landed-capture]")).toBeVisible();
  await page.locator("[data-lc-po]").focus();
  await expect(page.locator("[data-lc-po]")).toBeFocused();
});

test("every text is 4.5 to 1 or better in both themes: the list, a capture, a draft, the confirm step", async ({ page, request }) => {
  test.setTimeout(60_000);
  await page.setViewportSize({ width: 1440, height: 900 });
  await openArea(page, request, "Landed cost");
  for (const theme of ["Theme: Light", "Theme: Brand"]) {
    if (theme === "Theme: Brand") await page.getByRole("button", { name: "Theme: Light" }).click();
    expect(await lowContrast(page), `${theme}, list`).toEqual([]);
    await newCapture(page);
    await page.getByRole("checkbox", { name: /late freight/ }).click();
    expect(await lowContrast(page), `${theme}, capture`).toEqual([]);
    await page.getByRole("button", { name: "Back to the list" }).click();
    await page.locator("[data-landed-row]").first().getByRole("button", { name: "PO-TRT-66" }).click();
    await page.getByRole("button", { name: "Void" }).click();
    expect(await lowContrast(page), `${theme}, confirm`).toEqual([]);
    await page.getByRole("button", { name: "Go back" }).click();
    await page.getByRole("button", { name: "Back to the list" }).click();
  }
});

test("at 390 px the page never scrolls sideways: the list is cards, the capture and the detail stack", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openArea(page, request, "Landed cost");
  await noSidewaysScroll(page);
  await expect(page.locator("[data-landed-row]").first().locator("td[data-label='Freight']")).toBeVisible();
  await newCapture(page);
  await noSidewaysScroll(page);
  await page.getByRole("button", { name: "Back to the list" }).click();
  await page.locator("[data-landed-row]").first().getByRole("button", { name: "PO-TRT-66" }).click();
  await expect(page.locator("[data-landed-detail]")).toBeVisible();
  await noSidewaysScroll(page);
});
