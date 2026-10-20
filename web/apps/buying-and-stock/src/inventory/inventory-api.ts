// The inventory slice's one door to the core: the reads and writes the
// inventory area makes, over the generated client, answering a Result and
// never throwing. Every route here is a route the core already serves
// (core/internal/inventory); no route here changes the core.
//
// THE ENVELOPE KEYS ARE THE CORE'S, AND THE CONTRACT SAYS THEM. Ten of these
// reads answer under a domain-named key (on_hand, moves, pending, branches,
// reservations, transfers, lines, pairs, reorder_points; browse under rows,
// with its total and the echoed page); the contract moved to those keys, so
// the generated types carry them and the readers below are typed access with
// no cast. A list the core can write as null (it has no nil guard on branches,
// reservations, transfers, reorder points or browse's facets) reads as empty.
// The shortfall worklist and every counts route answer under items.
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
  type InventoryInTransitLineList,
  type InventoryMoveList,
  type InventoryOnHand,
  type InventoryOnHandList,
  type InventoryPendingFulfilment,
  type InventoryPendingFulfilmentList,
  type InventoryBranchStockList,
  type InventoryBrowseResult,
  type InventoryReorderPoint,
  type InventoryReorderPointList,
  type InventoryReservation,
  type InventoryReservationList,
  type InventoryReserveInput,
  type InventoryReserveResult,
  type InventoryReceiptResult,
  type InventoryStockMove,
  type InventoryTransitDaysPair,
  type InventoryTransitDaysPairList,
  type InventoryTransfer,
  type InventoryTransferList,
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

import { serveBuyingZone } from "../po/po-api.js";

// ---- the shapes the core writes --------------------------------------------------------------------

/** The on-hand rows. */
export function onHandRows(page: InventoryOnHandList): InventoryOnHand[] {
  return page.on_hand;
}

/** The move ledger. */
export function moveRows(page: InventoryMoveList): InventoryStockMove[] {
  return page.moves;
}

/** The deferred fulfilment lines. */
export function pendingRows(page: InventoryPendingFulfilmentList): InventoryPendingFulfilment[] {
  return page.pending;
}

/** The four-bucket per-branch rows. */
export function branchRows(page: InventoryBranchStockList): InventoryBranchStock[] {
  return page.branches ?? [];
}

/** Browse's page: the rows, the facets, the honest total and the echoed page. */
export interface BrowseAnswer {
  readonly rows: InventoryBrowseRow[];
  readonly facets: InventoryFacet[];
  readonly total: number;
  readonly limit: number;
  readonly offset: number;
}

export function browseAnswer(page: InventoryBrowseResult): BrowseAnswer {
  return { rows: page.rows, facets: page.facets ?? [], total: page.total, limit: page.limit, offset: page.offset };
}

/** The active holds. */
export function reservationRows(page: InventoryReservationList): InventoryReservation[] {
  return page.reservations ?? [];
}

/** The transfers, lines included. */
export function transferRows(page: InventoryTransferList): InventoryTransfer[] {
  return page.transfers ?? [];
}

/** The truck's contents for one product. */
export function incomingRows(page: InventoryInTransitLineList): InventoryInTransitLine[] {
  return page.lines;
}

/** Every ordered branch pair. */
export function transitPairs(page: InventoryTransitDaysPairList): InventoryTransitDaysPair[] {
  return page.pairs;
}

/** The managed replenishment lines. */
export function pointRows(page: InventoryReorderPointList): InventoryReorderPoint[] {
  return page.reorder_points ?? [];
}

// ---- the door --------------------------------------------------------------------------------------

export class InventoryApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  /** The branches the caller reaches: the names the screens show. */
  async branches(): Promise<Result<Branch[]>> {
    const [, branches] = await Promise.all([serveBuyingZone(this.#client), staffBranches.branchesList(this.#client)]);
    return branches;
  }

  /** The active catalogue: the pickers' lists. */
  products(): Promise<Result<ProductPage>> {
    return staffProduct.productList(this.#client, { active: true, limit: 200 });
  }

  /** One product's own record: the list price and the sale unit the stock lookup's header reads. */
  product(id: string): Promise<Result<Product>> {
    return staffProduct.productGet(this.#client, id);
  }

  onHand(productId: string): Promise<Result<InventoryOnHandList>> {
    return staffInventory.inventoryOnHand(this.#client, { product_id: productId });
  }

  moves(productId: string, branchId?: string): Promise<Result<InventoryMoveList>> {
    return staffInventory.inventoryListMoves(this.#client, { product_id: productId, ...(branchId ? { branch_id: branchId } : {}) });
  }

  pending(branchId?: string): Promise<Result<InventoryPendingFulfilmentList>> {
    return staffInventory.inventoryListPendingFulfilment(this.#client, branchId ? { branch_id: branchId } : undefined);
  }

  branchStock(productId: string, branchId?: string): Promise<Result<InventoryBranchStockList>> {
    return staffInventory.inventoryBranchStock(this.#client, { product_id: productId, ...(branchId ? { branch_id: branchId } : {}) });
  }

  browse(text: string): Promise<Result<InventoryBrowseResult>> {
    return staffInventory.inventoryBrowse(this.#client, { text, limit: 60 });
  }

  transitDays(): Promise<Result<InventoryTransitDaysPairList>> {
    return staffInventory.inventoryListTransitDays(this.#client);
  }

  putTransitDays(body: InventorySetTransitDaysInput): Promise<Result<InventoryTransitDaysPair>> {
    return staffInventory.inventoryPutTransitDays(this.#client, body);
  }

  reservations(branchId?: string): Promise<Result<InventoryReservationList>> {
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

  transfers(branchId?: string): Promise<Result<InventoryTransferList>> {
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

  incomingTransfers(productId: string): Promise<Result<InventoryInTransitLineList>> {
    return staffInventory.inventoryListIncomingTransfers(this.#client, { product_id: productId });
  }

  receivePO(poId: string, lines: { po_line_id: string; qty_base: number }[]): Promise<Result<InventoryReceiptResult>> {
    return staffInventory.inventoryReceivePO(this.#client, { po_id: poId, lines });
  }

  reorderPoints(branchId?: string): Promise<Result<InventoryReorderPointList>> {
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
