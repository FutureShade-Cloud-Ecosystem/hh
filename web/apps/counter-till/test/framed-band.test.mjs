// GAPS-r2 row G1, the Counter Till: run on its own, the till renders the
// framed-mode header (the board's white band) ABOVE its app-local header,
// the centred selector carrying "Counter Till > New sale" and the account
// chip the persona's name with the role label. Framed by the Work shell the
// till wears no band of its own: the host's band is the one band.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

const appRootSrc = readFileSync(fileURLToPath(new URL("../src/app-root.ts", import.meta.url)), "utf8");

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

test("the band renders above the app-local header, and only when the shell is not framing the till", () => {
  const bandAt = appRootSrc.indexOf("hh-shell-header");
  assert.ok(bandAt > 0, "the band element renders");
  const headerAt = appRootSrc.indexOf("<header>");
  assert.ok(headerAt > 0 && bandAt < headerAt, "the band sits above the app-local header");
  const bandRender = appRootSrc.slice(Math.max(0, bandAt - 400), bandAt);
  assert.ok(/#framed\s*\?/.test(bandRender), "framed by the Work shell, the till renders no band of its own");
});
