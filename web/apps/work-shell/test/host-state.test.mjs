// The host's pure state: the door (sign in and the role picker, a page under
// the host), then the desk (the chrome around one hosted app). Sign in lands
// the role on its own app; Switch role drops the session and returns to the
// door; nothing here touches pins or any per user store (subject binding:
// those belong to the person and wait for them).
import { test } from "node:test";
import assert from "node:assert/strict";
import { initialState, openApp, signedIn, switchRole } from "../src/host-state.js";
import { launchableApps, switcherFor } from "../src/switcher.js";
import { readLaunchMap, readWorkProfile } from "./support-data.mjs";

const apps = launchableApps(readWorkProfile().apps, readLaunchMap());

function session(roleCode, extra = {}) {
  return {
    source: "persona",
    userId: `demo-account-${roleCode}`,
    email: `${roleCode}@demo.invalid`,
    name: `${roleCode} (demo)`,
    roleCode,
    roleLabel: roleCode,
    roleName: roleCode,
    token: "t",
    tokenExpiresAtMs: 2_000_000_000_000,
    flow: roleCode,
    tier: 2,
    scope: "BRANCH",
    ...extra,
  };
}

test("the host starts at the door", () => {
  assert.deepEqual(initialState(), { phase: "door" });
});

test("signing in lands each role on its own app, with the switcher the profile gives it", () => {
  const sales = signedIn(initialState(), session("inside_sales"), apps);
  assert.equal(sales.phase, "desk");
  assert.equal(sales.currentAppId, "counter-till");
  assert.deepEqual(sales.apps, switcherFor(apps, "inside_sales"));

  const manager = signedIn(initialState(), session("sales_manager"), apps);
  assert.equal(manager.currentAppId, "manager-home");

  const yard = signedIn(initialState(), session("yard_manager"), apps);
  assert.equal(yard.currentAppId, "manager-home");
});

test("a role with no Work app signs in to an empty desk and lands nowhere", () => {
  const driver = signedIn(initialState(), session("driver"), apps);
  assert.equal(driver.phase, "desk");
  assert.equal(driver.currentAppId, null);
  assert.deepEqual(driver.apps, []);
});

test("opening an app the role holds changes the current app; one it does not hold changes nothing", () => {
  const sales = signedIn(initialState(), session("inside_sales"), apps);
  const quote = openApp(sales, "quote-desk");
  assert.equal(quote.currentAppId, "quote-desk");
  assert.equal(quote.session, sales.session);
  assert.equal(openApp(sales, "manager-home"), sales);
  assert.equal(openApp(sales, "no-such-app"), sales);
  assert.equal(openApp(initialState(), "counter-till").phase, "door");
});

test("Switch role drops the session and returns to the door, from any app", () => {
  const sales = openApp(signedIn(initialState(), session("inside_sales"), apps), "quote-desk");
  const after = switchRole(sales);
  assert.deepEqual(after, { phase: "door" });
  assert.deepEqual(switchRole(after), { phase: "door" });
  assert.ok(!("session" in after));
});

test("a second person signing in after a Switch role starts clean: no app, no switcher of the first", () => {
  const first = openApp(signedIn(initialState(), session("sales_manager"), apps), "dealer-admin");
  const second = signedIn(switchRole(first), session("yard_team"), apps);
  assert.equal(second.session.userId, "demo-account-yard_team");
  assert.deepEqual(
    second.apps.map((a) => a.id),
    ["dispatch-board", "buying-and-stock"],
  );
  assert.equal(second.currentAppId, "buying-and-stock");
});
