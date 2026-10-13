// The counts slice's one door to the core: the eleven count routes, over the
// generated client, answering a Result and never throwing. Every route here is
// a route the core already serves (core/internal/invcount); the counts pages
// answer under the contract's items key, so no envelope reading is needed.
import {
  staffBranches,
  staffInvcount,
  type ApiClient,
  type Branch,
  type InvCountCancelResult,
  type InvCountCountLineInput,
  type InvCountCreateSheetRequest,
  type InvCountNegativeStockPage,
  type InvCountReasonCodePage,
  type InvCountReconResult,
  type InvCountSheet,
  type InvCountSheetDetail,
  type InvCountSheetPage,
  type InvCountSubmitCountsRequest,
  type Result,
} from "@hardscapehouse/api-client";

export class CountsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The branches the caller reaches: the names the sheets and the recon show. */
  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  sheets(branchId?: string): Promise<Result<InvCountSheetPage>> {
    return staffInvcount.invcountListSheets(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  sheet(id: string): Promise<Result<InvCountSheetDetail>> {
    return staffInvcount.invcountGetSheet(this.#client, id);
  }

  createSheet(body: InvCountCreateSheetRequest): Promise<Result<InvCountSheet>> {
    return staffInvcount.invcountCreateSheet(this.#client, body);
  }

  freeze(id: string): Promise<Result<InvCountSheet>> {
    return staffInvcount.invcountFreezeSheet(this.#client, id);
  }

  submitCounts(id: string, lines: InvCountCountLineInput[]): Promise<Result<InvCountSheet>> {
    const body: InvCountSubmitCountsRequest = { lines };
    return staffInvcount.invcountSubmitCounts(this.#client, id, body);
  }

  approve(id: string): Promise<Result<InvCountSheet>> {
    return staffInvcount.invcountApproveSheet(this.#client, id);
  }

  post(id: string): Promise<Result<InvCountSheetDetail>> {
    return staffInvcount.invcountPostSheet(this.#client, id);
  }

  cancel(id: string): Promise<Result<InvCountCancelResult>> {
    return staffInvcount.invcountCancelSheet(this.#client, id);
  }

  reasonCodes(): Promise<Result<InvCountReasonCodePage>> {
    return staffInvcount.invcountListReasonCodes(this.#client);
  }

  negativeStock(branchId?: string): Promise<Result<InvCountNegativeStockPage>> {
    return staffInvcount.invcountListNegativeStock(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  recon(branchId: string): Promise<Result<InvCountReconResult>> {
    return staffInvcount.invcountRecon(this.#client, { branch_id: branchId });
  }
}
