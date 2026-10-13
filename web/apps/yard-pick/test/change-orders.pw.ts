// The yard's stage of the change orders ladder (stage 2): the "Changes
// waiting on the yard" list and its acknowledgement, held against the stub's
// change routes, which serve the core's shapes and refusal words field by
// field (stub-contract.test.mjs holds that half).
//
// The stories this suite holds are the brief's own legs:
//   the landing offers the door with the count of waiting changes;
//   the list shows each row's order number, reason, who asked, and the age;
//   an empty list says plainly that the counter raises the changes;
//   the sheet's acknowledge carries the optional note, the row leaves, and
//     the next stage word comes from the server's answer;
//   a colleague's acknowledgement the screen never saw (the race the core
//     refuses) shows the server's own sentence in place, the sheet kept;
//   with money laid on every read, none of it reaches the glass (the walk in
//     money.pw.ts covers the poisoned half).
import { test, expect } from "@playwright/test";
import { resetStub, stubLog, useScenario, yardHandoff } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(yardHandoff);
});

test("the landing offers the changes waiting on the yard, with the count", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("[data-pick-card]").first()).toBeVisible();
  const door = page.locator("[data-changes-door]");
  await expect(door).toContainText("Changes waiting on the yard");
  await expect(door.locator("[data-changes-count]")).toHaveText("2");
});

test("the list shows each waiting change: order, reason, who asked, and the age", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  await expect(page.getByRole("heading", { name: "Changes waiting on the yard" })).toBeVisible();
  const rows = page.locator("[data-change-row]");
  await expect(rows).toHaveCount(2);
  const first = rows.nth(0);
  await expect(first.locator("[data-change-no]")).toHaveText("SO-1039");
  await expect(first).toContainText("Customer halved the wall block");
  await expect(first).toContainText("Sales Manager");
  await expect(first).toContainText("waiting 2h");
  await expect(first.locator("[data-change-status]")).toHaveText("With yard");
  const second = rows.nth(1);
  await expect(second.locator("[data-change-no]")).toHaveText("SO-1042");
  await expect(second).toContainText("Customer added two more pallets");
  await expect(second).toContainText("Inside Sales");
});

test("an empty yard says who raises a change", async ({ page, request }) => {
  await useScenario(request, "empty");
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  const empty = page.locator("[data-changes-empty]");
  await expect(empty).toBeVisible();
  await expect(empty).toContainText("Nothing is waiting on the yard.");
  await expect(empty).toContainText("The counter raises a change");
});

test("the sheet's acknowledgement carries the note, and the row leaves the list", async ({ page, request }) => {
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  await page.locator("[data-change-row]", { hasText: "SO-1042" }).locator(".line-row").click();
  const sheet = page.locator("[data-change-sheet]");
  await expect(sheet).toBeVisible();
  await expect(sheet).toContainText("CO-2042");
  await expect(sheet).toContainText("SO-1042");
  await expect(sheet).toContainText("Customer added two more pallets");
  await expect(sheet).toContainText("Inside Sales");
  await page.getByLabel("A note for the counter, optional").fill("can still do it");
  await page.getByRole("button", { name: "Acknowledge" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Acknowledged. The change is approved and waits to be applied.");
  await expect(page.locator("[data-change-sheet]")).toHaveCount(0);
  await expect(page.locator("[data-change-row]")).toHaveCount(1);
  const calls = await stubLog(request);
  const ack = calls.find((c) => c.method === "POST" && c.path.endsWith("/yard-ack"));
  expect(ack?.body).toEqual({ note: "can still do it" });
});

test("a change over the threshold lands on finance's word after the yard", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  await page.locator("[data-change-row]", { hasText: "SO-1039" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Acknowledge" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Acknowledged. The change now waits on finance.");
});

test("a colleague's acknowledgement the screen never saw keeps the sheet and says so", async ({ page, request }) => {
  await useScenario(request, "change-decided");
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  await page.locator("[data-change-row]").first().locator(".line-row").click();
  await page.getByRole("button", { name: "Acknowledge" }).click();
  await expect(page.locator("[data-change-error]")).toHaveText(
    "This change order is already closed: the yard has already acknowledged this change.",
  );
  await expect(page.locator("[data-change-sheet]")).toBeVisible();
});

test("the sheet and its typed note do not outlive the person: switch role, and sign out", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  await page.locator("[data-change-row]", { hasText: "SO-1039" }).locator(".line-row").click();
  await expect(page.locator("[data-change-sheet]")).toBeVisible();
  await page.getByLabel("A note for the counter, optional").fill("the truck can take it Friday");
  // The shared phone changes hands: the next persona signs in and opens the
  // list. Nothing of the last person's may be on the glass.
  await page.getByRole("button", { name: "Switch role" }).click();
  await page.getByRole("button", { name: "Yard Team" }).click();
  await expect(page.locator("[data-pick-card]").first()).toBeVisible();
  await page.locator("[data-changes-door]").click();
  await expect(page.locator("[data-change-sheet]")).toHaveCount(0);
  // The other way out leaves nothing behind either.
  await page.locator("[data-change-row]", { hasText: "SO-1042" }).locator(".line-row").click();
  await page.getByLabel("A note for the counter, optional").fill("the second pallet is damp");
  await page.getByRole("button", { name: "Sign out" }).click();
  await page.getByRole("button", { name: "Yard Team" }).click();
  await expect(page.locator("[data-pick-card]").first()).toBeVisible();
  await page.locator("[data-changes-door]").click();
  await expect(page.locator("[data-change-sheet]")).toHaveCount(0);
  await expect(page.locator("[data-change-row]")).toHaveCount(2);
});

test("back from the changes, the door's count has caught up", async ({ page }) => {
  await page.goto("/");
  await page.locator("[data-changes-door]").click();
  await page.locator("[data-change-row]", { hasText: "SO-1042" }).locator(".line-row").click();
  await page.getByRole("button", { name: "Acknowledge" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Acknowledged");
  await page.getByRole("button", { name: "Back to the queue" }).click();
  const door = page.locator("[data-changes-door]");
  await expect(door).toBeVisible();
  await expect(door.locator("[data-changes-count]")).toHaveText("1");
});
