// The ledger slice's stub against the contract and against the core, field by
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
import { IDS } from "./stub/gl-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = [join("identity", "staffcontext"), "gl", join("platform", "authz"), join("platform", "httpx")]
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

const decide = (as, id, approve) => call(as, "POST", `/api/v1/staff/approvals/${id}/decide`, { approve, reason: "stub" });

test("the approvals queue answers the contract's page, every branch to the controller and Point Loma alone to the branch manager", async () => {
  await reset();
  const mine = await call(IDS.controller, "GET", "/api/v1/staff/approvals?status=PENDING");
  assert.equal(mine.status, 200);
  holds("IdentityApprovalPage", mine.json);
  assert.equal(mine.json.items.length, 5);
  assert.equal(mine.json.next_cursor, null);
  assert.deepEqual([...new Set(mine.json.items.map((r) => r.branch_id))].sort(), [IDS.julian, IDS.pointLoma].sort());
  const theirs = await call(IDS.manager, "GET", "/api/v1/staff/approvals?status=PENDING");
  assert.ok(theirs.json.items.every((r) => r.branch_id === IDS.pointLoma));
  // The cursor walks every row once.
  const first = await call(IDS.controller, "GET", "/api/v1/staff/approvals?status=PENDING&limit=2");
  assert.equal(first.json.items.length, 2);
  assert.ok(typeof first.json.next_cursor === "string");
  const second = await call(IDS.controller, "GET", `/api/v1/staff/approvals?status=PENDING&limit=2&cursor=${first.json.next_cursor}`);
  assert.equal(second.json.items.length, 2);
  assert.ok(second.json.items.every((r) => !first.json.items.some((f) => f.id === r.id)));
});

test("the journals, a journal, the trial balance and the branches answer the contract's shapes", async () => {
  await reset();
  const list = await call(IDS.controller, "GET", "/api/v1/gl/journals");
  assert.equal(list.status, 200);
  holds("GlJournalPage", list.json);
  assert.ok(list.json.items.length >= 6);
  const one = await call(IDS.controller, "GET", `/api/v1/gl/journals/${IDS.j1}`);
  assert.equal(one.status, 200);
  holds("GlJournal", one.json);
  assert.equal(one.json.derived_status, "PENDING_APPROVAL");
  const tb = await call(IDS.controller, "GET", "/api/v1/gl/trial-balance");
  assert.equal(tb.status, 200);
  holds("GlTrialBalanceResult", tb.json);
  assert.equal(tb.json.balance_cents, 0);
  const branches = await call(IDS.controller, "GET", "/api/v1/branches");
  assert.equal(branches.status, 200);
  branches.json.forEach((b) => holds("Branch", b));
});

test("the late posted filter lists the journals that carry a closed month's date and landed in an open one", async () => {
  await reset();
  const all = await call(IDS.controller, "GET", "/api/v1/gl/journals");
  holds("GlJournalPage", all.json);
  assert.ok(
    all.json.items.every((j) => typeof j.late_posted === "boolean"),
    "every journal carries the marker the core derives on read",
  );
  const late = await call(IDS.controller, "GET", "/api/v1/gl/journals?late_posted=true");
  assert.equal(late.status, 200);
  holds("GlJournalPage", late.json);
  assert.ok(late.json.items.length >= 1, "the stub has no late posted journal to list");
  assert.ok(late.json.items.every((j) => j.late_posted === true));
  // Each one carries a document date that is not in the month it landed in.
  const periods = (await call(IDS.controller, "GET", "/api/v1/gl/periods")).json.items;
  late.json.items.forEach((j) => {
    const period = periods.find((p) => p.id === j.period_id);
    assert.ok(period !== undefined, `the period ${j.period_id} is not on the list`);
    assert.ok(
      Number(j.posted_at.slice(0, 4)) !== period.year || Number(j.posted_at.slice(5, 7)) !== period.month,
      `${j.id} is marked late posted but its date falls in the month it landed in`,
    );
  });
  // The filter that is neither true nor false is refused in the core's words.
  refused(await call(IDS.controller, "GET", "/api/v1/gl/journals?late_posted=maybe"), 400, "late_posted must be true or false");
});

