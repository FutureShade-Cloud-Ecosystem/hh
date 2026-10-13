// The Logistics Manager's first walk on the image: a load waiting for a truck goes onto a planned
// truck that needs a crew, the truck cannot go until it has one, and then it goes and loads what is
// on it. Every check reads the screen against the core's own answer.
import { expect, myBranch, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

async function dayWithWork(page: import("@playwright/test").Page, branch: string): Promise<{ date: string; stop: any; run: any }> {
  const first = await staff(page, "GET", `/api/v1/dispatch/loadboard?branch_id=${branch}`);
  expect(first.status).toBe(200);
  const start = first.body.date as string;
  for (let n = 0; n < 6; n += 1) {
    const d = new Date(`${start}T12:00:00Z`);
    d.setUTCDate(d.getUTCDate() + n);
    const date = d.toISOString().slice(0, 10);
    const board = await staff(page, "GET", `/api/v1/dispatch/loadboard?branch_id=${branch}&date=${date}`);
    const run = (board.body.loads as any[]).find((r) => r.run.status === "PLANNED" && !(r.run.truck_id && r.run.driver_id));
    const stop = (board.body.staging as any[]).find((s) => s.scheduled_date === date && s.status === "READY");
    if (run !== undefined && stop !== undefined) return { date, stop, run: run.run };
  }
  throw new Error("the seeded world holds no day with a waiting load and a planned truck that needs a crew");
}

test("a waiting load goes onto the truck that needs a crew, and the truck goes once it has one", async ({ page }, testInfo) => {
  const branch = await myBranch(page);
  const { date, stop, run } = await dayWithWork(page, branch);
  await arriveFromFrontDoor(page);
  await page.locator("input[data-field='date']").fill(date);
  await expect(page.locator(`[data-run-no='${run.run_no}']`)).toBeVisible();
  const card = page.locator("li.load", { hasText: stop.order_no });
  await expect(card).toBeVisible();
  await shot(page, "20-before", testInfo);

  await card.locator("select").first().selectOption({ label: new RegExp(run.run_no) as unknown as string }).catch(async () => {
    const options = card.locator("select option:not([disabled])");
    const labels = await options.allTextContents();
    const index = labels.findIndex((l) => l.includes(run.run_no));
    await card.locator("select").first().selectOption({ index });
  });
  await expect(page.locator("[data-notice]")).toContainText(stop.order_no);
  const moved = await staff(page, "GET", `/api/v1/dispatch/stops/${stop.id}`);
  expect(moved.body.run_id).toBe(run.id);
  expect(moved.body.commit_state).toBe("COMMITTED");

  const lane = page.locator(`[data-run-no='${run.run_no}']`);
  await lane.getByRole("button", { name: "Dispatch" }).click();
  await expect(lane.locator("[data-lane-error]")).toContainText("a run needs both a truck and a driver before it can be dispatched");
  await expect(lane.locator("[data-dispatch-why]")).toContainText("truck and a driver");
  await lane.locator("select[data-field='crew-truck']").selectOption({ index: 1 });
  await expect(page.locator("[data-notice]")).toHaveText("Crew set.");
  await lane.locator("select[data-field='crew-driver']").selectOption({ index: 1 });
  await expect(page.locator("[data-notice]")).toHaveText("Crew set.");
  await shot(page, "21-crewed", testInfo);
  await lane.getByRole("button", { name: "Dispatch" }).click();
  await expect(page.locator("[data-notice]")).toContainText(`${run.run_no} dispatched`);
  const after = await staff(page, "GET", `/api/v1/dispatch/runs/${run.id}`);
  expect(after.body.status).toBe("DISPATCHED");
  await shot(page, "22-dispatched", testInfo);
});
