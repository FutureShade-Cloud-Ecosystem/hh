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
  staffGl,
  staffPurchasing,
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
  type BillingCreditMemoInput,
  type BillingCreditMemoQuote,
  type BillingCreditMemoResult,
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
import type { PurchasingAPAgingPage } from "./ap-logic.js";
import type { PurchasingT5018Report } from "./t5018-logic.js";

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

  /**
   * The open AP the Payables board reads: the core's GET /api/v1/purchasing/ap-aging, as of an
   * instant in the dealer's house zone.
   *
   * Until W2-9a's contract is regenerated on main, the call goes through the client's own
   * request seam over the same path and the same shape, the way the statements slice reads its
   * export, so the generated tree stays untouched. When the contract lands, this one method
   * becomes the generated operation and the shape below is replaced by the generated type
   * (src/ar/ap-logic.ts holds the shape and the bands).
   */
  apAging(opts: { as_of?: string } = {}): Promise<Result<PurchasingAPAgingPage>> {
    return this.#client.request<PurchasingAPAgingPage>({
      method: "GET",
      path: "/api/v1/purchasing/ap-aging",
      query: opts,
      operationId: "purchasingGetAPAging",
    });
  }

  /**
   * The year's T5018 report: the core's GET /api/v1/purchasing/t5018. The year is the dealer's
   * own, so the view asks for one and the route answers with the year it read.
   */
  t5018(opts: { year?: number; branch_id?: string } = {}): Promise<Result<PurchasingT5018Report>> {
    return staffPurchasing.purchasingT5018Report(this.#client, opts);
  }

  /**
   * The same report as the file a bookkeeper hands the contractor's book: the core's own CSV
   * answer, fetched as text, so the file says what the screen says.
   */
  t5018Csv(opts: { year?: number; branch_id?: string } = {}): Promise<Result<string>> {
    return staffPurchasing.purchasingT5018ReportCSV(this.#client, opts);
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

  // Issue a credit memo (FW1-12): the money the dealer gives back, against the
  // account or against one invoice. Manager-gated on the server; the key makes a
  // double press one credit and not two. The server decides the tax, the cap and
  // whether the credit parks for a second pair of eyes.
  issueCreditMemo(input: BillingCreditMemoInput): Promise<Result<BillingCreditMemoResult>> {
    return staffBilling.billingIssueCreditMemo(this.#client, input);
  }

  /**
   * What a credit against one invoice would come to, and how much room that invoice has left
   * (FW1-12 r3): the room before tax and with it, and the credit's own subtotal, tax and total.
   *
   * The figures are the SERVER's. The desk never works out a tax and never guesses a cap: the
   * balance it used to show is what is owed, not what may be credited, and a paid invoice still has
   * a room. The amount is a positive magnitude before tax, exactly as the credit takes it; zero
   * prices the whole of what is left.
   */
  quoteCreditMemo(input: {
    customerId: string;
    branchId: string;
    invoiceId: string;
    amountCents: number;
  }): Promise<Result<BillingCreditMemoQuote>> {
    return staffBilling.billingQuoteCreditMemo(this.#client, {
      customer_id: input.customerId,
      branch_id: input.branchId,
      invoice_id: input.invoiceId,
      amount_cents: input.amountCents,
    });
  }

  /**
   * The branch's maker-checker threshold (gl_policy.je_approval_threshold_cents), read the way the
   * credit route reads it: the branch's own row over the org default, which is the same read
   * GET /api/v1/gl/policy serves.
   *
   * The form needs the number to say what the rail will do BEFORE the press. Null when the read
   * fails: the desk then says nothing about approval rather than guess, and the server still decides.
   */
  async creditApprovalThreshold(branchId: string): Promise<number | null> {
    const policy = await staffGl.glGetPolicy(this.#client, { branch_id: branchId });
    if (!policy.ok) return null;
    return policy.value.je_approval_threshold_cents;
  }
}
