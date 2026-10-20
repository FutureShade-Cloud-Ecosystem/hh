// The desk's branch resolution (UX-79): one granted branch is taken, several granted read the front door's
// session branch key through the granted list, and a desk that cannot tell asks once instead of refusing.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { SESSION_BRANCHES_KEY } from "@hardscapehouse/pos-kit/src/session-branches.js";
import { QdClient } from "../src/qd-client.js";

const WEST = { id: "b-west", code: "WST", name: "West Yard" };
const EAST = { id: "b-east", code: "EST", name: "East Yard" };

function rig(rows, keyValue) {
  const fetch = async () => ({ status: 200, ok: true, text: async () => JSON.stringify(rows), arrayBuffer: async () => new ArrayBuffer(0) });
  const store = { getItem: (k) => (k === SESSION_BRANCHES_KEY ? (keyValue ?? null) : null), setItem() {}, removeItem() {} };
  return { client: new QdClient(new ApiClient({ baseUrl: "", fetch }), () => "u"), store };
}

test("one granted branch is taken as it is, whatever the key says", async () => {
  const r = rig([WEST], '{"ids":["b-east"]}');
  const got = await r.client.resolveBranchChoice(r.store);
  assert.deepEqual(got, { ok: true, value: { kind: "one", branch: { id: "b-west", code: "WST", name: "West Yard" } } });
});

test("several granted with exactly one chosen and granted lands on it", async () => {
  const r = rig([WEST, EAST], '{"ids":["b-east"]}');
  const got = await r.client.resolveBranchChoice(r.store);
  assert.equal(got.ok, true);
  assert.deepEqual(got.value, { kind: "one", branch: { id: "b-east", code: "EST", name: "East Yard" } });
});

test("several granted with both chosen, none chosen, or a forged id asks, with the granted branches by name", async () => {
  for (const key of ['{"ids":["b-west","b-east"]}', undefined, '{"ids":[]}', '{"ids":["forged"]}', "not json"]) {
    const r = rig([WEST, EAST], key);
    const got = await r.client.resolveBranchChoice(r.store);
    assert.equal(got.ok, true, String(key));
    assert.deepEqual(got.value, { kind: "ask", choices: [WEST, EAST] }, String(key));
  }
});

test("no granted branch stays a refusal in words, and an unreadable row is refused", async () => {
  const none = await rig([], undefined).client.resolveBranchChoice(rig([], undefined).store);
  assert.equal(none.ok, false);
  assert.match(none.error, /no branch|did not answer/);
  const bad = await rig([{ id: "b-west" }, EAST], undefined).client.resolveBranchChoice();
  assert.equal(bad.ok, false);
});

test("resolveBranch for the panels refuses until a branch is chosen, never inventing one", async () => {
  const r = rig([WEST, EAST], undefined);
  const got = await r.client.resolveBranch(r.store);
  assert.equal(got.ok, false);
  const chosen = rig([WEST, EAST], '{"ids":["b-west"]}');
  const ok = await chosen.client.resolveBranch(chosen.store);
  assert.deepEqual(ok, { ok: true, value: { id: "b-west", code: "WST", name: "West Yard" } });
});
