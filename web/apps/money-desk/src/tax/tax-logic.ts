// The tax slice's plain logic: the words for every state the core stores, which rate rows are in force, how its
// refusals read out loud, and what the screen checks before it sends. Nothing here computes money and nothing
// here is rendered.
//
// The core's tax module was written for an Ontario dealer: its reason codes, its sentences and two of its fields
// still say HST, band card, TCRD and Ontario. This app is shown to a California dealer, so none of those words
// reaches a screen. Every code has a word of ours, every refusal that carries one of the old words is rewritten,
// and an unknown value is opened out (lowercased, underscores gone), never hidden. The wire is untouched.
//
// Port provenance: the reason, profile status and forced evidence words are the original app's
// (app/src/tax-api.ts taxReasonWord and cardProfileStatusWord, app/src/tax-admin-logic.ts forcedEvidence), with the
// words changed to the dealer's trade: a resale or exemption certificate, a signed statement, sales tax.
import { formatMoney, formatRate } from "./tax-money.js";

export type Tone = "stop" | "watch" | "good" | "plain";

function opened(value: string): string {
  return value.toLowerCase().replace(/_/g, " ");
}

function wordsFrom(table: Readonly<Record<string, string>>, value: string): string {
  return table[value] ?? opened(value);
}

// ---- the rate table -----------------------------------------------------------------------------------

/** The five reasons the contract names; the screen offers these when a row is added. */
export const REASONS: readonly string[] = ["STD_ON_HST_13", "FED_S87_VENDOR_DELIVERY", "ON_FN_POS_8", "FNGST_5", "MANUAL_TAX_REVIEW"];

const REASON: Record<string, string> = {
  STD_ON_HST_13: "Standard sales tax",
  FED_S87_VENDOR_DELIVERY: "Exempt delivery, proof of delivery required",
  ON_FN_POS_8: "Reduced rate at the counter, certificate required",
  FNGST_5: "Reduced rate, certificate required",
  MANUAL_TAX_REVIEW: "Held for a person to decide",
};
export const reasonWord = (code: string): string => REASON[code] ?? "Other reason";

/** The line under a reason that says what it does to a sale. */
const REASON_LINE: Record<string, string> = {
  STD_ON_HST_13: "Charged on every taxable line unless a certificate applies.",
  FED_S87_VENDOR_DELIVERY: "Takes effect only with proof of delivery attached; without it the standard rate is charged.",
  ON_FN_POS_8: "Takes effect only with a valid certificate on file; without it the standard rate is charged.",
  FNGST_5: "Takes effect only with a valid certificate on file; without it the standard rate is charged.",
  MANUAL_TAX_REVIEW: "The sale waits for a person to set the tax.",
};
export const reasonLine = (code: string): string => REASON_LINE[code] ?? "A reason the screen does not describe; its rate still applies as stored.";

/** The reasons whose reduced rate binds only with evidence on file (core evidenceGatedReasons). */
export const NEEDS_EVIDENCE: readonly string[] = ["FED_S87_VENDOR_DELIVERY", "ON_FN_POS_8", "FNGST_5"];

const AREA: Record<string, string> = { CA: "California" };
/** An area the screen does not know reads neutrally: the core's own default is an Ontario code, never shown. */
export const areaWord = (code: string): string => AREA[code] ?? "Other area";

export type RateState = "in-force" | "scheduled" | "ended";

/** A row is in force from its first day to its last, both included; the days compare as the core's own text. */
export function rateState(effectiveFrom: string, effectiveTo: string | undefined, today: string): RateState {
  if (effectiveFrom > today) return "scheduled";
  if (effectiveTo !== undefined && effectiveTo !== "" && effectiveTo < today) return "ended";
  return "in-force";
}

const RATE_STATE: Record<RateState, string> = { "in-force": "In force", scheduled: "Starts later", ended: "Ended" };
export const rateStateWord = (state: RateState): string => RATE_STATE[state];

export function rateStateTone(state: RateState): Tone {
  if (state === "in-force") return "good";
  if (state === "scheduled") return "watch";
  return "plain";
}

/** The tax area new rows join: the standard rate in force today, else the first row, else the dealer's own (California). */
export function areaForNewRows(rows: ReadonlyArray<{ jurisdiction: string; reason_code: string; effective_from: string; effective_to?: string }>, today: string): string {
  const standard = rows.find((r) => r.reason_code === "STD_ON_HST_13" && rateState(r.effective_from, r.effective_to, today) === "in-force");
  return standard?.jurisdiction ?? rows[0]?.jurisdiction ?? "CA";
}

// ---- profiles -----------------------------------------------------------------------------------------

const PROFILE_STATUS: Record<string, string> = { ACTIVE: "In force", PENDING: "Waiting for review", RETIRED: "Retired" };
export const profileStatusWord = (status: string): string => wordsFrom(PROFILE_STATUS, status);

export function profileTone(status: string): Tone {
  if (status === "ACTIVE") return "good";
  if (status === "PENDING") return "watch";
  return "plain";
}

