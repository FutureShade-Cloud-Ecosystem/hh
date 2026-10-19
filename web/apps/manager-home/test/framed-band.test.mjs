// GAPS-r2 row G1, Manager Home: run on its own, the app renders the
// framed-mode header (the board's white band) ABOVE its app-local header,
// the centred selector carrying "Manager Home > Home" and the account chip
// the persona's name with the role label. Framed by the Work shell the app
// wears no band of its own: the host's band is the one band.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

const appRootSrc = readFileSync(fileURLToPath(new URL("../src/app-root.ts", import.meta.url)), "utf8");

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

test("the band renders above the app-local header, and only when the shell is not framing the app", () => {
  assert.ok(appRootSrc.includes("<hh-shell-header"), "the band element renders");
  assert.ok(/#framed\s*\?\s*nothing\s*:\s*html`<hh-shell-header/.test(appRootSrc), "framed by the Work shell, the app renders no band of its own");
  const renderAt = appRootSrc.indexOf("render(): TemplateResult {");
  assert.ok(renderAt > 0, "the render method exists");
  const renderBody = appRootSrc.slice(renderAt);
  const signedInReturn = renderBody.search(/return html\`\$\{band\}\$\{this\.renderSignedIn\(\)\}/);
  const signedOutReturn = renderBody.indexOf("${band}");
  const headerAfterBand = renderBody.indexOf("<hos-app-header");
  assert.ok(signedInReturn > 0 || signedOutReturn > 0, "the band variable is rendered");
  assert.ok(headerAfterBand > 0, "the app-local header renders");
  const firstBandRender = signedInReturn > 0 ? signedInReturn : signedOutReturn;
  assert.ok(firstBandRender < headerAfterBand, "the band sits above the app-local header in the rendered output");
});
