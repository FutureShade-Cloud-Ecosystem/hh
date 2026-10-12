// The dispatch board's browser legs, on the stub: the way in, the load board's moves (put a load on a
// truck, crew, dispatch, load many, reopen, move a date, cancel, commit, the gaps, auto assign), the
// day board's lifecycle, the refusals in the core's words, the money rule, and both widths.
import { test, expect, openBoard, resetStub, stubPost, stubToday } from "./helpers.js";
import type { Page } from "@playwright/test";

const load = (page: Page, order: string) => page.locator("li.load", { hasText: order });
const truck = (page: Page, no: string) => page.locator(`[data-run-no='${no}']`);

test("the way in offers the two roles whose screens live here, and a card signs in", async ({ page, request }) => {
  await resetStub(request);
  await page.goto("/");
  await expect(page.locator("[data-flow]")).toHaveCount(2);
  await page.getByRole("button", { name: "Logistics Manager", exact: true }).click();
  await expect(page.locator("[data-card='card.deliveries']")).toBeVisible();
  await expect(page.locator("[data-branch-name]")).toHaveText("Point Loma");
});

test("the load board reads the quick cards, the staging lane, the trucks with their gauges and the gaps", async ({ page, request }) => {
  await openBoard(page, request);
  await expect(page.locator("[data-card='card.deliveries'] .figure")).toHaveText("8");
  await expect(page.locator("[data-card='card.pickups']")).toContainText("2 to pick · 1 ready");
  await expect(page.locator("[data-staging-count]")).toHaveText("3");
  await expect(truck(page, "RUN-PLM-1").locator("[data-gauge='weight']")).toContainText("5,080 of 9,000 kg");
  await expect(truck(page, "RUN-PLM-1").locator("[data-gauge='weight']")).toContainText("Room");
  await expect(truck(page, "RUN-PLM-2").locator("[data-gauge='weight']")).toContainText("Near full");
  await expect(truck(page, "RUN-PLM-3").locator("[data-gauge='weight']")).toContainText("Over");
  await expect(truck(page, "RUN-PLM-4")).toContainText("1 of 2 loads not weighed");
  await expect(page.locator("[data-gaps]")).toContainText("Copperline cobble paver");
  await expect(page.locator("[data-gaps]")).toContainText("170 sq ft");
});

test("a load put on a truck is committed by the core and the gauge follows", async ({ page, request }) => {
  await openBoard(page, request);
  await load(page, "ORD-0029").locator("select").first().selectOption({ label: /RUN-PLM-1/ as unknown as string }).catch(async () => {
    await load(page, "ORD-0029").locator("select").first().selectOption({ index: 1 });
  });
  await expect(page.locator("[data-notice]")).toContainText("ORD-0029 moved to RUN-PLM-1");
  await expect(truck(page, "RUN-PLM-1").locator("[data-gauge='weight']")).toContainText("8,480 of 9,000 kg");
  await expect(truck(page, "RUN-PLM-1").locator("[data-gauge='weight']")).toContainText("Near full");
  await expect(truck(page, "RUN-PLM-1").locator("li.load", { hasText: "ORD-0029" }).locator("[data-date-state]")).toHaveText("Committed");
  await expect(page.locator("[data-staging-count]")).toHaveText("2");
});

test("the move list names why a truck is not offered", async ({ page, request }) => {
  await openBoard(page, request);
  // The tomorrow load cannot ride a truck that runs today: the option is disabled with the two dates.
  const option = load(page, "ORD-0030").locator("select option", { hasText: "RUN-PLM-1" });
  await expect(option).toBeDisabled();
  await expect(option).toContainText("runs on");
});

test("a truck is dispatched only with a crew, and then loads what is on it", async ({ page, request }) => {
  await openBoard(page, request);
  await expect(truck(page, "RUN-PLM-2").getByRole("button", { name: "Dispatch" })).toBeDisabled();
  await expect(truck(page, "RUN-PLM-2").locator("[data-dispatch-why]")).toContainText("needs a truck and a driver");
  await truck(page, "RUN-PLM-2").locator("select[data-field='crew-driver']").selectOption({ index: 1 });
  await expect(page.locator("[data-notice]")).toHaveText("Crew set.");
  await truck(page, "RUN-PLM-2").getByRole("button", { name: "Dispatch" }).click();
  await expect(page.locator("[data-notice]")).toContainText("RUN-PLM-2 dispatched");
  await expect(truck(page, "RUN-PLM-2")).toHaveAttribute("data-run-status", "DISPATCHED");
  await expect(truck(page, "RUN-PLM-2").locator("li.load")).toHaveAttribute("data-status", "LOADED");
});

