// The phone header: the large title row the Field phone apps carry instead of
// the desktop header band. One row: the back chevron (slot), the title (or
// slot), the sync dot (the state the band already names, the same words), the
// branch chip (a rounded chip), and the review bell with its badge.
//
// What the Field boards draw: a phone (390 wide) carries no desktop header band; a
// large title with the sync state as a small dot inline after it, the branch
// as a rounded chip, and Review as a bell with a count badge. The light page
// ground and white cards are already right, so this row is the same surface.
//
// Purely presentational: the shell reads its own sync state and hands the
// element the word (one of SHELL_SYNC_STATES); the dot's colour follows the
// chip kind the state module already answers for (syncChipView). The bell is
// a button; a press fires hh-phone-review (bubbles, composed) and the shell
// decides what Review means. The host's own listener reads the data-action
// name off composedPath, the design system's delegation pattern; the markup
// carries no listener spelling.
//
// Tokens only (no hex literals); no opacity for state; the chip kind comes
// from the state module, so a state the machine grows and this dot misses
// turns the suite red the way the band's own chip turns it.

import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import {
  initialsOf,
  monogramOf,
  SHELL_SYNC_STATES,
  syncChipView,
  type ShellSyncState,
} from './shell-state.js';

const styles = css`
  :host {
    display: block;
    container-type: inline-size;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  /* Its own class: the design system's base .row wraps, and this row must not. */
  .phone-row {
    display: flex;
    flex-wrap: nowrap;
    align-items: center;
    gap: var(--hh-s-12);
    min-height: 56px;
    padding: var(--hh-s-8) var(--hh-s-16);
    background: var(--hh-card);
    color: var(--hh-text);
    border-bottom: 1px solid var(--hh-line);
  }

  /* The back chevron slot: the host renders a button here on an inner screen, the r4 target; a root screen
   * slots nothing and the slot takes no room (the wrapper is display: contents). */
  .back { display: contents; }
  slot[name='back']::slotted(button) {
    flex: none;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    margin-left: calc(-1 * var(--hh-s-8));
    min-height: 0;
    padding: 0;
    border: 1px solid transparent;
    border-radius: var(--hh-radius-control);
    background: transparent;
    color: inherit;
    cursor: pointer;
  }
  slot[name='back']::slotted(button:hover) { color: var(--hh-text); }
  slot[name='back']::slotted(button:focus-visible) {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }

  /* Tenant lockup from config, sync pill, and user avatar: hidden on phone, shown on tablet (>= 600px). */
  .tenant-lockup {
    display: none;
    align-items: center;
    gap: var(--hh-s-8);
    flex: none;
    min-width: 0;
  }
  .tenant-monogram {
    flex: none;
    display: inline-grid;
    place-items: center;
    min-width: 28px;
    height: 28px;
    padding: 0 var(--hh-s-8);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: var(--hh-s-12);
    font-weight: 700;
    line-height: 1;
  }
  .tenant-name {
    font-size: var(--hh-s-14);
    font-weight: 700;
    color: var(--hh-text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* The title block: the large word and the sync dot that rides beside it. */
  .title-block {
    flex: 1 1 auto;
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
    min-width: 0;
  }
  .title {
    font-size: 28px;
    font-weight: 700;
    line-height: 1.2;
    color: var(--hh-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    min-width: 0;
  }

  /* The sync dot: a small inline marker, the same colour roles the band chip
   * carries, so a state that is good/busy/stop reads in the dot as the chip
   * itself would. The word never rides the dot alone: the title or the chip
   * already names the surface. */
  .dot {
    flex: none;
    width: 8px;
    height: 8px;
    border-radius: var(--hh-radius-pill);
    background: currentColor;
  }
  .dot[data-kind='good'] { color: var(--hh-state-good); }
  .dot[data-kind='busy'] { color: var(--hh-state-watch); }
  .dot[data-kind='stop'] { color: var(--hh-state-stop); }

  /* The branch chip: a rounded chip beside the title. */
  .branch {
    flex: none;
    display: inline-flex;
    align-items: center;
    height: 26px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: var(--hh-s-12);
    font-weight: 600;
    line-height: 1;
  }

  /* The tablet sync pill: hidden on phone, shown on tablet (>= 600px). */
  .sync-pill {
    display: none;
    flex: none;
    align-items: center;
    gap: var(--hh-s-8);
    height: 26px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 1px solid currentColor;
    font-size: var(--hh-s-12);
    font-weight: 600;
    line-height: 1;
    white-space: nowrap;
  }
  .sync-pill[data-kind='good'] { color: var(--hh-state-good); }
  .sync-pill[data-kind='busy'] { color: var(--hh-state-watch); }
  .sync-pill[data-kind='stop'] { color: var(--hh-state-stop); }

  /* The bell: the r4 target, a count badge at the top-right. The badge hides
   * at zero: a bell without a count is just a bell, and the word "Notifications"
   * alone is its accessible name. */
  .bell {
    flex: none;
    position: relative;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    padding: 0;
    background: transparent;
    color: var(--hh-text-dim);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    cursor: pointer;
    font-family: inherit;
  }
  .bell:hover:not(:disabled) {
    color: var(--hh-text);
    border-color: var(--hh-accent-text);
  }
  .bell:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .bell svg {
    display: block;
    width: 18px;
    height: 18px;
  }
  .badge {
    position: absolute;
    top: -6px;
    right: -6px;
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: 10px;
    font-weight: 700;
    line-height: 1;
    display: grid;
    place-items: center;
  }

  /* User initials avatar: hidden on phone, shown on tablet (>= 600px). */
  .user-avatar {
    display: none;
    flex: none;
    place-items: center;
    width: 32px;
    height: 32px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-field);
    border: 1px solid var(--hh-line);
    color: var(--hh-text);
    font-size: var(--hh-s-12);
    font-weight: 700;
    line-height: 1;
  }

  @media (min-width: 600px) {
    .phone-row {
      height: 48px;
      min-height: 48px;
      padding: 0 var(--hh-s-16);
    }
    .tenant-lockup {
      display: inline-flex;
    }
    .title {
      font-size: var(--hh-s-16);
      font-weight: 600;
    }
    .title-block .dot {
      display: none;
    }
    .sync-pill {
      display: inline-flex;
    }
    .user-avatar {
      display: inline-grid;
    }
  }
`;

