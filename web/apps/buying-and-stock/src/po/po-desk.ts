// The procurement desk's controller: the state the screens draw from, the
// loads, and what each press does. It is not an element; the root element owns
// it, hands it a way to repaint and a way to read the form fields, and asks it
// for the template.
//
// Every write is held to the server: a draft exists only when the core made it,
// an order is placed only when the core answers ORDERED, and the screen then
// shows what the core now says (the register it lists, the worklist it
// grades), never what the app expected. A refusal reads in words (po-logic.ts),
// a walled move in words naming who may do it. Nothing is stored.
import type { TemplateResult } from "lit";
import type { InventoryShortfallRow, PurchasingPO, PurchasingPODetail, Result } from "@hardscapehouse/api-client";
import { PoApi, type DeliveryMethod } from "./po-api.js";
import { incomingLines, poRows, refusalWords, shortfallRows, validateExpectedDate } from "./po-logic.js";
import { formatMoney, formatQty } from "./po-money.js";
import { incomingWords, renderDesk, type DeskModel, type DraftModel, type Tab } from "./po-views.js";

export interface DeskHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string) => string;
}

const DELIVERY: readonly DeliveryMethod[] = ["PICKUP", "DELIVERY", "UNKNOWN"];

export class PoDesk {
  readonly #api: PoApi;
  readonly #host: DeskHost;

  #phase: DeskModel["phase"] = "loading";
  #tab: Tab = "orders";
  #shortfall: readonly InventoryShortfallRow[] = [];
  #orders: readonly PurchasingPO[] = [];
  #picked: string | null = null;
  #draft: DraftModel | null = null;
  #placedId: string | null = null;
  #busy = false;
  #error = "";
  #notice = "";
  // The draft whose starting field values the screen has already been given.
  #seeded: string | null = null;

  constructor(api: PoApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
  }

  // ---- loads ----------------------------------------------------------------------------------

  /** The first read of the desk: the worklist and the register. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [shortfall, orders] = await Promise.all([this.#api.shortfall(), this.#api.orders()]);
    if (!shortfall.ok) return this.#loadFailed(shortfall);
    if (!orders.ok) return this.#loadFailed(orders);
    this.#shortfall = shortfallRows(shortfall.value);
    this.#orders = poRows(orders.value);
    this.#phase = "ready";
    this.#host.repaint();
  }

  #loadFailed(result: Extract<Result<unknown>, { ok: false }>): void {
    this.#phase = "error";
    this.#error = refusalWords("load", result.error, result.status);
    this.#host.repaint();
  }

  /** The book after a write: the worklist and the register, quietly. */
  async #reload(): Promise<void> {
    const [shortfall, orders] = await Promise.all([this.#api.shortfall(), this.#api.orders()]);
    if (shortfall.ok) this.#shortfall = shortfallRows(shortfall.value);
    if (orders.ok) this.#orders = poRows(orders.value);
  }

  // ---- the press ------------------------------------------------------------------------------

  /** Acts on a control carrying data-action. Returns true when the press was this slice's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    const attr = (name: string): string => control.getAttribute(name) ?? "";
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "refresh":
        void this.#reload().then(() => this.#host.repaint());
        return true;
      case "tab":
        this.#tab = attr("data-tab-id") === "incoming" ? "incoming" : "orders";
        this.#host.repaint();
        return true;
      case "pick": {
        const id = attr("data-product-id");
        this.#picked = this.#picked === id ? null : id;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      }
      case "draft":
        void this.#draftFromShortfall();
        return true;
      case "remove-line":
        void this.#removeLine(attr("data-line-id"));
        return true;
      case "save-draft":
        void this.#saveDraft();
        return true;
      case "place":
        void this.#place();
        return true;
      case "cancel-draft":
        void this.#cancelDraft();
        return true;
      default:
        return false;
    }
  }

  #clearMessages(): void {
    this.#error = "";
    this.#notice = "";
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>): void {
    this.#notice = "";
    this.#error = refusalWords(action, result.error, result.status);
  }

  async #draftFromShortfall(): Promise<void> {
    const row = this.#shortfall.find((r) => r.product_id === this.#picked);
    if (row === undefined || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const made = await this.#api.draftFromShortfall(row.product_id, row.branch_id);
    if (!made.ok) {
      this.#fail("order", made);
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    const detail = await this.#api.order(made.value.po_id);
    if (!detail.ok) {
      // The core made the draft but it could not be read back: say so, and show it in the register.
      this.#fail("order", detail);
      await this.#reload();
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    this.#draft = { po: detail.value, row };
    this.#picked = null;
    this.#seeded = null;
    await this.#reload();
    this.#busy = false;
    this.#host.repaint();
  }

  /** The promised date and the delivery, sent when they differ from what the draft already carries. */
  async #writeFields(po: PurchasingPODetail): Promise<PurchasingPODetail | null> {
    let current = po;
    const typed = this.#host.field("expected");
    if (typed !== "" && typed !== (current.expected_at ?? "").slice(0, 10)) {
      const check = validateExpectedDate(typed);
      if (!check.ok) {
        this.#error = check.words;
        return null;
      }
      const set = await this.#api.setExpected(current.id, check.date);
      if (!set.ok) {
        this.#fail("order", set);
        return null;
      }
      current = set.value;
    }
    const chosen = this.#host.field("delivery");
    const method = DELIVERY.find((d) => d === chosen);
    if (method !== undefined && method !== (current.delivery_method ?? "UNKNOWN")) {
      const set = await this.#api.setDelivery(current.id, method);
      if (!set.ok) {
        this.#fail("order", set);
        return null;
      }
      current = set.value;
    }
    return current;
  }

