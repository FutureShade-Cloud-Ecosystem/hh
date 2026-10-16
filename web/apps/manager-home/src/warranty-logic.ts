// Warranty's decisions, with no DOM in sight: the lifecycle vocabulary and
// the legal moves, the words a walled refusal becomes, the money and
// checklist rules, and the words the timeline, the buckets and the service
// levels render.
//
// Port provenance: the phase columns, the outcome and registry vocabularies,
// the recovery buckets and the money paths are the original app's warranty
// surface (repository hardscapeos_dibbits, read only; see PROVENANCE.md),
// carried over under the port and polish rule. The wall words are this app's
// own: they name the guard the core actually runs (the tier gates of
// identity/guards.go) in the role catalog's own tier words, the same way the
// approvals view names who decides a kind.
//
// THE WORDS-ONLY ZONE, as manager-logic.ts runs it: no raw enum, no raw wire
// decimal, no raw ISO stamp reaches a screen. Anything this file cannot say
// honestly is omitted by the view, never shown raw. Money is integer cents
// from the core; this file formats it for a person and computes nothing.
import { formatDollars, plainDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";
import type { DefectClassification, DefectRow, VendorAgreementKind, VendorCreditPeriod, VendorCreditRow } from "./warranty-api.js";

// -- the lifecycle vocabulary -------------------------------------------------------------------------

/** The lifecycle phases, in the board's column order, the two exits last (the confirmed board). */
export const PHASES = [
  "INTAKE",
  "TRIAGE",
  "SUBMITTED",
  "WAITING_ON_DECISION",
  "DECISION",
  "SETTLEMENT",
  "CLOSED",
  "REJECTED",
  "ABANDONED",
] as const;

export type Phase = (typeof PHASES)[number];

const PHASE_WORD: Readonly<Record<string, string>> = Object.freeze({
  INTAKE: "Intake",
  TRIAGE: "Triage",
  SUBMITTED: "Submitted",
  WAITING_ON_DECISION: "Waiting on decision",
  DECISION: "Decision",
  SETTLEMENT: "Settlement",
  CLOSED: "Closed",
  REJECTED: "Rejected",
  ABANDONED: "Abandoned",
});

export function phaseWord(value: string): string {
  return PHASE_WORD[value] ?? plainly(value);
}

/**
 * The forward moves a claim can take from each phase, the core's transition
 * map. The board only OFFERS legal moves; the server re-enforces every gate
 * and names the missing checklist items in its own words when one is unmet.
 */
const NEXT: Readonly<Record<string, readonly string[]>> = Object.freeze({
  INTAKE: ["TRIAGE", "ABANDONED"],
  TRIAGE: ["SUBMITTED", "REJECTED", "ABANDONED"],
  SUBMITTED: ["WAITING_ON_DECISION", "TRIAGE", "REJECTED", "ABANDONED"],
  WAITING_ON_DECISION: ["DECISION", "TRIAGE", "REJECTED", "ABANDONED"],
  DECISION: ["SETTLEMENT", "REJECTED", "ABANDONED"],
  SETTLEMENT: ["CLOSED", "ABANDONED"],
  CLOSED: [],
  REJECTED: [],
  ABANDONED: [],
});

export function legalMoves(value: string): readonly string[] {
  return NEXT[value] ?? [];
}

const TERMINAL = new Set(["CLOSED", "REJECTED", "ABANDONED"]);

export function isTerminalPhase(value: string): boolean {
  return TERMINAL.has(value);
}

/** The settlement outcomes the contract admits. */
export const OUTCOMES = ["VENDOR_CREDIT", "REPLACEMENT_STOCK", "CUSTOMER_ACCOMMODATION", "WRITE_OFF"] as const;

const OUTCOME_WORD: Readonly<Record<string, string>> = Object.freeze({
  VENDOR_CREDIT: "Vendor credit",
  REPLACEMENT_STOCK: "Replacement stock",
  CUSTOMER_ACCOMMODATION: "Customer accommodation",
  WRITE_OFF: "Write-off",
});

export function outcomeWord(value: string): string {
  return OUTCOME_WORD[value] ?? plainly(value);
}

/** Settlement books only once the vendor's verdict is in: the core refuses earlier phases in its own words. */
export const SETTLEMENT_PHASES: readonly string[] = ["DECISION", "SETTLEMENT"];

// -- the registry vocabulary ---------------------------------------------------------------------------

/** The profile statuses the store admits, for the editor's pick list. */
export const VENDOR_STATUSES = ["DOCUMENTED", "UNDOCUMENTED"] as const;

const VENDOR_STATUS_WORD: Readonly<Record<string, string>> = Object.freeze({
  DOCUMENTED: "Documented",
  UNDOCUMENTED: "Undocumented",
});

export function vendorStatusWord(value: string): string {
  return VENDOR_STATUS_WORD[value] ?? plainly(value);
}

/** The claim channels, how a claim reaches the vendor, for the editor's pick list. */
export const CLAIM_CHANNELS = ["EMAIL", "PORTAL", "REP", "PHONE", "OTHER"] as const;

const CLAIM_CHANNEL_WORD: Readonly<Record<string, string>> = Object.freeze({
  EMAIL: "Email",
  PORTAL: "Portal",
  REP: "Vendor rep",
  PHONE: "Phone",
  OTHER: "Other",
});

export function channelWord(value: string): string {
  return CLAIM_CHANNEL_WORD[value] ?? plainly(value);
}

/**
 * Where a claim CAME FROM, and how it reaches the vendor, are two different
 * facts and the claim detail shows both. WEB is the public intake page, which
 * a homeowner reaches without an account. The capture sheet offers the two
 * staff doors; WEB arrives through the public desk, not this app.
 */
export const CLAIM_SOURCES = ["INTAKE", "COUNTER"] as const;

const CLAIM_SOURCE_WORD: Readonly<Record<string, string>> = Object.freeze({
  INTAKE: "Filed here",
  COUNTER: "Counter",
  WEB: "Web form",
});

export function sourceWord(value: string): string {
  return CLAIM_SOURCE_WORD[value] ?? plainly(value);
}

// -- recovery ------------------------------------------------------------------------------------------

/** The aging buckets, the board's four. */
export const RECOVERY_BUCKETS = ["0-30", "31-60", "61-90", "90+"] as const;

const BUCKET_WORD: Readonly<Record<string, string>> = Object.freeze({
  "0-30": "0 to 30 days",
  "31-60": "31 to 60 days",
  "61-90": "61 to 90 days",
  "90+": "90 days and older",
});

export function bucketWord(value: string): string {
  return BUCKET_WORD[value] ?? plainly(value);
}

/** The chase actions, each with whether it carries money (the form's own rule). */
export const RECOVERY_ACTIONS: readonly {
  readonly action: string;
  readonly word: string;
  readonly money: boolean;
  readonly promised: boolean;
}[] = Object.freeze([
  { action: "CONTACT_MADE", word: "Contact made", money: false, promised: false },
  { action: "PROMISED", word: "Promised", money: true, promised: true },
  { action: "RECEIVED", word: "Received", money: true, promised: false },
  { action: "DISPUTED", word: "Disputed", money: false, promised: false },
  { action: "NOTE", word: "Note", money: false, promised: false },
]);

export function recoveryActionWord(value: string): string {
  return RECOVERY_ACTIONS.find((a) => a.action === value)?.word ?? plainly(value);
}

// -- service levels --------------------------------------------------------------------------------------

export const SLA_STATE_WORDS: Readonly<Record<string, string>> = Object.freeze({
  OK: "On time",
  DUE_SOON: "Due soon",
  BREACHED: "Overdue",
  NO_TARGET: "No target",
});

export function slaStateWord(value: string): string {
  return SLA_STATE_WORDS[value] ?? plainly(value);
}

const NOTIFY_WORD: Readonly<Record<string, string>> = Object.freeze({
  SENT: "Sent",
  SUPPRESSED: "Suppressed",
  FAILED: "Failed",
});

export function notifyWord(value: string): string {
  return NOTIFY_WORD[value] ?? plainly(value);
}

// -- postings, the money paths ----------------------------------------------------------------------------

export const MONEY_PATH_WORDS: Readonly<Record<string, string>> = Object.freeze({
  VENDOR_RECOVERY: "Vendor recovery",
  CUSTOMER_CREDIT: "Customer credit memo",
  INVENTORY_REPLACEMENT: "Inventory replacement",
  EXPENSE_WRITE_OFF: "Warranty expense",
});

export function moneyPathWord(value: string): string {
  return MONEY_PATH_WORDS[value] ?? plainly(value);
}

export const PATH_STATUS_WORDS: Readonly<Record<string, string>> = Object.freeze({
  POSTED: "Posted",
  NO_EFFECT: "No money moved",
  SEAM_UNWIRED: "Awaiting integration",
  FALLBACK_GL: "Posted to the ledger, no document raised",
  FAILED: "Failed",
  MANUAL: "Manual override",
});

export function pathStatusWord(value: string): string {
  return PATH_STATUS_WORDS[value] ?? plainly(value);
}

// -- the money rules --------------------------------------------------------------------------------------

/**
 * The one money rendering on these screens: the core's integer cents,
 * formatted for a person. Computed nowhere, summed nowhere, converted
 * nowhere.
 */
export function centsWords(cents: number): string {
  return formatDollars(cents);
}

/**
 * Dollars a person typed to cents, through the decimal string and never a
 * float multiply (the repo's money rule; mirrors pos-kit's parseAmountCents).
 * Blank, more than two places, a sign, an exponent or anything else that is
 * not plain dollars answers null; a blank is a different mistake from a
 * deliberate zero, so callers word it themselves.
 */
export function centsFromDollars(typed: string): number | null {
  const trimmed = typed.trim();
  if (!/^\d+(\.\d{0,2})?$/.test(trimmed)) return null;
  const point = trimmed.indexOf(".");
  if (point === -1) return Number(`${trimmed}00`);
  return Number(`${trimmed.slice(0, point)}${trimmed.slice(point + 1).padEnd(2, "0")}`);
}

/**
 * The settlement sheet needs a good realized figure; a blank expected figure
 * is allowed (the server keeps the captured exposure), a typed one must parse.
 */
export function canSettle(outcome: string, expectedTyped: string, realizedTyped: string): boolean {
  if (!OUTCOMES.some((o) => o === outcome)) return false;
  if (centsFromDollars(realizedTyped) === null) return false;
  return expectedTyped.trim() === "" || centsFromDollars(expectedTyped) !== null;
}

/** Hours as a person reads them: one decimal at most, the unit carried. */
export function hoursWords(value: number): string {
  const rounded = Math.round(value * 10) / 10;
  return `${rounded}h`;
}

/**
 * The settlement sheet's starting expected figure: the server's own cents as
 * the dollars a person edits, two places, typed once. Display of a server
 * figure; the cents a settlement sends are parsed from what the person left
 * in the field, never from this rendering.
 */
export function dollarsTyped(cents: number): string {
  return (cents / 100).toFixed(2);
}

// -- the checklist ------------------------------------------------------------------------------------------

/**
 * The checklist header's words: the required items still open, counted, or
 * complete. An optional item left unticked holds nobody up; the server says
 * so again in its own words when a move is refused.
 */
export function checklistWords(states: readonly { readonly item: { readonly required: boolean }; readonly checked: boolean }[]): string {
  const open = states.filter((s) => s.item.required && !s.checked).length;
  return open === 0 ? "Checklist complete" : `${open} required item${open === 1 ? "" : "s"} still open`;
}

// -- the timeline -------------------------------------------------------------------------------------------

/**
 * One timeline event as its own sentence. The phase move names both ends;
 * a tick names the item and the note it carried; the server's own note is
 * the sentence for anything else.
 */
export function eventLine(event: {
  readonly event_type: string;
  readonly from_phase?: string;
  readonly to_phase?: string;
  readonly checklist_key?: string;
  readonly checked?: boolean | null;
  readonly note: string;
  readonly evidence_ref?: string;
}): string {
  if (event.event_type === "PHASE_CHANGE") {
    return `Moved from ${phaseWord(event.from_phase ?? "").toLowerCase()} to ${phaseWord(event.to_phase ?? "").toLowerCase()}`;
  }
  if (event.event_type === "CHECKLIST_TICK") {
    const base = `${event.checked === true ? "Ticked" : "Unticked"} ${event.checklist_key ?? ""}`.trim();
    return event.note !== "" ? `${base}: ${event.note}` : base;
  }
  if (event.event_type === "SETTLEMENT") {
    return event.note !== "" ? `Settlement: ${event.note}` : "Settlement recorded";
  }
  if (event.event_type === "EVIDENCE") {
    return event.evidence_ref !== undefined && event.evidence_ref !== "" ? `Evidence attached: ${event.evidence_ref}` : "Evidence attached";
  }
  return event.note;
}

/** A timeline stamp: the day and the hour, never the raw ISO shape. */
export function stampWords(iso: string): string {
  const day = datePart(iso);
  const time = /^.+T(\d{2}:\d{2})/.exec(iso.trim());
  if (day === "" || time === null) return day;
  return `${day}, ${time[1]}`;
}

function datePart(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso.trim());
  if (m === null) return "";
  const month = Number.parseInt(m[2], 10);
  const day = Number.parseInt(m[3], 10);
  if (month < 1 || month > 12 || day < 1 || day > 31) return "";
  return `${MONTH_WORD[month - 1]} ${day}`;
}

