// PricingTabBase: what the five tabs of the pricing panel share. The panel base's
// delegation (the templates carry no property or event bindings, so one listener per
// root hands every click and field change to the tab by data-action and data-field), the
// server's refusal as an event the panel host answers, dialog focus (Tab stays inside,
// Escape closes the top dialog, focus returns to what had it), the uncontrolled field and
// select helpers the binding rules call for, and the editors' floor warning (CP0-6):
// check before the save, show the products the new price would put under the floor,
// save only on confirm, then log the confirmation.
//
// Every write is bound to what was captured when it began: the save closure holds the
// form's snapshot and the record's id, so a form that changes under an open warning, or a
// list that reloads, cannot change what a confirmed save writes.
import { html, nothing, type TemplateResult } from "lit";
import { property, state } from "lit/decorators.js";
import type { PricingFloorConfirmation } from "@hardscapehouse/api-client";
import type { Result } from "@hardscapehouse/api-client";
import { PanelBase } from "./panel-base.js";
import { spokenRefusal } from "./_quote-workarea.js";
import type { PricingClient } from "./pricing-client.js";
import {
  affectedItems,
  confirmationBody,
  itemLine,
  needsConfirm,
  settledNote,
  previewBody,
  warningHeadline,
  warningNotes,
  type FloorChange,
} from "./_pricing-floor.js";
import type { FloorPreview } from "./_pricing-wire.js";

/** Fired (bubbling, composed) when the server refuses with 403: the host shows the refusal and drops the controls. */
export const PRICING_REFUSED_EVENT = "pricing-refused";

export interface PricingRefusalDetail {
  /** The server's own words. */
  message: string;
  /** True when a write or the floor check was refused (the prices stay readable); false when a read was refused. */
  write: boolean;
}

interface FloorPrompt {
  preview: FloorPreview;
  change: FloorChange;
  /** Writes the record and answers its id. Bound to the snapshot taken when the save began. */
  save: () => Promise<Result<string>>;
  onSaved: () => void;
  busy: boolean;
  error: string;
  /** The id of the record once it is saved, "" before. */
  savedId: string;
  /** The confirmation that still has to reach the log, when the save landed and the log write did not. */
  unlogged: PricingFloorConfirmation | null;
  /** The control that had focus when the save began: a decline returns focus here, inside the dialog that is still open. */
  returnTo: HTMLElement | null;
  /** Set when Close or Escape was pressed once with the log row still owed: the next press leaves it. */
  closeWarned: boolean;
}

export abstract class PricingTabBase extends PanelBase {
  @property({ attribute: false }) pricing: PricingClient | null = null;
  /** The host drops this when the server has refused the writes; the controls go with it. */
  @property({ attribute: false }) canEdit = true;

  @state() protected saving = false;
  @state() protected formError = "";
  @state() protected floorPrompt: FloorPrompt | null = null;
  /** What a straight save said afterwards about products already under their floor and no worse. */
  @state() protected settled = "";
  private returnFocus: HTMLElement | null = null;

  /** Close the tab's own top dialog; true when one was open. */
  protected abstract closeTopDialog(): boolean;

  /**
   * HOOK for the app's leaving paths (the panel's tab switch, the desk's Quotes toggle, and
   * the sign out): true while a price is saved and its floor confirmation is not yet in the
   * pricing log, so leaving would lose the row. quote/qd-3b-app's SIGN_OUT_EVENT wait should
   * register this (through QdPricingPanel.holdLeave) when that branch is merged; until then
   * the desk's own sign out button asks the panel the same question.
   */
  unloggedConfirmationPending(): boolean {
    return this.floorPrompt?.unlogged != null;
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.renderRoot.addEventListener("keydown", this.onRootKey as EventListener);
  }

  override disconnectedCallback(): void {
    this.renderRoot.removeEventListener("keydown", this.onRootKey as EventListener);
    super.disconnectedCallback();
  }

