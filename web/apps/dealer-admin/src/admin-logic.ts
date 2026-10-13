// The admin-rest surfaces' pure half: the assistant's budget and spend words,
// the decimal string rule for every amount the server sets, and the refusal
// words. No DOM, no Lit, no client. admin-work.ts holds the state, the
// admin-*.ts files the element side.
//
// Port provenance: the budget and spend words are the original app's
// assist-model.ts (repository hardscapeos_dibbits, read only; see
// PROVENANCE.md), kept in meaning with one house change: the em dashes in the
// budget sentence became commas (the house dash rule, the same change the
// hotkeys port made).

// -- the assistant's budget and spend ------------------------------------------------
//
// THE ONE RULE CARRIED OVER FROM THE ORIGINAL: an error and an empty answer
// are different states, and a person at their limit is neither. A zero row on
// the spend report is a fact, not a gap; the server zero-fills the report
// (core/internal/assist/admin.go) so the screen never reconciles two lists.

/** A resolved allowance and where it came from, as the wire carries it. */
export interface AssistBudget {
  readonly tokens: number;
  readonly source: string;
}

/** One row of the admin spend report, as the screens use it. */
export interface AssistSpendRow {
  readonly user_id: string;
  readonly display_name: string;
  readonly email: string;
  readonly role_code: string;
  readonly exchanges: number;
  readonly tokens_in: number;
  readonly tokens_out: number;
  readonly budget: AssistBudget;
}

/** Total tokens for a spend, both directions, because both are billed. */
export function spendTotal(s: { readonly tokens_in: number; readonly tokens_out: number } | null | undefined): number {
  if (!s) return 0;
  return (s.tokens_in ?? 0) + (s.tokens_out ?? 0);
}

/**
 * The enforcement predicate, mirrored from the core's own OverBudget exactly:
 * spent >= budget. The client enforces nothing, the server refuses, but the
 * meter must not disagree with the refuser.
 */
export function overBudget(spent: number, budget: number): boolean {
  return spent >= budget;
}

/**
 * The meter's fill, clamped to [0, 1]. A zero budget reads FULL, not empty:
 * a person switched off has used all of what they were given, and an empty
 * bar next to a spent allowance reads as a contradiction.
 */
export function usedFraction(spent: number, budget: number): number {
  if (budget <= 0) return 1;
  const f = spent / budget;
  if (f < 0) return 0;
  return f > 1 ? 1 : f;
}

/** Thousands-grouped token counts, the core's own grouping (assist/format.go). */
export function formatTokens(n: number): string {
  if (!Number.isFinite(n)) return "0";
  const neg = n < 0;
  const s = Math.abs(Math.trunc(n)).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  return neg ? `-${s}` : s;
}

/**
 * The one sentence the spend row shows for its budget. The source is TAKEN
 * from the server, never inferred by comparing the number to the role
 * default: two people can legitimately hold an override equal to their role's
 * number, and inferring would mislabel them and then silently move the label
 * when the role default moved.
 */
export function budgetSentence(b: AssistBudget | null | undefined, roleCode = ""): string {
  if (!b) return "No budget resolved";
  const n = formatTokens(b.tokens);
  switch (b.source) {
    case "user":
      return `${n}, their own`;
    case "role":
      return roleCode !== "" ? `${n}, from ${roleCode}` : `${n}, from their role`;
    default:
      return `${n}, the default`;
  }
}

/**
 * The one line under a person's name. EXCHANGES COME FIRST, and that is the
 * point of the pilot: the default provider costs zero tokens, so a line that
 * led with tokens would read "0 tokens" for every person all month and answer
 * nobody's question.
 */
export function spendSummary(s: AssistSpendRow | null | undefined): string {
  const exchanges = s?.exchanges ?? 0;
  const total = spendTotal(s);
  const word = exchanges === 1 ? "question" : "questions";
  if (exchanges === 0) return "No questions yet";
  if (total === 0) {
    return `${formatTokens(exchanges)} ${word}, no AI tokens used`;
  }
  return `${formatTokens(exchanges)} ${word}, ${formatTokens(total)} tokens`;
}

// -- the amounts ------------------------------------------------------------------------
//
// Limits are counts or cents set by the server. An amount is parsed through
// the decimal string, digits only, never through float arithmetic: a budget
// box that accepted "1e6" or "12.5" would send a number the sender never
// meant, and a negative budget is the core's own refusal to answer, not this
// box's to send.

/**
 * The whole amount a budget box holds, or null when it is not one. The
 * decimal string is the source of truth: digits only, trimmed, so what the
 * person typed is what the server is asked.
 */
export function wholeAmount(raw: string): number | null {
  const trimmed = (raw ?? "").trim();
  if (trimmed === "" || !/^\d+$/.test(trimmed)) return null;
  return Number(trimmed);
}

/** The budget box's own refusal, before any call. */
export function assistBudgetInputWords(): string {
  return "A budget is a whole number of tokens. Use 0 to switch the assistant off for this person or role.";
}

/**
 * The assist admin read is a tier 1 global role's (the core's RequireAdmin):
 * the refusal names who may act, and other statuses pass the server's own
 * words through, never a paraphrase.
 */
export function assistRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not open this. The assistant's usage, budgets and settings are read and set by leadership only.";
  }
  return error !== "" ? error : "Try again in a moment.";
}

// -- the print queue ---------------------------------------------------------------------------
//
// One document's print history: how many times it was printed, by whom. The
// read is staff wide (the core registers it on RequireStaff), so the screen
// invents no gate of its own; the refusals it can meet are the server's own
// words about the doc type and the reference.

/** The three papers the core's print queue knows, as the select offers them. */
export const PRINT_DOC_TYPES: readonly { readonly value: string; readonly label: string }[] = [
  { value: "RECEIPT", label: "Receipt" },
  { value: "LABEL_ZPL", label: "Label (ZPL)" },
  { value: "DELIVERY_DOC", label: "Delivery document" },
];

/** A doc type as a person reads it, degrading honestly: an unknown value as itself, opened out. */
export function printDocLabel(value: string): string {
  const hit = PRINT_DOC_TYPES.find((t) => t.value === value);
  if (hit !== undefined) return hit.label;
  return plainlyWords(value);
}

/**
 * The select answers in its option's own words (the register's role select is
 * the precedent: the option list carries the names, the answer maps back to
 * the code). An answer the papers do not name falls back to the receipt.
 */
export function printDocTypeFromLabel(label: string): string {
  return PRINT_DOC_TYPES.find((t) => t.label === label)?.value ?? "RECEIPT";
}

/** A job's status as words. */
export function printStatusWords(status: string): string {
  if (status === "QUEUED") return "Queued";
  if (status === "PRINTED") return "Printed";
  if (status === "FAILED") return "Failed";
  return plainlyWords(status);
}

/** What the job was rendered for. */
export function printTargetWords(target: string): string {
  if (target === "BROWSER") return "Browser";
  if (target === "ZPL") return "ZPL";
  return plainlyWords(target);
}

/** The closed-set fallback the app carries everywhere: lowercased, separators opened out. */
function plainlyWords(value: string): string {
  return value.trim().toLowerCase().replace(/[_.-]+/g, " ").replace(/\s+/g, " ");
}

/**
 * The empty history: a document prints its way into this list, so an empty
 * answer is about the document, never about the queue.
 */
export function printEmptyWords(): string {
  return "No print jobs for this document. A document prints its way into this list, and nothing has been sent to the printer for this one yet.";
}
