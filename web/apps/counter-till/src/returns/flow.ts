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
import {
  staffPayment,
  staffPos,
  staffTill,
  type ApiClient,
  type PosRefundApproval,
  type PosReturn,
  type PosReturnInput,
  type PosReturnLineInput,
  type PosReturnLookup,
} from "@hardscapehouse/api-client";
import type { RefundApprovalCapture } from "./capture.js";

/** How the original sale was paid, as the lookup's tender field spells it. */
export type SaleTender = "ACCOUNT" | "CASH" | "CARD" | "CHEQUE";

const TENDERS: readonly SaleTender[] = ["ACCOUNT", "CASH", "CARD", "CHEQUE"];

/** The refund rails the contract's return input carries. */
export type RefundMethod = "CASH" | "CARD" | "ACCOUNT_CREDIT";
const REFUND_METHODS: readonly RefundMethod[] = ["CASH", "CARD", "ACCOUNT_CREDIT"];

/** The per line reason codes the contract spells. */
export type ReasonCode = PosReturnLineInput["reason_code"];
const REASON_CODES: readonly ReasonCode[] = ["DAMAGED", "DEFECT", "WRONG_ITEM", "NO_LONGER_NEEDED", "OTHER"];

/** The drawer at the sale's branch, as the desk's cash method needs it. */
export type DrawerState = { kind: "unknown" } | { kind: "open"; sessionId: string } | { kind: "closed" };

/** What the desk keeps of a committed return, for the receipt's words. */
export interface RefundReceipt {
  returnNo: string;
  method: RefundMethod;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  replayed: boolean;
  /** The approver's name when a manager approved this return; null when the caller's own answer committed it. */
  approverName: string | null;
  /** The clerk the till is signed in as, or null when the session carries no name. */
  clerkName: string | null;
  /** The simulator's own flag at the time of the refund; null when the status route has not answered. */
  cardSimulated: boolean | null;
}

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
  /** The clerk the till is signed in as, read at receipt time. */
  clerkName: () => string | null;
  /** The wrapped fetch's reader for the refusal body's approval. */
  capture: RefundApprovalCapture;
  /** Called after every state change, so the host re-renders. */
  onChange: () => void;
}

export class ReturnsFlow {
  readonly #client: ApiClient;
  readonly #clerkName: () => string | null;
  readonly #capture: RefundApprovalCapture;
  readonly #onChange: () => void;

  /** The sale number input's live value, so a re-render keeps what is typed. */
  orderNo = "";
  /** True while the lookup is in flight: the find control goes disabled. */
  finding = false;
  /** A refusal in words, shown beside the desk. */
  notice: string | null = null;
  /** The found sale, or null while idle. */
  lookup: SaleLookup | null = null;
  /** The drawer at the sale's branch, asked once the sale is found. */
  drawer: DrawerState = { kind: "unknown" };
  /** The card rail's simulator flag from the status route; null until it answers. */
  cardSimulated: boolean | null = null;
  /** The chosen refund method, until the clerk picks one. */
  method: RefundMethod | null = null;
  /** The draft's quantity per order line id, whole units only in this desk. */
  quantities: Record<string, number> = {};
  reasonCode: ReasonCode = "OTHER";
  /** The clerk's free words about the return, sent verbatim as the return's reason. */
  note = "";
  restock = true;
  /** True while the return is on the wire: the desk's controls go disabled. */
  submitting = false;
  /** The committed return, when one is. */
  receipt: RefundReceipt | null = null;
  /**
   * The approval a 409 named: the dealer's amount and the roles that may
   * approve. The desk shows both, and the approval's refusal never carries a
   * figure of its own: the amount named is the dealer's threshold, exactly
   * what the server said.
   */
  refusal: PosRefundApproval | null = null;

  constructor(options: ReturnsFlowOptions) {
    this.#client = options.client;
    this.#clerkName = options.clerkName;
    this.#capture = options.capture;
    this.#onChange = options.onChange;
  }

  get busy(): boolean {
    return this.finding;
  }