test("the controller decides the floor request and the parked journal, and the trial balance carries the journal", async () => {
  await reset();
  const before = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance")).json;
  const floor = await decide(IDS.controller, IDS.fr1, true);
  assert.equal(floor.status, 200);
  holds("IdentityStatusResponse", floor.json);
  const posted = await decide(IDS.controller, IDS.j1Request, true);
  assert.equal(posted.status, 200);
  const journal = (await call(IDS.controller, "GET", `/api/v1/gl/journals/${IDS.j1}`)).json;
  assert.equal(journal.status, "POSTED");
  assert.equal(journal.approved_by, IDS.controller);
  holds("GlJournal", journal);
  const after = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance")).json;
  assert.equal(after.total_debit_cents - before.total_debit_cents, 750000);
  assert.equal(after.total_credit_cents - before.total_credit_cents, 750000);
  assert.equal(after.balance_cents, 0);
  refused(await decide(IDS.controller, IDS.j1Request, true), 409, "request already");
});

test("the refusals are the core's sentences, in the core's order", async () => {
  await reset();
  refused(await decide(IDS.controller, IDS.j2Request, true), 403, "separation of duties: you cannot approve your own request");
  refused(await decide(IDS.controller, IDS.j2Request, false), 403, "separation of duties: you cannot approve your own request");
  // The branch Sales manager filed J1 itself, so its own is refused by the four eyes rule first; a freight
  // raise it did not file is refused by the policy's approver set.
  refused(await decide(IDS.manager, IDS.j1Request, true), 403, "separation of duties: you cannot approve your own request");
  refused(await decide(IDS.manager, IDS.freight2, true), 403, "not authorized for");
  refused(await decide(IDS.controller, "not-a-uuid", true), 400, "invalid approval id");
  const own = (await call(IDS.controller, "GET", `/api/v1/gl/journals/${IDS.j2}`)).json;
  assert.equal(own.status, "DRAFT");
  assert.equal(own.derived_status, "PENDING_APPROVAL");
});

// ---- the other seventeen routes: chart, journals, recurring, periods, checks, policy, export ----------

const BOOKS_MONTH = 9;
const line = (account_code, debit_cents, credit_cents, memo = "") => ({ account_code, debit_cents, credit_cents, memo });
const fresh = { branch_id: IDS.pointLoma, memo: "Freight accrual", source: "MANUAL" };

test("the chart of accounts answers the contract's page, creates an account and refuses in the core's words and order", async () => {
  await reset();
  const list = await call(IDS.controller, "GET", "/api/v1/gl/accounts");
  assert.equal(list.status, 200);
  holds("GlAccountPage", list.json);
  assert.ok(list.json.items.some((a) => a.code === "1000" && a.is_active));
  const made = await call(IDS.controller, "POST", "/api/v1/gl/accounts", { code: "6100", name: "Freight out", account_type: "expense", normal_balance: "debit", parent_id: "" });
  assert.equal(made.status, 201);
  holds("GlAccount", made.json);
  assert.equal(made.json.account_type, "EXPENSE");
  assert.equal(made.json.normal_balance, "DEBIT");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/accounts", { code: "6100", name: "Again", account_type: "EXPENSE", normal_balance: "DEBIT", parent_id: "" }), 409, "already exists");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/accounts", { code: " ", name: "x", account_type: "EXPENSE", normal_balance: "DEBIT", parent_id: "" }), 400, "gl: account code and name are required");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/accounts", { code: "6200", name: "x", account_type: "INCOME", normal_balance: "DEBIT", parent_id: "" }), 400, "gl: account_type must be one of ASSET/LIABILITY/EQUITY/REVENUE/EXPENSE");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/accounts", { code: "6200", name: "x", account_type: "EXPENSE", normal_balance: "BOTH", parent_id: "" }), 400, "gl: normal_balance must be DEBIT or CREDIT");
  const off = await call(IDS.controller, "POST", "/api/v1/gl/accounts/6100/deactivate");
  assert.equal(off.status, 200);
  holds("GlAccount", off.json);
  assert.equal(off.json.is_active, false);
  refused(await call(IDS.controller, "POST", "/api/v1/gl/accounts/9999/deactivate"), 404, "gl: not found");
  // 1300 sits on the parked insurance journal, so it cannot be deactivated while that is a draft.
  refused(await call(IDS.controller, "POST", "/api/v1/gl/accounts/1300/deactivate"), 409, "gl: account is referenced by DRAFT journal(s); cannot deactivate");
  // 1100 is free of drafts, but the seeded posting rules point at it (FW1-09a): the refusal names them.
  const guarded = await call(IDS.controller, "POST", "/api/v1/gl/accounts/1100/deactivate");
  refused(guarded, 409, "need it; deactivating it would stop those postings");
  assert.ok(guarded.json.error.includes("the posting rules invoice.finalized (ar), payment.bounced (ar), payment.received (ar), payment.reversed (ar)"), guarded.json.error);
});

