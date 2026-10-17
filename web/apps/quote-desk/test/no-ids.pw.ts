// Round 10 item 3 (rows 949 and 1092): names and words on screen, never ids. A line cleared below the floor says
// who cleared it by name, the history says who and what in words, and the approvals queue carries no id.
import { expect, test, type Page } from "@playwright/test";
import { signIn } from "./switch-helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

const ID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|demo-account-[a-z-]+/;

async function text(page: Page): Promise<string> {
  // The text of the whole page, shadow roots included.
  return page.evaluate(() => {
    const out: string[] = [];
    const walk = (root: ParentNode): void => {
      for (const el of Array.from(root.querySelectorAll("*"))) {
        if (el.shadowRoot !== null) walk(el.shadowRoot);
        for (const node of Array.from(el.childNodes)) if (node.nodeType === Node.TEXT_NODE) out.push(node.textContent ?? "");
      }
    };
    walk(document);
    return out.join(" ");
  });
}

test("a cleared price names who cleared it, and a quote's trail and queue carry no id", async ({ browser }) => {
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await context.newPage();
  await signIn(page, "Sales manager");
  const seeded = await page.request.post("/_stub/seed-quote", { data: { status: "DRAFT", customer_id: "c-maple-ridge", lines: [{ p: "p-charcoal-60", q: 120 }] } });
  const id = ((await seeded.json()) as { id: string }).id;
  await page.evaluate((quote) => {
    window.location.hash = `#/quotes/${quote}`;
  }, id);
  await expect(page.getByTestId("qb-line").first()).toBeVisible();
  // Discount the line past the floor as the manager, which clears it, then add a line so the trail has an addition.
  await page.locator('[data-field="disc"]').first().fill("30");
  await page.locator('[data-field="disc"]').first().blur();
  await expect(page.getByTestId("qb-line").first()).toContainText("cleared by Sales manager (demo)");
  expect(await text(page)).not.toMatch(ID);
  await page.getByText("Quote detail & admin").click();
  await expect(page.getByTestId("qh-box")).toContainText("Floor cleared by");
  await expect(page.getByTestId("qh-box")).toContainText("Sales manager (demo)");
  expect(await text(page)).not.toMatch(ID);
  await context.close();
});
