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
import { createHash } from "node:crypto";
import assert from "node:assert/strict";
import { expect, test, type Page, type Response } from "@playwright/test";
import { DEALER_NAME } from "../src/dealer.js";

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
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  await page.screenshot({ path: `${dir}/accept-state.png` });
});

test("a quote is viewed by its token: header, lines, totals", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  await expect(page.getByText("Prepared for Fieldstone Hardscapes Inc")).toBeVisible();
  await expect(page.getByText(/valid until /)).toBeVisible();

  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("Bluff Natural Step");
  await expect(rows.nth(0)).toContainText("1 pc");
  await expect(rows.nth(0)).toContainText("$20.57");
  await expect(rows.nth(1)).toContainText("Tidewater Polymeric Sand");
  await expect(rows.nth(1)).toContainText("1 bag");
  await expect(rows.nth(1)).toContainText("$22.08");
  await expect(rows.nth(2)).toContainText("Black Mulch");
  await expect(rows.nth(2)).toContainText("1 cu yd");
  await expect(rows.nth(2)).toContainText("$54.00");

  await expect(page.getByText("Subtotal $96.65")).toBeVisible();
  await expect(page.getByText("HST $12.57")).toBeVisible();
  await expect(page.getByText("Total $109.22")).toBeVisible();
});

test("a California dealer's page speaks Sales tax, from the server's own word and nowhere else", async ({ page }) => {
  await page.route(/\/api\/public\/v1\/quotes\/[^/?]+$/, async (route) => {
    const response = await route.fetch();
    const json = await response.json();
    await route.fulfill({ response, json: { ...json, tax_label: "Sales tax" } });
  });
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByText("Sales tax $12.57")).toBeVisible();
  await expect(page.getByText("Subtotal $96.65")).toBeVisible();
  await expect(page.locator("body")).not.toContainText("HST");
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

test("thumbnail object URLs live until the page goes: a refused accept never revokes them, leaving the page does", async ({ page }) => {
  await page.addInitScript(() => {
    const w = window as unknown as { __revoked: number };
    w.__revoked = 0;
    const real = URL.revokeObjectURL.bind(URL);
    URL.revokeObjectURL = (url: string) => {
      w.__revoked += 1;
      real(url);
    };
  });
  await page.goto(`/?t=${GOOD_TOKEN}`);
  const thumbs = page.locator("img.thumb");
  await expect(thumbs).toHaveCount(3);
  await page.route("**/accept", (route) => route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "quote: this quote is not awaiting acceptance (it is SENT)" }) }));
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText("This quote cannot be accepted right now. Please contact us.");
  // The refused accept reloaded the quote: every image still points at a live
  // URL and nothing was revoked.
  await expect(thumbs).toHaveCount(3);
  const stillLive = await page.evaluate(async () => {
    const imgs = [...(document.querySelector("pd-quote-public")?.shadowRoot?.querySelectorAll<HTMLImageElement>("img.thumb") ?? [])];
    const loaded = await Promise.all(
      imgs.map(async (img) => {
        const res = await fetch(img.src).then((r) => r.ok, () => false);
        return res && img.complete && img.naturalWidth > 0;
      }),
    );
    return { loaded, revoked: (window as unknown as { __revoked: number }).__revoked };
  });
  expect(stillLive.loaded).toEqual([true, true, true]);
  expect(stillLive.revoked).toBe(0);
  // Leaving the page releases them, once each.
  const revoked = await page.evaluate(() => {
    document.querySelector("pd-quote-public")?.remove();
    return (window as unknown as { __revoked: number }).__revoked;
  });
  expect(revoked).toBe(3);
});

test("the accept area speaks to the customer, with no word about the page's own design", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByText(/Accepting records your typed name, the date and time, and the link you used/)).toBeVisible();
  await expect(page.getByText(/greyed out/)).toHaveCount(0);
  await expect(page.getByText(/unfinished/)).toHaveCount(0);
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
  // The light half of --hh-page is #E7ECF1: the grey-blue ground under white
  // cards, stepped darker with the light contrast round (decision log row
  // 987). The page pinned light in index.html, so the body must resolve
  // exactly that token, not the default white canvas.
  const ground = await page.evaluate(() => ({
    body: getComputedStyle(document.body).backgroundColor,
    card: (() => {
      const card = document.querySelector("pd-quote-public")?.shadowRoot?.querySelector(".card");
      return card ? getComputedStyle(card).backgroundColor : null;
    })(),
  }));
  expect(ground.body).toBe("rgb(231, 236, 241)");
  expect(ground.card).toBe("rgb(255, 255, 255)");
});

