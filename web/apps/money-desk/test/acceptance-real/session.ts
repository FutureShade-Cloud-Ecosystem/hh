// The acceptance-real session helper (W2-4 (a)): the one way in for the
// Money Desk's real core acceptance specs.
//
// THE INTERFACE (W2-4-HARNESS-INTERFACE-r1). This file exports three names
// the later parts write against: the StaffRole type, asStaff(context, who)
// and coreURL(). A part's spec imports only those from "./session.js", so a
// part never has to know how the world signs a person in.
//
// ONE WAY IN, TWO WORLDS.
//
//   THE REAL BOX. The session is the core's own. asStaff drives the real
//   exchange the desk's console build drives: the console double mints a
//   Console JWT for the box's provisioned acceptance subject (the subject the
//   box wrote into its state), and the core answers with the staff session it
//   mints itself, signed with the box's own session key. Nothing here signs a
//   token. The run's throwaway key is written for the core to use and is never
//   used to sign anything here.
//
//   THE STUB (no box state). The session is the front door's demo mint, the
//   way the harness's stub leg signs in: POST /api/v1/demo/session with the
//   role's flow, answered by the stub's own mint.
//
// THE ROLE. The box provisions the Financial Controller alone today (the
// finance seed's acceptance user, granted by HH_FINANCE_USERS at boot), so a
// role the box does not provision is refused in words. It is never faked: an
// invented identity would pass the desk's own checks and prove nothing about
// the core.
import type { BrowserContext } from "@playwright/test";
import { generateKeyPairSync, randomUUID } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync, chmodSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

/** The staff roles the interface names. */
export type StaffRole = "financial_controller" | "bookkeeper" | "general_manager" | "branch_manager";

/**
 * The flow each role signs in through under the stub, the front door's own
 * flow names. The stub's Payables and Receivables is the desk's reader, the
 * role the interface calls the bookkeeper.
 */
const FLOW_BY_ROLE: Record<StaffRole, string> = {
  financial_controller: "financial-controller",
  bookkeeper: "payables-receivables",
  general_manager: "general-manager",
  branch_manager: "branch-manager",
};

/** The console the box's own config names; the double answers for it. */
const CONSOLE_PROJECT = "acceptance-console";

/** The run's throwaway EC P-256 key: the core's session key for this run, and nothing else. */
export interface ThrowawayKey {
  readonly privateKeyPem: string;
}

export interface AcceptanceState {
  readonly boxUrl: string;
  readonly consoleEndpoint: string;
  readonly org: string;
  readonly issuer: string;
  readonly staffAudience: string;
  /** The platform user id the box provisioned; the token's sub. */
  readonly subject: string;
  readonly email: string;
  readonly displayName: string;
  readonly roleCode: string;
  readonly roleName: string;
}

const stateDir = (): string => {
  const dir = process.env.MDREAL_STATE;
  if (dir === undefined || dir === "") throw new Error("MDREAL_STATE is not set: run through acceptance-real.config.ts or box.sh");
  return dir;
};

export const statePath = (): string => join(stateDir(), "state.json");

/**
 * The box's state, when this run has a box. Null under the stub: there is no
 * box, so there is no provisioned subject and no box to read the core at.
 */
export function loadState(): AcceptanceState | null {
  const dir = process.env.MDREAL_STATE;
  if (dir === undefined || dir === "") return null;
  const path = join(dir, "state.json");
  if (!existsSync(path)) return null;
  return JSON.parse(readFileSync(path, "utf8")) as AcceptanceState;
}

/** One invented acceptance tester; nothing here names a real person. */
export function acceptanceTester(): { email: string; displayName: string; roleCode: string; roleName: string } {
  return {
    email: "acceptance.controller@example.invalid",
    displayName: "Acceptance Controller",
    roleCode: "financial_controller",
    roleName: "Financial Controller",
  };
}

