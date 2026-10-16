// The Logistics Manager's settings tab on the image: the rules on screen are the rules the core holds,
// and the trail either shows the core's rows or says why it is not shown. Reads only: the dealer's
// rules are not changed on the shared image.
import { expect, myBranch, staff, test, shot, arriveFromFrontDoor } from "./helpers.js";

test("the settings tab shows the rules the core holds", async ({ page }, testInfo) => {
  const branch = await myBranch(page);
  await arriveFromFrontDoor(page);
  await page.locator("[data-tab-id='settings']").first().click();
  await expect(page.locator("[data-rules]")).toBeVisible();
  const policy = await staff(page, "GET", `/api/v1/dispatch/policy?branch_id=${branch}`);
  expect(policy.status).toBe(200);
  const p = policy.body as { commit_prompt_hours: number; commit_horizon_weeks: number; working_view_days: number; driver_session_hours: number };
  await expect(page.locator("[data-rule='commit_prompt_hours'] [data-rule-word]")).toHaveText(`${p.commit_prompt_hours} ${p.commit_prompt_hours === 1 ? "hour" : "hours"}`);
  await expect(page.locator("[data-rule='commit_horizon_weeks'] [data-rule-word]")).toHaveText(`${p.commit_horizon_weeks} ${p.commit_horizon_weeks === 1 ? "week" : "weeks"}`);
  await expect(page.locator("[data-rule='working_view_days'] [data-rule-word]")).toHaveText(`${p.working_view_days} ${p.working_view_days === 1 ? "day" : "days"}`);
  await expect(page.locator("[data-rule='driver_session_hours'] [data-rule-word]")).toHaveText(`${p.driver_session_hours} ${p.driver_session_hours === 1 ? "hour" : "hours"}`);
  const trail = await staff(page, "GET", `/api/v1/dispatch/policy/history?branch_id=${branch}`);
  if (trail.status === 200) {
    await expect(page.locator("[data-history-row]")).toHaveCount((trail.body.items as unknown[]).length);
  } else {
    await expect(page.locator("[data-history-fault]")).toBeVisible();
  }
  await shot(page, "60-settings", testInfo);
});
