// The receivables desk's controller: the state the screens draw from, the
// loads, and what each press does. It is not an element; the root element owns
// it, hands it a way to repaint and a way to read the form fields, and asks it
// for the template. The Money Desk's other slices sit beside it the same way.
//
// Every write is held to the server: a payment is applied, a promise recorded,
// a payment marked bounced only when the core answers, and the screen then
// shows what the core now says (the balance it answered, the queue it lists),
// never what the app expected. A refusal reads in words (ar-logic.ts), a walled
// move in words naming who may do it. Nothing is stored: the idempotency keys,
// like the rest, rest in memory for the life of the page.
import type { TemplateResult } from "lit";
import { html } from "lit";
import type { Result } from "@hardscapehouse/api-client";
import { ArApi, type PaymentMethod } from "./ar-api.js";
import { refusalWords, validatePayment, validatePromise } from "./ar-logic.js";
import { formatMoney } from "./ar-money.js";
import { renderAccount, renderLanding, renderLoadFailed, renderLoading, type AccountModel, type BouncedWork, type DeskModel, type Tab } from "./ar-views.js";

export interface DeskHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string, holdId?: string) => string;
}

function newKey(): string {
  const c = globalThis.crypto;
  if (c !== undefined && typeof c.randomUUID === "function") return c.randomUUID();
  return `k-${Date.now()}-${Math.floor(Math.random() * 1e9)}`;
}

const METHODS: readonly PaymentMethod[] = ["CASH", "CARD", "CHEQUE", "EFT"];

export class ArDesk {
  readonly #api: ArApi;
  readonly #host: DeskHost;

  #phase: DeskModel["phase"] = "loading";
  #loadError = "";
  #tab: Tab = "aging";
  #landing: DeskModel["landing"] = null;
  #aging: DeskModel["aging"] = [];
  #bounced: DeskModel["bounced"] = [];
  #holds: DeskModel["holds"] = [];
  #cardFilter: string | null = null;
  #account: AccountModel | null = null;
  #work: BouncedWork | null = null;
  #busy = false;
  #error = "";
  #notice = "";

  // One key per attempt: a double press replays the first rather than writing twice.
  #payKey = newKey();
  #promiseKey = newKey();
  #statementKey = newKey();

  constructor(api: ArApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
  }

  // ---- loads ----------------------------------------------------------------------------------

