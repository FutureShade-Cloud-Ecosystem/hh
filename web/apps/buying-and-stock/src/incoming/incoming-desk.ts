// The Incoming area's controller: the state the screen draws from, the load,
// and what each press does. It is not an element; the root element owns it,
// hands it a way to repaint, and asks it for the template. Nothing is stored.
//
// The reads: the PO register, then each placed order's own detail, one row per
// line that has not landed. Any read that is refused fails the screen in words:
// a quietly skipped order would read as "not coming", and the yard would miss
// the truck it names.
import type { Result } from "@hardscapehouse/api-client";
import type { TemplateResult } from "lit";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { type PoApi } from "../po/po-api.js";
import { isPlaced, poRows, refusalWords } from "../po/po-logic.js";
import { arrivalRows, sortByPromise, type ArrivalRow } from "./incoming-logic.js";
import { renderIncoming, type IncomingModel } from "./incoming-views.js";

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
    const details = await Promise.all(placed.map((o) => this.#api.order(o.id)));
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
      today: new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date()),
    });
  }
}
