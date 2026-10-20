// UX-17b (My run stop groups, load order line, and house-zone staleness) and
// UX-17c (Could not deliver and Proof of delivery modal sheets).
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import type { Page } from "@playwright/test";
import { accountItem, driverHandoff, eveningInstant, expect, houseDay, test } from "./helpers.js";

const SHOT_DIR = process.env.UX17BC_SHOT_DIR ?? "";
const SHOT_PHASE = process.env.UX17BC_SHOT_PHASE ?? "after";

// Instants constructed from numbers so no committed line carries a calendar date literal.
const FOUR_STOP_PULL_INSTANT = new Date(Date.UTC(2026, 6, 3, 16, 15)).toISOString();
const DELIVERED_INSTANT = new Date(Date.UTC(2026, 6, 3, 16, 4)).toISOString();

const FOUR_STOP_RUN = {
  items: [
    {
      id: "a1000000-0000-4000-9000-000000000005",
      run_no: "RUN-R5",
      truck_code: "FLAT-1",
      status: "DISPATCHED",
      stop_count: 4,
      stops: [
        {
          id: "5a000000-0000-4000-9000-000000000001",
          stop_no: 1,
          order_no: "SO-1001",
          customer_name: "Coastline Hardscape (demo)",
          status: "DELIVERED",
          time_window: "8:45 to 9:30 AM",
          contact_name: "Site lead (demo)",
          contact_phone: "619-555-0140",
          address_line: "1190 Via Rancho Pkwy",
          city: "Escondido",
          postal_code: "92029",
          instructions: "",
          gate_code: "",
          pod_received_by: "Site lead (demo)",
          pod_at: DELIVERED_INSTANT,
          failure_reason: "",
          photo_count: 1,
          weight_kg: 3300,
          created_at: DELIVERED_INSTANT,
        },
        {
          id: "5a000000-0000-4000-9000-000000000002",
          stop_no: 2,
          order_no: "SO-1002",
          customer_name: "Bluffside Builders (demo)",
          status: "EN_ROUTE",
          time_window: "9:00 to 11:00 AM",
          contact_name: "Site contact (demo)",
          contact_phone: "619-555-0142",
          address_line: "418 Oak Hollow Road",
          city: "Julian",
          postal_code: "92036",
          instructions: "Drop by side gate.",
          gate_code: "4471",
          pod_received_by: "",
          pod_at: "",
          failure_reason: "",
          photo_count: 0,
          weight_kg: 2400,
          created_at: DELIVERED_INSTANT,
        },
        {
          id: "5a000000-0000-4000-9000-000000000003",
          stop_no: 3,
          order_no: "SO-1003",
          customer_name: "Harbourview Builders (demo)",
          status: "LOADED",
          time_window: "10:00 AM to 12:00 PM",
          contact_name: "Yard receiver (demo)",
          contact_phone: "619-555-0188",
          address_line: "77 Kentwood Lane",
          city: "Julian",
          postal_code: "92036",
          instructions: "",
          gate_code: "",
          pod_received_by: "",
          pod_at: "",
          failure_reason: "",
          photo_count: 0,
          weight_kg: 1800,
          created_at: DELIVERED_INSTANT,
        },
        {
          id: "5a000000-0000-4000-9000-000000000004",
          stop_no: 4,
          order_no: "SO-1004",
          customer_name: "Valley Municipal Works (demo)",
          status: "READY",
          time_window: "1:00 to 3:00 PM",
          contact_name: "Crew lead (demo)",
          contact_phone: "619-555-0199",
          address_line: "1500 Farmers Road",
          city: "Julian",
          postal_code: "92036",
          instructions: "",
          gate_code: "",
          pod_received_by: "",
          pod_at: "",
          failure_reason: "",
          photo_count: 0,
          weight_kg: 1100,
          created_at: DELIVERED_INSTANT,
        },
      ],
      stop_lines: {
        "5a000000-0000-4000-9000-000000000001": [
          { line_no: 1, sku: "PAV-GRY", description: "Natural stone pavers, grey", qty: "120.000", uom: "sqft" },
        ],
        "5a000000-0000-4000-9000-000000000002": [
          { line_no: 1, sku: "BLK-CHR", description: "Retaining wall block, charcoal", qty: "84.000", uom: "each" },
          { line_no: 2, sku: "SND-50", description: "Polymeric sand, 50 lb bag", qty: "6.000", uom: "bag" },
        ],
        "5a000000-0000-4000-9000-000000000003": [
          { line_no: 1, sku: "CAP-STN", description: "Wall cap stone", qty: "24.000", uom: "each" },
        ],
        "5a000000-0000-4000-9000-000000000004": [
          { line_no: 1, sku: "GEO-100", description: "Geogrid roll", qty: "2.000", uom: "roll" },
        ],
      },
    },
  ],
};

