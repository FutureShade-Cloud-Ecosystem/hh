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
