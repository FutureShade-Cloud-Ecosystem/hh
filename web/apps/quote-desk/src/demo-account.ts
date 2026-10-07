// The front door's handoff, read on the Quote Desk's side (HH-DEMO-1, the
// Quote Desk's adopt, decision log rows 893, 899 and 906). The front door
// mints the demo session and holds it for the box in its own key in the
// tab's session storage; when the visitor opens a module tile the desk boots
// at this app and lands signed in as that role, with no second picker. The
// shape is the till's (web/apps/counter-till/src/demo-account.ts on
// demo/front-door 385cd8d6), mirrored field for field: one record, one
// validation, one drop.
//
// THE TWO KEYS, AND WHY THE DESK READS ONLY ONE. The front door writes the
// minted session whole, token included, under its own key
// "hh-demo.front-door.session.v1", and the display profile alone under the
// custody's mirror key "hh-auth.session.v2" (the auth package's
// SESSION_MIRROR_KEY). The desk's custody (@hardscapehouse/auth) keeps every
// token in private memory and NEVER reads a credential back from storage:
// the mirror carries the profile alone, and a payload it cannot validate it
// drops. So the handoff's one usable artifact is the front door's own key,
// and the seam is exactly three moves, all this app's: read that key here,
// hand the session to AuthController.adopt (custody takes the token into
// private memory and rewrites the display mirror itself), and drop the key
// in the same step. Nothing in the auth package changes, and nothing here
// writes a token anywhere: the key is read and removed, never echoed or
// logged.
//
// THE STORAGE TOUCH RIDES THE ONE SANCTIONED HELPER. This app's committed
// sink gate (test/sinks.test.mjs) keeps every word of a storage accessor out
// of this app's sources: the credential is memory and nothing else. The read
// and the drop therefore go through the auth package's ThrowSafeMirror over
// the front door's key, the same throw-safe, probed, degraded-aware accessor
// the custody's own mirror rides; where storage is blocked the mirror
// degrades, the read answers absent, and the entry page with the cards comes
// up, which is the desk's ordinary way in.
//
// Every read is validated field by field, never cast, exactly the front
// door's own parse discipline on the same record: a hand-edited, corrupt or
// expired entry reads as unusable, never as a session with fields it does
// not carry.
//
// The theme is untouched by this seam: it is a data-theme attribute on the
// document root, and the desk's own theme controller keeps cycling Auto,
// Light and Brand over an adopted session exactly as over a minted one.
import { ThrowSafeMirror, type StorageLike } from "@hardscapehouse/auth";

/** The front door's own session record key: the mint, token included. */
export const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";

/** The front door's validated record, in the shape the desk's boot takes. */
export interface FrontDoorHandoff {
  readonly token: string;
  readonly tokenExpiresAtMs: number;
  /** The demo flow id the mint was asked for ("sales", "sales-manager"). */
  readonly flow: string;
  /** The role label on the card the visitor tapped, the chrome's word. */
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
 * Read the front door's key without touching it: "absent" when there is no
 * readable record, "unusable" when a record is there but fails a read (a
 * field missing, or the token past its expiry), "ready" with the validated
 * session otherwise. The injected storage double is for the tests; without
 * one the mirror resolves the tab's own session storage. Nothing here
 * throws on a hostile or blocked accessor: the mirror degrades to memory,
 * which reads as absent.
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

/** Drop the front door's key. Never throws; the key was maybe never there. */
export function clearFrontDoorHandoff(options?: { storage?: StorageLike }): void {
  new ThrowSafeMirror(FRONT_DOOR_SESSION_KEY, options?.storage).clear();
}

/**
 * The record, read field by field: the same fields the front door wrote, in
 * the same validation the front door reads its own storage back with. A
 * record whose token has expired is unusable: a stale mint is not a session.
 */
function parseRecord(raw: unknown, now: number): FrontDoorHandoff | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const token = record["token"];
  const tokenExpiresAtMs = record["tokenExpiresAtMs"];
  const flow = record["flow"];
  const roleLabel = record["roleLabel"];
  const roleCode = record["roleCode"];
  const roleName = record["roleName"];
  const tier = record["tier"];
  const scope = record["scope"];
  if (typeof token !== "string" || token === "") return null;
  if (typeof tokenExpiresAtMs !== "number" || !Number.isFinite(tokenExpiresAtMs)) return null;
  if (typeof flow !== "string" || flow === "") return null;
  if (typeof roleLabel !== "string" || roleLabel === "") return null;
  if (typeof roleCode !== "string" || roleCode === "") return null;
  if (typeof roleName !== "string" || roleName === "") return null;
  if (typeof tier !== "number" || !Number.isInteger(tier)) return null;
  if (typeof scope !== "string" || scope === "") return null;
  const user = record["user"];
  if (user === null || typeof user !== "object" || Array.isArray(user)) return null;
  const userRecord = user as Record<string, unknown>;
  const userId = userRecord["userId"];
  const email = userRecord["email"];
  const name = userRecord["name"];
  if (typeof userId !== "string" || userId === "") return null;
  if (typeof email !== "string" || typeof name !== "string") return null;
  if (tokenExpiresAtMs <= now) return null;
  return {
    token,
    tokenExpiresAtMs,
    flow,
    roleLabel,
    roleCode,
    roleName,
    tier,
    scope,
    user: { userId, email, name },
  };
}
