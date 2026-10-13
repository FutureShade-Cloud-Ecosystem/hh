// The body hash: one operation, one canonical byte string, one SHA-256. It is
// how the client notices a reused key with a different body, and a stored
// body that changed on disk.
import { test } from "node:test";
import assert from "node:assert/strict";
import { canonicalJson, bodyHash } from "../src/canonical.ts";

test("canonical JSON sorts object keys at every depth and keeps array order", () => {
  assert.equal(canonicalJson({ b: 1, a: { d: [3, 1], c: null } }), '{"a":{"c":null,"d":[3,1]},"b":1}');
});

test("canonical JSON refuses what JSON cannot carry exactly", () => {
  for (const bad of [undefined, NaN, Infinity, () => 1, 10n, { a: undefined }]) {
    assert.throws(() => canonicalJson(bad), /canonical/);
  }
});

test("the hash ignores key order and sees every value", async () => {
  const a = await bodyHash({ scope: "pick_tasks", op: "update", entity: null, payload: { id: "x", n: 1, m: 2 }, baseSeq: 3 });
  const b = await bodyHash({ baseSeq: 3, payload: { m: 2, n: 1, id: "x" }, entity: null, op: "update", scope: "pick_tasks" });
  const c = await bodyHash({ scope: "pick_tasks", op: "update", entity: null, payload: { id: "x", n: 1, m: 3 }, baseSeq: 3 });
  assert.equal(a, b);
  assert.notEqual(a, c);
  assert.match(a, /^[0-9a-f]{64}$/);
});

test("the hash matches a known SHA-256 vector for a known canonical string", async () => {
  // sha256 of the canonical text {"a":1}
  assert.equal(await bodyHash({ a: 1 }), "015abd7f5cc57a2dd94b7590f04ad8084273905ee33ec5cebeae62276a97f862");
});

test("canonical JSON refuses anything that is not plain JSON: objects with another prototype, holes, toJSON values", () => {
  class Point {
    constructor() {
      this.x = 1;
    }
  }
  const cyclic = {};
  cyclic.self = cyclic;
  const withSymbol = { a: 1, [Symbol("s")]: 2 };
  const sparse = new Array(2);
  const holey = [1, , 3];
  for (const [name, bad] of Object.entries({
    date: new Date(0),
    map: new Map([["a", 1]]),
    set: new Set([1]),
    instance: new Point(),
    regexp: /x/,
    nestedDate: { at: new Date(1e12) },
    inArray: [new Date(0)],
    sparse,
    holey,
    toJSON: { toJSON: () => "x" },
    symbolKey: withSymbol,
    cyclic,
    typedArray: new Uint8Array(2),
  })) {
    assert.throws(() => canonicalJson(bad), /canonical/, name);
  }
});

test("canonical JSON still carries a null prototype object and every plain JSON shape", () => {
  const bare = Object.create(null);
  bare.b = 1;
  bare.a = [null, true, "x", 1.5, { c: [] }];
  assert.equal(canonicalJson(bare), '{"a":[null,true,"x",1.5,{"c":[]}],"b":1}');
  assert.equal(canonicalJson(JSON.parse('{"__proto__":{"x":1},"k":2}')), '{"__proto__":{"x":1},"k":2}');
});

test("two different dates can never share a hash: neither is hashable", async () => {
  await assert.rejects(bodyHash({ a: new Date(0) }), /canonical/);
  await assert.rejects(bodyHash({ a: new Date(1e12) }), /canonical/);
});
