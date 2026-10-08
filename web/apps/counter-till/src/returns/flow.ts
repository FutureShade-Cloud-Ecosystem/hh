// The returns flow: the framework-free state machine behind the till's Returns surface. A return
// is built like a credit: the customer first, then the lines from every order that customer holds,
// then the refund. One way to return: there is no search by one sale's number. The core's reads are
// the authority for every fact held here; this adds only the person's own choices (the customer, the
// lines, the quantities, the dispositions) through the draft, and no figure is computed here.
import {
  staffCustomer,
  staffPayment,
  staffPos,
  staffTill,
  type ApiClient,
  type CustomerSearchResult,
  type DemoFlow,
  type PosRefundApproval,
  type PosReturnAcrossInput,
  type Result,
} from "@hardscapehouse/api-client";
import type { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { isApproverRole, type OneTimeCredential } from "./approval.js";
import type { RefundApprovalCapture } from "./capture.js";
import { ReturnDraft, type DraftAnswer } from "./draft.js";
import { filterOrders, readCustomerLines, type CustomerOrder } from "./lines.js";
import { buildNoteInput, buildPreviewInput, railAvailable, readNote, readPreview, REFUND_METHODS, type CreditNote, type CreditPreview, type DrawerState, type RefundMethod } from "./note.js";

/** The stepper's four steps. */
export type ReturnStep = "customer" | "lines" | "refund" | "done";

export const STEP_ORDER: readonly ReturnStep[] = ["customer", "lines", "refund", "done"];

/**
 * Who the till is signed in as, read live from custody at use: the display name the session carries,
 * the role code the mint stated, and the role's label as the entry card showed it.
 */
export interface ClerkIdentity {
  name: string;
  roleCode: string | null;
  roleLabel: string | null;
}

/** What the Done step keeps of a committed note, for its words. */
export interface DoneNote {
  note: CreditNote;
  /** The order number of each child return, from the lines the draft was built on. */
  orderNos: Record<string, string>;
  /** The approver's name when a manager approved this note; null when the clerk's own answer committed it. */
  approverName: string | null;
  clerkName: string | null;
  /** The simulator's own flag at the time of the refund; null when the status route has not answered. */
  cardSimulated: boolean | null;
}

export interface ReturnsFlowOptions {
  /** The clerk's client: the till's own session rides its header provider. */
  client: ApiClient;
  /**
   * The approver's client: the same transport rules, and a header provider that takes the one time
   * credential when the approval's single send runs. The clerk's client never carries it.
   */
  approverClient: ApiClient;
  /** The demo adapter, for the flows list and the approver's mint. */
  account: DemoAccount;
  /** The one time credential slot the approval's send consumes. */
  credential: OneTimeCredential;
  /** Who the till is signed in as, read from custody at receipt time. */
  clerk: () => ClerkIdentity | null;
  /** The wrapped fetch's reader for the refusal body's approval. */
  capture: RefundApprovalCapture;
  /** The branch this login reaches, read live; null before it resolves. */
  branchId: () => string | null;
  /** Called after every state change, so the host re-renders. */
  onChange: () => void;
}

export class ReturnsFlow {
  readonly #client: ApiClient;
  readonly #approverClient: ApiClient;
  readonly #account: DemoAccount;
  readonly #credential: OneTimeCredential;
  readonly #clerk: () => ClerkIdentity | null;
  readonly #capture: RefundApprovalCapture;
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
  /** The credit so far, as the server priced the draft; null until it answers or when nothing is picked. */
  preview: CreditPreview | null = null;
  /** True while a preview is on the wire. */
  previewing = false;
  /** The preview's refusal in the server's words (a wall, an over-return), shown beside the credit. */
  previewNotice: string | null = null;
  #previewSeq = 0;
  /** A draft refusal in words, shown beside the lines (a quantity above open, another branch). */
  draftNotice: string | null = null;
  /** The chosen refund rail, until the clerk picks one. */
  method: RefundMethod | null = null;
  /** The drawer at the note's branch, asked when the Refund step opens. */
  drawer: DrawerState = { kind: "unknown" };
  /** The card rail's simulator flag from the status route; null until it answers. */
  cardSimulated: boolean | null = null;
  /** True while the note is on the wire: the surface's controls go disabled. */
  submitting = false;
  /** The committed note, once one is. */
  done: DoneNote | null = null;
  /** The approval a 409 named: the dealer's amount (judged on the note total) and the roles that may approve. */
  refusal: PosRefundApproval | null = null;
  dialogOpen = false;
  approverFlows: DemoFlow[] | null = null;
  approving = false;
  /** One key per drafted note: a retry of the same note replays, a changed draft is a new note. */
  #pendingKey: string | null = null;
  /** The exact body sent and, when refused for approval, sent again unchanged. */
  #pendingInput: PosReturnAcrossInput | null = null;
  #epoch = 0;
  /** A search or a read that has been overtaken: its late answer is dropped. */
  #seq = 0;

  constructor(options: ReturnsFlowOptions) {
    this.#client = options.client;
    this.#approverClient = options.approverClient;
    this.#account = options.account;
    this.#credential = options.credential;
    this.#clerk = options.clerk;
    this.#capture = options.capture;
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
    return this.step === "lines" && this.draft.ready && !this.loading && this.preview !== null;
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
    this.#draftChanged();
    return answer;
  }

  /** Step a line's quantity by one, capped at the open quantity by the draft's own refusal. */
  stepQty(orderLineId: string, by: 1 | -1): DraftAnswer {
    return this.setQty(orderLineId, String(this.draft.qtyOf(orderLineId) + by));
  }

  setDisposition(orderLineId: string, value: string): DraftAnswer {
    const answer = this.draft.setDisposition(orderLineId, value);
    this.draftNotice = answer.ok ? null : answer.words;
    this.#draftChanged();
    return answer;
  }

  /**
   * A changed draft is a different note: its pending key, the input an approval would resend and the
   * refusal the last send earned belong to the draft that earned them, so all three go.
   */
  #draftChanged(): void {
    this.refusal = null;
    this.#pendingKey = null;
    this.#pendingInput = null;
    this.#onChange();
    void this.#refreshPreview();
  }

  /**
   * Ask the server what the draft would credit (the commit's own pricing function, nothing written).
   * The till computes no money: the answer, or nothing, is what the screen shows. The latest ask wins.
   */
  async #refreshPreview(): Promise<void> {
    const seq = ++this.#previewSeq;
    const body = this.customer === null ? null : buildPreviewInput(this.customer.id, this.draft);
    if (body === null) {
      this.preview = null;
      this.previewing = false;
      this.previewNotice = null;
      this.#onChange();
      return;
    }
    this.previewing = true;
    this.#onChange();
    const result = await staffPos.posPreviewReturnAcross(this.#client, body);
    if (seq !== this.#previewSeq) return;
    this.previewing = false;
    if (!result.ok) {
      this.preview = null;
      this.previewNotice = `The credit could not be priced: ${result.error}`;
      this.#onChange();
      return;
    }
    const read = readPreview(result.value);
    if (read === null) {
      this.preview = null;
      this.previewNotice = "The credit's pricing was not readable: no total is shown rather than half of one.";
      this.#onChange();
      return;
    }
    this.preview = read;
    this.previewNotice = null;
    this.#onChange();
  }

  /** Continue to the refund step once every picked line has its disposition. */
  async toRefund(): Promise<void> {
    if (!this.canContinue) return;
    this.step = "refund";
    this.method = null;
    this.notice = null;
    this.drawer = { kind: "unknown" };
    this.cardSimulated = null;
    this.#draftChanged();
    // The step's side reads, in words on the screen when they fail: the drawer the cash rail pays from,
    // and the card rail's simulator flag that decides whether the card rail may call itself simulated.
    const branchId = this.draft.branchId;
    if (branchId !== null) await this.#readDrawer(branchId);
    await this.#readPaymentsStatus();
    this.#onChange();
  }

  toLines(): void {
    if (this.customer === null || this.submitting) return;
    this.step = "lines";
    this.notice = null;
    this.#draftChanged();
  }

  railAvailable(method: RefundMethod): boolean {
    return railAvailable(this.draft, method, this.drawer);
  }

  chooseMethod(method: string): void {
    if (!REFUND_METHODS.includes(method as RefundMethod)) return;
    if (!this.railAvailable(method as RefundMethod)) return;
    this.method = method as RefundMethod;
    this.notice = null;
    this.#draftChanged();
  }

  get canSubmit(): boolean {
    return this.step === "refund" && this.method !== null && !this.submitting && !this.approving && !this.dialogOpen;
  }

  /** Commit the note once. The server's answer is the Done step's facts. */
  async submit(): Promise<void> {
    if (!this.canSubmit || this.customer === null || this.method === null) return;
    if (this.#pendingKey === null) this.#pendingKey = crypto.randomUUID();
    const built = buildNoteInput({
      customerId: this.customer.id,
      key: this.#pendingKey,
      draft: this.draft,
      method: this.method,
      drawerSessionId: this.drawer.kind === "open" ? this.drawer.sessionId : "",
    });
    if (!built.ok) {
      this.notice = built.words;
      this.#onChange();
      return;
    }
    this.#pendingInput = built.input;
    this.submitting = true;
    this.notice = null;
    this.#onChange();
    const result = await staffPos.posCreateReturnAcross(this.#client, built.input);
    this.submitting = false;
    if (this.customer === null) return;
    if (!result.ok) {
      if (result.status === 409 && result.error === "refund_approval_required") {
        // A client discriminates on the error string, never on the key's presence: the approval rides the
        // body the transport's capture kept, or the surface says the refusal without its figures.
        const approval = this.#capture.take();
        if (approval !== null) {
          this.refusal = approval;
          this.#onChange();
          return;
        }
        this.notice = "The credit note needs a manager whose role holds the approval, but the refusal did not say which: try again or call the desk lead.";
        this.#onChange();
        return;
      }
      this.notice = `The credit note was refused: ${result.error}`;
      this.#onChange();
      return;
    }
    this.#finish(result.value, null);
  }

  #finish(raw: unknown, approverName: string | null): void {
    const note = readNote(raw);
    if (note === null) {
      this.notice = "The credit note's answer was not readable: the receipt waits rather than guessing.";
      this.#onChange();
      return;
    }
    const orderNos: Record<string, string> = {};
    for (const order of this.orders) orderNos[order.orderId] = order.orderNo;
    const clerk = this.#clerk();
    // The approver line on the caller's own path: when the till is signed in as a role the approval names,
    // the caller is the approver. It is display wording; the server's audit row holds the record.
    const ownApprover =
      approverName === null && clerk !== null && clerk.roleCode !== null && isApproverRole(clerk.roleCode)
        ? `the ${clerk.roleLabel ?? clerk.roleCode} on this till: ${clerk.name}`
        : approverName;
    this.done = {
      note,
      orderNos,
      approverName: ownApprover,
      clerkName: clerk?.name ?? null,
      cardSimulated: note.method === "CARD" ? this.cardSimulated : null,
    };
    this.step = "done";
    this.refusal = null;
    this.#pendingKey = null;
    this.#pendingInput = null;
    this.#onChange();
  }

  /** The refusal's "Manager approval" control: the dialog with the approver roles' cards. */
  async openApprovalDialog(): Promise<void> {
    if (this.refusal === null || this.dialogOpen) return;
    this.dialogOpen = true;
    this.approverFlows = null;
    this.#onChange();
    const result = await this.#account.listFlows();
    if (!result.ok) {
      this.approverFlows = [];
      this.#onChange();
      return;
    }
    const wanted = new Set(this.refusal.approvers);
    this.approverFlows = result.value.filter(
      (row): row is DemoFlow & { flow: string } =>
        typeof row.flow === "string" && row.flow !== "" && typeof row.role_code === "string" && wanted.has(row.role_code),
    );
    this.#onChange();
  }

  closeApprovalDialog(): void {
    this.dialogOpen = false;
    this.approverFlows = null;
    this.#onChange();
  }

  /**
   * The one send with the approver's credential: the dialog's card mints it through the demo adapter,
   * the same note goes once more with it in X-Approver-Authorization, and the token lives in the one
   * time slot for exactly that send, swept in the same breath whatever the answer. The clerk's session
   * is never touched.
   */
  async approveWithFlow(flowName: string): Promise<void> {
    if (this.refusal === null || this.approving) return;
    const input = this.#pendingInput;
    if (input === null) return;
    this.approving = true;
    this.#onChange();
    const minted = await this.#account.mintApproverCredential(flowName);
    // A sign out mid flight blanks the surface; nothing of this approval then sends or writes onto it.
    if (this.customer === null) {
      this.#endApproval(null);
      return;
    }
    if (!minted.ok) {
      this.#endApproval(`The manager's sign-in was refused: ${minted.error}`);
      return;
    }
    const approverName = minted.value.approverName;
    this.#credential.set(minted.value.token);
    let result: Result<unknown>;
    try {
      result = await staffPos.posCreateReturnAcross(this.#approverClient, input);
    } finally {
      this.#credential.drop();
    }
    if (this.customer === null) {
      this.#endApproval(null);
      return;
    }
    if (!result.ok) {
      this.#endApproval(`The credit note was refused again: ${result.error}`);
      return;
    }
    this.approving = false;
    this.dialogOpen = false;
    this.approverFlows = null;
    this.#finish(result.value, approverName);
  }

  #endApproval(notice: string | null): void {
    this.approving = false;
    this.dialogOpen = false;
    this.approverFlows = null;
    if (this.customer === null) {
      // Blanked under this approval (a sign out): write nothing on it, for whoever signs in next.
      this.refusal = null;
      this.done = null;
      this.#pendingKey = null;
      this.#pendingInput = null;
      this.#onChange();
      return;
    }
    if (notice !== null) this.notice = notice;
    this.#onChange();
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

  /** Back to the blank first step: another customer, a clean draft, no note. */
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
    this.method = null;
    this.#previewSeq += 1;
    this.preview = null;
    this.previewing = false;
    this.previewNotice = null;
    this.drawer = { kind: "unknown" };
    this.cardSimulated = null;
    this.submitting = false;
    this.done = null;
    this.refusal = null;
    this.dialogOpen = false;
    this.approverFlows = null;
    this.approving = false;
    this.#pendingKey = null;
    this.#pendingInput = null;
    this.#epoch += 1;
    this.#onChange();
  }

  /** Sign out's reset: nothing of the last person's customer, draft, refusal or note stays on the next person's till. */
  reset(): void {
    this.startAnother();
  }
}
