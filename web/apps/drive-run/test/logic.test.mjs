// The ported framework free logic, on its own: the lifecycle the driver sees,
// the run sheet's words and the small formatters. No browser, no network.
// Each case is a fact the original app held and HH keeps (provenance in
// src/drive-logic.ts), plus the places HH differs on purpose.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  STOP_LABEL,
  addressLine,
  driverActions,
  formatTime,
  plainSentence,
  qtyLabel,
  roadsideError,
  runSummary,
  stopNextStates,
  stopsInOrder,
  stopWord,
  weightLabel,
} from "../src/drive-logic.ts";

test("the stop lifecycle matches the core's matrix, forward only", () => {
  assert.deepEqual(stopNextStates("READY"), ["LOADED", "CANCELLED"]);
  assert.deepEqual(stopNextStates("LOADED"), ["EN_ROUTE", "CANCELLED"]);
  assert.deepEqual(stopNextStates("EN_ROUTE"), ["DELIVERED", "FAILED"]);
  for (const done of ["DELIVERED", "FAILED", "CANCELLED"]) assert.deepEqual(stopNextStates(done), []);
  assert.deepEqual(stopNextStates("SOMETHING_NEW"), []);
});

test("the next states come back as a copy, never the table", () => {
  const next = stopNextStates("READY");
  next.push("DELIVERED");
  assert.deepEqual(stopNextStates("READY"), ["LOADED", "CANCELLED"]);
});

test("a driver is offered only the moves a driver makes: loaded, en route, proof, could not deliver", () => {
  assert.deepEqual(driverActions("READY").map((a) => a.action), ["LOADED"]);
  assert.deepEqual(driverActions("LOADED").map((a) => a.action), ["EN_ROUTE"]);
  assert.deepEqual(driverActions("EN_ROUTE").map((a) => a.action), ["POD", "FAILED"]);
  assert.deepEqual(driverActions("DELIVERED"), []);
  assert.deepEqual(driverActions("FAILED"), []);
  assert.deepEqual(driverActions("CANCELLED"), []);
});

test("could not deliver is the one destructive move and the labels are the drivers' words", () => {
  const en = driverActions("EN_ROUTE");
  assert.equal(en[0].label, "Proof of delivery");
  assert.equal(en[1].label, "Could not deliver");
  assert.equal(en[1].destructive, true);
  assert.equal(driverActions("READY")[0].label, "Mark loaded");
  assert.equal(driverActions("LOADED")[0].label, "Mark en route");
});

test("the run's progress sentence counts what is left and never reads an empty run as done", () => {
  assert.deepEqual(runSummary([]).done, false);
  assert.equal(runSummary([]).sentence, "No stops on this run.");
  const open = runSummary([{ status: "READY" }, { status: "DELIVERED" }, { status: "EN_ROUTE" }]);
  assert.equal(open.sentence, "2 stops left · 1 delivered");
  assert.equal(open.done, false);
  assert.equal(runSummary([{ status: "READY" }]).sentence, "1 stop left · 0 delivered");
  assert.equal(runSummary([{ status: "DELIVERED" }, { status: "DELIVERED" }]).sentence, "Run finished · all 2 delivered");
  const mixed = runSummary([{ status: "DELIVERED" }, { status: "FAILED" }]);
  assert.equal(mixed.sentence, "Run finished · 1 delivered · 1 could not be delivered");
  assert.equal(mixed.done, true);
});

test("stops keep the server's order: by stop number, then by creation", () => {
  const sorted = stopsInOrder([
    { id: "c", stop_no: 2, created_at: "2" },
    { id: "a", stop_no: 1, created_at: "9" },
    { id: "b", stop_no: 2, created_at: "1" },
  ]);
  assert.deepEqual(sorted.map((s) => s.id), ["a", "b", "c"]);
  assert.deepEqual(stopsInOrder(null), []);
});

test("an address line drops blanks and an unknown value is a placeholder, not a guess", () => {
  assert.equal(addressLine({ address_line: "95 Bagot St", city: "Kingston", postal_code: "K7L 3E7" }), "95 Bagot St, Kingston, K7L 3E7");
  assert.equal(addressLine({ address_line: " ", city: "Kingston" }), "Kingston");
  assert.equal(addressLine({}), "");
});

test("quantities keep the unit and lose trailing zeros; weights say unknown rather than zero", () => {
  assert.equal(qtyLabel("12.500", "bag"), "12.5 bag");
  assert.equal(qtyLabel("4", "pallet"), "4 pallet");
  assert.equal(qtyLabel("", "bag"), "");
  assert.equal(weightLabel(null), "weight not recorded");
  assert.equal(weightLabel(0), "weight not recorded");
  assert.equal(weightLabel(1250.5), "1,250.5 kg");
});

