// The Catalog surface: what the yard sells, found by search. The core serves
// the search and the product; the catalog carries no prices, because pricing
// is the dealer's (it lands on a line at the RFQ, and on a quote). The search
// answers as the person types, the latest query winning; its state survives a
// drill into a product and back. Visual references: the ref's CatalogPage
// (search above everything, one row per product, the empty search says what to
// do) and the original portal's catalog search.
import { html, nothing, type TemplateResult } from "lit";
import { tenantCatalog, type TenantCatalogResult } from "@hardscapehouse/api-client";
import type { Page, SurfaceAnswer } from "./views.js";

export const CATALOG_WORDS = {
  empty: "Search the dealer's catalog.",
  none: "No matches. Try another word.",
};

interface SearchState {
  seq: number;
  term: string;
  results: TenantCatalogResult[];
  searching: boolean;
  failed: boolean;
}

const searches = new WeakMap<Page, SearchState>();

function searchOf(page: Page): SearchState {
  let held = searches.get(page);
  if (held === undefined) {
    held = { seq: 0, term: "", results: [], searching: false, failed: false };
    searches.set(page, held);
  }
  return held;
}

export async function catalogLoad(page: Page): Promise<SurfaceAnswer> {
  if (!page.session.can("submit_rfq")) return { kind: "refusal" };
  const search = searchOf(page);
  if (search.term !== "" && !search.searching) {
    // Back from a drill into a product: the search rides, the field refills after render.
    page.assign({ "catalog-search": search.term });
  }
  return { kind: "ready", data: search };
}

export function catalogView(dataRaw: unknown, page: Page): TemplateResult {
  void page;
  const search = dataRaw as SearchState;
  const asked = search.term.trim() !== "";
  return html`<label class="field">Search the catalog
      <input class="control" data-control="search" type="search" data-field="catalog-search" data-testid="catalog-search" aria-label="Search the catalog" />
    </label>
    ${search.searching
      ? html`<hh-pro-state data-state="loading" data-testid="catalog-state"></hh-pro-state>`
      : asked && search.results.length === 0
        ? html`<hh-pro-state data-state="empty" data-testid="catalog-state">${search.failed ? "The search did not go through. Try again." : CATALOG_WORDS.none}</hh-pro-state>`
        : !asked
          ? html`<hh-pro-state data-state="empty" data-testid="catalog-state">${CATALOG_WORDS.empty}</hh-pro-state>`
          : html`<ul class="cards" data-testid="catalog-rows">
              ${search.results.map(
                (item) => html`<li>
                  <button type="button" class="card" data-action="open-product" data-product="${item.product_id}" data-testid="catalog-row">
                    <span class="rowline"><span class="name figure">${item.name}</span><span class="grow"></span><span class="dim">${item.category}</span></span>
                    <span class="rowline dim"><span class="mono">${item.sku}</span><span class="grow"></span><span>Sold by the ${item.base_uom}</span></span>
                  </button>
                </li>`,
              )}
            </ul>`}`;
}

export function catalogCommand(action: string, control: Element, page: Page): boolean {
  void action;
  void control;
  void page;
  return false;
}

export function catalogInput(name: string, value: string, page: Page): void {
  if (name !== "catalog-search") return;
  void runSearch(page, value);
}

async function runSearch(page: Page, term: string): Promise<void> {
  const search = searchOf(page);
  const seq = (search.seq += 1);
  search.term = term;
  if (term.trim() === "") {
    search.results = [];
    search.searching = false;
    search.failed = false;
    page.touch();
    return;
  }
  search.searching = true;
  page.touch();
  const answer = await tenantCatalog.tenantSearchCatalog(page.client, { q: term.trim(), limit: 50 });
  if (seq !== search.seq) return;
  search.searching = false;
  search.failed = !answer.ok;
  search.results = answer.ok ? answer.value.items : [];
  page.touch();
}
