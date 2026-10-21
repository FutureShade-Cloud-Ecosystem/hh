// The bank reconciliation slice's plain logic: the money it reads and the one amount it parses, the words for every
// state the core stores, which row action a line admits, how a statement's dates read, how the core's refusals read
// out loud, and how the rule form becomes the spec the core stores. No money is computed here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMoney, MONEY_PLACEHOLDER, parseBalanceToCents, parseToleranceToCents, centsToField } from "../src/bankrec/bankrec-money.ts";
import {
  accountTypeWord,
  dayWords,
  importFailedWords,
  isUuid,
  lineAction,
  lineStatusWord,
  lineTone,
  matchMethodWord,
  periodWords,
  reconStatusWord,
  refusalWords,
  ruleKindWord,
  ruleSpec,
  ruleSpecFields,
  ruleStatusWord,
  targetWord,
  varianceWords,
} from "../src/bankrec/bankrec-logic.ts";

test("cents read as dollars, and a missing figure is never a plausible zero", () => {
  assert.equal(formatMoney(2525575), "$25,255.75");
  assert.equal(formatMoney(-4500), "-$45.00");
  assert.equal(formatMoney(0), "$0.00");
  assert.equal(formatMoney(undefined), MONEY_PLACEHOLDER);
  assert.equal(formatMoney(12.5), MONEY_PLACEHOLDER);
  assert.equal(centsToField(61050), "610.50");
});

test("a statement balance is parsed through the decimal string, may be negative, and is refused when it is not money", () => {
  assert.equal(parseBalanceToCents("25,255.75"), 2525575);
  assert.equal(parseBalanceToCents("$25,255.75"), 2525575);
  assert.equal(parseBalanceToCents("0.10"), 10);
  assert.equal(parseBalanceToCents("19.68"), 1968);
  assert.equal(parseBalanceToCents("-1,200.5"), -120050);
  assert.equal(parseBalanceToCents("(45.00)"), -4500);
  assert.equal(parseBalanceToCents("1.005"), null, "three decimals are refused, never rounded");
  assert.equal(parseBalanceToCents("12,34.00"), null);
  assert.equal(parseBalanceToCents("abc"), null);
  assert.equal(parseBalanceToCents(""), null);
  assert.equal(parseBalanceToCents("1234567890"), null, "more than nine digits is refused");
});

test("a tolerance is unsigned dollars and cents", () => {
  assert.equal(parseToleranceToCents("0"), 0);
  assert.equal(parseToleranceToCents("0.00"), 0);
  assert.equal(parseToleranceToCents("1.50"), 150);
  assert.equal(parseToleranceToCents("-1.50"), null);
  assert.equal(parseToleranceToCents("one"), null);
});

test("each state the core stores has a word, and an unknown value is opened out rather than hidden", () => {
  assert.equal(accountTypeWord("BANK"), "Bank account");
  assert.equal(accountTypeWord("CREDIT_CARD"), "Credit card");
  assert.equal(accountTypeWord("NEW_KIND"), "new kind");
  assert.equal(lineStatusWord("UNMATCHED"), "Unmatched");
  assert.equal(lineStatusWord("MATCHED"), "Matched");
  assert.equal(lineStatusWord("EXCLUDED"), "Excluded");
  assert.equal(lineTone("UNMATCHED"), "watch");
  assert.equal(lineTone("MATCHED"), "good");
  assert.equal(lineTone("EXCLUDED"), "plain");
  assert.equal(matchMethodWord("AUTO"), "Automatic");
  assert.equal(matchMethodWord("MANUAL"), "Manual");
  assert.equal(targetWord("PAYMENT"), "Payment");
  assert.equal(targetWord("LODGEMENT"), "Lodgement");
  assert.equal(targetWord("JOURNAL"), "Journal");
  assert.equal(targetWord("GROUP"), "Group");
  assert.equal(ruleStatusWord("APPROVED"), "In use");
  assert.equal(ruleStatusWord("RETIRED"), "Retired");
  assert.equal(ruleStatusWord("DRAFT"), "Draft");
  assert.equal(reconStatusWord("OPEN"), "Open");
  assert.equal(reconStatusWord("COMPLETED"), "Signed off");
  assert.equal(ruleKindWord("gateway_ref_exact"), "Card settlement, reference exact");
  assert.equal(ruleKindWord("lodgement_ref_exact"), "Cash or cheque lodgement, reference exact");
  assert.equal(ruleKindWord("descriptor_fuzzy"), "Batch settlement, description and date window");
});

