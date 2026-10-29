// GAPS-r2 row G1, the Money Desk: run on its own, the desk wears the ONE
// band (the board's white band, PR 503 round 1): the centred selector
// carries "Money Desk > Receivables" and the account chip the persona's name
// with the role label. The rendered band (no app-local header, the account
// menu's actions) is the Playwright leg's ground: test/framed-band.pw.ts.

import { test } from "node:test";
import assert from "node:assert/strict";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

test("the framed-mode header carries the App > Surface text: Money Desk > Receivables", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("receivables")), "Money Desk > Receivables");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("ledger")), "Money Desk > Approvals and ledger");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("statements")), "Money Desk > Statements");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("bank")), "Money Desk > Bank reconciliation");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("tax")), "Money Desk > Tax");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("liens")), "Money Desk > Liens");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("compliance")), "Money Desk > Compliance");
});

test("the account chip matches the persona's name and tier label", () => {
  assert.equal(framedBandAccount("Dana", "Sales"), "Dana, Sales");
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("", "Sales"), "Sales", "no persona yet: the role label alone");
});

// Item 370: a dealer's own box (console mode) names its dealer and wears no DEMO chip; only a demo page
// (no console config) is DEMO. The name is the box's own web config first, the public tenant config second.
import { bandIdentity } from "../src/band-identity.js";

const consoleConfig = (name) => ({ mode: "console", org: "acme", consoleEndpoint: "https://console.invalid/v1", consoleProjectId: "p", coreBaseUrl: "", personas: false, personaCaller: "partner", personasOpen: false, dealer: { name } });

test("a console box names the dealer from its web config and is not DEMO", () => {
  assert.deepEqual(bandIdentity(consoleConfig("Acme Stone"), ""), { org: "Acme Stone", demo: false });
  assert.deepEqual(bandIdentity(consoleConfig("Acme Stone"), "Other Name"), { org: "Acme Stone", demo: false });
});

test("a console box with a blank web config name falls back to the tenant config's name", () => {
  assert.deepEqual(bandIdentity(consoleConfig("  "), "Acme Stone"), { org: "Acme Stone", demo: false });
});

test("a demo page (no console config) is DEMO and names the tenant config's dealer", () => {
  assert.deepEqual(bandIdentity(null, "Acme Stone"), { org: "Acme Stone", demo: true });
  assert.deepEqual(bandIdentity(null, ""), { org: "", demo: true });
});