  // ── the server's refusal ──────────────────────────────────────────────────

  /** A 403 is the server saying this role may not: tell the host, in the server's words. True when it was one. */
  protected noteRefusal(result: { error: string; status?: number | undefined }, write: boolean): boolean {
    if (result.status !== 403) return false;
    this.dispatchEvent(
      new CustomEvent<PricingRefusalDetail>(PRICING_REFUSED_EVENT, { detail: { message: spokenRefusal(result.error), write }, bubbles: true, composed: true }),
    );
    return true;
  }

  // ── dialog focus ──────────────────────────────────────────────────────────

  private static deepActive(): HTMLElement | null {
    let node: Element | null = document.activeElement;
    while (node?.shadowRoot?.activeElement) node = node.shadowRoot.activeElement;
    return node instanceof HTMLElement ? node : null;
  }

  protected captureFocus(): void {
    if (this.returnFocus === null) this.returnFocus = PricingTabBase.deepActive();
  }

  protected restoreFocus(): void {
    const back = this.returnFocus;
    this.returnFocus = null;
    if (back !== null && back.isConnected) back.focus();
  }

  private topBackdrop(): HTMLElement | null {
    const all = [...this.renderRoot.querySelectorAll<HTMLElement>(".backdrop")];
    return all.length === 0 ? null : (all[all.length - 1] ?? null);
  }

  private readonly onRootKey = (event: KeyboardEvent): void => {
    if (event.key === "Escape") {
      if (this.floorPrompt !== null) {
        event.preventDefault();
        event.stopPropagation();
        this.declineFloor();
      } else if (this.closeTopDialog()) {
        event.preventDefault();
        event.stopPropagation();
      }
      return;
    }
    if (event.key !== "Tab") return;
    const top = this.topBackdrop();
    if (top === null) return;
    const stops = [...top.querySelectorAll<HTMLElement>("button, input, select, textarea, [tabindex]")].filter(
      (el) => !(el as HTMLButtonElement).disabled && el.getAttribute("tabindex") !== "-1",
    );
    const first = stops[0];
    const last = stops[stops.length - 1];
    if (first === undefined || last === undefined) return;
    const active = (this.renderRoot as ShadowRoot).activeElement;
    if (event.shiftKey && (active === first || active === null)) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  };

  /** Move focus into the freshly opened dialog: the field named, else its first control. */
  protected focusDialog(selector: string): void {
    const top = this.topBackdrop();
    (top?.querySelector<HTMLElement>(selector) ?? top?.querySelector<HTMLElement>("input, select, button"))?.focus();
  }

  // ── uncontrolled fields ───────────────────────────────────────────────────

  /** Set every [data-field] control the values name, unless it already holds the value (typing is never overwritten). */
  protected syncFields(values: Record<string, string | boolean>): void {
    for (const el of this.renderRoot.querySelectorAll<HTMLInputElement | HTMLSelectElement>("[data-field]")) {
      const key = el.getAttribute("data-field") ?? "";
      const value = values[key];
      if (value === undefined) continue;
      if (el instanceof HTMLInputElement && el.type === "checkbox") {
        if (el.checked !== value) el.checked = value === true;
      } else if (typeof value === "string" && el.value !== value) {
        el.value = value;
      }
    }
  }

  /** Fill a select from [value, label] pairs once per distinct list, then select the value. */
  protected fillSelect(field: string, options: ReadonlyArray<readonly [string, string]>, selected: string): void {
    const el = this.renderRoot.querySelector<HTMLSelectElement>(`select[data-field="${field}"]`);
    if (el === null) return;
    const signature = options.map(([v, l]) => `${v}\u0001${l}`).join("\u0002");
    if (el.getAttribute("data-sig") !== signature) {
      el.replaceChildren(
        ...options.map(([value, label]) => {
          const option = document.createElement("option");
          option.value = value;
          option.textContent = label;
          return option;
        }),
      );
      el.setAttribute("data-sig", signature);
    }
    if (el.value !== selected) el.value = selected;
  }

