// The receivables office's controller: the state its screens draw from, the loads, and what each press does. It
// sits beside the receivables desk (ar-desk.ts) the same way the ledger's controller sits beside it: not an element,
// handed a way to repaint, to read and set form fields and to say a message, and asked for a model to draw.
//
// Every write is held to the server: nothing here changes what the screen shows until the core has answered, and
// the screen then shows the core's answer. A refusal reads in words (office-logic.ts), a walled move in words naming
// who may do it. Each write that carries a key sends one key per attempt and the same key again on a retry whose
// answer was lost. No address or number a customer gave is ever put on screen.
import type {
  BillingAgingRow,
  BillingAPContactRead,
  BillingLandingPolicy,
  BillingPaperPathEntry,
  BillingRegisterRow,
  BillingReminderSend,
  BillingStatementRunResult,
  Result,
} from "@hardscapehouse/api-client";
import { OfficeApi } from "./office-api.js";
import { SettingsDesk, type SettingsModel } from "./office-settings.js";
import { newKey } from "./held-keys.js";
import {
  AttemptKeys,
  disputeCheck,
  officeRefusal,
  outcomeUnknown,
  paperResolveCheck,
  reminderResultWords,
  reversalCheck,
  reversalWords,
  SAVED_NOTICE,
  sendInvoiceWords,
  validateApContact,
  DISPUTE_OUTCOMES,
  dispatchWords,
  type Channel,
} from "./office-logic.js";
import { canLiftHolds } from "./ar-logic.js";

export interface OfficeHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** Brings the first element matching the selector into view and focuses it. */
  readonly reveal: (selector: string) => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string, key?: string) => string;
  /** Sets a form field's text; the screens bind no value, so a fill goes through here. */
  readonly setField: (name: string, value: string, key?: string) => void;
  /** Says a message in the desk's notice banner (and clears the error). */
  readonly notice: (words: string) => void;
  /** Says a refusal in the desk's error banner (and clears the notice). */
  readonly error: (words: string) => void;
  /** The accounts the aging lists: the choices a reminder or a history filter offers. */
  readonly accounts: () => readonly BillingAgingRow[];
  /** The dealer's receivables policy as the landing read it. */
  readonly policy: () => BillingLandingPolicy | null;
  /** Reads the book again (cards, aging, register, holds, Needs you) after a write that moves it. */
  readonly reloadBook: () => Promise<void>;
  /** Reads the open account's invoices and the selected invoice again after a write that moves them. */
  readonly reloadAccount: () => Promise<void>;
  /** Puts an invoice document's HTML into the element matching the selector, in a sandboxed frame. */
  readonly mountDocument: (selector: string, html: string) => void;
}

/** The panel an invoice's extra actions open, and what it is about. */
export type InvoiceMode = "none" | "document" | "dispute" | "resolve";

export interface OfficeWho {
  readonly userId: string;
  readonly roleCode: string;
  readonly userName: string;
}

export type RunView =
  | { readonly phase: "none" }
  | { readonly phase: "loading" }
  | { readonly phase: "error"; readonly words: string }
  | { readonly phase: "ready"; readonly result: BillingStatementRunResult };

export type Listing<T> = { readonly phase: "none" | "loading" } | { readonly phase: "error"; readonly words: string } | { readonly phase: "ready"; readonly items: readonly T[] };

export interface RemindForm {
  readonly customerId: string;
  /** The account's invoices, once read: "loading" until then. */
  readonly invoices: readonly BillingRegisterRow[] | "loading";
  readonly invoiceId: string;
  readonly channel: Channel;
  readonly resend: boolean;
  readonly confirm: boolean;
}

export type DocumentView = { readonly phase: "none" } | { readonly phase: "loading" } | { readonly phase: "error"; readonly words: string } | { readonly phase: "ready" };

