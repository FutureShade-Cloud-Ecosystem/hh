// GAPS-r2 row G1, the Dealer Admin: run on its own, the app renders the
// framed-mode header (the board's white band) ABOVE its app-local header,
// the centred selector carrying "Dealer Admin > Staff" and the account chip
// the persona's name with the role label. Framed by the Work shell the app
// wears no band of its own: the host's band is the one band.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

const appRootSrc = readFileSync(fileURLToPath(new URL("../src/app-root.ts", import.meta.url)), "utf8");

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

test("the band renders above the app-local header, and only when the shell is not framing the app", () => {
  const bandAt = appRootSrc.indexOf('<hh-shell-header data-testid="framed-band"');
  assert.ok(bandAt > 0, "the band element renders");
  const headerAt = appRootSrc.indexOf("<hos-app-header");
  assert.ok(headerAt > 0 && bandAt < headerAt, "the band sits above the app-local header");
  const bandRender = appRootSrc.slice(Math.max(0, bandAt - 400), bandAt);
  assert.ok(/#framed\s*\?/.test(bandRender), "framed by the Work shell, the app renders no band of its own");
});