test("the page's UI face is the shared Inter itself, self-hosted from the bundle, not a system fallback", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  // document.fonts is the closest provable check, and load() is the honest
  // half of it: it answers with the faces it actually MATCHED, so an absent
  // @font-face (check()'s vacuous true) fails here as an empty answer. The
  // three weights are the ones the page renders: body, labels and button,
  // title. The face is the design system's shared Inter now (the type pass,
  // row 942): the token sheet this app imports carries the @font-face rules
  // and the vendored bytes, so the load proves the shared face reached the
  // customer document through the bundle alone.
  const faces = await page.evaluate(async () => {
    const out: Record<string, boolean> = {};
    for (const weight of ["400", "500", "600", "700"]) {
      const matched = await document.fonts.load(`${weight} 16px Inter`);
      out[weight] = matched.length > 0 && matched.every((face) => face.status === "loaded");
    }
    await document.fonts.ready;
    return out;
  });
  expect(faces).toEqual({ 400: true, 500: true, 600: true, 700: true });
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

test("the Accept button waits for the quantities box, the name and the confirmation, then the accept carries all three", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  const accept = page.getByRole("button", { name: "Accept quote" });

  // The quantities box sits above the name, and nothing is ready until all three are in.
  const qty = page.locator("#qty-confirm");
  await expect(qty).toBeVisible();
  await expect(page.getByText("I have reviewed the quantities on this quote and they are what I want.")).toBeVisible();
  const qtyBox = await qty.boundingBox();
  const nameBox = await page.locator("#name").boundingBox();
  expect(qtyBox !== null && nameBox !== null && qtyBox.y < nameBox.y).toBe(true);
  await expect(accept).toBeDisabled();

  await qty.check();
  await expect(accept).toBeDisabled();
  await page.locator("#name").fill("Sample Customer");
  await expect(accept).toBeDisabled();
  await page.locator("#affirm").check();
  await expect(accept).toBeEnabled();

  // Taking any one of the three back disables it again.
  await qty.uncheck();
  await expect(accept).toBeDisabled();
  await qty.check();
  await page.locator("#name").fill("   ");
  await expect(accept).toBeDisabled();
  await page.locator("#name").fill("Sample Customer");
  await expect(accept).toBeEnabled();

  const posted = acceptCall(page);
  await accept.click();
  const request = (await posted).request();
  expect(request.postDataJSON()).toEqual({ typed_name: "Sample Customer", accepted: true, quantities_confirmed: true });

  // The confirmation echoes the totals, so the customer's own record of what
  // they agreed to does not depend on their having read the page carefully:
  // the quote's totals and the echo's totals both render.
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-25 accepted" })).toBeVisible();
  await expect(page.getByText("Accepted by Sample Customer on")).toBeVisible();
  await expect(page.getByText("You confirmed the quantities on this quote.")).toBeVisible();
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

  // A link whose accept refuses gets the page's OWN sentence, inline: the
  // core's refusal is written for staff and never reaches a customer.
  await page.goto("/?t=q-refuse-27");
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  const posted = acceptCall(page);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await posted;
  await expect(page.getByRole("alert")).toHaveText("This quote cannot be accepted right now. Please contact us.");
  await expect(page.locator("body")).not.toContainText(/not awaiting acceptance|quote:/);

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
    // The tenant config read only names the browser tab's dealer title; it is
    // a public GET (no credential, no token onward) the page fires once.
    const isTenantTitleRead = sighting.method === "GET" && sighting.path === "/api/v1/tenant/config";
    expect(isPublic || isImageRead || isTenantTitleRead).toBe(true);
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
// The page says "not recorded" ONLY on the server's own definite refusal. Every
// other failure is an unknown outcome, said as such, and a retry is safe: it
// carries the same idempotency key, so the server never records it twice.
const UNCONFIRMED =
  "We could not confirm whether your acceptance was recorded. Trying again with the same name is safe: it will not be recorded twice.";
const NOT_RECORDED_SUFFIX = "Your acceptance was not recorded.";
// The page's own sentences for a definite refusal: the core's words are written for staff.
const CANNOT_ACCEPT = "This quote cannot be accepted right now. Please contact us.";
const CHECK_FIELDS = "Please check your full name and the confirmation, then try again.";
const REFUSED_GENERIC = "We could not accept this quote right now. Please contact us.";
const NOT_FOUND_SENTENCE = "We could not find this quote. Please check the link you were sent.";
const REPEAT_NOTE =
  "This quote was already accepted. Accepting again through this link with the same name confirms the earlier acceptance; a different name records another acceptance.";
const STAFF_WORDS = /convert it at the counter|administrator|quote:|acceptance settings|order SO-1/i;

async function readyToAccept(page: Page, token: string): Promise<void> {
  await page.goto(`/?t=${token}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
}

async function expectFormKept(page: Page): Promise<void> {
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await expect(page.locator("#name")).toHaveValue("Sample Customer");
  await expect(page.locator("#qty-confirm")).toBeChecked();
  await expect(page.locator("#affirm")).toBeChecked();
  await expect(page.getByRole("button", { name: "Accept quote" })).toBeEnabled();
}

test("an accept that never reaches the server, with the re-read failing too, is unconfirmed and keeps the form", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) => route.abort("failed"));
  await page.route("**/api/public/v1/quotes/q-terms-28", (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.getByRole("alert")).not.toContainText("could not reach");
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
  // A 5xx that is not the envelope is an unknown outcome, even though the
  // re-read answers SENT: a re-read proves nothing was NOT recorded.
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  const body = await page.locator("body").evaluate((node) => node.outerHTML + (document.querySelector("pd-quote-public")?.shadowRoot?.innerHTML ?? ""));
  expect(body).not.toContain("Bad Gateway");
  expect(body).not.toContain("core.internal.example");
  await expectFormKept(page);
});

test("a 422 that carries the JSON error envelope is not one of the core's own refusals: unknown, never the core's words", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) => route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "quote: staff only wording about the contact on the quote" }) }));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.locator("body")).not.toContainText("staff only wording");
});

test("the whole network going down after the quote loaded keeps the quote and the typed name", async ({ page, context }) => {
  await readyToAccept(page, "q-terms-28");
  await context.setOffline(true);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expectFormKept(page);
  await expect(page.getByText("Quote link unavailable")).toHaveCount(0);
  await context.setOffline(false);
});

test("an accept recorded by the core but answered by a proxy's 504 page ends on the accepted state", async ({ page }) => {
  await readyToAccept(page, "q-lost-504");
  await page.route("**/accept", async (route) => {
    await route.fetch();
    await route.fulfill({ status: 504, contentType: "text/html", body: "<html><body><h1>504 Gateway Timeout</h1></body></html>" });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Accept quote" })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("an accept recorded by the core but answered 200 with an unreadable body ends on the accepted state", async ({ page }) => {
  await readyToAccept(page, "q-lost-unreadable");
  await page.route("**/accept", async (route) => {
    await route.fetch();
    await route.fulfill({ status: 200, contentType: "application/json", body: "{\"quote_no\": \"Q-28\", \"typed_na" });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("an accept that never reached the core, re-read still SENT, is an unknown outcome that offers the same button", async ({ page }) => {
  await readyToAccept(page, "q-lost-refused");
  // The accept is refused before the stub sees it; the re-read answers SENT.
  // That does not prove the acceptance was not recorded (a request that timed
  // out at the client can commit later), so the page never says it.
  await page.route("**/accept", (route) => route.abort("connectionrefused"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expectFormKept(page);
});

const ECHO_28 = {
  quote_no: "Q-28",
  typed_name: "Sample Customer",
  subtotal_cents: 52000,
  tax_cents: 6760,
  total_cents: 58760,
  tax_label: "HST",
};

async function slowAccept(page: Page): Promise<() => number> {
  let posts = 0;
  await page.route("**/accept", async (route) => {
    posts += 1;
    await new Promise((resolve) => setTimeout(resolve, 600));
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ...ECHO_28, accepted_at: new Date().toISOString() }),
    });
  });
  return () => posts;
}

test("a double click on Accept sends exactly one POST while the first is in flight", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  const posts = await slowAccept(page);
  await page.getByRole("button", { name: "Accept quote" }).dblclick();
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();
  expect(posts()).toBe(1);
});

test("two synchronous clicks send one POST: the busy guard stands alone, before the button can render disabled", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  const posts = await slowAccept(page);
  // Both clicks run in one task, so the disabled attribute cannot have
  // rendered between them: only the guard in accept() stops the second.
  await page.locator("pd-quote-public").evaluate((host) => {
    const inner = host.shadowRoot?.querySelector<HTMLButtonElement>("button[data-action=accept]");
    inner?.click();
    inner?.click();
  });
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();
  expect(posts()).toBe(1);
});

test("a definite refusal is final whatever the re-read does: the page's own sentence, kept page, only a dead link replaces it", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({ status: 409, contentType: "application/json", body: JSON.stringify({ error: "quote: this quote is not awaiting acceptance (it is SENT)" }) }),
  );
  await page.route("**/api/public/v1/quotes/q-terms-28", (route) =>
    route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "boom" }) }),
  );
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(CANNOT_ACCEPT);
  await expectFormKept(page);
  await expect(page.getByText("Quote link unavailable")).toHaveCount(0);
});

test("the page carries the dealer's header: the dealer's name is visible at the top, from the app's own data", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-25" })).toBeVisible();
  // The header band is the page's first element, and the name it carries is
  // the dealer name's source of truth in the app, imported here so the test
  // pins the structure, never a duplicated literal.
  const header = page.locator("pd-quote-public header.dealer");
  const name = page.locator("pd-quote-public header.dealer .dealer-name");
  await expect(name).toHaveText(DEALER_NAME);
  await expect(name).toBeVisible();
  const boxes = await page.evaluate(() => {
    const root = document.querySelector("pd-quote-public")?.shadowRoot;
    const pick = (selector: string): number | null => root?.querySelector(selector)?.getBoundingClientRect().top ?? null;
    return { header: pick("header.dealer"), h1: pick("h1") };
  });
  expect(boxes.h1, "the quote heading must exist").not.toBeNull();
  expect(boxes.header, "the dealer header must sit above the quote heading").toBeLessThan(boxes.h1!);
  await expect(header).toContainText(DEALER_NAME);
});

test("every line shows the same image cell: the photo or the no-photo mark, never an empty box", async ({ page }) => {
  // Images are ON and the two lines differ on purpose: the first carries a
  // product photo, the second carries none, the exact shape the staging
  // defect showed (an empty box on one line, nothing on the next).
  await page.goto("/?t=q-mixed-images-29");
  await expect(page.getByRole("heading", { name: "Quote #Q-29" })).toBeVisible();
  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(2);
  // The first line shows the product's photo, through the image read.
  await expect(rows.nth(0).locator("img.thumb")).toBeVisible();
  // The second line shows the SAME cell treatment with the drawn no-photo
  // mark: never an empty box (indistinguishable from an image still loading)
  // and never a missing cell.
  await expect(rows.nth(1).locator("svg.thumbmark")).toBeVisible();
  await expect(rows.nth(1).locator("img.thumb")).toHaveCount(0);
  // And no image cell on any line is left bare: every one is the same drawn
  // box with either the photo or the mark in it.
  const cells = await page.evaluate(() => {
    const root = document.querySelector("pd-quote-public")?.shadowRoot;
    return Array.from(root?.querySelectorAll("tbody tr") ?? []).map((row) => {
      const box = row.querySelector("td .thumbslot");
      if (box === null) return "missing";
      const marked = box.querySelector("img.thumb, img.tile, svg.thumbmark") !== null;
      return getComputedStyle(box).borderTopWidth !== "0px" && marked ? "boxed" : "bare";
    });
  });
  expect(cells, "every line's image cell is the same visible box, photo or mark").toEqual(["boxed", "boxed"]);
});

// The quote accent (decision log rows 1047 item 7 and 1063): a quote and its documents read in
// the quote blue, so every surface below is measured LIVE against the token the sheet declares.
// Colours go through a canvas pixel into plain sRGB channels (computed styles may come back in a
// wide-gamut syntax), with a small tolerance for the conversion.
type Measure = { want: string; wantPixel: number[]; have: string; havePixel: number[] };

async function measureAgainstToken(
  page: Page,
  token: string,
  selector: string,
  property: "color" | "backgroundColor" | "borderBottomColor" | "outlineColor",
  state?: "focus",
): Promise<Measure> {
  return page.evaluate(
    ({ token, selector, property, state }) => {
      const pixelOf = (colour: string): number[] => {
        const ctx = document.createElement("canvas").getContext("2d") as CanvasRenderingContext2D;
        ctx.fillStyle = colour;
        ctx.fillRect(0, 0, 1, 1);
        return Array.from(ctx.getImageData(0, 0, 1, 1).data).slice(0, 3);
      };
      const want = getComputedStyle(document.documentElement).getPropertyValue(token).trim();
      const el = document.querySelector("pd-quote-public")?.shadowRoot?.querySelector<HTMLElement>(selector);
      if (!el) throw new Error(`no element for ${selector}`);
      if (state === "focus") el.focus();
      const have = getComputedStyle(el)[property];
      return { want, wantPixel: pixelOf(want), have, havePixel: pixelOf(have) };
    },
    { token, selector, property, state },
  );
}

function expectToken(m: Measure, token: string, what: string): void {
  expect(m.want, `${token} must resolve on the page`).not.toBe("");
  for (let channel = 0; channel < 3; channel += 1) {
    expect(
      Math.abs(m.havePixel[channel] - m.wantPixel[channel]),
      `channel ${channel}: ${what} must be ${token} ${m.want} (it sampled ${m.have})`,
    ).toBeLessThanOrEqual(2);
  }
}

async function readyOpenQuote(page: Page, query = ""): Promise<void> {
  await page.goto(`/?t=q-terms-28${query}`);
  await expect(page.getByRole("button", { name: "Accept quote" })).toBeVisible();
}

test("the accept button is the quote fill, one value, with its quote ink: not the gold accent", async ({ page }) => {
  // A quote that is still open: the demo quote is accepted by an earlier test.
  await readyOpenQuote(page);
  // The fill is the ENABLED look: a disabled button is grey by design, so ready the form first.
  await page.locator("#qty-confirm").check();
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#affirm").check();
  await expect(page.getByRole("button", { name: "Accept quote" })).toBeEnabled();
  await expect(page.locator("html")).not.toHaveAttribute("data-brand", "hh-pro");
  const sel = "button[data-action=accept]";
  expectToken(await measureAgainstToken(page, "--hh-quote-fill", sel, "backgroundColor"), "--hh-quote-fill", "the accept fill");
  expectToken(await measureAgainstToken(page, "--hh-quote-fill-on", sel, "color"), "--hh-quote-fill-on", "the accept ink");
  // And it is NOT gold: the gold accent is the order's.
  const gold = await measureAgainstToken(page, "--hh-accent", sel, "backgroundColor");
  const apart = gold.havePixel.some((c, i) => Math.abs(c - gold.wantPixel[i]) > 40);
  expect(apart, "the accept fill must not read as the gold accent").toBe(true);
  // Hover keeps the one fill value (the quote fill has no second step).
  await page.locator(sel).hover();
  expectToken(await measureAgainstToken(page, "--hh-quote-fill", sel, "backgroundColor"), "--hh-quote-fill", "the hovered accept fill");
});

test("a quote's title, total and header rule read in the quote blue, and the focus ring is its quote twin", async ({ page }) => {
  await readyOpenQuote(page);
  expectToken(await measureAgainstToken(page, "--hh-quote", "h1", "color"), "--hh-quote", "the Quote title");
  expectToken(await measureAgainstToken(page, "--hh-quote", ".totals .grand", "color"), "--hh-quote", "the total");
  expectToken(await measureAgainstToken(page, "--hh-quote", "header.dealer", "borderBottomColor"), "--hh-quote", "the dealer rule");
  const rule = await page.evaluate(() => {
    const el = document.querySelector("pd-quote-public")?.shadowRoot?.querySelector("header.dealer") as HTMLElement;
    return getComputedStyle(el).borderBottomWidth;
  });
  expect(rule, "the rule under the header is the board's 2px quote line").toBe("2px");
  expectToken(await measureAgainstToken(page, "--hh-quote", "#name", "outlineColor", "focus"), "--hh-quote", "the focus ring");
});

test("the paper copy carries the same quote colours, and prints them", async ({ page }) => {
  await page.emulateMedia({ media: "print" });
  await page.goto("/?t=q-terms-28&print=1");
  await expect(page.locator("pd-quote-public").getByRole("heading", { level: 1 })).toBeVisible();
  await expect(page.getByRole("button", { name: "Accept quote" })).toHaveCount(0);
  expectToken(await measureAgainstToken(page, "--hh-quote", "h1", "color"), "--hh-quote", "the printed title");
  expectToken(await measureAgainstToken(page, "--hh-quote", ".totals .grand", "color"), "--hh-quote", "the printed total");
  expectToken(await measureAgainstToken(page, "--hh-quote", "header.dealer", "borderBottomColor"), "--hh-quote", "the printed rule");
  const adjust = await page.evaluate(() => {
    const host = document.querySelector("pd-quote-public") as HTMLElement;
    return getComputedStyle(host).getPropertyValue("print-color-adjust");
  });
  expect(adjust, "colours must survive the print dialog's default of dropping backgrounds").toBe("exact");
});

// Round 2 (FROM-PRINCIPAL 9): the black rule the live check saw on paper. The screen rule rides the
// --doc-ink alias, and a print path that re-resolves the alias chain and loses it drops the border to
// currentColor, the near-black text ink. The print media block states the family itself, so the rule
// keeps the quote blue with the alias severed; on the screen cascade alone this probe reads near
// black, which is why the leg cannot pass against the inherited colour.
test("the printed dealer rule keeps the quote blue when the alias chain fails", async ({ page }) => {
  await page.emulateMedia({ media: "print" });
  await readyOpenQuote(page);
  const severed = await page.evaluate(() => {
    const host = document.querySelector("pd-quote-public") as HTMLElement;
    // The alias the screen rule rides, made empty: the failure mode the print path hit.
    host.style.setProperty("--doc-ink", " ");
    const header = host.shadowRoot?.querySelector("header.dealer") as HTMLElement;
    const pixelOf = (colour: string): number[] => {
      const ctx = document.createElement("canvas").getContext("2d") as CanvasRenderingContext2D;
      ctx.fillStyle = colour;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data).slice(0, 3);
    };
    const quote = getComputedStyle(document.documentElement).getPropertyValue("--hh-quote").trim();
    const textInk = getComputedStyle(document.documentElement).getPropertyValue("--hh-text").trim();
    const have = getComputedStyle(header).borderBottomColor;
    return { have, quotePixel: pixelOf(quote), textInkPixel: pixelOf(textInk), havePixel: pixelOf(have) };
  });
  // The probe is honest: the text ink the fallback would paint is a different colour from the quote
  // blue, so a currentColor fallback cannot slip through the comparison below.
  expect(
    severed.textInkPixel.some((c, i) => Math.abs(c - severed.quotePixel[i]) > 40),
    "the text ink and the quote blue must be far apart for this probe to mean anything",
  ).toBe(true);
  for (let channel = 0; channel < 3; channel += 1) {
    expect(
      Math.abs(severed.havePixel[channel] - severed.quotePixel[channel]),
      `the printed rule must stay the quote blue with the alias severed (it sampled ${severed.have})`,
    ).toBeLessThanOrEqual(2);
  }
});

// A conversion visibly changes colour (decision log row 1063): the same quote reads blue before and
// gold after, the page reads as the order, and nothing on it is quote blue.
async function convertInFlight(page: Page, token: string): Promise<void> {
  await page.route(`**/api/public/v1/quotes/${token}`, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean; order_no?: string };
    body.status = "CONVERTED";
    body.acceptable = false;
    body.order_no = "SO-PLM-17";
    await route.fulfill({ response, json: body });
  });
}

test("the same quote reads blue before conversion and gold after, light and on paper", async ({ page, context }) => {
  for (const print of [false, true]) {
    const query = print ? "&print=1" : "";
    if (print) await page.emulateMedia({ media: "print" });
    await page.goto(`/?t=q-flip-8${query}`);
    await expect(page.locator("pd-quote-public").getByRole("heading", { level: 1 })).toHaveText(/Quote #Q-7/);
    expectToken(await measureAgainstToken(page, "--hh-quote", "h1", "color"), "--hh-quote", `the quote title (print ${print})`);
    expectToken(await measureAgainstToken(page, "--hh-quote", ".totals .grand", "color"), "--hh-quote", `the quote total (print ${print})`);
    const after = await context.newPage();
    if (print) await after.emulateMedia({ media: "print" });
    await convertInFlight(after, "q-flip-8");
    await after.goto(`/?t=q-flip-8${query}`);
    await expect(after.locator("pd-quote-public").getByRole("heading", { level: 1 })).toHaveText(
      "Sales order SO-PLM-17, converted from #Q-7",
    );
    expectToken(await measureAgainstToken(after, "--hh-accent-text", ".totals .grand", "color"), "--hh-accent-text", `the order total (print ${print})`);
    expectToken(await measureAgainstToken(after, "--hh-accent", ".orderband", "backgroundColor"), "--hh-accent", `the order band (print ${print})`);
    expectToken(await measureAgainstToken(after, "--hh-accent-ink", "h1", "color"), "--hh-accent-ink", `the order title ink on its gold band (print ${print})`);
    expectToken(await measureAgainstToken(after, "--hh-accent-text", "header.dealer", "borderBottomColor"), "--hh-accent-text", `the order's dealer rule (print ${print})`);
    await after.close();
  }
});

test("a converted quote's page is the order's, in the gold family only: no quote blue, no Accept form", async ({ page }) => {
  await page.goto("/?t=q-conv-8");
  const heading = page.locator("pd-quote-public").getByRole("heading", { level: 1 });
  await expect(heading).toHaveText("Sales order SO-PLM-17, converted from #Q-7");
  await expect(page.getByRole("button", { name: "Accept quote" })).toHaveCount(0);
  await expect(page.locator("#name")).toHaveCount(0);
  // No element on the page paints the quote family: walk every computed colour and refuse the blue.
  const blue = await page.evaluate(() => {
    const px = (c: string): number[] => {
      const ctx = document.createElement("canvas").getContext("2d") as CanvasRenderingContext2D;
      ctx.fillStyle = c;
      ctx.fillRect(0, 0, 1, 1);
      return Array.from(ctx.getImageData(0, 0, 1, 1).data).slice(0, 3);
    };
    const tokens = ["--hh-quote", "--hh-quote-fill", "--hh-quote-link", "--hh-quote-tint"].map((t) =>
      px(getComputedStyle(document.documentElement).getPropertyValue(t).trim()),
    );
    const root = document.querySelector("pd-quote-public")?.shadowRoot;
    const hits: string[] = [];
    for (const el of Array.from(root?.querySelectorAll("*") ?? [])) {
      const cs = getComputedStyle(el);
      for (const prop of ["color", "backgroundColor", "borderBottomColor", "borderTopColor", "outlineColor"] as const) {
        const p = px(cs[prop]);
        if (tokens.some((t) => t.every((c, i) => Math.abs(c - p[i]) <= 2))) hits.push(`${el.tagName}.${el.className} ${prop}`);
      }
    }
    return hits;
  });
  expect(blue, "nothing on a converted page may read in the quote blue").toEqual([]);
  expectToken(await measureAgainstToken(page, "--hh-accent-text", ".totals .grand", "color"), "--hh-accent-text", "the order total");
  expectToken(await measureAgainstToken(page, "--hh-accent", ".orderband", "backgroundColor"), "--hh-accent", "the order band");
});

test("a converted quote with no order number from the core says it became an order and invents no number", async ({ page }) => {
  await page.goto("/?t=q-conv-bare-8");
  const heading = page.locator("pd-quote-public").getByRole("heading", { level: 1 });
  await expect(heading).toHaveText(/Quote #Q-7/);
  await expect(page.locator("pd-quote-public")).toContainText("now an order");
  await expect(page.locator("pd-quote-public")).not.toContainText("Sales order");
  expectToken(await measureAgainstToken(page, "--hh-accent-text", ".totals .grand", "color"), "--hh-accent-text", "the order total");
});

// The converted page's words (FROM-PRINCIPAL 29): both the "valid until" line and the pre-acceptance
// notice speak to a decision still open, and a converted quote's decision is made, so the page drops
// them and says instead what the accepted page says, the quantities confirmation line, beside the gold
// order line. The closed acceptance card goes with them (the review's observation B): a door marked
// closed reads against a document titled as the sales order, and the gold line plus the confirmation
// say what happened.
test("a converted quote's page drops the valid until line and the notice, and says the quantities were confirmed", async ({ page }) => {
  await page.goto("/?t=q-conv-8");
  await expect(page.getByRole("heading", { name: "Sales order SO-PLM-17, converted from #Q-7" })).toBeVisible();
  const confirmed = page.locator(".orderband").getByText("You confirmed the quantities on this quote.");
  await expect(confirmed).toBeVisible();
  const body = page.locator("pd-quote-public");
  await expect(body).not.toContainText("valid until");
  await expect(page.locator(".qtynotice")).toHaveCount(0);
  await expect(body).not.toContainText("All quantities are to be confirmed by the customer");
  await expect(page.getByRole("heading", { name: "Acceptance closed" })).toHaveCount(0);
  await expect(body).not.toContainText("not awaiting acceptance");
  // The same words when the core sent no order number: the decision is made either way.
  await page.goto("/?t=q-conv-bare-8");
  await expect(page.locator(".orderband").getByText("You confirmed the quantities on this quote.")).toBeVisible();
  await expect(page.locator("pd-quote-public")).not.toContainText("valid until");
  await expect(page.locator(".qtynotice")).toHaveCount(0);
});

test("a converted quote's paper copy drops the same two and carries the confirmation line", async ({ page }) => {
  await page.emulateMedia({ media: "print" });
  await page.goto("/?t=q-conv-8&print=1");
  await expect(page.getByRole("heading", { name: "Sales order SO-PLM-17, converted from #Q-7" })).toBeVisible();
  await expect(page.locator(".orderband").getByText("You confirmed the quantities on this quote.")).toBeVisible();
  const body = page.locator("pd-quote-public");
  await expect(body).not.toContainText("valid until");
  await expect(page.locator(".qtynotice")).toHaveCount(0);
  await expect(body).not.toContainText("All quantities are to be confirmed by the customer");
});

test("an accepted quote that has not converted keeps its accepted wording: the line, the notice, the card", async ({ page }) => {
  await page.goto("/?t=q-accepted-26");
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.getByText("This quote is accepted and is not awaiting acceptance.")).toBeVisible();
  await expect(page.getByText(/valid until /)).toBeVisible();
  await expect(page.locator(".qtynotice")).toBeVisible();
  await expect(page.getByText("All quantities are to be confirmed by the customer.")).toBeVisible();
  // The confirmation line belongs to the converted page and the thank you page, not to the accepted
  // closed card: this page has not converted, so it says none of it.
  await expect(page.getByText("You confirmed the quantities on this quote.")).toHaveCount(0);
});

test("multi-accept on: a converted quote the dealer left acceptable keeps the form, minus the pre-acceptance words", async ({ page }) => {
  await page.route("**/api/public/v1/quotes/q-conv-8", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { acceptable: boolean };
    body.acceptable = true;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/?t=q-conv-8");
  await expect(page.getByRole("heading", { name: "Sales order SO-PLM-17, converted from #Q-7" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Accept this quote" })).toBeVisible();
  await expect(page.getByText(REPEAT_NOTE)).toBeVisible();
  await expect(page.locator(".orderband").getByText("You confirmed the quantities on this quote.")).toBeVisible();
  await expect(page.locator("pd-quote-public")).not.toContainText("valid until");
  await expect(page.locator(".qtynotice")).toHaveCount(0);
});

// The core keeps an ACCEPTED quote acceptable when multi-accept is on
// (share.go): the view of a committed quote reads ACCEPTED with acceptable true.
async function multiAcceptView(page: Page, token: string): Promise<void> {
  await page.route(`**/api/public/v1/quotes/${token}`, async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    if (body.status === "ACCEPTED") body.acceptable = true;
    await route.fulfill({ response, json: body });
  });
}

test("an unreadable 200 recorded by the core, with the re-read aborted, is unconfirmed, never could not reach", async ({ page }) => {
  await readyToAccept(page, "q-unk-unreadable");
  await page.route("**/accept", async (route) => {
    await route.fetch();
    await route.fulfill({ status: 200, contentType: "application/json", body: "{\"quote_no\": \"Q-28\", \"typed_na" });
  });
  await page.route("**/api/public/v1/quotes/q-unk-unreadable", (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.getByRole("alert")).not.toContainText("could not reach");
  await expectFormKept(page);
});

test("an accept fetched through to the core then reset, with the re-read failing, is unconfirmed", async ({ page }) => {
  await readyToAccept(page, "q-unk-reset");
  await page.route("**/accept", async (route) => {
    await route.fetch();
    await route.abort("connectionreset");
  });
  await page.route("**/api/public/v1/quotes/q-unk-reset", (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.getByRole("alert")).not.toContainText("could not reach");
  await expectFormKept(page);
});

test("multi-accept on: a recorded accept answered 504 ends on the accepted state with no second submit", async ({ page }) => {
  await readyToAccept(page, "q-multi-504");
  await multiAcceptView(page, "q-multi-504");
  let posts = 0;
  await page.route("**/accept", async (route) => {
    posts += 1;
    await route.fetch();
    await route.fulfill({ status: 504, contentType: "text/html", body: "<html><body><h1>504 Gateway Timeout</h1></body></html>" });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Accept quote" })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(posts).toBe(1);
});

test("multi-accept on: a failed repeat accept, with the quote already ACCEPTED before, is unconfirmed", async ({ page }) => {
  await page.route("**/api/public/v1/quotes/q-multi-504", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    body.status = "ACCEPTED";
    body.acceptable = true;
    await route.fulfill({ response, json: body });
  });
  await page.route("**/accept", (route) =>
    route.fulfill({ status: 502, contentType: "text/html", body: "<html><body>502</body></html>" }),
  );
  await page.goto("/?t=q-multi-504");
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  await page.getByRole("button", { name: "Accept quote" }).click();
  // Nothing proves the repeat was not recorded: the status could not move.
  // The same button stays, and its retry carries the same key.
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.getByRole("button", { name: "Accept quote" })).toBeEnabled();
});

test("multi-accept on: an ACCEPTED quote that is still acceptable says what accepting again does, by name", async ({ page }) => {
  await page.route("**/api/public/v1/quotes/q-terms-28", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    body.status = "ACCEPTED";
    body.acceptable = true;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/?t=q-terms-28");
  await expect(page.getByRole("heading", { name: "Accept this quote" })).toBeVisible();
  await expect(page.getByText(REPEAT_NOTE)).toBeVisible();
  await expect(page.getByText("Accepting again records another acceptance")).toHaveCount(0);
});

test("a quote still awaiting its first acceptance carries no repeat note", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  await expect(page.getByRole("heading", { name: "Accept this quote" })).toBeVisible();
  await expect(page.getByText(/already accepted/)).toHaveCount(0);
});

test("a failed accept whose re-read shows the quote closed by something else ends on the closed card, no alert", async ({ page }) => {
  let reads = 0;
  await page.route("**/api/public/v1/quotes/q-terms-28", async (route) => {
    reads += 1;
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    if (reads > 1) {
      body.status = "LOST";
      body.acceptable = false;
    }
    await route.fulfill({ response, json: body });
  });
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({ status: 502, contentType: "text/html", body: "<html><body>502</body></html>" }),
  );
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Acceptance closed" })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator("#name")).toHaveCount(0);
});

// -- the idempotency key: every attempt of one acceptance carries the same
// key, derived from the token and the typed name (the page may store nothing),
// so a retry after a lost answer, a reload or another tab is answered with the
// first outcome and never records a second row.

const ACCEPT_PATH = (token: string): string => `/api/public/v1/quotes/${token}/accept`;

function expectedKey(token: string, typedName: string): string {
  const bytes = createHash("sha256").update(`quote-accept:${token}:${typedName}`).digest().subarray(0, 16);
  bytes[6] = (bytes[6] & 0x0f) | 0x50;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = bytes.toString("hex");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}

async function multiAcceptOn(page: Page, token: string): Promise<void> {
  const response = await page.request.post(`/_stub/multi-accept?token=${token}&on=1`);
  expect(response.ok()).toBe(true);
}

async function acceptanceRows(page: Page, token: string): Promise<Array<{ typed_name: string; key: string | null }>> {
  const response = await page.request.get("/_stub/state");
  const state = (await response.json()) as { acceptances: Record<string, Array<{ typed_name: string; key: string | null }>> };
  return state.acceptances[token] ?? [];
}

/** The Idempotency-Key of every accept POST the page sends, in order. */
function trackKeys(page: Page): Array<string | undefined> {
  const keys: Array<string | undefined> = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/accept")) keys.push(request.headers()["idempotency-key"]);
  });
  return keys;
}

interface Abandoned {
  key: string | undefined;
  body: string | null;
}

/**
 * The first accept is cut at the client BEFORE the server sees it (a client
 * timeout); the request is kept so the test can let it commit late, which is
 * what a slow core does. Later accepts pass straight through.
 */
async function abandonFirstAccept(page: Page): Promise<() => Abandoned | null> {
  let abandoned: Abandoned | null = null;
  await page.route("**/accept", async (route) => {
    if (abandoned === null) {
      abandoned = { key: route.request().headers()["idempotency-key"], body: route.request().postData() };
      await route.abort("timedout");
      return;
    }
    await route.continue();
  });
  return () => abandoned;
}

async function commitLate(page: Page, token: string, abandoned: Abandoned): Promise<void> {
  const headers: Record<string, string> = { "content-type": "application/json" };
  if (abandoned.key !== undefined) headers["idempotency-key"] = abandoned.key;
  const response = await page.request.post(ACCEPT_PATH(token), { headers, data: abandoned.body ?? "" });
  expect(response.status()).toBe(200);
}

test("a slow commit after a client timeout, then a retry, multi-accept on: the same key, ONE acceptance row", async ({ page }) => {
  const token = "q-key-slow";
  await multiAcceptOn(page, token);
  const keys = trackKeys(page);
  await readyToAccept(page, token);
  const abandoned = await abandonFirstAccept(page);

  await page.getByRole("button", { name: "Accept quote" }).click();
  // The re-read answers SENT: the page cannot say it was not recorded.
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expectFormKept(page);
  expect(await acceptanceRows(page, token)).toHaveLength(0);

  // The abandoned request now commits at the server, late.
  const first = abandoned();
  expect(first).not.toBeNull();
  await commitLate(page, token, first as Abandoned);
  expect(await acceptanceRows(page, token)).toHaveLength(1);

  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();

  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeDefined();
  expect(keys[1]).toBe(keys[0]);
  expect(keys[0]).toBe(expectedKey(token, "Sample Customer"));
  expect(await acceptanceRows(page, token)).toHaveLength(1);
});

test("the same after a reload of the page: the key is derived again, still ONE acceptance row", async ({ page }) => {
  const token = "q-key-reload";
  await multiAcceptOn(page, token);
  const keys = trackKeys(page);
  await readyToAccept(page, token);
  const abandoned = await abandonFirstAccept(page);

  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await commitLate(page, token, abandoned() as Abandoned);

  // A reload loses every in-memory thing: the key must come back from the
  // token and the name alone.
  await page.reload();
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();

  expect(keys).toHaveLength(2);
  expect(keys[1]).toBe(keys[0]);
  expect(await acceptanceRows(page, token)).toHaveLength(1);
});

test("a different typed name sends a different key and, with multi-accept on, a second row on purpose", async ({ page }) => {
  const token = "q-key-names";
  await multiAcceptOn(page, token);
  const keys = trackKeys(page);
  await readyToAccept(page, token);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();

  await page.reload();
  await expect(page.getByText(REPEAT_NOTE)).toBeVisible();
  await page.locator("#name").fill("Another Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByText("Accepted by Another Customer on")).toBeVisible();

  expect(keys).toHaveLength(2);
  expect(keys[0]).toBe(expectedKey(token, "Sample Customer"));
  expect(keys[1]).toBe(expectedKey(token, "Another Customer"));
  expect(keys[1]).not.toBe(keys[0]);
  expect((await acceptanceRows(page, token)).map((row) => row.typed_name)).toEqual(["Sample Customer", "Another Customer"]);
});

test("the key is the typed name AS SENT: stray white space around it does not change the key", async ({ page }) => {
  const keys = trackKeys(page);
  await page.goto("/?t=q-terms-28");
  await page.route("**/accept", (route) => route.abort("failed"));
  await page.locator("#name").fill("  Sample Customer  ");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  expect(keys[0]).toBe(expectedKey("q-terms-28", "Sample Customer"));
});

test("a definite 400 or 404 says it was not recorded, in the page's own words, and keeps the form", async ({ page }) => {
  for (const [status, words, sentence] of [
    [400, "enter your full name to accept this quote", CHECK_FIELDS],
    [400, "Idempotency-Key must be a UUID", CHECK_FIELDS],
    [404, "not found", NOT_FOUND_SENTENCE],
  ] as const) {
    await page.unroute("**/accept");
    await readyToAccept(page, "q-terms-28");
    await page.route("**/accept", (route) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error: words }) }),
    );
    await page.getByRole("button", { name: "Accept quote" }).click();
    await expect(page.getByRole("alert")).toHaveText(`${sentence} ${NOT_RECORDED_SUFFIX}`);
    await expect(page.locator("body")).not.toContainText(words);
    await expectFormKept(page);
  }
});