export interface OfficeModel {
  readonly busy: boolean;
  readonly nowMs: number;
  /** What the open invoice's extra actions are showing: nothing, its document, a dispute form, or the close form. */
  readonly invoiceMode: InvoiceMode;
  readonly document: DocumentView;
  /** The payment being reversed, waiting on a reason. */
  readonly reversing: string | null;
  readonly accounts: readonly BillingAgingRow[];
  readonly policy: BillingLandingPolicy | null;
  readonly remind: RemindForm;
  readonly history: Listing<BillingReminderSend>;
  readonly historyFor: string;
  readonly paper: Listing<BillingPaperPathEntry>;
  /** The paper path flag being closed and the way it closes, waiting on a note. */
  readonly closing: { readonly flagId: string; readonly status: "MOVED_TO_PAPER" | "DISMISSED" } | null;
  readonly run: RunView;
  readonly settings: SettingsModel;
  /** The holds a returned payment placed, by hold id: the payment each one names. */
  readonly tillHolds: ReadonlyMap<string, string>;
  /** The AP contact read for each account asked about: the read, or "error" when it could not be read. */
  readonly contacts: ReadonlyMap<string, BillingAPContactRead | "error">;
  /** The account whose contact is being removed and waits for the second press. */
  readonly removing: string | null;
}

export class OfficeDesk {
  readonly #api: OfficeApi;
  readonly #host: OfficeHost;
  readonly #who: OfficeWho;
  readonly #keys = new AttemptKeys(newKey);
  readonly #settings: SettingsDesk;
  readonly #tillHolds = new Map<string, string>();

  #busy = false;
  #run: RunView = { phase: "none" };
  #contacts = new Map<string, BillingAPContactRead | "error">();
  #removing: string | null = null;
  #remind: RemindForm = { customerId: "", invoices: [], invoiceId: "", channel: "EMAIL", resend: false, confirm: false };
  #history: Listing<BillingReminderSend> = { phase: "none" };
  #historyFor = "";
  #paper: Listing<BillingPaperPathEntry> = { phase: "none" };
  #closing: { flagId: string; status: "MOVED_TO_PAPER" | "DISMISSED" } | null = null;
  #invoiceMode: InvoiceMode = "none";
  #invoiceId: string | null = null;
  #document: DocumentView = { phase: "none" };
  /** The invoice document's HTML, held until the next paint can mount it. */
  #documentHtml: string | null = null;
  #reversing: string | null = null;
  /** The account whose name field the next paint must fill from its contact. */
  #fillContactFor: string | null = null;

  constructor(api: OfficeApi, host: OfficeHost, who: OfficeWho) {
    this.#api = api;
    this.#host = host;
    this.#who = who;
    this.#settings = new SettingsDesk(api, host, canLiftHolds(who.roleCode));
  }

  get who(): OfficeWho {
    return this.#who;
  }

  model(): OfficeModel {
    return {
      busy: this.#busy,
      nowMs: Date.now(),
      invoiceMode: this.#invoiceMode,
      document: this.#document,
      reversing: this.#reversing,
      accounts: this.#host.accounts(),
      policy: this.#host.policy(),
      remind: this.#remind,
      history: this.#history,
      historyFor: this.#historyFor,
      paper: this.#paper,
      closing: this.#closing,
      run: this.#run,
      settings: this.#settings.model(),
      tillHolds: this.#tillHolds,
      contacts: this.#contacts,
      removing: this.#removing,
    };
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>): void {
    this.#host.error(officeRefusal(action, result.error, result.status));
  }

  // ---- statement runs ----------------------------------------------------------------------------------------

  /** Reads a run back by its id (the server's stored run, not the answer to the press) and shows it. */
  async showRun(id: string): Promise<void> {
    this.#run = { phase: "loading" };
    this.#host.repaint();
    const got = await this.#api.statementRun(id);
    if (!got.ok) {
      this.#run = { phase: "error", words: officeRefusal("statement-run", got.error, got.status) };
      this.#host.repaint();
      return;
    }
    this.#run = { phase: "ready", result: got.value };
    this.#host.repaint();
  }

  // ---- AP contacts -------------------------------------------------------------------------------------------

