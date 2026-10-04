// The page in a real browser, against the stubbed harness (the stub serves
// the public quote routes' contract shapes; the vite dev server proxies /api
// to it). The suite covers the item's four behaviours: a quote viewed by its
// token, an expired or revoked token's refusal state, the typed name
// acceptance (refused when blank, accepted when typed, the second visit shown
// as already accepted), and the no-credential rule held at runtime: every
// call the page sends arrives with no credential header, on a public route or
// the product image read alone, and no storage is touched.
//
// The stub's quote world is in memory and an acceptance flips its own quote,
// so the tests run in order and each reads only what it itself caused or
// names deliberately.
import { expect, test, type Page, type Response } from "@playwright/test";

const GOOD_TOKEN = "q-demo-25";

async function stubState(page: Page): Promise<{ sightings: Array<{ method: string; path: string; headerNames: string[] }> }> {
  const response = await page.request.get("/_stub/state");
  return (await response.json()) as { sightings: Array<{ method: string; path: string; headerNames: string[] }> };
}

function acceptCall(page: Page): Promise<Response> {
  return page.waitForResponse((response) => response.url().endsWith("/accept") && response.request().method() === "POST");
}

test("the lane's reference shots, against the stub, on demand", async ({ page }) => {
  test.skip(!process.env.PD_SHOT_DIR, "reference shots are taken on demand, outside the repo");
  const dir = process.env.PD_SHOT_DIR as string;

  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  // The thumbnails ride the image read and then decode; hold the shot until
  // every one has its pixels, the way the reference's product photos render.
  await page.waitForFunction(() => {
    const root = document.querySelector("pd-quote-public")?.shadowRoot;
    const thumbs = root?.querySelectorAll<HTMLImageElement>("img.thumb");
    return (
      thumbs !== undefined && thumbs.length === 3 && Array.from(thumbs).every((img) => img.complete && img.naturalWidth > 0)
    );
  });
  await page.screenshot({ path: `${dir}/share-quote.png` });

  await page.locator("#name").fill("Sample Customer");
  await page.locator("#affirm").check();
  await page.screenshot({ path: `${dir}/accept-state.png` });
});

test("a quote is viewed by its token: header, lines, totals", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  await expect(page.getByText("Prepared for Quinte Hardscapes Inc")).toBeVisible();
  await expect(page.getByText(/valid until /)).toBeVisible();

  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("Banas Natural Step");
  await expect(rows.nth(0)).toContainText("1.0000");
  await expect(rows.nth(0)).toContainText("PC");
  await expect(rows.nth(0)).toContainText("$20.57");
  await expect(rows.nth(1)).toContainText("Alliance Gator Polymeric Sand");
  await expect(rows.nth(1)).toContainText("BAG");
  await expect(rows.nth(1)).toContainText("$22.08");
  await expect(rows.nth(2)).toContainText("Black Mulch");
  await expect(rows.nth(2)).toContainText("CYD");
  await expect(rows.nth(2)).toContainText("$54.00");

  await expect(page.getByText("Subtotal $96.65")).toBeVisible();
  await expect(page.getByText("HST $12.57")).toBeVisible();
  await expect(page.getByText("Total $109.22")).toBeVisible();
});

test("the line thumbnails ride the product image read, when the dealer chose images", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  const thumbs = page.locator("img.thumb");
  await expect(thumbs).toHaveCount(3);
  // Each thumbnail's source is an object URL fed by the image read; the
  // ledger assertions below hold which route fed them.
  for (let index = 0; index < 3; index += 1) {
    await expect(thumbs.nth(index)).toHaveAttribute("width", "36");
  }
});

test("the accept area says why the button is not disabled on a blank form", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  // The ERP behaviour is kept: the button stays clickable and the refusal is
  // the red inline line, so the area must SAY that is deliberate, not look
  // like the lane's disabled-look rule was forgotten.
  await expect(page.getByText(/never greyed out/)).toBeVisible();
});

test("a dead link reads as a deliberate page: heading on the refusal, room above it", async ({ page }) => {
  await page.goto("/?t=q-revoked-410");
  await expect(page.getByRole("heading", { name: "Quote link unavailable" })).toBeVisible();

  await page.goto("/?t=q-never-minted");
  await expect(page.getByRole("heading", { name: "Quote link unavailable" })).toBeVisible();

  await page.goto("/?t=q-expired-25");
  await expect(page.getByRole("heading", { name: "Acceptance closed" })).toBeVisible();

  // The fatal card does not hug the viewport top: it clears the host's own
  // padding with a margin of its own, so the page reads deliberate, not broken.
  await page.goto("/?t=q-revoked-410");
  const top = await page.evaluate(() => {
    const card = document.querySelector("pd-quote-public")?.shadowRoot?.querySelector(".card");
    return card ? card.getBoundingClientRect().top : null;
  });
  expect(top).not.toBeNull();
  expect(top!, "the refusal card must sit clear of the viewport top").toBeGreaterThanOrEqual(48);
});

