// Shared helpers for the Playwright legs: the front door's handoff for a persona, written the way the
// front door writes it, and the stub's controls.
import { expect, test as base, type APIRequestContext, type Page } from "@playwright/test";

export { expect };

export type Persona = "sales" | "yard-manager" | "payables-receivables";

export const PERSONA = {
  sales: { id: "a0000000-0000-4000-9000-000000000001", email: "sales@hh-demo.invalid", name: "Inside Sales (demo)", roleCode: "inside_sales", roleName: "Inside Sales", label: "Sales" },
  "yard-manager": { id: "a0000000-0000-4000-9000-000000000002", email: "yard.manager@hh-demo.invalid", name: "Yard manager (demo)", roleCode: "yard_manager", roleName: "Yard Manager", label: "Yard manager" },
  "payables-receivables": { id: "a0000000-0000-4000-9000-000000000003", email: "payables@hh-demo.invalid", name: "Payables and Receivables (demo)", roleCode: "payables_receivables", roleName: "Payables and Receivables", label: "Payables and Receivables" },
} as const;

export const CH = {
  branch: "c0000000-0000-4000-9000-000000000001",
  yard: "c0000000-0000-4000-9000-000000000002",
  deliveries: "c0000000-0000-4000-9000-000000000003",
  esc: "c0000000-0000-4000-9000-000000000004",
  house: "c0000000-0000-4000-9000-000000000005",
  dm: "c0000000-0000-4000-9000-000000000006",
  pay: "c0000000-0000-4000-9000-000000000007",
} as const;

/** Runs in the page before any script: the front door's record, as the minted session. */
export function handoff(who: (typeof PERSONA)[Persona]): void {
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 7 * 86_400;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: who.id, exp })}.c2ln`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 7 * 86_400_000,
    flow: "demo",
    roleLabel: who.label,
    user: { userId: who.id, email: who.email, name: who.name },
    roleCode: who.roleCode,
    roleName: who.roleName,
    tier: 3,
    scope: "BRANCH",
  };
  if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
  }
}

/** Puts the stub's world back to the start. The dev server proxies /_stub to the stub. */
export async function resetStub(request: APIRequestContext): Promise<void> {
  const response = await request.post("/_stub/reset");
  if (!response.ok()) throw new Error(`stub reset failed: ${response.status()}`);
}

export async function stubPost(request: APIRequestContext, path: string, data: unknown = {}): Promise<unknown> {
  const response = await request.post(`/_stub/${path}`, { data });
  if (!response.ok()) throw new Error(`stub ${path} failed: ${response.status()}`);
  return response.json();
}

/** Another persona posts into a channel, as a person on another screen would. */
export async function says(request: APIRequestContext, who: Persona, channel: string, body: string, threadRootId?: string): Promise<void> {
  await stubPost(request, `as/${who}`, { channel_id: channel, body, ...(threadRootId ? { thread_root_id: threadRootId } : {}) });
}

/** Signs in through the front door's handoff and lands on the channel list. */
export async function openAs(page: Page, request: APIRequestContext, who: Persona = "sales"): Promise<void> {
  await resetStub(request);
  await page.addInitScript(handoff, PERSONA[who]);
  await page.goto("/");
  await expect(page.locator("[data-channel-list]")).toBeVisible();
}

export const channel = (page: Page, id: string) => page.locator(`[data-action='open-channel'][data-id='${id}']`);

export const test = base;