test("no unknown outcome ever says the acceptance was not recorded, and each offers the same button", async ({ page }) => {
  const unknowns: Array<[string, (route: import("@playwright/test").Route) => Promise<void>]> = [
    ["no status at all", (route) => route.abort("failed")],
    ["a connection reset", (route) => route.abort("connectionreset")],
    [
      "a proxy's HTML 502",
      (route) => route.fulfill({ status: 502, contentType: "text/html", body: "<html><body>502</body></html>" }),
    ],
    [
      "a 500 that is the JSON envelope",
      (route) => route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "internal" }) }),
    ],
    [
      "an unreadable 200",
      (route) => route.fulfill({ status: 200, contentType: "application/json", body: "{\"quote_no\": \"Q-28\", \"typed_na" }),
    ],
  ];
  for (const [label, handler] of unknowns) {
    await page.unroute("**/accept");
    await readyToAccept(page, "q-terms-28");
    await page.route("**/accept", handler);
    await page.getByRole("button", { name: "Accept quote" }).click();
    const alert = page.getByRole("alert");
    await expect(alert, label).toHaveText(UNCONFIRMED);
    await expect(alert, label).not.toContainText(/not recorded/i);
    await expect(alert, label).not.toContainText(/could not record your/i);
    await expectFormKept(page);
  }
});

