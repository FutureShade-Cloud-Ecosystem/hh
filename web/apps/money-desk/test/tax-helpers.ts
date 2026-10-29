// Shared helpers for the tax slice's Playwright legs: the controller's way in (the front door's handoff, written
// the way the front door writes it), the stub's reset, and the days the stub's world is cut from the host's clock.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";
import { controllerHandoff, resetStub } from "./gl-helpers.js";

export { expect, controllerHandoff, resetStub };

// Words that must never reach a person on a tax screen (the demo tenant is American; the core's are Ontario's;
// only the compound "GST/HST" on the GST/HST return tab is lawful).
export const OLD_WORDS = /\b(?!GST\/HST\b)(?<!\bGST\/)(HST|GST)\b|\b(PST|CAD|Ontario|TCRD|band.card|First Nations?)\b|status card/i;

/** The month before today, the way the desk and the stub cut it from the host's own clock. */
export function priorMonth(): { year: number; month: number; words: string; first: string; last: string } {
  const before = new Date(new Date().getFullYear(), new Date().getMonth() - 1, 1);
  const year = before.getFullYear();
  const month = before.getMonth() + 1;
  const pad = (n: number): string => String(n).padStart(2, "0");
  const last = new Date(year, month, 0).getDate();
  return { year, month, words: `${before.toLocaleString("en-US", { month: "long" })} ${year}`, first: `${year}-${pad(month)}-01`, last: `${year}-${pad(month)}-${pad(last)}` };
}

/** The month before that one: the month whose ledger is a little off. */
export function olderMonth(): { year: number; month: number; words: string } {
  const before = new Date(new Date().getFullYear(), new Date().getMonth() - 2, 1);
  return { year: before.getFullYear(), month: before.getMonth() + 1, words: `${before.toLocaleString("en-US", { month: "long" })} ${before.getFullYear()}` };
}

const QUARTER_LABELS: Record<number, string> = {
  1: "Q1 (Jan to Mar)",
  2: "Q2 (Apr to Jun)",
  3: "Q3 (Jul to Sep)",
  4: "Q4 (Oct to Dec)",
};

/** The quarter holding the month before today: the quarter the GST/HST return ties out for. */
export function priorQuarter(): { year: number; quarter: number; label: string } {
  const m = priorMonth();
  const quarter = Math.trunc((m.month - 1) / 3) + 1;
  return { year: m.year, quarter, label: QUARTER_LABELS[quarter] ?? `Q${quarter}` };
}

/** The quarter before that one: the quarter whose GST/HST return has a GL 2200 variance. */
export function olderQuarter(): { year: number; quarter: number; label: string } {
  const pq = priorQuarter();
  const year = pq.quarter === 1 ? pq.year - 1 : pq.year;
  const quarter = pq.quarter === 1 ? 4 : pq.quarter - 1;
  return { year, quarter, label: QUARTER_LABELS[quarter] ?? `Q${quarter}` };
}

/** A day offset from the host's own day, written year, month, day. */
export function dayFromToday(offset: number): string {
  const d = new Date();
  d.setDate(d.getDate() + offset);
  const pad = (n: number): string => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** A day written the way the screens write it. */
export function dayWords(day: string): string {
  const [y, m, d] = day.split("-").map(Number) as [number, number, number];
  const names = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  return `${names[m - 1]} ${d}, ${y}`;
}

export const ORDER_ONE = "7d000000-0000-4000-8000-000000000001";

/** Signs in as the controller through the front door's handoff and opens the Tax area. */
export async function openTax(page: Page, request: APIRequestContext): Promise<void> {
  await resetStub(request);
  await page.addInitScript(controllerHandoff);
  await page.goto("/");
  await page.getByRole("button", { name: "Tax", exact: true }).click();
  await expect(page.locator("[data-rate-id]")).toHaveCount(3);
}

export async function tab(page: Page, id: "rates" | "profiles" | "certificates" | "settings" | "checks" | "worksheet"): Promise<void> {
  await page.locator(`[data-tab-id='${id}']`).click();
}

/** The text of the whole screen the person reads, the shadow root included. */
export async function screenText(page: Page): Promise<string> {
  return page.locator("money-desk-app").evaluate((el) => (el.shadowRoot?.textContent ?? "").replace(/\s+/g, " "));
}

export const test = base;
