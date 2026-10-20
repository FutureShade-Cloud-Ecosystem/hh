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
import { houseDay } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { serveBuyingZone, serveBuyingZoneBeside, watchBuyingZone } from "../po/po-api.js";

export type { PurchasingReceiptLine };

export class ReceivingApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** Repaint the screen when the served zone lands and differs from the one in force. */
  watchZone(repaint: () => void): void {
    watchBuyingZone(this.#client, repaint);
  }

  /** The dealer's day, after refreshing the served business zone from the surface config read. */
  async dealersDay(now: Date = new Date()): Promise<string> {
    await serveBuyingZone(this.#client);
    return houseDay(now);
  }

  async branches(): Promise<Result<Branch[]>> {
    serveBuyingZoneBeside(this.#client);
    return staffBranches.branchesList(this.#client);
  }

  /** The receiving queue: the orders with something still to arrive. A scoped caller's reach applies itself. */
  async worklist(): Promise<Result<PurchasingWorklistItemPage>> {
    serveBuyingZoneBeside(this.#client);
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
