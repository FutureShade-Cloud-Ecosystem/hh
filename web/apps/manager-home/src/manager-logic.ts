// Manager Home's decisions, with no DOM in sight: what each widget shape
// shows, which needs row opens which view, the words a walled refusal
// becomes, and the one formatting rule (money is integer cents from the
// core; this file formats it for a person and computes nothing).
//
// Port provenance: the widget vocabulary (severity and window words, the
// null-is-not-zero stat rule, the bars share note) is the original app's
// home bento (repository hardscapeos_dibbits, read only; see PROVENANCE.md),
// narrowed to the read-only landing this first slice builds.
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";
import type { NeedsRow, ReceivingRow, WidgetDatum, WidgetView } from "./manager-api.js";

// -- the words a state carries ----------------------------------------------------------------------

/** Severity words. State colour never travels alone: the word is the carrier. */
const SEVERITY_WORD: Readonly<Record<string, string>> = Object.freeze({
  good: "Routine",
  watch: "Watch",
  stop: "Urgent",
});

export function severityWord(severity: string): string {
  return SEVERITY_WORD[severity] ?? plainly(severity);
}

/** Named windows, the widget's own caption words. */
const RANGE_WORD: Readonly<Record<string, string>> = Object.freeze({
  today: "Today",
  wtd: "This week",
  mtd: "This month",
  qtd: "This quarter",
  ytd: "This year",
  last_7: "Last 7 days",
  last_14: "Last 14 days",
  last_30: "Last 30 days",
  last_90: "Last 90 days",
  open: "Everything open",
  all_time: "All time",
  now: "Right now",
});

export function rangeWord(range: string): string {
  return RANGE_WORD[range] ?? plainly(range);
}

/** The plain fallback for any unknown closed-set value: lowercased, separators opened out. */
function plainly(value: string): string {
  return value.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
}

/**
 * The one figure a stat, gauge or bars widget shows, formatted by the kind the core named.
 *
 * NULL IS NOT ZERO. A datum that is unwired, errored, or simply absent answers null and the card
 * renders its empty state; only state "ok" with a real total is a figure. There is no ?? 0 here.
 */
export function figureFor(datum: WidgetDatum | undefined, kind: string): string | null {
  if (datum === undefined || datum.state !== "ok" || datum.total === null) return null;
  if (kind === "money_cents") return formatDollars(datum.total);
  if (kind === "days") return datum.total === 1 ? "1 day" : `${grouped(datum.total)} days`;
  if (kind === "basis_points") {
    const magnitude = Math.abs(datum.total);
    const whole = Math.trunc(magnitude / 100);
    const frac = magnitude % 100;
    const digits = frac === 0 ? "" : `.${String(frac).padStart(2, "0")}`;
    return `${datum.total < 0 ? "-" : ""}${whole}${digits}%`;
  }
  return grouped(datum.total);
}

/** Grouped integer: 1234 becomes "1,234". A count a person reads, never a float. */
export function grouped(value: number): string {
  const sign = value < 0 ? "-" : "";
  const digits = String(Math.trunc(Math.abs(value)));
  const out: string[] = [];
  for (let i = digits.length; i > 0; i -= 3) out.unshift(digits.slice(Math.max(0, i - 3), i));
  return sign + out.join(",");
}

/** The words under a card the server could not measure. Never a number, never silent. */
export function stateNote(datum: WidgetDatum | undefined, moduleName: string): string {
  if (datum === undefined) return `Not connected (${moduleName})`;
  if (datum.state === "unwired") return datum.note !== "" ? datum.note : `Not connected (${moduleName})`;
  if (datum.state === "error") return "Not measured right now. Try again in a moment.";
  return datum.note;
}

/** The share of a total a bars row carries, as the note under the bar. */
export function shareNote(part: number, total: number): string {
  if (total <= 0 || part < 0) return "";
  const percent = Math.round((part / total) * 100);
  return `${percent}% of ${grouped(total)}`;
}

// -- the landing's views ---------------------------------------------------------------------------

/** The views this app owns. The needs links name them with these words. */
export type ViewKind = "home" | "shortages" | "receiving" | "approvals" | "drivers" | "warranty";

export const VIEW_TITLE: Readonly<Record<ViewKind, string>> = Object.freeze({
  home: "Home",
  shortages: "Shortages · open",
  receiving: "Receiving · short",
  approvals: "Approvals",
  drivers: "Drivers",
  warranty: "Warranty",
});

/**
 * Where one needs row goes. The two yard manager rows name views nothing else serves
 * (MANAGER-HOME-UI-NOTES): Shortages · open and Receiving · short are THIS app's views, and
 * receiving differences open Receiving, not Yard Pick. Approvals waiting opens this app's
 * approvals view. Any other row names a surface this box does not serve yet: the row shows its
 * count and its words, and goes nowhere, said plainly rather than linked to nothing.
 */
