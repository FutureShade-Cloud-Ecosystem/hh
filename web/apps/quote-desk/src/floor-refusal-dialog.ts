// Ported from hardscapeos_dibbits master 87d7941b app/src/fallback-modal.ts
// (the Permission Fallback modal); adapted for HH.
//
// The floor refusal dialog. It opens on the window event the work area, the
// edit panel and the guard fire (FLOOR_REFUSAL_SEAM_EVENT) and tells the
// caller what the server said: the policy that refused, who may approve
// (the approver roles exactly as the server names them, never decided here),
// the margin and floor figures only when the refusal carries them, and the
// requester's view of any request the server already holds. It FILES THE
// REQUEST through the client's requestFloorApproval door (the ERP posted a
// generic approval; this app files against the quote's line) and shows the
// request as the server returns it: pending, approved or rejected. The
// contract has no used state, so none is claimed. When the answer says the
// caller may clear the price itself, the dialog shows that and the server's
// offer and nothing more: no request was filed.
//
// A refusal that belongs to no quote (the Check Quote form's, event quote id
// empty) files nothing and says plainly it is a check only. The guard form
// has no approver roles to show, so none are invented: the dialog says the
// server named none.
//
// A real modal: role=dialog, aria-modal, labelled by its title; focus moves
// in on open, Tab stays inside, Escape and the Close button close it, a click
// on the backdrop closes it, and focus returns to what had it. The template
// carries no property or event bindings: the panel base delegates by
// data-action and data-field.
import { css, html, nothing } from "lit";
import { customElement, state } from "lit/decorators.js";
import { PanelBase } from "./panel-base.js";
import { panelStyles } from "./panel-styles.js";
import { approverWords, policyLabel, requesterStateWords } from "./_approval-copy.js";
import { quoteErrorMessage } from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { bpsToPercent } from "./_guard-format.js";
import { FLOOR_REFUSAL_CLOSED_EVENT, FLOOR_REFUSAL_SEAM_EVENT, type FloorRefusalSeamEvent, type FloorRefusalView } from "./floor-refusal.js";

/** The check-only refusal the guard raises: the seam event plus the server's own sentence, when it wrote one. */
export interface GuardRefusalEvent extends FloorRefusalSeamEvent {
  sentence?: string;
}

/**
 * openGuardRefusal: the Check Quote form's refusal. It names no quote and no
 * approver (the check answer carries neither), so the dialog files nothing.
 * The margin is the server's figure for the pair that was checked.
 */
export function openGuardRefusal(policy: string, marginBps: number | null, sentence: string): void {
  const refusal: FloorRefusalView = {
    policy,
    approverRoles: [],
    managerMaySet: false,
    requestId: null,
    requestStatus: null,
    marginBps,
    floorBps: null,
  };
  const detail: GuardRefusalEvent = { refusal, quoteId: "", lineNo: 0, discPct: 0, ...(sentence === "" ? {} : { sentence }) };
  window.dispatchEvent(new CustomEvent<GuardRefusalEvent>(FLOOR_REFUSAL_SEAM_EVENT, { detail }));
}

@customElement("qd-floor-refusal")
export class QdFloorRefusal extends PanelBase {
  @state() private open = false;
  @state() private refusal: FloorRefusalView | null = null;
  @state() private target: { quoteId: string; lineNo: number; discPct: number } | null = null;
  @state() private sentence = "";
  /** The request's state as the server last returned it: the refusal's own, then the filing answer's. */
  @state() private requestStatus: string | null = null;
  @state() private mayClear = false;
  @state() private note = "";
  private returnFocus: HTMLElement | null = null;

  static styles = [
    panelStyles,
    css`
      :host {
        display: contents;
      }
      .backdrop {
        position: fixed;
        inset: 0;
        z-index: 1000;
        display: flex;
        align-items: flex-start;
        justify-content: center;
        padding-top: 64px;
        background: color-mix(in srgb, var(--hos-deep-earth, var(--hh-page)) 62%, transparent);
      }
      .dialog {
        width: min(460px, calc(100vw - 32px));
        background: var(--hos-slate-warm, var(--hh-card));
        border: 1px solid var(--hos-line, var(--hh-line));
        border-radius: var(--hos-radius-card, var(--hh-radius-card));
        box-shadow: 0 12px 40px rgba(0, 0, 0, 0.35);
        font-size: var(--qp-body);
      }
      .head {
        display: flex;
        align-items: center;
        justify-content: space-between;
        padding: 12px 16px;
        border-bottom: 1px solid var(--hos-line, var(--hh-line));
      }
      .head h3 {
        margin: 0;
      }
      .x {
        border: none;
        background: transparent;
        padding: 2px 8px;
        font-size: 1.1rem;
        line-height: 1;
      }
      .body {
        padding: 14px 16px;
        display: flex;
        flex-direction: column;
        gap: 10px;
      }
      .foot {
        display: flex;
        justify-content: flex-end;
        gap: var(--hos-s-8, 8px);
        padding: 12px 16px;
        border-top: 1px solid var(--hos-line, var(--hh-line));
      }
      .pill {
        display: inline-block;
        font-size: var(--qp-caption);
        border: 1px solid var(--hos-safety-red, var(--hh-danger));
        color: var(--hos-safety-red, var(--hh-danger));
        border-radius: 999px;
        padding: 1px 8px;
      }
      .offer {
        border: 1px solid var(--hos-line, var(--hh-line));
        border-left: 3px solid var(--hos-stone-amber, var(--hh-accent));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        padding: 8px 10px;
      }
      textarea {
        font: inherit;
        font-size: var(--qp-body);
        padding: 4px 6px;
        width: 100%;
        box-sizing: border-box;
        min-height: 3.2rem;
        resize: vertical;
        border: 1px solid var(--hos-control-line, var(--hh-control-line));
        border-radius: var(--hos-radius-control, var(--hh-radius-control));
        background: var(--hos-deep-earth, var(--hh-page));
        color: var(--hos-text, var(--hh-text));
      }
      textarea:focus-visible {
        outline: 2px solid var(--hos-stone-amber-text, var(--hh-accent-text));
        outline-offset: 2px;
      }
    `,
  ];

