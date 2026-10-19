// GAPS-r2 row G1, the Money Desk: run on its own, the desk renders the
// framed-mode header (the board's white band) ABOVE its app-local header,
// the centred selector carrying "Money Desk > Receivables" and the account
// chip the persona's name with the role label. Framed by the Work shell the
// desk wears no band of its own: the host's band is the one band.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

const appRootSrc = readFileSync(fileURLToPath(new URL("../src/app-root.ts", import.meta.url)), "utf8");

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

test("the band renders above the app-local header, and only when the shell is not framing the desk", () => {
  const renderAt = appRootSrc.indexOf("render(): TemplateResult {");
  assert.ok(renderAt > 0, "the render method exists");
  const renderBody = appRootSrc.slice(renderAt, appRootSrc.indexOf("\n  }\n", renderAt));
  const bandAt = renderBody.indexOf("<hh-shell-header");
  assert.ok(bandAt > 0, "the band element renders");
  const headerAt = renderBody.indexOf("<hos-app-header");
  assert.ok(headerAt > 0 && bandAt < headerAt, "the band sits above the app-local header");
  const bandRender = renderBody.slice(Math.max(0, bandAt - 400), bandAt);
  assert.ok(/#framed\s*\?/.test(bandRender), "framed by the Work shell, the desk renders no band of its own");
});
