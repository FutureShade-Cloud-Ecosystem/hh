// HH Drive, sub item 5: no money anywhere. The stub's "poisoned" scenario lays
// money fields on every read; the driver walks every screen and none of it may
// show, and nothing the app sends may carry any. The clean path is held too:
// the stub's own payloads (the core's shapes) are scanned for money words.
import { test, expect, deliverStop, driverHandoff, resetStub, stubLog, takeFirstStopEnRoute, useScenario } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

const POISON = "987654";
const FIGURES = [POISON, "9,876.54", "9876.54", "$"];

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

async function everyWordOnScreen(page: import("@playwright/test").Page): Promise<string> {
  return page.evaluate(() => {
    const parts: string[] = [];
    const walk = (node: Node) => {
      if (node.nodeType === Node.TEXT_NODE) parts.push(node.textContent ?? "");
      if (node instanceof Element) {
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
  await expect(page.locator("[data-stop]")).toHaveCount(3);
  const screens: string[] = [await everyWordOnScreen(page)];

  await page.locator("[data-stop]").first().click();
  await expect(page.getByRole("heading", { name: "Stop 1" })).toBeVisible();
  screens.push(await everyWordOnScreen(page));
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();

  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Proof of delivery" })).toBeVisible();
  screens.push(await everyWordOnScreen(page));
  await page.getByRole("button", { name: "Not yet" }).click();

  await page.getByRole("button", { name: "Could not deliver" }).click();
  await expect(page.getByRole("heading", { name: "Could not deliver" })).toBeVisible();
  screens.push(await everyWordOnScreen(page));
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: "Back to my run" }).click();

  await page.getByRole("button", { name: "End of run" }).click();
  await expect(page.getByRole("heading", { name: "End of run" })).toBeVisible();
  screens.push(await everyWordOnScreen(page));

  expect(screens.every((text) => text.includes("Bluffside") || text.includes("End of run") || text.includes("Proof") || text.includes("Could not"))).toBe(true);
  for (const text of screens) {
    for (const figure of FIGURES) expect(text).not.toContain(figure);
    expect(text.toLowerCase()).not.toMatch(/cents|unit_price|freight|gauge|amount_due|payment_hold/);
  }
});

test("nothing the app sends to the core carries a money field", async ({ page, request }) => {
  await useScenario(request, "poisoned");
  await page.goto("/");
  await deliverStop(page, 1);
  await takeFirstStopEnRoute2(page);
  const calls = await stubLog(request);
  expect(calls.length).toBeGreaterThan(3);
  for (const call of calls) {
    const sent = JSON.stringify({ query: call.query, body: call.body }).toLowerCase();
    expect(sent).not.toMatch(/cents|price|cost|margin|freight|amount|balance|total|value/);
    expect(sent).not.toContain(POISON);
  }
});

// Stop 2 to en route, so the calls include a failure with a reason as well as a signature.
async function takeFirstStopEnRoute2(page: import("@playwright/test").Page): Promise<void> {
  await page.locator("[data-stop]").nth(1).click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.getByRole("radio", { name: "Nobody on site" }).check();
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.locator("[data-stop-status]").filter({ hasText: "Not delivered" }).waitFor();
}
