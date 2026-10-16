// The customer lines read, validated field by field before it becomes state (the till's discipline:
// an answer that is not a readable list of orders is a refusal in words, never a half rendered
// screen). Every figure is the server's integer cents or its own quantity; nothing here computes one.

export type ShelfState = "NOT_FULFILLED" | "PARTIALLY_FULFILLED" | "FULFILLED";
export type OrderSource = "TILL" | "QUOTE";

const SHELF_STATES: readonly ShelfState[] = ["NOT_FULFILLED", "PARTIALLY_FULFILLED", "FULFILLED"];
const SOURCES: readonly OrderSource[] = ["TILL", "QUOTE"];

/** One line of one order, as the customer lines read answers it. */
export interface CustomerLine {
  orderLineId: string;
  productId: string;
  sku: string;
  description: string;
  qtySold: number;
  qtyReturned: number;
  /** What the commit guard would still accept: the one quantity a draft is capped at. */
  qtyOpen: number;
  unitPriceCents: number;
  /**
   * The product's unit cost in integer cents (the dealer's costing basis, NOT the sell price). Walk
   * change 6 item 4 feeds the manufacturer defect threshold sentence from this figure (it
   * multiplies the picked quantity by it). The customer's lines read leaves it absent when the
   * server's line row has no unit cost on it yet (older rows, manual entries); the drawer then
   * refuses the sentence rather than guess.
   */
  unitCostCents: number | null;
  lineTotalCents: number;
  taxCents: number;
  moneyReturnedCents: number;
  taxReturnedCents: number;
  shelfState: ShelfState;
}

/** One order the customer holds. */
export interface CustomerOrder {
  orderId: string;
  orderNo: string;
  branchId: string;
  /** The branch's name, so the one branch refusal can name the branches. */
  branchName: string;
  createdAt: string;
  /** The job address, else the job reference, else the PO reference; empty when the order has none. */
  jobLabel: string;
  source: OrderSource;
  saleType: string;
  tender: string;
  fulfilmentState: string;
  invoiceId: string;
  cardRefundable: boolean;
  /** False when the server says this order cannot be returned yet, with its reason in words (the contract's `returnable`, `not_returnable_reason`). */
  returnable: boolean;
  notReturnableReason: string;
  lines: CustomerLine[];
}

function record(raw: unknown): Record<string, unknown> | null {
  return raw !== null && typeof raw === "object" && !Array.isArray(raw) ? (raw as Record<string, unknown>) : null;
}

function text(value: unknown): string | null {
  return typeof value === "string" ? value : null;
}

function filled(value: unknown): string | null {
  return typeof value === "string" && value !== "" ? value : null;
}

function quantity(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : null;
}

function cents(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) ? value : null;
}

function readLine(raw: unknown): CustomerLine | null {
  const r = record(raw);
  if (r === null) return null;
  const orderLineId = filled(r["order_line_id"]);
  const productId = filled(r["product_id"]);
  const sku = text(r["sku"]);
  const description = text(r["description"]);
  const qtySold = quantity(r["qty_sold"]);
  const qtyReturned = quantity(r["qty_returned"]);
  const qtyOpen = quantity(r["qty_open"]);
  const unitPriceCents = cents(r["unit_price_cents"]);
  // unit_cost_cents is OPTIONAL on the wire (older rows lack it); ABSENT stays null so the drawer
  // can refuse its sentence rather than guess at zero. The contract enforces the field on new
  // lines, so null is a legacy shape, never a fresh answer. A PRESENT but unsafe value (non
  // integer, negative, or non numeric) refuses the whole read: a silently rounded figure is the
  // exact defect the threshold sentence exists to prevent.
  const rawUnitCost = r["unit_cost_cents"];
  let unitCostCents: number | null;
  if (rawUnitCost === undefined) {
    unitCostCents = null;
  } else if (typeof rawUnitCost === "number" && Number.isSafeInteger(rawUnitCost) && rawUnitCost >= 0) {
    unitCostCents = rawUnitCost;
  } else {
    return null;
  }
  const lineTotalCents = cents(r["line_total_cents"]);
  const taxCents = cents(r["tax_cents"]);
  const moneyReturnedCents = cents(r["money_returned_cents"]);
  const taxReturnedCents = cents(r["tax_returned_cents"]);
  const shelfState = r["shelf_state"];
  if (orderLineId === null || productId === null || sku === null || description === null) return null;
  if (qtySold === null || qtyReturned === null || qtyOpen === null) return null;
  if (unitPriceCents === null || lineTotalCents === null || taxCents === null) return null;
  if (moneyReturnedCents === null || taxReturnedCents === null) return null;
  if (typeof shelfState !== "string" || !SHELF_STATES.includes(shelfState as ShelfState)) return null;
  return {
    orderLineId,
    productId,
    sku,
    description,
    qtySold,
    qtyReturned,
    qtyOpen,
    unitPriceCents,
    unitCostCents,
    lineTotalCents,
    taxCents,
    moneyReturnedCents,
    taxReturnedCents,
    shelfState: shelfState as ShelfState,
  };
}