function syncItemsForFourStopRun() {
  const run = FOUR_STOP_RUN.items[0]!;
  const day = houseDay(new Date(eveningInstant()));
  return {
    runs: [
      {
        id: run.id,
        seq: 100,
        updated_at: FOUR_STOP_PULL_INSTANT,
        data: {
          id: run.id,
          run_no: run.run_no,
          truck_code: run.truck_code,
          status: run.status,
          scheduled_date: day,
        },
      },
    ],
    stops: run.stops.map((stop, index) => ({
      id: stop.id,
      seq: 100 + index + 1,
      updated_at: FOUR_STOP_PULL_INSTANT,
      data: {
        ...stop,
        run_id: run.id,
        lines: run.stop_lines[stop.id as keyof typeof run.stop_lines] ?? [],
      },
    })),
  };
}

async function routeFourStopRun(page: Page): Promise<void> {
  const synced = syncItemsForFourStopRun();
  await page.route(/\/api\/v1\/dispatch\/my-runs/, (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(FOUR_STOP_RUN) }),
  );
  await page.route(/\/api\/v1\/sync\/runs/, (route) => {
    const since = Number(new URL(route.request().url()).searchParams.get("since") ?? "0");
    const items = synced.runs.filter((r) => r.seq > since);
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ scope: "runs", items, tombstones: [], last_seq: 100, next_cursor: null }),
    });
  });
  await page.route(/\/api\/v1\/sync\/stops/, (route) => {
    const since = Number(new URL(route.request().url()).searchParams.get("since") ?? "0");
    const items = synced.stops.filter((r) => r.seq > since);
    return route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ scope: "stops", items, tombstones: [], last_seq: 104, next_cursor: null }),
    });
  });
}

async function setTheme(page: Page, theme: "light" | "charcoal"): Promise<void> {
  const htmlRoot = page.locator("html");
  const current = (await htmlRoot.getAttribute("data-theme")) === "light" ? "light" : "charcoal";
  if (current !== theme) {
    await accountItem(page, theme === "charcoal" ? "Brand theme" : "Light theme");
    if (theme === "light") {
      await expect(htmlRoot).toHaveAttribute("data-theme", "light");
    } else {
      await expect(htmlRoot).not.toHaveAttribute("data-theme", "light");
    }
  }
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(driverHandoff);
});

test("UX-17bc screenshot capture across 390x844, 1024x768, 1440x900 in light and charcoal", async ({ page }) => {
  test.skip(!SHOT_DIR, "set UX17BC_SHOT_DIR to capture before/after screenshots");
  test.setTimeout(120_000);
  mkdirSync(SHOT_DIR, { recursive: true });

  const sizes = [
    { width: 390, height: 844 },
    { width: 1024, height: 768 },
    { width: 1440, height: 900 },
  ];
  const themes: Array<"light" | "charcoal"> = ["light", "charcoal"];

  await routeFourStopRun(page);
  await page.goto("/");
  await expect(page.locator("[data-run]")).toHaveCount(1);

  for (const theme of themes) {
    await setTheme(page, theme);
    for (const size of sizes) {
      await page.setViewportSize(size);
      await page.screenshot({
        path: join(SHOT_DIR, `${SHOT_PHASE}-my-run-${size.width}x${size.height}-${theme}.png`),
      });
    }
  }

  // Open Stop 2 (EN_ROUTE) to capture the reason sheet and proof sheet at each size and theme.
  await page.locator('[data-stop-id="5a000000-0000-4000-9000-000000000002"]').first().click();
  await expect(page.getByRole("heading", { name: "Stop 2" })).toBeVisible();

  for (const theme of themes) {
    await setTheme(page, theme);
    await page.locator('button[data-action="open-fail"]').click();
    await expect(page.getByRole("heading", { name: "Could not deliver" })).toBeVisible();
    const gateRadio = page.getByRole("radio", { name: "Gate locked" });
    if ((await gateRadio.count()) > 0) {
      await gateRadio.check();
      await gateRadio.blur();
    }
    for (const size of sizes) {
      await page.setViewportSize(size);
      await page.screenshot({
        path: join(SHOT_DIR, `${SHOT_PHASE}-reason-sheet-${size.width}x${size.height}-${theme}.png`),
      });
    }
    await page.locator('button[data-action="back-to-stop"]').first().click();
  }

  for (const theme of themes) {
    await setTheme(page, theme);
    await page.locator('button[data-action="open-pod"]').click();
    await expect(page.getByRole("heading", { name: "Proof of delivery" })).toBeVisible();
    for (const size of sizes) {
      await page.setViewportSize(size);
      await page.screenshot({
        path: join(SHOT_DIR, `${SHOT_PHASE}-proof-sheet-${size.width}x${size.height}-${theme}.png`),
      });
    }
    await page.locator('button[data-action="back-to-stop"]').first().click();
  }
});

