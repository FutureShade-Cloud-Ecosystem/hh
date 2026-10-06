// The badge swap (decision log rows 953 and 968; Colton's "when does the new
// logo reach the demo"): the shared header's brand mark takes the measured
// Hardscape House H in place of the plain "HH" text square, and the Quote
// Desk serves the badge favicon set the other three apps already carry.
//
// The mark is mark.svg's geometry inlined in header.ts's template, one path
// with fill currentColor, so the colour is the sheet's own ink token per
// theme: the badge yellow on the dark page (the on navy colourway's H) and
// the token's light half on the light page. The gold square ground and its
// near-black square ink go with the text: the mark is the H alone, the
// badge's own colourway per theme, never a redrawn square.
//
// The favicon set is generated from badge-on-yellow.svg (never hand drawn)
// and ships in this app's public/favicons exactly as the front door, the
// Counter Till and the public desk ship theirs, with root absolute hrefs so
// Vite carries the app's base into the built links.
import { expect, test, type Page } from "@playwright/test";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function signIn(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

/** The brand mark's own element inside the shared header's shadow tree. */
async function markBox(page: Page) {
  return page.evaluate(() => {
    const appRoot = document.querySelector("qd-app-root");
    const header =
      appRoot !== null && appRoot.shadowRoot !== null ? appRoot.shadowRoot.querySelector("hos-app-header") : null;
    const root = header !== null && header.shadowRoot !== null ? header.shadowRoot : null;
    const mark = root !== null ? root.querySelector(".brand-mark") : null;
    if (mark === null) return { present: false } as const;
    const brand = root !== null ? root.querySelector(".brand") : null;
    // The mark's inline vector, named to keep the gate's reserved tag names
    // (html, svg) out of this file's identifiers.
    const markVector = mark.querySelector("svg");
    const path = mark.querySelector("svg path");
    const styles = getComputedStyle(mark);
    return {
      present: true,
      text: mark.textContent ?? "",
      svgPresent: markVector !== null,
      viewBox: markVector !== null ? markVector.getAttribute("viewBox") : null,
      pathPresent: path !== null,
      pathFill: path !== null ? path.getAttribute("fill") : null,
      pathD: path !== null ? (path.getAttribute("d") ?? "").slice(0, 24) : null,
      fillComputed: path !== null ? getComputedStyle(path).fill : null,
      markColour: styles.color,
      markBackground: styles.backgroundColor,
      lockupRole: root !== null ? root.querySelector(".brand")?.getAttribute("role") : null,
      lockupLabel: brand !== null ? brand.getAttribute("aria-label") : null,
      width: mark.getBoundingClientRect().width,
      height: mark.getBoundingClientRect().height,
      gap: brand !== null ? getComputedStyle(brand).columnGap : null,
    };
  });
}

// Item B: the chrome's mark is the inline badge SVG, not the plain text.
test("the shared header's brand mark is the inline mark.svg H with fill currentColor, no plain HH text", async ({
  page,
}) => {
  await signIn(page);
  const mark = await markBox(page);
  expect(mark.present).toBe(true);
  // The measured H, inlined: one svg at the badge's own viewBox, one path
  // carrying currentColor, and the geometry table's own first move.
  expect(mark.svgPresent).toBe(true);
  expect(mark.viewBox).toBe("0 0 3000 3000");
  expect(mark.pathPresent).toBe(true);
  expect(mark.pathFill).toBe("currentColor");
  expect((mark.pathD ?? "").trim().startsWith("M644.94 465.17 H1386.06")).toBe(true);
  // The regression this round removes: the square's plain text letter pair.
  expect(mark.text).not.toContain("HH");
  // The ground is the front door's badge square: the badge yellow, square
  // cornered, carrying the H in the near black ink.
  expect(mark.markBackground).toBe("rgb(255, 195, 19)");
  expect(mark.markColour).toBe("rgb(11, 23, 37)");
  // The lockup's own contract is unchanged: one image, the house name, and
  // the front door's geometry (the 30px mark, the 10px lockup gap).
  expect(mark.lockupRole).toBe("img");
  expect(mark.lockupLabel).toBe("HH Yard, a Hardscape House product");
  expect(mark.width).toBe(30);
  expect(mark.height).toBe(30);
  expect(mark.gap).toBe("10px");
});

// Item C: the landing carries the badge favicon set, the same five links the
// other three apps carry, and the files themselves are served.
test("the Quote Desk's landing carries the badge favicon links and serves the set", async ({ page, request }) => {
  await page.goto("/");
  const links = await page.evaluate(() =>
    [...document.querySelectorAll("head link[rel='icon'], head link[rel='apple-touch-icon']")].map((link) => ({
      rel: link.getAttribute("rel"),
      type: link.getAttribute("type"),
      sizes: link.getAttribute("sizes"),
      href: link.getAttribute("href"),
    })),
  );
  expect(links).toEqual([
    { rel: "icon", type: "image/svg+xml", sizes: null, href: "/favicons/favicon.svg" },
    { rel: "icon", type: "image/x-icon", sizes: null, href: "/favicons/favicon.ico" },
    { rel: "icon", type: "image/png", sizes: "192x192", href: "/favicons/favicon-192.png" },
    { rel: "icon", type: "image/png", sizes: "512x512", href: "/favicons/favicon-512.png" },
    { rel: "apple-touch-icon", type: null, sizes: null, href: "/favicons/apple-touch-icon.png" },
  ]);
  // The set is served from this app's own public folder, every file of it.
  for (const file of ["favicon.svg", "favicon.ico", "favicon-192.png", "favicon-512.png", "apple-touch-icon.png"]) {
    const response = await request.get(`/favicons/${file}`);
    expect(response.status(), file).toBe(200);
  }
});

// Item D: the badge is the same yellow square carrying the near black H in
// both themes, as the front door's lockup draws it.
test("the mark is the near black H on the badge yellow square under both themes", async ({ page }) => {
  await signIn(page);
  for (const theme of ["dark", "light"]) {
    await page.evaluate((t) => {
      document.documentElement.setAttribute("data-theme", t);
    }, theme);
    const box = await markBox(page);
    expect(box.pathFill, theme).toBe("currentColor");
    expect(box.fillComputed, theme).toBe("rgb(11, 23, 37)");
    expect(box.markColour, theme).toBe("rgb(11, 23, 37)");
    expect(box.markBackground, theme).toBe("rgb(255, 195, 19)");
  }
});
