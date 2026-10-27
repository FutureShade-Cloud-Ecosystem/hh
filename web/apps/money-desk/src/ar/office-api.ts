// The receivables office's one door to the core: the routes the Money Desk makes beyond the first receivables slice,
// over the generated client, each answering a Result and never throwing. Every route is the core's existing
// /api/v1/ar surface; nothing new was asked of the core.
import {
  staffBilling,
  type ApiClient,
  type BillingAPContactRead,
  type BillingAutoHoldSetting,
  type BillingAccountPaymentPolicy,
  type BillingAccountPaymentPolicyInput,
  type BillingDispatchGateResult,
  type BillingDisputeView,
  type BillingLandingPolicy,
  type BillingPolicyChangePage,
  type BillingPaperPathPage,
  type BillingPaperPathSweepResult,
  type BillingPromisePage,
  type BillingRegisterPage,
  type BillingRegisterPolicy,
  type BillingReminderEnrolResult,
  type BillingReminderResult,
  type BillingReminderRulePage,
  type BillingReminderSend,
  type BillingReminderSendPage,
  type BillingStatementRunResult,
  type PaymentsHeld,
  type PaymentsReapplication,
  type PaymentsReversalResult,
  type PaymentsVoidResult,
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

  /** Voids a mis-keyed desk receipt as never taken (FW1-06b): manager only, the same dealer day, nothing held. */
  voidPayment(paymentId: string, reason: string): Promise<Result<PaymentsVoidResult>> {
    return staffPayments.paymentsVoidPayment(this.#client, paymentId, { reason });
  }

  /** What a reversed payment still holds in 2310: the figure the re-application form shows. */
  heldFor(paymentId: string): Promise<Result<PaymentsHeld>> {
    return staffPayments.paymentsHeldFor(this.#client, paymentId);
  }

  /** Applies up to what a reversed payment holds to one invoice of the same customer; one key per attempt. */
  reapplyHeld(paymentId: string, input: { invoiceId: string; amountCents: number; reason: string; key: string }): Promise<Result<PaymentsReapplication>> {
    return staffPayments.paymentsReapplyHeld(this.#client, paymentId, {
      reason: input.reason,
      idempotency_key: input.key,
      allocations: [{ invoice_id: input.invoiceId, amount_cents: input.amountCents }],
    });
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

  // ---- walk change 8: account payment policy (S6), reminder rules (S8 / S5M), enrol / exempt -------------------

  /** Read the account's payment policy (the take-a-payment panel reads this to order its tenders). */
  accountPaymentPolicy(customerId: string): Promise<Result<BillingAccountPaymentPolicy>> {
    return staffBilling.billingGetAccountPaymentPolicy(this.#client, customerId);
  }

  /** Upsert the account's payment policy (ach_first, surcharge_rate_bps, min_ach_cents). */
  putAccountPaymentPolicy(customerId: string, body: BillingAccountPaymentPolicyInput): Promise<Result<BillingAccountPaymentPolicy>> {
    return staffBilling.billingPutAccountPaymentPolicy(this.#client, customerId, body);
  }

  /** The cross-account Promises tab read. */
  promises(status?: readonly string[], customerId?: string): Promise<Result<BillingPromisePage>> {
    const statusParam = status === undefined || status.length === 0 ? undefined : (status.join(",") as "OPEN" | "KEPT" | "BROKEN" | "CANCELLED" | "OPEN,KEPT");
    return staffBilling.billingListPromises(this.#client, {
      limit: 100,
      ...(statusParam === undefined ? {} : { status: statusParam }),
      ...(customerId === undefined || customerId === "" ? {} : { customer_id: customerId }),
    });
  }

  /** The reminder rules table (S5M / S8). */
  reminderRules(): Promise<Result<BillingReminderRulePage>> {
    return staffBilling.billingListReminderRules(this.#client);
  }

  /** Re-enrol an exempt account into one reminder rule. */
  enrolReminder(ruleId: string, customerId: string, note?: string): Promise<Result<BillingReminderEnrolResult>> {
    return staffBilling.billingEnrolReminder(this.#client, {
      customer_id: customerId,
      rule_id: ruleId,
      ...(note === undefined || note === "" ? {} : { note }),
    });
  }

  /** Exempt an account from one reminder rule. The note is mandatory and passed verbatim. */
  exemptReminder(ruleId: string, customerId: string, note: string): Promise<Result<BillingReminderEnrolResult>> {
    return staffBilling.billingExemptReminder(this.#client, {
      customer_id: customerId,
      rule_id: ruleId,
      note,
    });
  }
}
