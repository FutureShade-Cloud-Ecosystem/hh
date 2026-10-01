// The till screen. This slice: the drawer strip (the branch's MAIN drawer,
// found or opened with the invented float), the product lookup, the customer
// (the walk-in by default, an account with its balance, limit and any hold),
// and the ticket with its margin column: every line priced by the engine
// through the counter price picture, no price field anywhere, and the margin
// rendered only as the glance answered it, the explicit placeholder where it
// answered nothing. Tenders, receipt and parked tickets build on this view in
// the cycle's own steps, so the screen shows only what the server has
// answered: a figure the till cannot prove has no place here, and a refusal
// is always in words.
import { css, html, nothing, type TemplateResult } from "lit";
import { formatMargin, formatMoney } from "../money.js";
import type { CustomerRef, ProductRef } from "../till-client.js";

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

export type PickedCustomer =
  | { kind: "walkin" }
  | {
      kind: "account";
      customer: CustomerRef;
      /** The standing read's answers, each possibly unknown; null until it answers. */
      standing: { balance: string | null; limit: string | null; pastDue: string | null; onHold: boolean | null } | null;
      standingMessage: string | null;
    };

export interface CustomerPanel {
  picked: PickedCustomer;
  searchState: "idle" | "loading" | "ready" | "refused";
  hits: CustomerRef[];
  message: string | null;
}

export interface TicketLineView {
  key: string;
  sku: string;
  name: string;
  uom: string;
  /** The quantity, as the ticket last priced it: the input's starting value. */
  qty: number;
  /** The rule unit price, already through the one money helper, or null for the placeholder. */
  unit: string | null;
  /** The rule basis word when the price read named one (an account rule, a promotion). */
  basis: string | null;
  /** The margin, already through the helper, or null for the placeholder. */
  margin: string | null;
}

export interface TicketPanel {
  lines: TicketLineView[];
  pricing: "idle" | "pricing" | "priced" | "refused";
  message: string | null;
  /** The server's own resolution of whose prices these are, or null before the first read. */
  customerType: string | null;
}

