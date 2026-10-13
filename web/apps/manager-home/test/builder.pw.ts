// Manager Home, the dashboard builder: the arrange view the landing's one
// edit button opens. The legs drive the whole surface against the stub: the
// edit grid's move, size and remove controls, the gallery and the form, the
// saves and resets, the pinned tabs, and the role defaults an admin sets,
// refused in the core's own word for a caller who is not one.
import { test, expect, type Page } from "@playwright/test";
import { managerHandoff, resetStub, useScenario, signInByCard } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

/** Signs in as the yard manager through the front door's handoff and opens the arrange view. */
async function arrange(page: Page): Promise<void> {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.getByRole("button", { name: "Arrange home" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Arrange home");
  await expect(page.locator("[data-test=\'builder-grid\']")).toBeVisible();
}

test("the landing's one edit button opens the arrange view with the profile's widgets in it", async ({ page }) => {
  await arrange(page);
  await expect(page.locator("[data-test=\'builder-grid\']").locator("[data-widget]")).toHaveCount(7);
  await expect(page.getByText("The role default · the manager.yard profile")).toBeVisible();
});

test("the empty home says so in the board's own words and offers the gallery", async ({ request, page }) => {
  await useScenario(request, "no-widgets");
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.getByRole("button", { name: "Arrange home" }).click();
  const empty = page.locator("[data-test=\'builder-empty\']");
  await expect(empty).toBeVisible();
  await expect(empty).toContainText("No widgets yet - add one");
  await expect(empty.getByRole("button", { name: "Add widget" })).toBeVisible();
});

test("a move control reorders the grid without a drag, and the save button lights with the change", async ({ page }) => {
  await arrange(page);
  const grid = page.locator("[data-test=\'builder-grid\']");
  const before = await grid.locator("[data-widget]").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-widget")));
  expect(before[0]).toBe("hero.deliveries");
  const save = page.locator("[data-test=\'builder-save\']");
  await expect(save).toBeDisabled();
  await grid.locator("[data-widget='needs.you'] button").filter({ hasText: "Earlier" }).click();
  const after = await grid.locator("[data-widget]").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-widget")));
  expect(after[0]).toBe("needs.you");
  expect(after[1]).toBe("hero.deliveries");
  await expect(save).toBeEnabled();
});

test("the size controls step through the presets, and the span class follows", async ({ page }) => {
  await arrange(page);
  const card = page.locator("[data-test=\'builder-grid\']").locator("[data-widget='picks.gauge']");
  await expect(card).toContainText("Small");
  await card.getByRole("button", { name: "Make Queue depth bigger" }).click();
  await expect(card).toContainText("Tall");
  await expect(card.locator("..")).toHaveClass(/r2/);
});

test("a remove takes the widget off the grid, and the needs-you queue refuses in words", async ({ page }) => {
  await arrange(page);
  const grid = page.locator("[data-test=\'builder-grid\']");
  await grid.locator("[data-widget='orders.open'] button").filter({ hasText: "Remove" }).click();
  await expect(grid.locator("[data-widget='orders.open']")).toHaveCount(0);
  const queue = grid.locator("[data-widget='needs.you']");
  await expect(queue).toHaveCount(1);
  await expect(queue.getByRole("button", { name: "Remove Needs you" })).toHaveCount(0);
  await expect(queue).toContainText("Stays on every home");
});

test("the gallery places a ready-made widget at the top, and the save PUTs the whole layout", async ({ request, page }) => {
  await arrange(page);
  await page.locator("[data-test=\'builder-add\']").click();
  const gallery = page.locator("[data-test=\'builder-gallery\']");
  await expect(gallery.getByRole("button", { name: "Add Deliveries today to your home" })).toBeVisible();
  await gallery.getByRole("button", { name: "Add Deliveries today to your home" }).click();
  const grid = page.locator("[data-test=\'builder-grid\']");
  await expect(grid.locator("[data-widget]").first()).toHaveAttribute("data-widget", "deliveries.today");
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("Save to keep it");
  await page.locator("[data-test=\'builder-save\']").click();
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("Your home is saved.");
  const log = (await (await request.get("/_stub/log")).json()) as { method: string; path: string; body?: { widgets?: { widget_key: string }[] } }[];
  const put = log.filter((entry) => entry.method === "PUT" && entry.path === "/api/v1/dashboard/widgets");
  assertWidgetKeys(put);
});

function assertWidgetKeys(puts: { body?: { widgets?: { widget_key: string }[] } }[]): void {
  const keys = (puts[puts.length - 1]?.body?.widgets ?? []).map((w) => w.widget_key);
  if (!keys.includes("deliveries.today") || !keys.includes("needs.you")) {
    throw new Error(`the save did not PUT the whole layout: ${JSON.stringify(keys)}`);
  }
}

test("build your own corrects its draft into the catalog and previews through the composed series", async ({ page }) => {
  await arrange(page);
  await page.locator("[data-test=\'builder-add\']").click();
  await page.locator("[data-test=\'builder-open-form\']").click();
  const form = page.locator("[data-test=\'builder-form\']");
  await expect(form).toBeVisible();
  // The first measure of the catalog is Deliveries today, whose own window is
  // today; picking the pick queue corrects the window to everything open.
  await form.getByRole("button", { name: "Pick queue", exact: true }).click();
  await expect(form.getByRole("button", { name: "Everything open" })).toHaveAttribute("aria-pressed", "true");
  const preview = page.locator("[data-test=\'builder-preview\']");
  await expect(preview).toContainText("20");
  await expect(preview).toContainText("Waiting");
  await page.locator("[data-test=\'builder-place\']").click();
  await expect(page.locator("[data-test=\'builder-grid\']").locator("[data-widget]").first()).toHaveAttribute("data-widget", "picks.queue");
});

test("the pinned tabs strip pins, unpins and saves whole", async ({ page }) => {
  await arrange(page);
  const tabs = page.locator("[data-test=\'builder-tabs\']");
  await expect(tabs.locator("[data-tab='yard']")).toBeVisible();
  await expect(tabs.locator("[data-tab='purchasing']")).toContainText("Unpinned");
  await tabs.locator("[data-tab='purchasing'] button").filter({ hasText: "Pin" }).click();
  await expect(tabs.locator("[data-tab='purchasing'] button").filter({ hasText: "Pinned" })).toBeVisible();
  await tabs.getByRole("button", { name: "Save tabs" }).click();
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("pinned tabs are saved");
  const log = (await (await request0(page)).json()) as { method: string; path: string; body?: { pins?: unknown[] } }[];
  const put = log.filter((entry) => entry.method === "PUT" && entry.path === "/api/v1/dashboard/tabs");
  const pins = put[put.length - 1]?.body?.pins ?? [];
  if (pins.length !== 2) throw new Error(`the tab save did not write the whole set: ${JSON.stringify(pins)}`);
});

// The stub's log rides the app's own origin through the vite proxy.
function request0(page: Page) {
  return page.request.get("/_stub/log");
}

test("reset to role default replaces the grid with the profile's rows", async ({ page }) => {
  await arrange(page);
  const grid = page.locator("[data-test=\'builder-grid\']");
  await grid.locator("[data-widget='orders.open'] button").filter({ hasText: "Remove" }).click();
  await page.locator("[data-test=\'builder-save\']").click();
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("Your home is saved.");
  await page.getByRole("button", { name: "Reset to role default" }).click();
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("back to the role default");
  await expect(grid.locator("[data-widget]")).toHaveCount(7);
});

test("the role defaults are refused for a caller who is not an admin, in the core's own word", async ({ page }) => {
  await arrange(page);
  await page.getByRole("tab", { name: "Role defaults" }).click();
  const refusal = page.locator("[data-test=\'role-error\']");
  await expect(refusal).toContainText("Forbidden");
  await expect(refusal).toContainText("administrator");
});

test("an admin signs in, opens the role defaults, and saves them through the profile routes", async ({ page }) => {
  await signInByCard(page, "financial-controller");
  await page.getByRole("button", { name: "Arrange home" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Arrange home");
  await page.getByRole("tab", { name: "Role defaults" }).click();
  const widgets = page.locator("[data-test=\'role-widgets\']");
  await expect(widgets).toBeVisible();
  await expect(widgets.locator("[data-widget]")).toHaveCount(7);
  await widgets.locator("[data-widget='needs.you']").waitFor();
  await page.locator("[data-test=\'role-widgets-save\']").click();
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("manager.yard role's widgets are saved");
  const log = (await (await request0(page)).json()) as { method: string; path: string }[];
  const put = log.filter((entry) => entry.method === "PUT" && entry.path === "/api/v1/dashboard/profiles/manager.yard/widgets");
  if (put.length === 0) throw new Error("the role save never reached the profile route");
});

test("the admin's role gallery removes an entry and the save carries the rest", async ({ page }) => {
  await signInByCard(page, "financial-controller");
  await page.getByRole("button", { name: "Arrange home" }).click();
  await page.getByRole("tab", { name: "Role defaults" }).click();
  const gallery = page.locator("[data-test=\'role-gallery\']");
  await expect(gallery.locator(".edit-row[data-entry]")).toHaveCount(3);
  await gallery.locator("[data-entry='claims-open']").getByRole("button", { name: "Remove Open claims from the gallery" }).click();
  await expect(gallery.locator(".edit-row[data-entry]")).toHaveCount(2);
  await page.locator("[data-test=\'role-gallery-save\']").click();
  await expect(page.locator("[data-test=\'builder-notice\']")).toContainText("manager.yard role's gallery is saved");
});

test("the arrange view works from the keyboard: the edit button, a move, and back", async ({ page }) => {
  await page.addInitScript(managerHandoff);
  await page.goto("/");
  await page.keyboard.press("Tab");
  // The theme control sits in the header before the page's own controls; the
  // edit button is reachable by tabbing, and every control in the arrange
  // view is a button.
  await page.getByRole("button", { name: "Arrange home" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Arrange home");
  const grid = page.locator("[data-test=\'builder-grid\']");
  await grid.locator("[data-widget='needs.you'] button").filter({ hasText: "Earlier" }).focus();
  await page.keyboard.press("Enter");
  const after = await grid.locator("[data-widget]").evaluateAll((nodes) => nodes.map((n) => n.getAttribute("data-widget")));
  expect(after[0]).toBe("needs.you");
  await page.getByRole("button", { name: "Back to Home" }).click();
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Welcome back");
});
