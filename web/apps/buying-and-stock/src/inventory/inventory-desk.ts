// The inventory area's controller: the state the screens draw from, the loads,
// and what each press does. It is not an element; the root element owns it,
// hands it a way to repaint and a way to read the form fields, and asks it for
// the template.
//
// Every write is held to the server: a hold exists only when the core wrote
// it, a truck is loaded only when the core answers IN_TRANSIT, and the screen
// then shows what the core now says, never what the app expected. A refusal
// reads in words (inventory-logic.ts); a walled move in words naming who may
// do it. Nothing is stored.
import type { TemplateResult } from "lit";
import type { DeskHost } from "../purch/area.js";
import type { InventoryAllocationPolicy, InventoryBranchStock, InventoryInTransitLine, InventoryOnHand, InventoryPendingFulfilment, InventoryReorderPoint, InventoryReservation, InventoryStockMove, InventoryTransitDaysPair, InventoryTransfer, Result } from "@hardscapehouse/api-client";
import { formatMoney, formatQty } from "../po/po-money.js";
import { houseDay } from "../po/po-logic.js";
import { InventoryApi, browseAnswer, branchRows, incomingRows, moveRows, onHandRows, pendingRows, pointRows, reservationRows, transitPairs, transferRows, type BrowseAnswer } from "./inventory-api.js";
import { bootstrapConfirm, cancelTransferConfirm, defaultShipETA, expireConfirm, receiveTransferConfirm, refusalWords, releaseConfirm, reorderFieldsFromTyped, seatFor } from "./inventory-logic.js";
import { renderInventory, type InvConfirm, type InvModel, type InvTab, type ReceiveOrder } from "./inventory-views.js";

type Field = (name: string) => string;

const TABS: readonly InvTab[] = ["lookup", "browse", "branch-stock", "pending", "reservations", "transfers", "receive", "replenish"];

export class InventoryDesk {
  readonly #api: InventoryApi;
  readonly #field: Field;
  readonly #repaint: () => void;

  #phase: InvModel["phase"] = "loading";
  #tab: InvTab = "lookup";
  #busy = false;
  #error = "";
  #notice = "";
  #loaded = new Set<string>();
  #catalogue: readonly { id: string; sku: string; name: string }[] = [];
  #branchNames: ReadonlyMap<string, string> = new Map();

  // The lookup.
  #lookupPicked = "";
  #lookupName = "";
  #lookupSearched = false;
  #lookupLoading = false;
  #onHand: readonly InventoryOnHand[] = [];
  #moves: readonly InventoryStockMove[] = [];
  // Browse.
  #browseAnswer: BrowseAnswer | null = null;
  #browseLoading = false;
  // Branch stock.
  #bsPicked = "";
  #bsRows: readonly InventoryBranchStock[] = [];
  #bsLoading = false;
  // Pending.
  #pending: readonly InventoryPendingFulfilment[] = [];
  // Reservations.
  #reservations: readonly InventoryReservation[] = [];
  #policy: InvModel["policy"] = null;
  // Transfers.
  #transfers: readonly InventoryTransfer[] = [];
  #transit: readonly InventoryTransitDaysPair[] = [];
  #shipping: string | null = null;
  #shipEta = "";
  #incoming: readonly InventoryInTransitLine[] = [];
  #incomingName = "";
  // Receive.
  #receiveOrders: readonly ReceiveOrder[] = [];
  #receiveConfirm: ReceiveOrder | null = null;
  // Replenish.
  #points: readonly InventoryReorderPoint[] = [];
  #editingPoint: string | null = null;
  // The one press waiting on its confirm: a move that cannot be taken back asks first.
  #confirm: InvConfirm | null = null;

  // The signed in role's code decides the seat: it picks the words a walled move is named with.
  readonly #role: () => string;

  constructor(api: InventoryApi, host: DeskHost) {
    this.#api = api;
    this.#repaint = host.repaint;
    api.watchZone(() => host.repaint());
    this.#field = host.field;
    this.#role = host.role;
  }

