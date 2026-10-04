// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-assist.ts;
// adapted for HH.
//
//   <qd-quote-assist quote-id="..." branch-id="..."></qd-quote-assist>
//
// TWO PANELS, ONE ELEMENT, BECAUSE THEY ARE ONE CONVERSATION. A rep pastes
// what the contractor sent, confirms the lines they recognise, and is then
// told what the yard's own installation rules say is missing from what they
// just built.
//
// WHAT THIS SURFACE WILL NOT DO: add a line by itself. Every proposal arrives
// UNTICKED, the confirm button counts only ticked and completable rows, and
// the add itself goes through the client's addLine (the audited, priced, stock
// holding write a hand typed line goes through), one request per line so a
// partial failure is attributable. The decisions live in _quote-assist.ts.
//
// SUGGESTIONS ARE role="status". A scope gap is advice: nothing has failed, so
// nothing here is role="alert". The ONE role="alert" on this element is the
// request failure line.
//
// THE AI HELPER. The contract has no availability read, so the helper starts
// available and unticked. When the server answers an extraction asked with
// use_ai true by source deterministic and an ai_note, the helper latches
// DISABLED (grey, unticked, no fill) with the server's note shown verbatim.
// The plain parser's proposals still arrive, unticked, as the real answer.
// Nothing is ever mocked or defaulted.
//
// THE FIND BUTTON. As in the ERP, a gap's Find puts its suggest_query in the
// paste box and reads it, which lands the rep in the same review list. The
// edit panel has no hook for its add line search, so nothing fires there.
//
// The templates carry no property or event bindings: the base delegates by
// data-action and data-field, and uncontrolled fields are synced after render.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { quoteErrorMessage } from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import {
  AI_OFF_HINT,
  interruptedAddText,
  READ_LABEL,
  addPayloads,
  addResultText,
  aiHelperLatched,
  blockedReason,
  canConfirm,
  confidenceLabel,
  confirmButtonLabel,
  editedProposal,
  extractStatusText,
  gapEmptyText,
  gapStatusText,
  sourceText,
  toggleRef,
  visibleFindings,
  withAlternate,
  type Extraction,
  type Finding,
  type Proposal,
  type ScopeGaps,
} from "./_quote-assist.js";

@customElement("qd-quote-assist")
export class QdQuoteAssist extends PanelBase {
  @state() private text = "";
  @state() private extraction: Extraction | null = null;
  @state() private proposals: Proposal[] = [];
  /** THE CONFIRM SET, AND IT STARTS EMPTY. Nothing is added that is not in here. */
  @state() private selected: string[] = [];
  @state() private reading = false;
  @state() private adding = false;
  @state() private readError = "";
  @state() private addNote = "";
  @state() private useAi = false;
  @state() private aiLatched = false;

  @state() private gaps: ScopeGaps | null = null;
  @state() private dismissed: string[] = [];
  @state() private gapsError = "";

  private gapsSeq = 0;
  private lastQuote = "";
  /** The epoch of the context the flags below belong to: a new one means a read or an add still out will not answer here. */
  private lastEpoch = -1;