test("the page stands on the token's pale ground so the white cards lift off it", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  // The light half of --hh-page is #F5F7FA: the reference's grey-blue ground
  // under white cards. The page pinned light in index.html, so the body must
  // resolve exactly that token, not the default white canvas.
  const ground = await page.evaluate(() => ({
    body: getComputedStyle(document.body).backgroundColor,
    card: (() => {
      const card = document.querySelector("pd-quote-public")?.shadowRoot?.querySelector(".card");
      return card ? getComputedStyle(card).backgroundColor : null;
    })(),
  }));
  expect(ground.body).toBe("rgb(245, 247, 250)");
  expect(ground.card).toBe("rgb(255, 255, 255)");
});

test("the page's UI face is Outfit itself, self-hosted, not a system fallback", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  // document.fonts is the closest provable check, and load() is the honest
  // half of it: it answers with the faces it actually MATCHED, so an absent
  // @font-face (check()'s vacuous true) fails here as an empty answer. The
  // three weights are the ones the page renders: body, labels and button, title.
  const faces = await page.evaluate(async () => {
    const out: Record<string, boolean> = {};
    for (const weight of ["400", "600", "700"]) {
      const matched = await document.fonts.load(`${weight} 16px Outfit`);
      out[weight] = matched.length > 0 && matched.every((face) => face.status === "loaded");
    }
    await document.fonts.ready;
    return out;
  });
  expect(faces).toEqual({ 400: true, 600: true, 700: true });
});

test("the line table never overflows its card or the page, with thumbnails, at phone and tablet widths", async ({ page }) => {
  for (const width of [390, 820]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`/?t=${GOOD_TOKEN}`);
    await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
    // The thumbnail column is the one that broke the card on a phone, so the
    // hold is WITH the three thumbnails painted, not on the bare table.
    await expect(page.locator("img.thumb")).toHaveCount(3);

    const overflow = await page.evaluate(() => {
      const root = document.querySelector("pd-quote-public")?.shadowRoot;
      const card = root?.querySelector(".card");
      const table = root?.querySelector("table");
      if (!card || !table) return null;
      const cardBox = card.getBoundingClientRect();
      const tableBox = table.getBoundingClientRect();
      return {
        pageOverhang: document.documentElement.scrollWidth - window.innerWidth,
        cardOverhang: Math.ceil(tableBox.right - cardBox.right),
      };
    });
    expect(overflow, `the quote card and table must exist at ${width}px`).not.toBeNull();
    expect(overflow!.pageOverhang, `page body must not scroll sideways at ${width}px`).toBeLessThanOrEqual(0);
    expect(overflow!.cardOverhang, `table must not pass the card's right edge at ${width}px`).toBeLessThanOrEqual(0);
  }
});

test("an expired quote refuses acceptance in its own words", async ({ page }) => {
  await page.goto("/?t=q-expired-25");
  await expect(page.getByRole("heading", { name: "Quote #Q-24" })).toBeVisible();
  await expect(page.getByText(/This quote expired on .+ and can no longer be accepted online/)).toBeVisible();
  await expect(page.getByText("Accept this quote")).toHaveCount(0);
  await expect(page.locator("#name")).toHaveCount(0);
});

test("a revoked link is refused as gone, and an unknown one as not found", async ({ page }) => {
  await page.goto("/?t=q-revoked-410");
  await expect(page.getByText("This link is no longer valid.")).toBeVisible();
  await expect(page.getByText("Please contact us for an up-to-date link.")).toBeVisible();

  await page.goto("/?t=q-never-minted");
  await expect(page.getByText("We could not find this quote. Please check the link you were sent.")).toBeVisible();

  await page.goto("/");
  await expect(page.getByText("This link is missing its access code. Please use the full link you were sent.")).toBeVisible();
});

test("the typed name acceptance is refused when blank, then when unticked, then accepted when typed", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  const accept = page.getByRole("button", { name: "Accept quote" });

  await accept.click();
  await expect(page.getByRole("alert")).toContainText("Please type your full name.");

  await page.locator("#name").fill("Sample Customer");
  await accept.click();
  await expect(page.getByRole("alert")).toContainText("Please tick the box to confirm you accept this quote.");

  const posted = acceptCall(page);
  await page.locator("#affirm").check();
  await accept.click();
  const request = (await posted).request();
  expect(request.postDataJSON()).toEqual({ typed_name: "Sample Customer", accepted: true });

  // The confirmation echoes the totals, so the customer's own record of what
  // they agreed to does not depend on their having read the page carefully:
  // the quote's totals and the echo's totals both render.
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-25 accepted" })).toBeVisible();
  await expect(page.getByText("Accepted by Sample Customer on")).toBeVisible();
  await expect(page.getByText("Total $109.22")).toHaveCount(2);
  // The success is MARKED, not bare body text: the check rides the heading's
  // words (colour never encodes state alone).
  await expect(page.locator("svg.okmark")).toHaveCount(1);
});