  /** The action key the refusal words answer: the moves with words of their own keep them; the
   * rest name the seat's wall. The gate's body is wordless, so the seat is what says who may. */
  #words(action: string): string {
    if (action === "receive" || action === "count-approve") return action;
    if (seatFor(this.#role()) === "read") return "write";
    if (seatFor(this.#role()) === "work") return "manage";
    return action;
  }

  /** The first load, when the area is opened: the shared picks and the first tab's rows. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#repaint();
    const [branches, products] = await Promise.all([this.#api.branches(), this.#api.products()]);
    if (!branches.ok) return this.#loadFailed(branches);
    if (!products.ok) return this.#loadFailed(products);
    this.#branchNames = new Map(branches.value.map((b) => [b.id, b.name]));
    this.#catalogue = (products.value.items ?? []).map((p) => ({ id: p.id, sku: p.sku, name: p.name }));
    this.#phase = "ready";
    await this.#open("lookup");
  }

  #loadFailed(result: Extract<Result<unknown>, { ok: false }>): void {
    this.#phase = "error";
    this.#error = refusalWords("load", result.error, result.status, undefined, result.code);
    this.#repaint();
  }

  async #reloadShared(): Promise<void> {
    const branches = await this.#api.branches();
    if (branches.ok) this.#branchNames = new Map(branches.value.map((b) => [b.id, b.name]));
  }

  #fail(action: string, result: { ok: false; error: string; status?: number; code?: string }): void {
    this.#notice = "";
    this.#error = refusalWords(this.#words(action), result.error, result.status, undefined, result.code);
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  /** Loads one tab's reads when it is opened (once; the tab's own Refresh re-reads). */
  async #open(tab: InvTab): Promise<void> {
    this.#tab = tab;
    this.#confirm = null;
    this.#clearMessages();
    if (!this.#loaded.has(tab)) {
      await this.#readTab(tab);
      this.#loaded.add(tab);
    }
    this.#repaint();
  }

  async #readTab(tab: InvTab): Promise<void> {
    switch (tab) {
      case "pending": {
        const rows = await this.#api.pending();
        if (rows.ok) this.#pending = pendingRows(rows.value);
        return;
      }
      case "reservations": {
        const [holds, policy] = await Promise.all([this.#api.reservations(), this.#api.allocationPolicy()]);
        if (holds.ok) this.#reservations = reservationRows(holds.value);
        if (policy.ok) this.#policy = this.#policyView(policy.value);
        return;
      }
      case "transfers": {
        const [trfs, transit] = await Promise.all([this.#api.transfers(), this.#api.transitDays()]);
        if (trfs.ok) this.#transfers = transferRows(trfs.value);
        if (transit.ok) this.#transit = transitPairs(transit.value);
        return;
      }
      case "receive": {
        await this.#readReceiveOrders();
        return;
      }
      case "replenish": {
        const points = await this.#api.reorderPoints();
        if (points.ok) this.#points = pointRows(points.value);
        return;
      }
      default:
        return;
    }
  }

  #policyView(policy: InventoryAllocationPolicy): InvModel["policy"] {
    return {
      order_mode: policy.order_mode,
      quote_mode: policy.quote_mode,
      order_ttl_hours: policy.order_ttl_hours,
      quote_ttl_hours: policy.quote_ttl_hours,
      is_default: policy.is_default,
    };
  }

  /** The placed orders with their outstanding lines, the receive tab's list. */
  async #readReceiveOrders(): Promise<void> {
    const orders = await this.#api.orders();
    if (!orders.ok) {
      this.#fail("receive", orders);
      return;
    }
    const placed = (orders.value.items ?? []).filter((o) => o.status === "ORDERED" || o.status === "PARTIAL");
    const details = await Promise.all(placed.map((o) => this.#api.order(o.id)));
    const rows: ReceiveOrder[] = [];
    details.forEach((d, i) => {
      const header = placed[i];
      if (header === undefined || !d.ok) return;
      rows.push({
        id: d.value.id,
        no: d.value.po_no,
        vendorName: d.value.vendor_name,
        status: d.value.status,
        branchName: this.#branchNames.get(d.value.branch_id) ?? "the yard",
        lines: d.value.lines
          .filter((l) => l.qty_outstanding_base > 0)
          .map((l) => ({ id: l.id, sku: l.sku, name: l.description, uom: l.uom, outstanding: l.qty_outstanding_base })),
      });
    });
    this.#receiveOrders = rows;
  }

  // ---- the press -----------------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "inv-reload":
        void this.load();
        return true;
      case "inv-tab": {
        const tab = attr("data-inv-tab") as InvTab;
        if (!TABS.includes(tab)) return true;
        void this.#open(tab);
        return true;
      }
      case "lookup-run":
        void this.#lookup();
        return true;
      case "browse-run":
        void this.#runBrowse();
        return true;
      case "bs-run":
        void this.#branchStock();
        return true;
      case "pending-reload":
        void this.#readTab("pending").then(() => this.#repaint());
        return true;
      case "res-reload":
        void this.#readTab("reservations").then(() => this.#repaint());
        return true;
      case "reserve":
        void this.#reserve();
        return true;
      case "release-reservation":
        this.#askRelease(attr("data-res-id"));
        return true;
      case "expire-reservations":
        this.#ask({ kind: "expire", id: "", ...expireConfirm() });
        return true;
      case "confirm-back":
        this.#confirm = null;
        this.#clearMessages();
        this.#repaint();
        return true;
      case "confirm-go":
        void this.#confirmGo();
        return true;
      case "ship-transfer":
        this.#beginShip(attr("data-transfer-id"));
        return true;
      case "ship-cancel":
        this.#shipping = null;
        this.#shipEta = "";
        this.#clearMessages();
        this.#repaint();
        return true;
      case "confirm-ship":
        void this.#ship(attr("data-transfer-id"));
        return true;
      case "receive-transfer":
        this.#askTransfer("receive-transfer", attr("data-transfer-id"));
        return true;
      case "cancel-transfer":
        this.#askTransfer("cancel-transfer", attr("data-transfer-id"));
        return true;
      case "raise-transfer":
        void this.#raiseTransfer();
        return true;
      case "receive-reload":
        void this.#readReceiveOrders().then(() => this.#repaint());
        return true;
      case "receive-order":
        void this.#askReceive(attr("data-po-id"));
        return true;
      case "receive-back":
        this.#receiveConfirm = null;
        this.#clearMessages();
        this.#repaint();
        return true;
      case "receive-confirm":
        void this.#receive();
        return true;
      case "edit-point":
        this.#editingPoint = attr("data-point-id");
        this.#clearMessages();
        this.#repaint();
        return true;
      case "edit-point-close":
        this.#editingPoint = null;
        this.#repaint();
        return true;
      case "save-point":
        void this.#savePoint();
        return true;
      case "nudge":
        void this.#nudge(attr("data-point-id"));
        return true;
      case "bootstrap-points": {
        const branch = this.#field("bootstrap-branch");
        if (branch === "") {
          this.#error = "Pick the yard to open reorder lines at.";
          this.#repaint();
          return true;
        }
        this.#ask({ kind: "bootstrap", id: branch, ...bootstrapConfirm(this.#branchNames.get(branch) ?? branch) });
        return true;
      }
      default:
        return false;
    }
  }

  #ask(confirm: InvConfirm): void {
    this.#confirm = confirm;
    this.#clearMessages();
    this.#repaint();
  }

  #askTransfer(kind: "receive-transfer" | "cancel-transfer", id: string): void {
    const t = this.#transfers.find((x) => x.id === id);
    if (t === undefined) return;
    const words = kind === "receive-transfer" ? receiveTransferConfirm(t, this.#branchNames.get(t.to_branch_id) ?? "the yard") : cancelTransferConfirm(t);
    this.#ask({ kind, id, ...words });
  }

  #askRelease(id: string): void {
    const r = this.#reservations.find((x) => x.id === id);
    if (r === undefined) return;
    const picked = this.#catalogue.find((c) => c.id === r.product_id);
    this.#ask({
      kind: "release",
      id,
      ...releaseConfirm({ product: picked === undefined ? r.product_id : `${picked.sku} ${picked.name}`, yard: this.#branchNames.get(r.branch_id) ?? r.branch_id, qty: r.qty_base }),
    });
  }

  /** The confirm's go ahead: the held press is made now, and the confirm closes first. */
  async #confirmGo(): Promise<void> {
    const confirm = this.#confirm;
    if (confirm === null || this.#busy) return;
    this.#confirm = null;
    switch (confirm.kind) {
      case "receive-transfer":
        return this.#receiveTransfer(confirm.id);
      case "cancel-transfer":
        return this.#cancelTransfer(confirm.id);
      case "release":
        return this.#release(confirm.id);
      case "expire":
        return this.#expire();
      case "bootstrap":
        return this.#bootstrap(confirm.id);
    }
  }

  /** The lookup: both reads, so the empty state can be about the product and not the request. */
  async #lookup(): Promise<void> {
    const id = this.#field("lookup-product");
    if (id === "" || this.#busy) return;
    this.#lookupLoading = true;
    this.#lookupSearched = true;
    this.#clearMessages();
    this.#repaint();
    const [onHand, moves] = await Promise.all([this.#api.onHand(id), this.#api.moves(id)]);
    if (!onHand.ok) {
      this.#fail("lookup", onHand);
      this.#onHand = [];
      this.#moves = [];
    } else if (!moves.ok) {
      this.#fail("lookup", moves);
      this.#onHand = [];
      this.#moves = [];
    } else {
      // Nothing is kept from the previous product: a failed lookup says nothing about stock.
      this.#onHand = onHandRows(onHand.value);
      this.#moves = moveRows(moves.value);
    }
    const picked = this.#catalogue.find((c) => c.id === id);
    this.#lookupName = picked === undefined ? id : `${picked.sku} ${picked.name}`;
    this.#lookupPicked = id;
    this.#lookupLoading = false;
    this.#repaint();
  }

  async #runBrowse(): Promise<void> {
    this.#browseLoading = true;
    this.#clearMessages();
    this.#repaint();
    const res = await this.#api.browse(this.#field("browse-text"));
    if (!res.ok) {
      this.#fail("browse", res);
      this.#browseAnswer = null;
    } else {
      this.#browseAnswer = browseAnswer(res.value);
    }
    this.#browseLoading = false;
    this.#repaint();
  }

  async #branchStock(): Promise<void> {
    const id = this.#field("bs-product");
    if (id === "" || this.#busy) return;
    this.#bsLoading = true;
    this.#bsPicked = id;
    this.#clearMessages();
    this.#repaint();
    const res = await this.#api.branchStock(id);
    if (!res.ok) {
      this.#fail("branch-stock", res);
      this.#bsRows = [];
    } else {
      this.#bsRows = branchRows(res.value);
    }
    this.#bsLoading = false;
    this.#repaint();
  }

  async #reserve(): Promise<void> {
    const productId = this.#field("res-product");
    const branch = this.#field("res-branch");
    const qtyText = this.#field("res-qty");
    const qty = Number(qtyText);
    if (productId === "" || branch === "" || !Number.isFinite(qty) || qty <= 0) {
      this.#error = "Pick a product and a branch, and write how many to hold.";
      this.#repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const made = await this.#api.reserve({
      product_id: productId,
      branch_id: branch,
      location_id: branch,
      res_type: (this.#field("res-type") || "ORDER") as "ORDER" | "QUOTE",
      qty_base: qty,
      source_type: "manual",
      source_ref_id: this.#field("res-ref"),
      reason: "",
    });
    if (!made.ok) {
      this.#fail("reserve", made);
    } else if (!made.value.applied) {
      this.#notice = "The branch's policy grades this hold as no hold, so nothing was written.";
    } else {
      const picked = this.#catalogue.find((c) => c.id === productId);
      this.#notice = `Held ${formatQty(made.value.reservation.qty_base, "")} ${picked?.sku ?? ""} at ${this.#branchNames.get(branch) ?? branch}. Only a hard hold subtracts from available.`;
    }
    this.#busy = false;
    await this.#readTab("reservations");
    this.#repaint();
  }

  async #release(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const released = await this.#api.release({ reservation_id: id, source_type: "", source_ref_id: "", status: "RELEASED" });
    if (!released.ok) {
      this.#fail("release", released);
    } else {
      this.#notice = released.value.released === 1 ? "The hold is released; the stock is back on the floor." : `Released ${released.value.released} holds.`;
    }
    this.#busy = false;
    await this.#readTab("reservations");
    this.#repaint();
  }

