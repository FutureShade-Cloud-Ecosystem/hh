// GAPS-r2 row G1, the Counter Till: run on its own, the till wears the ONE
// band (the board's white band, PR 503 round 1): the centred selector
// carries "Counter Till > New sale" and the account chip the persona's name
// with the role label. The rendered band (no app-local header, the account
// menu's actions) is the Playwright leg's ground: test/framed-band.pw.ts.

import { test } from "node:test";
import assert from "node:assert/strict";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

test("the framed-mode header carries the App > Surface text: Counter Till > New sale", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("sale")), "Counter Till > New sale");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("returns")), "Counter Till > Returns");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("pay-balance")), "Counter Till > Pay balance");
});

test("the account chip matches the persona's name and tier label", () => {
  assert.equal(framedBandAccount("Dana", "Sales"), "Dana, Sales");
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("", "Sales"), "Sales", "no persona yet: the role label alone");
});

// Item 371: a dealer's own box (console mode) names its dealer from its web config and wears no DEMO chip;
// only a page served no console config is DEMO (the same rule as the Money Desk, PR 690).
import { bandIdentity } from "../src/band-identity.js";

const consoleConfig = (name) => ({ mode: "console", org: "acme", consoleEndpoint: "https://console.invalid/v1", consoleProjectId: "p", coreBaseUrl: "", personas: false, personaCaller: "partner", personasOpen: false, dealer: { name } });

test("a console box names the dealer from its web config and is not DEMO", () => {
  assert.deepEqual(bandIdentity(consoleConfig("Acme Stone"), ""), { org: "Acme Stone", demo: false });
  assert.deepEqual(bandIdentity(consoleConfig("Acme Stone"), "Other Name"), { org: "Acme Stone", demo: false });
});

test("a demo page (no console config) is DEMO and names the tenant config's dealer", () => {
  assert.deepEqual(bandIdentity(null, "Acme Stone"), { org: "Acme Stone", demo: true });
});
