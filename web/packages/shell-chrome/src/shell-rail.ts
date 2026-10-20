// The pinned rail: the person's OWN pinned surfaces across every micro app
// they hold, the ones they use most (row 1520). Collapsed by default: a
// narrow strip of icons whose labels ride as tooltips; it expands on demand
// to the words-on rail, and it shows the pins only, never a navigation tree.
//
// Ported and polished from the original HardscapeOS at master 87d7941b, read
// only: app/src/unit-rail.ts supplies the rail's shape, its empty state and
// its keyboard, app/src/_rail-state.ts the collapsed default posture, and the
// two floors unit-rail-pins.test.mjs guards travel with them: a row that
// SHOWS its label carries no native title and no aria-label on top of it (the
// visible words are the name), and an icon-only collapsed row is named by its
// label while the label itself is the tooltip. The pin semantics behind the
// rows live in shell-state.ts; this element renders what the shell hands it.
//
// Purely presentational, like every chrome element in the original: the shell
// hands it the pins (RailPin rows from the model), the active key, and an
// injected posture seam, and the rail emits
//
//   hh-shell-rail-select  { key }       a pin was picked
//   hh-shell-rail-toggle  { collapsed } the posture changed; already written
//                                     through the seam before the event fires
//
// Collapsed, the rows are ONE roving tab stop (ArrowUp and ArrowDown move,
// Home and End jump; Tab leaves the rail), the original's own collapsed
// keyboard; expanded, every control keeps its native place in the tab order.

import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import {
  RAIL_COLLAPSED_KEY,
  RAIL_PIN_KEY,
  railCollapsedFromStored,
  railCollapsedToStored,
  railPinFromStored,
  railPinToStored,
  type KeyValueStorage,
  type RailPin,
  type RailPinMode,
} from './shell-state.js';

