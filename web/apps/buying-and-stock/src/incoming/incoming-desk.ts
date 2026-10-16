// The sales seat's Incoming area (change 5, board bs-stock-lookup-sales-search-a-product's second
// pill): what is on order, read only. Ruling R1 gives Sales no buying actions, so this desk offers
// the arrivals and nothing else: the register read the buyers' screen draws its own Incoming from,
// the placed orders with their promised dates, refreshed when the seat asks.
import type { TemplateResult } from "lit";
import type { PurchasingPO } from "@hardscapehouse/api-client";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { PoApi } from "../po/po-api.js";
import { isPlaced, poRows, whenWords } from "../po/po-logic.js";
import { renderSalesIncoming, type SalesIncomingModel } from "./incoming-views.js";

export class IncomingDesk implements AreaDesk {
  readonly #api: PoApi;
  readonly #host: DeskHost;

  #phase: "loading" | "ready" = "loading";
  #error = "";
  #orders: readonly PurchasingPO[] = [];
  #busy = false;
  readonly #today = new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());

  constructor(api: PoApi, host: DeskHost) {
    this.#api = api;
    this.#host = host;
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
        expected: whenWords(o.expected_at, this.#today),
        how: o.delivery_method,
        lines: o.line_count,
        justPlaced: false,
      })),
    });
  }
}
