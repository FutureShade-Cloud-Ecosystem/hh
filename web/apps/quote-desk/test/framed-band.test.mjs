// GAPS-r2 row G1, the Quote Desk: run on its own, the desk renders the
// framed-mode header (the board's white band) as the ONE band, the centred
// selector carrying "Quote Desk > Quotes" and the account chip the persona's
// name with the role label. Framed by the Work shell the desk wears no band
// of its own: the host's band is the one band. The rendered band is the
// Playwright leg's ground (test/framed-band.pw.ts).

import { test } from "node:test";
import assert from "node:assert/strict";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

test("the framed-mode header carries the App > Surface text for each mapped route", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("list")), "Quote Desk > Quotes");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("compose")), "Quote Desk > New quote");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("quote")), "Quote Desk > Quote");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("convert")), "Quote Desk > Convert to order");
});

test("the account chip matches the persona's name and role label", () => {
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("", "General Manager"), "General Manager", "no persona yet: the role label alone");
});