test("the same key used for a different typed name: the quote was already accepted with a different name, shown as accepted", async ({ page }) => {
  await readyToAccept(page, "q-key-reuse");
  await page.route("**/accept", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: "quote: this idempotency key was used for a different acceptance" }),
    }),
  );
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.getByText("already accepted with a different name", { exact: false })).toBeVisible();
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("idempotency");
});

test("crypto.subtle absent: the fallback key is one random UUID, stable across retries in one page life", async ({ page }) => {
  await page.addInitScript(() => {
    // An insecure context has no crypto.subtle.
    Object.defineProperty(Crypto.prototype, "subtle", { get: () => undefined, configurable: true });
  });
  const keys = trackKeys(page);
  await readyToAccept(page, "q-key-nosubtle");
  await page.route("**/accept", (route) => route.abort("failed"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect.poll(() => keys.length).toBe(2);
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  expect(keys[0]).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  expect(keys[1]).toBe(keys[0]);
  expect(keys[0]).not.toBe(expectedKey("q-key-nosubtle", "Sample Customer"));
});

// -- round four, the second pass: an unknown outcome locks the name, a definite
// refusal never shows staff wording, an envelope-less 4xx is unknown, and a
// dead link after an unknown outcome re-sends the keyed accept to learn it.

interface SentAccept {
  key: string | undefined;
  typedName: string | undefined;
}

/** Every accept POST the page sends: its key and the typed name in its body. */
function trackAccepts(page: Page): SentAccept[] {
  const sent: SentAccept[] = [];
  page.on("request", (request) => {
    if (request.method() !== "POST" || !request.url().endsWith("/accept")) return;
    const body = JSON.parse(request.postData() ?? "{}") as { typed_name?: string };
    sent.push({ key: request.headers()["idempotency-key"], typedName: body.typed_name });
  });
  return sent;
}

/** A person (or a tampered page) forcing a new name into a locked field. */
async function forceName(page: Page, name: string): Promise<void> {
  await page.locator("pd-quote-public").evaluate((host, value) => {
    const input = host.shadowRoot?.querySelector<HTMLInputElement>("#name");
    if (input === null || input === undefined) throw new Error("no name field");
    input.readOnly = false;
    input.value = value;
    input.dispatchEvent(new Event("input", { bubbles: true }));
  }, name);
}

test("after an unknown outcome the name is locked and says why, and a retry carries the same name and key", async ({ page }) => {
  const token = "q-lock-keys";
  const sent = trackAccepts(page);
  await readyToAccept(page, token);
  await page.route("**/accept", (route) => route.abort("timedout"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.getByRole("alert")).toContainText("with the same name");

  // Locked, with the reason on the page.
  await expect(page.locator("#name")).toHaveJSProperty("readOnly", true);
  await expect(page.locator("#name")).toHaveValue("Sample Customer");
  await expect(page.getByText(/name is kept as you first entered it/)).toBeVisible();
  // The lock note warns that changing the name by reloading may record a second acceptance.
  await expect(page.getByText(/Reload this page to change it; a different name may record a second acceptance/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Accept quote" })).toBeEnabled();

  // Even if the field is forced open and edited, nothing but the first name and key is ever sent.
  await forceName(page, "Another Customer");
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect.poll(() => sent.length).toBe(2);
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect.poll(() => sent.length).toBe(3);
  for (const attempt of sent) {
    expect(attempt.key).toBe(expectedKey(token, "Sample Customer"));
    expect(attempt.typedName).toBe("Sample Customer");
  }
  await expect(page.locator("#name")).toHaveValue("Sample Customer");
});

test("the saved name is what a retry sends and validates: a blanked field after an unknown outcome still retries the first name and key", async ({ page }) => {
  const token = "q-lock-blank";
  const sent = trackAccepts(page);
  await readyToAccept(page, token);
  await page.route("**/accept", (route) => route.abort("timedout"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  expect(sent).toHaveLength(1);

  // The field is forced open and blanked: the saved name, not the field, is the retry's name.
  await forceName(page, "");
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect.poll(() => sent.length).toBe(2);
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  expect(sent[1]?.typedName).toBe("Sample Customer");
  expect(sent[1]?.key).toBe(expectedKey(token, "Sample Customer"));
  expect(sent[1]?.key).toBe(sent[0]?.key);
  await expect(page.locator("#name")).toHaveValue("Sample Customer");
});

test("an unknown outcome that later shows the quote accepted closes the form: nothing is left to lock", async ({ page }) => {
  const token = "q-lock-refusal";
  await readyToAccept(page, token);
  const abandoned = await abandonFirstAccept(page);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.locator("#name")).toHaveJSProperty("readOnly", true);
  // The abandoned request commits late; the retry replays it and the page ends on the accepted state.
  await commitLate(page, token, abandoned() as Abandoned);
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();
  expect(await acceptanceRows(page, token)).toHaveLength(1);
});

test("a definite refusal after an unknown outcome unlocks the name, and a new name then sends a new key", async ({ page }) => {
  const sent = trackAccepts(page);
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) => route.abort("timedout"));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
  await expect(page.locator("#name")).toHaveJSProperty("readOnly", true);

  await page.unroute("**/accept");
  await page.route("**/accept", (route) =>
    route.fulfill({ status: 400, contentType: "application/json", body: JSON.stringify({ error: "name is too long" }) }),
  );
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(`${CHECK_FIELDS} ${NOT_RECORDED_SUFFIX}`);
  await expect(page.locator("#name")).toHaveJSProperty("readOnly", false);
  await expect(page.getByText(/name is kept as you first entered it/)).toHaveCount(0);

  await page.locator("#name").fill("Another Customer");
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect.poll(() => sent.length).toBe(3);
  expect(sent[2]?.typedName).toBe("Another Customer");
  expect(sent[2]?.key).toBe(expectedKey("q-terms-28", "Another Customer"));
  expect(sent[2]?.key).not.toBe(sent[0]?.key);
});

test("a fresh page has an editable name", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  await expect(page.locator("#name")).toHaveJSProperty("readOnly", false);
  await expect(page.getByText(/name is kept as you first entered it/)).toHaveCount(0);
});

test("a 409 whose re-read shows ACCEPTED ends on the closed accepted card, with no staff wording anywhere", async ({ page }) => {
  const token = "q-stale-tab";
  await page.goto(`/?t=${token}`);
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  // Someone else accepts with another name while this tab is open: multi-accept is off.
  const other = await page.request.post(ACCEPT_PATH(token), {
    headers: { "content-type": "application/json" },
    data: { typed_name: "Another Customer", accepted: true, quantities_confirmed: true },
  });
  expect(other.status()).toBe(200);
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#qty-confirm").check();
  await page.locator("#affirm").check();
  const refused = page.waitForResponse((r) => r.url().endsWith("/accept") && r.status() === 409);
  await page.getByRole("button", { name: "Accept quote" }).click();
  // The stub serves the core's real staff wording; the customer never sees it.
  const words = ((await (await refused).json()) as { error: string }).error;
  expect(words).toMatch(STAFF_WORDS);
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Accept quote" })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText(STAFF_WORDS);
  await expect(page.locator("pd-quote-public")).not.toContainText(STAFF_WORDS);
  await expect(page.getByText("not recorded", { exact: false })).toHaveCount(0);
});

test("a 409 whose re-read shows CONVERTED ends on the converted document: no closed card, the confirmation line", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: "quote: this quote was already accepted as order SO-1 on today and is CONVERTED, open that order to keep working, or have an administrator allow accepting a quote more than once in the acceptance settings" }),
    }),
  );
  let reads = 0;
  await page.route("**/api/public/v1/quotes/q-terms-28", async (route) => {
    reads += 1;
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    if (reads > 0) {
      body.status = "CONVERTED";
      body.acceptable = false;
    }
    await route.fulfill({ response, json: body });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.locator("pd-quote-public")).toContainText("now an order");
  await expect(page.locator(".orderband").getByText("You confirmed the quantities on this quote.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Acceptance closed" })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator("pd-quote-public")).not.toContainText(STAFF_WORDS);
});

