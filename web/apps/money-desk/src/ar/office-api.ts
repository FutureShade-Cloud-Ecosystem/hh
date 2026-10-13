// The receivables office's one door to the core: the routes the Money Desk makes beyond the first receivables slice,
// over the generated client, each answering a Result and never throwing. Every route is the core's existing
// /api/v1/ar surface; nothing new was asked of the core.
import {
  staffBilling,
  type ApiClient,
  type BillingAPContactRead,
  type BillingAutoHoldSetting,
  type BillingDispatchGateResult,
  type BillingDisputeView,
  type BillingLandingPolicy,
  type BillingPolicyChangePage,
  type BillingPaperPathPage,
  type BillingPaperPathSweepResult,
  type BillingRegisterPage,
  type BillingRegisterPolicy,
  type BillingReminderResult,
  type BillingReminderSend,
  type BillingReminderSendPage,
  type BillingStatementRunResult,
  type PaymentsReversalResult,
  type Result,
  type TillCreditHoldPage,
  type TillNSFPolicy,
  type TillStatusResult,
  staffPayments,
  staffTill,
} from "@hardscapehouse/api-client";

export class OfficeApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** A statement run read back by its id: the run's counts and the statements it made. */
  statementRun(id: string): Promise<Result<BillingStatementRunResult>> {
    return staffBilling.billingGetStatementRun(this.#client, id);
  }

  apContact(customerId: string): Promise<Result<BillingAPContactRead>> {
    return staffBilling.billingGetAPContact(this.#client, customerId);
  }

  /** Tags or replaces the account's AP contact: the save replaces the whole contact. */
  putApContact(customerId: string, contact: { contact_name: string; email: string; phone: string }): Promise<Result<BillingAPContactRead>> {
    return staffBilling.billingPutAPContact(this.#client, customerId, { customer_id: customerId, ...contact });
  }

  deleteApContact(customerId: string): Promise<Result<BillingAPContactRead>> {
    return staffBilling.billingDeleteAPContact(this.#client, customerId);
  }

  /** An account's invoices, paid ones too: the reminder asks which invoice it is about. */
  accountInvoices(customerId: string): Promise<Result<BillingRegisterPage>> {
    return staffBilling.billingListRegister(this.#client, { customer_id: customerId, limit: 100 });
  }

  /** The reminders sent, newest first, for every account or one. */
  reminders(customerId?: string): Promise<Result<BillingReminderSendPage>> {
    return staffBilling.billingListARReminders(this.#client, { limit: 50, ...(customerId === undefined || customerId === "" ? {} : { customer_id: customerId }) });
  }

  sendReminder(input: { customerId: string; invoiceId: string; channel: "EMAIL" | "SMS"; resend: boolean; confirmFallback: boolean; key: string }): Promise<Result<BillingReminderSend>> {
    return staffBilling.billingSendARReminder(this.#client, {
      customer_id: input.customerId,
      invoice_id: input.invoiceId,
      channel: input.channel,
      resend: input.resend,
      confirm_fallback: input.confirmFallback,
      idempotency_key: input.key,
    });
  }

  paperPath(): Promise<Result<BillingPaperPathPage>> {
    return staffBilling.billingListPaperPath(this.#client, { limit: 100 });
  }

  sweepPaperPath(): Promise<Result<BillingPaperPathSweepResult>> {
    return staffBilling.billingSweepPaperPath(this.#client);
  }

  resolvePaperPath(flagId: string, status: "MOVED_TO_PAPER" | "DISMISSED", note: string): Promise<Result<{ ok: boolean }>> {
    return staffBilling.billingResolvePaperPath(this.#client, flagId, { status, note });
  }

  /** Sends the invoice document to the account's contact; one key per attempt. */
  sendInvoice(id: string, key: string): Promise<Result<BillingReminderResult>> {
    return staffBilling.billingSendInvoice(this.#client, id, { idempotency_key: key });
  }

  /** The invoice as a document: HTML text, read fresh and never cached. */
  invoiceDocument(id: string): Promise<Result<string>> {
    return staffBilling.billingGetRegisterInvoiceDocument(this.#client, id);
  }

  openDispute(id: string, input: { reason: string; note: string; amountCents: number; key: string }): Promise<Result<BillingDisputeView>> {
    return staffBilling.billingOpenDispute(this.#client, id, {
      reason: input.reason as BillingDisputeView["reason"],
      note: input.note,
      amount_cents: input.amountCents,
      idempotency_key: input.key,
    });
  }

  resolveDispute(id: string, outcome: string): Promise<Result<{ resolved: boolean }>> {
    return staffBilling.billingResolveDispute(this.#client, id, { outcome: outcome as "PAID" | "CREDITED" | "WRITTEN_OFF" | "WITHDRAWN" });
  }

  reversePayment(paymentId: string, reason: string): Promise<Result<PaymentsReversalResult>> {
    return staffPayments.paymentsReversePayment(this.#client, paymentId, { reason });
  }

  // ---- settings: the receivables rules, the register, the returned payment fee, automatic holds -------------------

  landingPolicy(): Promise<Result<BillingLandingPolicy>> {
    return staffBilling.billingGetARLandingPolicy(this.#client);
  }

  putLandingPolicy(policy: Omit<BillingLandingPolicy, "branch_id">): Promise<Result<BillingLandingPolicy>> {
    return staffBilling.billingPutARLandingPolicy(this.#client, policy);
  }

  policyHistory(): Promise<Result<BillingPolicyChangePage>> {
    return staffBilling.billingGetARLandingPolicyHistory(this.#client, { limit: 50 });
  }

  registerPolicy(): Promise<Result<BillingRegisterPolicy>> {
    return staffBilling.billingGetRegisterPolicy(this.#client);
  }

  putRegisterPolicy(policy: BillingRegisterPolicy): Promise<Result<{ saved: boolean }>> {
    return staffBilling.billingPutRegisterPolicy(this.#client, policy);
  }

  nsfPolicy(): Promise<Result<TillNSFPolicy>> {
    return staffTill.tillGetNSFPolicy(this.#client);
  }

  putNsfPolicy(policy: TillNSFPolicy): Promise<Result<TillStatusResult>> {
    return staffTill.tillUpdateNSFPolicy(this.#client, policy);
  }

  autoHold(): Promise<Result<BillingAutoHoldSetting>> {
    return staffBilling.billingGetAutoHold(this.#client);
  }

  putAutoHold(enabled: boolean, reason: string): Promise<Result<BillingAutoHoldSetting>> {
    return staffBilling.billingPutAutoHold(this.#client, { enabled, ...(reason === "" ? {} : { reason }) });
  }

  // ---- holds a returned payment placed, and the dispatch gate -----------------------------------------------------

  tillHolds(): Promise<Result<TillCreditHoldPage>> {
    return staffTill.tillListCreditHolds(this.#client, { limit: 100 });
  }

  /** An NSF hold is lifted through the till's path: the billing release refuses it, in the core's words. */
  releaseTillHold(holdId: string, reason: string): Promise<Result<TillStatusResult>> {
    return staffTill.tillReleaseCreditHold(this.#client, holdId, { reason });
  }

  dispatchGate(orderId: string): Promise<Result<BillingDispatchGateResult>> {
    return staffBilling.billingGetDispatchGate(this.#client, orderId);
  }
}