/** Generate the run's throwaway EC P-256 key (PKCS8, one of the core's accepted forms). */
export function generateThrowawayKey(): ThrowawayKey {
  const { privateKey } = generateKeyPairSync("ec", { namedCurve: "P-256" });
  return { privateKeyPem: privateKey.export({ type: "pkcs8", format: "pem" }).toString() };
}

/** Write the private key where box.sh mounts it for the core; owner read only, the core's rule. */
export function writeKeyFile(key: ThrowawayKey): string {
  const dir = join(stateDir(), "keys");
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const path = join(dir, "session.pem");
  // A previous run left the file owner read only; reopen it owner writable
  // before the rewrite, then pin the owner read only mode again.
  try {
    chmodSync(path, 0o600);
  } catch {
    // the file does not exist yet
  }
  writeFileSync(path, key.privateKeyPem, { mode: 0o400 });
  chmodSync(path, 0o400);
  return path;
}

// ── the interface the parts write against ──

/** The staff user the core names, in the core's own words. */
interface StaffUser {
  readonly user_id: string;
  readonly email: string;
  readonly display_name: string;
  readonly role_code: string;
  readonly role_name: string;
  readonly tier: number;
  readonly scope: string;
}

/** The session a context carries: the front door's record shape, the world's session inside it. */
interface SessionRecord {
  readonly token: string;
  readonly tokenExpiresAtMs: number;
  readonly flow: string;
  readonly roleLabel: string;
  readonly user: { readonly userId: string; readonly email: string; readonly name: string };
  readonly roleCode: string;
  readonly roleName: string;
  readonly tier: number;
  readonly scope: string;
}

/**
 * Puts the world's own session on the context, so the next page load lands
 * signed in as that role.
 *
 * On the real box the session is the core's own: asStaff drives the real
 * exchange (the console double mints the Console JWT, the core verifies it,
 * resolves the staff user and its grant, and mints the staff session with the
 * box's key). Under the stub it is the front door's demo mint, the way the
 * harness's stub leg signs in. A role the world does not name is refused in
 * words, never faked with an invented identity.
 */
export async function asStaff(context: BrowserContext, who: StaffRole): Promise<void> {
  const state = loadState();
  const record = state === null ? await stubSession(context, who) : await boxSession(state, who);
  await context.addInitScript(plantSession, JSON.stringify(record));
}

/**
 * The box's own sign in budget, kept. The core admits ten exchanges a minute for one subject
 * (core/internal/identity/exchange/exchange.go, UserAllowance per LimitWindow), and the desk's
 * console build spends one on every page load (its own session recovery), so a suite that loads
 * the desk more than ten times a minute is refused with 429 for no product reason. Before each
 * load, asStaff waits until the last minute holds room for what it is about to spend. The times
 * live in the run's state directory, so a worker that restarts after a failure keeps the count.
 * Nine, not ten: one short of the core's allowance, so a retry by the desk itself still fits.
 */
const EXCHANGE_ALLOWANCE = 9;
const EXCHANGE_WINDOW_MS = 60_000;

async function paceExchanges(spend: number): Promise<void> {
  const file = join(stateDir(), "exchanges.json");
  let times: number[] = [];
  try {
    const parsed: unknown = JSON.parse(readFileSync(file, "utf8"));
    if (Array.isArray(parsed)) times = parsed.filter((t): t is number => typeof t === "number");
  } catch {
    // No exchange yet in this run.
  }
  for (;;) {
    const now = Date.now();
    times = times.filter((t) => now - t < EXCHANGE_WINDOW_MS);
    if (times.length + spend <= EXCHANGE_ALLOWANCE) break;
    const oldest = Math.min(...times);
    await new Promise((resolve) => setTimeout(resolve, EXCHANGE_WINDOW_MS - (now - oldest) + 250));
  }
  const at = Date.now();
  for (let i = 0; i < spend; i += 1) times.push(at);
  writeFileSync(file, JSON.stringify(times), { mode: 0o600 });
}

/**
 * The core's own base URL: the box's, when this run has a box, and the stub's
 * under the stub. A spec that reads the core's API directly reads it here.
 */
