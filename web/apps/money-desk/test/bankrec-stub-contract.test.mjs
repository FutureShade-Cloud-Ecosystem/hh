// The bank reconciliation slice's stub against the contract and against the core, field by field. A stub that
// drifts from the server makes every browser leg a flattering lie, so this test starts the stub, makes every call
// the slice makes, and holds each answer to the generated wire descriptors: required fields present, types and
// vocabularies right, and no field the contract does not declare. Then each refusal is held to the core's words:
// the status, the envelope, and the sentence itself, which must appear in the core's source, and in the core's
// order. A refusal the stub invented fails here.
//
// Two places where the core and the contract disagree are pinned here so they cannot move in silence: the core
// answers four lists (accounts, matches, rules, reconciliations) with a bare array where the contract says a page,
// and a rule's spec is typed by its kind alone while the core stores and returns the whole object.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import { IDS, OPENING_CENTS, statementMonth } from "./stub/bankrec-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["bankrec", join("platform", "authz"), join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");
const handlerSource = readFileSync(join(coreInternal, "bankrec", "handler.go"), "utf8");

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

function tokenFor(sub) {
  const part = (v) => Buffer.from(JSON.stringify(v), "utf8").toString("base64url");
  return `${part({ alg: "ES256" })}.${part({ sub, exp: 4102444800 })}.c3R1Yg`;
}

const SALES = "de110000-0000-4000-9000-000000000001";

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

const ctl = (method, path, body) => call(IDS.controller, method, path, body);
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

const { year: Y, month: M } = statementMonth();
const period = `${Y}-${String(M).padStart(2, "0")}`;
const CLOSING = OPENING_CENTS + 128_000 + 245_050 + 98_000 + 115_000 + 64_025 - 4_500 + 30_000;

const lineByRef = async (description) => {
  const page = (await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}&limit=50`)).json;
  return page.items.find((l) => l.description === description);
};

test("the accounts answer a bare array, as the core writes them, and each account fits the contract", async () => {
  await reset();
  const res = await ctl("GET", "/api/v1/bankrec/accounts");
  assert.equal(res.status, 200);
  assert.ok(Array.isArray(res.json), "the core's handler writes the array itself");
  assert.ok(handlerSource.includes("httpx.WriteJSON(w, http.StatusOK, accts)"), "the core's accounts handler no longer writes a bare array: bring the stub and the contract in step");
  holds("BankRecAccountList", res.json);
  assert.equal(res.json.length, 3);
  const operating = res.json.find((a) => a.id === IDS.operating);
  assert.equal(operating.unmatched_lines, 7);
  assert.equal(operating.tracking_only, false);
  assert.ok(res.json.filter((a) => a.account_type === "CREDIT_CARD").every((a) => a.tracking_only === true));
});

test("the statement lines answer the contract's page, newest first, and the cursor walks every line once", async () => {
  await reset();
  refused(await ctl("GET", "/api/v1/bankrec/lines"), 400, "bank_account_id is required");
  const all = await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}`);
  assert.equal(all.status, 200);
  holds("BankRecStatementLinePage", all.json);
  assert.equal(all.json.items.length, 7);
  const dates = all.json.items.map((l) => l.txn_date);
  assert.deepEqual(dates, [...dates].sort().reverse(), "newest first");
  const seen = [];
  let cursor = "";
  for (let i = 0; i < 10; i += 1) {
    const page = (await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}&limit=3${cursor === "" ? "" : `&cursor=${encodeURIComponent(cursor)}`}`)).json;
    holds("BankRecStatementLinePage", page);
    seen.push(...page.items.map((l) => l.id));
    cursor = page.next_cursor ?? "";
    if (cursor === "") break;
  }
  assert.equal(seen.length, 7);
  assert.equal(new Set(seen).size, 7);
  const unmatched = await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}&status=MATCHED`);
  assert.equal(unmatched.json.items.length, 0);
  const cards = await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.fuelCard}`);
  assert.equal(cards.json.items.length, 0);
});

test("a statement imports, a re-import inserts nothing, and a file the parser cannot read is a failed import in the core's envelope", async () => {
  await reset();
  const csv = "Date,Description,Amount,Reference\n2031-06-01,YARD SUPPLY CARD PAYMENT,\"(1,200.50)\",REF-1\n2031-06-02,CUSTOMER DEPOSIT,500.00,\n";
  const first = await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, profile_code: "rbc-default", filename: "june.csv", content: csv });
  assert.equal(first.status, 201);
  holds("BankRecImportResult", first.json);
  assert.deepEqual([first.json.status, first.json.parsed, first.json.inserted, first.json.duplicates], ["IMPORTED", 2, 2, 0]);
  const again = await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, profile_code: "rbc-default", filename: "june.csv", content: csv });
  assert.equal(again.status, 201);
  assert.deepEqual([again.json.parsed, again.json.inserted, again.json.duplicates], [2, 0, 2]);
  const lines = (await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}&limit=100`)).json.items;
  assert.equal(lines.find((l) => l.description === "YARD SUPPLY CARD PAYMENT").amount_cents, -120_050, "a bracketed amount is a debit");

  const noColumn = await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, profile_code: "rbc-default", filename: "bad.csv", content: "Date,Description,Reference\n2031-06-01,X,Y\n" });
  assert.equal(noColumn.status, 422);
  holds("BankRecImportResult", noColumn.json);
  assert.equal(noColumn.json.status, "FAILED");
  assert.ok(noColumn.json.error.includes('csv is missing the mapped column "Amount"'));
  assert.ok(coreSource.includes("csv is missing the mapped column"), "the parser's sentence is the core's");
  assert.ok(coreSource.includes("unprocessable(\"bankrec: statement parse failed: %v\", perr)"));

  const badDate = await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, profile_code: "rbc-default", filename: "bad.csv", content: "Date,Description,Amount,Reference\nJune 4,X,1.00,\n" });
  assert.equal(badDate.status, 422);
  assert.match(badDate.json.error, /csv row 1: bad date "June 4"/);
  assert.ok(coreSource.includes("csv row %d: bad date %q (want %q)"));
  const badAmount = await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, profile_code: "rbc-default", filename: "bad.csv", content: "Date,Description,Amount,Reference\n2031-06-04,X,12..5,\n" });
  assert.match(badAmount.json.error, /csv row 1: bad amount "12\.\.5"/);
  assert.ok(coreSource.includes("csv row %d: bad amount %q"));

  refused(await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: "", content: "x" }), 400, "bank_account_id is required");
  refused(await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, content: "  " }), 400, "content is required");
  refused(await ctl("POST", "/api/v1/bankrec/imports", { bank_account_id: "ba000000-0000-4000-8000-0000000000ff", content: "x" }), 404, "not found");
});

