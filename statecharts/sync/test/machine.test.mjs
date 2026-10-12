// The sync statechart as a pure table: every (state, event) pair is checked
// against an independent literal of the intended transitions, then the graph
// properties the shells rely on are checked structurally.
import { test } from "node:test";
import assert from "node:assert/strict";
import { INITIAL, SYNC_STATES, EVENT_TYPES, transition } from "../machine.ts";

const events = {
  ONLINE: { type: "ONLINE" },
  OFFLINE: { type: "OFFLINE" },
  SYNC_REQUESTED: { type: "SYNC_REQUESTED" },
  PUSH_DONE: { type: "PUSH_DONE" },
  PUSH_FAULT: { type: "PUSH_FAULT" },
  "PULL_DONE/cards": { type: "PULL_DONE", openCards: true },
  "PULL_DONE/none": { type: "PULL_DONE", openCards: false },
  "PULL_FAULT/cards": { type: "PULL_FAULT", openCards: true },
  "PULL_FAULT/none": { type: "PULL_FAULT", openCards: false },
  NETWORK_LOST: { type: "NETWORK_LOST" },
  AUTH_EXPIRED: { type: "AUTH_EXPIRED" },
  "SESSION_REFRESHED/online": { type: "SESSION_REFRESHED", online: true },
  "SESSION_REFRESHED/offline": { type: "SESSION_REFRESHED", online: false },
  CARDS_CLEARED: { type: "CARDS_CLEARED" },
};

// Everything not listed is ignored: same state, no effects.
const intended = {
  offline: {
    ONLINE: ["pushing", ["push"]],
    AUTH_EXPIRED: ["auth_expired", []],
  },
  // A request that failed (NETWORK_LOST) is not the platform saying offline
  // (OFFLINE): it parks in unreachable, which a retry leaves on its own.
  unreachable: {
    ONLINE: ["pushing", ["push"]],
    SYNC_REQUESTED: ["pushing", ["push"]],
    OFFLINE: ["offline", []],
    AUTH_EXPIRED: ["auth_expired", []],
  },
  idle: {
    SYNC_REQUESTED: ["pushing", ["push"]],
    OFFLINE: ["offline", []],
    NETWORK_LOST: ["unreachable", []],
    AUTH_EXPIRED: ["auth_expired", []],
  },
  pushing: {
    PUSH_DONE: ["pulling", ["pull"]],
    // A push fault does not stop the pull: the device's reads must not go stale behind a stuck write.
    PUSH_FAULT: ["pulling", ["pull"]],
    OFFLINE: ["offline", []],
    NETWORK_LOST: ["unreachable", []],
    AUTH_EXPIRED: ["auth_expired", []],
  },
  pulling: {
    "PULL_DONE/cards": ["conflict", []],
    "PULL_DONE/none": ["idle", []],
    "PULL_FAULT/cards": ["conflict", []],
    "PULL_FAULT/none": ["idle", []],
    OFFLINE: ["offline", []],
    NETWORK_LOST: ["unreachable", []],
    AUTH_EXPIRED: ["auth_expired", []],
  },
  conflict: {
    SYNC_REQUESTED: ["pushing", ["push"]],
    CARDS_CLEARED: ["idle", []],
    OFFLINE: ["offline", []],
    NETWORK_LOST: ["unreachable", []],
    AUTH_EXPIRED: ["auth_expired", []],
  },
  auth_expired: {
    "SESSION_REFRESHED/online": ["pushing", ["push"]],
    "SESSION_REFRESHED/offline": ["offline", []],
  },
};

test("the states are the plan's six plus unreachable, and the machine starts offline", () => {
  assert.deepEqual([...SYNC_STATES].sort(), ["auth_expired", "conflict", "idle", "offline", "pulling", "pushing", "unreachable"]);
  assert.equal(INITIAL, "offline");
});

test("every event type the table is tested with is in the machine's vocabulary, and the reverse", () => {
  assert.deepEqual([...EVENT_TYPES].sort(), [...new Set(Object.values(events).map((e) => e.type))].sort());
});

test("every state and event pair does what the intended table says", () => {
  let pairs = 0;
  for (const state of SYNC_STATES) {
    for (const [name, event] of Object.entries(events)) {
      const want = intended[state][name] ?? [state, []];
      const got = transition(state, event);
      assert.deepEqual([got.state, [...got.effects]], want, `${state} on ${name}`);
      pairs++;
    }
  }
  assert.equal(pairs, SYNC_STATES.length * Object.keys(events).length);
});

