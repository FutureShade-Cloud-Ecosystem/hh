// W2-4 (b): the session signer, provisional until part (a) lands.
//
// The harness interface (W2-4-HARNESS-INTERFACE-r1) pins this file's two
// exports: asStaff(context, who) puts the world's named staff user's session
// on the browser context so the next page.goto lands signed in, and coreURL()
// is the box's base URL for a spec that reads the core's API directly. Part
// (a) owns the file; this version is (b)'s seam, written to that interface so
// the (b) specs can run and so the fold has a concrete thing to reconcile.
//
// HOW THE SESSION LANDS. The Money Desk reads the front door's own session
// key out of session storage at boot, hands the token to the auth controller,
// and drops the key. That record is the one credential a test can plant, so
// asStaff writes it for every page of the context before the app's script
// runs.
//
// THE TOKEN IS THE BOX'S OWN. On a real box the token is signed with the
// session key the box was started with (CORE_SESSION_SIGNING_KEY_FILE), for
// the box's issuer and staff audience, with the subject the box granted the
// role to. Without those three names there is no box to sign for, and the
// record carries the stub's placeholder token instead: the stub does not
// verify a token, and a spec that runs under the stub has to fail on the
// world's figures, not on a missing session.
import { createHash, createPrivateKey, createSign, randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import type { BrowserContext } from "@playwright/test";
import { AR_FINANCIAL_CONTROLLER } from "./world.js";

/** The roles the interface names. */
export type StaffRole = "financial_controller" | "bookkeeper" | "general_manager" | "branch_manager";

/** The world's named staff: who holds a role, and the platform id the box granted it to. */
export interface StaffPerson {
  readonly platformUserId: string;
  readonly email: string;
  readonly name: string;
  readonly roleName: string;
  readonly tier: number;
  readonly scope: string;
}

/** The roles today's world names. A role the world has not named yet is refused, never faked. */
const NAMED: Partial<Record<StaffRole, StaffPerson>> = {
  financial_controller: AR_FINANCIAL_CONTROLLER,
};

/**
 * Puts the world's named staff user's session on the browser context, so the
 * next page.goto lands signed in on the real core.
 */
export async function asStaff(context: BrowserContext, who: StaffRole): Promise<void> {
  const person = NAMED[who];
  if (person === undefined) {
    throw new Error(`the world names no ${who} yet: the box has not granted that role to a person`);
  }
  if (person.platformUserId === "") {
    throw new Error(`the world names no ${who} with a platform id yet: the box has not granted that role to a person`);
  }
  const record = {
    token: sessionToken(person),
    tokenExpiresAtMs: Date.now() + 3600_000,
    flow: who.replace(/_/g, "-"),
    roleLabel: person.roleName,
    user: { userId: person.platformUserId, email: person.email, name: person.name },
    roleCode: who,
    roleName: person.roleName,
    tier: person.tier,
    scope: person.scope,
  };
  await context.addInitScript(plant, JSON.stringify(record));
}

/** The box's base URL, where a spec reads the core's API directly to compare figures. */
export function coreURL(): string {
  const named = process.env.W2_4B_CORE_URL;
  if (named !== undefined && named !== "") return named.replace(/\/+$/, "");
  return "http://127.0.0.1:57700";
}

/**
 * Writes the front door's record before the app's own script runs. The key is
 * written out here: an init script is serialized on its own, so it carries no
 * closure from this module.
 */
function plant(raw: string): void {
  window.sessionStorage.setItem("hh-demo.front-door.session.v1", raw);
}

/**
 * The box's own session token: ES256 over the box's key, the box's issuer and
 * staff audience, the platform user the box granted the role to.
 */
function sessionToken(person: StaffPerson): string {
  const keyFile = process.env.CORE_SESSION_SIGNING_KEY_FILE;
  const issuer = process.env.CORE_TOKEN_ISSUER;
  const audience = process.env.CORE_STAFF_AUDIENCE;
  if (keyFile === undefined || keyFile === "" || issuer === undefined || issuer === "" || audience === undefined || audience === "") {
    return placeholderToken(person.platformUserId);
  }
  const key = createPrivateKey(readFileSync(keyFile, "utf8"));
  const jwk = key.export({ format: "jwk" }) as { crv?: string; kty?: string; x?: string; y?: string };
  if (jwk.kty !== "EC" || jwk.crv !== "P-256" || jwk.x === undefined || jwk.y === undefined) {
    throw new Error("the box's session key is not an EC P-256 key");
  }
  // The kid is the RFC 7638 thumbprint of the public key: the same canonical
  // JWK the core hashes, so the core finds this key by the id the token names.
  const canonical = `{"crv":"P-256","kty":"EC","x":"${jwk.x}","y":"${jwk.y}"}`;
  const kid = createHash("sha256").update(canonical).digest("base64url");
  const now = Math.floor(Date.now() / 1000);
  const header = { alg: "ES256", kid, typ: "JWT" };
  const claims = {
    iss: issuer,
    sub: person.platformUserId,
    aud: audience,
    jti: randomBytes(16).toString("hex"),
    iat: now,
    exp: now + 3600,
  };
  const signingInput = `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(claims))}`;
  const signature = createSign("SHA256")
    .update(signingInput)
    .end()
    .sign({ key, dsaEncoding: "ieee-p1363" });
  return `${signingInput}.${signature.toString("base64url")}`;
}

/** The stub's placeholder token, for a run with no box to sign for. */
function placeholderToken(subject: string): string {
  const exp = Math.floor(Date.now() / 1000) + 3600;
  return `${base64url(JSON.stringify({ alg: "ES256", typ: "JWT" }))}.${base64url(JSON.stringify({ sub: subject, exp }))}.c2ln`;
}

function base64url(value: string): string {
  return Buffer.from(value, "utf8").toString("base64url");
}
