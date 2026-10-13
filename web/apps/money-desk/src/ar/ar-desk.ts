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
import type { BillingRegisterRow, Result } from "@hardscapehouse/api-client";
import { ArApi, type PaymentMethod } from "./ar-api.js";
import { HeldKeys, newKey } from "./held-keys.js";
import { canLiftHolds, MAX_APPLY_INVOICES, paymentOutcome, pickCeilingWords, refusalWords, validatePromise } from "./ar-logic.js";
import { formatMoney, parseAmountToCents } from "./ar-money.js";
import { OfficeDesk, type OfficeHost } from "./office-desk.js";
import {
  renderAccount,
  renderHolds,
  renderHome,
  renderLoadFailed,
  renderLoading,
  renderReminders,
  renderSettings,
  renderStatements,
  shownInvoiceIds,
  type AccountModel,
  type BouncedWork,
  type DeskModel,
  type PayMethod,
  type RegisterFilter,
  type RightMode,
  type Tab,
  type WorkChoice,
} from "./ar-views.js";

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
  readonly mountDocument: (selector: string, html: string) => void;
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
  #landing: DeskModel["landing"] = null;
  #aging: DeskModel["aging"] = [];
  #rows: readonly BillingRegisterRow[] = [];
  #filter: RegisterFilter = "open";
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

  // One pending payment per account: the key is minted when the payment is first sent and sent again for every
  // retry, whatever the form now says (a corrected cheque number, another method, a changed amount, other
  // invoices), until the outcome is known: the payment route's own answer (recorded, its refusal, or "already
  // recorded"). A payments list, a reload, a sign out and a sign in of the same user never end it; another user
  // never sees it. The route refuses a changed body under a spent key in words, so a lost answer can never post
  // the same money twice, and a payment on another account never touches this one's key.
  readonly #payPins: HeldKeys;
  /** The office: statement run read back, AP contacts, reminders, dispute, settings (office-desk.ts). */
  readonly #office: OfficeDesk;
  #promiseKey = newKey();
  #remindKey = newKey();

  constructor(api: ArApi, host: DeskHost, who: DeskWho) {
    this.#api = api;
    this.#host = host;
    this.#who = who;
    this.#payPins = new HeldKeys(who.userId);
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
    if (bounced.ok) this.#bounced = bounced.value.items;
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
    if (bounced.ok) this.#bounced = bounced.value.items;
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
    const register = await this.#api.register({ chip: "PAID", customerId: account.customerId });
    if (!register.ok || this.#account === null) return;
    this.#account = { ...this.#account, invoices: register.value.items };
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
      case "tab":
        void this.#goTab(attr("data-tab") as Tab);
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
        this.#clearMessages();
        this.#host.repaint();
        this.#afterFill();
        return true;
      case "invoice-scope":
        if (this.#account !== null) this.#account = { ...this.#account, showAll: choice === "all" };
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
        this.#host.repaint();
        return true;
      case "place-later":
        this.#holdPanel = "place-later";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "run-sweep":
        void this.#runSweep();
        return true;
      default:
        return this.#office.press(control);
    }
  }

  /** An input in a field the screen carries. Only the payment amount asks the server again as it changes. */
  input(control: Element): void {
    const account = this.#account;
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

  async #loadPaid(): Promise<void> {
    const all = await this.#api.register({ chip: "PAID" });
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
    this.#tab = "receivables";
    this.#account = {
      customerId,
      name: row?.customer_name ?? "Account",
      accountNo: row?.account_no ?? "",
      terms: row?.payment_terms_days ?? null,
      branchName: "",
      invoices: [],
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
    const register = await this.#api.register({ chip: "PAID", customerId });
    if (this.#account === null || this.#account.customerId !== customerId) return;
    if (!register.ok) {
      this.#fail("load", register);
    } else {
      const first = register.value.items[0];
      const branch = this.#branches.find((b) => b.id === first?.branch_id)?.name.replace(/ \(demo\)$/, "") ?? "";
      this.#account = { ...this.#account, name: first?.customer_name ?? this.#account.name, accountNo: first?.account_no ?? this.#account.accountNo, branchName: branch, invoices: register.value.items };
      const target = invoiceId === null ? undefined : register.value.items.find((r) => r.id === invoiceId);
      if (target !== undefined && right === "receive" && target.balance_cents > 0) {
        this.#account = { ...this.#account, amountText: formatMoney(target.balance_cents), picked: [target.id] };
        this.#schedulePreview(0);
      }
      this.#afterFill();
    }
    this.#host.repaint();
    if (invoiceId !== null && right === "invoice") await this.#selectInvoice(invoiceId);
    await this.#loadStatement(customerId);
    if (sub === "contacts") await this.#office.ensureContact(customerId);
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
    return (this.#account?.invoices ?? []).filter((r) => r.status !== "PAID");
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
    // The pick ceiling holds for a preview too: the route would refuse this body ahead of its replay
    // judgement, so the split area says the route's words and no such body is sent.
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
    // The route's own ceiling, said here so the body is never sent: a pick over it is refused ahead of
    // the route's replay judgement, which would end a held key for a body the desk could refuse itself.
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
      if (outcome.kind === "recorded") {
        // The route's replay refusal is how it says the earlier press landed: the outcome is known,
        // the payment is ended, and the account is reloaded so the person reads the server's balances.
        this.#payPins.delete(account.customerId);
        this.#endApplyForm();
        this.#notice = outcome.words;
        this.#reveal = "[data-notice]";
        await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
        this.#host.repaint();
        return;
      }
      if (outcome.kind === "recorded-other") {
        // The route's refusal is its own answer for the held key: the earlier press landed as a different
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
    this.#notice = `Payment ${r.payment_no ?? "recorded"} ${r.replayed ? "was already applied" : "applied"}: ${done.join("; ")}.`;
    // The notice carries the only words saying the money is recorded, so a phone brings it into view as
    // the error branches do (after the unknown outcome's exit this is the screen's one honest sentence).
    this.#reveal = "[data-notice]";
    this.#endApplyForm();
    await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
    this.#host.repaint();
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
    const register = await this.#api.register({ chip: "PAID", customerId });
    const overdue = register.ok ? register.value.items.filter((r) => r.days_overdue > 0 && r.status !== "PAID").map((r) => r.id) : [];
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
      const s = sent.value;
      this.#notice = `Reminder sent for ${s.sent} ${s.sent === 1 ? "invoice" : "invoices"} to the AP contact.${s.replayed > 0 ? ` ${s.replayed} had already been sent.` : ""}`;
      if (this.#work !== null) this.#work = { ...this.#work, note: this.#notice };
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
      this.#notice = `The sweep checked ${result.value.evaluated} accounts and placed ${result.value.placed} ${result.value.placed === 1 ? "hold" : "holds"}.`;
      await this.#reloadBook();
    }
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
      office: this.#office.model(),
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
    };
  }

  render(): TemplateResult {
    if (this.#phase === "loading") return renderLoading();
    if (this.#phase === "error") return renderLoadFailed(this.#loadError);
    const model = this.#model();
    if (model.account !== null) return html`${renderAccount(model, model.account)}`;
    if (model.tab === "statements") return html`${renderStatements(model)}`;
    if (model.tab === "reminders") return html`${renderReminders(model)}`;
    if (model.tab === "settings") return html`${renderSettings(model)}`;
    if (model.tab === "holds") return html`${renderHolds(model)}`;
    return html`${renderHome(model)}`;
  }
}
