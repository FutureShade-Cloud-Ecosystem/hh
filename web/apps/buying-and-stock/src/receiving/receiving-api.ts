// The receiving area's one door to the core: the receiving queue, an order read in full, the
// receive, a receipt and the landed cost attached to it, the order's document, its cost payload and
// its sends, over the generated client, answering a Result and never throwing. Every route here is
// one the core serves.
import {
  staffBranches,
  staffPurchasing,
  type ApiClient,
  type Branch,
  type PurchasingAttachLandedCostRequest,
  type PurchasingPOCosts,
  type PurchasingPODetail,
  type PurchasingPOSend,
  type PurchasingPOSendPage,
  type PurchasingReceiptLine,
  type PurchasingReceiveRequest,
  type PurchasingVendorContactResult,
  type PurchasingWorklistItemPage,
  type PurchasingReceipt,
  type Result,
} from "@hardscapehouse/api-client";

export type { PurchasingReceiptLine };

export class ReceivingApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  /** The receiving queue: the orders with something still to arrive. A scoped caller's reach applies itself. */
  worklist(): Promise<Result<PurchasingWorklistItemPage>> {
    return staffPurchasing.purchasingListReceivingWorklist(this.#client);
  }

  order(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingGetPO(this.#client, id);
  }

  /** The gated money payload on its own: exactly {} for a role the policy hides cost from. */
  costs(id: string): Promise<Result<PurchasingPOCosts>> {
    return staffPurchasing.purchasingGetPOCosts(this.#client, id);
  }

  receive(id: string, body: PurchasingReceiveRequest): Promise<Result<PurchasingReceipt>> {
    return staffPurchasing.purchasingReceivePO(this.#client, id, body);
  }

  receipt(id: string): Promise<Result<PurchasingReceipt>> {
    return staffPurchasing.purchasingGetReceipt(this.#client, id);
  }

  attachLandedCost(receiptId: string, body: PurchasingAttachLandedCostRequest): Promise<Result<PurchasingReceipt>> {
    return staffPurchasing.purchasingAttachLandedCost(this.#client, receiptId, body);
  }

  document(id: string, kind: "csv" | "html"): Promise<Result<string>> {
    return kind === "csv" ? staffPurchasing.purchasingGetPODocumentCSV(this.#client, id) : staffPurchasing.purchasingGetPODocumentHTML(this.#client, id);
  }

  sends(id: string): Promise<Result<PurchasingPOSendPage>> {
    return staffPurchasing.purchasingListPOSends(this.#client, id);
  }

  /** One send: the key makes a retry of the same attempt one send, never two. */
  send(id: string, idempotencyKey: string, confirmFallback: boolean): Promise<Result<PurchasingPOSend>> {
    return staffPurchasing.purchasingSendPO(this.#client, id, { idempotency_key: idempotencyKey, ...(confirmFallback ? { confirm_fallback: true } : {}) });
  }

  contact(vendorId: string): Promise<Result<PurchasingVendorContactResult>> {
    return staffPurchasing.purchasingGetVendorContact(this.#client, vendorId);
  }
}
