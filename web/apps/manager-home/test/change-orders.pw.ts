// HH-681 Manager Home: change orders waiting on the manager.
import { test, expect } from "@playwright/test";
import { resetStub, useScenario } from "./helpers.js";

test.use({ viewport: { width: 1440, height: 900 } });

test.beforeEach(async ({ request }) => {
  await resetStub(request);
});

test("the Change orders tab shows the waiting count", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  const tab = page.locator(".kind-tab", { hasText: "Change orders" });
  await expect(tab).toContainText("Change orders 2");
});

test("a PENDING_FINANCE card signs and applies in one tap and leaves the list", async ({ page, request }) => {
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await page.locator(".kind-tab", { hasText: "Change orders" }).click();
  const row = page.locator("[data-approval]").first();
  await expect(row.locator(".detail")).toContainText("CO-PLM-1001");
  await row.getByRole("button", { name: "Sign and apply" }).click();
  await expect(page.locator("[data-approval]")).toHaveCount(1);
  // The stub's log records the idempotency key.
  const log = await (await request.get("/_stub/log")).json();
  const signApply = log.find((entry: { method: string; path: string }) => entry.method === "POST" && entry.path.includes("/sign-apply"));
  expect(signApply).toBeTruthy();
  expect(signApply.idempotencyKey).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i);
});

test("a double press of Sign and apply sends one request", async ({ page, request }) => {
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await page.locator(".kind-tab", { hasText: "Change orders" }).click();
  await page.locator("[data-approval]").first().getByRole("button", { name: "Sign and apply" }).click({ clickCount: 2 });
  // The card leaves the list when the request is answered; the stub's log is read after that, never at the click.
  await expect(page.locator("[data-approval]")).toHaveCount(1);
  const log = await (await request.get("/_stub/log")).json();
  const signApply = log.filter((entry: { method: string; path: string }) => entry.method === "POST" && entry.path.includes("/sign-apply"));
  expect(signApply).toHaveLength(1);
});

test("an APPROVED card applies without a signature", async ({ page, request }) => {
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await page.locator(".kind-tab", { hasText: "Change orders" }).click();
  const rows = page.locator("[data-approval]");
  await rows.nth(1).click();
  await page.locator("[data-approval-detail]").getByRole("button", { name: "Sign and apply" }).click();
  await expect(page.locator("[data-approval]")).toHaveCount(1);
  const log = await (await request.get("/_stub/log")).json();
  const signApply = log.find((entry: { method: string; path: string }) => entry.method === "POST" && entry.path.includes("/sign-apply"));
  expect(signApply).toBeTruthy();
});

test("Reject asks for a note and removes the card", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await page.locator(".kind-tab", { hasText: "Change orders" }).click();
  const row = page.locator("[data-approval]").first();
  await row.getByRole("button", { name: "Reject" }).click();
  await expect(page.locator(".error-line")).toContainText("A note is needed to reject");
  await page.locator("input[data-field='decline-reason']").fill("Customer cancelled the add-on");
  await row.getByRole("button", { name: "Reject" }).click();
  await expect(page.locator("[data-approval]")).toHaveCount(1);
});

test("a caller whose next_actions lacks sign_apply sees no such button", async ({ request, page }) => {
  await useScenario(request, "change-order-no-sign");
  await page.goto("/");
  await page.locator("[data-flow='general-manager']").click();
  await page.locator(".view-link", { hasText: "Approvals" }).click();
  await page.locator(".kind-tab", { hasText: "Change orders" }).click();
  await expect(page.locator("[data-approval]").first()).toContainText("Waiting on finance");
  await expect(page.locator("[data-approval]").first().getByRole("button", { name: "Sign and apply" })).toHaveCount(0);
});
