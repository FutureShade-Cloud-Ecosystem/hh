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
  assert.equal(en[0].label, "Delivered");
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
  assert.equal(addressLine({ address_line: "418 Oak Hollow Road", city: "Julian", postal_code: "92036" }), "418 Oak Hollow Road, Julian, 92036");
  assert.equal(addressLine({ address_line: " ", city: "Julian" }), "Julian");
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

test("a time reads in the dealer's zone using glossary 12-hour AM/PM form, and an unreadable one is blank", () => {
  const janMorningUtc = new Date(Date.UTC(2000, 0, 1, 15, 30)).toISOString();
  assert.equal(formatTime(janMorningUtc, "America/Toronto"), "10:30 AM");
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
import { goTypedNameBytes } from "./_lib/gosets.mjs";
import {
  ATTESTATION,
  MAX_NAME_BYTES,
  PHOTO_LIMIT_BYTES,
  PROOF_QUEUED_NOTICE,
  canSign,
  nameProblem,
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

// ---- sub item 4: could not deliver, and closing the run ----
import {
  CLOSE_RULE,
  HANDBACK_BODY,
  HANDBACK_PROMPT,
  HANDBACK_TITLE,
  canRecordFailure,
  closeLine,
  runClose,
  stopHandle,
} from "../src/drive-logic.ts";

const stop = (over) => ({ stop_no: 1, customer_name: "Bluffside", status: "READY", ...over });

test("the handback words tell the driver the stop is closed and the load comes back to the yard", () => {
  assert.equal(HANDBACK_TITLE, "Recorded. This stop is closed.");
  assert.match(HANDBACK_BODY, /Bring the load back to the yard/);
  assert.match(HANDBACK_BODY, /a dispatcher will set a new date/);
  assert.equal(HANDBACK_PROMPT, "What stopped this delivery?");
});

test("a stop is marked not delivered only with a reason in the driver's own words", () => {
  assert.equal(canRecordFailure("Nobody on site"), true);
  assert.equal(canRecordFailure("   "), false);
  assert.equal(canRecordFailure(""), false);
});

test("a stop is named by its number and its customer", () => {
  assert.equal(stopHandle(stop()), "Stop 1 · Bluffside");
  assert.equal(stopHandle({ status: "READY" }), "A stop");
  assert.equal(closeLine(stop({ status: "DELIVERED" })), "Stop 1 · Bluffside · Delivered");
});

test("a run closes only from a dispatched run whose every stop is delivered or not delivered", () => {
  const done = runClose({ status: "DISPATCHED" }, [stop({ status: "DELIVERED" }), stop({ stop_no: 2, status: "FAILED" })]);
  assert.equal(done.can, true);
  assert.equal(done.blocked, "");
  assert.equal(done.progress.done, true);
});

test("an open stop blocks the close and is named, with the rule", () => {
  const out = runClose({ status: "DISPATCHED" }, [stop({ status: "DELIVERED" }), stop({ stop_no: 2, customer_name: "Harbourview", status: "LOADED" })]);
  assert.equal(out.can, false);
  assert.equal(out.blocked, `Stop 2 · Harbourview is on the truck. ${CLOSE_RULE}`);
  assert.equal(runClose({ status: "DISPATCHED" }, [stop({ status: "READY" })]).blocked, `Stop 1 · Bluffside is not loaded yet. ${CLOSE_RULE}`);
  assert.equal(runClose({ status: "DISPATCHED" }, [stop({ status: "EN_ROUTE" })]).blocked, `Stop 1 · Bluffside is still en route. ${CLOSE_RULE}`);
});

test("a cancelled stop blocks a driver's close, as the core does, and says who closes it", () => {
  const out = runClose({ status: "DISPATCHED" }, [stop({ status: "DELIVERED" }), stop({ stop_no: 2, customer_name: "Harbourview", status: "CANCELLED" })]);
  assert.equal(out.can, false);
  assert.equal(out.blocked, "Stop 2 · Harbourview is cancelled, and that is not an outcome a driver records. Ask dispatch to close this run.");
});

test("a run that is not out, is closed, is cancelled or has no stops cannot be closed, and says why", () => {
  assert.match(runClose({ status: "PLANNED" }, [stop()]).blocked, /has not been sent out yet/);
  assert.equal(runClose({ status: "COMPLETED" }, [stop({ status: "DELIVERED" })]).blocked, "This run is closed. Nothing else is needed from you today.");
  assert.equal(runClose({ status: "CANCELLED" }, [stop()]).blocked, "This run is cancelled, so there is nothing here to close.");
  assert.match(runClose({ status: "DISPATCHED" }, []).blocked, /There are no stops on this run yet/);
  assert.equal(runClose({ status: "DISPATCHED" }, []).can, false);
  assert.equal(runClose(null, null).can, false);
});

// ---- the typed name bound (item 6c) ----

test("the client's name bound is the core's (dispatch.maxTypedNameBytes, read from the Go source)", () => {
  assert.equal(MAX_NAME_BYTES, goTypedNameBytes());
});

test("nameProblem refuses what the attestation door would, in words, before the signature is queued", () => {
  assert.equal(nameProblem("Site Foreman"), "");
  assert.equal(nameProblem("  Site Foreman  "), "", "the core trims, so does the form");
  assert.equal(nameProblem("e".repeat(MAX_NAME_BYTES)), "");
  assert.match(nameProblem("e".repeat(MAX_NAME_BYTES + 1)), /too long/);
  assert.match(nameProblem("\u00e9".repeat(MAX_NAME_BYTES / 2 + 1)), /too long/, "bytes, not characters");
  assert.equal(nameProblem("\u00e9".repeat(MAX_NAME_BYTES / 2)), "");
  for (const bad of ["a\u0000", "a\tb", "a\nb", "a\u007f", "a\u202e", "a\u2066"]) assert.match(nameProblem(bad), /control characters/, JSON.stringify(bad));
});

test("a name the door would refuse blocks the sign control, with the words", () => {
  const long = { receivedBy: "e".repeat(MAX_NAME_BYTES + 1), affirmed: true, busy: false };
  assert.equal(canSign(long), false);
  assert.match(signBlockedReason(long), /too long/);
  assert.equal(canSign({ receivedBy: "A\u202eB", affirmed: true, busy: false }), false);
  assert.doesNotMatch(PROOF_QUEUED_NOTICE, /delivered|told/);
});

// ---- UX-17b & UX-17c: stop groups, load order line, house-zone staleness, and preset failure reasons ----
import * as driveLogic from "../src/drive-logic.ts";
import { loadOrderLine, nextStopOf, routeStops } from "../src/drive-logic.ts";
import { stalenessWords } from "../src/views.ts";

test("preset failure reasons list the five board choices and resolveFailureReason requires free text only for Other, say what", () => {
  assert.deepEqual(driveLogic.FAILURE_REASON_PRESETS, [
    "Nobody on site",
    "Gate locked",
    "Site not ready",
    "Customer refused",
    "Other, say what",
  ]);
  assert.equal(driveLogic.resolveFailureReason?.("Nobody on site", ""), "Nobody on site");
  assert.equal(driveLogic.resolveFailureReason?.("Gate locked", "ignored"), "Gate locked");
  assert.equal(driveLogic.resolveFailureReason?.("Site not ready", ""), "Site not ready");
  assert.equal(driveLogic.resolveFailureReason?.("Customer refused", ""), "Customer refused");
  assert.equal(driveLogic.resolveFailureReason?.("Other, say what", ""), "");
  assert.equal(canRecordFailure(driveLogic.resolveFailureReason?.("Other, say what", "   ") ?? ""), false);
  assert.equal(driveLogic.resolveFailureReason?.("Other, say what", "Driveway washed out"), "Driveway washed out");
  assert.equal(canRecordFailure(driveLogic.resolveFailureReason?.("Other, say what", "Driveway washed out") ?? ""), true);
});

test("loadOrderLine names the last stop in delivery order first (back of truck) and the first stop last", () => {
  assert.equal(loadOrderLine([]), "");
  assert.equal(loadOrderLine(null), "");
  assert.equal(
    loadOrderLine([{ stop_no: 1 }, { stop_no: 2 }, { stop_no: 3 }, { stop_no: 4 }]),
    "Checked once for the whole run. Load order: stop 4 first, stop 1 last.",
  );
  assert.equal(
    loadOrderLine([{ stop_no: 3 }, { stop_no: 1 }, { stop_no: 2 }]),
    "Checked once for the whole run. Load order: stop 3 first, stop 1 last.",
  );
});

test("routeStops keeps the run's order with one state chip per stop and the next stop marked", () => {
  const rows = routeStops([
    { id: "s4", stop_no: 4, status: "READY" },
    { id: "s2", stop_no: 2, status: "EN_ROUTE" },
    { id: "s3", stop_no: 3, status: "LOADED" },
    { id: "s1", stop_no: 1, status: "DELIVERED" },
    { id: "s6", stop_no: 6, status: "CANCELLED" },
    { id: "s5", stop_no: 5, status: "FAILED" },
  ]);
  assert.deepEqual(
    rows.map((r) => [r.stop.id, r.chip, r.next]),
    [
      ["s1", "Delivered", false],
      ["s2", "Next stop", true],
      ["s3", "Waiting", false],
      ["s4", "Waiting", false],
      ["s5", "Not delivered", false],
      ["s6", "Cancelled", false],
    ],
  );
});

test("nextStopOf is the en route stop, else the first open stop, else none", () => {
  assert.equal(nextStopOf([{ id: "a", stop_no: 1, status: "READY" }, { id: "b", stop_no: 2, status: "EN_ROUTE" }]).id, "b");
  assert.equal(nextStopOf([{ id: "a", stop_no: 1, status: "DELIVERED" }, { id: "b", stop_no: 2, status: "LOADED" }]).id, "b");
  assert.equal(nextStopOf([{ id: "a", stop_no: 1, status: "CANCELLED" }, { id: "b", stop_no: 2, status: "FAILED" }]), null);
  assert.equal(nextStopOf([]), null);
});

test("stalenessWords includes the last pull clock time in the dealer's house zone using glossary 12-hour AM/PM form with a plain space", () => {
  const morningInstant = new Date(Date.UTC(2026, 6, 3, 15, 30)).toISOString();
  const eveningInstantUtc = new Date(Date.UTC(2026, 6, 3, 0, 30)).toISOString();
  assert.equal(
    stalenessWords({ neverPulled: false, ageMs: 10_000, lastPullAt: morningInstant }, "America/Toronto"),
    "Pulled just now · as of 11:30 AM",
  );
  assert.equal(
    stalenessWords({ neverPulled: false, ageMs: 180_000, lastPullAt: morningInstant }, "America/Los_Angeles"),
    "Pulled 3 min ago · as of 8:30 AM",
  );
  const eveningWords = stalenessWords({ neverPulled: false, ageMs: 5_000, lastPullAt: eveningInstantUtc }, "America/Toronto");
  assert.equal(eveningWords, "Pulled just now · as of 8:30 PM");
  assert.equal(eveningWords.includes("\u202f"), false);
  assert.equal(eveningWords.includes("\u00a0"), false);
  assert.equal(stalenessWords({ neverPulled: true, ageMs: null }), "Never pulled");
});
