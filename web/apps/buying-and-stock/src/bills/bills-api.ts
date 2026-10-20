// The bills area's one door to the core: the vendor bills and their three way match, the tolerance
// and the cost policy, and the order register the entry form starts from, over the generated
// client, answering a Result and never throwing. Every route here is one the core serves.
import {
  staffBranches,
  staffPurchasing,
  type ApiClient,
  type Branch,
  type PurchasingBillDetail,
  type PurchasingBillPage,
  type PurchasingBillStatus,
  type PurchasingCreateBillRequest,
  type PurchasingMatchState,
  type PurchasingPODetail,
  type PurchasingPOPage,
  type PurchasingPolicy,
  type PurchasingSetPolicyRequest,
  type PurchasingSetToleranceRequest,
  type PurchasingTolerance,
  type Result,
} from "@hardscapehouse/api-client";
import { serveBuyingZone, serveBuyingZoneBeside, watchBuyingZone } from "../po/po-api.js";

export class BillsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Repaint the screen when the served zone lands and differs from the one in force. */
  watchZone(repaint: () => void): void {
    watchBuyingZone(this.#client, repaint);
  }

  async branches(): Promise<Result<Branch[]>> {
    serveBuyingZoneBeside(this.#client);
    return staffBranches.branchesList(this.#client);
  }

  /** The register; the entry form keeps the orders a bill can be entered against. */
  orders(): Promise<Result<PurchasingPOPage>> {
    return staffPurchasing.purchasingListPOs(this.#client);
  }

  order(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingGetPO(this.#client, id);
  }

  /** A scoped caller names a branch the read can use; the filters narrow it. */
  list(branchId: string, status?: PurchasingBillStatus, matchState?: PurchasingMatchState): Promise<Result<PurchasingBillPage>> {
    return staffPurchasing.purchasingListBills(this.#client, {
      ...(branchId === "" ? {} : { branch_id: branchId }),
      ...(status === undefined ? {} : { status }),
      ...(matchState === undefined ? {} : { match_state: matchState }),
    });
  }

  get(id: string): Promise<Result<PurchasingBillDetail>> {
    return staffPurchasing.purchasingGetBill(this.#client, id);
  }

  create(body: PurchasingCreateBillRequest): Promise<Result<PurchasingBillDetail>> {
    return staffPurchasing.purchasingCreateBill(this.#client, body);
  }

  /** Posting re-runs the match against what has arrived by now; a variance needs an acceptance and a note. */
  post(id: string, acceptance?: { note: string }): Promise<Result<PurchasingBillDetail>> {
    return staffPurchasing.purchasingPostBill(this.#client, id, acceptance === undefined ? {} : { accept_variance: true, note: acceptance.note });
  }

  tolerance(branchId: string): Promise<Result<PurchasingTolerance>> {
    return staffPurchasing.purchasingGetTolerance(this.#client, branchId === "" ? undefined : { branch_id: branchId });
  }

  setTolerance(body: PurchasingSetToleranceRequest): Promise<Result<PurchasingTolerance>> {
    return staffPurchasing.purchasingSetTolerance(this.#client, body);
  }

  policy(branchId: string): Promise<Result<PurchasingPolicy>> {
    return staffPurchasing.purchasingGetPolicy(this.#client, branchId === "" ? undefined : { branch_id: branchId });
  }

  setPolicy(body: PurchasingSetPolicyRequest): Promise<Result<PurchasingPolicy>> {
    return staffPurchasing.purchasingSetPolicy(this.#client, body);
  }
}
