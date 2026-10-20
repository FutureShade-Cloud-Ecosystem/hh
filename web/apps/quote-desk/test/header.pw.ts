// The ONE band adoption (HH-RD2 polish round 2 item 5, decision log row
// 938, reworked for PR 503 round 1): the Quote Desk's chrome IS the shell
// chrome's hh-shell-header in band variant. The app-local header (the HH
// lockup, the sub line, the amber Demo strip, the role label) is gone: the
// dealer lockup, the centred selector, the DEMO and Synced chips, and the
// account chip's menu carry Theme, Feedback, Switch role and Sign out.
// Switch role ends the session the sign-out way and returns to the front
// door at / (row 938's wording); the harness serves this app itself at /,
// so the walk lands on this app's own entry page from a cold boot.
import { expect, test, type Page } from "@playwright/test";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openAccountMenu(page: Page): Promise<void> {
  const menu = page.locator("hh-shell-header .account-menu");
  if (!(await menu.isVisible())) await page.locator("hh-shell-header button.account").click();
}

test("the chrome is the band, not the app's own bar", async ({ page }) => {
  await signIn(page);
  const band = page.locator("hh-shell-header");
  await expect(band).toBeVisible();
  await expect(band).toContainText("Gable Landscape Supply");
  await expect(band.locator("button.selector")).toContainText("Quote Desk > Quotes");
  // The old inline brand is gone.
  await expect(page.getByTestId("qd-title")).toHaveCount(0);
  await expect(page.getByTestId("qd-mark")).toHaveCount(0);
  // The role sits in the account menu.
  await openAccountMenu(page);
  await expect(page.getByTestId("signed-in-role")).toHaveText("Sales");
});

test("signed in, the account menu carries Switch role and Sign out under their action names", async ({ page }) => {
  await signIn(page);
  await openAccountMenu(page);
  const switchRole = page.getByRole("button", { name: "Switch role" });
  const signOut = page.getByRole("button", { name: "Sign out", exact: true });
  await expect(switchRole).toBeVisible();
  await expect(signOut).toBeVisible();
  expect(await switchRole.getAttribute("data-action")).toBe("switch-role");
  expect(await signOut.getAttribute("data-action")).toBe("sign-out");
});

test("the DEMO chip and Synced chip ride the band: one DEMO marker, no amber strip", async ({ page }) => {
  await page.goto("/");
  const band = page.locator("hh-shell-header");
  await expect(band.getByText("DEMO", { exact: true })).toHaveCount(1);
  await expect(band).toContainText("Synced");
  await expect(page.getByText("Everything on this box is invented demo data")).toHaveCount(0);
});

test("signed out, the account menu has no role actions and still carries the theme control", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("hh-shell-header")).toBeVisible();
  await expect(page.getByRole("button", { name: "Switch role" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toHaveCount(0);
  await openAccountMenu(page);
  await expect(page.getByTestId("theme-control")).toBeVisible();
});

test("Switch role ends the session and returns to the front door at /", async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => {
    (window as { marker?: string }).marker = "alive";
  });
  await openAccountMenu(page);
  await page.getByRole("button", { name: "Switch role" }).click();
  // The navigation reloaded the document (the marker died with it) and the
  // boot that came up is signed out: the picker's cards show, the custody
  // mirror went with the session, so the front door it lands on comes up at
  // its own picker rather than at a session it did not mint.
  await page.waitForFunction(() => (window as { marker?: string }).marker === undefined, undefined, { timeout: 15_000 });
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  const state = await page.evaluate(() => ({
    marker: (window as { marker?: string }).marker,
    mirror: window.sessionStorage.getItem("hh-auth.session.v2"),
    handoff: window.sessionStorage.getItem("hh-demo.front-door.session.v1"),
  }));
  expect(state.marker).toBeUndefined();
  expect(state.mirror).toBeNull();
  expect(state.handoff).toBeNull();
  await expect(page.getByTestId("qb-list")).toHaveCount(0);
});

test("the theme control lives inside the band's account menu, not on a row of its own", async ({ page }) => {
  await signIn(page);
  await openAccountMenu(page);
  const control = page.getByTestId("theme-control");
  await expect(control).toBeVisible();
  // The band's next sibling is the desk tab strip or main, never a bare
  // control row between the band and the page.
  const afterBand = await page.evaluate(() => {
    const band = document.querySelector("qd-app-root")?.shadowRoot?.querySelector("hh-shell-header");
    const next = band?.nextElementSibling ?? null;
    return next === null ? null : { tag: next.tagName.toLowerCase(), id: next.getAttribute("data-testid") };
  });
  expect(afterBand).toMatchObject({ tag: "nav", id: "desk-tabs" });
});
