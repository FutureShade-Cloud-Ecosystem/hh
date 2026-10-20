// The session branch key: one narrow, per tab sessionStorage key the front door writes with the branch ids the
// person chose, and each desk's branch resolution reads. It holds ids only. A reader returns only ids that are
// also in the caller's granted list (the core's branches route, fenced to the caller's own grants), so a
// tampered, stale or forged key can never widen what the person reaches; the core still fences every read.

export const SESSION_BRANCHES_KEY = "hh.session.branches.v1";

/** The part of Storage this module uses, so a test can hand it a plain object. */
export interface BranchKeyStore {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
}

export type SessionBranchDecision = { kind: "none" } | { kind: "one"; id: string } | { kind: "ask" };

function tabStore(): BranchKeyStore | undefined {
  try {
    return globalThis.sessionStorage;
  } catch {
    return undefined;
  }
}

/** Write the chosen ids. A store that refuses leaves the key unwritten: the desk then asks. */
export function writeSessionBranchIds(ids: readonly string[], store: BranchKeyStore | undefined = tabStore()): void {
  if (store === undefined) return;
  try {
    store.setItem(SESSION_BRANCHES_KEY, JSON.stringify({ ids: [...ids] }));
  } catch {
    // The choice is only a convenience; the desk asks again when the key is not there.
  }
}

/** The chosen ids that the core also granted, once each, in the order written. Anything else is dropped. */
export function readSessionBranchIds(
  granted: readonly { readonly id: string }[],
  store: BranchKeyStore | undefined = tabStore(),
): string[] {
  if (store === undefined) return [];
  let raw: string | null;
  try {
    raw = store.getItem(SESSION_BRANCHES_KEY);
  } catch {
    return [];
  }
  if (raw === null) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return [];
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return [];
  const ids = (parsed as { ids?: unknown }).ids;
  if (!Array.isArray(ids)) return [];
  const grantedIds = new Set(granted.map((row) => row.id));
  const seen = new Set<string>();
  for (const id of ids) {
    if (typeof id === "string" && grantedIds.has(id)) seen.add(id);
  }
  return [...seen];
}

/**
 * What a desk does with the granted list: none granted is a refusal, one granted is taken as it is, and with
 * several granted exactly one chosen and granted id is used; several chosen, or none, means ask once inside.
 */
export function decideSessionBranch(
  granted: readonly { readonly id: string }[],
  store: BranchKeyStore | undefined = tabStore(),
): SessionBranchDecision {
  const first = granted[0];
  if (first === undefined) return { kind: "none" };
  if (granted.length === 1) return { kind: "one", id: first.id };
  const chosen = readSessionBranchIds(granted, store);
  const only = chosen[0];
  return chosen.length === 1 && only !== undefined ? { kind: "one", id: only } : { kind: "ask" };
}
