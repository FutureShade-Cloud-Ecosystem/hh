// The handoff between the host, the front door and the desk apps. The front
// door mints into its own session key; each desk app reads that key once and
// DROPS it (web/apps/manager-home/src/demo-account.ts), so the host holds the
// session in memory and writes the key again before every app it loads.
import { test } from "node:test";
import assert from "node:assert/strict";
import { SESSION_MIRROR_KEY } from "@hardscapehouse/auth";
import {
  FRONT_DOOR_SESSION_KEY,
  clearHandoff,
  readDoorSession,
  writeHandoff,
} from "../src/handoff.js";
import { NOW, doorRecord, fakeStorage } from "./support-data.mjs";

test("a session the front door minted reads back as the host's session", () => {
  const storage = fakeStorage();
  storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(doorRecord()));
  const read = readDoorSession({ storage, now: () => NOW });
  assert.ok(read !== null);
  assert.equal(read.source, "persona");
  assert.equal(read.userId, "demo-account-sales");
  assert.equal(read.roleCode, "inside_sales");
  assert.equal(read.roleLabel, "Sales");
  assert.equal(read.flow, "sales");
  assert.ok(read.token.length > 0);
});

test("nothing, a corrupt record, an expired token and a record missing a field all read as no session", () => {
  const empty = fakeStorage();
  assert.equal(readDoorSession({ storage: empty, now: () => NOW }), null);

  const corrupt = fakeStorage();
  corrupt.setItem(FRONT_DOOR_SESSION_KEY, "{not json");
  assert.equal(readDoorSession({ storage: corrupt, now: () => NOW }), null);

  const expired = fakeStorage();
  expired.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(doorRecord({ tokenExpiresAtMs: NOW - 1 })));
  assert.equal(readDoorSession({ storage: expired, now: () => NOW }), null);

  for (const field of ["token", "roleCode", "roleLabel", "user", "scope"]) {
    const partial = doorRecord();
    delete partial[field];
    const storage = fakeStorage();
    storage.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(partial));
    assert.equal(readDoorSession({ storage, now: () => NOW }), null, `missing ${field}`);
  }
});

test("writing the handoff puts the whole session under the door's key and the display profile alone under the mirror's", () => {
  const storage = fakeStorage();
  const read = (() => {
    const seed = fakeStorage();
    seed.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(doorRecord()));
    return readDoorSession({ storage: seed, now: () => NOW });
  })();
  writeHandoff(read, { storage });
  const record = JSON.parse(storage.getItem(FRONT_DOOR_SESSION_KEY));
  assert.deepEqual(record, doorRecord());
  const mirror = JSON.parse(storage.getItem(SESSION_MIRROR_KEY));
  assert.deepEqual(mirror, {
    v: 2,
    userId: "demo-account-sales",
    email: "sales@demo.invalid",
    name: "Sales (demo)",
  });
  assert.ok(!JSON.stringify(mirror).includes("token"));
});

test("an app that reads and drops the key leaves the host able to write it again for the next app", () => {
  const storage = fakeStorage();
  const seed = fakeStorage();
  seed.setItem(FRONT_DOOR_SESSION_KEY, JSON.stringify(doorRecord()));
  const session = readDoorSession({ storage: seed, now: () => NOW });
  writeHandoff(session, { storage });
  storage.removeItem(FRONT_DOOR_SESSION_KEY); // the first app adopted and dropped it
  assert.equal(readDoorSession({ storage, now: () => NOW }), null);
  writeHandoff(session, { storage });
  assert.equal(readDoorSession({ storage, now: () => NOW })?.userId, "demo-account-sales");
});

test("clearing the handoff removes both keys and nothing else", () => {
  const storage = fakeStorage();
  storage.setItem(FRONT_DOOR_SESSION_KEY, "x");
  storage.setItem(SESSION_MIRROR_KEY, "y");
  storage.setItem("hh-shell.pins.v1.someone", "pins");
  clearHandoff({ storage });
  assert.deepEqual(storage.keys(), ["hh-shell.pins.v1.someone"]);
});

test("a storage that throws on every use never throws out of the handoff", () => {
  const hostile = {
    getItem() {
      throw new Error("denied");
    },
    setItem() {
      throw new Error("denied");
    },
    removeItem() {
      throw new Error("denied");
    },
  };
  assert.equal(readDoorSession({ storage: hostile, now: () => NOW }), null);
  const session = {
    source: "persona",
    userId: "u",
    email: "e",
    name: "n",
    roleCode: "r",
    roleLabel: "R",
    roleName: "R",
    token: "t",
    tokenExpiresAtMs: NOW + 1,
    flow: "f",
    tier: 1,
    scope: "BRANCH",
  };
  assert.doesNotThrow(() => writeHandoff(session, { storage: hostile }));
  assert.doesNotThrow(() => clearHandoff({ storage: hostile }));
});
