// The Work shell host, walked in a real browser: sign in through the front
// door's own picker (a page under the host), the app switcher by role, the
// landing per role, switching between hosted apps, Switch role back to the
// picker, and pins that belong to the person. The desk apps here are fixture
// pages that read and drop the front door's key the way the real apps do
// (test/support/compose.mjs); the real bundles run in the built shell leg and
// in the WS_DIST run.
import { expect, test } from "./fixtures";
import type { Page } from "@playwright/test";

const KEYS = ["hh-demo.front-door.session.v1", "hh-auth.session.v2"];

async function open(page: Page, shellUrl: string): Promise<void> {
  await page.goto(`${shellUrl}/`);
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "door");
}

async function signInAs(page: Page, shellUrl: string, label: string): Promise<void> {
  await open(page, shellUrl);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: label, exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
}

async function switcherApps(page: Page): Promise<string[]> {
  await page.locator("hh-shell-header button.selector").click();
  const ids = await page.locator("hh-shell-header .panel .mi").evaluateAll((rows) => rows.map((r) => r.getAttribute("data-app") ?? ""));
  await page.keyboard.press("Escape");
  return ids;
}

async function switchApp(page: Page, appId: string): Promise<void> {
  await page.locator("hh-shell-header button.selector").click();
  await page.locator(`hh-shell-header .panel .mi[data-app="${appId}"]`).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", appId);
}

async function switchRole(page: Page): Promise<void> {
  await page.locator("hh-shell-header button.account").click();
  await page.getByTestId("account-menu").getByRole("button", { name: "Switch role" }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "door");
}

test("Sales signs in through the front door's picker and lands on the Counter Till", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  const shell = page.getByTestId("ws-shell");
  await expect(shell).toHaveAttribute("data-role", "inside_sales");
  await expect(shell).toHaveAttribute("data-current-app", "counter-till");
  await expect(page.locator("hh-shell-header button.selector")).toContainText("Counter Till");
  const frame = page.frameLocator('[data-testid="app-frame"]');
  await expect(frame.getByTestId("fixture")).toHaveText("fixture:/till/:inside_sales");
});

test("the switcher is built from the profile by the signed in role", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  expect(await switcherApps(page)).toEqual(["counter-till", "quote-desk", "buying-and-stock", "money-desk", "house-messages"]);
  await switchRole(page);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Logistics Manager", exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  expect(await switcherApps(page)).toEqual(["dispatch-board", "manager-home", "house-messages"]);
});

test("a sales manager lands on Manager Home, not the Counter Till", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales manager");
  const shell = page.getByTestId("ws-shell");
  await expect(shell).toHaveAttribute("data-current-app", "manager-home");
  await expect(page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture")).toHaveText("fixture:/home/:sales_manager");
  expect(await switcherApps(page)).toContain("counter-till");
});

test("an app the role does not hold is not offered in its switcher", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  const apps = await switcherApps(page);
  expect(apps).not.toContain("manager-home");
  expect(apps).not.toContain("dealer-admin");
  expect(apps).not.toContain("dispatch-board");
  // every internal role holds House Messages, Sales included
  expect(apps).toContain("house-messages");
});

test("switching apps hosts the chosen app and hands it the session again", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  const frame = page.frameLocator('[data-testid="app-frame"]');
  await expect(frame.getByTestId("fixture")).toHaveText("fixture:/till/:inside_sales");
  await switchApp(page, "quote-desk");
  // the first app dropped the key; the host wrote it again for the second
  await expect(frame.getByTestId("fixture")).toHaveText("fixture:/quote/:inside_sales");
  await switchApp(page, "money-desk");
  await expect(frame.getByTestId("fixture")).toHaveText("fixture:/money-desk/:inside_sales");
  await expect(page.locator("hh-shell-header button.selector")).toContainText("Money Desk");
});

test("Switch role returns to the picker and drops the session", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  await switchRole(page);
  await expect(page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Sales manager", exact: true })).toBeVisible();
  await expect(page.locator("hh-shell-header")).toHaveCount(0);
  const kept = await page.evaluate((keys) => keys.filter((k) => sessionStorage.getItem(k) !== null), KEYS);
  expect(kept).toEqual([]);
  // a reload lands on the picker too: there is nothing to restore
  await page.reload();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "door");
});

test("a second role sees none of the first role's pins, and the first gets them back", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  await expect(page.locator("hh-shell-rail")).toContainText("No surfaces pinned yet");
  await page.locator('hh-shell-header button[data-action="pin"]').click();
  await expect(page.locator('hh-shell-rail button.row[data-key="counter-till"]')).toHaveCount(1);
  await switchApp(page, "quote-desk");
  await page.locator('hh-shell-header button[data-action="pin"]').click();
  await expect(page.locator("hh-shell-rail button.row")).toHaveCount(2);

  await switchRole(page);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Sales manager", exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  await expect(page.locator("hh-shell-rail button.row")).toHaveCount(0);
  await expect(page.locator("hh-shell-rail")).toContainText("No surfaces pinned yet");

  await switchRole(page);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  await expect(page.locator("hh-shell-rail button.row")).toHaveCount(2);
  // a pin opens its app; the unpinned rail waits as its toggle and shows its rows on hover (G1)
  await page.locator("hh-shell-rail").hover();
  await page.locator('hh-shell-rail button.row[data-key="quote-desk"]').click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", "quote-desk");
});

test("an app that asks for the origin root (its own Switch role) returns the host to the picker", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  await page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture-switch-role").click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "door");
  await expect(page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Sales", exact: true })).toBeVisible();
  // exactly one host on the page: the framed host did not mount a second chrome
  await expect(page.locator("hh-shell-header")).toHaveCount(0);
});

test("a role holding only House Messages lands on it, with Switch role in the header", async ({ page, shellUrl }) => {
  await signInAs(page, shellUrl, "Driver");
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", "house-messages");
  await expect(page.frameLocator('[data-testid="app-frame"]').getByTestId("fixture")).toHaveText("fixture:/messages/:driver");
  await switchRole(page);
});

test("going offline shows the offline banner and the chip says Offline", async ({ page, context, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  await expect(page.locator("hh-shell-header .chip .word")).toHaveText("Synced");
  await expect(page.locator("hh-shell-offline-banner .banner")).toHaveCount(0);
  await context.setOffline(true);
  await expect(page.locator("hh-shell-header .chip .word")).toHaveText("Offline");
  await expect(page.locator('hh-shell-offline-banner .banner[data-mode="offline"]')).toBeVisible();
  await context.setOffline(false);
  await expect(page.locator("hh-shell-header .chip .word")).toHaveText("Synced");
});

test("the host loads nothing from outside its own origin", async ({ page, shellUrl }) => {
  const foreign: string[] = [];
  page.on("request", (request) => {
    const url = request.url();
    if (!url.startsWith(shellUrl) && !url.startsWith("data:") && !url.startsWith("blob:")) foreign.push(url);
  });
  await signInAs(page, shellUrl, "Sales");
  await switchApp(page, "quote-desk");
  await switchRole(page);
  expect(foreign).toEqual([]);
});

test("offline over the Counter Till: the banner shows the copy on the device and never says writes wait", async ({ page, context, shellUrl }) => {
  await signInAs(page, shellUrl, "Sales");
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", "counter-till");
  await context.setOffline(true);
  const banner = page.locator('hh-shell-offline-banner .banner[data-mode="offline"]');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("showing the copy on this device");
  const words = (await banner.innerText()).toLowerCase();
  expect(words).not.toMatch(/writes? wait|outbox|send/);
  await context.setOffline(false);
});
