// The front door's shot walk, the polish round's set (HH-DEMO-1 D2 polish)
// plus the Yard Team card's live leg: the five deliverable states, retaken
// at every viewport and in both themes against the local harness (the stub
// and the dev server of test/acceptance.config.ts) in the loaded state. The
// desktop light set keeps D2's own names; the variants prefix the viewport
// or the theme, so the lane's folder reads
//
//   front-door-01-picker.png            desktop, light
//   front-door-02-sales-home.png        desktop, light
//   front-door-03-sales-manager-home.png  desktop, light
//   front-door-04-coming.png            desktop, light, the opened disclosure
//   front-door-05-yard-home.png         desktop, light, the Yard Team's home
//   tablet-front-door-01..05.png        tablet 768, light
//   phone-front-door-01..05.png         phone 390, light
//   dark-front-door-01..05.png          desktop, dark
//   dark-tablet-front-door-01..05.png   tablet 768, dark
//   dark-phone-front-door-01..05.png    phone 390, dark
//
//   FD_APP_PORT=4926 node test/acceptance/shots.mjs <out-dir>
//
// The harness is up with:
//   FD_STUB_PORT=4927 node test/stub/demo-stub.mjs &
//   npx vite --port 4926 --strictPort --host 127.0.0.1 &
import { chromium } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const appPort = process.env.FD_APP_PORT ?? "4926";
const base = `http://127.0.0.1:${appPort}`;
const outDir = process.argv[2] ?? "/tmp/fd-shots";
mkdirSync(outDir, { recursive: true });

const VIEWPORTS = [
  { name: "", width: 1280, height: 900 },
  { name: "tablet-", width: 768, height: 1024 },
  { name: "phone-", width: 390, height: 844 },
];

const browser = await chromium.launch({ channel: "chrome", headless: true });

/** The pointer rests in the corner for a shot: no hover state in the deliverable. */
async function settle(page) {
  await page.mouse.move(0, 0);
  await page.waitForTimeout(300);
}

/**
 * One viewport's or one theme's four deliverable states. The theme rides the
 * document root's data-theme attribute (the token sheet's own contract): the
 * app comes up light, dark is the sheet's default half, reached by removing
 * the attribute.
 */
async function walk(prefix, viewport, theme) {
  const page = await browser.newPage({ viewport: { width: viewport.width, height: viewport.height } });
  await page.goto(`${base}/`);
  await page.getByRole("button", { name: "Sales", exact: true }).waitFor();
  if (theme === "dark") {
    await page.evaluate(() => document.documentElement.removeAttribute("data-theme"));
  }
  await settle(page);
  await page.screenshot({ path: join(outDir, `${prefix}front-door-01-picker.png`), fullPage: true });

  // The opened disclosure: the coming list's content, today's rows and
  // their muted chips.
  await page.getByTestId("coming-disclosure").getByText("Six more roles are coming").click();
  await page.getByRole("list", { name: "Coming roles" }).waitFor();
  await settle(page);
  await page.locator(".coming-card").screenshot({ path: join(outDir, `${prefix}front-door-04-coming.png`) });

  // Sales signed in: the role home with the two live tiles.
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await page.getByRole("button", { name: "Counter Till", exact: true }).waitFor();
  await settle(page);
  await page.screenshot({ path: join(outDir, `${prefix}front-door-02-sales-home.png`) });

  // Switch role, then the manager's home with the live tiles and the
  // coming-next group.
  await page.getByRole("button", { name: "Switch role", exact: true }).click();
  await page.getByRole("button", { name: "Sales manager", exact: true }).waitFor();
  await page.getByRole("button", { name: "Sales manager", exact: true }).click();
  await page.getByRole("button", { name: "Manager Home", exact: true }).waitFor();
  await settle(page);
  await page.screenshot({ path: join(outDir, `${prefix}front-door-03-sales-manager-home.png`) });

  // Switch role again, then the Yard Team's home: the live Yard Pick tile
  // and House Messages under the coming-next word.
  await page.getByRole("button", { name: "Switch role", exact: true }).click();
  await page.getByRole("button", { name: "Yard Team", exact: true }).waitFor();
  await page.getByRole("button", { name: "Yard Team", exact: true }).click();
  await page.getByRole("button", { name: "Yard Pick and Receive", exact: true }).waitFor();
  await settle(page);
  await page.screenshot({ path: join(outDir, `${prefix}front-door-05-yard-home.png`) });
  await page.close();
}

for (const viewport of VIEWPORTS) {
  await walk(viewport.name, viewport, "light");
}
for (const viewport of VIEWPORTS) {
  await walk(`dark-${viewport.name}`, viewport, "dark");
}

// QA beside the deliverables: a live card's hover state, the accent the
// polish round asks the cards to carry.
{
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  await page.goto(`${base}/`);
  await page.getByRole("button", { name: "Sales", exact: true }).waitFor();
  await page.getByRole("button", { name: "Sales manager", exact: true }).hover();
  await page.waitForTimeout(200);
  await page.screenshot({
    path: join(outDir, "qa-card-hover.png"),
    clip: { x: 140, y: 80, width: 1000, height: 380 },
  });
  await page.close();
}

await browser.close();
process.stdout.write(`shots in ${outDir}\n`);
