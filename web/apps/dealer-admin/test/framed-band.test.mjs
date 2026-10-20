// GAPS-r2 row G1, the Dealer Admin: run on its own, the app wears the ONE
// band (the board's white band, PR 503 round 1): the centred selector
// carries "Dealer Admin > Staff" and the account chip the persona's name
// with the role label. The rendered band (no app-local header, the account
// menu's actions) is the Playwright leg's ground: test/framed-band.pw.ts.

import { test } from "node:test";
import assert from "node:assert/strict";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

test("the framed-mode header carries the App > Surface text: Dealer Admin > Staff", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("staff")), "Dealer Admin > Staff");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("pricing")), "Dealer Admin > Pricing");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("deployment")), "Dealer Admin > Deployment");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("roles")), "Dealer Admin > Roles");
});

test("the account chip matches the persona's name and tier label", () => {
  assert.equal(framedBandAccount("Dana", "Sales"), "Dana, Sales");
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("", "Sales"), "Sales", "no persona yet: the role label alone");
});
