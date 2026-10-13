// The Pro apps live, for the contractor roles (cycle brief PRO-F item 5): the front door and the three Pro apps
// are the real demo builds on one origin; the tenant stub answers the demo routes and the tenant routes in the
// core's shape. Three legs of the roles roadmap's common set apply to a contractor:
//
//   L-card   a contractor persona signs in from its card on the front door and lands in the Pro app for its role.
//   L-wall   a surface a role lacks is refused by the server, whatever the screen offers.
//   L-cost   no dealer cost reaches a contractor: no cost, margin or markup field in any answer the Pro apps read.
//
// L-branch does not apply: the tenant routes carry no branch (the caller's own account is the only scope).
import { test, expect, type APIRequestContext } from "@playwright/test";
import { mkdirSync } from "node:fs";

// Set PRO_SHOTS to a folder to keep a screenshot of each landing for a visual review.
const SHOTS = process.env.PRO_SHOTS ?? "";

test.beforeEach(async ({ request }) => {
  expect((await request.post("/_stub/reset")).ok()).toBe(true);
});

// ---- L-card ---------------------------------------------------------------------------------------------

const CARDS: ReadonlyArray<{ label: string; app: "procurement" | "pay" | "field"; path: RegExp; surface: string }> = [
  { label: "Owner", app: "procurement", path: /\/pro-procurement\/\?as=account_admin$/, surface: "Today" },
  { label: "Buyer", app: "procurement", path: /\/pro-procurement\/\?as=buyer$/, surface: "Today" },
  { label: "Project manager", app: "procurement", path: /\/pro-procurement\/\?as=buyer_no_pay$/, surface: "Today" },
  { label: "Accounts payable", app: "pay", path: /\/pro-pay\/\?as=ap_finance$/, surface: "Invoices" },
  { label: "Field crew", app: "field", path: /\/pro-field\/\?as=field_crew$/, surface: "Tracking" },
];

for (const card of CARDS) {
  test(`L-card: the ${card.label} card signs the persona in and lands in Pro ${card.app}`, async ({ page }) => {
    await page.goto("/");
    if (SHOTS !== "" && card.label === "Owner") {
      mkdirSync(SHOTS, { recursive: true });
      await page.screenshot({ path: `${SHOTS}/front-door-picker.png`, fullPage: true });
    }
    await page.getByTestId("contractor-section").getByRole("button", { name: card.label, exact: true }).click();
    await expect(page).toHaveURL(card.path);
    const frame = page.locator("hh-pro-frame");
    await expect(frame).toBeVisible();
    await expect(frame).toHaveAttribute("data-app", card.app);
    await expect(frame).toHaveAttribute("data-role", card.label);
    await expect(page.getByTestId("surface-title")).toHaveText(card.surface);
    // No second sign in, no sign in card, and nothing of the person left in web storage.
    await expect(page.locator("hh-pro-signin")).toHaveCount(0);
    expect(await page.evaluate(() => window.localStorage.length + window.sessionStorage.length)).toBe(0);
    if (SHOTS !== "") {
      mkdirSync(SHOTS, { recursive: true });
      await page.screenshot({ path: `${SHOTS}/landing-${card.app}-${card.label.toLowerCase().replace(/ /g, "-")}.png` });
    }
  });
}

test("L-card: a Pro app opened with no card shows the sign in card with the demo roles to pick", async ({ page }) => {
  await page.goto("/pro-pay/");
  await expect(page.locator("hh-pro-signin")).toBeVisible();
  await expect(page.getByTestId("signin-demo-roles")).toBeVisible();
  await page.getByRole("button", { name: "Accounts payable", exact: true }).click();
  await expect(page.locator("hh-pro-frame")).toHaveAttribute("data-role", "Accounts payable");
});

test("L-card: hopping to another Pro app carries the role, so the person is not signed out on the way", async ({ page }) => {
  await page.goto("/pro-procurement/?as=buyer");
  await expect(page.locator("hh-pro-frame")).toHaveAttribute("data-role", "Buyer");
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  await panel.getByRole("button", { name: /Invoices/ }).click();
  await expect(page).toHaveURL(/\/pro-pay\/\?as=buyer$/);
  await expect(page.locator("hh-pro-frame")).toHaveAttribute("data-app", "pay");
  await expect(page.locator("hh-pro-frame")).toHaveAttribute("data-role", "Buyer");
});

test("L-card: the front door's role code asks for a persona only in the five contractor roles", async ({ page }) => {
  await page.goto("/pro-pay/?as=inside_sales");
  await expect(page.locator("hh-pro-signin")).toBeVisible();
  await expect(page.locator("hh-pro-frame")).toHaveCount(0);
});

// ---- L-wall ---------------------------------------------------------------------------------------------

const FLOW_OF: Readonly<Record<string, string>> = {
  account_admin: "contractor-account-admin",
  buyer: "contractor-buyer",
  buyer_no_pay: "contractor-buyer-no-pay",
  ap_finance: "contractor-ap-finance",
  field_crew: "contractor-field-crew",
};

