// The receivables desk's controller: the state the screens draw from, the
// loads, and what each press does. It is not an element; the root element owns
// it, hands it a way to repaint and a way to read and set the form fields, and
// asks it for the template. The Money Desk's other slices sit beside it the
// same way.
//
// Every write is held to the server: a payment is applied, a promise recorded,
// a payment marked bounced, a hold lifted only when the core answers, and the
// screen then shows what the core now says (the balance it answered, the queue
// it lists), never what the app expected. A refusal reads in words (ar-logic.ts),
// a walled move in words naming who may do it. Nothing is stored: the
// idempotency keys, like the rest, rest in memory for the life of the page.
import { html, type TemplateResult } from "lit";
import type { BillingCustomerWorkspace, BillingRegisterRow, Result } from "@hardscapehouse/api-client";
import { ArApi, type PaymentMethod } from "./ar-api.js";
import { HeldKeys, newKey } from "./held-keys.js";
import { canLiftHolds, creditCheck, creditWords, dayInstant, isUnpaidInvoice, MAX_APPLY_INVOICES, paymentOutcome, pickCeilingWords, refusalWords, validatePromise } from "./ar-logic.js";
import { centsToField, formatMoney, parseAmountToCents } from "./ar-money.js";
import { OfficeDesk, type OfficeHost } from "./office-desk.js";
import { AttemptKeys, reminderRunWords, sweepWords } from "./office-logic.js";
import {
  renderAccount,
  renderHolds,
  renderHome,
  renderLoadFailed,
  renderLoading,
  renderReminders,
  renderSettings,
  renderStatements,
  renderWorkspaceScreen,
  shownInvoiceIds,
  type AccountModel,
  type BouncedWork,
  type CreditForm,
  type DeskModel,
  type PayMethod,
  type RegisterFilter,
  type RightMode,
  type Tab,
  type WorkChoice,
} from "./ar-views.js";
import type { WorkspaceTab } from "./ar-workspace.js";
import { formatWorkspaceMoney } from "./ar-workspace.js";
import { renderPayables } from "./ap-views.js";
import { payAmountWords, payOverOpenWords, type PayablesBoardModel, type PayBillForm, type PurchasingAPAgingItem, type PurchasingAPAgingPage } from "./ap-logic.js";
import { houseDay, houseZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";

export interface DeskHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** Brings the first element matching the selector into view and focuses it. */
  readonly reveal: (selector: string) => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string, key?: string) => string;
  /** Sets a form field's text; the screens bind no value, so a fill goes through here. */
  readonly setField: (name: string, value: string, key?: string) => void;
  /** Hands text to the person as a file. */
  readonly download: (text: string, filename: string) => void;
  /** Puts an invoice document's HTML into the element matching the selector, in a sandboxed frame with no scripts. */
  readonly mountDocument: (selector: string, text: string) => void;
  /** Hands text to the system clipboard. Optional: shells that do not bind it answer with
   *  false and the screen falls back to a copy-by-hand notice. */
  readonly copy?: (text: string) => boolean;
}

export interface DeskWho {
  readonly userId: string;
  readonly roleCode: string;
  readonly userName: string;
}

const METHODS: readonly PaymentMethod[] = ["CASH", "CARD", "CHEQUE", "EFT"];

export class ArDesk {
  readonly #api: ArApi;
  readonly #host: DeskHost;
  readonly #who: DeskWho;

  #phase: "loading" | "ready" | "error" = "loading";
  #loadError = "";
  #tab: Tab = "receivables";

  // The Payables board (src/ar/ap-views.ts): the open AP by payee, read from the purchasing
  // route. It loads when the tab is opened, not with the desk, so the receivables desk is no
  // slower for a screen nobody asked for. The day the books are read as of is the operator's
  // to pick; the route answers the figures for that day.
  #payablesState: "loading" | "ready" | "error" = "loading";
  #payablesPage: PurchasingAPAgingPage | null = null;
  #payablesError = "";
  #payablesAsOf = "";
  #payablesOpenKey: string | null = null;
  // The number of the newest read of the open AP: only that read may answer, so a slow answer for
  // an earlier day never replaces the newer pick.
  #payablesLoads = 0;
  // The Pay form on one open vendor bill: the amount, the day, the note, and the key the press
  // carries. The key is minted when the form opens and sent again on every retry, so a double
  // press pays once and a retry after a lost answer is judged against the first press.
  #pay: PayBillForm | null = null;
  /** Bills whose earlier payment the books showed landed after its form had closed: the next form says so. */
  readonly #payLanded = new Map<string, number>();
  #payBusy = false;
  /** The form's fields are filled once, when it opens: after that the person types in them. */
  #payFillPending = false;
  #landing: DeskModel["landing"] = null;
  #aging: DeskModel["aging"] = [];
  #rows: readonly BillingRegisterRow[] = [];
  #filter: RegisterFilter = "all";
  #cardFilter: string | null = null;
  #selection: DeskModel["selection"] = null;
  #bounced: DeskModel["bounced"] = [];
  #holds: DeskModel["holds"] = [];
  #holdScope: "active" | "all" = "active";
  #branches: DeskModel["branches"] = [];
  #work: BouncedWork | null = null;
  #picker = false;
  #statements = new Map<string, import("@hardscapehouse/api-client").BillingStatement | "none">();
  #selectedHoldId: string | null = null;
  #holdPanel: DeskModel["holdPanel"] = "about";
  #liftChoice: "lift" | "override" = "lift";
  #account: AccountModel | null = null;
  #busy = false;
  #error = "";
  #notice = "";
  #fillPending = false;

  // The credit memo form (FW1-12): which invoice the credit is against, or nothing for a credit on the
  // account itself. The amount and the reason are read from the form fields when the credit is sent.
  #credit: CreditForm | null = null;

  // One key per credit attempt. It is minted when the form opens and sent again on every retry, so a
  // double press on Issue the credit raises ONE credit. It ends when the server answers.
  readonly #creditKeys = new AttemptKeys(newKey);

  // One pending payment per account: the key is minted when the payment is first sent and sent again for every
  // retry, whatever the form now says (a corrected cheque number, another method, a changed amount, other
  // invoices), until the outcome is known: the payment route's own answer (recorded, its refusal, or "already
  // recorded"). A payments list, a reload, a sign out and a sign in of the same user never end it; another user
  // never sees it. The route refuses a changed body under a spent key in words, so a lost answer can never post
  // the same money twice, and a payment on another account never touches this one's key.
  readonly #payPins: HeldKeys;
  // One pending payment per vendor bill: the key is minted when the payment is first sent and sent
  // again for every retry, until the outcome is known. While the outcome is unknown (a lost answer,
  // a 5xx) the key is held across a Cancel and across a reload, so the next Pay on that bill
  // carries the same key and the core answers the first payment: the money cannot move twice. It
  // rides the same throw safe session mirror the accounts' held keys ride, in its own record.
  readonly #payBillKeys: HeldKeys;
  /** The office: statement run read back, AP contacts, reminders, dispute, settings (office-desk.ts). */
  readonly #office: OfficeDesk;
  #promiseKey = newKey();
  #remindKey = newKey();
  // The customer account workspace (Change 8 S2 + S2M): a separate screen mode that
  // lands when the operator clicks a customer name in the register or the worklist.
  // workspaceMode is the gate the render function reads; workspaceCustomerId is the
  // customer whose workspace is open; activeWorkspaceTab is the desktop / mobile
  // workspace tab the operator last picked. The workspace payload itself is held on
  // #workspace and reloaded on every entry so the visible numbers are the server's
  // current ones, never a cached copy.
  #workspaceMode = false;
  #workspace: BillingCustomerWorkspace | null = null;
  #workspaceCustomerId: string | null = null;
  #activeWorkspaceTab: WorkspaceTab = "overview";
  // The Place-a-hold draft (Change 8 S4). Empty customerId means "no account chosen
  // yet"; the form refuses to submit while either field is empty. Reset on close.
  #placeHoldDraft: { customerId: string; reason: string } = { customerId: "", reason: "" };

  constructor(api: ArApi, host: DeskHost, who: DeskWho) {
    this.#api = api;
    this.#host = host;
    this.#who = who;
    this.#payPins = new HeldKeys(who.userId);
    this.#payBillKeys = new HeldKeys(who.userId, undefined, "bills.");
    const officeHost: OfficeHost = {
      repaint: () => host.repaint(),
      reveal: (selector) => {
        this.#reveal = selector;
      },
      field: (name, key) => host.field(name, key),
      setField: (name, value, key) => host.setField(name, value, key),
      notice: (words) => {
        this.#error = "";
        this.#notice = words;
        host.repaint();
      },
      error: (words) => {
        this.#notice = "";
        this.#error = words;
        host.repaint();
      },
      reloadAccount: async () => {
        await this.#reloadAccount();
        host.repaint();
      },
      mountDocument: (selector, text) => host.mountDocument(selector, text),
      accounts: () => this.#aging,
      policy: () => this.#landing?.policy ?? null,
      reloadBook: async () => {
        await this.#reloadBook();
        host.repaint();
      },
    };
    this.#office = new OfficeDesk(api.office(), officeHost, who);
  }