function readOrder(raw: unknown): CustomerOrder | null {
  const r = record(raw);
  if (r === null) return null;
  const orderId = filled(r["order_id"]);
  const orderNo = filled(r["order_no"]);
  const branchId = filled(r["branch_id"]);
  const branchName = text(r["branch_name"]);
  const createdAt = filled(r["created_at"]);
  const jobLabel = text(r["job_label"]);
  const source = r["source"];
  const saleType = text(r["sale_type"]);
  const tender = text(r["tender"]);
  const fulfilmentState = text(r["fulfilment_state"]);
  const invoiceId = text(r["invoice_id"]);
  const cardRefundable = r["card_refundable"];
  const rawLines = r["lines"];
  const returnable = r["returnable"];
  const reasonText = text(r["not_returnable_reason"]);
  if (orderId === null || orderNo === null || branchId === null || branchName === null || createdAt === null) return null;
  if (jobLabel === null || saleType === null || tender === null || fulfilmentState === null) return null;
  if (invoiceId === null || typeof cardRefundable !== "boolean" || !Array.isArray(rawLines)) return null;
  if (typeof returnable !== "boolean" || reasonText === null) return null;
  // A blocked converted order has no live invoice, so its invoice id is empty; a returnable one always names it.
  if (invoiceId === "" && returnable) return null;
  if (typeof source !== "string" || !SOURCES.includes(source as OrderSource)) return null;
  const lines: CustomerLine[] = [];
  for (const entry of rawLines) {
    const line = readLine(entry);
    if (line === null) return null;
    lines.push(line);
  }
  return {
    orderId,
    orderNo,
    branchId,
    branchName,
    createdAt,
    jobLabel,
    source: source as OrderSource,
    saleType,
    tender,
    fulfilmentState,
    invoiceId,
    cardRefundable,
    returnable,
    notReturnableReason:
      returnable ? "" : reasonText !== null && reasonText !== "" ? reasonText : "This order cannot be returned from here yet.",
    lines,
  };
}

/** The customer's orders newest first as the server answered them, or null when any part is unreadable. */
export function readCustomerLines(raw: unknown): CustomerOrder[] | null {
  const r = record(raw);
  if (r === null || !Array.isArray(r["items"])) return null;
  const orders: CustomerOrder[] = [];
  for (const entry of r["items"]) {
    const order = readOrder(entry);
    if (order === null) return null;
    orders.push(order);
  }
  return orders;
}

/**
 * The sales order number as a filter inside the customer's line list, and nothing more: it hides
 * rows, it never searches for a sale and never changes a draft. The contract's line read carries
 * the order number alone (no separate receipt number), so that is what the term matches.
 */
export function filterOrders(orders: readonly CustomerOrder[], term: string): CustomerOrder[] {
  const wanted = term.trim().toLowerCase();
  if (wanted === "") return [...orders];
  return orders.filter((order) => order.orderNo.toLowerCase().includes(wanted));
}
