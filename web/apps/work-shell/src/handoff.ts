// The handoff between the host, the front door and the desk apps.
//
// The front door mints a demo session and holds it whole under its own key in
// the tab's session storage (FRONT_DOOR_SESSION_KEY), the display profile
// alone under the auth custody's mirror key. A desk app boots, reads the front
// door's key ONCE, adopts the session into private memory and DROPS the key
// (see web/apps/manager-home/src/demo-account.ts). So the host reads the key
// when the person signs in, keeps the session in memory, and writes the key
// again right before it loads each app. Every storage touch rides the auth
// package's throw-safe mirror; a hostile storage degrades to nothing, never
// to a throw.
import { SESSION_MIRROR_KEY, ThrowSafeMirror, type StorageLike } from "@hardscapehouse/auth";
import type { HostSession } from "./host-state.js";

/** The front door's own session record key. */
export const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";

interface Options {
  /** A storage double for tests; the tab's session storage when omitted. */
  storage?: StorageLike;
  now?: () => number;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

/** The front door's record, read field by field. An expired or malformed record is no session. */
function parseRecord(raw: unknown, now: number): HostSession | null {
  if (!isRecord(raw)) return null;
  const { token, tokenExpiresAtMs, flow, roleLabel, roleCode, roleName, tier, scope, user } = raw;
  if (typeof token !== "string" || token === "") return null;
  if (typeof tokenExpiresAtMs !== "number" || !Number.isFinite(tokenExpiresAtMs) || tokenExpiresAtMs <= now) return null;
  if (typeof flow !== "string" || flow === "") return null;
  if (typeof roleLabel !== "string" || roleLabel === "") return null;
  if (typeof roleCode !== "string" || roleCode === "") return null;
  if (typeof roleName !== "string" || roleName === "") return null;
  if (typeof tier !== "number" || !Number.isInteger(tier)) return null;
  if (typeof scope !== "string" || scope === "") return null;
  if (!isRecord(user)) return null;
  const { userId, email, name } = user;
  if (typeof userId !== "string" || userId === "") return null;
  if (typeof email !== "string" || typeof name !== "string") return null;
  return {
    source: "persona",
    userId,
    email,
    name,
    roleCode,
    roleLabel,
    roleName,
    token,
    tokenExpiresAtMs,
    flow,
    tier,
    scope,
  };
}

/** The session the front door minted, or null. Never throws. */
export function readDoorSession(options?: Options): HostSession | null {
  const mirror = new ThrowSafeMirror(FRONT_DOOR_SESSION_KEY, options?.storage);
  return parseRecord(mirror.read(), (options?.now ?? Date.now)());
}

/** Write the session where the next desk app will read it: the door's key whole, the mirror's key profile only. */
export function writeHandoff(session: HostSession, options?: Options): void {
  new ThrowSafeMirror(FRONT_DOOR_SESSION_KEY, options?.storage).write({
    token: session.token,
    tokenExpiresAtMs: session.tokenExpiresAtMs,
    flow: session.flow,
    roleLabel: session.roleLabel,
    user: { userId: session.userId, email: session.email, name: session.name },
    roleCode: session.roleCode,
    roleName: session.roleName,
    tier: session.tier,
    scope: session.scope,
  });
  new ThrowSafeMirror(SESSION_MIRROR_KEY, options?.storage).write({
    v: 2,
    userId: session.userId,
    email: session.email,
    name: session.name,
  });
}

/** Drop both keys. Sign out and Switch role call this. Never throws. */
export function clearHandoff(options?: Pick<Options, "storage">): void {
  new ThrowSafeMirror(FRONT_DOOR_SESSION_KEY, options?.storage).clear();
  new ThrowSafeMirror(SESSION_MIRROR_KEY, options?.storage).clear();
}