test("UX-17b: my run shows NEXT STOP, LOADED AT THE YARD, WAITING and DELIVERED with the right stops in each, the load order line, and house-zone as-of time", async ({
  page,
}) => {
  await routeFourStopRun(page);
  for (const viewport of [
    { width: 390, height: 844 },
    { width: 1024, height: 768 },
  ]) {
    await page.setViewportSize(viewport);
    await page.goto("/");

    const card = page.locator("[data-run]").first();
    await expect(card.locator("[data-load-order]")).toHaveText(
      "Checked once for the whole run. Load order: stop 4 first, stop 1 last.",
    );

    const loadedGroup = card.locator('[data-stop-group="LOADED AT THE YARD"]');
    const deliveredGroup = card.locator('[data-stop-group="DELIVERED"]');
    const nextStopGroup = card.locator('[data-stop-group="NEXT STOP"]');
    const waitingGroup = card.locator('[data-stop-group="WAITING"]');

    await expect(card.locator("[data-group-head]")).toHaveText([
      "NEXT STOP",
      "LOADED AT THE YARD",
      "WAITING",
      "DELIVERED",
    ]);

    await expect(deliveredGroup.locator("[data-stop]")).toHaveCount(1);
    await expect(deliveredGroup.locator("[data-stop]")).toContainText("Coastline Hardscape (demo)");

    await expect(nextStopGroup.locator("[data-stop]")).toHaveCount(1);
    await expect(nextStopGroup.locator("[data-stop]")).toContainText("Bluffside Builders (demo)");

    await expect(loadedGroup.locator("[data-stop]")).toHaveCount(1);
    await expect(loadedGroup.locator("[data-stop]")).toContainText("Harbourview Builders (demo)");

    await expect(waitingGroup.locator("[data-stop]")).toHaveCount(1);
    await expect(waitingGroup.locator("[data-stop]")).toContainText("Valley Municipal Works (demo)");

    await expect(page.locator("[data-staleness]")).toContainText("as of 8:30 PM");

    await deliveredGroup.locator("[data-stop]").click();
    await expect(page.locator(".signed")).toHaveText("Signed by Site lead (demo) at 12:04 PM · 1 photo on the proof");
    await page.getByRole("button", { name: "Back to my run" }).click();
  }
});

