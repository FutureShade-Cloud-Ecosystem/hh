// The console mode's acceptance spec (item 7b): the real sign in screen, every
// state of it, the role entry, the persona cards and the custody, walked in a
// real browser against the stub's fake FB Console and fake core routes. The
// demo mode's legs (front-door.pw.ts and the rest) stay as they are and prove
// the tap a role page is untouched.
//
// Every state is also photographed, light and dark, at 1280 and at a 390 px
// phone, into FD_SHOTS_DIR (default /tmp/fd-console-shots), so the screens are
// looked at by a person before they are judged.
import { expect, test, type Page } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { join } from "node:path";

const PARTNER_EMAIL = "partner.one@hh-demo.invalid";
const PARTNER_PASSWORD = "correct horse battery";
const SHOTS = process.env.FD_SHOTS_DIR ?? "/tmp/fd-console-shots";

type Arms = Record<string, unknown>;

function consoleConfig(baseURL: string, patch: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    mode: "console",
    org: "gable",
    consoleEndpoint: `${baseURL}/console/v1`,
    consoleProjectId: "fake-project",
    coreBaseUrl: "",
    personas: false,
    personasOpen: false,
    dealer: { name: "Gable Landscape Supply", mark: "gable" },
    ...patch,
  };
}

async function serve(page: Page, baseURL: string, patch: Record<string, unknown> = {}, arms: Arms = {}): Promise<void> {
  expect((await page.request.post("/_stub/reset")).ok()).toBe(true);
  expect((await page.request.post("/_stub/config", { data: { config: consoleConfig(baseURL, patch) } })).ok()).toBe(true);
  if (Object.keys(arms).length > 0) expect((await page.request.post("/_stub/console", { data: arms })).ok()).toBe(true);
}

async function arm(page: Page, arms: Arms): Promise<void> {
  expect((await page.request.post("/_stub/console", { data: arms })).ok()).toBe(true);
}

async function peek(page: Page): Promise<{ signins: { email: string; passwordLength: number }[]; exchanges: number; jwts: number; personas: { persona: string; authorized: boolean }[]; methodsAsked: string[]; cookie: boolean }> {
  return (await page.request.get("/_stub/console-peek")).json();
}

async function fill(page: Page, email: string, password: string): Promise<void> {
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
}

async function signInAsPartner(page: Page): Promise<void> {
  await expect(page.getByTestId("signin-submit")).toBeVisible();
  await fill(page, PARTNER_EMAIL, PARTNER_PASSWORD);
  await page.getByTestId("signin-submit").click();
}

/** The picker's branch list is information: a label and the core's names, and no control inside it or anywhere near it. */
async function expectBranchesAsInformation(page: Page, label: string, names: string[]): Promise<void> {
  const list = page.getByTestId("branch-list");
  await expect(list).toBeVisible();
  await expect(list).toContainText(new RegExp(label, "i"));
  for (const name of names) await expect(list).toContainText(name);
  await expect(list.getByRole("radio")).toHaveCount(0);
  await expect(list.getByRole("button")).toHaveCount(0);
  await expect(list.getByRole("checkbox")).toHaveCount(0);
  await expect(list.locator("[aria-pressed], [aria-checked], [tabindex], button, input")).toHaveCount(0);
  await expect(page.getByRole("radiogroup", { name: /your branch/i })).toHaveCount(0);
  await expect(page.getByRole("group", { name: /your branch/i })).toHaveCount(0);
}

/** Several granted branches are a choice: toggle buttons in a labelled group, one tab stop, every branch chosen to start. */
async function expectBranchChoice(page: Page, names: string[]): Promise<void> {
  const group = page.getByRole("group", { name: /your branches/i });
  await expect(group).toBeVisible();
  const toggles = group.getByRole("button");
  await expect(toggles).toHaveCount(names.length);
  for (const [index, name] of names.entries()) {
    await expect(toggles.nth(index)).toContainText(name);
    await expect(toggles.nth(index)).toHaveAttribute("aria-pressed", "true");
  }
  await expect(group.locator('[tabindex="0"], button:not([tabindex])')).toHaveCount(1);
  await expect(page.getByRole("radiogroup", { name: /your branch/i })).toHaveCount(0);
}

/** What the front door wrote into this tab's branch key. */
async function branchKey(page: Page): Promise<string[] | null> {
  return page.evaluate(() => {
    const raw = window.sessionStorage.getItem("hh.session.branches.v1");
    return raw === null ? null : (JSON.parse(raw) as { ids: string[] }).ids;
  });
}

const status = (page: Page) => page.getByTestId("signin-status");

