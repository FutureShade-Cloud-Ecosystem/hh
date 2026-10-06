// The shared header adoption (HH-RD2 polish round 2 item 5, decision log row
// 938): the Quote Desk's chrome IS the design system's hos-app-header, the
// front door's shape. The old inline chrome (the QD mark and title bar, the
// app's own sign-out) is gone: the brand is the house lockup, the centred
// word rides the element's header-word hook, the right cluster carries
// Switch role and Sign out under their action names, and the demo banner
// rides the demo build flag (this harness builds the demo mode, the same
// seam the front door handoff rides). Switch role ends the session the
// sign-out way and returns to the front door at / (row 938's wording); the
// harness serves this app itself at /, so the walk lands on this app's own
// entry page from a cold boot.
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

test("the chrome is the shared header element, not the app's own bar", async ({ page }) => {
  await signIn(page);
  const header = page.locator("hos-app-header");
  await expect(header).toBeVisible();
  // The element's centred word carries the role; the old inline brand is gone.
  await expect(page.getByTestId("header-word")).toHaveText("Sales");
  await expect(page.getByTestId("qd-title")).toHaveCount(0);
  await expect(page.getByTestId("qd-mark")).toHaveCount(0);
  // The lockup is the house one, inside the element's shadow root.
  await expect(header.getByRole("img", { name: "HH Yard, a Hardscape House product" })).toBeVisible();
  await expect(header).toContainText("a Hardscape House product");
});

test("signed in, the right cluster carries Switch role and Sign out under their action names", async ({ page }) => {
  await signIn(page);
  const switchRole = page.getByRole("button", { name: "Switch role" });
  const signOut = page.getByRole("button", { name: "Sign out", exact: true });
  await expect(switchRole).toBeVisible();
  await expect(signOut).toBeVisible();
  expect(await switchRole.getAttribute("data-action")).toBe("switch-role");
  expect(await signOut.getAttribute("data-action")).toBe("sign-out");
});

test("the demo banner rides the demo build flag: present in this demo-mode harness", async ({ page }) => {
  await page.goto("/");
  const note = page.getByRole("note");
  await expect(note).toBeVisible();
  await expect(note).toContainText("Demo");
  await expect(note).toContainText("Everything on this box is invented demo data: nothing here is real.");
});

test("signed out, the right cluster is hidden and the centred word is the picker's", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role");
  await expect(page.getByRole("button", { name: "Switch role" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sign out", exact: true })).toHaveCount(0);
  // The old chrome's unsigned shape (the lone theme control in the bar) is gone too.
  await expect(page.getByTestId("qd-bar").getByTestId("theme-control")).toHaveCount(0);
});

test("Switch role ends the session and returns to the front door at /", async ({ page }) => {
  await signIn(page);
  await page.evaluate(() => {
    (window as { marker?: string }).marker = "alive";
  });
  await page.getByRole("button", { name: "Switch role" }).click();
  // The navigation reloaded the document (the marker died with it) and the
  // boot that came up is signed out: the picker's word, no desk, and the
  // custody mirror went with the session, so the front door it lands on
  // comes up at its own picker rather than at a session it did not mint.
  await expect(page.getByTestId("header-word")).toHaveText("Pick a role", { timeout: 15_000 });
  const state = await page.evaluate(() => ({
    marker: (window as { marker?: string }).marker,
    mirror: window.sessionStorage.getItem("hh-auth.session.v2"),
    handoff: window.sessionStorage.getItem("hh-demo.front-door.session.v1"),
  }));
  expect(state.marker).toBeUndefined();
  expect(state.mirror).toBeNull();
  expect(state.handoff).toBeNull();
  await expect(page.getByTestId("qb-list")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeVisible();
});
