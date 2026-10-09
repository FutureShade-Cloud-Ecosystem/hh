// The receivables slice's one door to the core: the reads and writes the
// Money Desk makes for accounts receivable, over the generated client,
// answering a Result and never throwing. Every route here is the core's
// existing /api/v1/ar surface; nothing new was asked of the core.
//
// Reads: the landing (the aging cards), the aging by account, the register
// (an account's open invoices), one invoice, an invoice's payments, a payment's
// history, the bounced queue, the credit holds, an account's latest statement.
// Writes: record a payment, record a promise to pay, mark a payment bounced,
// run a statement, and the credit hold release (which the role gate refuses
// this role; it is wired so the refusal is the server's, not the app's).
import {
  staffBilling,
  staffBranches,
  staffTill,
  type ApiClient,
  type BillingAgingPage,
  type BillingARLanding,
  type BillingCreditHoldPage,
  type BillingInvoiceDetail,
  type BillingPromiseView,
  type BillingReceiptResult,
  type BillingRegisterPage,
  type BillingStatement,
  type Branch,
  type BillingEvaluationResult,
  type BillingHoldOverrideResult,
  type BillingReminderResult,
  type BillingRegisterSelectionResult,
  type BillingStatementBatch,
  type BillingStatementRunResult,
  type Result,
  type TillBouncedPaymentPage,
  type TillInvoicePayments,
  type TillNSFResult,
  type TillPaymentHistory,
} from "@hardscapehouse/api-client";

export type PaymentMethod = "CASH" | "CARD" | "CHEQUE" | "EFT";

export class ArApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  landing(): Promise<Result<BillingARLanding>> {
    return staffBilling.billingGetARLanding(this.#client);
  }

  aging(): Promise<Result<BillingAgingPage>> {
    return staffBilling.billingGetAging(this.#client);
  }

  /** An account's invoices that still carry a balance. */
  accountInvoices(customerId: string): Promise<Result<BillingRegisterPage>> {
    return staffBilling.billingListRegister(this.#client, { customer_id: customerId, limit: 100 });
  }

  invoice(id: string): Promise<Result<BillingInvoiceDetail>> {
    return staffBilling.billingGetRegisterInvoice(this.#client, id);
  }

  invoicePayments(id: string): Promise<Result<TillInvoicePayments>> {
    return staffTill.tillInvoicePayments(this.#client, id);
  }

  paymentHistory(id: string): Promise<Result<TillPaymentHistory>> {
    return staffTill.tillPaymentHistory(this.#client, id);
  }

  bounced(): Promise<Result<TillBouncedPaymentPage>> {
    return staffTill.tillListBouncedPayments(this.#client);
  }

  /** Active holds, or every hold when no status is named. */
  holds(status?: "ACTIVE" | "RELEASED"): Promise<Result<BillingCreditHoldPage>> {
    return staffBilling.billingListHolds(this.#client, status === undefined ? {} : { status });
  }

  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  /** The register in one view; chip PAID with no filter reads every invoice, paid ones too. */
  register(opts: { chip?: "OVERDUE" | "DISPUTED" | "PAID"; customerId?: string } = {}): Promise<Result<BillingRegisterPage>> {
    return staffBilling.billingListRegister(this.#client, {
      limit: 200,
      ...(opts.chip === undefined ? {} : { chip: opts.chip }),
      ...(opts.customerId === undefined ? {} : { customer_id: opts.customerId }),
    });
  }

  /** The server's count and balance for a set of invoices: the app adds no money. */
  selection(invoiceIds: readonly string[]): Promise<Result<BillingRegisterSelectionResult>> {
    return staffBilling.billingRegisterSelection(this.#client, { invoice_ids: [...invoiceIds] });
  }

  remind(invoiceIds: readonly string[], key: string): Promise<Result<BillingReminderResult>> {
    return staffBilling.billingSendRegisterReminders(this.#client, { invoice_ids: [...invoiceIds], tone: "FRIENDLY", idempotency_key: key });
  }

  /** A statement run: one account (interim) or every account (month end). */
  statementRun(customerId: string | null): Promise<Result<BillingStatementRunResult>> {
    return staffBilling.billingCreateStatementRun(this.#client, customerId === null ? { mode: "MASS" } : { mode: "INTERIM", customer_id: customerId });
  }

  exportRegister(): Promise<Result<string>> {
    return staffBilling.billingExportRegister(this.#client, {});
  }

  overrideHold(holdId: string, reason: string): Promise<Result<BillingHoldOverrideResult>> {
    return staffBilling.billingOverrideHold(this.#client, holdId, { reason });
  }

  sweep(): Promise<Result<BillingEvaluationResult>> {
    return staffBilling.billingRunCreditSweep(this.#client);
  }

  statement(customerId: string): Promise<Result<BillingStatement>> {
    return staffBilling.billingGetCustomerStatement(this.#client, customerId);
  }

  recordPayment(
    invoiceId: string,
    input: { method: PaymentMethod; amountCents: number; reference: string; key: string },
  ): Promise<Result<BillingReceiptResult>> {
    return staffBilling.billingRecordInvoicePayment(this.#client, invoiceId, {
      method: input.method,
      amount_cents: input.amountCents,
      reference: input.reference,
      idempotency_key: input.key,
    });
  }

  recordPromise(
    invoiceId: string,
    input: { promisedCents: number; date: string; by: string; note: string; key: string },
  ): Promise<Result<BillingPromiseView>> {
    return staffBilling.billingAddPromise(this.#client, invoiceId, {
      promised_cents: input.promisedCents,
      promised_date: input.date,
      promised_by: input.by,
      note: input.note,
      idempotency_key: input.key,
    });
  }

  markBounced(paymentId: string, reason: string): Promise<Result<TillNSFResult>> {
    return staffTill.tillMarkPaymentNSF(this.#client, paymentId, { reason });
  }

  runStatement(invoiceIds: readonly string[], key: string): Promise<Result<BillingStatementBatch>> {
    return staffBilling.billingCreateRegisterStatements(this.#client, { invoice_ids: [...invoiceIds], idempotency_key: key });
  }

  releaseHold(holdId: string, reason: string): Promise<Result<{ ok: boolean }>> {
    return staffBilling.billingReleaseHold(this.#client, holdId, { reason });
  }
}
