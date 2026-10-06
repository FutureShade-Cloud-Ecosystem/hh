// The front door's handoff, read on the till's side (HH-DEMO-1 D2 polish,
// item 6). The front door mints the demo session and holds it for the box in
// its own sessionStorage key; when the visitor opens a module tile the till
// boots at this app and lands signed in as that role, with no second picker.
//
// THE TWO KEYS, AND WHY THE TILL READS ONLY ONE. The front door writes the
// minted session whole, token included, under its own key
// "hh-demo.front-door.session.v1", and the display profile alone under the
// till custody's mirror key "hh-auth.session.v2" (the auth package's
// SESSION_MIRROR_KEY). The till's custody (@hardscapehouse/auth, session.ts)
// keeps every token in private memory and NEVER reads a credential back from
// storage: the mirror carries the profile alone, and a payload it cannot
// validate it drops. So the handoff's one usable artifact is the front
// door's own key, and the seam is exactly three moves, all this app's:
// read that key here, hand the session to AuthController.adopt (custody
// takes the token into private memory and rewrites the mirror itself), and
// clear the key in the same step. Nothing in the auth package changes, and
// nothing here writes a token to storage at any point: the key is read and
// removed, never echoed or logged.
//
// Every read is validated field by field, never cast, exactly the front
// door's own parse discipline on the same record: a hand-edited, corrupt or
// expired entry reads as unusable, never as a session with fields it does
// not carry. The storage operations are throw-safe like every other storage
// touch in this workspace: a hostile or blocked accessor degrades to "no
// handoff" and the entry page with the cards comes up, which is the till's
// ordinary way in.
//
// The theme is untouched by this seam: it is a data-theme attribute on the
// document root, and the till's own theme controller keeps cycling Auto,
// Light and Dark over an adopted session exactly as over a minted one.

/** The front door's own session record key: the mint, token included. */
export const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";

/** The three storage operations the handoff needs, so tests can inject doubles. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** The front door's validated record, in the shape the till's boot takes. */
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
 * record, "unusable" when a record is there but fails a read (corrupt, a
 * field missing, or the token past its expiry), "ready" with the validated
 * session otherwise. Nothing here throws on a hostile storage accessor.
 */
export function readFrontDoorHandoff(storage: StorageLike, now: number): FrontDoorHandoffRead {
  let raw: string | null;
  try {
    raw = storage.getItem(FRONT_DOOR_SESSION_KEY);
  } catch {
    return { kind: "absent" };
  }
  if (raw === null) return { kind: "absent" };
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw) as unknown;
  } catch {
    return { kind: "unusable" };
  }
  const session = parseRecord(parsed, now);
  return session === null ? { kind: "unusable" } : { kind: "ready", session };
}

/** Drop the front door's key. Never throws; the key was maybe never there. */
export function clearFrontDoorHandoff(storage: StorageLike): void {
  try {
    storage.removeItem(FRONT_DOOR_SESSION_KEY);
  } catch {
    // the key was never written, or the accessor is gone
  }
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