  /** Reads an account's AP contact once; a later call finds it kept. */
  async ensureContact(customerId: string, force = false): Promise<void> {
    if (customerId === "" || (!force && this.#contacts.has(customerId))) return;
    const got = await this.#api.apContact(customerId);
    this.#contacts.set(customerId, got.ok ? got.value : "error");
    if (got.ok) this.#fillContactFor = customerId;
    this.#host.repaint();
  }

  async #saveContact(customerId: string): Promise<void> {
    if (this.#busy) return;
    const check = validateApContact({
      name: this.#host.field("contact-name"),
      email: this.#host.field("contact-email"),
      phone: this.#host.field("contact-phone"),
    });
    if (!check.ok) {
      this.#host.error(check.words);
      this.#host.reveal("[data-error]");
      return;
    }
    this.#busy = true;
    this.#removing = null;
    this.#host.repaint();
    const result = await this.#api.putApContact(customerId, check.contact);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("contact", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#contacts.set(customerId, result.value);
    this.#host.setField("contact-email", "");
    this.#host.setField("contact-phone", "");
    this.#host.notice(SAVED_NOTICE);
    this.#host.reveal("[data-notice]");
    this.#host.repaint();
  }

  async #removeContact(customerId: string): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.deleteApContact(customerId);
    this.#busy = false;
    this.#removing = null;
    if (!result.ok) {
      this.#fail("contact", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#contacts.set(customerId, result.value);
    this.#host.setField("contact-name", "");
    this.#host.notice("The AP contact is removed. Nothing will be queued for this account until one is tagged.");
    this.#host.reveal("[data-notice]");
    this.#host.repaint();
  }

  // ---- reminders and the paper path --------------------------------------------------------------------------

  /** Opens the Reminders tab: the history and the paper path are read afresh. */
  async openReminders(): Promise<void> {
    await Promise.all([this.#loadHistory(), this.#loadPaper()]);
  }

  async #loadHistory(): Promise<void> {
    this.#history = { phase: "loading" };
    this.#host.repaint();
    const got = await this.#api.reminders(this.#historyFor);
    this.#history = got.ok ? { phase: "ready", items: got.value.items } : { phase: "error", words: officeRefusal("reminders", got.error, got.status) };
    this.#host.repaint();
  }

  async #loadPaper(): Promise<void> {
    this.#paper = { phase: "loading" };
    this.#host.repaint();
    const got = await this.#api.paperPath();
    this.#paper = got.ok ? { phase: "ready", items: got.value.items } : { phase: "error", words: officeRefusal("paper-path", got.error, got.status) };
    this.#host.repaint();
  }

  async #chooseAccount(customerId: string): Promise<void> {
    this.#remind = { ...this.#remind, customerId, invoices: customerId === "" ? [] : "loading", invoiceId: "", confirm: false };
    this.#host.repaint();
    if (customerId === "") return;
    const [invoices] = await Promise.all([this.#api.accountInvoices(customerId), this.ensureContact(customerId)]);
    if (this.#remind.customerId !== customerId) return;
    const open = invoices.ok ? invoices.value.items.filter((r) => r.status !== "PAID" && r.balance_cents > 0) : [];
    this.#remind = { ...this.#remind, invoices: open, invoiceId: open[0]?.id ?? "" };
    if (!invoices.ok) this.#fail("load", invoices);
    this.#host.repaint();
  }

  async #sendReminder(): Promise<void> {
    const form = this.#remind;
    if (this.#busy) return;
    if (form.customerId === "") return this.#stop("Choose the account to remind.");
    if (form.invoices === "loading") return this.#stop("The account's invoices are still loading.");
    const invoice = form.invoices.find((r) => r.id === form.invoiceId);
    if (invoice === undefined) return this.#stop("Choose the invoice the reminder is about.");
    const attempt = `remind:${form.customerId}:${invoice.id}:${form.channel}:${form.resend}:${form.confirm}`;
    const key = this.#keys.keyFor(attempt);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.sendReminder({ customerId: form.customerId, invoiceId: invoice.id, channel: form.channel, resend: form.resend, confirmFallback: form.confirm, key });
    this.#busy = false;
    if (!result.ok) {
      // A lost answer keeps the key, so the same press sent again cannot send a second reminder; any answer ends it.
      if (!outcomeUnknown(result.status)) this.#keys.settle(attempt);
      this.#fail("reminder", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#keys.settle(attempt);
    this.#host.notice(reminderResultWords(result.value, invoice.invoice_no));
    this.#host.reveal("[data-notice]");
    await this.#loadHistory();
  }

  async #sweepPaper(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.sweepPaperPath();
    this.#busy = false;
    if (!result.ok) {
      this.#fail("paper-path", result);
      this.#host.repaint();
      return;
    }
    const { evaluated, raised } = result.value;
    this.#host.notice(`The sweep looked at ${evaluated} ${evaluated === 1 ? "account" : "accounts"} and raised ${raised} new ${raised === 1 ? "flag" : "flags"}.`);
    await Promise.all([this.#loadPaper(), this.#host.reloadBook()]);
  }

  async #resolvePaper(flagId: string): Promise<void> {
    const closing = this.#closing;
    if (closing === null || closing.flagId !== flagId || this.#busy) return;
    const check = paperResolveCheck(closing.status, this.#host.field("paper-note", flagId));
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.resolvePaperPath(flagId, check.status, check.note);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("paper-path", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#closing = null;
    this.#host.notice(check.status === "MOVED_TO_PAPER" ? "The account is marked as moved to paper, with your note on the record." : "The flag is dismissed, with your note on the record.");
    await Promise.all([this.#loadPaper(), this.#host.reloadBook()]);
  }

  #stop(words: string): void {
    this.#host.error(words);
    this.#host.reveal("[data-error]");
  }

  /** A field the screen carries was changed (a select, a checkbox); only the choices that move the form matter. */
  change(control: Element): boolean {
    const name = control.getAttribute("data-field");
    const value = (control as HTMLSelectElement).value;
    if (name === "remind-account") {
      void this.#chooseAccount(value);
      return true;
    }
    if (name === "remind-invoice") {
      this.#remind = { ...this.#remind, invoiceId: (control as HTMLSelectElement).selectedOptions[0]?.getAttribute("data-id") ?? "" };
      this.#host.repaint();
      return true;
    }
    if (name === "history-account") {
      this.#historyFor = (control as HTMLSelectElement).selectedOptions[0]?.getAttribute("data-id") ?? "";
      void this.#loadHistory();
      return true;
    }
    return false;
  }

  // ---- an invoice: send, document, dispute; a payment reversal ----------------------------------------------

  /** The open invoice changed (or closed): its extra panels start over. */
  invoiceChanged(invoiceId: string | null): void {
    if (invoiceId === this.#invoiceId) return;
    this.#invoiceId = invoiceId;
    this.#invoiceMode = "none";
    this.#document = { phase: "none" };
    this.#documentHtml = null;
    this.#reversing = null;
  }

  async #sendInvoice(invoiceId: string): Promise<void> {
    if (this.#busy) return;
    const attempt = `send-invoice:${invoiceId}`;
    const key = this.#keys.keyFor(attempt);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.sendInvoice(invoiceId, key);
    this.#busy = false;
    if (!result.ok) {
      if (!outcomeUnknown(result.status)) this.#keys.settle(attempt);
      this.#fail("send-invoice", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#keys.settle(attempt);
    this.#host.notice(sendInvoiceWords(result.value));
    this.#host.reveal("[data-notice]");
    await this.#host.reloadAccount();
    this.#host.repaint();
  }

  async #showDocument(invoiceId: string): Promise<void> {
    this.#invoiceMode = "document";
    this.#document = { phase: "loading" };
    this.#host.repaint();
    const got = await this.#api.invoiceDocument(invoiceId);
    if (this.#invoiceId !== invoiceId || this.#invoiceMode !== "document") return;
    if (!got.ok) {
      this.#document = { phase: "error", words: officeRefusal("document", got.error, got.status) };
      this.#host.repaint();
      return;
    }
    this.#document = { phase: "ready" };
    this.#documentHtml = got.value;
    this.#host.repaint();
  }

  async #openDispute(invoiceId: string, balanceCents: number): Promise<void> {
    if (this.#busy) return;
    const check = disputeCheck({
      reason: this.#host.field("dispute-reason"),
      amountText: this.#host.field("dispute-amount"),
      note: this.#host.field("dispute-note"),
      balanceCents,
    });
    if (!check.ok) return this.#stop(check.words);
    const attempt = `dispute:${invoiceId}:${check.reason}:${check.amountCents}:${check.note}`;
    const key = this.#keys.keyFor(attempt);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.openDispute(invoiceId, { reason: check.reason, note: check.note, amountCents: check.amountCents, key });
    this.#busy = false;
    if (!result.ok) {
      if (!outcomeUnknown(result.status)) this.#keys.settle(attempt);
      this.#fail("dispute", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#keys.settle(attempt);
    this.#invoiceMode = "none";
    this.#host.notice("The dispute is open. The invoice shows as disputed until it is closed.");
    this.#host.reveal("[data-notice]");
    await Promise.all([this.#host.reloadAccount(), this.#host.reloadBook()]);
    this.#host.repaint();
  }

  async #resolveDispute(invoiceId: string): Promise<void> {
    if (this.#busy) return;
    const outcome = this.#host.field("dispute-outcome");
    if (!DISPUTE_OUTCOMES.includes(outcome)) return this.#stop("Choose how the dispute ended.");
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.resolveDispute(invoiceId, outcome);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("dispute", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#invoiceMode = "none";
    this.#host.notice(result.value.resolved ? "The dispute is closed and the way it ended is on the record." : "There was no open dispute on this invoice, so nothing was closed.");
    this.#host.reveal("[data-notice]");
    await Promise.all([this.#host.reloadAccount(), this.#host.reloadBook()]);
    this.#host.repaint();
  }

  async #reversePayment(paymentId: string, paymentNo: string): Promise<void> {
    if (this.#busy) return;
    const check = reversalCheck(this.#host.field("reverse-reason"));
    if (!check.ok) return this.#stop(check.words);
    this.#busy = true;
    this.#host.repaint();
    const result = await this.#api.reversePayment(paymentId, check.reason);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("reverse", result);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    this.#reversing = null;
    this.#host.notice(reversalWords(result.value, paymentNo));
    this.#host.reveal("[data-notice]");
    await Promise.all([this.#host.reloadAccount(), this.#host.reloadBook()]);
    this.#host.repaint();
  }

  // ---- settings, holds a returned payment placed, the dispatch gate -----------------------------------------

  openSettings(): Promise<void> {
    return this.#settings.open();
  }

  /** Reads the holds a returned payment placed once: each names the payment that raised it. */
  async openHolds(): Promise<void> {
    const got = await this.#api.tillHolds();
    if (!got.ok) return;
    this.#tillHolds.clear();
    for (const h of got.value.items) if (h.source_payment_id !== undefined) this.#tillHolds.set(h.id, h.source_payment_id);
    this.#host.repaint();
  }

  /** Lifts a hold a returned payment placed, through the till's path: the billing release refuses it. */
  releaseNsfHold(holdId: string, reason: string): Promise<Result<{ status: string }>> {
    return this.#api.releaseTillHold(holdId, reason);
  }

  async #checkDispatch(orderId: string, orderNo: string): Promise<void> {
    if (orderId === "" || this.#busy) return;
    this.#busy = true;
    this.#host.repaint();
    const got = await this.#api.dispatchGate(orderId);
    this.#busy = false;
    if (!got.ok) {
      this.#fail("dispatch", got);
      this.#host.reveal("[data-error]");
      this.#host.repaint();
      return;
    }
    const words = dispatchWords(got.value, orderNo === "" ? "for this invoice" : orderNo);
    if (got.value.allowed) this.#host.notice(words);
    else this.#host.error(words);
    this.#host.reveal(got.value.allowed ? "[data-notice]" : "[data-error]");
  }

  // ---- the press ---------------------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "contact-save":
        void this.#saveContact(attr("data-customer-id"));
        return true;
      case "contact-remove":
        this.#removing = attr("data-customer-id");
        this.#host.repaint();
        return true;
      case "contact-remove-cancel":
        this.#removing = null;
        this.#host.repaint();
        return true;
      case "contact-remove-confirm":
        void this.#removeContact(attr("data-customer-id"));
        return true;
      case "remind-channel":
        this.#remind = { ...this.#remind, channel: attr("data-choice") === "SMS" ? "SMS" : "EMAIL", confirm: false };
        this.#host.repaint();
        return true;
      case "remind-toggle":
        this.#remind = attr("data-choice") === "resend" ? { ...this.#remind, resend: !this.#remind.resend } : { ...this.#remind, confirm: !this.#remind.confirm };
        this.#host.repaint();
        return true;
      case "remind-send":
        void this.#sendReminder();
        return true;
      case "invoice-dispatch":
        void this.#checkDispatch(attr("data-order-id"), attr("data-order-no"));
        return true;
      case "invoice-send":
        void this.#sendInvoice(attr("data-invoice-id"));
        return true;
      case "invoice-document":
        void this.#showDocument(attr("data-invoice-id"));
        return true;
      case "invoice-mode":
        this.#invoiceMode = attr("data-choice") === "dispute" ? "dispute" : attr("data-choice") === "resolve" ? "resolve" : "none";
        this.#document = { phase: "none" };
        this.#documentHtml = null;
        this.#host.repaint();
        return true;
      case "dispute-open":
        void this.#openDispute(attr("data-invoice-id"), Number(attr("data-balance-cents")));
        return true;
      case "dispute-close":
        void this.#resolveDispute(attr("data-invoice-id"));
        return true;
      case "reverse-start":
        this.#reversing = attr("data-payment-id");
        this.#host.repaint();
        return true;
      case "reverse-cancel":
        this.#reversing = null;
        this.#host.repaint();
        return true;
      case "reverse-confirm":
        void this.#reversePayment(attr("data-payment-id"), attr("data-payment-no"));
        return true;
      case "paper-sweep":
        void this.#sweepPaper();
        return true;
      case "paper-close":
        this.#closing = { flagId: attr("data-flag-id"), status: attr("data-choice") === "DISMISSED" ? "DISMISSED" : "MOVED_TO_PAPER" };
        this.#host.repaint();
        return true;
      case "paper-close-cancel":
        this.#closing = null;
        this.#host.repaint();
        return true;
      case "paper-close-confirm":
        void this.#resolvePaper(attr("data-flag-id"));
        return true;
      default:
        return this.#settings.press(control);
    }
  }

  /** After each paint: a contact's name fills its field once the contact has been read. */
  afterRender(): void {
    this.#settings.afterRender();
    if (this.#documentHtml !== null) {
      const text = this.#documentHtml;
      this.#documentHtml = null;
      this.#host.mountDocument("[data-doc-slot]", text);
    }
    const id = this.#fillContactFor;
    if (id === null) return;
    const read = this.#contacts.get(id);
    this.#fillContactFor = null;
    if (read !== undefined && read !== "error" && read.tagged && read.contact !== undefined) this.#host.setField("contact-name", read.contact.contact_name);
  }

  /** Forgets what was read about accounts: a sign out and a sign in of another user starts clean. */
  clear(): void {
    this.#contacts.clear();
    this.#run = { phase: "none" };
    this.#removing = null;
    this.#history = { phase: "none" };
    this.#paper = { phase: "none" };
    this.#closing = null;
    this.#settings.clear();
    this.#tillHolds.clear();
  }
}
