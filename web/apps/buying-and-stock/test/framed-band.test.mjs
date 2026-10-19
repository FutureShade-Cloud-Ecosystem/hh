// GAPS-r2 row G1, Buying and Stock: run on its own, the app wears the ONE
// band (the board's white band, PR 503 round 1): the centred selector carries
// "Buying and Stock > Purchase orders" and the account chip the persona's name
// with the role label. The rendered band (no app-local header, the account
// menu's actions) is the Playwright leg's ground: test/framed-band.pw.ts.

import { test } from "node:test";
import assert from "node:assert/strict";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

test("the framed-mode header carries the App > Surface text", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("orders")), "Buying and Stock > Purchase orders");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("incoming")), "Buying and Stock > Incoming");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("receiving")), "Buying and Stock > Receiving");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("vendors")), "Buying and Stock > Vendors");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("bills")), "Buying and Stock > Vendor bills");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("landed")), "Buying and Stock > Landed cost");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("inventory")), "Buying and Stock > Inventory");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("counts")), "Buying and Stock > Cycle counts");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("lookup")), "Buying and Stock > Stock lookup");
});

test("the account chip matches the persona's name and tier label", () => {
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("Dana", "Sales"), "Dana, Sales");
  assert.equal(framedBandAccount("", "Sales"), "Sales", "no persona yet: the role label alone");
});
