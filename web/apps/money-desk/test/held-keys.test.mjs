// The held payment keys: one record per user in the tab's session storage, one entry per account.
import { test } from "node:test";
import assert from "node:assert/strict";
import { HeldKeys, newKey } from "../src/ar/held-keys.ts";

function storage() {
  const map = new Map();
  return {
    map,
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => void map.set(k, v),
    removeItem: (k) => void map.delete(k),
  };
}

test("a key set by one desk is read by the next desk of the same user, and ends only when deleted", () => {
  const s = storage();
  new HeldKeys("user-1", s).set("acct-1", "key-a");
  const next = new HeldKeys("user-1", s);
  assert.equal(next.get("acct-1"), "key-a");
  assert.equal(next.get("acct-2"), undefined);
  next.delete("acct-1");
  assert.equal(new HeldKeys("user-1", s).get("acct-1"), undefined);
});

test("another user reads none of them and leaves them untouched", () => {
  const s = storage();
  new HeldKeys("user-1", s).set("acct-1", "key-a");
  const other = new HeldKeys("user-2", s);
  assert.equal(other.get("acct-1"), undefined);
  other.set("acct-1", "key-b");
  other.delete("acct-1");
  assert.equal(new HeldKeys("user-1", s).get("acct-1"), "key-a");
});

test("a record that is not ours reads as no keys, and no user id holds in memory only", () => {
  const s = storage();
  s.setItem("hh-money-desk.held-payment-keys.v1.user-1", JSON.stringify({ keys: ["x"] }));
  assert.equal(new HeldKeys("user-1", s).get("0"), undefined);
  const none = new HeldKeys("", s);
  none.set("acct-1", "key-a");
  assert.equal(none.get("acct-1"), "key-a");
  assert.equal(new HeldKeys("", s).get("acct-1"), undefined);
});

test("a key is a uuid even where randomUUID is missing (a plain http origin), from getRandomValues", () => {
  const real = globalThis.crypto;
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
  try {
    Object.defineProperty(globalThis, "crypto", { value: { getRandomValues: (a) => real.getRandomValues(a) }, configurable: true });
    const keys = new Set(Array.from({ length: 50 }, () => newKey()));
    assert.equal(keys.size, 50);
    for (const k of keys) assert.match(k, uuid);
    Object.defineProperty(globalThis, "crypto", { value: undefined, configurable: true });
    assert.match(newKey(), /^k-\d+-\d+$/);
  } finally {
    Object.defineProperty(globalThis, "crypto", { value: real, configurable: true });
  }
});
