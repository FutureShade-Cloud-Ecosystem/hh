// The liens slice's plain logic: the words for every state the core stores, how the core's two list shapes are
// read, how the new job form becomes the body the core takes, how a merge form becomes document data, who may
// release a holdback, and how a refusal reads out loud. Nothing here computes money and nothing here is rendered.
//
// Port provenance: the state words are the original app's (app/src/lien-shared.ts) with the stored tokens opened
// out and the statutory phrase kept ("Substantially performed" starts a clock; "Performed" would promise more).
// The merge fields are the core's own template keys (core/internal/lien/template.go); the original asked for them
// as a line of JSON, which a bookkeeper should not have to write. The co-signer was a window.prompt for a user id
// and a typed role; it is a pick from the roster here, and the core resolves the co-signer's authority itself.
import type { LienJobInput } from "@hardscapehouse/api-client";
import { parseAmountToCents, parsePercentToBps } from "./lien-money.js";

export type Tone = "stop" | "watch" | "good" | "plain";

function opened(value: string): string {
  return value.toLowerCase().replace(/_/g, " ");
}

function wordsFrom(table: Readonly<Record<string, string>>, value: string): string {
  return table[value] ?? opened(value);
}

/**
 * The core answers a list as the contract's page ({items, next_cursor}). An empty list is the one place it strays
 * from the contract: the Go side leaves the slice nil, so the page says {"items": null}. That is an empty list, not
 * an unreadable answer. Anything else that is not a list is not one.
 */
export function listOf<T>(value: unknown): T[] | null {
  if (Array.isArray(value)) return value as T[];
  if (value !== null && typeof value === "object" && "items" in value) {
    const items = (value as { items?: unknown }).items;
    if (Array.isArray(items)) return items as T[];
    if (items === null) return [];
  }
  return null;
}

const JOB_STATUS: Record<string, string> = {
  OPEN: "Open",
  SUBSTANTIALLY_PERFORMED: "Substantially performed",
  RELEASED: "Released",
  CLOSED: "Closed",
};
export const jobStatusWord = (status: string): string => wordsFrom(JOB_STATUS, status);

export function jobStatusTone(status: string): Tone {
  if (status === "RELEASED") return "good";
  if (status === "SUBSTANTIALLY_PERFORMED") return "watch";
  return "plain";
}

const DIRECTION: Record<string, string> = { RECEIVABLE: "Receivable", PAYABLE: "Payable" };
export const directionWord = (direction: string): string => wordsFrom(DIRECTION, direction);

const DIRECTION_LINE: Record<string, string> = {
  RECEIVABLE: "We hold the customer's retention; it can be released.",
  PAYABLE: "We owe a subcontractor's retention; it is recorded here and released by hand.",
};
export const directionLine = (direction: string): string => DIRECTION_LINE[direction] ?? "";

const BADGE: Record<string, string> = {
  RELEASE_DUE: "Release due",
  OVERDUE: "Overdue",
  AGING: "Aging",
  PAYABLE_MANUAL: "Payable, released by hand",
  OK: "On track",
};
export const badgeWord = (badge: string): string => wordsFrom(BADGE, badge);

export function badgeTone(badge: string): Tone {
  if (badge === "OVERDUE") return "stop";
  if (badge === "RELEASE_DUE" || badge === "PAYABLE_MANUAL") return "watch";
  if (badge === "OK") return "good";
  return "plain";
}

const BUCKET: Record<string, string> = { "0-30": "0 to 30 days", "31-60": "31 to 60 days", "61-90": "61 to 90 days", "90+": "Over 90 days" };
export const bucketWord = (bucket: string): string => BUCKET[bucket] ?? bucket;

export function ageWords(days: number): string {
  return `${days} ${days === 1 ? "day" : "days"}`;
}

const DOCUMENT_STATUS: Record<string, string> = { DRAFT: "Draft", PENDING: "Pending", ISSUED: "Issued", SUPERSEDED: "Superseded" };
export const documentStatusWord = (status: string): string => wordsFrom(DOCUMENT_STATUS, status);

export function documentTone(status: string): Tone {
  if (status === "ISSUED") return "good";
  if (status === "DRAFT" || status === "PENDING") return "watch";
  return "plain";
}

// ---- the document forms ----------------------------------------------------------------------------

export const TEMPLATE_KINDS = ["CONDITIONAL_RELEASE", "UNCONDITIONAL_RELEASE", "STATUTORY_DECLARATION", "DISCHARGE", "NOTICE", "OTHER"] as const;
export type TemplateKind = (typeof TEMPLATE_KINDS)[number];

