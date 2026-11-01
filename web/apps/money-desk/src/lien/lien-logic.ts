// The liens slice's plain logic: the words for every state the core stores, how the core's two list shapes are
// read, how the new job form becomes the body the core takes, how a merge form becomes document data, who may
// release a holdback, and how a refusal reads out loud. Nothing here computes money and nothing here is rendered.
//
// Port provenance: the state words are the original app's (app/src/lien-shared.ts) with the stored tokens opened
// out and the statutory phrase kept ("Substantially performed" starts a clock; "Performed" would promise more).
// The merge fields are the core's own template keys (core/internal/lien/template.go); the original asked for them
// as a line of JSON, which a bookkeeper should not have to write. The co-signer was a window.prompt for a user id
// and a typed role; it is a pick from the roster here, and the core resolves the co-signer's authority itself.
import type { BillingHoldbackDueRow, LienJob, LienJobInput, LienPreservationDatesInput } from "@hardscapehouse/api-client";
import { dueWords } from "../compliance/compliance-logic.js";
import { formatMoney, parseAmountToCents, parsePercentToBps } from "./lien-money.js";

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
const WRITERS = ["financial_controller", "general_manager"];

/**
 * Who may write here: the two roles the default role configuration gives manage on the Money Desk and Manager Home.
 * Payables and Receivables holds work on both, which reads the liens and the calendar. The core's role gate enforces
 * it again; the app shows it before a press.
 */
export function canWrite(roleCode: string): boolean {
  return WRITERS.includes(roleCode.toLowerCase());
}

/** The line a role that only reads is shown where the write controls would be. */
export const READ_ONLY_LIENS = "Payables and Receivables reads the liens here. Creating a job, syncing holdbacks and rendering a document are for the Financial Controller or the General Manager.";

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

// The core's job level release and its move refusal name routes and use one verb for two acts. The screen keeps the
// core's sentences and changes only those words.
const JOB_LEVEL_ROUTE = "by the Controller's holdback release (POST /api/v1/ar/holdback/releases)";

/** Whether the core refused the old job level release because the holdback is held on invoices. */
export function jobLevelRefusedWords(message: string): boolean {
  return message.includes(JOB_LEVEL_ROUTE);
}

function rewritten(message: string): string {
  return message
    .replace(JOB_LEVEL_ROUTE, "from Due to release by the Financial Controller")
    .replace("and not by this job level release", "and not here")
    .replace("cannot be unlinked from its customer job", "cannot be moved or unlinked from its customer job");
}

/**
 * A refusal in words. The role gate answers a walled route with its shared "Forbidden" body, which tells a person
 * nothing, so the app names who may do it. Every other refusal keeps the core's own sentence, cleaned of its
 * module prefix; the gatekeeper refusal gets the way out beside it.
 */