const MONTH_WORD = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"] as const;

// -- the walls: who each refusal names --------------------------------------------------------------------
//
// The core's tier gates answer the shared Forbidden body, one word, on every
// wall. A bare "Forbidden" on a screen tells nobody anything, so each wall
// becomes words naming who the gate lets through, in the role catalog's own
// tier words. Anything else a server says is the server's own sentence and
// passes through untouched.

/** Who settles: the tier 1 and tier 2 roles, the guards' RequireManager list. */
export const SETTLE_MANAGER_WORDS =
  "Settlements are recorded by the manager roles (tier 1 and tier 2): the general manager, the financial controller, and the branch, sales, yard, logistics and procurement managers.";

export function settleRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && isForbiddenBody(error)) {
    return `You may not record this settlement. ${SETTLE_MANAGER_WORDS}`;
  }
  return error !== "" ? error : "That did not go through. Try again in a moment.";
}

/** Who runs the sweep: the same manager list the settle gate names. */
export function slaRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && isForbiddenBody(error)) {
    return "You may not run the service level sweep. The sweep is run by the manager roles (tier 1 and tier 2).";
  }
  return error !== "" ? error : "That did not go through. Try again in a moment.";
}

/** Who configures the registry: the tier 1 global roles, the guards' RequireAdmin list. */
export const VENDOR_ADMIN_WORDS =
  "Registry configuration belongs to the tier 1 roles: the general manager, the financial controller, and the dealer administrator.";

