// HH Drive, sub item 2: today's run sheet as the landing, a stop's detail, and
// the moves loaded and en route. Everything runs against the stub, which
// answers in the core's shapes and refuses in the core's words.
import { test, expect } from "@playwright/test";
import { driverHandoff, houseDay, resetStub, stubLog, useScenario } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

test("the landing is today's run: the run, its crew line and its stops in stop order", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
  const run = page.locator("[data-run]");
  await expect(run).toHaveCount(1);
  await expect(run).toContainText("RUN-R5");
  await expect(run).toContainText("FLAT-1");
  await expect(run.locator("[data-progress]")).toHaveText("3 stops left · 0 delivered");
  const stops = page.locator("[data-stop]");
  await expect(stops).toHaveCount(3);
  await expect(stops.nth(0)).toContainText("Northline Hardscapes Ltd (demo)");
  await expect(stops.nth(0)).toContainText("95 Bagot St, Kingston, K7L 3E7");
  await expect(stops.nth(0)).toContainText("AM");
  await expect(stops.nth(0).locator("[data-stop-no]")).toHaveText("1");
  await expect(stops.nth(2)).toContainText("County of Hastings (demo)");
  await expect(stops.nth(0).locator("[data-stop-status]")).toHaveText("Ready");
});

test("the run is asked for on the dealer's local day, not the browser's UTC day", async ({ page, request }) => {
  // Late in the evening in the dealer's zone the UTC clock is already on the next day.
  const lateEvening = new Date();
  lateEvening.setUTCHours(2, 30, 0, 0);
  await page.clock.install({ time: lateEvening });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
  await expect(page.locator("[data-run]")).toHaveCount(0);
  const calls = (await stubLog(request)).filter((c) => c.path === "/api/v1/dispatch/my-runs");
  expect(calls.length).toBeGreaterThan(0);
  expect(calls[0].query.date).toBe(houseDay(lateEvening));
  expect(calls[0].bearer).toBe(true);
  expect(calls[0].org).toBe("hh-demo");
});

test("no run assigned says so in the driver's words", async ({ page, request }) => {
  await useScenario(request, "empty");
  await page.goto("/");
  await expect(page.locator("[data-empty]")).toContainText("No run assigned to you today.");
  await expect(page.locator("[data-stop]")).toHaveCount(0);
});

test("a login with no driver record is told so, never shown a day off", async ({ page, request }) => {
  await useScenario(request, "not-driver");
  await page.goto("/");
  await expect(page.getByRole("alert")).toContainText("You are not set up as a driver");
  await expect(page.locator("[data-empty]")).toHaveCount(0);
});

test("a stop opens to its detail: where, who to call, how to get in, what is on the truck", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await expect(page.getByRole("heading", { name: "Stop 1" })).toBeVisible();
  const detail = page.locator("[data-stop-detail]");
  await expect(detail).toContainText("Northline Hardscapes Ltd (demo)");
  await expect(detail).toContainText("SO-1001");
  await expect(detail.getByRole("link", { name: "Open in maps" })).toHaveAttribute("href", /openstreetmap\.org\/search\?query=95%20Bagot/);
  await expect(detail.getByRole("link", { name: "613-555-0101" })).toHaveAttribute("href", "tel:613-555-0101");
  await expect(detail).toContainText("Gate code on the paperwork.");
  const lines = page.locator("[data-cargo] [data-line]");
  await expect(lines).toHaveCount(2);
  await expect(lines.first()).toContainText("Natural stone pavers, grey");
  await expect(lines.first()).toContainText("120 sqft");
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.locator("[data-stop]")).toHaveCount(3);
});

test("a stop moves loaded then en route, and the run sheet follows", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await expect(page.getByRole("button", { name: "Mark en route" })).toHaveCount(0);
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Loaded");
  await page.getByRole("button", { name: "Mark en route" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("En route");
  await expect(page.getByRole("button", { name: "Mark loaded" })).toHaveCount(0);
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.locator("[data-stop]").first().locator("[data-stop-status]")).toHaveText("En route");
  await expect(page.locator("[data-progress]")).toHaveText("3 stops left · 0 delivered");
});

test("a refused move shows the core's words and leaves the stop where it was", async ({ page, request }) => {
  await useScenario(request, "run-planned");
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Loaded");
  await page.getByRole("button", { name: "Mark en route" }).click();
  await expect(page.getByRole("alert")).toContainText("The run has not been dispatched.");
  await expect(page.locator("[data-stop-status]")).toHaveText("Loaded");
});

test("every control a driver taps is at least 56 px tall at 390 px", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  const heights = await page.evaluate(() => {
    const out: number[] = [];
    const walk = (root: ParentNode) => {
      for (const el of Array.from(root.querySelectorAll("*"))) {
        if ((el as HTMLElement).shadowRoot) walk((el as HTMLElement).shadowRoot as ShadowRoot);
      }
      for (const el of Array.from(root.querySelectorAll("[data-tap]"))) out.push(el.getBoundingClientRect().height);
    };
    walk(document);
    return out;
  });
  expect(heights.length).toBeGreaterThan(0);
  for (const h of heights) expect(h).toBeGreaterThanOrEqual(56);
});
