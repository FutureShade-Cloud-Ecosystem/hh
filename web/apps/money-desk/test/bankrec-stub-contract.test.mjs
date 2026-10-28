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
import { IDS, statementMonth } from "./stub/bankrec-world.mjs";

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
// The statement's closing balance in the stub world: the balance the ledger holds at the month end, read from the
// ledger's own world ($25,025.00: the opening balances, the payment received and the till over/short). The same
// figure the cut over form shows for the account.
const CLOSING = 2_502_500;
// What the matching moves the ledger by: the four auto lines' nets reach 1000 ($1,280.00, $2,450.50, $980.00 and
// $1,150.00, the batch's $25.00 fee on its own account) and the wire's $300.00 with it.
const MATCHING_MOVE = 586_050 + 30_000;
// The ledger's balance once the matching has posted those clearing journals: the GL world's own journals plus
// the move. A statement that says this agrees with the ledger once the lines are matched.
const CLOSING_AFTER_MATCHING = CLOSING + MATCHING_MOVE;

/** A journal to the bank account through the ledger's own route, the shape the ledger's own payment journal has
 *  (DR 1000 / CR 1100): the ledger moves the way the core's ledger moves, never by a balance set by hand. */
async function postToBank(cents, direction) {
  const up = direction === "up";
  const res = await ctl("POST", "/api/v1/gl/journals", {
    source: "MANUAL",
    memo: up ? "a receipt on the account" : "the receipt unwinds",
    lines: [
      { account_code: "1000", debit_cents: up ? cents : 0, credit_cents: up ? 0 : cents },
      { account_code: "1100", credit_cents: up ? cents : 0, debit_cents: up ? 0 : cents },
    ],
  });
  assert.equal(res.status, 201);
  return res.json;
}

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