async function bearerFor(request: APIRequestContext, role: string): Promise<string> {
  const minted = await request.post("/api/v1/demo/session", { data: { flow: FLOW_OF[role] } });
  expect(minted.ok()).toBe(true);
  const body = (await minted.json()) as { token: string; class: string };
  expect(body.class).toBe("tenant");
  return body.token;
}

// The matrix (core/internal/portal/rbac.go), one read route per capability the Pro apps lean on.
const WALL: ReadonlyArray<{ route: string; capability: string; roles: readonly string[] }> = [
  { route: "/api/v1/tenant/procurement/projects", capability: "submit_rfq", roles: ["account_admin", "buyer", "buyer_no_pay"] },
  { route: "/api/v1/tenant/invoices", capability: "view_billing", roles: ["account_admin", "buyer", "buyer_no_pay", "ap_finance"] },
  { route: "/api/v1/tenant/payment-methods", capability: "manage_payment_methods", roles: ["account_admin", "buyer", "ap_finance"] },
  { route: "/api/v1/tenant/orders", capability: "view_orders_deliveries", roles: ["account_admin", "buyer", "buyer_no_pay", "ap_finance", "field_crew"] },
  { route: "/api/v1/tenant/users", capability: "manage_users", roles: ["account_admin"] },
];

test("L-wall: the server refuses what a role lacks (403) and answers what it holds, for all five roles on every Pro route family", async ({ request }) => {
  for (const role of Object.keys(FLOW_OF)) {
    const token = await bearerFor(request, role);
    for (const { route, capability, roles } of WALL) {
      const answer = await request.get(route, { headers: { Authorization: `Bearer ${token}` } });
      expect(answer.status(), `${role} ${capability} ${route}`).toBe(roles.includes(role) ? 200 : 403);
    }
  }
});

test("L-wall: no bearer is a 401 and a card's role code opens nothing by itself", async ({ request }) => {
  expect((await request.get("/api/v1/tenant/orders")).status()).toBe(401);
  expect((await request.get("/api/v1/tenant/orders?as=account_admin")).status()).toBe(401);
});

test("L-wall: a role opened in an app that does not hold its surfaces is refused on screen and the server is never asked for them", async ({ page }) => {
  const calls: string[] = [];
  page.on("request", (r) => calls.push(new URL(r.url()).pathname));
  // Accounts payable in Pro Procurement: Today (the dashboard) is its own, the buying surfaces are not offered.
  await page.goto("/pro-procurement/?as=ap_finance");
  await expect(page.locator("hh-pro-frame")).toHaveAttribute("data-role", "Accounts payable");
  await page.getByTestId("frame-selector").click();
  const panel = page.locator("hh-pro-frame").getByRole("navigation", { name: "Apps and surfaces" });
  for (const name of ["Board", "Projects", "Catalog", "Quotes"]) await expect(panel.getByRole("button", { name, exact: true })).toHaveCount(0);
  expect(calls.filter((p) => p.startsWith("/api/v1/tenant/procurement") || p.startsWith("/api/v1/tenant/catalog") || p.startsWith("/api/v1/tenant/quotes"))).toEqual([]);
  // Field crew in Pro Pay: the landing is refused with no billing call.
  calls.length = 0;
  await page.goto("/pro-pay/?as=field_crew");
  await expect(page.getByTestId("landing-state")).toHaveAttribute("data-state", "refusal");
  expect(calls.filter((p) => p.startsWith("/api/v1/tenant/invoices") || p.startsWith("/api/v1/tenant/billing"))).toEqual([]);
});

// ---- L-cost ---------------------------------------------------------------------------------------------

const COST_KEY = /(^|_)(cost|margin|markup|vendor_price|landed)(_|$)/i;

function costKeys(value: unknown, path = "$"): string[] {
  if (Array.isArray(value)) return value.flatMap((item, index) => costKeys(item, `${path}[${index}]`));
  if (value !== null && typeof value === "object") {
    return Object.entries(value as Record<string, unknown>).flatMap(([key, inner]) => (COST_KEY.test(key) ? [`${path}.${key}`] : []).concat(costKeys(inner, `${path}.${key}`)));
  }
  return [];
}

test("L-cost applies: no cost, margin or markup field reaches a contractor in any answer the Pro apps read", async ({ request }) => {
  const token = await bearerFor(request, "account_admin");
  const headers = { Authorization: `Bearer ${token}` };
  const routes = [
    "/api/v1/tenant/catalog/search?q=paver",
    "/api/v1/tenant/orders",
    "/api/v1/tenant/quotes",
    "/api/v1/tenant/invoices",
    "/api/v1/tenant/procurement/projects",
    "/api/v1/tenant/dashboard",
    "/api/v1/tenant/billing/summary",
    "/api/v1/tenant/payments",
  ];
  let read = 0;
  for (const route of routes) {
    const answer = await request.get(route, { headers });
    expect(answer.ok(), route).toBe(true);
    expect(costKeys(await answer.json()), route).toEqual([]);
    read += 1;
  }
  expect(read).toBe(routes.length);
});
