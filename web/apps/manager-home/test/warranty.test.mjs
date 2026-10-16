// Warranty's pure decisions, on their own: the phase vocabulary and the legal
// forward moves, the words a walled refusal becomes, the money and checklist
// rules, and the words the timeline, the buckets and the SLA states render.
// No browser, no network. Every case is a fact the core holds (the wire
// contract, the transition map, the tier guards) or the original screen
// carried.
import { test } from "node:test";
import assert from "node:assert/strict";
import {
  CLAIM_CHANNELS,
  CLAIM_SOURCES,
  MONEY_PATH_WORDS,
  OUTCOMES,
  PATH_STATUS_WORDS,
  PHASES,
  RECOVERY_ACTIONS,
  RECOVERY_BUCKETS,
  SETTLE_MANAGER_WORDS,
  SLA_STATE_WORDS,
  VENDOR_ADMIN_WORDS,
  VENDOR_STATUSES,
  advanceRefusalWords,
  bucketWord,
  canSettle,
  captureWords,
  centsFromDollars,
  centsWords,
  checklistWords,
  eventLine,
  hoursWords,
  isTerminalPhase,
  legalMoves,
  phaseWord,
  recoveryRefusalWords,
  settleRefusalWords,
  slaRefusalWords,
  vendorRefusalWords,
  defectBadge,
  defectClaimLinkText,
  defectEvidenceWord,
  defectFilterWord,
  defectHeadlines,
  evidenceCountWord,
  filteredDefects,
  nextRequestHeadlineWord,
  nextRequestPhrase,
  nextRequestSubline,
  thresholdDollars,
  vendorAgreementWord,
  vendorCreditHeadlines,
  vendorPeriodWord,
} from "../src/warranty-logic.ts";

test("the phase vocabulary is the core's closed set, in board order, with the two exits last", () => {
  assert.deepEqual(PHASES, [
    "INTAKE",
    "TRIAGE",
    "SUBMITTED",
    "WAITING_ON_DECISION",
    "DECISION",
    "SETTLEMENT",
    "CLOSED",
    "REJECTED",
    "ABANDONED",
  ]);
  assert.equal(phaseWord("WAITING_ON_DECISION"), "Waiting on decision");
  assert.equal(phaseWord("INTAKE"), "Intake");
});

test("the legal moves are the core's transition map; a terminal phase moves nowhere", () => {
  assert.deepEqual(legalMoves("INTAKE"), ["TRIAGE", "ABANDONED"]);
  assert.deepEqual(legalMoves("TRIAGE"), ["SUBMITTED", "REJECTED", "ABANDONED"]);
  assert.deepEqual(legalMoves("SETTLEMENT"), ["CLOSED", "ABANDONED"]);
  assert.deepEqual(legalMoves("CLOSED"), []);
  assert.deepEqual(legalMoves("REJECTED"), []);
  assert.deepEqual(legalMoves("ABANDONED"), []);
  assert.equal(isTerminalPhase("CLOSED"), true);
  assert.equal(isTerminalPhase("TRIAGE"), false);
});

test("settlement outcomes are the four the contract admits", () => {
  assert.deepEqual(OUTCOMES, ["VENDOR_CREDIT", "REPLACEMENT_STOCK", "CUSTOMER_ACCOMMODATION", "WRITE_OFF"]);
});

test("vendor and channel and source vocabularies are the stored enums the profile editor offers", () => {
  assert.deepEqual(VENDOR_STATUSES, ["DOCUMENTED", "UNDOCUMENTED"]);
  assert.deepEqual(CLAIM_CHANNELS, ["EMAIL", "PORTAL", "REP", "PHONE", "OTHER"]);
  assert.deepEqual(CLAIM_SOURCES, ["INTAKE", "COUNTER"]);
});

test("money is integer cents in, words out, computed from a dollars string once", () => {
  assert.equal(centsFromDollars("12.5"), 1250);
  assert.equal(centsFromDollars("0"), 0);
  assert.equal(centsFromDollars(""), null);
  assert.equal(centsFromDollars("   "), null);
  assert.equal(centsFromDollars("1.005"), null);
  assert.equal(centsFromDollars("1e"), null);
  assert.equal(centsFromDollars("e5"), null);
  assert.equal(centsFromDollars("1.1"), 110);
  assert.equal(centsFromDollars("0.07"), 7);
  assert.equal(centsFromDollars("19.99"), 1999);
  assert.equal(centsFromDollars("8."), 800);
  assert.equal(centsFromDollars("nonsense"), null);
  assert.equal(centsFromDollars("-4"), null);
  assert.equal(centsWords(24000), "$240.00");
  assert.equal(centsWords(0), "$0.00");
  assert.equal(centsWords(1968), "$19.68");
});

