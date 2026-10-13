// The tax slice's plain logic: the rate a person types, the words for what the core stores, which rows are in
// force, how the core's list shapes are read, and every refusal in words with none of the core's Ontario words
// left in it.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  REASONS,
  areaForNewRows,
  areaWord,
  attestationWords,
  blockReasonWords,
  dayWords,
  forcedEvidence,
  hostDay,
  isDay,
  isProfileCode,
  isUuid,
  listOf,
  periodWords,
  profileStatusWord,
  profileTone,
  rateState,
  reasonWord,
  refusalWords,
  roundModeWord,
  varianceWords,
} from "../src/tax/tax-logic.ts";
import { bpsToField, formatMoney, formatRate, parseRateToBps } from "../src/tax/tax-money.ts";

test("a rate typed as a decimal becomes whole hundredths of a percent, never through a float", () => {
  assert.equal(parseRateToBps("7.75"), 775);
  assert.equal(parseRateToBps("7.75%"), 775);
  assert.equal(parseRateToBps(" 7.5 "), 750);
  assert.equal(parseRateToBps("0"), 0);
  assert.equal(parseRateToBps("10"), 1000);
  assert.equal(parseRateToBps("100"), 10000);
  assert.equal(parseRateToBps("0.05"), 5);
  // The case a float multiply gets wrong: 1.005 would round the half hundredth either way, so it is refused.
  assert.equal(parseRateToBps("1.005"), null);
  assert.equal(parseRateToBps("100.01"), null);
  assert.equal(parseRateToBps("101"), null);
  assert.equal(parseRateToBps("-1"), null);
  assert.equal(parseRateToBps("7,75"), null);
  assert.equal(parseRateToBps("seven"), null);
  assert.equal(parseRateToBps(""), null);
  assert.equal(parseRateToBps("%"), null);
  assert.equal(parseRateToBps("7."), null);
});

test("a rate reads as a percent and types back as the same number", () => {
  assert.equal(formatRate(775), "7.75%");
  assert.equal(formatRate(0), "0%");
  assert.equal(formatRate(1000), "10%");
  assert.equal(formatRate(750), "7.5%");
  assert.equal(formatRate(5), "0.05%");
  assert.equal(formatRate(10000), "100%");
  assert.equal(formatRate(undefined), "(no rate)");
  assert.equal(formatRate(1.5), "(no rate)");
  assert.equal(formatRate(-5), "(no rate)");
  for (const bps of [0, 5, 50, 775, 1000, 10000]) assert.equal(parseRateToBps(bpsToField(bps)), bps);
});

test("money reads in dollars and an unknown figure reads as a placeholder, never a zero", () => {
  assert.equal(formatMoney(482375), "$4,823.75");
  assert.equal(formatMoney(0), "$0.00");
  assert.equal(formatMoney(undefined), "(no figure)");
  assert.equal(formatMoney(12.5), "(no figure)");
});

test("every reason has a word of ours and none says HST, GST, Ontario or a band", () => {
  for (const code of [...REASONS, "FED_S87_GOODS_ON_RES", "FNT_PRODUCT", "SOMETHING_NEW"]) {
    const word = reasonWord(code);
    assert.ok(!/HST|GST|PST|CAD|Ontario|band|status card|First Nation|federal/i.test(word), `${code} reads ${word}`);
  }
  assert.equal(reasonWord("STD_ON_HST_13"), "Standard sales tax");
  assert.equal(reasonWord("SOMETHING_NEW"), "Other reason");
  assert.equal(areaWord("CA"), "California");
  assert.equal(areaWord("XX"), "XX");
});

test("a rate row is in force from its first day to its last, both included", () => {
  assert.equal(rateState("2030-01-01", undefined, "2030-01-01"), "in-force");
  assert.equal(rateState("2030-01-01", "", "2030-06-01"), "in-force");
  assert.equal(rateState("2030-01-01", "2030-06-01", "2030-06-01"), "in-force");
  assert.equal(rateState("2030-01-01", "2030-06-01", "2030-06-02"), "ended");
  assert.equal(rateState("2030-01-02", undefined, "2030-01-01"), "scheduled");
});