  /** The find control is dead while the input is blank, not merely faded. */
  get canFind(): boolean {
    return !this.busy && this.orderNo.trim() !== "";
  }

  /** A method is pickable, or it is greyed with its reason in words nearby. */
  methodAvailable(method: RefundMethod): boolean {
    if (this.lookup === null) return false;
    if (method === "CASH") return this.drawer.kind === "open";
    if (method === "CARD") return this.lookup.cardRefundable;
    return true;
  }

  /** The line's drafted quantity, whole units, never below nothing. */
  qtyOf(orderLineId: string): number {
    return this.quantities[orderLineId] ?? 0;
  }

  get hasDraftedLines(): boolean {
    return Object.values(this.quantities).some((qty) => qty > 0);
  }

  get canSubmit(): boolean {
    return (
      this.lookup !== null &&
      this.method !== null &&
      this.hasDraftedLines &&
      !this.submitting &&
      (this.method !== "CASH" || this.drawer.kind === "open")
    );
  }

  setOrderNo(value: string): void {
    this.orderNo = value;
    this.#onChange();
  }

  setQty(orderLineId: string, value: string): void {
    const parsed = Number(value);
    const qty = Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
    if (qty === 0) delete this.quantities[orderLineId];
    else this.quantities[orderLineId] = qty;
    this.#draftChanged();
  }

  chooseMethod(method: string): void {
    if (!REFUND_METHODS.includes(method as RefundMethod)) return;
    if (!this.methodAvailable(method as RefundMethod)) return;
    this.method = method as RefundMethod;
    this.#draftChanged();
  }

  setReasonCode(value: string): void {
    if (!REASON_CODES.includes(value as ReasonCode)) return;
    this.reasonCode = value as ReasonCode;
    this.#draftChanged();
  }

  setNote(value: string): void {
    this.note = value;
    this.#draftChanged();
  }

  setRestock(value: boolean): void {
    this.restock = value;
    this.#draftChanged();
  }

