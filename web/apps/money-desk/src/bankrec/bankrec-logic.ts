// The bank reconciliation slice's plain logic: the words for every state the core stores, which row action a
// statement line admits, how a statement's dates and periods read, how its refusals read out loud, and how the
// rule form becomes the spec the core stores. Nothing here computes money and nothing here is rendered.
//
// Port provenance: the state words and the row action rule are the original app's (app/src/bankrec-shared.ts and
// app/src/_bankrec-line-actions.ts), with the words made plainer: a bookkeeper reconciling a statement does not
// say UNMATCHED or CREDIT_CARD. An unknown value is opened out (lowercased, underscores gone), never hidden.
import type { BankRecRuleSpec } from "@hardscapehouse/api-client";
import { formatMoney, centsToField } from "./bankrec-money.js";

export type Tone = "stop" | "watch" | "good" | "plain";

function opened(value: string): string {
  return value.toLowerCase().replace(/_/g, " ");
}

function wordsFrom(table: Readonly<Record<string, string>>, value: string): string {
  return table[value] ?? opened(value);
}

const ACCOUNT_TYPE: Record<string, string> = { BANK: "Bank account", CREDIT_CARD: "Credit card" };
export const accountTypeWord = (type: string): string => wordsFrom(ACCOUNT_TYPE, type);

const LINE_STATUS: Record<string, string> = { UNMATCHED: "Unmatched", MATCHED: "Matched", EXCLUDED: "Excluded" };
export const lineStatusWord = (status: string): string => wordsFrom(LINE_STATUS, status);

/** The tone only backs the word; the word always carries the meaning. */
export function lineTone(status: string): Tone {
  if (status === "UNMATCHED") return "watch";
  if (status === "MATCHED") return "good";
  return "plain";
}

const METHOD: Record<string, string> = { AUTO: "Automatic", MANUAL: "Manual" };
export const matchMethodWord = (method: string): string => wordsFrom(METHOD, method);

const TARGET: Record<string, string> = { PAYMENT: "Payment", LODGEMENT: "Lodgement", JOURNAL: "Journal", GROUP: "Group" };
export const targetWord = (target: string): string => wordsFrom(TARGET, target);

// An approved rule is the one in use: approving a new version retires the old.
const RULE_STATUS: Record<string, string> = { DRAFT: "Draft", APPROVED: "In use", RETIRED: "Retired" };
export const ruleStatusWord = (status: string): string => wordsFrom(RULE_STATUS, status);

export function ruleTone(status: string): Tone {
  if (status === "APPROVED") return "good";
  return "plain";
}

const RECON_STATUS: Record<string, string> = { OPEN: "Open", COMPLETED: "Signed off" };
export const reconStatusWord = (status: string): string => wordsFrom(RECON_STATUS, status);

export function reconTone(status: string): Tone {
  if (status === "COMPLETED") return "good";
  return "watch";
}

const RULE_KIND: Record<string, string> = {
  gateway_ref_exact: "Card settlement, reference exact",
  lodgement_ref_exact: "Cash or cheque lodgement, reference exact",
  descriptor_fuzzy: "Batch settlement, description and date window",
};
export const ruleKindWord = (kind: string): string => wordsFrom(RULE_KIND, kind);

export type RuleKind = NonNullable<BankRecRuleSpec["kind"]>;

export const RULE_KINDS: readonly RuleKind[] = ["gateway_ref_exact", "lodgement_ref_exact", "descriptor_fuzzy"];

/** A kind the form or a stored spec names, or the first kind when it names none the core knows. */
function ruleKindOf(text: string): RuleKind {
  return RULE_KINDS.find((k) => k === text) ?? "gateway_ref_exact";
}

export type LineAction = "match" | "unmatch" | null;

/**
 * The row action a statement line's status admits, or null when it admits none. An unmatched line is paired by
 * hand; a matched one is undone. An excluded line gets nothing: nothing in the core sets a line excluded or
 * brings it back, so a control for it would only ever refuse. An unknown status gets nothing rather than the
 * wrong action.
 */
export function lineAction(status: string): LineAction {
  if (status === "UNMATCHED") return "match";
  if (status === "MATCHED") return "unmatch";
  return null;
}

const MONTHS_SHORT = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const MONTHS_LONG = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];

/** The core sends a date as a timestamp at midnight UTC; the day is read from its digits, never through a zone. */
export function dayWords(value: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(value);
  const month = m === null ? undefined : MONTHS_SHORT[Number(m[2]) - 1];
  if (m === null || month === undefined) return value;
  return `${month} ${Number(m[3])}, ${m[1]}`;
}