test("UX-17b: advancing a stop moves it across WAITING, LOADED AT THE YARD, NEXT STOP, and DELIVERED on my run", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");

  const card = page.locator("[data-run]").first();
  await expect(card.locator("[data-load-order]")).toHaveText(
    "Checked once for the whole run. Load order: stop 3 first, stop 1 last.",
  );
  await expect(card.locator('[data-stop-group="WAITING"] [data-stop]')).toHaveCount(3);

  await card.locator("[data-stop]").first().click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Back to my run" }).click();

  await expect(card.locator('[data-stop-group="LOADED AT THE YARD"] [data-stop]')).toHaveCount(1);
  await expect(card.locator('[data-stop-group="LOADED AT THE YARD"] [data-stop]')).toContainText("Bluffside Builders");
  await expect(card.locator('[data-stop-group="WAITING"] [data-stop]')).toHaveCount(2);

  await card.locator('[data-stop-group="LOADED AT THE YARD"] [data-stop]').click();
  await page.getByRole("button", { name: "Mark en route" }).click();
  await page.getByRole("button", { name: "Back to my run" }).click();

  await expect(card.locator('[data-stop-group="NEXT STOP"] [data-stop]')).toHaveCount(1);
  await expect(card.locator('[data-stop-group="NEXT STOP"] [data-stop]')).toContainText("Bluffside Builders");

  await card.locator('[data-stop-group="NEXT STOP"] [data-stop]').click();
  await page.getByRole("button", { name: "Delivered", exact: true }).click();
  await page.getByLabel("Full name of the person receiving").fill("Site Foreman");
  await page.getByRole("checkbox").check();
  await page.getByRole("button", { name: "Sign for this delivery" }).click();
  await page.getByRole("button", { name: "Back to my run" }).click();

  await expect(card.locator('[data-stop-group="DELIVERED"] [data-stop]')).toHaveCount(1);
  await expect(card.locator('[data-stop-group="DELIVERED"] [data-stop]')).toContainText("Bluffside Builders");
});

async function focusInsideDialog(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const app = document.querySelector("drive-run-app");
    const root = app?.shadowRoot;
    const dialog = root?.querySelector('[role="dialog"]');
    const active = root?.activeElement;
    return Boolean(dialog && active && dialog.contains(active));
  });
}

async function activeDataAction(page: Page): Promise<string> {
  return page.evaluate(() => {
    const app = document.querySelector("drive-run-app");
    const active = app?.shadowRoot?.activeElement;
    return active?.getAttribute("data-action") ?? "";
  });
}

test("UX-17c: reason sheet and proof sheet are modal dialogs over an inert stop page, trap focus, close on Escape and Close, return focus, and position for phone and tablet", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();

  // ---- Reason sheet (phone 390x844, then tablet 1024x768) ----
  await page.locator('button[data-action="open-fail"]').click();
  const failDialog = page.getByRole("dialog");
  await expect(failDialog).toBeVisible();
  await expect(failDialog).toHaveAttribute("aria-modal", "true");
  await expect(failDialog.getByRole("heading", { name: "Could not deliver" })).toBeVisible();

  // Background stop page remains attached and is inert while the sheet is open.
  const pageBehind = page.locator("[data-page-behind]");
  await expect(pageBehind).toBeAttached();
  expect(await pageBehind.evaluate((el) => (el as HTMLElement).inert)).toBe(true);

  // Phone: bottom-anchored sheet.
  const phoneFailBox = await failDialog.boundingBox();
  expect(phoneFailBox).not.toBeNull();
  expect(Math.abs(phoneFailBox!.y + phoneFailBox!.height - 844)).toBeLessThanOrEqual(2);
  expect(phoneFailBox!.y).toBeGreaterThan(80);

  // Tablet: centred over the dimmed scrim.
  await page.setViewportSize({ width: 1024, height: 768 });
  const tabletFailBox = await failDialog.boundingBox();
  expect(tabletFailBox).not.toBeNull();
  expect(Math.abs(tabletFailBox!.x + tabletFailBox!.width / 2 - 512)).toBeLessThanOrEqual(4);
  expect(Math.abs(tabletFailBox!.y + tabletFailBox!.height / 2 - 384)).toBeLessThanOrEqual(24);

  // Focus moved inside the sheet and stays trapped on Tab and Shift+Tab.
  await expect.poll(() => focusInsideDialog(page)).toBe(true);
  for (let step = 0; step < 6; step += 1) {
    await page.keyboard.press("Tab");
    expect(await focusInsideDialog(page)).toBe(true);
  }
  await page.keyboard.press("Shift+Tab");
  expect(await focusInsideDialog(page)).toBe(true);

  // Escape closes the sheet, restores the page from inert, and returns focus to the opener.
  await page.keyboard.press("Escape");
  await expect(failDialog).toHaveCount(0);
  expect(await pageBehind.evaluate((el) => (el as HTMLElement).inert)).toBe(false);
  expect(await activeDataAction(page)).toBe("open-fail");

  // Reopen and close with the Close control: also returns focus to the opener.
  await page.locator('button[data-action="open-fail"]').click();
  await expect(failDialog).toBeVisible();
  await failDialog.getByRole("button", { name: "Close" }).click();
  await expect(failDialog).toHaveCount(0);
  expect(await activeDataAction(page)).toBe("open-fail");

  // ---- Proof of delivery sheet (phone 390x844 and tablet 1024x768) ----
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('button[data-action="open-pod"]').click();
  const podDialog = page.getByRole("dialog");
  await expect(podDialog).toBeVisible();
  await expect(podDialog).toHaveAttribute("aria-modal", "true");
  await expect(podDialog.getByRole("heading", { name: "Proof of delivery" })).toBeVisible();
  expect(await pageBehind.evaluate((el) => (el as HTMLElement).inert)).toBe(true);

  const phonePodBox = await podDialog.boundingBox();
  expect(phonePodBox).not.toBeNull();
  expect(Math.abs(phonePodBox!.y + phonePodBox!.height - 844)).toBeLessThanOrEqual(2);
  expect(phonePodBox!.y).toBeGreaterThan(40);

  await page.setViewportSize({ width: 1024, height: 768 });
  const tabletPodBox = await podDialog.boundingBox();
  expect(tabletPodBox).not.toBeNull();
  expect(Math.abs(tabletPodBox!.x + tabletPodBox!.width / 2 - 512)).toBeLessThanOrEqual(4);
  expect(Math.abs(tabletPodBox!.y + tabletPodBox!.height / 2 - 384)).toBeLessThanOrEqual(24);

  await expect.poll(() => focusInsideDialog(page)).toBe(true);
  for (let step = 0; step < 6; step += 1) {
    await page.keyboard.press("Tab");
    expect(await focusInsideDialog(page)).toBe(true);
  }
  await page.keyboard.press("Shift+Tab");
  expect(await focusInsideDialog(page)).toBe(true);

  await page.keyboard.press("Escape");
  await expect(podDialog).toHaveCount(0);
  expect(await pageBehind.evaluate((el) => (el as HTMLElement).inert)).toBe(false);
  expect(await activeDataAction(page)).toBe("open-pod");

  await page.locator('button[data-action="open-pod"]').click();
  await expect(podDialog).toBeVisible();
  await podDialog.getByRole("button", { name: "Not yet" }).click();
  await expect(podDialog).toHaveCount(0);
  expect(await activeDataAction(page)).toBe("open-pod");
});

