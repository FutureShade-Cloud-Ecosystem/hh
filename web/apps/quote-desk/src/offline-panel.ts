// The Quote Desk's offline panel: what the Work shell says about the
// connection and the copy on the device, in one place above the desk.
//   - the offline strip (the shell chrome's banner): offline with the count of
//     changes waiting, the server unreachable, a second tab that only reads,
//     the sign in expired. It says nothing at all when all is well.
//   - the copy's age, when a read was answered from the device.
//   - the drafts waiting to be sent, each with a way to withdraw it, and no
//     total (a figure the server has not stated is not shown).
//   - the cards the server's answers left: a merge, a newer server version, a
//     case for a person, a refusal, each dismissed once read.
// Visual reference: the shell chrome's offline banner board (the r4 offline
// states) and the Yard Pick review list; no visual check has been done on it.
//
// The panel reads the controller (desk-offline.ts) and feeds it nothing but
// the person's two acts, withdraw and dismiss. The kits' wiring rule: values
// reach the shared elements through properties in updated(), never through
// template bindings; the buttons are found off composedPath() on the host.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import type { WaitingDraft } from "@hardscapehouse/work-offline";
// The offline banner registers itself (the shell chrome package carries it as a side effect import).
import "@hardscapehouse/shell-chrome/src/shell-banner.js";
import type { DeskCard, DeskOffline } from "./desk-offline.js";

/** One waiting entry in words. */
export function waitingWords(draft: WaitingDraft): string {
  const who = draft.customerName === "" ? "A customer" : draft.customerName;
  if (draft.kind === "withdraw") return `${who}: a draft is being withdrawn.`;
  if (draft.kind === "edit") return `A quote's valid until date changes${draft.validUntil !== null ? ` to ${draft.validUntil}` : " (no date)"}.`;
  return `${who}: new draft${draft.validUntil !== null ? `, valid until ${draft.validUntil}` : ", no valid until date"}.`;
}

export class QdOfflinePanel extends HHElement {
  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
      }
      .cache-note,
      section {
        margin: var(--hh-s-8) var(--hh-s-20);
      }
      .cache-note {
        color: var(--hh-text-dim);
        font-size: var(--hh-s-12);
      }
      section {
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-control);
        background: var(--hh-surface);
        padding: var(--hh-s-8) var(--hh-s-16);
      }
      h2 {
        margin: 0 0 var(--hh-s-4);
        font-size: var(--hh-s-14);
      }
      ul {
        list-style: none;
        margin: 0;
        padding: 0;
        display: grid;
        gap: var(--hh-s-4);
      }
      li {
        display: flex;
        align-items: center;
        gap: var(--hh-s-8);
      }
      li .words {
        flex: 1;
      }
      li .title {
        font-weight: 600;
      }
    `,
  ];

  #desk: DeskOffline | null = null;
  #unsubscribe: (() => void) | null = null;
  #mode = "";
  #count = 0;
  #cached = "";
  #online = true;
  #waiting: WaitingDraft[] = [];
  #cards: DeskCard[] = [];

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.#unsubscribe?.();
    this.#unsubscribe = null;
    super.disconnectedCallback();
  }

  /** The wiring pass hands the controller down; a changed one is followed from then on. */
  attach(desk: DeskOffline | null): void {
    if (desk === this.#desk) return;
    this.#unsubscribe?.();
    this.#desk = desk;
    this.#unsubscribe = desk === null ? null : desk.subscribe(() => void this.#refresh());
    void this.#refresh();
  }

  async #refresh(): Promise<void> {
    const desk = this.#desk;
    if (desk === null) {
      this.#mode = "";
      this.#count = 0;
      this.#cached = "";
      this.#online = true;
      this.#waiting = [];
      this.#cards = [];
    } else {
      this.#mode = desk.bannerMode();
      this.#cached = desk.cachedWords();
      this.#online = desk.online();
      this.#count = await desk.waitingCount();
      this.#waiting = await desk.waiting();
      this.#cards = await desk.cards();
    }
    this.requestUpdate();
  }

  readonly #onClick = (event: MouseEvent): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined || this.#desk === null) return;
    const action = control.getAttribute("data-action");
    if (action === "withdraw-draft") {
      const id = control.getAttribute("data-quote-id") ?? "";
      if (id !== "") void this.#desk.withdraw(id);
    }
    if (action === "dismiss-card") {
      const id = control.getAttribute("data-card-id") ?? "";
      if (id !== "") void this.#desk.dismiss(id);
    }
  };

  protected override updated(): void {
    const banner = this.renderRoot.querySelector<HTMLElement & { mode: string; waiting: number }>("hh-shell-offline-banner");
    if (banner !== null) {
      banner.mode = this.#mode;
      banner.waiting = this.#count;
    }
  }

  render(): TemplateResult {
    return html`
      ${this.#mode !== "" ? html`<hh-shell-offline-banner data-testid="qd-offline-banner"></hh-shell-offline-banner>` : nothing}
      ${this.#cached !== ""
        ? html`<p class="cache-note" role="status" data-testid="qd-cache-note">Showing the copy on this device. ${this.#cached}.${this.#online ? " You are back online: refresh to read from the server." : ""}</p>`
        : nothing}
      ${this.#waiting.length > 0
        ? html`<section aria-label="Waiting to send" data-testid="qd-waiting">
            <h2>Waiting to send</h2>
            <ul>
              ${this.#waiting.map(
                (draft) => html`<li data-testid="qd-waiting-item" data-kind=${draft.kind}>
                  <span class="words">${waitingWords(draft)}</span>
                  ${draft.kind === "new"
                    ? html`<button type="button" class="btn btn-secondary" data-action="withdraw-draft" data-quote-id=${draft.quoteId}>Withdraw</button>`
                    : nothing}
                </li>`,
              )}
            </ul>
          </section>`
        : nothing}
      ${this.#cards.length > 0
        ? html`<section aria-label="What the server answered" data-testid="qd-cards">
            <h2>What the server answered</h2>
            <ul>
              ${this.#cards.map(
                (card) => html`<li data-testid="qd-card" data-kind=${card.kind}>
                  <span class="words"><span class="title">${card.words.title}.</span> ${card.words.body}</span>
                  <button type="button" class="btn btn-secondary" data-action="dismiss-card" data-card-id=${card.id}>Dismiss</button>
                </li>`,
              )}
            </ul>
          </section>`
        : nothing}
    `;
  }
}

defineElement("qd-offline-panel", QdOfflinePanel);
