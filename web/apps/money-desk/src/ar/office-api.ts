// The receivables office's one door to the core: the routes the Money Desk makes beyond the first receivables slice,
// over the generated client, each answering a Result and never throwing. Every route is the core's existing
// /api/v1/ar surface; nothing new was asked of the core.
import {
  staffBilling,
  type ApiClient,
  type BillingAPContactRead,
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
}
