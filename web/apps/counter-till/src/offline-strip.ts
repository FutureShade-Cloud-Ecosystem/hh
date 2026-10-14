// The Counter Till's offline strip: what the Work shell says about the
// connection and the copy on the device, above the sale.
//   - the shell chrome's offline banner: offline, the server unreachable, a
//     second tab that only reads, the sign in expired. It says nothing at all
//     when all is well. The till has no outbox, so it never counts changes
//     waiting: there are none, by construction.
//   - the copy's age, when a read (the catalogue, a customer, the parked
//     tickets) was answered from the device.
//   - the money line: while there is no signal, the till says in plain words
//     that selling, tendering, parking and every other money action need the
//     signal and are not kept for later.
// Visual reference: the shell chrome's offline banner board (the r4 offline
// states); no visual check has been done on it.
//
// The kits' wiring rule: values reach the shared banner through properties in
// updated(), never through template bindings.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import type { WorkDesk } from "@hardscapehouse/work-offline";
// The offline banner registers itself (the shell chrome package carries it as a side effect import).
import "@hardscapehouse/shell-chrome/src/shell-banner.js";

export const MONEY_LINE = "Selling, tendering, parking and every other money action need the signal. Nothing is kept for later.";

export class CtOfflineStrip extends HHElement {
  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
      }
      .note {
        margin: var(--hh-s-4) var(--hh-s-16);
        color: var(--hh-text-dim);
        font-size: var(--hh-s-12);
      }
    `,
  ];

  #desk: WorkDesk | null = null;
  #unsubscribe: (() => void) | null = null;
  #mode = "";
  #cached = "";
  #online = true;

  disconnectedCallback(): void {
    this.#unsubscribe?.();
    this.#unsubscribe = null;
    super.disconnectedCallback();
  }

  /** The wiring pass hands the desk controller down; a changed one is followed from then on. */
  attach(desk: WorkDesk | null): void {
    if (desk === this.#desk) return;
    this.#unsubscribe?.();
    this.#desk = desk;
    this.#unsubscribe = desk === null ? null : desk.subscribe(() => this.#refresh());
    this.#refresh();
  }

  #refresh(): void {
    const desk = this.#desk;
    this.#mode = desk === null ? "" : desk.bannerMode();
    this.#cached = desk === null ? "" : desk.cachedWords();
    this.#online = desk === null ? true : desk.online();
    this.requestUpdate();
  }

  protected override updated(): void {
    const banner = this.renderRoot.querySelector<HTMLElement & { mode: string; waiting: number; cacheOnly: boolean }>("hh-shell-offline-banner");
    if (banner !== null) {
      banner.mode = this.#mode;
      // A read cache only app: the headline says what is shown, never that writes wait.
      banner.cacheOnly = true;
      // The till has no outbox: nothing is ever counted as waiting.
      banner.waiting = 0;
    }
  }

  render(): TemplateResult {
    return html`
      ${this.#mode !== "" ? html`<hh-shell-offline-banner data-testid="ct-offline-banner"></hh-shell-offline-banner>` : nothing}
      ${this.#mode === "offline" || this.#mode === "unreachable" ? html`<p class="note" role="status" data-testid="ct-money-line">${MONEY_LINE}</p>` : nothing}
      ${this.#cached !== ""
        ? html`<p class="note" role="status" data-testid="ct-cache-note">Showing the copy on this device. ${this.#cached}.${this.#online ? " You are back online: refresh to read from the server." : ""}</p>`
        : nothing}
    `;
  }
}

defineElement("ct-offline-strip", CtOfflineStrip);