  static styles = [
    panelStyles,
    css`
      :host > div {
        display: flex;
        flex-direction: column;
        gap: 12px;
      }
      .box.advice {
        border-left: 3px solid var(--hos-stone-amber, var(--hh-accent));
      }
      .kicker {
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
        font-size: var(--qp-caption);
        letter-spacing: 0.04em;
        text-transform: uppercase;
        margin: 0 0 6px;
      }
      p {
        margin: 0 0 8px;
      }
      textarea {
        width: 100%;
        box-sizing: border-box;
        min-height: 84px;
        font: inherit;
        font-size: var(--qp-body);
        color: var(--hos-text, var(--hh-text));
        background: var(--hos-deep-earth, var(--hh-page));
        border: 1px solid var(--hos-control-line, var(--hh-control-line));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        padding: 8px;
        resize: vertical;
      }
      textarea:focus-visible {
        outline: 2px solid var(--hos-stone-amber-text, var(--hh-accent-text));
        outline-offset: 2px;
      }
      .row {
        margin-top: 8px;
      }
      button.primary {
        background: var(--hos-slate-warm, var(--hh-card));
        border-color: var(--hos-stone-amber, var(--hh-accent));
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
      }
      button.primary:hover:not(:disabled) {
        background: var(--hos-slate-warm, var(--hh-card));
        border-color: var(--hos-stone-amber-hover, var(--hh-accent-hover));
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
      }
      button.link {
        background: none;
        border: none;
        padding: 2px 4px;
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
        text-decoration: underline;
      }
      button.link:hover:not(:disabled) {
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
      }
      ul {
        list-style: none;
        margin: 8px 0 0;
        padding: 0;
        display: flex;
        flex-direction: column;
        gap: 8px;
      }
      li.proposal {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: 6px;
        padding: 8px 10px;
      }
      li.proposal[data-confirmable="false"] {
        border-style: dashed;
      }
      .phead {
        display: flex;
        gap: 8px;
        align-items: flex-start;
      }
      .pbody {
        flex: 1;
        min-width: 0;
      }
      .pbody label {
        display: inline;
        margin: 0;
        font-size: var(--qp-body);
        color: var(--hos-text, var(--hh-text));
      }
      .pname {
        font-weight: 600;
      }
      .sku {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
      }
      .said {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        font-style: italic;
        margin-top: 2px;
      }
      .chip {
        display: inline-block;
        font-size: var(--qp-caption);
        letter-spacing: 0.03em;
        text-transform: uppercase;
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: 999px;
        padding: 1px 7px;
        color: var(--hos-text-dim, var(--hh-text-dim));
        white-space: nowrap;
      }
      .chip.high {
        border-color: var(--hos-stone-amber, var(--hh-accent));
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
      }
      .qty {
        display: flex;
        gap: 6px;
        align-items: center;
        margin-top: 6px;
        flex-wrap: wrap;
      }
      input[type="text"] {
        width: 6.5rem;
      }
      input[type="checkbox"] {
        margin-top: 3px;
        width: 16px;
        height: 16px;
      }
      ul.why {
        margin: 6px 0 0;
        padding-left: 16px;
        gap: 2px;
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        list-style: none;
      }
      .note {
        color: var(--hos-stone-amber-text, var(--hh-accent-text));
        font-size: var(--qp-caption);
        margin-top: 4px;
      }
      li.unmatched {
        border: 1px dashed var(--hos-line, var(--hh-line));
        border-radius: 6px;
        padding: 7px 10px;
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      li.unmatched .said {
        color: var(--hos-text, var(--hh-text));
        font-style: normal;
      }
      li.finding {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-left: 3px solid var(--hos-stone-amber, var(--hh-accent));
        border-radius: 6px;
        padding: 8px 10px;
      }
      .fhead {
        display: flex;
        gap: 8px;
        align-items: baseline;
        justify-content: space-between;
      }
      .ftitle {
        font-weight: 600;
      }
      .because {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
        margin-top: 4px;
      }
      label.switch {
        display: inline-flex;
        gap: 6px;
        align-items: center;
        margin: 0;
        font-size: var(--qp-body);
        color: var(--hos-text, var(--hh-text));
      }
      label.switch.latched {
        color: var(--hh-disabled-ink);
      }
      .ainote {
        color: var(--hos-text-dim, var(--hh-text-dim));
        font-size: var(--qp-caption);
      }
      .source {
        font-size: var(--qp-caption);
        color: var(--hos-text-dim, var(--hh-text-dim));
        margin: 6px 0 0;
      }
    `,
  ];

