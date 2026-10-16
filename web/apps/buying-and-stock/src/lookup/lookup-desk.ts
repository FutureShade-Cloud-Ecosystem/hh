// The Sales stock lookup's controller (change 5, board bs-stock-lookup-sales-search-a-product):
// the state the screen draws from, the loads, and what each press does. It is not an element; the
// root element owns it and asks it for the template, like every area's desk.
//
// The seat is read only (ruling R1): every read here is a GET the role matrix already answers at
// read, and the desk offers no press that writes anything. A search reads the browse, a pick reads
// the branch balances, the product and what is coming in for it. Nothing is stored; a refusal reads
// in words where the read that failed sits.
import type { TemplateResult } from "lit";
import type { Branch, InventoryBranchStock, Product } from "@hardscapehouse/api-client";
import type { AreaDesk, DeskHost } from "../purch/area.js";
import { InventoryApi, branchRows, browseAnswer } from "../inventory/inventory-api.js";
import type { InventoryBrowseRow } from "@hardscapehouse/api-client";
import { incomingLines } from "../po/po-logic.js";
import { chipGrants, comingInRows, type ComingInRow } from "./lookup-logic.js";
import { renderLookup, type LookupModel } from "./lookup-views.js";

export class LookupDesk implements AreaDesk {
  readonly #api: InventoryApi;
  readonly #host: DeskHost;
  /** The core's base address, the product images' origin (the image read is public by design). */
  readonly #base: string;

  #phase: LookupModel["phase"] = "loading";
  #error = "";
  // The search's answer: the page's rows, the chip, and the text the search ran with.
  #rows: readonly InventoryBrowseRow[] = [];
  #chip = "all";
  #searched = "";
  // The pick: the row's id, and the three reads the detail is drawn from.
  #pickedId: string | null = null;
  #product: Product | null = null;
  #branches: readonly InventoryBranchStock[] = [];
  #coming: readonly ComingInRow[] = [];
  #branchNames: ReadonlyMap<string, string> = new Map();
  #busy = false;
  // The detail read's own refusal, beside the detail card where the press that caused it is.
  #detailError = "";
  // The products whose image read answered (the route 404s a product with none): the screen draws
  // a thumb only where there is a picture, the empty slot where there is not.
  #images = new Set<string>();
  // The day the expected words are read against, the same source the other desks hand their views.
  readonly #today = new Intl.DateTimeFormat("en-CA", { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());

  constructor(api: InventoryApi, host: DeskHost, coreBaseUrl: string) {
    this.#api = api;
    this.#host = host;
    this.#base = coreBaseUrl;
  }

  /** The product image's public read, the same origin the staff client dials. */
  imageUrl(productId: string): string {
    return `${this.#base}/api/v1/products/${productId}/image`;
  }

  /** The first read of the area: the branches, whose names the coming-in table shows. */
  async load(): Promise<void> {
    this.#phase = "loading";
    this.#host.repaint();
    const branches = await this.#api.branches();
    if (branches.ok) this.#branchNames = new Map(branches.value.map((b: Branch) => [b.id, b.name]));
    this.#phase = "ready";
    this.#host.repaint();
  }

  /** Acts on a control carrying data-action. Returns true when the press was this area's. */
  press(control: Element): boolean {
    const action = control.getAttribute("data-action");
    switch (action) {
      case "reload":
        void this.load();
        return true;
      case "lookup-search":
        void this.#search(this.#host.field("lookup-text"));
        return true;
      case "lookup-chip":
        this.#chip = control.getAttribute("data-choice") ?? "all";
        this.#host.repaint();
        return true;
      case "lookup-pick": {
        const id = control.getAttribute("data-product-id") ?? "";
        void this.#pick(id);
        return true;
      }
      default:
        return false;
    }
  }

  async #search(text: string): Promise<void> {
    if (this.#busy) return;
    const term = text.trim();
    if (term === "") return;
    this.#busy = true;
    this.#error = "";
    this.#host.repaint();
    const answer = await this.#api.browse(term);
    if (!answer.ok) {
      this.#error = lookupRefusal("search", answer.error, answer.status);
      this.#busy = false;
      this.#host.repaint();
      return;
    }
    this.#rows = browseAnswer(answer.value).rows;
    this.#searched = term;
    this.#pickedId = null;
    this.#product = null;
    this.#branches = [];
    this.#coming = [];
    this.#detailError = "";
    this.#busy = false;
    this.#host.repaint();
    this.#readImages(this.#rows.map((r) => r.product_id));
  }

  /** Asks the public image read which of the products carry a picture; the misses stay slots. */
  #readImages(productIds: readonly string[]): void {
    void Promise.all(
      productIds.map((id) =>
        fetch(this.imageUrl(id))
          .then((r) => (r.ok ? id : null))
          .catch(() => null),
      ),
    ).then((answers) => {
      let landed = false;
      for (const id of answers) {
        if (id === null || this.#images.has(id)) continue;
        this.#images.add(id);
        landed = true;
      }
      if (landed) this.#host.repaint();
    });
  }

  /** Whether the product carries a picture the screen has seen the image read answer for. */
  hasImage(productId: string): boolean {
    return this.#images.has(productId);
  }

  async #pick(productId: string): Promise<void> {
    if (productId === "" || this.#busy) return;
    this.#busy = true;
    this.#detailError = "";
    this.#pickedId = productId;
    this.#host.repaint();
    const [stock, incoming, product] = await Promise.all([
      this.#api.branchStock(productId),
      this.#api.incomingFor(productId),
      this.#api.product(productId),
    ]);
    if (!stock.ok) {
      this.#detailError = lookupRefusal("stock", stock.error, stock.status);
    } else {
      this.#branches = branchRows(stock.value);
    }
    if (!incoming.ok) {
      this.#detailError = lookupRefusal("incoming", incoming.error, incoming.status);
    } else {
      this.#coming = comingInRows(incomingLines(incoming.value), this.#branchNames, this.#today);
    }
    this.#product = product.ok ? product.value : null;
    this.#busy = false;
    this.#host.repaint();
  }

  render(): TemplateResult {
    const rows = this.#rows.filter((r) => chipGrants(this.#chip, r));
    const picked = this.#pickedId === null ? undefined : this.#rows.find((r) => r.product_id === this.#pickedId);
    return renderLookup({
      phase: this.#phase,
      error: this.#error,
      busy: this.#busy,
      searched: this.#searched,
      chip: this.#chip,
      rows,
      total: this.#rows.length,
      picked,
      pickedId: this.#pickedId,
      product: this.#product,
      branches: this.#branches,
      coming: this.#coming,
      detailError: this.#detailError,
      imageUrl: (id: string) => this.imageUrl(id),
      hasImage: (id: string) => this.hasImage(id),
    });
  }
}

/** The read's refusal in words, the module's prefix stripped the way the other desks read it. */
function lookupRefusal(action: string, error: string, status: number | undefined): string {
  const words = error.replace(/^[a-z_]+: /, "");
  if (status === 403) return `You do not have access to this stock read. ${words}`;
  if (status === 404) return "That product is not in the catalogue any more.";
  return `${action === "search" ? "The search" : "The stock read"} did not go through. ${words}`;
}
