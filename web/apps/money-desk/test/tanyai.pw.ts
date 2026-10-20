// The TanyAI panel mounted on the Money Desk (PANEL-API r1 sections 1, 4 and 5), walked in a real browser against
// the stub. The stub answers the four staff assist routes and a few scripted questions (test/stub/assist-world.mjs);
// the panel itself is the shell-chrome component. Every assertion waits.
import type { Page } from "@playwright/test";
import { test, expect, openDesk } from "./helpers.js";

const panel = (page: Page) => page.getByRole("dialog", { name: "TanyAI" });
const askButton = (page: Page) => page.getByRole("button", { name: "Ask TanyAI" });

async function open(page: Page): Promise<void> {
  await askButton(page).click();
  await expect(panel(page)).toBeVisible();
}

async function ask(page: Page, question: string): Promise<void> {
  const box = panel(page).getByRole("textbox", { name: "Ask TanyAI" });
  await box.fill(question);
  await box.press("Enter");
}

test.beforeEach(async ({ page, request }) => {
  await openDesk(page, request);
});

test("the header button opens the panel with focus in the box, and Escape closes it and returns focus to the button", async ({ page }) => {
  await askButton(page).focus();
  await askButton(page).press("Enter");
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).getByRole("textbox", { name: "Ask TanyAI" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(panel(page)).toBeHidden();
  await expect(askButton(page)).toBeFocused();
});

test("F9 opens the panel and F9 closes it", async ({ page }) => {
  await page.keyboard.press("F9");
  await expect(panel(page)).toBeVisible();
  await expect(panel(page).getByRole("textbox", { name: "Ask TanyAI" })).toBeFocused();
  await page.keyboard.press("F9");
  await expect(panel(page)).toBeHidden();
});

test("first run says what TanyAI can do for this role, from the command list", async ({ page }) => {
  await open(page);
  await expect(panel(page).getByRole("heading", { name: "Ask about your day" })).toBeVisible();
  await expect(panel(page).getByRole("button", { name: /Who owes us/ })).toBeVisible();
  await expect(panel(page).getByRole("button", { name: /Find a payment/ })).toBeVisible();
  await expect(panel(page).getByText("answers only what your role may see")).toBeVisible();
});

test("a question shows working words, then the answer with its table, and Open reaches the customer's account", async ({ page }) => {
  await open(page);
  await ask(page, "slow, who owes us");
  await expect(panel(page).getByRole("status")).toContainText("TanyAI is checking what your role can see.");
  await expect(panel(page).getByText("Three accounts have an open balance.")).toBeVisible({ timeout: 10_000 });
  const table = panel(page).getByRole("table", { name: "Who owes us" });
  await expect(table).toBeVisible();
  await expect(table.getByRole("row")).toHaveCount(4);
  await expect(panel(page).getByText("Showing the 3 largest balances.")).toBeVisible();
  await panel(page).getByRole("button", { name: "Open Granite Edge Landscaping (demo)" }).click();
  await expect(panel(page)).toBeHidden();
  await expect(page.getByRole("heading", { name: "Granite Edge Landscaping (demo)" })).toBeVisible();
});

test("a clarify is picked by a chip, the choice goes back as the next turn, and the history is only what the person typed or picked", async ({ page, request }) => {
  await open(page);
  await ask(page, "stock of blu");
  await expect(panel(page).getByText('Two products match "Blu", which one?')).toBeVisible();
  await panel(page).getByRole("button", { name: /Techo-Bloc Blu 60/ }).click();
  await expect(panel(page).getByText("There are 410 on the shelf at Point Loma.")).toBeVisible();
  await expect(panel(page).getByText("Still about: product Techo-Bloc Blu 60")).toBeVisible();
  const { calls } = await (await request.get("/_stub/assist-calls")).json();
  const second = calls[calls.length - 1];
  expect(second.question).toBe("DBS-TB-BLU60");
  expect(second.history.length).toBeLessThanOrEqual(6);
  expect(second.history.map((m: { role: string }) => m.role)).toEqual(["user", "assistant"]);
  expect(second.history[1].clarify.arg).toBe("product");
  for (const m of second.history) expect(Object.keys(m).filter((k) => !["role", "text", "ran", "clarify"].includes(k))).toEqual([]);
});

test("a suggestion chip runs a command, which costs no AI", async ({ page }) => {
  await open(page);
  await ask(page, "nonsense");
  await panel(page).getByRole("button", { name: "Who owes us" }).last().click();
  await expect(panel(page).getByRole("table", { name: "Who owes us" })).toBeVisible();
});

test("a lookup that needs a value asks for it, then shows the result", async ({ page }) => {
  await open(page);
  await panel(page).getByRole("button", { name: /Find a payment/ }).click();
  await panel(page).getByLabel(/Payment number/).fill("PAY-77");
  await panel(page).getByRole("button", { name: "Look it up" }).click();
  await expect(panel(page).getByRole("table", { name: "Payment PAY-77" })).toBeVisible();
});

test("over budget: the core's sentence verbatim, the box off, and the lookups still work", async ({ page, request }) => {
  await request.post("/_stub/assist", { data: { over_budget: true } });
  await open(page);
  await expect(panel(page).getByText("You've used this month's AI allowance, ask your admin if you need more.")).toBeVisible();
  await expect(panel(page).getByRole("textbox", { name: "Ask TanyAI" })).toBeDisabled();
  await panel(page).getByRole("button", { name: /Who owes us/ }).click();
  await expect(panel(page).getByRole("table", { name: "Who owes us" })).toBeVisible();
});

test("chat switched off: the core's sentence and the lookups", async ({ page, request }) => {
  await request.post("/_stub/assist", { data: { chat_enabled: false } });
  await open(page);
  await expect(panel(page).getByText("The assistant's chat is switched off right now.")).toBeVisible();
  await expect(panel(page).getByRole("button", { name: /Open orders/ })).toBeVisible();
});

test("an over budget answer in the middle of a thread shows the sentence and the lookup chips", async ({ page }) => {
  await open(page);
  await ask(page, "over budget please");
  await expect(panel(page).getByText("You've used this month's AI allowance")).toBeVisible();
  await expect(panel(page).getByRole("button", { name: /Who owes us/ })).toBeVisible();
});

test("an error says so in fixed words, never the failure text, and Try again answers", async ({ page }) => {
  await open(page);
  await ask(page, "break it");
  const alert = panel(page).getByRole("alert");
  await expect(alert).toContainText("TanyAI cannot answer right now. Try again in a moment.");
  await expect(panel(page)).not.toContainText("assist_ledger");
  await expect(panel(page)).not.toContainText("pq:");
  await alert.getByRole("button", { name: "Try again" }).click();
  await expect(panel(page).getByText("I looked, and there is nothing unusual today.")).toBeVisible();
  await expect(panel(page).getByRole("alert")).toHaveCount(0);
});

test("markup in a reply is shown as text and never runs", async ({ page }) => {
  await open(page);
  await ask(page, "xss");
  await expect(panel(page).getByText("<img src=x onerror=", { exact: false })).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __tanyaiXss?: boolean }).__tanyaiXss)).toBeUndefined();
  await expect(panel(page).locator("img")).toHaveCount(0);
});

