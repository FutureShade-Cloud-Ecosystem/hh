// Pro Field in a real browser against the shared tenant stub. Visual references: the sign in screen is
// HH: Pro shell-signin-phone-brand; the frame is shell-frame-phone-light-r2 and shell-selector-phone-light; the
// landing is field-tracking-phone-brand (an order per row with its tracking word). Phone first (390 wide), then desktop. The legs cover the sign in, the frame, the
// selector and the landing surface's loading, ready, empty, error and refusal states.
import { test, expect, type Page } from "@playwright/test";

const PASSWORD = "stub-password";
const EMAIL = (who: string): string => `contractor.${who}@hh-demo.invalid`;

test.beforeEach(async ({ request }) => {
  const response = await request.post("/_stub/reset");
  expect(response.ok()).toBe(true);
});

async function signIn(page: Page, who: string, password = PASSWORD): Promise<void> {
  await page.goto("/");
  await page.getByTestId("signin-email").fill(EMAIL(who));
  await page.getByTestId("signin-password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

test.describe("phone", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("the sign in screen reads like a contractor's, with the dealer's name and nothing technical", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "HH Pro" })).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toContainText("Gable Landscape Supply");
    await expect(page.locator("hh-pro-signin")).toContainText("Use the email your contractor account knows.");
    const text = (await page.locator("hh-pro-signin").innerText()).toLowerCase();
    for (const word of ["console", "core", "token", "session", "exchange", "stub", "demo"]) expect(text).not.toContain(word);
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await page.screenshot({ path: test.info().outputPath("signin-phone.png") });
  });

  test("a wrong password is refused in plain words and the person stays on the sign in screen", async ({ page }) => {
    await signIn(page, "owner", "not-the-password");
    await expect(page.getByTestId("signin-message")).toHaveText("That email and password do not match.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("signin-password")).toHaveValue("");
  });

  test("signing in shows the frame with the dealer, the surface and the landing rows", async ({ page }) => {
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.getByTestId("frame-dealer")).toHaveText("Gable Landscape Supply");
    await expect(page.getByTestId("frame-selector")).toContainText("Tracking");
    await expect(page.getByTestId("surface-title")).toHaveText("Tracking");
    await expect(page.getByTestId("landing-rows")).toContainText("SO-");
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    await page.screenshot({ path: test.info().outputPath("landing-phone.png") });
  });

  test("the selector lists only the surfaces this role opens, and signing out returns to the sign in screen", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await expect(panel).toBeVisible();
    await expect(panel.getByRole("button", { name: "Tracking" })).toHaveAttribute("aria-current", "page");
    await page.screenshot({ path: test.info().outputPath("selector-phone.png") });
    await page.keyboard.press("Escape");
    await expect(panel).toHaveCount(0);
    await page.getByTestId("frame-sign-out").click();
    await expect(page.locator("hh-pro-signin")).toBeVisible();
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
  });

  test("nothing a person typed or was given rests in web storage, the address bar or the console", async ({ page }) => {
    const logged: string[] = [];
    page.on("console", (message) => logged.push(message.text()));
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    const stored = await page.evaluate(() => JSON.stringify({ local: { ...window.localStorage }, session: { ...window.sessionStorage }, cookie: document.cookie }));
    expect(stored).not.toMatch(/pro-stub|stub-password|Bearer/);
    expect(page.url()).not.toMatch(/pro-stub|token|password/);
    expect(logged.join("\n")).not.toMatch(/pro-stub|stub-password|Bearer/);
  });

  test("the landing shows a loading state while the server answers", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { delayMs: 900 } });
    await signIn(page, "field");
    await expect(page.getByTestId("landing-state")).toHaveAttribute("data-state", "loading");
    await expect(page.getByTestId("landing-rows")).toBeVisible();
  });

  test("a slow session check and slow data call keep the signed in frame with the loading pattern, never the sign in page", async ({ page }) => {
    await page.route("**/api/v1/tenant/**", async (route) => {
      if (route.request().url().endsWith("/tenant/config")) return route.continue();
      await new Promise((resolve) => setTimeout(resolve, 1800));
      return route.continue();
    });
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible({ timeout: 600 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "loading");
    await expect(state.locator('.state-indicator[data-pattern="progress"]')).toBeVisible();
    await expect(page.getByTestId("landing-rows")).toContainText("SO-", { timeout: 6000 });
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
  });

  test("a network failure on the session check or data call keeps the person in the frame with not reachable words and Try again", async ({ page }) => {
    let failMe = true;
    await page.route("**/api/v1/tenant/me", async (route) => {
      if (failMe) return route.abort("failed");
      return route.continue();
    });
    await signIn(page, "field");
    await expect(page.locator("hh-pro-frame")).toBeVisible();
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Pro is not reachable right now. Try again in a moment.");
    failMe = false;
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("landing-rows")).toContainText("SO-");
  });

  test("an empty answer shows this app's own empty words", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { empty: "orders" } });
    await signIn(page, "field");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "empty");
    await expect(state).toContainText("No orders to track yet");
  });

  test("a failure shows the error state, and Try again recovers once the server does", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/orders", status: 503 } } });
    await signIn(page, "field");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "error");
    await expect(state).toContainText("Nothing was changed.");
    await request.post("/_stub/reset");
    await state.getByRole("button", { name: "Try again" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
  });

  test("a server refusal shows the refusal state with no retry", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/orders", status: 403 } } });
    await signIn(page, "field");
    const state = page.getByTestId("landing-state");
    await expect(state).toHaveAttribute("data-state", "refusal");
    await expect(state).toContainText("Your role does not open this");
    await expect(state.getByRole("button", { name: "Try again" })).toHaveCount(0);
  });

  test("a lapsed session on the landing signs the person out with a plain message, not a role refusal", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/orders", status: 401 } } });
    await signIn(page, "field");
    await expect(page.getByTestId("signin-message")).toHaveText("You are signed out. Sign in again.");
    await expect(page.locator("hh-pro-frame")).toHaveCount(0);
    await expect(page.getByTestId("landing-state")).toHaveCount(0);
  });

  test("the selector moves between the order surfaces and each one filters its rows", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    await panel.getByRole("button", { name: "Deliveries" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Deliveries");
    const deliveryRows = page.getByTestId("landing-rows").getByTestId("order-row");
    await expect(deliveryRows).not.toHaveCount(0);
    for (const row of await deliveryRows.all()) await expect(row).toContainText("Delivery");
    await page.getByTestId("frame-selector").click();
    await panel.getByRole("button", { name: "Pickups" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Pickups");
    const pickupRows = page.getByTestId("landing-rows").getByTestId("order-row");
    await expect(pickupRows).not.toHaveCount(0);
    for (const row of await pickupRows.all()) {
      const words = (await row.innerText()).toLowerCase();
      expect(words.includes("pickup") || words.includes("will call")).toBe(true);
    }
  });

  test("when the orders list stops at 100 pages with a cursor left, the screen says the list is cut", async ({ page }) => {
    let pageNo = 0;
    await page.route("**/api/v1/tenant/orders*", async (route) => {
      pageNo += 1;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          items: [
            {
              id: `00000000-0000-4000-8000-${String(pageNo).padStart(12, "0")}`,
              order_no: `SO-${40000 + pageNo}`,
              customer_id: "00000000-0000-4000-8000-000000001001",
              quote_id: null,
              status: "CONFIRMED",
              fulfillment: "DELIVERY",
              requested_date: "2042-05-01",
              subtotal_cents: 10000,
              tax_cents: 800,
              total_cents: 10800,
              created_at: "2042-04-01T12:00:00Z",
              has_backorder: false,
            },
          ],
          next_cursor: `cursor-${pageNo}`,
          limit: 1,
        }),
      });
    });
    await signIn(page, "field");
    await expect(page.getByTestId("orders-truncated")).toHaveText(
      "Showing your newest orders. Older orders are not in this list; ask the dealer for an older order.",
    );
    await expect(page.getByTestId("landing-rows")).toContainText("SO-40001");
  });

  test("a row opens the order with its journey and its asked-for day, and back returns to the list", async ({ page }) => {
    await signIn(page, "field");
    const firstRow = page.getByTestId("landing-rows").getByTestId("order-row").first();
    const orderNo = (await firstRow.locator(".mono.name").innerText()).trim();
    await firstRow.click();
    await expect(page.getByTestId("surface-title")).toHaveText("Order");
    await expect(page.getByTestId("order-head")).toContainText(orderNo);
    await expect(page.getByTestId("order-head")).toContainText("Wanted by");
    const steps = page.getByTestId("order-timeline").locator(".tstep");
    await expect(steps).toHaveCount(5);
    await expect(page.getByTestId("order-lines")).toContainText("at $");
    await expect(page.getByTestId("order-totals")).toContainText("Tax");
    await page.screenshot({ path: test.info().outputPath("order-phone.png") });
    await page.getByRole("button", { name: "Orders" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await expect(page.getByTestId("surface-title")).toHaveText("Tracking");
  });

  test("the delivery notes open with the order's stored words, and saving them says so", async ({ page }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30503" }).click();
    const input = page.getByTestId("instructions-input");
    await expect(input).toHaveValue("Call the site lead 30 minutes before arrival.");
    await expect(input).toHaveAttribute("maxlength", "2000");
    await expect(page.getByTestId("instructions-count")).toHaveCount(0);
    await input.fill("x".repeat(1850));
    await expect(page.getByTestId("instructions-count")).toHaveText("1850 / 2000 characters");
    await input.fill("Call the site lead 30 minutes before arrival. Gate is on the north fence.");
    await expect(page.getByTestId("instructions-count")).toHaveCount(0);
    await page.getByRole("button", { name: "Save notes" }).click();
    await expect(page.getByTestId("instructions-saved")).toHaveText("Delivery instructions saved.");
    await page.screenshot({ path: test.info().outputPath("instructions-phone.png") });
  });

  test("a delivered order's notes read only, with the reason said", async ({ page }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30502" }).click();
    await expect(page.getByTestId("instructions-note")).toHaveText("No instructions.");
    await expect(page.locator("hh-pro-frame")).toContainText("The truck already left with these notes.");
    await expect(page.getByTestId("instructions-input")).toHaveCount(0);
  });

  test("a role without the notes capability reads them and is told why it cannot change them", async ({ page }) => {
    await signIn(page, "ap");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30503" }).click();
    await expect(page.getByTestId("instructions-note")).toHaveText("Call the site lead 30 minutes before arrival.");
    await expect(page.locator("hh-pro-frame")).toContainText("Your role reads these notes");
    await expect(page.getByRole("button", { name: "Save notes" })).toHaveCount(0);
  });

  test("requesting a return says what the dealer's answer allows, in plain words", async ({ page }) => {
    await signIn(page, "owner");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30501" }).click();
    await expect(page.getByTestId("returns-empty")).toBeVisible();
    await page.getByTestId("request-return").click();
    await expect(page.getByTestId("return-words")).toHaveText("This order cannot be returned through Pro. Ask the dealer to arrange it.");
    await expect(page.getByTestId("return-form")).toHaveCount(0);
  });

  test("a 403 on return eligibility reads as a role refusal naming who can request a return, with no retry", async ({ page }) => {
    await page.route("**/return-eligibility", (route) =>
      route.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: "insufficient permissions" }) }),
    );
    await signIn(page, "owner");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30501" }).click();
    await page.getByTestId("request-return").click();
    await expect(page.getByTestId("return-words")).toHaveText(
      "Your role cannot request a return. Ask an Owner, Buyer, or Project manager on your account to request it.",
    );
    await expect(page.getByTestId("returns-card").getByRole("button", { name: "Try again" })).toHaveCount(0);
  });

  test("a role that cannot commit a return sees no button and still sees the requests list", async ({ page }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30501" }).click();
    await expect(page.getByTestId("returns-empty")).toBeVisible();
    await expect(page.getByTestId("request-return")).toHaveCount(0);
  });

  test("the projects section lists the jobs, and one opens its rollup with its orders and invoices", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Projects" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Projects");
    const rows = page.getByTestId("projects-rows").getByTestId("project-row");
    await expect(rows).toHaveCount(3);
    await page.screenshot({ path: test.info().outputPath("projects-phone.png") });
    await rows.filter({ hasText: "La Jolla terrace" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("La Jolla terrace");
    await expect(page.getByTestId("project-stats")).toContainText("Spend");
    await expect(page.getByTestId("project-orders").getByTestId("project-order-row")).not.toHaveCount(0);
    await expect(page.locator("hh-pro-frame")).toContainText("Invoices");
    await page.screenshot({ path: test.info().outputPath("project-phone.png") });
  });

  test("an order inside a project rollup opens in the orders section with its journey", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Projects" }).click();
    await page.getByTestId("projects-rows").getByTestId("project-row").first().click();
    await page.getByTestId("project-orders").getByTestId("project-order-row").first().click();
    await expect(page.getByTestId("order-head")).toBeVisible();
    await expect(page.getByTestId("order-timeline")).toBeVisible();
    await page.getByRole("button", { name: "Orders" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
  });

  test("a role that cannot read billing sees no projects section at all", async ({ page }) => {
    await signIn(page, "field");
    const nav = page.getByTestId("section-nav");
    await expect(nav.getByRole("button", { name: "Orders" })).toBeVisible();
    await expect(nav.getByRole("button", { name: "Account" })).toBeVisible();
    await expect(nav.getByRole("button", { name: "Projects" })).toHaveCount(0);
    await expect(nav.getByRole("button", { name: "Team" })).toHaveCount(0);
  });

  test("the team lists its members, and a role change saves and says so", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Team");
    const members = page.getByTestId("team-members").getByTestId("member-row");
    await expect(members).toHaveCount(5);
    await expect(page.locator("hh-pro-frame")).toContainText("Field crew");
    await expect(page.getByTestId("invites-empty")).toBeVisible();
    const crewRow = members.filter({ hasText: "contractor.field@hh-demo.invalid" });
    await crewRow.getByTestId("member-role").selectOption({ label: "Buyer" });
    await expect(page.getByTestId("team-notice")).toHaveCount(0);
    await expect(crewRow.getByTestId("role-confirm")).toContainText("Change Contractor field crew from Field crew to Buyer?");
    await crewRow.getByTestId("confirm-role").click();
    await expect(page.getByTestId("team-notice")).toHaveText("Role saved.");
    await expect(crewRow.getByTestId("member-role")).toHaveValue("buyer");
    await page.screenshot({ path: test.info().outputPath("team-phone.png") });
  });

  test("a role change can be cancelled without asking the server, and an owner demoting themself is warned of losing Team access", async ({ page }) => {
    const rolePuts: string[] = [];
    page.on("request", (req) => {
      if (req.method() === "PUT" && req.url().includes("/role")) rolePuts.push(req.url());
    });
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    const members = page.getByTestId("team-members").getByTestId("member-row");
    const crewRow = members.filter({ hasText: "contractor.field@hh-demo.invalid" });
    await crewRow.getByTestId("member-role").selectOption({ label: "Buyer" });
    await expect(crewRow.getByTestId("role-confirm")).toBeVisible();
    await crewRow.getByTestId("cancel-role").click();
    await expect(crewRow.getByTestId("role-confirm")).toHaveCount(0);
    await expect(crewRow.getByTestId("member-role")).toHaveValue("field_crew");
    expect(rolePuts).toHaveLength(0);

    await crewRow.getByTestId("member-role").selectOption({ label: "Owner" });
    await crewRow.getByTestId("confirm-role").click();
    await expect(page.getByTestId("team-notice")).toHaveText("Role saved.");

    const ownerRow = members.filter({ hasText: "contractor.owner@hh-demo.invalid" });
    await ownerRow.getByTestId("member-role").selectOption({ label: "Buyer" });
    await expect(ownerRow.getByTestId("role-confirm")).toContainText(
      "Change your role from Owner to Buyer? You will lose access to Team and user management as soon as this saves.",
    );
    await ownerRow.getByTestId("cancel-role").click();
    await expect(ownerRow.getByTestId("member-role")).toHaveValue("account_admin");
  });

  test("deactivating a member asks the server, and the row says so and offers to reactivate", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    const crewRow = page.getByTestId("team-members").getByTestId("member-row").filter({ hasText: "contractor.field@hh-demo.invalid" });
    await crewRow.getByTestId("member-deactivate").click();
    await expect(crewRow).toContainText("Inactive");
    await expect(crewRow.getByTestId("member-reactivate")).toBeVisible();
    await crewRow.getByTestId("member-reactivate").click();
    await expect(crewRow).toContainText("Active");
  });

  test("a pending member row shows Invited and is not offered Reactivate or Deactivate", async ({ page }) => {
    await page.route("**/api/v1/tenant/users", async (route) => {
      const response = await route.fetch();
      const users = (await response.json()) as Record<string, unknown>[];
      users.push({
        id: "00000000-0000-4000-8000-000000009999",
        customer_id: "00000000-0000-4000-8000-000000001001",
        email: "contractor.pending@hh-demo.invalid",
        name: "Pending crew",
        role: "field_crew",
        status: "pending",
        notification_prefs: {},
        created_at: "",
        updated_at: "",
      });
      await route.fulfill({ response, json: users });
    });
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    const pendingRow = page.getByTestId("team-members").getByTestId("member-row").filter({ hasText: "contractor.pending@hh-demo.invalid" });
    await expect(pendingRow).toContainText("Invited");
    await expect(pendingRow.getByTestId("member-reactivate")).toHaveCount(0);
    await expect(pendingRow.getByTestId("member-deactivate")).toHaveCount(0);
  });

  test("the last owner's refusal reads in plain words, for the role picker and for Deactivate", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    const ownerRow = page.getByTestId("team-members").getByTestId("member-row").filter({ hasText: "contractor.owner@hh-demo.invalid" });
    await ownerRow.getByTestId("member-role").selectOption({ label: "Buyer" });
    await expect(page.getByTestId("team-error")).toHaveText("The account needs at least one active owner.");
    await expect(ownerRow.getByTestId("role-confirm")).toHaveCount(0);
    await expect(ownerRow.getByTestId("member-role")).toHaveValue("account_admin");
    await ownerRow.getByTestId("member-deactivate").click();
    await expect(page.getByTestId("team-error")).toHaveText("The account needs at least one active owner.");
    await expect(page.getByTestId("team-error")).not.toContainText("account_admin");
  });

  test("the invite form asks the server and carries its answer in plain words", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    await page.getByTestId("invite-email").fill("new.crew@hh-demo.invalid");
    await page.getByTestId("invite-role").selectOption({ label: "Field crew" });
    await page.getByRole("button", { name: "Create invite" }).click();
    await expect(page.getByTestId("team-error")).toHaveText("inviting users is not available yet");
    await expect(page.getByTestId("invite-email")).toHaveValue("new.crew@hh-demo.invalid");
  });

  test("the server's refusal to open the team shows the refusal state with no retry", async ({ page, request }) => {
    await request.post("/_stub/control", { data: { fault: { path: "/api/v1/tenant/users", status: 403 } } });
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    const state = page.getByTestId("team-state");
    await expect(state).toHaveAttribute("data-state", "refusal");
    await expect(state).toContainText("Your role does not open this");
    await expect(state.getByRole("button", { name: "Try again" })).toHaveCount(0);
  });

  test("the account shows the person's profile, and saving a new name says so", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("surface-title")).toHaveText("Account");
    await expect(page.getByTestId("account-name")).toHaveValue("Contractor owner");
    await expect(page.locator("hh-pro-frame")).toContainText("Owner");
    await expect(page.locator("hh-pro-frame")).toContainText("contractor.owner@hh-demo.invalid");
    const save = page.getByRole("button", { name: "Save profile" });
    await expect(save).toBeDisabled();
    await page.getByTestId("account-name").fill("Summit Ridge owner");
    await save.click();
    await expect(page.getByTestId("profile-saved")).toHaveText("Profile saved.");
    await page.screenshot({ path: test.info().outputPath("account-phone.png") });
  });

  test("the profile name is prefilled again on a second visit, and a typed draft is not kept blind", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-name")).toHaveValue("Contractor owner");
    await page.getByTestId("account-name").fill("a draft never saved");
    await page.getByTestId("section-nav").getByRole("button", { name: "Orders" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-name")).toHaveValue("Contractor owner");
    await expect(page.getByRole("button", { name: "Save profile" })).toBeDisabled();
  });

  test("the password form carries the server's answer in its own words", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await page.getByTestId("pw-current").fill("stub-password");
    await page.getByTestId("pw-new").fill("another-password");
    await page.getByTestId("pw-confirm").fill("another-password");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByTestId("password-error")).toHaveText("changing your password here is not available yet");
  });

  test("the password form is checked before the server is asked", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await page.getByTestId("pw-current").fill("stub-password");
    await page.getByTestId("pw-new").fill("another-password");
    await page.getByTestId("pw-confirm").fill("different");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByTestId("password-error")).toHaveText("The new passwords do not match.");
  });

  test("typed passwords do not survive leaving the account section, and none is sent unseen", async ({ page }) => {
    const passwordCalls: string[] = [];
    page.on("request", (request) => {
      if (request.url().includes("/me/password")) passwordCalls.push(`${request.method()} ${request.url()}`);
    });
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await page.getByTestId("pw-current").fill("stub-password");
    await page.getByTestId("pw-new").fill("another-password");
    await page.getByTestId("pw-confirm").fill("another-password");
    await page.getByTestId("section-nav").getByRole("button", { name: "Orders" }).click();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-password")).toBeVisible();
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByTestId("password-error")).toHaveText("Type your current password and a new one.");
    expect(passwordCalls).toHaveLength(0);
  });

  test("leaving a section during profile, password, or invite save does not leave the form stuck busy", async ({ page }) => {
    await signIn(page, "owner");

    let releaseProfile!: () => void;
    const profileHeld = new Promise<void>((resolve) => {
      releaseProfile = resolve;
    });
    await page.route("**/api/v1/tenant/me", async (route) => {
      if (route.request().method() === "PUT") {
        await profileHeld;
      }
      await route.continue();
    });

    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-name")).toHaveValue("Contractor owner");
    await page.getByTestId("account-name").fill("Summit Ridge owner");
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(page.getByRole("button", { name: "Save profile" })).toBeDisabled();
    await page.getByTestId("section-nav").getByRole("button", { name: "Orders" }).click();
    releaseProfile();
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-name")).toHaveValue("Summit Ridge owner");
    await page.getByTestId("account-name").fill("Summit Ridge second");
    await expect(page.getByRole("button", { name: "Save profile" })).toBeEnabled();

    let releasePassword!: () => void;
    const passwordHeld = new Promise<void>((resolve) => {
      releasePassword = resolve;
    });
    await page.route("**/api/v1/tenant/me/password", async (route) => {
      await passwordHeld;
      await route.continue();
    });

    await page.getByTestId("pw-current").fill("stub-password");
    await page.getByTestId("pw-new").fill("another-password");
    await page.getByTestId("pw-confirm").fill("another-password");
    await page.getByRole("button", { name: "Change password" }).click();
    await expect(page.getByRole("button", { name: "Change password" })).toBeDisabled();
    await page.getByTestId("section-nav").getByRole("button", { name: "Orders" }).click();
    releasePassword();
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByRole("button", { name: "Change password" })).toBeEnabled();

    let releaseInvite!: () => void;
    const inviteHeld = new Promise<void>((resolve) => {
      releaseInvite = resolve;
    });
    await page.route("**/api/v1/tenant/invites", async (route) => {
      if (route.request().method() === "POST") {
        await inviteHeld;
      }
      await route.continue();
    });

    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    await page.getByTestId("invite-email").fill("new.crew@hh-demo.invalid");
    await page.getByRole("button", { name: "Create invite" }).click();
    await expect(page.getByRole("button", { name: "Create invite" })).toBeDisabled();
    await page.getByTestId("section-nav").getByRole("button", { name: "Orders" }).click();
    releaseInvite();
    await page.getByTestId("section-nav").getByRole("button", { name: "Team" }).click();
    await page.getByTestId("invite-email").fill("new.crew@hh-demo.invalid");
    await expect(page.getByRole("button", { name: "Create invite" })).toBeEnabled();
  });

  test("an expired session on a write says so in dealer words with a sign in action, never raw server words", async ({ page, request }) => {
    await signIn(page, "field");
    await page.locator("[data-testid='order-row']", { hasText: "SO-30503" }).click();
    const input = page.getByTestId("instructions-input");
    await expect(input).toHaveValue("Call the site lead 30 minutes before arrival.");
    await page.route("**/delivery-instructions", async (route) => {
      await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "Missing or invalid credentials" }) });
    });
    await input.fill("Gate is on the north fence.");
    await page.getByRole("button", { name: "Save notes" }).click();
    await expect(page.getByTestId("signin-message")).toHaveText("You were signed out, so this was not saved. Sign in again and make the change again.");
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
    await expect(page.locator("hh-pro-signin")).not.toContainText("Missing or invalid credentials");

    await request.post("/_stub/reset");
    await signIn(page, "owner");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-name")).toHaveValue("Contractor owner");
    await page.route("**/api/v1/tenant/me", async (route) => {
      if (route.request().method() === "PUT") {
        await route.fulfill({ status: 401, contentType: "application/json", body: JSON.stringify({ error: "Missing or invalid credentials" }) });
        return;
      }
      await route.continue();
    });
    await page.getByTestId("account-name").fill("Summit Ridge owner");
    await page.getByRole("button", { name: "Save profile" }).click();
    await expect(page.getByTestId("signin-message")).toHaveText("You were signed out, so this was not saved. Sign in again and make the change again.");
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("the addresses list the jobs, and one opens in detail and back returns", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    const rows = page.getByTestId("address-rows").getByTestId("address-row");
    await expect(rows).toHaveCount(4);
    await page.screenshot({ path: test.info().outputPath("addresses-phone.png") });
    await rows.filter({ hasText: "La Jolla job" }).click();
    await expect(page.getByTestId("address-detail")).toBeVisible();
    await expect(page.getByTestId("address-detail")).toContainText("Job site");
    await expect(page.getByTestId("address-detail")).toContainText("88 Terrace Rise");
    await page.getByRole("button", { name: "Back" }).click();
    await expect(page.getByTestId("address-rows")).toBeVisible();
  });

  test("a surface in another app says it opens that app and asks to sign in again, one in this app says nothing", async ({ page }) => {
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const elsewhere = panel.locator("button[data-app]:not([data-app='field'])");
    expect(await elsewhere.count()).toBeGreaterThan(0);
    for (const button of await elsewhere.all()) {
      const app = await button.getAttribute("data-app");
      const name = app === "pay" ? "Pro Pay" : app === "field" ? "Pro Field" : "Pro Procurement";
      await expect(button.getByTestId("opens-note")).toHaveText(`opens ${name}, sign in again`);
    }
    await expect(panel.locator("button[data-app='field'] [data-testid='opens-note']")).toHaveCount(0);
  });

  test("choosing another app's surface goes to that app's own address", async ({ page }) => {
    await page.route(/^https?:\/\/[^/]+\/pro-(procurement|pay|field)\/$/, (route) => route.fulfill({ status: 200, contentType: "text/html", body: "<title>elsewhere</title>" }));
    await signIn(page, "owner");
    await page.getByTestId("frame-selector").click();
    const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
    const elsewhere = panel.locator("button[data-app]:not([data-app='field'])").first();
    const target = await elsewhere.getAttribute("data-app");
    await elsewhere.click();
    await page.waitForURL(/\/pro-[a-z]+\/$/);
    expect(new URL(page.url()).pathname).toBe(`/pro-${target === "procurement" ? "procurement" : target}/`);
  });

  test("Orders at 390x844 has zero serious contrast violations across the section segments", async ({ page }) => {
    const contrastOffenders = (floor = 4.5) =>
      page.evaluate((minRatio: number) => {
        const parse = (v: string): [number, number, number, number] | null => {
          const m = v.match(/rgba?\(([^)]+)\)/);
          if (!m) return null;
          const p = m[1].split(",").map((s) => Number.parseFloat(s));
          return p.length >= 3 && p.slice(0, 3).every((n) => !Number.isNaN(n))
            ? [p[0], p[1], p[2], p.length > 3 && !Number.isNaN(p[3]) ? p[3] : 1]
            : null;
        };
        const over = (t: [number, number, number, number], b: [number, number, number, number]): [number, number, number, number] => {
          const a = t[3] + b[3] * (1 - t[3]);
          if (a === 0) return [0, 0, 0, 1];
          const mix = (x: number, y: number): number => (t[3] * x + b[3] * (1 - t[3]) * y) / a;
          return [mix(t[0], b[0]), mix(t[1], b[1]), mix(t[2], b[2]), 1];
        };
        const parentOf = (n: Element): Element | null => {
          if (n.parentElement) return n.parentElement;
          const root = n.getRootNode();
          return root instanceof ShadowRoot ? root.host : null;
        };
        const backdrop = (el: Element): [number, number, number, number] => {
          let acc: [number, number, number, number] | null = null;
          for (let cur: Element | null = el; cur !== null; cur = parentOf(cur)) {
            const layer = parse(getComputedStyle(cur).backgroundColor);
            if (layer && layer[3] > 0) {
              acc = acc === null ? layer : over(acc, layer);
              if (acc[3] >= 1) return acc;
            }
          }
          return acc === null ? [255, 255, 255, 1] : over(acc, [255, 255, 255, 1]);
        };
        const ch = (v: number): number => {
          const s = v / 255;
          return s <= 0.04045 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
        };
        const lum = (c: [number, number, number, number]): number => 0.2126 * ch(c[0]) + 0.7152 * ch(c[1]) + 0.0722 * ch(c[2]);
        const ratio = (a: [number, number, number, number], b: [number, number, number, number]): number =>
          (Math.max(lum(a), lum(b)) + 0.05) / (Math.min(lum(a), lum(b)) + 0.05);
        const out: { text: string; ratio: number }[] = [];
        const walk = (root: Element | ShadowRoot | Document): void => {
          for (const el of Array.from(root.querySelectorAll("*"))) {
            if (el.shadowRoot) walk(el.shadowRoot);
            const cs = getComputedStyle(el);
            const rect = el.getBoundingClientRect();
            if (cs.display === "none" || cs.visibility === "hidden" || rect.width < 1 || rect.height < 1) continue;
            if (!Array.from(el.childNodes).some((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").trim() !== "")) continue;
            const ink = parse(cs.color);
            if (!ink) continue;
            const r = ratio(ink, backdrop(el));
            if (r < minRatio) out.push({ text: (el.textContent ?? "").trim().replace(/\s+/g, " ").slice(0, 40), ratio: Math.round(r * 100) / 100 });
          }
        };
        walk(document);
        return out;
      }, floor);

    await signIn(page, "owner");
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await expect.poll(() => contrastOffenders(4.5)).toEqual([]);
    await page.getByTestId("section-nav").getByRole("button", { name: "Account" }).click();
    await expect(page.getByTestId("account-name")).toBeVisible();
    await expect.poll(() => contrastOffenders(4.5)).toEqual([]);
  });
});

test.describe("desktop", () => {
  test.use({ viewport: { width: 1440, height: 900 } });

  test("the frame shows the role beside the sign out and the rows fit the page", async ({ page }) => {
    await signIn(page, "field");
    await expect(page.getByTestId("frame-role")).toBeVisible();
    await expect(page.getByTestId("landing-rows")).toBeVisible();
    await page.screenshot({ path: test.info().outputPath("landing-desktop.png") });
  });

  test("an order view reads as a page at desk width, with the journey beside its facts", async ({ page }) => {
    await signIn(page, "field");
    await page.getByTestId("landing-rows").getByTestId("order-row").first().click();
    await expect(page.getByTestId("order-head")).toBeVisible();
    await expect(page.getByTestId("order-timeline")).toBeVisible();
    await page.screenshot({ path: test.info().outputPath("order-desktop.png") });
  });
});
