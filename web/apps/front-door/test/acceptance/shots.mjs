// The front door's shot walk: one screenshot per state the D2 brief names,
// taken against the local harness (the stub and the dev server of
// test/acceptance.config.ts) in the loaded state, light theme. The four
// deliverable shots land in the lane's shots folder for the D4 walk:
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

const browser = await chromium.launch({ channel: "chrome", headless: true });
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

/** The pointer rests in the corner for a shot: no hover state in the deliverable. */
async function settle() {
  await page.mouse.move(0, 0);
  await page.waitForTimeout(300);
}

// The unauth picker: the heading, the intro, the two live cards, the Coming
// list. Full page, loaded state.
await page.goto(`${base}/`);
await page.getByRole("button", { name: "Sales", exact: true }).waitFor();
await settle();
await page.screenshot({ path: join(outDir, "front-door-01-picker.png"), fullPage: true });

// The Coming list rendered: the ruled card alone.
await page.locator(".coming-card").screenshot({ path: join(outDir, "front-door-04-coming.png") });

// Sales signed in: the role home with the two tiles.
await page.getByRole("button", { name: "Sales", exact: true }).click();
await page.getByRole("button", { name: "Counter Till", exact: true }).waitFor();
await settle();
await page.screenshot({ path: join(outDir, "front-door-02-sales-home.png") });

// Switch role, then the manager's home with the five tiles.
await page.getByRole("button", { name: "Switch role", exact: true }).click();
await page.getByRole("button", { name: "Sales manager", exact: true }).waitFor();
await page.getByRole("button", { name: "Sales manager", exact: true }).click();
await page.getByRole("button", { name: "Manager Home", exact: true }).waitFor();
await settle();
await page.screenshot({ path: join(outDir, "front-door-03-sales-manager-home.png") });

// QA beside the deliverables: the coming-soon page, the picker at phone
// width, and a live card's hover state.
await page.getByRole("button", { name: "Dealer Admin", exact: true }).click();
await page.getByRole("button", { name: "Back to Sales manager" }).waitFor();
await page.waitForTimeout(200);
await page.screenshot({ path: join(outDir, "qa-coming-soon.png") });
await page.getByRole("button", { name: "Sign out", exact: true }).click();
await page.getByRole("button", { name: "Sales", exact: true }).waitFor();
await page.setViewportSize({ width: 390, height: 844 });
await page.waitForTimeout(300);
await page.screenshot({ path: join(outDir, "qa-picker-phone.png"), fullPage: true });
await page.setViewportSize({ width: 1280, height: 900 });
await page.waitForTimeout(200);
await page.getByRole("button", { name: "Sales manager", exact: true }).hover();
await page.waitForTimeout(200);
await page.screenshot({
  path: join(outDir, "qa-card-hover.png"),
  clip: { x: 160, y: 90, width: 960, height: 360 },
});

await browser.close();
process.stdout.write(`shots in ${outDir}\n`);