test("the auto match pairs the card, lodgement and batch lines by the rules, leaves the rest, and a second run finds nothing new", async () => {
  await reset();
  const run = await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  assert.equal(run.status, 200);
  holds("BankRecMatchSummary", run.json);
  assert.deepEqual([run.json.matched, run.json.unmatched], [4, 2]);
  const kinds = run.json.matches.map((m) => `${m.target_type}:${m.method}`).sort();
  assert.deepEqual(kinds, ["LODGEMENT:AUTO", "PAYMENT:AUTO", "PAYMENT:AUTO", "PAYMENT:AUTO"]);
  assert.ok(run.json.matches.every((m) => typeof m.journal_id === "string" && typeof m.rule_id === "string"));
  const again = await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  assert.deepEqual([again.json.matched, again.json.unmatched], [0, 2]);
  const window = await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating, from: "2031-01-01", to: "2031-01-02" });
  assert.deepEqual([window.json.matched, window.json.unmatched], [0, 0]);
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "auto" }), 400, "bank_account_id is required");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating, from: "soon" }), 400, "bad 'from' date");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "sideways" }), 400, "mode must be 'auto' or 'manual'");
});

test("a manual match pairs a line to a payment or a journal, the refusals come in the core's order, and the history keeps every pairing", async () => {
  await reset();
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const fee = await lineByRef("MONTHLY SERVICE FEE");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", target_type: "PAYMENT" }), 400, "statement_line_id is required");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "CHEQUE" }), 400, "target_type must be PAYMENT, LODGEMENT, JOURNAL or GROUP");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: "b1000000-0000-4000-8000-0000000000ff", target_type: "JOURNAL" }), 404, "not found");
  const gone = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: "b9000000-0000-4000-8000-0000000000ff" });
  // The core's claim sentence: the stable words (found in the core's source), then the NUMBER of the payment that did
  // not move and the count ("payment already claimed, reversed or not RECEIVED: <payment no> (claimed N of M)").
  refused(gone, 409, "payment already claimed, reversed or not RECEIVED");
  assert.ok(gone.json.error.includes(": a payment that cannot be found (claimed 0 of 1)"), `the claim sentence names the payment in words and the count: ${gone.json.error}`);
  assert.ok(!gone.json.error.includes("b9000000-"), `the claim sentence never carries an id: ${gone.json.error}`);
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "UNMATCHED", "a refused claim leaves the line unmatched");
  const paid = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  assert.equal(paid.status, 201);
  holds("BankRecMatch", paid.json);
  assert.equal(paid.json.method, "MANUAL");
  assert.equal(paid.json.amount_cents, 30_000);
  assert.ok(typeof paid.json.journal_id === "string", "a payment target posts its clearing journal");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "JOURNAL" }), 409, "not UNMATCHED");
  // A payment already deposited is refused by its number, never its id.
  const again2 = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  refused(again2, 409, "payment already claimed");
  assert.ok(again2.json.error.includes(": PAY-000104 (claimed 0 of 1)") && !again2.json.error.includes(IDS.paymentWire), `the sentence names the number, not the id: ${again2.json.error}`);
  const booked = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000004" });
  assert.equal(booked.status, 201);
  holds("BankRecMatch", booked.json);
  assert.equal(booked.json.journal_id, undefined, "a journal target posts nothing new");
  assert.equal(booked.json.amount_cents, -4_500);
  const history = await ctl("GET", `/api/v1/bankrec/matches?statement_line_id=${wire.id}`);
  assert.equal(history.status, 200);
  assert.ok(Array.isArray(history.json), "the core's handler writes the array itself");
  assert.ok(handlerSource.includes("httpx.WriteJSON(w, http.StatusOK, matches)"));
  holds("BankRecMatchList", history.json);
  assert.equal(history.json.length, 1);
  refused(await ctl("GET", "/api/v1/bankrec/matches"), 400, "statement_line_id is required");
});

