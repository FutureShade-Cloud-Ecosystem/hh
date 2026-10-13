// The liens slice's plain logic: the money it reads and the amounts it parses, the words for every state the core
// stores, how the core's two list shapes are read, how the new job form becomes the body the core takes, how a
// merge form becomes document data, who may release a holdback, and how a refusal reads out loud. No money is
// computed here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { formatMoney, MONEY_PLACEHOLDER, parseAmountToCents, parsePercentToBps, bpsWords } from "../src/lien/lien-money.ts";
import {
  ageWords,
  cosigners,
  heldNowWords,
  holdingWords,
  noteWords,
  releaseStatusWord,
  releasedWords,
  renderedWords,
  syncWords,
  badgeTone,
  badgeWord,
  bucketWord,
  canRelease,
  directionLine,
  directionWord,
  documentStatusWord,
  jobForm,
  jobStatusTone,
  jobStatusWord,
  listOf,
  mergeData,
  refusalWords,
  releaseConfirmWords,
  templateFields,
  templateWord,
  TEMPLATE_KINDS,
} from "../src/lien/lien-logic.ts";

// A day this many days after an arbitrary fixed day, written as the core writes a date, so no calendar literal is typed.
const iso = (n) => new Date(Date.UTC(2000, 0, 1 + n)).toISOString().slice(0, 10);

const coreTemplate = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..", "..", "core", "internal", "lien", "template.go"), "utf8");

test("cents read as dollars, and a missing figure is never a plausible zero", () => {
  assert.equal(formatMoney(1250000), "$12,500.00");
  assert.equal(formatMoney(0), "$0.00");
  assert.equal(formatMoney(undefined), MONEY_PLACEHOLDER);
  assert.equal(formatMoney(12.5), MONEY_PLACEHOLDER);
});

test("a contract amount is parsed through the decimal string and refused when it is not money", () => {
  assert.equal(parseAmountToCents("125,000.00"), 12500000);
  assert.equal(parseAmountToCents("$480"), 48000);
  assert.equal(parseAmountToCents("0.10"), 10);
  for (const bad of ["", "12.345", "-5", "1,2", "abc", "1e3", "1234567890"]) assert.equal(parseAmountToCents(bad), null, bad);
});

test("a holdback percent becomes basis points through the string, with no float", () => {
  assert.equal(parsePercentToBps("10"), 1000);
  assert.equal(parsePercentToBps("7.5"), 750);
  assert.equal(parsePercentToBps("7.25%"), 725);
  assert.equal(parsePercentToBps("100"), 10000);
  assert.equal(parsePercentToBps(""), 0);
  for (const bad of ["101", "0", "-1", "7.255", "ten", "1,0"]) assert.equal(parsePercentToBps(bad), null, bad);
  assert.equal(bpsWords(1000), "10%");
  assert.equal(bpsWords(750), "7.5%");
  assert.equal(bpsWords(725), "7.25%");
});

test("the states the core stores read as plain words, with a tone that only backs the word", () => {
  assert.equal(jobStatusWord("SUBSTANTIALLY_PERFORMED"), "Substantially performed");
  assert.equal(jobStatusWord("OPEN"), "Open");
  assert.equal(jobStatusWord("SOMETHING_NEW"), "something new");
  assert.equal(jobStatusTone("RELEASED"), "good");
  assert.equal(jobStatusTone("OPEN"), "plain");
  assert.equal(directionWord("RECEIVABLE"), "Receivable");
  assert.equal(directionWord("PAYABLE"), "Payable");
  assert.match(directionLine("RECEIVABLE"), /we hold/i);
  assert.match(directionLine("PAYABLE"), /we owe/i);
  assert.equal(badgeWord("RELEASE_DUE"), "Release due");
  assert.equal(badgeWord("OVERDUE"), "Overdue");
  assert.equal(badgeWord("PAYABLE_MANUAL"), "Payable, released by hand");
  assert.equal(badgeWord("AGING"), "Aging");
  assert.equal(badgeTone("OVERDUE"), "stop");
  assert.equal(badgeTone("RELEASE_DUE"), "watch");
  assert.equal(badgeTone("AGING"), "plain");
  assert.equal(bucketWord("0-30"), "0 to 30 days");
  assert.equal(bucketWord("90+"), "Over 90 days");
  assert.equal(ageWords(1), "1 day");
  assert.equal(ageWords(0), "0 days");
  assert.equal(ageWords(52), "52 days");
  assert.equal(documentStatusWord("DRAFT"), "Draft");
  assert.equal(documentStatusWord("ISSUED"), "Issued");
});

