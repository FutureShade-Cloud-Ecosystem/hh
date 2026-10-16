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
import type { InventoryShortfallRow, Product, PurchasingPO, PurchasingPODetail, Result } from "@hardscapehouse/api-client";
import type { AreaDesk } from "../purch/area.js";
import { PoApi, type DeliveryMethod } from "./po-api.js";
import { draftOf, landedWords, poRows, refusalWords, shortfallRows, validateExpectedDate } from "./po-logic.js";
import { costCentsFromTyped, formatMoney, formatQty } from "./po-money.js";
import { renderDesk, type DeskModel, type DraftModel } from "./po-views.js";

export interface DeskHost {
  /** Asks the root to repaint. */
  readonly repaint: () => void;
  /** The text of a form field the screen carries, or "" when it is not on screen. */
  readonly field: (name: string) => string;
}

const DELIVERY: readonly DeliveryMethod[] = ["PICKUP", "DELIVERY", "UNKNOWN"];

export class PoDesk implements AreaDesk {
  readonly #api: PoApi;
  readonly #host: DeskHost;

  #phase: DeskModel["phase"] = "loading";
  #shortfall: readonly InventoryShortfallRow[] = [];
  #orders: readonly PurchasingPO[] = [];
  #picked: string | null = null;
  #draft: DraftModel | null = null;
  #placedId: string | null = null;
  #busy = false;
  #error = "";
  // Which control the refusal answers: the panel's own error note, or the line editor's Save
  // (the editor's refusal shows beside its Save, a screen above where the panel note sits at 390).
  #errorAt: "panel" | "editor" = "panel";
  #notice = "";
  // The draft whose starting field values the screen has already been given.
  #seeded: string | null = null;
  // The delivery the draft's segmented control holds, the branch names, the vendor's codes and the
  // part received orders' landed words: reads the core answered, kept for the screen.
  #method = "UNKNOWN";
  #expectedText = "";
  #branchNames: ReadonlyMap<string, string> = new Map();
  #vendorCodes: ReadonlyMap<string, string> = new Map();
  #landed: ReadonlyMap<string, string> = new Map();
  // The catalogue the add a line picker lists (read once; a refusal leaves the picker empty and
  // its Add line closed), and the line editor's state: the line being corrected, the add row.
  #products: readonly Product[] = [];
  #editing: string | null = null;
  #adding = false;

  constructor(api: PoApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
  }

  // ---- loads ----------------------------------------------------------------------------------

  /** The first read of the desk: the worklist, the register and the catalogue the picker lists. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const [shortfall, orders, branches, products] = await Promise.all([this.#api.shortfall(), this.#api.orders(), this.#api.branches(), this.#api.products()]);
    if (!shortfall.ok) return this.#loadFailed(shortfall);
    if (!orders.ok) return this.#loadFailed(orders);
    this.#shortfall = shortfallRows(shortfall.value);
    this.#orders = poRows(orders.value);
    // A branch name is a nicety: without it the screen reads the branch code.
    if (branches.ok) this.#branchNames = new Map(branches.value.map((b) => [b.id, b.name]));
    // The catalogue is the add a line picker's list: a refusal leaves it empty and honest.
    if (products.ok) this.#products = products.value.items ?? [];
    await this.#readLanded();
    // The yard's gap is what the manager came for: the worst short line that has no draft is already picked.
    const first = this.#shortfall.find((r) => draftOf(r) === null && r.projected_base < 0);
    this.#picked = first?.product_id ?? null;
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
    await this.#readLanded();
    if (this.#picked !== null && !this.#shortfall.some((r) => r.product_id === this.#picked && draftOf(r) === null)) this.#picked = null;
  }

  /** How much of each part received order has landed, from the order's own lines. */
  async #readLanded(): Promise<void> {
    const partial = this.#orders.filter((o) => o.status === "PARTIAL");
    const details = await Promise.all(partial.map((o) => this.#api.order(o.id)));
    const landed = new Map<string, string>();
    details.forEach((d, i) => {
      const po = partial[i];
      if (po !== undefined && d.ok) {
        const words = landedWords(d.value.lines);
        if (words !== "") landed.set(po.id, words);
      }
    });
    this.#landed = landed;
  }

  /** The vendor's own codes for the draft's lines, one read; a refusal leaves the column saying none on file. */
  async #readVendorCodes(vendorId: string): Promise<void> {
    const codes = await this.#api.vendorCodes(vendorId);
    if (!codes.ok) return;
    this.#vendorCodes = new Map((codes.value.items ?? []).map((c) => [c.product_id, c.vendor_sku]));
  }

