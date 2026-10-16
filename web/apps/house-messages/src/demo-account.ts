// The front door's handoff, read on this app's side (HH-DEMO-1 adopt).
// The front door mints the demo session and holds it in its own key in the
// tab's session storage; when the visitor opens a module tile this app boots
// and lands signed in as that role, with no second picker.
//
// THE TWO KEYS, AND WHY THIS APP READS ONLY ONE. The front door writes the
// minted session whole, token included, under its own key
// "hh-demo.front-door.session.v1", and the display profile alone under the
// custody's mirror key "hh-auth.session.v2". The auth package's custody keeps
// every token in private memory and NEVER reads a credential back from storage:
// the mirror carries the profile alone, and a payload it cannot validate it
// drops. So the handoff's one usable artifact is the front door's own key,
// and the seam is exactly three moves, all this app's: read that key here,
// hand the session to AuthController.adopt (custody takes the token into
// private memory and rewrites the display mirror itself), and drop the key
// in the same step. Nothing in the auth package changes, and nothing here
// writes a token anywhere.
//
// THE STORAGE TOUCH RIDES THE ONE SANCTIONED HELPER. The app's committed
// sink gate (test/sinks.test.mjs) keeps every word of a storage accessor out
// of this app's sources: the credential is memory and nothing else. The read
// and the drop therefore go through the auth package's ThrowSafeMirror over
// the front door's key.
import { ThrowSafeMirror, type StorageLike } from "@hardscapehouse/auth";

/** The front door's own session record key. */
export const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";

/** The front door's validated record, in the shape this app's boot takes. */
export interface FrontDoorHandoff {
  readonly token: string;
  readonly tokenExpiresAtMs: number;
  readonly flow: string;
  readonly roleLabel: string;
  readonly user: { userId: string; email: string; name: string };
  readonly roleCode: string;
  readonly roleName: string;
  readonly tier: number;
  readonly scope: string;
}

export type FrontDoorHandoffRead =
  | { kind: "absent" }
  | { kind: "unusable" }
  | { kind: "ready"; session: FrontDoorHandoff };

/**
 * Read the front door's key without touching it: absent when there is no
 * readable record, unusable when a record fails a read, ready with the
 * validated session otherwise. The injected storage double is for tests;
 * without one the mirror resolves the tab's own session storage.
 */
export function readFrontDoorHandoff(options?: {
  storage?: StorageLike;
  now?: () => number;
}): FrontDoorHandoffRead {
  const mirror = new ThrowSafeMirror(FRONT_DOOR_SESSION_KEY, options?.storage);
  const parsed = mirror.read();
  if (parsed === undefined) return { kind: "absent" };
  const now = options?.now ?? Date.now;
  const session = parseRecord(parsed, now());
  return session === null ? { kind: "unusable" } : { kind: "ready", session };
}

/** Drop the front door's key. Never throws. */
export function clearFrontDoorHandoff(options?: { storage?: StorageLike }): void {
  new ThrowSafeMirror(FRONT_DOOR_SESSION_KEY, options?.storage).clear();
}

function parseRecord(raw: unknown, now: number): FrontDoorHandoff | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  if (typeof record.token !== "string") return null;
  if (typeof record.tokenExpiresAtMs !== "number") return null;
  if (record.tokenExpiresAtMs < now) return null;
  if (typeof record.flow !== "string") return null;
  if (typeof record.roleLabel !== "string") return null;
  const user = record.user;
  if (typeof user !== "object" || user === null) return null;
  const u = user as Record<string, unknown>;
  if (typeof u.userId !== "string") return null;
  if (typeof u.email !== "string") return null;
  if (typeof u.name !== "string") return null;
  if (typeof record.roleCode !== "string") return null;
  if (typeof record.roleName !== "string") return null;
  if (typeof record.tier !== "number") return null;
  if (typeof record.scope !== "string") return null;
  return {
    token: record.token,
    tokenExpiresAtMs: record.tokenExpiresAtMs,
    flow: record.flow,
    roleLabel: record.roleLabel,
    user: { userId: u.userId, email: u.email, name: u.name },
    roleCode: record.roleCode,
    roleName: record.roleName,
    tier: record.tier,
    scope: record.scope,
  };
}