test("new rows join the tax area of the standard rate in force, else the first row, else nothing", () => {
  const rows = [
    { jurisdiction: "AA", reason_code: "MANUAL_TAX_REVIEW", effective_from: "2000-01-01" },
    { jurisdiction: "CA", reason_code: "STD_ON_HST_13", effective_from: "2000-01-01" },
    { jurisdiction: "ZZ", reason_code: "STD_ON_HST_13", effective_from: "2000-01-01", effective_to: "2001-01-01" },
  ];
  assert.equal(areaForNewRows(rows, "2030-01-01"), "CA");
  assert.equal(areaForNewRows(rows.slice(0, 1), "2030-01-01"), "AA");
  assert.equal(areaForNewRows([], "2030-01-01"), "");
});

test("a profile's standing reads in words with the tone behind them", () => {
  assert.equal(profileStatusWord("ACTIVE"), "In force");
  assert.equal(profileStatusWord("PENDING"), "Waiting for review");
  assert.equal(profileStatusWord("RETIRED"), "Retired");
  assert.equal(profileStatusWord("ON_HOLD"), "on hold");
  assert.equal(profileTone("PENDING"), "watch");
  assert.equal(profileTone("ACTIVE"), "good");
  assert.equal(profileTone("RETIRED"), "plain");
});

test("what a profile forces reads as one sentence", () => {
  assert.equal(forcedEvidence({ requires_photo: true, requires_number: true, requires_attestation: true }), "Requires the photo of the certificate, certificate number and the signed statement");
  assert.equal(forcedEvidence({ requires_photo: false, requires_number: true, requires_attestation: false }), "Requires the certificate number");
  assert.equal(forcedEvidence({ requires_photo: false, requires_number: false, requires_attestation: false }), "Nothing is required");
});

test("a profile code is capitals, digits and underscores", () => {
  assert.ok(isProfileCode("RESALE"));
  assert.ok(isProfileCode("EXEMPT_ORG_2"));
  assert.ok(!isProfileCode("resale"));
  assert.ok(!isProfileCode(""));
  assert.ok(!isProfileCode("has space"));
});

test("the round modes and the evidence banners read in our words", () => {
  assert.equal(roundModeWord("INVOICE_TOTAL"), "Round once, on the invoice total");
  assert.equal(roundModeWord("PER_LINE"), "Round each line");
  assert.equal(attestationWords("PENDING_LEGAL_REVIEW"), "The signed statement's wording is waiting for legal review.");
  assert.equal(attestationWords(undefined), "");
  assert.equal(blockReasonWords("status card (TCRD) expired"), "The certificate has expired.");
  assert.equal(blockReasonWords("attestation missing"), "The signed statement is missing.");
  assert.equal(blockReasonWords(undefined), "");
});

test("the variance is said in words, with its direction", () => {
  assert.deepEqual(varianceWords(0), { word: "Tied out", tone: "good", detail: "The ledger holds the tax the sales collected." });
  assert.equal(varianceWords(-1350).word, "Off by $13.50");
  assert.equal(varianceWords(-1350).tone, "stop");
  assert.equal(varianceWords(-1350).detail, "The ledger holds less tax than the sales collected.");
  assert.equal(varianceWords(250).detail, "The ledger holds more tax than the sales collected.");
});

test("dates read from their digits and a real day is the only day accepted", () => {
  assert.equal(dayWords("2030-06-04T00:00:00Z"), "Jun 4, 2030");
  assert.equal(dayWords("not a date"), "not a date");
  assert.equal(periodWords(2030, 6), "June 2030");
  assert.equal(periodWords(2030, 0), "2030");
  assert.ok(isDay("2030-02-28"));
  assert.ok(!isDay("2030-02-30"));
  assert.ok(!isDay("2030-2-3"));
  assert.ok(!isDay("June 4"));
  assert.match(hostDay(new Date(2030, 0, 5)), /^2030-01-05$/);
  assert.ok(isUuid("7a000000-0000-4000-8000-000000000001"));
  assert.ok(!isUuid("order 12"));
});

