// THE SESSION THE HARNESS PUTS ON THE BROWSER CONTEXT (W2-4-HARNESS-INTERFACE-r1).
//
// The interface this file answers:
//   - asStaff(context, who) puts a session for the world's named staff user of that role on
//     the browser context, so the next page.goto lands signed in on the real core.
//   - coreURL() is the box's base URL, for a spec that reads the core's API directly.
//
// HOW THE SESSION REACHES THE APP. The Money Desk reads the front door's handoff record
// (src/demo-account.ts: the key "hh-demo.front-door.session.v1") in the demo build, adopts
// its token and drops the key. That is the only seam the app has that carries a bearer
// without FB Console, so the box under test serves the app in demo mode and this file
// writes the record the front door would have written.
//
// THE TOKEN, TWO PATHS.
//   - The box's path: the run's throwaway session key file. The core signs and verifies with
//     the key it was started with (cmd/core/signin.go: CORE_SESSION_SIGNING_KEY_FILE), so the
//     token here is the same shape the core's own minter writes (internal/identity/
//     sessionsigner.go): ES256, the kid is the RFC 7638 thumbprint of the public key, and the
//     claims are iss, sub, aud, jti, iat and exp.
//   - The stub's path: with no key file named, the token carries the shape the stub reads
//     (the stub decodes the payload and never checks the signature). That is what makes the
//     red run under the stub possible.
//
// THE STAFF TABLE is the demo world's staff users. The world's own named staff users come
// with the world, and the part (a) brief's fold replaces this table with them.
import { createHash, createPrivateKey, createPublicKey, randomBytes, sign as signBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import type { BrowserContext } from "@playwright/test";

/** The roles the interface names. */
export type StaffRole = "financial_controller" | "bookkeeper" | "general_manager" | "branch_manager";

/** One staff user of the world: who they are and what the desk shows for them. */
interface StaffUser {
  readonly id: string;
  readonly email: string;
  readonly name: string;
  readonly roleCode: string;
  readonly roleName: string;
  readonly flow: string;
  readonly tier: number;
  readonly scope: string;
}

/**
 * The demo world's staff users (the stub's world names them: test/stub/money-desk-stub.mjs
 * and test/stub/gl-world.mjs). The Financial Controller is the role the ledger's screens
 * are for; Payables and Receivables is the desk's reader, the one the interface calls the
 * bookkeeper; the General Manager and the branch's manager are the other two the interface
 * names.
 */
const STAFF: Readonly<Record<StaffRole, StaffUser>> = {
  financial_controller: {
    id: "de110000-0000-4000-9000-000000000008",
    email: "financial.controller@hh-demo.invalid",
    name: "Financial Controller (demo)",
    roleCode: "financial_controller",
    roleName: "Financial Controller",
    flow: "financial-controller",
    tier: 1,
    scope: "GLOBAL",
  },
  bookkeeper: {
    id: "de110000-0000-4000-9000-000000000007",
    email: "payables.receivables@hh-demo.invalid",
    name: "Payables and Receivables (demo)",
    roleCode: "payables_receivables",
    roleName: "Payables and Receivables",
    flow: "payables-receivables",
    tier: 2,
    scope: "GLOBAL",
  },
  general_manager: {
    id: "de110000-0000-4000-9000-000000000009",
    email: "general.manager@hh-demo.invalid",
    name: "General Manager (demo)",
    roleCode: "general_manager",
    roleName: "General Manager",
    flow: "general-manager",
    tier: 1,
    scope: "GLOBAL",
  },
  branch_manager: {
    id: "de110000-0000-4000-9000-000000000002",
    email: "sales.manager@hh-demo.invalid",
    name: "Sales manager (demo)",
    roleCode: "sales_manager",
    roleName: "Sales Manager",
    flow: "sales-manager",
    tier: 2,
    scope: "BRANCH",
  },
};

/** The front door's own key for the handoff record (src/demo-account.ts). */
const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";

/** The session window: eight hours, inside the core's bounds (StaffSessionTTL is twelve). */
const WINDOW_SECONDS = 8 * 60 * 60;

/**
 * The box's base URL, where its /api/v1 routes live. The harness names it in the
 * environment: the real config points it at the box's core, the stub variant at the stub.
 */
export function coreURL(): string {
  const url = (process.env.HH_CORE_URL ?? "").trim();
  return url.replace(/\/+$/, "");
}

/**
 * Put a session for the world's named staff user of that role on the browser context, so
 * the next page.goto lands signed in.
 */
export async function asStaff(context: BrowserContext, who: StaffRole): Promise<void> {
  const user = STAFF[who];
  if (user === undefined) throw new Error(`asStaff: no staff user for the role ${who}`);
  const record = {
    token: mintToken(user.id),
    tokenExpiresAtMs: Date.now() + WINDOW_SECONDS * 1000,
    flow: user.flow,
    roleLabel: user.roleName,
    user: { userId: user.id, email: user.email, name: user.name },
    roleCode: user.roleCode,
    roleName: user.roleName,
    tier: user.tier,
    scope: user.scope,
  };
  await context.addInitScript(writeFrontDoorRecord, { key: FRONT_DOOR_SESSION_KEY, record });
}

/** Runs in the page before any script: the front door's record, the way the front door writes it. */
function writeFrontDoorRecord(payload: { key: string; record: unknown }): void {
  if (window.sessionStorage.getItem(payload.key) === null) {
    window.sessionStorage.setItem(payload.key, JSON.stringify(payload.record));
  }
}

/** The token the box accepts: signed with the run's session key when one is named, else the stub's shape. */
function mintToken(subject: string): string {
  const keyFile = (process.env.HH_SESSION_KEY_FILE ?? process.env.CORE_SESSION_SIGNING_KEY_FILE ?? "").trim();
  if (keyFile !== "") return signWithSessionKey(keyFile, subject);
  return demoShapeToken(subject);
}

/** The box's path: an ES256 token over the run's session key, in the core's own claim shape. */
function signWithSessionKey(keyFile: string, subject: string): string {
  const issuer = (process.env.HH_TOKEN_ISSUER ?? process.env.CORE_TOKEN_ISSUER ?? "").trim();
  const audience = (process.env.HH_STAFF_AUDIENCE ?? process.env.CORE_STAFF_AUDIENCE ?? "").trim();
  if (issuer === "" || audience === "") {
    throw new Error(
      "asStaff: the session signer needs the box's token issuer and staff audience (CORE_TOKEN_ISSUER and CORE_STAFF_AUDIENCE)",
    );
  }
  const privateKey = createPrivateKey(readFileSync(keyFile));
  const publicJwk = createPublicKey(privateKey).export({ format: "jwk" });
  const x = publicJwk.x;
  const y = publicJwk.y;
  if (typeof x !== "string" || typeof y !== "string") {
    throw new Error("asStaff: the session key is not an EC P-256 key");
  }
  // The kid is the RFC 7638 thumbprint: SHA-256 over the canonical JWK, base64url, no padding.
  const canonical = `{"crv":"P-256","kty":"EC","x":"${x}","y":"${y}"}`;
  const kid = createHash("sha256").update(canonical).digest("base64url");
  const now = Math.floor(Date.now() / 1000);
  const header = { alg: "ES256", kid, typ: "JWT" };
  const claims = {
    iss: issuer,
    sub: subject,
    aud: audience,
    jti: randomBytes(16).toString("hex"),
    iat: now,
    exp: now + WINDOW_SECONDS,
  };
  const signingInput = `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(claims))}`;
  // A JWS elliptic signature is the fixed width r || s, not DER.
  const signature = signBytes("sha256", Buffer.from(signingInput, "utf8"), {
    key: privateKey,
    dsaEncoding: "ieee-p1363",
  });
  return `${signingInput}.${signature.toString("base64url")}`;
}

/** The stub's path: the token shape the stub reads (three parts, the middle one base64url JSON). */
function demoShapeToken(subject: string): string {
  const now = Math.floor(Date.now() / 1000);
  const header = { alg: "ES256", typ: "JWT" };
  const claims = { iss: "hh-demo-stub", sub: subject, aud: "staff", iat: now, exp: now + WINDOW_SECONDS };
  return `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(claims))}.c3R1Yi1zaWduYXR1cmUtYnl0ZXM`;
}

function base64url(text: string): string {
  return Buffer.from(text, "utf8").toString("base64url");
}
