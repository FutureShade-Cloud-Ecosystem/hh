// The credit limit's change words and gates (CC-2): who may see and decide, how a waiting
// request, a decision and a refusal read to a bookkeeper. No money is worked out here; the
// figures are the core's row, read as words.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canDecideCreditChanges,
  canWorkCreditChanges,
  creditApproveWords,
  creditAskWords,
  creditBlockedWords,
  creditDecisionWords,
  creditPendingWords,
  creditRefusalWords,
  creditRejectWords,
  creditRequestWords,
  creditWho,
} from "../src/ar/ar-logic.ts";

const ME = "u-me";
const base = {
  change_kind: "LIMIT_CHANGE",
  previous_limit_cents: 500_000,
  new_limit_cents: 800_000,
  delta_cents: 300_000,
  reason: "Winter work",
  status: "PENDING",
  required_approvals: 1,
  requested_by: "u-ask",
  requested_by_role: "SALES_MANAGER",
  requested_at: "2000-01-05T15:00:00Z",
};

test("the credit change controls reach only the roles the route admits, and only deciders decide", () => {
  for (const role of ["financial_controller", "general_manager", "payables_receivables", "branch_manager", "sales_manager", "super_admin"]) {
    assert.equal(canWorkCreditChanges(role), true, role);
  }
  for (const role of ["inside_sales", "outside_sales", "yard_team", "driver", "hr", ""]) {
    assert.equal(canWorkCreditChanges(role), false, role);
    assert.equal(canDecideCreditChanges(role), false, role);
  }
  for (const role of ["branch_manager", "financial_controller", "general_manager", "super_admin"]) {
    assert.equal(canDecideCreditChanges(role), true, role);
  }
  for (const role of ["sales_manager", "payables_receivables", "procurement_manager"]) {
    assert.equal(canDecideCreditChanges(role), false, role);
  }
});

test("a person is named by role or as you, never by id", () => {
  assert.equal(creditWho(ME, "SALES_MANAGER", ME), "you");
  assert.equal(creditWho("u-x", "BRANCH_MANAGER", ME), "the branch manager");
  assert.equal(creditWho("u-x", "WHO_KNOWS", ME), "a manager");
  assert.equal(creditWho(undefined, undefined, ME), "a manager");
});

test("a waiting request says which approval it waits for", () => {
  assert.equal(creditPendingWords(base, ME), "Waiting for one approval");
  assert.equal(creditPendingWords({ ...base, required_approvals: 2 }, ME), "Waiting for two approvals");
  const first = creditPendingWords({ ...base, required_approvals: 2, first_approved_by: "u-fc", first_approved_by_role: "FINANCIAL_CONTROLLER", first_approved_at: "2000-01-06T15:00:00Z" }, ME);
  assert.match(first, /^First of two approvals recorded \(the financial controller, Jan 6, 2000\)\. Waiting for the second\.$/);
});

test("the ask and the answers are in dollars with the direction in words", () => {
  assert.equal(creditAskWords(base), "$8,000.00, up $3,000.00 from $5,000.00");
  assert.match(creditRequestWords({ ...base, status: "APPLIED", required_approvals: 0, delta_cents: -100_000, new_limit_cents: 400_000 }), /now \$4,000\.00, down \$1,000\.00 from \$5,000\.00\. The change is in place\.$/);
  assert.match(creditRequestWords(base), /waiting for one approval\. Nothing has moved: the limit stays \$5,000\.00/);
  assert.match(creditApproveWords({ ...base, required_approvals: 2 }), /^First of two approvals recorded\. Nothing has moved/);
  assert.match(creditApproveWords({ ...base, status: "APPLIED" }), /^Approved: the credit limit is now \$8,000\.00/);
  assert.match(creditRejectWords({ ...base, status: "REJECTED" }), /The limit stays \$5,000\.00; the asked \$8,000\.00 will not take effect\./);
});

test("the history line names who decided, with the reason of a rejection", () => {
  assert.equal(
    creditDecisionWords({ ...base, status: "REJECTED", rejected_by: "u-x", rejected_by_role: "BRANCH_MANAGER", rejected_at: "2000-01-07T15:00:00Z", rejection_reason: "Past due" }, ME),
    "Rejected by the branch manager on Jan 7, 2000: Past due.",
  );
  assert.equal(creditDecisionWords({ ...base, status: "APPLIED", required_approvals: 0, applied_at: "2000-01-05T15:00:00Z" }, ME), "Took effect the day it was asked, Jan 5, 2000.");
  assert.equal(
    creditDecisionWords({ ...base, status: "APPLIED", first_approved_by: "u-a", first_approved_by_role: "BRANCH_MANAGER", second_approved_by: ME, second_approved_by_role: "GENERAL_MANAGER", applied_at: "2000-01-08T15:00:00Z" }, ME),
    "Approved by the branch manager and you; in place since Jan 8, 2000.",
  );
});

test("a refusal keeps the server's sentence, with cents read as dollars", () => {
  assert.equal(creditRefusalWords("credit change: requester cannot approve their own request", 403), "Credit change: requester cannot approve their own request.");
  assert.equal(creditRefusalWords("limit of 250000 cents is over", 400), "Limit of $2,500.00 is over.");
  assert.match(creditRefusalWords("boom", 500), /Nothing was changed/);
  assert.match(creditRefusalWords("x", undefined), /could not be reached/);
  assert.match(creditRefusalWords("x", 401), /Sign in again/);
});

test("the person who asked decides nothing, and the first approver cannot give the second approval", () => {
  const mine = { ...base, requested_by: ME };
  assert.match(creditBlockedWords(mine, ME).approve, /You asked for this one/);
  assert.match(creditBlockedWords(mine, ME).reject, /You asked for this one/);
  const approved = { ...base, required_approvals: 2, first_approved_by: ME };
  assert.match(creditBlockedWords(approved, ME).approve, /You approved this one; it needs a second person/);
  assert.equal(creditBlockedWords(approved, ME).reject, "");
  assert.deepEqual(creditBlockedWords(approved, "u-other"), { approve: "", reject: "" });
  assert.deepEqual(creditBlockedWords(base, ""), { approve: "", reject: "" });
});
