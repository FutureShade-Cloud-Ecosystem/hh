// Round 13: the Send quote button's key hint never wraps inside the button on a phone, and the disabled Print and Copy
// link keep their text readable in brand mode (3:1 for a disabled control's text against its own fill, the design
// system's disabled pair, never a fade of the live colours).
import { expect, test, type Locator, type Page } from "@playwright/test";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function brand(page: Page): Promise<void> {
  const control = page.getByTestId("theme-control");
  for (let i = 0; i < 3; i += 1) {
    if ((await control.textContent())?.includes("Brand")) return;
    await control.click();
  }
  await expect(control).toContainText("Brand");
}

async function open(page: Page, quoteNo: string): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
  await page.getByTestId("qb-list-row").filter({ hasText: quoteNo }).first().click();
  await expect(page.getByTestId("qb-context")).toBeVisible();
}

/**
 * The ratio of the control's drawn text to its drawn fill, read from its own pixels: the fill is the commonest colour in
 * the button, the ink the colour farthest from it in lightness. What is measured is what the person sees, with any fade.
 */
async function ratio(page: Page, control: Locator): Promise<number> {
  const png = (await control.screenshot()).toString("base64");
  return page.evaluate(async (data) => {
    const blob = await (await fetch(`data:image/png;base64,${data}`)).blob();
    const bitmap = await createImageBitmap(blob);
    const canvas = document.createElement("canvas");
    canvas.width = bitmap.width;
    canvas.height = bitmap.height;
    const ctx = canvas.getContext("2d")!;
    ctx.drawImage(bitmap, 0, 0);
    const px = ctx.getImageData(2, 2, bitmap.width - 4, bitmap.height - 4).data;
    const lum = (r: number, g: number, b: number): number => {
      const f = (v: number): number => (v / 255 <= 0.03928 ? v / 255 / 12.92 : ((v / 255 + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const counts = new Map<number, number>();
    for (let i = 0; i < px.length; i += 4) {
      const key = (px[i]! << 16) | (px[i + 1]! << 8) | px[i + 2]!;
      counts.set(key, (counts.get(key) ?? 0) + 1);
    }
    let fill = 0;
    let most = 0;
    for (const [key, n] of counts) if (n > most) [fill, most] = [key, n];
    const fl = lum((fill >> 16) & 255, (fill >> 8) & 255, fill & 255);
    let far = fl;
    for (const key of counts.keys()) {
      const l = lum((key >> 16) & 255, (key >> 8) & 255, key & 255);
      if (Math.abs(l - fl) > Math.abs(far - fl)) far = l;
    }
    return (Math.max(fl, far) + 0.05) / (Math.min(fl, far) + 0.05);
  }, png);
}

test("a disabled Print, Copy link and Send quote read as the design system's disabled pair in brand mode", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await open(page, "Q-1003");
  await brand(page);
  for (const id of ["qb-print", "qb-copy-link", "qb-send"]) {
    const control = page.getByTestId(id);
    await expect(control).toBeDisabled();
    const got = await ratio(page, control);
    // The design system's disabled pair is the bar (it clears AA in both halves), well over the 3:1 a disabled control must meet.
    expect(got, `${id} disabled text against its fill`).toBeGreaterThanOrEqual(4.5);
  }
});

test("at 390 the key hint stays on one line or is not shown, and never wraps inside the Send quote button", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page, "Q-1001");
  const send = page.getByTestId("qb-send");
  await expect(send).toBeVisible();
  const lines = await send.evaluate((button) => {
    const key = button.querySelector(".key");
    if (key === null || getComputedStyle(key).display === "none") return 0;
    const range = document.createRange();
    range.selectNodeContents(key);
    return new Set(Array.from(range.getClientRects()).map((r) => Math.round(r.top))).size;
  });
  expect(lines).toBeLessThanOrEqual(1);
  const label = await send.evaluate((button) => {
    const range = document.createRange();
    range.selectNodeContents(button);
    return new Set(Array.from(range.getClientRects()).map((r) => Math.round(r.top))).size;
  });
  expect(label, "the button's whole face is one line").toBe(1);
});