test("a line admits one row action by its status, and an excluded or unknown line admits none", () => {
  assert.equal(lineAction("UNMATCHED"), "match");
  assert.equal(lineAction("MATCHED"), "unmatch");
  assert.equal(lineAction("EXCLUDED"), null);
  assert.equal(lineAction("SOMETHING_NEW"), null);
});

test("a statement date reads as a day, a period as a month and year", () => {
  assert.equal(dayWords("2031-06-03T00:00:00Z"), "Jun 3, 2031");
  assert.equal(dayWords("2031-12-31"), "Dec 31, 2031");
  assert.equal(dayWords("garbage"), "garbage");
  assert.equal(periodWords(2031, 6), "June 2031");
  assert.equal(periodWords(2031, 13), "2031-13");
});

test("the variance reads in words with the direction, and zero is balanced", () => {
  assert.deepEqual(varianceWords(0), { word: "Balanced", tone: "good", detail: "The statement and the ledger agree." });
  const over = varianceWords(586050);
  assert.equal(over.tone, "stop");
  assert.equal(over.word, "Off by $5,860.50");
  assert.match(over.detail, /statement is higher than the ledger/);
  const under = varianceWords(-4500);
  assert.equal(under.word, "Off by $45.00");
  assert.match(under.detail, /statement is lower than the ledger/);
});

test("an id is a uuid before it is sent", () => {
  assert.equal(isUuid("2f6a1c00-0000-4000-9000-0000000000f1"), true);
  assert.equal(isUuid(" 2F6A1C00-0000-4000-9000-0000000000F1 "), true);
  assert.equal(isUuid("pay-1"), false);
  assert.equal(isUuid(""), false);
});

