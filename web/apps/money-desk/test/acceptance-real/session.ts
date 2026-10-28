// The session signer for the box under test: the world's named staff user of a role, signed in on the browser
// context, and the box's base URL.
//
// W2-4 (d)'s stand-in, written to the interface (W2-4-HARNESS-INTERFACE-r1.md): asStaff(context, who) puts the
// session for the world's named staff user of that role on the browser context, so the next page.goto lands signed
// in on the real core, and coreURL() is the box's base URL, for a spec that reads the core's API directly. (a)
// writes the real one: a staff session signed with the run's throwaway key file, which the finance image verifies
// at core/cmd/core/main.go:793. The fold replaces this file with (a)'s, and the specs import only these two names,
// so nothing else moves.
//
// HOW THIS ONE SIGNS IN: through the demo mint the box's own origin carries, POST /api/v1/demo/session, the way
// web/apps/drive-run/test/acceptance/helpers.ts does. The mint answers with the token and with the user block, so
// the record the app adopts carries the core's own words for that user and this file hard codes no user. The gate's
// phrase comes from the environment by name and is never written down: the demo image asks for it and the stub
// answers without it.
//
// THE ONE ROLE THIS STAND-IN CANNOT SIGN: today's seed names no bookkeeper. The world's own bookkeeper is planted
// by W2-1b's world, and (a)'s helper signs that user.

import { type BrowserContext } from "@playwright/test";

export type StaffRole = "financial_controller" | "bookkeeper" | "general_manager" | "branch_manager";

interface MintedUser {
  readonly user_id: string;
  readonly email: string;
  readonly display_name: string;
  readonly role_code: string;
  readonly role_name: string;
  readonly tier: number;
  readonly scope: string;
}

interface Minted {
  readonly token: string;
  readonly flow: string;
  readonly user: MintedUser;
}

/** The flow the demo mint mints each role with. Undefined for a role today's seed does not name. */
const FLOW_BY_ROLE: Record<StaffRole, string | undefined> = {
  financial_controller: "financial-controller",
  bookkeeper: undefined,
  general_manager: "general-manager",
  branch_manager: "branch-manager",
};

/** The gate's phrase, by name in the environment, the way the other acceptance suites take it. */
const GATE_PHRASE_ENV = "MD_GATE_PHRASE";

/** The box's base URL, by name in the environment: the image's address, as the other acceptance suites take it. */
const BOX_URL_ENV = "MD_IMAGE_URL";

/** The box's base URL, for a spec that reads the core's API directly. */
export function coreURL(): string {
  return process.env[BOX_URL_ENV] ?? "http://127.0.0.1:8080";
}

/** The token's own expiry, read from the token the mint answered, so the record never claims more than it has. */
function expiresAtMs(token: string): number {
  const payload = token.split(".")[1] ?? "";
  try {
    const claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as { exp?: number };
    if (typeof claims.exp === "number") return claims.exp * 1000;
  } catch {
    // A token without a readable expiry falls back to the hour below.
  }
  return Date.now() + 3_600_000;
}

/** The demo mint, through the box's own origin, which is what carries the gate and the core behind it. */
async function mint(context: BrowserContext, flow: string): Promise<Minted> {
  const phrase = process.env[GATE_PHRASE_ENV] ?? "";
  const ask = () =>
    context.request.post("/api/v1/demo/session", {
      headers: {
        "Content-Type": "application/json",
        Authorization: `Basic ${Buffer.from(`demo:${phrase}`).toString("base64")}`,
      },
      data: { flow },
    });
  let answer = await ask();
  if (answer.status() === 429) {
    // The mint's window is a sliding one: the wait is the window's own width, then one more try.
    await new Promise((resolve) => setTimeout(resolve, 31_000));
    answer = await ask();
  }
  if (!answer.ok()) {
    throw new Error(`the demo mint refused the ${flow} session: ${answer.status()}`);
  }
  return (await answer.json()) as Minted;
}

/**
 * Puts the world's named staff user of that role on the browser context, so the next page.goto lands signed in on
 * the real core. The record is the front door's own shape, the way the app adopts it
 * (web/apps/money-desk/src/demo-account.ts).
 */
export async function asStaff(context: BrowserContext, who: StaffRole): Promise<void> {
  const flow = FLOW_BY_ROLE[who];
  if (flow === undefined) {
    throw new Error(
      `today's seed names no ${who} to sign in as: the world's own bookkeeper is planted by W2-1b's world, and (a)'s helper signs that user.`,
    );
  }
  const minted = await mint(context, flow);
  const record = {
    token: minted.token,
    tokenExpiresAtMs: expiresAtMs(minted.token),
    flow: minted.flow,
    roleLabel: minted.user.role_name,
    user: { userId: minted.user.user_id, email: minted.user.email, name: minted.user.display_name },
    roleCode: minted.user.role_code,
    roleName: minted.user.role_name,
    tier: minted.user.tier,
    scope: minted.user.scope,
  };
  await context.addInitScript((value: string) => {
    if (window.sessionStorage.getItem("hh-demo.front-door.session.v1") === null) {
      window.sessionStorage.setItem("hh-demo.front-door.session.v1", value);
    }
  }, JSON.stringify(record));
}