test("the core's list shapes: a page or a bare array is a list, an empty page says null for items, and anything else is not", () => {
  assert.deepEqual(listOf({ items: [1, 2], next_cursor: null }), [1, 2]);
  assert.deepEqual(listOf([3]), [3]);
  assert.deepEqual(listOf({ items: null }), []);
  assert.equal(listOf({}), null);
  assert.equal(listOf({ items: "x" }), null);
  assert.equal(listOf("nope"), null);
  assert.equal(listOf(null), null);
});

test("the new job form becomes the body the core takes, or says what is wrong", () => {
  const ok = jobForm({ job_no: " GLS-2101 ", branch_id: "b1", description: "Pool deck pavers", contract: "125,000.00", percent: "10", direction: "RECEIVABLE", perf: iso(40), supply: "" });
  assert.deepEqual(ok, {
    ok: true,
    body: {
      job_no: "GLS-2101",
      customer_id: "",
      branch_id: "b1",
      description: "Pool deck pavers",
      contract_cents: 12500000,
      holdback_pct_bps: 1000,
      substantial_perf_date: iso(40),
      last_supply_date: "",
      holdback_direction: "RECEIVABLE",
      hst_on_holdback: false,
    },
  });
  const blankPercent = jobForm({ job_no: "J", branch_id: "b1", description: "", contract: "", percent: "", direction: "PAYABLE", perf: "", supply: "" });
  assert.equal(blankPercent.ok, true);
  assert.equal(blankPercent.body.holdback_pct_bps, 0);
  assert.equal(blankPercent.body.contract_cents, 0);
  assert.equal(blankPercent.body.hst_on_holdback, false, "the form never asks for the core's placeholder tax on holdback");
  assert.deepEqual(jobForm({ job_no: " ", branch_id: "b1", description: "", contract: "", percent: "", direction: "RECEIVABLE", perf: "", supply: "" }), { ok: false, field: "job_no", words: "Give the job a number." });
  assert.deepEqual(jobForm({ job_no: "J", branch_id: "", description: "", contract: "", percent: "", direction: "RECEIVABLE", perf: "", supply: "" }), { ok: false, field: "branch_id", words: "Pick the branch the job belongs to." });
  assert.deepEqual(jobForm({ job_no: "J", branch_id: "b1", description: "", contract: "12.345", percent: "", direction: "RECEIVABLE", perf: "", supply: "" }), {
    ok: false,
    field: "contract",
    words: "The contract amount is dollars and cents, like 125,000.00.",
  });
  assert.deepEqual(jobForm({ job_no: "J", branch_id: "b1", description: "", contract: "", percent: "150", direction: "RECEIVABLE", perf: "", supply: "" }), {
    ok: false,
    field: "percent",
    words: "The holdback percent is a number from 0.01 to 100, like 10 or 7.5. Leave it blank for the core's default.",
  });
  assert.deepEqual(jobForm({ job_no: "J", branch_id: "b1", description: "", contract: "", percent: "", direction: "RECEIVABLE", perf: "yesterday", supply: "" }), {
    ok: false,
    field: "perf",
    words: "Dates are written yyyy-mm-dd.",
  });
});

test("the merge form keeps what was filled in and nothing else", () => {
  assert.deepEqual(mergeData([{ key: "owner", value: " Summit Ridge " }, { key: "claimant", value: "" }, { key: "job_no", value: "GLS-2101" }]), { owner: "Summit Ridge", job_no: "GLS-2101" });
  assert.deepEqual(mergeData([]), {});
});

test("every document form's merge fields are the core's own template keys, and the six kinds are the core's", () => {
  assert.deepEqual([...TEMPLATE_KINDS], ["CONDITIONAL_RELEASE", "UNCONDITIONAL_RELEASE", "STATUTORY_DECLARATION", "DISCHARGE", "NOTICE", "OTHER"]);
  for (const kind of TEMPLATE_KINDS) {
    assert.ok(coreTemplate.includes(`"${kind}": {`), `the core has no template ${kind}`);
    assert.notEqual(templateWord(kind), kind);
    for (const field of templateFields(kind)) {
      assert.ok(coreTemplate.includes(`"${field.key}"}`), `the core's ${kind} template has no merge key ${field.key}`);
      assert.ok(coreTemplate.includes(`{"${field.label}", "${field.key}"}`), `the core's label for ${field.key} is not "${field.label}"`);
    }
  }
  assert.equal(templateWord("CONDITIONAL_RELEASE"), "Conditional release");
  assert.equal(templateWord("STATUTORY_DECLARATION"), "Statutory declaration");
  assert.equal(templateFields("DISCHARGE").length, 4);
  assert.deepEqual(templateFields("UNKNOWN"), []);
});