export function needsRowTarget(row: NeedsRow): { view: ViewKind } | { view: null; screen: string } {
  if (row.source_key === "tasks.open_shortages") return { view: "shortages" };
  if (row.source_key === "purchasing.receiving_differences") return { view: "receiving" };
  if (row.source_key === "approvals.waiting") return { view: "approvals" };
  return { view: null, screen: row.view !== "" ? row.view : row.screen };
}

// -- the rows the board drew that nothing measures ---------------------------------------------------

/**
 * The board's needs rows with no wired source. Said plainly, never faked: each renders its words
 * and an explicit no-source sentence, never a number (the 0201 catalog has no measure behind
 * them; MANAGER-HOME-UI-NOTES names Weights pending).
 */
export const UNSOURCED_NEEDS: readonly { readonly key: string; readonly what: string; readonly words: string }[] = Object.freeze([
  { key: "weights.pending", what: "Weights pending", words: "No source is wired for this row yet, so it is not counted here." },
]);

/** The board's widget tiles with no measure in the widget catalog. Same rule, said plainly. */
export const UNSOURCED_TILES: readonly { readonly title: string; readonly words: string }[] = Object.freeze([
  { title: "Unread messages", words: "No measure is wired for this tile in the widget catalog yet, so it shows no number." },
]);

// -- shortage resolves -------------------------------------------------------------------------------

/** The resolution choices the resolve sheet offers, each a full sentence the core records. */
export const RESOLUTION_CHOICES: readonly {
  readonly key: string;
  readonly label: string;
  readonly sentence: string;
  /** The one line consequence, the cost of a wrong click (the board's own words). */
  readonly consequence: string;
}[] = Object.freeze([
  {
    key: "backorder",
    label: "Backorder the shortfall",
    sentence: "Backordered the shortfall; the order keeps the short line open.",
    consequence: "The line is backordered and the order's rep is notified.",
  },
  {
    key: "substitute",
    label: "Substitute from stock",
    sentence: "Substituted from stock at this branch.",
    consequence: "Pick the swap line before the truck leaves.",
  },
  {
    key: "approve",
    label: "Approve the short pick",
    sentence: "Approved the short pick; the order goes out short.",
    consequence: "The order ships as picked; the shortfall is written off.",
  },
]);

/** The note may be empty; the choice must be one of the three. The server caps the text at 400. */
export function canResolve(choice: string, note: string): boolean {
  if (!RESOLUTION_CHOICES.some((c) => c.key === choice)) return false;
  return resolutionText(choice, note) !== "";
}

/** The sentence the core records: the chosen words, with the note appended when there is one. */
export function resolutionText(choice: string, note: string): string {
  const found = RESOLUTION_CHOICES.find((c) => c.key === choice);
  if (found === undefined) return "";
  const trimmed = note.trim().slice(0, 200);
  return trimmed === "" ? found.sentence : `${found.sentence} ${trimmed}`;
}

/**
 * Who may resolve a shortage, as the matrix names the list (shortage_resolvers: the yard team
 * and the yard manager). The words the app shows beside the resolve sheet, and the words the
 * shared Forbidden body becomes when the core refuses a caller the list does not name.
 */
export const SHORTAGE_RESOLVER_WORDS = "Shortages are resolved by the yard team and the yard manager.";

/** Map a resolve refusal to plain words naming who may act (FROM-PRINCIPAL 39). */
export function resolveRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return `You may not resolve this shortage. ${SHORTAGE_RESOLVER_WORDS}`;
  }
  return error !== "" ? error : "That did not go through. Try again in a moment.";
}

// -- the words only zone (the round 1 review's P2-3) ------------------------------------------------
//
// No raw enum, no raw wire decimal, no raw ISO day reaches a screen: every
// system value renders as the words a person reads, and anything this file
// cannot say honestly is omitted by the view, never shown raw.

/** A wire decimal ("4.0000") as a person reads it ("4"); "" stays "". */
export function qtyWords(qty: string): string {
  const trimmed = qty.trim();
  if (trimmed === "" || !/^\d+(\.\d+)?$/.test(trimmed)) return trimmed;
  if (!trimmed.includes(".")) return trimmed;
  const cut = trimmed.replace(/0+$/, "").replace(/\.$/, "");
  return cut === "" ? "0" : cut;
}

/**
 * Picked is ordered less short, computed only when both are honest decimals and
 * the short does not exceed the ordered. Anything else answers null and the
 * view omits the line: never a confident wrong number.
 */
export function pickedWords(ordered: string, short: string): string | null {
  if (!/^\d+(\.\d+)?$/.test(ordered.trim()) || !/^\d+(\.\d+)?$/.test(short.trim())) return null;
  const a = Number.parseFloat(ordered);
  const b = Number.parseFloat(short);
  if (!Number.isFinite(a) || !Number.isFinite(b) || b > a) return null;
  return qtyWords((a - b).toFixed(4));
}

/** Task statuses in plain words; an unknown one is omitted, never shown raw. */
const TASK_STATUS_WORD: Readonly<Record<string, string>> = Object.freeze({
  QUEUED: "Waiting",
  PUSHED: "Sent",
  ACCEPTED: "Claimed",
  IN_PROGRESS: "In progress",
  STAGED: "Staged",
  DONE: "Done",
  CANCELLED: "Cancelled",
});

