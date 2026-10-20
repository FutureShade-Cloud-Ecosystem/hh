// The badge swap (decision log rows 953 and 968; Colton's "when does the new
// logo reach the demo"): the band variant carries the dealer lockup, not the
// HH mark, and the Quote Desk serves the badge favicon set the other three
// apps already carry.
//
// The favicon set is generated from badge-on-yellow.svg (never hand drawn)
// and ships in this app's public/favicons exactly as the front door, the
// Counter Till and the public desk ship theirs, with root absolute hrefs so
// Vite carries the app's base into the built links.
import { expect, test } from "@playwright/test";

// The stub keeps the person's layout row, which now remembers the open quote: every test starts from the seeded world.
test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

// Item C: the landing carries the badge favicon links and serves the set.
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