test("a journal posts below the threshold, parks at or over it, and every refusal is the core's sentence in the core's order", async () => {
  await reset();
  const small = await call(IDS.controller, "POST", "/api/v1/gl/journals", { ...fresh, lines: [line("5200", 12000, 0, "Freight"), line("1000", 0, 12000)] });
  assert.equal(small.status, 201);
  holds("GlJournal", small.json);
  assert.equal(small.json.status, "POSTED");
  assert.equal(small.json.derived_status, "POSTED");
  assert.equal(small.json.entry_no, 7);
  const big = await call(IDS.controller, "POST", "/api/v1/gl/journals", { ...fresh, lines: [line("5200", 500000, 0), line("1000", 0, 500000)] });
  assert.equal(big.status, 201);
  holds("GlJournal", big.json);
  assert.equal(big.json.status, "DRAFT");
  assert.equal(big.json.derived_status, "PENDING_APPROVAL");
  const queue = (await call(IDS.controller, "GET", "/api/v1/staff/approvals?status=PENDING")).json.items;
  assert.ok(queue.some((r) => r.subject_ref === big.json.id && r.policy_type === "JE_OVER_THRESHOLD" && r.amount_cents === 500000));
  const body = (lines, extra = {}) => ({ ...fresh, lines, ...extra });
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("5200", 100, 0)])), 400, "gl: a journal needs at least two lines");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("5200", 100, 100), line("1000", 0, 100)])), 400, "gl: each line must have exactly one of debit_cents / credit_cents > 0");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("", 100, 0), line("1000", 0, 100)])), 400, "gl: each line needs an account_code");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("5200", 100, 0), line("1000", 0, 90)])), 422, "gl: journal is unbalanced");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("5200", 500000, 0), line("1000", 0, 500000)], { source: "ADJUSTMENT" })), 400, "gl: only a manual journal can be parked for approval at or over the approval threshold");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("5200", 500000, 0), line("1000", 0, 500000)], { branch_id: "" })), 400, "gl: a journal at or over the approval threshold needs a branch so its approval request can be filed; choose a branch");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", body([line("7777", 100, 0), line("1000", 0, 100)])), 422, "gl: unknown account code");
  // A role below the manage level gets the gate's shared body, never a sentence.
  refused(await call(IDS.payRec, "POST", "/api/v1/gl/journals", body([line("5200", 100, 0), line("1000", 0, 100)])), 403, "Forbidden");
});

