// The ledger slice's plain logic: how the core's answers are read, what each
// approval kind is called, who may decide it, and how a refusal reads out
// loud. Nothing here computes money and nothing here is rendered.
import { formatMoney } from "./gl-money.js";

/** Both shapes the core answers a list in: the bare array it sends today and the contract's page object. */
export function readList<T>(value: unknown): T[] | null {
  if (Array.isArray(value)) return value as T[];
  if (typeof value === "object" && value !== null) {
    const items = (value as { items?: unknown }).items;
    if (Array.isArray(items)) return items as T[];
  }
  return null;
}

/**
 * The core files a journal request with its debit total in cents inside the
 * sentence ("(750000 cents)"). A person reads dollars, so the figure is
 * formatted where it stands; the sentence is otherwise the server's own.
 */
export function detailWords(detail: string): string {
  return detail.replace(/\((\d+) cents\)/g, (_all, digits: string) => `(${formatMoney(Number(digits))})`);
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
  const trimmed = text.trim();
  if (trimmed === "") return "";
  const cap = trimmed.charAt(0).toUpperCase() + trimmed.slice(1);
  return /[.!?]$/.test(cap) ? cap : `${cap}.`;
}

/**
 * A refusal in words. The role gate answers a walled route with its shared
 * "Forbidden" body, which tells a person nothing, so the app names who may do
 * it. The core's separation of duties words (the requester never decides their
 * own request) get the way out beside them. Any other refusal keeps the core's
 * own sentence.
 */
export function refusalWords(action: string, message: string, status: number): string {
  const lower = message.toLowerCase();
  if (action === "decide") {
    if (lower.includes("separation of duties") || lower.includes("four-eyes") || lower.includes("must differ")) {
      return `You filed this request, so you cannot decide it. Another approver decides it: ${WHO_DECIDES}.`;
    }
    if (status === 403) {
      return "Deciding a request is for the Financial Controller or the General Manager.";
    }
    if (status === 409) return "Someone already decided this request. Reload to see it.";
  }
  if (status === 403) {
    return "That is for the Financial Controller or the General Manager.";
  }
  const words = sentence(message);
  if (words === "") return "The ledger did not answer. Reload to try again.";
  return words;
}
