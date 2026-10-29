// The ledger slice's plain logic: how the core's answers are read, what each
// approval kind is called, who may decide it, and how a refusal reads out
// loud. Nothing here computes money and nothing here is rendered.
import { stampDay } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { formatMoney } from "./gl-money.js";

/**
 * The core files a journal request with its debit total in cents inside the
 * sentence ("(750000 cents)"). A person reads dollars, so the figure is
 * formatted where it stands; the sentence is otherwise the server's own.
 */
export function detailWords(detail: string): string {
  return detail.replace(/\((\d+) cents\)/g, (_all, digits: string) => `(${formatMoney(Number(digits))})`);
}

/** The six facts of a floor request that the core's own sentence carries, read out, never computed. */
export interface FloorFigures {
  readonly quote: string;
  readonly line: string;
  readonly discount: string;
  readonly marginAsked: string;
  readonly floorMargin: string;
}

// The sentence is quote/floor.go RequestFloorApproval's: the quote number, the line, the product id, the
// asked discount, the resulting margin and the floor, the margins in basis points.
const FLOOR_SENTENCE = /^quote (\S+) line (\d+) \([^)]*\): (\d+) percent discount, resulting margin (-?\d+) bps against the (\d+) bps floor$/;

/** 1630 reads "16.3%": the digits of the basis points, a point put in, no sum. */
export function bpsWords(bps: string): string {
  const negative = bps.startsWith("-");
  const digits = (negative ? bps.slice(1) : bps).padStart(3, "0");
  return `${negative ? "-" : ""}${digits.slice(0, -2)}.${digits.slice(-2, -1)}%`;
}

/** Null when the sentence is not the floor request's, so the caller falls back to the core's own words. */
export function floorFigures(detail: string): FloorFigures | null {
  const m = FLOOR_SENTENCE.exec(detail.trim());
  if (m === null) return null;
  return { quote: m[1] ?? "", line: m[2] ?? "", discount: `${m[3]}%`, marginAsked: bpsWords(m[4] ?? ""), floorMargin: bpsWords(m[5] ?? "") };
}

/** The floor request as a person says it: the quote and line, the discount asked, the margins, no id and no "bps". */
export function floorWords(detail: string): string | null {
  const f = floorFigures(detail);
  if (f === null) return null;
  return `Quote ${f.quote}, line ${f.line}: a ${f.discount} discount asked, margin ${f.marginAsked} against the ${f.floorMargin} floor.`;
}

const KIND_WORD: Record<string, string> = {
  MARGIN_BELOW_MINIMUM: "Quote",
  JE_OVER_THRESHOLD: "Journal",
  FREIGHT_ABOVE_QUOTE: "Freight",
  CREDIT_LIMIT_EXCEEDED: "Credit",
  COD_CONSTRAINT: "COD",
};

const KIND_TITLE: Record<string, string> = {
  MARGIN_BELOW_MINIMUM: "Below floor quote request",
  JE_OVER_THRESHOLD: "Journal parked over the threshold",
  FREIGHT_ABOVE_QUOTE: "Freight above the quote",
  CREDIT_LIMIT_EXCEEDED: "Credit limit exception",
  COD_CONSTRAINT: "Cash on demand exception",
};

/** The short pill on a queue row. */
export function approvalKind(policy: string): string {
  return KIND_WORD[policy] ?? "Request";
}

/** The row's heading. */
export function approvalTitle(policy: string): string {
  return KIND_TITLE[policy] ?? "Approval request";
}

/** A request is the caller's to decide while it is pending and somebody else filed it (four eyes). */
export function canDecide(row: { requested_by: string; status: string }, userId: string): boolean {
  if (userId === "") return false;
  return row.status === "PENDING" && row.requested_by !== userId;
}

const JOURNAL_WORD: Record<string, string> = {
  PENDING_APPROVAL: "Pending approval",
  POSTED: "Posted",
  DRAFT: "Draft",
  VOID: "Void",
};

export function journalWord(derivedStatus: string): string {
  return JOURNAL_WORD[derivedStatus] ?? derivedStatus;
}

/** The tone only backs the word; the word always carries the meaning. */
export function journalTone(derivedStatus: string): "stop" | "watch" | "good" | "plain" {
  if (derivedStatus === "PENDING_APPROVAL") return "watch";
  if (derivedStatus === "POSTED") return "good";
  if (derivedStatus === "VOID") return "stop";
  return "plain";
}

