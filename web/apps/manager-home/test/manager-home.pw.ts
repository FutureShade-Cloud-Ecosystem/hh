// Manager Home, sub item 1: the landing from the caller's dashboard profile.
// The app boots in demo mode against the stub, the yard manager's card is
// the way in, and the bento renders the profile's widgets in the profile's
// order with the needs-you queue inside it. The same code reads the sales
// manager's profile and renders a different landing: one app, configured per
// role (decision 5), never code per role.
import { test, expect } from "@playwright/test";
import { managerHandoff, resetStub, signInByCard, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the yard manager's card signs in and the band's account menu names the role", async ({ page }) => {
  await page.goto("/");
  const card = page.locator("[data-flow='yard-manager']");
  await expect(card).toBeVisible();
  await card.click();
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await band.locator("button.account").click();
  await expect(page.getByTestId("header-word")).toHaveText("Yard manager");
});

test("the landing is the profile's widgets in the profile's order, the unread tile beside them", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const widgets = page.locator("[data-widget]");
  await expect(widgets).toHaveCount(8);
  const keys = await widgets.evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-widget")));
  expect(keys).toEqual([
    "hero.deliveries",
    "needs.you",
    "picks.board",
    "deliveries.branch",
    "picks.gauge",
    "orders.open",
    "warranty.open",
    "messages.unread",
  ]);
});

test("the landing opens as the board draws it: the welcome line, the TODAY label, the page's own h1", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Welcome back, yard manager.");
  await expect(page.locator(".page-sub")).toHaveText("The pick queue, the shortages and what is going out.");
  await expect(page.locator(".eyebrow")).toHaveText("Today");
});

test("the pick queue board draws a labelled bar per status, the count at the right", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const board = page.locator("[data-widget='picks.board']");
  await expect(board.locator(".bar-row")).toHaveCount(4);
  const first = board.locator(".bar-row").first();
  await expect(first.locator(".label")).toHaveText("Waiting");
  await expect(first.locator(".track .fill")).toBeVisible();
  await expect(first.locator(".value")).toHaveText("8");
});

test("a card links to the live app that owns it, and only a source with no app says it is not here", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const hero = page.locator("[data-widget='hero.deliveries']");
  await expect(hero.locator(".tile-foot")).toHaveText("Not available yet");
  await expect(hero.locator(".tile-foot")).toBeVisible();
  await expect(page.locator("[data-widget='orders.open'] .tile-foot")).toHaveText("Not available yet");
  // Pick queue and Queue depth are the yard's (Yard Pick); Open claims is this app's Warranty view.
  const footLinks = page.locator(".tile-foot button");
  await expect(footLinks).toHaveCount(3);
  await expect(page.locator("[data-widget='picks.board'] .tile-foot button")).toHaveText("Open Yard Pick›");
  await expect(page.locator("[data-widget='warranty.open'] .tile-foot button")).toHaveText("Open Warranty›");
});

test("a foot link opens the live app, and a needs row for a yard source opens Yard Pick", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.route("**/yard/", (route) => route.fulfill({ contentType: "text/html", body: "<title>yard</title>" }));
  await page.goto("/");
  await page.locator("[data-widget='picks.board'] .tile-foot button").click();
  await page.waitForURL("**/yard/");
  await page.goBack();
  await page.locator("[data-needs='tasks.stale_picks'] button", { hasText: "Chase" }).click();
  await page.waitForURL("**/yard/");
});

test("the needs-you footer opens the queue, and the view links sit as small chips with chevrons", async ({ request, page }) => {
  // The whole strip is the plain profile's; each role's own strip is walked in roles.pw.ts.
  await useScenario(request, "plain-profile");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const foot = page.locator("[data-widget='needs.you'] .needs-foot");
  await expect(foot.locator(".queue-link")).toContainText("Open the queue");
  const links = page.locator(".view-link");
  await expect(links).toHaveCount(7);
  for (const label of ["Shortages · open", "Receiving · short", "Approvals", "Drivers", "Reporting", "Warranty", "Quick actions"]) {
    await expect(links.filter({ hasText: label })).toContainText("›");
  }
});

