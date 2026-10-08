// Round 15's live check of the till's payment step, brought to boards 00 and 01 (FROM-PRINCIPAL 34):
// the sentence under the lines is the server's and follows the reason; the step stands in the boards'
// two columns (the sale and its fulfilment tiles beside the customer, totals, tender and deposit
// lines) and stacks at phone width; one way onto the account; the supplier's deposit behind a link;
// the Pay balance result in board 02's green; the disabled key hints in the disabled pair; and a
// closed More holds no boxes off the phone's canvas.
import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
}

async function openStep(page: Page, step: "customer" | "items" | "payment" | "summary"): Promise<void> {
  const head = page.getByTestId(`step-head-${step}`);
  for (let attempt = 0; attempt < 5; attempt += 1) {
    if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
    try {
      await expect(head).toHaveAttribute("aria-expanded", "true", { timeout: 1_000 });
      await page.waitForTimeout(250);
      if ((await head.getAttribute("aria-expanded")) === "true") return;
    } catch {
      // closed again by an advance: press once more
    }
  }
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

async function toPayment(page: Page, search: string, name: string, willCall = true): Promise<void> {
  await signIn(page);
  await page.getByRole("button", { name: /Select customer/ }).click();
  await page.getByPlaceholder("Search name or account #…").fill(search);
  await page.locator("button.row").filter({ hasText: name }).first().click();
  await openStep(page, "items");
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  await expect(page.getByTestId("pos-line-row")).toHaveCount(1);
  if (willCall) await page.getByRole("button", { name: "Will-call" }).click();
  await openStep(page, "payment");
}

type Box = { x: number; y: number; width: number; height: number };
async function box(page: Page, testId: string): Promise<Box> {
  const b = await page.getByTestId(testId).boundingBox();
  expect(b, `${testId} has a box`).not.toBeNull();
  return b as Box;
}

const CREDIT_SENTENCE = "A credit customer: the rest rides their account and is paid from the statement.";
const COD_SENTENCE =
  "Cash On Demand means pay before release, never cash on delivery: the deposit puts the order in process, and picking waits for paid in full.";

test("the sentence under the lines follows the account: credit, Cash On Demand, and a gate refused retail account", async ({ page }) => {
  await toPayment(page, "C02", "Fenwick Builders Ltd");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT").click();
  await expect(page.getByTestId("deposit-sentence")).toHaveText(CREDIT_SENTENCE);

  await page.goto("/");
  await toPayment(page, "C10", "Cash On Demand Landscaping");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  await expect(page.getByTestId("deposit-sentence")).toHaveText(COD_SENTENCE);

  await page.goto("/");
  await toPayment(page, "C07", "Lachance Retail");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_DUE_LATER").click();
  const sentence = page.getByTestId("deposit-sentence");
  await expect(sentence).toContainText("This account cannot carry this sale on account: pay in full now, or take a deposit with the balance due later.");
  await expect(sentence).not.toContainText("Cash On Demand");
});

test("at desktop the payment step stands in two columns, at 390 it stacks, the sale first", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await toPayment(page, "C02", "Fenwick Builders Ltd");
  await page.getByTestId("pay-shape-DEPOSIT_NOW_REST_ON_ACCOUNT").click();

  const sale = await box(page, "pay-sale");
  const right = await box(page, "pay-right");
  expect(right.x, "the right column stands beside the sale").toBeGreaterThan(sale.x + sale.width - 1);
  expect(Math.abs(right.y - sale.y), "the columns start together").toBeLessThan(4);

  const card = page.getByTestId("pay-customer-card");
  await expect(card).toContainText("Fenwick Builders Ltd");
  await expect(card).toContainText(/Outstanding\s*\$[\d,]+\.\d\d/i);
  await expect(card).toContainText(/Credit available\s*\$[\d,]+\.\d\d/i);
  await expect(page.getByTestId("pay-lines")).toContainText("Holland Stone 60 mm");
  await expect(page.getByTestId("pay-fulfil")).toContainText("Will-call");
  await expect(page.getByTestId("pay-fulfil").getByRole("button", { name: "Delivery" })).toBeVisible();

  // The card or cash switch stands above the deposit lines.
  const cardBtn = await page.getByRole("button", { name: "Card", exact: true }).boundingBox();
  const lineOne = await box(page, "deposit-line-one");
  expect((cardBtn as Box).y + (cardBtn as Box).height).toBeLessThanOrEqual(lineOne.y);
  // Split tender stays a quiet link.
  await expect(page.getByTestId("tender-split")).toHaveClass(/splitquiet/);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(250);
  const sale390 = await box(page, "pay-sale");
  const right390 = await box(page, "pay-right");
  expect(right390.y, "stacked: the right column starts below the sale").toBeGreaterThanOrEqual(sale390.y + sale390.height - 1);
  for (const b of [sale390, right390]) expect(b.x + b.width, "nothing runs past the phone's canvas").toBeLessThanOrEqual(390.5);
});