export class PhoneHeader extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    title: { attribute: 'title' },
    syncState: { attribute: 'sync-state' },
    branch: { attribute: 'branch' },
    count: { type: Number, attribute: 'count' },
    org: { attribute: 'org' },
    monogram: { attribute: 'monogram' },
    user: { attribute: 'user' },
    syncNote: { attribute: 'sync-note' },
  };

  /** The row's large word; the slot wins when the host paints its own title. */
  declare title: string;
  /** The sync state the shell reports; the dot kind follows the chip's answer. */
  declare syncState: string;
  /** The branch chip's word; an empty branch drops the chip from the row. */
  declare branch: string;
  /** The review bell's count; zero hides the badge. */
  declare count: number;
  /** The tenant or dealer name from config for the tablet slim header. */
  declare org: string;
  /** Optional explicit monogram from tenant config; falls back to initials of org. */
  declare monogram: string;
  /** The signed-in user or role label for the tablet initials avatar. */
  declare user: string;
  /** Optional freshness note for the tablet sync pill. */
  declare syncNote: string;

  constructor() {
    super();
    this.title = '';
    this.syncState = 'idle';
    this.branch = '';
    this.count = 0;
    this.org = '';
    this.monogram = '';
    this.user = '';
    this.syncNote = '';
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

  protected override willUpdate(changed: PropertyValues): void {
    super.willUpdate?.(changed);
    // An unknown state is a defect at the call site: refuse loudly, never
    // paint a quietly wrong dot (the kit scaffold's own rule). The check
    // runs on every update (not just when the property changed), so a host
    // that hand-sets `syncState` after the first render still trips the
    // gate, and a passive read during the click delegation test does not
    // paint a quietly wrong dot.
    if (!(SHELL_SYNC_STATES as readonly string[]).includes(this.syncState)) {
      throw new Error(
        `hh-phone-header was given the unknown sync state ${JSON.stringify(this.syncState)}; ` +
          `expected one of: ${SHELL_SYNC_STATES.join(', ')}`,
      );
    }
  }

  /** The host reads data-action off composedPath: one click contract, no listener
   * spelling in markup, the design system's delegation pattern. */
  readonly #onClick = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    const action = control.getAttribute('data-action');
    if (action === 'review') {
      this.dispatchEvent(
        new CustomEvent('hh-phone-review', { bubbles: true, composed: true }),
      );
    }
  };

  render(): Template {
    const chip = syncChipView({
      state: this.syncState as ShellSyncState,
      openCards: 0,
      waiting: 0,
      attempts: 0,
      stalls: 0,
    });
    const bellName =
      this.count > 0 ? `Notifications, ${this.count} unread` : 'Notifications';
    const tenantMonogram = monogramOf(this.org, this.monogram);
    const userInitials = initialsOf(this.user);
    const pillNote =
      this.syncNote !== ''
        ? this.syncNote
        : this.syncState === 'idle'
          ? 'just now'
          : chip.note;
    const pillWords = pillNote !== '' ? `${chip.label} · ${pillNote}` : chip.label;
    return html`
      <header class="phone-row">
        <span class="back"><slot name="back"></slot></span>
        ${tenantMonogram !== '' || this.org !== ''
          ? html`<div class="tenant-lockup" data-tenant=${this.org}>
              ${tenantMonogram !== ''
                ? html`<span class="tenant-monogram" data-monogram=${tenantMonogram} aria-hidden="true">${tenantMonogram}</span>`
                : nothing}
              ${this.org !== '' ? html`<span class="tenant-name">${this.org}</span>` : nothing}
            </div>`
          : nothing}
        <div class="title-block">
          <span class="title">${this.title}</span>
          ${this.title !== ''
            ? html`<span class="dot" data-state=${this.syncState} data-kind=${chip.kind} aria-hidden="true"></span>`
            : nothing}
        </div>
        ${this.branch !== ''
          ? html`<span class="branch" data-branch=${this.branch}>${this.branch}</span>`
          : nothing}
        <span class="sync-pill" data-state=${this.syncState} data-kind=${chip.kind}>
          <span class="dot" aria-hidden="true"></span>
          <span class="sync-word">${pillWords}</span>
        </span>
        <button
          class="bell"
          type="button"
          data-action="review"
          aria-label=${bellName}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9"></path>
            <path d="M13.7 21a2 2 0 0 1-3.4 0"></path>
          </svg>
          ${this.count > 0 ? html`<span class="badge" aria-hidden="true">${this.count}</span>` : nothing}
        </button>
        ${this.user !== ''
          ? html`<span class="user-avatar" data-initials=${userInitials} role="img" aria-label=${this.user}>${userInitials}</span>`
          : nothing}
      </header>
    `;
  }
}

defineElement('hh-phone-header', PhoneHeader);