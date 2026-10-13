// The statements slice's stub against the contract and against the core, field by
// field. A stub that drifts from the server makes every browser leg a
// flattering lie, so this test starts the stub, makes every call the slice
// makes, and holds each answer to the generated wire descriptors: required
// fields present, types and vocabularies right, and no field the contract
// does not declare. Then each refusal is held to the core's words: the status,
// the envelope, and the sentence itself, which must appear in the core's
// source. A refusal the stub invented fails here.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import { IDS } from "./stub/finstmt-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["finstmt", join("identity", "staffcontext"), "gl", join("platform", "authz"), join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");

let child;
let base;

async function freePort() {
  return new Promise((resolve) => {
    const probe = createServer();
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

before(async () => {
  const port = await freePort();
  base = `http://127.0.0.1:${port}`;
  child = spawn(process.execPath, [join(appDir, "test", "stub", "money-desk-stub.mjs")], {
    env: { ROLE_STUB_PORT: String(port) },
    stdio: "ignore",
  });
  for (let i = 0; i < 100; i += 1) {
    try {
      if ((await fetch(`${base}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error("the stub did not start");
});

after(() => {
  child?.kill();
});

// A stub token for one account: the shape the stub reads a subject from.
function tokenFor(sub) {
  const part = (v) => Buffer.from(JSON.stringify(v), "utf8").toString("base64url");
  return `${part({ alg: "ES256" })}.${part({ sub, exp: 4102444800 })}.c3R1Yg`;
}

async function call(as, method, path, body) {
  const headers = { Authorization: `Bearer ${tokenFor(as)}`, "X-Org": "hh-demo" };
  let payload;
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    payload = JSON.stringify(body);
  }
  const res = await fetch(`${base}${path}`, { method, headers, body: payload });
  const text = await res.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    /* not json */
  }
  return { status: res.status, json };
}

const reset = () => fetch(`${base}/_stub/reset`, { method: "POST" });

function declared(d) {
  if (d.k === "ref") return declared(SCHEMA_DESCRIPTORS[d.name]);
  if (d.k === "allOf") return Object.assign({}, ...d.of.map(declared));
  if (d.k === "object") return d.props;
  return {};
}

function undeclared(value, d, path = "value", out = []) {
  if (d.k === "ref") return undeclared(value, SCHEMA_DESCRIPTORS[d.name], path, out);
  if (d.k === "allOf") {
    const props = declared(d);
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      for (const key of Object.keys(value)) if (props[key] === undefined) out.push(`${path}.${key}`);
      for (const [key, leg] of Object.entries(props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    }
    return out;
  }
  if (d.k === "object") {
    if (value === null || typeof value !== "object" || Array.isArray(value)) return out;
    for (const key of Object.keys(value)) if (d.props[key] === undefined) out.push(`${path}.${key}`);
    for (const [key, leg] of Object.entries(d.props)) if (value[key] !== undefined) undeclared(value[key], leg, `${path}.${key}`, out);
    return out;
  }
  if (d.k === "array" && Array.isArray(value)) value.forEach((v, i) => undeclared(v, d.of, `${path}[${i}]`, out));
  if (d.k === "union") {
    const leg = d.of.find((l) => l.k !== "null");
    if (leg !== undefined && value !== null) undeclared(value, leg, path, out);
  }
  return out;
}

function holds(schemaName, value) {
  const descriptor = SCHEMA_DESCRIPTORS[schemaName];
  assert.ok(descriptor !== undefined, `no descriptor named ${schemaName}`);
  assert.deepEqual(validateValue(value, descriptor), [], `${schemaName} does not fit the contract`);
  assert.deepEqual(undeclared(value, descriptor), [], `${schemaName} carries fields the contract does not declare`);
}

function refused(res, status, fragment) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.ok(res.json.error.includes(fragment), `expected "${fragment}" in "${res.json.error}"`);
  assert.ok(coreSource.includes(fragment), `the core's source does not contain the sentence "${fragment}": the stub invented it`);
}

const get = (as, path) => call(as, "GET", path);
const sumOf = (rows, key) => rows.reduce((n, r) => n + r[key], 0);

test("the periods answer the contract's page and the cursor walks every period once", async () => {
  await reset();
  const { year } = await (await fetch(`${base}/_stub/finstmt-calls`)).json();
  const all = await get(IDS.controller, "/api/v1/gl/periods?limit=100");
  assert.equal(all.status, 200);
  holds("GlPeriodPage", all.json);
  assert.equal(all.json.items.length, 24);
  assert.deepEqual([...new Set(all.json.items.map((p) => p.year))].sort(), [year - 1, year]);
  assert.ok(all.json.items.some((p) => p.status === "CLOSED") && all.json.items.some((p) => p.status === "OPEN"));
  const first = await get(IDS.controller, "/api/v1/gl/periods?limit=10");
  assert.equal(first.json.items.length, 10);
  assert.ok(typeof first.json.next_cursor === "string");
  const second = await get(IDS.controller, `/api/v1/gl/periods?limit=10&cursor=${first.json.next_cursor}`);
  assert.ok(second.json.items.every((p) => !first.json.items.some((f) => f.id === p.id)));
});

test("the income statement answers the contract, its totals are its rows, and a period and a branch narrow it", async () => {
  await reset();
  const { year } = await (await fetch(`${base}/_stub/finstmt-calls`)).json();
  const whole = await get(IDS.controller, `/api/v1/finstmt/income-statement?year=${year}`);
  assert.equal(whole.status, 200);
  holds("FinStmtIncomeStatementReport", whole.json);
  assert.equal(whole.json.month, 0);
  assert.equal(whole.json.revenue_cents, sumOf(whole.json.revenue, "amount_cents"));
  assert.equal(whole.json.expense_cents, sumOf(whole.json.expenses, "amount_cents"));
  assert.equal(whole.json.net_income_cents, whole.json.revenue_cents - whole.json.expense_cents);
  const one = await get(IDS.controller, `/api/v1/finstmt/income-statement?year=${year}&month=2`);
  assert.ok(one.json.revenue_cents < whole.json.revenue_cents);
  const trenton = await get(IDS.controller, `/api/v1/finstmt/income-statement?year=${year}&branch_id=${IDS.trenton}`);
  const kingston = await get(IDS.controller, `/api/v1/finstmt/income-statement?year=${year}&branch_id=${IDS.kingston}`);
  assert.equal(trenton.json.revenue_cents + kingston.json.revenue_cents, whole.json.revenue_cents);
  holds("FinStmtIncomeStatementReport", trenton.json);
  const compared = await get(IDS.controller, `/api/v1/finstmt/income-statement?year=${year}&compare=1`);
  assert.equal(compared.status, 200);
  holds("FinStmtComparativeRows", compared.json);
  assert.ok(compared.json.rows.every((r) => r.delta_cents === r.current_cents - r.prior_cents));
  assert.ok(compared.json.rows.some((r) => r.prior_cents > 0));
});

test("the balance sheet balances by the server's own flag and the comparison reads", async () => {
  await reset();
  const { year } = await (await fetch(`${base}/_stub/finstmt-calls`)).json();
  const sheet = await get(IDS.controller, `/api/v1/finstmt/balance-sheet?year=${year}`);
  assert.equal(sheet.status, 200);
  holds("FinStmtBalanceSheetReport", sheet.json);
  assert.equal(sheet.json.balanced, true);
  assert.equal(sheet.json.total_assets_cents, sumOf(sheet.json.assets, "amount_cents"));
  assert.equal(sheet.json.total_assets_cents, sheet.json.total_liabilities_cents + sheet.json.total_equity_cents + sheet.json.net_income_cents);
  const compared = await get(IDS.controller, `/api/v1/finstmt/balance-sheet?year=${year}&compare=1`);
  holds("FinStmtComparativeRows", compared.json);
});

test("the trial balance is in balance and carries debit, credit and balance for every account", async () => {
  await reset();
  const { year } = await (await fetch(`${base}/_stub/finstmt-calls`)).json();
  const tb = await get(IDS.controller, `/api/v1/finstmt/trial-balance?year=${year}`);
  assert.equal(tb.status, 200);
  holds("FinStmtTrialBalanceReport", tb.json);
  assert.equal(tb.json.in_balance, true);
  assert.equal(tb.json.total_debit_cents, sumOf(tb.json.rows, "total_debit_cents"));
  assert.ok(tb.json.rows.every((r) => r.balance_cents === r.total_debit_cents - r.total_credit_cents));
});

test("every statement figure ties to the ledger detail behind its account", async () => {
  await reset();
  const { year } = await (await fetch(`${base}/_stub/finstmt-calls`)).json();
  const income = (await get(IDS.controller, `/api/v1/finstmt/income-statement?year=${year}&month=3`)).json;
  for (const row of [...income.revenue, ...income.expenses]) {
    const detail = await get(IDS.controller, `/api/v1/finstmt/gl-detail?year=${year}&month=3&account_code=${row.account_code}&account_type=${row.account_type}`);
    assert.equal(detail.status, 200);
    holds("FinStmtGLDetailResponse", detail.json);
    assert.equal(detail.json.tie_cents, row.amount_cents, `${row.account_code} does not tie`);
  }
  const sheet = (await get(IDS.controller, `/api/v1/finstmt/balance-sheet?year=${year}`)).json;
  for (const row of sheet.assets) {
    const detail = await get(IDS.controller, `/api/v1/finstmt/gl-detail?year=${year}&account_code=${row.account_code}&account_type=ASSET`);
    assert.equal(detail.json.tie_cents, row.amount_cents);
  }
});

test("the receivables aging answers the contract, in the core's band order, and its total is its rows", async () => {
  await reset();
  const aging = await get(IDS.controller, "/api/v1/finstmt/ar-aging");
  assert.equal(aging.status, 200);
  holds("FinStmtAgingSummary", aging.json);
  assert.deepEqual(aging.json.buckets.map((b) => b.bucket), ["CURRENT", "1_30", "31_60", "61_90", "90_PLUS"]);
  assert.equal(aging.json.grand_total_cents, sumOf(aging.json.rows, "amount_cents"));
  assert.equal(aging.json.grand_total_cents, sumOf(aging.json.buckets, "cents"));
  const one = await get(IDS.controller, `/api/v1/finstmt/ar-aging?branch_id=${IDS.trenton}`);
  assert.ok(one.json.rows.every((r) => r.branch_id === IDS.trenton));
});

test("the saved packs list under the key the core writes, and create, change and delete answer the contract", async () => {
  await reset();
  const list = await get(IDS.controller, "/api/v1/finstmt/schedules");
  assert.equal(list.status, 200);
  assert.deepEqual(Object.keys(list.json), ["schedules"]);
  list.json.schedules.forEach((s) => holds("FinStmtSchedule", s));
  assert.equal(list.json.schedules.length, 2);
  const made = await call(IDS.controller, "POST", "/api/v1/finstmt/schedules", { report: "trial_balance", recipients: " a@b.example , c@d.example ", cadence: "daily", branch_id: "", is_active: true });
  assert.equal(made.status, 201);
  holds("FinStmtSchedule", made.json);
  assert.equal(made.json.report, "TRIAL_BALANCE");
  assert.equal(made.json.recipients, "a@b.example,c@d.example");
  assert.equal(made.json.cadence, "DAILY");
  const changed = await call(IDS.controller, "PUT", `/api/v1/finstmt/schedules/${made.json.id}`, { report: "GL_DETAIL", recipients: "a@b.example", cadence: "WEEKLY", branch_id: IDS.trenton, is_active: false });
  assert.equal(changed.status, 200);
  holds("FinStmtSchedule", changed.json);
  assert.equal(changed.json.is_active, false);
  const gone = await call(IDS.controller, "DELETE", `/api/v1/finstmt/schedules/${made.json.id}`);
  assert.equal(gone.status, 204);
  assert.equal((await get(IDS.controller, "/api/v1/finstmt/schedules")).json.schedules.length, 2);
});

test("the read refusals are the core's sentences, in the core's order", async () => {
  await reset();
  const { year } = await (await fetch(`${base}/_stub/finstmt-calls`)).json();
  // The manager gate comes first: a sales account reads nothing.
  refused(await get(IDS.sales, `/api/v1/finstmt/income-statement?year=${year}`), 403, "Forbidden");
  refused(await get(IDS.sales, "/api/v1/finstmt/schedules"), 403, "Forbidden");
  // Then the year, before the branch fence.
  refused(await get(IDS.manager, "/api/v1/finstmt/income-statement"), 400, "year is required");
  refused(await get(IDS.controller, "/api/v1/finstmt/balance-sheet?year=abc"), 400, "year must be a 4-digit fiscal year");
  refused(await get(IDS.controller, "/api/v1/finstmt/trial-balance?year=99999"), 400, "year must be a 4-digit fiscal year");
  // Then the branch fence: a scoped manager must name a branch it holds, and the trial balance is global only.
  refused(await get(IDS.manager, `/api/v1/finstmt/income-statement?year=${year}`), 403, "finstmt: caller cannot access this branch");
  refused(await get(IDS.manager, `/api/v1/finstmt/income-statement?year=${year}&branch_id=${IDS.kingston}`), 403, "finstmt: caller cannot access this branch");
  refused(await get(IDS.manager, `/api/v1/finstmt/trial-balance?year=${year}`), 403, "finstmt: caller cannot access this branch");
  refused(await get(IDS.manager, "/api/v1/finstmt/ar-aging"), 403, "finstmt: caller cannot access this branch");
  assert.equal((await get(IDS.manager, `/api/v1/finstmt/income-statement?year=${year}&branch_id=${IDS.trenton}`)).status, 200);
  assert.equal((await get(IDS.payrec, `/api/v1/finstmt/trial-balance?year=${year}`)).status, 200);
});

test("the pack refusals are the core's sentences, in the core's order", async () => {
  await reset();
  const pack = { report: "AR_AGING", recipients: "a@b.example", cadence: "DAILY", branch_id: "", is_active: true };
  const post = (as, body) => call(as, "POST", "/api/v1/finstmt/schedules", body);
  // The admin gate: a manager who is not the controller reads the list and writes nothing.
  assert.equal((await get(IDS.payrec, "/api/v1/finstmt/schedules")).status, 200);
  refused(await post(IDS.payrec, pack), 403, "Forbidden");
  refused(await call(IDS.payrec, "DELETE", `/api/v1/finstmt/schedules/${IDS.pack1}`), 403, "Forbidden");
  refused(await post(IDS.controller, { ...pack, report: "CASH_FLOW" }), 400, "report must be one of INCOME_STATEMENT, BALANCE_SHEET, TRIAL_BALANCE, GL_DETAIL, AR_AGING");
  refused(await post(IDS.controller, { ...pack, report: "CASH_FLOW", cadence: "HOURLY" }), 400, "report must be one of");
  refused(await post(IDS.controller, { ...pack, cadence: "HOURLY" }), 400, "cadence must be one of DAILY, WEEKLY, MONTHLY");
  refused(await post(IDS.controller, { ...pack, recipients: " , " }), 400, "at least one recipient e-mail is required");
  refused(await post(IDS.controller, { ...pack, recipients: "a@b.example, nobody" }), 400, "invalid recipient e-mail: ");
  const raw = await fetch(`${base}/api/v1/finstmt/schedules`, { method: "POST", headers: { Authorization: `Bearer ${tokenFor(IDS.controller)}`, "Content-Type": "application/json" }, body: "{not json" });
  refused({ status: raw.status, json: await raw.json() }, 400, "invalid_json");
  // The id is checked before the input, and a missing pack is a 404 after it.
  refused(await call(IDS.controller, "PUT", "/api/v1/finstmt/schedules/not-a-uuid", { ...pack, cadence: "HOURLY" }), 400, "id must be a uuid");
  refused(await call(IDS.controller, "PUT", `/api/v1/finstmt/schedules/${IDS.pack1}`, { ...pack, cadence: "HOURLY" }), 400, "cadence must be one of");
  refused(await call(IDS.controller, "PUT", "/api/v1/finstmt/schedules/5c4a0000-0000-4000-9000-0000000000ff", pack), 404, "not found");
  refused(await call(IDS.controller, "DELETE", "/api/v1/finstmt/schedules/not-a-uuid"), 400, "id must be a uuid");
  refused(await call(IDS.controller, "DELETE", "/api/v1/finstmt/schedules/5c4a0000-0000-4000-9000-0000000000ff"), 404, "not found");
});
