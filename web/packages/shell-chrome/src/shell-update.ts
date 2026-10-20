// The update prompt: the r4 updater board's card, shown once the signed shell
// release is whole (the prompt is quiet until then; the shell decides when
// `open` turns true). Update now calls the injected updater seam and restarts
// into the new version; Later closes the card and the shell asks again at its
// own pace. The Tauri updater itself is wired in the workspace item, not
// here: this package only carries the seam, so the browser demo can inject a
// double and the shells inject the real one.
//
// What the words promise, the offline store keeps: the sign in, the pins and
// everything in the outbox carry over a restart, nothing queued is lost.

import { css, html, nothing, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';

/** The updater seam: install() resolves when the new version is in place and
 * the shell may restart. The package never reaches for a platform updater. */
export interface ShellUpdater {
  install(): Promise<void>;
}

/** The mark, the measured H geometry the design system's own header carries
 * (its source of truth is the design system's src/header.ts). */
const MARK_PATH =
  'M644.94 465.17 H1386.06 A29.15 29.15 0 0 1 1415.21 494.32 V1306.38 A84.79 84.79 0 0 0 1500 1391.17 H2355.05 A29.15 29.15 0 0 1 2384.2 1420.32 V2505.68 A29.15 29.15 0 0 1 2355.05 2534.83 H1613.94 A29.15 29.15 0 0 1 1584.79 2505.68 V1940.1 A84.79 84.79 0 0 0 1415.21 1940.1 V2505.68 A29.15 29.15 0 0 1 1386.06 2534.83 H644.94 A29.15 29.15 0 0 1 615.79 2505.68 V494.32 A29.15 29.15 0 0 1 644.94 465.17 Z M1613.94 465.17 H2355.05 A29.15 29.15 0 0 1 2384.2 494.32 V1192.85 A29.15 29.15 0 0 1 2355.05 1222 H1613.94 A29.15 29.15 0 0 1 1584.79 1192.85 V494.32 A29.15 29.15 0 0 1 1613.94 465.17 Z';

const CARRY_OVER =
  'Restart to update. Your sign in, your pins and everything in the outbox carry over; nothing queued is lost.';

const styles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  .prompt {
    display: flex;
    flex-wrap: wrap;
    align-items: flex-start;
    gap: var(--hh-s-16);
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16);
  }
  .tile {
    width: 40px;
    height: 40px;
    flex: none;
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    display: grid;
    place-items: center;
  }
  .tile svg {
    width: 100%;
    height: 100%;
    display: block;
    padding: 6px;
    box-sizing: border-box;
  }
  .words {
    flex: 1 1 240px;
    min-width: 0;
    display: grid;
    gap: var(--hh-s-4);
  }
  .title {
    margin: 0;
    font-size: 15px;
    font-weight: 700;
    color: var(--hh-text);
  }
  .sub {
    margin: 0;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .body {
    margin: var(--hh-s-4) 0 0;
    font-size: var(--hh-s-12);
    line-height: 1.45;
    color: var(--hh-text-dim);
  }
  .actions {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex: none;
    align-self: stretch;
  }
`;

export class ShellUpdate extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    open: { type: Boolean, attribute: 'open' },
    shellName: { attribute: 'shell-name' },
    version: { attribute: 'version' },
    updater: { attribute: false },
  };

  /** The card is quiet until the download is whole; the shell opens it. */
  declare open: boolean;
  /** The shell's own name for the card's title. */
  declare shellName: string;
  /** The downloaded release's name, when the shell names one. */
  declare version: string;
  /** The injected updater seam; install() resolves when a restart may go. */
  declare updater: ShellUpdater | null;

  /** True between the press of Update now and the seam's answer. */
  #installing = false;

  constructor() {
    super();
    this.open = false;
    this.shellName = 'Field';
    this.version = '';
    this.updater = null;
    this.state = 'ready';
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener('click', this.#onClick);
  }

  override disconnectedCallback(): void {
    this.removeEventListener('click', this.#onClick);
    super.disconnectedCallback();
  }

  #emit(type: string, detail?: unknown): void {
    this.dispatchEvent(new CustomEvent(type, { detail, bubbles: true, composed: true }));
  }

  readonly #onClick = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    const action = control.getAttribute('data-action');
    if (action === 'later') {
      this.open = false;
      this.#emit('hh-shell-update-later');
      return;
    }
    if (action === 'install') this.#install();
  };

  async #install(): Promise<void> {
    if (this.#installing) return;
    this.#installing = true;
    this.requestUpdate();
    this.#emit('hh-shell-update-install');
    try {
      await this.updater?.install();
    } finally {
      this.#installing = false;
      this.requestUpdate();
    }
  }

  render(): Template {
    if (!this.open) return html``;
    return html`
      <div class="prompt" role="region" aria-label=${`${this.shellName} update`}>
        <span class="tile" aria-hidden="true">
          <svg viewBox="0 0 3000 3000"><path fill="currentColor" d="${MARK_PATH}"></path></svg>
        </span>
        <div class="words">
          <p class="title">${this.shellName} update ready</p>
          ${this.version !== '' ? html`<p class="sub">${this.version} is downloaded</p>` : nothing}
          <p class="body">${CARRY_OVER}</p>
        </div>
        <div class="actions">
          <button class="btn btn-primary" type="button" data-action="install" ?disabled=${this.#installing}>
            ${this.#installing ? 'Installing…' : 'Update now'}
          </button>
          <button class="btn btn-secondary" type="button" data-action="later" ?disabled=${this.#installing}>Later</button>
        </div>
      </div>
    `;
  }
}

defineElement('hh-shell-update-prompt', ShellUpdate);