export const PROFILE_STATUSES: readonly string[] = ["ACTIVE", "PENDING", "RETIRED"];

/** The evidence one profile forces, as the one sentence a reader sees. */
export function forcedEvidence(p: { requires_photo: boolean; requires_number: boolean; requires_attestation: boolean }): string {
  const parts = [p.requires_photo ? "photo of the certificate" : "", p.requires_number ? "certificate number" : "", p.requires_attestation ? "signed statement" : ""].filter((x) => x !== "");
  if (parts.length === 0) return "Nothing is required";
  if (parts.length === 1) return `Requires the ${parts[0]}`;
  return `Requires the ${parts.slice(0, -1).join(", ")} and the ${parts[parts.length - 1]}`;
}

/** A code on a profile: capitals, digits and underscores, so it reads the same wherever it is typed. */
export function isProfileCode(text: string): boolean {
  return /^[A-Z0-9_]{1,40}$/.test(text);
}

// ---- config -------------------------------------------------------------------------------------------

export const ROUND_MODES: readonly string[] = ["INVOICE_TOTAL", "PER_LINE"];
const ROUND_MODE: Record<string, string> = { INVOICE_TOTAL: "Round once, on the invoice total", PER_LINE: "Round each line" };
export const roundModeWord = (mode: string): string => wordsFrom(ROUND_MODE, mode);
const ROUND_MODE_LINE: Record<string, string> = {
  INVOICE_TOTAL: "The tax on an invoice is worked out once on its taxable subtotal and rounded half up.",
  PER_LINE: "The tax on each line is rounded on its own, and the invoice carries their sum.",
};
export const roundModeLine = (mode: string): string => ROUND_MODE_LINE[mode] ?? "";

// ---- evidence -----------------------------------------------------------------------------------------

/** The banner the core stamps on every record: the signed statement's wording is not yet cleared. */
export function attestationWords(status: string | undefined): string {
  if (status === undefined || status === "") return "";
  if (status === "PENDING_LEGAL_REVIEW") return "The signed statement's wording is waiting for legal review.";
  return opened(status);
}

/** Why a record cannot back a reduced rate, in our words. */
export function blockReasonWords(reason: string | undefined): string {
  if (reason === undefined || reason === "") return "";
  if (reason === "attestation missing") return "The signed statement is missing.";
  if (reason === "status card (TCRD) expired") return "The certificate has expired.";
  return sentence(reason);
}

// ---- recon --------------------------------------------------------------------------------------------

export interface VarianceView {
  readonly word: string;
  readonly tone: Tone;
  readonly detail: string;
}

/**
 * The reconciliation's variance, which the core computes as the ledger's payable balance less the tax the sales
 * collected. Zero is tied out. The direction is said in words so the number is never read alone.
 */
export function varianceWords(varianceCents: number): VarianceView {
  if (varianceCents === 0) return { word: "Tied out", tone: "good", detail: "The ledger holds the tax the sales collected." };
  const size = formatMoney(Math.abs(varianceCents));
  return {
    word: `Off by ${size}`,
    tone: "stop",
    detail: varianceCents > 0 ? "The ledger holds more tax than the sales collected." : "The ledger holds less tax than the sales collected.",
  };
}

// ---- dates and ids ------------------------------------------------------------------------------------

const MONTHS_SHORT = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const MONTHS_LONG = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

/** A date or timestamp is read from its digits, never through a zone. */
export function dayWords(value: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(value);
  const month = m === null ? undefined : MONTHS_SHORT[Number(m[2]) - 1];
  if (m === null || month === undefined) return value;
  return `${month} ${Number(m[3])}, ${m[1]}`;
}

export function periodWords(year: number, month: number): string {
  if (month === 0) return `${year}`;
  const name = MONTHS_LONG[month - 1];
  return name === undefined ? `${year}-${month}` : `${name} ${year}`;
}

/** A real calendar day written year, month, day with dashes. */
export function isDay(text: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text);
  if (m === null) return false;
  const probe = new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])));
  return probe.getUTCFullYear() === Number(m[1]) && probe.getUTCMonth() === Number(m[2]) - 1 && probe.getUTCDate() === Number(m[3]);
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
export const isUuid = (text: string): boolean => UUID.test(text.trim());

/** The host's own day, written year, month, day: the only clock the screen reads. */
export function hostDay(now: Date = new Date()): string {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
}

// ---- refusals -----------------------------------------------------------------------------------------