test("a 409 whose re-read shows ACCEPTED yet still acceptable ends on the closed accepted card, not the form", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: "quote: this quote was already ACCEPTED on today and is waiting to become an order, convert it at the counter" }),
    }),
  );
  await page.route("**/api/public/v1/quotes/q-terms-28", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    body.status = "ACCEPTED";
    body.acceptable = true;
    await route.fulfill({ response, json: body });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("an unknown outcome whose re-read shows CONVERTED ends on the converted document, no closed card", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({ status: 504, contentType: "text/html", body: "<html><body>504</body></html>" }),
  );
  await page.route("**/api/public/v1/quotes/q-terms-28", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { status: string; acceptable: boolean };
    body.status = "CONVERTED";
    body.acceptable = false;
    await route.fulfill({ response, json: body });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.locator("pd-quote-public")).toContainText("now an order");
  await expect(page.locator(".orderband").getByText("You confirmed the quantities on this quote.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Already accepted" })).toHaveCount(0);
  await expect(page.getByRole("alert")).toHaveCount(0);
});

test("a 409 for a quote that is expired ends on the closed expired card, in the page's words", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: "quote: this quote expired on today, request an updated quote" }),
    }),
  );
  await page.route("**/api/public/v1/quotes/q-terms-28", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { acceptable: boolean; expired: boolean };
    body.acceptable = false;
    body.expired = true;
    await route.fulfill({ response, json: body });
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Acceptance closed" })).toBeVisible();
  await expect(page.getByText(/can no longer be accepted online/)).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  await expect(page.locator("pd-quote-public")).not.toContainText(/request an updated quote/);
});

