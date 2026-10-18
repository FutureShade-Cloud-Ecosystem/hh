// The yard team's story, part one: claim a pick, record the lines, change
// the skid count, stage the pick. Written against the core's contract
// (PR 274's mint row and the yard routes on main) and proved on the demo
// image; this machine cannot build the image.
import { expect, test } from "./helpers.js";
import { POINT_LOMA, arriveFromFrontDoor, queue, shot, staff } from "./helpers.js";

/** One open pick at Point Loma, claimed by nobody yet. */
async function anUnclaimedPick(page: import("@playwright/test").Page): Promise<any> {
  const answer = await queue(page);
  expect(answer.status).toBe(200);
  const open = (answer.body.items as any[]).filter((e) => e.task.status === "QUEUED" || e.task.status === "PUSHED");
  expect(open.length, "the demo seed has open picks at Point Loma").toBeGreaterThan(0);
  return open[0];
}

test("the card claims a pick, records its lines in full, changes the skid count and stages", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  const pick = await anUnclaimedPick(page);
  const id = pick.task.id;

  // Claim.
  const claimed = await staff(page, "POST", `/api/v1/tasks/${id}/claim`);
  expect(claimed.status).toBe(200);
  expect(claimed.body.status).toBe("ACCEPTED");
  expect(claimed.body.claimed_by).toBe((await (await import("./helpers.js")).mint("yard-team")).user.user_id);

  // Start.
  const started = await staff(page, "POST", `/api/v1/tasks/${id}/accept`);
  expect(started.status).toBe(200);
  expect(started.body.status).toBe("IN_PROGRESS");

  // Record the lines in full, the whole set in one call.
  const detail = await staff(page, "GET", `/api/v1/yard/picks/${id}`);
  expect(detail.status).toBe(200);
  const lines = (detail.body.ticket?.lines ?? []).map((l: any) => ({
    order_line_no: l.line_no,
    product_id: l.product_id ?? "",
    sku: l.sku ?? "",
    description: l.description,
    qty_ordered: l.qty_ordered,
    qty_picked: l.qty_ordered,
    uom: l.uom,
  }));
  expect(lines.length).toBeGreaterThan(0);
  const recorded = await staff(page, "PUT", `/api/v1/yard/picks/${id}/lines`, { lines });
  expect(recorded.status).toBe(200);
  expect(recorded.body.items.length).toBe(lines.length);

  // Skids, with a reason.
  const skids = await staff(page, "PUT", `/api/v1/yard/picks/${id}/skids`, { skid_count: 12, reason: "COUNT_CORRECTION" });
  expect(skids.status).toBe(200);
  expect(skids.body.skid_count).toBe(12);

  // Stage.
  const staged = await staff(page, "POST", `/api/v1/yard/picks/${id}/stage`, { location: "Bay 3" });
  expect(staged.status).toBe(200);
  expect(staged.body.status).toBe("STAGED");
  expect(staged.body.stage_location).toBe("Bay 3");
  expect(staged.body.stage_ref).toMatch(/^STG-/);

  await shot(page, "pick-staged", testInfo);
});

test("the screen walks the same story", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page);
  await expect(page.locator("[data-pick-card]").first()).toBeVisible();
  const firstOpen = page.locator("[data-pick-card]").filter({ has: page.locator("[data-task-status]", { hasText: "Queued" }) }).first();
  await firstOpen.click();
  await expect(page.locator("[data-notice]")).toContainText("Claimed. It is yours to pull.");
  await expect(page.locator("[data-task-status]")).toHaveText("Picking");
  for (const line of await page.locator("[data-line]").all()) {
    await line.locator(".line-row").click();
    await page.getByRole("button", { name: /^All \d+/ }).click();
    await page.getByRole("button", { name: "Done" }).click();
  }
  await page.getByRole("button", { name: "Save pick" }).click();
  await expect(page.locator("[data-notice]")).toContainText("pulled in full");
  await page.getByRole("button", { name: "One skid more" }).click();
  await page.locator('[data-reason-group="skid"] [data-reason="COUNT_CORRECTION"]').click();
  await page.getByRole("button", { name: "Save skid count" }).click();
  await expect(page.locator("[data-notice]")).toContainText(/now holds \d+ skids?\./);
  await page.getByLabel("The stage location").fill("Bay 3");
  await page.getByRole("button", { name: "Stage", exact: true }).click();
  await expect(page.locator("[data-notice]")).toContainText("Staged at Bay 3");
  await shot(page, "pick-walked", testInfo);
});

test("Point Loma is the only branch the board names", async ({ page }) => {
  await arriveFromFrontDoor(page);
  await expect(page.locator("[data-branch]")).toHaveText(/Point Loma/);
});
