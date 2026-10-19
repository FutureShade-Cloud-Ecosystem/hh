// The header band: ONE simple band (row 1520, the r4 Field shell frame)
// holding the mark and the org, the app and surface selector with its panel,
// the pin control, the sync state chip, notifications, and the account chip.
//
// Ported and polished from the original HardscapeOS at master 87d7941b, read
// only: the selector control and its grouped panel follow app/src/surface-
// switcher.ts and app/src/app-launcher.ts (the raised control, the menu
// keyboard, focus handed back to the control that opened the panel), and the
// phone rules follow the r4 shell frame (build_r4_shell.py): under 500px the
// band drops the sync chip and the org word, because the offline banner
// carries that state at phone width.
//
// Purely presentational, exactly like every chrome element in the original:
// the shell hands the band the switcher groups (the model's answer for the
// person's roles over the manifest slice), the current surface key, and the
// outbox's health, and the band emits
//
//   hh-shell-surface-open  { appId, surfaceId, key }   a row of the panel was picked
//   hh-shell-pin           { key, pinned }             the pin control was pressed
//   hh-shell-notifications                             the bell was pressed
//   hh-shell-account                                   the account chip was pressed
//
// The panel's keyboard is the original launcher's, one keyboard for every
// menu in the house: ArrowUp and ArrowDown move, Home and End jump, Escape
// closes and hands focus back to the control, Tab dismisses. Every click
// rides a data-action name and the host's own listener, the design system's
// delegation pattern; the markup carries no listener spelling at all.

import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import {
  initialsOf,
  SHELL_SYNC_STATES,
  surfaceKey,
  syncChipView,
  type ShellSyncState,
  type SwitcherGroup,
} from './shell-state.js';

/** The band's two variants: the compact band the fixture pages host serves,
 * and the board's white band (GAPS-r2 row G1): the GABLE lockup at the left,
 * the "App > Surface" selector centred, DEMO and Synced chips in the chip
 * run, the bell with its count, the pin, the account chip. */
export const SHELL_HEADER_VARIANTS = ['compact', 'band'] as const;

export type ShellHeaderVariant = (typeof SHELL_HEADER_VARIANTS)[number];

/** The badge mark, the measured H geometry the design system's own header
 * carries (its source of truth is the design system's src/header.ts, rows 953
 * and 968): the badge yellow square ground, the mark in the accent ink. */
const MARK_PATH =
  'M644.94 465.17 H1386.06 A29.15 29.15 0 0 1 1415.21 494.32 V1306.38 A84.79 84.79 0 0 0 1500 1391.17 H2355.05 A29.15 29.15 0 0 1 2384.2 1420.32 V2505.68 A29.15 29.15 0 0 1 2355.05 2534.83 H1613.94 A29.15 29.15 0 0 1 1584.79 2505.68 V1940.1 A84.79 84.79 0 0 0 1415.21 1940.1 V2505.68 A29.15 29.15 0 0 1 1386.06 2534.83 H644.94 A29.15 29.15 0 0 1 615.79 2505.68 V494.32 A29.15 29.15 0 0 1 644.94 465.17 Z M1613.94 465.17 H2355.05 A29.15 29.15 0 0 1 2384.2 494.32 V1192.85 A29.15 29.15 0 0 1 2355.05 1222 H1613.94 A29.15 29.15 0 0 1 1584.79 1192.85 V494.32 A29.15 29.15 0 0 1 1613.94 465.17 Z';