test("the transition function is pure: the input state and event are not changed, and the same input gives the same answer", () => {
  const event = Object.freeze({ type: "PULL_DONE", openCards: true });
  const a = transition("pulling", event);
  const b = transition("pulling", event);
  assert.deepEqual(a, b);
  assert.notEqual(a.effects, b.effects, "effects arrays must not be shared between calls");
});

test("an unknown event, or an unknown state, changes nothing", () => {
  assert.deepEqual(transition("idle", { type: "NOT_AN_EVENT" }), { state: "idle", effects: [] });
  assert.deepEqual(transition("not_a_state", { type: "ONLINE" }), { state: "not_a_state", effects: [] });
});

test("every state is reachable from the start", () => {
  const seen = new Set([INITIAL]);
  const queue = [INITIAL];
  while (queue.length) {
    const s = queue.shift();
    for (const e of Object.values(events)) {
      const next = transition(s, e).state;
      if (!seen.has(next)) {
        seen.add(next);
        queue.push(next);
      }
    }
  }
  assert.deepEqual([...seen].sort(), [...SYNC_STATES].sort());
});

test("no state is a trap: from every state the start state is reachable again", () => {
  for (const from of SYNC_STATES) {
    const seen = new Set([from]);
    const queue = [from];
    while (queue.length) {
      const s = queue.shift();
      for (const e of Object.values(events)) {
        const next = transition(s, e).state;
        if (!seen.has(next)) {
          seen.add(next);
          queue.push(next);
        }
      }
    }
    assert.ok(seen.has(INITIAL), `${from} cannot get back to ${INITIAL}`);
  }
});

test("a push or a pull effect is requested only when entering pushing or pulling, and pairs with it", () => {
  for (const state of SYNC_STATES) {
    for (const event of Object.values(events)) {
      const t = transition(state, event);
      assert.ok(t.effects.length <= 1);
      for (const effect of t.effects) assert.equal(t.state, effect === "push" ? "pushing" : "pulling");
      if (t.state !== state && (t.state === "pushing" || t.state === "pulling")) assert.equal(t.effects.length, 1);
      if (t.state === state) assert.equal(t.effects.length, 0, "a self transition must not start work");
    }
  }
});

test("the only ways into pushing are a reconnect, a request, and a refreshed session", () => {
  const into = new Set();
  for (const state of SYNC_STATES) {
    for (const [name, event] of Object.entries(events)) {
      const t = transition(state, event);
      if (t.state === "pushing" && state !== "pushing") into.add(`${state} on ${event.type}`);
    }
  }
  assert.deepEqual([...into].sort(), [
    "auth_expired on SESSION_REFRESHED",
    "conflict on SYNC_REQUESTED",
    "idle on SYNC_REQUESTED",
    "offline on ONLINE",
    "unreachable on ONLINE",
    "unreachable on SYNC_REQUESTED",
  ]);
});

test("auth expired parks: nothing but a refreshed session leaves it, and it starts no work otherwise", () => {
  for (const [name, event] of Object.entries(events)) {
    const t = transition("auth_expired", event);
    if (event.type === "SESSION_REFRESHED") continue;
    assert.deepEqual(t, { state: "auth_expired", effects: [] }, name);
  }
});

test("offline never starts work except on reconnect", () => {
  for (const [name, event] of Object.entries(events)) {
    const t = transition("offline", event);
    if (event.type === "ONLINE") continue;
    assert.equal(t.effects.length, 0, name);
  }
});

test("a failed request leaves its state on a retry or a request, with no platform ONLINE event", () => {
  for (const state of ["idle", "pushing", "pulling", "conflict"]) {
    assert.equal(transition(state, events.NETWORK_LOST).state, "unreachable", state);
  }
  assert.deepEqual(transition("unreachable", events.SYNC_REQUESTED), { state: "pushing", effects: ["push"] });
  assert.equal(transition("offline", events.NETWORK_LOST).state, "offline");
  assert.deepEqual(transition("offline", events.SYNC_REQUESTED), { state: "offline", effects: [] }, "the platform said offline: a request must not try");
  assert.equal(transition("unreachable", events.OFFLINE).state, "offline", "the platform can still say offline");
});

test("a push fault runs the pull, so the reads do not go stale behind a stuck write", () => {
  assert.deepEqual(transition("pushing", events.PUSH_FAULT), { state: "pulling", effects: ["pull"] });
});
