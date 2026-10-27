// The tax administration slice's stub against the contract and against the core, field by field. A stub that
// drifts from the server makes every browser leg a flattering lie, so this test starts the stub, makes every call
// the slice makes, and holds each answer to the generated wire descriptors: required fields present, types and
// vocabularies right, and no field the contract does not declare. Then each refusal is held to the core's words:
// the status, the envelope, and the sentence itself, which must appear in the core's source, and in the core's
// order. A refusal the stub invented fails here.
//
// The core's list quirks are pinned here so they cannot move in silence: its four lists answer under its own
// keys (rates, profiles, pending_profiles, evidence), which the contract declares, and an empty rates, profiles
// or evidence list is null (a Go nil slice) where the review queue is never null.
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readdirSync, readFileSync } from "node:fs";
import { createServer } from "node:net";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { SCHEMA_DESCRIPTORS } from "../../../packages/api-client/src/testing.ts";
import { validateValue } from "../../../packages/api-client/test/_validate.mjs";
import { IDS, LEGAL_REASONS, RECON, olderMonth, taxMonth } from "./stub/tax-world.mjs";

const appDir = join(dirname(fileURLToPath(import.meta.url)), "..");
const coreInternal = join(appDir, "..", "..", "..", "core", "internal");

const coreSource = ["tax", join("platform", "authz"), join("platform", "httpx")]
  .flatMap((dir) =>
    readdirSync(join(coreInternal, dir))
      .filter((f) => f.endsWith(".go") && !f.endsWith("_test.go"))
      .map((f) => readFileSync(join(coreInternal, dir, f), "utf8")),
  )
  .join("\n");
const handlerSource = readFileSync(join(coreInternal, "tax", "handler.go"), "utf8");
const moduleSource = readFileSync(join(coreInternal, "tax", "module.go"), "utf8");

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

const prior = taxMonth();
const older = olderMonth();

test("the lists answer under a name, as the core writes them, and every row fits the contract", async () => {
  await reset();
  for (const [key, source] of [
    ["rates", 'map[string]any{"rates": rows}'],
    ["profiles", 'map[string]any{"profiles": rows}'],
    ["pending_profiles", 'map[string]any{"pending_profiles": pending}'],
    ["evidence", 'map[string]any{"evidence": rows}'],
  ]) {
    assert.ok(handlerSource.includes(source), `the core's handler no longer writes ${key} under that name: bring the stub and the app's reader in step`);
  }
  const rates = await ctl("GET", "/api/v1/tax/rates");
  assert.equal(rates.status, 200);
  assert.deepEqual(Object.keys(rates.json), ["rates"]);
  rates.json.rates.forEach((r) => holds("TaxRate", r));
  assert.equal(rates.json.rates.length, 3);
  const profiles = await ctl("GET", "/api/v1/tax/profiles");
  assert.deepEqual(Object.keys(profiles.json), ["profiles"]);
  profiles.json.profiles.forEach((p) => holds("TaxStatusCardProfile", p));
  assert.deepEqual(
    profiles.json.profiles.map((p) => p.rate_bps),
    [...profiles.json.profiles.map((p) => p.rate_bps)].sort((a, b) => a - b),
    "profiles come by rate, then code",
  );
  const queue = await ctl("GET", "/api/v1/tax/review-queue");
  assert.deepEqual(Object.keys(queue.json), ["pending_profiles"]);
  assert.deepEqual(
    queue.json.pending_profiles.map((p) => p.code),
    ["FARM"],
  );
  const evidence = await ctl("GET", "/api/v1/tax/evidence");
  assert.deepEqual(Object.keys(evidence.json), ["evidence"]);
  evidence.json.evidence.forEach((e) => holds("TaxEvidence", e));
  assert.equal(evidence.json.evidence.length, 4);
  assert.equal(evidence.json.evidence[0].id, IDS.evidenceFour, "newest first");
});

test("an empty list is null, as a Go nil slice writes it", async () => {
  await reset();
  const none = await ctl("GET", `/api/v1/tax/evidence?order_id=${IDS.rateStandard}`);
  assert.equal(none.status, 200);
  assert.deepEqual(none.json, { evidence: null });
  assert.ok(coreSource.includes("var out []Evidence"), "the core's evidence list is no longer a nil slice when empty: bring the stub and the reader in step");
  const limited = await ctl("GET", "/api/v1/tax/evidence?limit=2");
  assert.equal(limited.json.evidence.length, 2);
  const byOrder = await ctl("GET", `/api/v1/tax/evidence?order_id=${IDS.orderOne}`);
  assert.deepEqual(
    byOrder.json.evidence.map((e) => e.id),
    [IDS.evidenceOne],
  );
});

