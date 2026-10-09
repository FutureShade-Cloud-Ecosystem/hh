// The procurement slice's one door to the core: the reads and writes the Buying
// and Stock app makes for purchase orders, over the generated client, answering
// a Result and never throwing. Every route here is a route the core already
// serves (inventory shortfall and reorder PO, the purchasing PO routes); no
// vendor bill route is wired at all.
//
// Reads: the shortfall worklist, the PO register, one PO, and what is on order
// for one product. Writes: draft a PO from a shortfall row (the reorder PO
// route), set a draft's promised date and delivery, remove a draft's line, and
// move a PO to ORDERED (place) or CANCELLED.
import {
  staffBranches,
  staffInventory,
  staffPurchasing,
  type ApiClient,
  type Branch,
  type InventoryReorderResult,
  type InventoryShortfallPage,
  type PurchasingIncomingLinePage,
  type PurchasingPODetail,
  type PurchasingPOPage,
  type PurchasingVendorSKUPage,
  type Result,
} from "@hardscapehouse/api-client";

export type DeliveryMethod = "PICKUP" | "DELIVERY" | "UNKNOWN";

export class PoApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  shortfall(): Promise<Result<InventoryShortfallPage>> {
    return staffInventory.inventoryShortfall(this.#client);
  }

  orders(): Promise<Result<PurchasingPOPage>> {
    return staffPurchasing.purchasingListPOs(this.#client);
  }

  order(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingGetPO(this.#client, id);
  }

  /** The branches the caller reaches: the name shown beside "Ship to". */
  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  /** A vendor's own codes for our SKUs (the draft's "vendor code" column). */
  vendorCodes(vendorId: string): Promise<Result<PurchasingVendorSKUPage>> {
    return staffPurchasing.purchasingListVendorSKUs(this.#client, vendorId);
  }

  /** What is on order for one product, soonest first; the core answers it per product. */
  incomingFor(productId: string): Promise<Result<PurchasingIncomingLinePage>> {
    return staffPurchasing.purchasingListIncoming(this.#client, { product_id: productId });
  }

  /** Drafts a PO for one shortfall row at the server's suggested quantity (qty_base 0 asks for it). */
  draftFromShortfall(productId: string, branchId: string): Promise<Result<InventoryReorderResult>> {
    return staffInventory.inventoryReorderPO(this.#client, { product_id: productId, branch_id: branchId, qty_base: 0 });
  }

  setExpected(id: string, date: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingSetExpectedDate(this.#client, id, { expected_at: date });
  }

  setDelivery(id: string, method: DeliveryMethod): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingSetDeliveryMethod(this.#client, id, { delivery_method: method });
  }

  removeLine(id: string, lineId: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingRemoveLine(this.#client, id, lineId);
  }

  place(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingTransitionPO(this.#client, id, { to: "ORDERED" });
  }

  cancel(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingTransitionPO(this.#client, id, { to: "CANCELLED" });
  }
}