export function vendorRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && isForbiddenBody(error)) {
    return `You may not change the vendor warranty registry. ${VENDOR_ADMIN_WORDS}`;
  }
  return error !== "" ? error : "That did not go through. Try again in a moment.";
}

/** Who logs chase actions: the staff of the org, the same gate the reads sit behind. */
export function recoveryRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && isForbiddenBody(error)) {
    return "You may not log recovery actions on this claim. The recovery log belongs to the staff roles of this org, for claims in a branch your grants reach.";
  }
  return error !== "" ? error : "That did not go through. Try again in a moment.";
}

export function advanceRefusalWords(error: string, status: number | undefined): string {
  if (status === 403 && isForbiddenBody(error)) {
    return "You may not move this claim. The warranty lifecycle belongs to the staff roles of this org.";
  }
  return error !== "" ? error : "That did not go through. Try again in a moment.";
}

/** The shared Forbidden body is exactly one word; a sentence the core wrote passes through. */
export function isForbiddenBody(error: string): boolean {
  return error.trim() === "Forbidden";
}

// -- the capture sheet's own words -----------------------------------------------------------------------------

/** The two rules the capture sheet states, so nobody learns them from a refusal. */
export const captureWords: Readonly<{ readonly owner: string; readonly backdate: string }> = Object.freeze({
  owner: "The claim is owned by whoever captures it, and a vendor can be attached now or at triage.",
  backdate: "Opened can be an earlier day, so a claim that sat on the counter reads honestly in the reports.",
});

