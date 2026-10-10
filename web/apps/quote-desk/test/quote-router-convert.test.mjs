// The convert surface's place in the desk's router: a third route, written and read back the same.
import { test } from "node:test";
import assert from "node:assert/strict";

import { convertRoute, hashNames, parseRoute, routeHash, routeIsQuotePage } from "../src/quote-router.ts";

test("the convert route parses, writes and reads back", () => {
  assert.deepEqual(parseRoute("#/quotes/q-1003/convert"), { kind: "convert", id: "q-1003" });
  assert.equal(routeHash(convertRoute("q-1003")), "#/quotes/q-1003/convert");
  assert.deepEqual(parseRoute(routeHash(convertRoute("Q-7"))), convertRoute("Q-7"));
  assert.equal(hashNames("#/quotes/q-1/convert", convertRoute("q-1")), true);
  assert.equal(hashNames("#/quotes/q-2/convert", convertRoute("q-1")), false);
  assert.equal(hashNames("#/quotes/q-1", convertRoute("q-1")), false);
});

test("anything else with three parts is still the list, and the convert page is not a quote page", () => {
  assert.deepEqual(parseRoute("#/quotes/new/convert"), { kind: "list" });
  assert.deepEqual(parseRoute("#/quotes/q-1/other"), { kind: "list" });
  assert.deepEqual(parseRoute("#/quotes/q-1/convert/x"), { kind: "list" });
  assert.equal(routeIsQuotePage(convertRoute("q-1")), false);
  assert.equal(parseRoute("#/quotes/q-1").kind, "quote");
  assert.equal(parseRoute("#/quotes/new").kind, "compose");
});
