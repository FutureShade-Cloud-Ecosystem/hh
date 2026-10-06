// The Quote Desk's search and add box (decision log row 991): one box, results as you type, the line goes in by a
// click or by arrows and Enter, and the box stays open and focused for the next item. The component is mounted here
// by the test (the workarea's swap is the split list coder's one line), the way the workarea will hand it its
// branch and customer, and the events it fires are collected on the page.
import { expect, test, type Locator, type Page } from "@playwright/test";
import { shot } from "./shot";

const TRENTON_ID = "6f1c2b3a-0000-4000-8000-000000000001";
const MAPLE_RIDGE = "c-maple-ridge";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

type Added = { productId: string; code: string; unit: string; quantity: number };

async function mountBox(page: Page): Promise<{ box: Locator; input: Locator }> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.evaluate(
    ({ branch, customer }) => {
      const host = document.createElement("quote-search-add");
      host.setAttribute("branch-id", branch);
      host.setAttribute("customer-id", customer);
      host.setAttribute("data-testid", "search-add-host");
      host.style.cssText = "position:fixed;top:96px;left:48px;width:560px;z-index:50";
      const w = window as unknown as { __adds: unknown[] };
      w.__adds = [];
      host.addEventListener("quote-line-add", (event) => w.__adds.push((event as CustomEvent).detail));
      document.body.append(host);
    },
    { branch: TRENTON_ID, customer: MAPLE_RIDGE },
  );
  const box = page.getByTestId("search-add-host");
  return { box, input: box.getByRole("combobox", { name: "Search products by name, code or category" }) };
}

async function adds(page: Page): Promise<Added[]> {
  return page.evaluate(() => (window as unknown as { __adds: Added[] }).__adds);
}

test("search by name shows products and adds the row on click", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await expect(input).toBeVisible();
  await input.fill("polymeric");
  const rows = box.getByTestId("search-add-row");
  await expect(rows).toHaveCount(1);
  // Name, unit, the price this customer pays (the server's, 18 percent under retail), stock at the branch.
  await expect(rows.first()).toContainText("Polymeric Locking Sand");
  await expect(rows.first()).toContainText("PSN-100");
  await expect(rows.first()).toContainText("bag");
  await expect(box.getByTestId("search-add-price").first()).toContainText("11.48");
  await expect(rows.first()).toContainText("400 on hand");
  await shot(page, "search-add-name");
  await rows.first().click();
  expect(await adds(page)).toEqual([{ productId: "p-polymer-sand", code: "PSN-100", unit: "bag", quantity: 1 }]);
});

test("search by code adds at once", async ({ page }) => {
  const { input } = await mountBox(page);
  await input.fill("PSN-100");
  await expect.poll(async () => (await adds(page)).length).toBe(1);
  expect(await adds(page)).toEqual([{ productId: "p-polymer-sand", code: "PSN-100", unit: "bag", quantity: 1 }]);
  // The box is ready for the next item.
  await expect(input).toBeFocused();
  await expect(input).toHaveValue("");
});

test("keyboard arrow + Enter adds the highlighted row", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.fill("stone");
  await expect(box.getByTestId("search-add-row")).toHaveCount(3);
  // The first row is highlighted as the results arrive; two arrows walk to the third.
  await expect(box.getByTestId("search-add-row").first()).toHaveAttribute("aria-selected", "true");
  await input.press("ArrowDown");
  await input.press("ArrowDown");
  await expect(box.getByTestId("search-add-row").nth(2)).toHaveAttribute("aria-selected", "true");
  await input.press("ArrowUp");
  await expect(box.getByTestId("search-add-row").nth(1)).toHaveAttribute("aria-selected", "true");
  const name = (await box.getByTestId("search-add-row").nth(1).getAttribute("data-code")) ?? "";
  await input.press("Enter");
  const got = await adds(page);
  expect(got).toHaveLength(1);
  expect(got[0].code).toBe(name);
  expect(got[0].quantity).toBe(1);
  await expect(input).toBeFocused();
});

test("three lines in a row without leaving the box", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.click();
  await input.fill("stone");
  await expect(box.getByTestId("search-add-row")).toHaveCount(3);
  await input.press("Enter");
  await expect(input).toBeFocused();
  await input.fill("edge");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await box.getByTestId("search-add-row").first().click();
  await expect(input).toBeFocused();
  await input.fill("polymeric");
  await expect(box.getByTestId("search-add-row")).toHaveCount(1);
  await input.press("Enter");
  await expect(input).toBeFocused();
  const got = await adds(page);
  expect(got.map((line) => line.code)).toEqual(["HPV-60", "EDG-08", "PSN-100"]);
  expect(got.every((line) => line.quantity === 1)).toBe(true);
  // Typing continues in the same box with no click between lines.
  await page.keyboard.type("clear");
  await expect(input).toHaveValue("clear");
  await shot(page, "search-add-three");
});

test("empty results says so in words", async ({ page }) => {
  const { box, input } = await mountBox(page);
  await input.fill("zzzzqq");
  const empty = box.getByTestId("search-add-empty");
  await expect(empty).toBeVisible();
  await expect(empty).toContainText("No products match");
  await expect(empty).toContainText("zzzzqq");
  await expect(box.getByTestId("search-add-row")).toHaveCount(0);
  expect(await adds(page)).toEqual([]);
  // Enter on an empty answer adds nothing and leaves the box where it is.
  await input.press("Enter");
  expect(await adds(page)).toEqual([]);
  await expect(input).toBeFocused();
});