test("a server sentence loses its module prefix and a raw status word reads plainly", () => {
  assert.equal(plainSentence("dispatch: the run has not been dispatched"), "the run has not been dispatched");
  assert.equal(plainSentence("not found"), "not found");
  assert.equal(plainSentence(null), "");
  assert.equal(stopWord("EN_ROUTE"), "en route");
  assert.equal(STOP_LABEL.EN_ROUTE, "En route");
});

test("a time reads in the dealer's zone, 24 hour, and an unreadable one is blank", () => {
  assert.equal(formatTime("2000-01-01T15:30:00Z", "America/Toronto"), "10:30");
  assert.equal(formatTime("not a time", "America/Toronto"), "");
  assert.equal(formatTime(null, "America/Toronto"), "");
});

test("a refusal from the core reads as a sentence: prefix off, capital on, full stop on", () => {
  assert.equal(roadsideError("dispatch: the run has not been dispatched", 409, "That move did not go through."), "The run has not been dispatched.");
  assert.equal(roadsideError("Already a sentence.", 400, "x"), "Already a sentence.");
});

test("a failure with no answer from the server says what to do at the roadside, never an engineering string", () => {
  const generic = "Your run did not load. Check your signal and try again.";
  assert.equal(roadsideError("dispatchListMyRuns: transport error: fetch failed", undefined, generic), generic);
  assert.equal(roadsideError("internal error", 500, generic), generic);
  assert.equal(roadsideError("HTTP 502", 502, generic), generic);
});

test("a login that may not do this, or has no driver record, is told in the driver's words", () => {
  assert.equal(roadsideError("forbidden", 403, "x"), "You are not allowed to do that on this run.");
  assert.equal(roadsideError("unauthenticated", 401, "x"), "Your sign in has ended. Sign out and sign in again.");
  assert.equal(roadsideError("not found", 404, "x", { notFound: "You are not set up as a driver. Ask your branch manager to link your login." }), "You are not set up as a driver. Ask your branch manager to link your login.");
  assert.equal(roadsideError("not found", 404, "x"), "That is no longer on your run.");
});

// ---- sub item 3: proof of delivery ----
import {
  ATTESTATION,
  PHOTO_LIMIT_BYTES,
  canSign,
  needsShrink,
  normalizeName,
  photoTypeFor,
  signBlockedReason,
} from "../src/drive-logic.ts";

test("a delivery is signed only with a typed name AND the recipient's own confirmation", () => {
  assert.equal(canSign({ receivedBy: "Site contact", affirmed: true, busy: false }), true);
  assert.equal(canSign({ receivedBy: "   ", affirmed: true, busy: false }), false);
  assert.equal(canSign({ receivedBy: "Site contact", affirmed: false, busy: false }), false);
  assert.equal(canSign({ receivedBy: "Site contact", affirmed: true, busy: true }), false);
});

test("the sign control says why it is not ready, in order: name, then confirmation", () => {
  assert.equal(signBlockedReason({ receivedBy: "", affirmed: false, busy: false }), "Type the name of the person receiving.");
  assert.equal(signBlockedReason({ receivedBy: "A B", affirmed: false, busy: false }), "Tick the confirmation to sign.");
  assert.equal(signBlockedReason({ receivedBy: "A B", affirmed: true, busy: true }), "Signing.");
  assert.equal(signBlockedReason({ receivedBy: "A B", affirmed: true, busy: false }), "");
});

test("the name is trimmed before it is sent and never invented", () => {
  assert.equal(normalizeName("  Site contact  "), "Site contact");
  assert.equal(normalizeName(""), "");
});

test("the attestation says what it is: an electronic attestation, not a captured signature image", () => {
  assert.match(ATTESTATION, /electronic attestation/);
  assert.match(ATTESTATION, /not a captured signature image/);
});

test("only the three picture types the core takes are sent as proof photos", () => {
  assert.equal(photoTypeFor("image/jpeg"), "image/jpeg");
  assert.equal(photoTypeFor("IMAGE/PNG"), "image/png");
  assert.equal(photoTypeFor("image/webp"), "image/webp");
  assert.equal(photoTypeFor("image/heic"), null);
  assert.equal(photoTypeFor("application/pdf"), null);
  assert.equal(photoTypeFor(""), null);
});

test("a camera photo over a safe share of the core's limit is shrunk before it is sent", () => {
  assert.equal(PHOTO_LIMIT_BYTES, 4 * 1024 * 1024);
  assert.equal(needsShrink(200_000), false);
  assert.equal(needsShrink(2 * 1024 * 1024), true);
});
