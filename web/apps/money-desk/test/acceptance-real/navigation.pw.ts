// THE NAVIGATION MAKES NO SIGN-IN EXCHANGE (W2-4 (b): FROM-PRINCIPAL 411 and 412).
//
// WHAT THIS SPEC PROVES, in three named figures:
//   1. THE FIRST LOAD: the desk's console build recovers its session on the page load that
//      signed the person in, so the page spends exactly one POST /api/v1/auth/exchange;
//   2. THE NAVIGATION: switching through every area of the desk's switch, and through up to
//      six tabs of each area, twice over, spends nothing at all: the exchange count after
//      the navigation is the count after the first load;
//   3. THE RELOAD: one reload spends exactly one more exchange. That is the per-load cost
//      the core's allowance is sized against (UserAllowance 10 a minute,
//      core/internal/identity/exchange/exchange.go).
//
// WHY THE FIGURES MATTER (FROM-PRINCIPAL 411 (b), and 412's answer). The desk's console
// build spends an exchange on every page load: it recovers its own session on load (the
// custody, web/packages/pos-kit/src/ported/boot-custody.ts, asks the console for the
// account and then exchanges it for a core session). The core admits ten exchanges a minute
// for one subject, so a person who moves around the desk must not spend the allowance on
// the way. The lead measured this on the real box with a scratch spec (TO-PRINCIPAL 666:
// one exchange after the first load, still one after two rounds of every area and its tabs,
// two after one reload). This spec is that measurement made permanent: a desk that began
// exchanging on an area switch, or on a tab, shows here.
//
// NOTHING HERE IS TYPED. The area list is read off the desk's own switch (the buttons'
// data-area values) and the tab list off the tabs' own data-tab-id values, so the walk
// covers whatever the desk actually offers and can never name an area or a tab that is not
// there. The figures are the page's own requests, counted as they leave.
//
// ONE WAY IN. The session comes from ./session's asStaff, the same way every acceptance-real
// spec signs in, so this leg adds no way in of its own and reads the box's own session.
//
// THE RED. This spec runs on the real box (test/acceptance-real.config.ts). Under the stub
// (test/acceptance-real-stub.config.ts) the desk is in demo mode and recovers no session at
// all, so the page spends no exchange on any load: there the first figure is zero, not one,
// and the spec says so in the open rather than passing quietly.
import { expect, test, type Page } from "@playwright/test";
import { asStaff } from "./session.js";

/** The core's exchange route: the one call a page load spends. */
const EXCHANGE_PATH = "/api/v1/auth/exchange";

/** How many times over the walk covers every area and its tabs. */
const ROUNDS = 2;

/** How many tabs of one area the walk visits. */
const TABS_PER_AREA = 6;

/** The three figures this spec names, each in the words of the claim it carries. */
const FIGURE_FIRST_LOAD = "the exchange count after the first load";
const FIGURE_NAVIGATION = "the exchange count after the navigation rounds";
const FIGURE_RELOAD = "the exchange count after one reload";

/**
 * Counts the exchanges the page makes, as they leave the page. The listener sits
 * on the page, so it sees the desk's own requests and nothing else: the harness's
 * own exchange (the one asStaff spends) never crosses the page.
 */
function countExchanges(page: Page): { readonly count: () => number } {
  let count = 0;
  page.on("request", (request) => {
    if (request.method() !== "POST") return;
    const path = new URL(request.url()).pathname;
    if (path.endsWith(EXCHANGE_PATH)) count += 1;
  });
  return { count: () => count };
}

/**
 * Signs in as the Financial Controller, the one way in, and lands on the desk.
 * "./" stays under the real config's baseURL (/money-desk/ on the box); "/" would
 * escape to the front door at the origin root. The heading is the desk's own sign
 * that the session was recovered and the first area is open, so the exchange that
 * load spent has already happened when this returns.
 */
async function signIn(page: Page): Promise<void> {
  await asStaff(page.context(), "financial_controller");
  await page.goto("./");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals", { timeout: 30_000 });
}

/** The areas the desk's switch offers, read off the page. */
async function areaIds(page: Page): Promise<string[]> {
  const ids = await page.locator('[data-action="area"]').evaluateAll((els) =>
    els
      .map((el) => el.getAttribute("data-area"))
      .filter((id): id is string => typeof id === "string" && id !== ""),
  );
  expect(ids.length, "the number of areas the desk's switch offers").toBeGreaterThan(0);
  return ids;
}

/** The tabs the open area offers, read off the page. */
async function tabIds(page: Page): Promise<string[]> {
  return await page.locator("[data-tab-id]").evaluateAll((els) =>
    els
      .map((el) => el.getAttribute("data-tab-id"))
      .filter((id): id is string => typeof id === "string" && id !== ""),
  );
}

/**
 * Walks the desk: every area of the switch, and up to six of its tabs, twice over.
 *
 * The area button's own aria-current is the settle point: the switch and the area's
 * screens are drawn in one pass, so a button that reads current is a switch that has
 * already drawn the area, and the tab list read after it is that area's own list. The
 * same holds for a tab. Each tab is read again before it is clicked, so a list that
 * changes under the walk cannot leave the spec clicking a tab that is gone.
 */
async function walkTheDesk(page: Page): Promise<void> {
  const areas = await areaIds(page);
  for (let round = 0; round < ROUNDS; round += 1) {
    for (const area of areas) {
      const areaButton = page.locator(`[data-action="area"][data-area="${area}"]`);
      await areaButton.click();
      await expect(areaButton, `the area ${area} to be the open one`).toHaveAttribute("aria-current", "true");
      const visited: string[] = [];
      for (let step = 0; step < TABS_PER_AREA; step += 1) {
        const tabs = await tabIds(page);
        const next = tabs.find((id) => !visited.includes(id));
        if (next === undefined) break;
        visited.push(next);
        const tabButton = page.locator(`[data-tab-id="${next}"]`);
        await tabButton.click();
        await expect(tabButton, `the tab ${next} to be the open one`).toHaveAttribute("aria-current", "true");
      }
    }
  }
}

test("the desk exchanges once for the page load, and moving around it spends nothing", async ({ page }) => {
  const tally = countExchanges(page);
  await signIn(page);

  // 1. THE FIRST LOAD. The console build recovers its session here, so the page
  // spends exactly one exchange, and that is the whole of the desk's sign-in cost
  // for this page.
  const afterFirstLoad = tally.count();
  expect(afterFirstLoad, FIGURE_FIRST_LOAD).toBe(1);

  // 2. THE NAVIGATION. Two rounds over every area of the switch and up to six
  // tabs of each: the areas and the tabs are read off the page, never typed.
  await walkTheDesk(page);
  const afterNavigation = tally.count();
  expect(afterNavigation, FIGURE_NAVIGATION).toBe(afterFirstLoad);

  // 3. THE RELOAD. One reload is one more page load, so it spends exactly one more
  // exchange. This is the per-load cost the core's allowance is sized against.
  await page.reload();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Approvals", { timeout: 30_000 });
  const afterReload = tally.count();
  expect(afterReload, FIGURE_RELOAD).toBe(afterFirstLoad + 1);
});