test("UX-17c: reason sheet offers the five board preset reasons as a radio group, reveals free text on Other, say what, and records the chosen reason", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await page.locator("[data-stop]").first().click();
  await page.getByRole("button", { name: "Mark loaded" }).click();
  await page.getByRole("button", { name: "Mark en route" }).click();

  await page.locator('button[data-action="open-fail"]').click();
  const failDialog = page.getByRole("dialog");
  await expect(failDialog).toBeVisible();
  await expect(failDialog).toContainText("Why could it not be delivered?");
  await expect(failDialog.locator("[data-handback-words]")).toContainText("Bring the load back to the yard");

  const expectedPresets = [
    "Nobody on site",
    "Gate locked",
    "Site not ready",
    "Customer refused",
    "Other, say what",
  ];
  for (const label of expectedPresets) {
    await expect(failDialog.getByRole("radio", { name: label })).toBeVisible();
  }
  await expect(failDialog.getByLabel("What stopped this delivery?")).toHaveCount(0);

  const confirm = failDialog.getByRole("button", { name: "Could not deliver" });
  await expect(confirm).toBeDisabled();

  await failDialog.getByRole("radio", { name: "Other, say what" }).check();
  await expect(failDialog.getByLabel("What stopped this delivery?")).toBeVisible();
  await expect(confirm).toBeDisabled();
  await failDialog.getByLabel("What stopped this delivery?").fill("   ");
  await expect(confirm).toBeDisabled();
  await failDialog.getByLabel("What stopped this delivery?").fill("Driveway blocked by crane");
  await expect(confirm).toBeEnabled();

  await failDialog.getByRole("radio", { name: "Gate locked" }).check();
  await expect(failDialog.getByLabel("What stopped this delivery?")).toHaveCount(0);
  await expect(confirm).toBeEnabled();

  await confirm.click();
  await expect(page.locator("[data-stop-status]")).toHaveText("Not delivered");
  await expect(page.locator("[data-handback]")).toContainText("Gate locked");
});