  protected override willUpdate(changed: Map<string, unknown>): void {
    if (changed.has("quoteId") || changed.has("client")) {
      // Leaving a quote and coming back inside one update is a new context too: what was out for the first visit is gone.
      const epoch = this.ctx.epoch;
      if (epoch !== this.lastEpoch) {
        this.lastEpoch = epoch;
        this.reading = false;
        this.adding = false;
      }
      if (this.quoteId !== this.lastQuote) {
        // A different quote is a different conversation: the proposals, the ticks and the
        // dismissals all belong to the quote they were made against.
        this.lastQuote = this.quoteId;
        this.extraction = null;
        this.proposals = [];
        this.selected = [];
        this.dismissed = [];
        this.addNote = "";
        this.readError = "";
        // The pasted job description belongs to the quote it was pasted for.
        this.text = "";
        // A read or an add still out for the last quote will never answer here: its flags go with it.
        this.reading = false;
        this.adding = false;
        this.gaps = null;
        this.gapsError = "";
      }
      this.reload();
    }
  }

  protected override updated(): void {
    // Uncontrolled fields follow the state's value unless they already hold it.
    this.syncValue("#jobtext", this.text);
    for (const p of this.proposals) {
      this.syncValue(`[data-field="qty"][data-ref="${p.ref}"]`, p.qty_entered ?? "");
      this.syncValue(`[data-field="uom"][data-ref="${p.ref}"]`, p.uom_entered ?? "");
      const tick = this.renderRoot.querySelector<HTMLInputElement>(`[data-field="tick"][data-ref="${p.ref}"]`);
      const want = this.selected.includes(p.ref);
      if (tick !== null && tick.checked !== want) tick.checked = want;
    }
    const helper = this.renderRoot.querySelector<HTMLInputElement>('[data-field="use-ai"]');
    if (helper !== null && helper.checked !== (this.useAi && !this.aiLatched)) helper.checked = this.useAi && !this.aiLatched;
  }

  /** The builder's quote-changed fan-out calls this: the suggestions re-run against what the quote holds. */
  reload(): void {
    void this.loadGaps();
  }

  // ── read the description ──────────────────────────────────────────────────────────────────────

  private async readDescription(): Promise<void> {
    const client = this.client;
    if (client === null || this.reading) return;
    const ctx = this.ctx;
    this.readError = "";
    this.addNote = "";
    this.reading = true;
    const asked = this.useAi && !this.aiLatched;
    try {
      const result = await ctx.guarded(client.assistExtract(this.text, this.branchId, asked));
      if (result === undefined) return;
      if (!result.ok) {
        this.readError = spokenRefusal(quoteErrorMessage(result.status, result.error, "read that description"));
        return;
      }
      this.extraction = result.value;
      this.proposals = result.value.proposals;
      // AND THE TICKS STAY EMPTY. This is the line a reader should check first.
      this.selected = [];
      if (aiHelperLatched(asked, result.value)) {
        this.aiLatched = true;
        this.useAi = false;
      }
    } finally {
      if (!ctx.stale) this.reading = false;
    }
  }

