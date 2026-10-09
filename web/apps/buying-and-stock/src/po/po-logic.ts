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
  PARTIAL: "Part received",
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

/** What each walled move is, and who may do it (the decided matrix, row 457). */
const WHO_MAY: Record<string, string> = {
  order:
    "Drafting, changing and placing purchase orders is for the Procurement Manager. Other roles can read what is on order and when it is due.",
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
