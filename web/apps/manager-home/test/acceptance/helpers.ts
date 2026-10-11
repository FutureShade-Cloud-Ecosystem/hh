// Shared helpers for Manager Home's acceptance legs: the mint through the
// box's gated route (one token per flow per process, the pace a person
// tapping cards holds) and a staff GET through the same origin the app uses.
import { expect, type Page } from "@playwright/test";

const minted = new Map<string, Promise<string>>();

/** The yard manager's mint, or any flow's, memoized: a run never bursts the box's own sign-in guard. */
export function mint(page: Page, flow: string): Promise<string> {
  let inFlight = minted.get(flow);
  if (inFlight === undefined) {
    inFlight = (async () => {
      const phrase = process.env.MH_GATE_PHRASE ?? "";
      const base = process.env.MH_IMAGE_URL ?? "http://127.0.0.1:5811";
      const answer = await fetch(new URL("/api/v1/demo/session", base), {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Basic ${Buffer.from(`demo:${phrase}`).toString("base64")}`,
        },
        body: JSON.stringify({ flow }),
      });
      expect(answer.ok).toBe(true);
      const body = (await answer.json()) as { token?: string };
      expect(typeof body.token).toBe("string");
      return body.token as string;
    })();
    minted.set(flow, inFlight);
  }
  return inFlight;
}

/** A staff GET through the same origin the app uses, with a bearer and org. */
export async function staffGet(
  page: Page,
  bearer: string,
  path: string,
): Promise<{ status: number; body: Record<string, unknown> }> {
  const answer = await page.request.get(path, {
    headers: { Authorization: `Bearer ${bearer}`, "X-Org": "hh-demo" },
  });
  return { status: answer.status(), body: (await answer.json()) as Record<string, unknown> };
}

/** Writes the front door's handoff for the yard manager, the way the front door writes it. */
export function yardHandoff(): void {
  const yardManagerId = "de110000-0000-4000-9000-000000000003";
  const part = (value: unknown): string =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const exp = Math.floor(Date.now() / 1000) + 3600;
  const token = `${part({ alg: "ES256", typ: "JWT" })}.${part({ sub: yardManagerId, exp })}.acc`;
  const record = {
    token,
    tokenExpiresAtMs: Date.now() + 3_600_000,
    flow: "yard-manager",
    roleLabel: "Yard manager",
    user: { userId: yardManagerId, email: "yard.manager@hh-demo.invalid", name: "Yard manager (demo)" },
    roleCode: "yard_manager",
    roleName: "Yard manager",
    tier: 2,
    scope: "BRANCH",
  };
  window.sessionStorage.setItem("hh-demo.front-door.session.v1", JSON.stringify(record));
}
