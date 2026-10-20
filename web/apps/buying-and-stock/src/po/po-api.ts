// The procurement slice's one door to the core: the reads and writes the Buying
// and Stock app makes for purchase orders, over the generated client, answering
// a Result and never throwing. Every route here is a route the core already
// serves (inventory shortfall and reorder PO, the purchasing PO routes); no
// vendor bill route is wired at all.
//
// Reads: the shortfall worklist, the PO register, one PO, what is on order
// for one product, and the active catalogue the add a line picker lists.
// Writes: draft a PO from a shortfall row (the reorder PO route), set a draft's
// promised date and delivery, add, correct or remove a draft's line, and
// move a PO to ORDERED (place) or CANCELLED.
import {
  staffBranches,
  staffInventory,
  staffProduct,
  staffPurchasing,
  staffSettings,
  type ApiClient,
  type Branch,
  type InventoryReorderResult,
  type InventoryShortfallPage,
  type ProductPage,
  type PurchasingApplyWarrantyCreditRequest,
  type PurchasingIncomingLinePage,
  type PurchasingPODetail,
  type PurchasingPOPage,
  type PurchasingVendorSKUPage,
  type Result,
} from "@hardscapehouse/api-client";
import { houseDay, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";

export type DeliveryMethod = "PICKUP" | "DELIVERY" | "UNKNOWN";

/** A stalling connection must not hold the day: the zone read carries a short timeout. */
const ZONE_READ_TIMEOUT_MS = 4_000;

/**
 * The dealer's business day zone is served on the buying-and-stock surface's config read
 * (business_zone) and held by the kit; a read that fails keeps the default, so every screen still
 * names a day, never a blank screen.
 */
export async function serveBuyingZone(client: ApiClient): Promise<void> {
  const config = await staffSettings.settingsGetSurfaceConfig(client, "buying-and-stock", {}, { timeoutMs: ZONE_READ_TIMEOUT_MS });
  if (config.ok) serveHouseZoneFrom(config.value);
}

export class PoApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The dealer's day, after refreshing the served business zone from the surface config read. */
  async dealersDay(now: Date = new Date()): Promise<string> {
    await serveBuyingZone(this.#client);
    return houseDay(now);
  }

  shortfall(): Promise<Result<InventoryShortfallPage>> {
    return staffInventory.inventoryShortfall(this.#client);
  }

  async orders(): Promise<Result<PurchasingPOPage>> {
    const [, orders] = await Promise.all([serveBuyingZone(this.#client), staffPurchasing.purchasingListPOs(this.#client)]);
    return orders;
  }

  order(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingGetPO(this.#client, id);
  }

  /** The branches the caller reaches: the name shown beside "Ship to". */
  async branches(): Promise<Result<Branch[]>> {
    const [, branches] = await Promise.all([serveBuyingZone(this.#client), staffBranches.branchesList(this.#client)]);
    return branches;
  }

  /** A vendor's own codes for our SKUs (the draft's "vendor code" column). */
  vendorCodes(vendorId: string): Promise<Result<PurchasingVendorSKUPage>> {
    return staffPurchasing.purchasingListVendorSKUs(this.#client, vendorId);
  }

  /** The active catalogue, the list the add a line picker reads. */
  products(): Promise<Result<ProductPage>> {
    return staffProduct.productList(this.#client, { active: true, limit: 200 });
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

  /** Adds a line to a DRAFT: the product, a quantity, and the cost when the vendor's confirmation carries one. */
  addLine(id: string, body: { product_id: string; qty_ordered_base: number; unit_cost_cents?: number }): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingAddLine(this.#client, id, body);
  }

  /** Corrects one line's quantity or cost on a DRAFT; the product is not editable (remove and add another). */
  updateLine(id: string, lineId: string, body: { qty_ordered_base?: number; unit_cost_cents?: number }): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingUpdateLine(this.#client, id, lineId, body);
  }

  place(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingTransitionPO(this.#client, id, { to: "ORDERED" });
  }

  cancel(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingTransitionPO(this.#client, id, { to: "CANCELLED" });
  }

  /** Applies open vendor warranty credit to a DRAFT purchase order. */
  applyWarrantyCredit(id: string, params: PurchasingApplyWarrantyCreditRequest = {}): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingApplyWarrantyCredit(this.#client, id, params);
  }
}
