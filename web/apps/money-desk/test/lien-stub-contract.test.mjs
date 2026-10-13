// The liens and compliance slice's stub against the contract and against the core, field by field. A stub that drifts
// from the server makes every browser leg a flattering lie, so this test starts the stub, makes every call the slice
// makes, and holds each answer to the generated wire descriptors: required fields present, types and vocabularies
// right, and no field the contract does not declare. Then each refusal is held to the core's words: the status, the
// envelope, and the sentence itself, which must appear in the core's source. A refusal the stub invented fails here.
//
// One place the core strays from its own contract and the stub copies it: an empty list answers {"items": null}
// where the contract says an array. The test names it where it checks it.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import { IDS, dayFrom } from "./stub/lien-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["lien", "compliance", join("platform", "authz"), join("platform", "httpx"), join("identity", "guards")]
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
    env: { ROLE_STUB_PORT: String(port), ROLE_STUB_STDIN_EXIT: "1" },
    stdio: ["pipe", "ignore", "ignore"],
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

// The stub ends when its standard input closes: no signal is sent to it.
after(() => {
  child?.stdin.end();
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

/** A refusal: the status, the contract's envelope, the sentence the stub wrote, and the same sentence in the core. */
function refused(res, status, sentence, coreFragment = sentence) {
  assert.equal(res.status, status);
  holds("Error", res.json);
  assert.equal(res.json.error, sentence);
  assert.ok(coreSource.includes(coreFragment), `the core's source does not contain "${coreFragment}": the stub invented it`);
}

const get = (as, path) => call(as, "GET", path);
const GM = IDS.generalManager;

test("the jobs answer the contract's page, newest first, with one branch's rows on request", async () => {
  await reset();
  const all = await get(IDS.controller, "/api/v1/lien/jobs");
  assert.equal(all.status, 200);
  holds("LienJobPage", all.json);
  assert.equal(all.json.items.length, 5);
  assert.deepEqual(
    all.json.items.map((j) => j.job_no),
    ["GLS-2103", "GLS-2104", "GLS-2101", "GLS-2102", "GLS-2100"],
  );
  const mine = await get(IDS.payrec, `/api/v1/lien/jobs?branch_id=${IDS.branch2}`);
  assert.deepEqual(mine.json.items.map((j) => j.job_no), ["GLS-2104", "GLS-2102"]);
  const one = await get(IDS.controller, `/api/v1/lien/jobs/${IDS.job1}`);
  assert.equal(one.status, 200);
  holds("LienJob", one.json);
  assert.equal(one.json.holdback_direction, "RECEIVABLE");
  refused(await get(IDS.controller, `/api/v1/lien/jobs/${IDS.job1.replace("10b0", "99b0")}`), 404, "lien: job not found");
  assert.equal((await get(IDS.controller, "/api/v1/lien/jobs/not-a-uuid")).status, 500);
  // The core's stray: an empty branch answers a page whose items is null, and the stub answers it the same way.
  const none = await get(IDS.controller, "/api/v1/lien/jobs?branch_id=b0000000-0000-4000-8000-0000000000ff");
  assert.deepEqual(none.json, { items: null });
});

test("the roles the guards wall: staff are refused, the managers are not", async () => {
  await reset();
  const walled = await get(IDS.sales, "/api/v1/lien/jobs");
  assert.equal(walled.status, 403);
  holds("Error", walled.json);
  assert.equal(walled.json.error, "Forbidden");
  assert.ok(coreSource.includes('BodyForbidden'), "the core's forbidden body");
  for (const route of ["/api/v1/lien/triage", "/api/v1/lien/holdback/aging", "/api/v1/compliance/calendar", "/api/v1/compliance/upcoming"]) {
    assert.equal((await get(IDS.sales, route)).status, 403, route);
    assert.equal((await get(IDS.payrec, route)).status, 200, route);
  }
});

test("the triage and the aging read the same ledger: oldest first, badges and release eligibility as the core classes them", async () => {
  await reset();
  const aging = await get(IDS.controller, "/api/v1/lien/holdback/aging");
  assert.equal(aging.status, 200);
  holds("LienAgingRowPage", aging.json);
  // GLS-2100 is fully released and is dropped; the rest are oldest first.
  assert.deepEqual(aging.json.items.map((r) => r.job_no), ["GLS-2102", "GLS-2101", "GLS-2104", "GLS-2103"]);
  const byJob = Object.fromEntries(aging.json.items.map((r) => [r.job_no, r]));
  assert.equal(byJob["GLS-2101"].outstanding_cents, 1_860_000);
  assert.equal(byJob["GLS-2101"].oldest_days, 62);
  assert.equal(byJob["GLS-2101"].bucket, "61-90");
  assert.equal(byJob["GLS-2101"].release_eligible, true);
  assert.equal(byJob["GLS-2102"].bucket, "90+");
  assert.equal(byJob["GLS-2103"].bucket, "0-30");
  assert.equal(byJob["GLS-2103"].release_eligible, false);
  assert.equal(byJob["GLS-2104"].release_eligible, false, "a payable holdback is never release eligible");
  const triage = await get(IDS.controller, "/api/v1/lien/triage");
  assert.equal(triage.status, 200);
  holds("LienTriageItemPage", triage.json);
  assert.deepEqual(
    triage.json.items.map((t) => [t.job_no, t.badge]),
    [["GLS-2102", "OVERDUE"], ["GLS-2101", "RELEASE_DUE"], ["GLS-2104", "PAYABLE_MANUAL"], ["GLS-2103", "AGING"]],
  );
  // The notes are the core's own words (core/internal/lien/aging.go).
  for (const t of triage.json.items) assert.ok(coreSource.includes(t.note), `the core has no triage note "${t.note}"`);
  assert.deepEqual((await get(IDS.controller, `/api/v1/lien/triage?branch_id=${IDS.branch2}`)).json.items.map((t) => t.job_no), ["GLS-2102", "GLS-2104"]);
});

test("creating a job answers the contract, defaults as the core does, registers its release deadline and refuses in the core's order", async () => {
  await reset();
  const perf = dayFrom(-3);
  const made = await call(IDS.controller, "POST", "/api/v1/lien/jobs", {
    job_no: "GLS-2201",
    customer_id: "",
    branch_id: IDS.branch1,
    description: "Driveway pavers",
    contract_cents: 6_400_000,
    holdback_pct_bps: 0,
    substantial_perf_date: perf,
    last_supply_date: "",
    holdback_direction: "",
    hst_on_holdback: false,
  });
  assert.equal(made.status, 201);
  holds("LienJob", made.json);
  assert.equal(made.json.status, "OPEN");
  assert.equal(made.json.holdback_pct_bps, 1000, "a zero percent is the core's default");
  assert.equal(made.json.holdback_direction, "RECEIVABLE", "no direction is receivable");
  assert.equal(made.json.last_supply_date, undefined, "a blank date is absent, never written");
  const listed = await get(IDS.controller, "/api/v1/lien/jobs");
  assert.equal(listed.json.items[0].job_no, "GLS-2201");
  const calendar = await get(IDS.controller, "/api/v1/compliance/calendar");
  const registered = calendar.json.items.find((d) => d.job_id === made.json.id);
  assert.ok(registered !== undefined, "the job's release deadline is on the calendar");
  assert.equal(registered.deadline_kind, "HOLDBACK_RELEASE");
  assert.equal(registered.rule_days, 45);
  assert.equal(registered.due_date, dayFrom(42));
  // Checks in the service's order.
  refused(await call(IDS.controller, "POST", "/api/v1/lien/jobs", { job_no: " ", branch_id: "" }), 422, "lien: job_no is required");
  refused(await call(IDS.controller, "POST", "/api/v1/lien/jobs", { job_no: "J", branch_id: " ", holdback_direction: "SIDEWAYS" }), 422, "lien: branch_id is required");
  refused(await call(IDS.controller, "POST", "/api/v1/lien/jobs", { job_no: "J", branch_id: IDS.branch1, holdback_direction: "SIDEWAYS" }), 422, "lien: holdback_direction must be RECEIVABLE or PAYABLE");
});

test("the holdback sync books each held invoice once, and a re run books nothing", async () => {
  await reset();
  const first = await call(IDS.controller, "POST", `/api/v1/lien/jobs/${IDS.job1}/sync`);
  assert.equal(first.status, 200);
  holds("LienSyncResult", first.json);
  assert.deepEqual(first.json, { job_id: IDS.job1, booked_count: 1, skipped_count: 2, booked_cents: 420_000 });
  const again = await call(IDS.controller, "POST", `/api/v1/lien/jobs/${IDS.job1}/sync`);
  assert.deepEqual(again.json, { job_id: IDS.job1, booked_count: 0, skipped_count: 3, booked_cents: 0 });
  const aging = (await get(IDS.controller, "/api/v1/lien/holdback/aging")).json.items.find((r) => r.job_no === "GLS-2101");
  assert.equal(aging.outstanding_cents, 2_280_000);
  // A job with no customer sweeps its whole branch.
  const made = (await call(IDS.controller, "POST", "/api/v1/lien/jobs", { job_no: "GLS-2202", branch_id: IDS.branch1 })).json;
  const swept = await call(IDS.controller, "POST", `/api/v1/lien/jobs/${made.id}/sync`);
  assert.deepEqual(swept.json, { job_id: made.id, booked_count: 4, skipped_count: 0, booked_cents: 2_406_000 });
  refused(await call(IDS.controller, "POST", `/api/v1/lien/jobs/${IDS.job1.replace("10b0", "99b0")}/sync`), 404, "lien: job not found");
});

test("a document renders as the core renders it, versions append, and the lists read the latest and the whole history", async () => {
  await reset();
  const body = { job_id: IDS.job1, template_kind: "CONDITIONAL_RELEASE", data: { job_no: "GLS-2101", owner: "Summit Ridge Builders", extra_note: "Phase two" } };
  const v1 = await call(IDS.controller, "POST", "/api/v1/lien/documents", body);
  assert.equal(v1.status, 201);
  holds("LienDocument", v1.json);
  assert.equal(v1.json.version, 1);
  assert.equal(v1.json.status, "DRAFT");
  assert.equal(v1.json.is_draft_banner, true);
  const text = v1.json.rendered_content;
  assert.ok(text.startsWith("*** DRAFT, statute-derived, pending client validation ***\n\nConditional Release of Preserved Lien (Payment)  (v1)\n"));
  assert.ok(text.includes("Job / Improvement: GLS-2101\nOwner: Summit Ridge Builders\nContractor / Claimant: ____\n"), "a missing merge key is a blank");
  assert.ok(text.endsWith("\nAdditional:\n  extra_note: Phase two\n"), "an unused key is appended");
  for (const words of ["Conditional Release of Preserved Lien (Payment)", "Contractor / Claimant", "pending client validation"]) assert.ok(coreSource.includes(words), `the core has no "${words}"`);
  const v2 = await call(IDS.controller, "POST", "/api/v1/lien/documents", { ...body, data: { job_no: "GLS-2101", owner: "Summit Ridge Builders (revised)" } });
  assert.equal(v2.json.version, 2);
  await call(IDS.controller, "POST", "/api/v1/lien/documents", { job_id: IDS.job1, template_kind: "DISCHARGE", data: {} });
  const listed = await get(IDS.controller, `/api/v1/lien/jobs/${IDS.job1}/documents`);
  assert.equal(listed.status, 200);
  holds("LienDocumentPage", listed.json);
  assert.deepEqual(listed.json.items.map((d) => [d.template_kind, d.version]), [["CONDITIONAL_RELEASE", 2], ["DISCHARGE", 1]]);
  const versions = await get(IDS.controller, `/api/v1/lien/documents/${v1.json.id}/versions`);
  assert.equal(versions.status, 200);
  holds("LienDocumentPage", versions.json);
  assert.deepEqual(versions.json.items.map((d) => d.version), [2, 1], "newest first");
  const empty = await get(IDS.controller, `/api/v1/lien/jobs/${IDS.job2}/documents`);
  assert.deepEqual(empty.json, { items: null });
  refused(await call(IDS.controller, "POST", "/api/v1/lien/documents", { job_id: " ", template_kind: "NOTICE" }), 422, "lien: job_id is required");
  refused(await call(IDS.controller, "POST", "/api/v1/lien/documents", { job_id: IDS.job1, template_kind: "LETTER" }), 422, 'lien: unknown template_kind "LETTER"', "lien: unknown template_kind");
  refused(await call(IDS.controller, "POST", "/api/v1/lien/documents", { job_id: IDS.job1.replace("10b0", "99b0"), template_kind: "NOTICE" }), 404, "lien: job not found");
  refused(await get(IDS.controller, `/api/v1/lien/documents/${v1.json.id.replace("d0c0", "99c0")}/versions`), 404, "lien: document not found");
});

test("the release checks the dual gate in the core's order, posts one journal and leaves nothing outstanding", async () => {
  await reset();
  const release = (as, job, approver) => call(as, "POST", `/api/v1/lien/holdback/${job}/release`, approver === undefined ? {} : { approver_id: approver, approver_role: "FINANCIAL_CONTROLLER" });
  // The gatekeeper is the General Manager; the controller is refused first, whatever else is wrong.
  refused(await release(IDS.controller, IDS.job1, IDS.payrec), 403, "lien: holdback release gatekeeper must be GENERAL_MANAGER", "lien: holdback release gatekeeper must be");
  refused(await release(GM, IDS.job1), 403, "lien: four-eyes, the co-signer must differ from the gatekeeper");
  refused(await release(GM, IDS.job1, GM), 403, "lien: four-eyes, the co-signer must differ from the gatekeeper");
  refused(await release(GM, IDS.job1, "de110000-0000-4000-9000-0000000000ff"), 403, "lien: holdback release co-signer could not be verified");
  refused(await release(GM, IDS.job1, IDS.formerPayrec), 403, "lien: holdback release co-signer must be an active staff member");
  refused(await release(GM, IDS.job1, IDS.manager), 403, "lien: holdback release co-signer must be FINANCIAL_CONTROLLER or PAYABLES_RECEIVABLES", "lien: holdback release co-signer must be");
  refused(await release(GM, IDS.job1.replace("10b0", "99b0"), IDS.controller), 404, "lien: job not found");
  refused(await release(GM, IDS.job4, IDS.controller), 422, "lien: PAYABLE holdback release is manual, recorded, not posted (register a calendar deadline)");
  refused(await release(GM, IDS.job5, IDS.controller), 422, "lien: no outstanding retention to release for this job");
  const done = await release(GM, IDS.job1, IDS.controller);
  assert.equal(done.status, 200);
  holds("LienReleaseResult", done.json);
  assert.equal(done.json.amount_cents, 1_860_000);
  assert.equal(done.json.approved_by, IDS.controller);
  assert.equal(done.json.gatekeeper_by, GM);
  assert.ok(typeof done.json.gl_journal_id === "string" && done.json.gl_journal_id !== "");
  const aging = (await get(IDS.controller, "/api/v1/lien/holdback/aging")).json.items.map((r) => r.job_no);
  assert.ok(!aging.includes("GLS-2101"), "a released job leaves the aging");
  refused(await release(GM, IDS.job1, IDS.payrec), 422, "lien: no outstanding retention to release for this job");
  // The co-signer's authority is the roster's, never the body's claim: a manager who claims to be a controller is refused.
  const claimed = await call(GM, "POST", `/api/v1/lien/holdback/${IDS.job2}/release`, { approver_id: IDS.manager, approver_role: "FINANCIAL_CONTROLLER" });
  assert.equal(claimed.status, 403);
});

test("the compliance calendar lists by due date, marks past due rows in the upcoming list and answers the horizon asked", async () => {
  await reset();
  const cal = await get(IDS.controller, "/api/v1/compliance/calendar");
  assert.equal(cal.status, 200);
  holds("ComplianceDeadlinePage", cal.json);
  assert.equal(cal.json.items.length, 7);
  const dues = cal.json.items.map((d) => d.due_date);
  assert.deepEqual(dues, [...dues].sort());
  assert.ok(cal.json.items.every((d) => d.status !== "OVERDUE"), "the stored status is never overdue; only the upcoming list marks it");
  const waived = await get(IDS.controller, "/api/v1/compliance/calendar?status=WAIVED");
  assert.equal(waived.json.items.length, 1);
  assert.deepEqual((await get(IDS.controller, "/api/v1/compliance/calendar?status=OVERDUE")).json, { items: null });
  const up = await get(IDS.controller, "/api/v1/compliance/upcoming");
  assert.equal(up.status, 200);
  holds("ComplianceDeadlinePage", up.json);
  // Open rows due inside the default window, past due rows marked overdue, soonest first.
  assert.ok(up.json.items.some((d) => d.status === "OVERDUE"));
  assert.ok(up.json.items.every((d) => d.status === "OVERDUE" || d.status === "OPEN"));
  assert.ok(!up.json.items.some((d) => d.status === "SATISFIED" || d.status === "WAIVED"));
  const wide = await get(IDS.controller, "/api/v1/compliance/upcoming?horizon_days=90");
  const narrow = await get(IDS.controller, "/api/v1/compliance/upcoming?horizon_days=5");
  assert.ok(wide.json.items.length > up.json.items.length - 1);
  assert.ok(narrow.json.items.length < up.json.items.length);
  assert.ok(up.json.items.map((d) => d.due_date).every((d, i, all) => i === 0 || all[i - 1] <= d));
});

test("the review status answers the core's own draft banner until a row is set, and the set is an upsert the core audits", async () => {
  await reset();
  const subject = "00000000-0000-4000-9000-0000000000d6";
  const none = await get(IDS.controller, `/api/v1/compliance/review-status?subject_type=OTHER&subject_id=${subject}`);
  assert.equal(none.status, 200);
  holds("ComplianceReviewStatus", none.json);
  assert.deepEqual(none.json, { subject_type: "OTHER", subject_id: subject, state: "DRAFT", banner_text: "DRAFT, statute-derived, pending client validation", is_draft: true });
  assert.ok(coreSource.includes("DRAFT, statute-derived, pending client validation"));
  const set = await call(IDS.controller, "PUT", "/api/v1/compliance/review-status", { subject_type: "OTHER", subject_id: subject, state: "APPROVED", banner_text: "Checked against the dealer's forms" });
  assert.equal(set.status, 200);
  holds("ComplianceReviewStatus", set.json);
  assert.equal(set.json.state, "APPROVED");
  assert.equal(set.json.is_draft, false);
  assert.equal(set.json.reviewed_by, IDS.controller);
  const again = await get(IDS.payrec, `/api/v1/compliance/review-status?subject_type=OTHER&subject_id=${subject}`);
  assert.equal(again.json.banner_text, "Checked against the dealer's forms");
  const blank = await call(IDS.controller, "PUT", "/api/v1/compliance/review-status", { subject_type: "LIEN_JOB", subject_id: IDS.job1, state: "PENDING", banner_text: "" });
  assert.equal(blank.json.banner_text, "DRAFT, statute-derived, pending client validation");
  assert.equal(blank.json.is_draft, true);
  refused(await get(IDS.controller, "/api/v1/compliance/review-status?subject_type=OTHER&subject_id="), 400, "compliance: subject_id is required");
  refused(await call(IDS.controller, "PUT", "/api/v1/compliance/review-status", { subject_type: "OTHER", subject_id: " ", state: "DRAFT" }), 400, "compliance: subject_id is required");
  refused(await call(IDS.controller, "PUT", "/api/v1/compliance/review-status", { subject_type: "OTHER", subject_id: subject, state: "MAYBE" }), 422, 'compliance: invalid review state "MAYBE"', "compliance: invalid review state");
  assert.equal((await get(IDS.controller, "/api/v1/compliance/review-status?subject_type=OTHER&subject_id=nope")).status, 500);
});

test("the staff roster is the dealer admin's read and carries the stored role codes the co-signer is picked by", async () => {
  await reset();
  const roster = await get(GM, "/api/v1/staff");
  assert.equal(roster.status, 200);
  holds("IdentityStaffMemberPage", roster.json);
  const codes = roster.json.items.map((p) => p.role_code);
  assert.ok(codes.includes("FINANCIAL_CONTROLLER") && codes.includes("PAYABLES_RECEIVABLES"));
  for (const code of ["FINANCIAL_CONTROLLER", "PAYABLES_RECEIVABLES", "GENERAL_MANAGER"]) assert.ok(coreSource.includes(`"${code}"`), `the core has no role ${code}`);
  assert.equal((await get(IDS.payrec, "/api/v1/staff")).status, 403);
});

test("the role gate: Payables and Receivables reads the liens and the calendar and is refused every write, as the default configuration and the census say", async () => {
  await reset();
  // The default role configuration: payables_receivables holds money-desk and manager-home at work, the
  // controller and the General Manager at manage or above. The census puts the lien writes at money-desk manage
  // and the review status write at manager-home manage.
  const config = JSON.parse(readFileSync(join(coreInternal, "roleconfig", "default.json"), "utf8"));
  const level = (role, surface) => config.cells.find((c) => c.role === role && c.surface === surface)?.level;
  for (const surface of ["money-desk", "manager-home"]) {
    assert.equal(level("payables_receivables", surface), "work", `payables_receivables on ${surface}`);
    for (const role of ["financial_controller", "general_manager"]) assert.ok(["manage", "admin"].includes(level(role, surface)), `${role} on ${surface}`);
  }
  const census = readFileSync(join(coreInternal, "platform", "apigen", "routes.gen.go"), "utf8");
  for (const [op, surface] of [["lienCreateJob", "money-desk"], ["lienSyncJobHoldbacks", "money-desk"], ["lienCreateDocument", "money-desk"], ["lienReleaseHoldback", "money-desk"], ["complianceUpsertReviewStatus", "manager-home"]]) {
    const line = census.split("\n").find((l) => l.includes(`OperationID: "${op}"`)) ?? "";
    assert.ok(line.includes(`Surfaces: []string{"${surface}"}, Level: "manage"`), `${op} is not ${surface} manage in the census`);
  }
  // Reads pass for Payables and Receivables.
  for (const route of ["/api/v1/lien/jobs", "/api/v1/lien/triage", "/api/v1/lien/holdback/aging", `/api/v1/lien/jobs/${IDS.job1}/documents`, "/api/v1/compliance/calendar", "/api/v1/compliance/upcoming"]) {
    assert.equal((await get(IDS.payrec, route)).status, 200, route);
  }
  // The four writes, and the release, answer the role gate's shared body.
  const writes = [
    ["POST", "/api/v1/lien/jobs", { job_no: "J", branch_id: IDS.branch1 }],
    ["POST", `/api/v1/lien/jobs/${IDS.job1}/sync`, {}],
    ["POST", "/api/v1/lien/documents", { job_id: IDS.job1, template_kind: "NOTICE", data: {} }],
    ["PUT", "/api/v1/compliance/review-status", { subject_type: "OTHER", subject_id: IDS.job1, state: "DRAFT" }],
    ["POST", `/api/v1/lien/holdback/${IDS.job1}/release`, { approver_id: IDS.controller, approver_role: "FINANCIAL_CONTROLLER" }],
  ];
  for (const [method, path, body] of writes) {
    const res = await call(IDS.payrec, method, path, body);
    assert.equal(res.status, 403, path);
    holds("Error", res.json);
    assert.equal(res.json.error, "Forbidden", path);
    // The controller passes the gate on the same calls (the release then meets its own gatekeeper check).
    assert.notEqual((await call(IDS.controller, method, path, body)).json?.error, "Forbidden", path);
  }
});
