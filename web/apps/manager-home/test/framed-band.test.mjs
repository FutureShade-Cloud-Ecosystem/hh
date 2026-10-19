// GAPS-r2 row G1, Manager Home: run on its own, the app wears the ONE band
// (the board's white band, PR 503 round 1): the centred selector carries
// "Manager Home > Home" and the account chip the persona's name with the role
// label. The rendered band (no app-local header, the account menu's actions)
// is the Playwright leg's ground: test/framed-band.pw.ts.

import { test } from "node:test";
import assert from "node:assert/strict";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

test("the framed-mode header carries the App > Surface text for each view", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("home")), "Manager Home > Home");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("shortages")), "Manager Home > Shortages · open");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("receiving")), "Manager Home > Receiving · short");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("approvals")), "Manager Home > Approvals");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("drivers")), "Manager Home > Drivers");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("builder")), "Manager Home > Arrange home");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("reporting")), "Manager Home > Reporting");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("warranty")), "Manager Home > Warranty");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("quick-actions")), "Manager Home > Quick actions");
});

test("the account chip matches the persona's name and role label", () => {
  assert.equal(framedBandAccount("Dana", "Sales"), "Dana, Sales");
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("", "Sales"), "Sales", "no persona yet: the role label alone");
});
