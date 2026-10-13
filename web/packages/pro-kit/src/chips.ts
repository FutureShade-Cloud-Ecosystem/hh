// Stage chips and status chips. The colours are the platform's, through design
// system tokens: a stage or a status means the same thing on every dealer's
// screen, so neither borrows the dealer's gold (gold means "you can act here")
// or the contractor brand layer. Colour never encodes state alone: every chip
// carries its word.
//
// The stages are the contractor's chain, Plan then Quote then Order then
// Invoice. The statuses are the core's vocabularies, one table per kind,
// translated to sentence case words a person reads (never the stored string).
import { css, html, type TemplateResult } from "lit";

export const STAGES = ["plan", "quote", "order", "invoice"] as const;
export type Stage = (typeof STAGES)[number];

export const STAGE_WORDS: Readonly<Record<Stage, string>> = {
  plan: "Plan",
  quote: "Quote",
  order: "Order",
  invoice: "Invoice",
};

export type Tone = "neutral" | "good" | "watch" | "stop";

interface StatusEntry {
  words: string;
  tone: Tone;
}

// One table per kind. A status the core adds later is "Unknown" and neutral until it is added here.
const STATUSES: Readonly<Record<string, Readonly<Record<string, StatusEntry>>>> = {
  project: {
    PLANNING: { words: "Planning", tone: "neutral" },
    QUOTING: { words: "Quoting", tone: "watch" },
    ORDERED: { words: "Ordered", tone: "good" },
    FULFILLED: { words: "Fulfilled", tone: "good" },
    CANCELLED: { words: "Cancelled", tone: "stop" },
  },
  quote: {
    DRAFT: { words: "Draft", tone: "neutral" },
    SENT: { words: "Sent", tone: "watch" },
    ACCEPTED: { words: "Accepted", tone: "good" },
    CONVERTED: { words: "Ordered", tone: "good" },
    EXPIRED: { words: "Expired", tone: "stop" },
    LOST: { words: "Lost", tone: "stop" },
  },
  order: {
    DRAFT: { words: "Draft", tone: "neutral" },
    CONFIRMED: { words: "Confirmed", tone: "good" },
    PICKING: { words: "Picking", tone: "watch" },
    READY: { words: "Ready", tone: "good" },
    PARTIALLY_FULFILLED: { words: "Part delivered", tone: "watch" },
    FULFILLED: { words: "Delivered", tone: "good" },
    INVOICED: { words: "Invoiced", tone: "good" },
    CLOSED: { words: "Closed", tone: "neutral" },
    CANCELLED: { words: "Cancelled", tone: "stop" },
  },
  invoice: {
    DRAFT: { words: "Draft", tone: "neutral" },
    FINALIZED: { words: "Open", tone: "watch" },
    PARTIALLY_PAID: { words: "Part paid", tone: "watch" },
    PAID: { words: "Paid", tone: "good" },
    VOID: { words: "Void", tone: "neutral" },
  },
  payment: {
    RECEIVED: { words: "Received", tone: "good" },
    DEPOSITED: { words: "Deposited", tone: "good" },
    PENDING: { words: "Pending", tone: "watch" },
    BOUNCED: { words: "Bounced", tone: "stop" },
    REFUNDED: { words: "Refunded", tone: "neutral" },
    VOID: { words: "Void", tone: "neutral" },
  },
  team: {
    active: { words: "Active", tone: "good" },
    pending: { words: "Invited", tone: "watch" },
    inactive: { words: "Inactive", tone: "neutral" },
  },
};

const UNKNOWN: StatusEntry = { words: "Unknown", tone: "neutral" };

function entry(kind: string, status: string): StatusEntry {
  const table = Object.prototype.hasOwnProperty.call(STATUSES, kind) ? STATUSES[kind] : undefined;
  if (table === undefined || !Object.prototype.hasOwnProperty.call(table, status)) return UNKNOWN;
  return table[status] ?? UNKNOWN;
}

export function statusWords(kind: string, status: string): string {
  return entry(kind, status).words;
}

export function statusTone(kind: string, status: string): Tone {
  return entry(kind, status).tone;
}

/** The chip sheet an element adopts beside baseStyles. Tokens only. */
export const chipStyles = css`
  .pchip {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-4);
    padding: var(--hh-s-4) var(--hh-s-8);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-pill);
    background: transparent;
    color: var(--hh-text-dim);
    font-size: 0.75rem;
    font-weight: 600;
    line-height: 1.2;
    white-space: nowrap;
  }
  .pchip-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .pchip-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .pchip-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .stage-plan { color: var(--hh-text-dim); border-color: var(--hh-line); }
  .stage-quote { color: var(--hh-quote); border-color: var(--hh-quote); }
  .stage-order { color: var(--hh-info); border-color: var(--hh-info); }
  .stage-invoice { color: var(--hh-success); border-color: var(--hh-success); }
  .pchip[aria-current="step"] { background: var(--hh-widget); }
`;

export function stageChip(stage: Stage, current = false): TemplateResult {
  const mark = current ? "step" : "false";
  return html`<span class="pchip stage-${stage}" data-stage="${stage}" aria-current="${mark}">${STAGE_WORDS[stage]}</span>`;
}

export function statusChip(kind: string, status: string): TemplateResult {
  const tone = statusTone(kind, status);
  return html`<span class="pchip pchip-${tone}" data-status="${status}">${statusWords(kind, status)}</span>`;
}
