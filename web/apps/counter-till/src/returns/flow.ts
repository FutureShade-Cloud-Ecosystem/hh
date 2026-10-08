// The returns flow: the framework-free state machine behind the till's Returns surface. A return
// is built like a credit: the customer first, then the lines from every order that customer holds,
// then the refund. One way to return: there is no search by one sale's number. The core's reads are
// the authority for every fact held here; this adds only the person's own choices (the customer, the
// lines, the quantities, the dispositions) through the draft, and no figure is computed here.
import { staffCustomer, staffPos, type ApiClient, type CustomerSearchResult } from "@hardscapehouse/api-client";
import { ReturnDraft, type DraftAnswer } from "./draft.js";
import { filterOrders, readCustomerLines, type CustomerOrder } from "./lines.js";

/** The stepper's four steps; Refund and Done wait on the write contract. */
export type ReturnStep = "customer" | "lines" | "refund" | "done";

export const STEP_ORDER: readonly ReturnStep[] = ["customer", "lines", "refund", "done"];

export interface ReturnsFlowOptions {
  /** The clerk's client: the till's own session rides its header provider. */
  client: ApiClient;
  /** The branch this login reaches, read live; null before it resolves. */
  branchId: () => string | null;
  /** Called after every state change, so the host re-renders. */
  onChange: () => void;
}

export class ReturnsFlow {
  readonly #client: ApiClient;
  readonly #branchId: () => string | null;
  readonly #onChange: () => void;

  step: ReturnStep = "customer";
  /** The customer search field's live value, so a re-render keeps what is typed. */
  term = "";
  searching = false;
  /** The last search's rows. */
  matches: CustomerSearchResult[] = [];
  /** True once a search has answered, so an empty answer reads as a miss rather than a blank. */
  searched = false;
  customer: CustomerSearchResult | null = null;
  /** True while the customer's lines are being read. */
  loading = false;
  /** The customer's orders newest first, as the server answered. */
  orders: CustomerOrder[] = [];
  draft: ReturnDraft = new ReturnDraft([]);
  /** The sales order number filter inside the line list. */
  filter = "";
  /** A refusal or an empty answer in words. */
  notice: string | null = null;
  /** A draft refusal in words, shown beside the lines (a quantity above open, another branch). */
  draftNotice: string | null = null;
  #epoch = 0;
  /** A search or a read that has been overtaken: its late answer is dropped. */
  #seq = 0;

  constructor(options: ReturnsFlowOptions) {
    this.#client = options.client;
    this.#branchId = options.branchId;
    this.#onChange = options.onChange;
  }

  /** Bumped on every fresh customer or reset: the view keys its uncontrolled fields on it. */
  get epoch(): number {
    return this.#epoch;
  }

  get visibleOrders(): CustomerOrder[] {
    return filterOrders(this.orders, this.filter);
  }

  /** True when the customer holds orders, but every line on them has already gone back. */
  get nothingOpen(): boolean {
    return this.orders.length > 0 && this.orders.every((order) => order.lines.every((line) => line.qtyOpen <= 0));
  }

  get canContinue(): boolean {
    return this.step === "lines" && this.draft.ready && !this.loading;
  }

  setTerm(value: string): void {
    this.term = value;
    void this.search();
  }

  /** The customer search over the existing route: two letters or more, the latest ask wins. */
  async search(): Promise<void> {
    const q = this.term.trim();
    const seq = ++this.#seq;
    if (q.length < 2) {
      this.searching = false;
      this.matches = [];
      this.searched = false;
      this.notice = null;
      this.#onChange();
      return;
    }
    this.searching = true;
    this.notice = null;
    this.#onChange();
    const branchId = this.#branchId();
    const result = await staffCustomer.customerSearch(this.#client, {
      q,
      limit: 20,
      ...(branchId === null ? {} : { branch_id: branchId }),
    });
    if (seq !== this.#seq) return;
    this.searching = false;
    this.searched = true;
    if (!result.ok || !Array.isArray(result.value)) {
      this.matches = [];
      this.notice = result.ok ? "The customer search answered something unreadable: nothing is listed." : `The customer search was refused: ${result.error}`;
      this.#onChange();
      return;
    }
    this.matches = result.value;
    this.#onChange();
  }

  /** Pick a customer and read every order they hold. The walk-in is refused by the server, in words. */
  async chooseCustomer(customerId: string): Promise<void> {
    const customer = this.matches.find((row) => row.id === customerId);
    if (customer === undefined || this.loading) return;
    const seq = ++this.#seq;
    this.loading = true;
    this.notice = null;
    this.#onChange();
    const result = await staffPos.posCustomerReturnLines(this.#client, { customer_id: customer.id });
    if (seq !== this.#seq) return;
    this.loading = false;
    if (!result.ok) {
      this.notice =
        result.status === 422
          ? `A walk-in sale is not returned from here: ${result.error}`
          : `This customer's orders could not be read: ${result.error}`;
      this.#onChange();
      return;
    }
    const orders = readCustomerLines(result.value);
    if (orders === null) {
      this.notice = "The answer about this customer's orders was not readable: nothing is shown rather than half of it.";
      this.#onChange();
      return;
    }
    this.customer = customer;
    this.orders = orders;
    this.draft = new ReturnDraft(orders);
    this.filter = "";
    this.draftNotice = null;
    this.#epoch += 1;
    this.step = "lines";
    this.#onChange();
  }

  setFilter(value: string): void {
    this.filter = value;
    this.#onChange();
  }

  setQty(orderLineId: string, raw: string): DraftAnswer {
    const answer = this.draft.setQty(orderLineId, raw);
    this.draftNotice = answer.ok ? null : answer.words;
    this.#onChange();
    return answer;
  }

  /** Step a line's quantity by one, capped at the open quantity by the draft's own refusal. */
  stepQty(orderLineId: string, by: 1 | -1): DraftAnswer {
    return this.setQty(orderLineId, String(this.draft.qtyOf(orderLineId) + by));
  }

  setDisposition(orderLineId: string, value: string): DraftAnswer {
    const answer = this.draft.setDisposition(orderLineId, value);
    this.draftNotice = answer.ok ? null : answer.words;
    this.#onChange();
    return answer;
  }

  /** Continue to the refund step once every picked line has its disposition. */
  toRefund(): void {
    if (!this.canContinue) return;
    this.step = "refund";
    this.#onChange();
  }

  toLines(): void {
    if (this.customer === null) return;
    this.step = "lines";
    this.#onChange();
  }

  /** Back to the blank first step: another customer, a clean draft. */
  startAnother(): void {
    this.#seq += 1;
    this.step = "customer";
    this.term = "";
    this.searching = false;
    this.matches = [];
    this.searched = false;
    this.customer = null;
    this.loading = false;
    this.orders = [];
    this.draft = new ReturnDraft([]);
    this.filter = "";
    this.notice = null;
    this.draftNotice = null;
    this.#epoch += 1;
    this.#onChange();
  }

  /** Sign out's reset: nothing of the last person's customer or draft stays on the next person's till. */
  reset(): void {
    this.startAnother();
  }
}
