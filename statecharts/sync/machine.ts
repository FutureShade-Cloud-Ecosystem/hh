// The device-local sync statechart: a pure transition table. No imports, no
// clock, no I/O: the runtime that drives it (web/packages/offline-store, later
// a Go port) performs the effects it names and feeds the outcomes back as
// events (context section 2, standing decision 6).
//
//   offline      no network; nothing is sent, the outbox only grows
//   pushing      the outbox is replaying, in order
//   pulling      the scopes are being pulled, after a push
//   idle         at rest, signed in, online
//   conflict     at rest, and conflict cards wait for a person
//   auth_expired the session is gone; the outbox is parked, nothing is sent
//
// A cycle is push, then pull: the device's own writes land before the world is
// read back, so the pull shows them. A fault ends the cycle at rest; the
// shell's timer or the user asks again (SYNC_REQUESTED), and the retry sends
// the whole outbox again, where the server dedups on the keys.

export const SYNC_STATES = ["offline", "idle", "pushing", "pulling", "conflict", "auth_expired"] as const;
export type SyncState = (typeof SYNC_STATES)[number];

export type SyncEffect = "push" | "pull";

export type SyncEvent =
  | { type: "ONLINE" }
  | { type: "OFFLINE" }
  | { type: "SYNC_REQUESTED" }
  | { type: "PUSH_DONE" }
  | { type: "PUSH_FAULT"; openCards: boolean }
  | { type: "PULL_DONE"; openCards: boolean }
  | { type: "PULL_FAULT"; openCards: boolean }
  | { type: "NETWORK_LOST" }
  | { type: "AUTH_EXPIRED" }
  | { type: "SESSION_REFRESHED"; online: boolean }
  | { type: "CARDS_CLEARED" };

export const EVENT_TYPES: readonly SyncEvent["type"][] = [
  "ONLINE",
  "OFFLINE",
  "SYNC_REQUESTED",
  "PUSH_DONE",
  "PUSH_FAULT",
  "PULL_DONE",
  "PULL_FAULT",
  "NETWORK_LOST",
  "AUTH_EXPIRED",
  "SESSION_REFRESHED",
  "CARDS_CLEARED",
];

export const INITIAL: SyncState = "offline";

export interface Transition {
  state: SyncState;
  effects: SyncEffect[];
}

const go = (state: SyncState, ...effects: SyncEffect[]): Transition => ({ state, effects });

/** At rest after a cycle: conflict while cards wait, idle otherwise. */
const rest = (openCards: boolean): Transition => go(openCards ? "conflict" : "idle");

/**
 * The next state and the effects to perform. Total: a pair the table does not
 * name, an unknown event or an unknown state, is ignored (same state, no
 * effects), which fails closed because no effect ever starts from an ignore.
 */
export function transition(state: SyncState, event: SyncEvent): Transition {
  switch (state) {
    case "offline":
      if (event.type === "ONLINE") return go("pushing", "push");
      if (event.type === "AUTH_EXPIRED") return go("auth_expired");
      break;
    case "idle":
      if (event.type === "SYNC_REQUESTED") return go("pushing", "push");
      if (event.type === "OFFLINE" || event.type === "NETWORK_LOST") return go("offline");
      if (event.type === "AUTH_EXPIRED") return go("auth_expired");
      break;
    case "pushing":
      if (event.type === "PUSH_DONE") return go("pulling", "pull");
      if (event.type === "PUSH_FAULT") return rest(event.openCards);
      if (event.type === "OFFLINE" || event.type === "NETWORK_LOST") return go("offline");
      if (event.type === "AUTH_EXPIRED") return go("auth_expired");
      break;
    case "pulling":
      if (event.type === "PULL_DONE" || event.type === "PULL_FAULT") return rest(event.openCards);
      if (event.type === "OFFLINE" || event.type === "NETWORK_LOST") return go("offline");
      if (event.type === "AUTH_EXPIRED") return go("auth_expired");
      break;
    case "conflict":
      if (event.type === "SYNC_REQUESTED") return go("pushing", "push");
      if (event.type === "CARDS_CLEARED") return go("idle");
      if (event.type === "OFFLINE" || event.type === "NETWORK_LOST") return go("offline");
      if (event.type === "AUTH_EXPIRED") return go("auth_expired");
      break;
    case "auth_expired":
      // Parked: only a refreshed session leaves, and nothing else starts work.
      if (event.type === "SESSION_REFRESHED") return event.online ? go("pushing", "push") : go("offline");
      break;
  }
  return go(state);
}
