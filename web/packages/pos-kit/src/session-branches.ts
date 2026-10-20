// The session branch key: one narrow, per tab key the front door writes with the branch ids the person chose, and
// each desk's branch resolution reads. It holds ids only. A reader returns only ids that are also in the caller's
// granted list (the core's branches route, fenced to the caller's own grants), so a tampered, stale or forged key
// can never widen what the person reaches; the core still fences every read. The key rides the auth package's
// throw-safe mirror, this kit's one sanctioned accessor for tab storage.
import { ThrowSafeMirror, type StorageLike } from "@hardscapehouse/auth";

export const SESSION_BRANCHES_KEY = "hh.session.branches.v1";

/** The storage seam of the mirror, so a test can hand it a double; omitted, the mirror uses this tab's own. */
export type BranchKeyStore = StorageLike;

export type SessionBranchDecision = { kind: "none" } | { kind: "one"; id: string } | { kind: "ask" };

/** Write the chosen ids. A tab that refuses the write leaves the key unwritten: the desk then asks. */
export function writeSessionBranchIds(ids: readonly string[], store?: BranchKeyStore): void {
  new ThrowSafeMirror(SESSION_BRANCHES_KEY, store).write({ ids: [...ids] });
}

/** The chosen ids that the core also granted, once each, in the order written. Anything else is dropped. */
export function readSessionBranchIds(granted: readonly { readonly id: string }[], store?: BranchKeyStore): string[] {
  const parsed = new ThrowSafeMirror(SESSION_BRANCHES_KEY, store).read();
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
export function decideSessionBranch(granted: readonly { readonly id: string }[], store?: BranchKeyStore): SessionBranchDecision {
  const first = granted[0];
  if (first === undefined) return { kind: "none" };
  if (granted.length === 1) return { kind: "one", id: first.id };
  const chosen = readSessionBranchIds(granted, store);
  const only = chosen[0];
  return chosen.length === 1 && only !== undefined ? { kind: "one", id: only } : { kind: "ask" };
}
