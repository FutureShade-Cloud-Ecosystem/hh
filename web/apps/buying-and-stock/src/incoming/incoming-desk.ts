// The Incoming area's controller: the state the screen draws from, the load,
// and what each press does. It is not an element; the root element owns it,
// hands it a way to repaint, and asks it for the template. Nothing is stored.
//
// The reads: the PO register, then each placed order's own detail, one row per
// line that has not landed. Any read that is refused fails the screen in words:
// a quietly skipped order would read as "not coming", and the yard would miss
// the truck it names.
import type { PurchasingPO, Result } from "@hardscapehouse/api-client";
import type { TemplateResult } from "lit";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { type PoApi } from "../po/po-api.js";
import { houseDay, isPlaced, poRows, refusalWords, whenWords } from "../po/po-logic.js";
import { arrivalRows, sortByPromise, type ArrivalRow } from "./incoming-logic.js";
import { renderIncoming, renderSalesIncoming, type IncomingModel } from "./incoming-views.js";

export class IncomingDesk implements AreaDesk {
  readonly #api: PoApi;
  readonly #host: DeskHost;

  #phase: IncomingModel["phase"] = "loading";
  #rows: readonly ArrivalRow[] = [];
  #busy = false;
  #error = "";

  constructor(api: PoApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
    api.watchZone(() => host.repaint());
  }

  /** The first read of the area, run when it is first shown. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    await this.#read();
    this.#phase = "ready";
    this.#host.repaint();
  }

  async #read(): Promise<void> {
    const orders = await this.#api.orders();
    if (!orders.ok) return this.#fail(orders);
    const placed = poRows(orders.value).filter((o) => isPlaced(o.status));
    // Bound the per-PO reads: a branch with many open POs would otherwise fire one
    // request per order at once. The helper keeps the answer order, runs at most
    // INCOMING_DETAIL_CONCURRENCY at a time, and never fires a request for an empty
    // input list.
    const details = await bounded(INCOMING_DETAIL_CONCURRENCY, placed, (o) => this.#api.order(o.id));
    const rows: ArrivalRow[] = [];
    for (let i = 0; i < placed.length; i += 1) {
      const po = placed[i]!;
      const detail = details[i]!;
      if (!detail.ok) return this.#fail(detail);
      rows.push(...arrivalRows(po, detail.value));
    }
    this.#rows = sortByPromise(rows);
    this.#error = "";
  }

  #fail(result: Extract<Result<unknown>, { ok: false }>): void {
    this.#error = refusalWords("load", result.error, result.status);
  }

  /** Acts on a control carrying data-action. Returns true when the press was this area's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === "refresh") {
      void this.#refresh();
      return true;
    }
    if (action === "reload") {
      void this.load();
      return true;
    }
    return false;
  }

  async #refresh(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.#host.repaint();
    await this.#read();
    this.#busy = false;
    this.#host.repaint();
  }

  render(): TemplateResult {
    return renderIncoming({
      phase: this.#phase,
      rows: this.#rows,
      busy: this.#busy,
      error: this.#error,
      today: houseDay(),
    });
  }
}

/**
 * The cap on per-PO detail reads fired at once from the Incoming desk's load. A branch with many
 * open orders would otherwise dial one request per order at once; six keeps the desk below the
 * page's own bound (the staff client's one-flight write limit) and the server's own hover cap.
 */
const INCOMING_DETAIL_CONCURRENCY = 6;

/**
 * Runs `work` over `items` with at most `limit` calls in flight at a time. Results are returned in
 * the source order. A limit greater than the input length caps at the input length; an empty input
 * answers an empty array without firing any work. Used by IncomingDesk.#read to keep the placed
 * orders' detail reads bounded.
 */
export async function bounded<T, R>(limit: number, items: readonly T[], work: (item: T) => Promise<R>): Promise<R[]> {
  if (items.length === 0) return [];
  const out: R[] = new Array(items.length);
  let next = 0;
  const cap = Math.max(1, Math.min(limit, items.length));
  const workers = Array.from({ length: cap }, async () => {
    while (true) {
      const i = next++;
      if (i >= items.length) return;
      out[i] = await work(items[i]!);
    }
  });
  await Promise.all(workers);
  return out;
}

// The sales seat's Incoming area (change 5, board bs-stock-lookup-sales-search-a-product's second
// pill): what is on order, read only. Ruling R1 gives Sales no buying actions, so this desk offers
// the arrivals and nothing else: the register read the buyers' screen draws its own Incoming from,
// the placed orders with their promised dates, refreshed when the seat asks.
export class SalesIncomingDesk implements AreaDesk {
  readonly #api: PoApi;
  readonly #host: DeskHost;

  #phase: "loading" | "ready" = "loading";
  #error = "";
  #orders: readonly PurchasingPO[] = [];
  #busy = false;

  constructor(api: PoApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
    api.watchZone(() => host.repaint());
  }

  /** The first read of the area: the register, of which the placed orders are the arrivals. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const orders = await this.#api.orders();
    if (!orders.ok) {
      this.#error = orders.error.replace(/^[a-z_]+: /, "");
      this.#phase = "ready";
      this.#host.repaint();
      return;
    }
    this.#orders = poRows(orders.value).filter((o) => isPlaced(o.status));
    this.#error = "";
    this.#phase = "ready";
    this.#host.repaint();
  }

  /** Acts on a control carrying data-action. Returns true when the press was this area's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    if (action === "reload" || action === "refresh") {
      void this.load();
      return true;
    }
    return false;
  }

  render(): TemplateResult {
    return renderSalesIncoming({
      phase: this.#phase,
      error: this.#error,
      busy: this.#busy,
      rows: this.#orders.map((o) => ({
        po: o.po_no,
        vendor: o.vendor_name,
        expected: whenWords(o.expected_at, houseDay()),
        how: o.delivery_method,
        lines: o.line_count,
        justPlaced: false,
      })),
    });
  }
}