export function taskStatusWord(status: string): string {
  return TASK_STATUS_WORD[status] ?? "";
}

/** Month words for an expected day the server sends as YYYY-MM-DD. */
const MONTH_WORD = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"] as const;

/** An ISO day as words ("2026-10-14" becomes "Oct 14"); unparsable answers "". */
export function dateWords(day: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(day.trim());
  if (m === null) return "";
  const month = Number.parseInt(m[2], 10);
  const date = Number.parseInt(m[3], 10);
  if (month < 1 || month > 12 || date < 1 || date > 31) return "";
  return `${MONTH_WORD[month - 1]} ${date}`;
}

/**
 * A server error a person reads. A one word token (a status word, an enum) is
 * not a sentence; it becomes plain words. A sentence stays the server's own.
 */
export function errorWords(error: string): string {
  const trimmed = error.trim();
  if (trimmed === "" || !trimmed.includes(" ")) return "Try again in a moment.";
  return trimmed;
}

/** The board's welcome line, the caller's own role label in it. */
export function welcomeLine(roleLabel: string): string {
  if (roleLabel === "") return "Welcome back.";
  return `Welcome back, ${roleLabel.toLowerCase()}.`;
}

// -- approvals by kind --------------------------------------------------------------------------------

/** The closed kind vocabulary, in the core's own order, with the words a person reads. */
export const APPROVAL_KINDS: readonly { readonly kind: string; readonly word: string }[] = Object.freeze([
  { kind: "MARGIN_BELOW_MINIMUM", word: "Margin" },
  { kind: "CREDIT_LIMIT_EXCEEDED", word: "Credit" },
  { kind: "COD_CONSTRAINT", word: "Cash On Demand" },
  { kind: "JE_OVER_THRESHOLD", word: "Journal entry" },
  { kind: "FREIGHT_ABOVE_QUOTE", word: "Freight above quote" },
]);

export function kindWord(kind: string): string {
  return APPROVAL_KINDS.find((k) => k.kind === kind)?.word ?? plainly(kind);
}

/**
 * Who decides each kind of approval, as the matrix names the lists (exception_approvers, with
 * the own-branch floor clearers on margin). The words the approvals view carries when the core
 * refuses a kind this caller does not decide.
 */
export const APPROVAL_DECIDER_WORDS: Readonly<Record<string, string>> = Object.freeze({
  MARGIN_BELOW_MINIMUM:
    "Margin approvals are decided by the financial controller, the general manager, and a sales manager or branch manager in their own branch.",
  CREDIT_LIMIT_EXCEEDED: "Credit approvals are decided by the financial controller and the general manager.",
  COD_CONSTRAINT: "Cash On Demand approvals are decided by the financial controller and the general manager.",
  JE_OVER_THRESHOLD: "Journal entry approvals are decided by the financial controller and the general manager.",
  FREIGHT_ABOVE_QUOTE: "Freight approvals are decided by the financial controller and the general manager.",
});

/** Map a kind refusal to plain words naming who decides it: no enum on screen, ever. */
export function approvalsRefusalWords(kind: string, error: string, status: number | undefined): string {
  if (status === 403) {
    const shown = kindWord(kind);
    // A name with its own capitals (Cash On Demand) keeps them; a plain one reads lower case mid sentence.
    const word = /[A-Z]/.test(shown.slice(1)) ? shown : shown.toLowerCase();
    const who = APPROVAL_DECIDER_WORDS[kind] ?? "Approvals are decided by the financial controller and the general manager.";
    return `You do not decide ${word} approvals. ${who}`;
  }
  return error !== "" ? error : "The approvals list did not load. Try again in a moment.";
}

/** A decide refusal, the same plain words for the row's own kind. */
export function decideRefusalWords(kind: string, error: string, status: number | undefined): string {
  if (status === 403) return approvalsRefusalWords(kind, error, status);
  return `The decision did not go through. ${errorWords(error)}`;
}

// -- receiving -----------------------------------------------------------------------------------------

/**
 * The receiving difference definition in words (decision log row 1245, ruled in the purchasing
 * module's code): what counts as a difference, and when it stops being one.
 */
export const RECEIVING_DIFFERENCE_WORDS =
  "A receiving difference is a purchase order that landed part of what was ordered and is still open: at least one line is short. It leaves this list when the rest lands or the order is closed short.";

/** The worklist rows that are differences: the status the count source itself keys on. */
export function isReceivingDifference(row: ReceivingRow): boolean {
  return row.status === "PARTIAL";
}

// -- the grid ------------------------------------------------------------------------------------------

/**
 * The grid footprint a widget takes: the server's span (its free span or its preset's), never
 * recomputed here. Twelve columns at the desktop breakpoint, six at tablet and phone.
 */
export function gridSpan(w: WidgetView): { cols: number; rows: number } {
  return { cols: w.span_cols, rows: w.span_rows };
}
