// Projects legs in a real browser against the shared tenant stub. The surface
// is the flat list of the account's procurement projects, the QUOTING ones
// included: the Board keeps only the planning projects, so this list is where
// a quoting project stands while the dealer prices it. Visual references: the
// HH: Pro board's plan cards on the design system's cards, the same card shape
// the Board's Plan column carries. Phone first (390).
import { test, expect, type Page } from "@playwright/test";
import { join } from "node:path";

const SHOTS = process.env.PRO_SHOT_DIR ?? join("test", "shots");
const PASSWORD = "stub-password";
const EMAIL = (who: string): string => `contractor.${who}@hh-demo.invalid`;

async function signIn(page: Page, who: string): Promise<void> {
  await page.goto("/");
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator("hh-pro-frame")).toBeVisible();
}

async function openProjects(page: Page): Promise<void> {
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await panel.getByRole("button", { name: "Projects", exact: true }).click();
  await expect(page.getByTestId("surface-title")).toHaveText("Projects");
}

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

test.use({ viewport: { width: 390, height: 844 } });

test("Projects opens from the selector and lists every project, the QUOTING one included", async ({ page }) => {
  await signIn(page, "owner");
  await openProjects(page);
  const rows = page.getByTestId("projects-row");
  await expect(rows).toHaveCount(4);
  const quoting = rows.filter({ hasText: "Santa Fe courtyard stone" });
  await expect(quoting).toContainText("Quoting");
  await expect(quoting).toContainText("2 lines");
  await expect(quoting).toContainText("Estimated");
  const planning = rows.filter({ hasText: "Backyard planting beds" });
  await expect(planning).toContainText("Planning");
  await expect(planning).toContainText("No lines yet");
  const lines = rows.filter({ hasText: "La Jolla terrace pavers" });
  await expect(lines).toContainText("Estimated");
  await expect(lines).not.toContainText("Not priced");
  await page.screenshot({ path: join(SHOTS, "projects-phone.png"), fullPage: true });
});

test("a project card opens the scope page as having come from Projects, and back returns there", async ({ page }) => {
  await signIn(page, "buyer");
  await openProjects(page);
  await page.getByTestId("projects-row").filter({ hasText: "Santa Fe courtyard stone" }).click();
  await expect(page.getByTestId("surface-title")).toHaveText("Santa Fe courtyard stone");
  await expect(page.getByTestId("back")).toHaveText("Projects");
  await page.getByTestId("back").click();
  await expect(page.getByTestId("surface-title")).toHaveText("Projects");
  await expect(page.getByTestId("projects-row")).toHaveCount(4);
});
