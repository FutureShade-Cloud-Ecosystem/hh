// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's controls.
import type { APIRequestContext } from "@playwright/test";

/**
 * Runs in the page before any script: the front door's record, as the minted
 * session. The id is written inline because addInitScript serializes the
 * function alone: a module binding would arrive as a ReferenceError in the
 * page.
 */
export function yardHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "d0200000-0000-4000-9000-000000000002", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 3_600_000,
    flow: "yard-team",
    roleLabel: "Yard Team",
    user: { userId: "d0200000-0000-4000-9000-000000000002", email: "yard.demo@dibbits.example", name: "Yard persona (demo)" },
    roleCode: "yard_team",
    roleName: "Yard Team",
    tier: 3,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Puts the stub's world back to the morning: two unclaimed picks, one claimed, one held, one in progress with a shortage. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  // The dev server proxies /_stub to the stub, so the app's own base URL reaches it.
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

/** Switches the stub to a named scenario (empty, not-yard, poisoned, resolve-walled). */
export async function useScenario(request: APIRequestContext, name: string): Promise<void> {
  const response = await request.post("/_stub/scenario", { data: { name } });
  if (!response.ok()) throw new Error(`stub scenario failed: ${response.status()}`);
}

export interface LoggedCall {
  method: string;
  path: string;
  query: Record<string, string>;
  bearer: boolean;
  org: string;
  contentType: string;
  body: Record<string, unknown> | null;
}

/** The calls the stub saw on the yard's routes, in order. */
export async function stubLog(request: APIRequestContext): Promise<LoggedCall[]> {
  const response = await request.get("/_stub/log");
  return (await response.json()) as LoggedCall[];
}

export interface RepNotice {
  topic: string;
  order_no: string;
  line_no: number;
  body: string;
}

/** The notices the order's rep holds, the double of the rep's API read. */
export async function repNotices(request: APIRequestContext): Promise<RepNotice[]> {
  const response = await request.get("/_stub/rep-notices");
  return (await response.json()) as RepNotice[];
}