test("a second accept is shown as already accepted", async ({ page }) => {
  // The same link after acceptance: the quote left the acceptable state, so
  // the page says the quote is accepted instead of offering the form again,
  // under the success mark: the customer's own action, acknowledged.
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByText("This quote is accepted and is not awaiting acceptance.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.locator("svg.okmark")).toHaveCount(1);
  await expect(page.locator("#name")).toHaveCount(0);

  // A link whose accept refuses meets the server's own words, inline: the
  // page shows the refusal rather than replacing it with canned text.
  await page.goto("/?t=q-refuse-27");
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#affirm").check();
  const posted = acceptCall(page);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await posted;
  await expect(page.getByRole("alert")).toContainText("This quote has already been accepted.");

  // A quote that was accepted elsewhere loads in the accepted state too.
  await page.goto("/?t=q-accepted-26");
  await expect(page.getByText("This quote is accepted and is not awaiting acceptance.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
});

test("the quote's terms are spoken to the customer: delivery, deposit, notes, discount", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await expect(page.getByText("Delivery to 12 Test Yard Lane")).toBeVisible();
  await expect(page.getByText(/delivery fee \$45\.00 \(included in the total\)/)).toBeVisible();
  await expect(page.getByText(/33% deposit of \$193\.91 is required to\s+schedule this work/)).toBeVisible();
  await expect(page.getByText("Notes: Gate code at the side fence, ask for the site lead.")).toBeVisible();
  await expect(page.getByText("10% discount applied")).toBeVisible();
  // The dealer chose no images on this quote: no thumbnail renders.
  await expect(page.locator("img.thumb")).toHaveCount(0);
});

test("no credential header rides any call, every call is a public route or the image read, and no storage is touched", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await page.goto("/?t=q-terms-28");
  await page.goto("/?t=q-revoked-410");
  await page.goto(`/?t=${GOOD_TOKEN}`);

  const state = await stubState(page);
  const apiCalls = state.sightings.filter((sighting) => sighting.path.startsWith("/api/"));
  expect(apiCalls.length).toBeGreaterThan(0);
  for (const sighting of apiCalls) {
    expect(sighting.headerNames).not.toContain("authorization");
    expect(sighting.headerNames).not.toContain("cookie");
    expect(sighting.headerNames).not.toContain("x-org");
    const isPublic = sighting.path.startsWith("/api/public/v1/");
    const isImageRead = /^\/api\/v1\/products\/[^/]+\/image$/.test(sighting.path);
    expect(isPublic || isImageRead).toBe(true);
  }

  const storage = await page.evaluate(() => ({
    local: window.localStorage.length,
    session: window.sessionStorage.length,
    cookie: document.cookie,
  }));
  expect(storage).toEqual({ local: 0, session: 0, cookie: "" });
});

// A failed accept must speak plainly and keep the form. The server's own words
// are shown only when the answer carried a status and the JSON error envelope;
// a transport failure or a proxy's page never reaches the customer.
const COULD_NOT_REACH = "We could not reach the server. Please try again.";
const NOT_RECORDED = "We could not record your acceptance. Please try again.";

async function readyToAccept(page: Page, token: string): Promise<void> {
  await page.goto(`/?t=${token}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#affirm").check();
}

async function expectFormKept(page: Page): Promise<void> {
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await expect(page.locator("#name")).toHaveValue("Sample Customer");
  await expect(page.locator("#affirm")).toBeChecked();
  await expect(page.getByRole("button", { name: "Accept quote" })).toBeEnabled();
}

test("an accept that never reaches the server says so plainly and keeps the form", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(COULD_NOT_REACH);
  await expect(page.getByRole("alert")).not.toContainText("transport");
  await expect(page.getByRole("alert")).not.toContainText("quotePublicAcceptQuote");
  await expectFormKept(page);
});

test("an accept answered by a proxy's HTML page shows neither the page nor its host", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({
      status: 502,
      contentType: "text/html",
      body: "<html><body><h1>502 Bad Gateway</h1><p>upstream core.internal.example:8000 refused</p></body></html>",
    }),
  );
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(NOT_RECORDED);
  const body = await page.locator("body").evaluate((node) => node.outerHTML + (document.querySelector("pd-quote-public")?.shadowRoot?.innerHTML ?? ""));
  expect(body).not.toContain("Bad Gateway");
  expect(body).not.toContain("core.internal.example");
  await expectFormKept(page);
});

test("a refusal that carries the server's JSON error envelope shows the server's words", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) => route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "The typed name must match the contact on the quote." }) }));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText("The typed name must match the contact on the quote.");
});

test("the whole network going down after the quote loaded keeps the quote and the typed name", async ({ page, context }) => {
  await readyToAccept(page, "q-terms-28");
  await context.setOffline(true);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(COULD_NOT_REACH);
  await expectFormKept(page);
  await expect(page.getByText("Quote link unavailable")).toHaveCount(0);
  await context.setOffline(false);
});