test("the guards: a read is a manager's, a write an administrator's, a capture any staff member's", async () => {
  await reset();
  for (const line of [
    'r.Handle("GET /api/v1/tax/rates", g.RequireManager(',
    'r.Handle("POST /api/v1/tax/rates", g.RequireAdmin(',
    'r.Handle("POST /api/v1/tax/profiles", g.RequireAdmin(',
    'r.Handle("POST /api/v1/tax/config", g.RequireAdmin(',
    'r.Handle("POST /api/v1/tax/recompute", g.RequireAdmin(',
    'r.Handle("GET /api/v1/tax/recon", g.RequireManager(',
    'r.Handle("POST /api/v1/tax/evidence", g.RequireStaff(',
    'r.Handle("POST /api/v1/tax/evidence/{id}/download", g.RequireManager(',
    'r.Handle("GET /api/v1/tax/review-queue", g.RequireManager(',
  ]) {
    assert.ok(moduleSource.includes(line), `the core's guard moved: ${line}`);
  }
  // The recon names Point Loma for the manager leg: it is fenced to the caller's branch reach
  // (FW1-10), and the unnamed figure is a dealer wide caller's read. The certificate book and the
  // review queue are the dealer's whole book (615 round 2, P2-3): dealer wide reads, refused to a
  // branch manager in the core's own sentence.
  const reads = ["/api/v1/tax/rates", "/api/v1/tax/profiles", "/api/v1/tax/config", `/api/v1/tax/recon?year=${prior.year}&branch_id=b0000000-0000-4000-8000-000000000001`];
  for (const path of reads) {
    const staff = await call(SALES, "GET", path);
    assert.equal(staff.status, 403, `${path} is not a staff member's`);
    assert.deepEqual(staff.json, { error: "Forbidden" });
    const manager = await call(IDS.manager, "GET", path);
    assert.equal(manager.status, 200, `${path} is a manager's`);
  }
  const dealerWide = ["/api/v1/tax/review-queue", "/api/v1/tax/evidence"];
  for (const path of dealerWide) {
    const manager = await call(IDS.manager, "GET", path);
    assert.equal(manager.status, 403, `${path} is the dealer's whole book, not a branch manager's`);
    refused(manager, 403, "tax: the exemption certificates and the review queue are the dealer's whole book, a dealer wide caller's read");
  }
  const writes = [
    ["/api/v1/tax/rates", { reason_code: "STD_ON_HST_13", rate_bps: 800 }],
    ["/api/v1/tax/profiles", { code: "X", rate_bps: 1500 }],
    ["/api/v1/tax/config", { hst_registration_no: "SR-X", round_mode: "PER_LINE" }],
    ["/api/v1/tax/recompute", { from_date: "", to_date: "" }],
  ];
  for (const [path, body] of writes) {
    const manager = await call(IDS.manager, "POST", path, body);
    assert.equal(manager.status, 403, `${path} is not a manager's`);
    assert.deepEqual(manager.json, { error: "Forbidden" });
  }
  const evidenceBody = { profile_id: "", card_profile_code: "", order_id: "", photo_ref: "", raw_number: "", attestation: false, tcrd_expiry: "" };
  assert.equal((await call(SALES, "POST", "/api/v1/tax/evidence", evidenceBody)).status, 201, "capture is any staff member's");
  const download = await call(SALES, "POST", `/api/v1/tax/evidence/${IDS.evidenceOne}/download`);
  assert.equal(download.status, 403);
});

