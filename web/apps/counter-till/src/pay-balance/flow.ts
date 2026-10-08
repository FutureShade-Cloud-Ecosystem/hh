// The Pay balance flow (item 9.7): the framework-free state behind the till's Pay balance surface.
// The core's routes are the authority for every fact held here: the find says which orders owe and
// how much, the take re-guards everything (the whole balance only, one capture per key). No figure
// on the screen is computed here; this holds the person's choices (the order, the tender) and the
// server's answers, field by field validated before they become state.
import { staffOrder, staffPos, staffTill, type ApiClient } from "@hardscapehouse/api-client";
import { readPayHold, type PayHold } from "./hold.js";
import type { PayBalanceCapture } from "./capture.js";
import {
  payBalanceRefusalWords,
  readOwingOrders,
  readTaken,
  type OwingOrder,
  type PayMethod,
  type TakenBalance,
} from "./words.js";

/** The drawer the take lands in: the branch's open till session. */
export type PayDrawer = { kind: "unknown" } | { kind: "open"; sessionId: string } | { kind: "closed" };

/** The card token the sale step sends today (the simulated terminal); one path, never a second. */
const CARD_TOKEN = "tok_simulated_runner";

export interface PayBalanceFlowOptions {
  client: ApiClient;
  /** The wrapped fetch's reader for the refusal body's money fields. */
  capture: PayBalanceCapture;
  /** The branch this login reaches, read live; null before it resolves. */
  branchId: () => string | null;
  onChange: () => void;
}

export class PayBalanceFlow {
  readonly #client: ApiClient;
  readonly #capture: PayBalanceCapture;
  readonly #branchId: () => string | null;
  readonly #onChange: () => void;

  /** The find field's live value, so a re-render keeps what is typed. */
  term = "";
  finding = false;
  /** A refusal or an empty answer in words. */
  notice: string | null = null;
  /** The matches of the last find (several list as rows to pick). */
  matches: OwingOrder[] = [];
  /** The order on the card, or null. */
  order: OwingOrder | null = null;
  method: PayMethod = "CARD";
  drawer: PayDrawer = { kind: "unknown" };
  taking = false;
  taken: TakenBalance | null = null;
  /** The staff order read's pay_hold for a held order (the chips' words), or null. */
  hold: PayHold | null = null;
  /** Whether the last find has been asked at all (an empty result then reads as a miss). */
  #asked = false;
  /** One key per drafted take: a retry of the same take replays, a different take is a new key. */
  #pendingKey: string | null = null;
  #epoch = 0;

  constructor(options: PayBalanceFlowOptions) {
    this.#client = options.client;
    this.#capture = options.capture;
    this.#branchId = options.branchId;
    this.#onChange = options.onChange;
  }

  /** Bumped on every reset, so the view rebuilds the uncontrolled find field. */
  get epoch(): number {
    return this.#epoch;
  }

  get canFind(): boolean {
    return !this.finding && !this.taking && this.term.trim() !== "";
  }

  get canTake(): boolean {
    return this.order !== null && this.taken === null && !this.taking && this.drawer.kind === "open";
  }

  setTerm(value: string): void {
    this.term = value;
    this.#onChange();
  }

  chooseMethod(method: string): void {
    if (method !== "CASH" && method !== "CARD") return;
    if (this.taking || this.taken !== null) return;
    this.method = method;
    this.#pendingKey = null;
    this.#onChange();
  }

