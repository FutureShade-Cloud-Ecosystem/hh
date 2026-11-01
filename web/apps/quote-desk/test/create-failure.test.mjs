// The picker's create-failure words: "could not reach" only for a request nobody answered.
import { test } from "node:test";
import assert from "node:assert/strict";
import { createFailureWords } from "../src/_create-failure.ts";

test("a rejected fetch, an abort and a timeout are a lost signal", () => {
  assert.equal(createFailureWords(new TypeError("fetch failed")), "Could not reach the server, try again.");
  assert.equal(createFailureWords(new DOMException("aborted", "AbortError")), "Could not reach the server, try again.");
  assert.equal(createFailureWords(new DOMException("slow", "TimeoutError")), "Could not reach the server, try again.");
});

test("a body that would not parse, or a fault in the picker, is a failed create, not a lost signal", () => {
  assert.equal(createFailureWords(new SyntaxError("Unexpected token < in JSON")), "Could not create the account.");
  assert.equal(createFailureWords(new Error("host not wired")), "Could not create the account.");
  assert.equal(createFailureWords("odd"), "Could not create the account.");
});
