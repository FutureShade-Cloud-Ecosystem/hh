// The hold expiry panel in a real browser, against the stubbed harness: it is
// silent until a hold lapses, then says how many lines are no longer held and
// names them, offers the re-hold, applies it (announcing quote-changed and
// re-reading), shows the server's own refusal verbatim on an expired quote,
// and says nothing at all when the seam is not wired.
import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";

const SHOTS = "/home/colton/Desktop/FBHQ/hh-rd2/scratch-app";

async function signIn(page: Page, role = "Sales manager"): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: role, exact: true }).click();
  await expect(page.getByTestId("qb-list")).toBeVisible();
}

async function openQuote(page: Page, text: string): Promise<void> {
  await page.getByTestId("qb-list-row").filter({ hasText: text }).first().click();
  const detail = page.getByTestId("qb-detail");
  if ((await detail.getAttribute("open")) === null) await page.getByText("Quote detail & admin").click();
  await expect(page.locator("qd-quote-followups").getByTestId("fu-box")).toBeVisible();
}

const panel = (page: Page) => page.locator("qd-quote-hold-expiry");

test.beforeAll(async ({ request }) => {
  mkdirSync(SHOTS, { recursive: true });
});

test.beforeEach(async ({ request }) => {
  await request.post("/_stub/reset");
});

test.afterAll(async ({ request }) => {
  await request.post("/_stub/reset");
});

test("a quote whose holds all stand says nothing at all", async ({ page }) => {
  await signIn(page);
  await openQuote(page, "Q-1002");
  await expect(panel(page).getByTestId("he-box")).toHaveCount(0);
  await expect(panel(page)).toHaveText("");
});

test("a lapsed hold is said in words, with the line named and the re-hold offered", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page);
  await openQuote(page, "Q-1001");
  const box = panel(page).getByTestId("he-box");
  await expect(box).toHaveAttribute("role", "status");
  await expect(panel(page).getByTestId("he-headline")).toHaveText("A stock hold on this quote has expired: 1 of 2 lines is no longer held.");
  await expect(panel(page).getByTestId("he-line")).toHaveCount(1);
  await expect(panel(page).getByTestId("he-line")).toContainText("Polymeric");
  await expect(panel(page).getByTestId("he-line")).toContainText("lapsed");
  await expect(panel(page).getByRole("button", { name: "Re-hold these lines" })).toBeEnabled();
  await box.screenshot({ path: `${SHOTS}/hold-expiry-lapsed.png` });
});

test("every line lapsed reads in the plural", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001" } });
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(panel(page).getByTestId("he-headline")).toHaveText("Stock holds on this quote have expired: 2 of 2 lines are no longer held.");
  await expect(panel(page).getByTestId("he-line")).toHaveCount(2);
});

test("the re-hold is applied, announced and re-read: the lapse is gone", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001", line_no: 2 } });
  await signIn(page);
  await openQuote(page, "Q-1001");
  const announced = page.evaluate(
    () => new Promise<string>((resolve) => document.addEventListener("quote-changed", (e) => resolve((e as CustomEvent).detail.quoteId), { once: true })),
  );
  await panel(page).getByRole("button", { name: "Re-hold these lines" }).click();
  expect(await announced).toBe("q-1001");
  await expect(panel(page).getByTestId("he-notice")).toHaveText("Re-held 1 line.");
  await expect(panel(page).getByTestId("he-headline")).toHaveCount(0);
  await expect(panel(page).getByRole("button", { name: "Re-hold these lines" })).toHaveCount(0);
  await expect(panel(page).getByTestId("he-error")).toHaveCount(0);
});

test("a re-hold on an expired quote is refused in the server's own words, and the lapse stays", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1002", quote_expired: true } });
  await signIn(page);
  await openQuote(page, "Q-1002");
  await expect(panel(page).getByTestId("he-headline")).toBeVisible();
  await panel(page).getByRole("button", { name: "Re-hold these lines" }).click();
  await expect(panel(page).getByTestId("he-error")).toHaveText("This quote has expired; extend its expiry date before holding stock again");
  await expect(panel(page).getByTestId("he-headline")).toBeVisible();
  await expect(panel(page).getByTestId("he-notice")).toHaveCount(0);
  await panel(page).getByTestId("he-box").screenshot({ path: `${SHOTS}/hold-expiry-refused.png` });
});

test("an unwired seam says nothing, even with a lapse in the database", async ({ page, request }) => {
  await request.post("/_stub/hold-lapse", { data: { quote_id: "q-1001" } });
  await request.put("/_stub/seam-wired", { data: { wired: false } });
  await signIn(page);
  await openQuote(page, "Q-1001");
  await expect(panel(page).getByTestId("he-box")).toHaveCount(0);
  await expect(panel(page)).toHaveText("");
});