test("there is one way onto the account: the Charge Account tile steps aside for All on account, and stays on a collect now sale", async ({ page }) => {
  await toPayment(page, "C02", "Fenwick Builders Ltd");
  await expect(page.getByTestId("pay-shape-ALL_ON_ACCOUNT")).toBeVisible();
  await expect(page.getByRole("button", { name: "Charge Account" })).toHaveCount(0);
  await page.getByTestId("pos-paytiles").getByRole("button", { name: "Cash" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByTestId("pos-paytiles").getByRole("button", { name: "Card" })).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await page.keyboard.press("ArrowLeft");
  await expect(page.getByTestId("pos-paytiles").getByRole("button", { name: "Cash" })).toBeFocused();

  await page.goto("/");
  await toPayment(page, "C02", "Fenwick Builders Ltd", false);
  await expect(page.getByRole("button", { name: "Charge Account" })).toBeVisible();
});

test("the supplier's deposit sits behind a small link on All on account, and stays open once an amount is typed", async ({ page }) => {
  await toPayment(page, "C02", "Fenwick Builders Ltd");
  await page.getByTestId("pay-shape-ALL_ON_ACCOUNT").click();
  await expect(page.getByTestId("supplier-deposit")).toHaveCount(0);
  const link = page.getByTestId("supplier-deposit-link");
  await expect(link).toHaveText("Supplier asked for a deposit");
  await link.click();
  await expect(page.getByTestId("supplier-deposit-link")).toHaveCount(0);
  await page.getByTestId("supplier-deposit-amount").fill("1.00");
  await expect(page.getByTestId("supplier-deposit")).toContainText("billed to the account");
});

test("the Pay balance result card wears board 02's green success border", async ({ page }) => {
  expect((await page.request.post("/_stub/reseed-owing")).ok()).toBe(true);
  await signIn(page);
  const dot = page.getByTestId("session-dot");
  const openButton = page.getByRole("button", { name: "Open Session", exact: true });
  await expect(dot.or(openButton).first()).toBeVisible({ timeout: 10_000 });
  if (!(await dot.isVisible())) await openButton.click({ timeout: 3_000 }).catch(() => undefined);
  await expect(dot).toContainText("Open");
  await page.getByTestId("till-pay-balance-entry").click();
  await page.getByTestId("pay-balance-term").fill("DEMO-TRT-D02");
  await page.getByTestId("pay-balance-find").click();
  await page.getByTestId("pay-balance-method-CASH").click();
  await page.getByTestId("pay-balance-take").click();
  const done = page.getByTestId("pay-balance-done");
  await expect(done).toContainText("Paid in full");
  const green = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--hh-success").trim());
  expect(green).not.toBe("");
  const style = await done.evaluate((el) => {
    const s = getComputedStyle(el);
    return { width: s.borderTopWidth, color: s.borderTopColor };
  });
  expect(style.width).toBe("2px");
  const probe = await page.evaluate((c) => {
    const el = document.createElement("span");
    el.style.color = c;
    document.body.appendChild(el);
    const resolved = getComputedStyle(el).color;
    el.remove();
    return resolved;
  }, green);
  expect(style.color).toBe(probe);
});

function luminance(rgb: string): number {
  const m = rgb.match(/[\d.]+/g)?.map(Number) ?? [0, 0, 0];
  const lin = (v: number): number => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(m[0]) + 0.7152 * lin(m[1]) + 0.0722 * lin(m[2]);
}
function ratio(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

for (const theme of ["light", "brand"] as const) {
  test(`the disabled key hint chips read in the disabled pair (${theme})`, async ({ page }) => {
    await signIn(page);
    if (theme === "brand") await page.getByTestId("theme-control").click();
    const chip = page.locator("hos-pos-hotkey-legend .k.off").first();
    await expect(chip).toBeVisible();
    const got = await chip.evaluate((el) => {
      const s = getComputedStyle(el);
      const cap = getComputedStyle(el.querySelector(".cap") as Element);
      return { ink: s.color, fill: s.backgroundColor, capInk: cap.color, opacity: s.opacity };
    });
    expect(got.opacity, "dimmed by colour, never by fade").toBe("1");
    expect(ratio(got.ink, got.fill), `label ink against the disabled fill, ${theme}`).toBeGreaterThanOrEqual(4.5);
    expect(ratio(got.capInk, got.fill), `key ink against the disabled fill, ${theme}`).toBeGreaterThanOrEqual(4.5);
  });
}

test("at 390 a closed More leaves no input or button off the canvas, and opened it fits", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  const offCanvas = (): Promise<string[]> =>
    page.evaluate(() => {
      const found: string[] = [];
      const walk = (root: Document | ShadowRoot): void => {
        root.querySelectorAll("input,button").forEach((el) => {
          const r = el.getBoundingClientRect();
          if (r.width > 0 && r.height > 0 && r.right > 390.5) found.push(`${el.tagName} ${(el.textContent ?? "").trim()}`);
        });
        root.querySelectorAll("*").forEach((el) => {
          if ((el as HTMLElement).shadowRoot) walk((el as HTMLElement).shadowRoot as ShadowRoot);
        });
      };
      walk(document);
      return found;
    });
  expect(await offCanvas()).toEqual([]);
  await page.locator("details.more > summary").filter({ hasText: /^\s*More\s*$/ }).first().click();
  await expect(page.getByPlaceholder("#SO or order id")).toBeVisible();
  expect(await offCanvas()).toEqual([]);
});