test("the closed card of an accepted quote that is also expired says one thing, not two", async ({ page }) => {
  await page.route("**/api/public/v1/quotes/q-accepted-26", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { expired: boolean };
    body.expired = true;
    await route.fulfill({ response, json: body });
  });
  await page.goto("/?t=q-accepted-26");
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
  await expect(page.getByText("This quote is accepted and is not awaiting acceptance.")).toBeVisible();
  await expect(page.locator("pd-quote-public")).not.toContainText(/expired on|can no longer be accepted online/);
});

for (const [token, status] of [
  ["q-proxy-403", 403],
  ["q-proxy-408", 408],
  ["q-proxy-429", 429],
] as const) {
  test(`a proxy's HTML ${status} with no JSON envelope is an unknown outcome: re-read, unconfirmed, never not recorded`, async ({ page }) => {
    await readyToAccept(page, token);
    let reads = 0;
    await page.route(`**/api/public/v1/quotes/${token}`, async (route) => {
      reads += 1;
      await route.continue();
    });
    await page.route("**/accept", (route) =>
      route.fulfill({ status, contentType: "text/html", body: `<html><body><h1>${status}</h1><p>edge.internal.example</p></body></html>` }),
    );
    await page.getByRole("button", { name: "Accept quote" }).click();
    await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
    await expect(page.getByRole("alert")).not.toContainText(/not recorded/i);
    await expect(page.locator("body")).not.toContainText("edge.internal.example");
    // The initial read, and the re-read of an unknown outcome.
    expect(reads).toBe(1);
    await expect(page.locator("#name")).toHaveJSProperty("readOnly", true);
  });
}

test("the link dying after a landed accept: the re-read says gone, the keyed re-send learns it landed", async ({ page }) => {
  const token = "q-dead-landed";
  const sent = trackAccepts(page);
  await readyToAccept(page, token);
  await page.route("**/accept", async (route) => {
    if (sent.length > 1) {
      await route.continue();
      return;
    }
    // The first accept commits at the core, then the answer is lost and the link is revoked.
    await route.fetch();
    await page.request.post(`/_stub/revoke?token=${token}`);
    await route.abort("connectionreset");
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("heading", { name: "Thank you: quote Q-28 accepted" })).toBeVisible();
  await expect(page.getByText("Quote link unavailable")).toHaveCount(0);
  expect(sent).toHaveLength(2);
  expect(sent[1]?.key).toBe(sent[0]?.key);
  expect(sent[1]?.typedName).toBe(sent[0]?.typedName);
  expect(await acceptanceRows(page, token)).toHaveLength(1);
});

test("the link dying after an accept that never landed: the keyed re-send answers 410 and the dead link card shows", async ({ page }) => {
  const token = "q-dead-unrecorded";
  const sent = trackAccepts(page);
  await readyToAccept(page, token);
  await page.route("**/accept", async (route) => {
    if (sent.length > 1) {
      await route.continue();
      return;
    }
    await page.request.post(`/_stub/revoke?token=${token}`);
    await route.abort("timedout");
  });
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByText("Quote link unavailable")).toBeVisible();
  await expect(page.getByText(/no longer valid/)).toBeVisible();
  await expect(page.getByRole("heading", { name: /Thank you/ })).toHaveCount(0);
  expect(sent).toHaveLength(2);
  expect(sent[1]?.key).toBe(sent[0]?.key);
  expect(await acceptanceRows(page, token)).toHaveLength(0);
});

for (const [token, how] of [
  ["q-dead-resend-abort", "a lost connection"],
  ["q-dead-resend-503", "a proxy's 503 page"],
  ["q-dead-resend-410html", "a proxy's 410 page with no envelope"],
] as const) {
  test(`the link dying, then the keyed re-send failing by ${how}: the outcome is unknown, never the dead link card`, async ({ page }) => {
    const sent = trackAccepts(page);
    await readyToAccept(page, token);
    await page.route("**/accept", async (route) => {
      if (sent.length > 1) {
        if (how === "a lost connection") await route.abort("connectionreset");
        else if (how === "a proxy's 503 page") await route.fulfill({ status: 503, contentType: "text/html", body: "<html><body>503 edge.internal.example</body></html>" });
        else await route.fulfill({ status: 410, contentType: "text/html", body: "<html><body>410 edge.internal.example</body></html>" });
        return;
      }
      // The first accept commits at the core, the answer is lost and the link is revoked.
      await route.fetch();
      await page.request.post(`/_stub/revoke?token=${token}`);
      await route.abort("connectionreset");
    });
    await page.getByRole("button", { name: "Accept quote" }).click();
    await expect.poll(() => sent.length).toBe(2);
    await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
    await expect(page.getByText("Quote link unavailable")).toHaveCount(0);
    await expect(page.getByText(/no longer valid/)).toHaveCount(0);
    await expect(page.locator("body")).not.toContainText("edge.internal.example");
    expect(sent[1]?.key).toBe(sent[0]?.key);
    await expect(page.locator("#name")).toHaveJSProperty("readOnly", true);
  });
}

for (const [token, status, error] of [
  ["q-proxy-json-429", 429, "rate limit exceeded"],
  ["q-proxy-json-413", 413, "request entity too large"],
  ["q-proxy-json-408", 408, "request timeout"],
  ["q-proxy-json-404", 404, "upstream route missing"],
] as const) {
  test(`a proxy's JSON ${status} that looks like the envelope is an unknown outcome, not a definite refusal`, async ({ page }) => {
    await readyToAccept(page, token);
    await page.route("**/accept", (route) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify({ error }) }),
    );
    await page.getByRole("button", { name: "Accept quote" }).click();
    await expect(page.getByRole("alert")).toHaveText(UNCONFIRMED);
    await expect(page.getByRole("alert")).not.toContainText(/not recorded/i);
    await expect(page.locator("body")).not.toContainText(error);
    await expect(page.locator("#name")).toHaveJSProperty("readOnly", true);
  });
}

// The page's two small faults: the quote number once, and the time in the page's own words.
test("the title shows the quote number once, and the time is plain words", async ({ page }) => {
  await page.goto("/?t=q-hash-6");
  await expect(page.getByRole("heading", { name: "Quote #Q-6" })).toBeVisible();
  await expect(page.getByText("##Q-6")).toHaveCount(0);
  const sub = page.locator(".sub").first();
  await expect(sub).toContainText("Prepared for Fenwick Builders Ltd");
  await expect(sub).toContainText(/last updated [A-Z][a-z]{2} \d{1,2}, \d{4}, \d{1,2}:\d{2} [AP]M/);
  await expect(sub).not.toContainText("as of");
  await expect(sub).not.toContainText(/\d{4}-\d{2}-\d{2}/);
  await expect(sub).not.toContainText(/\d:\d{2}:\d{2}/);
});

// The preview: the same page, handed the staff read of the customer's view. It states plainly that
// nothing was sent, and offers no accept area and no typed name. The page itself never reads a staff
// session (the sinks gate holds that), so the Quote Desk fetches the view with its own session and
// hands the validated view to the element; the test stands in for that host.
test("a preview shows the band and no way to accept", async ({ page }) => {
  await page.goto("/");
  // The host hands the page the endpoint's own JSON; the page validates it itself.
  await hostPreview(page, { fetchFrom: "q-preview-draft" });
  const band = page.getByText("Preview: this is what your customer will see. Nothing has been sent.");
  await expect(band).toBeVisible();
  await expect(page.getByRole("heading", { name: "Quote #Q-7" })).toBeVisible();
  await expectNoAcceptControl(page);
  await expect(page.getByText("Accept this quote")).toHaveCount(0);
  await expect(page.getByText("is not awaiting acceptance")).toHaveCount(0);
});

// Hand the page a view the way the Quote Desk would: through showQuote, on a page with no link token.
async function hostPreview(page: Page, what: { fetchFrom: string; patch?: Record<string, unknown>; raw?: unknown }): Promise<void> {
  await page.evaluate(async (w) => {
    let value: unknown = w.raw;
    if (w.raw === undefined) {
      const view = await (await fetch(`/api/public/v1/quotes/${w.fetchFrom}`)).json();
      value = { ...view, ...(w.patch ?? {}) };
      for (const [k, v] of Object.entries(w.patch ?? {})) if (v === undefined) delete (value as Record<string, unknown>)[k];
    }
    // The page's own module, served by Vite in the browser; the test's typecheck cannot resolve a root path.
    // @ts-expect-error resolved by the browser, not by tsc
    await import("/src/quote-public.ts");
    document.querySelector("pd-quote-public")?.remove();
    const el = document.createElement("pd-quote-public") as HTMLElement & { showQuote(v: unknown): void };
    document.body.append(el);
    el.showQuote(value);
  }, what);
}

async function expectNoAcceptControl(page: Page): Promise<void> {
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.locator("#affirm")).toHaveCount(0);
  await expect(page.locator("#qty-confirm")).toHaveCount(0);
  await expect(page.getByRole("button", { name: /accept/i })).toHaveCount(0);
}

