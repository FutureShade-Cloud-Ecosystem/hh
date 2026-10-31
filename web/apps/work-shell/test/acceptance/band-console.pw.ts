// UX-121b: the Work shell's band on a dealer's own box. A shell page served a console /config.json names the
// dealer from dealer.name and wears no DEMO chip; a page served no console config stays DEMO (item 370). The
// config is served to the top window only, so the front door frame under it keeps its demo picker.
import { expect, test, useConsole } from "./fixtures";

const CONSOLE_CONFIG = {
  mode: "console",
  org: "acme-stone",
  consoleEndpoint: "https://console.invalid/v1",
  consoleProjectId: "acme-project",
  coreBaseUrl: "",
  personas: false,
  personasOpen: false,
  dealer: { name: "Acme Stone" },
};

test("a console box names its dealer in the shell band and wears no DEMO chip", async ({ page, shellUrl }) => {
  await page.route("**/config.json", (route) =>
    route.request().frame() === page.mainFrame() ? route.fulfill({ json: CONSOLE_CONFIG }) : route.continue(),
  );
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto(`${shellUrl}/`);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  const band = page.locator("hh-shell-header");
  await expect(band).toContainText("Acme Stone");
  await expect(band.getByText(/^demo$/i)).toHaveCount(0);
});

test("a shell served no console config stays DEMO", async ({ page, shellUrl }) => {
  await page.goto(`${shellUrl}/`);
  await page.frameLocator('[data-testid="door-frame"]').getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  const band = page.locator("hh-shell-header");
  await expect(band.getByText(/^demo$/i)).toHaveCount(1);
  await expect(band).not.toContainText("Acme Stone");
});

// UX-127: every frame is served the console config too (the door frame and a framed app). The shell band names
// the dealer with no DEMO, a framed app wears no second band, and its page copy has no "Demo data only". The
// copy check bites on the real desk bundles (WS_DIST names an assembled dist); the composed fixture pages have none.
test("a console box serves its config to every frame: dealer band, no second band, no demo-data words", async ({ page, shellUrl }) => {
  await useConsole(page, shellUrl);
  await page.route("**/config.json", (route) => route.fulfill({ json: CONSOLE_CONFIG }));
  await page.route("https://console.invalid/**", (route) => route.abort());
  await page.goto(`${shellUrl}/`);
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-phase", "desk");
  const band = page.locator("hh-shell-header");
  await expect(band).toHaveCount(1);
  await expect(band).toContainText("Acme Stone");
  await expect(band.getByText(/^demo$/i)).toHaveCount(0);
  await page.locator("hh-shell-header button.selector").click();
  await page.locator('hh-shell-header .panel .mi[data-app="buying-and-stock"]').click();
  await expect(page.getByTestId("ws-shell")).toHaveAttribute("data-current-app", "buying-and-stock");
  const frames = page.frames().filter((f) => f !== page.mainFrame());
  expect(frames.length).toBeGreaterThan(0);
  for (const frame of frames) {
    await expect(frame.locator("hh-shell-header")).toHaveCount(0);
    await expect(frame.locator("body")).not.toContainText("Demo data only");
  }
});
