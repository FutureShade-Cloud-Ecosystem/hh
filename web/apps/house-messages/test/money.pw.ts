// The money and visibility legs on the stub: House Messages draws no payment figure of its own. A dollar
// figure on screen is one the core wrote into a message body; the structured amount a payment event
// carries is never drawn beside it, a day's total is never summed here, and a person who is not a member
// of a payments room never sees the room or what it holds.
import { test, expect, openAs, channel, CH, PERSONA, type Persona } from "./helpers.js";
import type { Page } from "@playwright/test";

const FIGURE = /\$\s?\d[\d,]*(?:\.\d{2})?/g;

async function bodiesFromCore(page: Page, who: Persona, channelId: string): Promise<string[]> {
  const session = await page.request.post("/api/v1/demo/session", { data: { flow: who } });
  const { token } = (await session.json()) as { token: string };
  const answer = await page.request.get(`/api/v1/messaging/channels/${channelId}/messages`, { headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" } });
  const body = (await answer.json()) as { messages: Array<{ body: string }> };
  return body.messages.map((m) => m.body);
}

const screenText = (page: Page): Promise<string> =>
  page.evaluate(() => document.querySelector("house-messages-app")?.shadowRoot?.querySelector("main")?.textContent ?? "");

test("a dollar figure on the screen is one the core wrote into a body, and none is drawn from the structured amount or summed", async ({ page, request }) => {
  await openAs(page, request, "payables-receivables");
  await channel(page, CH.pay).click();
  await expect(page.locator("[data-conversation] li[data-msg]")).toHaveCount(2);
  const sent = (await bodiesFromCore(page, "payables-receivables", CH.pay)).join("\n").match(FIGURE) ?? [];
  expect(sent.length, "the room holds figures to compare").toBeGreaterThan(0);
  const shown = (await screenText(page)).match(FIGURE) ?? [];
  expect([...new Set(shown)].sort()).toEqual([...new Set(sent)].sort());
  const text = await screenText(page);
  expect(text, "the day's two figures are not added here").not.toContain("$8,050.50");
  expect(text, "the event's structured amount in cents is not drawn beside its body").not.toMatch(/124000|681050/);
});

test("the chrome carries no figure: the rail, the strip and the sheets show words and counts only", async ({ page, request }) => {
  await openAs(page, request, "payables-receivables");
  const chrome = await page.evaluate(() => {
    const clone = document.querySelector("house-messages-app")?.shadowRoot?.querySelector("main")?.cloneNode(true) as HTMLElement | undefined;
    clone?.querySelectorAll("[data-body]").forEach((el) => el.remove());
    return clone?.textContent ?? "";
  });
  expect(chrome).not.toMatch(FIGURE);
});

test("a notice says why it reached the person from the rules the core named, and only those", async ({ page, request }) => {
  await openAs(page, request, "payables-receivables");
  await channel(page, CH.pay).click();
  await expect(page.locator("[data-why]")).toHaveCount(1);
  await expect(page.locator("[data-why]")).toHaveText("Reached you because: your own order");
});

test("a person outside the payments room never sees the room or its figures, and the core refuses the read", async ({ page, request }) => {
  await openAs(page, request, "sales");
  await expect(channel(page, CH.pay)).toHaveCount(0);
  for (const id of Object.values(CH)) {
    if (id === CH.pay) continue;
    await channel(page, id).click().catch(() => undefined);
  }
  expect(await screenText(page)).not.toContain("$1,240.00");
  const session = await page.request.post("/api/v1/demo/session", { data: { flow: "sales" } });
  const { token } = (await session.json()) as { token: string };
  const refused = await page.request.get(`/api/v1/messaging/channels/${CH.pay}/messages`, { headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" } });
  expect(refused.status()).toBe(403);
  void PERSONA;
});
