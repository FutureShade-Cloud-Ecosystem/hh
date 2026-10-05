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

  // A link whose accept refuses gets the page's OWN sentence, inline: the
  // core's refusal is written for staff and never reaches a customer.
  await page.goto("/?t=q-refuse-27");
  await page.locator("#name").fill("Sample Customer");
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
  "This quote was already accepted. Accepting again with the same name confirms the earlier acceptance; a different name records another acceptance.";
const STAFF_WORDS = /convert it at the counter|administrator|quote:|acceptance settings|order SO-1/i;

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

test("a 4xx refusal that carries the JSON error envelope is said in the page's own words, never the core's", async ({ page }) => {
  await readyToAccept(page, "q-terms-28");
  await page.route("**/accept", (route) => route.fulfill({ status: 422, contentType: "application/json", body: JSON.stringify({ error: "quote: staff only wording about the contact on the quote" }) }));
  await page.getByRole("button", { name: "Accept quote" }).click();
  await expect(page.getByRole("alert")).toHaveText(`${REFUSED_GENERIC} ${NOT_RECORDED_SUFFIX}`);
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

test("the page wears the manifest's brand: hh-pro pins the action fill to the dealer blue, not the house gold", async ({ page }) => {
  // A quote that is still open: the demo quote is accepted by an earlier test.
  await page.goto("/?t=q-terms-28");
  await expect(page.locator("html")).toHaveAttribute("data-brand", "hh-pro");
  // The computed colour may come back in a wide-gamut syntax; a canvas
  // pixel turns it into plain sRGB channels.
  const [r, , b] = await page.getByRole("button", { name: "Accept quote" }).evaluate((node) => {
    const ctx = document.createElement("canvas").getContext("2d") as CanvasRenderingContext2D;
    ctx.fillStyle = getComputedStyle(node).backgroundColor;
    ctx.fillRect(0, 0, 1, 1);
    return Array.from(ctx.getImageData(0, 0, 1, 1).data);
  });
  expect(b).toBeGreaterThan(r);
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
    data: { typed_name: "Another Customer", accepted: true },
  });
  expect(other.status()).toBe(200);
  await page.locator("#name").fill("Sample Customer");
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

test("a 409 whose re-read shows CONVERTED also ends on the closed accepted card", async ({ page }) => {
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
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
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

test("an unknown outcome whose re-read shows CONVERTED ends on the closed accepted card", async ({ page }) => {
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
  await expect(page.getByRole("heading", { name: "Already accepted" })).toBeVisible();
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
