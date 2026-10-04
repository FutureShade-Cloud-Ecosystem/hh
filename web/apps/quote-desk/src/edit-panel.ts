// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-edit.ts;
// adapted for HH.
//
// The staff quote edit panel: a quote's lines with the controls the SERVER
// says this caller may use right now, the expiry field, the add line form
// with catalogue search, share links (list, mint, revoke), a line's price
// alternatives, and the convert control with the fulfilment choice.
//
// Nothing here evaluates the edit state, the expiry window or who may do
// what. editable_fields and can_convert ride the quote read, computed by the
// server from the rules its write path enforces, so a control can never be
// offered for an edit the server would refuse. A missing offer means nothing
// is offered. The below floor refusal on the discount and quantity arms is
// the typed 409 (QuoteFloorRefusal): the plain message shows here and the
// same qd-floor-refusal event the work area fires opens the dialog the third
// coder builds.
//
// The templates carry no property or event bindings (the app's binding
// discipline): the panel base delegates by data-action and data-field, and
// the uncontrolled fields are set from state in updated().
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { money } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import { nextActive } from "@hardscapehouse/pos-kit/src/ported/_customer-picker.js";
import "./panel-seams.js";
import { SEAM_PAYMENT } from "./panel-seams.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import {
  canConvert,
  canEdit,
  catalogHitStatus,
  CATALOGUE_MIN_QUERY,
  comparisonBasisWord,
  comparisonDeltaLabel,
  EDIT_LINE_ADD,
  EDIT_LINE_QTY,
  EDIT_LINE_REMOVE,
  EDIT_VALID_UNTIL,
  expiryLabel,
  frozenSentence,
  hasAnyEdit,
  isShareable,
  quoteErrorMessage,
  quoteStatusWord,
  shareLinkState,
  shareSentence,
  validateDate,
  validateDiscPct,
  validateQty,
  type QuoteView,
  type QuoteViewLine,
} from "./_quote-edit.js";
import { floorRefusalMessage, openFloorRefusalDialog, parseFloorRefusal } from "./floor-refusal.js";
import { spokenRefusal } from "./_quote-workarea.js";
import type { CatalogHit, ComparisonRow, ShareLinkRow } from "./_panel-wire.js";
import type { Result } from "@hardscapehouse/api-client";

type Which = "add" | "compare";

/** The mint's own notice: it is said beside the button that made it, not at the foot of the panel. */
const LINK_CREATED_NOTICE = "Link created. Nothing was emailed: copy the link and send it yourself.";

@customElement("qd-quote-edit")
export class QdQuoteEdit extends PanelBase {
  @state() private view: QuoteView | null = null;
  /** The discount each line carries, from the terms read (the quote view itself carries none). */
  @state() private discByLine: Record<number, number> = {};
  @state() private discEnabled = false;
  @state() private qtyDraft: Record<number, string> = {};
  @state() private discDraft: Record<number, string> = {};
  @state() private expiryDraft = "";
  @state() private addQuery = "";
  @state() private hits: CatalogHit[] = [];
  @state() private addActive = -1;
  @state() private picked: CatalogHit | null = null;
  @state() private addQty = "";
  @state() private links: ShareLinkRow[] = [];
  @state() private mintedToken = "";
  /** The id of the link that token belongs to, when the list names it: revoking that link clears the token. */
  private mintedLinkId = "";
  @state() private compareLine: number | null = null;
  @state() private compareRows: ComparisonRow[] = [];
  @state() private compareQuery = "";
  @state() private compareHits: CatalogHit[] = [];
  @state() private compareActive = -1;
  @state() private compareAlts: CatalogHit[] = [];
  @state() private convertFulfilment = "";
  @state() private converted: { orderId: string; orderNo: string } | null = null;

