// The receiving area's plain logic: the check made before goods are received (it mirrors the
// core's own, in its words, so a person reads the sentence once beside the field), the confirm
// steps that name what each press will do, the body of a landed cost attached to a receipt, and the
// words for a send, which is a successful request whose delivery can still have failed. Nothing
// here computes money; the server values every receipt and decides every figure.
//
// Port provenance: the receiving worklist and the receipt form follow the original app's
// receiving screens (repository hardscapeos_dibbits, commit f8f4973db317b92296ae8ec7e62a3f54f5dc82ab:
// inventory-receiving and the purchasing receiving panel); the words and the confirm steps are HH's.
import type {
  PurchasingAttachLandedCostRequest,
  PurchasingPOCosts,
  PurchasingPOSend,
  PurchasingReceiveRequest,
  PurchasingVendorContactResult,
  PurchasingWorklistItem,
  PurchasingWorklistItemPage,
} from "@hardscapehouse/api-client";
import { amountCents, BASES, basisWord } from "../landed/landed-logic.js";
import { formatMoney, formatQty, parseQty, parseRate } from "../purch/purch-money.js";
import type { Tone } from "../po/po-logic.js";

/** The receiving queue, which the core answers as {"items":null} when there is none. */
export function worklistRows(page: Partial<Omit<PurchasingWorklistItemPage, "items">> & { items?: PurchasingWorklistItem[] | null }): PurchasingWorklistItem[] {
  return page.items ?? [];
}

// ---- may goods be received against this order ----------------------------------------------------------

/** "" when goods may be received against an order in this state, else the core's sentence for it. */
export function receivable(status: string): string {
  switch (status) {
    case "ORDERED":
    case "PARTIAL":
      return "";
    case "DRAFT":
      return "Cannot receive against a draft purchase order: place the order first.";
    case "RECEIVED":
      return "This purchase order is already fully received.";
    case "CLOSED":
      return "This purchase order is closed.";
    case "CANCELLED":
      return "This purchase order was cancelled.";
    default:
      return `Cannot receive against a ${status.toLowerCase()} purchase order.`;
  }
}

// ---- the receipt ----------------------------------------------------------------------------------------------

interface OrderLine {
  readonly id: string;
  readonly line_no: number;
  readonly sku: string;
  readonly description: string;
  readonly qty_outstanding_base: number;
  readonly uom: string;
}

export interface ReceivedPick {
  readonly line: OrderLine;
  readonly qty: number;
}

export type ReceiveCheck =
  | { ok: true; body: PurchasingReceiveRequest; picked: ReceivedPick[] }
  | { ok: false; words: string };

// The core's epsilon on a line's outstanding balance (model.go qtyEpsilon).
const EPSILON = 1e-6;

const four = (n: number): string => n.toFixed(4);

/**
 * The counted quantities of an order's lines as the body the core expects. A blank line is left
 * alone; a figure must be a positive decimal and may not pass what is outstanding (the core's own
 * over receipt sentence, with both figures at four decimals). At least one line must be counted.
 */
export function receiveBody(counted: Readonly<Record<string, string>>, notes: string, po: { lines: readonly OrderLine[] }): ReceiveCheck {
  const picked: ReceivedPick[] = [];
  for (const line of po.lines) {
    const text = (counted[line.id] ?? "").trim();
    if (text === "") continue;
    const qty = parseQty(text);
    if (!qty.ok) return { ok: false, words: `Write how many ${line.sku} arrived, as a number above zero with four decimals at most.` };
    if (qty.value > line.qty_outstanding_base + EPSILON) {
      return { ok: false, words: `Over-receipt on line ${line.line_no}: ${four(qty.value)} requested but only ${four(line.qty_outstanding_base)} outstanding.` };
    }
    picked.push({ line, qty: qty.value });
  }
  if (picked.length === 0) return { ok: false, words: "Write how many arrived on at least one line." };
  const body: PurchasingReceiveRequest = { lines: picked.map((p) => ({ po_line_id: p.line.id, qty_base: p.qty })) };
  if (notes.trim() !== "") body.notes = notes.trim();
  return { ok: true, body, picked };
}

function listed(parts: readonly string[]): string {
  return parts.length <= 1 ? (parts[0] ?? "") : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
}

/** The step before goods move into stock: every line counted, the order, and what it does. */
export function receiveConfirmWords(po: { po_no: string }, picked: readonly ReceivedPick[]): string {
  const what = listed(picked.map((p) => `${formatQty(p.qty, p.line.uom)} ${p.line.description}`));
  return `Receive ${what} against ${po.po_no}. This moves the goods into stock at the order's cost and cannot be undone here.`;
}

// ---- a landed cost attached to a receipt ------------------------------------------------------------------

export interface AttachText {
  readonly freight: string;
  readonly duty: string;
  readonly handling: string;
  readonly currency: string;
  readonly fx: string;
  readonly basis: string;
}