const TEMPLATE_WORD: Record<string, string> = {
  CONDITIONAL_RELEASE: "Conditional release",
  UNCONDITIONAL_RELEASE: "Unconditional release",
  STATUTORY_DECLARATION: "Statutory declaration",
  DISCHARGE: "Discharge",
  NOTICE: "Notice",
  OTHER: "Other document",
};
export const templateWord = (kind: string): string => wordsFrom(TEMPLATE_WORD, kind);

export interface MergeField {
  readonly key: string;
  readonly label: string;
}

// The core renders each form from these keys and prints a blank where one is missing. The labels are the core's.
const FIELDS: Record<string, readonly MergeField[]> = {
  CONDITIONAL_RELEASE: [
    { key: "job_no", label: "Job / Improvement" },
    { key: "owner", label: "Owner" },
    { key: "claimant", label: "Contractor / Claimant" },
    { key: "payment_amount", label: "Amount of this payment" },
    { key: "holdback_amount", label: "Holdback retained" },
    { key: "condition", label: "Condition" },
  ],
  UNCONDITIONAL_RELEASE: [
    { key: "job_no", label: "Job / Improvement" },
    { key: "owner", label: "Owner" },
    { key: "claimant", label: "Contractor / Claimant" },
    { key: "payment_amount", label: "Amount received in full" },
  ],
  STATUTORY_DECLARATION: [
    { key: "job_no", label: "Job / Improvement" },
    { key: "declarant", label: "Declarant" },
    { key: "capacity", label: "Capacity" },
    { key: "accounts_paid", label: "All accounts for labour/services/materials paid" },
    { key: "exceptions", label: "Exceptions" },
  ],
  DISCHARGE: [
    { key: "job_no", label: "Job / Improvement" },
    { key: "lien_claim_no", label: "Lien claim number" },
    { key: "claimant", label: "Claimant" },
    { key: "discharged_amount", label: "Discharged amount" },
  ],
  NOTICE: [
    { key: "job_no", label: "Job / Improvement" },
    { key: "recipient", label: "To" },
    { key: "subject", label: "Subject" },
    { key: "body", label: "Body" },
  ],
  OTHER: [
    { key: "job_no", label: "Job / Improvement" },
    { key: "body", label: "Body" },
  ],
};

export function templateFields(kind: string): readonly MergeField[] {
  return FIELDS[kind] ?? [];
}

/** What the person filled in, trimmed, and nothing for a blank: the core prints its own blank for a missing key. */
export function mergeData(entries: ReadonlyArray<{ key: string; value: string }>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const { key, value } of entries) {
    const text = value.trim();
    if (text !== "") out[key] = text;
  }
  return out;
}

// ---- the new job form --------------------------------------------------------------------------------

export interface JobFormValues {
  readonly job_no: string;
  readonly branch_id: string;
  readonly description: string;
  readonly contract: string;
  readonly percent: string;
  readonly direction: string;
  readonly perf: string;
  readonly supply: string;
}

export type JobFormResult = { readonly ok: true; readonly body: LienJobInput } | { readonly ok: false; readonly field: string; readonly words: string };

/** A real calendar day written year, month, day. A blank is fine: the date is optional. */
function validDay(text: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text);
  if (m === null) return false;
  const [year, month, day] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const at = new Date(Date.UTC(year, month - 1, day));
  return at.getUTCFullYear() === year && at.getUTCMonth() === month - 1 && at.getUTCDate() === day;
}

export function jobForm(values: JobFormValues): JobFormResult {
  const jobNo = values.job_no.trim();
  if (jobNo === "") return { ok: false, field: "job_no", words: "Give the job a number." };
  if (values.branch_id.trim() === "") return { ok: false, field: "branch_id", words: "Pick the branch the job belongs to." };
  const contractText = values.contract.trim();
  const contract = contractText === "" ? 0 : parseAmountToCents(contractText);
  if (contract === null) return { ok: false, field: "contract", words: "The contract amount is dollars and cents, like 125,000.00." };
  const bps = parsePercentToBps(values.percent);
  if (bps === null) {
    return { ok: false, field: "percent", words: "The holdback percent is a number from 0.01 to 100, like 10 or 7.5. Leave it blank for the core's default." };
  }
  for (const [field, text] of [["perf", values.perf.trim()], ["supply", values.supply.trim()]] as const) {
    if (text !== "" && !validDay(text)) return { ok: false, field, words: "Dates are written yyyy-mm-dd." };
  }
  return {
    ok: true,
    body: {
      job_no: jobNo,
      customer_id: "",
      branch_id: values.branch_id.trim(),
      description: values.description.trim(),
      contract_cents: contract,
      holdback_pct_bps: bps,
      substantial_perf_date: values.perf.trim(),
      last_supply_date: values.supply.trim(),
      holdback_direction: values.direction === "PAYABLE" ? "PAYABLE" : "RECEIVABLE",
      // The core's tax on holdback flag grosses the held amount up by a placeholder rate that is not California's,
      // so the form does not offer it: a job never asks the core to add tax to its retention.
      hst_on_holdback: false,
    },
  };
}

