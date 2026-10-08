// Shared helpers for the Playwright legs: the front door's handoff, written
// the way the front door writes it, and the stub's reset.
import type { APIRequestContext } from "@playwright/test";

/** Runs in the page before any script: the front door's record, as the minted session. */
export function driverHandoff(): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: "de110000-0000-4000-9000-000000000003", exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 3_600_000,
    flow: "driver",
    roleLabel: "Driver",
    user: { userId: "de110000-0000-4000-9000-000000000003", email: "driver@hh-demo.invalid", name: "Driver (demo)" },
    roleCode: "driver",
    roleName: "Driver",
    tier: 3,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Puts the stub's world back to the morning: three stops ready on one dispatched run. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  // The dev server proxies /_stub to the stub, so the app's own base URL reaches it.
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

/** Switches the stub to a named scenario (empty, not-driver, run-planned, photo-fails). */
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
  bytes?: number;
  body: Record<string, unknown> | null;
}

/** The calls the stub saw on the dispatch routes, in order. */
export async function stubLog(request: APIRequestContext): Promise<LoggedCall[]> {
  const response = await request.get("/_stub/log");
  return (await response.json()) as LoggedCall[];
}

/** The dealer's day the way the core names it for the demo dealer: the house zone. */
export function houseDay(at: Date = new Date()): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: "America/Toronto",
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(at);
}