test("creating a rate checks in the core's order and its words, and a good row fits the contract", async () => {
  await reset();
  refused(await ctl("POST", "/api/v1/tax/rates", {}), 400, "reason_code required");
  refused(await ctl("POST", "/api/v1/tax/rates", { reason_code: "NOT_A_REASON", rate_bps: 5 }), 400, "not one of the lawful reason codes the rate table can carry");
  refused(await ctl("POST", "/api/v1/tax/rates", { reason_code: "STD_ON_HST_13", rate_bps: 10001 }), 400, "rate_bps must be 0..10000");
  refused(await ctl("POST", "/api/v1/tax/rates", { reason_code: "STD_ON_HST_13", rate_bps: -1 }), 400, "rate_bps must be 0..10000");
  refused(await ctl("POST", "/api/v1/tax/rates", { reason_code: "STD_ON_HST_13", rate_bps: 800, effective_from: "June 4" }), 400, "effective_from must be YYYY-MM-DD");
  // An ungated reason cannot sit below the standard over its dates; the refusal names the standard.
  const misconfigured = await ctl("POST", "/api/v1/tax/rates", { jurisdiction: "CA", reason_code: "FNT_PRODUCT", rate_bps: 100, effective_from: "2000-01-01" });
  refused(misconfigured, 400, "without an evidence gate and without the manual exception: the rate row is misconfigured against the gate map");
  assert.ok(misconfigured.json.error.includes("below a standard of 775 bps"));
  const made = await ctl("POST", "/api/v1/tax/rates", { id: "", jurisdiction: "CA", reason_code: "STD_ON_HST_13", rate_bps: 800, effective_from: "2000-01-01", effective_to: "", notes: "Raised." });
  assert.equal(made.status, 201);
  holds("TaxRate", made.json);
  assert.equal(made.json.rate_bps, 800);
  assert.equal(made.json.notes, "Raised.");
  const listed = (await ctl("GET", "/api/v1/tax/rates")).json.rates;
  assert.equal(listed.length, 4);
  // The default jurisdiction is the core's, not the dealer's.
  const blank = await ctl("POST", "/api/v1/tax/rates", { reason_code: "MANUAL_TAX_REVIEW", rate_bps: 775, effective_from: "2001-01-01" });
  assert.equal(blank.json.jurisdiction, "ON");
  assert.ok(coreSource.includes('jurisdictionON = "ON"'));
  assert.deepEqual(LEGAL_REASONS.length, 12, "the lawful reason universe is the core's twelve");
  for (const code of LEGAL_REASONS) assert.ok(coreSource.includes(`"${code}"`), `${code} is not in the core`);
});

test("saving a profile checks in the core's order, upserts by code, and a relief profile must force the number", async () => {
  await reset();
  refused(await ctl("POST", "/api/v1/tax/profiles", {}), 400, "code required");
  refused(await ctl("POST", "/api/v1/tax/profiles", { code: "X", rate_bps: 10001 }), 400, "rate_bps must be 0..10000");
  refused(await ctl("POST", "/api/v1/tax/profiles", { code: "X", name: "X", rate_bps: 0, requires_number: false }), 400, "a relief profile must force the band-card number as evidence");
  const made = await ctl("POST", "/api/v1/tax/profiles", { id: "", code: "CONTRACTOR", name: "Licensed contractor", rate_bps: 0, requires_photo: true, requires_number: true, requires_attestation: false, status: "PENDING", notes: "" });
  assert.equal(made.status, 200);
  holds("TaxStatusCardProfile", made.json);
  assert.equal((await ctl("GET", "/api/v1/tax/profiles")).json.profiles.length, 4);
  assert.deepEqual(
    (await ctl("GET", "/api/v1/tax/review-queue")).json.pending_profiles.map((p) => p.code).sort(),
    ["CONTRACTOR", "FARM"],
  );
  // Saving the same code again edits it; the id stays.
  const again = await ctl("POST", "/api/v1/tax/profiles", { id: "", code: "CONTRACTOR", name: "Licensed contractor", rate_bps: 0, requires_photo: true, requires_number: true, requires_attestation: true, status: "ACTIVE" });
  assert.equal(again.json.id, made.json.id);
  assert.equal(again.json.status, "ACTIVE");
  assert.equal((await ctl("GET", "/api/v1/tax/profiles")).json.profiles.length, 4);
  assert.deepEqual(
    (await ctl("GET", "/api/v1/tax/review-queue")).json.pending_profiles.map((p) => p.code),
    ["FARM"],
  );
  // A blank status means active.
  const blank = await ctl("POST", "/api/v1/tax/profiles", { code: "BLANK", name: "Blank", rate_bps: 1500 });
  assert.equal(blank.json.status, "ACTIVE");
});