test.describe("configuration", () => {
  test("a missing, unreadable or unknown configuration is an honest error page, never the demo cards", async ({ page, baseURL }) => {
    for (const config of [{ mode: "staging" }, {}, null, { mode: "console", org: "gable" }]) {
      expect((await page.request.post("/_stub/reset")).ok()).toBe(true);
      expect((await page.request.post("/_stub/config", { data: { config } })).ok()).toBe(true);
      await page.goto("/");
      await expect(page.getByTestId("config-error")).toBeVisible();
      await expect(page.getByRole("button", { name: "Sales", exact: true })).toHaveCount(0);
      await expect(page.getByText("Pick a role to walk the demo.")).toHaveCount(0);
    }
    expect(baseURL).toBeTruthy();
  });

  test("demo mode keeps today's tap a role page exactly", async ({ page }) => {
    expect((await page.request.post("/_stub/reset")).ok()).toBe(true);
    await page.goto("/");
    await expect(page.getByText("Pick a role to walk the demo.")).toBeVisible();
    await expect(page.getByRole("button", { name: "Sales", exact: true })).toBeEnabled();
    await expect(page.getByTestId("signin-submit")).toHaveCount(0);
  });
});

test.describe("the sign in screen", () => {
  test("idle: the dealer, the methods it allows, labelled fields, 44 px targets, nothing from the demo", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await expect(page.getByTestId("dealer-lockup").first()).toContainText("Gable");
    await expect(page.getByTestId("dealer-lockup").first()).toContainText("Landscape Supply");
    await expect(page.getByRole("heading", { name: "Sign in to Work", exact: true })).toBeVisible();
    await expect(page.locator("header")).toHaveCount(0);
    await expect(page.getByTestId("signin-dealer-line")).toHaveText("Gable Landscape Supply");
    await expect(page.getByTestId("signin-footer")).toContainText("Powered by HardscapeOS");
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.getByTestId("google-button")).toHaveText(/Continue with Google/);
    await expect(page.getByText("Demo", { exact: true })).toHaveCount(0);
    expect((await peek(page)).methodsAsked).toEqual(["gable"]);
    for (const locator of [page.getByLabel("Email"), page.getByLabel("Password"), page.getByTestId("signin-submit"), page.getByTestId("google-button")]) {
      const box = await locator.boundingBox();
      expect(box!.height).toBeGreaterThanOrEqual(44);
    }
    // The live region exists at rest and is polite.
    await expect(status(page)).toHaveAttribute("aria-live", "polite");
    await expect(status(page)).toHaveText("");
  });

  test("board alignment: dealer branch line from config and keyboard-reachable quiet Theme and Feedback controls", async ({ page, baseURL }) => {
    await serve(page, baseURL!, {
      dealer: { name: "Gable Landscape Supply", mark: "gable", branch: "Point Loma" },
    });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Sign in to Work", exact: true })).toBeVisible();
    await expect(page.getByTestId("signin-dealer-line")).toHaveText("Gable Landscape Supply · Point Loma");
    await expect(page.locator("header")).toHaveCount(0);
    await expect(page.getByTestId("signin-footer")).toContainText("Powered by HardscapeOS");
    await page.getByTestId("google-button").focus();
    await page.keyboard.press("Tab");
    await expect(page.getByTestId("theme-control")).toBeFocused();
    const beforeLabel = await page.getByTestId("theme-control").getAttribute("aria-label");
    await page.keyboard.press("Enter");
    const afterLabel = await page.getByTestId("theme-control").getAttribute("aria-label");
    expect(afterLabel).not.toBe(beforeLabel);
    await page.keyboard.press("Tab");
    await expect(page.getByTestId("feedback-button")).toBeFocused();
    await page.keyboard.press("Enter");
    await expect(page.getByTestId("feedback-button")).toHaveAttribute("aria-expanded", "true");
  });

  test("quiet Theme and Feedback controls at 390x844 are at least 44 by 44 px", async ({ page, baseURL }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await serve(page, baseURL!);
    await page.goto("/");
    await expect(page.getByTestId("theme-control")).toBeVisible();
    await expect(page.getByTestId("feedback-button")).toBeVisible();
    for (const locator of [page.getByTestId("theme-control"), page.getByTestId("feedback-button")]) {
      const box = await locator.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.height).toBeGreaterThanOrEqual(44);
      expect(box!.width).toBeGreaterThanOrEqual(44);
    }
  });

  test("focus order follows the page: email, password, Sign in, Continue with Google; every control is named", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await expect(page.getByLabel("Email")).toBeVisible();
    await page.getByLabel("Email").focus();
    await page.keyboard.press("Tab");
    await expect(page.getByLabel("Password")).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(page.getByTestId("signin-submit")).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(page.getByTestId("google-button")).toBeFocused();
    // Every button, input and link on the screen has an accessible name.
    const unnamed = await page.evaluate(() => {
      const root = document.querySelector("fd-app-root")!.shadowRoot!;
      return Array.from(root.querySelectorAll("button, input, a, summary"))
        .filter((element) => {
          const el = element as HTMLElement;
          const labelled = (el.getAttribute("aria-label") ?? "").trim() !== "" || (el.textContent ?? "").trim() !== "";
          const byLabel = el instanceof HTMLInputElement && (el.labels?.length ?? 0) > 0;
          return !labelled && !byLabel;
        })
        .map((element) => element.outerHTML.slice(0, 80));
    });
    expect(unnamed).toEqual([]);
  });

  test("autocomplete attributes are right, the form never navigates, and Enter submits", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await expect(page.getByLabel("Email")).toHaveAttribute("autocomplete", "username");
    await expect(page.getByLabel("Email")).toHaveAttribute("type", "email");
    await expect(page.getByLabel("Password")).toHaveAttribute("autocomplete", "current-password");
    await expect(page.getByLabel("Password")).toHaveAttribute("type", "password");
    await expect(page.locator('form[data-form="signin"]')).toHaveAttribute("method", "post");
    const urls: string[] = [];
    page.on("request", (request) => urls.push(request.url()));
    await fill(page, PARTNER_EMAIL, PARTNER_PASSWORD);
    await page.getByLabel("Password").press("Enter");
    await expect(page.getByTestId("role-entry")).toBeVisible();
    expect(page.url()).not.toContain(PARTNER_PASSWORD);
    expect(page.url()).not.toContain("password");
    for (const url of urls) expect(url).not.toContain(PARTNER_PASSWORD);
  });

  test("only the methods the dealer allows are drawn", async ({ page, baseURL }) => {
    await serve(page, baseURL!, {}, { methods: ["google"] });
    await page.goto("/");
    await expect(page.getByTestId("google-button")).toBeVisible();
    await expect(page.getByLabel("Email")).toHaveCount(0);
    await serve(page, baseURL!, {}, { methods: ["microsoft", "magic_link", "email_password"] });
    await page.goto("/");
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByTestId("google-button")).toHaveCount(0);
    await serve(page, baseURL!, {}, { methods: ["microsoft"] });
    await page.goto("/");
    await expect(page.getByTestId("methods-none")).toBeVisible();
  });

  test("the options failing to load say so and offer a retry", async ({ page, baseURL }) => {
    await serve(page, baseURL!, {}, { methodsStatus: { status: 503, times: 1 } });
    await page.goto("/");
    await expect(page.getByTestId("methods-failed")).toBeVisible();
    await page.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByLabel("Email")).toBeVisible();
  });

  test("wrong credentials: plain words, the fields kept, focus back on the password", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await fill(page, PARTNER_EMAIL, "not the password");
    await page.getByTestId("signin-submit").click();
    await expect(status(page)).toHaveText("That sign in did not work. Check the email and the password and try again.");
    await expect(page.getByLabel("Email")).toHaveValue(PARTNER_EMAIL);
    await expect(page.getByLabel("Password")).toBeFocused();
    await expect(page.getByLabel("Password")).toHaveAttribute("aria-invalid", "true");
    expect((await peek(page)).exchanges).toBe(0);
  });

  test("not allowed, too many attempts, unavailable: each in its own words, focus on the button", async ({ page, baseURL }) => {
    const cases: [number, string][] = [
      [403, "This account is not set up for this site. Ask your administrator to add you."],
      [429, "Too many attempts. Wait a moment, then try again."],
      [503, "Sign in is unavailable right now. Try again in a moment."],
    ];
    for (const [code, words] of cases) {
      await serve(page, baseURL!, {}, { exchange: { status: code } });
      await page.goto("/");
      await signInAsPartner(page);
      await expect(status(page)).toHaveText(words);
      await expect(page.getByTestId("signin-submit")).toBeFocused();
      await expect(page.getByTestId("role-entry")).toHaveCount(0);
      expect((await peek(page)).cookie).toBe(false);
    }
  });

  test("signing in: the button is disabled with a spinner and the live region says so", async ({ page, baseURL }) => {
    await serve(page, baseURL!, {}, { delayMs: { exchange: 1500 } });
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("signin-submit")).toBeDisabled();
    await expect(page.getByTestId("signin-submit")).toContainText("Signing in");
    await expect(status(page)).toHaveText("Signing in.");
    await expect(page.getByLabel("Email")).toHaveJSProperty("readOnly", true);
    await expect(page.getByLabel("Password")).toHaveJSProperty("readOnly", true);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    // The sign in finished; the screen is gone, so nothing stays read only on a later visit.
  });

  test("offline: the browser being offline is said at once, then sign in works again online", async ({ page, baseURL, context }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await expect(page.getByLabel("Email")).toBeVisible();
    await context.setOffline(true);
    await fill(page, PARTNER_EMAIL, PARTNER_PASSWORD);
    await page.getByTestId("signin-submit").click();
    await expect(status(page)).toHaveText("You are offline. Connect to the internet, then try again.");
    await context.setOffline(false);
    await page.getByTestId("signin-submit").click();
    await expect(page.getByTestId("role-entry")).toBeVisible();
  });

  test("continue with Google returns signed in, and a failed return says so once", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await page.getByTestId("google-button").click();
    // Back from Google the console cookie is live: the page resumes it, and with no persona cards to choose from it lands.
    await expect(page.getByTestId("home-title")).toBeVisible();
    await expect(page.getByTestId("header-word")).toContainText("Partner One");
    await serve(page, baseURL!, {}, { google: "fail" });
    await page.goto("/");
    await page.getByTestId("google-button").click();
    await expect(status(page)).toHaveText("Google sign in did not finish. Try again, or use your email and password.");
    expect(page.url()).not.toContain("signin=failed");
  });
});