test("the settlement sheet refuses a blank or malformed realized figure and lets expected stay blank", () => {
  assert.equal(canSettle("VENDOR_CREDIT", "", ""), false);
  assert.equal(canSettle("VENDOR_CREDIT", "10.00", "1e"), false);
  assert.equal(canSettle("VENDOR_CREDIT", "10.00", "1.005"), false);
  assert.equal(canSettle("VENDOR_CREDIT", "1.005", "5.00"), false);
  assert.equal(canSettle("VENDOR_CREDIT", "", "5.00"), true);
  assert.equal(canSettle("VENDOR_CREDIT", "10.00", "0"), true);
});

test("hours render as a person reads them", () => {
  assert.equal(hoursWords(0), "0h");
  assert.equal(hoursWords(27.5), "27.5h");
  assert.equal(hoursWords(27.25), "27.3h");
});

test("the recovery buckets are the board's four, named in words", () => {
  assert.deepEqual(RECOVERY_BUCKETS, ["0-30", "31-60", "61-90", "90+"]);
  assert.equal(bucketWord("90+"), "90 days and older");
  assert.equal(bucketWord("0-30"), "0 to 30 days");
});

test("recovery actions are the five the core validates, with the money rule beside each", () => {
  assert.deepEqual(
    RECOVERY_ACTIONS.map((a) => a.action),
    ["CONTACT_MADE", "PROMISED", "RECEIVED", "DISPUTED", "NOTE"],
  );
  const byKey = Object.fromEntries(RECOVERY_ACTIONS.map((a) => [a.action, a]));
  assert.equal(byKey.RECEIVED.money, true);
  assert.equal(byKey.PROMISED.money, true);
  assert.equal(byKey.NOTE.money, false);
  assert.equal(byKey.CONTACT_MADE.money, false);
  assert.equal(byKey.DISPUTED.money, false);
});

test("the SLA states are the four the board answers", () => {
  assert.deepEqual(SLA_STATE_WORDS, { OK: "On time", DUE_SOON: "Due soon", BREACHED: "Overdue", NO_TARGET: "No target" });
});

test("the posting vocabulary names the money paths and the statuses the server stamps", () => {
  assert.equal(MONEY_PATH_WORDS.VENDOR_RECOVERY, "Vendor recovery");
  assert.equal(MONEY_PATH_WORDS.EXPENSE_WRITE_OFF, "Warranty expense");
  assert.equal(PATH_STATUS_WORDS.POSTED, "Posted");
  assert.equal(PATH_STATUS_WORDS.SEAM_UNWIRED, "Awaiting integration");
  assert.equal(PATH_STATUS_WORDS.FALLBACK_GL, "Posted to the ledger, no document raised");
});

test("a 403 on settle is the shared Forbidden body, walled in words naming who settles", () => {
  assert.equal(
    settleRefusalWords("Forbidden", 403),
    `You may not record this settlement. ${SETTLE_MANAGER_WORDS}`,
  );
  assert.match(SETTLE_MANAGER_WORDS, /manager roles/);
  // Anything else is the server's own sentence.
  assert.equal(settleRefusalWords("warranty: claim WTY-1 is already closed", 409), "warranty: claim WTY-1 is already closed");
});

test("a 403 on the sweep is walled in words naming who runs it", () => {
  const words = slaRefusalWords("Forbidden", 403);
  assert.match(words, /You may not run the service level sweep/);
  assert.match(words, /manager roles/);
  assert.equal(slaRefusalWords("warranty: caller cannot access branch b2", 403), "warranty: caller cannot access branch b2");
});

test("a 403 on the registry write is walled in words naming who configures the registry", () => {
  const words = vendorRefusalWords("Forbidden", 403);
  assert.match(words, /You may not change the vendor warranty registry/);
  assert.match(words, /tier 1/);
  assert.equal(vendorRefusalWords("warranty: unknown vendor \"v9\", create the vendor before configuring its warranty profile", 422).includes("unknown vendor"), true);
});

test("a 403 on a recovery write is walled in words naming who chases a recovery", () => {
  const words = recoveryRefusalWords("Forbidden", 403);
  assert.match(words, /You may not log recovery actions/);
  assert.equal(recoveryRefusalWords("warranty: amount_cents must be >= 0", 422), "warranty: amount_cents must be >= 0");
});

test("an advance refusal carries the core's own gate sentence, including the missing items", () => {
  assert.equal(
    advanceRefusalWords("warranty: cannot advance, required checklist items incomplete: photos", 422),
    "warranty: cannot advance, required checklist items incomplete: photos",
  );
  assert.equal(advanceRefusalWords("Forbidden", 403), "You may not move this claim. The warranty lifecycle belongs to the staff roles of this org.");
});

test("the checklist header counts the required items still open, and says complete when none are", () => {
  const states = [
    { item: { id: "1", phase: "TRIAGE", item_key: "photos", label: "Photos", required: true, sort_order: 0 }, checked: true },
    { item: { id: "2", phase: "TRIAGE", item_key: "invoice", label: "Invoice", required: true, sort_order: 1 }, checked: false },
    { item: { id: "3", phase: "TRIAGE", item_key: "note", label: "Note", required: false, sort_order: 2 }, checked: false },
  ];
  assert.equal(checklistWords(states), "1 required item still open");
  const done = states.map((s, i) => ({ ...s, checked: true }));
  assert.equal(checklistWords(done), "Checklist complete");
});

