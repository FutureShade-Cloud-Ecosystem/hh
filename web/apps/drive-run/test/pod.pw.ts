// HH Drive, sub item 3: proof of delivery. A typed name, the recipient's own
// confirmation and, if the driver wants it, a photo. Sent as the core's shape:
// {received_by, affirmed}, and the photo as the raw picture on its own call.
import { test, expect, TINY_PNG, driverHandoff, resetStub, stubLog, takeFirstStopEnRoute, useScenario, type LoggedCall } from "./helpers.js";

test.use({ viewport: { width: 390, height: 844 } });

test.beforeEach(async ({ request, page }) => {
  await resetStub(request);
  await page.addInitScript(driverHandoff);
});

test("proof of delivery opens with the name to type and the confirmation unticked, and sign is grey", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Proof of delivery" })).toBeVisible();
  await expect(page.locator("[data-pod]")).toContainText("Stop 1 · Northline Hardscapes Ltd (demo) · SO-1001");
  await expect(page.getByLabel("Full name of the person receiving")).toHaveValue("Site contact (demo)");
  await expect(page.getByRole("checkbox")).not.toBeChecked();
  await expect(page.locator(".confirm")).toContainText("not a captured signature image");
  const sign = page.getByRole("button", { name: "Sign for this delivery" });
  await expect(sign).toBeDisabled();
  await expect(page.locator("[data-sign-hint]")).toHaveText("Tick the confirmation to sign.");
  await page.getByRole("checkbox").check();
  await expect(sign).toBeEnabled();
  await page.getByLabel("Full name of the person receiving").fill("   ");
  await expect(sign).toBeDisabled();
  await expect(page.locator("[data-sign-hint]")).toHaveText("Type the name of the person receiving.");
});

test("signing sends the core's POD shape and the stop reads delivered", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByLabel("Full name of the person receiving").fill("  Receiving clerk  ");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await expect(page.getByRole("heading", { name: "Stop 1" })).toBeVisible();
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
  await expect(page.locator(".signed")).toContainText("Signed by Receiving clerk");
  await expect(page.getByRole("status")).toContainText("Delivery to Northline Hardscapes Ltd (demo) is signed for.");
  // The proof rides the outbox, and the replay creates it on the pod scope:
  // the core's own shape, with the recipient trimmed and the affirmation set.
  interface PushCall extends LoggedCall {
    body: { ops: Array<Record<string, unknown>> } | null;
  }
  let podOps: Array<Record<string, unknown>> = [];
  await expect
    .poll(async () => {
      const pushes = (await stubLog(request)).filter(
        (c): c is PushCall => c.path === "/api/v1/sync/pod" && c.method === "POST",
      );
      podOps = pushes.flatMap((p) => p.body?.ops ?? []);
      return podOps.length;
    })
    .toBe(1);
  const payload = (podOps[0]?.payload ?? {}) as Record<string, unknown>;
  expect(payload.received_by).toBe("Receiving clerk");
  expect(payload.affirmed).toBe(true);
  expect(payload.stop_id).toBe("5a000000-0000-4000-9000-000000000001");
  await page.getByRole("button", { name: "Back to my run" }).click();
  await expect(page.locator("[data-progress]")).toHaveText("2 stops left · 1 delivered");
});

test("a delivered stop offers no further move", async ({ page }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
  await expect(page.getByRole("button", { name: "Delivered", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Could not deliver" })).toHaveCount(0);
});

test("Not yet goes back to the stop and sends nothing", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByRole("button", { name: "Not yet" }).click();
  await expect(page.getByRole("heading", { name: "Stop 1" })).toBeVisible();
  // Nothing was SENT: the pulls read, but no proof create rode the outbox.
  expect((await stubLog(request)).filter((c) => c.method === "POST" && c.path.endsWith("/pod"))).toHaveLength(0);
});

test("a photo goes up as the raw picture and is counted on the proof", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.locator("input[type=file]").setInputFiles({ name: "drop.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.locator("[data-photo-sent]")).toHaveCount(1);
  await expect(page.locator("[data-photos]")).toContainText("Photo 1 sent");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await expect(page.locator(".signed")).toContainText("1 photo on the proof");
  const photos = (await stubLog(request)).filter((c) => c.path.endsWith("/photos"));
  expect(photos).toHaveLength(1);
  expect(photos[0].contentType).toBe("image/png");
  expect(photos[0].bytes).toBe(TINY_PNG.length);
});

test("a photo that fails to send never blocks the signature", async ({ page, request }) => {
  await useScenario(request, "photo-fails");
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.locator("input[type=file]").setInputFiles({ name: "drop.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.getByRole("alert")).toContainText("The photo did not send");
  await page.getByRole("checkbox").check();
  const sign = page.getByRole("button", { name: "Sign for this delivery" });
  await expect(sign).toBeEnabled();
  await sign.click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
});

test("a file that is not a picture the core takes is refused on the phone, with nothing sent", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.locator("input[type=file]").setInputFiles({ name: "notes.txt", mimeType: "text/plain", buffer: Buffer.from("hello") });
  await expect(page.getByRole("alert")).toContainText("A proof photo must be a JPEG, PNG or WebP picture.");
  expect((await stubLog(request)).filter((c) => c.path.endsWith("/photos"))).toHaveLength(0);
});

test("a camera sized photo is shrunk under the core's 4 MB limit before it is sent", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  // A noisy 1800 px picture saved as PNG is several megabytes: the shape of a camera file.
  await page.evaluate(async () => {
    const canvas = document.createElement("canvas");
    canvas.width = 1800;
    canvas.height = 1800;
    const ctx = canvas.getContext("2d") as CanvasRenderingContext2D;
    const image = ctx.createImageData(1800, 1800);
    for (let i = 0; i < image.data.length; i += 1) image.data[i] = i % 4 === 3 ? 255 : Math.floor(Math.random() * 256);
    ctx.putImageData(image, 0, 0);
    const blob: Blob = await new Promise((resolve) => canvas.toBlob((b) => resolve(b as Blob), "image/png"));
    const file = new File([blob], "camera.png", { type: "image/png" });
    const transfer = new DataTransfer();
    transfer.items.add(file);
    const input = document.querySelector("drive-run-app")?.shadowRoot?.querySelector("input[type=file]") as HTMLInputElement;
    input.files = transfer.files;
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await expect(page.locator("[data-photo-sent]")).toHaveCount(1);
  const photos = (await stubLog(request)).filter((c) => c.path.endsWith("/photos"));
  expect(photos).toHaveLength(1);
  expect(photos[0].contentType).toBe("image/jpeg");
  expect(photos[0].bytes ?? 0).toBeGreaterThan(0);
  expect(photos[0].bytes ?? 0).toBeLessThanOrEqual(4 * 1024 * 1024);
});

test("a photo added after delivery is folded into the proof", async ({ page, request }) => {
  await takeFirstStopEnRoute(page);
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Delivered");
  await page.locator("input[type=file]").setInputFiles({ name: "late.png", mimeType: "image/png", buffer: TINY_PNG });
  await expect(page.locator(".signed")).toContainText("1 photo on the proof");
  const calls = (await stubLog(request)).map((c) => c.path.split("/").slice(-1)[0]);
  expect(calls).toContain("photos");
  expect(calls).toContain("attach-photos");
});
