// The REAL desk app bundles, hosted: runs only when WS_DIST names an
// assembled dist (bash tauri/hh-work/assemble-dist.sh, then
// WS_DIST=<that dist> npx playwright test). For two roles it opens every app
// of the role's switcher and checks the app's own bundle loaded inside the
// content frame, took the handoff session (it did not bounce the host to the
// picker) and registered its root element. The apps have no core behind them
// in this run, so what they show past boot is their own offline or error
// state; that is not what this leg judges.
import { expect, test } from "./fixtures";
import type { Frame, Page } from "@playwright/test";

function pathOf(url: string): string {
  try {
    return new URL(url).pathname;
  } catch {
    return "";
  }
}

async function frameAt(page: Page, path: string): Promise<Frame> {
  for (let tries = 0; tries < 60; tries += 1) {
    const found = page.frames().find((f) => pathOf(f.url()) === path);
    if (found !== undefined) return found;
    await page.waitForTimeout(100);
  }
  throw new Error(`no frame at ${path}`);
}

const assembled = (process.env["WS_DIST"] ?? "") !== "";

test.skip(!assembled, "WS_DIST names an assembled dist for this leg");

async function signInAs(page: Page, shellUrl: string, label: string): Promise<void> {
  await page.goto(`${shellUrl}/`);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: label, exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
}

for (const [label, role] of [
  ["Sales", "inside_sales"],
  ["Sales manager", "sales_manager"],
] as const) {
  test(`${label}: every app of the switcher loads its real bundle in the content frame`, async ({ page, shellUrl }) => {
    await signInAs(page, shellUrl, label);
    await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-role", role);
    await page.locator("hh-shell-header button.selector").click();
    const ids = await page.locator("hh-shell-header .panel .mi").evaluateAll((rows) => rows.map((r) => r.getAttribute("data-app") ?? ""));
    await page.keyboard.press("Escape");
    expect(ids.length).toBeGreaterThan(1);
    for (const id of ids) {
      await page.locator("hh-shell-header button.selector").click();
      await page.locator(`hh-shell-header .panel .mi[data-app="${id}"]`).click();
      const shell = page.getByTestId("ws-shell");
      await expect(shell).toHaveAttribute("data-current-app", id);
      const path: string = await page.evaluate((app) => {
        const host = document.querySelector("ws-host")?.shadowRoot;
        void app;
        return host?.querySelector<HTMLIFrameElement>('iframe[data-testid="app-frame"]')?.getAttribute("src") ?? "";
      }, id);
      // the frame's address carries the framed flag (item 8d); the page is judged by its path
      const frame = await frameAt(page, path.split("?")[0] ?? path);
      await expect
        .poll(async () =>
          frame.evaluate(() => {
            const root = document.body?.querySelector(":scope > *:not(script)");
            const tag = root?.tagName.toLowerCase() ?? "";
            return tag.includes("-") && customElements.get(tag) !== undefined ? tag : "";
          }),
        )
        .not.toBe("");
      // the app took the handoff and stayed: the host did not bounce to the picker
      await expect(shell).toHaveAttribute("data-phase", "desk");
    }
  });
}