// -- the plain fallback -----------------------------------------------------------------------------------------

/** The plain fallback for any unknown closed-set value: lowercased, separators opened out. */
function plainly(value: string): string {
  return value.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
}

// -- change 6 item 5: defects and vendor credits --------------------------------------------------------------------

/** The Result column on the Defects table: the bucket the line cost fell into. */
export function defectBadge(classification: DefectClassification): string {
  if (classification === "CLAIM") return "Warranty claim";
  return "Shrinkage";
}

/** The Claim threshold tile's headline: integer cents to "X.YY" via the
 *  money format, never carries a raw currency symbol. */
export function thresholdDollars(cents: number): string {
  return plainDollars(cents);
}

/** The three tile numbers on the Defects tab. */
export interface DefectHeadlines {
  readonly claims: number;
  readonly shrinkage: number;
  readonly claimCents: number;
  readonly shrinkageCents: number;
}

export function defectHeadlines(defects: readonly DefectRow[], thresholdCents: number): DefectHeadlines {
  void thresholdCents;
  let claims = 0;
  let shrinkage = 0;
  let claimCents = 0;
  let shrinkageCents = 0;
  for (const row of defects) {
    if (row.classification === "CLAIM") {
      claims += 1;
      claimCents += row.cost_cents;
    } else {
      shrinkage += 1;
      shrinkageCents += row.cost_cents;
    }
  }
  return { claims, shrinkage, claimCents, shrinkageCents };
}