  /** Find the orders that owe: by order number, customer name or phone (the server's match). */
  async find(): Promise<void> {
    if (!this.canFind) return;
    const term = this.term.trim();
    this.finding = true;
    this.notice = null;
    this.#onChange();
    const result = await staffPos.posFindPayBalance(this.#client, { q: term });
    this.finding = false;
    this.#asked = true;
    this.taken = null;
    this.#pendingKey = null;
    if (!result.ok) {
      this.matches = [];
      this.order = null;
      this.notice = `The search was refused: ${result.error}`;
      this.#onChange();
      return;
    }
    this.matches = readOwingOrders(result.value);
    this.order = null;
    if (this.matches.length === 0) {
      this.notice = `No order that still owes matches ${term}: check the number, the name or the phone and try again.`;
      this.#onChange();
      return;
    }
    if (this.matches.length === 1) {
      await this.#select(this.matches[0]);
      return;
    }
    this.#onChange();
  }

  /** Pick one of several matches. */
  async pick(orderId: string): Promise<void> {
    const found = this.matches.find((o) => o.orderId === orderId);
    if (found === undefined || this.taking) return;
    await this.#select(found);
  }

  async #select(order: OwingOrder): Promise<void> {
    this.order = order;
    this.taken = null;
    this.notice = null;
    this.#pendingKey = null;
    this.drawer = { kind: "unknown" };
    this.hold = null;
    this.#onChange();
    await Promise.all([this.#readDrawer(), this.#readHold(order)]);
    this.#onChange();
  }

  /** A held order's chips and title come from the staff order read, the server's own words. */
  async #readHold(order: OwingOrder): Promise<void> {
    if (!order.held) return;
    const result = await staffOrder.orderRead(this.#client, order.orderId);
    if (!result.ok || this.order?.orderId !== order.orderId) return;
    this.hold = readPayHold(result.value.pay_hold);
  }

  async #readDrawer(): Promise<void> {
    const branch = this.#branchId();
    if (branch === null) {
      this.drawer = { kind: "unknown" };
      return;
    }
    const result = await staffTill.tillLookupOpenSession(this.#client, { branch_id: branch });
    if (!result.ok) {
      this.drawer = { kind: "unknown" };
      return;
    }
    const sessionId = result.value.id;
    this.drawer =
      result.value.open === true && typeof sessionId === "string" && sessionId !== ""
        ? { kind: "open", sessionId }
        : { kind: "closed" };
  }

  /** Take the whole balance. The amount sent is the server's own still owed, echoed. */
  async take(money: (cents: number) => string): Promise<void> {
    const order = this.order;
    if (order === null || !this.canTake || this.drawer.kind !== "open") return;
    if (this.#pendingKey === null) this.#pendingKey = crypto.randomUUID();
    this.taking = true;
    this.notice = null;
    this.#onChange();
    const result = await staffPos.posTakePayBalance(this.#client, {
      order_id: order.orderId,
      method: this.method,
      amount_cents: order.stillOwedCents,
      till_session_id: this.drawer.sessionId,
      idempotency_key: this.#pendingKey,
      ...(this.method === "CARD" ? { payment_token: CARD_TOKEN, card_type: "CREDIT" as const } : {}),
    });
    this.taking = false;
    if (!result.ok) {
      this.notice = payBalanceRefusalWords(
        { status: result.status ?? 0, error: result.error, ...this.#capture.take() },
        money,
      );
      // A refusal that names a changed balance, or a paid order, is a stale card: read it again.
      if (result.status === 409 || result.status === 422 || result.status === 404) this.#pendingKey = null;
      this.#onChange();
      return;
    }
    const taken = readTaken(result.value);
    if (taken === null) {
      this.notice = "The answer to the payment was not readable: check the order before taking it again.";
      this.#onChange();
      return;
    }
    this.taken = taken;
    this.#pendingKey = null;
    this.#onChange();
  }

  /** Back to the blank find: the field is rebuilt with the epoch, so the term goes with it. */
  another(): void {
    this.term = "";
    this.order = null;
    this.taken = null;
    this.hold = null;
    this.matches = [];
    this.notice = null;
    this.#pendingKey = null;
    this.#asked = false;
    this.#epoch += 1;
    this.#onChange();
  }

  /** Sign out blanks the surface whole, the term included. */
  reset(): void {
    this.term = "";
    this.finding = false;
    this.method = "CARD";
    this.drawer = { kind: "unknown" };
    this.another();
  }

  /** Open the surface on one order (the held card's Pay balance action): find it by number. */
  async openOn(orderNo: string): Promise<void> {
    this.term = orderNo;
    this.#onChange();
    await this.find();
  }

  get asked(): boolean {
    return this.#asked;
  }
}
