// Names and words, never ids (round 10 item 3, decision log row 949).
import assert from "node:assert/strict";
import test from "node:test";
import { looksLikeId, personName, withoutIds } from "../src/_names.ts";
import { approvalDetail } from "../src/_approval-copy.ts";
import { changeActor, formatChangeValue, historyHeadline } from "../src/_quote-history.ts";

const ID = "de110000-0000-4000-9000-000000000002";

test("an id is recognised, a word is not", () => {
  assert.equal(looksLikeId(ID), true);
  assert.equal(looksLikeId("Sales manager (demo)"), false);
});

test("a person is their name from the roster, else the plain word, never the id", () => {
  const roster = new Map([[ID, "Sales manager (demo)"]]);
  assert.equal(personName(ID, roster, "a manager"), "Sales manager (demo)");
  assert.equal(personName("another-id", roster, "a manager"), "a manager");
  assert.equal(personName(undefined, roster, "a manager"), "a manager");
});

test("a sentence keeps the server's words and loses only its ids", () => {
  assert.equal(withoutIds(`quote Q-1 line 1 (${ID}): 10 percent discount`), "quote Q-1 line 1: 10 percent discount");
  assert.equal(withoutIds(`cleared by ${ID}`), "cleared by a record");
  assert.equal(withoutIds("no ids here"), "no ids here");
});

test("the approval detail carries no id", () => {
  const said = approvalDetail(`quote Q-1 line 1 (${ID}): 10 percent discount, resulting margin 1000 bps against the 1200 bps floor`);
  assert.doesNotMatch(said, /[0-9a-f]{8}-[0-9a-f]{4}/);
  assert.match(said, /margin 10% against the 12% floor/);
});

test("the history says who and what in words, never an id", () => {
  assert.equal(changeActor({ actor_id: "abcdef1234567890" }), "a staff member");
  assert.equal(changeActor({}), "the system");
  const names = (id) => (id === ID ? "Sales manager (demo)" : undefined);
  assert.equal(formatChangeValue("floor_cleared_by", ID, names), "Sales manager (demo)");
  assert.equal(formatChangeValue("floor_cleared_by", "x", () => undefined), "a manager");
  assert.equal(formatChangeValue("line_added", ID, () => undefined), "a product");
  assert.equal(formatChangeValue("line_added", ID, (id) => (id === ID ? "Holland Stone" : undefined)), "Holland Stone");
  assert.equal(
    historyHeadline({ field: "line_added", line_no: 2, after: ID, occurred_at: "x" }, () => undefined),
    "Line added, line 2: none → a product",
  );
});
