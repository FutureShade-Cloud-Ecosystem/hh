// The schedule tab on the stub: a confirmed order becomes a delivery on a day, the day's books and the
// week ahead, a date moved with its reason, the core's own words when it refuses, and both widths.
import { test, expect, openBoard, stubToday } from "./helpers.js";
import type { Page } from "@playwright/test";

const addDays = (iso: string, n: number): string => {
  const [y, m, d] = iso.split("-").map(Number);
  const date = new Date(y, m - 1, d + n);
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
};
const form = (page: Page) => page.locator("[data-schedule]");
const field = (page: Page, name: string) => form(page).locator(`[data-field='${name}']`);
const card = (page: Page, order: string) => page.locator("li.load", { hasText: order });

async function openSchedule(page: Page, request: Parameters<typeof openBoard>[1]): Promise<void> {
  await openBoard(page, request);
  await page.locator("[data-tab-id='schedule']").first().click();
  await expect(form(page)).toBeVisible();
}

test("the schedule tab is a real tab: the form, the week ahead and the day's books", async ({ page, request }) => {
  await openSchedule(page, request);
  await expect(page.locator("[data-tab-id='schedule']").first()).toHaveAttribute("aria-current", "true");
  await expect(page.getByRole("heading", { name: "Schedule a delivery" })).toBeVisible();
  await expect(page.locator("[data-week] .day")).toHaveCount(7);
  await expect(page.locator("[data-books] li[data-stop-id]")).toHaveCount(8);
  await expect(card(page, "ORD-0029")).toContainText("Summit Ridge Builders");
  await expect(card(page, "ORD-0012")).toContainText("RUN-PLM-1");
  await expect(card(page, "ORD-0029")).toContainText("Not on a truck");
});

test("a confirmed order is scheduled by its number, lands on its day as a request, and the form clears", async ({ page, request }) => {
  await openSchedule(page, request);
  const day = addDays(await stubToday(request), 1);
  await field(page, "order").fill("ORD-0040");
  await field(page, "day").fill(day);
  await field(page, "window").fill("PM");
  await field(page, "address").fill("12 Cedar Court");
  await field(page, "city").fill("Julian");
  await form(page).getByRole("button", { name: "Schedule delivery" }).click();
  await expect(page.locator("[data-notice]")).toContainText("ORD-0040 scheduled for");
  await expect(page.locator("[data-notice]")).toContainText("It is a request until it is on a truck.");
  await expect(page.locator("[data-day-words]")).not.toHaveText("");
  await expect(card(page, "ORD-0040")).toContainText("Cedar Court Homeowner");
  await expect(card(page, "ORD-0040").locator("[data-date-state]")).toHaveText("Requested");
  await expect(card(page, "ORD-0040")).toContainText("12 Cedar Court");
  await expect(field(page, "order")).toHaveValue("");
});

test("the form refuses a blank order and a coordinate on its own before the core is asked", async ({ page, request }) => {
  await openSchedule(page, request);
  await form(page).getByRole("button", { name: "Schedule delivery" }).click();
  await expect(page.locator("[data-schedule-error]")).toContainText("Enter an order number");
  await field(page, "order").fill("ORD-0040");
  await field(page, "lat").fill("32.7");
  await form(page).getByRole("button", { name: "Schedule delivery" }).click();
  await expect(page.locator("[data-schedule-error]")).toHaveText("Give both coordinates, or neither.");
  await expect(card(page, "ORD-0040")).toHaveCount(0);
});

