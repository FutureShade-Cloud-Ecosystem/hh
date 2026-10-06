// Ported from hardscapeos_dibbits master 87d7941b app/src/_related-docs.ts;
// adapted for HH.
//
// The related documents panel's pure logic (QD-5): the words and the shape
// decisions of a quote's document family, DOM-free so the Node runner can
// drive it. The component (related-docs-panel.ts) renders; it does not judge.
//
// THE SHAPE. GET /api/v1/{quotes|orders}/{id}/related-documents answers
// {items: [...]} of normalized rows: kind / id / number / status / date /
// total_cents, plus parent_id (the lineage edge), origin and accept_seq (the
// acceptance provenance on orders) and self marking the document the caller
// asked from. The client proves every field before a row reaches this file
// (qd-client.ts), so a row here is a row the server sent; nothing filters or
// defaults at this layer.
//
// The panel is a compact LIST, not a tree: the ruling the ERP's DIB-491 item
// carried (data correctness and a clean list now, polish later) is the ruling
// this port keeps. HH changes: the house dash rule (no em or en dash anywhere)
// and the empty state's words match the desk's own voice.
import { quoteStatusWord } from "./_quote-edit.js";
import type { Result } from "@hardscapehouse/api-client";
import type { QdClient } from "./qd-client.js";

/**
 * The seam's one family read per read cycle. The related documents panel and the order payment
 * panel both need the quote's document family, and a read apiece on every quote open answered
 * twice (the round 1 and round 2 reviews' duplicate read). The share hands both callers of a
 * cycle the SAME read, keyed by the quote it was asked on; a new cycle begins with beginCycle(),
 * so a later cycle reads fresh and never joins an older answer. A cycle begins at every quote
 * switch and edit fan out (the edit panel begins it: it is the seam host that re-reads both
 * panels), at a new session (each panel begins one when its client changes), and at the payment
 * panel's own retry of its read. The read answers per caller through that caller's guardedRead,
 * so an answer that lands after the person left the quote is dropped for the quote now open, and
 * an older read never lands over a newer one, exactly as before the share: the read outlives a
 * quote switch as a request, and its answer is refused where the quote id moved.
 */
export class FamilyShare {
  private generation = 0;
  private quoteId = "";
  private live: Promise<Result<RelatedDoc[]>> | null = null;

  /** A new read cycle: the next read of a quote is fresh, whatever older cycles answered. */
  beginCycle(): void {
    this.generation += 1;
    this.quoteId = "";
    this.live = null;
  }

  /** The cycle's one read of the quote's family (the quote end only; an order end read is its own). */
  read(client: QdClient, quoteId: string, kind: "quote" | "order"): Promise<Result<RelatedDoc[]>> {
    if (kind !== "quote") return client.relatedDocuments(quoteId, kind);
    if (this.live !== null && this.quoteId === quoteId) return this.live;
    this.quoteId = quoteId;
    this.live = client.relatedDocuments(quoteId, kind);
    return this.live;
  }
}

/** The one share the seam's two family readers use. */
export const familyShare = new FamilyShare();

/** One row of a document family, as the client proved it off the wire. */
export interface RelatedDoc {
  kind: string; // QUOTE | ORDER | INVOICE
  id: string;
  number: string;
  status: string;
  date: string; // ISO timestamp, when the document came to exist
  totalCents: number;
  parentId?: string;
  origin?: string; // PORTAL | COUNTER, absent on documents that predate provenance
  acceptSeq?: number;
  self?: boolean;
}

/** The human word for a document kind. An unknown kind renders as itself rather than lying. */
export function kindLabel(kind: string): string {
  switch (kind) {
    case "QUOTE":
      return "Quote";
    case "ORDER":
      return "Order";
    case "INVOICE":
      return "Invoice";
    default:
      return kind;
  }
}

/**
 * The human phrase for an order's provenance door: client safe words with no
 * internal vocabulary. "" for an absent or unknown origin, because inventing
 * a door would be worse than saying nothing.
 */
export function originLabel(origin: string | undefined): string {
  switch (origin) {
    case "PORTAL":
      return "accepted online";
    case "COUNTER":
      return "created at the counter";
    default:
      return "";
  }
}

/**
 * What an invoice's states are CALLED, as against what the column stores.
 * The receivables register's own pills (Draft, Open, Part paid, Paid, Void)
 * are the one vocabulary this desk shows for invoices, so the family list
 * reuses them rather than minting a second set for the same states.
 * FINALIZED reads "Open" because that is the state of an invoice that has
 * been issued and is owing.
 */
