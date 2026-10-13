// HH Drive, the review fixes: the header says HH Drive and is compact on a
// phone, the stop's two doors are drawn as the board draws them, a thumbnail
// shows each photo taken, and the confirmation box and the grey sign control
// can be found in brand. Measured in the browser, on the stub.
import type { Page } from "@playwright/test";
import { test, expect, TINY_PNG, driverHandoff, resetStub, takeFirstStopEnRoute } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

/** The resolved colour of a CSS value, as [r, g, b, a], through a probe in the page. */
async function colour(page: Page, css: string): Promise<number[]> {
  return page.evaluate((value) => {
    const probe = document.createElement("div");
    probe.style.color = value;
    document.body.appendChild(probe);
    const resolved = getComputedStyle(probe).color;
    probe.remove();
    return (resolved.match(/[\d.]+/g) ?? []).map(Number);
  }, css);
}

function luminance([r = 0, g = 0, b = 0]: number[]): number {
  const lin = (v: number) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

function contrast(a: number[], b: number[]): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

async function elementColours(page: Page, selector: string): Promise<{ bg: number[]; border: number[]; borderWidth: number }> {
  return page.evaluate((sel) => {
    const find = (root: ParentNode): Element | null => {
      const direct = root.querySelector(sel);
      if (direct) return direct;
      for (const el of Array.from(root.querySelectorAll("*"))) {
        const inner = (el as HTMLElement).shadowRoot ? find((el as HTMLElement).shadowRoot as ShadowRoot) : null;
        if (inner) return inner;
      }
      return null;
    };
    const el = find(document) as HTMLElement;
    const style = getComputedStyle(el);
    const nums = (v: string) => (v.match(/[\d.]+/g) ?? []).map(Number);
    return { bg: nums(style.backgroundColor), border: nums(style.borderTopColor), borderWidth: parseFloat(style.borderTopWidth) };
  }, selector);
}

for (const theme of ["light", "brand"] as const) {
  test(`the stop's two doors are drawn as the board draws them (${theme})`, async ({ page }) => {
    await takeFirstStopEnRoute(page);
    if (theme === "brand") await page.getByRole("button", { name: "Brand theme" }).click();
    await page.mouse.move(0, 0);
    const delivered = page.getByRole("button", { name: "Delivered", exact: true });
    const fail = page.getByRole("button", { name: "Could not deliver" });
    const fill = await colour(page, "var(--brand-fill)");
    const danger = await colour(page, "var(--hh-danger)");
    const d = await delivered.evaluate((el) => getComputedStyle(el).backgroundColor.match(/[\d.]+/g)?.map(Number));
    expect((d ?? []).slice(0, 3)).toEqual(fill.slice(0, 3));
    const f = await fail.evaluate((el) => {
      const s = getComputedStyle(el);
      return { bg: s.backgroundColor, border: s.borderTopColor, width: parseFloat(s.borderTopWidth), ink: s.color };
    });
    expect(f.bg === "rgba(0, 0, 0, 0)" || f.bg === "transparent").toBe(true);
    expect(f.width).toBeGreaterThanOrEqual(2);
    expect((f.border.match(/[\d.]+/g) ?? []).map(Number).slice(0, 3)).toEqual(danger.slice(0, 3));
    expect((f.ink.match(/[\d.]+/g) ?? []).map(Number).slice(0, 3)).toEqual(danger.slice(0, 3));
    expect((await fail.boundingBox())!.height).toBeGreaterThanOrEqual(56);
  });
}

test("the header says HH Drive on every screen and not HH Yard", async ({ page, browser }) => {
  // The way in, with no handoff at all.
  const bare = await browser.newPage({ viewport: { width: 390, height: 844 } });
  await bare.goto(new URL("/", page.url() === "about:blank" ? test.info().project.use.baseURL! : page.url()).href);
  await expect(bare.locator("[data-flow]")).toHaveCount(1);
  await expect(bare.locator("hos-app-header").getByRole("img")).toHaveAttribute("aria-label", "HH Drive, HH Drive · HH Field shell");
  await expect(bare.locator("hos-app-header")).not.toContainText("Yard");
  await bare.close();
  const lockup = page.locator("hos-app-header").getByRole("img");
  await takeFirstStopEnRoute(page);
  await expect(lockup).toHaveAttribute("aria-label", "HH Drive, HH Drive · HH Field shell");
  await expect(page.locator("hos-app-header")).toContainText("HH Drive");
  await expect(page.locator("hos-app-header")).not.toContainText("Yard");
});

test("at 390 the header is compact and every control in it is at least 44 px tall", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "My run" })).toBeVisible();
  const header = await page.locator("hos-app-header").boundingBox();
  expect(header!.height).toBeLessThanOrEqual(170);
  const buttons = page.locator("hos-app-header button");
  const count = await buttons.count();
  expect(count).toBe(3);
  for (let i = 0; i < count; i += 1) expect((await buttons.nth(i).boundingBox())!.height).toBeGreaterThanOrEqual(44);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow).toBeLessThanOrEqual(0);
});