test("only the General Manager releases a holdback, and the confirm names the job and the amount", () => {
  assert.equal(canRelease("general_manager"), true);
  assert.equal(canRelease("financial_controller"), false);
  assert.equal(canRelease("payables_receivables"), false);
  assert.equal(canRelease(""), false);
  assert.equal(releaseConfirmWords("GLS-2101", "$12,500.00"), "Release $12,500.00 held on job GLS-2101. This posts a journal to the ledger.");
});

test("a refusal reads in the core's own sentence, with who may do it when the core says only Forbidden", () => {
  assert.equal(
    refusalWords("release", "lien: holdback release gatekeeper must be GENERAL_MANAGER", 403),
    "Holdback release gatekeeper must be GENERAL_MANAGER. Releasing is the General Manager's, co-signed by a Financial Controller or Payables and Receivables.",
  );
  assert.equal(
    refusalWords("release", "lien: four-eyes, the co-signer must differ from the gatekeeper", 403),
    "Four-eyes, the co-signer must differ from the gatekeeper.",
  );
  assert.equal(
    refusalWords("release", "lien: PAYABLE holdback release is manual, recorded, not posted (register a calendar deadline)", 422),
    "PAYABLE holdback release is manual, recorded, not posted (register a calendar deadline).",
  );
  assert.equal(refusalWords("release", "lien: no outstanding retention to release for this job", 422), "No outstanding retention to release for this job.");
  assert.equal(refusalWords("release", "Forbidden", 403), "Releasing a holdback is for the General Manager, co-signed by a Financial Controller or Payables and Receivables.");
  assert.equal(refusalWords("job", "Forbidden", 403), "That is for the Financial Controller, Payables and Receivables or the General Manager.");
  assert.equal(refusalWords("job", "lien: job_no is required", 422), "Job_no is required.");
  assert.equal(refusalWords("job", "lien: job not found", 404), "Job not found.");
  assert.equal(refusalWords("load", "", 0), "The liens did not answer. Reload to try again.");
});

test("the sentences the desk says after an action and beside a row", () => {
  assert.equal(syncWords(1, "$4,200.00", 2), "Synced: 1 invoice booked for $4,200.00, 2 already booked.");
  assert.equal(syncWords(3, "$9,000.00", 0), "Synced: 3 invoices booked for $9,000.00.");
  assert.equal(syncWords(0, "$0.00", 3), "Synced: nothing new to book, 3 already booked.");
  assert.equal(syncWords(0, "$0.00", 0), "Synced: nothing new to book.");
  assert.equal(releasedWords("$18,600.00", "GLS-2101", "Financial Controller (demo)"), "Released $18,600.00 on job GLS-2101, co-signed by Financial Controller (demo).");
  assert.equal(renderedWords(2, "Conditional release"), "Rendered version 2 of Conditional release.");
  assert.equal(heldNowWords("$18,600.00", 62), "$18,600.00 held, oldest 62 days");
  assert.equal(heldNowWords("$2,900.00", 1), "$2,900.00 held, oldest 1 day");
  assert.equal(heldNowWords(null, 0), "Nothing held.");
  assert.equal(holdingWords(4), "4 jobs hold retention");
  assert.equal(holdingWords(1), "1 job holds retention");
  assert.equal(holdingWords(0), "No job holds retention");
  assert.equal(releaseStatusWord("RECEIVABLE", true), "Release due");
  assert.equal(releaseStatusWord("RECEIVABLE", false), "Not yet");
  assert.equal(releaseStatusWord("PAYABLE", false), "Released by hand");
  assert.equal(noteWords("past the release window, action overdue"), "Past the release window, action overdue.");
  assert.equal(noteWords("at/over the 45-day window, eligible to release"), "At/over the 45-day window, eligible to release.");
  assert.equal(noteWords(""), "");
});

test("a co-signer is anyone on the roster the core would take: a Financial Controller or Payables and Receivables", () => {
  const person = (user_id, display_name, role_code) => ({ user_id, display_name, role_code, email: "", role_name: "", tier: 1, scope: "GLOBAL", branch_ids: [] });
  const roster = [person("a", "General Manager (demo)", "GENERAL_MANAGER"), person("b", "Financial Controller (demo)", "FINANCIAL_CONTROLLER"), person("c", "Payables and Receivables (demo)", "payables_receivables"), person("d", "Sales manager (demo)", "SALES_MANAGER")];
  assert.deepEqual(cosigners(roster, "a").map((p) => p.user_id), ["b", "c"]);
  assert.deepEqual(cosigners(roster, "b").map((p) => p.user_id), ["c"], "never the person releasing");
  assert.deepEqual(cosigners([], "a"), []);
});
