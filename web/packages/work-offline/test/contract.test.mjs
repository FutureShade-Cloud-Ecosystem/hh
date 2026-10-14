// The quote draft's payload fields against the core's own writable sets, read
// from the Go source, so the outbox cannot drift from the server again (the
// lesson of the Field offline review: a stub may only answer what the Go sets
// admit, and a payload may only carry what they admit).
import { test } from "node:test";
import assert from "node:assert/strict";
import { goFieldSet } from "../src/testing.ts";
import { QUOTE_CREATE_FIELDS, QUOTE_UPDATE_FIELDS } from "../src/quote-drafts.ts";

test("the draft create carries exactly the fields the core admits on a quotes create", () => {
  assert.deepEqual([...QUOTE_CREATE_FIELDS].sort(), [...goFieldSet("quote/syncscope.go", "quoteCreateFields")].sort());
});

test("the draft update carries exactly the fields the core admits on a quotes update", () => {
  assert.deepEqual([...QUOTE_UPDATE_FIELDS].sort(), [...goFieldSet("quote/syncscope.go", "quoteWriteFields")].sort());
});

test("the reader of the Go sets fails loudly on a name it cannot find", () => {
  assert.throws(() => goFieldSet("quote/syncscope.go", "noSuchFunction"), /not found/);
});

test("no money field is writable through the quotes sync", () => {
  for (const set of [QUOTE_CREATE_FIELDS, QUOTE_UPDATE_FIELDS]) {
    for (const field of set) assert.doesNotMatch(field, /cents|total|tax|price|discount|margin|cost/);
  }
});