test("the core's words show as they come: a draft order, a second delivery, an order that is not there", async ({ page, request }) => {
  await openSchedule(page, request);
  const submit = form(page).getByRole("button", { name: "Schedule delivery" });
  await field(page, "order").fill("ORD-0041");
  await submit.click();
  await expect(page.locator("[data-schedule-error]")).toHaveText("confirm the order before scheduling a delivery for it");
  await field(page, "order").fill("ORD-0029");
  await submit.click();
  await expect(page.locator("[data-schedule-error]")).toContainText("this order already has a delivery scheduled for");
  await field(page, "order").fill("ORD-9999");
  await submit.click();
  await expect(page.locator("[data-schedule-error]")).toContainText("No order matches that number at this yard");
  await field(page, "order").fill("banana");
  await submit.click();
  await expect(page.locator("[data-schedule-error]")).toContainText("is not an order number");
  await expect(field(page, "order")).toHaveValue("banana");
});

test("a date past the planning horizon reads in the core's words and keeps the form as typed", async ({ page, request }) => {
  await openSchedule(page, request);
  await field(page, "order").fill("ORD-0043");
  await field(page, "day").fill(addDays(await stubToday(request), 120));
  await form(page).getByRole("button", { name: "Schedule delivery" }).click();
  await expect(page.locator("[data-schedule-error]")).toContainText("planning horizon");
  await expect(field(page, "order")).toHaveValue("ORD-0043");
});

test("a delivery not on a truck moves to another day with its reason; one on a truck says where to go", async ({ page, request }) => {
  await openSchedule(page, request);
  await expect(card(page, "ORD-0012").getByRole("button", { name: "Move date" })).toHaveCount(0);
  await expect(card(page, "ORD-0012").locator("[data-move-why]")).toContainText("It is on run RUN-PLM-1");
  await card(page, "ORD-0029").getByRole("button", { name: "Move date" }).click();
  await card(page, "ORD-0029").locator("[data-field='reason']").fill("The pad is not poured yet");
  await card(page, "ORD-0029").getByRole("button", { name: "Move it" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The customer's date changed with it.");
  await expect(card(page, "ORD-0029")).toHaveCount(0);
});

test("a whole truck moves from the schedule, and the sheet keeps its refusal beside its button", async ({ page, request }) => {
  await openSchedule(page, request);
  const run = page.locator("[data-books-run='RUN-PLM-3']");
  await run.getByRole("button", { name: "Move truck" }).click();
  await run.locator("[data-field='reason']").fill("Customer asked for the next day");
  await run.getByRole("button", { name: "Move it" }).click();
  await expect(page.locator("[data-notice]")).toContainText("The customer's date changed with it.");
  await expect(page.locator("[data-books-run='RUN-PLM-3']")).toHaveCount(0);
  await expect(page.locator("[data-books-run='RUN-PLM-4']").getByRole("button", { name: "Move truck" })).toHaveCount(0);
});

test("the week strip opens that day's books", async ({ page, request }) => {
  await openSchedule(page, request);
  const day = addDays(await stubToday(request), 1);
  await page.locator(`[data-week] [data-day='${day}']`).click();
  await expect(page.locator("[data-books] li[data-stop-id]")).toHaveCount(1);
  await expect(card(page, "ORD-0030")).toContainText("Ridgeview Outdoor Living");
});

test("at 390 the schedule is one column with no sideways scroll, and every control is at least 44 tall", async ({ page, request }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await openSchedule(page, request);
  const wide = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
  expect(wide).toBe(false);
  const small = await page.evaluate(() => {
    const root = document.querySelector("dispatch-board-app")?.shadowRoot;
    const form = root?.querySelector("[data-schedule]");
    if (!form) return ["no form"];
    return [...form.querySelectorAll("input, button, select")]
      .filter((el) => el.getBoundingClientRect().height < 44)
      .map((el) => el.getAttribute("data-field") ?? el.textContent ?? "?");
  });
  expect(small).toEqual([]);
  const columns = await page.evaluate(() => {
    const root = document.querySelector("dispatch-board-app")?.shadowRoot;
    const lefts = [...(root?.querySelectorAll("[data-schedule] label") ?? [])].map((el) => Math.round(el.getBoundingClientRect().left));
    return new Set(lefts).size;
  });
  expect(columns).toBe(1);
});
