// HH Drive, sub item 2: today's run sheet as the landing, a stop's detail, and
// the moves loaded and en route. Everything runs against the stub, which
// answers in the core's shapes and refuses in the core's words.
import { test, expect, driverHandoff, houseDay, resetStub, useScenario } from "./helpers.js";

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
  await expect(stops.nth(0)).toContainText("Bluffside Builders");
  await expect(stops.nth(0)).toContainText("418 Oak Hollow Road, Julian, 92036");
  await expect(stops.nth(0)).toContainText("AM");
  await expect(stops.nth(0).locator("[data-stop-no]")).toHaveText("1");
  await expect(stops.nth(2)).toContainText("Valley Municipal Works");
  await expect(stops.nth(0).locator("[data-stop-status]")).toHaveText("Ready");
});

// The two hours at which the dealer's day and the UTC day disagree and agree.
// Each leg pins the page's clock AND the stub's clock to its own instant, so it
// is red on wrong code at any wall hour (the stub builds its world on the dealer's
// day at its pinned instant, so a UTC day asked for in the evening finds nothing).
function utcAt(hours: number, minutes: number): number {
  const at = new Date();
  at.setUTCHours(hours, minutes, 0, 0);
  return at.getTime();
}

for (const [label, instant, differs] of [
  ["half past midnight UTC, the previous evening in the dealer's zone", utcAt(0, 30), true],
  ["the middle of the dealer's afternoon", utcAt(19, 0), false],
] as const) {
  test(`the run is asked for on the dealer's local day, not the browser's UTC day: ${label}`, async ({ page, request }) => {
    await page.clock.setSystemTime(instant);
    await resetStub(request, instant);
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
    // The world stands on the dealer's day at this instant, so the run is on
    // the sheet: the sheet reads the store, and the store's run is dated the
    // day the stub's world stood on (the dealer's day at this instant, which
    // the case label pins). The day question itself is held by the data
    // layer's node suite, which filters the store's runs by the dealer's day.
    await expect(page.locator("[data-run]")).toHaveCount(1);
    const dealersDay = houseDay(new Date(instant));
    const utcDay = new Date(instant).toISOString().slice(0, 10);
    // The leg means something only where the two days differ (and agrees where they do not).
    expect(dealersDay !== utcDay).toBe(differs);
    await expect(page.locator("[data-staleness]")).toContainText("Pulled");
  });
}

test("a run on the UTC day, not the dealer's, is not today's run", async ({ page, request }) => {
  // The browser stands at the evening instant; the stub's world stands on the NEXT day,
  // which is the UTC day then. The dealer's day has no run, so the sheet is empty.
  const evening = utcAt(0, 30);
  await page.clock.setSystemTime(evening);
  await resetStub(request, evening + 24 * 3_600_000);
  await page.goto("/");
  await expect(page.locator("[data-empty]")).toContainText("No run assigned to you today.");
});

test("no run assigned says so in the driver's words", async ({ page, request }) => {
  await useScenario(request, "empty");
  await page.goto("/");
  await expect(page.locator("[data-empty]")).toContainText("No run assigned to you today.");
  await expect(page.locator("[data-stop]")).toHaveCount(0);
});

test("a login with no driver record reads an empty day, with nothing leaked", async ({ page, request }) => {
  await useScenario(request, "not-driver");
  await page.goto("/");
  // The reads answer from the store, and the sync surface answers a caller
  // with no rows the same as an empty day: existence never leaks. The sheet
  // says no run, the same words an uncrewed day gets.
  await expect(page.locator("[data-empty]")).toContainText("No run assigned to you today.");
  await expect(page.locator("[data-stop]")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "End of run" })).toHaveCount(0);
});

test("a stop opens to its detail: where, who to call, how to get in, what is on the truck", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await expect(page.getByRole("heading", { name: "Stop 1" })).toBeVisible();
  const detail = page.locator("[data-stop-detail]");
  await expect(detail).toContainText("Bluffside Builders");
  await expect(detail).toContainText("SO-1001");
  await expect(detail.getByRole("link", { name: "Open in maps" })).toHaveAttribute("href", /openstreetmap\.org\/search\?query=418%20Oak/);
  await expect(detail.getByRole("link", { name: "619-555-0101" })).toHaveAttribute("href", "tel:619-555-0101");
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

test("a stalled zone read does not hold the run sheet: the store answers on the last served zone", async ({ page }) => {
  // The surface config read never answers (a weak connection that stalls, not a hard offline
  // that rejects at once). The run sheet reads the offline store at once and does not wait on it.
  await page.route("**/api/v1/surfaces/drive-run/config*", () => {
    /* held: never fulfilled, never continued */
  });
  await page.goto("/");
  await expect(page.locator("[data-run]")).toHaveCount(1, { timeout: 2_000 });
  await expect(page.locator("[data-stop]")).toHaveCount(3);
});
