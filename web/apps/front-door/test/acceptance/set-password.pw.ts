// The set password page's acceptance spec: the card, the link it reads, the
// query it takes back out of the address bar, the rules it checks before it
// asks, and every answer the console can give. Walked in a real browser against
// the stub's fake FB Console.
//
// The stub's ledger keeps the field NAMES and the field LENGTHS of the
// recovery call, never the secret and never the password: the proof that the
// page sends three fields and nothing else is the ledger's field list.
//
// Every state is also photographed, light and charcoal, at 1280 and at a 390 px
// phone, into FD_SHOTS_DIR (default /tmp/fd-set-password-shots), so the screen
// is looked at by a person before it is judged.
import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const SHOTS = process.env.FD_SHOTS_DIR ?? "/tmp/fd-set-password-shots";
const USER_ID = "console-user-1";
const SECRET = "stub-recovery-secret";
const LINK = `/set-password?userId=${USER_ID}&secret=${SECRET}`;
const PASSWORD = "a good new password";

type Arms = Record<string, unknown>;

interface RecoveryLedgerEntry {
  fields: string[];
  userId: string;
  secretLength: number;
  passwordLength: number;
}

function consoleConfig(baseURL: string): Record<string, unknown> {
  return {
    mode: "console",
    org: "gable",
    consoleEndpoint: `${baseURL}/console/v1`,
    consoleProjectId: "fake-project",
    coreBaseUrl: "",
    personas: false,
    personasOpen: false,
    dealer: { name: "Gable Landscape Supply", mark: "gable" },
  };
}

async function serve(page: Page, baseURL: string, arms: Arms = {}): Promise<void> {
  expect((await page.request.post("/_stub/reset")).ok()).toBe(true);
  expect((await page.request.post("/_stub/config", { data: { config: consoleConfig(baseURL) } })).ok()).toBe(true);
  if (Object.keys(arms).length > 0) expect((await page.request.post("/_stub/console", { data: arms })).ok()).toBe(true);
}

async function recoveries(page: Page): Promise<RecoveryLedgerEntry[]> {
  const peeked = (await (await page.request.get("/_stub/console-peek")).json()) as { recoveries?: RecoveryLedgerEntry[] };
  return peeked.recoveries ?? [];
}

async function fill(page: Page, password: string, confirm: string): Promise<void> {
  await page.getByLabel("New password").fill(password);
  await page.getByLabel("Confirm password").fill(confirm);
}

const status = (page: Page) => page.getByTestId("set-password-status");

test.beforeEach(async ({ page, baseURL }) => {
  await serve(page, baseURL!);
});

