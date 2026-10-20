// HH Drive, sub item 4a: could not deliver. A reason in the driver's own
// words, the handback sentence, and a stop that is closed for good.
import { test, expect, driverHandoff, resetStub, stubLog, takeFirstStopEnRoute, type LoggedCall } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

test("could not deliver asks what stopped the delivery and says what happens to the load", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await expect(page.getByRole("heading", { name: "Could not deliver" })).toBeVisible();
  await expect(page.locator("[data-handback-words]")).toContainText("Bring the load back to the yard");
  const confirm = page.getByRole("button", { name: "Could not deliver" });
  await expect(confirm).toBeDisabled();
  await page.getByRole("radio", { name: "Other, say what" }).check();
  await expect(page.getByLabel("What stopped this delivery?")).toBeVisible();
  await expect(confirm).toBeDisabled();
  await page.getByLabel("What stopped this delivery?").fill("   ");
  await expect(confirm).toBeDisabled();
  await page.getByLabel("What stopped this delivery?").fill("Nobody on site and the gate is locked");
  await expect(confirm).toBeEnabled();
});

test("recording it sends the status and the reason, and closes the stop with the handback sentence", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.getByRole("radio", { name: "Other, say what" }).check();
  await page.getByLabel("What stopped this delivery?").fill("  Nobody on site  ");
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Not delivered");
  await expect(page.getByRole("status")).toContainText("Recorded. This stop is closed.");
  await expect(page.locator("[data-handback]")).toContainText("Nobody on site");
  await expect(page.locator("[data-handback]")).toContainText("Bring the load back to the yard");
  // The move rides the outbox, and the replay declares the core's wire shape:
  // the status and the failure reason, on the stops scope's push.
  interface PushCall extends LoggedCall {
    body: { ops: Array<Record<string, unknown>> } | null;
  }
  await expect
    .poll(async () => {
      const pushes = (await stubLog(request)).filter(
        (c): c is PushCall => c.path === "/api/v1/sync/stops" && c.method === "POST",
      );
      return pushes.flatMap((p) => p.body?.ops ?? []).filter((op) => (op?.payload as Record<string, unknown>)?.status === "FAILED").length;
    })
    .toBe(1);
  const pushes = (await stubLog(request)).filter(
    (c): c is PushCall => c.path === "/api/v1/sync/stops" && c.method === "POST",
  );
  const failedOp = pushes.flatMap((p) => p.body?.ops ?? []).find((op) => (op?.payload as Record<string, unknown>)?.status === "FAILED");
  expect((failedOp?.payload as Record<string, unknown>).failure_reason).toBe("Nobody on site");
  expect(Object.keys(failedOp?.payload as Record<string, unknown>).sort()).toEqual(["failure_reason", "id", "status"]);
  await expect(page.getByRole("button", { name: "Delivered", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Could not deliver" })).toHaveCount(0);
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.locator("[data-progress]")).toHaveText("2 stops left · 0 delivered · 1 could not be delivered");
});

test("Back leaves the stop en route and sends nothing", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.getByRole("button", { name: "Back" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("En route");
  expect((await stubLog(request)).filter((c) => c.body?.status === "FAILED")).toHaveLength(0);
});
