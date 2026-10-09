// The procurement slice's plain logic: the words for what the server answered,
// who may do what this role may not, the shapes the core really writes, and the
// one check made before a request is sent. Nothing here computes money; the
// server decides every figure and every refusal, and this file only says them.
//
// Port provenance: the order states and their vocabulary follow the original
// app's purchasing console screen (repository hardscapeos_dibbits, commit
// f8f4973db317b92296ae8ec7e62a3f54f5dc82ab); the refusal words follow the role
// gate's shared body ("Forbidden") and the decided role matrix (ROLE-ACCESS-r3,
// the Procurement Manager and Payables and Receivables sections, row 457).
import type {
  InventoryShortfallPage,
  InventoryShortfallRow,
  PurchasingIncomingLine,
  PurchasingPO,
  PurchasingPOPage,
} from "@hardscapehouse/api-client";

// ---- the shapes the core really writes ------------------------------------------------------------

/**
 * The shortfall worklist's rows. The core writes the list under "shortfall"
 * (Handler.shortfall) while the contract names it "items"; the shape census
 * records the drift. Both are read, so the fix on either side keeps this working.
 */
export function shortfallRows(page: Partial<InventoryShortfallPage> & { shortfall?: InventoryShortfallRow[] | null }): InventoryShortfallRow[] {
  return page.shortfall ?? page.items ?? [];
}

/** The PO list, which the core answers as {"items":null} when there is none. */
export function poRows(page: Partial<Omit<PurchasingPOPage, "items">> & { items?: PurchasingPO[] | null }): PurchasingPO[] {
  return page.items ?? [];
}

/** The product incoming read: the core writes "lines", the contract names "items". */
export function incomingLines(page: { lines?: PurchasingIncomingLine[] | null; items?: PurchasingIncomingLine[] | null }): PurchasingIncomingLine[] {
  return page.lines ?? page.items ?? [];
}

// ---- order states ---------------------------------------------------------------------------------

const STATUS_WORD: Record<string, string> = {
  DRAFT: "Draft",
  ORDERED: "Ordered",
  PARTIAL: "Partial",
  RECEIVED: "Received",
  CLOSED: "Closed",
  CANCELLED: "Cancelled",
};

export function statusWord(status: string): string {
  return STATUS_WORD[status] ?? status;
}

export type Tone = "good" | "watch" | "stop" | "plain";

/** The pill's tone; the word is always beside it, so colour never carries the meaning alone. */
export function statusTone(status: string): Tone {
  if (status === "RECEIVED") return "good";
  if (status === "PARTIAL" || status === "ORDERED") return "watch";
  if (status === "CANCELLED") return "stop";
  return "plain";
}

/** An order still has something coming: a draft, an order placed, or one part received. */
export function isOpen(status: string): boolean {
  return status === "DRAFT" || status === "ORDERED" || status === "PARTIAL";
}

/** An order the vendor has been given: the register's "open" and the incoming list's rows. */
export function isPlaced(status: string): boolean {
  return status === "ORDERED" || status === "PARTIAL";
}

export function deliveryWord(method: string | null | undefined): string {
  if (method === "DELIVERY") return "Vendor truck";
  if (method === "PICKUP") return "Pickup";
  return "Not set";
}

// ---- the check made before a request is sent ---------------------------------------------------------

export type DateCheck = { ok: true; date: string } | { ok: false; words: string };

/** The promised date, as the core wants it: a real calendar day written YYYY-MM-DD. */
export function validateExpectedDate(text: string): DateCheck {
  const value = text.trim();
  if (value === "") return { ok: false, words: "Pick the date the vendor promised." };
  const match = value.match(/^(\d{4})-(\d{2})-(\d{2})$/);
  if (match === null) return { ok: false, words: "Write the date as year, month and day, such as 2031-03-04." };
  const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])];
  const at = new Date(Date.UTC(year, month - 1, day));
  const real = at.getUTCFullYear() === year && at.getUTCMonth() === month - 1 && at.getUTCDate() === day;
  return real ? { ok: true, date: value } : { ok: false, words: "That is not a real date." };
}

// ---- words for a refusal ------------------------------------------------------------------------------

/**
 * What each walled move is, and who may do it. The order walls are the principal's ruling (FROM-PRINCIPAL 72):
 * drafting, placing, editing and cancelling a PO are the Procurement Manager's, the General Manager's and super admin's;
 * the Yard Manager receives only; the rest read. Posting a bill is Payables and Receivables' (row 457).
 */