test("a timeline event reads as its own sentence; the phase move names both ends", () => {
  assert.equal(
    eventLine({ id: "e1", claim_id: "c1", event_type: "PHASE_CHANGE", from_phase: "TRIAGE", to_phase: "SUBMITTED", note: "", occurred_at: "2026-10-01T10:00:00Z" }),
    "Moved from triage to submitted",
  );
  assert.equal(
    eventLine({ id: "e2", claim_id: "c1", event_type: "CHECKLIST_TICK", checklist_key: "photos", checked: true, note: "roof photos added", occurred_at: "2026-10-01T11:00:00Z" }),
    "Ticked photos: roof photos added",
  );
  assert.equal(
    eventLine({ id: "e3", claim_id: "c1", event_type: "CHECKLIST_TICK", checklist_key: "photos", checked: false, note: "", occurred_at: "2026-10-01T11:00:00Z" }),
    "Unticked photos",
  );
  assert.equal(
    eventLine({ id: "e4", claim_id: "c1", event_type: "SETTLEMENT", note: "credit approved", occurred_at: "2026-10-02T09:00:00Z" }),
    "Settlement: credit approved",
  );
  assert.equal(
    eventLine({ id: "e5", claim_id: "c1", event_type: "EVIDENCE", evidence_ref: "ev://claim/9f", note: "", occurred_at: "2026-10-02T09:00:00Z" }),
    "Evidence attached: ev://claim/9f",
  );
  assert.equal(
    eventLine({ id: "e6", claim_id: "c1", event_type: "NOTE", note: "vendor agreed to replace", occurred_at: "2026-10-02T09:00:00Z" }),
    "vendor agreed to replace",
  );
  assert.equal(
    eventLine({ id: "e7", claim_id: "c1", event_type: "CREATED", note: "Claim captured (INTAKE).", occurred_at: "2026-10-02T09:00:00Z" }),
    "Claim captured (INTAKE).",
  );
});

test("the capture sheet states its own rules: who owns a captured claim, and what the date does", () => {
  assert.match(captureWords.owner, /whoever captures it/);
  assert.match(captureWords.backdate, /earlier day/);
});

// -- change 6 item 5: defects and vendor credits ---------------------------------------------------------

test("defectBadge names the outcome the table column shows", () => {
  assert.equal(defectBadge("CLAIM"), "Warranty claim");
  assert.equal(defectBadge("SHRINKAGE"), "Shrinkage");
});

test("defectFilterWord names the chip's bucket text, an empty filter is All", () => {
  assert.equal(defectFilterWord(""), "All");
  assert.equal(defectFilterWord("CLAIM"), "Warranty claims");
  assert.equal(defectFilterWord("SHRINKAGE"), "Shrinkage");
});

test("defectEvidenceWord and evidenceCountWord name the photo count in people words", () => {
  const empty = { photo_ids: [] };
  const one = { photo_ids: ["p1"] };
  const three = { photo_ids: ["p1", "p2", "p3"] };
  assert.equal(defectEvidenceWord(empty), "none");
  assert.equal(defectEvidenceWord(one), "1 photo");
  assert.equal(defectEvidenceWord(three), "3 photos");
  assert.equal(evidenceCountWord(0), "none");
  assert.equal(evidenceCountWord(1), "1 photo");
  assert.equal(evidenceCountWord(4), "4 photos");
});

test("thresholdDollars rounds and never carries a raw currency symbol", () => {
  assert.equal(thresholdDollars(5000), "50.00");
  assert.equal(thresholdDollars(0), "0.00");
});

test("vendorAgreementWord names the three agreement kinds the boards show", () => {
  assert.equal(vendorAgreementWord("CLAIM_BY_CLAIM"), "Claim by claim");
  assert.equal(vendorAgreementWord("PERIODIC_CREDIT_NOTE"), "Periodic credit note");
  assert.equal(vendorAgreementWord("FIXED_ALLOWANCE"), "Fixed damage allowance");
});

test("vendorPeriodWord names the period a vendor's credits roll into", () => {
  assert.equal(vendorPeriodWord("EACH_CLAIM"), "Each claim");
  assert.equal(vendorPeriodWord("MONTHLY"), "Monthly");
  assert.equal(vendorPeriodWord("QUARTERLY"), "Quarterly");
});

test("nextRequestHeadlineWord names the days the chip shows on the home tab", () => {
  assert.equal(nextRequestHeadlineWord(0), "Today");
  assert.equal(nextRequestHeadlineWord(1), "Tomorrow");
  assert.equal(nextRequestHeadlineWord(7), "7 days");
});

test("nextRequestSubline names the vendor and period the chip's second line carries", () => {
  assert.match(nextRequestSubline("Northland Stone", "MONTHLY"), /Northland/);
  assert.match(nextRequestSubline("", "MONTHLY"), /No vendor/);
});