test("the page is the sign in card with two fields and the dealer's own name", async ({ page }) => {
  await page.goto(LINK);
  await expect(page.getByTestId("set-password-card")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Set your password" })).toBeVisible();
  await expect(page.getByLabel("New password")).toBeVisible();
  await expect(page.getByLabel("Confirm password")).toBeVisible();
  await expect(page.getByTestId("set-password-submit")).toBeEnabled();
  // The dealer's lockup, the same one the sign in card carries.
  await expect(page.getByTestId("set-password-dealer-line")).toContainText("Gable Landscape Supply");
  await expect(page.getByTestId("set-password-footer")).toContainText("Powered by HardscapeOS");
});

test("the secret is taken out of the address bar", async ({ page }) => {
  await page.goto(LINK);
  await expect(page.getByTestId("set-password-card")).toBeVisible();
  expect(page.url()).not.toContain("secret");
  expect(page.url()).not.toContain("userId");
  expect(page.url()).toContain("/set-password");
});

test("the two passwords must match before anything is sent", async ({ page }) => {
  await page.goto(LINK);
  await fill(page, PASSWORD, "a different password");
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("The two passwords do not match. Type them again.");
  expect(await recoveries(page)).toHaveLength(0);
});

test("the password must be eight characters at least", async ({ page }) => {
  await page.goto(LINK);
  await fill(page, "short", "short");
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("Use at least 8 characters.");
  expect(await recoveries(page)).toHaveLength(0);
});

test("a good password sets it, and the console is asked with the three fields and nothing else", async ({ page }) => {
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(page.getByTestId("set-password-done")).toBeVisible();
  await expect(page.getByTestId("set-password-done")).toContainText("Your password is set");
  // The success state points at the sign in page.
  await expect(page.getByTestId("set-password-signin")).toHaveAttribute("href", "/");

  // The stub's ledger is polled, not read once: the page shows the done state
  // when the console's answer lands, and the ledger is the stub's own view of
  // the call it took.
  await expect.poll(async () => (await recoveries(page)).length).toBe(1);
  const ledger = await recoveries(page);
  expect(ledger[0].fields).toEqual(["password", "secret", "userId"]);
  expect(ledger[0].userId).toBe(USER_ID);
  expect(ledger[0].secretLength).toBe(SECRET.length);
  expect(ledger[0].passwordLength).toBe(PASSWORD.length);
});

test("the password never travels in the address bar", async ({ page }) => {
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(page.getByTestId("set-password-done")).toBeVisible();
  expect(page.url()).not.toContain(PASSWORD);
  expect(page.url()).not.toContain("secret");
});

test("a link the console will not take says it has expired", async ({ page, baseURL }) => {
  await serve(page, baseURL!, { recovery: { status: 401, type: "user_invalid_token" } });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("This link has expired or was already used. Ask for a new one.");
  await expect(page.getByTestId("set-password-done")).toHaveCount(0);
});

test("the console's recently used rule is said in plain words", async ({ page, baseURL }) => {
  await serve(page, baseURL!, { recovery: { status: 400, type: "password_recently_used" } });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("Choose a password you have not used here before.");
  await expect(page.getByTestId("set-password-done")).toHaveCount(0);
});

test("the console's personal data rule is said in plain words", async ({ page, baseURL }) => {
  await serve(page, baseURL!, { recovery: { status: 400, type: "password_personal_data" } });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("Do not use your name, email or user ID in the password.");
  await expect(page.getByTestId("set-password-done")).toHaveCount(0);
});

test("the console's anonymous password refusal says to choose another", async ({ page, baseURL }) => {
  // A length outside 8 to 256, the project's own policy and a breached
  // password all arrive as this one anonymous 400: the console does not
  // separate them, so the words name no rule.
  await serve(page, baseURL!, { recovery: { status: 400, type: "general_argument_invalid" } });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("That password will not do. Choose a different one and try again.");
  await expect(page.getByTestId("set-password-done")).toHaveCount(0);
});

test("a throttled console says wait", async ({ page, baseURL }) => {
  await serve(page, baseURL!, { recovery: { status: 429, type: "general_rate_limit_exceeded" } });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("Too many attempts. Wait a moment, then try again.");
});

test("a console that is down says try again", async ({ page, baseURL }) => {
  await serve(page, baseURL!, { recovery: { status: 503, type: "general_unavailable" } });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("Setting a password is unavailable right now. Try again in a moment.");
});

test("a network failure says what the sign in screen says", async ({ page }) => {
  await page.route("**/console/v1/account/recovery", (route) => route.abort("failed"));
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(status(page)).toHaveText("You are offline. Connect to the internet, then try again.");
});

test("the secret goes to the console and to nowhere else", async ({ page, baseURL }) => {
  const seen: { url: string; body: string | null; referer: string }[] = [];
  page.on("request", (request) => {
    // The link's own address is a navigation the browser must fetch; it is
    // the page's address, not a request the page makes.
    if (request.isNavigationRequest()) return;
    seen.push({
      url: request.url(),
      body: request.postData(),
      referer: request.headers()["referer"] ?? "",
    });
  });
  await page.goto(LINK);
  await fill(page, PASSWORD, PASSWORD);
  await page.getByTestId("set-password-submit").click();
  await expect(page.getByTestId("set-password-done")).toBeVisible();

  // The secret is in no address the page asks for, and in no Referer it hands
  // on: the document sets its own referrer policy, and the query is gone.
  for (const request of seen) {
    expect(request.url).not.toContain(SECRET);
    expect(request.referer).not.toContain(SECRET);
  }
  // The one request that carries the secret is the console's own recovery route.
  const carrying = seen.filter((request) => (request.body ?? "").includes(SECRET));
  expect(carrying).toHaveLength(1);
  expect(carrying[0].url).toBe(`${baseURL}/console/v1/account/recovery`);
});

test("the page shows the rule, and the policy is carried by the response header", async ({ page }) => {
  const response = await page.goto(LINK);
  // The header is the policy's home: a meta tag in the document is parsed
  // after the browser has begun fetching the page's own modules.
  expect(response?.headers()["referrer-policy"]).toBe("no-referrer");
  const meta = await page.evaluate(() => document.querySelector('meta[name="referrer"]')?.getAttribute("content") ?? "");
  expect(meta).toBe("no-referrer");
  await expect(page.getByTestId("set-password-rule")).toHaveText("At least 8 characters.");
});

test("a page with no link in it says what it needs", async ({ page }) => {
  await page.goto("/set-password");
  await expect(status(page)).toHaveText("This page needs the link from your email.");
  await expect(page.getByTestId("set-password-submit")).toHaveCount(0);
  await expect(page.getByTestId("set-password-signin")).toBeVisible();
});

test("the page fits a phone, and its targets are big enough to tap", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(LINK);
  await expect(page.getByTestId("set-password-card")).toBeVisible();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow).toBeLessThanOrEqual(0);
  for (const locator of [
    page.getByLabel("New password"),
    page.getByLabel("Confirm password"),
    page.getByTestId("set-password-submit"),
  ]) {
    const box = await locator.boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(44);
  }
});