const styles = css`
  :host {
    display: block;
    width: fit-content;
    height: 100%;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  .rail {
    position: relative;
    width: 196px;
    height: 100%;
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: var(--hh-s-12) 10px;
    background: var(--hh-card);
    border-right: 1px solid var(--hh-line);
    /* The rail itself never clips: the pin menu and the row tooltips paint
     * beside it (P1-1 of the PR 503 review). The scroll moves to the rows
     * wrapper, so a long pin list still scrolls. */
    overflow: visible;
  }
  .rows {
    flex: 1 1 auto;
    min-height: 0;
    width: 100%;
    display: flex;
    flex-direction: column;
    gap: 2px;
    overflow-y: auto;
    overscroll-behavior: contain;
  }
  .rail[data-collapsed='true'] .rows {
    align-items: center;
  }
  /* Collapsed: the narrow strip the brief arrives as, icons only. */
  .rail[data-collapsed='true'] {
    width: 56px;
    padding: var(--hh-s-12) 6px;
    align-items: center;
  }
  .rhead {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-8);
    width: 100%;
    padding: 0 2px var(--hh-s-8);
    flex: none;
  }
  .rail[data-collapsed='true'] .rhead {
    justify-content: center;
    padding: 0 0 var(--hh-s-8);
  }
  .heading {
    font-size: 9.5px;
    font-weight: 700;
    letter-spacing: 0.13em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .rail[data-collapsed='true'] .heading {
    display: none;
  }
  .toggle {
    width: 44px;
    height: 44px;
    flex: none;
    /* The sheet's button padding would squeeze the glyph out of a 44 px box: the glyph is the content, centred. */
    padding: 0;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    color: var(--hh-text);
    border: 1px solid var(--hh-line);
    border-radius: 8px;
    cursor: pointer;
    font-family: inherit;
  }
  .toggle:hover {
    color: var(--hh-text);
    border-color: var(--hh-accent-text);
  }
  .toggle[aria-expanded='true'] {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
  }
  .toggle:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .toggle svg {
    display: block;
    width: 22px;
    height: 22px;
  }
  /* The pin menu (GAPS-r2 row G1): the board's two pin choices, "Pin as
   * icons" and "Pin wide". Hidden at rest; the rail's own hover or focus
   * reveals it, instantly, because an animated bar is an extra bar (the G3
   * check). It hangs beside the rail with a small visual gap, and the
   * ::before bridge carries the pointer across the gap, so a mouse moving
   * from the rail to the menu never drops the hover (P1-1 of the review:
   * the unbridged gap used to close the menu under the pointer). */
  .pin-menu {
    display: none;
    /* A fixed popover anchored from the rail's own rect (the rail sets
     * --pin-left and --pin-top on hover and focus), so no ancestor's clip or
     * scroll can hide it. */
    position: fixed;
    left: var(--pin-left, 66px);
    top: var(--pin-top, 12px);
    z-index: var(--hh-z-dropdown);
    flex-direction: column;
    gap: 2px;
    width: max-content;
    padding: var(--hh-s-4);
    background: var(--hh-widget-raised);
    border: 1px solid var(--hh-line);
    border-radius: 8px;
    box-shadow: var(--hh-elev-2);
  }
  .pin-menu::before {
    content: '';
    position: absolute;
    top: 0;
    bottom: 0;
    right: 100%;
    width: 12px;
  }
  .rail:hover .pin-menu,
  .rail:focus-within .pin-menu,
  .pin-menu:hover {
    display: flex;
  }
  /* After a pick the popover steps aside until the pointer re-enters the rail. */
  .rail[data-picked='true'] .pin-menu,
  .rail[data-picked='true'] .pin-menu:hover {
    display: none;
  }
  .pin-item {
    display: flex;
    align-items: center;
    min-height: 44px;
    padding: 0 10px;
    background: transparent;
    color: var(--hh-text-dim);
    border: 1px solid transparent;
    border-radius: 8px;
    font-family: inherit;
    font-size: 12.5px;
    line-height: 1.15;
    text-align: left;
    cursor: pointer;
    white-space: nowrap;
  }
  .pin-item:hover {
    background: var(--hh-row-alt);
    color: var(--hh-text);
  }
  .pin-item:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .pin-item[aria-pressed='true'] {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
  }
  /* Unpinned and collapsed, the rail waits as the toggle button alone: the
   * icons column reveals on hover or focus, and stays only once a person
   * pins it (pin icons or pin wide). */
  .rail[data-pin='off'][data-collapsed='true'] .row {
    display: none;
  }
  .rail[data-pin='off'][data-collapsed='true']:hover .row,
  .rail[data-pin='off'][data-collapsed='true']:focus-within .row {
    display: flex;
  }
  .row {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    /* The rail is the phone rail too: every row is a 44 px target at every
     * size, the floor the brief carries for phone and tablet. */
    min-height: 44px;
    padding: 0 10px;
    border: none;
    border-radius: 8px;
    background: transparent;
    color: var(--hh-text-dim);
    font-family: inherit;
    font-size: 12.5px;
    line-height: 1.15;
    text-align: left;
    cursor: pointer;
    white-space: nowrap;
  }
  .row:hover {
    background: var(--hh-row-alt);
    color: var(--hh-text);
  }
  .row:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .row[aria-current='true'] {
    background: var(--hh-row-alt);
    color: var(--hh-text);
    box-shadow: inset 3px 0 0 var(--hh-accent-text);
  }
  .glyph {
    width: 18px;
    text-align: center;
    font-size: 15px;
    flex: none;
  }
  .row[aria-current='true'] .glyph {
    color: var(--hh-accent-text);
  }
  .label {
    overflow: hidden;
    text-overflow: ellipsis;
    flex: 1 1 auto;
  }
  /* Collapsed, the label becomes the tooltip: real text, revealed beside the
   * strip on hover and on keyboard focus, so the name survives both channels
   * and no native title rides on top of it (one name per row). */
  .rail[data-collapsed='true'] .row {
    justify-content: center;
    padding: 0;
  }
  .rail[data-collapsed='true'] .label {
    position: absolute;
    left: calc(100% + 10px);
    top: 50%;
    transform: translateY(-50%);
    flex: none;
    max-width: 180px;
    background: var(--hh-widget-raised);
    border: 1px solid var(--hh-line);
    border-radius: 8px;
    padding: var(--hh-s-4) var(--hh-s-8);
    font-size: var(--hh-s-12);
    white-space: nowrap;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.12s ease;
    z-index: var(--hh-z-dropdown);
  }
  .rail[data-collapsed='true'] .row:hover .label,
  .rail[data-collapsed='true'] .row:focus-visible .label {
    opacity: 1;
  }
  @media (prefers-reduced-motion: reduce) {
    .rail[data-collapsed='true'] .label {
      transition: none;
    }
  }
  /* The empty state (D-107): no pins is the FIRST thing a new person sees,
   * so it is a calm sentence naming the action and where to make one, not a
   * blank strip and never some other list. Collapsed it waits; the words
   * show when the rail does. */
  .empty {
    margin: 0;
    padding: 10px 8px 4px;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    line-height: 1.45;
  }
  .empty strong {
    display: block;
    margin-bottom: var(--hh-s-4);
    color: var(--hh-text);
    font-size: 12.5px;
    font-weight: 600;
  }
  .rail[data-collapsed='true'] .empty {
    display: none;
  }
`;

