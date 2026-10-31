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
//   hh-shell-assist                                    the TanyAI button was pressed (the shell opens its hh-tanyai-panel)
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
   * selector centred by the grid's middle column, the chip run at the right.
   * The lockup is the dealer's word alone (rows 1520, 1826, 1856: one white
   * labeled band, never the HH mark beside the dealer name). */
  .band[data-variant='band'] {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    /* The board's band is 56px tall with its bottom rule inside: 44px controls
     * and a 5px pad above and below. */
    min-height: 56px;
    padding-block: 5px;
  }
  .band[data-variant='band'] .brand {
    justify-self: start;
  }
  .band[data-variant='band'] .lockup {
    color: var(--hh-text);
    font-size: 15px;
    font-weight: 700;
    letter-spacing: 0.01em;
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
  /* The board's DEMO marker is one outlined chip, never a second filled strip. */
  .band[data-variant='band'] .demo {
    background: transparent;
    color: var(--hh-accent-text);
    border: 1px solid currentColor;
  }
  /* Phone width: the band never widens the page. The lockup and the chip run
   * share the first row (the lockup truncates), the selector takes a row of its
   * own, and the account chip keeps its initial only. */
  @media (max-width: 640px) {
    .band[data-variant='band'] {
      grid-template-columns: minmax(0, 1fr) auto;
      padding-inline: var(--hh-s-12);
    }
    .band[data-variant='band'] .brand {
      min-width: 0;
      /* A nowrap word's min-content is its whole width, so the box is clamped to its track here: the lockup
       * gives way (ellipsis) before the run of chips and icons ever overlaps it. */
      max-width: 100%;
    }
    .band[data-variant='band'] .lockup {
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .band[data-variant='band'] .selector {
      grid-row: 2;
      grid-column: 1 / -1;
      justify-self: stretch;
      min-width: 0;
    }
    .band[data-variant='band'] .account-word {
      display: none;
    }
  }
  /* Small phones (390): the first row keeps the board's lockup and the run of icons, so the DEMO marker drops to the
   * right end of the selector's row (the last row, so it is anchored to the band's bottom) and the selector yields
   * its width. One DEMO marker still; it never sits over the lockup. */
  @media (max-width: 440px) {
    .band[data-variant='band'] {
      position: relative;
    }
    .band[data-variant='band'] .demo {
      position: absolute;
      right: var(--hh-s-12);
      bottom: 16px;
    }
    .band[data-variant='band'] .selector {
      margin-right: 72px;
    }
  }
  /* The account chip's menu: the actions the app-local header used to carry
   * (Theme, Feedback, Switch role, Sign out) ride the slot, so the band is
   * the one band. */
  .account-menu {
    position: absolute;
    top: calc(100% + 6px);
    right: var(--hh-s-16);
    z-index: var(--hh-z-dropdown);
    min-width: 220px;
    max-width: min(320px, calc(100% - var(--hh-s-32)));
    display: grid;
    gap: 2px;
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: 12px;
    box-shadow: var(--hh-elev-2);
    padding: 6px;
  }
  .account-menu p {
    margin: 0;
    padding: var(--hh-s-4) var(--hh-s-8);
    font-size: 13px;
    color: var(--hh-text-dim);
  }
  .account-menu strong {
    color: var(--hh-text);
  }
  /* One face for every action in the menu, whatever class the app gave the
   * button (btn-secondary, theme-btn): the app's own class sheets do not reach
   * into the band's shadow. */
  .account-menu button {
    width: 100%;
    min-height: 44px;
    display: flex;
    align-items: center;
    justify-content: flex-start;
    padding: 0 var(--hh-s-12);
    background: transparent;
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: 8px;
    font-family: inherit;
    font-size: 13px;
    text-align: left;
    cursor: pointer;
  }
  .account-menu button:hover:not(:disabled) {
    background: var(--hh-row-alt);
  }
  .account-menu button:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 1px;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
    flex: none;
  }
  /* The dealer's initial on the badge ground (row 1858: our own mark is never drawn on a dealer's screen). */
  .mark {
    width: 30px;
    height: 30px;
    flex: none;
    display: grid;
    place-items: center;
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: var(--hh-s-16);
    font-weight: 700;
    line-height: 1;
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
  .icon-btn[aria-pressed='true'] svg path:last-child {
    fill: currentColor;
  }
  /* A pinned icon button that is refused is a disabled control, and the
     pressed rule above outranks the primitive's own disabled rule. The pair
     is restated in the grey triple, so the pin says nothing in brand
     colour while it cannot be pressed. */
  .icon-btn[aria-pressed='true']:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
    box-shadow: inset 0 0 0 2px var(--hh-disabled-ink);
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
    assist: { type: Boolean, attribute: 'assist' },
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
    accountOpen: { type: Boolean, attribute: 'account-open' },
    accountMenu: { attribute: false },
    theme: { attribute: 'theme' },
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
  /** The host mounts a TanyAI panel and answers the assist press: only then does the band carry the Ask TanyAI
   * button, so no band ever shows a button that does nothing. */
  declare assist: boolean;
  /** The account chip's menu, open or closed. The chip toggles it in the band
   * variant; a shell may set it too. */
  declare accountOpen: boolean;
  /** The account menu's content: the app hands its own template (the Theme,
   * Feedback, Switch role and Sign out actions the app-local header used to
   * carry). It renders inside the band's shadow, and the actions' clicks
   * bubble back to the app's own delegation, so no listener moves. */
  declare accountMenu: unknown;
  /** The theme the band applies to the document root, the sheet's own
   * contract: light sets data-theme, dark removes it. The app drives it from
   * its own theme control in the account menu. */
  declare theme: string;

  constructor() {
    super();
    this.org = '';
    this.demo = false;
    this.assist = false;
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
    this.accountOpen = false;
    this.accountMenu = null;
    this.theme = '';
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
    if (changed.has('theme') && typeof document !== 'undefined') {
      // The theme is a document concern (the sheet's own attribute contract),
      // applied here because the app-local header that used to apply it is
      // gone in the band's world: one band carries it.
      const root: Document['documentElement'] | undefined = document.documentElement;
      if (root !== undefined && root !== null) {
        if (this.theme === 'light') root.setAttribute('data-theme', 'light');
        else if (this.theme === 'dark') root.removeAttribute('data-theme');
      }
    }
    if (changed.has('selectorOpen') || changed.has('accountOpen')) {
      if (this.selectorOpen || this.accountOpen) this.#startOutsideWatch();
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
    if (action === 'assist') {
      this.#emit('hh-shell-assist');
      return;
    }
    if (action === 'account') {
      // The band variant's chip opens its own menu (the slot's actions); the
      // event still rides for a shell that carries a menu of its own.
      if (this.variant === 'band') {
        this.accountOpen = !this.accountOpen;
        if (this.accountOpen) this.#focusAccountMenu();
      }
      this.#emit('hh-shell-account');
    }
  };

  #focusAccountMenu(): void {
    void this.updateComplete.then(() => {
      const first = this.renderRoot.querySelector('.account-menu button');
      (first instanceof HTMLElement ? first : null)?.focus();
    });
  }

  /** The menu keyboard, the original launcher's, one keyboard for the house. */
  readonly #onKeyDown = (event: Event): void => {
    const key = (event as KeyboardEvent).key;
    if (this.accountOpen && key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      this.accountOpen = false;
      void this.updateComplete.then(() =>
        this.renderRoot.querySelector<HTMLButtonElement>('button.account')?.focus(),
      );
      return;
    }
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
    this.accountOpen = false;
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
          ${isBand || this.org === ''
            ? nothing
            : html`<span class="mark" aria-hidden="true">${this.org.trim().slice(0, 1).toUpperCase()}</span>`}
          ${this.org !== ''
            ? isBand
              ? html`<span class="lockup">${this.org}</span>`
              : html`<span class="org">${this.org}</span>`
            : nothing}
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
          ${this.assist
            ? html`<button class="icon-btn" type="button" data-action="assist" aria-label="Ask TanyAI" title="Ask TanyAI">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"></path>
            </svg>
          </button>`
            : nothing}
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
          <button
            class="account"
            type="button"
            data-action="account"
            aria-haspopup=${isBand ? 'menu' : nothing}
            aria-expanded=${isBand ? (this.accountOpen ? 'true' : 'false') : nothing}
            aria-label=${this.user === '' ? 'Account' : `${this.user} account`}
          >
            <span class="avatar" aria-hidden="true" data-initials=${initials}>${initials}</span>
            <span class="account-word">${this.user === '' ? 'Account' : this.user}</span>
          </button>
        </div>
      </header>
      ${this.selectorOpen ? this.#renderPanel() : nothing}
      ${this.accountOpen && isBand
        ? html`<div class="account-menu" role="group" aria-label="Account actions">${this.accountMenu ?? nothing}</div>`
        : nothing}
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
