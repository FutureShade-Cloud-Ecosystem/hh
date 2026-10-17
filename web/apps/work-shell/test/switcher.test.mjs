// The app switcher by role: the Work profile's apps (from the generated
// profile data the shell ships as profile.json), narrowed to the apps that
// have a bundle in the profile (the launch map), then to the apps the signed
// in role holds. Server gates stay the boundary: the switcher only decides
// what to offer.
import { test } from "node:test";
import assert from "node:assert/strict";
import { launchableApps, switcherFor, parseProfile } from "../src/switcher.js";
import { readLaunchMap, readWorkProfile } from "./support-data.mjs";

const profile = readWorkProfile();
const launch = readLaunchMap();
const apps = launchableApps(profile.apps, launch);
const ids = (list) => list.map((a) => a.id);

test("only apps with a bundle in the profile are launchable, in manifest order, each with its serve path", () => {
  assert.deepEqual(ids(apps), [
    "counter-till",
    "quote-desk",
    "dispatch-board",
    "buying-and-stock",
    "money-desk",
    "manager-home",
    "house-messages",
    "dealer-admin",
  ]);
  assert.equal(apps.find((a) => a.id === "counter-till")?.path, "/till/");
  assert.equal(apps.find((a) => a.id === "manager-home")?.path, "/home/");
  // House Messages is in the profile and has its bundle: it is offered, in manifest order.
  assert.ok(profile.apps.some((a) => a.id === "house-messages"));
  assert.ok(ids(apps).includes("house-messages"));
});

test("different roles see different switchers", () => {
  assert.deepEqual(ids(switcherFor(apps, "inside_sales")), ["counter-till", "quote-desk", "buying-and-stock", "money-desk", "house-messages"]);
  assert.deepEqual(ids(switcherFor(apps, "logistics_manager")), ["dispatch-board", "manager-home", "house-messages"]);
  assert.deepEqual(ids(switcherFor(apps, "yard_team")), ["dispatch-board", "buying-and-stock", "house-messages"]);
  assert.deepEqual(ids(switcherFor(apps, "sales_manager")), [
    "counter-till",
    "quote-desk",
    "dispatch-board",
    "buying-and-stock",
    "money-desk",
    "manager-home",
    "house-messages",
    "dealer-admin",
  ]);
});

test("an app a role does not hold is not in its switcher", () => {
  assert.ok(!ids(switcherFor(apps, "inside_sales")).includes("manager-home"));
  assert.ok(!ids(switcherFor(apps, "inside_sales")).includes("dealer-admin"));
  assert.ok(!ids(switcherFor(apps, "logistics_manager")).includes("counter-till"));
  for (const role of ["inside_sales", "yard_team", "hr", "financial_controller"]) {
    for (const app of switcherFor(apps, role)) {
      assert.ok(app.roles.includes(role), `${role} was offered ${app.id}, which the profile does not give it`);
    }
  }
});

test("a role holding one Work app gets just that one, and an unknown role gets none", () => {
  assert.deepEqual(ids(switcherFor(apps, "driver")), ["house-messages"]);
  assert.deepEqual(switcherFor(apps, "no_such_role"), []);
  assert.deepEqual(switcherFor(apps, ""), []);
});

test("parseProfile reads the two shipped files field by field and refuses anything else", () => {
  const read = parseProfile(profile, launch);
  assert.ok(read !== null);
  assert.deepEqual(ids(read.apps), ids(apps));
  assert.equal(parseProfile(null, launch), null);
  assert.equal(parseProfile(profile, null), null);
  assert.equal(parseProfile({ apps: "no" }, launch), null);
  assert.equal(parseProfile({ apps: [{ id: "x" }] }, launch), null);
  // a launch path that does not look like a served directory is refused whole
  assert.equal(parseProfile(profile, { ...launch, "money-desk": "https://elsewhere.example/" }), null);
  assert.equal(parseProfile(profile, { ...launch, "money-desk": "//elsewhere.example/" }), null);
  assert.equal(parseProfile(profile, { ...launch, "money-desk": "/a/../b/" }), null);
});