const INVOICE_STATUS_LABEL: Record<string, string> = {
  DRAFT: "Draft",
  FINALIZED: "Open",
  PARTIALLY_PAID: "Part paid",
  PAID: "Paid",
  VOID: "Void",
};

/** An invoice's state in counter words, degrading honestly (see docStatusWord). */
function invoiceStatusWord(status: string): string {
  return INVOICE_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, " ");
}

/**
 * An order's own states (the orders table's CHECK: DRAFT, CONFIRMED, PICKING,
 * READY, PARTIALLY_FULFILLED, FULFILLED, INVOICED, CLOSED, CANCELLED) in
 * counter words. Unmapped states degrade honestly like every other kind.
 */
const ORDER_STATUS_LABEL: Record<string, string> = {
  DRAFT: "Draft",
  CONFIRMED: "Confirmed",
  PICKING: "Picking",
  READY: "Ready",
  PARTIALLY_FULFILLED: "Partly fulfilled",
  FULFILLED: "Fulfilled",
  INVOICED: "Invoiced",
  CLOSED: "Closed",
  CANCELLED: "Cancelled",
};

/**
 * The three vocabularies this panel lists side by side, one per document
 * kind. A family is the one place where three lifecycles show together, and
 * the same spelling is not the same fact across them: a quote's DRAFT, an
 * order's DRAFT and an invoice's DRAFT are three different documents' first
 * states. Quotes keep their own word (the edit panel's), orders and invoices
 * carry theirs here because no other surface of this desk renders them.
 */
const DOC_STATUS_WORD: Record<string, (status: string) => string> = {
  QUOTE: quoteStatusWord,
  ORDER: (status) => ORDER_STATUS_LABEL[status] ?? status.toLowerCase().replace(/_/g, " "),
  INVOICE: invoiceStatusWord,
};

/**
 * One row's status in the vocabulary of the KIND that row is. Both arms
 * degrade honestly: an unknown KIND is not guessed at as an invoice, and an
 * unmapped STATUS comes back as itself (lowercased, underscores opened out)
 * rather than as an invented phrase, so a state added server side reads as
 * strange instead of as a confident lie. Never "Unknown", never "".
 */
export function docStatusWord(kind: string, status: string): string {
  const word = DOC_STATUS_WORD[kind];
  return word !== undefined ? word(status) : status.toLowerCase().replace(/_/g, " ");
}

/** The chip's main text: "Order SO-1041 · Draft". The number is the identity; the status rides beside it. */
export function chipText(d: RelatedDoc): string {
  return `${kindLabel(d.kind)} ${d.number} · ${docStatusWord(d.kind, d.status)}`;
}

/**
 * The chip's secondary line: the day, then the provenance where it exists,
 * then "acceptance N" for a repeat (N of 2 or more). The first acceptance is
 * the ordinary case and numbering it would be noise.
 */
export function chipDetail(d: RelatedDoc): string {
  const parts: string[] = [];
  const day = docDay(d.date);
  if (day !== "") parts.push(day);
  const door = originLabel(d.origin);
  if (door !== "") parts.push(door);
  if ((d.acceptSeq ?? 0) >= 2) parts.push(`acceptance ${d.acceptSeq}`);
  return parts.join(" · ");
}

/** The date, day grained (en-CA gives YYYY-MM-DD, the app's other timestamps). "" when unreadable. */
export function docDay(iso: string): string {
  if (iso === "") return "";
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? "" : t.toLocaleDateString("en-CA");
}

/**
 * Whether a chip is a LINK in this host. openKinds is the space separated
 * list of kinds the hosting surface can actually open (the ERP's builder
 * opens quotes; the counter opens orders). A chip that dispatched an event
 * nobody handles is a button that does nothing, which is worse than a label,
 * and the self row is never a link: it points at the document the reader is
 * already reading.
 */
export function isOpenable(d: RelatedDoc, openKinds: string): boolean {
  if (d.self === true) return false;
  return openKinds
    .split(/\s+/)
    .filter(Boolean)
    .includes(d.kind);
}

/**
 * The empty state sentence for a family of one (the document itself). Empty
 * is never an error, and it says what would make the list grow, because an
 * empty panel that explains itself saves the "is this broken?" question.
 */
export function familyEmptyText(kind: string): string {
  if (kind === "ORDER") return "No related documents yet: nothing else is linked to this order.";
  return "No related documents yet: converting this quote to an order links them here.";
}

/** True when the family holds nothing beyond the document asked from. */
export function familyIsOnlySelf(rows: RelatedDoc[]): boolean {
  return rows.length <= 1;
}