  /**
   * A changed draft is a different return: its pending idempotency key and
   * the refusal the last send earned belong to the draft that earned them,
   * so both go, and the next send is a fresh one.
   */
  #draftChanged(): void {
    this.refusal = null;
    this.#pendingKey = null;
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
    this.#resetDraft();
    this.#onChange();
    // The desk's side reads, in words on the screen when they fail: the
    // drawer the cash method pays from, and the card rail's simulator flag
    // that decides whether the card method may call itself simulated.
    await this.#readDrawer(lookup.branchId);
    await this.#readPaymentsStatus();
    this.#onChange();
  }

  /** Commit the return once. The server's answer is the receipt's facts. */
  async submit(): Promise<void> {
    if (!this.canSubmit || this.lookup === null || this.method === null) return;
    const input = this.#buildInput();
    if (input === null) return;
    this.submitting = true;
    this.notice = null;
    this.#onChange();
    const result = await staffPos.posCreateReturn(this.#client, input);
    this.submitting = false;
    if (!result.ok) {
      if (result.status === 409 && result.error === "refund_approval_required") {
        // The contract says a client discriminates on the error string, never
        // on the key's presence: the approval rides the body this transport's
        // capture kept, or the desk says the refusal without its figures.
        const approval = this.#capture.take();
        if (approval !== null) {
          this.refusal = approval;
          this.#onChange();
          return;
        }
        this.notice = "The refund needs a manager whose role holds the approval, but the refusal did not say which: try again or call the desk lead.";
        this.#onChange();
        return;
      }
      this.notice = `The refund was refused: ${result.error}`;
      this.#onChange();
      return;
    }
    const committed = validateReturn(result.value);
    if (committed === null) {
      this.notice = "The refund's answer was not a readable return: the receipt waits rather than guessing.";
      this.#onChange();
      return;
    }
    const clerk = this.#clerkName();
    this.receipt = {
      returnNo: committed.returnNo,
      method: committed.method,
      subtotalCents: committed.subtotalCents,
      taxCents: committed.taxCents,
      totalCents: committed.totalCents,
      replayed: committed.replayed,
      approverName: null,
      clerkName: clerk !== null && clerk !== "" ? clerk : null,
      cardSimulated: this.method === "CARD" ? this.cardSimulated : null,
    };
    this.#onChange();
  }

  /** Back to the blank desk: the receipt's story is told, the next one starts. The sale number stays typed: the next lookup is often the same sale. */
  startAnotherReturn(): void {
    this.lookup = null;
    this.receipt = null;
    this.notice = null;
    this.method = null;
    this.#resetDraft();
    this.#onChange();
  }

  /**
   * The one return input the desk sends, and re-sends unchanged when a
   * manager approves it: the idempotency key is minted once per drafted
   * return, so the approval's send is the same send the server may treat as
   * a replay if the clerk's own attempt landed unheard. A changed draft
   * drops the pending key: a different return is a different send.
   */
  #pendingKey: string | null = null;

  #buildInput(): PosReturnInput | null {
    if (this.lookup === null || this.method === null) return null;
    if (this.#pendingKey === null) this.#pendingKey = crypto.randomUUID();
    const lines: PosReturnLineInput[] = [];
    for (const line of this.lookup.lines) {
      const qty = this.quantities[line.orderLineId] ?? 0;
      if (qty <= 0) continue;
      lines.push({ order_line_id: line.orderLineId, qty_base: qty, reason_code: this.reasonCode });
    }
    if (lines.length === 0) return null;
    return {
      idempotency_key: this.#pendingKey,
      order_id: this.lookup.orderId,
      order_no: this.lookup.orderNo,
      lines,
      refund_method: this.method,
      reason: this.note,
      restock: this.restock,
      till_session_id: this.drawer.kind === "open" ? this.drawer.sessionId : "",
    };
  }

  #resetDraft(): void {
    this.quantities = {};
    this.method = null;
    this.reasonCode = "OTHER";
    this.note = "";
    this.restock = true;
    this.drawer = { kind: "unknown" };
    this.cardSimulated = null;
    this.refusal = null;
    this.#pendingKey = null;
  }

  async #readDrawer(branchId: string): Promise<void> {
    const result = await staffTill.tillLookupOpenSession(this.#client, { branch_id: branchId });
    if (!result.ok) {
      this.drawer = { kind: "unknown" };
      return;
    }
    const open = result.value.open === true;
    const sessionId = result.value.id;
    this.drawer = open && typeof sessionId === "string" && sessionId !== "" ? { kind: "open", sessionId } : { kind: "closed" };
  }

  async #readPaymentsStatus(): Promise<void> {
    const result = await staffPayment.paymentGetConfig(this.#client);
    this.cardSimulated = result.ok && typeof result.value.simulated === "boolean" ? result.value.simulated : null;
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

/** Field by field, never a cast: the committed return the receipt spells, or null. */
function validateReturn(raw: PosReturn): {
  returnNo: string;
  method: RefundMethod;
  subtotalCents: number;
  taxCents: number;
  totalCents: number;
  replayed: boolean;
} | null {
  if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const returnNo = record["return_no"];
  const method = record["refund_method"];
  const subtotalCents = record["subtotal_cents"];
  const taxCents = record["tax_cents"];
  const totalCents = record["total_cents"];
  const replayed = record["replayed"];
  const status = record["status"];
  if (typeof returnNo !== "string" || returnNo === "") return null;
  if (typeof method !== "string" || !REFUND_METHODS.includes(method as RefundMethod)) return null;
  if (typeof subtotalCents !== "number" || !Number.isSafeInteger(subtotalCents)) return null;
  if (typeof taxCents !== "number" || !Number.isSafeInteger(taxCents)) return null;
  if (typeof totalCents !== "number" || !Number.isSafeInteger(totalCents)) return null;
  if (typeof replayed !== "boolean") return null;
  if (status !== "COMMITTED") return null;
  return {
    returnNo,
    method: method as RefundMethod,
    subtotalCents,
    taxCents,
    totalCents,
    replayed,
  };
}