test("section 2 fields: citations, scope and the notices, in the glossary's words", async ({ page }) => {
  await open(page);
  await ask(page, "figures");
  await expect(panel(page).getByText("Point Loma, this month, as of 8:15 AM")).toBeVisible();
  await expect(panel(page).getByRole("button", { name: "Open Granite Edge Landscaping (demo)" })).toHaveCount(3);
  await ask(page, "refuse");
  await expect(panel(page).getByText("Costs are not shown for your role. A manager can see them.")).toBeVisible();
  await ask(page, "model");
  await expect(panel(page).getByText("TanyAI's chat is not available right now. The quick lookups below still work.")).toBeVisible();
  await ask(page, "privately");
  await expect(panel(page).getByText("TanyAI answered privately for the members at the time.")).toBeVisible();
  await ask(page, "toobroad");
  await expect(panel(page).getByText("That question covers too much at once.")).toBeVisible();
});

test("usage shows only where assist/me says spend is visible", async ({ page, request }) => {
  await open(page);
  await ask(page, "who owes us");
  await expect(panel(page).getByText("AI runs used: 540")).toBeVisible();
  await page.keyboard.press("Escape");
  await request.post("/_stub/assist", { data: { spend_visible: false } });
  await page.reload();
  await openDesk(page, request);
  await request.post("/_stub/assist", { data: { spend_visible: false } });
  await open(page);
  await ask(page, "who owes us");
  await expect(panel(page).getByText("Three accounts have an open balance.")).toBeVisible();
  await expect(panel(page).getByText("AI runs used")).toHaveCount(0);
});

