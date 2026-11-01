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

/** A storage that passed the constructor's probe and then fails every real write (quota, or a context that throws). */
function throwingStorage() {
  const map = new Map();
  return {
    map,
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => {
      if (k === "hh-auth.storage-probe") {
        map.set(k, v);
        return;
      }
      throw new Error("QuotaExceededError");
    },
    removeItem: (k) => void map.delete(k),
  };
}

test("a storage write that fails at run time keeps the key held in memory for the page's life", () => {
  const s = throwingStorage();
  new HeldKeys("user-throw-1", s).set("acct-1", "key-a");
  // The write failed, so nothing of ours is in storage; the key is still held, and a later desk of the
  // same user on the same page (a sign out and back in) reads it rather than minting a new one.
  assert.equal(s.map.size, 0);
  assert.equal(new HeldKeys("user-throw-1", s).get("acct-1"), "key-a");
  // When a write lands again, storage carries the whole record and the memory copy is stood down.
  s.setItem = (k, v) => void s.map.set(k, v);
  const healed = new HeldKeys("user-throw-1", s);
  healed.set("acct-2", "key-b");
  const after = new HeldKeys("user-throw-1", s);
  assert.equal(after.get("acct-1"), "key-a");
  assert.equal(after.get("acct-2"), "key-b");
  // Deleting the last entry clears storage and the memory copy with it.
  healed.delete("acct-1");
  healed.delete("acct-2");
  assert.equal(new HeldKeys("user-throw-1", s).get("acct-1"), undefined);
  assert.equal(new HeldKeys("user-throw-1", s).get("acct-2"), undefined);
});

/** A storage whose record writes fail on demand: the probe always passes and reads always work, the
 * shape of a quota that starts hitting mid session (the probe key is never the record's own key). */
function failingOnDemand() {
  const map = new Map();
  const record = "hh-money-desk.held-payment-keys.";
  const s = {
    map,
    failWrites: false,
    failClears: false,
    getItem: (k) => (map.has(k) ? map.get(k) : null),
    setItem: (k, v) => {
      if (s.failWrites && k.startsWith(record)) throw new Error("QuotaExceededError");
      map.set(k, v);
    },
    removeItem: (k) => {
      if (s.failClears && k.startsWith(record)) throw new Error("InvalidStateError");
      map.delete(k);
    },
  };
  return s;
}

test("a key the route ended while writes were failing stays ended: the memory record is the whole truth until a write lands", () => {
  const s = failingOnDemand();
  const user = "user-ended-A";
  const h = new HeldKeys(user, s);
  h.set("acct-A", "kA"); // lands
  s.failWrites = true; // every record write now fails
  h.set("acct-B", "kB");
  h.delete("acct-A"); // the route answered A
  assert.equal(h.get("acct-A"), undefined);
  assert.equal(h.get("acct-B"), "kB");
  // A later desk of the same user on the same page (a sign out and back in) reads the same whole truth.
  assert.equal(new HeldKeys(user, s).get("acct-A"), undefined);
  assert.equal(new HeldKeys(user, s).get("acct-B"), "kB");
  // Writes land again: the landing write writes the memory record as it stands, so A stays ended.
  s.failWrites = false;
  h.set("acct-C", "kC");
  const stored = JSON.parse(s.map.get(`hh-money-desk.held-payment-keys.v1.${user}`));
  assert.deepEqual(Object.keys(stored.keys).sort(), ["acct-B", "acct-C"]);
  const after = new HeldKeys(user, s);
  assert.equal(after.get("acct-A"), undefined);
  assert.equal(after.get("acct-B"), "kB");
  assert.equal(after.get("acct-C"), "kC");
});

test("under failing writes, a delete of one of two held accounts keeps it gone", () => {
  const s = failingOnDemand();
  const h = new HeldKeys("user-ended-B", s);
  h.set("acct-1", "K1"); // lands
  s.failWrites = true;
  h.set("acct-2", "K2"); // the write fails
  h.delete("acct-1");
  assert.equal(h.get("acct-1"), undefined);
  assert.equal(h.get("acct-2"), "K2");
  assert.equal(new HeldKeys("user-ended-B", s).get("acct-1"), undefined);
  assert.equal(new HeldKeys("user-ended-B", s).get("acct-2"), "K2");
});

test("a delete to empty in the failing window stays empty even when the clear itself fails", () => {
  const s = failingOnDemand();
  const h = new HeldKeys("user-ended-C", s);
  h.set("acct-1", "K1"); // lands
  s.failWrites = true;
  s.failClears = true; // the stored record cannot be dropped either
  h.set("acct-2", "K2");
  h.delete("acct-1");
  h.delete("acct-2"); // the record is empty: nothing may come back from storage
  assert.equal(h.get("acct-1"), undefined);
  assert.equal(h.get("acct-2"), undefined);
  assert.equal(new HeldKeys("user-ended-C", s).get("acct-2"), undefined);
  // A write that lands writes the record as it stands (still without the ended key) and stands it down.
  s.failWrites = false;
  s.failClears = false;
  h.set("acct-3", "K3");
  const stored = JSON.parse(s.map.get("hh-money-desk.held-payment-keys.v1.user-ended-C"));
  assert.deepEqual(Object.keys(stored.keys), ["acct-3"]);
});

test("a key is a uuid even where randomUUID is missing (a plain http origin), from getRandomValues", () => {  const real = globalThis.crypto;
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

test("ids lists the entries that hold a key, and none once each is deleted", async () => {
  const store = new Map();
  const storage = { getItem: (k) => store.get(k) ?? null, setItem: (k, v) => void store.set(k, v), removeItem: (k) => void store.delete(k) };
  const keys = new HeldKeys("user-ids", storage, "ids.");
  assert.deepEqual(keys.ids(), []);
  keys.set("a", "k1");
  keys.set("b", "k2");
  assert.deepEqual(keys.ids().sort(), ["a", "b"]);
  keys.delete("a");
  keys.delete("b");
  assert.deepEqual(keys.ids(), []);
});
