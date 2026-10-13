// The door's reads of the audit trail, over an injected transport. The
// entity's whole trail is the one read the core answers as a bare JSON array
// (the reopen handler) where the contract draws a page; both must read.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { DealerApi } from "../src/dealer-api.js";
import { auditRefusalWords, trailEmptyWords } from "../src/audit-logic.js";

const ENTRY = (id) => ({
  id,
  action: "SALE_COMPLETED",
  entity_type: "pos.sale",
  entity_id: "s1",
  actor_id: "a1",
  actor_role: "inside_sales",
  branch_id: "b1",
  changes: {},
  request_id: "r1",
  created_at: "2000-01-01T00:00:00Z",
});

function apiAnswering(body) {
  const fetch = async () => new Response(JSON.stringify(body), { status: 200, headers: { "content-type": "application/json" } });
  return new DealerApi(new ApiClient({ baseUrl: "http://core.test", fetch }));
}

test("the entity trail reads a bare array, the shape the core's reopen handler writes", async () => {
  const result = await apiAnswering([ENTRY("e1"), ENTRY("e2")]).auditTransaction("pos.sale", "s1");
  assert.equal(result.ok, true);
  assert.equal(result.value.length, 2);
  assert.equal(result.value[0].id, "e1");
});

test("the entity trail still reads the contract's page", async () => {
  const result = await apiAnswering({ items: [ENTRY("e1")], next_cursor: null, limit: 200 }).auditTransaction("pos.sale", "s1");
  assert.equal(result.ok, true);
  assert.equal(result.value.length, 1);
});

test("an empty entity trail has its own sentence, and it does not say nothing happened", () => {
  const words = trailEmptyWords();
  assert.notEqual(words, "");
  assert.match(words, /no entries/i);
  assert.doesNotMatch(words, /nothing happened/i);
});

test("the audit refusal names leadership and the trail, not the staff register", () => {
  const words = auditRefusalWords("Forbidden", 403);
  assert.match(words, /leadership/);
  assert.match(words, /audit trail/i);
  assert.doesNotMatch(words, /staff register|managers/);
  assert.equal(auditRefusalWords("entry not found", 404), "entry not found");
});