test.describe("after sign in", () => {
  test("role and branch picker: greets by first name and period in houseZone, lists only held roles and granted branches from the core, shows the granted branches as information, and opens the chosen role's app", async ({ page, baseURL }) => {
    // 02:30 UTC is 22:30 (evening) in America/Toronto and 19:30 (evening) in America/Los_Angeles,
    // even though UTC is 02:30 (morning).
    await page.clock.install({ time: new Date(1_000_002_600_000) });
    await page.route("**/api/v1/tenant/config", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          id: "demo-dealer",
          dealer_name: "Gable Landscape Supply",
          logo_url: "",
          primary_color: "#3a7d56",
          support_email: "support@gablelandscape.invalid",
          support_phone: "",
          business_zone: "America/Los_Angeles",
        }),
      }),
    );
    await page.route("**/api/v1/branches", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          { id: "branch-north-yard", code: "NTH", name: "North Yard" },
        ]),
      }),
    );
    await serve(page, baseURL!, {}, { role: { role_code: "inside_sales", role_name: "Sales" } });
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    await expect(page.locator("#entry-title")).toHaveText("Good evening, Partner. What are you working on today?");
    await expect(page.getByTestId("entry-name")).toContainText("Partner One · Sales");
    await expect(page.getByRole("button", { name: "Not you? Sign out" })).toBeVisible();

    const roleGroup = page.getByRole("radiogroup", { name: /your role/i });
    await expect(roleGroup).toBeVisible();
    const roleRadios = roleGroup.getByRole("radio");
    await expect(roleRadios).toHaveCount(1);
    await expect(roleRadios.first()).toHaveAttribute("aria-checked", "true");
    await expect(roleRadios.first()).toContainText("Sales");
    await expect(roleRadios.first()).toContainText("Opens Counter Till");

    await expectBranchesAsInformation(page, "Your branch", ["North Yard"]);

    await expect(page.getByTestId("continue-own")).toContainText("Open Counter Till");
    await page.getByTestId("continue-own").click();
    await expect(page.getByTestId("home-title")).toHaveText("Sales");
    await expect(page.getByTestId("header-word")).toContainText("North Yard");
    // A one branch role writes that one id and showed it as information.
    expect(await branchKey(page)).toEqual(["branch-north-yard"]);
  });

  test("role and branch picker: a General Manager with two branches, keyboard arrow keys move within the role group with one tab stop, the branches are a choice with one tab stop, and 44 px targets on a phone across light and charcoal at 1440x900, 1024x768, and 390x844", async ({ page, baseURL }) => {
    await page.clock.install({ time: new Date(1_000_040_400_000) }); // 09:00 EDT morning in America/Toronto
    await page.route("**/api/v1/branches", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          { id: "branch-west", code: "WST", name: "West Yard" },
          { id: "branch-east", code: "EST", name: "East Yard" },
        ]),
      }),
    );
    await serve(
      page,
      baseURL!,
      { personas: true },
      { role: { role_code: "general_manager,sales_manager", role_name: "General Manager", scope: "GLOBAL" } },
    );
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    await expect(page.locator("#entry-title")).toHaveText("Good morning, Partner. What are you working on today?");

    const roleGroup = page.getByRole("radiogroup", { name: /your role/i });
    const roleRadios = roleGroup.getByRole("radio");
    await expect(roleRadios).toHaveCount(2);
    await expect(roleRadios.nth(0)).toHaveAttribute("aria-checked", "true");
    await expect(roleRadios.nth(0)).toHaveAttribute("tabindex", "0");
    await expect(roleRadios.nth(1)).toHaveAttribute("tabindex", "-1");

    // Arrow key navigation within YOUR ROLE radio group.
    await roleRadios.nth(0).focus();
    await page.keyboard.press("ArrowDown");
    await expect(roleRadios.nth(1)).toBeFocused();
    await expect(roleRadios.nth(1)).toHaveAttribute("aria-checked", "true");
    await expect(roleRadios.nth(1)).toHaveAttribute("tabindex", "0");
    await expect(roleRadios.nth(0)).toHaveAttribute("tabindex", "-1");
    await page.keyboard.press("ArrowUp");
    await expect(roleRadios.nth(0)).toHaveAttribute("aria-checked", "true");

    // Several granted branches are a choice again: toggles, every branch chosen to start, one tab stop.
    await expectBranchChoice(page, ["West Yard", "East Yard"]);
    const branchToggles = page.getByRole("group", { name: /your branches/i }).getByRole("button");
    await branchToggles.nth(0).focus();
    await page.keyboard.press("ArrowDown");
    await expect(branchToggles.nth(1)).toBeFocused();
    await expect(branchToggles.nth(1)).toHaveAttribute("tabindex", "0");
    await expect(branchToggles.nth(0)).toHaveAttribute("tabindex", "-1");
    await expect(branchToggles.nth(0)).toHaveAttribute("aria-pressed", "true"); // arrows only move focus
    await page.keyboard.press("Space");
    await expect(branchToggles.nth(1)).toHaveAttribute("aria-pressed", "false");
    await page.keyboard.press("Enter");
    await expect(branchToggles.nth(1)).toHaveAttribute("aria-pressed", "true");
    await page.keyboard.press("ArrowUp");
    await expect(branchToggles.nth(0)).toBeFocused();
    await page.keyboard.press("Space");
    await expect(branchToggles.nth(0)).toHaveAttribute("aria-pressed", "false");
    await branchToggles.nth(1).click(); // the last chosen branch stays chosen
    await expect(branchToggles.nth(1)).toHaveAttribute("aria-pressed", "true");
    await branchToggles.nth(0).click();
    await expect(branchToggles.nth(0)).toHaveAttribute("aria-pressed", "true");

    // Demo walk section below the card is collapsed by default.
    const walkDetails = page.getByTestId("persona-walk");
    await expect(walkDetails).toBeVisible();
    await expect(walkDetails).not.toHaveAttribute("open", "");
    await expect(walkDetails.locator("summary")).toContainText("Walk the demo as another role");

    // Check 1440x900, 1024x768, and 390x844 in light and charcoal.
    for (const [width, height] of [[1440, 900], [1024, 768], [390, 844]] as const) {
      await page.setViewportSize({ width, height });
      for (const theme of ["light", "brand"] as const) {
        await setTheme(page, theme);
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        expect(overflow).toBeLessThanOrEqual(0);
        const shots = process.env.UX79_SHOTS;
        if (shots !== undefined && shots !== "" && width !== 1024) {
          await page.screenshot({ path: `${shots}/front-door-picker-${theme === "brand" ? "charcoal" : theme}-${width}.png` });
        }
      }
    }
    // At 390x844 on a phone, targets on the picker card are at least 44 px tall.
    await page.setViewportSize({ width: 390, height: 844 });
    for (const locator of [
      page.getByRole("button", { name: "Not you? Sign out" }),
      roleRadios.nth(0),
      branchToggles.nth(0),
      branchToggles.nth(1),
      page.getByTestId("continue-own"),
    ]) {
      const box = await locator.boundingBox();
      expect(box!.height).toBeGreaterThanOrEqual(44);
    }
  });

  test("role and branch picker: Open writes only the chosen branch ids to the tab's branch key, for one branch and for both, and the foot reads Powered by HardscapeOS", async ({ page, baseURL }) => {
    await page.route("**/api/v1/branches", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          { id: "branch-west", code: "WST", name: "West Yard" },
          { id: "branch-east", code: "EST", name: "East Yard" },
        ]),
      }),
    );
    await serve(page, baseURL!, {}, { role: { role_code: "sales_manager,inside_sales", role_name: "Sales Manager" } });
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    await expect(page.getByTestId("entry-footer")).toContainText("Powered by HardscapeOS");
    await expectBranchChoice(page, ["West Yard", "East Yard"]);
    // Switching to the other held role changes nothing about the choice.
    await page.getByTestId("role-radiogroup").getByRole("radio").filter({ hasText: "Opens Counter Till" }).click();
    await expectBranchChoice(page, ["West Yard", "East Yard"]);
    await expect(page.getByRole("radiogroup", { name: /your role/i }).getByRole("radio")).toHaveCount(2);
    // Both chosen: both ids written on Open.
    await page.getByTestId("continue-own").click();
    await expect(page.getByTestId("home-title")).toBeVisible();
    expect(await branchKey(page)).toEqual(["branch-west", "branch-east"]);
  });

  test("role and branch picker: narrowing to one branch writes only that id on Open", async ({ page, baseURL }) => {
    await page.route("**/api/v1/branches", (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify([
          { id: "branch-west", code: "WST", name: "West Yard" },
          { id: "branch-east", code: "EST", name: "East Yard" },
        ]),
      }),
    );
    await serve(page, baseURL!, {}, { role: { role_code: "general_manager", role_name: "General Manager", scope: "GLOBAL" } });
    await page.goto("/");
    await signInAsPartner(page);
    await expectBranchChoice(page, ["West Yard", "East Yard"]);
    await page.getByRole("group", { name: /your branches/i }).getByRole("button", { name: /West Yard/ }).click();
    await page.getByTestId("continue-own").click();
    await expect(page.getByTestId("home-title")).toBeVisible();
    expect(await branchKey(page)).toEqual(["branch-east"]);
    await expect(page.getByTestId("header-word")).toContainText("East Yard");
    await expect(page.getByTestId("header-word")).not.toContainText("West Yard");
  });

  test("the role entry names the person's own role, continuing lands them, and the header carries them", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    await expect(page.getByTestId("entry-name")).toContainText("Partner One");
    await expect(page.getByTestId("continue-own")).toContainText("Open Manager Home");
    await expect(page.getByTestId("persona-cards")).toHaveCount(0);
    await page.getByTestId("continue-own").click();
    await expect(page.getByTestId("home-title")).toHaveText("Sales manager");
    await expect(page.getByTestId("header-word")).toContainText("Partner One");
    await expect(page.getByTestId("header-word")).toContainText("Sales Manager");
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await expect(page.getByText("Demo", { exact: true })).toHaveCount(0);
    await page.getByTestId("apps-menu").locator("summary").click();
    await expect(page.getByTestId("apps-menu").getByRole("button", { name: "Counter Till" })).toBeVisible();
  });

  test("a role this site has no screens for says so and offers no way in", async ({ page, baseURL }) => {
    await serve(page, baseURL!, {}, { role: { role_code: "platform_admin", role_name: "Platform admin" } });
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("entry-no-screens")).toBeVisible();
    await expect(page.getByTestId("continue-own")).toHaveCount(0);
  });

  test("a reload while signed in resumes from the console cookie, with the token never in storage", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    const stored = await page.evaluate(() => {
      const dump: Record<string, string> = {};
      for (const area of [window.localStorage, window.sessionStorage]) {
        for (let index = 0; index < area.length; index += 1) {
          const key = area.key(index)!;
          dump[key] = area.getItem(key) ?? "";
        }
      }
      return dump;
    });
    const joined = JSON.stringify(stored);
    expect(joined).not.toContain(PARTNER_PASSWORD);
    expect(joined).not.toMatch(/eyJ|ES256|stub-signature|stub\.jwt/);
    expect(Object.keys(stored).filter((key) => key.includes("front-door.session"))).toEqual([]);
    expect(await page.evaluate(() => document.cookie)).not.toContain("a_session");
    await page.reload();
    await expect(page.getByTestId("home-title")).toBeVisible();
    await expect(page.getByTestId("header-word")).toContainText("Partner One");
  });

  test("sign out ends it, tells the person, and a reload stays signed out", async ({ page, baseURL }) => {
    await serve(page, baseURL!);
    await page.goto("/");
    await signInAsPartner(page);
    await page.getByTestId("continue-own").click();
    await page.getByRole("button", { name: "Sign out" }).click();
    await expect(status(page)).toHaveText("You are signed out.");
    expect((await peek(page)).cookie).toBe(false);
    await page.reload();
    await expect(page.getByTestId("signin-submit")).toBeVisible();
    await expect(status(page)).toHaveText("");
  });

  test("a session the core ends underneath the page returns to sign in with the session ended words", async ({ page, baseURL }) => {
    await serve(page, baseURL!, {}, { windowSeconds: 2 });
    await page.goto("/");
    await signInAsPartner(page);
    await expect(page.getByTestId("role-entry")).toBeVisible();
    await arm(page, { exchange: { status: 401 } });
    await expect(status(page)).toHaveText("Your session ended. Sign in again.", { timeout: 15_000 });
    await expect(page.getByTestId("signin-submit")).toBeVisible();
  });
});

