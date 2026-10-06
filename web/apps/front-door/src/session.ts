// The front door's session custody: the demo mint, validated and held, and
// the two sessionStorage keys the D2 brief's handoff rule names.
//
// WHAT THE MINT IS. A tap on a live role card asks POST /api/v1/demo/session
// with the flow id and nothing else; the demo issuer answers a token and the
// staff user block (core/api/openapi.yaml, DemoSessionResponse). Everything
// is validated, never cast, exactly as the till's demo-account.ts reads the
// same answer: the user block field by field, and the token's expiry from its
// own exp claim, because the mint response carries no expiry field. A mint
// that lacks a token, a usable user block, or a readable unexpired expiry is
// refused as data.
//
// THE TWO KEYS, AND WHY THERE ARE TWO. The brief's handoff rule reads "store
// the token in sessionStorage under the same key the till uses". Read in the
// till's own sources, that rule meets this fact: the till's custody
// (@hardscapehouse/auth, session.ts) keeps every token in private memory and
// NEVER writes a credential to any storage; its one sessionStorage key
// (SESSION_MIRROR_KEY, "hh-auth.session.v2") carries the display profile
// alone, in a payload the till's custody validates field by field and DROPS
// from storage the moment it does not parse. Writing a raw token under that
// key would therefore buy no handoff: the till would refuse the payload and
// clear the key on its first load. So the brief's rule is carried in the two
// halves the till's own code makes possible:
//
//   1. SESSION_MIRROR_KEY carries the mint's display profile, in the mirror's
//      own v2 payload shape (nothing credential-shaped, exactly the fields
//      the till's custody writes there itself). The front door and the till
//      hold the same key for the same kind of record, and the till's custody
//      reads a familiar profile instead of tripping its refuse-and-clear
//      path.
//   2. The front door's own key, "hh-demo.front-door.session.v1", carries the
//      minted session whole, token included: the brief's "store the token"
//      and the artifact a later lane can read to hand one session across the
//      box's apps. Sign out and Switch role clear both keys.
//
// The record dies with the tab either way: sessionStorage is per tab, so a
// new visitor starts at the picker, and the box's demo data is invented.
//
// The storage accessor is injectable and probed before use, the auth package's
// own throw-safe discipline (browsers set to deny site data make the accessor
// itself throw): a hostile context degrades to memory, the session then lives
// for this document only, and nothing throws.
import { SESSION_MIRROR_KEY } from "@hardscapehouse/auth";
import type { DemoSessionResponse } from "@hardscapehouse/api-client";

/** The three storage operations the custody needs, so tests can inject doubles. */
export interface StorageLike {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

/** The front door's own session record key: the mint, token included. */
export const FRONT_DOOR_SESSION_KEY = "hh-demo.front-door.session.v1";

export { SESSION_MIRROR_KEY };

/** The validated mint, in the shape the front door works with. */
export interface FrontDoorSession {
  readonly token: string;
  readonly tokenExpiresAtMs: number;
  /** The demo flow id the mint was asked for ("sales", "sales-manager"). */
  readonly flow: string;
  /** The role label on the card that minted, the chrome's word for the role. */
  readonly roleLabel: string;
  readonly user: { userId: string; email: string; name: string };
  readonly roleCode: string;
  readonly roleName: string;
  readonly tier: number;
  readonly scope: string;
}

/** The mirror's own v2 payload, field for field (the auth package's shape). */
interface MirrorPayload {
  readonly v: 2;
  readonly userId: string;
  readonly email: string;
  readonly name: string;
}

const PROBE_KEY = "hh-front-door.storage-probe";

function memoryStorage(): StorageLike {
  const mem = new Map<string, string>();
  return {
    getItem: (key) => (mem.has(key) ? (mem.get(key) as string) : null),
    setItem: (key, value) => {
      mem.set(key, value);
    },
    removeItem: (key) => {
      mem.delete(key);
    },
  };
}

// Some browsers throw on ACCESS of the storage object, others on use, so the
// probe touches all three operations before the accessor is trusted (the auth
// package's discipline, shared with the ERP shim's recorded finding).
function probe(accessor: StorageLike): boolean {
  try {
    accessor.getItem(PROBE_KEY);
    accessor.setItem(PROBE_KEY, "1");
    accessor.removeItem(PROBE_KEY);
    return true;
  } catch {
    return false;
  }
}

function browserSessionStorage(): StorageLike | null {
  try {
    const candidate = (globalThis as { sessionStorage?: StorageLike }).sessionStorage;
    if (candidate !== undefined && probe(candidate)) return candidate;
  } catch {
    // denied site data: the caller keeps the memory backing
  }
  return null;
}

/**
 * The mint's user block, read field by field, never cast: the staff login
 * user block (IdentityUser) the front door renders from.
 */
function userFromMint(raw: unknown): {
  user: FrontDoorSession["user"];
  roleCode: string;
  roleName: string;
  tier: number;
  scope: string;
} | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const userId = record["user_id"];
  const email = record["email"];
  const name = record["display_name"];
  const roleCode = record["role_code"];
  const roleName = record["role_name"];
  const tier = record["tier"];
  const scope = record["scope"];
  if (typeof userId !== "string" || userId === "") return null;
  if (typeof email !== "string") return null;
  if (typeof name !== "string") return null;
  if (typeof roleCode !== "string" || roleCode === "") return null;
  if (typeof roleName !== "string") return null;
  if (typeof tier !== "number" || !Number.isInteger(tier)) return null;
  if (typeof scope !== "string" || scope === "") return null;
  return { user: { userId, email, name }, roleCode, roleName, tier, scope };
}

