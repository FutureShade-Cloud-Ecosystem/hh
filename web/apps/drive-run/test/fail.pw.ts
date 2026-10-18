// HH Drive, sub item 4a: could not deliver. A reason chosen from the sheet's
// five chips, the handback sentence once it lands, and a stop that is closed
// for good. The fail view is the bottom sheet (hh-sheet) the design system
// provides; the sheet's title is "Why could it not be delivered?" and the
// confirm button reads "Take a photo and hand it back". The sheet's scrim
// closes it (Escape and a tap on the scrim).
import { test, expect, driverHandoff, resetStub, stubLog, takeFirstStopEnRoute, type LoggedCall } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

// The sheet closes through its scrim: tapping the scrim calls the sheet's
// close path, fires hh-sheet-close, and brings the stop card back.
async function closeFailSheet(page: import("@playwright/test").Page): Promise<void> {
  await page.locator("[data-fail-sheet]").evaluate((el: Element) => {
    const root = el as HTMLElement & { close?: () => void };
    root.close?.();
  });
}

test("could not deliver asks what stopped the delivery and says what happens to the load", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  // The hh-sheet paints its own title heading; the per-stop "Could not deliver"
  // door button stays on the stop card.
  await expect(page.getByRole("heading", { name: "Why could it not be delivered?" })).toBeVisible();
  // Five reasons sit as buttons; the first four carry their own label.
  await expect(page.getByRole("button", { name: "Nobody on site" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Gate locked" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Site not ready" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Customer refused" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Other, say what" })).toBeVisible();
  // The confirm button stays grey until a reason is picked.
  const confirm = page.getByRole("button", { name: "Take a photo and hand it back" });
  await expect(confirm).toBeDisabled();
  await page.getByRole("button", { name: "Nobody on site" }).click();
  await expect(confirm).toBeEnabled();
  // The other reason opens a note field.
  await page.getByRole("button", { name: "Other, say what" }).click();
  const note = page.locator("[data-field=other-note]");
  await expect(note).toBeVisible();
  await expect(confirm).toBeDisabled();
  await note.fill("Nobody on site and the gate is locked");
  await expect(confirm).toBeEnabled();
});

test("recording it sends the status and the reason, and closes the stop with the handback sentence", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Could not deliver" }).click();
  await page.getByRole("button", { name: "Nobody on site" }).click();
  await page.getByRole("button", { name: "Take a photo and hand it back" }).click();
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
  await closeFailSheet(page);
  await expect(page.locator("[data-stop-status]")).toHaveText("En route");
  expect((await stubLog(request)).filter((c) => c.body?.status === "FAILED")).toHaveLength(0);
});