export function periodWords(year: number, month: number): string {
  const name = MONTHS_LONG[month - 1];
  return name === undefined ? `${year}-${month}` : `${name} ${year}`;
}

export interface VarianceView {
  readonly word: string;
  readonly tone: Tone;
  readonly detail: string;
}

/**
 * The reconciliation's variance, which the core computes as statement balance less ledger balance. Zero is the
 * only state that can be signed off. The direction is said in words so the number is never read alone.
 */
export function varianceWords(varianceCents: number): VarianceView {
  if (varianceCents === 0) return { word: "Balanced", tone: "good", detail: "The statement and the ledger agree." };
  const size = formatMoney(Math.abs(varianceCents));
  return {
    word: `Off by ${size}`,
    tone: "stop",
    detail: varianceCents > 0 ? "The statement is higher than the ledger." : "The statement is lower than the ledger.",
  };
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isUuid(text: string): boolean {
  return UUID.test(text.trim());
}

function sentence(text: string): string {
  const trimmed = text.trim();
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

const DOWN = "The bank reconciliation service did not answer. Reload to try again.";

function plural(n: number, one: string, many: string): string {
  return n === 1 ? `1 ${one}` : `${n} ${many}`;
}

/**
 * A refusal in words. The role gate answers a walled route with its shared "Forbidden" body, which tells a person
 * nothing, so the screen names who may do it (reads are any staff; import, match, undo and opening a month are a
 * manager's; approving a rule and signing off a month are the controller's, an administrator tier). The core's own
 * sentences for the rest are rewritten into what to do next; anything unknown keeps the core's sentence.
 */
export function refusalWords(action: string, message: string, status: number): string {
  const lower = message.toLowerCase();
  if (status === 403) {
    switch (action) {
      case "import":
        return "Importing a statement is for the Financial Controller or a manager. Sign out and in again as one of them if this was not the role you meant.";
      case "match":
      case "unmatch":
        return "Matching is for the Financial Controller or a manager. Sign out and in again as one of them if this was not the role you meant.";
      case "open":
        return "Opening a month is for the Financial Controller or a manager. Sign out and in again as one of them if this was not the role you meant.";
      case "complete":
        return "Signing off a month is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      case "rule":
        return "Approving a match rule is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      case "cutover":
        return "Recording the starting point is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
      default:
        return "That is for the Financial Controller.";
    }
  }
  const cutoverDone = /cannot record cut-over at (\d{4}-\d{2}-\d{2}) because month (\d{4})-(\d{2}) is already completed/i.exec(message);
  if (cutoverDone !== null) {
    return `Not recorded: ${periodWords(Number(cutoverDone[2]), Number(cutoverDone[3]))} is already signed off, so the starting point cannot change. It stands as it was recorded.`;
  }
  if (lower.includes("only the financial controller may record")) {
    return "Recording the starting point is for the Financial Controller. Sign out and in again as the controller if this was not the role you meant.";
  }
  const cutoverGap = /cannot record cut-over at \d{4}-\d{2}-\d{2}: the statement and the book differ by (-?\$[\d,]+\.\d{2})/i.exec(message);
  if (cutoverGap !== null) {
    return `The statement and the book differ by ${cutoverGap[1].replace("-", "")}; tick the items still outstanding at cut over, or check the date and balance.`;
  }
  const remain = /cannot complete, (\d+) unmatched line\(s\) remain for (\d{4})-(\d{2})/i.exec(message);
  if (remain !== null) {
    const n = Number(remain[1]);
    const period = periodWords(Number(remain[2]), Number(remain[3]));
    return `Not signed off: ${plural(n, "statement line is", "statement lines are")} still unmatched for ${period}. ${n === 1 ? "Match it" : "Match them"}, or refresh the figures if you just did.`;
  }
  const beforeEnd =
    /cannot complete, (\d{4})-(\d{2}) has not reached month end yet \(month end is (\d{4}-\d{2}-\d{2})(?:; first day to sign off is (\d{4}-\d{2}-\d{2}))?\)/i.exec(
      message,
    );
  if (beforeEnd !== null) {
    const firstDay =
      beforeEnd[4] !== undefined && beforeEnd[4] !== ""
        ? ` The first day it may be signed off is ${dayWords(beforeEnd[4])}.`
        : " Wait until the month has ended before signing it off.";
    return `Not signed off: ${periodWords(Number(beforeEnd[1]), Number(beforeEnd[2]))} has not reached month end yet (${dayWords(beforeEnd[3])}).${firstDay}`;
  }
  const noLines = /cannot complete, no statement lines have been (?:imported|loaded) for (\d{4})-(\d{2})/i.exec(message);
  if (noLines !== null) {
    return `Not signed off: no statement lines have been imported for ${periodWords(Number(noLines[1]), Number(noLines[2]))}. Import the bank statement for that month first.`;
  }
  const clearingRev = /cannot complete, the clearing journal for "([^"]*)" (?:has been reversed in the ledger|no longer exists in the ledger|is not posted)/i.exec(message);
  if (clearingRev !== null) {
    return `Not signed off: the clearing journal for "${clearingRev[1]}" was reversed in the ledger. Undo the match and match the line again.`;
  }
  const earlierOpen = /cannot complete, an earlier month \((\d{4})-(\d{2})\) is still open/i.exec(message);
  if (earlierOpen !== null) {
    return `Not signed off: ${periodWords(Number(earlierOpen[1]), Number(earlierOpen[2]))} is still open. Sign off the earlier month first.`;
  }
  const laterDone = /cannot complete, a later month \((\d{4})-(\d{2})\) is already completed/i.exec(message);
  if (laterDone !== null) {
    return `Not signed off: ${periodWords(Number(laterDone[1]), Number(laterDone[2]))} is already signed off. Earlier months cannot be signed off after a later month.`;
  }
  const continuity =
    /cannot complete, the statement closing balance \((-?\$[\d,]+\.\d{2})\) does not equal the prior month's closing balance \((-?\$[\d,]+\.\d{2})\) plus this month's statement lines \((-?\$[\d,]+\.\d{2}) = (-?\$[\d,]+\.\d{2})\)/i.exec(
      message,
    );
  if (continuity !== null) {
    return `Not signed off: the statement closing balance (${continuity[1]}) does not continue from the prior month's closing balance (${continuity[2]}) plus this month's lines (${continuity[3]}, expected ${continuity[4]}). Check the closing balance and the imported lines.`;
  }
  const movedAdjusted =
    /cannot complete, the adjusted bank balance reads (-?\$[\d,]+\.\d{2}) \(statement (-?\$[\d,]+\.\d{2}), deposits in transit (-?\$[\d,]+\.\d{2}), outstanding cheques (-?\$[\d,]+\.\d{2})\) and the ledger reads (-?\$[\d,]+\.\d{2}) \((-?\$[\d,]+\.\d{2}) apart\)/i.exec(
      message,
    );
  if (movedAdjusted !== null) {
    return `Not signed off: the adjusted bank balance reads ${movedAdjusted[1]} (statement ${movedAdjusted[2]}, deposits in transit ${movedAdjusted[3]}, outstanding cheques ${movedAdjusted[4]}) and the ledger reads ${movedAdjusted[5]}, ${movedAdjusted[6]} apart, and they must agree. Refresh the figures, then match what is open.`;
  }
  const moved = /cannot complete, the statement reads (-?\$[\d,]+\.\d{2}) and the ledger reads (-?\$[\d,]+\.\d{2}) \((-?\$[\d,]+\.\d{2}) apart\)/i.exec(message);
  if (moved !== null) {
    return `Not signed off: the statement reads ${moved[1]} and the ledger reads ${moved[2]}, ${moved[3]} apart, and they must agree. Something was posted or imported since the month was opened: refresh the figures, then match what is open.`;
  }
  const apart = /cannot complete, variance is (-?\d+) cents/i.exec(message);
  if (apart !== null) {
    return `Not signed off: the statement and the ledger are ${formatMoney(Math.abs(Number(apart[1])))} apart, and they must agree. Match the open lines, then refresh the figures.`;
  }
  const completedFor = /reconciliation for (\d{4})-(\d{2}) is already completed/i.exec(message);
  if (completedFor !== null) {
    return `${periodWords(Number(completedFor[1]), Number(completedFor[2]))} is already signed off for this account, so its figures no longer change.`;
  }
  if (lower.includes("is already completed")) return "That month is already signed off.";
  if (lower.includes("is not open")) return "That month is not open. Reload to see where it stands.";
  if (/statement line .* (is matched, not unmatched|is not unmatched)/.test(lower)) {
    return "That line is already matched. Reload to see where it stands.";
  }
  if (lower.includes("payment already claimed")) {
    // The core's sentence: "payment already claimed, reversed or not RECEIVED: <payment no>[, <payment no>]
    // (claimed N of M)". It names the payments that did not move, by payment NUMBER (the dealer reads a number,
    // never an id), and three causes (the core does not read the payments table to say which, so neither does
    // the desk).
    const named = /payment already claimed[^:]*:\s*([^()]+?)\s*\(claimed/i.exec(message);
    const ids = named === null ? [] : named[1].split(/,\s*/).filter((n) => n !== "");
    const causes = "it is already matched to another line, it was reversed, or it is not a received payment.";
    if (ids.length === 0) return `That payment is not waiting to be deposited: ${causes}`;
    if (ids.length === 1 && ids[0] === "a payment that cannot be found") return "That payment cannot be found, so it cannot be deposited.";
    if (ids.length === 1) return `Payment ${ids[0]} is not waiting to be deposited: ${causes}`;
    return `Payments ${ids.join(", ")} are not waiting to be deposited: for each, ${causes}`;
  }
  if (lower.includes("a group match is not available yet")) {
    return "A group match is not available yet. Match the line to a payment, a lodgement or a journal.";
  }
  const lodgementApart = /that lodgement is for (-?\d+) cents and the line is (-?\d+) cents/i.exec(message);
  if (lodgementApart !== null) {
    return `That lodgement is ${formatMoney(Number(lodgementApart[1]))} and the line is ${formatMoney(Number(lodgementApart[2]))}, so it does not explain the line. Pick the lodgement for the same amount.`;
  }
  // FW1-05: a hand match checks the payment's amount, the line's sign, and the tender.
  const paymentApart = /that payment is for (-?\d+) cents and the line is (-?\d+) cents/i.exec(message);
  if (paymentApart !== null) {
    return `That payment is ${formatMoney(Number(paymentApart[1]))} and the line is ${formatMoney(Number(paymentApart[2]))}, so it does not explain the line. Pick the payment for the line's amount.`;
  }
  const matchApart = /the match is for (-?\d+) cents and the statement line is for (-?\d+) cents/i.exec(message);
  if (matchApart !== null) {
    return `The match was typed for ${formatMoney(Number(matchApart[1]))} and the line is ${formatMoney(Number(matchApart[2]))}. Leave the amount out, or type the line's own.`;
  }
  if (lower.includes("only a deposit (a positive line)")) {
    return "Only a deposit (money in) can be matched to a payment; this line reads money out.";
  }
  if (lower.includes("cannot be matched by hand")) {
    return "That payment's tender cannot be matched by hand; match a card, a cash, a cheque or an EFT deposit.";
  }
  // FW1-05b: a till cash payment reaches the bank only by its lodgement, so it is never a PAYMENT target.
  if (lower.includes("taken in a till session")) {
    return "That payment was taken in by the till, so it reaches the bank only with its lodgement. Match the lodgement's deposit line, not the payment alone.";
  }
  if (lower.includes("that journal is not posted")) return "That journal is not posted yet, so it cannot explain a bank line.";
  if (lower.includes("that journal does not touch this bank account")) return "That journal does not move this bank account, so it cannot explain this line.";
  const journalApart = /that journal moves this bank account by (-?\d+) cents and the line is (-?\d+) cents/i.exec(message);
  if (journalApart !== null) {
    return `That journal moves this account by ${formatMoney(Number(journalApart[1]))} and the line is ${formatMoney(Number(journalApart[2]))}. Pick the journal for the line's amount.`;
  }
  if (lower.includes("that journal has been reversed")) return "That journal has been reversed, so it no longer explains a bank line.";
  if (lower.includes("that journal is itself a reversal")) return "That journal is itself a reversal, so it cannot explain a bank line.";
  if (lower.includes("that journal was posted by the bank reconciliation itself")) {
    return "That journal was posted by the bank reconciliation itself, so it cannot explain a bank line. Pick a journal from the ledger.";
  }
  if (lower.includes("sits in a month that is already reconciled and completed, so it cannot be matched")) {
    return "That line sits in a month that is already signed off, so it cannot be matched.";
  }
  const recheck = /cannot complete, the journal matched to (?:the statement line )?"([^"]*)" (?:no longer explains it|no longer exists|cannot be checked)/i.exec(message);
  if (recheck !== null) {
    return `Not signed off: the journal matched to "${recheck[1]}" no longer explains that line. Undo the match and match the line again.`;
  }
  if (lower.includes("that journal already explains another statement line")) return "That journal already explains another line.";
  const lockedMonth = /that match sits in (\d{4})-(\d{2}), which is already reconciled and completed/i.exec(message);
  if (lockedMonth !== null) {
    return `That match belongs to ${periodWords(Number(lockedMonth[1]), Number(lockedMonth[2]))}, which is already signed off, so it cannot be undone.`;
  }
  if (lower.includes("already has a live match")) return "That target is already matched to this line.";
  if (lower.includes("cannot be unmatched")) {
    return "This deposit cannot be undone: the payment was reversed, and a reversed cheque or cash payment cannot be matched again.";
  }
  if (lower.includes("is not live (already unmatched)")) return "That match was already undone. Reload to see where the line stands.";
  if (lower.includes("another approved version")) return "Another version of this rule was approved a moment ago. Reload the rules and try again.";
  if (lower.includes("spec.kind must be one of")) return "Pick one of the three match kinds.";
  if (status === 404 || lower === "not found") return "Not found. Reload to see what is there now.";
  const words = sentence(message.replace(/bankrec: /g, ""));
  if (status >= 500 || words === "" || lower === "internal error") return DOWN;
  return words;
}