// ---- release ------------------------------------------------------------------------------------------

const RELEASER = "general_manager";

/** The core's gate: the gatekeeper is the General Manager. The app shows it before a press; the core enforces it again. */
export function canRelease(roleCode: string): boolean {
  return roleCode.toLowerCase() === RELEASER;
}

export function releaseConfirmWords(jobNo: string, amount: string): string {
  return `Release ${amount} held on job ${jobNo}. This posts a journal to the ledger.`;
}

const WHO_RELEASES = "the General Manager, co-signed by a Financial Controller or Payables and Receivables";

function sentence(message: string): string {
  const trimmed = message.replace(/^(lien|compliance): /, "").trim();
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

/**
 * A refusal in words. The role gate answers a walled route with its shared "Forbidden" body, which tells a person
 * nothing, so the app names who may do it. Every other refusal keeps the core's own sentence, cleaned of its
 * module prefix; the gatekeeper refusal gets the way out beside it.
 */
export function refusalWords(action: string, message: string, status: number): string {
  const lower = message.toLowerCase().trim();
  const generic = lower === "" || lower === "forbidden";
  if (status === 403 && generic) {
    return action === "release"
      ? `Releasing a holdback is for ${WHO_RELEASES}.`
      : "That is for the Financial Controller, Payables and Receivables or the General Manager.";
  }
  if (generic && status === 0) return "The liens did not answer. Reload to try again.";
  const words = sentence(message);
  if (words === "") return "The liens did not answer. Reload to try again.";
  if (action === "release" && status === 403 && lower.includes("gatekeeper must be")) return `${words} Releasing is the General Manager's, co-signed by a Financial Controller or Payables and Receivables.`;
  return words;
}

// ---- the sentences the desk says -----------------------------------------------------------------------

/** What a holdback sync did, in the core's own counts. */
export function syncWords(booked: number, bookedMoney: string, skipped: number): string {
  const already = skipped > 0 ? `${skipped} already booked` : "";
  if (booked === 0) return `Synced: nothing new to book${already === "" ? "" : `, ${already}`}.`;
  return `Synced: ${booked} ${booked === 1 ? "invoice" : "invoices"} booked for ${bookedMoney}${already === "" ? "" : `, ${already}`}.`;
}

export function releasedWords(amount: string, jobNo: string, cosigner: string): string {
  return `Released ${amount} on job ${jobNo}, co-signed by ${cosigner}.`;
}

export function renderedWords(version: number, form: string): string {
  return `Rendered version ${version} of ${form}.`;
}

/** What a job holds right now, from its aging row: the amount and the oldest retention's age, or nothing. */
export function heldNowWords(amount: string | null, oldestDays: number): string {
  return amount === null ? "Nothing held." : `${amount} held, oldest ${ageWords(oldestDays)}`;
}

export function holdingWords(count: number): string {
  if (count === 0) return "No job holds retention";
  return count === 1 ? "1 job holds retention" : `${count} jobs hold retention`;
}

/** Whether a holdback is ready to go: the core's own eligibility for a receivable, a manual release for a payable. */
export function releaseStatusWord(direction: string, eligible: boolean): string {
  if (direction === "PAYABLE") return "Released by hand";
  return eligible ? "Release due" : "Not yet";
}

/** The core's triage note is a lowercase clause; a row reads it as a sentence. */
export function noteWords(note: string): string {
  return sentence(note);
}

// ---- the co-signer -------------------------------------------------------------------------------------

const COSIGNER_ROLES = ["FINANCIAL_CONTROLLER", "PAYABLES_RECEIVABLES"];

/**
 * The people on the roster the core would take as a co-signer: a Financial Controller or Payables and Receivables,
 * and never the person releasing. The roster does not say who is still active, so the core is still the judge of
 * that after the pick.
 */
export function cosigners<T extends { user_id: string; role_code: string }>(roster: readonly T[], releasingId: string): T[] {
  return roster.filter((p) => COSIGNER_ROLES.includes(p.role_code.toUpperCase()) && p.user_id !== releasingId);
}