test("void, reverse and approve answer the contract's shapes and refuse in the core's words", async () => {
  await reset();
  const posted = IDS.opening;
  const rev = await call(IDS.controller, "POST", `/api/v1/gl/journals/${posted}/reverse`);
  assert.equal(rev.status, 201);
  holds("GlJournal", rev.json);
  assert.equal(rev.json.source, "ADJUSTMENT");
  assert.equal(rev.json.reverses_journal_id, posted);
  assert.equal(rev.json.memo, "Reversal of entry #1");
  const original = (await call(IDS.controller, "GET", `/api/v1/gl/journals/${posted}`)).json;
  assert.deepEqual(rev.json.lines.map((l) => [l.account_code, l.debit_cents, l.credit_cents]), original.lines.map((l) => [l.account_code, l.credit_cents, l.debit_cents]));
  // The guards (FW1-09a): a journal a module posted is not reversed from the ledger and names its module
  // (the till's journal files under its topic key), and a journal is reversed once.
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.till}/reverse`), 409, "not by a person in the ledger; it cannot be reversed from here");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${posted}/reverse`), 409, "was already reversed; a journal can be reversed once");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.j1}/reverse`), 409, "gl: only a POSTED journal can be reversed");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${posted}/void`), 409, "gl: only a DRAFT journal can be voided (POSTED journals must be reversed)");
  const voided = await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.j2}/void`);
  assert.equal(voided.status, 200);
  holds("GlJournal", voided.json);
  assert.equal(voided.json.derived_status, "VOID");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.j2}/approve`), 409, ", not DRAFT, cannot approve");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.j1}/approve`), 409, "gl: this journal has a pending approval request; decide it from the approvals queue, which posts it");
  refused(await call(IDS.manager, "POST", `/api/v1/gl/journals/${IDS.j1}/approve`), 403, "Forbidden");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals/not-a-uuid/void"), 400, "invalid id");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals/7a1e0000-0000-4000-9000-0000000000ff/approve"), 404, "gl: not found");
  // The approval request declined: the journal route says so in the core's words.
  await decide(IDS.controller, IDS.j1Request, false);
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.j1}/approve`), 409, "was rejected in the approvals queue; it cannot be posted from here");
});

test("the controller's own parked journal is refused to its maker by the four eyes rule before the queue is asked", async () => {
  await reset();
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${IDS.j2}/approve`), 403, "an approver must differ from the journal's creator (four-eyes)");
});

test("recurring templates list as a page and run into a draft journal", async () => {
  await reset();
  const list = await call(IDS.controller, "GET", "/api/v1/gl/recurring");
  assert.equal(list.status, 200);
  holds("GlRecurringTemplatePage", list.json);
  assert.equal(list.json.items.length, 2);
  const small = await call(IDS.controller, "POST", `/api/v1/gl/recurring/${list.json.items[0].id}/run`);
  assert.equal(small.status, 201);
  holds("GlJournal", small.json);
  assert.equal(small.json.status, "DRAFT");
  assert.equal(small.json.source_ref_id, list.json.items[0].id);
  assert.equal(small.json.derived_status, "DRAFT");
  const large = await call(IDS.controller, "POST", `/api/v1/gl/recurring/${list.json.items[1].id}/run`);
  assert.equal(large.json.derived_status, "PENDING_APPROVAL");
  assert.equal(large.json.memo, "Recurring: Insurance prepaid release");
  // The run's maker is the caller, so the maker is refused by the four eyes rule before the queue is asked.
  refused(await call(IDS.controller, "POST", `/api/v1/gl/journals/${large.json.id}/approve`), 403, "an approver must differ from the journal's creator (four-eyes)");
  refused(await call(IDS.payRec, "POST", `/api/v1/gl/recurring/${list.json.items[0].id}/run`), 403, "Forbidden");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/recurring/6c000000-0000-4000-9000-0000000000ff/run"), 404, "gl: not found");
});

test("the periods list, close, reopen and the year end close answer the contract and refuse in the core's words", async () => {
  await reset();
  const list = await call(IDS.controller, "GET", "/api/v1/gl/periods");
  assert.equal(list.status, 200);
  holds("GlPeriodPage", list.json);
  assert.equal(list.json.items.length, 12);
  const open = list.json.items.find((p) => p.status === "OPEN");
  const sealed = list.json.items.find((p) => p.status === "CLOSED");
  const closed = await call(IDS.controller, "POST", `/api/v1/gl/periods/${open.id}/close`, { reason: "Month done" });
  assert.equal(closed.status, 200);
  holds("GlPeriod", closed.json);
  assert.equal(closed.json.status, "CLOSED");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/periods/${open.id}/close`, {}), 409, "gl: period is not OPEN");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/periods/${open.id}/reopen`, { reason: "  " }), 422, "gl: a reason is required");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/periods/${sealed.id}/close`, {}), 409, "gl: period is not OPEN");
  const reopened = await call(IDS.controller, "POST", `/api/v1/gl/periods/${open.id}/reopen`, { reason: "A late invoice" });
  assert.equal(reopened.status, 200);
  assert.equal(reopened.json.status, "OPEN");
  refused(await call(IDS.controller, "POST", `/api/v1/gl/periods/${open.id}/reopen`, { reason: "again" }), 409, "gl: period is not CLOSED");
  refused(await call(IDS.payRec, "POST", `/api/v1/gl/periods/${sealed.id}/reopen`, { reason: "x" }), 403, "Forbidden");
  refused(await call(IDS.controller, "POST", "/api/v1/gl/periods/nope/close", {}), 400, "invalid id");
  // A closed period refuses a journal dated into it, in its own words.
  await call(IDS.controller, "POST", `/api/v1/gl/periods/${list.json.items.find((p) => p.month === BOOKS_MONTH).id}/close`, {});
  refused(await call(IDS.controller, "POST", "/api/v1/gl/journals", { ...fresh, lines: [line("5200", 100, 0), line("1000", 0, 100)] }), 409, "gl: fiscal period is CLOSED");
});