const styles = css`
  :host {
    display: block;
    position: relative;
    container-type: inline-size;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  .band {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    min-height: 56px;
    padding: var(--hh-s-8) var(--hh-s-16);
    background: var(--hh-card);
    border-bottom: 1px solid var(--hh-line);
  }
  /* The board's white band (GAPS-r2 row G1): the lockup at the left, the
   * selector centred by the grid's middle column, the chip run at the right. */
  .band[data-variant='band'] {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
  }
  .band[data-variant='band'] .brand {
    justify-self: start;
  }
  .band[data-variant='band'] .selector {
    justify-self: center;
  }
  .band[data-variant='band'] .cluster {
    justify-self: end;
    margin-left: 0;
  }
  .band[data-variant='band'] + .panel {
    left: 50%;
    transform: translateX(-50%);
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    flex: none;
  }
  .mark {
    width: 30px;
    height: 30px;
    flex: none;
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
  }
  .mark svg {
    width: 100%;
    height: 100%;
    display: block;
  }
  .org {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.02em;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  /* The Demo badge: the demo's gold chip (a fill, so it carries the accent ink), the word and nothing else. */
  .demo {
    flex: none;
    padding: var(--hh-s-2) var(--hh-s-8);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    border-radius: var(--hh-radius-control);
    font-size: var(--hh-s-12);
    font-weight: 700;
    line-height: 1.3;
  }
  .selector {
    display: inline-flex;
    align-items: center;
    height: 34px;
    min-width: 0;
    max-width: 100%;
    padding: 0;
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-line);
    border-radius: 9px;
    font-family: inherit;
    text-align: left;
    cursor: pointer;
    overflow: hidden;
  }
  .selector:hover,
  .selector[aria-expanded='true'] {
    border-color: var(--hh-accent-text);
  }
  .selector:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .stk {
    display: flex;
    flex-direction: column;
    justify-content: center;
    gap: 3px;
    align-self: stretch;
    width: 30px;
    flex: none;
    padding-left: 9px;
    border-right: 1px solid var(--hh-line);
    color: var(--hh-text-dim);
  }
  .stk-r {
    display: block;
    width: 12px;
    height: 3px;
    border-radius: 1px;
    background: currentColor;
  }
  .crumb {
    padding: 0 var(--hh-s-12);
    font-size: 13px;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .caret {
    display: grid;
    place-items: center;
    padding-right: 10px;
    color: var(--hh-text-dim);
  }
  .caret svg {
    display: block;
    width: 9px;
    height: 6px;
  }
  .cluster {
    margin-left: auto;
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    height: 22px;
    padding: 0 var(--hh-s-8);
    border-radius: var(--hh-radius-pill);
    border: 1px solid currentColor;
    font-size: var(--hh-s-12);
    font-weight: 700;
    white-space: nowrap;
  }
  .chip .dot {
    width: 6px;
    height: 6px;
    border-radius: var(--hh-radius-pill);
    background: currentColor;
    flex: none;
  }
  .chip .note {
    color: var(--hh-text-dim);
    font-weight: 400;
  }
  .chip[data-kind='good'] { color: var(--hh-state-good); }
  .chip[data-kind='busy'] { color: var(--hh-state-watch); }
  .chip[data-kind='stop'] { color: var(--hh-state-stop); }
  .chip[data-alarm='true'] {
    box-shadow: 0 0 0 1px currentColor;
  }
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    padding: 0;
    background: transparent;
    color: var(--hh-text-dim);
    border: 1px solid var(--hh-line);
    border-radius: 9px;
    cursor: pointer;
    position: relative;
    font-family: inherit;
  }
  .icon-btn:hover:not(:disabled) {
    color: var(--hh-text);
    border-color: var(--hh-accent-text);
  }
  .icon-btn:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .icon-btn svg {
    display: block;
    width: 16px;
    height: 16px;
  }
  .icon-btn[aria-pressed='true'] {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
  }
  .badge {
    position: absolute;
    top: -5px;
    right: -5px;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: 10px;
    font-weight: 700;
    line-height: 1;
    display: grid;
    place-items: center;
  }
  .account {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    background: transparent;
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-pill);
    padding: 2px 12px 2px 2px;
    color: var(--hh-text);
    cursor: pointer;
    font-family: inherit;
    font-size: var(--hh-s-12);
    font-weight: 600;
    white-space: nowrap;
  }
  .account:hover {
    border-color: var(--hh-accent-text);
  }
  .account:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .avatar {
    width: 30px;
    height: 30px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-field);
    border: 1px solid var(--hh-line);
    display: grid;
    place-items: center;
    font-size: 10px;
    font-weight: 600;
    flex: none;
  }
  .account-word {
    max-width: 160px;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* The panel: the whole map, hanging under its own control. */
  .panel {
    position: absolute;
    top: calc(100% + 6px);
    left: var(--hh-s-16);
    z-index: var(--hh-z-dropdown);
    min-width: 268px;
    max-width: min(468px, calc(100% - var(--hh-s-32)));
    max-height: 72vh;
    overflow-y: auto;
    overscroll-behavior: contain;
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: 12px;
    box-shadow: var(--hh-elev-2);
    padding: 6px;
  }
  .group-label {
    margin: var(--hh-s-8) var(--hh-s-8) var(--hh-s-4);
    font-size: 10.5px;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    font-weight: 700;
  }
  .mi {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 34px;
    background: transparent;
    border: none;
    border-radius: 8px;
    padding: var(--hh-s-8) 9px;
    font-family: inherit;
    font-size: 12.5px;
    line-height: 1.2;
    color: var(--hh-text);
    text-align: left;
    cursor: pointer;
  }
  .mi:hover {
    background: var(--hh-row-alt);
  }
  .mi:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .mi[aria-current='true'] {
    background: var(--hh-row-alt);
    font-weight: 600;
  }
  .mi-label {
    flex: 1 1 auto;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .hint {
    flex: none;
    font-size: 10.5px;
    color: var(--hh-state-good);
    font-weight: 700;
  }

  /* Narrow bands: phone and tablet targets come up to 44 px, and the phone
   * band drops the sync chip and the org word (the r4 frame's own rule: the
   * offline banner carries that state at phone width). */
  @container (max-width: 834px) {
    .selector,
    .icon-btn,
    .account,
    .mi {
      min-height: 44px;
    }
    .icon-btn {
      min-width: 44px;
    }
    .account {
      padding: 2px 12px 2px 6px;
    }
  }
  @container (max-width: 500px) {
    .chip,
    .org {
      display: none;
    }
    .crumb {
      font-size: 12.5px;
    }
  }
`;