const WHO_DECIDES = "the General Manager, or a Financial Controller who did not file it";

function sentence(text: string): string {
  // The core prefixes its sentences with the module ("gl: "); a person reads the sentence alone.
  const trimmed = text.trim().replace(/^gl:\s*/, "");
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

/** What each write is called in a refusal, and who the role gate lets through. */
const WRITES: Record<string, { what: string; who: string }> = {
  post: { what: "post a journal", who: "the Financial Controller or the General Manager" },
  "approve-journal": { what: "approve a journal", who: "the Financial Controller or the General Manager" },
  void: { what: "void a journal", who: "the Financial Controller or the General Manager" },
  reverse: { what: "reverse a journal", who: "the Financial Controller or the General Manager" },
  run: { what: "run a template", who: "the Financial Controller or the General Manager" },
  account: { what: "add an account", who: "the Financial Controller or the General Manager" },
  deactivate: { what: "deactivate an account", who: "the Financial Controller or the General Manager" },
  "close-period": { what: "close a period", who: "the Financial Controller or the General Manager" },
  reopen: { what: "reopen a period", who: "the Financial Controller" },
  "close-year": { what: "close the year", who: "the Financial Controller" },
  "open-year": { what: "open the next fiscal year", who: "the Financial Controller" },
  "legacy-import": { what: "read the legacy import check", who: "the Financial Controller" },
  export: { what: "take the export", who: "the Financial Controller" },
};

/** True when nothing says whether the write landed: no answer at all, or the server failed on its own. */
export function outcomeUnknown(status: number): boolean {
  return status === 0 || status === 500 || status === 502 || status === 504;
}

/** The words when a write's answer was lost, naming where to look before pressing again. */
export function unknownWords(action: string): string {
  const what = WRITES[action]?.what ?? "make that change";
  return `The answer to ${what} was lost, so it may or may not have been saved. Reload and look at the register before you press again.`;
}

// ---- the ledger's own guards (FW1-09a) -------------------------------------------------------------

/** The facts of one journal the panel needs to say who posted it. */
export interface SystemJournalFacts {
  readonly status: string;
  readonly source: string;
  readonly reverses_journal_id?: string;
  readonly idempotency_key?: string;
}

const SOURCE_OWNER: Record<string, string> = {
  INVOICE: "the invoicing module",
  PAYMENT: "the payments module",
  FULFILLMENT: "the fulfilment module",
  PO_RECEIPT: "the purchasing module",
  CLOSING: "the year end close",
};

// Several writers share ADJUSTMENT, so the idempotency key's prefix names the writer, as the core's own
// refusal does. Keep in step with the core's systemJournalOwner.
const KEY_OWNER: readonly (readonly [string, string])[] = [
  ["bankrec:", "the bank reconciliation module"],
  ["landedcost.", "the landed cost module"],
  ["holdback-release:", "the holdback module"],
  ["till.session.completed:", "the till module"],
];

/**
 * "" when a person posted the journal, so the ledger offers Reverse; otherwise the words the panel
 * carries instead: who posted it, and where its correction belongs. The server refuses those reversals
 * (FW1-09a), so the button is never offered where the answer would be a refusal.
 */
export function systemJournalNote(journal: SystemJournalFacts): string {
  if (journal.status !== "POSTED" || journal.source === "MANUAL") return "";
  if (journal.reverses_journal_id !== undefined && journal.reverses_journal_id !== "") {
    return "This journal is itself a reversal. A reversal cannot be reversed.";
  }
  let owner = "another module";
  const bySource = SOURCE_OWNER[journal.source];
  if (bySource !== undefined) {
    owner = bySource;
  } else {
    for (const [prefix, name] of KEY_OWNER) {
      if ((journal.idempotency_key ?? "").startsWith(prefix)) {
        owner = name;
        break;
      }
    }
  }
  return `Posted by ${owner}, not by a person here. To undo it, correct it where it came from; it cannot be reversed from the ledger.`;
}

/**
 * A refusal in words. The role gate answers a walled route with its shared
 * "Forbidden" body, which tells a person nothing, so the app names who may
 * do it. The core's separation of duties words (the requester never decides
 * their own request) get the way out beside them. A refusal that has a way
 * out gets it; any other keeps the core's own sentence, without its module
 * prefix.
 */
export function refusalWords(action: string, message: string, status: number): string {
  const lower = message.toLowerCase();
  if (action === "decide") {
    if (lower.includes("separation of duties") || lower.includes("four-eyes") || lower.includes("must differ")) {
      return `You filed this request, so you cannot decide it. Another approver decides it: ${WHO_DECIDES}.`;
    }
    if (status === 403) {
      return "Deciding a request is for the Financial Controller or the General Manager. Sign out and in again if this was not the role you meant.";
    }
    if (status === 409) return "Someone already decided this request. Reload to see it.";
  }
  const write = WRITES[action];
  if (write !== undefined) {
    if (outcomeUnknown(status)) return unknownWords(action);
    if (status === 403 && lower.includes("four-eyes")) {
      return "You made this journal, so you cannot approve it. Another approver posts it: the General Manager, or a Financial Controller who did not make it.";
    }
    if (status === 403) {
      return `To ${write.what} is for ${write.who}. Sign out and in again if this was not the role you meant.`;
    }
    if (lower.includes("unbalanced")) {
      return "Debits and credits are not equal, so nothing was saved. Match the two sides and post again.";
    }
    if (lower.includes("fiscal period is closed")) {
      return "That month is closed, so nothing was saved. The Financial Controller can reopen it with a reason.";
    }
    if (lower.includes("no fiscal period")) {
      return `${sentence(message)} Add the period before posting into it.`;
    }
    if (lower.includes("reason is required")) return "A reason is required. Say why, and try again.";
    // The ledger's own guards, in the core's words (FW1-09a), said the way a dealer reads them.
    if (lower.includes("is itself a reversal")) {
      return "This journal is itself a reversal, and a reversal cannot be reversed. Nothing was posted.";
    }
    const owner = lower.match(/was posted by (.+), not by a person in the ledger/);
    if (owner !== null) {
      return `This journal was posted by ${owner[1]}, not here. To undo it, correct it where it came from; nothing was reversed.`;
    }
    if (lower.includes("was already reversed")) {
      return "This journal was already reversed. A journal can be reversed once, so nothing was posted.";
    }
    const rules = lower.match(/the posting rules (.+?) need it/);
    if (rules !== null) {
      return `This account carries automated postings (${rules[1]}). Deactivating it would stop those postings, so nothing was changed.`;
    }
  }
  if (status === 403 && lower.includes("cannot access this branch")) {
    return "You cannot read the books of that branch. Choose a branch you hold.";
  }
  if (status === 403) {
    return "That is for the Financial Controller or the General Manager.";
  }
  const words = sentence(message);
  if (words === "") return "The ledger did not answer. Reload to try again.";
  return words;
}

// ---- the chart of accounts ------------------------------------------------------------------------

const TYPE_WORD: Record<string, string> = {
  ASSET: "Asset",
  LIABILITY: "Liability",
  EQUITY: "Equity",
  REVENUE: "Revenue",
  EXPENSE: "Expense",
};

/** ASSET reads "Asset". */
export function accountTypeWord(type: string): string {
  return TYPE_WORD[type] ?? type;
}

/** The normal balance in words: DEBIT reads "Debit". */
export function sideWord(side: string): string {
  return side === "DEBIT" ? "Debit" : side === "CREDIT" ? "Credit" : side;
}

/** The type a new account's normal side follows, as the chart sets it: assets and expenses sit on the debit side. */
export function suggestedSide(type: string): "DEBIT" | "CREDIT" {
  return type === "ASSET" || type === "EXPENSE" ? "DEBIT" : "CREDIT";
}

// ---- the journal composer ------------------------------------------------------------------------

/** One typed line of a new journal, as the form holds it. */
export interface DraftLine {
  readonly account: string;
  readonly debit: string;
  readonly credit: string;
  readonly memo: string;
}

/** A line ready to send: the contract's body, in integer cents. */
export interface ReadyLine {
  readonly account_code: string;
  readonly debit_cents: number;
  readonly credit_cents: number;
  readonly memo: string;
}

/** What a typed account choice ("1000 · Cash") names: the code before the middot. */
export function accountCodeOf(choice: string): string {
  const code = choice.split("\u00b7")[0] ?? "";
  return code.trim();
}

/**
 * The form's own checks before anything is sent: a memo, at least two lines, an account on each, and exactly one
 * side filled on each, as a well formed amount. These are the core's own rules said before the press; the core
 * still decides, and whether the two sides match is the core's answer, not computed here.
 */
export function checkDraft(
  memo: string,
  lines: readonly DraftLine[],
  parse: (text: string) => number | null,
): { ok: true; lines: ReadyLine[] } | { ok: false; error: string } {
  if (memo.trim() === "") return { ok: false, error: "Say what the journal is for: add a memo." };
  if (lines.length < 2) return { ok: false, error: "A journal needs at least two lines." };
  const ready: ReadyLine[] = [];
  for (const [i, line] of lines.entries()) {
    const n = i + 1;
    const code = accountCodeOf(line.account);
    if (code === "") return { ok: false, error: `Line ${n}: choose an account.` };
    const debit = line.debit.trim() === "" ? null : parse(line.debit);
    const credit = line.credit.trim() === "" ? null : parse(line.credit);
    if (line.debit.trim() !== "" && debit === null) return { ok: false, error: `Line ${n}: the debit is not an amount. Type dollars, like 1,250.00.` };
    if (line.credit.trim() !== "" && credit === null) return { ok: false, error: `Line ${n}: the credit is not an amount. Type dollars, like 1,250.00.` };
    const hasDebit = debit !== null && debit > 0;
    const hasCredit = credit !== null && credit > 0;
    if (hasDebit === hasCredit) return { ok: false, error: `Line ${n}: fill exactly one of debit or credit, with an amount above zero.` };
    ready.push({ account_code: code, debit_cents: hasDebit ? (debit as number) : 0, credit_cents: hasCredit ? (credit as number) : 0, memo: line.memo.trim() });
  }
  return { ok: true, lines: ready };
}

// ---- periods --------------------------------------------------------------------------------------

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

/** Month 9 reads "September". */
export function monthWord(month: number): string {
  return MONTHS[month - 1] ?? String(month);
}

/** A period by name: year and month, "September 2026". */
export function periodName(period: { year: number; month: number }): string {
  return `${monthWord(period.month)} ${period.year}`;
}

export function periodTone(status: string): "good" | "plain" {
  return status === "OPEN" ? "good" : "plain";
}

export function periodStatusWord(status: string): string {
  return status === "OPEN" ? "Open" : status === "CLOSED" ? "Closed" : status;
}

// ---- the journal register's window -----------------------------------------------------------------

/** What the register asks the core for: the applied posted date range and, after the first page, the core's next cursor. */
export interface JournalWindow {
  readonly dateFrom: string;
  readonly dateTo: string;
  readonly cursor?: string;
}

/** The register's page size: the newest hundred first, then a page at a time. */
export const REGISTER_PAGE = 100;

/**
 * The query the generated client is handed: an empty bound is unbounded and sends no key at all, and
 * a page after the first carries the core's cursor. The core answers date_from and date_to in
 * YYYY-MM-DD (the date input's own shape) and the cursor it minted on the page before.
 */
export function journalListParams(window: JournalWindow): { limit: number; date_from?: string; date_to?: string; cursor?: string } {
  return {
    limit: REGISTER_PAGE,
    ...(window.dateFrom === "" ? {} : { date_from: window.dateFrom }),
    ...(window.dateTo === "" ? {} : { date_to: window.dateTo }),
    ...(window.cursor === undefined || window.cursor === "" ? {} : { cursor: window.cursor }),
  };
}

/** The page's way to the next page: the core's cursor, or "" when the register is at its end. */
export function nextCursorOf(page: { next_cursor?: string | null }): string {
  return typeof page.next_cursor === "string" && page.next_cursor !== "" ? page.next_cursor : "";
}

/**
 * The window's words: what is loaded, said once beside the fields that change it. The days are said in
 * the desk's own date words, the same words the register's posted column reads (dayWords), never the
 * core's ISO digits: a date that sits beside a date field reads the way the desk reads dates.
 */
export function rangeWords(dateFrom: string, dateTo: string): string {
  if (dateFrom !== "" && dateTo !== "") return `Showing journals posted ${dayWords(dateFrom)} through ${dayWords(dateTo)}.`;
  if (dateFrom !== "") return `Showing journals posted ${dayWords(dateFrom)} or later.`;
  if (dateTo !== "") return `Showing journals posted through ${dayWords(dateTo)}.`;
  return "";
}

/**
 * The count the register's filters show: how many of the loaded rows carry that status. The core
 * answers no total for the register, so this is the count of the rows the desk has loaded, and the
 * line under the chips says so once, while the core's cursor says more rows are still to come.
 */
export function statusCountWord(count: number): string {
  return String(count);
}

/**
 * What the counts are of, said once under the chips. The core's register answers no total, so a
 * count is of the rows the desk has loaded; the line is there only while more rows are still to
 * come, and at the end of the register it drops away with the caveat it carried.
 */
export function countsNoteWords(loaded: number): string {
  return `Counts are of the ${loaded} journals loaded; Show more loads the rest.`;
}

/**
 * What "Pending approval" means to a bookkeeper: the desk works the label out from the journal's own
 * amount and the policy's threshold, so the words say that plainly and name no mechanism.
 */
export const PENDING_APPROVAL_NOTE =
  "Pending approval means the journal is a draft someone entered and its amount is at or over the approval threshold in the policy, so it waits for another approver to post it.";

/**
 * The same thing, said in the detail pane where one journal is read: the register's note, without
 * the opening that names the label.
 */
export const PENDING_APPROVAL_WHY =
  "It is a draft someone entered and its amount is at or over the approval threshold in the policy, so it waits for another approver to post it.";

// ---- the late posted list -------------------------------------------------------------------------

const MONTHS_SHORT = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** A document date in the dealer's house zone, where lateness is decided (P3). */
export function dayWords(value: string): string {
  const day = stampDay(value);
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(day);
  const month = m === null ? undefined : MONTHS_SHORT[Number(m[2]) - 1];
  if (m === null || month === undefined) return value;
  return `${month} ${Number(m[3])}, ${m[1]}`;
}

/** A journal by the number the register names it by. */
export function entryLabel(journal: { entry_no: number }): string {
  return `JE-${String(journal.entry_no).padStart(4, "0")}`;
}

/** One journal on the Controller's late posted list, as a person reads it. */
export interface LatePostedRow {
  /** The journal's own id, for the way to the journal. */
  readonly id: string;
  /** The number the register names it by. */
  readonly entry: string;
  readonly memo: string;
  /** The document date the journal carries. */
  readonly documentDate: string;
  /** The month it landed in, the period's own name; "" when the period is not on the list. */
  readonly landedIn: string;
}

/**
 * A late posted journal as the list reads it: the number it is known by, the document date it carries and
 * the month it landed in. Both dates are the server's: the document date is the one the journal carries and
 * the month is the period it was filed in.
 */
export function latePostedRow(
  journal: { id: string; entry_no: number; memo: string; posted_at?: string | null },
  period: { year: number; month: number } | undefined,
): LatePostedRow {
  return {
    id: journal.id,
    entry: entryLabel(journal),
    memo: journal.memo,
    documentDate: journal.posted_at === undefined || journal.posted_at === null ? "" : dayWords(journal.posted_at),
    landedIn: period === undefined ? "" : periodName(period),
  };
}

// ---- policy and checks ----------------------------------------------------------------------------

export function costBasisWord(strategy: string): string {
  return strategy === "WEIGHTED_AVG" ? "Weighted average cost" : strategy === "STANDARD_BUY" ? "Standard buy cost" : strategy;
}

/** The tie out's verdict in words, beside its colour. */
export function tieWords(inTie: boolean, ageDays: number): string {
  if (inTie) return "In tie";
  return ageDays > 0 ? `Out of tie for ${ageDays} ${ageDays === 1 ? "day" : "days"}` : "Out of tie";
}

/** The legacy import line source in words (the wire word names the first dealer's system; the screen does not). */
export function bisSourceWord(source: string): string {
  return source === "BOTH" ? "In both books" : source === "ERP_ONLY" ? "Only in this ledger" : source === "BISTRACK_ONLY" ? "Only in the legacy import" : source;
}

/** The two counts the posting queue's banner reads, in words; an empty queue shows nothing. */
export interface PostingQueueCounts {
  readonly waiting: number;
  readonly dead: number;
}

/** The banner's sentence: what is stuck, in the count's own words, and who lists it. */
export function postingQueueBannerWords(counts: PostingQueueCounts): string {
  if (counts.waiting <= 0 && counts.dead <= 0) return "";
  if (counts.waiting <= 0) {
    return `${counts.dead === 1 ? "1 journal failed" : `${counts.dead} journals failed`} to post and ${counts.dead === 1 ? "is" : "are"} set aside. The posting queue lists them for the Financial Controller.`;
  }
  const waiting = counts.waiting === 1 ? "1 journal is waiting to post" : `${counts.waiting} journals are waiting to post`;
  const dead = counts.dead <= 0 ? "" : ` and ${counts.dead === 1 ? "1 has" : `${counts.dead} have`} failed and ${counts.dead === 1 ? "is" : "are"} set aside`;
  return `${waiting}${dead}. The posting queue lists them for the Financial Controller.`;
}