test("the TODAY money cards carry the core's figures, and a measure with no core says so in words", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const kpis = page.locator("[data-test='c10-today-kpis']");
  await expect(kpis).toBeVisible();

  // The stub's viz answer: sales 123456 cents today, margin 3180 bps all time,
  // AR 9624000 cents open of which 3494000 is past its due day. Every figure
  // the card draws came back on the wire; none is a constant the app holds.
  const sales = kpis.locator(".kpi-card", { hasText: "Sales today" });
  await expect(sales.locator(".kpi-value")).toHaveText("$1,234.56");
  await expect(sales.locator(".kpi-compare")).toHaveText("Counter sales committed today");
  const margin = kpis.locator(".kpi-card", { hasText: "Gross margin" });
  await expect(margin.locator(".kpi-value")).toHaveText("31.8%");
  const pastDue = kpis.locator(".kpi-card", { hasText: "Receivables past due" });
  await expect(pastDue.locator(".kpi-value")).toHaveText("$34,940.00");

  // Cash collected today has no core measure: the words, never a figure.
  const cash = kpis.locator(".kpi-card", { hasText: "Cash collected today" });
  await expect(cash).toContainText("Not connected yet");
  await expect(cash).not.toContainText("$");

  // Branch beside branch draws the branches the core named, never a hardcoded pair.
  await kpis.locator(".toggle-pill", { hasText: "Branch beside branch" }).click();
  await expect(sales.locator(".kpi-branch-label").first()).toHaveText("Trenton");
  await expect(sales.locator(".kpi-branch-value").first()).toHaveText("$1,234.56");
});

test("the stops card counts the dispatch board's own stops under its own words, beside the hero's remaining count", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  // FROM-PRINCIPAL 127: the hero's deliveries.today measure counts the day's stops still to do
  // (DELIVERED and CANCELLED excluded); the C10 card counts the day's whole book of stops off the
  // dispatch board. Different measures must not share one heading: the card says Stops today.
  const stops = page.locator("[data-test='c10-deliveries-today']");
  await expect(stops).toBeVisible();
  await expect(stops.locator("h2")).toHaveText("Stops today");
  await expect(stops).not.toContainText("Deliveries today");
  await expect(stops.locator(".deliveries-count")).toHaveText(/^\d+$/);
  await expect(stops.locator(".deliveries-summary")).toContainText(/done, \d+ on the road/);
  const firstRun = stops.locator(".run").first();
  await expect(firstRun.locator(".run-id")).not.toHaveText("");

  // The hero beside it keeps its own measure and its own words, and the two numbers are free to
  // differ: 14 remaining against the stub board's 8, same seed, both the core's own answers.
  const hero = page.locator("[data-widget='hero.deliveries']");
  await expect(hero.locator(".figure")).toHaveText("14");
  await expect(hero.locator(".card-title")).toHaveText("Deliveries today");
  const stopsCount = Number(await stops.locator(".deliveries-count").textContent());
  expect(stopsCount).not.toBe(Number(await hero.locator(".figure").textContent()));

  const needs = page.locator("[data-test='c10-needs-stake']");
  await expect(needs).toBeVisible();
  const amounts = needs.locator(".need-amount");
  const count = await amounts.count();
  for (let i = 1; i < count; i += 1) {
    const before = (await amounts.nth(i - 1).textContent()) ?? "";
    const after = (await amounts.nth(i).textContent()) ?? "";
    if (before !== "not priced" && after !== "not priced") {
      expect(Number(before.replace(/[^0-9.]/g, ""))).toBeGreaterThanOrEqual(Number(after.replace(/[^0-9.]/g, "")));
    }
  }
  await expect(needs.locator(".need-action").first()).toHaveText("Open the request");
});

test("the hero carries the day's count and its window word", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const hero = page.locator("[data-widget='hero.deliveries']");
  await expect(hero).toBeVisible();
  await expect(hero.locator(".figure")).toHaveText("14");
  await expect(hero.locator(".card-sub")).toHaveText("Today");
  await expect(hero.locator(".card-title")).toHaveText("Deliveries today");
});

test("the needs-you queue counts its rows, and Weights pending is the core's own count with a link to Yard Pick", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const needs = page.locator("[data-widget='needs.you']");
  await expect(needs.locator("[data-needs='tasks.open_shortages'] .needs-count")).toHaveText("2");
  await expect(needs.locator("[data-needs='tasks.stale_picks'] .needs-count")).toHaveText("1");
  await expect(needs.locator("[data-needs='purchasing.receiving_differences'] .needs-count")).toHaveText("1");
  await expect(needs.locator("[data-needs='weights.pending'] .what")).toHaveText("Weights pending");
  await expect(needs.locator("[data-needs='weights.pending'] .needs-count")).toHaveText("2");
  await expect(needs.locator("[data-needs='weights.pending'] button")).toHaveAttribute("aria-label", "Weigh, opens Yard Pick");
  await expect(needs.locator("[data-unsourced]")).toHaveCount(0);
  await expect(needs.locator(".needs-total")).toHaveText("6 waiting · Urgent");
});

test("a weights count the core cannot measure is said in words on the row, never a number", async ({ request, page }) => {
  await useScenario(request, "weights-unwired");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const needs = page.locator("[data-widget='needs.you']");
  await expect(needs.locator("[data-needs='weights.pending']")).toHaveCount(0);
  await expect(needs.locator("[data-unsourced='weights.pending'] .what")).toHaveText("Weights pending");
  await expect(needs.locator("[data-unsourced='weights.pending'] .words")).toContainText("not counted");
  await expect(needs.locator("[data-unsourced='weights.pending'] .needs-count")).toHaveCount(0);
  await expect(needs.locator(".needs-total")).toHaveText("4 waiting · Urgent");
});