  async #openDraft(poId: string): Promise<void> {
    if (poId === "" || this.#busy) return;
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const detail = await this.#api.order(poId);
    if (!detail.ok) {
      this.#fail("order", detail);
    } else {
      await this.#readVendorCodes(detail.value.vendor_id);
      const first = detail.value.lines[0];
      const row = first === undefined ? null : (this.#shortfall.find((r) => r.product_id === first.product_id) ?? null);
      this.#draft = { po: detail.value, row, existingDraft: null };
      this.#method = detail.value.delivery_method ?? "UNKNOWN";
      this.#expectedText = (detail.value.expected_at ?? "").slice(0, 10);
      this.#seeded = null;
      this.#editing = null;
      this.#adding = false;
    }
    this.#busy = false;
    this.#host.repaint();
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
      case "open-draft":
        void this.#openDraft(attr("data-po-id"));
        return true;
      case "method": {
        // Pressing the lit way again puts the order back to not set.
        const chosen = attr("data-method");
        this.#method = this.#method === chosen ? "UNKNOWN" : chosen;
        this.#host.repaint();
        return true;
      }
      case "remove-line":
        void this.#removeLine(attr("data-line-id"));
        return true;
      case "edit-line": {
        this.#editing = attr("data-line-id") || null;
        this.#adding = false;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      }
      case "edit-line-close":
        this.#editing = null;
        this.#host.repaint();
        return true;
      case "save-line":
        void this.#saveLine(attr("data-line-id"));
        return true;
      case "add-line":
        this.#adding = true;
        this.#editing = null;
        this.#clearMessages();
        this.#host.repaint();
        return true;
      case "add-line-close":
        this.#adding = false;
        this.#host.repaint();
        return true;
      case "add-line-confirm":
        void this.#addLine();
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
    this.#errorAt = "panel";
    this.#notice = "";
  }

  #fail(action: string, result: Extract<Result<unknown>, { ok: false }>, at: "panel" | "editor" = "panel"): void {
    this.#notice = "";
    this.#error = refusalWords(action, result.error, result.status);
    this.#errorAt = at;
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
    await this.#readVendorCodes(detail.value.vendor_id);
    this.#picked = null;
    this.#seeded = null;
    this.#editing = null;
    this.#adding = false;
    this.#method = detail.value.delivery_method ?? "UNKNOWN";
    this.#expectedText = (detail.value.expected_at ?? "").slice(0, 10);
    await this.#reload();
    // The row as the core now reads it: it carries the draft that does not cover it. When the core
    // named a draft that was already open before this press, the panel says so in the row's words.
    const existing = made.value.existing_draft_po_id;
    const existingDraft = existing === undefined || existing === "" ? null : { id: existing, no: made.value.existing_draft_po_no ?? "" };
    this.#draft = { po: detail.value, row: this.#shortfall.find((r) => r.product_id === row.product_id) ?? row, existingDraft };
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
    const method = DELIVERY.find((d) => d === this.#method);
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
    if (removed.ok) {
      this.#draft = { ...draft, po: removed.value };
      if (this.#editing === lineId) this.#editing = null;
    } else {
      this.#fail("order", removed);
    }
    this.#busy = false;
    this.#host.repaint();
  }

  /** Corrects one line's quantity or cost on the draft: only the fields that changed are sent. */
  async #saveLine(lineId: string): Promise<void> {
    const draft = this.#draft;
    const line = draft?.po.lines.find((l) => l.id === lineId);
    if (draft === null || line === undefined || this.#busy) return;
    const qty = Number(this.#host.field("qty"));
    const costText = this.#host.field("cost");
    const body: { qty_ordered_base?: number; unit_cost_cents?: number } = {};
    if (Number.isFinite(qty) && qty !== line.qty_ordered_base) body.qty_ordered_base = qty;
    if (costText !== "") {
      // Through the decimal string, never a float multiply: 0.1 and 19.99 land as the exact cents.
      const check = costCentsFromTyped(costText);
      if (!check.ok) {
        this.#error = check.words;
        this.#errorAt = "editor";
        this.#host.repaint();
        return;
      }
      if (check.cents !== line.unit_cost_cents) body.unit_cost_cents = check.cents;
    }
    if (Object.keys(body).length === 0) {
      this.#error = "Nothing changed on this line yet.";
      this.#errorAt = "editor";
      this.#host.repaint();
      return;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const saved = await this.#api.updateLine(draft.po.id, lineId, body);
    if (saved.ok) {
      this.#draft = { ...draft, po: saved.value };
      this.#editing = null;
    } else {
      this.#fail("order", saved, "editor");
    }
    this.#busy = false;
    this.#host.repaint();
  }

  /** Adds a line from the catalogue: the picker's text is resolved back to the product it listed. */
  async #addLine(): Promise<void> {
    const draft = this.#draft;
    if (draft === null || this.#busy) return;
    const product = this.#products.find((p) => `${p.sku} ${p.name}` === this.#host.field("add-product"));
    const qty = Number(this.#host.field("add-qty"));
    const costText = this.#host.field("add-cost");
    if (product === undefined || !Number.isFinite(qty)) {
      this.#error = "Pick a product and write how many to order.";
      this.#host.repaint();
      return;
    }
    const body: { product_id: string; qty_ordered_base: number; unit_cost_cents?: number } = { product_id: product.id, qty_ordered_base: qty };
    if (costText !== "") {
      // Through the decimal string, never a float multiply: 0.1 and 19.99 land as the exact cents.
      const check = costCentsFromTyped(costText);
      if (!check.ok) {
        this.#error = check.words;
        this.#host.repaint();
        return;
      }
      body.unit_cost_cents = check.cents;
    }
    this.#busy = true;
    this.#clearMessages();
    this.#host.repaint();
    const added = await this.#api.addLine(draft.po.id, body);
    if (added.ok) {
      this.#draft = { ...draft, po: added.value };
      this.#adding = false;
    } else {
      this.#fail("order", added);
    }
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
    const what = po.lines.map((l) => `${formatQty(l.qty_ordered_base, l.uom)} ${l.description}`).join(", ");
    const promised = ready.expected_at === undefined || ready.expected_at === "" ? "no promised date yet" : `promised ${ready.expected_at.slice(0, 10)}`;
    this.#notice = `Placed: ${po.po_no} to ${po.vendor_name}, ${what}, ${formatMoney(po.subtotal_cents)}.\nMoved to Ordered, ${promised}. It shows under Incoming. The yard team receives it in Yard Pick and Receive.`;
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

  /** The draft's date field changed: the words under it follow what was typed. */
  typed(name: string, text: string): void {
    if (name !== "expected") return;
    this.#expectedText = text;
    this.#host.repaint();
  }

  // ---- the template ---------------------------------------------------------------------------

  /**
   * After a render: the draft's starting field values, set once per draft, and the line editor's
   * starting figures, set whenever the edited line or the server's answer moves. The position gate
   * allows no bound value on an input, so the root hands this the field setter.
   */
  seed(set: (name: string, value: string) => void): void {
    const draft = this.#draft;
    if (draft === null) return;
    if (this.#seeded !== draft.po.id) {
      set("expected", (draft.po.expected_at ?? "").slice(0, 10));
      this.#seeded = draft.po.id;
    }
    const line = this.#editing === null ? undefined : draft.po.lines.find((l) => l.id === this.#editing);
    if (line !== undefined && this.#seededLine !== `${line.id}:${line.qty_ordered_base}:${line.unit_cost_cents}`) {
      set("qty", String(line.qty_ordered_base));
      set("cost", ((line.unit_cost_cents ?? 0) / 100).toFixed(2));
      this.#seededLine = `${line.id}:${line.qty_ordered_base}:${line.unit_cost_cents}`;
    }
  }

  /** The edited line whose figures the screen has already been given. */
  #seededLine: string | null = null;

  render(): TemplateResult {
    return renderDesk({
      phase: this.#phase,
      shortfall: this.#shortfall,
      orders: this.#orders,
      picked: this.#picked,
      draft: this.#draft,
      placedId: this.#placedId,
      busy: this.#busy,
      error: this.#phase === "error" || (this.#error !== "" && this.#errorAt === "panel") ? this.#error : "",
      notice: this.#notice,
      method: this.#method,
      expectedText: this.#expectedText,
      branchNames: this.#branchNames,
      vendorCodes: this.#vendorCodes,
      landed: this.#landed,
      products: this.#products,
      editing: this.#editing,
      adding: this.#adding,
      editorError: this.#error !== "" && this.#errorAt === "editor" && this.#editing !== null ? this.#error : "",
      today: new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date()),
    });
  }
}
