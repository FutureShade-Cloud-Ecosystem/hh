// The receivables office's one door to the core: the routes the Money Desk makes beyond the first receivables slice,
// over the generated client, each answering a Result and never throwing. Every route is the core's existing
// /api/v1/ar surface; nothing new was asked of the core.
import {
  staffBilling,
  type ApiClient,
  type BillingAPContactRead,
  type BillingPaperPathPage,
  type BillingPaperPathSweepResult,
  type BillingRegisterPage,
  type BillingReminderSend,
  type BillingReminderSendPage,
  type BillingStatementRunResult,
  type Result,
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
}
