// Shared moves for the dispatch board's acceptance run on the demo image: the Logistics Manager's
// demo session through the gated public route, the staff reads the assertions compare the screen
// against, and the front door's handoff written the way the front door writes it.
import { expect, test as base, type Page, type TestInfo } from "@playwright/test";
import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const BASE = process.env.DISPATCH_IMAGE_URL ?? "http://127.0.0.1:5801";

export interface Minted {
  token: string;
  user: { user_id: string; email: string; display_name: string; role_code: string; role_name: string; tier: number; scope: string };
  flow: string;
}

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));
let memo: Promise<Minted> | null = null;

/** Mint the Logistics Manager's demo session once per run: the mint is a credential shaped effect. */
export function mintDispatcher(): Promise<Minted> {
  if (memo === null) {
    memo = (async () => {
      const ask = () =>
        fetch(new URL("/api/v1/demo/session", BASE), {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Basic ${Buffer.from(`demo:${process.env.DISPATCH_GATE_PHRASE ?? ""}`).toString("base64")}`,
          },
          body: JSON.stringify({ flow: "logistics-manager" }),
        });
      let answer = await ask();
      if (answer.status === 429) {
        await sleep(31_000);
        answer = await ask();
      }
      expect(answer.ok, "the demo mint answers the Logistics Manager's flow").toBe(true);
      return (await answer.json()) as Minted;
    })();
  }
  return memo;
}

/** A staff call through the app's own origin, with the dispatcher's bearer and org. */
export async function staff(page: Page, method: "GET" | "POST", path: string, data?: unknown): Promise<{ status: number; body: any; text: string }> {
  const { token } = await mintDispatcher();
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

/** The branch the Logistics Manager works: the one yard its login reaches. */
export async function myBranch(page: Page): Promise<string> {
  const branches = await staff(page, "GET", "/api/v1/branches");
  expect(branches.status).toBe(200);
  const rows = (Array.isArray(branches.body) ? branches.body : branches.body.items) as Array<{ id: string }>;
  expect(rows, "the Logistics Manager reaches exactly one branch").toHaveLength(1);
  return rows[0]!.id;
}

/** The front door's handoff, written the way the front door writes it, before the app boots. */
export async function arriveFromFrontDoor(page: Page): Promise<void> {
  const minted = await mintDispatcher();
  const payload = minted.token.split(".")[1] ?? "";
  const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as { exp?: number };
  const record = {
    token: minted.token,
    tokenExpiresAtMs: (claims.exp ?? Math.floor(Date.now() / 1000) + 3600) * 1000,
    flow: minted.flow,
    roleLabel: "Logistics Manager",
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
  await page.goto("/dispatch/");
}

/** A screenshot on a settled page, named for the story, into the untracked shot folder. */
export async function shot(page: Page, name: string, testInfo: TestInfo): Promise<void> {
  const dir = process.env.DISPATCH_SHOT_DIR ?? join(tmpdir(), "dispatch-acceptance-shots");
  mkdirSync(dir, { recursive: true });
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${testInfo.workerIndex}-${name}.png`), fullPage: true });
}

export const test = base;
export { expect };