/**
 * A failed import, in words. The core records the parse failure and answers it with the audit envelope whose
 * error carries the parser's sentence; the common ones are rewritten into what the file lacked.
 */
export function importFailedWords(error: string): string {
  const text = error.replace(/^statement parse failed: /, "").replace(/bankrec: /g, "").trim();
  if (text === "") return "Not imported: the file could not be read.";
  const column = /^csv is missing the mapped column "([^"]+)"/.exec(text);
  if (column !== null) {
    return `Not imported: the file has no "${column[1]}" column. The first row must name Date, Description, Amount and Reference.`;
  }
  const date = /^csv row (\d+): bad date "([^"]*)"/.exec(text);
  if (date !== null) {
    return `Not imported: row ${date[1]} has a date the file format does not read ("${date[2]}"). Dates are written year, month, day, with dashes.`;
  }
  const usd = /^csv row (\d+): USD amount "([^"]*)" is not supported/.exec(text);
  if (usd !== null) {
    return `Not imported: row ${usd[1]} carries a USD amount ("${usd[2]}"), which cannot be imported into a CAD statement.`;
  }
  const missingAmt = /^csv row (\d+): missing amount/.exec(text);
  if (missingAmt !== null) {
    return `Not imported: row ${missingAmt[1]} has no amount.`;
  }
  const amount = /^csv row (\d+): bad (?:amount|debit|credit) "([^"]*)"/.exec(text);
  if (amount !== null) {
    return `Not imported: row ${amount[1]} has an amount that is not money ("${amount[2]}"). Amounts are dollars and cents; a debit has a minus sign or brackets.`;
  }
  if (/read csv header/.test(text)) return "Not imported: the file is empty or has no header row.";
  return `Not imported: ${text.charAt(0).toLowerCase()}${text.slice(1)}.`.replace(/\.\.$/, ".");
}