export function refusalWords(action: string, message: string, status: number): string {
  const lower = message.toLowerCase().trim();
  const generic = lower === "" || lower === "forbidden";
  if (action === "due-release" && status === 403 && generic) return "Releasing a due holdback is for the Financial Controller.";
  if (status === 403 && generic) {
    return action === "release"
      ? `Releasing a holdback is for ${WHO_RELEASES}.`
      : "That is for the Financial Controller or the General Manager.";
  }
  if (generic && status === 0) return "The liens did not answer. Reload to try again.";
  const words = sentence(rewritten(message));
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


// ---- RT-3b: due to release ----------------------------------------------------------------------------

/** Who releases a due holdback: the core's own answer is the Financial Controller (RoleCodeFinancialController). */
export function canReleaseDue(roleCode: string): boolean {
  return roleCode.toLowerCase() === "financial_controller";
}

/** One customer job's held invoices with the one judgement the core gave them (the core judges per customer job). */
export interface DueGroup {
  readonly customerJobId: string;
  readonly branchId: string;
  readonly rows: readonly BillingHoldbackDueRow[];
  readonly label: string;
  readonly lienJobNo: string;
  readonly due: boolean;
  readonly kind: string;
  readonly reason: string;
  readonly releasableOn: string;
  readonly lastDay: string;
}

/** The rows grouped by customer job, in the order the core sent them. No figure is added up. */
export function groupDue(rows: readonly BillingHoldbackDueRow[]): DueGroup[] {
  const order: string[] = [];
  const by = new Map<string, BillingHoldbackDueRow[]>();
  for (const row of rows) {
    const held = by.get(row.customer_job_id);
    if (held === undefined) {
      order.push(row.customer_job_id);
      by.set(row.customer_job_id, [row]);
    } else {
      held.push(row);
    }
  }
  return order.map((id) => {
    const mine = by.get(id) ?? [];
    const first = mine[0] as BillingHoldbackDueRow;
    const lienJobNo = first.lien_job_no ?? "";
    return {
      customerJobId: id,
      branchId: first.branch_id,
      rows: mine,
      label: lienJobNo !== "" ? `lien job ${lienJobNo}` : `the customer job with ${mine.map((r) => r.invoice_no).join(", ")}`,
      lienJobNo,
      due: first.due,
      kind: first.blocked_kind ?? "",
      reason: first.blocked_reason ?? "",
      releasableOn: first.releasable_on ?? "",
      lastDay: first.last_day_to_preserve ?? "",
    };
  });
}

/** The state of a customer job's holdback, in words a bookkeeper can act on. */
export function dueStateWords(g: DueGroup): string {
  if (g.due) return "Due now";
  switch (g.kind) {
    case "NOT_DUE":
      return `Not due until ${dueWords(g.releasableOn)}`;
    case "LIEN_PRESERVED":
      return `Blocked by a preserved lien on ${g.lienJobNo}`;
    case "NO_DATE":
      return "No date to count from";
    case "NO_LIEN_JOB":
      return "No lien job linked";
    default:
      return "Not due";
  }
}

export function dueTone(g: DueGroup): Tone {
  if (g.due) return "good";
  return g.kind === "NOT_DUE" ? "watch" : "stop";
}

/** The question the Controller is asked first: the job, each invoice and its amount, and what the press does. */
export function dueConfirmWords(g: DueGroup): string {
  const invoices = g.rows.map((r) => `${r.invoice_no} (${formatMoney(r.holdback_cents)})`).join(", ");
  return `Release the holdback held on ${g.label}: ${invoices}. The whole holdback of the job is released at once and a journal is posted to the ledger.`;
}

export function releasedDueWords(label: string, total: string, invoices: number, replayed: boolean): string {
  const count = `${invoices} ${invoices === 1 ? "invoice" : "invoices"}`;
  return replayed ? `That release was already made: ${total} on ${label}, ${count}.` : `Released ${total} on ${label}: ${count}.`;
}

export function reversedWords(invoiceNo: string, restored: string): string {
  return `Reversed the release of ${invoiceNo}: ${restored} is held back again.`;
}

export function reverseForm(reason: string): { readonly ok: true; readonly reason: string } | { readonly ok: false; readonly words: string } {
  const text = reason.trim();
  return text === "" ? { ok: false, words: "Say why the release is reversed." } : { ok: true, reason: text };
}

// ---- RT-3b: the lien doors ----------------------------------------------------------------------------

export type DoorResult<T> = { readonly ok: true; readonly body: T } | { readonly ok: false; readonly words: string };

export function preservedForm(day: string, note: string): DoorResult<{ preserved_on: string; note: string }> {
  const text = day.trim();
  if (!validDay(text)) return { ok: false, words: "Give the day the lien was preserved, written yyyy-mm-dd." };
  return { ok: true, body: { preserved_on: text, note: note.trim() } };
}

export function dischargeForm(day: string, note: string): DoorResult<{ discharged_on: string; note: string }> {
  const text = day.trim();
  if (!validDay(text)) return { ok: false, words: "Give the day the lien was discharged, written yyyy-mm-dd." };
  return { ok: true, body: { discharged_on: text, note: note.trim() } };
}

/**
 * The three dates as typed, against the job's own. Only a changed date is sent; a box emptied that held a date
 * clears it (null); a box that held none and is still empty is left alone.
 */
export function datesForm(
  job: Pick<LienJob, "csp_published_on" | "substantial_perf_date" | "last_supply_date">,
  typed: { readonly csp: string; readonly perf: string; readonly supply: string },
): DoorResult<LienPreservationDatesInput> {
  const body: LienPreservationDatesInput = {};
  const fields = [
    ["csp_published_on", job.csp_published_on, typed.csp],
    ["substantial_perf_date", job.substantial_perf_date, typed.perf],
    ["last_supply_date", job.last_supply_date, typed.supply],
  ] as const;
  for (const [name, held, raw] of fields) {
    const text = raw.trim();
    if (text !== "" && !validDay(text)) return { ok: false, words: "Dates are written yyyy-mm-dd." };
    if (text === (held ?? "")) continue;
    body[name] = text === "" ? null : text;
  }
  if (Object.keys(body).length === 0) return { ok: false, words: "Change at least one date." };
  return { ok: true, body };
}

export function recordedPreservedWords(jobNo: string): string {
  return `Recorded: a lien was preserved on lien job ${jobNo}.`;
}

export function recordedDischargeWords(jobNo: string): string {
  return `Recorded: the lien on lien job ${jobNo} was discharged.`;
}

export function savedDatesWords(jobNo: string): string {
  return `Saved the dates of lien job ${jobNo}.`;
}
