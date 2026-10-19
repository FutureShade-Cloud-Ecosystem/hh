// GAPS-r2 row G1, Yard Pick and Receive: run on its own, the yard renders the
// framed-mode header (the board's white band) ABOVE its app-local header,
// the centred selector carrying "Yard Pick and Receive > Pick queue" and the
// account chip the persona's name with the role label. Framed by the Work shell
// the yard wears no band of its own: the host's band is the one band.

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { FRAMED_BAND_APP, framedBandSurface } from "../src/framed-band.js";

const appRootSrc = readFileSync(fileURLToPath(new URL("../src/app-root.ts", import.meta.url)), "utf8");

test("the framed-mode header carries the App > Surface text: Yard Pick and Receive > Pick queue", () => {
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("queue")), "Yard Pick and Receive > Pick queue");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("pick")), "Yard Pick and Receive > Open pick");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("changes")), "Yard Pick and Receive > Changes");
  assert.equal(framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface("conflicts")), "Yard Pick and Receive > Review");
});

test("the account chip matches the persona's name and tier label", () => {
  assert.equal(framedBandAccount("Dana", "Sales"), "Dana, Sales");
  assert.equal(framedBandAccount("Dana", "General Manager"), "Dana, General Manager");
  assert.equal(framedBandAccount("", "Sales"), "Sales", "no persona yet: the role label alone");
});

test("the band renders above the app-local header, and only when the shell is not framing the yard", () => {
  const bandAt = appRootSrc.indexOf('<hh-shell-header data-testid="framed-band">');
  assert.ok(bandAt > 0, "the band element renders");
  const headerAt = appRootSrc.indexOf("<hos-app-header");
  assert.ok(headerAt > 0 && bandAt < headerAt, "the band sits above the app-local header");
  const bandRender = appRootSrc.slice(Math.max(0, bandAt - 400), bandAt);
  assert.ok(/#framed\s*\?/.test(bandRender), "framed by the Work shell, the yard renders no band of its own");
});
