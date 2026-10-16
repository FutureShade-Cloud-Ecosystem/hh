// The inventory slice's one door to the core: the reads and writes the
// inventory area makes, over the generated client, answering a Result and
// never throwing. Every route here is a route the core already serves
// (core/internal/inventory); no route here changes the core.
//
// THE ENVELOPE KEYS ARE THE CORE'S. Ten of these reads answer under a
// domain-named key (on_hand, moves, pending, branches, reservations,
// transfers, lines, pairs, reorder_points; browse under rows), not under the
// items key the contract's page schemas declare; the shortfall worklist and
// every counts route answer under items, as FROM-PRINCIPAL 72 settled for the
// worklist. The row readers below read the keys the core actually writes, so
// the desk never shows an empty table against a server that answered. The
// divergence is recorded in the lane's report; the generated types still say
// items, so the readers take the answers through one honest cast each.
import {
  staffBranches,
  staffInventory,
  staffProduct,
  staffPurchasing,
  type ApiClient,
  type Branch,
  type InventoryAllocationPolicy,
  type InventoryBranchStock,
  type InventoryBrowseRow,
  type InventoryFacet,
  type InventoryInTransitLine,
  type InventoryInTransitLinePage,
  type InventoryMovePage,
  type InventoryOnHand,
  type InventoryOnHandPage,
  type InventoryPendingFulfilment,
  type InventoryPendingFulfilmentPage,
  type InventoryBranchStockPage,
  type InventoryBrowsePage,
  type InventoryReorderPoint,
  type InventoryReorderPointPage,
  type InventoryReservation,
  type InventoryReservationPage,
  type InventoryReserveInput,
  type InventoryReserveResult,
  type InventoryReceiptResult,
  type InventoryStockMove,
  type InventoryTransitDaysPair,
  type InventoryTransitDaysPairPage,
  type InventoryTransfer,
  type InventoryTransferPage,
  type InventoryReleaseRequest,
  type InventoryReleaseResult,
  type InventoryExpireResult,
  type InventorySetTransitDaysInput,
  type InventoryNudgeResult,
  type InventoryCreatePOResult,
  type Product,
  type ProductPage,
  type PurchasingIncomingLinePage,
  type PurchasingPOPage,
  type PurchasingPODetail,
  type Result,
} from "@hardscapehouse/api-client";

// ---- the shapes the core really writes -------------------------------------------------------------

type Body = Record<string, unknown>;

function list<T>(body: unknown, key: string): T[] {
  const rows = (body as Body)[key];
  return Array.isArray(rows) ? (rows as T[]) : [];
}

/** The on-hand page under the core's key. */
export function onHandRows(page: Partial<InventoryOnHandPage>): InventoryOnHand[] {
  return list(page, "on_hand");
}

/** The move ledger under the core's key. */
export function moveRows(page: Partial<InventoryMovePage>): InventoryStockMove[] {
  return list(page, "moves");
}

/** The deferred fulfilment lines under the core's key. */
export function pendingRows(page: Partial<InventoryPendingFulfilmentPage>): InventoryPendingFulfilment[] {
  return list(page, "pending");
}

/** The four-bucket per-branch rows under the core's key. */
export function branchRows(page: Partial<InventoryBranchStockPage>): InventoryBranchStock[] {
  return list(page, "branches");
}

