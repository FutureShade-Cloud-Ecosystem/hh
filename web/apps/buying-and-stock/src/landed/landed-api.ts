// The landed cost area's one door to the core: the seven landed cost routes and the reads they
// lean on (the order register, one order for its lines, the caller's branches), over the generated
// client, answering a Result and never throwing. No route here is one the core lacks.
import {
  staffBranches,
  staffLandedcost,
  staffPurchasing,
  type ApiClient,
  type Branch,
  type LandedCostAllocationPreview,
  type LandedCostBasis,
  type LandedCostCreateRequest,
  type LandedCostCreateResult,
  type LandedCostDetail,
  type LandedCostPage,
  type LandedCostStatus,
  type LandedCostStatusResult,
  type PurchasingPODetail,
  type PurchasingPOPage,
  type Result,
} from "@hardscapehouse/api-client";
import { serveBuyingZone } from "../po/po-api.js";

export class LandedApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  async branches(): Promise<Result<Branch[]>> {
    const [, branches] = await Promise.all([serveBuyingZone(this.#client), staffBranches.branchesList(this.#client)]);
    return branches;
  }

  /** The register; the area keeps the orders with goods received. */
  orders(): Promise<Result<PurchasingPOPage>> {
    return staffPurchasing.purchasingListPOs(this.#client);
  }

  order(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingGetPO(this.#client, id);
  }

  /** A scoped caller must name a branch it reaches: the core refuses an unnamed read in words. */
  list(branchId: string, status?: LandedCostStatus): Promise<Result<LandedCostPage>> {
    return staffLandedcost.landedCostList(this.#client, { branch_id: branchId, ...(status === undefined ? {} : { status }) });
  }

  get(id: string): Promise<Result<LandedCostDetail>> {
    return staffLandedcost.landedCostGet(this.#client, id);
  }

  /** The live split of the total across the received lines under a basis; writes nothing. */
  preview(id: string, basis?: LandedCostBasis): Promise<Result<LandedCostAllocationPreview>> {
    return staffLandedcost.landedCostPreview(this.#client, id, basis === undefined ? undefined : { basis });
  }

  create(body: LandedCostCreateRequest): Promise<Result<LandedCostCreateResult>> {
    return staffLandedcost.landedCostCreate(this.#client, body);
  }

  apply(id: string): Promise<Result<LandedCostStatusResult>> {
    return staffLandedcost.landedCostApply(this.#client, id);
  }

  retro(id: string): Promise<Result<LandedCostStatusResult>> {
    return staffLandedcost.landedCostRetro(this.#client, id);
  }

  void(id: string): Promise<Result<LandedCostStatusResult>> {
    return staffLandedcost.landedCostVoid(this.#client, id);
  }
}
