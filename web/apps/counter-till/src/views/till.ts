// The till screen. This slice: the drawer strip (the branch's MAIN drawer,
// found or opened with the invented float) and the product lookup. The sale's
// ticket, tenders, receipt and parked tickets build on this view in the
// cycle's own steps, so the screen shows only what the server has answered:
// a figure the till cannot prove has no place here, and a refusal is always
// in words.
import { css, html, nothing, type TemplateResult } from "lit";
import type { ProductRef } from "../till-client.js";

export interface DrawerPanel {
  /** resolving: the branch and drawer lookup is in flight. */
  state: "resolving" | "open" | "refused";
  how: "found" | "opened" | null;
  branchName: string | null;
  station: string | null;
  /** The float, already through the one money helper. */
  float: string | null;
  message: string | null;
  busy: boolean;
}

export interface SearchPanel {
  state: "idle" | "loading" | "ready" | "refused";
  hits: ProductRef[];
  message: string | null;
}

export interface TillView {
  drawer: DrawerPanel;
  search: SearchPanel;
}

export const tillStyles = css`
  .till h2 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .drawer-strip {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hh-s-12);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    padding: var(--hh-s-12) var(--hh-s-16);
    margin: 0 0 var(--hh-s-16);
    max-width: 720px;
  }
  .drawer-strip .word {
    font-weight: 600;
  }
  .panels {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
    gap: var(--hh-s-16);
    max-width: 980px;
  }
  .panel {
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-16);
    background: var(--hh-card);
  }
  .panel h3 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-12);
  }
  .panel .notice {
    color: var(--hh-text-dim);
    margin: var(--hh-s-8) 0 0;
  }
  form.search {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8);
    align-items: center;
    margin: 0 0 var(--hh-s-8);
  }
  form.search label {
    min-width: 100%;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
  }
  form.search input {
    flex: 1;
    min-width: 160px;
    min-height: 44px;
    background: var(--hh-page);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: 0 var(--hh-s-12);
    font-family: inherit;
    font-size: var(--hh-s-16);
  }
  form.search input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  ul.hits {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .hit {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hh-s-12);
    padding: var(--hh-s-8) 0;
    border-bottom: 1px solid var(--hh-line);
  }
  .hit .sku {
    font-family: var(--hh-font-ui);
    font-weight: 600;
    min-width: 72px;
  }
  .hit .name {
    flex: 1;
  }
  .hit .sold-by {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
  }
`;

function renderDrawer(panel: DrawerPanel): TemplateResult {
  if (panel.state === "resolving") {
    return html`
      <div class="drawer-strip" role="status">Reading the branch and its drawer.</div>
    `;
  }
  if (panel.state === "refused") {
    return html`
      <div class="drawer-strip" role="status">
        <span class="word">No drawer.</span>
        <span>${panel.message}</span>
        <button type="button" class="btn-secondary" data-action="retry-drawer" ?disabled=${panel.busy}>
          Try the drawer again
        </button>
      </div>
    `;
  }
  return html`
    <div class="drawer-strip">
      <span class="word">Drawer</span>
      <span data-testid="drawer-status">
        ${panel.branchName} ${panel.station}: open with a float of ${panel.float}
      </span>
    </div>
  `;
}

function renderSearch(panel: SearchPanel): TemplateResult {
  return html`
    <section class="panel" aria-label="Product search">
      <h3>Products</h3>
      <form class="search" data-form="product-search">
        <label for="product-search-input">Search the catalogue by sku or name</label>
        <input
          id="product-search-input"
          data-input="product-search"
          type="search"
          autocomplete="off"
          placeholder="for example, Holland"
        />
        <button type="submit" class="btn-primary">Search</button>
      </form>
      ${panel.state === "loading"
        ? html`<p class="notice" role="status">Searching the catalogue.</p>`
        : nothing}
      ${panel.state === "refused"
        ? html`<p class="notice" role="status">${panel.message}</p>`
        : nothing}
      ${panel.state === "ready" && panel.hits.length === 0
        ? html`<p class="notice" role="status">Nothing in the catalogue matches that search.</p>`
        : nothing}
      ${panel.state === "ready" && panel.hits.length > 0
        ? html`
            <ul class="hits" data-testid="product-hits">
              ${panel.hits.map(
                (hit) => html`
                  <li class="hit">
                    <span class="sku">${hit.sku}</span>
                    <span class="name">${hit.name}</span>
                    <span class="sold-by">sold by ${hit.soldBy}</span>
                  </li>
                `,
              )}
            </ul>
          `
        : nothing}
    </section>
  `;
}

export function renderTill(view: TillView): TemplateResult {
  return html`
    <section class="till">
      <h2>Till</h2>
      ${renderDrawer(view.drawer)}
      <div class="panels">
        ${renderSearch(view.search)}
        <section class="panel" aria-label="Sale ticket">
          <h3>Ticket</h3>
          <p class="notice">No lines yet: the ticket builds with the sale's steps in the cycle.</p>
        </section>
      </div>
    </section>
  `;
}