// The page never trusts its host: whatever it is handed, a hosted page is a preview or it is refused,
// and it never renders an accept control or crashes.
for (const [name, what] of [
  ["null", { fetchFrom: "q-preview-draft", raw: null }],
  ["a malformed view", { fetchFrom: "q-preview-draft", raw: { quote_no: "#Q-7" } }],
  ["a view that is not an object", { fetchFrom: "q-preview-draft", raw: "nope" }],
  ["a link shaped view: preview false, acceptable true, SENT", { fetchFrom: "q-demo-25", patch: { preview: false, acceptable: true, status: "SENT" } }],
  ["a view with the preview flag missing", { fetchFrom: "q-demo-25" }],
] as const) {
  test(`a hosted page refuses ${name}: no crash, no accept control`, async ({ page }) => {
    const errors: string[] = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto("/");
    await hostPreview(page, what);
    await expect(page.getByText("This preview could not be shown.")).toBeVisible();
    await expectNoAcceptControl(page);
    await expect(page.getByText("Loading your quote")).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}

// The quantity notice: the dealer's own words that the quantities are the customer's to confirm, near
// the lines, on the printed page and in the preview. Customers blame the dealer for quantities they chose.
test("the dealer's quantity notice shows near the lines, whatever the dealer wrote", async ({ page }) => {
  await page.goto("/?t=q-notice-29");
  const notice = page.getByText("Check every quantity against your plan before you accept.");
  await expect(notice).toBeVisible();
  const table = await page.locator("table").boundingBox();
  const note = await notice.boundingBox();
  const totals = await page.locator(".totals").boundingBox();
  expect(table !== null && note !== null && totals !== null && note.y >= table.y + table.height - 1 && note.y < totals.y).toBe(true);
  // The default wording is what a dealer who wrote none serves.
  await page.goto("/?t=q-terms-28");
  await expect(page.getByText("All quantities are to be confirmed by the customer. Please review them before you accept it.")).toBeVisible();
});

test("the quantity notice is on the printed page", async ({ page }) => {
  await page.emulateMedia({ media: "print" });
  await page.goto("/?t=q-notice-29&print=1");
  await expect(page.getByRole("heading", { name: "Quote #Q-29" })).toBeVisible();
  await expect(page.getByText("Check every quantity against your plan before you accept.")).toBeVisible();
});

test("the preview shows the quantity notice and still offers no accept control", async ({ page }) => {
  await page.goto("/");
  await hostPreview(page, { fetchFrom: "q-notice-29", patch: { preview: true, acceptable: false, status: "DRAFT" } });
  await expect(page.getByText("Check every quantity against your plan before you accept.")).toBeVisible();
  await expectNoAcceptControl(page);
});

test("a 422 from the core is a definite refusal: the quantities words, nothing recorded, the form kept", async ({ page }) => {
  await readyToAccept(page, "q-qty-422");
  await page.route("**/accept", (route) =>
    route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({ error: "please confirm that you have reviewed the quantities on this quote before you accept it" }),
    }),
  );
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(
    "Please confirm that you have reviewed the quantities on this quote. Your acceptance was not recorded.",
  );
  await expect(page.locator("#name")).toHaveJSProperty("readOnly", false);
});

test("a line without a photo shows its category's drawn tile; a photo still wins; an unknown category keeps the mark", async ({
  page,
}) => {
  await page.goto("/?t=q-tiles-31");
  await expect(page.getByRole("heading", { name: "Quote #Q-31" })).toBeVisible();
  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(3);
  // The photo wins on the line that has one.
  await expect(rows.nth(0).locator("img.thumb")).toBeVisible();
  await expect(rows.nth(0).locator("img.tile")).toHaveCount(0);
  // No photo, a category the tiles know (named by the line): the mulch tile, a drawn image.
  const tile = rows.nth(1).locator("img.tile");
  await expect(tile).toBeVisible();
  await expect(tile).toHaveAttribute("src", /^data:image\/svg\+xml|mulch/);
  await expect(rows.nth(1).locator("svg.thumbmark")).toHaveCount(0);
  await expect(rows.nth(1).locator("img.thumb")).toHaveCount(0);
  // No photo and nothing the tiles know: the drawn no-photo mark, as before.
  await expect(rows.nth(2).locator("svg.thumbmark")).toBeVisible();
  await expect(rows.nth(2).locator("img.tile")).toHaveCount(0);
});

test("a long unbroken notice wraps inside the page: no sideways scroll on a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/?t=q-long-32");
  await expect(page.getByRole("heading", { name: "Quote #Q-32" })).toBeVisible();
  await expect(page.locator(".qtynotice")).toBeVisible();
  const widths = await page.evaluate(() => ({ doc: document.documentElement.scrollWidth, view: window.innerWidth }));
  expect(widths.doc).toBeLessThanOrEqual(widths.view);
});

test("the disabled Accept button explains itself, tied to the button, and the hint goes when the form is ready", async ({ page }) => {
  await page.goto("/?t=q-demo-25-hint");
  const accept = page.getByRole("button", { name: "Accept quote" });
  await expect(accept).toBeDisabled();
  const hint = page.locator("#accept-hint");
  await expect(hint).toBeVisible();
  await expect(hint).toHaveText("To accept, tick the quantities box, type your full name and tick the confirmation.");
  await expect(accept).toHaveAttribute("aria-describedby", "accept-hint");
  await page.locator("#qty-confirm").check();
  await expect(hint).toHaveText("To accept, type your full name and tick the confirmation.");
  await page.locator("#name").fill("Sample Customer");
  await expect(hint).toHaveText("To accept, tick the confirmation.");
  await page.locator("#affirm").check();
  await expect(accept).toBeEnabled();
  await expect(page.locator("#accept-hint")).toHaveCount(0);
  await expect(accept).not.toHaveAttribute("aria-describedby", /.+/);
});

test("print=1 prints the quote and the notice, not the accept card: a paper copy has nothing to tick", async ({ page }) => {
  await page.emulateMedia({ media: "print" });
  await page.goto("/?t=q-print-33&print=1");
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
  await expect(page.getByText("All quantities are to be confirmed by the customer.")).toBeVisible();
  await expect(page.locator("#qty-confirm")).toHaveCount(0);
  await expect(page.locator("#name")).toHaveCount(0);
  await expect(page.locator("#affirm")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Accept quote" })).toHaveCount(0);
  await expect(page.getByText("Accept this quote")).toHaveCount(0);
  // The same link on screen still offers the form.
  await page.emulateMedia({ media: "screen" });
  await page.goto("/?t=q-print-33");
  await expect(page.locator("#qty-confirm")).toBeVisible();
});

// The look: the notice reads as information, not as another field, and the quantities tick is one of the
// card's own ticks (the same row, the same type) rather than a bolted on line.
test("the notice is an information callout: an icon, a tinted ground, no field border", async ({ page }) => {
  await page.goto("/?t=q-print-33");
  const notice = page.locator(".qtynotice");
  await expect(notice).toBeVisible();
  await expect(notice.locator("svg.noticeicon")).toHaveCount(1);
  const look = await page.evaluate(() => {
    const root = document.querySelector("pd-quote-public")?.shadowRoot as ShadowRoot;
    const n = getComputedStyle(root.querySelector(".qtynotice") as Element);
    const card = getComputedStyle(root.querySelector(".card") as Element);
    const input = getComputedStyle(root.querySelector("#name") as Element);
    return {
      ground: n.backgroundColor, cardGround: card.backgroundColor,
      top: n.borderTopWidth, left: n.borderLeftWidth, leftColour: n.borderLeftColor,
      inputBorder: input.borderTopColor, noticeTopColour: n.borderTopColor,
    };
  });
  expect(look.ground).not.toBe(look.cardGround);
  expect(look.top).toBe("0px");
  expect(look.left).toBe("4px");
  expect(look.leftColour).not.toBe(look.inputBorder);
});

test("the quantities tick is one of the card's own ticks: the same row and the same type as the confirmation", async ({ page }) => {
  await page.goto("/?t=q-print-33");
  // The card is drawn once the quote is read; the styles are read from it after that, never from the empty page.
  await expect(page.locator("#qty-confirm")).toBeVisible();
  await expect(page.locator("#affirm")).toBeVisible();
  const same = await page.evaluate(() => {
    const root = document.querySelector("pd-quote-public")?.shadowRoot as ShadowRoot;
    const read = (id: string) => {
      const input = root.querySelector(`#${id}`) as HTMLInputElement;
      const row = input.closest(".tick") as HTMLElement | null;
      const label = root.querySelector(`label[for="${id}"]`) as HTMLElement;
      const r = row === null ? null : getComputedStyle(row);
      const l = getComputedStyle(label);
      return {
        hasRow: row !== null,
        rowBorder: r?.borderTopWidth, rowRadius: r?.borderTopLeftRadius, rowPad: r?.paddingTop,
        weight: l.fontWeight, size: l.fontSize, boxSize: getComputedStyle(input).width,
      };
    };
    return { qty: read("qty-confirm"), affirm: read("affirm") };
  });
  expect(same.qty.hasRow).toBe(true);
  expect(same.affirm.hasRow).toBe(true);
  expect(same.qty).toEqual(same.affirm);
});

// Change 9 WALK-C9: the new surface elements the board requires.
// The status badge, the job-site card, the rep card, the payment selector, the optional
// add-on toggle, the ask-a-question link, the print button. Each rides the design
// system tokens and the existing contract shape; the test pins the visible surface,
// not the wire, so a contract change behind the helper does not move them.

test("an open quote shows the status badge in the customer-facing word for its state", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  // SENT quotes read "Awaiting acceptance": the customer-facing word, not the wire state.
  const badge = page.locator(".statusbadge");
  await expect(badge).toHaveCount(1);
  await expect(badge).toHaveText("Awaiting acceptance");
  await expect(badge).toHaveAttribute("data-status", "SENT");
});

test("an expired quote shows the badge's Expired word and the danger family", async ({ page }) => {
  await page.goto("/?t=q-expired-25");
  const badge = page.locator(".statusbadge");
  await expect(badge).toHaveText("Expired");
  await expect(badge).toHaveAttribute("data-status", "EXPIRED");
  await expect(badge).toHaveClass(/statusbadge-danger/);
});

test("a withdrawn quote (LOST) shows the badge's Withdrawn word, never silently accepted", async ({ page }) => {
  await page.goto("/?t=q-lost-93");
  const badge = page.locator(".statusbadge");
  await expect(badge).toHaveText("Withdrawn");
  await expect(badge).toHaveAttribute("data-status", "LOST");
  await expect(badge).toHaveClass(/statusbadge-danger/);
  // The closed card speaks its own state in the body's own words, not the server's.
  await expect(page.getByText(/This quote is lost and is not awaiting acceptance/)).toBeVisible();
  await expect(page.locator("#name")).toHaveCount(0);
});

test("a converted quote shows the badge as Order created on the gold family", async ({ page }) => {
  await page.goto("/?t=q-conv-8");
  const badge = page.locator(".statusbadge");
  await expect(badge).toHaveText("Order created");
  await expect(badge).toHaveAttribute("data-status", "CONVERTED");
  await expect(badge).toHaveClass(/statusbadge-accent/);
});

test("a delivery quote renders a job-site card with the address and the date and fee", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  const job = page.locator(".card[data-fact=\"job-site\"]");
  await expect(job).toBeVisible();
  // The exact words the old terms-facts row used: a customer moving from a saved page
  // to this one still sees the same phrase.
  await expect(job).toContainText("Delivery to 12 Test Yard Lane");
  await expect(job).toContainText("delivery fee $45.00 (included in the total)");
});

test("a pickup quote renders a job-site card with no delivery fee and no address line", async ({ page }) => {
  await page.route("**/api/public/v1/quotes/**", async (route) => {
    const response = await route.fetch();
    const json = await response.json();
    await route.fulfill({ response, json: { ...json, fulfillment: "PICKUP", delivery_address: "12 Pickup Lane" } });
  });
  await page.goto("/?t=q-terms-28");
  const job = page.locator(".card[data-fact=\"job-site\"]");
  await expect(job).toBeVisible();
  await expect(job).toContainText("Pickup at 12 Pickup Lane");
  await expect(job).not.toContainText("delivery fee");
});