test.describe("a configured core base", () => {
  test("the page's own core calls (the approvals count) go to coreBaseUrl, not the static host", async ({ page, baseURL }) => {
    const port = new URL(baseURL!).port;
    const other = `http://localhost:${port}`;
    const seen: string[] = [];
    // The configured core is a different origin; answer it from the stub with the CORS headers a real core sends.
    await page.route(`${other}/**`, async (route) => {
      const request = route.request();
      seen.push(request.url());
      const cors = {
        "access-control-allow-origin": baseURL!,
        "access-control-allow-headers": "authorization, x-org, content-type, accept",
        "access-control-allow-methods": "GET, POST, OPTIONS",
      };
      if (request.method() === "OPTIONS") return route.fulfill({ status: 204, headers: cors });
      const response = await route.fetch({ url: request.url().replace("localhost", "127.0.0.1") });
      return route.fulfill({ response, headers: { ...response.headers(), ...cors } });
    });
    await serve(page, baseURL!, { coreBaseUrl: other });
    await page.goto("/");
    await signInAsPartner(page);
    await page.getByTestId("continue-own").click();
    await expect(page.getByText("3 approvals waiting")).toBeVisible();
    expect(seen.some((url) => url.startsWith(`${other}/api/v1/staff/approvals`))).toBe(true);
    expect(seen.some((url) => url.startsWith(`${other}/api/v1/auth/exchange`))).toBe(true);
  });
});