test("the next fiscal year opens for the Controller with a reason and refuses in the core's words", async () => {
  await reset();
  refused(await call(IDS.controller, "POST", "/api/v1/gl/periods/open-year", { reason: "   " }), 422, "gl: a reason is required");
  refused(await call(IDS.payRec, "POST", "/api/v1/gl/periods/open-year", { reason: "x" }), 403, "Forbidden");
  const opened = await call(IDS.controller, "POST", "/api/v1/gl/periods/open-year", { reason: "The year is set up early" });
  assert.equal(opened.status, 200);
  holds("GlYearOpenResult", opened.json);
  assert.equal(opened.json.fiscal_year, 2027);
  // The year joins the periods with its twelve months, open for posting.
  const periods = (await call(IDS.controller, "GET", "/api/v1/gl/periods")).json.items;
  const next = periods.filter((p) => p.year === 2027);
  assert.equal(next.length, 12);
  assert.ok(next.every((p) => p.status === "OPEN"));
  // The core's roll forward is a year at a time (core/internal/gl/repository.go EnsureNextFiscalYear makes
  // the year after the latest that exists), so asking again answers the year after the one just made.
  const again = await call(IDS.controller, "POST", "/api/v1/gl/periods/open-year", { reason: "And again" });
  assert.equal(again.status, 200);
  assert.equal(again.json.fiscal_year, 2028);
  assert.equal((await call(IDS.controller, "GET", "/api/v1/gl/periods")).json.items.filter((p) => p.year === 2027).length, 12);
});

