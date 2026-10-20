// Unit tests for the session branch key: the front door writes the branch ids the person chose, a desk reads
// them back through the granted list, and anything the core did not grant is dropped.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  SESSION_BRANCHES_KEY,
  decideSessionBranch,
  readSessionBranchIds,
  writeSessionBranchIds,
} from "./session-branches.js";

function memoryStore(seed = {}) {
  const map = new Map(Object.entries(seed));
  return {
    getItem: (key) => (map.has(key) ? map.get(key) : null),
    setItem: (key, value) => void map.set(key, String(value)),
    removeItem: (key) => void map.delete(key),
  };
}

const GRANTED = [{ id: "b1" }, { id: "b2" }];

test("a written choice reads back through the granted list", () => {
  const store = memoryStore();
  writeSessionBranchIds(["b2"], store);
  assert.equal(SESSION_BRANCHES_KEY, "hh.session.branches.v1");
  assert.deepEqual(JSON.parse(store.getItem(SESSION_BRANCHES_KEY)), { ids: ["b2"] });
  assert.deepEqual(readSessionBranchIds(GRANTED, store), ["b2"]);
});

test("a tampered key is dropped, never trusted", () => {
  for (const raw of ["not json", "[]", "null", '{"ids":"b1"}', '{"ids":[1,{"x":1}]}', '{"other":["b1"]}']) {
    const store = memoryStore({ [SESSION_BRANCHES_KEY]: raw });
    assert.deepEqual(readSessionBranchIds(GRANTED, store), [], raw);
  }
  const forged = memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["b1","not-granted","b1"]}' });
  assert.deepEqual(readSessionBranchIds(GRANTED, forged), ["b1"]);
});

test("an empty or missing key reads as no choice", () => {
  assert.deepEqual(readSessionBranchIds(GRANTED, memoryStore()), []);
  assert.deepEqual(readSessionBranchIds(GRANTED, memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":[]}' })), []);
  assert.deepEqual(readSessionBranchIds(GRANTED, undefined), []);
});

test("a stale id the core no longer grants is dropped", () => {
  const store = memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["gone","b2"]}' });
  assert.deepEqual(readSessionBranchIds(GRANTED, store), ["b2"]);
  const onlyStale = memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["gone"]}' });
  assert.deepEqual(readSessionBranchIds(GRANTED, onlyStale), []);
});

test("a store that throws reads as no choice and writes nothing", () => {
  const broken = {
    getItem() { throw new Error("blocked"); },
    setItem() { throw new Error("blocked"); },
    removeItem() { throw new Error("blocked"); },
  };
  assert.deepEqual(readSessionBranchIds(GRANTED, broken), []);
  assert.doesNotThrow(() => writeSessionBranchIds(["b1"], broken));
});

test("decide: none granted refuses, one granted is taken, several ask unless exactly one is chosen", () => {
  assert.deepEqual(decideSessionBranch([], memoryStore()), { kind: "none" });
  assert.deepEqual(decideSessionBranch([{ id: "b1" }], memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["zzz"]}' })), { kind: "one", id: "b1" });
  assert.deepEqual(decideSessionBranch(GRANTED, memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["b2"]}' })), { kind: "one", id: "b2" });
  assert.deepEqual(decideSessionBranch(GRANTED, memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["b1","b2"]}' })), { kind: "ask" });
  assert.deepEqual(decideSessionBranch(GRANTED, memoryStore()), { kind: "ask" });
  assert.deepEqual(decideSessionBranch(GRANTED, memoryStore({ [SESSION_BRANCHES_KEY]: '{"ids":["forged"]}' })), { kind: "ask" });
});