test("many loads are marked loaded in one move, and the one the core refuses is named", async ({ page, request }) => {
  await openBoard(page, request);
  await load(page, "ORD-0012").getByRole("checkbox").click();
  await load(page, "ORD-0014").getByRole("checkbox").click();
  await expect(page.locator("[data-bulk]")).toContainText("2 selected");
  await page.getByRole("button", { name: "Mark 2 loads loaded" }).click();
  await expect(page.locator("[data-notice]")).toHaveText("1 load marked loaded.");
  await expect(page.locator("[data-refusals]")).toContainText("ORD-0014: this order is held for payment");
  await expect(load(page, "ORD-0012")).toHaveAttribute("data-status", "LOADED");
  await expect(load(page, "ORD-0014").getByRole("checkbox")).toHaveAttribute("aria-checked", "true");
});

test("a payment hold shows as a hold in words and never as an amount unless the core sent one", async ({ page, request }) => {
  await openBoard(page, request);
  await expect(load(page, "ORD-0014").locator("[data-hold]")).toHaveText("Held for payment");
  await expect(load(page, "ORD-0014").locator("[data-hold-words]")).toHaveText("payment needed before release, call the office");
  await expect(page.locator("main")).not.toContainText("$");
  await stubPost(request, "hold-amount", { on: true });
  await page.getByRole("button", { name: "Refresh" }).click();
  await expect(load(page, "ORD-0014").locator("[data-hold-words]")).toContainText("$1,250.50 due");
});

test("the board draws no money of its own when the core sends none", async ({ page, request }) => {
  await openBoard(page, request);
  await expect(page.locator("main")).not.toContainText(/\$\d/);
  await expect(page.locator("main")).not.toContainText(/freight|value/i);
});

test("reopening a committed truck asks a reason, then makes every load an ask again", async ({ page, request }) => {
  await openBoard(page, request);
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Reopen truck" }).click();
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Reopen it" }).click();
  await expect(page.locator("[data-sheet-error]")).toContainText("Say why");
  await truck(page, "RUN-PLM-1").locator("[data-field='reason']").fill("A better fit turned up");
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Reopen it" }).click();
  await expect(page.locator("[data-notice]")).toContainText("RUN-PLM-1 reopened");
  await expect(truck(page, "RUN-PLM-1").locator("[data-reopened]")).toBeVisible();
  await expect(truck(page, "RUN-PLM-1").locator("[data-committed]")).toHaveCount(0);
  await truck(page, "RUN-PLM-1").getByRole("button", { name: "Clear the reopen marker" }).click();
  await expect(truck(page, "RUN-PLM-1").locator("[data-reopened]")).toHaveCount(0);
});

test("a caller who may not reopen sees why, not a button that answers 403", async ({ page, request }) => {
  await resetStub(request);
  await stubPost(request, "caller", { can_reopen: false });
  await page.addInitScript((await import("./helpers.js")).dispatchHandoff);
  await page.goto("/");
  await expect(page.locator("[data-card]").first()).toBeVisible();
  await expect(truck(page, "RUN-PLM-1").getByRole("button", { name: "Reopen truck" })).toHaveCount(0);
  await expect(truck(page, "RUN-PLM-1").locator("[data-blocked]")).toContainText("A dispatch manager can reopen it");
  await expect(load(page, "ORD-0012").getByRole("button", { name: "Take off truck" })).toHaveCount(0);
});