  async #saveDraft(): Promise<void> {
    const draft = this.#draft;
    if (draft === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const saved = await this.#writeFields(draft.po);
    if (saved !== null) {
      this.#draft = null;
      this.#notice = `Draft ${saved.po_no} saved. It stays in the register until it is placed.`;
      await this.#reload();
    }
    this.#busy = false;
    this.#host.repaint();
  }

  async #removeLine(lineId: string): Promise<void> {
    const draft = this.#draft;
    if (draft === null || lineId === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const removed = await this.#api.removeLine(draft.po.id, lineId);
    if (removed.ok) this.#draft = { ...draft, po: removed.value };
    else this.#fail("order", removed);
    this.#busy = false;
    this.#host.repaint();
  }

  async #place(): Promise<void> {
    const draft = this.#draft;
    if (draft === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const ready = await this.#writeFields(draft.po);
    if (ready === null) {
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    const placed = await this.#api.place(ready.id);
    if (!placed.ok) {
      this.#draft = { ...draft, po: ready };
      this.#fail("order", placed);
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    const po = placed.value;
    this.#draft = null;
    this.#placedId = po.id;
    await this.#reload();
    // What is on order for the product just ordered, from the core's own incoming read.
    const first = po.lines[0];
    let onOrder = "";
    if (first !== undefined) {
      const lines = await this.#api.incomingFor(first.product_id);
      if (lines.ok) onOrder = incomingWords(incomingLines(lines.value));
    }
    const what = po.lines.map((l) => `${formatQty(l.qty_ordered_base, l.uom)} ${l.description}`).join(", ");
    this.#notice = `Placed: ${po.po_no} to ${po.vendor_name}, ${what}, ${formatMoney(po.subtotal_cents)}. It shows under Incoming${onOrder === "" ? "" : ` (on order now: ${onOrder})`}. The yard team receives it in Yard Pick and Receive.`;
    this.#busy = false;
    this.#host.repaint();
  }

  async #cancelDraft(): Promise<void> {
    const draft = this.#draft;
    if (draft === null || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const cancelled = await this.#api.cancel(draft.po.id);
    if (cancelled.ok) {
      this.#draft = null;
      this.#notice = `Draft ${cancelled.value.po_no} cancelled. The line is back on the worklist if it is still short.`;
      await this.#reload();
    } else {
      this.#fail("order", cancelled);
    }
    this.#busy = false;
    this.#host.repaint();
  }

  // ---- the template ---------------------------------------------------------------------------

  /**
   * After a render: the draft's starting field values, set once per draft. The position gate
   * allows no bound value on an input, so the root hands this the field setter.
   */
  seed(set: (name: string, value: string) => void): void {
    const draft = this.#draft;
    if (draft === null || this.#seeded === draft.po.id) return;
    set("expected", (draft.po.expected_at ?? "").slice(0, 10));
    set("delivery", draft.po.delivery_method ?? "UNKNOWN");
    this.#seeded = draft.po.id;
  }

  render(): TemplateResult {
    return renderDesk({
      phase: this.#phase,
      tab: this.#tab,
      shortfall: this.#shortfall,
      orders: this.#orders,
      picked: this.#picked,
      draft: this.#draft,
      placedId: this.#placedId,
      busy: this.#busy,
      error: this.#phase === "error" || this.#error !== "" ? this.#error : "",
      notice: this.#notice,
    });
  }
}