test("the core's refusals read in words that say what to do, and a walled route names who may use it", () => {
  assert.equal(refusalWords("import", "Forbidden", 403), "Importing a statement is for the Financial Controller or a manager. Sign out and in again as one of them if this was not the role you meant.");
  assert.equal(refusalWords("complete", "Forbidden", 403), "Signing off a month is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.");
  assert.equal(refusalWords("rule", "Forbidden", 403), "Approving a match rule is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.");
  assert.equal(refusalWords("match", "Forbidden", 403), "Matching is for the Financial Controller or a manager. Sign out and in again as one of them if this was not the role you meant.");
  assert.equal(
    refusalWords("complete", "cannot complete, 2 UNMATCHED line(s) remain for 2031-06", 422),
    "Not signed off: 2 statement lines are still unmatched for June 2031. Match them, or refresh the figures if you just did.",
  );
  assert.equal(
    refusalWords("complete", "cannot complete, 1 UNMATCHED line(s) remain for 2031-06", 422),
    "Not signed off: 1 statement line is still unmatched for June 2031. Match it, or refresh the figures if you just did.",
  );
  assert.equal(
    refusalWords("complete", "cannot complete, variance is 586050 cents (must be zero)", 422),
    "Not signed off: the statement and the ledger are $5,860.50 apart, and they must agree. Match the open lines, then refresh the figures.",
  );
  assert.equal(refusalWords("complete", "cannot complete, variance is -4500 cents (must be zero)", 422), "Not signed off: the statement and the ledger are $45.00 apart, and they must agree. Match the open lines, then refresh the figures.");
  assert.equal(refusalWords("complete", "reconciliation abc is already COMPLETED", 409), "That month is already signed off.");
  assert.equal(refusalWords("open", "reconciliation for 2031-06 is already COMPLETED", 409), "June 2031 is already signed off for this account, so its figures no longer change.");
  assert.equal(refusalWords("match", "statement line abc is MATCHED, not UNMATCHED", 409), "That line is already matched. Reload to see where it stands.");
  assert.equal(refusalWords("match", "statement line abc is not UNMATCHED", 409), "That line is already matched. Reload to see where it stands.");
  const causes = "it is already matched to another line, it was reversed, or it is not a received payment.";
  // The core's sentence names the payment that did not move by its NUMBER, and three causes; the dealer's
  // sentence carries the number and never a uuid.
  assert.equal(
    refusalWords("match", "payment already claimed, reversed or not RECEIVED: PAY-000104 (claimed 0 of 1)", 409),
    `Payment PAY-000104 is not waiting to be deposited: ${causes}`,
  );
  assert.equal(
    refusalWords("match", "bankrec: payment already claimed, reversed or not RECEIVED: PAY-000101, PAY-000102 (claimed 1 of 3)", 409),
    `Payments PAY-000101, PAY-000102 are not waiting to be deposited: for each, ${causes}`,
  );
  assert.equal(
    refusalWords("match", "payment already claimed, reversed or not RECEIVED: a payment that cannot be found (claimed 0 of 1)", 409),
    "That payment cannot be found, so it cannot be deposited.",
  );
  assert.doesNotMatch(refusalWords("match", "payment already claimed, reversed or not RECEIVED: PAY-000104 (claimed 0 of 1)", 409), /[0-9a-f]{8}-[0-9a-f]{4}-/);
  // A sentence without ids (an older core) still reads as words.
  assert.equal(refusalWords("match", "payment already claimed, reversed or not RECEIVED (claimed 0 of 1)", 409), `That payment is not waiting to be deposited: ${causes}`);
  // No refusal text a dealer reads carries a uuid: every core sentence that holds an id is rewritten without it.
  const someId = "b9000000-0000-4000-8000-0000000000ff";
  for (const [op, text] of [
    ["match", `bankrec: statement line ${someId} is MATCHED, not UNMATCHED`],
    ["match", `bankrec: statement line ${someId} is not UNMATCHED`],
    ["match", `bankrec: line ${someId} / target ${someId} already has a live match`],
    ["unmatch", `bankrec: match ${someId} is not live (already unmatched)`],
    ["open", `bankrec: reconciliation ${someId} is not OPEN`],
    ["match", "bankrec: payment already claimed, reversed or not RECEIVED: PAY-000104 (claimed 0 of 1)"],
  ]) {
    assert.doesNotMatch(refusalWords(op, text, 409), /[0-9a-f]{8}-[0-9a-f]{4}-/, `${text} reaches the dealer with an id`);
  }
  // SF-2j: what a manual match must explain, and the signed off month, in the dealer's words.
  assert.equal(refusalWords("match", "bankrec: a group match is not available yet, match the line to a payment, a lodgement or a journal", 422), "A group match is not available yet. Match the line to a payment, a lodgement or a journal.");
  assert.equal(refusalWords("match", "bankrec: that lodgement is for 5000 cents and the line is 500000 cents, so it does not explain the line", 422), `That lodgement is ${formatMoney(5000)} and the line is ${formatMoney(500000)}, so it does not explain the line. Pick the lodgement for the same amount.`);
  assert.equal(refusalWords("match", "bankrec: that journal is not posted, so it cannot explain a bank line", 422), "That journal is not posted yet, so it cannot explain a bank line.");
  assert.equal(refusalWords("match", "bankrec: that journal does not touch this bank account", 422), "That journal does not move this bank account, so it cannot explain this line.");
  assert.equal(refusalWords("match", "bankrec: that journal moves this bank account by 3000 cents and the line is 5000 cents", 422), `That journal moves this account by ${formatMoney(3000)} and the line is ${formatMoney(5000)}. Pick the journal for the line's amount.`);
  assert.equal(refusalWords("match", "bankrec: that journal already explains another statement line", 409), "That journal already explains another line.");
  assert.equal(refusalWords("unmatch", "bankrec: that match sits in 2026-06, which is already reconciled and completed, so it cannot be unmatched", 409), `That match belongs to ${periodWords(2026, 6)}, which is already signed off, so it cannot be undone.`);
  assert.equal(refusalWords("match", "line abc / target def already has a live match", 409), "That target is already matched to this line.");
  assert.equal(refusalWords("unmatch", "match abc is not live (already unmatched)", 409), "That match was already undone. Reload to see where the line stands.");
  assert.equal(refusalWords("rule", "another APPROVED version of \"x\" already exists", 409), "Another version of this rule was approved a moment ago. Reload the rules and try again.");
  assert.equal(refusalWords("rule", "spec.kind must be one of gateway_ref_exact, lodgement_ref_exact, descriptor_fuzzy", 422), "Pick one of the three match kinds.");
  assert.equal(refusalWords("match", "bankrec: not found", 404), "Not found. Reload to see what is there now.");
  assert.equal(refusalWords("load", "not found", 404), "Not found. Reload to see what is there now.");
  assert.equal(refusalWords("load", "internal error", 500), "The bank reconciliation service did not answer. Reload to try again.");
  assert.equal(refusalWords("load", "", 0), "The bank reconciliation service did not answer. Reload to try again.");
  assert.equal(refusalWords("open", "valid year and month (1-12) are required", 400), "Valid year and month (1-12) are required.");
});

