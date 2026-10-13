// The api-client adapter: the outbox's push and the store's pull go through
// the generated syncPush and syncPull, with the request shape the contract
// declares.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { apiClientTransport } from "../src/transport-api.ts";

function client(respond) {
  const seen = [];
  const fetch = async (url, init) => {
    seen.push({ url, method: init.method, body: init.body });
    const { status, body } = respond(url);
    return { status, ok: status < 400, text: async () => JSON.stringify(body), arrayBuffer: async () => new ArrayBuffer(0) };
  };
  return { api: new ApiClient({ baseUrl: "https://core.example.invalid", fetch }), seen };
}

test("push posts the ops to /api/v1/sync/{scope}", async () => {
  const { api, seen } = client(() => ({ status: 200, body: { results: [] } }));
  const res = await apiClientTransport(api).push("pick_tasks", [{ client_id: "k", op: "create", payload: { a: 1 } }]);
  assert.equal(res.ok, true);
  assert.equal(seen[0].method, "POST");
  assert.equal(seen[0].url, "https://core.example.invalid/api/v1/sync/pick_tasks");
  assert.deepEqual(JSON.parse(seen[0].body), { ops: [{ client_id: "k", op: "create", payload: { a: 1 } }] });
});

test("pull gets /api/v1/sync/{scope} with since and limit", async () => {
  const { api, seen } = client(() => ({ status: 200, body: { scope: "pick_tasks", items: [], tombstones: [], last_seq: 7 } }));
  const res = await apiClientTransport(api).pull("pick_tasks", 7, 50);
  assert.equal(res.ok, true);
  assert.equal(seen[0].method, "GET");
  const url = new URL(seen[0].url);
  assert.equal(url.pathname, "/api/v1/sync/pick_tasks");
  assert.equal(url.searchParams.get("since"), "7");
  assert.equal(url.searchParams.get("limit"), "50");
});

test("a 401 keeps its status so the outbox can tell auth expired from a fault", async () => {
  const { api } = client(() => ({ status: 401, body: { error: "expired" } }));
  const res = await apiClientTransport(api).push("pick_tasks", []);
  assert.equal(res.ok, false);
  assert.equal(res.status, 401);
});