/**
 * The expiry is the token's own exp claim, the one source (the mint response
 * carries no expiry field): a JSON Web Token's middle segment, decoded. A
 * token that is not JWT shaped, or whose exp is missing, unreadable or past,
 * refuses.
 */
function expiryFromToken(token: string, now: number): number | null {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  let payload: unknown;
  try {
    payload = JSON.parse(decodeBase64Url(parts[1]));
  } catch {
    return null;
  }
  if (payload === null || typeof payload !== "object" || Array.isArray(payload)) return null;
  const exp = (payload as Record<string, unknown>)["exp"];
  if (typeof exp !== "number" || !Number.isFinite(exp)) return null;
  const expiresAtMs = exp * 1000;
  return expiresAtMs > now ? expiresAtMs : null;
}

function decodeBase64Url(segment: string): string {
  const padded = segment.replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(padded + "=".repeat((4 - (padded.length % 4)) % 4));
  const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
  return new TextDecoder().decode(bytes);
}

/**
 * The mint answer, validated whole: a token, a usable user block, a readable
 * unexpired expiry, and the flow and card label the caller asked with. Any
 * answer that fails a read refuses as null, like every other refusal in this
 * app.
 */
export function validateMint(
  response: DemoSessionResponse,
  flow: string,
  roleLabel: string,
  now: number,
): FrontDoorSession | null {
  const token = response.token;
  if (typeof token !== "string" || token === "") return null;
  const minted = userFromMint(response.user);
  if (minted === null) return null;
  const tokenExpiresAtMs = expiryFromToken(token, now);
  if (tokenExpiresAtMs === null) return null;
  return {
    token,
    tokenExpiresAtMs,
    flow,
    roleLabel,
    user: minted.user,
    roleCode: minted.roleCode,
    roleName: minted.roleName,
    tier: minted.tier,
    scope: minted.scope,
  };
}

/**
 * Reads a stored record back, validated field by field: a hand-edited or
 * corrupt entry reads as absent, never as a session with fields it does not
 * carry (the auth package's parse discipline). An expired record reads as
 * absent too: a stale mint is not a session.
 */
function parseRecord(raw: unknown, now: number): FrontDoorSession | null {
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

/**
 * The custody: the minted session in this tab, mirrored to the two keys, or
 * memory only when storage refuses. adopt writes, current reads, clear wipes
 * both keys; nothing here throws on a hostile storage accessor.
 */
export class FrontDoorSessionStore {
  readonly #backing: StorageLike;
  readonly #degraded: boolean;
  readonly #now: () => number;
  #session: FrontDoorSession | null = null;

  constructor(options?: { storage?: StorageLike; now?: () => number }) {
    this.#now = options?.now ?? Date.now;
    if (options?.storage !== undefined) {
      this.#degraded = !probe(options.storage);
      this.#backing = this.#degraded ? memoryStorage() : options.storage;
    } else {
      const found = browserSessionStorage();
      this.#degraded = found === null;
      this.#backing = found ?? memoryStorage();
    }
  }

  /** True when storage could not be trusted: a reload restores nothing. */
  get degraded(): boolean {
    return this.#degraded;
  }

  /** Hold a validated mint: the record and the profile mirror go to storage. */
  adopt(session: FrontDoorSession): void {
    this.#session = session;
    this.#writeKey(FRONT_DOOR_SESSION_KEY, session);
    const mirror: MirrorPayload = {
      v: 2,
      userId: session.user.userId,
      email: session.user.email,
      name: session.user.name,
    };
    this.#writeKey(SESSION_MIRROR_KEY, mirror);
  }

  /** The live session, or null when there is none or the token has expired. */
  current(): FrontDoorSession | null {
    if (this.#session !== null) {
      return this.#now() < this.#session.tokenExpiresAtMs ? this.#session : null;
    }
    if (this.#degraded) return null;
    return parseRecord(this.#readKey(FRONT_DOOR_SESSION_KEY), this.#now());
  }

  /** Wipe custody: memory and both keys. Sign out and Switch role call this. */
  clear(): void {
    this.#session = null;
    for (const key of [FRONT_DOOR_SESSION_KEY, SESSION_MIRROR_KEY]) {
      try {
        this.#backing.removeItem(key);
      } catch {
        // the key was never written, or the accessor is gone
      }
    }
  }

  #readKey(key: string): unknown {
    try {
      const raw = this.#backing.getItem(key);
      if (raw === null) return undefined;
      return JSON.parse(raw) as unknown;
    } catch {
      return undefined;
    }
  }

  #writeKey(key: string, value: unknown): void {
    let raw: string;
    try {
      raw = JSON.stringify(value);
    } catch {
      return;
    }
    try {
      this.#backing.setItem(key, raw);
    } catch {
      // quota or a blocked accessor: the session lives in memory only
    }
  }
}