const WHO_MAY: Record<string, string> = {
  order:
    "Drafting, placing, editing and cancelling a purchase order is for the Procurement Manager, the General Manager and the super admin only. The Yard Manager receives goods only; other roles can read.",
  "post-bill":
    "Posting a vendor bill is for Payables and Receivables. The Procurement Manager can read the bills against its own orders and the three way match, but does not post them.",
};

const SERVER_PREFIX = /^[a-z][a-z0-9]*: /;

function sentence(text: string): string {
  const bare = text.trim().replace(SERVER_PREFIX, "");
  if (bare === "") return "";
  const cased = bare.charAt(0).toUpperCase() + bare.slice(1);
  return /[.!?]$/.test(cased) ? cased : `${cased}.`;
}

/**
 * A refusal or failure as the words a person reads. A 403 from the role gate
 * names who may do the move; the server's own sentence is otherwise kept, only
 * capitalised and ended.
 */
export function refusalWords(action: string, error: string, status: number | undefined): string {
  if (status === 403) {
    return WHO_MAY[action] ?? "Your role may not do that. Ask the General Manager.";
  }
  if (status === 401) return "Your sign in has ended. Sign in again.";
  if (status === undefined) return "The server could not be reached. Nothing was changed. Try again.";
  if (status >= 500) return "The server hit a problem. Nothing was changed. Try again.";
  const words = sentence(error);
  return words === "" ? "The server refused that." : words;
}

// ---- the draft the core says does not cover a row ----------------------------------------------------

/**
 * PENDING on lane B core (FROM-PRINCIPAL 72): the shortfall row will carry the draft that does not cover it.
 * The field names are the app's reading of that change (draft_po_id, draft_po_no); the stub contract test
 * holds the stub to them and switches to the generated descriptor the day the contract declares them.
 */
export type DraftMarker = { readonly id: string; readonly no: string };

export function draftOf(row: InventoryShortfallRow): DraftMarker | null {
  const marked = row as InventoryShortfallRow & { draft_po_id?: string; draft_po_no?: string };
  if (marked.draft_po_id === undefined || marked.draft_po_id === "") return null;
  return { id: marked.draft_po_id, no: marked.draft_po_no ?? "" };
}

// ---- dates as the yard reads them -------------------------------------------------------------------------

function dayNumber(date: string): number | null {
  const m = date.match(/^(\d{4})-(\d{2})-(\d{2})/);
  return m === null ? null : Math.floor(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3])) / 86_400_000);
}

/**
 * A promised day as words against today (both YYYY-MM-DD, today in the reader's own day): "in 2 days",
 * "tomorrow", "today", "yesterday", "3 days ago". Further than sixty days either way it is left to the
 * date itself and this returns "".
 */
export function relativeDay(date: string, today: string): string {
  const a = dayNumber(date);
  const b = dayNumber(today);
  if (a === null || b === null) return "";
  const d = a - b;
  if (Math.abs(d) > 60) return "";
  if (d === 0) return "today";
  if (d === 1) return "tomorrow";
  if (d === -1) return "yesterday";
  return d > 0 ? `in ${d} days` : `${-d} days ago`;
}

/** What has landed against an order, as the register's small line: "6 of 10 pc landed". "" when nothing is known. */
export function landedWords(lines: readonly { qty_ordered_base: number; qty_received_base: number; uom: string }[]): string {
  if (lines.length === 0) return "";
  const uoms = new Set(lines.map((l) => l.uom));
  if (uoms.size !== 1) return "part landed";
  const ordered = lines.reduce((sum, l) => sum + l.qty_ordered_base, 0);
  const received = lines.reduce((sum, l) => sum + l.qty_received_base, 0);
  const unit = [...uoms][0] ?? "";
  const fmt = (n: number): string => n.toLocaleString("en-CA", { maximumFractionDigits: 3 });
  return `${fmt(received)} of ${fmt(ordered)} ${unit} landed`;
}

/** A promised day as the field's words: "Tuesday, 4 March 2031 (in 5 days)", "" when it is not a real date. */
export function dateWords(date: string, today: string): string {
  const check = validateExpectedDate(date);
  if (!check.ok) return "";
  const [y, m, d] = check.date.split("-").map(Number);
  const at = new Date(Date.UTC(y ?? 0, (m ?? 1) - 1, d ?? 1));
  // Written out by hand: a locale's idea of the comma differs between a browser and a test runner.
  const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
  const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
  const words = `${DAYS[at.getUTCDay()]} ${at.getUTCDate()} ${MONTHS[at.getUTCMonth()]} ${at.getUTCFullYear()}`;
  const rel = relativeDay(check.date, today);
  return rel === "" ? words : `${words} (${rel})`;
}
