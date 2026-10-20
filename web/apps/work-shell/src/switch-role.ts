// Ending a session, the one place Switch role's custody work lives. Both keys
// of the front door handoff are cleared, and the Console session is signed out
// whenever the host holds a Console session object, NOT only when the desk's
// session says `console`: a person the host restored from the front door's
// key always reads as a persona (handoff.ts), even when the record was a
// Console session's (the Counter Till keeps that key for the life of its
// frame), and their custody on the Rust side must not outlive them. A build
// with no Console configuration answers `unavailable`, which is harmless.
//
// The core session token is also revoked server side (HH-678): endSession
// posts it to /api/v1/auth/sign-out. A call that fails on the network (the
// box is offline) or meets a 5xx is held in a module level pending set, in
// memory only (never in storage, which the handoff keys' clearing owns),
// and flushed the next time the host comes back online.
import type { StorageLike } from "@hardscapehouse/auth";
import { clearHandoff } from "./handoff.js";
import type { ConsoleSession } from "./console-session.js";
import { readDoorSession } from "./handoff.js";

/** Tokens whose revocation post failed on the network or met a 5xx, held in memory until it can be retried. */
const pendingRevocations = new Set<string>();

function revokeURL(coreBaseUrl?: string): string {
  const base = coreBaseUrl ?? "";
  return `${base}/api/v1/auth/sign-out`;
}

async function postRevocation(token: string, options?: { fetch?: typeof fetch; coreBaseUrl?: string }): Promise<void> {
  const doFetch = options?.fetch ?? globalThis.fetch;
  if (typeof doFetch !== "function") return;
  let response: Response;
  try {
    response = await doFetch(revokeURL(options?.coreBaseUrl), {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ token }),
    });
  } catch {
    pendingRevocations.add(token);
    return;
  }
  if (response.status >= 500) {
    pendingRevocations.add(token);
    return;
  }
  // A 400 or 401 is a final answer on this token (malformed, forged, or
  // already dead); it is never retried.
}

/**
 * Retry every pending revocation. Called from endSession (which adds the
 * fresh token first) and whenever the host's connection comes back. Never
 * throws: a still-offline retry just stays pending.
 */
export async function flushPendingRevocations(options?: { fetch?: typeof fetch; coreBaseUrl?: string }): Promise<void> {
  const held = [...pendingRevocations];
  if (held.length === 0) return;
  for (const token of held) pendingRevocations.delete(token);
  for (const token of held) await postRevocation(token, options);
}

/** Test hook: drop every pending revocation without posting. */
export function clearPendingRevocationsForTest(): void {
  pendingRevocations.clear();
}

/** Test hook: the tokens currently held pending, in insertion order. */
export function pendingRevocationsForTest(): readonly string[] {
  return [...pendingRevocations];
}

export async function endSession(deps: {
  consoleSession: (Pick<ConsoleSession, "signOut"> & { currentToken?: () => string | null }) | null;
  storage?: StorageLike;
  token?: string | null;
  fetch?: typeof fetch;
  coreBaseUrl?: string;
}): Promise<void> {
  // Read the token BEFORE the handoff keys are cleared: after clearing,
  // the door record (and its token) is gone.
  const token = deps.token ?? readDoorSession({ storage: deps.storage })?.token ?? deps.consoleSession?.currentToken?.() ?? null;
  clearHandoff(deps.storage === undefined ? undefined : { storage: deps.storage });
  if (deps.consoleSession !== null) await deps.consoleSession.signOut();
  if (token !== null && token !== "") {
    await postRevocation(token, { fetch: deps.fetch, coreBaseUrl: deps.coreBaseUrl });
    await flushPendingRevocations({ fetch: deps.fetch, coreBaseUrl: deps.coreBaseUrl });
  }
}
