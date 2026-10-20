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