test("a match posts its clearing journal into the ledger: 1000 moves by the line's net", async () => {
  await reset();
  const lines = (await ctl("GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}&limit=50`)).json.items;
  // The ledger before any match: the GL world's own journals, read as the month's closing balance.
  const before = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING })).json;
  assert.equal(before.gl_balance_cents, 2_502_500, "the ledger before any match is the GL world's own journals");
  // The core's rule for the four lines the auto match pairs (core/internal/bankrec/matcher.go): a card settlement
  // is DR 1000 by the net paid in and CR 1010 by the gross claimed, with the fee on its own account; a cash
  // lodgement that carries no journal of its own is DR 1000 and CR 1020.
  const run = await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  assert.equal(run.json.matched, 4);
  const afterAuto = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING })).json;
  assert.equal(
    afterAuto.gl_balance_cents,
    2_502_500 + 128_000 + 245_050 + 98_000 + 115_000,
    "the four lines' nets reach 1000: $1,280.00, $2,450.50, $980.00 and $1,150.00 (the batch's $25.00 fee is its own account, never 1000's)",
  );
  // The wire, matched by hand: an EFT receipt clears 1020 (the core's manualJournal).
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const manual = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  assert.equal(manual.status, 201);
  const afterManual = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING })).json;
  assert.equal(afterManual.gl_balance_cents, 3_118_550, "the wire's $300.00 reaches 1000 too: the ledger reads $31,185.50");
  // The journals themselves, read from the ledger: each is dated on the line's day at 12:00 UTC (the core's
  // statementDocDate) and carries the legs the core's rule names.
  const legs = async (journalId, line) => {
    const res = await ctl("GET", `/api/v1/gl/journals/${journalId}`);
    assert.equal(res.status, 200);
    assert.equal(res.json.posted_at, `${line.txn_date.slice(0, 10)}T12:00:00.000Z`, "the journal is dated on the line's day");
    return res.json.lines.map((l) => [l.account_code, l.debit_cents, l.credit_cents]);
  };
  const matched = (description) => {
    const line = lines.find((l) => l.description === description);
    return run.json.matches.find((m) => m.statement_line_id === line.id).journal_id;
  };
  assert.deepEqual(await legs(matched("CARD SETTLEMENT BATCH 0142"), lines.find((l) => l.description === "CARD SETTLEMENT BATCH 0142")), [["1000", 128_000, 0], ["1010", 0, 128_000]]);
  assert.deepEqual(await legs(matched("CASH LODGEMENT DEPOSIT"), lines.find((l) => l.description === "CASH LODGEMENT DEPOSIT")), [["1000", 245_050, 0], ["1020", 0, 245_050]]);
  assert.deepEqual(await legs(matched("RUN BATCH SETTLEMENT"), lines.find((l) => l.description === "RUN BATCH SETTLEMENT")), [["1000", 115_000, 0], ["6300", 2_500, 0], ["1010", 0, 117_500]]);
  assert.deepEqual(await legs(manual.json.journal_id, wire), [["1000", 30_000, 0], ["1020", 0, 30_000]]);
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
  // A lodgement target that is not there (or not in the caller's reach) answers as a missing target.
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "LODGEMENT", target_ref_id: "b9000000-0000-4000-8000-0000000000ee" }), 404, "not found");
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "UNMATCHED", "a refused lodgement target leaves the line unmatched");
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
  // A deposit whose payment was reversed cannot be unmatched when it is not a card: refused in plain words, and
  // nothing changes.
  await fetch(`${base}/_stub/reverse-payment`, { method: "POST", body: JSON.stringify({ payment_id: IDS.paymentWire }) });
  refused(await ctl("POST", "/api/v1/bankrec/unmatch", { match_id: again.json.id }), 409, "a reversed cheque or cash payment cannot be matched again");
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "MATCHED", "a refused unmatch leaves the line matched");
  // A reversed CARD can be unmatched (SF-2d round 7): the wrong match is undone and the card is claimable again.
  const card = await lineByRef("CARD SETTLEMENT BATCH 0143");
  const cardMatch = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: card.id, target_type: "PAYMENT", target_ref_id: IDS.paymentGateway1 });
  assert.equal(cardMatch.status, 201);
  await fetch(`${base}/_stub/reverse-payment`, { method: "POST", body: JSON.stringify({ payment_id: IDS.paymentGateway1 }) });
  assert.equal((await ctl("POST", "/api/v1/bankrec/unmatch", { match_id: cardMatch.json.id })).status, 200);
  assert.equal((await lineByRef("CARD SETTLEMENT BATCH 0143")).status, "UNMATCHED");
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

