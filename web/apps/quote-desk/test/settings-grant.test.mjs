// The settings grant is asked once per signed in person (round 14 (a)): the client remembers the answer, shares
// it with an ask that arrives while the first is out, starts clean for another person, and keeps no failure.
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { QdClient } from "../src/qd-client.ts";

const GRANT = { can_read_acceptance: true, can_read_scope_rules: false, can_edit_policy: true };

function clientWith(answers, caller) {
  const calls = [];
  const fetch = async (url) => {
    calls.push(String(url));
    const answer = answers.shift();
    return new Response(JSON.stringify(answer.body), { status: answer.status, headers: { "content-type": "application/json" } });
  };
  const qd = new QdClient(new ApiClient({ baseUrl: "http://core.test", fetch }), caller);
  return { qd, calls };
}

test("settingsGrant asks the server once for one person and answers the rest from memory", async () => {
  const { qd, calls } = clientWith([{ status: 200, body: GRANT }], () => "u-1");
  const first = await qd.settingsGrant();
  const second = await qd.settingsGrant();
  assert.equal(first.ok, true);
  assert.deepEqual(first.value, { canReadAcceptance: true, canReadScopeRules: false, canEditPolicy: true });
  assert.deepEqual(second.value, first.value);
  assert.equal(calls.length, 1);
  assert.match(calls[0], /\/api\/v1\/quotes\/settings-grant$/);
});

test("settingsGrant shares one read between asks that overlap", async () => {
  const { qd, calls } = clientWith([{ status: 200, body: GRANT }], () => "u-1");
  const [a, b] = await Promise.all([qd.settingsGrant(), qd.settingsGrant()]);
  assert.equal(a.ok && b.ok, true);
  assert.equal(calls.length, 1);
});

test("settingsGrant starts clean when another person signs in", async () => {
  let caller = "u-1";
  const { qd, calls } = clientWith(
    [{ status: 200, body: GRANT }, { status: 200, body: { can_read_acceptance: false, can_read_scope_rules: false, can_edit_policy: false } }],
    () => caller,
  );
  assert.equal((await qd.settingsGrant()).value.canReadAcceptance, true);
  caller = "u-2";
  assert.equal((await qd.settingsGrant()).value.canReadAcceptance, false);
  assert.equal(calls.length, 2);
});

test("settingsGrant keeps no failure: a refused or unreadable answer is asked again", async () => {
  const { qd, calls } = clientWith(
    [{ status: 503, body: { error: "the settings grant could not be read" } }, { status: 200, body: { can_read_acceptance: "yes" } }, { status: 200, body: GRANT }],
    () => "u-1",
  );
  assert.equal((await qd.settingsGrant()).ok, false);
  assert.equal((await qd.settingsGrant()).ok, false);
  assert.equal((await qd.settingsGrant()).ok, true);
  assert.equal(calls.length, 3);
});