export function coreURL(): string {
  const state = loadState();
  if (state !== null) return state.boxUrl;
  return stubURL();
}

/**
 * Runs in the page before any script: the session on the context. The key is
 * written out here because the browser gets this function's source alone, with
 * no module scope behind it.
 */
function plantSession(raw: string): void {
  try {
    window.sessionStorage.setItem("hh-demo.front-door.session.v1", raw);
  } catch {
    // A frame with no storage of its own (about:blank) has nowhere to keep it.
  }
}

/**
 * The run's one exchange. The core mints the staff session once for the run's
 * subject: every context that asks for that role carries that same core
 * minted session, so a suite spends one exchange on the core's budget rather
 * than one per leg (the core admits ten exchanges a minute for one subject).
 */
let boxSessionCache: { readonly key: string; readonly record: SessionRecord } | null = null;

async function boxSession(state: AcceptanceState, who: StaffRole): Promise<SessionRecord> {
  if (state.roleCode !== who) {
    throw new Error(
      `asStaff: the box does not provision the role ${who}: it provisions only ${state.roleName} (${state.roleCode}) today.`,
    );
  }
  const key = `${state.subject}:${who}`;
  const cached = boxSessionCache;
  if (cached !== null && cached.key === key && cached.record.tokenExpiresAtMs > Date.now() + 60_000) {
    // The desk's own load is the one exchange this call spends.
    await paceExchanges(1);
    return cached.record;
  }
  // This call's own exchange, and the desk's load after it.
  await paceExchanges(2);
  const answer = await exchange(state);
  if (answer.user.role_code !== who) {
    throw new Error(`asStaff: the core answered the exchange with the role ${answer.user.role_code}, not ${who}.`);
  }
  const record: SessionRecord = {
    token: answer.token,
    tokenExpiresAtMs: answer.expiresAtMs,
    flow: FLOW_BY_ROLE[who],
    roleLabel: answer.user.role_name,
    user: { userId: answer.user.user_id, email: answer.user.email, name: answer.user.display_name },
    roleCode: answer.user.role_code,
    roleName: answer.user.role_name,
    tier: answer.user.tier,
    scope: answer.user.scope,
  };
  boxSessionCache = { key, record };
  return record;
}

/** The core's own exchange answer: the staff session it minted itself. */
interface ExchangeAnswer {
  readonly token: string;
  readonly expiresAtMs: number;
  readonly user: StaffUser;
}

async function exchange(state: AcceptanceState): Promise<ExchangeAnswer> {
  // The Console JWT, minted by the console the way the desk's console build
  // asks for it (POST /account/jwts on the console's own endpoint).
  const minted = await fetch(`${state.consoleEndpoint}/account/jwts`, {
    method: "POST",
    headers: { "X-Appwrite-Project": CONSOLE_PROJECT },
  });
  if (!minted.ok) {
    throw new Error(`asStaff: the console did not mint a Console JWT: ${minted.status}.`);
  }
  const consoleJwt = ((await minted.json()) as { jwt?: string }).jwt ?? "";
  if (consoleJwt === "") throw new Error("asStaff: the console answered no Console JWT.");
  // The core's own exchange. It verifies the Console JWT against the console,
  // resolves the staff user and its winning grant from the database, and
  // mints the staff session with the box's key. The token in the answer is
  // the core's own, never one signed here.
  const answered = await fetch(`${state.boxUrl}/api/v1/auth/exchange`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ jwt: consoleJwt, org: state.org }),
  });
  if (!answered.ok) {
    throw new Error(`asStaff: the core refused the exchange: ${answered.status} ${await answered.text()}`);
  }
  return (await answered.json()) as ExchangeAnswer;
}

