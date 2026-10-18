// The bottom tab bar: the four tabs (Pick, Drive, Messages, More) the Field
// phone apps ride at the bottom of the viewport. The active tab carries a
// gold top rule and a bold label; pressing a tab fires hh-tab-select with
// the tab's key (bubbles, composed), and the host decides what each tab
// means and where it goes. The bar does NOT navigate: the press is an event
// the host catches and answers.
//
// What the Field boards draw: hidden at 600 px and wider (the tablet has no tab
// bar); fixed to the bottom with env(safe-area-inset-bottom); a `nav` with
// aria-label, each tab a button with aria-current="page" when active; 44 px
// minimum targets; the active one carries a gold top rule and a bold label.
//
// Purely presentational: the host hands the bar the tabs and the active key,
// the bar reads the press off the host's data-action delegation, and the
// event rides to the host. Tokens only (no hex literals); no opacity for
// state; the active mark is the gold rule and the bold label, never a fade.
//
// The icon name is one the design system (see
// design-system/src/state-icons.ts and the icons/README.md seam) carries.
// Until the ERP icon registry lands, the house 24 grid icons for the four
// Field tabs are hand-drawn below, on the same grid the state icons use:
// stroked, 2 px, round caps and joins, currentColor, no colour of their own.

import { css, html, nothing, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';

export const TAB_ICON_NAMES = ['pick', 'drive', 'messages', 'more'] as const;
export type TabIconName = (typeof TAB_ICON_NAMES)[number];

/** The path geometry the four Field tab icons wear, on the house 24 grid,
 * stroked, 2 px, round caps and joins. The pick tab's mark is a crate; the
 * drive tab's is a route line; the messages tab's is a balloon; the more
 * tab's is three dots. The geometry is bound through the escaped template
 * path (an interpolated attribute value), so a poisoned string could not
 * close the attribute and inject. */
const TAB_ICON_PATHS: Record<TabIconName, readonly string[]> = {
  pick: ['M4 7l8-3 8 3v10l-8 3-8-3z', 'M4 7l8 3 8-3', 'M12 10v10'],
  drive: ['M5 18a2 2 0 1 1 0-4 2 2 0 0 1 0 4z', 'M19 18a2 2 0 1 1 0-4 2 2 0 0 1 0 4z', 'M7 16h10l-2-6H9z', 'M9 10V6h6v4'],
  messages: ['M4 5h16v11H8l-4 4z'],
  more: ['M6 12h.01', 'M12 12h.01', 'M18 12h.01'],
};

function renderTabIcon(name: string): Template {
  const safe = (TAB_ICON_PATHS as Record<string, readonly string[]>)[name] ?? TAB_ICON_PATHS.more;
  return html`<svg
    class="icon"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    data-icon=${name}
  >${safe.map((d) => html`<path d="${d}"></path>`)}</svg>`;
}

const styles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  /* The bar: fixed to the bottom, the safe-area-inset-bottom keeps the bar
   * clear of the home indicator. Hidden at 600 px and wider: the tablet has
   * no tab bar. */
  .bar {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: var(--hh-z-nav);
    display: flex;
    align-items: stretch;
    background: var(--hh-card);
    color: var(--hh-text);
    border-top: 1px solid var(--hh-line);
    /* 60 px tall above the home indicator: action-bar.ts sits its phone action exactly this far up. */
    height: calc(60px + env(safe-area-inset-bottom, 0px));
    padding-bottom: env(safe-area-inset-bottom, 0px);
  }
  @media (min-width: 600px) {
    .bar { display: none; }
  }

  /* One tab: a column of an icon and a word, the r4 44 px target. The active
   * tab carries a gold top rule and a bold label; the colour rule is the
   * kind the state chip already names (--hh-accent), so the system speaks
   * with one voice across the row. */
  .tab {
    flex: 1 1 0;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    min-height: 44px;
    padding: var(--hh-s-8) 0 var(--hh-s-4);
    background: transparent;
    color: var(--hh-text-dim);
    border: none;
    border-top: 3px solid transparent;
    cursor: pointer;
    font-family: inherit;
    font-size: 11px;
    font-weight: 500;
    line-height: 1;
    text-align: center;
    position: relative;
  }
  .tab:hover:not([aria-current='page']) {
    color: var(--hh-text);
  }
  .tab:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }
  .tab .icon {
    width: 22px;
    height: 22px;
    flex: none;
  }
  .tab .label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 100%;
    padding: 0 var(--hh-s-4);
  }
  .tab[aria-current='page'] {
    color: var(--hh-text);
    border-top-color: var(--hh-accent);
    font-weight: 700;
  }

  /* The badge: a small pill on the icon's top-right, only when count > 0. */
  .badge {
    position: absolute;
    top: 2px;
    right: 50%;
    transform: translateX(calc(50% + 10px));
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
`;

export interface TabSpec {
  /** The key the host reads off the click event; the only id the host owns. */
  key: string;
  /** The tab's word, the visible label. */
  label: string;
  /** The icon name from the design system's icon set; the bar draws the path. */
  icon: string;
  /** Optional badge count; zero or absent hides the badge. */
  count?: number;
}

export interface TabBarSpec {
  tabs: readonly TabSpec[];
  active: string;
}

/** The template form: the demo and tests render the same markup the element
 * paints, so a host can compose one in a static page (the demo's case) or
 * mount the element in code (the shells' case). Empty tabs render nothing
 * a person can see or hear. */
export function renderTabBar(spec: TabBarSpec): Template {
  if (spec.tabs.length === 0) {
    return html``;
  }
  return html`
    <nav class="bar" aria-label="Tabs">
      ${spec.tabs.map((tab) => renderTabButton(tab, tab.key === spec.active))}
    </nav>
  `;
}

function renderTabButton(tab: TabSpec, isActive: boolean): Template {
  return html`
    <button
      class="tab"
      type="button"
      data-action="tab-select"
      data-key=${tab.key}
      aria-current=${isActive ? 'page' : 'false'}
      ?disabled=${tab.key === ''}
    >
      ${renderTabIcon(tab.icon)}
      ${typeof tab.count === 'number' && tab.count > 0
        ? html`<span class="badge" aria-hidden="true">${tab.count}</span>`
        : nothing}
      <span class="label">${tab.label}</span>
    </button>
  `;
}

export class TabBar extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    tabs: { attribute: false },
    active: { attribute: 'active' },
  };

  /** The tabs the bar paints; the host hands them through a property, and the
   * demo's static page reads the same shape. */
  declare tabs: readonly TabSpec[];
  /** The active tab's key; '' or unknown presses none. */
  declare active: string;

  constructor() {
    super();
    this.tabs = [];
    this.active = '';
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

  /** The host reads the tab's data-key off the closest [data-key] ancestor of
   * the click target (or off the target itself); the click rides a
   * data-action="tab-select" name, the design system's delegation pattern. */
  readonly #onClick = (event: Event): void => {
    const path = event.composedPath();
    const target = path.find(
      (node): node is Element => node instanceof Element && node.hasAttribute('data-action'),
    );
    if (target === undefined) return;
    if (target.getAttribute('data-action') !== 'tab-select') return;
    const button = target.hasAttribute('data-key')
      ? target
      : target.closest?.<HTMLElement>('[data-key]');
    const key = button?.getAttribute('data-key') ?? '';
    this.dispatchEvent(
      new CustomEvent('hh-tab-select', { detail: { key }, bubbles: true, composed: true }),
    );
  };

  render(): Template {
    if (this.tabs.length === 0) {
      // Empty tabs render nothing a person can see or hear.
      return html``;
    }
    return renderTabBar({ tabs: this.tabs, active: this.active });
  }
}

defineElement('hh-tab-bar', TabBar);