test("an undo returns the line and the payment, keeps the history, and a second undo is refused", async () => {
  await reset();
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const paid = (await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire })).json;
  const undone = await ctl("POST", "/api/v1/bankrec/unmatch", { match_id: paid.id });
  assert.equal(undone.status, 200);
  holds("BankRecUnmatchResult", undone.json);
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "UNMATCHED");
  const history = (await ctl("GET", `/api/v1/bankrec/matches?statement_line_id=${wire.id}`)).json;
  assert.equal(history.length, 1);
  assert.ok(typeof history[0].unmatched_at === "string", "the undone match stays in the history, stamped");
  refused(await ctl("POST", "/api/v1/bankrec/unmatch", { match_id: paid.id }), 409, "is not live (already unmatched)");
  refused(await ctl("POST", "/api/v1/bankrec/unmatch", {}), 400, "match_id is required");
  // The released payment can be claimed again by a corrective match.
  const again = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  assert.equal(again.status, 201);
  assert.equal((await ctl("GET", `/api/v1/bankrec/matches?statement_line_id=${wire.id}`)).json.length, 2);
  // A deposit whose payment was reversed cannot be unmatched: refused in plain words, and nothing changes.
  await fetch(`${base}/_stub/reverse-payment`, { method: "POST", body: JSON.stringify({ payment_id: IDS.paymentWire }) });
  refused(await ctl("POST", "/api/v1/bankrec/unmatch", { match_id: again.json.id }), 409, "cannot be unmatched");
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "MATCHED", "a refused unmatch leaves the line matched");
});

test("the rules answer a bare array, a new version retires the old, and the spec is the core's whole object", async () => {
  await reset();
  const list = await ctl("GET", "/api/v1/bankrec/rules");
  assert.equal(list.status, 200);
  assert.ok(Array.isArray(list.json));
  assert.ok(handlerSource.includes("httpx.WriteJSON(w, http.StatusOK, items)"));
  holds("BankRecMatchRuleList", list.json);
  assert.equal(list.json.length, 3);
  assert.ok(list.json.every((r) => r.status === "APPROVED" && r.version === 1));
  assert.equal(list.json.find((r) => r.code === "run-settlement-fuzzy").spec.allow_fee_split, true);
  const next = await ctl("POST", "/api/v1/bankrec/rules", { code: "run-settlement-fuzzy", name: "Batch settlement, wider window", priority: 30, spec: { kind: "descriptor_fuzzy", target: "PAYMENT", descriptor_ilike: "%RUN%", date_window_days: 5, allow_fee_split: true, fee_account_code: "6300" } });
  assert.equal(next.status, 201);
  holds("BankRecMatchRule", next.json);
  assert.equal(next.json.version, 2);
  assert.equal(next.json.supersedes_rule_id, IDS.ruleBatch);
  const all = (await ctl("GET", "/api/v1/bankrec/rules")).json;
  assert.equal(all.find((r) => r.id === IDS.ruleBatch).status, "RETIRED");
  const live = (await ctl("GET", "/api/v1/bankrec/rules?live=true")).json;
  assert.equal(live.length, 3);
  assert.ok(live.every((r) => r.status === "APPROVED"));
  refused(await ctl("POST", "/api/v1/bankrec/rules", { name: "x", spec: { kind: "gateway_ref_exact" } }), 400, "rule code is required");
  refused(await ctl("POST", "/api/v1/bankrec/rules", { code: "x", name: "x", spec: {} }), 400, "rule spec is required");
  refused(await ctl("POST", "/api/v1/bankrec/rules", { code: "x", name: "x", spec: { kind: "guess" } }), 422, "spec.kind must be one of gateway_ref_exact, lodgement_ref_exact, descriptor_fuzzy");
});

