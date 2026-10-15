// What counts as an answer that was lost on the way back: no answer at all, or a server fault.
import { test } from "node:test";
import assert from "node:assert/strict";
import { answerLost } from "../src/purch/purch-logic.ts";

test("no answer, and a server fault, are a lost answer; a refusal and a success are not", () => {
  assert.equal(answerLost({ ok: false, error: "network", status: undefined }), true);
  assert.equal(answerLost({ ok: false, error: "bad gateway", status: 502 }), true);
  assert.equal(answerLost({ ok: false, error: "internal error", status: 500 }), true);
  assert.equal(answerLost({ ok: false, error: "conflict", status: 409 }), false);
  assert.equal(answerLost({ ok: false, error: "Forbidden", status: 403 }), false);
  assert.equal(answerLost({ ok: true, value: {} }), false);
});