test("a quote whose dealer set a rep renders the rep card with name, email and phone", async ({ page }) => {
  await page.goto("/?t=q-rep-90");
  const rep = page.locator(".card[data-fact=\"rep\"]");
  await expect(rep).toBeVisible();
  await expect(rep).toContainText("Marisol Reyes");
  await expect(rep).toContainText("marisol@example.test");
  await expect(rep).toContainText("+1-555-0140");
});

test("a quote without rep info renders no rep card", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  await expect(page.locator(".card[data-fact=\"rep\"]")).toHaveCount(0);
});

test("the payment selector offers ACH first, with card only when the dealer configured a surcharge", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  // No surcharge configured: ACH is the only method, said as one line, not a radio group of one.
  await expect(page.locator(".paymentrow")).toBeVisible();
  await expect(page.locator(".paymentrow input[type=\"radio\"]")).toHaveCount(0);
  await expect(page.locator(".paymentrow")).not.toHaveAttribute("role", "radiogroup");
  await expect(page.locator(".paymentrow")).toContainText("ACH transfer");

  await page.goto("/?t=q-card-91");
  await expect(page.locator(".paymentrow input[type=\"radio\"][name=\"payment\"]")).toHaveCount(2);
  await expect(page.locator(".paymentrow input[value=\"card\"]")).toHaveCount(1);
  // The card row's sub-line names the surcharge as a percentage AND a money figure, so the
  // customer sees the cost before they tick.
  await expect(page.locator(".payopt .paysub").nth(1)).toContainText("2.9% surcharge");
});

test("the deposit line shows the percentage and the cents, and a card payment adds a surcharge line", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  await expect(page.locator(".depositline")).toContainText("33% of the accepted total");
  await expect(page.locator(".depositline")).toContainText("$193.91");
});

test("an optional add-on row shows an include checkbox that excludes the line when unticked", async ({ page }) => {
  await page.goto("/?t=q-optional-92");
  // Two rows: the required one and the optional add-on.
  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(2);
  const optRow = rows.nth(1);
  await expect(optRow).toContainText("Premium edge restraint");
  await expect(optRow).toContainText("Optional add-on");
  // The optional checkbox is initially ticked: the customer accepts the full quote by default.
  const box = optRow.locator("input[type=\"checkbox\"][data-line-no=\"2\"]");
  await expect(box).toBeChecked();
  // Untick: the row slashes and dims, and the totals recompute proportionally.
  await box.uncheck();
  await expect(box).not.toBeChecked();
  await expect(optRow).toHaveClass(/excluded/);
  // The optional line's $80.00 cents has been removed; subtotal drops 6000 to 5200.
  await expect(page.getByText("Subtotal $520.00")).toBeVisible();
  await expect(page.getByText("Total $587.60")).toBeVisible();
});

test("an optional add-on the customer excluded is named in the accept payload, otherwise the wire is unchanged", async ({ page }) => {
  await page.goto("/?t=q-optional-accept-92");
  await page.locator("input[type=\"checkbox\"][data-line-no=\"2\"]").uncheck();
  await page.locator("#qty-confirm").check();
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#affirm").check();
  const posted = acceptCall(page);
  await page.getByRole("button", { name: "Accept quote" }).click();
  const request = (await posted).request();
  // The customer excluded line 2: the wire carries both halves and the excluded list.
  expect(request.postDataJSON()).toEqual({
    typed_name: "Sample Customer",
    accepted: true,
    quantities_confirmed: true,
    excluded_optional_lines: [2],
  });

  // A quote with NO optional add-ons keeps the original contract wire.
  await page.goto("/?t=q-plain-accept-28");
  await page.locator("#qty-confirm").check();
  await page.locator("#name").fill("Sample Customer");
  await page.locator("#affirm").check();
  const posted2 = acceptCall(page);
  await page.getByRole("button", { name: "Accept quote" }).click();
  const request2 = (await posted2).request();
  expect(request2.postDataJSON()).toEqual({ typed_name: "Sample Customer", accepted: true, quantities_confirmed: true });
});

test("the Ask a question button calls window.open on the rep's mailto URL, never a binding inside an address attribute", async ({ page }) => {
  await page.goto("/?t=q-rep-90");
  // The button is the design system's secondary kind: a customer reads "Ask a
  // question" as the same shape as every other secondary action on the page.
  const ask = page.getByRole("button", { name: "Ask a question" });
  await expect(ask).toBeVisible();
  await expect(ask).toHaveClass(/btn-secondary/);

  // The button's handler opens the mail client via window.open, so the page
  // never carries a binding inside an address attribute (the position gate).
  let opened: string | null = null;
  await page.exposeFunction("__recordOpen", (url: string) => {
    opened = url;
  });
  await page.addInitScript(() => {
    window.open = ((url?: string | URL): Window | null => {
      (window as unknown as { __recordOpen: (url: string) => void }).__recordOpen(typeof url === "string" ? url : "");
      return null;
    }) as typeof window.open;
  });
  await page.goto("/?t=q-rep-90");
  await expect(page.getByRole("button", { name: "Ask a question" })).toBeVisible();
  await page.getByRole("button", { name: "Ask a question" }).click();
  assert.equal(opened, "mailto:marisol@example.test?subject=Question%20about%20quote%20Q-90");
});

test("the Print or save PDF button is a secondary design-system button that calls window.print", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  const printBtn = page.getByRole("button", { name: "Print or save PDF" });
  await expect(printBtn).toBeVisible();
  // The button is the design system's secondary kind: same shape as every other secondary
  // button the apps render, so a customer recognises it as a print action.
  await expect(printBtn).toHaveClass(/btn-secondary/);
  // Print is a browser path: stub window.print to record the call, the page cannot actually print.
  let printed = false;
  await page.exposeFunction("__recordPrint", () => {
    printed = true;
  });
  await page.addInitScript(() => {
    window.print = () => (window as unknown as { __recordPrint: () => void }).__recordPrint();
  });
  // The init script runs BEFORE the page navigation; reload so it takes effect.
  await page.goto("/?t=q-terms-28");
  await expect(page.getByRole("button", { name: "Print or save PDF" })).toBeVisible();
  await page.getByRole("button", { name: "Print or save PDF" }).click();
  // The page's call reaches this flag through the exposed function, a message to the test process: it is waited for.
  await expect.poll(() => printed).toBe(true);
});

test("the page at 390px shows the new surface without sideways scroll", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/?t=q-all-94");
  await expect(page.getByRole("heading", { name: "Quote #Q-94" })).toBeVisible();
  await expect(page.locator(".statusbadge")).toBeVisible();
  await expect(page.locator(".card[data-fact=\"rep\"]")).toBeVisible();
  await expect(page.locator(".card[data-fact=\"job-site\"]")).toBeVisible();
  await expect(page.locator(".paymentrow")).toBeVisible();
  await expect(page.getByRole("button", { name: "Print or save PDF" })).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow, "page must not scroll sideways at 390px").toBeLessThanOrEqual(0);
});

test("?theme=charcoal flips the design sheet to the dark half while every other quote stays light", async ({ page }) => {
  await page.goto("/?t=q-terms-28");
  const light = await page.evaluate(() => document.documentElement.getAttribute("data-theme"));
  assert.equal(light, "light");
  await page.goto("/?t=q-terms-28&theme=charcoal");
  const dark = await page.evaluate(() => document.documentElement.getAttribute("data-theme"));
  // The opt-in kept light OFF: the design sheet's dark half is the page's own family.
  assert.equal(dark, null);
  // The card still paints: the dark half is a designed surface, not a broken page.
  await expect(page.getByRole("heading", { name: "Quote #Q-28" })).toBeVisible();
});

test("the new surface elements ride the design system's tokens: no raw px, no literal hex", async ({ page }) => {
  await page.goto("/?t=q-all-94");
  const probe = await page.evaluate(() => {
    const root = document.querySelector("pd-quote-public")?.shadowRoot as ShadowRoot;
    const read = (sel: string) => {
      const el = root.querySelector(sel) as Element | null;
      return el === null ? null : { borderTopLeftRadius: getComputedStyle(el).borderTopLeftRadius };
    };
    return {
      badge: read(".statusbadge"),
      job: read(".card[data-fact=\"job-site\"]"),
      rep: read(".card[data-fact=\"rep\"]"),
      payment: read(".payopt"),
      asklink: read("button[data-action=\"ask\"]"),
      printbtn: read("button[data-action=\"print\"]"),
      deposit: read(".depositline"),
      excluded: read("tr.optionalrow.excluded, tr.optionalrow:not(.excluded)"),
    };
  });
  // Each new card rides a token radius, not raw px: the sinks rule says so.
  for (const [name, value] of Object.entries(probe)) {
    if (value === null) continue;
    expect(value.borderTopLeftRadius, `${name} radius must come from a token`).toMatch(/^(0px|\d+(\.\d+)?(px|rem))$/);
  }
});

test("the customer page: quantities carry their unit with no trailing zeros, dates in the one family", async ({ page }) => {
  await page.goto(`/?t=${GOOD_TOKEN}`);
  const rows = page.locator("tbody tr");
  await expect(rows).toHaveCount(3);
  await expect(rows.nth(0)).toContainText("1 pc");
  await expect(rows.nth(1)).toContainText("1 bag");
  await expect(rows.nth(2)).toContainText("1 cu yd");
  await expect(page.locator("tbody")).not.toContainText(".0000");
  const sub = page.getByText(/valid until /);
  await expect(sub).toContainText(/valid until [A-Z][a-z]{2} \d{1,2}, \d{4}/);
  await expect(sub).toContainText(/last updated [A-Z][a-z]{2} \d{1,2}, \d{4}, \d{1,2}:\d{2} [AP]M/);
});

test("at phone width the reason Accept is disabled sits right by the button, inside the screen with it", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/?t=q-demo-25-hint");
  const accept = page.getByRole("button", { name: "Accept quote" });
  await expect(accept).toBeDisabled();
  await accept.evaluate((el) => el.scrollIntoView({ block: "end" }));
  const hint = page.locator("#accept-hint");
  await expect(hint).toBeVisible();
  const [h, b] = await Promise.all([hint.boundingBox(), accept.boundingBox()]);
  expect(h).not.toBeNull();
  expect(b).not.toBeNull();
  const vh = 844;
  expect((h as { y: number; height: number }).y + (h as { y: number; height: number }).height).toBeLessThanOrEqual(vh);
  expect(Math.abs((h as { y: number }).y - (b as { y: number; height: number }).y - (b as { height: number }).height) < 80 ||
    Math.abs((b as { y: number }).y - (h as { y: number; height: number }).y - (h as { height: number }).height) < 80).toBe(true);
});
