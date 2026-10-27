// The ledger slice's plain logic: how the core's two answer shapes are read,
// what each approval kind is called, who may decide it, and how a refusal
// reads out loud. No money is computed here; the cases hold the display words.
import { test } from "node:test";
import assert from "node:assert/strict";
import { formatMoney, MONEY_PLACEHOLDER } from "../src/gl/gl-money.ts";
import {
  approvalKind,
  bpsWords,
  detailWords,
  floorFigures,
  floorWords,
  approvalTitle,
  canDecide,
  journalTone,
  journalWord,
  refusalWords,
  postingQueueBannerWords,
} from "../src/gl/gl-logic.ts";

test("cents read as dollars, and a missing figure is never a plausible zero", () => {
  assert.equal(formatMoney(750000), "$7,500.00");
  assert.equal(formatMoney(0), "$0.00");
  assert.equal(formatMoney(undefined), MONEY_PLACEHOLDER);
  assert.equal(formatMoney(12.5), MONEY_PLACEHOLDER);
});

test("a figure in cents inside the core's sentence reads as dollars, and the rest of the sentence is left alone", () => {
  assert.equal(
    detailWords("Journal entry at/over the maker-checker threshold (750000 cents) awaiting approval"),
    "Journal entry at/over the maker-checker threshold ($7,500.00) awaiting approval",
  );
  assert.equal(detailWords("quote DEMO-PLM-Q1 line 1: 12 percent discount, margin 1630 bps against the 2000 bps floor"), "quote DEMO-PLM-Q1 line 1: 12 percent discount, margin 1630 bps against the 2000 bps floor");
  assert.equal(detailWords(""), "");
});

test("each approval kind has the words the queue draws", () => {
  assert.equal(approvalKind("MARGIN_BELOW_MINIMUM"), "Quote");
  assert.equal(approvalKind("JE_OVER_THRESHOLD"), "Journal");
  assert.equal(approvalKind("FREIGHT_ABOVE_QUOTE"), "Freight");
  assert.equal(approvalKind("CREDIT_LIMIT_EXCEEDED"), "Credit");
  assert.equal(approvalKind("COD_CONSTRAINT"), "COD");
  assert.equal(approvalTitle("MARGIN_BELOW_MINIMUM"), "Below floor quote request");
  assert.equal(approvalTitle("JE_OVER_THRESHOLD"), "Journal parked over the threshold");
  assert.equal(approvalTitle("FREIGHT_ABOVE_QUOTE"), "Freight above the quote");
  assert.equal(approvalKind("SOMETHING_NEW"), "Request");
  assert.equal(approvalTitle("SOMETHING_NEW"), "Approval request");
});

test("a request the caller filed is never theirs to decide, and a decided one is not either", () => {
  const row = { requested_by: "u-1", status: "PENDING" };
  assert.equal(canDecide(row, "u-2"), true);
  assert.equal(canDecide(row, "u-1"), false);
  assert.equal(canDecide({ ...row, status: "APPROVED" }, "u-2"), false);
  assert.equal(canDecide(row, ""), false, "no session user, nothing is decidable");
});

test("a journal state is a word before it is a colour", () => {
  assert.equal(journalWord("PENDING_APPROVAL"), "Pending approval");
  assert.equal(journalWord("POSTED"), "Posted");
  assert.equal(journalWord("DRAFT"), "Draft");
  assert.equal(journalWord("VOID"), "Void");
  assert.equal(journalTone("PENDING_APPROVAL"), "watch");
  assert.equal(journalTone("POSTED"), "good");
  assert.equal(journalTone("VOID"), "stop");
  assert.equal(journalTone("DRAFT"), "plain");
});

test("a refused decision names who may decide it, never the bare gate word", () => {
  const gate = refusalWords("decide", "Forbidden", 403);
  assert.ok(gate.includes("Financial Controller"), gate);
  assert.ok(gate.includes("General Manager"), gate);
  assert.ok(!gate.includes("Forbidden"), gate);
  const own = refusalWords("decide", "separation of duties: you cannot approve your own request", 403);
  assert.ok(own.includes("You filed this request"), own);
  assert.ok(own.includes("General Manager"), own);
  const gl = refusalWords("decide", "gl: an approver must differ from the journal's creator (four-eyes)", 403);
  assert.ok(gl.includes("You filed this request"), gl);
  assert.equal(refusalWords("decide", "request already APPROVED", 409), "Someone already decided this request. Reload to see it.");
});

test("any other refusal keeps the core's own sentence, capitalised and ended", () => {
  assert.equal(refusalWords("decide", "invalid approval id", 400), "Invalid approval id.");
  assert.equal(refusalWords("trial", "no fiscal period for 2031-01", 422), "No fiscal period for 2031-01.");
  assert.equal(refusalWords("trial", "", 500), "The ledger did not answer. Reload to try again.");
  // FW1-10: a read out of the caller's branch reach gets the way out beside it.
  assert.equal(
    refusalWords("trial", "gl: caller cannot access this branch", 403),
    "You cannot read the books of that branch. Choose a branch you hold.",
  );
});

test("a floor request reads in words: the quote, the line, the discount, the margins, no id and no bps", () => {
  const sentence =
    "quote DEMO-PLM-Q1 line 1 (62e4ad55-5a14-5945-80ee-866864a6c774): 12 percent discount, resulting margin 1630 bps against the 2000 bps floor";
  assert.deepEqual(floorFigures(sentence), { quote: "DEMO-PLM-Q1", line: "1", discount: "12%", marginAsked: "16.3%", floorMargin: "20.0%" });
  const words = floorWords(sentence);
  assert.equal(words, "Quote DEMO-PLM-Q1, line 1: a 12% discount asked, margin 16.3% against the 20.0% floor.");
  assert.ok(!words.includes("62e4ad55") && !words.includes("bps"));
  assert.equal(floorFigures("Freight on DEMO-JUL-O01 is being raised"), null);
  assert.equal(floorWords("something else"), null);
  assert.equal(bpsWords("2000"), "20.0%");
  assert.equal(bpsWords("95"), "0.9%");
  assert.equal(bpsWords("-120"), "-1.2%");
});

test("the posting queue banner reads its two counts, and an empty queue shows nothing", () => {
  assert.equal(postingQueueBannerWords({ waiting: 0, dead: 0 }), "");
  assert.equal(postingQueueBannerWords({ waiting: 1, dead: 0 }),
    "1 journal is waiting to post. The posting queue lists them for the Financial Controller.");
  assert.equal(postingQueueBannerWords({ waiting: 3, dead: 0 }),
    "3 journals are waiting to post. The posting queue lists them for the Financial Controller.");
  assert.equal(postingQueueBannerWords({ waiting: 0, dead: 1 }),
    "1 journal failed to post and is set aside. The posting queue lists them for the Financial Controller.");
  assert.equal(postingQueueBannerWords({ waiting: 0, dead: 2 }),
    "2 journals failed to post and are set aside. The posting queue lists them for the Financial Controller.");
  assert.equal(postingQueueBannerWords({ waiting: 1, dead: 4 }),
    "1 journal is waiting to post and 4 have failed and are set aside. The posting queue lists them for the Financial Controller.");
});