test.describe("the persona cards", () => {
  test("the testers' copy: cards after sign in, each calling the persona route with the partner's bearer; a refusal stays in its card", async ({ page, baseURL }) => {
    await serve(page, baseURL!, { personas: true }, { personaStatus: { driver: 429 } });
    await page.goto("/");
    await expect(page.getByTestId("persona-cards")).toHaveCount(0);
    await signInAsPartner(page);
    await expect(page.getByTestId("persona-walk")).toBeVisible();
    await page.getByTestId("persona-walk").locator("summary").click();
    await expect(page.getByTestId("persona-cards")).toBeVisible();
    await expect(page.getByTestId("persona-cards").getByRole("button")).toHaveCount(13);
    await expect(page.getByTestId("coming-disclosure")).toHaveCount(0);
    await page.getByTestId("persona-cards").getByRole("button", { name: "Driver", exact: true }).click();
    await expect(page.getByTestId("persona-cards").getByRole("button", { name: "Driver", exact: true })).toContainText("Too many tries. Wait a moment, then tap again.");
    await expect(page.getByTestId("persona-cards").getByRole("button", { name: "Yard Team", exact: true })).not.toContainText("Too many tries");
    await page.getByTestId("persona-cards").getByRole("button", { name: "Yard Team", exact: true }).click();
    await expect(page.getByTestId("home-title")).toHaveText("Yard Team");
    const calls = (await peek(page)).personas;
    expect(calls.map((call) => call.persona)).toEqual(["driver", "yard-team"]);
    expect(calls.every((call) => call.authorized)).toBe(true);
    await expect(page.getByRole("button", { name: "Switch role" })).toBeVisible();
    await page.getByRole("button", { name: "Switch role" }).click();
    await expect(page.getByTestId("role-entry")).toBeVisible();
    await expect(page.getByTestId("continue-own")).toContainText("Open Manager Home");
  });

  test("the public copy shows no admin persona card; the testers' copy shows them all (ruling A6)", async ({ page, baseURL }) => {
    await serve(page, baseURL!, { personas: true, personasOpen: true, personaCaller: "public" });
    await page.goto("/");
    const publicCards = page.getByTestId("persona-cards").getByRole("button");
    await expect(publicCards).toHaveCount(10);
    await expect(page.getByTestId("persona-cards").getByRole("button", { name: "Financial Controller", exact: true })).toHaveCount(0);
    for (const name of ["General Manager", "HR"]) {
      await expect(page.getByTestId("persona-cards").getByRole("button", { name, exact: true })).toHaveCount(0);
    }
    await serve(page, baseURL!, { personas: true, personasOpen: false, personaCaller: "partner" });
    await page.goto("/");
    await signInAsPartner(page);
    await page.getByTestId("persona-walk").locator("summary").click();
    await expect(page.getByTestId("persona-cards").getByRole("button")).toHaveCount(13);
    await expect(page.getByTestId("persona-cards").getByRole("button", { name: "Financial Controller", exact: true })).toHaveCount(1);
  });

  test("a persona whose flow id and role code differ is sent by flow and lands on its role's home", async ({ page, baseURL }) => {
    await serve(page, baseURL!, { personas: true, personasOpen: true });
    await page.goto("/");
    await page.getByTestId("persona-cards").getByRole("button", { name: "Sales manager", exact: true }).click();
    await expect(page.getByTestId("home-title")).toHaveText("Sales manager");
    expect((await peek(page)).personas.map((call) => call.persona)).toEqual(["sales-manager"]);
  });

  test("the public copy: cards on the sign in screen with no bearer, and a persona ends at its expiry", async ({ page, baseURL }) => {
    await serve(page, baseURL!, { personas: true, personasOpen: true }, { personaWindowSeconds: 3 });
    await page.goto("/");
    await expect(page.getByTestId("persona-cards")).toBeVisible();
    await page.getByTestId("persona-cards").getByRole("button", { name: "Sales", exact: true }).click();
    await expect(page.getByTestId("home-title")).toHaveText("Sales");
    expect((await peek(page)).personas).toEqual([{ persona: "sales", authorized: false }]);
    expect((await peek(page)).exchanges).toBe(0);
    await expect(status(page)).toHaveText("Your session ended. Sign in again.", { timeout: 15_000 });
    expect((await peek(page)).exchanges).toBe(0);
  });
});