export class ShellHeader extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    org: { attribute: 'org' },
    demo: { type: Boolean, attribute: 'demo' },
    user: { attribute: 'user' },
    breadcrumb: { attribute: 'breadcrumb' },
    groups: { attribute: false },
    currentKey: { attribute: 'current-key' },
    pinned: { type: Boolean, attribute: 'pinned' },
    syncState: { attribute: 'sync-state' },
    cards: { type: Number, attribute: 'cards' },
    waiting: { type: Number, attribute: 'waiting' },
    attempts: { type: Number, attribute: 'attempts' },
    stalls: { type: Number, attribute: 'stalls' },
    notifications: { type: Number, attribute: 'notifications' },
    selectorOpen: { type: Boolean, attribute: 'selector-open' },
    variant: { attribute: 'variant' },
  };

  /** The org's name, beside the mark. Empty renders no word. */
  declare org: string;
  /** Whether the Demo badge renders beside the org: the shell of a demo build says so once, for every app it frames. */
  declare demo: boolean;
  /** The signed in person's role label; drives the initials. */
  declare user: string;
  /** The band's breadcrumb words, the model's answer for where the person is. */
  declare breadcrumb: string;
  /** The switcher groups: the model's answer for the person's roles. */
  declare groups: SwitcherGroup[];
  /** The key of the surface on screen, in surfaceKey spelling. */
  declare currentKey: string;
  /** Whether the current surface is pinned; the pin control shows it. */
  declare pinned: boolean;
  /** The sync statechart's state the runner settled in. */
  declare syncState: string;
  /** Open conflict cards. */
  declare cards: number;
  /** The outbox health: writes waiting, the stuck write's attempts, stalls. */
  declare waiting: number;
  declare attempts: number;
  declare stalls: number;
  /** Unread notifications; zero renders the quiet bell. */
  declare notifications: number;
  /** The selector panel. The control toggles it; a shell may set it too. */
  declare selectorOpen: boolean;
  /** The band's variant: compact (the default, the fixture pages host) or
   * band (the board's white band: lockup left, selector centred, DEMO and
   * Synced chips in the run). */
  declare variant: string;

  constructor() {
    super();
    this.org = '';
    this.demo = false;
    this.user = '';
    this.breadcrumb = '';
    this.groups = [];
    this.currentKey = '';
    this.pinned = false;
    this.syncState = 'idle';
    this.cards = 0;
    this.waiting = 0;
    this.attempts = 0;
    this.stalls = 0;
    this.notifications = 0;
    this.selectorOpen = false;
    this.variant = 'compact';
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener('click', this.#onClick);
    this.addEventListener('keydown', this.#onKeyDown);
  }

  override disconnectedCallback(): void {
    this.removeEventListener('click', this.#onClick);
    this.removeEventListener('keydown', this.#onKeyDown);
    this.#stopOutsideWatch();
    super.disconnectedCallback();
  }

  protected override willUpdate(changed: PropertyValues): void {
    super.willUpdate?.(changed);
    if (changed.has('syncState') && !(SHELL_SYNC_STATES as readonly string[]).includes(this.syncState)) {
      // An unknown state is a defect at the call site: refuse loudly, never
      // paint a quietly wrong chip (the kit scaffold's own rule).
      throw new Error(
        `hh-shell-header was given the unknown sync state ${JSON.stringify(this.syncState)}; ` +
          `expected one of: ${SHELL_SYNC_STATES.join(', ')}`,
      );
    }
    if (changed.has('variant') && !(SHELL_HEADER_VARIANTS as readonly string[]).includes(this.variant)) {
      // Same rule as the sync chip: an unknown variant is a defect at the
      // call site, refused loudly, never a quietly wrong band.
      throw new Error(
        `hh-shell-header was given the unknown variant ${JSON.stringify(this.variant)}; ` +
          `expected one of: ${SHELL_HEADER_VARIANTS.join(', ')}`,
      );
    }
    if (changed.has('selectorOpen')) {
      if (this.selectorOpen) this.#startOutsideWatch();
      else this.#stopOutsideWatch();
    }
  }

  #emit(type: string, detail?: unknown): void {
    this.dispatchEvent(new CustomEvent(type, { detail, bubbles: true, composed: true }));
  }

  /** Every click rides a data-action name; the host's own listener acts. */
  readonly #onClick = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    const action = control.getAttribute('data-action');
    if (action === 'selector') {
      this.selectorOpen = !this.selectorOpen;
      if (this.selectorOpen) this.#focusFirstItem();
      return;
    }
    if (action === 'select') {
      const appId = control.getAttribute('data-app') ?? '';
      const surfaceId = control.getAttribute('data-surface') ?? '';
      this.selectorOpen = false;
      this.#emit('hh-shell-surface-open', { appId, surfaceId, key: surfaceKey({ appId, surfaceId }) });
      return;
    }
    if (action === 'pin') {
      this.#emit('hh-shell-pin', { key: this.currentKey, pinned: !this.pinned });
      return;
    }
    if (action === 'notifications') {
      this.#emit('hh-shell-notifications');
      return;
    }
    if (action === 'account') {
      this.#emit('hh-shell-account');
    }
  };

  /** The menu keyboard, the original launcher's, one keyboard for the house. */
  readonly #onKeyDown = (event: Event): void => {
    const key = (event as KeyboardEvent).key;
    const path = event.composedPath();
    const onControl = path.some(
      (node) => node instanceof Element && node.hasAttribute?.('data-action') && node.getAttribute('data-action') === 'selector',
    );
    if (onControl && key === 'ArrowDown' && !this.selectorOpen) {
      event.preventDefault();
      event.stopPropagation();
      this.selectorOpen = true;
      this.#focusFirstItem();
      return;
    }
    if (!this.selectorOpen) return;
    const items = this.#menuItems();
    if (items.length === 0) return;
    const here = items.indexOf(
      path.find((node) => node instanceof Element && node.classList?.contains('mi')) as HTMLButtonElement,
    );
    const move = (to: number): void => {
      event.preventDefault();
      event.stopPropagation();
      items[Math.max(0, Math.min(items.length - 1, to))]?.focus();
    };
    switch (key) {
      case 'ArrowDown':
        return move(here + 1);
      case 'ArrowUp':
        return move(here - 1);
      case 'Home':
        return move(0);
      case 'End':
        return move(items.length - 1);
      case 'Escape':
        event.preventDefault();
        event.stopPropagation();
        return this.#close(true);
      case 'Tab':
        // Tabbing away is a dismissal, not a trap.
        return this.#close();
      default:
    }
  };

  #menuItems(): HTMLButtonElement[] {
    if (typeof this.renderRoot.querySelectorAll !== 'function') return [];
    return [...this.renderRoot.querySelectorAll<HTMLButtonElement>('.panel .mi')];
  }

  #focusFirstItem(): void {
    void this.updateComplete.then(() => this.#menuItems()[0]?.focus());
  }

  /** Closes; refocus hands the ring back to the control, where Escape leaves it. */
  #close(refocus = false): void {
    if (!this.selectorOpen) return;
    this.selectorOpen = false;
    if (refocus) {
      void this.updateComplete.then(() =>
        this.renderRoot.querySelector<HTMLButtonElement>('button.selector')?.focus(),
      );
    }
  }

  readonly #onDocPointerDown = (event: Event): void => {
    if (event.composedPath().includes(this)) return;
    this.selectorOpen = false;
  };

  #startOutsideWatch(): void {
    if (typeof document === 'undefined') return;
    document.addEventListener('pointerdown', this.#onDocPointerDown, true);
  }

  #stopOutsideWatch(): void {
    if (typeof document === 'undefined') return;
    document.removeEventListener('pointerdown', this.#onDocPointerDown, true);
  }

  render(): Template {
    const chip = syncChipView({
      state: this.syncState as ShellSyncState,
      openCards: this.cards,
      waiting: this.waiting,
      attempts: this.attempts,
      stalls: this.stalls,
    });
    const initials = initialsOf(this.user);
    const bellName = this.notifications > 0 ? `Notifications, ${this.notifications} unread` : 'Notifications';
    const isBand = this.variant === 'band';
    return html`
      <header class="band" data-variant=${isBand ? 'band' : 'compact'} role="banner">
        <div class="brand">
          <span class="mark" aria-hidden="true">
            <svg viewBox="0 0 3000 3000"><path fill="currentColor" d="${MARK_PATH}"></path></svg>
          </span>
          ${this.org !== '' ? html`<span class="org">${this.org}</span>` : nothing}
          ${this.demo && !isBand ? html`<span class="demo" role="note">Demo</span>` : nothing}
        </div>
        <button
          class="selector"
          type="button"
          data-action="selector"
          aria-haspopup="menu"
          aria-expanded=${this.selectorOpen ? 'true' : 'false'}
        >
          <span class="stk" aria-hidden="true">
            <span class="stk-r"></span>
            <span class="stk-r"></span>
            <span class="stk-r"></span>
          </span>
          <span class="crumb">${this.breadcrumb === '' ? 'Surfaces' : this.breadcrumb}</span>
          <span class="caret" aria-hidden="true">
            <svg viewBox="0 0 10 6"><path d="M0 0h10L5 6z" fill="currentColor"></path></svg>
          </span>
        </button>
        <div class="cluster">
          ${this.demo && isBand ? html`<span class="demo" role="note">DEMO</span>` : nothing}
          <span class="chip" data-state=${this.syncState} data-kind=${chip.kind} data-alarm=${chip.alarm ? 'true' : 'false'}>
            <span class="dot" aria-hidden="true"></span>
            <span class="word">${chip.label}</span>
            ${chip.note !== '' ? html`<span class="note">${chip.note}</span>` : nothing}
          </span>
          <button class="icon-btn" type="button" data-action="notifications" aria-label=${bellName}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"></path>
              <path d="M13.7 21a2 2 0 0 1-3.4 0"></path>
            </svg>
            ${this.notifications > 0 ? html`<span class="badge" aria-hidden="true">${this.notifications}</span>` : nothing}
          </button>
          <button
            class="icon-btn"
            type="button"
            data-action="pin"
            aria-pressed=${this.pinned ? 'true' : 'false'}
            title="Pin this surface"
            aria-label="Pin this surface"
            ?disabled=${this.currentKey === ''}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M12 17v5"></path>
              <path d="M9 10.8V4h6v6.8l2.6 2.7a1 1 0 0 1-.7 1.7H7.1a1 1 0 0 1-.7-1.7z"></path>
            </svg>
          </button>
          <button class="account" type="button" data-action="account" aria-label=${this.user === '' ? 'Account' : `${this.user} account`}>
            <span class="avatar" aria-hidden="true" data-initials=${initials}>${initials}</span>
            <span class="account-word">${this.user === '' ? 'Account' : this.user}</span>
          </button>
        </div>
      </header>
      ${this.selectorOpen ? this.#renderPanel() : nothing}
    `;
  }

  #renderPanel(): Template {
    return html`
      <div class="panel" role="menu" aria-label="Surfaces">
        ${this.groups.map(
          (group) => html`
            <h4 class="group-label">${group.app.name}</h4>
            ${group.surfaces.length === 0
              ? this.#renderRow(group.app.id, group.app.name, '')
              : group.surfaces.map((s) => this.#renderRow(group.app.id, s.label, s.id))}
          `,
        )}
      </div>
    `;
  }

  #renderRow(appId: string, label: string, surfaceId: string): Template {
    const key = surfaceId === '' ? appId : `${appId}/${surfaceId}`;
    const current = key === this.currentKey;
    return html`
      <button
        class="mi"
        role="menuitem"
        tabindex="-1"
        data-action="select"
        data-app=${appId}
        data-surface=${surfaceId}
        data-key=${key}
        aria-current=${current ? 'true' : 'false'}
      >
        <span class="mi-label">${label}</span>
        ${current ? html`<span class="hint">here</span>` : nothing}
      </button>
    `;
  }
}

defineElement('hh-shell-header', ShellHeader);