test("capturing evidence checks the profile's forced fields in order, masks the number and never echoes it", async () => {
  await reset();
  const full = { profile_id: "", card_profile_code: "RESALE", order_id: IDS.orderOne, photo_ref: "evidence/new.jpg", raw_number: "SR-5550-4821", attestation: true, tcrd_expiry: "" };
  refused(await ctl("POST", "/api/v1/tax/evidence", { ...full, card_profile_code: "NOPE" }), 400, "unknown card_profile_code");
  refused(await ctl("POST", "/api/v1/tax/evidence", { ...full, photo_ref: "", raw_number: "", attestation: false }), 400, "band-card photo is required for this profile");
  refused(await ctl("POST", "/api/v1/tax/evidence", { ...full, raw_number: "", attestation: false }), 400, "band-card number is required for this profile");
  refused(await ctl("POST", "/api/v1/tax/evidence", { ...full, attestation: false }), 400, "attestation is required for this profile");
  const made = await ctl("POST", "/api/v1/tax/evidence", full);
  assert.equal(made.status, 201);
  holds("TaxEvidence", made.json);
  assert.equal(made.json.number_mask, "•••• 4821");
  assert.equal(made.json.attestation_status, "PENDING_LEGAL_REVIEW");
  assert.equal(made.json.captured_by, IDS.controller);
  assert.ok(!JSON.stringify(made.json).includes("5550"), "the raw number is not echoed");
  const read = await ctl("GET", `/api/v1/tax/evidence/${made.json.id}`);
  assert.ok(!JSON.stringify(read.json).includes("5550"), "the raw number is not stored");
  assert.equal(read.json.number_mask, "•••• 4821");
  // A short number masks fully.
  const short = await ctl("POST", "/api/v1/tax/evidence", { ...full, raw_number: "12" });
  assert.equal(short.json.number_mask, "•••• ••••");
  assert.ok(coreSource.includes('return "•••• ••••"'));
});

test("viewing, downloading and validating evidence: the missing record, the date, and the two reasons relief is blocked", async () => {
  await reset();
  const missing = "7c000000-0000-4000-8000-0000000000ff";
  refused(await ctl("GET", `/api/v1/tax/evidence/${missing}`), 404, "evidence not found");
  refused(await ctl("POST", `/api/v1/tax/evidence/${missing}/download`), 404, "evidence not found");
  refused(await ctl("POST", `/api/v1/tax/evidence/${missing}/validate`), 400, "evidence not found");
  refused(await ctl("POST", `/api/v1/tax/evidence/${IDS.evidenceOne}/validate?as_of=June%204`), 400, "date must be YYYY-MM-DD");
  const view = await ctl("GET", `/api/v1/tax/evidence/${IDS.evidenceOne}`);
  holds("TaxEvidence", view.json);
  const download = await ctl("POST", `/api/v1/tax/evidence/${IDS.evidenceOne}/download`);
  assert.equal(download.status, 200);
  holds("TaxEvidence", download.json);
  const good = await ctl("POST", `/api/v1/tax/evidence/${IDS.evidenceOne}/validate`);
  holds("TaxReliefEligibility", good.json);
  assert.deepEqual(good.json, { eligible: true, number_mask: "•••• 4821" });
  const expired = await ctl("POST", `/api/v1/tax/evidence/${IDS.evidenceTwo}/validate`);
  holds("TaxReliefEligibility", expired.json);
  assert.equal(expired.json.eligible, false);
  assert.equal(expired.json.block_reason, "status card (TCRD) expired");
  const unattested = await ctl("POST", `/api/v1/tax/evidence/${IDS.evidenceThree}/validate`);
  assert.equal(unattested.json.block_reason, "attestation missing");
  for (const reason of ["status card (TCRD) expired", "attestation missing"]) assert.ok(coreSource.includes(reason));
  // Checked as of a date before the expiry, the expired card is good.
  const earlier = await ctl("POST", `/api/v1/tax/evidence/${IDS.evidenceTwo}/validate?as_of=2000-01-01`);
  assert.equal(earlier.json.eligible, true);
});