test("a month opens with the ledger's balance and the variance, a zero balance is replaced by the month's net, and a refresh follows the lines", async () => {
  await reset();
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: 13, statement_balance_cents: CLOSING }), 400, "valid year and month (1-12) are required");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { year: Y, month: M }), 400, "bank_account_id is required");
  const opened = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING });
  assert.equal(opened.status, 201);
  holds("BankRecReconciliation", opened.json);
  assert.equal(opened.json.status, "OPEN");
  assert.equal(opened.json.statement_balance_cents, CLOSING_AFTER_MATCHING);
  // The ledger's balance, read from the ledger's own world: the GL world's posted journals on the account's code
  // at the month end. The stub keeps no ledger of its own.
  assert.equal(opened.json.gl_balance_cents, CLOSING, "before any match the ledger holds the GL world's own journals");
  assert.equal(opened.json.variance_cents, opened.json.statement_balance_cents - opened.json.gl_balance_cents);
  assert.equal(opened.json.variance_cents, MATCHING_MOVE, "before any match the ledger lacks the four auto lines and the wire");
  // The core's placeholder: a typed zero is replaced by the month's line net.
  const zero = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: 0 });
  assert.equal(zero.json.id, opened.json.id, "opening the same month again refreshes it");
  assert.equal(zero.json.statement_balance_cents, 675_575);
  assert.equal(zero.json.variance_cents, 675_575 - CLOSING, "a statement that does not agree with the ledger shows the whole difference");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  const stale = (await ctl("GET", `/api/v1/bankrec/reconciliations?bank_account_id=${IDS.operating}`)).json;
  assert.ok(Array.isArray(stale), "the core's handler writes the array itself");
  assert.ok(handlerSource.includes("httpx.WriteJSON(w, http.StatusOK, items)"));
  holds("BankRecReconciliationList", stale);
  assert.equal(stale[0].variance_cents, zero.json.variance_cents, "the stored variance is a snapshot until the month is opened again");
  const fresh = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING });
  assert.equal(fresh.json.gl_balance_cents, CLOSING_AFTER_MATCHING, "the matching moved the ledger: each line's net reached 1000");
  assert.equal(fresh.json.variance_cents, 0, "the ledger moves with the matching: the statement that agrees with it balances the month");
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
  // A statement that does not agree with the ledger refuses in dollars, the way the core does.
  await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING + 23_075 });
  const apart = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`);
  refused(apart, 422, "the statement reads");
  assert.equal(
    apart.json.error,
    "cannot complete, the statement reads $31,416.25 and the ledger reads $31,185.50 ($230.75 apart); they must agree, refresh the figures and match what is open",
  );
  // The statement that agrees with the ledger signs the month off.
  await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING });
  // The ledger moves after the open: a journal to 1000 through the ledger's own route. The sign off recomputes the
  // variance and refuses in dollars, and signs once the ledger is back, without a refresh in between.
  await postToBank(10_000, "up");
  const moved = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`);
  refused(moved, 422, "the statement reads");
  assert.equal(
    moved.json.error,
    "cannot complete, the statement reads $31,185.50 and the ledger reads $31,285.50 ($100.00 apart); they must agree, refresh the figures and match what is open",
  );
  await postToBank(10_000, "down");
  const signed = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`);
  assert.equal(signed.status, 200);
  holds("BankRecReconciliation", signed.json);
  assert.equal(signed.json.status, "COMPLETED");
  assert.ok(typeof signed.json.completed_at === "string");
  refused(await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`), 409, "is already COMPLETED");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING }), 409, "is already COMPLETED");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, refresh: true }), 409, "is already COMPLETED");
  refused(await ctl("POST", "/api/v1/bankrec/reconciliations/not-a-uuid/complete"), 404, "not found");
});

