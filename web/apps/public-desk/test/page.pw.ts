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
  await expect(page.locator("img.thumb").nth(2)).toBeVisible();
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
});

test("a second accept is shown as already accepted", async ({ page }) => {
  // The same link after acceptance: the quote left the acceptable state, so
  // the page says the quote is accepted instead of offering the form again.
  await page.goto(`/?t=${GOOD_TOKEN}`);
  await expect(page.getByText("This quote is accepted and is not awaiting acceptance.")).toBeVisible();
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