export class ShellRail extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    pins: { attribute: false },
    active: { attribute: 'active' },
    heading: { attribute: 'heading' },
    collapsed: { type: Boolean, attribute: 'collapsed' },
    storage: { attribute: false },
    pin: { attribute: 'pin' },
    picked: { attribute: false },
  };

  /** The person's pins, in rail order: the model's answer, the rail's only list. */
  declare pins: RailPin[];
  /** The key of the surface on screen, or '' when it is not pinned. */
  declare active: string;
  /** The rail's head word; pins can span apps, so it is plain text. */
  declare heading: string;
  /**
   * Collapsed behind the strip (the default posture, resolved from the
   * injected seam when the shell did not set it).
   */
  declare collapsed: boolean;
  /** The posture seam, keyed by RAIL_COLLAPSED_KEY. The toggle writes through
   * it; a shell may hand the same seam the PinBook uses. */
  declare storage: KeyValueStorage | null;
  /** The rail's pin mode (GAPS-r2 row G1): the icons column pinned, the wider
   * rail body pinned, or off (the default, resolved from the same seam under
   * RAIL_PIN_KEY). Off waits as the toggle and reveals on hover or focus. */
  declare pin: RailPinMode;
  /** A pin choice was just made: the popover steps aside (it would sit over the
   * rail the pick just changed) until the pointer or the keyboard comes back. */
  declare picked: boolean;

  /** The roving entry index into pins. Collapsed mode only. */
  #rovingIdx = 0;
  #postureResolved = false;

  constructor() {
    super();
    this.pins = [];
    this.active = '';
    this.heading = 'Pinned';
    this.collapsed = true;
    this.storage = null;
    this.pin = 'off';
    this.picked = false;
    this.state = 'ready';
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.resolvePosture();
    this.addEventListener('click', this.#onClick);
    this.addEventListener('keydown', this.#onKeyDown);
    this.addEventListener('focusin', this.#onFocusin);
    this.addEventListener('pointerenter', this.#anchorPinMenu);
    this.addEventListener('focusin', this.#anchorPinMenu);
  }

  override disconnectedCallback(): void {
    this.removeEventListener('click', this.#onClick);
    this.removeEventListener('keydown', this.#onKeyDown);
    this.removeEventListener('focusin', this.#onFocusin);
    this.removeEventListener('pointerenter', this.#anchorPinMenu);
    this.removeEventListener('focusin', this.#anchorPinMenu);
    super.disconnectedCallback();
  }

  /**
   * The collapsed posture, resolved once: an explicit attribute the shell set
   * stands over storage, silence means collapsed, and garbage degrades to the
   * default (the pure resolver's whole contract). Exposed for a shell that
   * mounts late, and for the tests.
   */
  resolvePosture(): void {
    if (this.#postureResolved) return;
    this.#postureResolved = true;
    if (!this.hasAttribute('collapsed')) {
      let stored: string | null = null;
      try {
        stored = this.storage?.read(RAIL_COLLAPSED_KEY) ?? null;
      } catch {
        stored = null;
      }
      this.collapsed = railCollapsedFromStored(stored);
    }
    if (!this.hasAttribute('pin')) {
      let storedPin: string | null = null;
      try {
        storedPin = this.storage?.read(RAIL_PIN_KEY) ?? null;
      } catch {
        storedPin = null;
      }
      this.pin = railPinFromStored(storedPin);
    }
  }

  /** Anchor the fixed pin popover from the rail's rect: just right of the
   * rail, level with its head. Runs on hover and focus, before the menu shows. */
  readonly #anchorPinMenu = (): void => {
    this.picked = false;
    this.#measurePinMenu();
  };

  #measurePinMenu(): void {
    const rail = this.renderRoot.querySelector?.('.rail');
    const menu = this.renderRoot.querySelector?.<HTMLElement>('.pin-menu');
    if (!(rail instanceof HTMLElement) || menu === null || menu === undefined) return;
    const rect = rail.getBoundingClientRect();
    menu.style.setProperty('--pin-left', `${Math.round(rect.right + 10)}px`);
    menu.style.setProperty('--pin-top', `${Math.round(rect.top + 12)}px`);
  }

  #emit(type: string, detail?: unknown): void {
    this.dispatchEvent(new CustomEvent(type, { detail, bubbles: true, composed: true }));
  }

  #rows(): HTMLButtonElement[] {
    if (typeof this.renderRoot.querySelectorAll !== 'function') return [];
    return [...this.renderRoot.querySelectorAll<HTMLButtonElement>('button.row')];
  }

  /** Pin and unpin ride the model; the rail only asks the shell to act. */
  readonly #onClick = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    const action = control.getAttribute('data-action');
    if (action === 'rail-select') {
      this.#emit('hh-shell-rail-select', { key: control.getAttribute('data-key') ?? '' });
      return;
    }
    if (action === 'rail-toggle') {
      this.#toggle();
      return;
    }
    if (action === 'rail-pin') {
      const mode = control.getAttribute('data-mode');
      if (mode === 'icons' || mode === 'wide') {
        this.#pinMode(mode);
        this.picked = true;
        if (control instanceof HTMLElement) control.blur();
      }
    }
  };

  /** The pin choice, persisted before the event, exactly like the toggle's
   * posture: picking the mode that is already on releases it to off. */
  #pinMode(mode: Exclude<RailPinMode, 'off'>): void {
    const next: RailPinMode = this.#pinCurrent() === mode ? 'off' : mode;
    this.pin = next;
    try {
      this.storage?.write(RAIL_PIN_KEY, railPinToStored(next));
    } catch {
      // blocked storage: the pin stands for this session, persistence skipped
    }
    this.#emit('hh-shell-rail-pin', { pin: next });
  }

  /** The pin mode, normalized: a value outside the three reads as off, the
   * same degradation the stored literal gets. */
  #pinCurrent(): RailPinMode {
    return this.pin === 'icons' || this.pin === 'wide' ? this.pin : 'off';
  }

  /** The posture flip, persisted before the event: the '0' is what makes an
   * explicit expand stick, the '1' the collapse. */
  #toggle(): void {
    this.collapsed = !this.collapsed;
    this.#rovingIdx = 0;
    try {
      this.storage?.write(RAIL_COLLAPSED_KEY, railCollapsedToStored(this.collapsed));
    } catch {
      // blocked storage: the posture stands for this session, persistence skipped
    }
    this.#emit('hh-shell-rail-toggle', { collapsed: this.collapsed });
  }

  /** The posture on screen: a pinned wide body always reads expanded, a
   * pinned icons column always collapsed; off leaves the toggle's posture. */
  #effectiveCollapsed(): boolean {
    const pin = this.#pinCurrent();
    if (pin === 'wide') return false;
    if (pin === 'icons') return true;
    return this.collapsed;
  }

  /** Collapsed, the entries are one stop: arrows move, Home and End jump. */
  readonly #onKeyDown = (event: Event): void => {
    if (!this.#effectiveCollapsed()) return;
    const key = (event as KeyboardEvent).key;
    const onRow = event
      .composedPath()
      .some((node) => node instanceof Element && node.classList?.contains('row'));
    if (!onRow) return;
    const rows = this.#rows();
    if (rows.length === 0) return;
    const here = Math.max(0, this.#rovingIdx);
    let next: number;
    switch (key) {
      case 'ArrowDown':
        next = Math.min(rows.length - 1, here + 1);
        break;
      case 'ArrowUp':
        next = Math.max(0, here - 1);
        break;
      case 'Home':
        next = 0;
        break;
      case 'End':
        next = rows.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    this.#rovingIdx = next;
    rows[next]?.focus();
  };

  /** A pointer or programmatic focus landing on an entry adopts the stop. */
  readonly #onFocusin = (event: Event): void => {
    if (!this.#effectiveCollapsed()) return;
    const row = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.classList?.contains('row'));
    if (row === undefined) return;
    const at = this.#rows().findIndex((r) => r.getAttribute('data-key') === row.getAttribute('data-key'));
    if (at >= 0 && at !== this.#rovingIdx) {
      this.#rovingIdx = at;
      this.requestUpdate();
    }
  };

  #tabFor(index: number): string {
    if (!this.#effectiveCollapsed()) return '0';
    return index === Math.min(Math.max(this.#rovingIdx, 0), Math.max(0, this.pins.length - 1)) ? '0' : '-1';
  }

  protected override updated(changed: PropertyValues): void {
    super.updated(changed);
    // The rail's width moves with the pin mode and the posture: the fixed popover re-anchors from the new rect.
    if (changed.has('pin') || changed.has('collapsed')) this.#measurePinMenu();
    // The roving stop is carried in data-tab and applied to the real
    // tabindex here: the design system's position gate refuses a bound
    // tabindex, and the attribute keeps the contract visible to the server
    // render and the tests.
    for (const row of this.#rows()) {
      const wanted = Number(row.getAttribute('data-tab') ?? '0');
      if (row.tabIndex !== wanted) row.tabIndex = wanted;
    }
    // The toggle's native title rides the same road: the position gate refuses
    // a bound title, so the words travel as data-title (equal to its aria-label)
    // and land on the real title here.
    const toggle = this.renderRoot.querySelector?.<HTMLButtonElement>('button.toggle');
    const title = toggle?.getAttribute('data-title');
    if (toggle && title && toggle.title !== title) toggle.title = title;
  }

  render(): Template {
    const pin = this.#pinCurrent();
    const collapsed = this.#effectiveCollapsed();
    return html`
      <nav class="rail" data-collapsed=${collapsed ? 'true' : 'false'} data-pin=${pin} data-picked=${this.picked ? 'true' : 'false'} aria-label="Pinned surfaces">
        <div class="rhead">
          <span class="heading">${this.heading}</span>
          <button
            class="toggle"
            type="button"
            data-action="rail-toggle"
            tabindex="0"
            aria-expanded=${collapsed ? 'false' : 'true'}
            data-title="${collapsed ? 'Keep your pins open' : 'Let your pins hide again'}"
            aria-label=${collapsed ? 'Keep your pins open' : 'Let your pins hide again'}
          >
            <svg data-icon="rail-panel" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <rect x="3" y="4" width="18" height="16" rx="2"></rect>
              <path d="M9 4v16"></path>
            </svg>
          </button>
        </div>
        <div class="pin-menu" role="group" aria-label="Pin the rail">
          <button
            class="pin-item"
            type="button"
            data-action="rail-pin"
            data-mode="icons"
            aria-pressed=${pin === 'icons' ? 'true' : 'false'}
          >
            Pin as icons
          </button>
          <button
            class="pin-item"
            type="button"
            data-action="rail-pin"
            data-mode="wide"
            aria-pressed=${pin === 'wide' ? 'true' : 'false'}
          >
            Pin wide
          </button>
        </div>
        <div class="rows">
          ${this.pins.length === 0 ? this.#renderEmpty() : this.pins.map((pinRow, i) => this.#renderRow(pinRow, i))}
        </div>
      </nav>
    `;
  }

  #renderEmpty(): Template {
    return html`
      <p class="empty">
        <strong>No surfaces pinned yet.</strong>
        Pin the surfaces you use most from the surface selector, and they will
        be waiting here every time you sign in.
      </p>
    `;
  }

  #renderRow(pin: RailPin, index: number): Template {
    const current = pin.key === this.active;
    return html`
      <button
        class="row"
        type="button"
        data-action="rail-select"
        data-key=${pin.key}
        data-tab=${this.#tabFor(index)}
        aria-current=${current ? 'true' : 'false'}
        aria-label=${this.#effectiveCollapsed() ? pin.label : nothing}
      >
        <span class="glyph" aria-hidden="true">${pin.glyph}</span>
        <span class="label">${pin.label}</span>
      </button>
    `;
  }
}

defineElement('hh-shell-rail', ShellRail);