// ── every state, photographed, and looked at ─────────────────────────────────

const THEMES = ["light", "brand"] as const;
const SIZES = [
  { name: "1280", width: 1280, height: 800 },
  { name: "390", width: 390, height: 844 },
];

for (const theme of THEMES) {
  for (const size of SIZES) {
    test(`screenshots: every state, ${theme}, ${size.name}`, async ({ page, context, baseURL }) => {
      test.setTimeout(120_000);
      mkdirSync(SHOTS, { recursive: true });
      // The theme is set the way a person sets it: the choice rides in the tab's
      // session storage and the page's own theme controller applies it. The
      // brand half is the sheet's default (no data-theme attribute), the light
      // half is the opt-in one.
      await context.addInitScript((choice) => {
        window.sessionStorage.setItem("hh-demo.theme.v1", JSON.stringify(choice));
      }, theme === "light" ? "light" : "dark");
      await page.setViewportSize({ width: size.width, height: size.height });
      const shot = async (name: string): Promise<void> => {
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        expect(overflow, `${name}: the page is wider than the viewport`).toBeLessThanOrEqual(0);
        await page.screenshot({ path: join(SHOTS, `${theme}-${size.name}-${name}.png`), fullPage: true });
      };
      // Idle, with a good link.
      await serve(page, baseURL!);
      await page.goto(LINK);
      await expect(page.getByTestId("set-password-card")).toBeVisible();
      await shot("01-idle");
      // The mismatch.
      await fill(page, PASSWORD, "a different password");
      await page.getByTestId("set-password-submit").click();
      await expect(status(page)).toHaveText("The two passwords do not match. Type them again.");
      await shot("02-mismatch");
      // The expired link.
      await serve(page, baseURL!, { recovery: { status: 401, type: "user_invalid_token" } });
      await page.goto(LINK);
      await fill(page, PASSWORD, PASSWORD);
      await page.getByTestId("set-password-submit").click();
      await expect(status(page)).toHaveText("This link has expired or was already used. Ask for a new one.");
      await shot("03-expired");
      // The done state.
      await serve(page, baseURL!);
      await page.goto(LINK);
      await fill(page, PASSWORD, PASSWORD);
      await page.getByTestId("set-password-submit").click();
      await expect(page.getByTestId("set-password-done")).toBeVisible();
      await shot("04-done");
    });
  }
}