test("a delivery moves to another day in the customer's view, and the sheet checks before the round trip", async ({ page, request }) => {
  await openBoard(page, request);
  await load(page, "ORD-0029").getByRole("button", { name: "Move date" }).click();
  await load(page, "ORD-0029").getByRole("button", { name: "Move it" }).click();
  await expect(page.locator("[data-sheet-error]")).toContainText("Say why");
  await load(page, "ORD-0029").locator("[data-field='reason']").fill("Gate is closed that day");
  await load(page, "ORD-0029").getByRole("button", { name: "Move it" }).click();
  await expect(page.locator("[data-notice]")).toContainText("Moved to");
  await expect(load(page, "ORD-0029").locator("[data-day-pill]")).toBeVisible();
});

test("a whole truck cancels and its loads come back to the staging lane", async ({ page, request }) => {
  await openBoard(page, request);
  await truck(page, "RUN-PLM-2").getByRole("button", { name: "Cancel truck" }).click();
  await expect(page.locator("[data-notice]")).toContainText("RUN-PLM-2 cancelled");
  await expect(truck(page, "RUN-PLM-2")).toHaveCount(0);
  await expect(page.locator("[data-staging-count]")).toHaveText("4");
});

test("a requested load commits its date, and a load comes off its truck", async ({ page, request }) => {
  await openBoard(page, request);
  await load(page, "ORD-0030").getByRole("button", { name: "Commit date" }).click();
  await expect(page.locator("[data-notice]")).toContainText("ORD-0030 committed");
  await expect(load(page, "ORD-0030").locator("[data-date-state]")).toHaveText("Committed");
  await load(page, "ORD-0012").getByRole("button", { name: "Take off truck" }).click();
  await expect(page.locator("[data-notice]")).toContainText("ORD-0012 taken off its truck");
  await expect(page.locator("[data-staging-count]")).toHaveText("4");
});

test("a new truck opens for the day with an optional crew", async ({ page, request }) => {
  await openBoard(page, request);
  await page.getByRole("button", { name: "New truck" }).click();
  await page.locator("[data-sheet='new-run'] select[data-field='truck']").selectOption({ index: 1 });
  await page.getByRole("button", { name: "Open truck", exact: true }).click();
  await expect(page.locator("[data-notice]")).toContainText("Truck opened");
  await expect(page.locator("[data-run-no='RUN-PLM-5']")).toContainText("Truck 01");
});

test("the quick cards filter in place and say so, and the status axis lanes the same loads", async ({ page, request }) => {
  await openBoard(page, request);
  await page.locator("[data-card='card.committed']").click();
  await expect(page.locator("[data-filter]")).toContainText("committed only");
  await expect(page.locator("[data-run-no='RUN-PLM-2']")).toHaveCount(0);
  await page.getByRole("button", { name: "Show everything" }).click();
  await page.getByRole("button", { name: "By status" }).click();
  await expect(page.locator("[data-lane='READY']")).toBeVisible();
  await expect(page.locator("[data-lane='EN_ROUTE'] li.load")).toHaveCount(1);
});

test("auto assign says what would answer it, as information and not as a failure", async ({ page, request }) => {
  await openBoard(page, request);
  await page.getByRole("button", { name: "Auto assign" }).click();
  await expect(page.locator("[data-info]")).toContainText("part of the Pro module");
  await expect(page.locator("[data-error]")).toHaveCount(0);
});

test("a refusal reads in the core's words and the board reads again", async ({ page, request }) => {
  await openBoard(page, request);
  await stubPost(request, "refuse", { status: 409, error: "dispatch: this load is committed", path: "/stops/" });
  await load(page, "ORD-0030").getByRole("button", { name: "Commit date" }).click();
  await expect(page.locator("[data-error]")).toHaveText("this load is committed");
});

test("the day moves with the arrows, the date field and the week strip", async ({ page, request }) => {
  await openBoard(page, request);
  const today = await stubToday(request);
  await page.getByRole("button", { name: "Next day" }).click();
  await expect(page.locator("input[data-field='date']")).not.toHaveValue(today);
  await expect(page.locator("[data-gauge]")).toHaveCount(0);
  await expect(page.locator("main")).toContainText("No trucks on the board");
  await page.getByRole("button", { name: "Today" }).click();
  await expect(page.locator("input[data-field='date']")).toHaveValue(today);
  await page.locator("[data-week] [data-action='goto-day']").nth(1).click();
  await expect(page.locator("[data-staging-count]")).toBeVisible();
});

