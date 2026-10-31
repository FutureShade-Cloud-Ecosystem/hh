// UX-121b: the Work shell's band on a dealer's own box. A shell page served a console /config.json names the
// dealer from dealer.name and wears no DEMO chip; a page served no console config stays DEMO (item 370). The
// config is served to the top window only, so the front door frame under it keeps its demo picker.
import { expect, test } from "./fixtures";

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