  async #expire(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const expired = await this.#api.expireReservations();
    if (!expired.ok) {
      this.#fail("expire", expired);
    } else {
      this.#notice = expired.value.expired === 0 ? "The sweep ran: no reservation had expired." : `Expired ${expired.value.expired} ${expired.value.expired === 1 ? "hold" : "holds"}.`;
    }
    this.#busy = false;
    await this.#readTab("reservations");
    this.#repaint();
  }

  #beginShip(transferId: string): void {
    const t = this.#transfers.find((x) => x.id === transferId);
    if (t === undefined) return;
    this.#confirm = null;
    this.#shipping = transferId;
    this.#shipEta = defaultShipETA(this.#transit, t.from_branch_id, t.to_branch_id, houseDay());
    this.#clearMessages();
    this.#repaint();
  }

  async #reloadTransfers(): Promise<void> {
    const [trfs, transit] = await Promise.all([this.#api.transfers(), this.#api.transitDays()]);
    if (trfs.ok) this.#transfers = transferRows(trfs.value);
    if (transit.ok) this.#transit = transitPairs(transit.value);
    // The truck's contents, read against the first product riding in transit.
    const riding = this.#transfers
      .filter((t) => t.status === "IN_TRANSIT")
      .flatMap((t) => t.lines.map((l) => ({ product: l.product_id, name: l.product_name, moved: l.qty_shipped_base - l.qty_received_base })))
      .find((l) => l.moved > 0);
    if (riding !== undefined) {
      const incoming = await this.#api.incomingTransfers(riding.product);
      if (incoming.ok) {
        this.#incoming = incomingRows(incoming.value);
        this.#incomingName = riding.name;
      }
    } else {
      this.#incoming = [];
    }
  }

  async #ship(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    const typed = this.#field("ship-eta");
    this.#repaint();
    const shipped = await this.#api.shipTransfer(id, typed);
    if (!shipped.ok) {
      this.#fail("ship", shipped);
    } else {
      const t = shipped.value;
      this.#notice = t.eta !== "" ? `${t.transfer_no} shipped, expected ${t.eta}.` : `${t.transfer_no} shipped, no promised arrival.`;
    }
    this.#shipping = null;
    this.#shipEta = "";
    this.#busy = false;
    await this.#reloadTransfers();
    this.#repaint();
  }

  async #receiveTransfer(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const received = await this.#api.receiveTransfer(id);
    if (!received.ok) {
      this.#fail("receive-truck", received);
    } else {
      this.#notice = `${received.value.transfer_no} received at ${this.#branchNames.get(received.value.to_branch_id) ?? "the yard"}; the stock is on the floor.`;
    }
    this.#busy = false;
    await this.#reloadTransfers();
    this.#repaint();
  }

  async #cancelTransfer(id: string): Promise<void> {
    if (id === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const cancelled = await this.#api.cancelTransfer(id);
    if (!cancelled.ok) {
      this.#fail("cancel", cancelled);
    } else {
      this.#notice = `${cancelled.value.transfer_no} cancelled. Nothing moved.`;
    }
    this.#busy = false;
    await this.#reloadTransfers();
    this.#repaint();
  }

  async #raiseTransfer(): Promise<void> {
    const from = this.#field("tr-from");
    const to = this.#field("tr-to");
    const productId = this.#field("tr-product");
    const qty = Number(this.#field("tr-qty"));
    if (from === "" || to === "" || productId === "" || !Number.isFinite(qty) || qty <= 0) {
      this.#error = "Pick two different yards, a product, and how many to move.";
      this.#repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const made = await this.#api.createTransfer({ from_branch_id: from, to_branch_id: to, notes: "raised from the transfers surface", lines: [{ product_id: productId, qty_base: qty }] });
    if (!made.ok) {
      this.#fail("transfer", made);
    } else {
      this.#notice = `${made.value.transfer_no} raised as a draft. Nothing has moved yet; shipping loads the truck.`;
    }
    this.#busy = false;
    await this.#reloadTransfers();
    this.#repaint();
  }

  /** The receive confirm: read the order's outstanding lines, then ask. */
  async #askReceive(poId: string): Promise<void> {
    if (poId === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const detail = await this.#api.order(poId);
    if (!detail.ok) {
      this.#fail("receive", detail);
      this.#busy = false;
      this.#repaint();
      return;
    }
    const order: ReceiveOrder = {
      id: detail.value.id,
      no: detail.value.po_no,
      vendorName: detail.value.vendor_name,
      status: detail.value.status,
      branchName: this.#branchNames.get(detail.value.branch_id) ?? "the yard",
      lines: detail.value.lines
        .filter((l) => l.qty_outstanding_base > 0)
        .map((l) => ({ id: l.id, sku: l.sku, name: l.description, uom: l.uom, outstanding: l.qty_outstanding_base })),
    };
    this.#receiveConfirm = order;
    this.#busy = false;
    this.#repaint();
  }

  async #receive(): Promise<void> {
    const order = this.#receiveConfirm;
    if (order === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    // No explicit per-line quantities: the whole outstanding, which is what the confirm named.
    const receipt = await this.#api.receivePO(order.id, []);
    if (!receipt.ok) {
      this.#fail("receive", receipt);
      this.#busy = false;
      this.#repaint();
      return;
    }
    this.#receiveConfirm = null;
    this.#notice = `${receipt.value.po_no} received: ${receipt.value.lines_received} ${receipt.value.lines_received === 1 ? "line" : "lines"}, ${formatMoney(receipt.value.value_cad_cents)} to the goods-not-invoiced account. The stock is on the floor.`;
    this.#busy = false;
    await this.#readReceiveOrders();
    await this.#reloadShared();
    this.#repaint();
  }

  async #savePoint(): Promise<void> {
    const row = this.#points.find((r) => r.product_id === this.#editingPoint);
    if (row === undefined || this.#busy) return;
    const check = reorderFieldsFromTyped(this.#field("point"), this.#field("reorder-qty"));
    if (!check.ok) {
      this.#error = check.words;
      this.#repaint();
      return;
    }
    const { point, reorderQty } = check;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const saved = await this.#api.putReorderPoint({
      ...row,
      reorder_point_base: point,
      reorder_qty_base: reorderQty,
      is_active: true,
    });
    if (!saved.ok) {
      this.#fail("point", saved);
    } else {
      this.#editingPoint = null;
      const branch = this.#branchNames.get(saved.value.branch_id) ?? saved.value.branch_id;
      this.#notice = `${saved.value.product_name} at ${branch} now carries a reorder point of ${formatQty(saved.value.reorder_point_base, "")} and orders ${formatQty(saved.value.reorder_qty_base, "")} at a time.`;
    }
    this.#busy = false;
    await this.#readTab("replenish");
    this.#repaint();
  }

  async #nudge(productId: string): Promise<void> {
    const row = this.#points.find((r) => r.product_id === productId);
    if (row === undefined || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const sent = await this.#api.nudge(productId, row.branch_id);
    if (!sent.ok) {
      this.#fail("nudge", sent);
    } else if (sent.value.posted) {
      this.#notice = "Nudge sent to procurement: one message naming the product, the branch and the position.";
    } else {
      this.#notice = "A nudge for this product and branch was already sent this cycle, so nothing was posted twice.";
    }
    this.#busy = false;
    this.#repaint();
  }

  async #bootstrap(branch: string): Promise<void> {
    if (branch === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#repaint();
    const made = await this.#api.bootstrap(branch);
    if (!made.ok) {
      this.#fail("bootstrap", made);
    } else {
      this.#notice = `${made.value.created} reorder point ${made.value.created === 1 ? "row" : "rows"} opened, at half of today's on hand. A line a person tuned is never overwritten.`;
    }
    this.#busy = false;
    await this.#readTab("replenish");
    this.#repaint();
  }

  /** The starting field values, set once per state after the render. */
  #seededPoint: string | null = null;

  seed(set: (name: string, value: string) => void): void {
    if (this.#shipping !== null) set("ship-eta", this.#shipEta);
    const row = this.#points.find((r) => r.product_id === this.#editingPoint);
    if (row !== undefined) {
      const stamp = `point:${row.product_id}:${row.reorder_point_base}:${row.reorder_qty_base}`;
      if (this.#seededPoint !== stamp) {
        set("point", String(row.reorder_point_base));
        set("reorder-qty", String(row.reorder_qty_base));
        this.#seededPoint = stamp;
      }
    }
  }

  render(): TemplateResult {
    const model: InvModel = {
      phase: this.#phase,
      tab: this.#tab,
      busy: this.#busy,
      error: this.#error,
      notice: this.#notice,
      today: houseDay(),
      catalogue: this.#catalogue,
      branchNames: this.#branchNames,
      lookupPicked: this.#lookupPicked,
      lookupName: this.#lookupName,
      lookupSearched: this.#lookupSearched,
      lookupLoading: this.#lookupLoading,
      onHand: this.#onHand,
      moves: this.#moves,
      browse: this.#browseAnswer,
      browseLoading: this.#browseLoading,
      bsPicked: this.#bsPicked,
      bsRows: this.#bsRows,
      bsLoading: this.#bsLoading,
      pending: this.#pending,
      reservations: this.#reservations,
      policy: this.#policy,
      transfers: this.#transfers,
      transit: this.#transit,
      shipping: this.#shipping ?? "",
      shipEta: this.#shipEta,
      incoming: this.#incoming,
      incomingName: this.#incomingName,
      receiveOrders: this.#receiveOrders,
      receiveConfirm: this.#receiveConfirm === null ? null : { order: this.#receiveConfirm },
      points: this.#points,
      editingPoint: this.#editingPoint,
      confirm: this.#confirm,
    };
    return renderInventory(model);
  }
}
