// The Field frame (GAPS-r2 row G2): the one frame Yard Pick, HH Drive and House Messages share, in place of each
// app's own header. The boards' frame is a menu button, the dealer lockup from the tenant config (never an HH mark,
// never a hardcoded dealer), a selector chip naming the app and surface, the sync state, a bell (where the app has something to open behind it) and an account chip
// (initials plus the role label); on a phone a bottom tab bar (Pick, Drive, Messages, More) shows the tabs the role
// holds. The Demo state is a small DEMO chip, not a full width block.
//
// Field rule (row 1520): no navigation tree. The menu button opens the role's own apps as a short list; the account
// chip's menu holds Switch role, Sign out, Theme and (where the app has one) Feedback. The More tab opens the same
// account menu, so every control stays reachable on a phone.
//
// Purely presentational, like the other chrome elements: the app feeds properties and listens for clicks. Each
// control rides a data-action name (switch-role, sign-out, theme, feedback, and the bell's own), so the app's one host
// listener acts on it through the shadow boundary exactly as it did for the design system's header. The menus' keyboard
// is the one keyboard of every menu in the house: ArrowUp and ArrowDown move, Home and End jump, Escape closes and
// hands the ring back to the control, Tab closes.

import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { hhAddress } from '@hardscapehouse/design-system/src/address.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import { initialsOf, syncChipView, type ShellSyncState } from './shell-state.js';

/** The three Field apps a tab can name. */
export type FieldTabId = 'pick' | 'drive' | 'messages';

export interface FieldTab {
  id: FieldTabId;
  label: string;
  href: string;
}

/** Where each Field app lives on the box (the front door's tiles name the same prefixes). */
export const FIELD_TAB_HREFS: Readonly<Record<FieldTabId, string>> = {
  pick: '/yard/',
  drive: '/drive/',
  messages: '/messages/',
};

const FIELD_TAB_LABELS: Readonly<Record<FieldTabId, string>> = {
  pick: 'Pick',
  drive: 'Drive',
  messages: 'Messages',
};

/** The role codes that hold each Field app (the manifest's own role lists). House Messages is held by every role. */
const PICK_ROLES = new Set(['yard_team', 'yard_manager', 'general_manager']);
const DRIVE_ROLES = new Set(['driver', 'yard_manager', 'general_manager']);

/** The tabs a role holds, in the board's order; a role no app names still holds Messages. */
export function fieldTabsForRole(roleCode: string): FieldTab[] {
  const held: FieldTabId[] = [];
  if (PICK_ROLES.has(roleCode)) held.push('pick');
  if (DRIVE_ROLES.has(roleCode)) held.push('drive');
  held.push('messages');
  return held.map((id) => ({ id, label: FIELD_TAB_LABELS[id], href: FIELD_TAB_HREFS[id] }));
}

/** The surface chip's words: the app, then the surface ("Drive > My run"); no surface names the app alone. */
export function fieldBreadcrumb(appName: string, surfaceLabel: string): string {
  const surface = surfaceLabel.trim();
  return surface === '' ? appName : `${appName} > ${surface}`;
}

/** The role label the account chip shows, without the demo personas' " (demo)" suffix. */
export function fieldRoleWords(roleLabel: string): string {
  return roleLabel.replace(/\s*\(demo\)\s*$/i, '').trim();
}

/** The bottom padding a Field app's page keeps on a phone so its last row clears the tab bar (the bar is fixed to the
 * foot): the bar's own height plus the safe area. Apps put it on their host under the phone media query. */
export const FIELD_TABBAR_CLEARANCE = 'calc(var(--hh-s-64) + var(--hh-s-24) + var(--hh-safe-bottom))';

/** The properties an app hands the frame after each render (the position gate keeps bound values out of templates). */
export interface FieldFrameProps {
  org: string;
  appName: string;
  surface: string;
  roleLabel: string;
  syncState: ShellSyncState;
  theme: 'light' | 'dark';
  signedIn: boolean;
  demo: boolean;
  feedback: boolean;
  current: FieldTabId;
  tabs: FieldTab[];
  /** The data-action the bell presses (the app's own door for what waits on the person); empty draws no bell. */
  bellAction: string;
  /** What waits behind the bell: zero draws the quiet bell, more a count badge. */
  bellCount: number;
}