test("a match explains its line: a group, a lodgement of another amount and a journal already used are refused, and a signed off month's match cannot be undone", async () => {
  await reset();
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const cheque = await lineByRef("CHEQUE DEPOSIT");
  const fee = await lineByRef("MONTHLY SERVICE FEE");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "GROUP", target_ref_id: IDS.lodgement1 }), 422, "a group match is not available yet");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "LODGEMENT", target_ref_id: IDS.lodgement1 }), 422, "that lodgement is for");
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "UNMATCHED", "a refused match writes nothing");
  const journal = "7a1e0000-0000-4000-9000-000000000004";
  assert.equal((await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "JOURNAL", target_ref_id: journal })).status, 201);
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: journal }), 409, "that journal already explains another statement line");
  // A journal a match posted as its clearing is spent too (SF-2j round 2).
  const clearing = await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  assert.equal(clearing.status, 201);
  assert.ok(clearing.json.journal_id, "a payment match posts its clearing journal");
  refused(await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: clearing.json.journal_id }), 409, "that journal already explains another statement line");
  // A match inside a signed off month cannot be undone.
  const opened = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING })).json;
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  const paid = clearing.json;
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000003" });
  await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING });
  assert.equal((await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`)).status, 200);
  refused(await ctl("POST", "/api/v1/bankrec/unmatch", { match_id: paid.id }), 409, "which is already reconciled and completed");
  assert.equal((await lineByRef("WIRE FROM CONTRACTOR")).status, "MATCHED", "a refused undo writes nothing");
});

test("the walls: Money Desk work or above reads, counter and branch staff are refused reads, a manager imports, matches, undoes and opens a month, only the controller approves a rule and signs off", async () => {
  await reset();
  refused(await call(SALES, "GET", "/api/v1/bankrec/accounts"), 403, "Forbidden");
  refused(await call(SALES, "GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}`), 403, "Forbidden");
  refused(await call(IDS.manager, "GET", "/api/v1/bankrec/accounts"), 403, "Forbidden");
  refused(await call(IDS.manager, "GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}`), 403, "Forbidden");
  assert.equal((await call(IDS.payrec, "GET", "/api/v1/bankrec/accounts")).status, 200);
  assert.equal((await call(IDS.payrec, "GET", `/api/v1/bankrec/lines?bank_account_id=${IDS.operating}`)).status, 200);
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

test("the cut over door: the controller records the starting point on the account, it can be corrected while no month has signed off, and a signed off later month locks it", async () => {
  await reset();
  const cutPath = `/api/v1/bankrec/accounts/${IDS.operating}/cutover`;
  const prior = M === 1 ? `${Y - 1}-12` : `${Y}-${String(M - 1).padStart(2, "0")}`;
  // A date the core's words refuse, and an id that is not a journal id.
  refused(await ctl("POST", cutPath, { cutover_date: "not-a-date", opening_statement_balance_cents: 100 }), 400, "cutover_date must be YYYY-MM-DD");
  refused(await ctl("POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 100, outstanding_journal_ids: ["nope"] }), 400, "invalid outstanding journal id");
  // The work role is refused at the admin gate.
  refused(await call(IDS.payrec, "POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 100 }), 403, "Forbidden");
  // A starting point that does not reconcile is refused in the desk's words: the book at the date is
  // $25,025.00 and the ticked till entry ($25.00 still outstanding) lifts the statement it expects to $25,000.00.
  const tillId = "7a1e0000-0000-4000-9000-000000000004";
  const gappedRecord = await ctl("POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 2_500_000 });
  refused(gappedRecord, 422, "the statement and the book differ by");
  assert.equal(gappedRecord.json.error, "bankrec: cannot record cut-over at " + `${period}-28` + ": the statement and the book differ by $25.00; tick the items still outstanding at cut over, or check the date and balance");
  refused(await ctl("POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 2_400_000, outstanding_journal_ids: [tillId] }), 422, "tick the items still outstanding at cut over, or check the date and balance");
  // The controller records it: the account read carries the recorded point.
  const recorded = await ctl("POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 2_500_000, outstanding_journal_ids: [tillId] });
  assert.equal(recorded.status, 200);
  holds("BankRecAccount", recorded.json);
  assert.equal(recorded.json.cutover_date, `${period}-28`);
  assert.equal(recorded.json.cutover_statement_balance_cents, 2_500_000);
  assert.deepEqual(recorded.json.cutover_outstanding_journal_ids, [tillId]);
  const listed = await ctl("GET", "/api/v1/bankrec/accounts");
  holds("BankRecAccountList", listed.json);
  assert.equal(listed.json.find((a) => a.id === IDS.operating).cutover_date, `${period}-28`);
  // Before any month signs off it can be corrected; the core takes the re-record.
  const corrected = await ctl("POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 2_502_500 });
  assert.equal(corrected.status, 200);
  assert.equal(corrected.json.cutover_statement_balance_cents, 2_502_500);
  // The form's read: the ledger's entries on or before the date and the book balance they make, the core's figures.
  refused(await ctl("GET", `${cutPath}?date=nope`), 400, "cutover_date must be YYYY-MM-DD");
  refused(await ctl("GET", cutPath), 400, "cutover_date must be YYYY-MM-DD");
  const preview = await ctl("GET", `${cutPath}?date=${period}-28`);
  assert.equal(preview.status, 200);
  holds("BankRecCutoverPreview", preview.json);
  assert.equal(preview.json.book_balance_cents, 2_502_500);
  const memos = preview.json.entries.map((e) => e.memo);
  assert.deepEqual(memos, ["Opening balances", "Payment received", "Till over/short"]);
  // With the form's typed balance and ticks, the difference and whether it reconciles ride the answer.
  const gapped = await ctl("GET", `${cutPath}?date=${period}-28&statement_cents=2500000`);
  assert.equal(gapped.status, 200);
  assert.equal(gapped.json.difference_cents, -2_500);
  assert.equal(gapped.json.reconciles, false);
  const ticked = await ctl("GET", `${cutPath}?date=${period}-28&statement_cents=2500000&ticked=${tillId}`);
  assert.equal(ticked.status, 200);
  holds("BankRecCutoverPreview", ticked.json);
  assert.equal(ticked.json.difference_cents, 0);
  assert.equal(ticked.json.reconciles, true);
  refused(await ctl("GET", `${cutPath}?date=${period}-28&statement_cents=nope`), 400, "statement_cents must be a whole number of cents");
  const before = await ctl("GET", `${cutPath}?date=${prior}-01`);
  assert.equal(before.status, 200);
  assert.deepEqual(before.json, { book_balance_cents: 0 });
  // Once the statement month has signed off, recording at the month before it is refused with the core's conflict.
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const cheque = await lineByRef("CHEQUE DEPOSIT");
  const fee = await lineByRef("MONTHLY SERVICE FEE");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000003" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000004" });
  await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING });
  assert.equal((await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, refresh: true })).status, 201);
  const opened = (await ctl("GET", `/api/v1/bankrec/reconciliations?bank_account_id=${IDS.operating}`)).json.find((r) => r.year === Y && r.month === M);
  assert.equal((await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.id}/complete`)).status, 200);
  // Once any month on the account has signed off, any cut-over (before, in, or after the signed-off month)
  // is refused with 409 naming that signed-off month (FROM-PRINCIPAL 183).
  refused(await ctl("POST", cutPath, { cutover_date: `${prior}-28`, opening_statement_balance_cents: 2_502_500 }), 409, "cannot record cut-over at");
  refused(await ctl("POST", cutPath, { cutover_date: `${period}-28`, opening_statement_balance_cents: 2_502_500 }), 409, "is already completed");
  const laterReconciling = await ctl("POST", cutPath, { cutover_date: `${Y + 1}-06-30`, opening_statement_balance_cents: 2_502_500 });
  refused(laterReconciling, 409, "is already completed");
  assert.match(laterReconciling.json.error, new RegExp(`month ${period} is already completed`));
  const laterPenny = await ctl("POST", cutPath, { cutover_date: `${Y + 1}-06-30`, opening_statement_balance_cents: 1 });
  refused(laterPenny, 409, "is already completed");
  assert.match(laterPenny.json.error, new RegExp(`month ${period} is already completed`));
});

test("a derived month in the stub recomputes its statement balance from the lines, and it refuses against the ledger", async () => {
  await reset();
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const cheque = await lineByRef("CHEQUE DEPOSIT");
  const fee = await lineByRef("MONTHLY SERVICE FEE");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000003" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000004" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  const derived = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: 0 })).json;
  assert.equal(derived.statement_balance_cents, 675_575);
  // The month's lines do not agree with the ledger, so the difference is the whole gap. The ledger already holds
  // the clearing journals the matching posted.
  assert.equal(derived.variance_cents, 675_575 - CLOSING_AFTER_MATCHING);
  // Probe A: refreshing before the late import, taking no figure, keeps the month derived.
  const preRefreshed = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, refresh: true });
  assert.equal(preRefreshed.status, 201);
  assert.equal(preRefreshed.json.statement_balance_cents, 675_575);
  // Import a late +$1,000.00 line into the month and match it to an existing journal.
  await ctl("POST", "/api/v1/bankrec/imports", {
    bank_account_id: IDS.operating,
    profile_code: "rbc-default",
    filename: "late.csv",
    content: `Date,Description,Amount,Reference\n${period}-26,LATE JOURNAL LINE,1000.00,\n`,
  });
  const late = await lineByRef("LATE JOURNAL LINE");
  assert.equal((await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: late.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000099" })).status, 201);
  // The sign off reads the ledger and refuses in dollars: the month's net does not agree with it.
  const staleDerived = await ctl("POST", `/api/v1/bankrec/reconciliations/${derived.id}/complete`);
  refused(staleDerived, 422, "the statement reads");
  assert.equal(
    staleDerived.json.error,
    "cannot complete, the statement reads $7,755.75 and the ledger reads $31,185.50 ($23,429.75 apart); they must agree, refresh the figures and match what is open",
  );
  // Refreshing after the late import updates the derived statement balance to $7,755.75.
  const postRefreshed = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, refresh: true });
  assert.equal(postRefreshed.json.statement_balance_cents, 775_575);
  // Probe B: opening again with a typed statement balance keeps that figure, not the month's net. A typed balance
  // refuses against a ledger that moved after the open: a journal to 1000 through the ledger's own route.
  const typedAgain = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING });
  assert.equal(typedAgain.json.statement_balance_cents, CLOSING_AFTER_MATCHING);
  const typedRefreshed = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, refresh: true });
  assert.equal(typedRefreshed.json.statement_balance_cents, CLOSING_AFTER_MATCHING, "a typed balance is kept across the refresh; only a derived one follows the lines");
  await postToBank(100_000, "up");
  const probeB = await ctl("POST", `/api/v1/bankrec/reconciliations/${derived.id}/complete`);
  refused(probeB, 422, "the statement reads");
  assert.equal(
    probeB.json.error,
    "cannot complete, the statement reads $31,185.50 and the ledger reads $32,185.50 ($1,000.00 apart); they must agree, refresh the figures and match what is open",
  );
  // With the balance that agrees with the moved ledger, the month signs off.
  await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING + 100_000 });
  assert.equal((await ctl("POST", `/api/v1/bankrec/reconciliations/${derived.id}/complete`)).status, 200);

  // A completed month is skipped by autoMatch.
  const cardRec = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.fuelCard, year: Y, month: M, statement_balance_cents: 0 })).json;
  assert.equal((await ctl("POST", `/api/v1/bankrec/reconciliations/${cardRec.id}/complete`)).status, 200);
  await ctl("POST", "/api/v1/bankrec/imports", {
    bank_account_id: IDS.fuelCard,
    profile_code: "rbc-default",
    filename: "closed-month.csv",
    content: `Date,Description,Amount,Reference\n${period}-05,CLOSED MONTH LODGEMENT,2450.50,LODGE-310\n`,
  });
  const autoClosed = await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.fuelCard });
  assert.equal(autoClosed.status, 200);
  assert.deepEqual([autoClosed.json.matched, autoClosed.json.unmatched], [0, 1]);
});

test("FW1-04: outstanding items adjust the bank balance, sign off before month end or with no statement lines is refused, and the reconciliation report fits the contract", async () => {
  await reset();
  // Sign off before month end (2087-12) is refused in the core's words, naming the first day to sign off.
  const futureRec = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: 2087, month: 12, statement_balance_cents: CLOSING })).json;
  const futureComplete = await ctl("POST", `/api/v1/bankrec/reconciliations/${futureRec.id}/complete`);
  refused(futureComplete, 422, "has not reached month end yet");
  assert.ok(futureComplete.json.error.includes("first day to sign off is 2088-01-01"));

  // RBC CSV import refuses USD-only and empty-amount rows in the core's words.
  const usdImp = await ctl("POST", "/api/v1/bankrec/imports", {
    bank_account_id: IDS.operating,
    profile_code: "rbc-default",
    filename: "rbc-usd.csv",
    content: "Account Type,Account Number,Transaction Date,Cheque Number,Description 1,Description 2,CAD$,USD$\nChequing,00123-1002345,05/12/2023,,WIRE FROM US CLIENT,INV 8841,,250.00\n",
  });
  assert.equal(usdImp.status, 422);
  assert.ok(usdImp.json.error.includes("is not supported on a CAD statement import"));
  const emptyAmtImp = await ctl("POST", "/api/v1/bankrec/imports", {
    bank_account_id: IDS.operating,
    profile_code: "rbc-default",
    filename: "rbc-empty.csv",
    content: `Date,Description,Amount,Reference\n${period}-14,BLANK AMOUNT ROW,,REF-BLANK\n`,
  });
  assert.equal(emptyAmtImp.status, 422);
  assert.ok(emptyAmtImp.json.error.includes("missing amount"));

  // Sign off on an earlier closed month with no imported statement lines is refused.
  await reset();
  const emptyRec = (await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: 2021, month: 5, statement_balance_cents: 0 })).json;
  refused(await ctl("POST", `/api/v1/bankrec/reconciliations/${emptyRec.id}/complete`), 422, "no statement lines have been imported for");

  // Outstanding items: 1 deposit in transit (+$300.00) and 1 outstanding cheque (-$120.00) adjust the bank balance by +$180.00.
  await reset();
  await fetch(`${base}/_stub/outstanding-items`, {
    method: "POST",
    body: JSON.stringify({
      deposits_in_transit: [{ journal_id: "7a1e0000-0000-4000-9000-000000000081", posted_at: `${period}-28T12:00:00Z`, memo: "Month-end deposit in transit", amount_cents: 30_000 }],
      outstanding_cheques: [{ journal_id: "7a1e0000-0000-4000-9000-000000000082", posted_at: `${period}-29T12:00:00Z`, memo: "Cheque 1044 to vendor", amount_cents: 12_000 }],
    }),
  });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "auto", bank_account_id: IDS.operating });
  const wire = await lineByRef("WIRE FROM CONTRACTOR");
  const cheque = await lineByRef("CHEQUE DEPOSIT");
  const fee = await lineByRef("MONTHLY SERVICE FEE");
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: cheque.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000003" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: fee.id, target_type: "JOURNAL", target_ref_id: "7a1e0000-0000-4000-9000-000000000004" });
  await ctl("POST", "/api/v1/bankrec/match", { mode: "manual", statement_line_id: wire.id, target_type: "PAYMENT", target_ref_id: IDS.paymentWire });
  // The outstanding items adjust the bank balance: the statement carries the ledger's balance less them.
  const opened = await ctl("POST", "/api/v1/bankrec/reconciliations", { bank_account_id: IDS.operating, year: Y, month: M, statement_balance_cents: CLOSING_AFTER_MATCHING - 18_000 });
  assert.equal(opened.status, 201);
  holds("BankRecReconciliation", opened.json);
  assert.equal(opened.json.deposits_in_transit_cents, 30_000);
  assert.equal(opened.json.outstanding_cheques_cents, 12_000);
  assert.equal(opened.json.adjusted_bank_balance_cents, CLOSING_AFTER_MATCHING);
  assert.equal(opened.json.deposits_in_transit.length, 1);
  assert.equal(opened.json.outstanding_cheques.length, 1);
  assert.equal(opened.json.matched_lines_count, 7);
  // The adjusted bank balance agrees with the ledger, so the difference is zero and the month signs off.
  assert.equal(opened.json.gl_balance_cents, CLOSING_AFTER_MATCHING);
  assert.equal(opened.json.variance_cents, 0);
  const done = await ctl("POST", `/api/v1/bankrec/reconciliations/${opened.json.id}/complete`);
  assert.equal(done.status, 200);
  holds("BankRecReconciliation", done.json);
  assert.equal(done.json.status, "COMPLETED");
  assert.equal(done.json.matched_lines_count, 7);
  assert.equal(done.json.deposits_in_transit.length, 1);
  assert.equal(done.json.outstanding_cheques.length, 1);
});


