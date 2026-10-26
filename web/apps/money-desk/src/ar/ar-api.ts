// The receivables slice's one door to the core: the reads and writes the
// Money Desk makes for accounts receivable, over the generated client,
// answering a Result and never throwing. Every route here is the core's
// existing /api/v1/ar surface; nothing new was asked of the core.
//
// Reads: the landing (the aging cards), the aging by account, the register
// (an account's open invoices), one invoice, an invoice's payments, a payment's
// history, the bounced queue, the credit holds, an account's latest statement.
// Writes: record one received payment across invoices, record a promise to pay, mark a payment bounced,
// run a statement, and the credit hold release (which the role gate refuses
// this role; it is wired so the refusal is the server's, not the app's).
import {
  staffBilling,
  staffBranches,
  staffTill,
  type ApiClient,
  type BillingAddCustomerNoteInput,
  type BillingAddCustomerNoteResult,
  type BillingAgingPage,
  type BillingApplyPaymentAnswer,
  type BillingApplyPaymentLine,
  type BillingARLanding,
  type BillingARWorklistResponse,
  type BillingCreateHoldRequest,
  type BillingCreditHold,
  type BillingCreditHoldPage,
  type BillingCustomerWorkspace,
  type BillingInvoiceDetail,
  type BillingPromiseView,
  type BillingRegisterPage,
  type BillingSendPaylinkRequest,
  type BillingSendPaylinkResult,
  type BillingStatement,
  type BillingStatementPreview,
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

import { OfficeApi } from "./office-api.js";

export type PaymentMethod = "CASH" | "CARD" | "CHEQUE" | "EFT";

export type ApplyAnswer = BillingApplyPaymentAnswer;
export type ApplyLine = BillingApplyPaymentLine;

export class ArApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The receivables office's door, over the same client. */
  office(): OfficeApi {
    return new OfficeApi(this.#client);
  }

  landing(): Promise<Result<BillingARLanding>> {
    return staffBilling.billingGetARLanding(this.#client);
  }

  worklist(opts: { chip?: "all" | "action_needed" | "over_60" | "credit_hold"; q?: string } = {}): Promise<Result<BillingARWorklistResponse>> {
    return staffBilling.billingGetARWorklist(this.#client, opts);
  }

  /**
   * The aging, today's books when no day is named. A named day reads the books AS OF that day
   * (FW1-06b): what each account owed at it, from invoices issued by it less what had come in by
   * it, not today's balances back dated.
   */
  aging(opts: { as_of?: string } = {}): Promise<Result<BillingAgingPage>> {
    return staffBilling.billingGetAging(this.#client, opts);
  }

  /** Every invoice of an account, the unpaid ones first and the paid ones after: the core's read with no chip. */
  accountInvoices(customerId: string): Promise<Result<BillingRegisterPage>> {
    return staffBilling.billingListRegister(this.#client, { customer_id: customerId, limit: 200 });
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

  /** The register in one view. With no chip it reads every invoice, paid ones too (unpaid first); a chip narrows it,
   *  and chip PAID is the paid invoices only, as the core's chipPredicates read it.
   *  The C8 chip set (all / past_due / current / on_hold) sits alongside the legacy StatusX
   *  names; an old URL using OVERDUE or DISPUTED still resolves the same way it always did.
   */
  register(opts: { chip?: "OVERDUE" | "DISPUTED" | "PAID" | "all" | "past_due" | "current" | "on_hold"; customerId?: string } = {}): Promise<Result<BillingRegisterPage>> {
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

  /**
   * One received payment against the picked invoices; the server splits it, oldest first, whatever order the list
   * is in. The route requires a key on a preview too (it never reads it), so a preview rides a throwaway key.
   */
  applyPayment(input: {
    customerId: string;
    method: PaymentMethod;
    amountCents: number;
    reference: string;
    invoiceIds: readonly string[];
    key: string;
    preview: boolean;
  }): Promise<Result<ApplyAnswer>> {
    return staffBilling.paymentsApplyReceived(this.#client, {
      customer_id: input.customerId,
      method: input.method,
      amount_cents: input.amountCents,
      reference: input.reference,
      invoice_ids: [...input.invoiceIds],
      idempotency_key: input.key,
      preview: input.preview,
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

  // The customer account workspace (Change 8 S2 + S2M): one round trip the panel
  // renders in one paint. The 5-bucket aging the workspace opens with is the AR
  // console's own read; the workspace handler does not re-bucket.
  workspace(customerId: string): Promise<Result<BillingCustomerWorkspace>> {
    return staffBilling.billingGetCustomerWorkspace(this.#client, customerId);
  }

  // Add a free-text collection note on customer_ar_notes (0405). Outbound copy
  // never lives here; this is a fact the next collections person picks up.
  addNote(customerId: string, body: BillingAddCustomerNoteInput): Promise<Result<BillingAddCustomerNoteResult>> {
    return staffBilling.billingAddCustomerNote(this.#client, customerId, body);
  }

  // Place a manual credit hold on an account by hand (Change 8 S4). Manager-gated:
  // the gate refuses every non-manager role with the server's own 403, the refusal
  // rides through the same Result channel every other call rides through.
  createHold(input: BillingCreateHoldRequest): Promise<Result<BillingCreditHold>> {
    return staffBilling.billingCreateHold(this.#client, input);
  }

  // Generate a deterministic dealer paylink (Change 8 S3). Staff-gated: the row
  // lands on ar_invoice_deliveries (kind STATEMENT, tone PAYLINK_NOTICE) under a
  // deterministic idempotency_key derived from (customer, invoice, amount, channel),
  // so a repeated call lands on the same delivery row and the URL is the same URL.
  sendPaylink(input: BillingSendPaylinkRequest): Promise<Result<BillingSendPaylinkResult>> {
    return staffBilling.billingSendPaylink(this.#client, input);
  }

  // Read the C8 statement preview: the ar_statements artefact, the customer's
  // open lines, and the 5-bucket aging. Reconciled is the server's assertion that
  // the buckets add up to openCents and the late buckets add up to overdueCents;
  // a drift makes the panel refuse to render.
  statementPreview(customerId: string, statementId: string): Promise<Result<BillingStatementPreview>> {
    return staffBilling.billingGetStatementPreview(this.#client, customerId, statementId);
  }
}
