// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-availability.ts;
// adapted for HH.
//
// The at-quote availability read: a chip per quote line, graded by the server
// against what is AVAILABLE at the quote's branch (on hand minus hard holds),
// and a cross branch peek behind the lines that are short. The app only words
// the grade (see _quote-availability.ts). Each grade has its own look AND its
// own word, so colour is never the only signal; the grey one (not reported)
// is not the red one (none available): an unwired seam must not read as an
// empty shelf, nor as stock.
//
// The peek is deliberately not offered on a covered line. It shows every
// branch row the server returns, with the average cost only when the server
// sent one.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { quoteErrorMessage } from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import {
  availabilityChip,
  availabilityLabel,
  branchRowLabel,
  formatQty,
  shouldOfferPeek,
  softHeldNote,
  type BranchStock,
  type LineAvailability,
} from "./_quote-availability.js";

@customElement("qd-quote-availability")
export class QdQuoteAvailability extends PanelBase {
  @state() private lines: LineAvailability[] = [];
  /** The product name of each line, read from the quote itself (the availability wire carries only the product id). */
  @state() private names: Record<number, string> = {};
  @state() private seamWired = true;
  @state() private loaded = false;
  @state() private peeks: Record<string, BranchStock[]> = {};
  @state() private openPeek = "";

  static styles = [
    panelStyles,
    css`
      :host {
        margin-top: 8px;
      }
      .chip {
        display: inline-block;
        font-size: var(--qp-caption);
        font-family: var(--hos-font-mono, var(--hh-font-mono, monospace));
        padding: 2px 8px;
        border-radius: 99px;
        border: 1px dotted var(--hos-line, var(--hh-line));
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      /* Each grade differs in its border style as well as its hue, and always carries its word. */
      .chip.green {
        border: 1px solid var(--hos-success, var(--hh-success));
        color: var(--hos-success, var(--hh-success));
      }
      .chip.amber {
        border: 1px dashed var(--hos-stone-amber, var(--hh-accent));
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
      }
      .chip.red {
        border: 2px solid var(--hos-safety-red, var(--hh-danger));
        color: var(--hos-safety-red, var(--hh-danger));
      }
      td.num {
        font-family: var(--hos-font-mono, var(--hh-font-mono, monospace));
      }
      .label {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
      }
      .peek td {
        box-shadow: var(--hos-elev-1, none);
      }
      .peek .label {
        display: inline-block;
        margin-right: 14px;
      }
      .warn {
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
        margin-bottom: 6px;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // The last quote's lines, peeks and open peek never sit under this quote.
      this.lines = [];
      this.names = {};
      this.loaded = false;
      this.peeks = {};
      this.openPeek = "";
      this.error = "";
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  private async load(): Promise<void> {
    const client = this.client;
    const quoteId = this.quoteId;
    if (client === null || quoteId === "") {
      this.lines = [];
      this.loaded = false;
      return;
    }
    this.error = "";
    const [result, quote] = await Promise.all([client.availability(quoteId), client.readQuote(quoteId)]);
    if (quoteId !== this.quoteId) return;
    this.names = quote.ok ? Object.fromEntries(quote.value.lines.map((line) => [line.line_no, line.description])) : {};
    if (!result.ok) {
      // A failed read shows no lines at all: the previous read's chips would be stale facts under this quote.
      this.lines = [];
      this.loaded = false;
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "read this quote's availability"));
      return;
    }
    this.lines = result.value.lines;
    this.seamWired = result.value.seamWired;
    this.loaded = true;
    // A line write or a stock change makes a peeked branch list stale: forget them and re-read the open one.
    this.peeks = {};
    if (this.openPeek !== "") await this.readPeek(this.openPeek);
  }

  protected override onClickAction(action: string, control: HTMLElement): void {
    if (action !== "peek") return;
    const productId = control.getAttribute("data-product") ?? "";
    if (productId === "") return;
    if (this.openPeek === productId) {
      this.openPeek = "";
      return;
    }
    this.openPeek = productId;
    void this.readPeek(productId);
  }

  private async readPeek(productId: string): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.availabilityBranches(productId);
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "read the other branches' stock"));
      return;
    }
    this.error = "";
    this.peeks = { ...this.peeks, [productId]: result.value };
  }

  private renderLine(l: LineAvailability) {
    const rows = this.peeks[l.product_id];
    const held = softHeldNote(l);
    return html`
      <tr data-testid="av-line" data-line-no=${l.line_no}>
        <td>${l.line_no}${(this.names[l.line_no] ?? "") !== "" ? html` · ${this.names[l.line_no]}` : nothing}</td>
        <td class="num">${formatQty(l.qty_base)}</td>
        <td>
          <span class="${availabilityChip(l.grade)}" data-testid="av-chip" data-avail-grade=${l.grade}>${availabilityLabel(l)}</span>
          ${held !== "" ? html`<span class="label" data-testid="av-soft-held">${held}</span>` : nothing}
        </td>
        <td>
          ${shouldOfferPeek(l)
            ? html`<button type="button" data-action="peek" data-product=${l.product_id} aria-expanded=${this.openPeek === l.product_id ? "true" : "false"}>
                Other branches
              </button>`
            : nothing}
        </td>
      </tr>
      ${this.openPeek === l.product_id
        ? html`<tr class="peek" data-testid="av-peek">
            <td colspan="4">
              ${rows === undefined
                ? html`<span class="label">Reading the other branches...</span>`
                : rows.length === 0
                  ? html`<span class="label">The server returned no branch stock for this product.</span>`
                  : rows.map((b) => html`<span class="label" data-testid="av-branch" data-branch=${b.branch_code}>${branchRowLabel(b, l.branch.branch_id)}</span>`)}
            </td>
          </tr>`
        : nothing}
    `;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    return html`
      <div class="box" data-testid="av-box">
        <h3>Availability at this branch</h3>
        ${!this.seamWired
          ? html`<div class="warn" data-testid="av-unwired">
              Stock availability is not being reported on this deployment: every line reads as "not reported", which is not the same as "none in stock".
            </div>`
          : nothing}
        ${this.loaded && this.lines.length === 0
          ? html`<p class="dim" data-testid="av-empty">This quote has no lines yet.</p>`
          : this.lines.length > 0
            ? html`<table>
                <thead>
                  <tr>
                    <th>Line</th>
                    <th class="num">Quoted</th>
                    <th>Availability</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  ${this.lines.map((l) => this.renderLine(l))}
                </tbody>
              </table>`
            : nothing}
        ${this.error !== "" ? html`<p class="err" role="alert" data-testid="av-error">${this.error}</p>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-availability": QdQuoteAvailability;
  }
}