test("a month opens with the ledger balance and the variance, a zero balance is replaced by the month's net, and a refresh follows the matching", async () => {
  await reset();
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: 13, statement_balance_cents: CLOSING }), 400, "valid year and month (1-12) are required");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { year: Y, month: M }), 400, "bank_account_id is required");
  const opened = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING });
  assert.equal(opened.status, 201);
  holds("BankRecReconciliation", opened.json);
  assert.equal(opened.json.status, "OPEN");
  assert.equal(opened.json.statement_balance_cents, CLOSING);
  assert.equal(opened.json.variance_cents, opened.json.statement_balance_cents - opened.json.gl_balance_cents);
  assert.equal(opened.json.variance_cents, 586_050 + 30_000, "before any match the ledger lacks the four auto lines and the wire");
  // The core's placeholder: a typed zero is replaced by the month's line net.
  const zero = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: 0 });
  assert.equal(zero.json.id, opened.json.id, "opening the same month again refreshes it");
  assert.equal(zero.json.statement_balance_cents, 675_575);
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  const stale = (await ctl("GET", `/api/v1/bankrec/reconciliations?bank_account_id=${IDS.operating}`)).json;
  assert.ok(Array.isArray(stale), "the core's handler writes the array itself");
  assert.ok(handlerSource.includes("httpx.WriteJSON(w, http.StatusOK, items)"));
  holds("BankRecReconciliationList", stale);
  assert.equal(stale[0].variance_cents, zero.json.variance_cents, "the stored variance is a snapshot until the month is opened again");
  const fresh = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING });
  assert.equal(fresh.json.variance_cents, 0, "the cheque and the fee were booked by journals before matching, so the ledger reaches the statement once the rest is matched");
});

test("a month signs off only with no unmatched line and a zero variance, in the core's order, and a signed off month is closed", async () => {
  await reset();
  const opened = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING })).json;
  refused(await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`), 422, "UNMATCHED line(s) remain for");
  const res = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`);
  assert.match(res.json.error, new RegExp(`^cannot complete, 7 UNMATCHED line\\(s\\) remain for ${period}$`));
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const cheque = await lineByRef("CHEQUE DEPOSIT");
  const fee = await lineByRef("MONTHLY SERVICE FEE");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000003" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000004" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  // Every line is matched but the stored variance is still the old snapshot.
  const snapshot = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`);
  refused(snapshot, 422, "must be zero)");
  assert.match(snapshot.json.error, /^cannot complete, variance is \d+ cents \(must be zero\)$/);
  const refreshed = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING })).json;
  assert.equal(refreshed.variance_cents, 0);
  const signed = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`);
  assert.equal(signed.status, 200);
  holds("BankRecReconciliation", signed.json);
  assert.equal(signed.json.status, "COMPLETED");
  assert.ok(typeof signed.json.completed_at === "string");
  refused(await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`), 409, "is already COMPLETED");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING }), 409, "is already COMPLETED");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations/not-a-uuid/complete"), 404, "not found");
});

test("the walls: any staff reads, a manager imports, matches, undoes and opens a month, only the controller approves a rule and signs off", async () => {
  await reset();
  assert.equal((await call(SALES, "GET", "/api/v1/bankrec/accounts")).status, 200);
  assert.equal((await call(SALES, "GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}`)).status, 200);
  refused(await call(SALES, "POST", "/api/v1/bankrec/imports", { bank_account_id: IDS.operating, content: "x" }), 403, "Forbidden");
  refused(await call(SALES, "POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating }), 403, "Forbidden");
  refused(await call(SALES, "POST", "/api/v1/bankrec/unmatch", { match_id: "x" }), 403, "Forbidden");
  refused(await call(SALES, "POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M }), 403, "Forbidden");
  const manager = await call(IDS.payrec, "POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING });
  assert.equal(manager.status, 201);
  refused(await call(IDS.payrec, "POST", `/api/v1/bankrec/reconciliations/${manager.json.id}/complete`), 403, "Forbidden");
  refused(await call(IDS.payrec, "POST", "/api/v1/bankrec/rules", { code: "x", name: "x", spec: { kind: "gateway_ref_exact" } }), 403, "Forbidden");
  assert.equal((await call(IDS.payrec, "POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating })).status, 200);
});
