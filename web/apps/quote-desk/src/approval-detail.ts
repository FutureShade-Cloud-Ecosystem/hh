// The approval request detail (decision log row 956), the Approvals surface's second screen at
// #/approvals/<request id>: one margin floor request in its quote's context. The customer, the rep, every line
// with the requested line marked (its discount, its price, its margin against the floor and the gap), the quote's
// totals, margin and notes, Approve and Refuse with the optional reason, and a link to the full quote. Deciding
// goes back to the queue on the next pending request, or the queue itself when none is left.
//
// Every figure is the server's: the lines, prices and totals come from the quote read, the margins from the
// margin glance (absent for a caller off the cost list, then not shown), the discount, margin and floor from the
// request's own sentence. Nothing is computed here but the gap between two figures the sentence names. The route's
// wall is the host's (this element is never mounted for Sales). The templates carry no property or event
// bindings: the host delegation reads data-action off the click's path.
import { LitElement, css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import type { IdentityApproval } from "@hardscapehouse/api-client";
import { hhAddress } from "@hardscapehouse/design-system/src/address.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { money } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import type { QdClient } from "./qd-client.js";
import type { QuoteView } from "./_quote-edit.js";
import { quoteStatusWord } from "./_quote-edit.js";
import { lineMarginLabel, marginPctLabel, type GlanceIn } from "./_quote-workarea.js";
import { decisionWord, isDecidable, parseRequestDetail, quoteIdOfSubject, approvalDetail } from "./_approval-copy.js";

@customElement("qd-approval-detail")
export class QdApprovalDetail extends LitElement {
  @property({ attribute: false }) client: QdClient | null = null;
  @property({ attribute: "request-id" }) requestId = "";

  @state() private request: IdentityApproval | null = null;
  @state() private view: QuoteView | null = null;
  @state() private glance: GlanceIn | null = null;
  @state() private notes = "";
  @state() private loaded = false;
  @state() private error = "";
  @state() private busy = false;
  private seq = 0;

  static styles = [
    baseStyles,
    css`
    :host {
      display: block;
      padding: var(--hh-s-16, 16px);
      font-family: var(--hh-font-ui);
      color: var(--hh-text);
    }
    a {
      color: var(--hh-accent-text);
    }
    .top {
      display: flex;
      flex-wrap: wrap;
      align-items: baseline;
      gap: var(--hh-s-12, 12px);
      margin-bottom: var(--hh-s-12, 12px);
    }
    h1 {
      margin: 0;
      font-size: 1.3rem;
    }
    .card {
      border: 1px solid var(--hh-line);
      border-radius: var(--hh-radius-card, 10px);
      background: var(--hh-card);
      padding: var(--hh-s-12, 12px) var(--hh-s-16, 16px);
      margin-bottom: var(--hh-s-12, 12px);
    }
    h2 {
      margin: 0 0 var(--hh-s-8, 8px);
      font-size: 0.95rem;
    }
    dl {
      display: grid;
      grid-template-columns: max-content 1fr;
      gap: var(--hh-s-4, 4px) var(--hh-s-16, 16px);
      margin: 0;
    }
    dt {
      color: var(--hh-text-dim);
    }
    dd {
      margin: 0;
    }
    .scroll {
      overflow-x: auto;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 0.85rem;
    }
    th,
    td {
      text-align: left;
      padding: var(--hh-s-4, 4px) var(--hh-s-8, 8px);
      border-bottom: 1px solid var(--hh-line);
    }
    th {
      color: var(--hh-text-dim);
      font-weight: 600;
    }
    td.num,
    th.num {
      text-align: right;
      white-space: nowrap;
    }
    tr[data-requested="true"] td {
      background: color-mix(in srgb, var(--hh-accent) 16%, transparent);
      border-bottom-color: var(--hh-accent-text);
      font-weight: 600;
    }
    tr[data-requested="true"] td:first-child {
      box-shadow: inset 3px 0 0 var(--hh-accent-text);
    }
    .tag {
      display: inline-block;
      margin-left: var(--hh-s-8, 8px);
      padding: 0 8px;
      border: 1px solid var(--hh-accent-text);
      border-radius: 999px;
      color: var(--hh-accent-text);
      font-size: 0.7rem;
      font-weight: 600;
    }
    .gap {
      color: var(--hh-danger, #c0392b);
    }
    .actions {
      display: flex;
      flex-wrap: wrap;
      gap: var(--hh-s-8, 8px);
      align-items: end;
    }
    .actions label {
      display: block;
      font-size: 0.8rem;
      color: var(--hh-text-dim);
      margin-bottom: var(--hh-s-4, 4px);
    }
    .actions input {
      min-width: 18rem;
      max-width: 100%;
      box-sizing: border-box;
    }
    .err {
      color: var(--hh-danger, #c0392b);
    }
    .dim {
      color: var(--hh-text-dim);
    }
  `,
  ];

  override connectedCallback(): void {
    super.connectedCallback();
    this.renderRoot.addEventListener("click", this.onClick);
  }

  override disconnectedCallback(): void {
    this.renderRoot.removeEventListener("click", this.onClick);
    super.disconnectedCallback();
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("client") || changed.has("requestId")) {
      // Another request is another page: nothing of the last one stays under its heading.
      this.request = null;
      this.view = null;
      this.glance = null;
      this.notes = "";
      this.loaded = false;
      this.error = "";
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    const client = this.client;
    const id = this.requestId;
    if (client === null || id === "") return;
    const seq = ++this.seq;
    const all = await client.listApprovals("");
    if (seq !== this.seq) return;
    if (!all.ok) {
      this.error = all.error;
      this.loaded = true;
      return;
    }
    const request = all.value.find((row) => row.id === id) ?? null;
    this.request = request;
    const quoteId = quoteIdOfSubject(request?.subject_ref);
    if (request === null || quoteId === "") {
      this.loaded = true;
      return;
    }
    const [view, glance, terms] = await Promise.all([client.readQuote(quoteId), client.marginGlance(quoteId), client.readTerms(quoteId)]);
    if (seq !== this.seq) return;
    this.view = view.ok ? view.value : null;
    this.error = view.ok ? "" : view.error;
    this.glance = glance.ok ? glance.value : null;
    this.notes = terms.ok ? terms.value.customerNotes : "";
    this.loaded = true;
  }

  private readonly onClick = (event: Event): void => {
    const control = event.composedPath().find((n): n is HTMLElement => n instanceof HTMLElement && n.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    if (action === "approve") void this.decide(true);
    if (action === "refuse") void this.decide(false);
  };

  private isOwn(): boolean {
    const me = this.client?.callerId() ?? "";
    return me !== "" && this.request?.requested_by === me;
  }

  private async decide(approve: boolean): Promise<void> {
    const client = this.client;
    const request = this.request;
    if (client === null || request === null || this.busy) return;
    this.busy = true;
    this.error = "";
    const reason = this.renderRoot.querySelector<HTMLInputElement>('[data-field="reason"]')?.value.trim() ?? "";
    const result = await client.decideApproval(request.id, approve, reason);
    this.busy = false;
    if (!result.ok) {
      // The server's own text, verbatim; the reason stays typed unless the refusal is final.
      this.error = result.error;
      if (result.status === 409) await this.load();
      return;
    }
    // The count in the navigation and every other view of the queue re-read.
    this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: this.view?.id ?? "" }, bubbles: true, composed: true }));
    const pending = await client.listApprovals("PENDING");
    const next = pending.ok ? pending.value.find((row) => row.id !== request.id && isDecidable(row.status)) : undefined;
    window.location.hash = next === undefined ? "#/approvals" : `#/approvals/${encodeURIComponent(next.id)}`;
  }

  private renderLines(view: QuoteView, figures: ReturnType<typeof parseRequestDetail>) {
    const marginOn = typeof this.glance?.margin_bps === "number";
    return html`<div class="card">
      <h2>Lines</h2>
      <div class="scroll">
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>Product</th>
              <th class="num">Qty</th>
              <th class="num">Price</th>
              <th class="num">Total</th>
              ${marginOn ? html`<th class="num">Margin</th>` : nothing}
            </tr>
          </thead>
          <tbody>
            ${view.lines.map((line) => {
              const asked = line.line_no === figures.lineNo;
              return html`<tr data-testid="ad-line" data-line=${String(line.line_no)} data-requested=${asked ? "true" : "false"}>
                <td>${line.line_no}</td>
                <td>
                  ${line.description}${asked ? html`<span class="tag" data-testid="ad-asked">Requested${figures.discountPct !== null ? html`: ${figures.discountPct}% discount` : nothing}</span>` : nothing}
                </td>
                <td class="num">${line.qty_entered} ${line.uom_entered}</td>
                <td class="num">${money(line.unit_price_cents)} / ${line.price_uom}</td>
                <td class="num">${money(line.line_total_cents)}</td>
                ${marginOn ? html`<td class="num">${lineMarginLabel(this.glance, line.line_no)}</td>` : nothing}
              </tr>`;
            })}
          </tbody>
        </table>
      </div>
      ${figures.floorPct !== null || figures.marginPct !== null
        ? html`<p data-testid="ad-floor">
            The requested line:
            ${figures.marginPct !== null ? html`margin ${figures.marginPct.toFixed(1)}%` : nothing}
            ${figures.floorPct !== null ? html`against a floor of ${figures.floorPct.toFixed(1)}%` : nothing}
            ${figures.gapPct !== null ? html`<span class="gap" data-testid="ad-gap">, ${figures.gapPct.toFixed(1)} points under the floor</span>` : nothing}
          </p>`
        : nothing}
    </div>`;
  }

  override render() {
    if (!this.loaded) return html`<p class="dim" data-testid="ad-loading">Loading the request.</p>`;
    const request = this.request;
    if (request === null) {
      return html`<div class="card" data-testid="ad-missing" role="alert">
        <h2>Request not found</h2>
        <p class="dim">${this.error !== "" ? this.error : "The server holds no approval request with this id."}</p>
        <a href="#/approvals">Back to Approvals</a>
      </div>`;
    }
    const view = this.view;
    const figures = parseRequestDetail(request.detail);
    const decidable = isDecidable(request.status);
    const own = decidable && this.isOwn();
    const quoteHref = `#/quotes/${encodeURIComponent(view?.id ?? "")}`;
    return html`
      <div class="top">
        <a href="#/approvals" data-testid="ad-back">All approvals</a>
        <h1 data-testid="ad-quote">${view !== null ? html`Quote ${view.quote_no}` : "Approval request"}</h1>
        <span data-testid="ad-status">${decisionWord(request.status)}</span>
      </div>
      <p data-testid="ad-sentence">${approvalDetail(request.detail)}</p>
      ${this.error !== "" ? html`<p class="err" role="alert" data-testid="ad-error">${this.error}</p>` : nothing}
      ${view !== null
        ? html`
            <div class="card">
              <h2>The quote</h2>
              <dl>
                <dt>Customer</dt>
                <dd data-testid="ad-customer">${view.customer_name}</dd>
                <dt>Rep</dt>
                <dd data-testid="ad-rep">${view.owner_name ?? "Unassigned"}</dd>
                <dt>Status</dt>
                <dd>${quoteStatusWord(view.status)}</dd>
                <dt>Notes</dt>
                <dd data-testid="ad-notes">${this.notes !== "" ? this.notes : "No notes."}</dd>
              </dl>
            </div>
            ${this.renderLines(view, figures)}
            <div class="card">
              <h2>Totals</h2>
              <dl data-testid="ad-totals">
                <dt>Subtotal</dt>
                <dd>${money(view.subtotal_cents)}</dd>
                <dt>Tax</dt>
                <dd>${money(view.tax_cents)}</dd>
                <dt>Total</dt>
                <dd>${money(view.total_cents)}</dd>
                ${typeof this.glance?.margin_bps === "number"
                  ? html`<dt>Quote margin</dt>
                      <dd data-testid="ad-margin">${marginPctLabel(this.glance.margin_bps)}</dd>`
                  : nothing}
              </dl>
              <p><a href="${hhAddress(quoteHref)}" data-testid="ad-open-quote">Open the full quote</a></p>
            </div>
          `
        : nothing}
      ${decidable && !own
        ? html`<div class="card actions">
            <div>
              <label for="ad-reason">Reason for the decision (optional)</label>
              <input id="ad-reason" data-field="reason" type="text" maxlength="300" data-testid="ap-reason" />
            </div>
            <button type="button" class="btn btn-primary" data-action="approve" ?disabled=${this.busy} data-testid="ap-approve">Approve</button>
            <button type="button" class="btn btn-danger" data-action="refuse" ?disabled=${this.busy} data-testid="ap-refuse">Refuse</button>
          </div>`
        : nothing}
      ${own ? html`<p class="dim" data-testid="ap-own">You filed this request. Another person decides it.</p>` : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-approval-detail": QdApprovalDetail;
  }
}