  /** The first read of the desk: the cards, the aging, the bounced queue and the holds. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [landing, aging, bounced, holds] = await Promise.all([this.#api.landing(), this.#api.aging(), this.#api.bounced(), this.#api.holds("ACTIVE")]);
    for (const read of [landing, aging, bounced, holds]) {
      if (!read.ok) {
        this.#phase = "error";
        this.#loadError = refusalWords("load", read.error, read.status);
        this.#host.repaint();
        return;
      }
    }
    if (landing.ok) this.#landing = landing.value;
    if (aging.ok) this.#aging = aging.value.items;
    if (bounced.ok) this.#bounced = bounced.value.items;
    if (holds.ok) this.#holds = holds.value.items;
    this.#phase = "ready";
    this.#host.repaint();
  }

  /** The book after a write: the cards, the aging, the queue and the holds, quietly. */
  async #reloadBook(): Promise<void> {
    const [landing, aging, bounced, holds] = await Promise.all([this.#api.landing(), this.#api.aging(), this.#api.bounced(), this.#api.holds("ACTIVE")]);
    if (landing.ok) this.#landing = landing.value;
    if (aging.ok) this.#aging = aging.value.items;
    if (bounced.ok) this.#bounced = bounced.value.items;
    if (holds.ok) this.#holds = holds.value.items;
  }

  async #reloadAccount(): Promise<void> {
    const account = this.#account;
    if (account === null) return;
    const register = await this.#api.accountInvoices(account.customerId);
    if (!register.ok || this.#account === null) return;
    this.#account = { ...this.#account, invoices: register.value.items.filter((r) => r.balance_cents > 0) };
    if (this.#account.openInvoiceId !== null) await this.#reloadInvoice();
  }

  async #reloadInvoice(): Promise<void> {
    const id = this.#account?.openInvoiceId;
    if (this.#account === null || id === null || id === undefined) return;
    const [detail, payments] = await Promise.all([this.#api.invoice(id), this.#api.invoicePayments(id)]);
    if (this.#account === null || this.#account.openInvoiceId !== id) return;
    this.#account = {
      ...this.#account,
      detail: detail.ok ? detail.value : this.#account.detail,
      payments: payments.ok ? payments.value : this.#account.payments,
    };
  }

  async #loadStatement(customerId: string): Promise<void> {
    const got = await this.#api.statement(customerId);
    if (this.#account === null || this.#account.customerId !== customerId) return;
    if (got.ok) this.#account = { ...this.#account, statement: got.value };
    else if (got.status === 404) this.#account = { ...this.#account, statement: "none" };
    else this.#account = { ...this.#account, statement: "none" };
    this.#host.repaint();
  }

  // ---- the press ------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "refresh":
        void this.#reloadBook().then(() => this.#host.repaint());
        return true;
      case "tab":
        this.#tab = attr("data-tab-id") === "bounced" ? "bounced" : attr("data-tab-id") === "holds" ? "holds" : "aging";
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "card":
        this.#cardFilter = this.#cardFilter === attr("data-card-id") ? null : attr("data-card-id");
        this.#tab = "aging";
        this.#host.repaint();
        return true;
      case "clear-card":
        this.#cardFilter = null;
        this.#host.repaint();
        return true;
      case "open-account":
        void this.#openAccount(attr("data-customer-id"));
        return true;
      case "back":
        this.#account = null;
        this.#clearMessages();
        void this.#reloadBook().then(() => this.#host.repaint());
        this.#host.repaint();
        return true;
      case "open-invoice":
        void this.#openInvoice(attr("data-invoice-id"));
        return true;
      case "close-invoice":
        if (this.#account !== null) this.#account = { ...this.#account, openInvoiceId: null, detail: null, payments: null, bounceFor: null };
        this.#clearMessages();
        this.#host.repaint();
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
      case "close-work":
        this.#work = null;
        this.#host.repaint();
        return true;
      case "run-statement":
        void this.#runStatement();
        return true;
      case "release-hold":
        void this.#releaseHold(attr("data-hold-id"));
        return true;
      default:
        return false;
    }
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>): void {
    this.#notice = "";
    this.#error = refusalWords(action, result.error, result.status);
  }

  async #openAccount(customerId: string): Promise<void> {
    if (customerId === "") return;
    const row = this.#aging.find((a) => a.customer_id === customerId);
    this.#clearMessages();
    this.#work = null;
    this.#account = {
      customerId,
      name: row?.customer_name ?? "Account",
      accountNo: row?.account_no ?? "",
      invoices: [],
      openInvoiceId: null,
      detail: null,
      payments: null,
      bounceFor: null,
      statement: null,
    };
    this.#host.repaint();
    const register = await this.#api.accountInvoices(customerId);
    if (this.#account === null || this.#account.customerId !== customerId) return;
    if (!register.ok) {
      this.#fail("load", register);
    } else {
      const first = register.value.items[0];
      this.#account = {
        ...this.#account,
        name: first?.customer_name ?? this.#account.name,
        accountNo: first?.account_no ?? this.#account.accountNo,
        invoices: register.value.items.filter((r) => r.balance_cents > 0),
      };
    }
    this.#host.repaint();
    await this.#loadStatement(customerId);
  }

  async #openInvoice(invoiceId: string): Promise<void> {
    if (this.#account === null || invoiceId === "") return;
    this.#clearMessages();
    this.#payKey = newKey();
    this.#promiseKey = newKey();
    this.#account = { ...this.#account, openInvoiceId: invoiceId, detail: null, payments: null, bounceFor: null };
    this.#host.repaint();
    await this.#reloadInvoice();
    this.#host.repaint();
  }

  async #applyPayment(): Promise<void> {
    const account = this.#account;
    const detail = account?.detail;
    if (account === null || detail === null || detail === undefined || this.#busy) return;
    const check = validatePayment({
      amountText: this.#host.field("pay-amount"),
      reference: this.#host.field("pay-reference"),
      balanceCents: detail.totals.balance_cents,
    });
    if (!check.ok) {
      this.#notice = "";
      this.#error = check.words;
      this.#host.repaint();
      return;
    }
    const picked = this.#host.field("pay-method");
    const method = METHODS.find((m) => m === picked) ?? "CHEQUE";
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.recordPayment(detail.id, {
      method,
      amountCents: check.amountCents,
      reference: this.#host.field("pay-reference").trim(),
      key: this.#payKey,
    });
    this.#busy = false;
    if (!result.ok) {
      this.#fail("payment", result);
      this.#host.repaint();
      return;
    }
    this.#payKey = newKey();
    const r = result.value;
    this.#notice = r.replayed
      ? `Payment ${r.payment_no} was already applied. The invoice balance is ${formatMoney(r.balance_cents)}.`
      : `Payment ${r.payment_no} applied: ${formatMoney(r.amount_cents)}. The invoice balance is now ${formatMoney(r.balance_cents)}.`;
    await Promise.all([this.#reloadAccount(), this.#reloadBook()]);
    this.#host.repaint();
  }

  async #recordPromise(): Promise<void> {
    const account = this.#account;
    const detail = account?.detail;
    if (account === null || detail === null || detail === undefined || this.#busy) return;
    const input = {
      amountText: this.#host.field("promise-amount"),
      date: this.#host.field("promise-date"),
      by: this.#host.field("promise-by"),
      note: this.#host.field("promise-note"),
      balanceCents: detail.totals.balance_cents,
    };
    const check = validatePromise(input);
    if (!check.ok) {
      this.#notice = "";
      this.#error = check.words;
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.recordPromise(detail.id, {
      promisedCents: check.promisedCents,
      date: input.date,
      by: input.by.trim(),
      note: input.note.trim(),
      key: this.#promiseKey,
    });
    this.#busy = false;
    if (!result.ok) {
      this.#fail("promise", result);
      this.#promiseKey = newKey();
      this.#host.repaint();
      return;
    }
    this.#promiseKey = newKey();
    this.#notice = `Promise recorded: ${formatMoney(result.value.promised_cents)} by ${result.value.promised_date}.`;
    await this.#reloadInvoice();
    this.#host.repaint();
  }

  async #markBounced(paymentId: string): Promise<void> {
    if (this.#account === null || paymentId === "" || this.#busy) return;
    const reason = this.#host.field("bounce-reason").trim();
    if (reason === "") {
      this.#notice = "";
      this.#error = "Enter why it came back, such as cheque returned NSF.";
      this.#host.repaint();
      return;
    }
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
    this.#work = { history: history.value, accountName: name };
    this.#host.repaint();
  }

  async #runStatement(): Promise<void> {
    const account = this.#account;
    if (account === null || this.#busy) return;
    if (account.invoices.length === 0) {
      this.#notice = "";
      this.#error = "There is nothing owing on this account to put on a statement.";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const run = await this.#api.runStatement(account.invoices.map((r) => r.id), this.#statementKey);
    this.#busy = false;
    if (!run.ok) {
      this.#fail("statement", run);
      this.#host.repaint();
      return;
    }
    this.#statementKey = newKey();
    this.#notice = "Statement run for this account.";
    await this.#loadStatement(account.customerId);
  }

  async #releaseHold(holdId: string): Promise<void> {
    if (holdId === "" || this.#busy) return;
    const reason = this.#host.field("release-reason", holdId).trim();
    if (reason === "") {
      this.#notice = "";
      this.#error = "Enter why the hold can be released.";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const result = await this.#api.releaseHold(holdId, reason);
    this.#busy = false;
    if (!result.ok) {
      this.#fail("release-hold", result);
      this.#host.repaint();
      return;
    }
    this.#notice = "The credit hold is released.";
    await this.#reloadBook();
    this.#host.repaint();
  }

  // ---- the screen -----------------------------------------------------------------------------

  render(): TemplateResult {
    if (this.#phase === "loading") return renderLoading();
    if (this.#phase === "error") return renderLoadFailed(this.#loadError);
    const model: DeskModel = {
      phase: this.#phase,
      tab: this.#tab,
      landing: this.#landing,
      aging: this.#aging,
      bounced: this.#bounced,
      holds: this.#holds,
      cardFilter: this.#cardFilter,
      account: this.#account,
      work: this.#work,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
      asOf: this.#landing?.as_of ?? "",
    };
    return html`${model.account === null ? renderLanding(model) : renderAccount(model, model.account)}`;
  }
}