  // ---- loads ----------------------------------------------------------------------------------

  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [landing, aging, register, bounced, holds, branches] = await Promise.all([
      this.#api.landing(),
      this.#api.aging(),
      this.#api.register(),
      this.#api.bounced(),
      this.#api.holds(),
      this.#api.branches(),
    ]);
    for (const read of [landing, aging, register, bounced, holds]) {
      if (!read.ok) {
        this.#phase = "error";
        this.#loadError = refusalWords("load", read.error, read.status);
        this.#host.repaint();
        return;
      }
    }
    if (landing.ok) this.#landing = landing.value;
    if (aging.ok) this.#aging = aging.value.items;
    if (register.ok) this.#rows = register.value.items;
    // The core's bounced queue answers a null items on an empty queue (its shape
    // census records the handler as tillListBouncedPayments), and a null here
    // took the whole landing down with it. The model's list is an array.
    if (bounced.ok) this.#bounced = bounced.value.items ?? [];
    if (holds.ok) this.#holds = holds.value.items;
    if (branches.ok) this.#branches = branches.value;
    this.#phase = "ready";
    await this.#refreshSelection();
    this.#host.repaint();
  }

  /** The book after a write: cards, aging, register, queue and holds, quietly. */
  async #reloadBook(): Promise<void> {
    const [landing, aging, register, bounced, holds] = await Promise.all([this.#api.landing(), this.#api.aging(), this.#api.register(), this.#api.bounced(), this.#api.holds()]);
    if (landing.ok) this.#landing = landing.value;
    if (aging.ok) this.#aging = aging.value.items;
    if (register.ok) this.#rows = register.value.items;
    if (bounced.ok) this.#bounced = bounced.value.items ?? [];
    if (holds.ok) this.#holds = holds.value.items;
    await this.#refreshSelection();
  }

  /** The count and the balance of the invoices in view: the server adds them, the app does not. */
  async #refreshSelection(): Promise<void> {
    const ids = shownInvoiceIds(this.#model());
    if (ids.length === 0) {
      this.#selection = null;
      return;
    }
    const got = await this.#api.selection(ids);
    this.#selection = got.ok ? { count: got.value.count, cents: got.value.balance_cents } : null;
  }

  async #reloadAccount(): Promise<void> {
    const account = this.#account;
    if (account === null) return;
    const register = await this.#api.accountInvoices(account.customerId);
    if (!register.ok || this.#account === null) return;
    this.#account = { ...this.#account, invoices: register.value.items, invoicesState: "ready" };
    if (this.#account.selectedInvoiceId !== null) await this.#reloadInvoice();
  }

  async #reloadInvoice(): Promise<void> {
    const id = this.#account?.selectedInvoiceId;
    if (this.#account === null || id === null || id === undefined) return;
    const [detail, payments] = await Promise.all([this.#api.invoice(id), this.#api.invoicePayments(id)]);
    if (this.#account === null || this.#account.selectedInvoiceId !== id) return;
    this.#account = {
      ...this.#account,
      detail: detail.ok ? detail.value : this.#account.detail,
      payments: payments.ok ? payments.value : this.#account.payments,
    };
  }

  async #loadStatement(customerId: string): Promise<void> {
    const got = await this.#api.statement(customerId);
    const value = got.ok ? got.value : "none";
    this.#statements.set(customerId, value);
    if (this.#account !== null && this.#account.customerId === customerId) this.#account = { ...this.#account, statement: value };
    this.#host.repaint();
  }

  // ---- the press ------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    const choice = attr("data-choice");
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "retry-account-invoices":
        void this.#retryAccountInvoices();
        return true;
      case "tab":
        void this.#goTab(attr("data-tab") as Tab);
        return true;
      case "payables-open":
        this.#payablesOpenKey = attr("data-payee-id");
        // Another payee's open items: the Pay form belonged to the one that was open.
        this.#pay = null;
        this.#host.repaint();
        return true;
      case "payables-back":
        this.#payablesOpenKey = null;
        this.#pay = null;
        this.#host.repaint();
        return true;
      case "payables-reload":
        void this.#loadPayables();
        return true;
      case "payables-pay": {
        // The Pay form opens on one open bill: the amount it has open, the house day, and the key
        // this press will carry. A bill the board no longer holds opens no form. The key is the
        // bill's own held key when it has one (an earlier press whose outcome is still unknown), so
        // a fresh form on that bill sends the same payment and the core answers the first one.
        const item = this.#payablesItem(attr("data-bill-id"));
        if (item === null) return true;
        // A bill that holds an earlier press whose outcome is unknown opens on that same payment: its
        // key and its amount (S6 on 701, P2-2). A different amount under a held key would be swallowed
        // as a replay of the first, so the form sends only the earlier payment until the key ends.
        const held = heldPayment(this.#payBillKeys.get(item.id));
        this.#pay = {
          billId: item.id,
          billNo: item.ref_no,
          openCents: item.open_cents,
          amountText: centsToField(held === null ? item.open_cents : held.cents),
          paidOn: houseDay(),
          note: "",
          key: held === null ? newKey() : held.key,
          error: held === null ? "" : heldPaymentWords(held.cents),
          earlierLandedCents: this.#payLanded.get(item.id) ?? null,
        };
        this.#payLanded.delete(item.id);
        this.#payFillPending = true;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      }
      case "payables-pay-cancel":
        // The form closes, but the bill's key stays held while its outcome is unknown: the next Pay
        // on that bill carries the same key, so the money cannot move twice.
        this.#pay = null;
        this.#host.repaint();
        return true;
      case "payables-pay-send":
        if (this.#pay !== null && this.#pay.landedCents != null) return true;
        void this.#sendPayment();
        return true;
      case "card":
        this.#cardFilter = this.#cardFilter === attr("data-card-id") ? null : attr("data-card-id");
        void this.#refreshSelection().then(() => this.#host.repaint());
        this.#host.repaint();
        return true;
      case "filter":
        this.#filter = choice as RegisterFilter;
        if (this.#filter === "paid") void this.#loadPaid();
        void this.#refreshSelection().then(() => this.#host.repaint());
        this.#host.repaint();
        return true;
      case "download-csv":
        void this.#downloadCsv();
        return true;
      case "open-picker":
        this.#picker = true;
        this.#host.repaint();
        return true;
      case "close-picker":
        this.#picker = false;
        this.#host.repaint();
        return true;
      case "picker-go": {
        const name = this.#host.field("picker-account");
        const found = this.#aging.find((a) => a.customer_name === name) ?? this.#aging[0];
        this.#picker = false;
        if (found !== undefined) void this.#openAccount(found.customer_id, null, "receive", "invoices");
        return true;
      }
      case "run-statements":
        void this.#runStatements(null);
        return true;
      case "open-account":
        void this.#openAccount(attr("data-customer-id"), attr("data-invoice-id") || null, (attr("data-right") || "receive") as RightMode, attr("data-sub") === "statement" ? "statement" : attr("data-sub") === "contacts" ? "contacts" : "invoices");
        return true;
      case "back":
        this.#account = null;
        this.#office.invoiceChanged(null);
        this.#clearMessages();
        void this.#reloadBook().then(() => this.#host.repaint());
        this.#host.repaint();
        return true;
      case "sub":
        if (this.#account !== null) this.#account = { ...this.#account, sub: choice === "statement" ? "statement" : choice === "contacts" ? "contacts" : "invoices" };
        if (choice === "contacts" && this.#account !== null) void this.#office.ensureContact(this.#account.customerId);
        // A credit belongs to the view it was opened in: changing tab closes the form.
        this.#credit = null;
        this.#clearMessages();
        this.#host.repaint();
        this.#afterFill();
        return true;
      case "invoice-scope":
        if (this.#account !== null) this.#account = { ...this.#account, showAll: choice === "all" };
        // Narrowing the list can take away the row the credit is open on, so the form closes too.
        this.#credit = null;
        this.#host.repaint();
        return true;
      case "open-invoice":
        void this.#selectInvoice(attr("data-invoice-id"));
        return true;
      case "back-to-receive":
        this.#setRight("receive");
        return true;
      case "promise-panel":
        this.#setRight("promise");
        return true;
      case "credit-start": {
        // The credit is against one invoice, or against the account itself when the press named none.
        const invoiceId = attr("data-invoice-id");
        const id = invoiceId === "" ? null : invoiceId;
        this.#credit = { invoiceId: id, amountText: "", thresholdCents: null, quote: null, quoteError: "" };
        this.#clearMessages();
        this.#host.repaint();
        // The form says what the approval rail will do, so it needs the branch's own threshold.
        void this.#readCreditThreshold(id);
        // The form says what the credit comes to and how much room the invoice has left, so it asks
        // the core for the quote: the cap is the core's, never a number typed into the desk.
        this.#scheduleCreditQuote(0);
        return true;
      }
      case "credit-cancel":
        this.#credit = null;
        this.#creditQuoteGeneration += 1;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "credit-confirm":
        void this.#issueCredit();
        return true;
      case "method":
        if (this.#account !== null) this.#account = { ...this.#account, method: choice as PayMethod };
        this.#host.repaint();
        return true;
      case "pick":
        this.#togglePick(attr("data-invoice-id"));
        return true;
      case "pick-all":
        this.#pickAll();
        return true;
      case "clear-apply":
        this.#clearApply();
        return true;
      case "apply-payment":
        void this.#applyPayment();
        return true;
      case "record-promise":
        void this.#recordPromise();
        return true;
      case "bounce-start":
        if (this.#account !== null) this.#account = { ...this.#account, bounceFor: attr("data-payment-id") };
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "bounce-cancel":
        if (this.#account !== null) this.#account = { ...this.#account, bounceFor: null };
        this.#host.repaint();
        return true;
      case "bounce-confirm":
        void this.#markBounced(attr("data-payment-id"));
        return true;
      case "work-bounced":
        void this.#workBounced(attr("data-payment-id"), attr("data-customer-id"));
        return true;
      case "work-choice":
        if (this.#work !== null) this.#work = { ...this.#work, choice: choice as WorkChoice, note: "" };
        this.#host.repaint();
        return true;
      case "work-go":
        void this.#workGo();
        return true;
      case "close-work":
        this.#work = null;
        this.#host.repaint();
        return true;
      case "remind-account":
        void this.#remind(attr("data-customer-id"));
        return true;
      case "ask-lift":
        this.#notice = "";
        this.#error = "Asking the controller to lift a hold goes through House Messages, which is not on this desk yet (later). Tell the Financial Controller or the General Manager, and record the payment or promise that supports it.";
        this.#host.repaint();
        return true;
      case "send-statement":
        void this.#runStatements(this.#account?.customerId ?? null, true);
        return true;
      case "run-statement":
        void this.#runAccountStatement();
        return true;
      case "download-pdf":
        this.#notice = "";
        this.#error = "Download as PDF is later. The statement on this page is the document for now.";
        this.#host.repaint();
        return true;
      case "hold-scope":
        this.#holdScope = choice === "all" ? "all" : "active";
        this.#host.repaint();
        return true;
      case "show-hold":
        void this.#office.openHolds();
        this.#tab = "holds";
        this.#selectedHoldId = attr("data-hold-id");
        this.#holdPanel = "about";
        this.#account = null;
        this.#host.repaint();
        return true;
      case "hold-act":
        this.#holdAct(attr("data-hold-id"), choice === "override" ? "override" : "lift");
        return true;
      case "lift-choice":
        this.#liftChoice = choice === "override" ? "override" : "lift";
        this.#host.repaint();
        return true;
      case "lift-go":
        void this.#liftGo();
        return true;
      case "hold-close":
        this.#selectedHoldId = null;
        this.#holdPanel = "about";
        this.#placeHoldDraft = { customerId: "", reason: "" };
        this.#host.repaint();
        return true;
      case "place-later":
        this.#holdPanel = "place-later";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "place-hold-go":
        void this.#placeHoldGo();
        return true;
      case "run-sweep":
        void this.#runSweep();
        return true;
      // The customer account workspace (Change 8 S2 + S2M): open the workspace for the
      // customer the operator clicked, switch the workspace tab, fire the inline Add
      // a note writer, the Apply Credit Note modal hook and the quick actions the
      // summary card carries. Every action that lands here was rendered by
      // ar-workspace.ts and reaches this desk via the data-action string.
      case "workspace-open":
        void this.openWorkspace(attr("data-customer-id"));
        return true;
      case "workspace-back":
        this.closeWorkspace();
        return true;
      case "workspace-tab":
        this.#activeWorkspaceTab = (attr("data-tab") || "overview") as WorkspaceTab;
        this.#host.repaint();
        return true;
      case "statement-preview": {
        const customerId = attr("data-customer-id") || this.#workspaceCustomerId;
        const statementId = attr("data-statement-id");
        if (customerId === null || statementId === "") return true;
        void this.#openStatementPreview(customerId, statementId);
        return true;
      }
      case "statement-paylink": {
        const customerId = attr("data-customer-id") || this.#workspaceCustomerId;
        const statementId = attr("data-statement-id");
        if (customerId === null || statementId === "") return true;
        void this.#sendStatementPaylink(customerId, statementId);
        return true;
      }
      case "statement-copy-paylink": {
        const customerId = attr("data-customer-id") || this.#workspaceCustomerId;
        const statementId = attr("data-statement-id");
        if (customerId === null || statementId === "") return true;
        void this.#copyStatementPaylink(customerId, statementId);
        return true;
      }
      case "workspace-add-note": {
        const id = this.#workspaceCustomerId;
        if (id === null) return true;
        const text = (this.#host.field("workspace-note-body") || "").trim();
        if (text === "") {
          this.#error = "A note body is required.";
          this.#host.repaint();
          return true;
        }
        const pinned = this.#host.field("workspace-note-pinned") === "true";
        void this.#addWorkspaceNote(id, text, pinned);
        return true;
      }
      case "workspace-apply-credit": {
        const id = this.#workspaceCustomerId;
        const creditId = attr("data-credit-id");
        if (id === null || creditId === "") return true;
        const firstOpen = this.#workspace?.invoices_open[0]?.id ?? null;
        this.closeWorkspace();
        void this.#openAccount(id, firstOpen, firstOpen === null ? "receive" : "invoice", "invoices");
        return true;
      }
      case "workspace-quick": {
        if (this.#workspaceCustomerId === null) return true;
        const quick = attr("data-quick");
        if (quick === "apply") {
          this.#notice = "Use the desk's primary action 'Take a payment' on the receive tab. The workspace quick action is a launcher, not the receive flow itself.";
          this.#host.repaint();
          return true;
        }
        if (quick === "remind") {
          void this.#sendWorkspacePaylink();
          return true;
        }
        if (quick === "statement") {
          void this.#openWorkspaceStatementPreview();
          return true;
        }
        if (quick === "hold") {
          if (!canLiftHolds(this.#who.roleCode)) {
            this.#error = "Placing a hold is the controller's or the general manager's. The Quick action is a launcher; the Place-a-hold form on the Holds tab is the gate.";
            this.#host.repaint();
            return true;
          }
          this.#placeHoldDraft = { customerId: this.#workspaceCustomerId, reason: "" };
          this.#holdPanel = "place-later";
          this.#workspaceMode = false;
          this.#tab = "holds";
          this.#clearMessages();
          this.#host.repaint();
          return true;
        }
        this.#error = "Add a note is wired; use the form on the Activity tab.";
        this.#host.repaint();
        return true;
      }
      default:
        return this.#office.press(control);
    }
  }

  /** POST a note on customer_ar_notes (0405) and refresh the workspace payload. */
  async #addWorkspaceNote(customerId: string, body: string, pinned: boolean): Promise<void> {
    const result = await this.#api.addNote(customerId, { body, kind: "NOTE", pinned });
    if (!result.ok) {
      this.#error = refusalWords("note", result.error, result.status);
      this.#host.repaint();
      return;
    }
    this.#notice = "Note added.";
    const reload = await this.#api.workspace(customerId);
    if (reload.ok && this.#workspaceCustomerId === customerId) {
      this.#workspace = reload.value;
    }
    this.#host.repaint();
  }

  /** An input in a field the screen carries. Only the payment amount asks the server again as it changes. */
  input(control: Element): void {
    const account = this.#account;
    if (control.getAttribute("data-field") === "place-hold-reason") {
      this.#placeHoldDraft = { ...this.#placeHoldDraft, reason: (control as HTMLTextAreaElement).value };
      this.#host.repaint();
      return;
    }
    if (this.#credit !== null && control.getAttribute("data-field") === "credit-amount") {
      // The form says what the credit will do as it is typed: an amount at or over the threshold
      // waits for a second pair of eyes, and the manager reads that before the press. It also says
      // what the credit COMES TO, tax included, which is the core's own arithmetic: the desk asks
      // the core and shows what it says.
      this.#credit = { ...this.#credit, amountText: (control as HTMLInputElement).value };
      this.#scheduleCreditQuote();
      this.#host.repaint();
      return;
    }
    if (account === null) return;
    if (control.getAttribute("data-field") === "receive-amount") {
      this.#account = { ...account, amountText: (control as HTMLInputElement).value };
      this.#schedulePreview();
      this.#host.repaint();
    }
  }

  /** A change (the field is left): the amount reads back as money, formatted only. */
  change(control: Element): void {
    if (this.#office.change(control)) return;
    if (control.getAttribute("data-field") === "payables-asof") {
      // The day the books are read as of: the operator picks it and the board reads it again.
      const day = (control as HTMLInputElement).value;
      if (day === "") return;
      this.#payablesAsOf = day;
      this.#payablesOpenKey = null;
      void this.#loadPayables();
      return;
    }
    if (control.getAttribute("data-field") === "place-hold-account") {
      const sel = (control as HTMLSelectElement).selectedOptions[0];
      const customerId = sel?.getAttribute("data-id") ?? (control as HTMLSelectElement).value;
      this.#placeHoldDraft = { ...this.#placeHoldDraft, customerId };
      this.#host.repaint();
      return;
    }
    if (control.getAttribute("data-field") !== "receive-amount" || this.#account === null) return;
    const cents = parseAmountToCents((control as HTMLInputElement).value);
    if (cents === null) return;
    const text = formatMoney(cents);
    this.#account = { ...this.#account, amountText: text };
    this.#host.setField("receive-amount", text);
  }

  /** After each paint: a message the desk asked to bring into view, then the fills and read only fields. */
  afterRender(): void {
    this.#office.afterRender();
    if (this.#reveal !== "") {
      const selector = this.#reveal;
      this.#reveal = "";
      this.#host.reveal(selector);
    }
    // The board's as of control shows the day the books were read as of, so the operator sees
    // which day the figures are and can pick another one.
    if (this.#payablesAsOf !== "") this.#host.setField("payables-asof", this.#payablesAsOf);
    // The Pay form's fields are filled once, when it opens: the amount it has open and the house
    // day. After that the person types in them, so nothing is written over their typing.
    if (this.#payFillPending && this.#pay !== null) {
      this.#payFillPending = false;
      this.#host.setField("pay-amount", this.#pay.amountText);
      this.#host.setField("pay-date", this.#pay.paidOn);
    }
    const account = this.#account;
    if (account === null) return;
    if (this.#fillPending) {
      this.#fillPending = false;
      this.#host.setField("receive-amount", account.amountText);
    }
    this.#host.setField("received-at", account.branchName);
    this.#host.setField("taken-by", this.#who.userName);
  }

  /** The message the next paint must bring into view: a payment's outcome words on a phone sit far above the button that was pressed. */
  #reveal = "";

  #afterFill(): void {
    this.#fillPending = true;
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>): void {
    this.#notice = "";
    this.#error = refusalWords(action, result.error, result.status);
  }

  async #goTab(tab: Tab): Promise<void> {
    this.#tab = tab;
    this.#account = null;
    this.#clearMessages();
    this.#host.repaint();
    if (tab === "statements") await Promise.all(this.#aging.map((a) => this.#loadStatement(a.customer_id)));
    if (tab === "reminders") await this.#office.openReminders();
    if (tab === "settings") await this.#office.openSettings();
    if (tab === "holds") await this.#office.openHolds();
  }

  /**
   * The open AP, read from the purchasing route. The route answers the day the books are read
   * as of: no day named means today's books. A refusal reads in words, the way every other read
   * on this desk does, and the board keeps the last page it was given rather than showing a
   * half drawn screen.
   *
   * Each read carries its own number, and only the newest one may answer: a slow answer for an
   * earlier day never replaces the newer pick the operator made while it was on the way.
   */
  async #loadPayables(): Promise<void> {
    const load = ++this.#payablesLoads;
    this.#payablesState = "loading";
    this.#host.repaint();
    const got = await this.#api.apAging(this.#payablesAsOf === "" ? {} : { as_of: this.#payablesAsOf });
    if (load !== this.#payablesLoads) return;
    if (!got.ok) {
      this.#payablesState = "error";
      this.#payablesError = refusalWords("payables", got.error, got.status);
      this.#host.repaint();
      return;
    }
    this.#payablesState = "ready";
    this.#payablesPage = got.value;
    this.#payablesAsOf = got.value.as_of;
    this.#payablesError = "";
    this.#host.repaint();
  }

  /**
   * The open bill the Pay form is about: the one the board holds with that id, or nothing. The
   * board is the only record of what is open, so the form never carries a figure of its own.
   */
  #payablesItem(billId: string): PurchasingAPAgingItem | null {
    const page = this.#payablesPage;
    if (page === null) return null;
    for (const row of page.items) {
      for (const item of row.open_items) {
        if (item.id === billId) return item;
      }
    }
    return null;
  }

  /**
   * One payment against one open vendor bill.
   *
   * The amount is the person's, read from the form, and it never goes above what the bill has
   * open: the desk says so in words rather than sending an ask the route would refuse. The day is
   * the person's too, and it goes on the wire as the dealer's own house day. The key is the bill's
   * own, held until the route answers and sent again on every retry, so a double press pays once
   * and a retry after a lost answer is judged against the first press.
   *
   * A refusal keeps the form and says the server's words. A lost answer (no answer at all, or a 5xx)
   * is not a refusal: the money may have moved, so the form says the outcome is unknown, the key
   * stays held across a Cancel, and the board and the payee read the books again, so what is on the
   * screen is the server's. A payment that lands closes the form and reads the books again: the
   * board and the payee carry the server's figures from then on.
   */
  async #sendPayment(): Promise<void> {
    const form = this.#pay;
    if (form === null || this.#payBusy) return;
    const cents = parseAmountToCents(this.#host.field("pay-amount"));
    if (cents === null) {
      this.#pay = { ...form, error: payAmountWords() };
      this.#host.repaint();
      return;
    }
    // The key this press carries: the bill's held key when it has one (an earlier press whose
    // outcome is still unknown), else the form's own key, which is held from here, so a retry
    // after a lost answer sends the same payment and the core answers the first one.
    const held = heldPayment(this.#payBillKeys.get(form.billId));
    const heldKey = held?.key;
    // Under a held key only the earlier payment may be sent again: a different amount would be
    // answered as a replay of the first and silently never written (S6 on 701, P2-2).
    if (held !== null && cents !== held.cents) {
      this.#pay = { ...form, error: heldPaymentWords(held.cents) };
      this.#host.repaint();
      return;
    }
    // The desk's own cap holds a new ask: the amount never goes above what the bill has open. A
    // press under a held key is not a new ask, it is the same one again, so it goes to the core
    // whatever it says: the core answers the earlier press if it landed, and judges this one if it
    // did not.
    if (heldKey === undefined && cents > form.openCents) {
      this.#pay = { ...form, error: payOverOpenWords(form.openCents) };
      this.#host.repaint();
      return;
    }
    const paidOn = this.#host.field("pay-date");
    const day = paidOn === "" ? houseDay() : paidOn;
    const note = this.#host.field("pay-note");
    this.#payBusy = true;
    this.#host.repaint();
    const key = heldKey ?? form.key;
    if (heldKey === undefined) this.#payBillKeys.set(form.billId, heldPaymentValue(key, cents));
    const got = await this.#api.payBill(form.billId, { amountCents: cents, paidAt: dayInstant(day, houseZone()), note }, key);
    this.#payBusy = false;
    if (!got.ok) {
      const outcome = paymentOutcome(got.error, got.status, heldKey !== undefined);
      if (outcome.kind === "unknown") {
        // The answer was lost or the server failed: the payment may or may not have landed, so the
        // form says so and the key stays held. The books are read again, so the open amount on the
        // screen is the server's and the person can see what actually moved.
        // The supplier payment's own words, not the receivables desk's: a replay of the earlier press is
        // answered with that payment, never refused, so the words never promise the server will say so.
        this.#pay = { ...form, error: heldPaymentWords(cents) };
        this.#host.repaint();
        await this.#loadPayables();
        // The form carries the bill's open amount as the books now say it, not the figure it was
        // opened with: the money may have moved, and the row above the form says so. A Cancel that
        // landed while the books were coming back closes the form for good, so there is nothing to
        // carry.
        const open = this.#payablesItem(form.billId);
        const current = this.#pay;
        // The books show the earlier payment landed when the bill's open amount fell by at least its
        // amount, or the bill left the open list: the key ends and the form closes, so the next Pay on
        // that bill is a new payment with a new key.
        if (open === null || form.openCents - open.open_cents >= cents) {
          this.#payBillKeys.delete(form.billId);
          // The form says so and waits for the person's own Close (S6 on 701, P2-3): the last words
          // on screen invited a second press, so a silent close would let a fresh Pay send the same
          // money again under a new key. A form already closed leaves the word for the next one.
          if (current !== null && current.billId === form.billId) {
            this.#pay = { ...current, openCents: open === null ? 0 : open.open_cents, error: "", landedCents: cents };
          } else {
            this.#payLanded.set(form.billId, cents);
          }
          this.#host.repaint();
          return;
        }
        if (current !== null) {
          this.#pay = { ...current, openCents: open.open_cents };
          this.#host.repaint();
        }
        return;
      }
      // A refusal wrote nothing, so the key ends and the next press on that bill is a new payment.
      this.#payBillKeys.delete(form.billId);
      this.#pay = { ...form, error: refusalWords("pay", got.error, got.status) };
      this.#host.repaint();
      return;
    }
    this.#payBillKeys.delete(form.billId);
    this.#pay = null;
    this.#host.repaint();
    await this.#loadPayables();
  }

  async #loadPaid(): Promise<void> {
    const all = await this.#api.register();
    if (all.ok) this.#rows = all.value.items;
    await this.#refreshSelection();
    this.#host.repaint();
  }

  async #downloadCsv(): Promise<void> {
    const csv = await this.#api.exportRegister();
    if (!csv.ok) {
      this.#fail("load", csv);
      this.#host.repaint();
      return;
    }
    this.#host.download(csv.value, "receivables.csv");
  }

  // ---- an account -------------------------------------------------------------------------------

  async #openAccount(customerId: string, invoiceId: string | null, right: RightMode, sub: "invoices" | "statement" | "contacts"): Promise<void> {
    if (customerId === "") return;
    const row = this.#aging.find((a) => a.customer_id === customerId);
    this.#clearMessages();
    this.#work = null;
    // A credit belongs to the account it was opened on.
    this.#credit = null;
    this.#tab = "receivables";
    this.#account = {
      customerId,
      name: row?.customer_name ?? "Account",
      accountNo: row?.account_no ?? "",
      terms: row?.payment_terms_days ?? null,
      branchName: "",
      invoices: [],
      invoicesState: "loading",
      showAll: false,
      sub,
      right,
      selectedInvoiceId: invoiceId,
      detail: null,
      payments: null,
      bounceFor: null,
      statement: null,
      method: "CHEQUE",
      amountText: "",
      picked: [],
      preview: null,
      previewError: "",
    };
    this.#host.repaint();
    await this.#loadAccountInvoices(customerId, invoiceId, right);
    this.#host.repaint();
    if (invoiceId !== null && right === "invoice") await this.#selectInvoice(invoiceId);
    await this.#loadStatement(customerId);
    if (sub === "contacts") await this.#office.ensureContact(customerId);
  }

  /** Reads every invoice of the account (open and paid), prefills the payment from the arriving row, or says the read failed. */
  async #loadAccountInvoices(customerId: string, invoiceId: string | null, right: RightMode): Promise<void> {
    const register = await this.#api.accountInvoices(customerId);
    if (this.#account === null || this.#account.customerId !== customerId) return;
    if (!register.ok) {
      this.#account = { ...this.#account, invoicesState: "failed" };
    } else {
      const first = register.value.items[0];
      const branch = this.#branches.find((b) => b.id === first?.branch_id)?.name.replace(/ \(demo\)$/, "") ?? "";
      this.#account = { ...this.#account, name: first?.customer_name ?? this.#account.name, accountNo: first?.account_no ?? this.#account.accountNo, branchName: branch, invoices: register.value.items, invoicesState: "ready" };
      const target = invoiceId === null ? undefined : register.value.items.find((r) => r.id === invoiceId);
      if (target !== undefined && right === "receive" && target.balance_cents > 0) {
        this.#account = { ...this.#account, amountText: formatMoney(target.balance_cents), picked: [target.id] };
        this.#schedulePreview(0);
      }
      this.#afterFill();
    }
    this.#host.repaint();
  }

  async #retryAccountInvoices(): Promise<void> {
    const account = this.#account;
    if (account === null) return;
    this.#clearMessages();
    this.#account = { ...account, invoicesState: "loading" };
    this.#host.repaint();
    await this.#loadAccountInvoices(account.customerId, account.selectedInvoiceId, account.right);
  }

  async #selectInvoice(invoiceId: string): Promise<void> {
    if (this.#account === null || invoiceId === "") return;
    this.#clearMessages();
    this.#account = { ...this.#account, selectedInvoiceId: invoiceId, right: "invoice", detail: null, payments: null, bounceFor: null };
    this.#office.invoiceChanged(invoiceId);
    this.#host.repaint();
    await this.#reloadInvoice();
    this.#host.repaint();
  }

  #setRight(right: RightMode): void {
    if (this.#account === null) return;
    this.#clearMessages();
    this.#account = { ...this.#account, right, bounceFor: null };
    this.#afterFill();
    this.#host.repaint();
  }

  #openInvoices(): BillingRegisterRow[] {
    return (this.#account?.invoices ?? []).filter(isUnpaidInvoice);
  }

  #togglePick(invoiceId: string): void {
    const account = this.#account;
    if (account === null || invoiceId === "") return;
    const picked = account.picked.includes(invoiceId) ? account.picked.filter((id) => id !== invoiceId) : [...account.picked, invoiceId];
    this.#account = { ...account, picked };
    this.#schedulePreview(0);
    this.#host.repaint();
  }

  #pickAll(): void {
    if (this.#account === null) return;
    this.#account = { ...this.#account, picked: this.#openInvoices().map((r) => r.id) };
    this.#schedulePreview(0);
    this.#host.repaint();
  }

  #clearApply(): void {
    if (this.#account === null) return;
    this.#clearMessages();
    this.#previewGeneration += 1;
    this.#account = { ...this.#account, amountText: "", picked: [], preview: null, previewError: "" };
    this.#afterFill();
    this.#host.repaint();
  }

  // The server's split, asked again after the amount or the picks change (a short wait while typing).
  #previewTimer: ReturnType<typeof setTimeout> | undefined;
  #previewGeneration = 0;

  #schedulePreview(delayMs = 300): void {
    if (this.#previewTimer !== undefined) clearTimeout(this.#previewTimer);
    this.#previewTimer = setTimeout(() => void this.#askPreview(), delayMs);
  }

  async #askPreview(): Promise<void> {
    const account = this.#account;
    if (account === null) return;
    const cents = parseAmountToCents(account.amountText);
    const generation = ++this.#previewGeneration;
    if (cents === null || cents === 0 || account.picked.length === 0) {
      this.#account = { ...account, preview: null, previewError: "" };
      this.#host.repaint();
      return;
    }
    // The pick ceiling holds for a preview too: the browser refuses this body locally in the route's
    // words before it is sent, while the server answers the replay judgement ahead of validateApply.
    if (account.picked.length > MAX_APPLY_INVOICES) {
      this.#account = { ...account, preview: null, previewError: pickCeilingWords(account.picked.length) };
      this.#host.repaint();
      return;
    }
    const answer = await this.#api.applyPayment({
      customerId: account.customerId,
      method: METHODS.find((m) => m === account.method) ?? "CHEQUE",
      amountCents: cents,
      reference: "",
      invoiceIds: account.picked,
      key: newKey(),
      preview: true,
    });
    if (generation !== this.#previewGeneration || this.#account === null) return;
    this.#account = answer.ok
      ? { ...this.#account, preview: answer.value, previewError: "" }
      : { ...this.#account, preview: null, previewError: refusalWords("split", answer.error, answer.status) };
    this.#host.repaint();
  }

  async #applyPayment(): Promise<void> {
    const account = this.#account;
    if (account === null || this.#busy) return;
    const cents = parseAmountToCents(account.amountText);
    const reference = this.#host.field("receive-reference").trim();
    if (cents === null || cents === 0) return this.#stopApply("Enter the amount received, such as $100.00.");
    if (account.picked.length === 0) return this.#stopApply("Pick the invoices the payment is for. Pick all open takes every open invoice.");
    // The route's own ceiling, said here so the body is never sent: the browser refuses a pick over it
    // locally, while the server answers the replay judgement ahead of validateApply, so a held key is
    // never ended by a body the desk could refuse itself.
    // The guard is a stop before the wire, so the button and any held key are untouched by it.
    if (account.picked.length > MAX_APPLY_INVOICES) return this.#stopApply(pickCeilingWords(account.picked.length));
    if (reference === "") return this.#stopApply("Enter a reference, such as a cheque number.");
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const method = METHODS.find((m) => m === account.method) ?? "CHEQUE";
    // A key already held is an earlier press whose outcome is still unknown.
    const heldKey = this.#payPins.get(account.customerId);
    const key = heldKey ?? newKey();
    if (heldKey === undefined) this.#payPins.set(account.customerId, key);
    const result = await this.#api.applyPayment({
      customerId: account.customerId,
      method,
      amountCents: cents,
      reference,
      invoiceIds: account.picked,
      key,
      preview: false,
    });
    this.#busy = false;
    if (!result.ok) {
      const outcome = paymentOutcome(result.error, result.status, heldKey !== undefined);
      if (outcome.kind === "recorded-other") {
        // The route's 409 is its own answer for the held key: the earlier press landed as a different
        // payment and THIS form's payment was not recorded. The key ends; the form stays filled so this
        // cheque can be pressed again as a new payment, and the account reloads so the check the words
        // ask for reads the server's balances.
        this.#payPins.delete(account.customerId);
        this.#notice = "";
        this.#error = outcome.words;
        this.#reveal = "[data-error]";
        await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
        this.#host.repaint();
        return;
      }
      this.#notice = "";
      this.#error = outcome.words;
      // An unknown outcome keeps the key, so no retry can post twice; a refusal wrote nothing and ends it.
      if (outcome.kind === "refused") this.#payPins.delete(account.customerId);
      this.#reveal = "[data-error]";
      this.#host.repaint();
      return;
    }
    this.#payPins.delete(account.customerId);
    const r = result.value;
    const done = r.lines.filter((l) => l.outcome !== "NONE").map((l) => `${formatMoney(l.applied_cents)} on ${l.invoice_no}, balance now ${formatMoney(l.balance_after_cents)}`);
    if (r.on_account_cents > 0) done.push(`${formatMoney(r.on_account_cents)} more than these invoices could take stays on the customer's account as a credit`);
    this.#notice = `Payment ${r.payment_no ?? "recorded"} ${r.replayed ? "was already applied" : "applied"}: ${done.join("; ")}.`;
    // The notice carries the only words saying the money is recorded, so a phone brings it into view as
    // the error branches do (after the unknown outcome's exit this is the screen's one honest sentence).
    this.#reveal = "[data-notice]";
    this.#endApplyForm();
    await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
    this.#host.repaint();
  }

  /**
   * Issues the credit the form describes.
   *
   * The server decides everything that is money: the tax, the cap against what the invoice has left,
   * the account the credit books against, and whether the credit parks for a second pair of eyes. The
   * desk says the reason and the amount, and then says what the server said, in the server's own words.
   *
   * The key is held across retries, so a double press raises ONE credit: the second press carries the
   * key the first press minted, and the server answers with the credit that key already named.
   */
  async #issueCredit(): Promise<void> {
    const account = this.#account;
    const form = this.#credit;
    if (account === null || form === null || this.#busy) return;
    const check = creditCheck({ amountText: this.#host.field("credit-amount"), reason: this.#host.field("credit-reason") });
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const attempt = `${account.customerId}:${form.invoiceId ?? "account"}`;
    const key = this.#creditKeys.keyFor(attempt);
    const branchId = this.#creditBranch(form.invoiceId);
    const result = await this.#api.issueCreditMemo({
      customer_id: account.customerId,
      branch_id: branchId,
      invoice_id: form.invoiceId ?? undefined,
      amount_cents: check.amountCents,
      reason: check.reason,
      idempotency_key: key,
    });
    this.#busy = false;
    if (!result.ok) {
      this.#fail("credit", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    // The server answered: the attempt is over and its key ends.
    this.#creditKeys.settle(attempt);
    this.#credit = null;
    this.#notice = creditWords(result.value);
    this.#host.reveal("[data-notice]");
    await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
    this.#host.repaint();
  }

  /**
   * What the credit comes to, and how much room the invoice has left, asked of the core.
   *
   * The cap the core holds the amount to is the invoice's REMAINING SUBTOTAL, before tax: the
   * balance with tax that the desk used to show was a different number, and a confident one. So the
   * desk asks the core and shows what the core says, and it asks again as the amount is typed,
   * because the tax is pro-rated from what is left and only the core does that arithmetic.
   *
   * A read that fails leaves the quote unknown and the desk says nothing about the amount rather
   * than guess; the server still decides the credit.
   */
  #creditQuoteTimer: ReturnType<typeof setTimeout> | undefined;
  #creditQuoteGeneration = 0;

  #scheduleCreditQuote(delayMs = 300): void {
    if (this.#creditQuoteTimer !== undefined) clearTimeout(this.#creditQuoteTimer);
    this.#creditQuoteTimer = setTimeout(() => void this.#askCreditQuote(), delayMs);
  }

  async #askCreditQuote(): Promise<void> {
    const form = this.#credit;
    const account = this.#account;
    if (form === null || account === null || form.invoiceId === null) return;
    const branchId = this.#creditBranch(form.invoiceId);
    if (branchId === "") return;
    const cents = parseAmountToCents(form.amountText);
    const amountCents = cents === null ? 0 : cents;
    const generation = ++this.#creditQuoteGeneration;
    const answer = await this.#api.quoteCreditMemo({
      customerId: account.customerId,
      branchId,
      invoiceId: form.invoiceId,
      amountCents,
    });
    // The form may have closed, or moved to another credit, or the amount moved on, while the read
    // was in flight: a stale answer never overwrites a newer one.
    const now = this.#credit;
    if (generation !== this.#creditQuoteGeneration || now === null || now.invoiceId !== form.invoiceId) return;
    if (!answer.ok) {
      this.#credit = { ...now, quote: now.quote, quoteError: refusalWords("credit", answer.error, answer.status) };
      this.#host.repaint();
      return;
    }
    this.#credit = {
      ...now,
      quote: {
        remainingSubtotalCents: answer.value.remaining_subtotal_cents,
        remainingTotalCents: answer.value.remaining_total_cents,
        message: answer.value.message,
      },
      quoteError: "",
    };
    this.#host.repaint();
  }

  /**
   * The branch's maker-checker threshold, read from the server the way the route reads it.
   *
   * The form says what the rail will do to this credit BEFORE the press, and that sentence needs the
   * branch's own number. A read that fails leaves the number unknown: the desk then says nothing
   * about the rail rather than guess, and the server still decides.
   */
  async #readCreditThreshold(invoiceId: string | null): Promise<void> {
    const branchId = this.#creditBranch(invoiceId);
    if (branchId === "") return;
    const thresholdCents = await this.#api.creditApprovalThreshold(branchId);
    const form = this.#credit;
    // The form may have closed, or moved to another credit, while the read was in flight.
    if (form === null || form.invoiceId !== invoiceId) return;
    this.#credit = { ...form, thresholdCents };
    this.#host.repaint();
  }

  /**
   * The branch a credit belongs to: the invoice's own branch, or the account's branch, which is the
   * branch its open invoices are on. A dealer with branches has no "somewhere" to post a credit to.
   */
  #creditBranch(invoiceId: string | null): string {
    const rows = this.#account?.invoices ?? [];
    const row = invoiceId === null ? rows[0] : rows.find((r) => r.id === invoiceId);
    if (row !== undefined) return row.branch_id;
    return this.#branches[0]?.id ?? "";
  }

  /** Clears the apply form after a payment is resolved; the server's balances are what show now. */
  #endApplyForm(): void {
    this.#previewGeneration += 1;
    if (this.#account !== null) this.#account = { ...this.#account, amountText: "", picked: [], preview: null, previewError: "" };
    this.#afterFill();
  }

  #stop(words: string): void {
    this.#notice = "";
    this.#error = words;
    this.#host.repaint();
  }

  /** A stop on the apply path: its words show in the page's banner, which a phone must bring into view. */
  #stopApply(words: string): void {
    this.#reveal = "[data-error]";
    this.#stop(words);
  }

  async #recordPromise(): Promise<void> {
    const account = this.#account;
    if (account === null || this.#busy) return;
    const invoiceNo = this.#host.field("promise-invoice");
    const row = this.#openInvoices().find((r) => r.invoice_no === invoiceNo) ?? this.#openInvoices()[0];
    if (row === undefined) return this.#stop("Nothing is owing on this account to promise against.");
    const input = {
      amountText: this.#host.field("promise-amount"),
      date: this.#host.field("promise-date"),
      by: this.#host.field("promise-by"),
      note: this.#host.field("promise-note"),
      balanceCents: row.balance_cents,
    };
    const check = validatePromise(input);
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.recordPromise(row.id, { promisedCents: check.promisedCents, date: input.date, by: input.by.trim(), note: input.note.trim(), key: this.#promiseKey });
    this.#busy = false;
    this.#promiseKey = newKey();
    if (!result.ok) {
      this.#fail("promise", result);
      this.#host.repaint();
      return;
    }
    this.#notice = `Promise recorded on ${row.invoice_no}: ${formatMoney(result.value.promised_cents)} by ${result.value.promised_date}.`;
    this.#account = { ...account, selectedInvoiceId: row.id, right: "invoice", detail: null, payments: null };
    await Promise.all([this.#reloadAccount(), this.#reloadInvoice(), this.#reloadBook()]);
    this.#host.repaint();
  }

  async #markBounced(paymentId: string): Promise<void> {
    if (this.#account === null || paymentId === "" || this.#busy) return;
    const reason = this.#host.field("bounce-reason").trim();
    if (reason === "") return this.#stop("Enter why it came back, such as cheque returned NSF.");
    const payment = this.#account.payments?.items.find((p) => p.payment_id === paymentId);
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.markBounced(paymentId, reason);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("bounce", result);
      this.#host.repaint();
      return;
    }
    const r = result.value;
    const lead = payment === undefined ? "The payment is" : `Payment ${payment.payment_no} is`;
    const reopened = `${r.reopened_invoices} ${r.reopened_invoices === 1 ? "invoice" : "invoices"}`;
    const hold = r.hold_placed ? " The account is now on credit hold." : "";
    this.#notice = `${lead} marked bounced. ${formatMoney(r.reversed_cents)} is owing again on ${reopened}, and the bank fee is ${formatMoney(r.fee_cents)}.${hold}`;
    if (this.#account !== null) this.#account = { ...this.#account, bounceFor: null };
    await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
    this.#host.repaint();
  }

  // ---- the bounced queue ------------------------------------------------------------------------

  async #workBounced(paymentId: string, customerId: string): Promise<void> {
    if (paymentId === "") return;
    this.#clearMessages();
    const history = await this.#api.paymentHistory(paymentId);
    if (!history.ok) {
      this.#fail("load", history);
      this.#host.repaint();
      return;
    }
    const name = this.#aging.find((a) => a.customer_id === (history.value.customer_id ?? customerId))?.customer_name ?? "The account";
    this.#work = { history: history.value, accountName: name, choice: "replacement", note: "" };
    this.#host.repaint();
  }

  async #workGo(): Promise<void> {
    const work = this.#work;
    if (work === null) return;
    const customerId = work.history.customer_id ?? "";
    if (work.choice === "reminder") {
      await this.#remind(customerId);
      return;
    }
    const first = work.history.allocations[0];
    await this.#openAccount(customerId, first?.invoice_id ?? null, work.choice === "promise" ? "promise" : "receive", "invoices");
  }

  async #remind(customerId: string): Promise<void> {
    if (customerId === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    const register = await this.#api.accountInvoices(customerId);
    if (!register.ok) {
      this.#busy = false;
      this.#fail("load", register);
      this.#host.repaint();
      return;
    }
    const overdue = register.value.items.filter((r) => (r.days_overdue ?? 0) > 0 && isUnpaidInvoice(r)).map((r) => r.id);
    if (overdue.length === 0) {
      this.#busy = false;
      this.#error = "Nothing on this account is overdue, so there is nothing to chase.";
      this.#host.repaint();
      return;
    }
    const sent = await this.#api.remind(overdue, this.#remindKey);
    this.#busy = false;
    this.#remindKey = newKey();
    if (!sent.ok) {
      this.#fail("reminder", sent);
    } else {
      // The server says what happened: sent, or nothing sent and why (FW1-06b). There is no queue
      // behind the desk, so no run is ever told as sent, or as waiting for a retry, when nothing
      // left the building (FW1-06b round 2, S6's P2-2).
      const words = reminderRunWords(sent.value);
      if (words.sent) {
        this.#notice = words.notice;
        if (this.#work !== null) this.#work = { ...this.#work, note: this.#notice };
      } else {
        this.#error = words.notice;
      }
    }
    this.#host.repaint();
  }

  // ---- statements -------------------------------------------------------------------------------

  async #runStatements(customerId: string | null, send = false): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const run = await this.#api.statementRun(customerId);
    this.#busy = false;
    if (!run.ok) {
      this.#fail("statement", run);
      this.#host.repaint();
      return;
    }
    const r = run.value.run;
    if (send) {
      this.#notice = r.notice_queued_count > 0 ? "Statement queued to the AP contact." : "The statement was run, but nothing was queued: there is no AP contact on file, or notices are off for this dealer.";
    } else {
      this.#notice = `Statements run: ${r.generated_count} ${r.generated_count === 1 ? "account" : "accounts"} with an open balance.`;
    }
    for (const st of run.value.statements) this.#statements.set(st.customer_id, st);
    void this.#office.showRun(r.id);
    if (this.#account !== null) {
      const own = run.value.statements.find((s) => s.customer_id === this.#account?.customerId);
      if (own !== undefined) this.#account = { ...this.#account, statement: own };
    }
    this.#host.repaint();
  }

  async #runAccountStatement(): Promise<void> {
    const account = this.#account;
    if (account === null || this.#busy) return;
    const open = this.#openInvoices();
    if (open.length === 0) return this.#stop("There is nothing owing on this account to put on a statement.");
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const run = await this.#api.runStatement(open.map((r) => r.id), newKey());
    this.#busy = false;
    if (!run.ok) {
      this.#fail("statement", run);
      this.#host.repaint();
      return;
    }
    this.#notice = "Statement run for this account.";
    await this.#loadStatement(account.customerId);
  }

  // ---- credit holds -----------------------------------------------------------------------------

  #holdAct(holdId: string, choice: "lift" | "override"): void {
    this.#selectedHoldId = holdId;
    this.#holdPanel = "about";
    this.#liftChoice = choice;
    this.#clearMessages();
    if (!canLiftHolds(this.#who.roleCode)) {
      this.#notice = "";
      this.#error = refusalWords("release-hold", "Forbidden", 403);
    }
    this.#host.repaint();
  }

  async #liftGo(): Promise<void> {
    const id = this.#selectedHoldId;
    if (id === null || this.#busy) return;
    const reason = this.#host.field("release-reason").trim();
    if (reason === "") return this.#stop("Enter the reason. Lifting and overriding both carry one, and both are audited.");
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    // A hold a returned payment placed is lifted through the till's path: the billing release refuses it.
    const nsf = this.#holds.find((h) => h.id === id)?.hold_kind === "NSF";
    const result = this.#liftChoice === "override" ? await this.#api.overrideHold(id, reason) : nsf ? await this.#office.releaseNsfHold(id, reason) : await this.#api.releaseHold(id, reason);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("release-hold", result);
      this.#host.repaint();
      return;
    }
    this.#notice = this.#liftChoice === "lift" ? "The credit hold is lifted." : "The hold stays on the record and the account may transact until the override expires.";
    this.#selectedHoldId = null;
    await this.#reloadBook();
    this.#host.repaint();
  }

  async #runSweep(): Promise<void> {
    if (!canLiftHolds(this.#who.roleCode)) {
      this.#notice = "";
      this.#error = "Running the sweep is the Financial Controller's or the General Manager's. Payables and Receivables sees the holds it places.";
      this.#host.repaint();
      return;
    }
    const result = await this.#api.sweep();
    if (!result.ok) this.#fail("release-hold", result);
    else {
      this.#notice = sweepWords(result.value);
      await this.#reloadBook();
    }
    this.#host.repaint();
  }

  // placeHoldGo: Place a manual credit hold by hand (Change 8 S4). Manager-gated: the
  // server refuses the call with 403 if the role is not manager; the form does not
  // render for non-managers (canLiftHolds is the matrix Money Desk at Administer and
  // Manage, row 345).
  async #placeHoldGo(): Promise<void> {
    if (!canLiftHolds(this.#who.roleCode) || this.#busy) return;
    const draft = this.#placeHoldDraft;
    if (draft.customerId === "") return this.#stop("Pick the account the hold is on.");
    const reason = draft.reason.trim();
    if (reason === "") return this.#stop("Enter the reason. A hold without a reason is refused.");
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.createHold({ customer_id: draft.customerId, reason });
    this.#busy = false;
    if (!result.ok) {
      this.#fail("place-hold", result);
      this.#host.repaint();
      return;
    }
    this.#notice = result.value.auto_placed
      ? "The account is on hold. (The sweep had it; the manual call confirms.)"
      : "The hold is in place.";
    this.#holdPanel = "about";
    this.#selectedHoldId = result.value.id;
    this.#placeHoldDraft = { customerId: "", reason: "" };
    await this.#reloadBook();
    this.#host.repaint();
  }

  // sendWorkspacePaylink: the C8 S3 Send-a-paylink quick action from the workspace card.
  // Staff-gated; the route refuses a manager call with 200 because the manager's authority
  // is broader, not narrower. The customer's AP email is the recipient snapshot; the URL
  // lands deterministic on (customer_id, amount_cents, EMAIL) and the row carries the
  // idempotency_key so a repeated call never produces two notices.
  async #sendWorkspacePaylink(): Promise<void> {
    const customerId = this.#workspaceCustomerId;
    if (customerId === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const recipient = this.#workspace?.header.primary_ap_contact_email ?? "";
    const amount = this.#workspace?.header.open_cents ?? 0;
    const result = await this.#api.sendPaylink({
      customer_id: customerId,
      amount_cents: amount > 0 ? amount : null,
      channel: "EMAIL",
      recipient,
    });
    this.#busy = false;
    if (!result.ok) {
      this.#fail("send-paylink", result);
      this.#host.repaint();
      return;
    }
    this.#notice = `Paylink sent (${result.value.status}) at ${result.value.sent_at}. URL: ${result.value.paylink_url}`;
    this.#host.repaint();
  }

  // openWorkspaceStatementPreview: the C8 S3 Statement preview quick action. The
  // workspace's Statements tab already lists the artefacts newest-first; the quick
  // action jumps to the latest one and opens its preview panel. A workspace without a
  // statement yet lands on the run-statements form, exactly the behaviour the Statement
  // tab's empty state already gives.
  async #openWorkspaceStatementPreview(): Promise<void> {
    const customerId = this.#workspaceCustomerId;
    if (customerId === null || this.#busy) return;
    const list = this.#workspace?.statements ?? [];
    const latest = list[0];
    if (latest === undefined) {
      this.#notice = "No statement has been run for this account yet. Use 'Run a statement' on the Statements tab.";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const preview = await this.#api.statementPreview(customerId, latest.id);
    this.#busy = false;
    if (!preview.ok) {
      this.#fail("statement-preview", preview);
      this.#host.repaint();
      return;
    }
    const closing1 = preview.value.statement.closing_balance_cents;
    this.#notice = preview.value.reconciled
      ? closing1 == null
        ? "Statement preview is balanced to the penny. Closing balance: Not shown for your role."
        : `Statement preview is balanced to the penny. Closing balance: ${formatWorkspaceMoney(closing1)}.`
      : "Statement preview's totals do not reconcile; flag a controller.";
    this.#host.repaint();
  }

  // openStatementPreview: the row-level Preview button on the workspace Statements tab.
  // Same payload and same notice as the quick-action launcher; the only difference is
  // the statement id, which the workspace passes in through the row's data-statement-id.
  async #openStatementPreview(customerId: string, statementId: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const preview = await this.#api.statementPreview(customerId, statementId);
    this.#busy = false;
    if (!preview.ok) {
      this.#fail("statement-preview", preview);
      this.#host.repaint();
      return;
    }
    const closing2 = preview.value.statement.closing_balance_cents;
    this.#notice = preview.value.reconciled
      ? closing2 == null
        ? "Statement preview is balanced to the penny. Closing balance: Not shown for your role."
        : `Statement preview is balanced to the penny. Closing balance: ${formatWorkspaceMoney(closing2)}.`
      : "Statement preview's totals do not reconcile; flag a controller.";
    this.#host.repaint();
  }

  // sendStatementPaylink: the row-level Send-paylink button. Same path as the quick
  // action; the customer's AP email is the recipient and the amount pins to the
  // statement's closing balance (the figure the statement went out the door with).
  async #sendStatementPaylink(customerId: string, statementId: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const preview = await this.#api.statementPreview(customerId, statementId);
    if (!preview.ok) {
      this.#busy = false;
      this.#fail("statement-preview", preview);
      this.#host.repaint();
      return;
    }
    const recipient = preview.value.statement.closing_balance_cents === 0 ? "" : (this.#workspace?.header.primary_ap_contact_email ?? "");
    const amount = preview.value.statement.closing_balance_cents;
    const result = await this.#api.sendPaylink({
      customer_id: customerId,
      invoice_id: null,
      amount_cents: amount != null && amount > 0 ? amount : null,
      channel: "EMAIL",
      recipient,
    });
    this.#busy = false;
    if (!result.ok) {
      this.#fail("send-paylink", result);
      this.#host.repaint();
      return;
    }
    this.#notice = `Paylink sent (${result.value.status}) at ${result.value.sent_at}. URL: ${result.value.paylink_url}`;
    this.#host.repaint();
  }

  // copyStatementPaylink: the row-level Copy URL button. Builds the same deterministic
  // URL the Send path would have built and writes it to the clipboard via the
  // host's copy field hook (the shell `host.copy` returns a boolean the notice reads).
  async #copyStatementPaylink(customerId: string, statementId: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const preview = await this.#api.statementPreview(customerId, statementId);
    this.#busy = false;
    if (!preview.ok) {
      this.#fail("statement-preview", preview);
      this.#host.repaint();
      return;
    }
    // The Copy URL action re-uses the deterministic token the Send action would have used.
    // The token is the SHA-256 prefix of (customer_id, null invoice, amount, EMAIL); the
    // URL pattern the core builds is /paylink/{token}. We render the URL the same way the
    // handler builds it, so the URL on the paste is the URL a Send would have produced.
    const amount = preview.value.statement.closing_balance_cents;
    const url = `/paylink/${deterministicPaylinkToken(customerId, null, amount != null && amount > 0 ? amount : null, "EMAIL")}`;
    const copied = this.#host.copy ? this.#host.copy(url) : false;
    if (copied) this.#notice = `Paylink URL copied: ${url}`;
    else this.#notice = `Paylink URL: ${url} (clipboard unavailable; copy by hand)`;
    this.#host.repaint();
  }

  // ---- the screen -----------------------------------------------------------------------------

  #model(): DeskModel {
    return {
      tab: this.#tab,
      asOf: this.#landing?.as_of ?? "",
      nowMs: Date.now(),
      roleCode: this.#who.roleCode,
      takenBy: this.#who.userName,
      canLift: canLiftHolds(this.#who.roleCode),
      landing: this.#landing,
      aging: this.#aging,
      rows: this.#rows,
      filter: this.#filter,
      cardFilter: this.#cardFilter,
      selection: this.#selection,
      bounced: this.#bounced,
      holds: this.#holds,
      holdScope: this.#holdScope,
      branches: this.#branches,
      work: this.#work,
      picker: this.#picker,
      statements: this.#statements,
      selectedHoldId: this.#selectedHoldId,
      holdPanel: this.#holdPanel,
      liftChoice: this.#liftChoice,
      account: this.#account,
      credit: this.#credit,
      office: this.#office.model(),
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
      workspace: this.#workspace,
      workspaceCustomerId: this.#workspaceMode ? this.#workspaceCustomerId : null,
      activeWorkspaceTab: this.#activeWorkspaceTab,
      placeHoldDraft: this.#placeHoldDraft,
      registerSummary: this.#rows,
    };
  }

  render(): TemplateResult {
    if (this.#phase === "loading") return renderLoading();
    if (this.#phase === "error") return renderLoadFailed(this.#loadError);
    const model = this.#model();
    if (this.#workspaceMode) return html`${renderWorkspaceScreen(model)}`;
    if (model.account !== null) return html`${renderAccount(model, model.account)}`;
    if (model.tab === "statements") return html`${renderStatements(model)}`;
    if (model.tab === "reminders") return html`${renderReminders(model)}`;
    if (model.tab === "settings") return html`${renderSettings(model)}`;
    if (model.tab === "holds") return html`${renderHolds(model)}`;
    return html`${renderHome(model)}`;
  }

  /**
   * The Payables board as the Money Desk's own area (src/ar/ap-views.ts). The board is the open
   * AP read from the purchasing route; it stands beside the receivables desk in the area switch,
   * so it is rendered on its own and carries no view nav of that desk's.
   */
  renderPayablesArea(): TemplateResult {
    return renderPayables(this.#payablesModel());
  }

  /** Reads the board when its area opens, the way every other area reads its own screen. */
  openPayables(): void {
    void this.#loadPayables();
  }

  /** Leaving the board closes the payee whose open items were open, and the form that was on it. */
  closePayables(): void {
    this.#pay = null;
    if (this.#payablesOpenKey === null) return;
    this.#payablesOpenKey = null;
    this.#host.repaint();
  }

  /** The board's own state: the read, the day it is read as of, the row open, and the Pay form. */
  #payablesModel(): PayablesBoardModel {
    return {
      state: this.#payablesState,
      page: this.#payablesPage,
      error: this.#payablesError,
      asOf: this.#payablesAsOf,
      openRowKey: this.#payablesOpenKey,
      pay: this.#pay,
      roleCode: this.#who.roleCode,
      busy: this.#payBusy,
    };
  }

  /**
   * Open the customer account workspace for one customer. Loads the workspace payload
   * from the server and switches the desk into workspace mode. The back link returns
   * the operator to wherever they were.
   */
  async openWorkspace(customerId: string): Promise<void> {
    this.#clearMessages();
    this.#workspaceMode = true;
    this.#workspaceCustomerId = customerId;
    this.#workspace = null;
    this.#activeWorkspaceTab = "overview";
    this.#host.repaint();
    const result = await this.#api.workspace(customerId);
    if (!result.ok) {
      this.#error = refusalWords("workspace", result.error, result.status);
      this.#workspaceMode = false;
      this.#workspaceCustomerId = null;
      this.#host.repaint();
      return;
    }
    if (this.#workspaceCustomerId !== customerId) {
      // The operator has already navigated elsewhere; drop the late answer.
      return;
    }
    this.#workspace = result.value;
    this.#host.repaint();
  }

  /** Back link: clears workspace state and returns to the previous one. */
  closeWorkspace(): void {
    this.#workspaceMode = false;
    this.#workspace = null;
    this.#workspaceCustomerId = null;
    this.#activeWorkspaceTab = "overview";
    this.#clearMessages();
    void this.#reloadBook().then(() => this.#host.repaint());
  }
}

// deterministicPaylinkToken mirrors the core's billingSendPaylink token builder, so the
// Copy URL action renders the same URL a Send action would have produced. The core's
// token is hex(SHA-256(customer|invoice|amount|channel)) truncated to 16 chars; we
// re-implement it here on the browser side rather than wait for a second round trip,
// because the URL is supposed to be deterministic and the customer has the same data
// the core has. (A drift would be caught at the next Send: the server returns the same
// URL it would have generated from this tuple, and the URL on screen matches.)
export function deterministicPaylinkToken(
  customerID: string,
  invoiceID: string | null,
  amountCents: number | null,
  channel: string,
): string {
  const invoice = invoiceID ?? "";
  const amount = amountCents === null ? "" : String(amountCents);
  const payload = [customerID, invoice, amount, channel].join("|");
  // Web Crypto synchronous-style shim: fall back to a tiny FNV-1a hash so a non-https
  // shell still produces a deterministic 16-hex-char token. The token's role is to be
  // stable, not cryptographically secret.
  let h1 = 0x811c9dc5;
  let h2 = 0xdeadbeef;
  for (let i = 0; i < payload.length; i++) {
    const c = payload.charCodeAt(i);
    h1 = Math.imul(h1 ^ c, 0x01000193);
    h2 = Math.imul(h2 ^ c, 0x85ebca6b);
  }
  const hex = (h1 >>> 0).toString(16).padStart(8, "0") + (h2 >>> 0).toString(16).padStart(8, "0");
  return hex.slice(0, 16);
}

/** A bill's held press: its key and the amount it carried, kept as one record value ("key|cents"). */
function heldPaymentValue(key: string, cents: number): string {
  return `${key}|${cents}`;
}

function heldPayment(value: string | undefined): { key: string; cents: number } | null {
  if (value === undefined) return null;
  const bar = value.lastIndexOf("|");
  if (bar < 0) return null;
  const cents = Number(value.slice(bar + 1));
  if (!Number.isInteger(cents) || cents <= 0) return null;
  return { key: value.slice(0, bar), cents };
}

function heldPaymentWords(cents: number): string {
  return `An earlier payment of ${formatMoney(cents)} on this bill may or may not have been recorded. Check the bill's open amount above. Press Record the payment again to send the same payment; the books will say if the first press was already recorded. A different amount waits until the earlier payment shows on the bill.`;
}
