// The admin-rest door's reads and writes, over an injected transport. Each
// surface's section lands with its commit, red first: this file landed before
// admin-api.ts existed, and the run below it failed on the missing module (see
// the report).
import { test } from "node:test";
import assert from "node:assert/strict";
import { ApiClient } from "@hardscapehouse/api-client";
import { AdminApi } from "../src/admin-api.js";

function apiAnswering(body, status = 200) {
  const fetch = async () =>
    new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
  return new AdminApi(new ApiClient({ baseUrl: "http://core.test", fetch }));
}

// -- the assistant's admin reads and writes ---------------------------------------------------

const SPEND = {
  month: "2026-10",
  users: [
    {
      user_id: "u1",
      display_name: "Dealer admin (demo)",
      email: "dealer.admin@hh-demo.invalid",
      role_code: "GENERAL_MANAGER",
      exchanges: 4,
      tokens_in: 300,
      tokens_out: 200,
      budget: { tokens: 60000, source: "default" },
    },
    {
      user_id: "u2",
      display_name: "Yard team (demo)",
      email: "yard.team@hh-demo.invalid",
      role_code: "YARD_TEAM",
      exchanges: 0,
      tokens_in: 0,
      tokens_out: 0,
      budget: { tokens: 0, source: "user" },
    },
  ],
};

test("the spend report projects whole rows, the zero rows included", async () => {
  const result = await apiAnswering(SPEND).assistSpend();
  assert.equal(result.ok, true);
  assert.equal(result.value.month, "2026-10");
  assert.equal(result.value.users.length, 2);
  assert.equal(result.value.users[0].budget.source, "default");
  assert.equal(result.value.users[1].budget.tokens, 0);
});

test("a refusal of the spend read arrives as the transport's error", async () => {
  const result = await apiAnswering({ error: "Forbidden" }, 403).assistSpend();
  assert.equal(result.ok, false);
  assert.equal(result.error, "Forbidden");
});

test("the limits view carries the role defaults, the overrides and the code default", async () => {
  const result = await apiAnswering({
    role_budgets: { GENERAL_MANAGER: 500000 },
    user_overrides: { u2: 0 },
    default_budget: 60000,
  }).assistLimits();
  assert.equal(result.ok, true);
  assert.equal(result.value.role_budgets.GENERAL_MANAGER, 500000);
  assert.equal(result.value.user_overrides.u2, 0);
  assert.equal(result.value.default_budget, 60000);
});

test("a role budget write answers the role and the tokens it landed", async () => {
  const result = await apiAnswering({ role_code: "YARD_TEAM", tokens: 120000 }).putRoleLimit("YARD_TEAM", 120000);
  assert.equal(result.ok, true);
  assert.equal(result.value.role_code, "YARD_TEAM");
  assert.equal(result.value.tokens, 120000);
});

test("a user budget write answers the override it landed", async () => {
  const result = await apiAnswering({ user_id: "u2", tokens: 0, source: "user" }).putUserLimit("u2", 0);
  assert.equal(result.ok, true);
  assert.equal(result.value.source, "user");
});

test("reset to role answers the user and the role source", async () => {
  const result = await apiAnswering({ user_id: "u2", source: "role" }).resetUserLimit("u2");
  assert.equal(result.ok, true);
  assert.equal(result.value.source, "role");
});

test("the settings read carries the four keys and the key's name, never a credential", async () => {
  const result = await apiAnswering({
    settings: { provider: "stub", model: "hos-stub-1", chat_enabled: true, spend_visible_to_non_admin: false },
    key_env_var: "HH_ASSIST_API_KEY",
    key_present: false,
  }).assistSettings();
  assert.equal(result.ok, true);
  assert.equal(result.value.provider, "stub");
  assert.equal(result.value.model, "hos-stub-1");
  assert.equal(result.value.chat_enabled, true);
  assert.equal(result.value.spend_visible_to_non_admin, false);
  assert.equal(result.value.key_env_var, "HH_ASSIST_API_KEY");
  assert.equal(result.value.key_present, false);
});

test("a setting write answers the key and the value it landed", async () => {
  const result = await apiAnswering({ key: "chat_enabled", value: "false" }).putSetting("chat_enabled", "false");
  assert.equal(result.ok, true);
  assert.equal(result.value.key, "chat_enabled");
  assert.equal(result.value.value, "false");
});
