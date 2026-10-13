// The admin-rest surfaces' pure half: the assistant's budget and spend words,
// the decimal string rule for every amount the server sets, and the refusal
// words. No DOM, no Lit, no client. admin-work.ts holds the state, the
// admin-*.ts files the element side.
//
// Port provenance: the budget and spend words are the original app's
// assist-model.ts, the product editor's field list and changed-only save are
// the original app's product-edit.ts (repository hardscapeos_dibbits, read
// only; see PROVENANCE.md), kept in meaning with one house change: the em
// dashes in the budget sentence became commas (the house dash rule, the same
// change the hotkeys port made).
import { formatDollars } from "@hardscapehouse/pos-kit/src/ported/_money-format.js";

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
  const amount = Number(trimmed);
  // The contract's tokens are an int32; past it the box refuses in its own words.
  return amount > 2147483647 ? null : amount;
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

// -- the exports: one call, one file, no queue ---------------------------------------------------
//
// Each export is a single server call that lands as a file in the person's
// downloads. There is no queue and no second call: the workbook arrives as
// the raw bytes the route answers, the messages export as the rows the route
// answers, written to the file from that one answer.

/** The audit workbook's file name. */
export function auditWorkbookName(): string {
  return "audit.xlsx";
}

/** The messages export's file name. */
export function messagesExportName(): string {
  return "house-messages-export.json";
}

/** A message's kind as a word. */
export function messageKindWords(kind: string): string {
  if (kind === "TEXT") return "Text";
  if (kind === "EVENT") return "Event";
  if (kind === "APPROVAL") return "Approval";
  return plainlyWords(kind);
}

/** An export that found nothing: about the filters, never about the house. */
export function messagesEmptyWords(): string {
  return "No messages match this export. An export answers with the rows it finds, and this one found none, so widen the window or clear the channel.";
}

/**
 * The messages export is leadership's read (the core's RequireAdmin). A
 * refusal names who may act; the rest pass the server's own words through.
 */
export function messagesRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not take this export. The house's messages export is leadership's own.";
  }
  return error !== "" ? error : "Try again in a moment.";
}

/**
 * What the export carries, said once above the rows: the server's own masking
 * rule, not this screen's promise.
 */
export function messagesMaskWords(): string {
  return "The export is the masked projection: deleted messages are tombstoned and cleartext-looking identifiers are redacted before the rows leave the system.";
}

// -- the catalog: the product's words and its picture ----------------------------------------------
//
// The editor edits the product's DESCRIPTION fields, the text allowlist the
// contract's ProductAttributeUpdate names. The save is a PARTIAL map: only
// the fields the person changed go on the wire, the contract refuses any key
// outside the allowlist, and an untouched draft sends nothing at all.

/** One editable description field: the key on the wire and the label on the glass. */
export interface ProductTextField {
  readonly key: string;
  readonly label: string;
  readonly wide: boolean;
}

/** The description fields this editor edits, in the order the form draws them. */
export const PRODUCT_TEXT_FIELDS: readonly ProductTextField[] = [
  { key: "name", label: "Name", wide: true },
  { key: "description", label: "Description", wide: true },
  { key: "manufacturer", label: "Manufacturer", wide: false },
  { key: "collection", label: "Collection", wide: false },
  { key: "color", label: "Color", wide: false },
  { key: "color_family", label: "Color family", wide: false },
  { key: "finish", label: "Finish", wide: false },
  { key: "application", label: "Application", wide: false },
  { key: "texture", label: "Texture", wide: false },
  { key: "product_size", label: "Product size", wide: false },
  { key: "dimensions_lwh", label: "Dimensions (L W H)", wide: false },
  { key: "sold_by", label: "Sold by", wide: false },
];

/** The words a product row holds for the editor's fields, empties for absent ones. */
export function productDraftFrom(p: object): Record<string, string> {
  const src = p as Record<string, unknown>;
  const draft: Record<string, string> = {};
  for (const f of PRODUCT_TEXT_FIELDS) {
    const v = src[f.key];
    draft[f.key] = typeof v === "string" ? v : "";
  }
  return draft;
}

/**
 * The save's body: only the fields whose words differ from the row the server
 * holds. The wire value is the words as they were typed; a decimal the row
 * carries (the minimum order) is never touched here, so it can never be
 * routed through a float.
 */
export function productChangedPatch(draft: Readonly<Record<string, string>>, original: object): Record<string, string> {
  const src = original as Record<string, unknown>;
  const patch: Record<string, string> = {};
  for (const f of PRODUCT_TEXT_FIELDS) {
    const next = draft[f.key] ?? "";
    const had = typeof src[f.key] === "string" ? (src[f.key] as string) : "";
    if (next !== had) patch[f.key] = next;
  }
  return patch;
}

/**
 * The retail anchor as a person reads it, through the shared formatter and
 * never a currency literal. Absence is NOT zero: the contract is explicit
 * that an absent anchor is not the same as a zero one, so it reads as words.
 */
export function listPriceWords(cents: number | null | undefined, uom: string): string {
  if (typeof cents !== "number" || !Number.isFinite(cents)) return "No list price set";
  const words = formatDollars(cents);
  return uom !== "" ? `${words} per ${uom}` : words;
}

/** The editor's refusal for a picture the contract does not declare. */
export function imageTypeWords(): string {
  return "The picture must be a JPEG, a PNG or a WebP image.";
}

/** The catalog search's empty answer: about the words, never about the catalogue. */
export function catalogEmptyWords(q: string): string {
  return `No product matches "${q}". The catalogue's names, SKUs and collections are what the search reads; try fewer words, or the SKU alone.`;
}

/**
 * The product writes' refusal (the core's RequireManager, tiers 1 and 2): the
 * 403 names who may write, and the rest pass the server's own words through,
 * an uneditable column named by the column.
 */
export function productRefusalWords(error: string, status: number | undefined): string {
  if (status === 403) {
    return "You may not save this. A product's words and its picture are written by managers and above.";
  }
  return error !== "" ? error : "Try again in a moment.";
}

// -- the platform keys: the read only roster ------------------------------------------------------
//
// The contract marks this read for this surface (x-access dealer-admin,
// level read) and makes it staff wide; the write is the operator's alone and
// this screen renders no write at all. The floor and the override are opaque
// objects by contract: the screen says what the server said, as it came,
// never a guess at their shape.

/** A key's floor or override, as the server sent it, or the words for absent. */
export function keysValueWords(value: unknown): string {
  if (value === null || value === undefined) return "not set";
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

/**
 * The waits words: the read is contract-marked for this surface, and the
 * system does not serve it yet. The words name what is missing and nothing
 * of how the product is built.
 */
export function keysWaitWords(): string {
  return "The platform's key roster is not served here yet. It opens when the platform's operator tools are connected for this system, and until then there is nothing to read.";
}