function sentence(text: string): string {
  const trimmed = text.trim();
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

const DOWN = "The tax service did not answer. Reload to try again.";

export type TaxAction = "load" | "rate" | "profile" | "config" | "capture" | "evidence" | "recompute" | "recon";

/** The words of the core's own codes in any sentence the core wrote; a person never reads a code. */
function plainCodes(text: string): string {
  return text
    .replace(/"?\b(STD_ON_HST_13|ON_FN_POS_8|FED_S87_[A-Z_]+|FED_BAND_MANAGEMENT|FNGST_5|FNT_PRODUCT|TREATY_OR_SELF_GOV_REVIEW|MANUAL_TAX_REVIEW|RELIEF_DECLINED_EVIDENCE)\b"?/g, (_all, code: string) => `"${reasonWord(code)}"`)
    .replace(/band-card/gi, "exemption certificate")
    .replace(/status card \(TCRD\)/gi, "certificate")
    .replace(/\bTCRD\b/g, "certificate")
    .replace(/\bHST\b/g, "sales tax")
    .replace(/\bavalara\/postal routing\b/gi, "automatic tax lookup and postal routing");
}

/**
 * A refusal in words. The role gate answers a walled route with its shared "Forbidden" body, which tells a person
 * nothing, so the screen names who may do it (reads are a manager's; the rate table, profiles, config and the
 * recompute are the Financial Controller's, an administrator tier; capturing evidence is any staff member's). The
 * core's own sentences are rewritten into what to do next, and the old words in them are swapped for ours;
 * anything unknown keeps the core's sentence in our words.
 */
export function refusalWords(action: TaxAction, message: string, status: number): string {
  const lower = message.toLowerCase();
  if (status === 403 && lower.includes("dealer's whole book")) {
    // The certificate book and the review queue hold no branch of their own: the whole dealer's, so a
    // branch holder is refused (615 round 2).
    return "The certificates are the whole dealer's book, so they are for someone who holds every branch. Ask the Financial Controller.";
  }
  if (status === 403 && lower.includes("cannot access this branch")) {
    // The reconcile covers every branch unless a branch is named, so a branch-scoped caller is
    // refused (FW1-10); the screen itself asks for the whole company, so the way out is the
    // controller's, not a branch choice the screen does not offer.
    return "This reconciliation covers every branch, so it is for someone who holds every branch. Ask the Financial Controller.";
  }
  if (status === 403) {
    switch (action) {
      case "rate":
        return "Changing the rate table is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      case "profile":
        return "Changing an exemption profile is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      case "config":
        return "Changing the tax settings is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      case "recompute":
        return "Recomputing past sales is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      case "capture":
        return "Recording a certificate is for signed in staff. Sign in again and retry.";
      case "evidence":
        return "Opening a certificate record is for a manager or the Financial Controller. Sign out and in again as one of them if this was not the role you meant.";
      default:
        return "Tax administration is for a manager or the Financial Controller.";
    }
  }
  if (status === 401) return "Your sign in has ended. Sign in again and retry.";
  if (lower.includes("is misconfigured against the gate map")) {
    const apart = /resolves (\d+) bps, below a standard of (\d+) bps/.exec(message);
    if (apart !== null) {
      return `Not added: this reason may not charge less than the standard rate (${formatRate(Number(apart[2]))}) unless it needs a certificate or proof of delivery. Its rate was ${formatRate(Number(apart[1]))}. Raise it, or choose a reason that needs evidence.`;
    }
    return "Not added: this reason may not charge less than the standard rate unless it needs a certificate or proof of delivery.";
  }
  if (lower.includes("not one of the lawful reason codes")) return "Not added: that reason is not one the rate table can carry. Choose one from the list.";
  if (lower.includes("rate_bps must be 0..10000")) return "The rate must be between 0 and 100 percent.";
  if (lower.includes("effective_from must be yyyy-mm-dd")) return "The first day must be a real day, written year, month, day with dashes.";
  if (lower === "reason_code required") return "Choose a reason first.";
  if (lower === "code required") return "Give the profile a code first.";
  if (lower.includes("a relief profile must force")) {
    return "Not saved: this profile must require the certificate number. Tick Certificate number and save again.";
  }
  if (lower.includes("is required for this profile")) {
    const what = /^(band-card photo|band-card number|attestation) is required/i.exec(message)?.[1]?.toLowerCase();
    const word = what === "band-card photo" ? "photo of the certificate" : what === "band-card number" ? "certificate number" : "signed statement";
    return `Not recorded: this profile requires the ${word}.`;
  }
  if (lower.includes("unknown card_profile_code")) return "Not recorded: that profile is not on file. Reload the profiles and choose one.";
  if (lower.includes("round_mode must be")) return "Choose how the tax is rounded: once on the invoice total, or on each line.";
  if (lower.includes("dormant at w6")) return "Automatic tax lookup and postal routing are not available yet, so they cannot be turned on.";
  if (lower === "evidence not found") return "That certificate record is not there any more. Reload the list.";
  if (lower === "year required") return "Give the year to reconcile.";
  if (lower === "date must be yyyy-mm-dd") return "The date must be a real day, written year, month, day with dashes.";
  if (status === 404 || lower === "not found") return "Not found. Reload to see what is there now.";
  const words = sentence(plainCodes(message));
  if (status >= 500 || words === "" || lower === "internal error") return DOWN;
  return words;
}