test("a list is read as a bare array, as its name, as a page, or as a Go nil slice, and nothing else", () => {
  assert.deepEqual(listOf([1], "rates"), [1]);
  assert.deepEqual(listOf({ rates: [1, 2] }, "rates"), [1, 2]);
  assert.deepEqual(listOf({ rates: null }, "rates"), []);
  assert.deepEqual(listOf({ items: [3] }, "rates"), [3]);
  assert.equal(listOf({}, "rates"), null);
  assert.equal(listOf({ rates: "x" }, "rates"), null);
  assert.equal(listOf({ profiles: [1] }, "rates"), null);
  assert.equal(listOf(null, "rates"), null);
  assert.equal(listOf("rates", "rates"), null);
});

const OLD_WORDS = /HST|GST|PST|CAD|Ontario|band.card|TCRD|status card|First Nation|\bON_|FED_|FNGST|avalara/i;

test("a refusal names who may do it and never shows the bare word Forbidden", () => {
  for (const action of ["rate", "profile", "config", "recompute", "capture", "evidence", "load", "recon"]) {
    const words = refusalWords(action, "Forbidden", 403);
    assert.ok(!/^Forbidden/.test(words), `${action}: ${words}`);
    assert.ok(/Financial Controller|manager|staff/.test(words), `${action}: ${words}`);
  }
  assert.match(refusalWords("rate", "Forbidden", 403), /Financial Controller/);
  assert.match(refusalWords("evidence", "Forbidden", 403), /manager/);
});

test("the core's sentences are rewritten into what to do next, with none of its old words", () => {
  const cases = [
    ["rate", "reason_code required", "Choose a reason first."],
    ["rate", 'unknown reason_code "X": not one of the lawful reason codes the rate table can carry', "Not added: that reason is not one the rate table can carry. Choose one from the list."],
    ["rate", "rate_bps must be 0..10000", "The rate must be between 0 and 100 percent."],
    ["rate", "effective_from must be YYYY-MM-DD", "The first day must be a real day, written year, month, day with dashes."],
    [
      "rate",
      'tax reason "FNT_PRODUCT" resolves 100 bps, below a standard of 775 bps, without an evidence gate and without the manual exception: the rate row is misconfigured against the gate map',
      "Not added: this reason may not charge less than the standard rate (7.75%) unless it needs a certificate or proof of delivery. Its rate was 1%. Raise it, or choose a reason that needs evidence.",
    ],
    ["profile", "code required", "Give the profile a code first."],
    ["profile", "a relief profile must force the band-card number as evidence", "Not saved: a profile that charges less than the standard rate must require the certificate number. Tick Certificate number, or raise the rate."],
    ["capture", "band-card photo is required for this profile", "Not recorded: this profile requires the photo of the certificate."],
    ["capture", "band-card number is required for this profile", "Not recorded: this profile requires the certificate number."],
    ["capture", "attestation is required for this profile", "Not recorded: this profile requires the signed statement."],
    ["capture", "unknown card_profile_code", "Not recorded: that profile is not on file. Reload the profiles and choose one."],
    ["config", "round_mode must be INVOICE_TOTAL or PER_LINE", "Choose how the tax is rounded: once on the invoice total, or on each line."],
    ["config", "avalara/postal routing are dormant at W6 and cannot be enabled", "Automatic tax lookup and postal routing are not available yet, so they cannot be turned on."],
    ["evidence", "evidence not found", "That certificate record is not there any more. Reload the list."],
    ["recon", "year required", "Give the year to reconcile."],
  ];
  for (const [action, message, want] of cases) {
    const words = refusalWords(action, message, 400);
    assert.equal(words, want);
    assert.ok(!OLD_WORDS.test(words), `${message} still reads ${words}`);
  }
});

test("a sentence the screen does not know keeps the core's meaning in our words, and a server fault says the service did not answer", () => {
  const unknown = refusalWords("rate", 'something about "STD_ON_HST_13" and the HST band-card', 400);
  assert.ok(!OLD_WORDS.test(unknown), unknown);
  assert.match(unknown, /Standard sales tax/);
  assert.equal(refusalWords("recompute", "internal error", 500), "The tax service did not answer. Reload to try again.");
  assert.equal(refusalWords("load", "", 502), "The tax service did not answer. Reload to try again.");
  assert.equal(refusalWords("load", "Unauthorized", 401), "Your sign in has ended. Sign in again and retry.");
  assert.equal(refusalWords("load", "not found", 404), "Not found. Reload to see what is there now.");
});