  static styles = [
    panelStyles,
    css`
      .frozen {
        color: var(--hos-text-dim, var(--hh-text-dim));
        margin: 6px 0 0;
      }
      .sentence {
        margin: 0;
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      .floor {
        color: var(--hos-state-watch, var(--hh-state-watch));
        font-size: var(--qp-caption);
      }
      .blocked {
        color: var(--hos-safety-red, var(--hh-danger));
      }
      td.actions {
        white-space: nowrap;
        text-align: right;
      }
      td.actions button + button {
        margin-left: 4px;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // Everything a person left on the last quote stays with that quote: the
      // outcome lines, the half typed add box, the open comparison, the link.
      this.error = "";
      this.notice = "";
      this.mintedToken = "";
      this.mintedLinkId = "";
      this.view = null;
      this.links = [];
      this.converted = null;
      this.compareLine = null;
      this.compareRows = [];
      this.compareAlts = [];
      this.compareHits = [];
      this.compareQuery = "";
      this.addQuery = "";
      this.addQty = "";
      this.addActive = -1;
      this.hits = [];
      this.picked = null;
      this.reload();
    }
  }

  reload(): void {
    void this.load();
  }

  // ── reads ─────────────────────────────────────────────────────────────────

  /**
   * A read is only ever applied to the quote that asked for it: a slow answer
   * for the quote that was open before is dropped, never shown over the one
   * open now, and a save can only be made from a view of the open quote.
   */
  private loadSeq = 0;

  private async load(): Promise<void> {
    const client = this.client;
    if (client === null || this.quoteId === "") {
      this.loadSeq += 1;
      this.view = null;
      return;
    }
    const seq = ++this.loadSeq;
    const result = await client.readQuote(this.quoteId);
    if (seq !== this.loadSeq) return;
    if (!result.ok) {
      this.error = this.refuse(result, "read this quote");
      this.view = null;
      return;
    }
    this.view = result.value;
    // Drafts return to the server's truth after every load, so a refused edit
    // never leaves a stale value sitting in a box looking saved. The one
    // exception is the box under the person's hands: a late re-read must not
    // wipe the figure being typed.
    const typing = this.typingField();
    this.qtyDraft = typing !== null && typing.field === "qty" ? { [typing.line]: typing.value } : {};
    this.discDraft = typing !== null && typing.field === "disc" ? { [typing.line]: typing.value } : {};
    this.expiryDraft = typing !== null && typing.field === "expiry" ? typing.value : (result.value.valid_until ?? "");
    await Promise.all([this.loadDiscounts(seq), this.loadLinks(seq)]);
  }

  /** The quantity, discount or expiry box holding focus, with what is in it, or null. */
  private typingField(): { field: "qty" | "disc" | "expiry"; line: number; value: string } | null {
    const active = this.renderRoot.querySelector<HTMLInputElement>('[data-field="qty"]:focus, [data-field="disc"]:focus, [data-field="expiry"]:focus');
    if (active === null) return null;
    const field = active.getAttribute("data-field");
    return {
      field: field === "disc" ? "disc" : field === "expiry" ? "expiry" : "qty",
      line: Number(active.getAttribute("data-line") ?? "0"),
      value: active.value,
    };
  }

  /**
   * The discount arm's facts: each line's current discount (the terms read)
   * and the dealer's per line discount switch (the surface config). Both are
   * the server's answers; an unreadable answer offers no discount control.
   */
  private async loadDiscounts(seq: number): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const [terms, config] = await Promise.all([client.readTerms(this.quoteId), client.surfaceConfig(this.branchId)]);
    if (seq !== this.loadSeq) return;
    const byLine: Record<number, number> = {};
    if (terms.ok) for (const line of terms.value.lines) byLine[line.lineNo] = line.discPct;
    this.discByLine = byLine;
    this.discEnabled = terms.ok && config.ok && config.value.policy?.lineDiscountsEnabled !== false;
  }

  private async loadLinks(seq: number): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.listShareLinks(this.quoteId);
    if (seq !== this.loadSeq) return;
    this.links = result.ok ? result.value : [];
  }

  // ── writes ────────────────────────────────────────────────────────────────

  /** One refused answer as a sentence a person reads. */
  private refuse(result: Extract<Result<unknown>, { ok: false }>, what: string): string {
    return spokenRefusal(quoteErrorMessage(result.status, result.error, what));
  }

  /**
   * One guarded write. The panel re-reads on BOTH outcomes: a refusal usually
   * means the quote moved underneath it (accepted, lapsed), and the controls
   * must show the server's new truth, not keep offering an edit that will now
   * always fail. A typed floor refusal shows its plain message and fires the
   * dialog event; any other refusal shows the server's own sentence.
   */
  private async write(
    what: string,
    action: () => Promise<Result<unknown>>,
    floor?: { lineNo: number; discPct: number },
  ): Promise<boolean> {
    if (this.busy || this.client === null) return false;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const quoteAtStart = this.quoteId;
    const result = await action();
    this.busy = false;
    // The quote the person moved to meanwhile owns the screen now: this answer says nothing about it.
    if (quoteAtStart !== this.quoteId) return result.ok;
    if (!result.ok) {
      const refusal = result.status === 409 && floor !== undefined ? parseFloorRefusal(result.error) : null;
      if (refusal !== null && floor !== undefined) {
        this.error = floorRefusalMessage(refusal);
        openFloorRefusalDialog(refusal, this.quoteId, floor.lineNo, floor.discPct);
      } else {
        this.error = this.refuse(result, what);
      }
      await this.load();
      return false;
    }
    this.notice = "Saved.";
    await this.load();
    this.announceChanged();
    return true;
  }

  private async saveLine(line: QuoteViewLine): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const rawQty = (this.qtyDraft[line.line_no] ?? line.qty_entered).trim();
    const qtyChanged = rawQty !== line.qty_entered;
    const current = this.discByLine[line.line_no] ?? 0;
    const rawDisc = (this.discDraft[line.line_no] ?? (current > 0 ? String(current) : "")).trim();
    const discChanged = rawDisc !== (current > 0 ? String(current) : "");
    if (!qtyChanged && !discChanged) {
      this.notice = "Nothing to save on this line.";
      this.error = "";
      return;
    }
    if (qtyChanged) {
      const bad = validateQty(rawQty);
      if (bad !== null) {
        this.error = bad;
        return;
      }
    }
    if (discChanged) {
      const bad = validateDiscPct(rawDisc);
      if (bad !== null) {
        this.error = bad;
        return;
      }
    }
    if (qtyChanged) {
      const saved = await this.write("change this quantity", () => client.editLine(this.quoteId, line.line_no, { qty: rawQty }), {
        lineNo: line.line_no,
        discPct: current,
      });
      if (!saved || !discChanged) return;
    }
    if (discChanged) {
      const n = Number(rawDisc === "" ? 0 : rawDisc);
      await this.write("change this discount", () => client.editLine(this.quoteId, line.line_no, { discPct: n }), {
        lineNo: line.line_no,
        discPct: n,
      });
    }
  }

  private async removeLine(line: QuoteViewLine): Promise<void> {
    const client = this.client;
    if (client === null) return;
    await this.write("remove this line", () => client.removeLine(this.quoteId, line.line_no));
  }

  private async saveExpiry(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const bad = validateDate(this.expiryDraft);
    if (bad !== null) {
      this.error = bad;
      return;
    }
    await this.write("change the expiry date", () => client.setValidUntil(this.quoteId, this.expiryDraft.trim()));
  }

  private async addLine(): Promise<void> {
    const client = this.client;
    const picked = this.picked;
    if (client === null) return;
    if (picked === null) {
      this.error = "Pick a product first.";
      return;
    }
    const bad = validateQty(this.addQty);
    if (bad !== null) {
      this.error = bad;
      return;
    }
    const saved = await this.write("add this line", () => client.addLine(this.quoteId, picked.product_id, this.addQty.trim(), picked.base_uom));
    if (saved) {
      this.picked = null;
      this.addQty = "";
      this.addQuery = "";
      this.hits = [];
    }
  }

  // ── catalogue search (the add box and the compare box) ────────────────────

  private async search(which: Which, raw: string): Promise<void> {
    const client = this.client;
    if (which === "add") {
      this.addQuery = raw;
      this.addActive = -1;
    } else {
      this.compareQuery = raw;
      this.compareActive = -1;
    }
    const q = raw.trim();
    if (client === null || q.length < CATALOGUE_MIN_QUERY) {
      if (which === "add") this.hits = [];
      else this.compareHits = [];
      return;
    }
    const result = await client.searchProducts(q, which === "add" ? 10 : 8);
    // A slower answer for an older query never replaces the newer one.
    if ((which === "add" ? this.addQuery : this.compareQuery).trim() !== q) return;
    const rows = result.ok ? result.value : [];
    if (which === "add") this.hits = rows;
    else this.compareHits = rows;
  }

  private chooseHit(hit: CatalogHit, which: Which): void {
    if (which === "add") {
      this.picked = hit;
      this.hits = [];
      this.addActive = -1;
      return;
    }
    if (!this.compareAlts.some((a) => a.product_id === hit.product_id)) this.compareAlts = [...this.compareAlts, hit];
    this.compareHits = [];
    this.compareActive = -1;
  }

  /**
   * The keyboard path for both catalogue boxes: one handler, so the two
   * answer the same keystroke the same way. Enter on nothing highlighted only
   * moves the highlight to the first row (committing the top hit for someone
   * still reading is how the wrong paver lands on a quote); Escape closes
   * without choosing; Tab leaves without choosing.
   */
  protected override onFieldKey(field: string, _control: HTMLElement, event: KeyboardEvent): void {
    if (field !== "add-search" && field !== "cmp-search") return;
    const which: Which = field === "add-search" ? "add" : "compare";
    const rows = which === "add" ? this.hits : this.compareHits;
    const active = which === "add" ? this.addActive : this.compareActive;
    const setActive = (n: number): void => {
      if (which === "add") this.addActive = n;
      else this.compareActive = n;
    };
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      if (rows.length === 0) return;
      event.preventDefault();
      setActive(nextActive(active, event.key === "ArrowDown" ? 1 : -1, rows.length));
      return;
    }
    if (event.key === "Enter") {
      if (rows.length === 0) return;
      event.preventDefault();
      const row = rows[active];
      if (active >= 0 && row !== undefined) this.chooseHit(row, which);
      else setActive(0);
      return;
    }
    if (event.key === "Escape") {
      if (rows.length === 0) return;
      event.stopPropagation();
      if (which === "add") this.hits = [];
      else this.compareHits = [];
      setActive(-1);
      return;
    }
    if (event.key === "Tab") {
      if (which === "add") this.hits = [];
      else this.compareHits = [];
      setActive(-1);
    }
  }

  // ── share links ───────────────────────────────────────────────────────────

  private shareUrl(token: string): string {
    return `${location.origin}/quote.html?t=${encodeURIComponent(token)}`;
  }

  private async mintLink(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const quoteAtStart = this.quoteId;
    const knownLinks = new Set(this.links.map((l) => l.id));
    const result = await client.mintShareLink(quoteAtStart);
    this.busy = false;
    if (quoteAtStart !== this.quoteId) return;
    if (!result.ok) {
      this.error = this.refuse(result, "create a share link");
      return;
    }
    // The raw token is returned exactly once, so it is shown now and the
    // person is told plainly that it will not be shown again. Minting moves
    // the quote to Sent and starts its follow-ups; it addresses nothing.
    this.mintedToken = result.value;
    this.notice = LINK_CREATED_NOTICE;
    await this.load();
    // The just-minted link is the live row that was not there before; revoking it must clear its URL.
    const fresh = this.links.filter((l) => l.live && !knownLinks.has(l.id));
    this.mintedLinkId = fresh.length === 1 ? (fresh[0]?.id ?? "") : "";
    this.announceChanged();
  }

  private async revokeLink(id: string): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const revoked = await this.write("revoke this link", () => client.revokeShareLink(this.quoteId, id));
    // A revoked link is dead: its address and the "copy this now" offer go with it.
    if (revoked && id === this.mintedLinkId) {
      this.mintedToken = "";
      this.mintedLinkId = "";
    }
  }

  private async copyLink(): Promise<void> {
    // The clipboard is only there on a secure page: say so rather than claim a copy that did not happen.
    try {
      if (navigator.clipboard === undefined) throw new Error("no clipboard");
      await navigator.clipboard.writeText(this.shareUrl(this.mintedToken));
    } catch {
      this.error = "The link could not be copied from this page. Select it in the box and copy it yourself.";
      this.notice = "";
      return;
    }
    this.error = "";
    this.notice = "Link copied. Nothing was emailed: paste it to send it.";
  }

  // ── compare ───────────────────────────────────────────────────────────────

  private async runCompare(lineNo: number): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.compareLine = lineNo;
    this.error = "";
    const result = await client.compareLine(this.quoteId, lineNo, this.compareAlts.map((a) => a.product_id));
    if (!result.ok) {
      this.error = this.refuse(result, "compare this line");
      return;
    }
    this.compareRows = result.value;
  }

  private closeCompare(): void {
    this.compareLine = null;
    this.compareRows = [];
    this.compareAlts = [];
    this.compareHits = [];
    this.compareQuery = "";
  }

  // ── convert ───────────────────────────────────────────────────────────────

  /**
   * Convert from the counter. The offer is the server's (can_convert); the
   * outcome is kept after the quote moves to Converted, because the order
   * number is what the counter needs to read back and the very next read
   * makes the offer disappear. A refusal is shown verbatim: a credit refusal
   * naming the limit and the shortfall IS the useful part.
   */
  private async doConvert(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy || this.quoteId === "") return;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const result = await client.convertQuote(this.quoteId, this.convertFulfilment);
    this.busy = false;
    if (!result.ok) {
      this.error = this.refuse(result, "convert this quote");
      await this.load();
      return;
    }
    this.converted = result.value;
    await this.load();
    this.announceChanged();
  }

  // ── delegation ────────────────────────────────────────────────────────────

  protected override onClickAction(action: string, control: HTMLElement): void {
    const line = Number(control.getAttribute("data-line") ?? "0");
    const row = this.view?.lines.find((l) => l.line_no === line);
    switch (action) {
      case "save-line":
        if (row !== undefined) void this.saveLine(row);
        break;
      case "remove-line":
        if (row !== undefined) void this.removeLine(row);
        break;
      case "compare-open":
        this.compareLine = line;
        this.compareRows = [];
        void this.runCompare(line);
        break;
      case "compare-score":
        if (this.compareLine !== null) void this.runCompare(this.compareLine);
        break;
      case "compare-close":
        this.closeCompare();
        break;
      case "save-expiry":
        void this.saveExpiry();
        break;
      case "add-line":
        void this.addLine();
        break;
      case "hit-add":
        this.chooseHit(this.hits[Number(control.getAttribute("data-index"))] as CatalogHit, "add");
        break;
      case "hit-compare":
        this.chooseHit(this.compareHits[Number(control.getAttribute("data-index"))] as CatalogHit, "compare");
        break;
      case "mint-link":
        void this.mintLink();
        break;
      case "copy-link":
        void this.copyLink();
        break;
      case "revoke-link":
        void this.revokeLink(control.getAttribute("data-link") ?? "");
        break;
      case "convert":
        void this.doConvert();
        break;
    }
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLInputElement).value;
    const line = Number(control.getAttribute("data-line") ?? "0");
    if (field === "qty") this.qtyDraft = { ...this.qtyDraft, [line]: value };
    if (field === "disc") this.discDraft = { ...this.discDraft, [line]: value };
    if (field === "expiry") this.expiryDraft = value;
    if (field === "add-qty") this.addQty = value;
    if (field === "add-search") void this.search("add", value);
    if (field === "cmp-search") void this.search("compare", value);
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    if (field === "fulfilment") this.convertFulfilment = (control as HTMLSelectElement).value;
  }

  // ── the uncontrolled fields ───────────────────────────────────────────────

  protected override updated(): void {
    const view = this.view;
    if (view !== null) {
      for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="qty"]')) {
        const lineNo = Number(input.getAttribute("data-line"));
        const line = view.lines.find((l) => l.line_no === lineNo);
        const want = this.qtyDraft[lineNo] ?? line?.qty_entered ?? "";
        if (input.value !== want) input.value = want;
      }
      for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="disc"]')) {
        const lineNo = Number(input.getAttribute("data-line"));
        const current = this.discByLine[lineNo] ?? 0;
        const want = this.discDraft[lineNo] ?? (current > 0 ? String(current) : "");
        if (input.value !== want) input.value = want;
      }
    }
    this.syncValue('[data-field="expiry"]', this.expiryDraft);
    this.syncValue('[data-field="add-search"]', this.addQuery);
    this.syncValue('[data-field="add-qty"]', this.addQty);
    this.syncValue('[data-field="cmp-search"]', this.compareQuery);
    this.syncValue('[data-field="fulfilment"]', this.convertFulfilment);
    this.syncValue('[data-field="link-url"]', this.mintedToken === "" ? "" : this.shareUrl(this.mintedToken));
    // Option ids carry the combobox's active descendant; ids are not bound in
    // the template, so they are set here from position.
    this.renderRoot.querySelectorAll<HTMLElement>("[data-opt]").forEach((li) => {
      li.id = `qe-${li.getAttribute("data-opt")}-opt-${li.getAttribute("data-index")}`;
    });
    const seam = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-panel-seam");
    if (seam !== null) {
      seam.quoteId = this.quoteId;
      seam.branchId = this.branchId;
      // The converted order's id, once this quote has been converted ("" before): what QD-5's payment step needs.
      seam.orderId = this.converted?.orderId ?? "";
    }
  }

  // ── render ────────────────────────────────────────────────────────────────

  private renderLines(view: QuoteView) {
    const mayQty = canEdit(view, EDIT_LINE_QTY);
    const mayRemove = canEdit(view, EDIT_LINE_REMOVE);
    const showDisc = mayQty && this.discEnabled;
    const anyDisc = view.lines.some((l) => (this.discByLine[l.line_no] ?? 0) > 0);
    const discColumn = showDisc || anyDisc;
    return html`
      <table data-testid="qe-lines">
        <thead>
          <tr>
            <th>#</th>
            <th>Product</th>
            <th class="num">Qty</th>
            <th>UoM</th>
            <th class="num">Unit</th>
            ${discColumn ? html`<th class="num">Disc %</th>` : nothing}
            <th class="num">Total</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          ${view.lines.map((l) => {
            const disc = this.discByLine[l.line_no] ?? 0;
            return html`<tr data-testid="qe-line">
              <td>${l.line_no}</td>
              <td>
                ${l.description}
                ${l.rule_price_below_floor === true ? html`<div class="floor">Rule price is below the floor</div>` : nothing}
                ${l.floor_cleared_by !== undefined ? html`<div class="floor">Floor cleared by ${l.floor_cleared_by}</div>` : nothing}
              </td>
              <td class="num">
                ${mayQty
                  ? html`<input class="qty" type="text" inputmode="decimal" data-field="qty" data-line=${l.line_no} aria-label="Quantity on line ${l.line_no}" />`
                  : l.qty_entered}
              </td>
              <td>${l.uom_entered}</td>
              <td class="num">${money(l.unit_price_cents)}</td>
              ${discColumn
                ? html`<td class="num">
                    ${showDisc
                      ? html`<input class="disc" type="text" inputmode="numeric" data-field="disc" data-line=${l.line_no} aria-label="Discount percent on line ${l.line_no}" />`
                      : disc > 0
                        ? `${disc}%`
                        : ""}
                  </td>`
                : nothing}
              <td class="num">${money(l.line_total_cents)}</td>
              <td class="actions">
                ${mayQty ? html`<button type="button" data-action="save-line" data-line=${l.line_no} ?disabled=${this.busy}>Save</button>` : nothing}
                ${mayRemove ? html`<button type="button" class="danger" data-action="remove-line" data-line=${l.line_no} ?disabled=${this.busy}>Remove</button>` : nothing}
                <button type="button" data-action="compare-open" data-line=${l.line_no}>Compare</button>
              </td>
            </tr>`;
          })}
          ${view.lines.length === 0
            ? html`<tr>
                <td colspan="8" class="dim">No lines yet.</td>
              </tr>`
            : nothing}
        </tbody>
      </table>
    `;
  }

  private renderExpiry(view: QuoteView) {
    if (!canEdit(view, EDIT_VALID_UNTIL)) return nothing;
    return html`
      <div class="row">
        <label for="vu" style="margin:0">Expiry</label>
        <input id="vu" type="date" data-field="expiry" />
        <button type="button" data-action="save-expiry" ?disabled=${this.busy}>Save expiry</button>
        <span class="picked">Clear the date to remove the expiry entirely.</span>
      </div>
    `;
  }

  private renderAdd(view: QuoteView) {
    if (!canEdit(view, EDIT_LINE_ADD)) return nothing;
    return html`
      <fieldset>
        <legend>Add a line</legend>
        <div class="row" style="margin-top:0">
          <input
            class="wide"
            type="search"
            data-field="add-search"
            role="combobox"
            aria-label="Search products"
            aria-expanded=${this.hits.length > 0 ? "true" : "false"}
            aria-controls="qe-add-list"
            aria-autocomplete="list"
            aria-activedescendant=${this.addActive >= 0 ? `qe-add-opt-${this.addActive}` : nothing}
          />
          <span class="sr" role="status" aria-live="polite">${catalogHitStatus({ query: this.addQuery, count: this.hits.length })}</span>
          <input class="qty" type="text" inputmode="decimal" placeholder="Qty" aria-label="Quantity to add" data-field="add-qty" />
          <button type="button" data-action="add-line" ?disabled=${this.busy || this.picked === null}>Add</button>
          ${this.picked !== null ? html`<span class="picked">${this.picked.sku} · ${this.picked.name} (${this.picked.base_uom})</span>` : nothing}
        </div>
        ${this.hits.length > 0
          ? html`<ul class="hits" id="qe-add-list" role="listbox" aria-label="Products">
              ${this.hits.map(
                (h, i) => html`<li role="option" data-opt="add" data-index=${i} data-action="hit-add" aria-selected=${this.addActive === i ? "true" : "false"}>
                  ${h.sku} · ${h.name}
                </li>`,
              )}
            </ul>`
          : nothing}
      </fieldset>
    `;
  }

  private renderShare(view: QuoteView) {
    const shareable = isShareable(view);
    const offered = shareable && hasAnyEdit(view);
    return html`
      <fieldset>
        <legend>Customer link</legend>
        ${offered
          ? html`<div class="row" style="margin-top:0">
              <button type="button" data-action="mint-link" ?disabled=${this.busy}>Create link to copy</button>
              <span class="picked">Nothing is emailed: copy the link and send it yourself. The customer can accept online.</span>
            </div>
            ${this.notice === LINK_CREATED_NOTICE
              ? html`<p class="ok" role="status" aria-live="polite" data-testid="qe-notice">${this.notice}</p>`
              : nothing}`
          : html`<p class="sentence" data-testid="qe-share-note">${shareable ? "Sharing is not offered to you on this quote." : shareSentence(view)}</p>`}
        ${this.mintedToken !== ""
          ? html`<div class="row">
              <input class="wide" type="text" readonly data-field="link-url" aria-label="Customer link" />
              <button type="button" data-action="copy-link">Copy</button>
              <span class="picked">Copy this now: it is not shown again.</span>
            </div>`
          : nothing}
        ${this.links.length > 0
          ? html`<table style="margin-top:8px" data-testid="qe-links">
              <thead>
                <tr>
                  <th>Issued</th>
                  <th>Expires</th>
                  <th>State</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                ${this.links.map(
                  (l) => html`<tr data-testid="qe-link">
                    <td>${new Date(l.createdAt).toLocaleDateString("en-CA")}</td>
                    <td>${new Date(l.expiresAt).toLocaleDateString("en-CA")}</td>
                    <td>${shareLinkState(l)}</td>
                    <td class="actions">
                      ${l.live
                        ? html`<button type="button" class="danger" data-action="revoke-link" data-link=${l.id} ?disabled=${this.busy}>Revoke</button>`
                        : nothing}
                    </td>
                  </tr>`,
                )}
              </tbody>
            </table>`
          : nothing}
      </fieldset>
    `;
  }

  private renderConvert(view: QuoteView) {
    // The outcome is checked first: a successful conversion moves the quote to
    // Converted, which makes the offer false on the very next read, and this
    // method re-runs after that read. Gating on the offer first would erase
    // the confirmation the instant it was earned.
    if (this.converted !== null) {
      return html`
        <fieldset>
          <legend>Convert to order</legend>
          <p class="ok" role="status" data-testid="qe-converted">Converted to order ${this.converted.orderNo}.</p>
        </fieldset>
      `;
    }
    if (!canConvert(view)) return nothing;
    return html`
      <fieldset>
        <legend>Convert to order</legend>
        <p class="sub">
          This quote is accepted. Converting it commits the order against the customer's account: their credit is checked at that
          moment, and the stock this quote is holding becomes a firm reservation.
        </p>
        <label for="convert-fulfilment">How is it leaving?</label>
        <div class="row" style="margin-top:0">
          <select id="convert-fulfilment" data-field="fulfilment" data-testid="qe-fulfilment">
            <option value="">Pickup at the counter (default)</option>
            <option value="WILL_CALL">Will-call: hold it on the rack</option>
          </select>
          <button type="button" class="primary" data-action="convert" data-testid="qe-convert" ?disabled=${this.busy}>Convert to order</button>
        </div>
      </fieldset>
    `;
  }

  private renderCompare() {
    if (this.compareLine === null) return nothing;
    const label = (r: ComparisonRow): string => {
      if (r.is_current) return "Current line";
      const alt = this.compareAlts.find((a) => a.product_id === r.product_id);
      return alt !== undefined ? `${alt.sku} · ${alt.name}` : r.product_id.slice(0, 8);
    };
    return html`
      <fieldset data-testid="qe-compare">
        <legend>Compare line ${this.compareLine}</legend>
        <div class="row" style="margin-top:0">
          <input
            class="wide"
            type="search"
            data-field="cmp-search"
            role="combobox"
            aria-label="Search alternate products"
            aria-expanded=${this.compareHits.length > 0 ? "true" : "false"}
            aria-controls="qe-cmp-list"
            aria-autocomplete="list"
            aria-activedescendant=${this.compareActive >= 0 ? `qe-compare-opt-${this.compareActive}` : nothing}
          />
          <span class="sr" role="status" aria-live="polite"
            >${catalogHitStatus({ query: this.compareQuery, count: this.compareHits.length, subject: "alternate products" })}</span
          >
          <button type="button" data-action="compare-score">Score</button>
          <button type="button" data-action="compare-close">Close</button>
          ${this.compareAlts.length > 0 ? html`<span class="picked">${this.compareAlts.map((a) => a.sku).join(", ")}</span>` : nothing}
        </div>
        ${this.compareHits.length > 0
          ? html`<ul class="hits" id="qe-cmp-list" role="listbox" aria-label="Alternate products">
              ${this.compareHits.map(
                (h, i) => html`<li role="option" data-opt="compare" data-index=${i} data-action="hit-compare" aria-selected=${this.compareActive === i ? "true" : "false"}>
                  ${h.sku} · ${h.name}
                </li>`,
              )}
            </ul>`
          : nothing}
        ${this.compareRows.length > 0
          ? html`<table style="margin-top:8px">
                <thead>
                  <tr>
                    <th>Option</th>
                    <th class="num">Unit</th>
                    <th class="num">List</th>
                    <th class="num">Line total</th>
                    <th class="num">Difference</th>
                    <th>Basis</th>
                  </tr>
                </thead>
                <tbody>
                  ${this.compareRows.map(
                    (r) => html`<tr>
                      <td>${label(r)}</td>
                      ${r.unavailable !== undefined
                        ? html`<td colspan="5" class="picked">${r.unavailable}</td>`
                        : html`
                            <td class="num">${money(r.unit_price_cents)}</td>
                            <td class="num">${money(r.list_price_cents)}</td>
                            <td class="num">${money(r.line_total_cents)}</td>
                            <td class="num">${comparisonDeltaLabel(r)}</td>
                            <td class="${r.blocked ? "blocked" : ""}">${comparisonBasisWord(r)}</td>
                          `}
                    </tr>`,
                  )}
                </tbody>
              </table>
              <p class="picked" style="margin:6px 0 0">Comparison only: nothing here changes the quote or any price.</p>`
          : nothing}
      </fieldset>
    `;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    const view = this.view;
    if (view === null) {
      return html`<div class="box">
        ${this.error !== "" ? html`<p class="err" role="alert">${this.error}</p>` : html`<p class="sub">Loading...</p>`}
      </div>`;
    }
    return html`
      <div class="box" data-testid="qe-box">
        <h3 data-testid="qe-title">${view.quote_no} · ${quoteStatusWord(view.status)}</h3>
        <div class="sub" data-testid="qe-sub">${view.customer_name} · ${expiryLabel(view)}</div>
        ${this.renderLines(view)}
        <div class="totals">
          <span>Subtotal <b>${money(view.subtotal_cents)}</b></span>
          <span>Tax <b>${money(view.tax_cents)}</b></span>
          <span>Total <b>${money(view.total_cents)}</b></span>
        </div>
        ${hasAnyEdit(view)
          ? html`${this.renderExpiry(view)}${this.renderAdd(view)}`
          : html`<p class="frozen" data-testid="qe-frozen">${frozenSentence(view)}</p>`}
        ${this.renderShare(view)}
        ${this.renderConvert(view)}
        <qd-panel-seam data-seam=${SEAM_PAYMENT} data-seam-label="Related documents and the order's payment (QD-5)"></qd-panel-seam>
        ${this.renderCompare()}
        <p class="err" role="alert" data-testid="qe-error">${this.error}</p>
        <p class="ok" role="status" aria-live="polite" aria-atomic="true" data-testid=${this.notice === LINK_CREATED_NOTICE ? nothing : "qe-notice"}>${this.notice === LINK_CREATED_NOTICE ? "" : this.notice}</p>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-edit": QdQuoteEdit;
  }
}