test("a failed import says what the file lacked in words a bookkeeper can act on", () => {
  assert.equal(
    importFailedWords('statement parse failed: bankrec: csv is missing the mapped column "Amount"'),
    'Not imported: the file has no "Amount" column. The first row must name Date, Description, Amount and Reference.',
  );
  assert.equal(
    importFailedWords('statement parse failed: bankrec: csv row 3: bad date "June 4" (want "2006-01-02"): parsing time "June 4" as "2006-01-02": cannot parse "June 4" as "2006"'),
    'Not imported: row 3 has a date the file format does not read ("June 4"). Dates are written year, month, day, with dashes.',
  );
  assert.equal(
    importFailedWords('statement parse failed: bankrec: csv row 5: bad amount "12..5": not a number: "12..5"'),
    'Not imported: row 5 has an amount that is not money ("12..5"). Amounts are dollars and cents; a debit has a minus sign or brackets.',
  );
  // The envelope the core writes carries the parser's sentence with its module prefix and no wrapper.
  assert.equal(
    importFailedWords('bankrec: csv is missing the mapped column "Date"'),
    'Not imported: the file has no "Date" column. The first row must name Date, Description, Amount and Reference.',
  );
  assert.equal(importFailedWords("statement parse failed: bankrec: read csv header: EOF"), "Not imported: the file is empty or has no header row.");
  assert.equal(importFailedWords("statement parse failed: something else"), "Not imported: something else.");
  assert.equal(importFailedWords(""), "Not imported: the file could not be read.");
});

test("the rule form becomes the spec the core stores, and a stored spec fills the form back", () => {
  assert.deepEqual(ruleSpec({ kind: "gateway_ref_exact", toleranceCents: 0, descriptor: "", windowDays: 0, feeSplit: false, feeAccount: "" }), {
    kind: "gateway_ref_exact",
    target: "PAYMENT",
    tolerance_cents: 0,
  });
  assert.deepEqual(ruleSpec({ kind: "lodgement_ref_exact", toleranceCents: 0, descriptor: "", windowDays: 0, feeSplit: false, feeAccount: "" }), {
    kind: "lodgement_ref_exact",
    target: "LODGEMENT",
    tolerance_cents: 0,
  });
  assert.deepEqual(ruleSpec({ kind: "descriptor_fuzzy", toleranceCents: 0, descriptor: "RUN", windowDays: 3, feeSplit: true, feeAccount: "6300" }), {
    kind: "descriptor_fuzzy",
    target: "PAYMENT",
    descriptor_ilike: "%RUN%",
    date_window_days: 3,
    allow_fee_split: true,
    fee_account_code: "6300",
  });
  assert.equal(ruleSpec({ kind: "descriptor_fuzzy", toleranceCents: 0, descriptor: "%RUN%", windowDays: 3, feeSplit: false, feeAccount: "" }).descriptor_ilike, "%RUN%", "a pattern already carrying percent signs is kept");
  assert.deepEqual(ruleSpecFields({ kind: "descriptor_fuzzy", target: "PAYMENT", descriptor_ilike: "%RUN%", date_window_days: 3, allow_fee_split: true, fee_account_code: "6300" }), {
    kind: "descriptor_fuzzy",
    toleranceCents: 0,
    descriptor: "RUN",
    windowDays: 3,
    feeSplit: true,
    feeAccount: "6300",
  });
  assert.deepEqual(ruleSpecFields({ kind: "gateway_ref_exact", tolerance_cents: 150 }).toleranceCents, 150);
  assert.equal(ruleSpecFields({}).kind, "gateway_ref_exact", "a spec with no kind falls back to the first kind, never a blank");
});
