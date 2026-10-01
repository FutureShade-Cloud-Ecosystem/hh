// The returns flow: the framework-free state machine behind the till's
// returns desk (the house invariant: the UI layer is thin and renders this
// state; nothing Lit-shaped lives here). The core's routes are the authority
// for every fact this flow holds: the lookup says which lines still have
// something to return, the commit re-guards everything server side, and no
// figure on the till's screen is computed here. What this flow adds to the
// wire's facts is only the draft's own choices: which lines, how many, and
// which refund method.
//
// Every server answer is validated field by field before it becomes state
// (the demo adapter's discipline, carried): an answer that is not a readable
// sale is a refusal in words, never a half-rendered screen.
import { staffPos, type ApiClient, type PosReturnLookup } from "@hardscapehouse/api-client";

/** How the original sale was paid, as the lookup's tender field spells it. */
export type SaleTender = "ACCOUNT" | "CASH" | "CARD" | "CHEQUE";

const TENDERS: readonly SaleTender[] = ["ACCOUNT", "CASH", "CARD", "CHEQUE"];

/** The lookup's shape this flow holds, as the views read it. */
export interface ReturnableLine {
  orderLineId: string;
  description: string;
  qtySold: number;
  qtyReturned: number;
  unitPriceCents: number;
  lineTotalCents: number;
}

export interface SaleLookup {
  orderId: string;
  orderNo: string;
  branchId: string;
  saleTender: SaleTender;
  cardRefundable: boolean;
  lines: ReturnableLine[];
}

export interface ReturnsFlowOptions {
  /** The clerk's client: the till's own session rides its header provider. */
  client: ApiClient;
  /** Called after every state change, so the host re-renders. */
  onChange: () => void;
}

export class ReturnsFlow {
  readonly #client: ApiClient;
  readonly #onChange: () => void;

  /** The sale number input's live value, so a re-render keeps what is typed. */
  orderNo = "";
  /** True while the lookup is in flight: the find control goes disabled. */
  finding = false;
  /** A refusal in words, shown beside the desk. */
  notice: string | null = null;
  /** The found sale, or null while idle. */
  lookup: SaleLookup | null = null;

  constructor(options: ReturnsFlowOptions) {
    this.#client = options.client;
    this.#onChange = options.onChange;
  }

  get busy(): boolean {
    return this.finding;
  }

  /** The find control is dead while the input is blank, not merely faded. */
  get canFind(): boolean {
    return !this.busy && this.orderNo.trim() !== "";
  }

  setOrderNo(value: string): void {
    this.orderNo = value;
    this.#onChange();
  }

  /** Find the sale by its number: the branch fenced lookup route. */
  async findSale(): Promise<void> {
    if (!this.canFind) return;
    const orderNo = this.orderNo.trim();
    this.finding = true;
    this.notice = null;
    this.#onChange();
    const result = await staffPos.posLookupReturn(this.#client, { order_no: orderNo });
    this.finding = false;
    if (!result.ok) {
      this.#refuseLookup(orderNo, result);
      return;
    }
    const lookup = validateLookup(result.value);
    if (lookup === null) {
      this.lookup = null;
      this.notice = "The answer about this sale was not a readable sale: nothing is shown rather than half of one.";
      this.#onChange();
      return;
    }
    this.lookup = lookup;
    this.notice = null;
    this.#onChange();
  }

  #refuseLookup(orderNo: string, result: { error: string; status?: number }): void {
    this.lookup = null;
    this.notice =
      result.status === 404
        ? `No sale numbered ${orderNo} answers at this counter: check the number and try again.`
        : `The lookup was refused: ${result.error}`;
    this.#onChange();
  }

  #touch(): void {
    this.#onChange();
  }
}

/** Field by field, never a cast: the lookup this flow holds, or null. */
function validateLookup(raw: PosReturnLookup): SaleLookup | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const orderId = record["order_id"];
  const orderNo = record["order_no"];
  const branchId = record["branch_id"];
  const tender = record["sale_tender"];
  const cardRefundable = record["card_refundable"];
  if (typeof orderId !== "string" || orderId === "") return null;
  if (typeof orderNo !== "string" || orderNo === "") return null;
  if (typeof branchId !== "string" || branchId === "") return null;
  if (typeof tender !== "string" || !TENDERS.includes(tender as SaleTender)) return null;
  if (typeof cardRefundable !== "boolean") return null;
  const lines = validateLines(record["lines"]);
  if (lines === null) return null;
  return {
    orderId,
    orderNo,
    branchId,
    saleTender: tender as SaleTender,
    cardRefundable,
    lines,
  };
}

function validateLines(raw: unknown): ReturnableLine[] | null {
  if (!Array.isArray(raw)) return null;
  const lines: ReturnableLine[] = [];
  for (const entry of raw) {
    if (entry === null || typeof entry !== "object" || Array.isArray(entry)) return null;
    const line = entry as Record<string, unknown>;
    const orderLineId = line["order_line_id"];
    const description = line["description"];
    const qtySold = line["qty_sold"];
    const qtyReturned = line["qty_returned"];
    const unitPriceCents = line["unit_price_cents"];
    const lineTotalCents = line["line_total_cents"];
    if (typeof orderLineId !== "string" || orderLineId === "") return null;
    if (typeof description !== "string") return null;
    if (typeof qtySold !== "number" || !Number.isFinite(qtySold)) return null;
    if (typeof qtyReturned !== "number" || !Number.isFinite(qtyReturned)) return null;
    if (typeof unitPriceCents !== "number" || !Number.isSafeInteger(unitPriceCents)) return null;
    if (typeof lineTotalCents !== "number" || !Number.isSafeInteger(lineTotalCents)) return null;
    lines.push({
      orderLineId,
      description,
      qtySold,
      qtyReturned,
      unitPriceCents,
      lineTotalCents,
    });
  }
  return lines;
}