test("the year end close sweeps net income into retained earnings and seals the year", async () => {
  await reset();
  refused(await call(IDS.controller, "POST", "/api/v1/gl/periods/close-year", { year: 0, reason: "x" }), 400, "gl: year is required");
  refused(await call(IDS.payRec, "POST", "/api/v1/gl/periods/close-year", { year: 2026, reason: "x" }), 403, "Forbidden");
  const done = await call(IDS.controller, "POST", "/api/v1/gl/periods/close-year", { year: 2026, reason: "year-end close FY2026" });
  assert.equal(done.status, 200);
  holds("GlYearCloseResult", done.json);
  assert.equal(done.json.fiscal_year, 2026);
  assert.equal(done.json.periods_closed, true);
  assert.ok(typeof done.json.closing_journal_id === "string");
  // The posted revenue of $12,000.00 plus the till's $25.00 of over and short credited to expense.
  assert.equal(done.json.net_income_cents, 1202500);
  const closing = (await call(IDS.controller, "GET", `/api/v1/gl/journals/${done.json.closing_journal_id}`)).json;
  holds("GlJournal", closing);
  assert.equal(closing.source, "CLOSING");
  assert.ok(closing.lines.some((l) => l.account_code === "3900"));
  const periods = (await call(IDS.controller, "GET", "/api/v1/gl/periods")).json.items;
  assert.ok(periods.every((p) => p.status === "CLOSED"));
});

test("the legacy import check is served only where the surface config names a source", async () => {
  await reset();
  const config = await call(IDS.controller, "GET", "/api/v1/surfaces/money-desk/config");
  assert.equal(config.status, 200);
  holds("SettingsSurfaceConfig", config.json);
  assert.equal(config.json.legacy_import_source, "bistrack");
  const off = await fetch(`${base}/_stub/legacy-import`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ source: "" }) });
  assert.equal(off.status, 200);
  const bare = await call(IDS.controller, "GET", "/api/v1/surfaces/money-desk/config");
  assert.equal(bare.status, 200);
  holds("SettingsSurfaceConfig", bare.json);
  assert.equal("legacy_import_source" in bare.json, false, "a dealer with no source serves no field");
  refused(await call(IDS.controller, "GET", "/api/v1/gl/tieout/bistrack?year=2026&month=6"), 404, "the legacy import check is not enabled for this dealer");
  await reset();
  const back = await call(IDS.controller, "GET", "/api/v1/gl/tieout/bistrack?year=2026&month=6");
  assert.equal(back.status, 200, "a reset puts the first dealer's source back");
});

test("the policy, the receivables tie out, the BisTrack check and the export answer the contract", async () => {
  await reset();
  const policy = await call(IDS.controller, "GET", "/api/v1/gl/policy");
  assert.equal(policy.status, 200);
  holds("GlPolicy", policy.json);
  assert.equal(policy.json.je_approval_threshold_cents, 500000);
  refused(await call(IDS.controller, "GET", "/api/v1/gl/policy?branch_id=nope"), 400, "invalid branch_id");
  const tie = await call(IDS.controller, "GET", "/api/v1/gl/tie-out");
  assert.equal(tie.status, 200);
  holds("GlTieoutStatePage", tie.json);
  assert.equal(tie.json.items[0].subledger, "AR");
  assert.equal(tie.json.items[0].in_tie, tie.json.items[0].delta_cents === 0);
  const bis = await call(IDS.controller, "GET", "/api/v1/gl/tieout/bistrack?year=2026&month=6&tolerance_cents=0");
  assert.equal(bis.status, 200);
  holds("GlBisTrackTieoutReport", bis.json);
  assert.equal(bis.json.is_synthetic, true);
  refused(await call(IDS.controller, "GET", "/api/v1/gl/tieout/bistrack?year=2026"), 400, "gl: bistrack tie-out requires a fiscal month 1-12");
  refused(await call(IDS.controller, "GET", "/api/v1/gl/tieout/bistrack?year=2025&month=6"), 400, "gl: bistrack snapshot is FY");
  refused(await call(IDS.controller, "GET", "/api/v1/gl/tieout/bistrack?year=2026&month=7"), 400, "gl: bistrack snapshot is month");
  refused(await call(IDS.payRec, "GET", "/api/v1/gl/tieout/bistrack?year=2026&month=6"), 403, "Forbidden");
  const exp = await call(IDS.controller, "POST", "/api/v1/gl/export", { year: 2026, month: 0, branch_id: "" });
  assert.equal(exp.status, 200);
  holds("GlExportResult", exp.json);
  assert.equal(exp.json.format, "GP_PARITY");
  assert.ok(exp.json.content.startsWith("# PARITY DRAFT"));
  assert.ok(exp.json.content.includes("entry_no,posted_at,account_code,account_name,debit,credit,memo"));
  refused(await call(IDS.controller, "POST", "/api/v1/gl/export", { year: 0, month: 0, branch_id: "" }), 400, "gl: export requires a fiscal_year");
  refused(await call(IDS.payRec, "POST", "/api/v1/gl/export", { year: 2026, month: 0, branch_id: "" }), 403, "Forbidden");
});

