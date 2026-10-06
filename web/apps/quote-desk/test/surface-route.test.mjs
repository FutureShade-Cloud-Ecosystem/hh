// The surface table's pure half: the hash parse, the hash write, and the role walls (rows 955 to 957).
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  canSee,
  deskTabOf,
  deskTabsFor,
  parseSurfaceRoute,
  quoteTabsFor,
  roleOf,
  routeHash,
} from "../src/surface-route.ts";

test("every route round trips through its hash", () => {
  const routes = [
    { kind: "quotes" },
    { kind: "compose" },
    { kind: "quote", id: "q-1", tab: "build" },
    { kind: "quote", id: "q-1", tab: "activity" },
    { kind: "quote", id: "q-1", tab: "stock" },
    { kind: "quote", id: "q-1", tab: "admin" },
    { kind: "approvals" },
    { kind: "approval", id: "fr-q-1001:1" },
    { kind: "pricing" },
    { kind: "settings" },
  ];
  for (const route of routes) assert.deepEqual(parseSurfaceRoute(routeHash(route)), route);
});

test("a hash the table does not name is the Quotes list", () => {
  for (const hash of ["", "#", "#/", "#/nope", "#/quotes/q-1/billing", "#/quotes/q-1/activity/x", "#/approvals/a/b", "#/pricing/x", "#/quotes/q-1/build"]) {
    assert.deepEqual(parseSurfaceRoute(hash), { kind: "quotes" }, hash);
  }
});

test("only the Sales manager role code is a manager", () => {
  assert.equal(roleOf("sales_manager"), "manager");
  assert.equal(roleOf("inside_sales"), "sales");
  assert.equal(roleOf(""), "sales");
  assert.equal(roleOf(null), "sales");
});

test("Sales sees Quotes and the Build, Activity and Stock tabs; the manager sees everything", () => {
  assert.deepEqual(deskTabsFor("sales"), ["quotes"]);
  assert.deepEqual(deskTabsFor("manager"), ["quotes", "approvals", "pricing", "settings"]);
  assert.deepEqual(quoteTabsFor("sales"), ["build", "activity", "stock"]);
  assert.deepEqual(quoteTabsFor("manager"), ["build", "activity", "stock", "admin"]);
});

test("each route's wall holds per role", () => {
  const walled = ["#/approvals", "#/approvals/x", "#/pricing", "#/settings", "#/quotes/q/admin"];
  const open = ["#/quotes", "#/quotes/new", "#/quotes/q", "#/quotes/q/activity", "#/quotes/q/stock"];
  for (const hash of walled) {
    assert.equal(canSee("sales", parseSurfaceRoute(hash)), false, hash);
    assert.equal(canSee("manager", parseSurfaceRoute(hash)), true, hash);
  }
  for (const hash of open) {
    assert.equal(canSee("sales", parseSurfaceRoute(hash)), true, hash);
    assert.equal(canSee("manager", parseSurfaceRoute(hash)), true, hash);
  }
});

test("a request detail sits under Approvals, a quote page under Quotes", () => {
  assert.equal(deskTabOf(parseSurfaceRoute("#/approvals/x")), "approvals");
  assert.equal(deskTabOf(parseSurfaceRoute("#/quotes/q/stock")), "quotes");
  assert.equal(deskTabOf(parseSurfaceRoute("#/quotes/new")), "quotes");
});
