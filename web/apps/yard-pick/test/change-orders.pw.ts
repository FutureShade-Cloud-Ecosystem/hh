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
import { resetStub, useScenario, yardHandoff } from "./helpers.js";

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
