// Ported from hardscapeos_dibbits master 87d7941b app/src/_approval-copy.ts;
// adapted for HH.
//
// _approval-copy.ts: the words the refusal dialog and the approvals list say,
// in one place (DOM-free, unit tested on the Node runner). The policy on a card
// is a word a counter person would use, not the database enum; a request's
// state is worded once for both the requester and the approver; who may
// approve is spoken from the roles the server names and never decided here.
// Not ported from the ERP: the refusal softener (this app shows the server's
// refusal text verbatim) and the return reasons (another panel's).
import { humanizeBps } from "./_guard-format.js";

/** The policy the margin floor requests carry. */
export const MARGIN_POLICY = "MARGIN_BELOW_MINIMUM";

/**
 * The five policy types, as a counter person names them. An unknown code is
 * title cased rather than dropped, so a policy added server side reads on the
 * day it ships.
 */
const POLICY_LABELS: Record<string, string> = {
  MARGIN_BELOW_MINIMUM: "Below floor",
  CREDIT_LIMIT_EXCEEDED: "Credit limit",
  COD_CONSTRAINT: "COD hold",
  JE_OVER_THRESHOLD: "Journal limit",
  FREIGHT_ABOVE_QUOTE: "Freight raise",
};

/** What a request's own state is called. One map for the requester and the approver. */
export const DECISION_LABEL: Record<string, string> = {
  PENDING: "Pending",
  APPROVED: "Approved",
  REJECTED: "Rejected",
};

/** decisionWord words a state; an unmapped value comes back lowercased, spaces for underscores, never a confident lie. */
export function decisionWord(status: string): string {
  return DECISION_LABEL[status] ?? String(status ?? "").toLowerCase().replace(/_/g, " ");
}

function titleCaseCode(code: string): string {
  const words = String(code || "").toLowerCase().split(/[_\s]+/).filter(Boolean);
  if (words.length === 0) return "";
  return words[0].charAt(0).toUpperCase() + words[0].slice(1) + (words.length > 1 ? " " + words.slice(1).join(" ") : "");
}

export function policyLabel(policyType: string): string {
  return POLICY_LABELS[policyType] ?? titleCaseCode(policyType);
}

/**
 * approvalDetail is what a request card says it is about: the server's own
 * sentence with any basis points spoken as percent, or a plain placeholder when
 * the server wrote none.
 */
export function approvalDetail(detail: string): string {
  return humanizeBps(detail || "").trim() || "Override requested";
}

/**
 * approverWords speaks the approver roles exactly as the server names them,
 * each with a leading "the" unless it already has one, joined with a final
 * "or". The app never adds, drops or reorders a role.
 */
export function approverWords(roles: readonly string[]): string {
  const spoken = roles.map((role) => (role.startsWith("the ") ? role : `the ${role}`));
  return spoken.join(", ").replace(/, ([^,]*)$/, " or $1");
}

/**
 * requesterStateWords is the requester's view of a request as the server
 * returns it. The contract has three states (pending, approved, rejected) and
 * no used state, so nothing here claims a request was used; an unknown state
 * reads as itself.
 */
export function requesterStateWords(status: string): string {
  if (status === "PENDING") return "Your request is pending. Nobody has decided it yet.";
  if (status === "APPROVED") return "Your request was approved.";
  if (status === "REJECTED") return "Your request was rejected.";
  return `Your request is ${decisionWord(status)}.`;
}

/** Only a pending row offers a decision; whether this caller may make it is the server's answer. */
export function isDecidable(status: string): boolean {
  return status === "PENDING";
}

/** What the server's own request sentence names: the line asked about, the discount, the margin and the floor, each only if the sentence says it. */
export interface RequestFigures {
  lineNo: number | null;
  discountPct: number | null;
  marginPct: number | null;
  floorPct: number | null;
  /** How far the asked margin sits under the floor, in percentage points; null unless both are named and the margin is under. */
  gapPct: number | null;
}

/**
 * parseRequestDetail reads the figures out of the request's sentence ("Quote Q-1001, line 2, Holland 60, 10% discount,
 * margin 9.1% against a floor of 20.0%"), after any basis points are spoken as percent. The request carries no
 * separate fields for them, so the sentence is the only source; a figure the sentence does not name stays null and
 * is never computed here (the margin gate is the server's: a caller off the cost list gets no margin in it).
 */
export function parseRequestDetail(detail: string): RequestFigures {
  const text = humanizeBps(detail || "");
  const num = (m: RegExpMatchArray | null): number | null => (m === null ? null : Number(m[1]));
  const lineNo = num(text.match(/\bline\s+(\d+)\b/i));
  const discountPct = num(text.match(/(-?\d+(?:\.\d+)?)%\s+discount/i));
  const marginPct = num(text.match(/margin\s+(-?\d+(?:\.\d+)?)%/i));
  const floorPct = num(text.match(/floor of\s+(-?\d+(?:\.\d+)?)%/i));
  const gapPct = marginPct !== null && floorPct !== null && marginPct < floorPct ? Math.round((floorPct - marginPct) * 10) / 10 : null;
  return { lineNo, discountPct, marginPct, floorPct, gapPct };
}

/** The quote id a request's subject names ("quote:<id>"), or "" for any other subject. */
export function quoteIdOfSubject(subjectRef: string | undefined): string {
  const m = /^quote:(.+)$/.exec(subjectRef ?? "");
  return m === null ? "" : m[1]!;
}