test("a home with no weights work shows no Weights pending row at all", async ({ request, page }) => {
  await resetStub(request);
  await signInByCard(page, "sales-manager");
  await expect(page.locator("[data-needs='weights.pending'], [data-unsourced]")).toHaveCount(0);
});

test("the needs rows sit as the board draws them: a severity bar, a bold title, the count lighter than it", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const row = page.locator("[data-needs='tasks.open_shortages']");
  await expect(row.locator(".sev")).toBeVisible();
  const titleSize = await row.locator(".what").evaluate((n) => Number.parseFloat(getComputedStyle(n).fontSize));
  const countSize = await row.locator(".needs-count").evaluate((n) => Number.parseFloat(getComputedStyle(n).fontSize));
  expect(countSize).toBeLessThanOrEqual(titleSize);
  const chip = row.locator(".row-chip");
  await expect(chip).toContainText("Resolve");
  await expect(chip).toContainText("›");
});

test("the bars render the server's points, and the tile nothing measures is a tile in the grid, never a number", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const bars = page.locator("[data-widget='deliveries.branch']");
  await expect(bars.locator(".bar-row")).toHaveCount(2);
  await expect(bars.locator(".bar-row .label").first()).toHaveText("Point Loma");
  await expect(bars.locator(".bar-row .value").first()).toHaveText("10");
  const unread = page.locator("[data-widget='messages.unread']");
  await expect(unread.locator(".card-title")).toHaveText("Unread messages");
  await expect(unread).toContainText("No measure is wired");
  await expect(unread.locator(".figure")).toHaveCount(0);
});

test("an unmeasured card says which module is not connected, never a number", async ({ request, page }) => {
  await useScenario(request, "unwired-one");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const claims = page.locator("[data-widget='warranty.open']");
  await expect(claims.locator(".figure")).toHaveText("-");
  await expect(claims.locator(".card-sub")).toContainText("Not connected (warranty)");
});

test("the sales manager's landing comes from the sales profile: the same code, different rows", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='sales-manager']").click();
  await expect(page.locator("[data-widget]")).toHaveCount(9);
  await expect(page.locator("[data-widget='hero.sales'] .figure")).toHaveText("$1,234.56");
  await expect(page.locator("[data-widget='needs.you'] [data-needs='approvals.waiting'] .needs-count")).toHaveText("1");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Welcome back, sales manager.");
});

test("the theme control in the band's account menu flips the document between light and brand", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await band.locator("button.account").click();
  await expect(page.getByRole("button", { name: "Brand theme" })).toBeVisible();
  // The band's theme input drives the document root's attribute: light keeps
  // data-theme=light, brand (the dark half) drops it back to the sheet's dark default.
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await page.getByRole("button", { name: "Brand theme" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", "light");
  await expect(page.getByRole("button", { name: "Light theme" })).toBeVisible();
});

test("the needs link for shortages opens this app's own view, named with the core's words", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator("[data-needs='tasks.open_shortages'] button", { hasText: "Resolve" }).click();
  await expect(page.getByRole("heading", { name: "Shortages · open" })).toBeVisible();
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(page.locator("[data-widget='hero.deliveries']")).toBeVisible();
});

test("the needs link for receiving differences opens Receiving, not Yard Pick", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.locator("[data-needs='purchasing.receiving_differences'] button", { hasText: "Settle" }).click();
  await expect(page.getByRole("heading", { name: "Receiving · short" })).toBeVisible();
});

test("a needs row whose source a live app owns carries a link to it, named for the screen reader", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  const stale = page.locator("[data-needs='tasks.stale_picks']");
  await expect(stale.locator("button")).toHaveAttribute("aria-label", "Chase, opens Yard Pick");
  await expect(stale.locator(".not-here")).toHaveCount(0);
});

test("the way in speaks for this app, never for the till", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Who is working today?" })).toBeVisible();
  await expect(page.getByText("Tap a role to open their home:")).toBeVisible();
  await expect(page.getByText("Who is at the counter?")).toHaveCount(0);
});

test("a failed mint says so in plain words, never the server's raw word", async ({ request, page }) => {
  await useScenario(request, "mint-refused");
  await page.goto("/");
  await page.locator("[data-flow='yard-manager']").click();
  const notice = page.locator(".notice-line[role='status']");
  await expect(notice).toHaveText("Sign-in did not go through. Try again in a moment.");
  await expect(notice).not.toContainText("internal");
});

test("the landing never scrolls sideways at 390 px", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await expect(page.locator("[data-widget='hero.deliveries']")).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});