test("offline: the box is off and the words say the question is not saved", async ({ page, context }) => {
  await open(page);
  await context.setOffline(true);
  await expect(panel(page).getByText("TanyAI needs a connection. Your question is not saved.")).toBeVisible();
  await expect(panel(page).getByRole("textbox", { name: "Ask TanyAI" })).toBeDisabled();
  await context.setOffline(false);
  await expect(panel(page).getByRole("textbox", { name: "Ask TanyAI" })).toBeEnabled();
});

test("a very long answer scrolls inside the panel and the wide table scrolls in its card", async ({ page }) => {
  await open(page);
  await ask(page, "long");
  await expect(panel(page).getByRole("table", { name: "Open orders" })).toBeVisible();
  const thread = panel(page).getByRole("log");
  const metrics = await thread.evaluate((el) => ({ scroll: el.scrollHeight, client: el.clientHeight }));
  expect(metrics.scroll).toBeGreaterThan(metrics.client);
  const card = panel(page).getByRole("region", { name: "Open orders" });
  const wide = await card.evaluate((el) => ({ scroll: el.scrollWidth, client: el.clientWidth }));
  expect(wide.scroll).toBeGreaterThan(wide.client);
  const page_ = await page.evaluate(() => ({ x: document.documentElement.scrollWidth, w: window.innerWidth }));
  expect(page_.x).toBeLessThanOrEqual(page_.w);
});

test("the stop control gives the question back and Start again clears the thread", async ({ page }) => {
  await open(page);
  await ask(page, "slow one");
  await panel(page).getByRole("button", { name: "Stop" }).click();
  await expect(panel(page).getByRole("textbox", { name: "Ask TanyAI" })).toHaveValue("slow one");
  await ask(page, "who owes us");
  await expect(panel(page).getByRole("table", { name: "Who owes us" })).toBeVisible();
  await panel(page).getByRole("button", { name: "Start a new conversation" }).click();
  await expect(panel(page).getByRole("heading", { name: "Ask about your day" })).toBeVisible();
});

test("reduced motion: the sheet does not animate", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await open(page);
  const animation = await panel(page).evaluate((el) => getComputedStyle(el).animationName);
  expect(animation).toBe("none");
});

test("on a phone the sheet is full height and every control is at least 44 px", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  await ask(page, "who owes us");
  await expect(panel(page).getByRole("table", { name: "Who owes us" })).toBeVisible();
  const box = await panel(page).boundingBox();
  expect(box?.width).toBeGreaterThanOrEqual(389);
  expect(box?.height).toBeGreaterThanOrEqual(843);
  const controls = panel(page).locator("button, textarea");
  const count = await controls.count();
  expect(count).toBeGreaterThan(3);
  for (let i = 0; i < count; i += 1) {
    const b = await controls.nth(i).boundingBox();
    if (b === null) continue;
    expect(b.height, `control ${i} height`).toBeGreaterThanOrEqual(43.5);
    expect(b.width, `control ${i} width`).toBeGreaterThanOrEqual(43.5);
  }
});

test("on a phone Tab stays inside the sheet", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await open(page);
  for (let i = 0; i < 14; i += 1) await page.keyboard.press("Tab");
  const inside = await page.evaluate(() => {
    let el = document.activeElement as HTMLElement | null;
    while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement as HTMLElement;
    return el?.getRootNode() instanceof ShadowRoot && (el.getRootNode() as ShadowRoot).host.localName === "hh-tanyai-panel";
  });
  expect(inside).toBe(true);
});