// ── every state, photographed, and looked at ────────────────────────────────

const THEMES = ["light", "brand"] as const;
const SIZES = [
  { name: "1280", width: 1280, height: 800 },
  { name: "390", width: 390, height: 844 },
];

async function setTheme(page: Page, theme: (typeof THEMES)[number]): Promise<void> {
  await page.evaluate((value) => {
    if (value === "light") document.documentElement.setAttribute("data-theme", "light");
    else document.documentElement.removeAttribute("data-theme");
  }, theme);
}

for (const theme of THEMES) {
  for (const size of SIZES) {
    test(`screenshots: every state, ${theme}, ${size.name}`, async ({ page, baseURL, context }) => {
      test.setTimeout(120_000);
      mkdirSync(SHOTS, { recursive: true });
      await page.setViewportSize({ width: size.width, height: size.height });
      const shot = async (name: string): Promise<void> => {
        await setTheme(page, theme);
        // Nothing may push the page sideways at any width.
        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
        expect(overflow, `${name}: the page is wider than the viewport`).toBeLessThanOrEqual(0);
        await page.screenshot({ path: join(SHOTS, `${theme}-${size.name}-${name}.png`), fullPage: true });
      };
      // The configuration error page.
      expect((await page.request.post("/_stub/reset")).ok()).toBe(true);
      expect((await page.request.post("/_stub/config", { data: { config: { mode: "console" } } })).ok()).toBe(true);
      await page.goto("/");
      await expect(page.getByTestId("config-error")).toBeVisible();
      await shot("00-config-error");
      // Idle.
      await serve(page, baseURL!);
      await page.goto("/");
      await expect(page.getByLabel("Email")).toBeVisible();
      await shot("01-idle");
      // Signing in.
      await arm(page, { delayMs: { exchange: 2000 } });
      await signInAsPartner(page);
      await expect(page.getByTestId("signin-submit")).toBeDisabled();
      await shot("02-signing-in");
      await expect(page.getByTestId("role-entry")).toBeVisible({ timeout: 15_000 });
      await shot("07-role-entry");
      await page.getByTestId("continue-own").click();
      await expect(page.getByTestId("home-title")).toBeVisible();
      await page.getByTestId("apps-menu").locator("summary").click();
      await shot("08-home");
      // Wrong credentials.
      await serve(page, baseURL!);
      await page.goto("/");
      await fill(page, PARTNER_EMAIL, "not the password");
      await page.getByTestId("signin-submit").click();
      await expect(status(page)).toContainText("That sign in did not work");
      await shot("03-wrong-credentials");
      // Not allowed, too many, unavailable.
      for (const [code, name] of [[403, "04-not-allowed"], [429, "05-too-many"], [503, "06-unavailable"]] as const) {
        await serve(page, baseURL!, {}, { exchange: { status: code } });
        await page.goto("/");
        await signInAsPartner(page);
        await expect(status(page)).not.toHaveText("Signing in.");
        await expect(status(page)).not.toHaveText("");
        await shot(name);
      }
      // Offline.
      await serve(page, baseURL!);
      await page.goto("/");
      await expect(page.getByLabel("Email")).toBeVisible();
      await context.setOffline(true);
      await fill(page, PARTNER_EMAIL, PARTNER_PASSWORD);
      await page.getByTestId("signin-submit").click();
      await expect(status(page)).toContainText("offline");
      await shot("09-offline");
      await context.setOffline(false);
      // Options failed.
      await serve(page, baseURL!, {}, { methodsStatus: { status: 503, times: 5 } });
      await page.goto("/");
      await expect(page.getByTestId("methods-failed")).toBeVisible();
      await shot("10-options-failed");
      // Session ended.
      await serve(page, baseURL!, {}, { windowSeconds: 2 });
      await page.goto("/");
      await signInAsPartner(page);
      await expect(page.getByTestId("role-entry")).toBeVisible();
      await arm(page, { exchange: { status: 401 } });
      await expect(status(page)).toHaveText("Your session ended. Sign in again.", { timeout: 15_000 });
      await shot("11-session-ended");
      // Testers' copy: the role entry with persona cards, and one refusal.
      await serve(page, baseURL!, { personas: true }, { personaStatus: { driver: 503 } });
      await page.goto("/");
      await signInAsPartner(page);
      await page.getByTestId("persona-walk").locator("summary").click();
      await expect(page.getByTestId("persona-cards")).toBeVisible();
      await page.getByTestId("persona-cards").getByRole("button", { name: "Driver", exact: true }).click();
      await expect(page.getByTestId("persona-cards")).toContainText("not available right now");
      await shot("12-role-entry-personas");
      // Public copy: persona cards on the sign in screen.
      await serve(page, baseURL!, { personas: true, personasOpen: true });
      await page.goto("/");
      await expect(page.getByTestId("persona-cards")).toBeVisible();
      await shot("13-public-personas");
    });
  }
}
