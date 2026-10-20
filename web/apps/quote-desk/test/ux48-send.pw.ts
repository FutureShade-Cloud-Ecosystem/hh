// UX-48: a quote that reached no one is not "sent". Send texts only where the customer allows it and says so;
// a send that reached no one leads with that, leaves the quote a draft, starts nothing, and offers the link.
// Copy link never marks Sent; "I sent it myself" does, as the dealer's word.
import { expect, test, type Page } from "@playwright/test";
import { armedHold, hold, open, seed, settle, signIn, stubBrowserChannels, watchEvent, writes } from "./switch-helpers.js";

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});
test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

async function noConsent(page: Page): Promise<void> {
  const r = await page.request.post("/_stub/sms-consent", { data: { customer: "c-maple-ridge", consent: false } });
  expect(r.ok()).toBe(true);
}

test("a text the core queued reads Sent, naming the channel and the number on file", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-status")).toContainText("Draft");
  await expect(page.getByTestId("qb-send-how")).toHaveText("Sends by text");
  const seen = writes(page);
  await page.getByTestId("qb-send").click();
  const notice = page.getByTestId("qb-notice");
  await expect(notice).toContainText("Sent by text to the customer's number on file");
  await expect(notice).toContainText("**34");
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
  await expect(page.getByTestId("qb-not-sent")).toHaveCount(0);
  expect(seen.filter((w) => w.includes("/sms-share"))).toHaveLength(1);
  expect(seen.filter((w) => w.includes("/share-links")), "the send makes its link in the core, not in the page").toHaveLength(0);
});

test("a send that reached no one says so first, stays a draft, starts nothing and offers the link", async ({ page }) => {
  await noConsent(page);
  await signIn(page);
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-send-how")).toHaveText("Gives you the link to copy");
  const seen = writes(page);
  await page.getByTestId("qb-send").click();
  const box = page.getByTestId("qb-not-sent");
  await expect(box).toBeVisible();
  await expect(page.getByTestId("qb-not-sent-words")).toHaveText(/^Not sent yet\. Texts are not allowed for this customer/);
  await expect(box).toHaveAttribute("role", "alert");
  // The error colour: the same red the page's other refusals use, never the success green.
  const red = await page.getByTestId("qb-error").evaluate((el) => getComputedStyle(el).color);
  expect(await box.evaluate((el) => getComputedStyle(el).color)).toBe(red);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
  await expect(page.getByTestId("qb-status")).toContainText("Draft");
  await expect(page.getByTestId("qb-not-sent-copy")).toBeVisible();
  await expect(page.getByTestId("qb-not-sent-add")).toContainText("add the customer's phone or email");
  await settle(page);
  expect(seen.filter((w) => /\/(sms-share|share-links|mark-sent)/.test(w)), "a send that cannot reach anyone calls nothing").toEqual([]);
});

test("a text the core refuses at the call also reads Not sent yet and leaves the quote a draft", async ({ page }) => {
  await signIn(page);
  await open(page, "Q-1001");
  // The page read the permission as allowed; the customer withdrew it before the press.
  await expect(page.getByTestId("qb-send-how")).toHaveText("Sends by text");
  await noConsent(page);
  await page.getByTestId("qb-send").click();
  await expect(page.getByTestId("qb-not-sent-words")).toContainText("Not sent yet. The text could not be sent");
  await expect(page.getByTestId("qb-status")).toContainText("Draft");
});

test("Copy link leaves the quote a draft, then I sent it myself marks it Sent on the dealer's word", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await noConsent(page);
  await signIn(page);
  await open(page, "Q-1001");
  await expect(page.getByTestId("qb-copy-link")).toHaveText("Copy Link");
  await expect(page.getByTestId("qb-sent-myself")).toHaveCount(0);
  const seen = writes(page);
  await page.getByTestId("qb-send").click();
  await page.getByTestId("qb-not-sent-copy").click();
  await expect(page.getByTestId("qb-notice")).toContainText("Link copied. The quote is still a draft");
  await expect(page.getByTestId("qb-status")).toContainText("Draft");
  await expect(page.getByTestId("qb-not-sent")).toHaveCount(0);
  expect(seen.filter((w) => w.includes("/mark-sent"))).toHaveLength(0);
  await page.getByTestId("qb-sent-myself").click();
  await expect(page.getByTestId("qb-notice")).toContainText("Marked Sent on your word");
  await expect(page.getByTestId("qb-status")).toContainText("Sent");
  await expect(page.getByTestId("qb-sent-myself")).toHaveCount(0);
  expect(seen.filter((w) => w.includes("/mark-sent"))).toHaveLength(1);
});

async function copiedDraft(page: Page): Promise<void> {
  await stubBrowserChannels(page);
  await noConsentFenwick(page);
  await seed(page, { customer_id: "c-fenwick" });
  await signIn(page);
  await open(page, "Q-1004");
  await page.getByTestId("qb-copy-link").click();
  await expect(page.getByTestId("qb-sent-myself")).toBeVisible();
}

async function noConsentFenwick(page: Page): Promise<void> {
  const r = await page.request.post("/_stub/sms-consent", { data: { customer: "c-fenwick", consent: false } });
  expect(r.ok()).toBe(true);
}

test("guard sent myself answer: an I sent it myself answer that lands after another quote opened says nothing there", async ({ page }) => {
  await copiedDraft(page);
  const mark = await hold(page, /\/api\/v1\/quotes\/q-1004\/mark-sent$/, { method: "POST" });
  await page.getByTestId("qb-sent-myself").click();
  await mark.asked;
  await open(page, "Q-1001");
  await mark.release();
  await settle(page);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
  await expect(page.getByTestId("qb-error")).toHaveText("");
});

test("guard sent myself reload: a re-read after I sent it myself that answers after another quote opened announces nothing", async ({ page }) => {
  await copiedDraft(page);
  const gated = await armedHold(page, /\/api\/v1\/quotes\/q-1004$/, "GET");
  gated.arm();
  const marked = page.waitForResponse((r) => r.request().method() === "POST" && r.url().endsWith("/quotes/q-1004/mark-sent"));
  await page.getByTestId("qb-sent-myself").click();
  await marked;
  await gated.held.asked;
  await settle(page);
  await open(page, "Q-1001");
  const changed = await watchEvent(page, "quote-changed");
  await gated.held.release();
  await settle(page);
  expect(await changed()).toBe(0);
  await expect(page.getByTestId("qb-notice")).toHaveText("");
});
