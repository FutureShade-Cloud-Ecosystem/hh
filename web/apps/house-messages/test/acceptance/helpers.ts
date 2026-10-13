// Shared moves for House Messages' acceptance run on the demo image: a persona's demo session through
// the gated public route, the staff reads the assertions compare the screen against, and the front
// door's handoff written the way the front door writes it.
import { expect, test as base, type Browser, type Page, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const BASE = process.env.MSG_IMAGE_URL ?? "http://127.0.0.1:5801";

export type Flow = "sales" | "yard-manager" | "payables-receivables";

export interface Minted {
  token: string;
  user: { user_id: string; email: string; display_name: string; role_code: string; role_name: string; tier: number; scope: string };
  flow: string;
}

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));
const memo = new Map<string, Promise<Minted>>();

/** Mint a persona's demo session once per run: the mint is a credential shaped effect with a limit. */
export function mint(flow: Flow): Promise<Minted> {
  let held = memo.get(flow);
  if (held === undefined) {
    held = (async () => {
      const ask = () =>
        fetch(new URL("/api/v1/demo/session", BASE), {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Basic ${Buffer.from(`demo:${process.env.MSG_GATE_PHRASE ?? ""}`).toString("base64")}`,
          },
          body: JSON.stringify({ flow }),
        });
      let answer = await ask();
      if (answer.status === 429) {
        await sleep(31_000);
        answer = await ask();
      }
      expect(answer.ok, `the demo mint answers the ${flow} flow`).toBe(true);
      return (await answer.json()) as Minted;
    })();
    memo.set(flow, held);
  }
  return held;
}

/** A staff call through the app's own origin, with a persona's bearer and org. */
export async function staff(
  page: Page,
  flow: Flow,
  method: "GET" | "POST" | "PUT",
  path: string,
  data?: unknown,
): Promise<{ status: number; body: any; text: string }> {
  const { token } = await mint(flow);
  const answer = await page.request.fetch(path, {
    method,
    headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" },
    ...(data !== undefined ? { data } : {}),
  });
  const text = await answer.text();
  let body: unknown = null;
  try {
    body = JSON.parse(text);
  } catch {
    body = null;
  }
  return { status: answer.status(), body, text };
}

/** The same call with no credential at all. */
export async function bare(page: Page, path: string): Promise<number> {
  const answer = await page.request.fetch(path, { method: "GET", headers: { "X-Org": "hh-demo" } });
  return answer.status();
}

/** One persona's channels as the core answers them. */
export async function channelsOf(page: Page, flow: Flow): Promise<Array<{ id: string; slug?: string; name: string; kind: string; tier: string; unread_count: number }>> {
  const answer = await staff(page, flow, "GET", "/api/v1/messaging/channels");
  expect(answer.status).toBe(200);
  return answer.body.channels as Array<{ id: string; slug?: string; name: string; kind: string; tier: string; unread_count: number }>;
}

export async function channelBySlug(page: Page, flow: Flow, slug: string): Promise<{ id: string; name: string }> {
  const found = (await channelsOf(page, flow)).find((c) => c.slug === slug);
  expect(found, `${flow} holds the ${slug} room`).toBeDefined();
  return found as { id: string; name: string };
}

/** The front door's handoff, written the way the front door writes it, before the app boots. */
export async function arriveFromFrontDoor(page: Page, flow: Flow): Promise<void> {
  const minted = await mint(flow);
  const payload = minted.token.split(".")[1] ?? "";
  const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as { exp?: number };
  const record = {
    token: minted.token,
    tokenExpiresAtMs: (claims.exp ?? Math.floor(Date.now() / 1000) + 3600) * 1000,
    flow: minted.flow,
    roleLabel: minted.user.role_name,
    user: { userId: minted.user.user_id, email: minted.user.email, name: minted.user.display_name },
    roleCode: minted.user.role_code,
    roleName: minted.user.role_name,
    tier: minted.user.tier,
    scope: minted.user.scope,
  };
  await page.addInitScript((value: string) => {
    if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
      window.sessionStorage.setItem("hh-demo.front-door.session.v1", value);
    }
  }, JSON.stringify(record));
  await page.goto("/messages/");
  await expect(page.locator("[data-channel-list]")).toBeVisible();
}

/** A second person on their own screen: a fresh browser context with the gate's credentials. */
export async function secondPerson(browser: Browser, flow: Flow): Promise<Page> {
  const context = await browser.newContext({
    baseURL: BASE,
    viewport: { width: 1440, height: 900 },
    httpCredentials: { username: "demo", password: process.env.MSG_GATE_PHRASE ?? "" },
  });
  const page = await context.newPage();
  await arriveFromFrontDoor(page, flow);
  return page;
}

/** A screenshot on a settled page, named for the story, into the untracked shot folder. */
export async function shot(page: Page, name: string, testInfo: TestInfo): Promise<void> {
  const dir = process.env.MSG_SHOT_DIR ?? join(tmpdir(), "messages-acceptance-shots");
  mkdirSync(dir, { recursive: true });
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${testInfo.workerIndex}-${name}.png`), fullPage: true });
}

export const test = base;
export { expect };