  private async confirmSelected(): Promise<void> {
    const client = this.client;
    const ex = this.extraction;
    if (client === null || ex === null) return;
    const payloads = addPayloads({ ...ex, proposals: this.proposals }, this.selected);
    if (payloads.length === 0) return;
    const ctx = this.ctx;
    this.adding = true;
    this.addNote = "";
    const failures: Array<{ ref: string; message: string }> = [];
    let added = 0;
    try {
      for (const p of payloads) {
        // ONE REQUEST PER LINE through the audited write: each line is priced, holds stock and
        // writes its own audit row, and a partial failure names the line that caused it.
        // The add that is out when the person leaves has usually landed: its answer is counted even though it is dropped.
        const result = await ctx.guarded(client.addLine(ctx.id, p.product_id, p.qty_entered, p.uom_entered), (late) => {
          if (late.ok) added += 1;
        });
        if (result === undefined) {
          // The person opened another quote: the loop stops, no line goes to it, and what landed is said.
          this.addNote = interruptedAddText(added, payloads.length);
          if (added > 0) this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: ctx.id }, bubbles: true, composed: true }));
          return;
        }
        if (result.ok) {
          added += 1;
          this.selected = this.selected.filter((r) => r !== p.ref);
          this.proposals = this.proposals.filter((x) => x.ref !== p.ref);
          continue;
        }
        failures.push({ ref: p.ref, message: `${p.description}: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "add this line"))}` });
      }
    } finally {
      if (!ctx.stale) this.adding = false;
    }
    this.addNote = addResultText(added, failures);
    if (added > 0) {
      // The quote's own panels refresh, and the suggestions re-run against what the quote now
      // holds, which is the moment the jointing sand suggestion appears or goes away.
      this.announceChanged();
      void this.loadGaps();
    }
  }

  // ── the scope suggestions ─────────────────────────────────────────────────────────────────────

  private async loadGaps(): Promise<void> {
    const client = this.client;
    const ctx = this.ctx;
    if (client === null || ctx.id === "") {
      this.gaps = null;
      this.gapsError = "";
      return;
    }
    const seq = ++this.gapsSeq;
    const result = await ctx.guarded(client.assistScopeGaps(ctx.id));
    if (result === undefined || seq !== this.gapsSeq) return;
    if (!result.ok) {
      // A suggestion read that cannot load is quiet, not loud: nothing the rep is doing has
      // failed, so this is a status line and never an alert.
      this.gaps = null;
      this.gapsError = spokenRefusal(quoteErrorMessage(result.status, result.error, "read the scope suggestions"));
      return;
    }
    this.gapsError = "";
    this.gaps = result.value;
  }

  /**
   * suggestAdd is the one click add: it drops the rule's own search term into the paste box and
   * reads it, which lands the rep in the SAME review list every other suggestion goes through.
   * Deliberately NOT a direct add: the rep picks the product and the quantity.
   */
  private suggestAdd(f: Finding): void {
    if (f.suggest_query === "") return;
    this.text = f.suggest_query;
    void this.readDescription();
    this.renderRoot.querySelector<HTMLTextAreaElement>("#jobtext")?.focus();
  }

  // ── host delegation ───────────────────────────────────────────────────────────────────────────

  protected override onClickAction(action: string, control: HTMLElement): void {
    const ref = control.getAttribute("data-ref") ?? "";
    const code = control.getAttribute("data-code") ?? "";
    switch (action) {
      case "read":
        void this.readDescription();
        break;
      case "confirm":
        void this.confirmSelected();
        break;
      case "reject":
        this.proposals = this.proposals.filter((x) => x.ref !== ref);
        this.selected = this.selected.filter((r) => r !== ref);
        break;
      case "alternate":
        this.proposals = withAlternate(this.proposals, ref, control.getAttribute("data-product") ?? "");
        this.selected = this.selected.filter((r) => r !== ref);
        break;
      case "dismiss":
        if (!this.dismissed.includes(code)) this.dismissed = [...this.dismissed, code];
        break;
      case "undismiss":
        this.dismissed = [];
        break;
      case "find": {
        const f = this.gaps?.findings.find((x) => x.rule_code === code);
        if (f !== undefined) this.suggestAdd(f);
        break;
      }
    }
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    const value = (control as HTMLInputElement).value;
    const ref = control.getAttribute("data-ref") ?? "";
    if (field === "text") this.text = value;
    else if (field === "qty" || field === "uom") {
      const patch = field === "qty" ? { qty_entered: value } : { uom_entered: value };
      this.proposals = this.proposals.map((p) => (p.ref === ref ? editedProposal(p, patch) : p));
      // A row that stops being completable drops out of the confirm set.
      const row = this.proposals.find((p) => p.ref === ref);
      if (row !== undefined && !canConfirm(row)) this.selected = this.selected.filter((r) => r !== ref);
    }
  }

  protected override onFieldChange(field: string, control: HTMLElement): void {
    const checked = (control as HTMLInputElement).checked;
    if (field === "use-ai") this.useAi = checked && !this.aiLatched;
    else if (field === "tick") {
      const ref = control.getAttribute("data-ref") ?? "";
      const row = this.proposals.find((p) => p.ref === ref);
      if (row !== undefined && canConfirm(row)) this.selected = toggleRef(this.selected, ref);
    }
  }

  // ── render ────────────────────────────────────────────────────────────────────────────────────

  private renderProposal(p: Proposal) {
    const confirmable = canConfirm(p);
    const reason = blockedReason(p);
    return html`<li class="proposal" data-testid="qa-proposal" data-confirmable=${confirmable ? "true" : "false"} data-ref=${p.ref}>
      <div class="phead">
        <div class="pbody">
          <label>
            <input type="checkbox" data-field="tick" data-ref=${p.ref} ?disabled=${!confirmable} />
            <span class="pname">${p.name}</span>
            <span class="sku"> · ${p.sku}</span>
          </label>
          <div class="said">you wrote: “${p.source_text}”</div>
          <div class="qty">
            <span class="${`chip${p.confidence === "HIGH" ? " high" : ""}`}">${confidenceLabel(p.confidence)}</span>
            <label class="inline"><span class="sr">Quantity for ${p.name}</span>
              <input type="text" data-field="qty" data-ref=${p.ref} inputmode="decimal" placeholder="qty" /></label>
            <label class="inline"><span class="sr">Unit for ${p.name}</span>
              <input type="text" data-field="uom" data-ref=${p.ref} placeholder="unit" /></label>
            <button type="button" class="link" data-action="reject" data-ref=${p.ref}>Not this</button>
          </div>
          ${p.unit_note !== undefined && p.unit_note !== "" ? html`<div class="note">${p.unit_note}</div>` : nothing}
          ${reason !== "" ? html`<div class="note">${reason}</div>` : nothing}
          <ul class="why">
            ${(p.why ?? []).map((w) => html`<li>${w}</li>`)}
          </ul>
          ${(p.alternates ?? []).length > 0
            ? html`<div class="dim">
                or:
                ${p.alternates.map(
                  (a) => html`<button type="button" class="link" data-action="alternate" data-ref=${p.ref} data-product=${a.product_id}>${a.name}</button>`,
                )}
              </div>`
            : nothing}
        </div>
      </div>
    </li>`;
  }

  private renderExtract() {
    const ex: Extraction | null = this.extraction === null ? null : { ...this.extraction, proposals: this.proposals };
    const payloads = addPayloads(ex, this.selected);
    const unmatched = ex?.unmatched ?? [];
    const note = this.aiLatched ? (this.extraction?.ai_note ?? "") : "";
    return html`<section class="box" data-testid="qa-extract">
      <h3>Read a job description</h3>
      <p class="dim">Paste what the contractor sent and I will suggest quote lines from the catalogue. <strong>Nothing is added until you tick it.</strong></p>
      <label class="sr" for="jobtext">Job description</label>
      <textarea
        id="jobtext"
        data-field="text"
        placeholder="e.g. 480 sq ft of Holland Stone for the back patio, 12 yd of clear stone, 6 bags of polymeric sand"
      ></textarea>
      <div class="row">
        <button type="button" class="primary" data-action="read" ?disabled=${this.reading || this.text.trim() === ""}>
          ${this.reading ? "Reading..." : READ_LABEL}
        </button>
        <label class="${`switch${this.aiLatched ? " latched" : ""}`}">
          <input type="checkbox" data-field="use-ai" data-testid="qa-ai" ?disabled=${this.aiLatched} />
          Use AI to help split the text
        </label>
        ${note !== "" ? html`<span class="ainote" data-testid="qa-ai-note">${note}</span>` : nothing}
      </div>
      <p class="dim">${AI_OFF_HINT}</p>
      ${this.readError !== "" ? html`<p class="err" role="alert">${this.readError}</p>` : nothing}
      ${/* The proposal list's own live region: status, never alert, and it names BOTH counts. */ ""}
      <p class="sr" role="status">${extractStatusText({ loading: this.reading, error: this.readError, extraction: ex })}</p>
      ${ex !== null ? html`<p class="source" data-testid="qa-source">${sourceText(ex)}</p>` : nothing}
      ${this.proposals.length > 0
        ? html`
            <ul>
              ${this.proposals.map((p) => this.renderProposal(p))}
            </ul>
            <div class="row">
              <button type="button" class="primary" data-action="confirm" data-testid="qa-confirm" ?disabled=${this.adding || payloads.length === 0}>
                ${this.adding ? "Adding..." : confirmButtonLabel(ex, this.selected)}
              </button>
              <span class="dim flush">${this.selected.length} of ${this.proposals.length} ticked</span>
            </div>
          `
        : nothing}
      ${this.addNote !== "" ? html`<p class="note" role="status" data-testid="qa-add-note">${this.addNote}</p>` : nothing}
      ${unmatched.length > 0
        ? html`<div class="gap-m" data-testid="qa-unmatched">
            <div class="kicker">couldn't match</div>
            <p class="dim">These stayed here rather than disappearing: add them by hand, or reword and read again.</p>
            <ul>
              ${unmatched.map(
                (u) => html`<li class="unmatched">
                  <div class="said">${u.source_text}</div>
                  <div>${u.reason}</div>
                </li>`,
              )}
            </ul>
          </div>`
        : nothing}
      ${ex !== null && this.proposals.length === 0 && unmatched.length === 0 && this.addNote === ""
        ? html`<p class="dim gap-m">
            Nothing in that text matched the catalogue. Try naming the product the way the catalogue does (a SKU always works), or add the lines by hand.
          </p>`
        : nothing}
    </section>`;
  }

  private renderFinding(f: Finding) {
    return html`<li class="finding" data-testid="qa-finding" data-rule=${f.rule_code}>
      <div class="fhead">
        <span class="ftitle">${f.title}</span>
        <button type="button" data-action="dismiss" data-code=${f.rule_code} aria-label=${`Dismiss the suggestion: ${f.title}`}>Dismiss</button>
      </div>
      <p style="margin:4px 0 0">${f.advice}</p>
      ${f.triggered_by.length > 0
        ? html`<div class="because">because of ${f.triggered_by.map((l) => `line ${l.line_no} · ${l.name}`).join(", ")}</div>`
        : nothing}
      ${f.suggest_query !== ""
        ? html`<div class="row">
            <button type="button" data-action="find" data-code=${f.rule_code}>Find “${f.suggest_query}”</button>
            <span class="dim flush">opens the same review list: you still pick and confirm</span>
          </div>`
        : nothing}
    </li>`;
  }

  private renderGaps() {
    if (this.gapsError !== "") {
      // Not an alert: an advisory read that cannot load is quiet.
      return html`<section class="box"><p class="dim" role="status" data-testid="qa-gaps-error">${this.gapsError}</p></section>`;
    }
    if (this.gaps === null) return nothing;
    const shown = visibleFindings(this.gaps, this.dismissed);
    const announce = gapStatusText(this.gaps, this.dismissed);
    return html`<section class="box advice" data-testid="qa-gaps">
      <div class="kicker">worth checking</div>
      <h3>Suggestions for this quote</h3>
      ${/* role="status", NEVER role="alert": a gap is advice. Polite, and silent when there is nothing to say. */ ""}
      <div role="status" aria-live="polite">
        <span class="sr">${announce}</span>
        ${shown.length > 0
          ? html`<ul>
              ${shown.map((f) => this.renderFinding(f))}
            </ul>`
          : html`<p class="dim">${gapEmptyText(this.gaps)}</p>`}
      </div>
      ${this.dismissed.length > 0 && shown.length === 0
        ? html`<p class="dim">
            ${this.dismissed.length} suggestion${this.dismissed.length === 1 ? "" : "s"} dismissed for now.
            <button type="button" class="link" data-action="undismiss">Show them again</button>
          </p>`
        : nothing}
    </section>`;
  }

  override render() {
    if (this.quoteId === "") return nothing;
    return html`<div>${this.renderExtract()} ${this.renderGaps()}</div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-assist": QdQuoteAssist;
  }
}