test("the config reads, saves, rounds by one of two modes and refuses the dormant switches in the core's words", async () => {
  await reset();
  const read = await ctl("GET", "/api/v1/tax/config");
  holds("TaxConfig", read.json);
  assert.equal(read.json.round_mode, "INVOICE_TOTAL");
  refused(await ctl("POST", "/api/v1/tax/config", { hst_registration_no: "X", round_mode: "WHENEVER" }), 400, "round_mode must be INVOICE_TOTAL or PER_LINE");
  refused(await ctl("POST", "/api/v1/tax/config", { hst_registration_no: "X", round_mode: "PER_LINE", avalara_enabled: true }), 400, "avalara/postal routing are dormant at W6 and cannot be enabled");
  refused(await ctl("POST", "/api/v1/tax/config", { hst_registration_no: "X", postal_routing_enabled: true }), 400, "avalara/postal routing are dormant at W6 and cannot be enabled");
  const saved = await ctl("POST", "/api/v1/tax/config", { hst_registration_no: "SR-DEMO-0002", round_mode: "PER_LINE", avalara_enabled: false, postal_routing_enabled: false });
  assert.equal(saved.status, 200);
  holds("TaxConfig", saved.json);
  assert.equal((await ctl("GET", "/api/v1/tax/config")).json.round_mode, "PER_LINE");
  // A blank mode means the invoice total.
  const blank = await ctl("POST", "/api/v1/tax/config", { hst_registration_no: "SR-DEMO-0002", avalara_enabled: false, postal_routing_enabled: false });
  assert.equal(blank.json.round_mode, "INVOICE_TOTAL");
});

test("a recompute adds rows once and a second run skips them; a date the core cannot read is a server fault", async () => {
  await reset();
  const first = await ctl("POST", "/api/v1/tax/recompute", { from_date: "2000-01-01", to_date: "2100-01-01" });
  assert.equal(first.status, 200);
  holds("TaxRecomputeResult", first.json);
  assert.deepEqual(first.json, { from_date: "2000-01-01", to_date: "2100-01-01", scanned: 6, inserted: 6, skipped_exists: 0 });
  const second = await ctl("POST", "/api/v1/tax/recompute", { from_date: "2000-01-01", to_date: "2100-01-01" });
  assert.deepEqual(second.json, { from_date: "2000-01-01", to_date: "2100-01-01", scanned: 6, inserted: 0, skipped_exists: 6 });
  const bad = await ctl("POST", "/api/v1/tax/recompute", { from_date: "June 4", to_date: "" });
  assert.equal(bad.status, 500);
  assert.deepEqual(bad.json, { error: "internal error" });
  assert.ok(handlerSource.includes("INSERT-ONLY") || coreSource.includes("INSERT-ONLY"), "the core no longer says the recompute is insert only");
});

test("the reconciliation needs a year, ties out one month and is off in the other, in the core's fields", async () => {
  await reset();
  refused(await ctl("GET", "/api/v1/tax/recon"), 400, "year required");
  // Fenced to the caller's branch reach (FW1-10): the Sales manager (Point Loma alone) is refused
  // the whole company's reconcile and a foreign branch, in the core's own sentence.
  refused(await call(IDS.manager, "GET", `/api/v1/tax/recon?year=${prior.year}&month=${prior.month}`), 403, "tax: caller cannot access this branch");
  refused(await call(IDS.manager, "GET", `/api/v1/tax/recon?year=${prior.year}&month=${prior.month}&branch_id=b0000000-0000-4000-8000-000000000002`), 403, "tax: caller cannot access this branch");
  const tied = await ctl("GET", `/api/v1/tax/recon?year=${prior.year}&month=${prior.month}`);
  assert.equal(tied.status, 200);
  holds("TaxReconReport", tied.json);
  assert.equal(tied.json.collected_cents, RECON.tiedCollectedCents);
  assert.equal(tied.json.return_tax_cents, RECON.tiedReturnsCents);
  assert.equal(tied.json.input_tax_cents, RECON.tiedITCsCents);
  assert.equal(tied.json.variance_cents, 0);
  assert.equal(tied.json.tied_out, true);
  const off = await ctl("GET", `/api/v1/tax/recon?year=${older.year}&month=${older.month}`);
  holds("TaxReconReport", off.json);
  assert.equal(off.json.variance_cents, RECON.olderLedgerCents - (RECON.olderCollectedCents - RECON.olderReturnsCents - RECON.olderITCsCents));
  assert.equal(off.json.tied_out, false);
  assert.ok(
    coreSource.includes("rep.VarianceCents = rep.GLPayableCents - (rep.CollectedCents - rep.ReturnTaxCents - rep.InputTaxCreditsCents)"),
    "the core's variance no longer nets the documents against the ledger's month movement",
  );
  const empty = await ctl("GET", "/api/v1/tax/recon?year=1999&month=1");
  assert.equal(empty.json.collected_cents, 0);
  assert.equal(empty.json.tied_out, true);
});