test("each photo taken shows as a thumbnail beside the add tile", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.locator("input[type=file]").setInputFiles({ name: "a.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.locator("[data-photo-sent]")).toHaveCount(1);
  const thumb = page.locator("img[data-thumb]").first();
  await expect(thumb).toBeVisible();
  expect(await thumb.evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0);
  await page.locator("input[type=file]").setInputFiles({ name: "b.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.locator("img[data-thumb]")).toHaveCount(2);
});

test("a second photo picked while the first is still going up is kept, never dropped", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  // Both pictures arrive in the same breath, as two quick taps from a driver would.
  await page.locator("input[type=file]").evaluate(async (input: HTMLInputElement, bytes: number[]) => {
    for (const name of ["a.png", "b.png"]) {
      const transfer = new DataTransfer();
      transfer.items.add(new File([new Uint8Array(bytes)], name, { type: "image/png" }));
      input.files = transfer.files;
      input.dispatchEvent(new Event("change", { bubbles: true, composed: false }));
    }
  }, Array.from(TINY_PNG));
  await expect(page.locator("img[data-thumb]")).toHaveCount(2);
  await expect(page.locator("[data-photo-sent]")).toHaveCount(2);
});

test("a photo picked on the proof screen and sent after the signature is attached to the proof", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(/\/photos(\?|$)/, async (route) => {
    await held;
    await route.continue();
  });
  const attached: string[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().includes("/attach-photos")) attached.push(request.url());
  });
  await page.locator("input[type=file]").setInputFiles({ name: "a.png", mimeType: "image/png", buffer: TINY_PNG });
  await page.getByLabel("Full name of the person receiving").fill("Receiving clerk");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
  expect(attached).toHaveLength(0);
  release();
  await expect(page.locator("img[data-thumb]")).toHaveCount(1);
  expect(attached).toHaveLength(1);
});

test("a throw while a photo is being made ready does not wedge the queue", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.evaluate(() => {
    const original = File.prototype.arrayBuffer;
    let thrown = false;
    File.prototype.arrayBuffer = function (this: File) {
      if (!thrown) {
        thrown = true;
        return Promise.reject(new Error("unreadable"));
      }
      return original.call(this);
    };
  });
  await page.locator("input[type=file]").setInputFiles({ name: "a.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.getByText("Add a photo")).toBeVisible();
  await page.locator("input[type=file]").setInputFiles({ name: "b.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.locator("img[data-thumb]")).toHaveCount(1);
});

test("in brand the unticked box and the grey sign control can be found at 3:1", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Brand theme" }).click();
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  const card = await colour(page, "var(--hh-card)");
  const box = await elementColours(page, ".confirm-box");
  expect(box.borderWidth).toBeGreaterThanOrEqual(2);
  expect(contrast(box.border, card)).toBeGreaterThanOrEqual(3);
  await page.getByLabel("Full name of the person receiving").fill("");
  const sign = await elementColours(page, "button[data-action=sign]");
  expect(sign.borderWidth).toBeGreaterThanOrEqual(2);
  expect(contrast(sign.border, card)).toBeGreaterThanOrEqual(3);
});