export interface RuleFields {
  readonly kind: string;
  readonly toleranceCents: number;
  readonly descriptor: string;
  readonly windowDays: number;
  readonly feeSplit: boolean;
  readonly feeAccount: string;
}

/**
 * The rule form as the spec the core stores (core/internal/bankrec/repository.go decodeSpec): the exact kinds carry
 * a target and a tolerance; the batch kind carries a description pattern, a date window and the optional fee split.
 * A description with no percent sign is wrapped, so "RUN" means the description contains RUN.
 */
export function ruleSpec(f: RuleFields): BankRecRuleSpec {
  const kind = ruleKindOf(f.kind);
  if (kind === "descriptor_fuzzy") {
    const pattern = f.descriptor.includes("%") ? f.descriptor : `%${f.descriptor}%`;
    const spec: BankRecRuleSpec = {
      kind,
      target: "PAYMENT",
      descriptor_ilike: pattern,
      date_window_days: f.windowDays,
      allow_fee_split: f.feeSplit,
    };
    if (f.feeSplit && f.feeAccount !== "") spec.fee_account_code = f.feeAccount;
    return spec;
  }
  return { kind, target: kind === "lodgement_ref_exact" ? "LODGEMENT" : "PAYMENT", tolerance_cents: f.toleranceCents };
}

/** A stored spec read back into the form, to approve a changed copy as the next version. */
export function ruleSpecFields(spec: BankRecRuleSpec): RuleFields {
  return {
    kind: ruleKindOf(spec.kind ?? ""),
    toleranceCents: spec.tolerance_cents ?? 0,
    descriptor: (spec.descriptor_ilike ?? "").replace(/^%+|%+$/g, ""),
    windowDays: spec.date_window_days ?? 0,
    feeSplit: spec.allow_fee_split === true,
    feeAccount: spec.fee_account_code ?? "",
  };
}

export { centsToField };