test("the day board shows every stop by stage and offers only the moves the lifecycle allows", async ({ page, request }) => {
  await openBoard(page, request);
  await page.locator("[data-tab-id='day']").first().click();
  const run4 = truck(page, "RUN-PLM-4");
  await expect(run4.locator("[data-col='EN_ROUTE'] li.load")).toHaveCount(1);
  await expect(run4.locator("[data-col='LOADED'] li.load")).toHaveCount(1);
  await expect(run4.locator("[data-pod-note]")).toContainText("HH Drive");
  await expect(run4.getByRole("button", { name: "Complete run" })).toBeVisible();
  // A ready stop on a planned run is loaded from here, a loaded one goes en route only once dispatched.
  const stop = load(page, "ORD-0012");
  await expect(stop.getByRole("button", { name: "Mark loaded" })).toBeVisible();
  await stop.getByRole("button", { name: "Mark loaded" }).click();
  await expect(page.locator("[data-notice]")).toContainText("ORD-0012 is now loaded");
  await load(page, "ORD-0012").getByRole("button", { name: "Mark en route" }).click();
  await expect(page.locator("[data-error]")).toHaveText("the run has not been dispatched");
});

test("a stop that could not be delivered needs a reason, and the run closes once every stop has an answer", async ({ page, request }) => {
  await openBoard(page, request);
  await page.locator("[data-tab-id='day']").first().click();
  const run4 = truck(page, "RUN-PLM-4");
  await run4.getByRole("button", { name: "Complete run" }).click();
  await expect(page.locator("[data-error]")).toHaveText("the run still has stops that are not finished");
  await load(page, "ORD-0009").getByRole("button", { name: "Could not deliver" }).click();
  await load(page, "ORD-0009").getByRole("button", { name: "Record it" }).click();
  await expect(page.locator("[data-sheet-error]")).toContainText("Say what stopped");
  await load(page, "ORD-0009").locator("[data-field='reason']").fill("Gate locked, nobody on site");
  await load(page, "ORD-0009").getByRole("button", { name: "Record it" }).click();
  await expect(page.locator("[data-notice]")).toContainText("ORD-0009 is now failed");
  await expect(run4.locator("[data-col='DID_NOT_GO'] li.load")).toHaveCount(1);
});

test("the day board puts an unassigned stop on a planned run", async ({ page, request }) => {
  await openBoard(page, request);
  await load(page, "ORD-0029").locator("select").first().selectOption({ index: 1 });
  await page.locator("[data-tab-id='day']").first().click();
  await expect(truck(page, "RUN-PLM-1").locator("li.load", { hasText: "ORD-0029" })).toBeVisible();
});

test("a failed read says so in words and offers to try again", async ({ page, request }) => {
  await resetStub(request);
  await page.addInitScript((await import("./helpers.js")).dispatchHandoff);
  await stubPost(request, "refuse", { status: 500, error: "internal error", path: "loadboard", reads: true });
  await page.goto("/");
  await expect(page.locator("[data-error]")).toContainText("The server could not finish");
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator("[data-card]").first()).toBeVisible();
});

for (const size of [{ width: 390, height: 844 }, { width: 1440, height: 900 }]) {
  test(`at ${size.width} px the page never scrolls sideways and every control is at least 44 px`, async ({ page, request }) => {
    await page.setViewportSize(size);
    await openBoard(page, request);
    for (const tab of ["load", "day"]) {
      await page.locator(`[data-tab-id='${tab}']`).first().click();
      await expect(page.locator(tab === "load" ? "[data-card]" : "[data-col]").first()).toBeVisible();
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
      const small = await page.evaluate(() => {
        const root = document.querySelector("dispatch-board-app")?.shadowRoot;
        const out: string[] = [];
        root?.querySelectorAll("button:not(.box):not(.link), select, input").forEach((el) => {
          const r = el.getBoundingClientRect();
          if (r.width > 0 && r.height > 0 && r.height < 43.5 && !el.classList.contains("theme-btn")) out.push(`${el.tagName} ${el.textContent?.trim().slice(0, 20)} ${Math.round(r.height)}`);
        });
        return out;
      });
      expect(small).toEqual([]);
    }
  });
}
