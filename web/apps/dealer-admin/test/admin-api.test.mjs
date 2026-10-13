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

// -- the print queue --------------------------------------------------------------------------

const JOBS = {
  jobs: [
    {
      id: "9a000000-0000-4000-9000-000000000001",
      doc_type: "RECEIPT",
      ref_id: "0f000000-0000-4000-9000-0000000000s1",
      branch_id: "5d1b3f6e-0000-4000-9000-0000000000pl",
      render_target: "BROWSER",
      status: "PRINTED",
      requested_by: "da110000-0000-4000-9000-000000000001",
      requested_at: "2000-01-01T10:00:00Z",
      printed_at: "2000-01-01T10:00:01Z",
    },
    {
      id: "9a000000-0000-4000-9000-000000000002",
      doc_type: "RECEIPT",
      ref_id: "0f000000-0000-4000-9000-0000000000s1",
      branch_id: "5d1b3f6e-0000-4000-9000-0000000000pl",
      render_target: "ZPL",
      status: "QUEUED",
      requested_at: "2000-01-01T11:00:00Z",
    },
  ],
};

test("the print queue read projects one document's jobs, newest first as served", async () => {
  const result = await apiAnswering(JOBS).printJobs("RECEIPT", "0f000000-0000-4000-9000-0000000000s1");
  assert.equal(result.ok, true);
  assert.equal(result.value.length, 2);
  assert.equal(result.value[0].status, "PRINTED");
  assert.equal(result.value[0].printed_at, "2000-01-01T10:00:01Z");
  assert.equal(result.value[1].failure_reason, "");
});

test("the print queue read passes a refusal through as the transport's error", async () => {
  const result = await apiAnswering({ error: 'unknown print doc_type "LABEL"' }, 400).printJobs("LABEL", "x");
  assert.equal(result.ok, false);
  assert.match(result.error, /unknown print doc_type/);
});

// -- the exports: the audit workbook and the messages export ----------------------------------

function bytesAnswering(bytes) {
  const fetch = async () => new Response(bytes, { status: 200, headers: { "content-type": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" } });
  return new AdminApi(new ApiClient({ baseUrl: "http://core.test", fetch }));
}

test("the audit workbook read resolves the raw bytes, one call", async () => {
  const body = new Uint8Array([0x50, 0x4b, 0x03, 0x04, 1, 2, 3]);
  const result = await bytesAnswering(body).auditWorkbook({ entity_type: "pos.sale" });
  assert.equal(result.ok, true);
  assert.equal(result.value.byteLength, 7);
  assert.equal(result.value[0], 0x50);
});

test("the messages export projects the masked rows the server sent", async () => {
  const result = await apiAnswering({
    messages: [
      {
        id: "aa000000-0000-4000-9000-000000000001",
        channel_id: "bb000000-0000-4000-9000-000000000001",
        kind: "TEXT",
        author_name: "Dealer admin (demo)",
        body: "hello yard two",
        created_at: "2000-01-01T10:00:00Z",
        deleted_at: null,
      },
      {
        id: "aa000000-0000-4000-9000-000000000002",
        channel_id: "bb000000-0000-4000-9000-000000000001",
        kind: "APPROVAL",
        body: "payment approved",
        created_at: "2000-01-01T10:05:00Z",
        deleted_at: "2000-01-02T10:00:00Z",
      },
    ],
  }).messagingExport({});
  assert.equal(result.ok, true);
  assert.equal(result.value.length, 2);
  assert.equal(result.value[0].kind, "TEXT");
  assert.equal(result.value[0].author_name, "Dealer admin (demo)");
  assert.equal(result.value[1].deleted_at, "2000-01-02T10:00:00Z");
});

test("the messages export carries a refusal through in the server's own words", async () => {
  const result = await apiAnswering({ error: "until must be an RFC3339 timestamp" }, 400).messagingExport({});
  assert.equal(result.ok, false);
  assert.equal(result.error, "until must be an RFC3339 timestamp");
});
