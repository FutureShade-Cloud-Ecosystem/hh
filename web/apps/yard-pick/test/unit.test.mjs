// The template's unit leg: the demo handoff seam, read through an injected
// storage double. A role app copies this file with the seam and keeps it
// green; a copy that changes the seam changes these cases with it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { clearFrontDoorHandoff, FRONT_DOOR_SESSION_KEY, readFrontDoorHandoff } from "../src/demo-account.js";
import { readFrontDoorHandoff as defaultRead } from "../src/demo-account.default.js";

function storageWith(entries) {
  const map = new Map(Object.entries(entries));
  return {
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => void map.set(k, v),
    removeItem: (k) => void map.delete(k),
    has: (k) => map.has(k),
  };
}

const NOW = 1_000_000;
const record = (over = {}) => ({
  token: "t",
  tokenExpiresAtMs: NOW + 60_000,
  flow: "sales",
  roleLabel: "Sales",
  user: { userId: "u1", email: "sales@hh-demo.invalid", name: "Sales (demo)" },
  roleCode: "inside_sales",
  roleName: "Inside Sales",
  tier: 3,
  scope: "BRANCH",
  ...over,
});
const read = (storage) => readFrontDoorHandoff({ storage, now: () => NOW });

test("no record in the front door's key reads as absent", () => {
  assert.equal(read(storageWith({})).kind, "absent");
});

test("a whole, unexpired record reads as ready with the session", () => {
  const out = read(storageWith({ [FRONT_DOOR_SESSION_KEY]: JSON.stringify(record()) }));
  assert.equal(out.kind, "ready");
  assert.equal(out.session.roleCode, "inside_sales");
});

test("an expired record and a record missing a field both read as unusable", () => {
  const expired = record({ tokenExpiresAtMs: NOW - 1 });
  assert.equal(read(storageWith({ [FRONT_DOOR_SESSION_KEY]: JSON.stringify(expired) })).kind, "unusable");
  const { scope: _drop, ...partial } = record();
  assert.equal(read(storageWith({ [FRONT_DOOR_SESSION_KEY]: JSON.stringify(partial) })).kind, "unusable");
});

test("clearing drops the front door's key", () => {
  const storage = storageWith({ [FRONT_DOOR_SESSION_KEY]: JSON.stringify(record()) });
  clearFrontDoorHandoff({ storage });
  assert.equal(storage.has(FRONT_DOOR_SESSION_KEY), false);
});

test("the default build's stub never reads a handoff", () => {
  assert.deepEqual(defaultRead(), { kind: "absent" });
});
