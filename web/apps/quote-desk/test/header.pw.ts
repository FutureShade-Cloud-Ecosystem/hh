// The shared header adoption (HH-RD2 polish round 2 item 5, decision log row
// 938): the Quote Desk's chrome IS the design system's hos-app-header, the
// front door's shape. The old inline chrome (the QD mark and title bar, the
// app's own sign-out) is gone: the brand is the house lockup, the centred
// word rides the element's header-word hook, the right cluster carries
// Switch role and Sign out under their action names and the app's theme
// control in the element's named theme slot (the item 5 followup), and the
// demo banner rides the demo build flag (this harness builds the demo mode,
// the same seam the front door handoff rides). Switch role ends the session
// the sign-out way and returns to the front door at / (row 938's wording);
// the harness serves this app itself at /, so the walk lands on this app's
// own entry page from a cold boot.
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
  // The theme control keeps its home in the header's theme slot unsigned too
  // (the front door's own theme switcher is an unsigned control): it never
  // falls back to a row of its own under the bar.
  await expect(page.getByTestId("qd-bar").getByTestId("theme-control")).toHaveCount(1);
  await expect(page.getByTestId("qd-bar").getByTestId("theme-control")).toBeVisible();
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
  //
  // The sign out paints the picker's word on the OLD document a moment before the walk to / reloads
  // it, so the word alone does not prove the new document is up: a read taken between the two died
  // with "Execution context was destroyed" on a loaded runner. Wait for the marker to be gone (a
  // waitForFunction survives the navigation), then for the word, then read.
  await page.waitForFunction(() => (window as { marker?: string }).marker === undefined, undefined, { timeout: 15_000 });
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

// The theme control's home (the item 5 followup, the principal's correction
// to this adoption): the control passes into the shared header's named theme
// slot and lives INSIDE the element's right cluster, after Switch role and
// Sign out, behind the thin separator. The bare row under the bar was the
// regression: the control floated alone between the header and the page's
// title row. Red first at 1b314b77: the control sat outside the element
// (its own row, the div between the header and main), and the bounds check
// found it there.
test("the theme control lives inside the shared header's right cluster, not on a row of its own", async ({ page }) => {
  await signIn(page);
  const header = page.locator("hos-app-header");
  // The control is the element's slotted content: its wrapper claims the
  // named theme slot, and the cluster carries it after the two buttons.
  const control = header.getByTestId("theme-control");
  await expect(control).toBeVisible();
  const inside = await page.evaluate(() => {
    // The header renders inside the app root's shadow tree: the walk goes
    // through qd-app-root's shadowRoot, not document.querySelector.
    const appRoot = document.querySelector("qd-app-root");
    const el = appRoot !== null && appRoot.shadowRoot !== null ? appRoot.shadowRoot.querySelector("hos-app-header") : null;
    if (el === null || el.shadowRoot === null) return { ok: false } as const;
    const cluster = el.shadowRoot.querySelector(".controls");
    if (cluster === null) return { ok: false } as const;
    const slot = cluster.querySelector('slot[name="theme"]') as HTMLSlotElement | null;
    const sep = cluster.querySelector(".theme-sep");
    const signOut = cluster.querySelector('button[data-action="sign-out"]');
    const control = el.querySelector('[data-testid="theme-control"]');
    const box = (node: Element) => node.getBoundingClientRect();
    const controlBox = control !== null ? box(control) : null;
    const signBox = signOut !== null ? box(signOut) : null;
    return {
      ok: true,
      clusterChildren: [...cluster.children].map((node) => node.tagName.toLowerCase()),
      slotted:
        slot !== null &&
        control !== null &&
        slot.assignedElements().some((assigned: Element) => assigned.contains(control)),
      hasSeparator: sep !== null,
      sameBandAsSignOut:
        controlBox !== null &&
        signBox !== null &&
        Math.abs((controlBox.top + controlBox.bottom) / 2 - (signBox.top + signBox.bottom) / 2) < 12,
      insideBar: controlBox !== null && controlBox.bottom <= box(el).bottom + 0.5,
    };
  });
  expect(inside.ok).toBe(true);
  expect(inside.clusterChildren).toEqual(["button", "button", "span", "slot"]);
  expect(inside.slotted).toBe(true);
  expect(inside.hasSeparator).toBe(true);
  expect(inside.sameBandAsSignOut).toBe(true);
  expect(inside.insideBar).toBe(true);
  // The bounds test: nothing unnamed sits between the header and the page's
  // title row. The one sanctioned element there on the desk surface is the
  // desk tab strip (Quotes for every desk user, the manager's Pricing tab,
  // decision log rows 975 and 977): the header's next sibling is that nav,
  // and the nav's own next sibling is the page's main. A bare control row
  // (the regression this test was red on) is neither of those.
  const between = await page.evaluate(() => {
    const appRoot = document.querySelector("qd-app-root");
    const el = appRoot !== null && appRoot.shadowRoot !== null ? appRoot.shadowRoot.querySelector("hos-app-header") : null;
    const strip = el !== null ? el.nextElementSibling : null;
    return strip === null
      ? null
      : { tag: strip.tagName.toLowerCase(), id: strip.getAttribute("data-testid"), after: strip.nextElementSibling === null ? null : strip.nextElementSibling.tagName.toLowerCase() };
  });
  expect(between).toEqual({ tag: "nav", id: "desk-tabs", after: "main" });
});