test("the trial balance takes a month and a year, and a month shows that period's activity alone", async () => {
  await reset();
  const all = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance?year=2026")).json;
  holds("GlTrialBalanceResult", all);
  const august = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance?year=2026&month=8")).json;
  holds("GlTrialBalanceResult", august);
  assert.equal(august.month, 8);
  const september = (await call(IDS.controller, "GET", "/api/v1/gl/trial-balance?year=2026&month=9")).json;
  assert.equal(september.rows.length, 0);
  refused(await call(IDS.controller, "GET", "/api/v1/gl/trial-balance?branch_id=nope"), 400, "invalid branch_id");
});

test("the posting queue answers the Controller's list and the banner's counts, and the list is the Controller's alone", async () => {
  await reset();
  const empty = await call(IDS.controller, "GET", "/api/v1/gl/posting-queue/count");
  assert.equal(empty.status, 200);
  holds("GlPostingQueueCounts", empty.json);
  assert.deepEqual(empty.json, { waiting: 0, dead: 0 });
  const items = [
    {
      event_id: "d0560000-0000-4000-9000-0000000000f1",
      topic: "payment.received",
      document: "d0560000-0000-4000-9000-0000000000f2",
      status: "PENDING",
      attempts: 2,
      last_error: "gl: no fiscal period for 2027-01",
      occurred_at: "2026-03-12T10:00:00Z",
    },
    {
      event_id: "d0560000-0000-4000-9000-0000000000f3",
      topic: "ar.invoice.finalized",
      document: "d0560000-0000-4000-9000-0000000000f4",
      status: "DEAD",
      attempts: 5,
      last_error: "gl: account \"2200\" is inactive; cannot post",
      occurred_at: "2026-03-12T11:00:00Z",
    },
    {
      event_id: "d0560000-0000-4000-9000-0000000000f5",
      topic: "payment.received",
      document: "d0560000-0000-4000-9000-0000000000f6",
      status: "PENDING",
      attempts: 0,
      last_error: "",
      occurred_at: new Date().toISOString(),
    },
  ];
  const set = await fetch(`${base}/_stub/posting-queue`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ items }) });
  assert.equal(set.status, 200);
  const list = await call(IDS.controller, "GET", "/api/v1/gl/posting-queue");
  assert.equal(list.status, 200);
  holds("GlPostingQueuePage", list.json);
  assert.deepEqual(list.json.items, items);
  const counts = await call(IDS.controller, "GET", "/api/v1/gl/posting-queue/count");
  assert.equal(counts.status, 200);
  holds("GlPostingQueueCounts", counts.json);
  assert.deepEqual(counts.json, { waiting: 1, dead: 1 });
  refused(await call(IDS.generalManager, "GET", "/api/v1/gl/posting-queue"), 403, "gl: only the Financial Controller may read the posting queue");
  refused(await call(IDS.manager, "GET", "/api/v1/gl/posting-queue"), 403, "Forbidden");
  const gmCounts = await call(IDS.generalManager, "GET", "/api/v1/gl/posting-queue/count");
  assert.equal(gmCounts.status, 200);
  assert.deepEqual(gmCounts.json, { waiting: 1, dead: 1 });
  refused(await call(IDS.manager, "GET", "/api/v1/gl/posting-queue/count"), 403, "gl: caller cannot access this branch");
  await reset();
});