/** Sets the frame's properties in one pass; the frame renders once per change. */
export function setFieldFrame(frame: Element | null, props: Partial<FieldFrameProps>): void {
  if (frame === null) return;
  Object.assign(frame, props);
}

const styles = css`
  :host {
    display: block;
    position: relative;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  .band {
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
    min-height: var(--hh-s-64);
    padding: var(--hh-s-8) var(--hh-s-16);
    background: var(--hh-card);
    border-bottom: 1px solid var(--hh-line);
  }
  button, a { font-family: inherit; }
  /* One host knob: --hh-field-control-min raises the floor of every control in the frame (an app with a taller
     target floor sets it on the element); unset, the design system's own 44 px input height holds. */
  .icon-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    position: relative;
    width: max(var(--hh-h-input), var(--hh-field-control-min, 0px));
    height: max(var(--hh-h-input), var(--hh-field-control-min, 0px));
    padding: 0;
    background: transparent;
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    cursor: pointer;
  }
  .icon-btn svg { display: block; width: var(--hh-s-20); height: var(--hh-s-20); }
  .icon-btn:hover, .icon-btn[aria-expanded='true'] { border-color: var(--hh-accent-text); }
  :is(.icon-btn, .selector, .account, .mi, .tab):focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .badge {
    position: absolute;
    top: calc(-1 * var(--hh-s-4));
    right: calc(-1 * var(--hh-s-4));
    min-width: var(--hh-s-16);
    height: var(--hh-s-16);
    padding: 0 var(--hh-s-4);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: var(--hh-s-12);
    font-weight: 700;
    line-height: 1;
    display: grid;
    place-items: center;
  }

  .lockup { display: flex; align-items: center; gap: var(--hh-s-8); min-width: 0; flex: none; }
  .lockup-mark {
    width: var(--hh-s-32);
    height: var(--hh-s-32);
    flex: none;
    display: grid;
    place-items: center;
    border-radius: var(--hh-radius-pill);
    border: 2px solid var(--hh-text);
    font-size: var(--hh-s-12);
    font-weight: 700;
    color: var(--hh-text);
  }
  .lockup-name {
    font-size: var(--hh-s-16);
    font-weight: 700;
    letter-spacing: 0.06em;
    text-transform: uppercase;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    max-width: 28ch;
  }
  .demo {
    flex: none;
    padding: var(--hh-s-2) var(--hh-s-8);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    border-radius: var(--hh-radius-pill);
    font-size: var(--hh-s-12);
    font-weight: 700;
    line-height: 1.3;
    letter-spacing: 0.04em;
  }

  .selector {
    display: inline-flex;
    align-items: center;
    min-width: 0;
    height: var(--hh-h-input);
    padding: 0 var(--hh-s-12);
    margin-left: var(--hh-s-8);
    background: var(--hh-field);
    color: var(--hh-text);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    font-size: var(--hh-s-12);
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .cluster { margin-left: auto; display: flex; align-items: center; gap: var(--hh-s-8); }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    height: var(--hh-s-24);
    padding: 0 var(--hh-s-8);
    border-radius: var(--hh-radius-pill);
    border: 1px solid currentColor;
    font-size: var(--hh-s-12);
    font-weight: 700;
    white-space: nowrap;
  }
  .chip .dot { width: var(--hh-s-8); height: var(--hh-s-8); border-radius: var(--hh-radius-pill); background: currentColor; flex: none; }
  .chip .note { color: var(--hh-text-dim); font-weight: 400; }
  .chip[data-kind='good'] { color: var(--hh-state-good); }
  .chip[data-kind='busy'] { color: var(--hh-state-watch); }
  .chip[data-kind='stop'] { color: var(--hh-state-stop); }

  .account {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    height: max(var(--hh-h-input), var(--hh-field-control-min, 0px));
    padding: 0 var(--hh-s-12) 0 var(--hh-s-4);
    background: transparent;
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    font-size: var(--hh-s-12);
    font-weight: 600;
    white-space: nowrap;
    cursor: pointer;
  }
  .account:hover, .account[aria-expanded='true'] { border-color: var(--hh-accent-text); }
  .avatar {
    width: var(--hh-s-32);
    height: var(--hh-s-32);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-field);
    border: 1px solid var(--hh-line);
    display: grid;
    place-items: center;
    font-size: var(--hh-s-12);
    font-weight: 600;
    flex: none;
  }
  .account-word { max-width: 20ch; overflow: hidden; text-overflow: ellipsis; }

  .panel {
    position: absolute;
    top: calc(100% + var(--hh-s-4));
    z-index: var(--hh-z-dropdown);
    min-width: var(--hh-form-col);
    max-width: calc(100% - var(--hh-s-32));
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    box-shadow: var(--hh-elev-2);
    padding: var(--hh-s-4);
  }
  .panel[data-menu='nav'] { left: var(--hh-s-16); }
  .panel[data-menu='account'] { right: var(--hh-s-16); }
  .mi {
    display: flex;
    align-items: center;
    width: 100%;
    min-height: max(var(--hh-h-input), var(--hh-field-control-min, 0px));
    padding: var(--hh-s-8) var(--hh-s-12);
    background: transparent;
    border: none;
    border-radius: var(--hh-radius-control);
    font-size: var(--hh-s-16);
    color: var(--hh-text);
    text-align: left;
    text-decoration: none;
    cursor: pointer;
  }
  .mi:hover { background: var(--hh-row-alt); }
  .mi[aria-current='true'] { font-weight: 700; }
  .mi .here { margin-left: auto; color: var(--hh-text-dim); font-size: var(--hh-s-12); }
  .mi-label-dim { color: var(--hh-text-dim); font-size: var(--hh-s-12); padding: var(--hh-s-8) var(--hh-s-12) var(--hh-s-4); }

  /* The phone's bottom tab bar: fixed to the foot with the safe area beneath it. The app's page clears it with its own
     bottom padding of FIELD_TABBAR_CLEARANCE, so the last row is never hidden behind it. */
  .tabbar { display: none; }
  @media (max-width: 767.98px) {
    .tabbar {
      display: flex;
      position: fixed;
      left: 0;
      right: 0;
      bottom: 0;
      z-index: var(--hh-z-nav);
      background: var(--hh-card);
      border-top: 1px solid var(--hh-line);
      padding-bottom: var(--hh-safe-bottom);
    }
    .lockup-name { display: none; }
    .account-word { display: none; }
    .account { padding: 0 var(--hh-s-4); }
    .chip .note { display: none; }
  }
  @media (max-width: 479.98px) {
    .chip, .selector { display: none; }
  }
  .tab {
    flex: 1 1 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: var(--hh-s-2);
    min-width: var(--hh-h-input);
    min-height: var(--hh-s-64);
    padding: var(--hh-s-8) var(--hh-s-4);
    background: transparent;
    border: none;
    border-top: 3px solid transparent;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    font-weight: 600;
    text-decoration: none;
    cursor: pointer;
  }
  .tab svg { width: var(--hh-s-24); height: var(--hh-s-24); display: block; }
  .tab[aria-current='page'] { color: var(--hh-text); border-top-color: var(--hh-accent); font-weight: 700; }
  .tab[aria-expanded='true'] { color: var(--hh-text); }
  @media (prefers-reduced-motion: reduce) { * { transition: none; animation: none; } }
`;