/** Browse's page: the core writes rows, facets, total and the echoed page. */
export interface BrowseAnswer {
  readonly rows: InventoryBrowseRow[];
  readonly facets: InventoryFacet[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

export function browseAnswer(page: Partial<InventoryBrowsePage>): BrowseAnswer {
  const body = page as Body;
  return {
    rows: list(body, "rows"),
    facets: list(body, "facets"),
    total: typeof body.total === "number" ? body.total : 0,
    limit: typeof body.limit === "number" ? body.limit : 60,
    offset: typeof body.offset === "number" ? body.offset : 0,
  };
}

/** The active holds under the core's key. */
export function reservationRows(page: Partial<InventoryReservationPage>): InventoryReservation[] {
  return list(page, "reservations");
}

/** The transfers under the core's key, lines included. */
export function transferRows(page: Partial<InventoryTransferPage>): InventoryTransfer[] {
  return list(page, "transfers");
}

/** The truck's contents for one product, under the core's key. */
export function incomingRows(body: unknown): InventoryInTransitLine[] {
  return list(body, "lines");
}

/** Every ordered branch pair under the core's key. */
export function transitPairs(page: Partial<InventoryTransitDaysPairPage>): InventoryTransitDaysPair[] {
  return list(page, "pairs");
}

/** The managed replenishment lines under the core's key. */
export function pointRows(page: Partial<InventoryReorderPointPage>): InventoryReorderPoint[] {
  return list(page, "reorder_points");
}

// ---- the door --------------------------------------------------------------------------------------

export class InventoryApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The branches the caller reaches: the names the screens show. */
  branches(): Promise<Result<Branch[]>> {
    return staffBranches.branchesList(this.#client);
  }

  /** The active catalogue: the pickers' lists. */
  products(): Promise<Result<ProductPage>> {
    return staffProduct.productList(this.#client, { active: true, limit: 200 });
  }

  /** One product's own record: the list price and the sale unit the stock lookup's header reads. */
  product(id: string): Promise<Result<Product>> {
    return staffProduct.productGet(this.#client, id);
  }

  onHand(productId: string): Promise<Result<InventoryOnHandPage>> {
    return staffInventory.inventoryOnHand(this.#client, { product_id: productId });
  }

  moves(productId: string, branchId?: string): Promise<Result<InventoryMovePage>> {
    return staffInventory.inventoryListMoves(this.#client, { product_id: productId, ...(branchId ? { branch_id: branchId } : {}) });
  }

  pending(branchId?: string): Promise<Result<InventoryPendingFulfilmentPage>> {
    return staffInventory.inventoryListPendingFulfilment(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  branchStock(productId: string, branchId?: string): Promise<Result<InventoryBranchStockPage>> {
    return staffInventory.inventoryBranchStock(this.#client, { product_id: productId, ...(branchId ? { branch_id: branchId } : {}) });
  }

  browse(text: string): Promise<Result<InventoryBrowsePage>> {
    return staffInventory.inventoryBrowse(this.#client, { text, limit: 60 });
  }

  transitDays(): Promise<Result<InventoryTransitDaysPairPage>> {
    return staffInventory.inventoryListTransitDays(this.#client);
  }

  putTransitDays(body: InventorySetTransitDaysInput): Promise<Result<InventoryTransitDaysPair>> {
    return staffInventory.inventoryPutTransitDays(this.#client, body);
  }

  reservations(branchId?: string): Promise<Result<InventoryReservationPage>> {
    return staffInventory.inventoryListReservations(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  reserve(body: InventoryReserveInput): Promise<Result<InventoryReserveResult>> {
    return staffInventory.inventoryCreateReservation(this.#client, body);
  }

  release(body: InventoryReleaseRequest): Promise<Result<InventoryReleaseResult>> {
    return staffInventory.inventoryReleaseReservation(this.#client, body);
  }

  expireReservations(): Promise<Result<InventoryExpireResult>> {
    return staffInventory.inventoryExpireReservations(this.#client);
  }

  allocationPolicy(branchId?: string): Promise<Result<InventoryAllocationPolicy>> {
    return staffInventory.inventoryGetAllocationPolicy(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  putAllocationPolicy(policy: InventoryAllocationPolicy): Promise<Result<InventoryAllocationPolicy>> {
    return staffInventory.inventoryPutAllocationPolicy(this.#client, policy);
  }

  transfers(branchId?: string): Promise<Result<InventoryTransferPage>> {
    return staffInventory.inventoryListTransfers(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  transfer(id: string): Promise<Result<InventoryTransfer>> {
    return staffInventory.inventoryGetTransfer(this.#client, id);
  }

  createTransfer(body: { from_branch_id: string; to_branch_id: string; notes: string; lines: { product_id: string; qty_base: number }[] }): Promise<Result<InventoryTransfer>> {
    return staffInventory.inventoryCreateTransfer(this.#client, body);
  }

  shipTransfer(id: string, eta?: string): Promise<Result<InventoryTransfer>> {
    return staffInventory.inventoryShipTransfer(this.#client, id, eta === undefined ? {} : { eta });
  }

  receiveTransfer(id: string): Promise<Result<InventoryTransfer>> {
    return staffInventory.inventoryReceiveTransfer(this.#client, id);
  }

  cancelTransfer(id: string): Promise<Result<InventoryTransfer>> {
    return staffInventory.inventoryCancelTransfer(this.#client, id);
  }

  incomingTransfers(productId: string): Promise<Result<InventoryInTransitLinePage>> {
    return staffInventory.inventoryListIncomingTransfers(this.#client, { product_id: productId });
  }

  receivePO(poId: string, lines: { po_line_id: string; qty_base: number }[]): Promise<Result<InventoryReceiptResult>> {
    return staffInventory.inventoryReceivePO(this.#client, { po_id: poId, lines });
  }

  reorderPoints(branchId?: string): Promise<Result<InventoryReorderPointPage>> {
    return staffInventory.inventoryListReorderPoints(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  putReorderPoint(point: InventoryReorderPoint): Promise<Result<InventoryReorderPoint>> {
    return staffInventory.inventoryPutReorderPoint(this.#client, point);
  }

  bootstrap(branchId: string): Promise<Result<{ created: number }>> {
    return staffInventory.inventoryBootstrapReorderPoints(this.#client, { branch_id: branchId });
  }

  nudge(productId: string, branchId: string): Promise<Result<InventoryNudgeResult>> {
    return staffInventory.inventoryNudgeProcurement(this.#client, { product_id: productId, branch_id: branchId });
  }

  createPOFromShortfall(branchId: string, productIds: string[]): Promise<Result<InventoryCreatePOResult>> {
    return staffInventory.inventoryCreatePOFromShortfall(this.#client, { branch_id: branchId, product_ids: productIds });
  }

  /** The buyer's placed orders, the receive tab's list. */
  orders(): Promise<Result<PurchasingPOPage>> {
    return staffPurchasing.purchasingListPOs(this.#client);
  }

  order(id: string): Promise<Result<PurchasingPODetail>> {
    return staffPurchasing.purchasingGetPO(this.#client, id);
  }

  /** What is still to land on one order, the receive confirm's facts. */
  incomingFor(productId: string): Promise<Result<PurchasingIncomingLinePage>> {
    return staffPurchasing.purchasingListIncoming(this.#client, { product_id: productId });
  }
}
