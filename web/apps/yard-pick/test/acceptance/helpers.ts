// Shared moves for the yard team's acceptance run on the demo image: minting
// the Yard Team's demo session through the gated public route, the staff
// reads and writes the assertions compare the screen against, the front
// door's handoff written the way the front door writes it, and a watch that
// fails any leg whose traffic carried a money word.
import { expect, test as base, type Page, type TestInfo } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describeHits, scanPayload } from "../_lib/fence.mjs";

export const BASE = process.env.YARD_IMAGE_URL ?? "http://127.0.0.1:5801";

/** Trenton, the populate's own id: the branch the yard team's grant reaches. */
export const TRENTON = "5d1b3f6e-0000-4000-9000-000000000001";

// ---- the demo mint, paced to a human ----
//
// The mint is guarded by a sliding window, so the run spaces its mints the way
// the till's and the driver's acceptance runs do.

const MINT_SPACING_MS = 10_000;
const WINDOW_PASS_MS = 31_000;

function paceFile(): string {
  return join(tmpdir(), `yard-mint-pace-${createHash("sha256").update(BASE).digest("hex").slice(0, 16)}.json`);
}

function reserveSlot(): number {
  let held = 0;
  try {
    held = ((JSON.parse(readFileSync(paceFile(), "utf8")) as { lastMintAt?: number }).lastMintAt) ?? 0;
  } catch {
    held = 0;
  }
  const now = Date.now();
  const raw = held + MINT_SPACING_MS - now;
  const wait = raw > MINT_SPACING_MS + 30_000 ? 0 : Math.max(raw, 0);
  writeFileSync(paceFile(), JSON.stringify({ lastMintAt: now + wait }));
  return wait;
}

const sleep = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

export interface Minted {
  token: string;
  user: { user_id: string; email: string; display_name: string; role_code: string; role_name: string; tier: number; scope: string };
  flow: string;
}

const mints = new Map<string, Promise<Minted>>();

/** Mint a demo session once per run, by flow name: the yard team's, or the rep's (the Sales login). */
export function mint(flow: string): Promise<Minted> {
  const memo = mints.get(flow);
  if (memo !== undefined) return memo;
  const made = (async () => {
    await sleep(reserveSlot());
    const ask = () =>
      fetch(new URL("/api/v1/demo/session", BASE), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Basic ${Buffer.from(`demo:${process.env.YARD_GATE_PHRASE ?? ""}`).toString("base64")}`,
        },
        body: JSON.stringify({ flow }),
      });
    let answer = await ask();
    if (answer.status === 429) {
      await sleep(WINDOW_PASS_MS);
      await sleep(reserveSlot());
      answer = await ask();
    }
    expect(answer.ok, `the demo mint answers the ${flow} flow`).toBe(true);
    return (await answer.json()) as Minted;
  })();
  mints.set(flow, made);
  return made;
}

/** A staff call through the app's own origin, with a minted bearer and the org. */
export async function staff(
  page: Page,
  method: "GET" | "POST" | "PUT",
  path: string,
  data?: unknown,
  flow = "yard-team",
): Promise<{ status: number; body: any }> {
  const { token } = await mint(flow);
  const answer = await page.request.fetch(path, {
    method,
    headers: { Authorization: `Bearer ${token}`, "X-Org": "hh-demo" },
    ...(data !== undefined ? { data } : {}),
  });
  let body: unknown = null;
  try {
    body = await answer.json();
  } catch {
    body = null;
  }
  return { status: answer.status(), body };
}

/** The yard's own queue at a branch, as the core answers it. */
export async function queue(page: Page, branchId = TRENTON): Promise<any> {
  const answer = await staff(page, "GET", `/api/v1/yard/pick-queue?branch_id=${branchId}`);
  return answer;
}

/**
 * The front door's handoff, written the way the front door writes it, before
 * the app boots: the app lands signed in on the minted session with no picker.
 * Test code may touch storage; the app's own sources may not.
 */
export async function arriveFromFrontDoor(page: Page): Promise<void> {
  const minted = await mint("yard-team");
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
  await page.goto("/yard/");
}

/** A screenshot on a settled page, named for the story, into the untracked shot folder. */
export async function shot(page: Page, name: string, testInfo: TestInfo): Promise<void> {
  const dir = process.env.YARD_SHOT_DIR ?? join(tmpdir(), "yard-acceptance-shots");
  mkdirSync(dir, { recursive: true });
  await page.waitForLoadState("networkidle");
  await page.screenshot({ path: join(dir, `${testInfo.workerIndex}-${name}.png`), fullPage: true });
}

/**
 * Every leg is also the money leg: each JSON answer the page receives from the
 * core during a leg is scanned for money words and figures, and the leg fails
 * naming the route and the words if any arrived (the keys are scanned, and the
 * values for a figure that reads as money). The scan reads text the server
 * sent, never a credential.
 */
export const test = base.extend<{ moneyWatch: void }>({
  moneyWatch: [
    async ({ page }, use) => {
      const bodies: Array<[string, string]> = [];
      page.on("response", async (response) => {
        const url = new URL(response.url());
        if (!url.pathname.startsWith("/api/v1/")) return;
        if (!(response.headers()["content-type"] ?? "").includes("json")) return;
        try {
          bodies.push([`${response.request().method()} ${url.pathname}`, await response.text()]);
        } catch {
          // a body that cannot be read is not evidence of money
        }
      });
      await use();
      const hits = bodies.flatMap(([name, text]) => scanPayload(name, text));
      expect(describeHits(hits), "no money word or figure in any payload the core sent").toEqual([]);
    },
    { auto: true },
  ],
});

export { expect };