export interface TillView {
  drawer: DrawerPanel;
  search: SearchPanel;
  customer: CustomerPanel;
  ticket: TicketPanel;
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
  .hit button {
    min-height: 36px;
    padding: 0 var(--hh-s-12);
    font-size: var(--hh-s-12);
  }
  table.ticket {
    width: 100%;
    border-collapse: collapse;
    margin: 0 0 var(--hh-s-8);
  }
  table.ticket th {
    text-align: start;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    font-weight: 600;
    border-bottom: 1px solid var(--hh-control-line);
    padding: 0 var(--hh-s-8) var(--hh-s-8) 0;
  }
  table.ticket td {
    padding: var(--hh-s-8) var(--hh-s-8) var(--hh-s-8) 0;
    border-bottom: 1px solid var(--hh-line);
    vertical-align: baseline;
  }
  table.ticket td.item .sku {
    display: block;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
  }
  table.ticket td.qty input {
    width: 72px;
    min-height: 36px;
    background: var(--hh-page);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: 0 var(--hh-s-8);
    font-family: inherit;
    font-size: var(--hh-s-16);
  }
  table.ticket td.qty input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  table.ticket td.item button,
  table.ticket td.actions button {
    min-height: 36px;
    padding: 0 var(--hh-s-12);
    font-size: var(--hh-s-12);
  }
  .ticket-tools {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-12);
  }
  .unpriced {
    color: var(--hh-text-dim);
  }
  .account-rows {
    margin: 0 0 var(--hh-s-12);
    padding: 0;
  }
  .account-rows li {
    list-style: none;
    padding: var(--hh-s-4) 0;
  }
  .hold-chip {
    margin-inline-start: var(--hh-s-8);
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

function renderCustomer(panel: CustomerPanel): TemplateResult {
  const picked = panel.picked;
  return html`
    <section class="panel" aria-label="Customer">
      <h3>Customer</h3>
      ${picked.kind === "walkin"
        ? html`<p data-testid="customer-name">Walk-in: no account, the counter's default.</p>`
        : html`
            <p>
              <strong data-testid="customer-name">${picked.customer.name}</strong>
              <span>(${picked.customer.accountNo}, ${picked.customer.customerType})</span>
              ${picked.standing?.onHold === true
                ? html`<span class="chip chip-stop hold-chip" data-testid="hold-flag">On hold</span>`
                : nothing}
            </p>
            <ul class="account-rows" data-testid="account-standing">
              <li>
                Balance:
                ${picked.standing === null
                  ? html`<span class="unpriced">(reading the account)</span>`
                  : picked.standing.balance === null
                    ? html`<span class="unpriced">(not answered)</span>`
                    : html`<span data-testid="account-balance">${picked.standing.balance}</span>`}
              </li>
              <li>
                Past due:
                ${picked.standing === null || picked.standing.pastDue === null
                  ? html`<span class="unpriced">(not answered)</span>`
                  : picked.standing.pastDue}
              </li>
              <li>
                Credit limit:
                ${picked.standing === null || picked.standing.limit === null
                  ? html`<span class="unpriced">(not answered)</span>`
                  : picked.standing.limit}
              </li>
            </ul>
          `}
      ${picked.kind === "account" && picked.standingMessage !== null
        ? html`<p class="notice" role="status">${picked.standingMessage}</p>`
        : nothing}
      <form class="search" data-form="customer-search">
        <label for="customer-search-input">Find an account by name or account number</label>
        <input
          id="customer-search-input"
          data-input="customer-search"
          type="search"
          autocomplete="off"
          placeholder="for example, Fenwick"
        />
        <button type="submit" class="btn-secondary">Find</button>
      </form>
      ${panel.searchState === "loading"
        ? html`<p class="notice" role="status">Searching the accounts.</p>`
        : nothing}
      ${panel.searchState === "refused"
        ? html`<p class="notice" role="status">${panel.message}</p>`
        : nothing}
      ${panel.searchState === "ready" && panel.hits.length === 0
        ? html`<p class="notice" role="status">No account matches that search.</p>`
        : nothing}
      ${panel.searchState === "ready" && panel.hits.length > 0
        ? html`
            <ul class="hits" data-testid="customer-hits">
              ${panel.hits.map(
                (hit) => html`
                  <li class="hit">
                    <span class="sku">${hit.accountNo}</span>
                    <span class="name">${hit.name}</span>
                    <button
                      type="button"
                      class="btn-secondary"
                      data-action="pick-customer"
                      data-customer-id=${hit.id}
                    >
                      Use
                    </button>
                  </li>
                `,
              )}
            </ul>
          `
        : nothing}
      ${picked.kind === "account"
        ? html`
            <button type="button" class="btn-secondary" data-action="use-walkin">Back to the walk-in</button>
          `
        : nothing}
    </section>
  `;
}

function renderTicket(panel: TicketPanel): TemplateResult {
  return html`
    <section class="panel" aria-label="Sale ticket">
      <h3>Ticket</h3>
      ${panel.lines.length === 0
        ? html`<p class="notice">No lines on this ticket yet: search the catalogue and add one.</p>`
        : html`
            <table class="ticket" data-testid="ticket-lines">
              <thead>
                <tr>
                  <th scope="col">Item</th>
                  <th scope="col">Qty</th>
                  <th scope="col">Unit price</th>
                  <th scope="col">Margin</th>
                  <th scope="col"><span class="visually-hidden">Remove</span></th>
                </tr>
              </thead>
              <tbody>
                ${panel.lines.map(
                  (line) => html`
                    <tr data-testid="ticket-line">
                      <td class="item">
                        ${line.name}
                        <span class="sku">${line.sku}</span>
                        ${line.basis === null ? nothing : html`<span class="sold-by">${line.basis} price</span>`}
                      </td>
                      <td class="qty">
                        <input
                          type="text"
                          inputmode="decimal"
                          autocomplete="off"
                          aria-label="Quantity of ${line.name}"
                          value="${line.qty}"
                          data-qty-key=${line.key}
                        />
                        <span class="sold-by">${line.uom}</span>
                      </td>
                      <td>
                        ${line.unit === null
                          ? html`<span class="unpriced" data-testid="line-unpriced">(not priced)</span>`
                          : html`<span data-testid="line-unit-price">${line.unit}</span>`}
                      </td>
                      <td>
                        ${line.margin === null
                          ? html`<span class="unpriced" data-testid="line-margin-none">(no margin)</span>`
                          : html`<span data-testid="line-margin">${line.margin}</span>`}
                      </td>
                      <td class="actions">
                        <button
                          type="button"
                          class="btn-secondary"
                          data-action="remove-line"
                          data-line-key=${line.key}
                          aria-label="Remove ${line.name}"
                        >
                          Remove
                        </button>
                      </td>
                    </tr>
                  `,
                )}
              </tbody>
            </table>
          `}
      ${panel.pricing === "pricing"
        ? html`<p class="notice" role="status">Pricing the ticket.</p>`
        : nothing}
      ${panel.pricing === "refused"
        ? html`<p class="notice" role="status">${panel.message}</p>`
        : nothing}
      <div class="ticket-tools">
        <button
          type="button"
          class="btn-secondary"
          data-action="reprice"
          ?disabled=${panel.lines.length === 0 || panel.pricing === "pricing"}
        >
          Re-price the ticket
        </button>
        ${panel.customerType === null
          ? nothing
          : html`<span class="sold-by">priced for: ${panel.customerType}</span>`}
      </div>
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
        ${renderCustomer(view.customer)}
        ${renderTicket(view.ticket)}
      </div>
    </section>
  `;
}
