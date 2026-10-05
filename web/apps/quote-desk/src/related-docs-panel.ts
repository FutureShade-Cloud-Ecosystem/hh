// Ported from hardscapeos_dibbits master 87d7941b app/src/related-docs.ts;
// adapted for HH.
//
// The related documents panel (QD-5): the document family of the open quote,
// the quote itself, the order or orders it became and their invoices, as a
// flat list of chips, each naming the document, its status, its day and (on
// orders) which door minted it. A quote can be more than one order (multi
// accept), and the bar the ERP's ruling set is that staff can SEE where a
// document came from; this panel is that view.
//
// DELIBERATELY MINIMAL: a list, not a tree. No decision is made client side:
// the family, its order and its provenance all come from
// GET /api/v1/{quotes|orders}/{id}/related-documents, validated field by
// field by the client before it reaches this panel.
//
// HH changes: the panel rides the app's own base (PanelBase), so the read is
// guarded by the quote context (a family read for a quote left never shows on
// the next) and the host delegation is by data-action, never a bound click.
// This desk hosts the panel inside the edit panel's payment seam, where no
// document can be opened by this surface, so the chips render as labels
// (openKinds is empty: a button that does nothing is worse than a label, the
// ERP panel's own rule). The kind property keeps the port's two ends (quote
// or order) so a host that owns document opening can mount it either way.
import { css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { quoteErrorMessage } from "./_quote-edit.js";
import {
  chipDetail,
  chipText,
  familyEmptyText,
  familyIsOnlySelf,
  isOpenable,
  type RelatedDoc,
} from "./_related-docs.js";

@customElement("qd-related-docs")
export class QdRelatedDocs extends PanelBase {
  /** Which end of the family the host is standing on: the quote end or the order end. Picks the read. */
  @property() kind: "quote" | "order" = "quote";

  /** Space separated document kinds this HOST can open. Empty here: this desk opens no document from the family. */
  @property({ attribute: "open-kinds" }) openKinds = "";

  @state() private rows: RelatedDoc[] = [];
  @state() private loaded = false;
  /** A failed read, in words: it is not the same thing as an empty family and never renders as one. */
  @state() private readError = "";

  static styles = [
    panelStyles,
    css`
      :host {
        display: block;
        margin-top: 8px;
      }
      ul {
        list-style: none;
        margin: 0;
        padding: 0;
        display: flex;
        flex-wrap: wrap;
        gap: 6px;
      }
      .chip {
        display: inline-block;
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: 5px;
        padding: 4px 8px;
        font-size: var(--qp-body);
        background: var(--hos-slate-warm, var(--hh-card));
        color: var(--hos-text, var(--hh-text));
        text-align: left;
      }
      button.chip {
        cursor: pointer;
      }
      .chip .detail {
        display: block;
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        margin-top: 1px;
      }
      .here {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
      }
      .empty {
        font-size: var(--qp-body);
        color: var(--hos-text-dim, var(--hh-text-dim));
        margin: 0;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client") || changed.has("kind")) {
      // One quote's family never sits under the next quote's heading or error.
      this.rows = [];
      this.loaded = false;
      this.readError = "";
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  /** A family read for the quote that was left is dropped, never shown over the one open now, and an older read never lands over a newer one. */
  private async load(): Promise<void> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "") {
      this.rows = [];
      this.loaded = false;
      return;
    }
    // A cancelled context issues no more requests: the panel left the page (sign out) and a read of its is not this session's.
    if (ctx.stale) return;
    this.readError = "";
    const seq = ctx.startRead();
    const result = await ctx.guardedRead(seq, client.relatedDocuments(ctx.id, this.kind));
    if (result === undefined) return;
    if (!result.ok) {
      // A fault, not an empty family: nothing was read. A staff panel pointed at a document that
      // cannot be resolved says so rather than render a confident blank.
      this.rows = [];
      this.loaded = false;
      this.readError = spokenRefusal(quoteErrorMessage(result.status, result.error, "read this quote's related documents"));
      return;
    }
    this.rows = result.value;
    this.loaded = true;
  }

  /** A chip was clicked in a host that can open its kind: hand the row to the host, which owns opening. */
  private openDoc(row: RelatedDoc): void {
    this.dispatchEvent(new CustomEvent<RelatedDoc>("related-doc-open", { detail: row, bubbles: true, composed: true }));
  }

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action !== "open-doc") return;
    const row = this.rows[Number(control.getAttribute("data-index"))];
    if (row !== undefined) this.openDoc(row);
  }

  private renderChip(row: RelatedDoc, index: number) {
    const detail = chipDetail(row);
    const body = html`${chipText(row)}${row.self === true ? html` <span class="here">(this document)</span>` : nothing}
      ${detail !== "" ? html`<span class="detail">${detail}</span>` : nothing}`;
    return isOpenable(row, this.openKinds)
      ? html`<li>
          <button class="chip" type="button" title="Open ${chipText(row)}" data-action="open-doc" data-index=${index}>${body}</button>
        </li>`
      : html`<li><span class="chip">${body}</span></li>`;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    return html`
      <fieldset data-testid="rd-box">
        <legend>Related documents</legend>
        ${this.readError !== "" ? html`<p class="err" role="alert" data-testid="rd-error">${this.readError}</p>` : nothing}
        ${this.readError === "" && this.loaded && familyIsOnlySelf(this.rows)
          ? html`<p class="empty" data-testid="rd-empty">${familyEmptyText(this.kind === "order" ? "ORDER" : "QUOTE")}</p>`
          : nothing}
        ${this.rows.length > 1 ? html`<ul>${this.rows.map((row, index) => this.renderChip(row, index))}</ul>` : nothing}
      </fieldset>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-related-docs": QdRelatedDocs;
  }
}
