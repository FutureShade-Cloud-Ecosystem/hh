// The drawn category tiles, rendered (demo build): a seeded product with no picture shows its
// category's tile as the img in the hot list and in a search result, and the tile follows the
// document theme (Light is data-theme="light", Dark is the absence of it). The stub's products
// carry no image (has_image false), the same state as a demo product with no photograph. The
// default build's side of this (the glyph, no img) is test/default-build/glyph.pw.ts.
import { expect, test, type Page } from "@playwright/test";

async function signInToItems(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("pos-page-title")).toHaveText("New Counter Sale");
  const head = page.getByTestId("step-head-items");
  if ((await head.getAttribute("aria-expanded")) !== "true") await head.click();
  await expect(head).toHaveAttribute("aria-expanded", "true");
}

async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.evaluate((t) => {
    if (t === "light") document.documentElement.setAttribute("data-theme", "light");
    else document.documentElement.removeAttribute("data-theme");
  }, theme);
}

test("the hot list tiles show each group's category tile, in the document theme", async ({ page }) => {
  await signInToItems(page);
  const groups = page.getByTestId("hot-group");
  await expect(groups).toHaveCount(3);
  for (const theme of ["light", "dark"] as const) {
    await setTheme(page, theme);
    await expect(groups.filter({ hasText: "Pavers" }).locator("img")).toHaveAttribute("src", /\.svg$|^data:image\/svg/);
    await expect(groups.locator("img")).toHaveCount(3);
  }
  // A data URI carries the drawing itself: the pavers tile's own path text tells light from dark.
  await setTheme(page, "light");
  const light = await groups.filter({ hasText: "Pavers" }).locator("img").getAttribute("src");
  await setTheme(page, "dark");
  await expect(groups.filter({ hasText: "Pavers" }).locator("img")).not.toHaveAttribute("src", light ?? "");
});

test("a hot list item and a search result show their category tile and keep their alt text", async ({ page }) => {
  await signInToItems(page);
  await page.getByTestId("hot-group").filter({ hasText: "Pavers" }).click();
  await expect(page.getByTestId("hot-item").locator("img")).toHaveAttribute("src", /pavers|data:image\/svg/);

  await page.getByTestId("pos-scan-input").fill("holland");
  const results = page.getByTestId("drop-row");
  await expect(results.first()).toBeVisible();
  const thumb = results.first().locator("hos-product-thumb img");
  await expect(thumb).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);
  // The pavers tile's own drawing (its aria-label rides the data URI), not a request to the image route.
  expect(await thumb.getAttribute("src")).not.toContain("/api/v1/products/");
});

test("the cart line of an added product shows its category tile", async ({ page }) => {
  await signInToItems(page);
  await page.getByTestId("pos-scan-input").fill("holland");
  await page.getByTestId("drop-row").filter({ hasText: "Holland Stone 60 mm" }).first().click();
  const line = page.getByTestId("pos-line-row").filter({ hasText: "Holland Stone 60 mm" });
  await expect(line).toHaveCount(1);
  const img = line.locator("hos-product-thumb img");
  await expect(img).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);
  await expect(img).toHaveAttribute("alt", "Holland Stone 60 mm");
});

// Round 7: the demo seed gives each mulch colour a real 1x1 swatch PNG, which the thumb scaled to a
// flat dark square that read as a missing picture. A picture that is only a pixel or two is a swatch,
// not a photograph: the thumb falls through to the category's drawn tile, texture and all.
const ONE_PIXEL_PNG = Buffer.from(
  "89504e470d0a1a0a0000000d4948445200000001000000010802000000907753de0000000c4944415478da6330d6d706000126008e67d88cc60000000049454e44ae426082",
  "hex",
);


/** The demo dealer's hot list, answered as a dealer whose products all carry a stored picture. */
async function everyPickHasAPicture(page: Page): Promise<void> {
  await page.route("**/api/v1/pos/quick-picks**", async (route) => {
    const real = await route.fetch();
    const body = (await real.json()) as { picks?: Array<Record<string, unknown>>; items?: Array<Record<string, unknown>> };
    const rows = body.picks ?? body.items ?? [];
    for (const row of rows) row["has_image"] = true;
    await route.fulfill({ response: real, json: body });
  });
}

test("a one pixel swatch picture falls through to the category's drawn tile", async ({ page }) => {
  await page.route("**/api/v1/products/*/image", (route) =>
    route.fulfill({ status: 200, contentType: "image/png", body: ONE_PIXEL_PNG }),
  );
  await everyPickHasAPicture(page);
  await signInToItems(page);
  await page.getByTestId("hot-group").filter({ hasText: "Pavers" }).click();
  const img = page.getByTestId("hot-item").locator("img").first();
  await expect(img).toHaveAttribute("src", /^data:image\/svg\+xml|pavers/);
  expect(await img.getAttribute("src")).not.toContain("/api/v1/products/");
});

test("a real sized picture still wins over the tile", async ({ page }) => {
  // A 8x8 PNG is a picture, not a swatch: built here so the test needs no file.
  const { deflateSync } = await import("node:zlib");
  const size = 8;
  const row = Buffer.concat([Buffer.from([0]), Buffer.alloc(size * 3, 0x80)]);
  const raw = Buffer.concat(Array.from({ length: size }, () => row));
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k += 1) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf: Buffer): number => {
    let c = 0xffffffff;
    for (const byte of buf) c = (crcTable[(c ^ byte) & 0xff] as number) ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type: string, data: Buffer): Buffer => {
    const body = Buffer.concat([Buffer.from(type, "ascii"), data]);
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(body));
    return Buffer.concat([length, body, sum]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size, 0);
  header.writeUInt32BE(size, 4);
  header[8] = 8;
  header[9] = 2;
  const png = Buffer.concat([
    Buffer.from("89504e470d0a1a0a", "hex"),
    chunk("IHDR", header),
    chunk("IDAT", deflateSync(raw)),
    chunk("IEND", Buffer.alloc(0)),
  ]);
  await page.route("**/api/v1/products/*/image", (route) =>
    route.fulfill({ status: 200, contentType: "image/png", body: png }),
  );
  await everyPickHasAPicture(page);
  await signInToItems(page);
  await page.getByTestId("hot-group").filter({ hasText: "Pavers" }).click();
  const img = page.getByTestId("hot-item").locator("img").first();
  await expect(img).toHaveAttribute("src", /\/api\/v1\/products\/.+\/image/);
});