  // ── the floor warning ─────────────────────────────────────────────────────

  /**
   * Save a price with the editors' floor warning in front of it. The check runs first and
   * the save never starts unless the check answered: a check that could not run is said,
   * and nothing is saved. A clean answer saves straight away; an answer that names
   * products opens the warning, and the save runs only on the editor's confirm.
   */
  protected async saveWithFloor(change: FloorChange, save: () => Promise<Result<string>>, onSaved: () => void): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null || this.saving) return;
    this.saving = true;
    this.formError = "";
    this.settled = "";
    const asked = PricingTabBase.deepActive();
    const checked = await pricing.previewFloor(previewBody(change));
    if (!checked.ok) {
      this.saving = false;
      if (this.noteRefusal(checked, true)) return;
      // A 400 or 422 is the check refusing what was sent: say what, not that the check could not run.
      const refused = checked.status === 400 || checked.status === 422;
      this.formError = refused
        ? `${spokenRefusal(checked.error).replace(/\.+$/, "")}. Nothing was saved.`
        : `${spokenRefusal(checked.error).replace(/\.+$/, "")}. Nothing was saved, because the floor check could not run.`;
      return;
    }
    if (!needsConfirm(checked.value)) {
      const saved = await save();
      this.saving = false;
      if (!saved.ok) {
        if (!this.noteRefusal(saved, true)) this.formError = spokenRefusal(saved.error);
        return;
      }
      this.settled = settledNote(checked.value);
      onSaved();
      return;
    }
    this.saving = false;
    this.captureFocus();
    this.floorPrompt = {
      preview: checked.value, change, save, onSaved, busy: false, error: "", savedId: "", unlogged: null,
      returnTo: asked, closeWarned: false,
    };
  }

  protected declineFloor(): void {
    const prompt = this.floorPrompt;
    if (prompt === null || prompt.busy) return;
    // A price saved whose log row is still owed: the first Close or Escape says so, the next leaves.
    if (prompt.unlogged !== null && !prompt.closeWarned) {
      prompt.closeWarned = true;
      prompt.error = "The price was saved, but its confirmation is not in the pricing log. Log it again, or close once more to leave without it.";
      this.requestUpdate();
      return;
    }
    this.floorPrompt = null;
    // A price that is already saved stays saved; declining after that is closing the notice.
    if (prompt.savedId !== "") prompt.onSaved();
    else if (prompt.returnTo !== null && prompt.returnTo.isConnected) prompt.returnTo.focus();
    else this.restoreFocus();
  }

  private async confirmFloor(): Promise<void> {
    const prompt = this.floorPrompt;
    if (prompt === null || prompt.busy || prompt.savedId !== "") return;
    prompt.busy = true;
    prompt.error = "";
    this.requestUpdate();
    const saved = await prompt.save();
    if (!saved.ok) {
      prompt.busy = false;
      if (!this.noteRefusal(saved, true)) prompt.error = spokenRefusal(saved.error);
      this.requestUpdate();
      return;
    }
    prompt.savedId = saved.value;
    const body = confirmationBody(prompt.change, saved.value, prompt.preview);
    if (body === null) {
      // The notice named no product (a gap in the check only): there is nothing to log.
      prompt.busy = false;
      this.floorPrompt = null;
      prompt.onSaved();
      return;
    }
    await this.logConfirmation(prompt, body);
  }

  private async logConfirmation(prompt: FloorPrompt, body: PricingFloorConfirmation): Promise<void> {
    const pricing = this.pricing;
    if (pricing === null) return;
    prompt.busy = true;
    this.requestUpdate();
    const logged = await pricing.confirmFloor(body);
    prompt.busy = false;
    if (!logged.ok) {
      prompt.unlogged = body;
      prompt.closeWarned = false;
      prompt.error = `The price was saved, but the confirmation could not be logged: ${spokenRefusal(logged.error)}`;
      this.requestUpdate();
      return;
    }
    this.floorPrompt = null;
    prompt.onSaved();
  }

  protected async retryLog(): Promise<void> {
    const prompt = this.floorPrompt;
    if (prompt === null || prompt.busy || prompt.unlogged === null) return;
    prompt.error = "";
    await this.logConfirmation(prompt, prompt.unlogged);
  }

  protected onFloorAction(action: string): boolean {
    if (action === "floor-confirm") void this.confirmFloor();
    else if (action === "floor-decline") this.declineFloor();
    else if (action === "floor-retry-log") void this.retryLog();
    else return false;
    return true;
  }

  protected renderFloorDialog(): TemplateResult | typeof nothing {
    const prompt = this.floorPrompt;
    if (prompt === null) {
      return this.settled === "" ? nothing : html`<p class="msg settled" role="status" data-testid="floor-settled">${this.settled}</p>`;
    }
    const affected = affectedItems(prompt.preview);
    const saved = prompt.savedId !== "";
    const names = affected.length > 0 || prompt.preview.items.length > 0;
    return html`
      <div class="backdrop top" data-testid="floor-backdrop">
        <div class="dialog wide" role="dialog" aria-modal="true" aria-labelledby="floor-title" data-testid="floor-dialog">
          <div class="dialog-head">
            <h3 id="floor-title">${affected.length > 0 ? "Margin floor warning" : "Margin floor check"}</h3>
          </div>
          <div class="dialog-body">
            <div data-testid="floor-headline"><strong>${warningHeadline(prompt.preview)}</strong></div>
            ${names
              ? html`<ul class="floor-list" data-testid="floor-list">
                  ${affected.map((item) => html`<li data-testid="floor-item">${itemLine(item)}</li>`)}
                </ul>`
              : nothing}
            ${warningNotes(prompt.preview).map((note) => html`<div class="hint" data-testid="floor-note">${note}</div>`)}
            <div class="hint">
              ${saved
                ? "Saved. The confirmation goes into the pricing log."
                : affected.length > 0
                  ? "You may still save it: the editors set the prices. Confirming saves the price and records in the pricing log that you were shown this."
                  : "You may still save it: the editors set the prices. Confirming saves the price."}
            </div>
            ${prompt.error !== "" ? html`<div class="err" role="alert" data-testid="floor-error">${prompt.error}</div>` : nothing}
          </div>
          <div class="dialog-foot">
            ${prompt.unlogged !== null
              ? html`<button type="button" data-action="floor-retry-log" data-testid="floor-retry-log" ?disabled=${prompt.busy}>Log the confirmation again</button>
                  <button type="button" data-action="floor-decline" data-testid="floor-close" ?disabled=${prompt.busy}>Close</button>`
              : html`<button type="button" data-action="floor-decline" data-testid="floor-decline" ?disabled=${prompt.busy}>Do not save</button>
                  <button type="button" class="primary" data-action="floor-confirm" data-testid="floor-confirm" ?disabled=${prompt.busy}>
                    ${prompt.busy ? "Saving..." : "Save anyway"}
                  </button>`}
          </div>
        </div>
      </div>
    `;
  }

  protected override updated(changed: Map<string, unknown>): void {
    if (this.floorPrompt === null) return;
    const dialog = this.renderRoot.querySelector<HTMLElement>('[data-testid="floor-dialog"]');
    if (changed.has("floorPrompt")) {
      this.renderRoot.querySelector<HTMLElement>('[data-testid="floor-decline"]')?.focus();
    } else if (dialog !== null && !dialog.contains((this.renderRoot as ShadowRoot).activeElement)) {
      // A button that was busy (and so disabled) lost focus to the page: bring it back inside the dialog.
      dialog.querySelector<HTMLElement>("button:not([disabled])")?.focus();
    }
  }
}
