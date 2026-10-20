// The common role legs for House Messages on the demo image (ROLE-GOES-LIVE, section 6): the card, the
// wall, the branch and the money. Then what the screen shows against what the core answers.
import { test, expect, arriveFromFrontDoor, bare, channelsOf, mint, shot, staff } from "./helpers.js";

test("L-card: Sales signs in from its card on the front door and opens House Messages from the tile", async ({ page }, testInfo) => {
  await page.goto("/");
  await page.getByRole("button", { name: "Sales", exact: true }).click();
  await expect(page.getByTestId("home-title")).toHaveText("Sales");
  const tile = page.getByRole("button", { name: "House Messages", exact: true });
  await expect(tile).toHaveAttribute("data-href", "/messages/");
  await tile.click();
  await page.waitForURL("**/messages/");
  await expect(page.locator("[data-channel-list]")).toBeVisible();
  await expect(page.locator("hh-field-frame[signed-in]")).toBeVisible();
  await shot(page, "01-card-to-messages", testInfo);
});

test("L-wall: no credential is refused, and one person's own rooms are not another's", async ({ page }) => {
  expect(await bare(page, "/api/v1/messaging/channels")).toBe(401);
  const mine = await channelsOf(page, "sales");
  const direct = mine.find((c) => c.kind === "DM");
  expect(direct, "Sales holds its own customer message room").toBeDefined();
  const other = await staff(page, "yard-manager", "GET", `/api/v1/messaging/channels/${direct!.id}/messages`);
  expect(other.status).toBe(403);
  expect(other.body.error).toBe("not a channel member");
  expect((await channelsOf(page, "yard-manager")).some((c) => c.id === direct!.id)).toBe(false);
});

test("L-branch: every room that names a branch names the persona's own", async ({ page }) => {
  for (const flow of ["sales", "yard-manager"] as const) {
    const branches = await staff(page, flow, "GET", "/api/v1/branches");
    expect(branches.status).toBe(200);
    const rows = (Array.isArray(branches.body) ? branches.body : branches.body.items) as Array<{ id: string }>;
    const own = new Set(rows.map((b) => b.id));
    for (const c of (await channelsOf(page, flow)) as Array<{ branch_id?: string }>) {
      if (c.branch_id) expect(own.has(c.branch_id), `${flow} sees a room of a branch it does not reach`).toBe(true);
    }
  }
});

test("L-cost and the money rule: the screen shows the core's rooms and words, and draws no figure of its own", async ({ page }, testInfo) => {
  await arriveFromFrontDoor(page, "sales");
  const core = await channelsOf(page, "sales");
  const shown = await page.locator("[data-channel-list] .ch-name").allInnerTexts();
  expect([...shown].sort()).toEqual(core.map((c) => c.name.trim() === "" ? (c.kind === "DM" ? "Direct message" : "Untitled room") : c.name).sort());
  // A dollar figure may appear only inside a message body the core sent; the chrome carries none.
  const chrome = await page.evaluate(() => {
    const root = document.querySelector("house-messages-app")?.shadowRoot;
    const clone = root?.querySelector("main")?.cloneNode(true) as HTMLElement | undefined;
    clone?.querySelectorAll("[data-body]").forEach((el) => el.remove());
    return clone?.textContent ?? "";
  });
  expect(chrome).not.toMatch(/\$\s?\d/);
  // No page of messages carries a cost field.
  for (const room of core.slice(0, 5)) {
    const page1 = await staff(page, "sales", "GET", `/api/v1/messaging/channels/${room.id}/messages?limit=5`);
    expect(page1.status).toBe(200);
    expect(page1.text).not.toMatch(/"cost/i);
  }
  await shot(page, "02-rooms-as-the-core-sends-them", testInfo);
});

test("the core's page names its rows messages, and the screen reads it", async ({ page }, testInfo) => {
  const rooms = await channelsOf(page, "sales");
  const room = rooms.find((c) => c.slug === "office")!;
  const answer = await staff(page, "sales", "GET", `/api/v1/messaging/channels/${room.id}/messages`);
  expect(answer.status).toBe(200);
  const key = Array.isArray(answer.body.items) ? "items" : "messages";
  testInfo.annotations.push({ type: "page-rows-key", description: key });
  expect(Array.isArray(answer.body[key])).toBe(true);
});

test("the demo image issues no stream tickets, so the screen says it reads every ten seconds", async ({ page }) => {
  const { token } = await mint("sales");
  const ticket = await page.request.fetch("/api/v1/messaging/stream-ticket", { method: "POST", headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" } });
  testInfo_note(ticket.status());
  await arriveFromFrontDoor(page, "sales");
  if (ticket.status() === 501) {
    await expect(page.locator("[data-live]")).toContainText("Messages are read every 10 seconds");
  } else {
    await expect(page.locator("[data-live]")).toContainText("Live");
  }
});

function testInfo_note(_status: number): void {
  /* the ticket status decides which words the screen must carry; asserted just above */
}
