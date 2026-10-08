// Shared moves for the Driver's acceptance run on the demo image: minting the
// Driver's demo session through the gated public route, the staff reads and
// writes the assertions compare the screen against, the front door's handoff
// written the way the front door writes it, and a watch that fails any leg
// whose traffic carried a money word.
import { expect, test as base, type Page, type TestInfo } from "@playwright/test";
import { createHash } from "node:crypto";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describeHits, scanPayload } from "../_lib/fence.mjs";

export const BASE = process.env.DRIVE_IMAGE_URL ?? "http://127.0.0.1:5801";

/** The dealer's day in the house zone, the way the app and the core name it. */
export function houseDay(at: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(at);
}

// ---- the demo mint, paced to a human ----
//
// The mint is guarded by a sliding window, so the run spaces its mints the way
// the till's acceptance does (web/apps/counter-till/test/acceptance/helpers.ts):
// ten seconds between mints, held in a small file keyed on the box.

const MINT_SPACING_MS = 10_000;
const WINDOW_PASS_MS = 31_000;

function paceFile(): string {
  return join(tmpdir(), `drive-mint-pace-${createHash("sha256").update(BASE).digest("hex").slice(0, 16)}.json`);
}

function reserveSlot(): number {
  let held = 0;
  try {
    held = (JSON.parse(readFileSync(paceFile(), "utf8")) as { lastMintAt?: number }).lastMintAt ?? 0;
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

let memo: Promise<Minted> | null = null;

/** Mint the Driver's demo session once per run: the mint is a credential shaped effect. */
export function mintDriver(): Promise<Minted> {
  if (memo === null) {
    memo = (async () => {
      await sleep(reserveSlot());
      const ask = () =>
        fetch(new URL("/api/v1/demo/session", BASE), {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Basic ${Buffer.from(`demo:${process.env.DRIVE_GATE_PHRASE ?? ""}`).toString("base64")}`,
          },
          body: JSON.stringify({ flow: "driver" }),
        });
      let answer = await ask();
      if (answer.status === 429) {
        await sleep(WINDOW_PASS_MS);
        await sleep(reserveSlot());
        answer = await ask();
      }
      expect(answer.ok, "the demo mint answers the Driver's flow").toBe(true);
      return (await answer.json()) as Minted;
    })();
  }
  return memo;
}

/** A staff call through the app's own origin, with the driver's bearer and org. */
export async function staff(
  page: Page,
  method: "GET" | "POST",
  path: string,
  data?: unknown,
): Promise<{ status: number; body: any }> {
  const { token } = await mintDriver();
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

/** The driver's own runs on the dealer's day, as the core answers them. */
export async function myRuns(page: Page): Promise<any[]> {
  const answer = await staff(page, "GET", `/api/v1/dispatch/my-runs?date=${houseDay()}`);
  expect(answer.status, "my-runs answers the Driver").toBe(200);
  return answer.body.items as any[];
}

/**
 * The front door's handoff, written the way the front door writes it, before
 * the app boots: the app lands signed in on the minted session with no picker.
 * Test code may touch storage; the app's own sources may not.
 */
export async function arriveFromFrontDoor(page: Page): Promise<void> {
  const minted = await mintDriver();
  const payload = minted.token.split(".")[1] ?? "";
  const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as { exp?: number };
  const record = {
    token: minted.token,
    tokenExpiresAtMs: (claims.exp ?? Math.floor(Date.now() / 1000) + 3600) * 1000,
    flow: minted.flow,
    roleLabel: "Driver",
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
  await page.goto("/drive/");
}

/** A screenshot on a settled page, named for the story, into the untracked shot folder. */
export async function shot(page: Page, name: string, testInfo: TestInfo): Promise<void> {
  const dir = process.env.DRIVE_SHOT_DIR ?? join(tmpdir(), "drive-acceptance-shots");
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