export type AttachCheck = { ok: true; body: PurchasingAttachLandedCostRequest } | { ok: false; words: string };

/** The freight, duty and handling typed against a receipt, as cents through the decimal string. */
export function attachBody(form: AttachText): AttachCheck {
  const freight = amountCents(form.freight, "freight");
  if (!freight.ok) return freight;
  const duty = amountCents(form.duty, "duty");
  if (!duty.ok) return duty;
  const handling = amountCents(form.handling, "handling");
  if (!handling.ok) return handling;
  if (freight.cents === 0 && duty.cents === 0 && handling.cents === 0) {
    return { ok: false, words: "Write at least one of freight, duty or handling: a landed cost of nothing costs nothing." };
  }
  const usd = form.currency === "USD";
  const body: PurchasingAttachLandedCostRequest = {
    freight_cents: freight.cents,
    duty_cents: duty.cents,
    handling_cents: handling.cents,
    allocation_basis: BASES.find((b) => b === form.basis) ?? "VALUE",
    currency: usd ? "USD" : "CAD",
  };
  if (usd) {
    const rate = parseRate(form.fx);
    if (!rate.ok) return { ok: false, words: "Write the exchange rate as a positive number, six decimals at most, such as 1.352." };
    body.fx_rate = rate.value;
  }
  return { ok: true, body };
}

/** The step before a landed cost is applied to a receipt: each amount on its own, never added up. */
export function attachConfirmWords(grn: string, body: PurchasingAttachLandedCostRequest): string {
  const parts: string[] = [];
  if ((body.freight_cents ?? 0) > 0) parts.push(`freight ${formatMoney(body.freight_cents)}`);
  if ((body.duty_cents ?? 0) > 0) parts.push(`duty ${formatMoney(body.duty_cents)}`);
  if ((body.handling_cents ?? 0) > 0) parts.push(`handling ${formatMoney(body.handling_cents)}`);
  const usd = body.currency === "USD" ? " Amounts are in USD at the rate given; the core converts them." : "";
  return `Apply ${listed(parts)} to the goods on receipt ${grn}, ${basisWord(body.allocation_basis).toLowerCase()}. It is applied at once: it raises the cost of those goods and posts to the books.${usd} One landed cost per receipt; a later bill for the same truck is a late freight in Landed cost.`;
}

// ---- sending the order to the vendor -------------------------------------------------------------------------

/** True when the core refused a send because nobody is tagged and the fallback address needs a confirmation. */
export function fallbackAsk(refusal: string): boolean {
  return /confirm the fallback recipient/.test(refusal);
}

/** The step before an order leaves the building: the desk it goes to, or that nobody is tagged. */
export function sendConfirmWords(po: { po_no: string; vendor_name: string }, contact: PurchasingVendorContactResult | null): string {
  if (contact === null) return `Send ${po.po_no} to ${po.vendor_name}. The order and its prices go to the vendor by email and the attempt is kept on the record.`;
  if (!contact.tagged) {
    return `Nobody is tagged for ordering at ${po.vendor_name}. Send ${po.po_no} anyway and the core picks the address on the vendor record if there is exactly one; it refuses if there is none or several. The order and its prices go by email.`;
  }
  const name = contact.contact.contact_name === "" ? "" : `${contact.contact.contact_name}, `;
  const way = contact.contact.email === "" ? contact.contact.phone : contact.contact.email;
  return `Send ${po.po_no} to ${name}${way}. The order and its prices go to the vendor by email and the attempt is kept on the record.`;
}

/** What a send came to: the request succeeds even when the delivery failed, so the status is read. */
export function sendOutcome(send: Pick<PurchasingPOSend, "status" | "recipient" | "detail">): { tone: Tone; words: string } {
  if (send.status === "DELIVERED") return { tone: "good", words: `Sent to ${send.recipient}.` };
  if (send.status === "QUEUED") return { tone: "watch", words: `Queued for ${send.recipient}: not yet delivered.` };
  const why = send.detail === undefined || send.detail === "" ? "" : `: ${send.detail}`;
  return { tone: "stop", words: `Not delivered to ${send.recipient}${why}. The attempt is on the record as failed.` };
}

// ---- cost and the document ---------------------------------------------------------------------------------------

/** The core withholds cost from a role the purchasing policy hides it from, by leaving the keys out. */
export function costsHidden(costs: Pick<PurchasingPOCosts, "subtotal_cents" | "subtotal_cad_cents">): boolean {
  return costs.subtotal_cents === undefined && costs.subtotal_cad_cents === undefined;
}

/** A downloaded document keeps the order's own name, safe as a file name. */
export function documentFileName(poNo: string, ext: "csv" | "html"): string {
  return `${poNo.replace(/[^A-Za-z0-9._-]+/g, "-")}.${ext}`;
}
