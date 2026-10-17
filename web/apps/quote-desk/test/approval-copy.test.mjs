// The approval words, unit tested on the Node runner: the policy and decision
// labels, the detail line, who may approve spoken from the server's own roles,
// and the requester's view of a request in words. Ported from the ERP's
// _approval-copy test; the ERP's refusal softener is not ported (this app
// shows the server's refusal text verbatim) and the contract has no used state.
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  approvalDetail,
  approverWords,
  decisionWord,
  isDecidable,
  policyLabel,
  requesterStateWords,
  MARGIN_POLICY,
} from "../src/_approval-copy.js";

test("policyLabel names the five policy types and title cases an unknown one", () => {
  assert.equal(policyLabel("MARGIN_BELOW_MINIMUM"), "Below floor");
  assert.equal(policyLabel("CREDIT_LIMIT_EXCEEDED"), "Credit limit");
  assert.equal(policyLabel("COD_CONSTRAINT"), "COD hold");
  assert.equal(policyLabel("JE_OVER_THRESHOLD"), "Journal limit");
  assert.equal(policyLabel("FREIGHT_ABOVE_QUOTE"), "Freight raise");
  assert.equal(policyLabel("NEW_THING_HERE"), "New thing here");
  assert.equal(policyLabel("category_margin_floor"), "Category margin floor");
  assert.equal(MARGIN_POLICY, "MARGIN_BELOW_MINIMUM");
});

test("decisionWord words the three states and degrades honestly", () => {
  assert.equal(decisionWord("PENDING"), "Pending");
  assert.equal(decisionWord("APPROVED"), "Approved");
  assert.equal(decisionWord("REJECTED"), "Rejected");
  assert.equal(decisionWord("ON_HOLD"), "on hold");
  assert.equal(decisionWord(""), "");
});

test("approvalDetail keeps the server sentence and only humanises basis points", () => {
  assert.equal(approvalDetail("Quote Q-1, line 1, Charcoal, 10% discount, margin 20.0% against a floor of 25.0%"), "Quote Q-1, line 1, Charcoal, 10% discount, margin 20.0% against a floor of 25.0%");
  assert.equal(approvalDetail("margin 600 bps is below the 1200 bps floor"), "margin 6% is below the 12% floor");
  assert.equal(approvalDetail("   "), "Override requested");
});

test("approverWords speaks the roles exactly as the server names them", () => {
  assert.equal(approverWords(["General Manager", "Financial Controller", "Sales Manager"]), "the General Manager, the Financial Controller or the Sales Manager");
  assert.equal(approverWords(["the Sales manager"]), "the Sales manager");
  assert.equal(approverWords(["General Manager", "Sales Manager"]), "the General Manager or the Sales Manager");
  assert.equal(approverWords([]), "");
});

test("requesterStateWords is the requester's view of a request, with no used state", () => {
  assert.match(requesterStateWords("PENDING"), /pending/i);
  assert.match(requesterStateWords("APPROVED"), /approved/i);
  assert.match(requesterStateWords("REJECTED"), /rejected/i);
  assert.equal(requesterStateWords("USED"), "Your request is used.");
});

test("isDecidable is true only for a pending row, the server decides the rest", () => {
  assert.equal(isDecidable("PENDING"), true);
  assert.equal(isDecidable("APPROVED"), false);
  assert.equal(isDecidable("REJECTED"), false);
});