const TAB_ICONS: Readonly<Record<string, string>> = {
  pick: 'M4 6h16v5H4zM4 13h16v5H4z',
  drive: 'M2 6h11v9H2zM13 9h5l3 3v3h-8zM6 18a1.5 1.5 0 1 0 0-.01zM17 18a1.5 1.5 0 1 0 0-.01z',
  messages: 'M4 5h16v11H9l-5 4z',
  more: 'M5 12h.01M12 12h.01M19 12h.01',
};

type MenuKind = 'nav' | 'account';

export class FieldFrame extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    org: { type: String },
    appName: { type: String },
    surface: { type: String },
    roleLabel: { type: String },
    syncState: { type: String },
    theme: { type: String },
    signedIn: { type: Boolean, attribute: 'signed-in', reflect: true },
    demo: { type: Boolean },
    feedback: { type: Boolean },
    current: { type: String },
    tabs: { attribute: false },
    bellAction: { type: String },
    bellCount: { type: Number },
    menu: { state: true },
  };

  declare org: string;
  declare appName: string;
  declare surface: string;
  declare roleLabel: string;
  declare syncState: ShellSyncState;
  declare theme: 'light' | 'dark';
  declare signedIn: boolean;
  declare demo: boolean;
  declare feedback: boolean;
  declare current: FieldTabId;
  declare tabs: FieldTab[];
  declare bellAction: string;
  declare bellCount: number;
  declare menu: MenuKind | null;

  constructor() {
    super();
    this.org = '';
    this.appName = '';
    this.surface = '';
    this.roleLabel = '';
    this.syncState = 'idle';
    this.theme = 'light';
    this.signedIn = false;
    this.demo = false;
    this.feedback = false;
    this.current = 'messages';
    this.tabs = [];
    this.bellAction = '';
    this.bellCount = 0;
    this.menu = null;
  }

  /** The control that opened the open menu, which Escape and an item's action hand the ring back to. */
  #opener: HTMLElement | null = null;

  override connectedCallback(): void {
    super.connectedCallback();
    if (typeof document !== 'undefined') document.addEventListener('pointerdown', this.#onDocPointerDown, true);
  }

  override disconnectedCallback(): void {
    if (typeof document !== 'undefined') document.removeEventListener('pointerdown', this.#onDocPointerDown, true);
    super.disconnectedCallback();
  }

  readonly #onDocPointerDown = (event: Event): void => {
    if (event.composedPath().includes(this)) return;
    this.menu = null;
  };

  protected override firstUpdated(): void {
    this.renderRoot.addEventListener('click', this.#onClick as EventListener);
    this.renderRoot.addEventListener('keydown', this.#onKey as EventListener);
  }

  protected override updated(changed: PropertyValues): void {
    super.updated(changed);
    if (changed.has('menu') && this.menu !== null) this.#items()[0]?.focus();
    // The theme rides the document root, the sheet's own contract (light sets data-theme, dark clears it), as the
    // design system's header did for the apps this frame replaces.
    if (changed.has('theme') && typeof document !== 'undefined') {
      const root = document.documentElement;
      if (this.theme === 'light') root.setAttribute('data-theme', 'light');
      else root.removeAttribute('data-theme');
    }
  }

  #items(): HTMLElement[] {
    return [...this.renderRoot.querySelectorAll<HTMLElement>('.panel .mi')];
  }

  #open(kind: MenuKind, opener: HTMLElement): void {
    if (this.menu === kind) {
      this.#close(true);
      return;
    }
    this.#opener = opener;
    this.menu = kind;
  }

  /** Closes; refocus hands the ring back to the control that opened it. */
  #close(refocus: boolean): void {
    if (this.menu === null) return;
    this.menu = null;
    if (refocus) {
      const opener = this.#opener;
      void this.updateComplete.then(() => opener?.focus());
    }
  }

  readonly #onClick = (event: MouseEvent): void => {
    const control = event.composedPath().find((node): node is HTMLElement => node instanceof HTMLElement && node.hasAttribute('data-frame'));
    const frame = control?.getAttribute('data-frame');
    if (control === undefined || frame === null) return;
    if (frame === 'nav' || frame === 'account') {
      this.#open(frame, control);
      return;
    }
    if (frame === 'more') {
      this.#open('account', control);
      return;
    }
    // An item of the open menu: the app's own listener acts on its data-action as this click bubbles on; the menu
    // closes and the ring returns to its opener.
    if (frame === 'item') this.#close(true);
  };

  readonly #onKey = (event: KeyboardEvent): void => {
    if (this.menu === null) return;
    const items = this.#items();
    const at = items.indexOf((this.renderRoot as ShadowRoot).activeElement as HTMLElement);
    if (event.key === 'Escape') {
      event.preventDefault();
      this.#close(true);
    } else if (event.key === 'Tab') {
      this.#close(false);
    } else if (event.key === 'ArrowDown') {
      event.preventDefault();
      items[(at + 1) % items.length]?.focus();
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      items[(at <= 0 ? items.length : at) - 1]?.focus();
    } else if (event.key === 'Home') {
      event.preventDefault();
      items[0]?.focus();
    } else if (event.key === 'End') {
      event.preventDefault();
      items[items.length - 1]?.focus();
    }
  };

  #icon(path: string): Template {
    return html`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="${path}"></path></svg>`;
  }

  render(): Template {
    const chip = syncChipView({ state: this.syncState, openCards: 0, waiting: 0, attempts: 0, stalls: 0 });
    const role = fieldRoleWords(this.roleLabel);
    const initials = initialsOf(role);
    const bell = this.bellCount > 0 ? `Alerts, ${this.bellCount} waiting` : 'Alerts';
    const themeWord = this.theme === 'light' ? 'Brand theme' : 'Light theme';
    return html`
      <header class="band" role="banner">
        <button class="icon-btn" type="button" data-frame="nav" aria-label="Menu" aria-haspopup="menu" aria-expanded=${this.menu === 'nav' ? 'true' : 'false'}>
          ${this.#icon('M4 6h16M4 12h16M4 18h16')}
        </button>
        <div class="lockup" data-lockup>
          ${this.org !== '' ? html`<span class="lockup-mark" aria-hidden="true">${this.org.charAt(0).toUpperCase()}</span><span class="lockup-name">${this.org}</span>` : nothing}
          ${this.demo ? html`<span class="demo" role="note">DEMO</span>` : nothing}
        </div>
        ${this.signedIn && this.appName !== '' ? html`<span class="selector" data-selector>${fieldBreadcrumb(this.appName, this.surface)}</span>` : nothing}
        <div class="cluster">
          ${this.signedIn
            ? html`<span class="chip" data-sync data-state=${this.syncState} data-kind=${chip.kind}><span class="dot" aria-hidden="true"></span><span class="word">${chip.label}</span></span>
                ${this.bellAction !== ''
                  ? html`<button class="icon-btn" type="button" data-action=${this.bellAction} aria-label=${bell}>
                      ${this.#icon('M18 8a6 6 0 0 0-12 0c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 0 1-3.4 0')}
                      ${this.bellCount > 0 ? html`<span class="badge" aria-hidden="true">${this.bellCount}</span>` : nothing}
                    </button>`
                  : nothing}`
            : nothing}
          <button class="account" type="button" data-frame="account" aria-haspopup="menu" aria-expanded=${this.menu === 'account' ? 'true' : 'false'} aria-label=${role === '' ? 'Account' : `Account, ${role}`}>
            <span class="avatar" aria-hidden="true">${initials === '' ? '' : initials}</span>
            <span class="account-word" data-testid="header-word">${role === '' ? 'Account' : role}</span>
          </button>
        </div>
        ${this.menu === 'nav'
          ? html`<div class="panel" data-menu="nav" role="menu" aria-label="Menu">
              <div class="mi-label-dim">${this.org === '' ? 'Apps' : this.org}</div>
              ${this.tabs.map((tab) => html`<a class="mi" role="menuitem" tabindex="-1" data-frame="item" href=${hhAddress(tab.href)} aria-current=${tab.id === this.current ? 'true' : 'false'}>${tab.label}${tab.id === this.current ? html`<span class="here">here</span>` : nothing}</a>`)}
            </div>`
          : nothing}
        ${this.menu === 'account' ? this.#renderAccountMenu(themeWord) : nothing}
      </header>
      ${this.signedIn && this.tabs.length > 0 ? this.#renderTabbar() : nothing}
    `;
  }

  #renderAccountMenu(themeWord: string): Template {
    return html`<div class="panel" data-menu="account" role="menu" aria-label="Account">
      ${this.signedIn
        ? html`<button class="mi" type="button" role="menuitem" tabindex="-1" data-frame="item" data-action="switch-role">Switch role</button>
            <button class="mi" type="button" role="menuitem" tabindex="-1" data-frame="item" data-action="sign-out">Sign out</button>`
        : nothing}
      <button class="mi" type="button" role="menuitem" tabindex="-1" data-frame="item" data-action="theme">${themeWord}</button>
      ${this.feedback ? html`<button class="mi" type="button" role="menuitem" tabindex="-1" data-frame="item" data-action="feedback">Feedback</button>` : nothing}
    </div>`;
  }

  #renderTabbar(): Template {
    return html`<nav class="tabbar" aria-label="Field apps">
      ${this.tabs.map((tab) =>
        tab.id === this.current
          ? html`<a class="tab" href=${hhAddress(tab.href)} aria-current="page" data-tab=${tab.id}>${this.#icon(TAB_ICONS[tab.id] ?? '')}<span>${tab.label}</span></a>`
          : html`<a class="tab" href=${hhAddress(tab.href)} data-tab=${tab.id}>${this.#icon(TAB_ICONS[tab.id] ?? '')}<span>${tab.label}</span></a>`,
      )}
      <button class="tab" type="button" data-frame="more" data-tab="more" aria-haspopup="menu" aria-expanded=${this.menu === 'account' ? 'true' : 'false'}>${this.#icon(TAB_ICONS['more'] ?? '')}<span>More</span></button>
    </nav>`;
  }
}

defineElement('hh-field-frame', FieldFrame);