  reload(): void {}

  private readonly onSeam = (event: Event): void => {
    const detail = (event as CustomEvent<GuardRefusalEvent>).detail;
    if (detail === undefined || detail === null || detail.refusal === undefined) return;
    this.returnFocus = QdFloorRefusal.deepActive();
    this.refusal = detail.refusal;
    this.target = { quoteId: detail.quoteId, lineNo: detail.lineNo, discPct: detail.discPct };
    this.sentence = detail.sentence ?? "";
    this.requestStatus = detail.refusal.requestStatus;
    this.mayClear = false;
    this.note = "";
    this.error = "";
    this.notice = "";
    this.busy = false;
    this.open = true;
  };

  private readonly onWindowKey = (event: KeyboardEvent): void => {
    if (this.open && event.key === "Escape") {
      event.preventDefault();
      this.close();
    }
  };

  private readonly onDialogKey = (event: KeyboardEvent): void => {
    if (event.key !== "Tab") return;
    const stops = this.focusable();
    if (stops.length === 0) return;
    const first = stops[0];
    const last = stops[stops.length - 1];
    const active = (this.renderRoot as ShadowRoot).activeElement;
    if (event.shiftKey && (active === first || active === null || active === this.dialogEl())) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first?.focus();
    }
  };

  override connectedCallback(): void {
    super.connectedCallback();
    window.addEventListener(FLOOR_REFUSAL_SEAM_EVENT, this.onSeam);
    window.addEventListener("keydown", this.onWindowKey);
    this.renderRoot.addEventListener("keydown", this.onDialogKey as EventListener);
  }

  override disconnectedCallback(): void {
    window.removeEventListener(FLOOR_REFUSAL_SEAM_EVENT, this.onSeam);
    window.removeEventListener("keydown", this.onWindowKey);
    this.renderRoot.removeEventListener("keydown", this.onDialogKey as EventListener);
    super.disconnectedCallback();
  }

  private static deepActive(): HTMLElement | null {
    let node: Element | null = document.activeElement;
    while (node?.shadowRoot?.activeElement) node = node.shadowRoot.activeElement;
    return node instanceof HTMLElement ? node : null;
  }

  private dialogEl(): HTMLElement | null {
    return this.renderRoot.querySelector<HTMLElement>(".dialog");
  }

  private focusable(): HTMLElement[] {
    const dialog = this.dialogEl();
    if (dialog === null) return [];
    return [...dialog.querySelectorAll<HTMLElement>("button, textarea, input, [tabindex]")].filter(
      (el) => !(el as HTMLButtonElement).disabled && el.getAttribute("tabindex") !== "-1",
    );
  }

  protected override updated(changed: Map<string, unknown>): void {
    if (changed.has("open") && this.open) {
      const primary = this.renderRoot.querySelector<HTMLElement>('[data-action="request"]');
      (primary ?? this.renderRoot.querySelector<HTMLElement>('[data-testid="fr-close"]'))?.focus();
    }
    this.syncValue('[data-field="note"]', this.note);
  }

  private close(): void {
    const wasOpen = this.open;
    this.open = false;
    if (wasOpen) window.dispatchEvent(new CustomEvent(FLOOR_REFUSAL_CLOSED_EVENT));
    const back = this.returnFocus;
    this.returnFocus = null;
    if (back !== null && back.isConnected) back.focus();
  }

  protected override onClickAction(action: string, control: HTMLElement, event: MouseEvent): void {
    if (action === "close") this.close();
    // Only a click on the backdrop itself closes: the delegation hands over the nearest control, which for a click inside the dialog is the backdrop too.
    if (action === "backdrop" && event.composedPath()[0] === control) this.close();
    if (action === "request") void this.fileRequest();
  }

  protected override onFieldInput(field: string, control: HTMLElement): void {
    if (field === "note") this.note = (control as HTMLTextAreaElement).value;
  }

  /** Whether this refusal belongs to a quote line, so a request can be filed against it. */
  private fileable(): boolean {
    return this.target !== null && this.target.quoteId !== "" && this.client !== null;
  }

  private async fileRequest(): Promise<void> {
    const client = this.client;
    const target = this.target;
    if (client === null || target === null || target.quoteId === "" || this.busy) return;
    this.busy = true;
    this.error = "";
    this.notice = "";
    const result = await client.requestFloorApproval(target.quoteId, target.lineNo, target.discPct, this.note.trim());
    this.busy = false;
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "ask for this approval"));
      return;
    }
    const answer = result.value;
    if (answer.may_clear_itself) {
      this.mayClear = true;
      this.notice = "";
      return;
    }
    this.mayClear = false;
    if (answer.request !== undefined) this.requestStatus = answer.request.status;
    if (answer.filed) this.notice = "Sent. Your request is with the approvers.";
    else this.notice = "";
    this.announceChanged();
  }

  private figures(refusal: FloorRefusalView): string {
    if (refusal.marginBps !== null && refusal.floorBps !== null) {
      return `The margin at this discount is ${bpsToPercent(refusal.marginBps)} against a floor of ${bpsToPercent(refusal.floorBps)}.`;
    }
    if (refusal.marginBps !== null) return `The margin is ${bpsToPercent(refusal.marginBps)}.`;
    return "";
  }

  private renderBody(refusal: FloorRefusalView, target: { quoteId: string; lineNo: number; discPct: number }) {
    const checkOnly = target.quoteId === "";
    const figures = this.figures(refusal);
    const roles = approverWords(refusal.approverRoles);
    return html`
      <div><span class="pill" data-testid="fr-policy">${policyLabel(refusal.policy)}</span></div>
      ${checkOnly
        ? html`<div data-testid="fr-reason">${this.sentence !== "" ? this.sentence : "This price is below the margin floor."}</div>`
        : html`<div data-testid="fr-reason">This discount puts line ${target.lineNo} below the margin floor.</div>`}
      ${figures !== "" ? html`<div data-testid="fr-figures">${figures}</div>` : nothing}
      ${roles !== ""
        ? html`<div data-testid="fr-approvers">Who may approve it: ${roles}.${refusal.managerMaySet ? " The branch's Sales manager may also set the price itself." : ""}</div>`
        : html`<div class="dim" data-testid="fr-approvers">The server named no approver for this check.</div>`}
      ${checkOnly
        ? html`<div class="offer" data-testid="fr-check-only">This was a check only. Nothing is filed from here; open a quote line to ask for an approval.</div>`
        : nothing}
      ${this.mayClear
        ? html`<div class="offer" role="status" data-testid="fr-may-clear">You may set this price yourself. The server offers it, and no request was filed.</div>`
        : nothing}
      ${this.requestStatus !== null && !this.mayClear
        ? html`<div class="offer" data-testid="fr-request-state" data-status=${this.requestStatus}>${requesterStateWords(this.requestStatus)}</div>`
        : nothing}
      ${this.fileable() && !this.mayClear && this.requestStatus !== "PENDING" && this.requestStatus !== "APPROVED"
        ? html`<div>
            <label for="fr-note">Note for the approver (optional)</label>
            <textarea id="fr-note" data-field="note" maxlength="500"></textarea>
          </div>`
        : nothing}
      ${this.notice !== "" ? html`<div class="ok" role="status" data-testid="fr-notice">${this.notice}</div>` : nothing}
      ${this.error !== "" ? html`<div class="err" role="alert" data-testid="fr-error">${this.error}</div>` : nothing}
    `;
  }

  override render() {
    if (!this.open || this.refusal === null || this.target === null) return nothing;
    const askable = this.fileable() && !this.mayClear && this.requestStatus !== "PENDING" && this.requestStatus !== "APPROVED";
    const done = this.mayClear || (!askable && this.requestStatus !== null) || this.notice !== "";
    return html`
      <div class="backdrop" data-action="backdrop" data-testid="fr-backdrop">
        <div class="dialog" role="dialog" aria-modal="true" aria-labelledby="fr-title" data-testid="fr-dialog">
          <div class="head">
            <h3 id="fr-title">Action blocked</h3>
            <button type="button" class="x" data-action="close" aria-label="Close">&times;</button>
          </div>
          <div class="body">${this.renderBody(this.refusal, this.target)}</div>
          <div class="foot">
            <button type="button" data-action="close" data-testid="fr-close">${done || !askable ? "Close" : "Cancel"}</button>
            ${askable ? html`<button type="button" class="primary" data-action="request" data-testid="fr-request" ?disabled=${this.busy}>Request approval</button>` : nothing}
          </div>
        </div>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-floor-refusal": QdFloorRefusal;
  }
}
