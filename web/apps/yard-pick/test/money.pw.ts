// Yard Pick, sub item 6: no cost anywhere. The stub's "poisoned" scenario
// lays money fields on every read the yard makes; the yard walks every
// screen and none of it may show, and nothing the app sends may carry any.
// The clean path is held by the fence and payload tests beside this one.
import { test, expect } from "@playwright/test";
import { resetStub, stubLog, useScenario, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

const POISON = "987654";
const FIGURES = [POISON, "9,876.54", "9876.54", "$"];

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

async function everyWordOnScreen(page: import("@playwright/test").Page): Promise<string> {
  return page.evaluate(() => {
    const parts: string[] = [];
    const walk = (node: Node) => {
      if (node.nodeType === Node.TEXT_NODE) parts.push(node.textContent ?? "");
      if (node instanceof Element) {
        // A stylesheet's own text is not a screen: CSS spacing and token
        // comments are not money the yard can read.
        if (node.tagName === "STYLE" || node.tagName === "SCRIPT") return;
        for (const attr of Array.from(node.attributes)) parts.push(`${attr.name}=${attr.value}`);
        if (node.shadowRoot) walk(node.shadowRoot);
      }
      for (const child of Array.from(node.childNodes)) walk(child);
    };
    walk(document);
    return parts.join(" ");
  });
}

test("with money laid on every read, no screen shows a figure or a money word", async ({ page, request }) => {
  await useScenario(request, "poisoned");
  await page.goto("/");
  await expect(page.locator("[data-pick-card]")).toHaveCount(5);
  const screens: string[] = [await everyWordOnScreen(page)];

  await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
  await page.getByRole("button", { name: "Start picking" }).click();
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
  screens.push(await everyWordOnScreen(page));

  await page.locator("[data-line]").first().locator(".line-row").click();
  await expect(page.locator("[data-qty-edit]")).toBeVisible();
  screens.push(await everyWordOnScreen(page));

  await page.getByRole("button", { name: /^All \d+/ }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("pulled");
  screens.push(await everyWordOnScreen(page));

  await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-error]")).toContainText("Give the reason for the skid count.");
  await page.getByRole("radio", { name: "Count corrected" }).click();
  await page.getByRole("button", { name: "One skid more" }).click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText("skids");
  screens.push(await everyWordOnScreen(page));

  await page.getByRole("button", { name: "Back to the queue" }).click();
  await page.locator("[data-pick-card]", { hasText: "SO-1041" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1041");
  await page.getByRole("button", { name: "Report a shortage" }).click();
  await expect(page.locator("[data-shortage-sheet]")).toBeVisible();
  screens.push(await everyWordOnScreen(page));
  await page.getByLabel("How many are short").fill("5");
  await page.getByRole("radio", { name: "Not enough on the yard" }).click();
  await page.getByRole("button", { name: "Report short pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage sent");
  screens.push(await everyWordOnScreen(page));
  await page.locator("[data-shortage]", { hasText: "22 SQFT" }).getByRole("button", { name: "Mark it settled" }).click();
  await page.getByLabel("How the shortage was settled").fill("Settled with the customer");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Shortage cleared");
  screens.push(await everyWordOnScreen(page));

  // The changes waiting on the yard: the list, the sheet, and an
  // acknowledgement, every one of them a read the poisoned stub laces with
  // the delta figures the projection must drop.
  await page.getByRole("button", { name: "Back to the queue" }).click();
  await page.locator("[data-changes-door]").click();
  await expect(page.locator("[data-change-row]")).toHaveCount(2);
  screens.push(await everyWordOnScreen(page));

  await page.locator("[data-change-row]", { hasText: "SO-1042" }).locator(".line-row").click();
  await expect(page.locator("[data-change-sheet]")).toBeVisible();
  screens.push(await everyWordOnScreen(page));

  await page.getByLabel("A note for the counter, optional").fill("Two more pallets fit the truck");
  await page.getByRole("button", { name: "Acknowledge" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Acknowledged");
  screens.push(await everyWordOnScreen(page));

  for (const [index, text] of screens.entries()) {
    for (const figure of FIGURES) expect(text, `screen ${index + 1}`).not.toContain(figure);
    // Field names, not words in a sentence: the held order's own words say
    // "Balance due", which is the server's sentence, not a money field.
    const hit = /(cents|unit_price|unit_cost|avg_cost|freight|gauge|margin|price|cost|value_cents|amount_due)/i.exec(text);
    expect(hit ? JSON.stringify(text.slice(Math.max(0, hit.index - 70), hit.index + 70)) : "", `screen ${index + 1}`).toBe("");
  }
});

test("nothing the app sends to the core carries a money field", async ({ page, request }) => {
  await useScenario(request, "poisoned");
  await page.goto("/");
  await page.locator("[data-pick-card]", { hasText: "SO-1040" }).getByRole("button", { name: "Open" }).click();
  await expect(page.locator("[data-pick-order]")).toHaveText("SO-1040");
  await page.getByRole("button", { name: "Start picking" }).click();
  await page.locator("[data-line]").first().locator(".line-row").click();
  await page.getByRole("button", { name: "Add one on" }).click();
  await page.getByRole("radio", { name: "Damaged stock" }).click();
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("backorder");
  const calls = await stubLog(request);
  expect(calls.length).toBeGreaterThan(3);
  for (const call of calls) {
    const sent = JSON.stringify({ query: call.query, body: call.body }).toLowerCase();
    expect(sent).not.toMatch(/cents|price|cost|margin|freight|amount|balance|total|value/);
    expect(sent).not.toContain(POISON);
  }
});