/** The chip row applied to the table. Empty string = no filter (All). */
export function filteredDefects(defects: readonly DefectRow[], classification: DefectClassification | ""): readonly DefectRow[] {
  if (classification === "") return defects;
  return defects.filter((row) => row.classification === classification);
}

/** The chip label on the Defects tab. */
export function defectFilterWord(classification: DefectClassification | ""): string {
  if (classification === "") return "All";
  if (classification === "CLAIM") return "Warranty claims";
  return "Shrinkage";
}

/** The Claim cell on the Defects table: the claim id for claim rows, "Logged" otherwise. */
export function defectClaimLinkText(defect: DefectRow): string {
  if (defect.classification === "CLAIM" && defect.claim_no !== "") return defect.claim_no;
  return "Logged";
}

/** The agreement column on the Vendors and credits table. */
export function vendorAgreementWord(kind: VendorAgreementKind): string {
  if (kind === "CLAIM_BY_CLAIM") return "Claim by claim";
  if (kind === "PERIODIC_CREDIT_NOTE") return "Periodic credit note";
  return "Fixed damage allowance";
}

/** The period column on the Vendors and credits table. */
export function vendorPeriodWord(period: VendorCreditPeriod): string {
  if (period === "EACH_CLAIM") return "Each claim";
  if (period === "MONTHLY") return "Monthly";
  return "Quarterly";
}

/** The Next request column: "in N days" for the periodic vendors, "on each claim" for claim_by_claim. */
export function nextRequestPhrase(row: VendorCreditRow): string {
  if (row.period === "EACH_CLAIM") return "on each claim";
  if (row.next_request_in_days <= 0) return "today";
  if (row.next_request_in_days === 1) return "tomorrow";
  return `in ${row.next_request_in_days} days`;
}

/** The three tile numbers on the Vendors and credits tab. */
export interface VendorCreditHeadlines {
  readonly creditCents: number;
  readonly vendorCount: number;
  readonly nextRequestInDays: number;
  readonly nextRequestVendor: string;
  readonly nextRequestPeriod: VendorCreditPeriod | "";
  readonly claimsInPeriod: number;
}

export function vendorCreditHeadlines(rows: readonly VendorCreditRow[]): VendorCreditHeadlines {
  let creditCents = 0;
  let vendorCount = 0;
  let claimsInPeriod = 0;
  let nextIn = Number.POSITIVE_INFINITY;
  let nextVendor = "";
  let nextPeriod: VendorCreditPeriod | "" = "";
  for (const row of rows) {
    if (row.credit_cents > 0) {
      creditCents += row.credit_cents;
      vendorCount += 1;
    }
    claimsInPeriod += row.claims_in_period;
    if (row.period !== "EACH_CLAIM" && row.next_request_in_days < nextIn) {
      nextIn = row.next_request_in_days;
      nextVendor = row.vendor_name;
      nextPeriod = row.period;
    }
  }
  if (!Number.isFinite(nextIn)) nextIn = 0;
  return { creditCents, vendorCount, nextRequestInDays: nextIn, nextRequestVendor: nextVendor, nextRequestPeriod: nextPeriod, claimsInPeriod };
}

/** The Next credit note request tile's headline. */
export function nextRequestHeadlineWord(inDays: number): string {
  if (inDays <= 0) return "Today";
  if (inDays === 1) return "Tomorrow";
  return `${inDays} days`;
}

/** The Next credit note request tile's subline. */
export function nextRequestSubline(vendorName: string, period: VendorCreditPeriod | ""): string {
  const periodWord = period === "" ? "" : vendorPeriodWord(period).toLowerCase();
  if (vendorName === "") return period === "" ? "No vendor" : `No vendor, ${periodWord}`;
  return period === "" ? vendorName : `${vendorName}, ${periodWord}`;
}

/** The Evidence column on the Defects table: a people-readable count of photos. */
export function evidenceCountWord(count: number): string {
  if (count <= 0) return "none";
  if (count === 1) return "1 photo";
  return `${count} photos`;
}

/** The Evidence cell: count of photo ids on the defect row. */
export function defectEvidenceWord(defect: DefectRow): string {
  return evidenceCountWord(defect.photo_ids.length);
}
