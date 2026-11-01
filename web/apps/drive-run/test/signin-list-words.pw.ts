// UX-139: the sign-in list says check your signal only when nothing answered.
// A 503 on the demo flows read reads as a server problem; an aborted read keeps the signal line.
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { expect, resetStub, test } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

const SHOT_DIR = process.env.UX139_SHOT_DIR ?? "";
const SERVER = "The sign in list could not load. The server had a problem. Try again in a moment.";
const SIGNAL = "The sign in list could not load. Check your signal and try again.";

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

for (const theme of ["light", "charcoal"] as const) {
  for (const [name, text] of [["server", SERVER], ["signal", SIGNAL]] as const) {
    test(`the sign-in list ${name} line (${theme})`, async ({ page }) => {
      await page.route(/\/demo\/flows$/, (route) =>
        name === "server"
          ? route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: "unavailable" }) })
          : route.abort(),
      );
      await page.goto("/");
      await expect(page.getByRole("alert")).toHaveText(text);
      if (name === "server") await expect(page.locator("body")).not.toContainText("Check your signal");
      await page.evaluate((t) => {
        if (t === "light") document.documentElement.setAttribute("data-theme", "light");
        else document.documentElement.removeAttribute("data-theme");
      }, theme);
      if (SHOT_DIR !== "") {
        mkdirSync(SHOT_DIR, { recursive: true });
        await page.screenshot({ path: join(SHOT_DIR, `${name}-line-390-${theme}.png`) });
      }
    });
  }
}