/** The stub's own mint: the front door's demo mint, the way the stub leg signs in. */
async function stubSession(context: BrowserContext, who: StaffRole): Promise<SessionRecord> {
  const flow = FLOW_BY_ROLE[who];
  const minted = await context.request.post("/api/v1/demo/session", { data: { flow } });
  if (!minted.ok()) {
    throw new Error(
      `asStaff: the world names no ${who} to sign in as: the front door's demo mint refused the ${flow} flow (${minted.status()}).`,
    );
  }
  const answer = (await minted.json()) as { token?: string; user?: StaffUser };
  const user = answer.user;
  if (answer.token === undefined || user === undefined) {
    throw new Error(`asStaff: the front door's demo mint answered no session for ${who}.`);
  }
  return {
    token: answer.token,
    tokenExpiresAtMs: expiryFromToken(answer.token),
    flow,
    roleLabel: user.role_name,
    user: { userId: user.user_id, email: user.email, name: user.display_name },
    roleCode: user.role_code,
    roleName: user.role_name,
    tier: user.tier,
    scope: user.scope,
  };
}

/** The token's own expiry, when it carries one; an hour otherwise. */
function expiryFromToken(token: string): number {
  const claims = claimsOf(token);
  const exp = claims?.["exp"];
  return typeof exp === "number" ? exp * 1000 : Date.now() + 60 * 60 * 1000;
}

function claimsOf(token: string): Record<string, unknown> | null {
  const parts = token.split(".");
  if (parts.length < 2) return null;
  try {
    const parsed: unknown = JSON.parse(Buffer.from(parts[1] ?? "", "base64url").toString("utf8"));
    return typeof parsed === "object" && parsed !== null ? (parsed as Record<string, unknown>) : null;
  } catch {
    return null;
  }
}

/**
 * The stub's base URL under the stub: the app's default config serves the
 * stub beside the dev server and picks the port by the slot hashed from the
 * app directory, one past the dev server's own port.
 */
function stubURL(): string {
  const named = process.env.MD_STUB_URL;
  if (named !== undefined && named !== "") return named;
  const appDir = fileURLToPath(new URL("../../", import.meta.url));
  const port = process.env.ROLE_STUB_PORT ?? String(20000 + portSlot(appDir) + 1);
  return `http://127.0.0.1:${port}`;
}

/** The same FNV-1a slot the app's default config hashes (web/apps/money-desk/playwright.config.ts). */
function portSlot(text: string): number {
  let hash = 2166136261;
  for (let index = 0; index < text.length; index += 1) {
    hash ^= text.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0) % 2048;
}

// ── the setup CLI: box.sh runs this once per `up` to write the run's state ──
//
//   node --experimental-strip-types session.ts setup --box-port N --console-port N --org slug
//
// The issuer and the audiences are this run's throwaway names; the subject
// is a fresh uuid, the platform user id the box links the acceptance tester
// to. Nothing here names a real person or system.
function setupMain(argv: string[]): void {
  const arg = (name: string): string => {
    const at = argv.indexOf(name);
    if (at < 0 || at + 1 >= argv.length) throw new Error(`setup: ${name} is required`);
    return argv[at + 1];
  };
  const boxPort = arg("--box-port");
  const consolePort = arg("--console-port");
  const org = arg("--org");
  const tester = acceptanceTester();
  const key = generateThrowawayKey();
  writeKeyFile(key);
  const state: AcceptanceState = {
    boxUrl: `http://127.0.0.1:${boxPort}`,
    consoleEndpoint: `http://127.0.0.1:${consolePort}`,
    org,
    issuer: process.env.MDREAL_ISSUER ?? "hh-acceptance-real",
    staffAudience: process.env.MDREAL_STAFF_AUDIENCE ?? "hh-staff",
    // The workflow generates the subject per run and masks it; a developer
    // run draws a fresh uuid.
    subject: process.env.MDREAL_SUBJECT ?? randomUUID(),
    ...tester,
  };
  // The private key stays in its own file (keys/session.pem), never here.
  writeFileSync(statePath(), `${JSON.stringify(state, null, 2)}\n`, { mode: 0o600 });
}

const invokedAs = process.argv[1] === undefined ? "" : pathToFileURL(process.argv[1]).href;
if (import.meta.url === invokedAs && process.argv[2] === "setup") {
  setupMain(process.argv.slice(3));
}
