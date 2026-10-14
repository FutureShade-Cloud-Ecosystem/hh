// Ending a session, the one place Switch role's custody work lives. Both keys
// of the front door handoff are cleared, and the Console session is signed out
// whenever the host holds a Console session object, NOT only when the desk's
// session says `console`: a person the host restored from the front door's
// key always reads as a persona (handoff.ts), even when the record was a
// Console session's (the Counter Till keeps that key for the life of its
// frame), and their custody on the Rust side must not outlive them. A build
// with no Console configuration answers `unavailable`, which is harmless.
import type { StorageLike } from "@hardscapehouse/auth";
import { clearHandoff } from "./handoff.js";
import type { ConsoleSession } from "./console-session.js";

export async function endSession(deps: {
  consoleSession: Pick<ConsoleSession, "signOut"> | null;
  storage?: StorageLike;
}): Promise<void> {
  clearHandoff(deps.storage === undefined ? undefined : { storage: deps.storage });
  if (deps.consoleSession !== null) await deps.consoleSession.signOut();
}
