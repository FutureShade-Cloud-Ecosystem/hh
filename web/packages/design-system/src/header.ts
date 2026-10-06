// The shared app header: the chrome every micro-app wears, extracted from the
// front door's inline bar (HH-RD2 polish round 2 item 5, decision log row
// 938; the shape is the one recorded in hh-demo-1/STATUS.md, "Front door
// header component - location and API"). One bar, three zones: the brand
// lockup and the demo badge left, the word of the moment centred (truly
// centred: the side columns are equal fr tracks, so an empty right side
// cannot pull the word off centre), the signed-in controls right. Under the
// small breakpoint the zones stack and centre.
//
// The element carries no app handlers of its own: the buttons ride
// data-action names (switch-role, sign-out) and the app listens for clicks on
// the host and acts, the till's delegation pattern. Each app passes only
// what it has (a role label, a signed-in flag, a demo flag, a theme) and the
// element defaults the rest, so no app changes shape because another one
// did. The demo banner is off by default: a build without the demo banner is
// the production build's default.
//
// The theme slot (the item 5 followup): a host's own theme controls (the
// till's Auto/Light/Dark cycle) ride the NAMED slot "theme" inside the right
// cluster, after Switch role and Sign out, behind a thin separator. The
// element does not define the controls; it accepts them, so every app keeps
// its own switcher and none changes shape. The slot renders only when light
// DOM children claim it by name: an unused slot leaves no empty placeholder
// and no separator, the cluster staying exactly the two buttons. The claim
// is read off the host's own children at connect and watched from connect to
// disconnect (a host may slot its controls after the element mounts, the
// template-binding discipline every adopting app rides); the server render
// has no light DOM, so it carries the bare cluster and nothing else.
//
// Theming rides the document, not the template: the theme property applies
// the sheet's own contract to the document root (data-theme="light" opts
// into the light half; dark is the sheet's default, the attribute's
// absence), the same attribute the design system's sheet states. Every
// colour, gap and radius is a token except the lockup's own geometry (the
// 30px mark, the 56px bar), which has no token and keeps the front door's
// literal shape.

import { css, html, nothing, type PropertyValues, type TemplateResult as Template } from 'lit';
import { defineElement, HHElement } from './base.js';
import { baseStyles } from './primitives.js';

/** The word the centre zone carries when nobody is signed in. */
const DEFAULT_WORD = 'Pick a role';

/** The brand lockup's accessible name: the whole lockup is one image. */
const BRAND_LABEL = 'HH Yard, a Hardscape House product';

const styles = css`
  header {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    align-items: center;
    gap: var(--hh-s-12);
    box-sizing: border-box;
    min-height: 56px;
    padding: var(--hh-s-8) var(--hh-s-20);
    border-bottom: 1px solid var(--hh-line);
  }
  .demo-banner-cell {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-12);
  }
  /* The brand lockup, the front door's own: the Hardscape House badge at
     the left, the product name with the house line under it. The badge is
     the yellow square ground (the badge yellow IS --hh-accent), square
     cornered as the source colourways are, carrying mark.svg's measured H
     inlined in the ink a gold fill carries (--hh-accent-ink, through
     currentColor), in both themes (decision log rows 953 and 968). */
  .brand {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .brand-mark {
    width: 30px;
    height: 30px;
    flex: none;
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
  }
  .brand-mark svg {
    width: 100%;
    height: 100%;
    display: block;
  }
  .brand-names {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
  }
  .brand-product {
    font-size: var(--hh-s-16);
    font-weight: 700;
    letter-spacing: -0.02em;
    line-height: 1.05;
  }
  .brand-product .accent {
    color: var(--hh-accent-text);
  }
  .brand-sub {
    font-size: 10px;
    font-weight: 600;
    letter-spacing: 0.055em;
    line-height: 1.2;
    color: var(--hh-text-dim);
    white-space: nowrap;
  }
  .who {
    margin: 0;
    justify-self: center;
    text-align: center;
    font-weight: 600;
    font-size: 15px;
  }
  .controls {
    justify-self: end;
    display: flex;
    align-items: center;
    gap: var(--hh-s-8);
  }
  /* The thin divider between the cluster's own buttons and a host's slotted
     theme controls: the sheet's hairline role, stretched to the cluster's
     height, never a tokenless grey. */
  .controls .theme-sep {
    width: 1px;
    align-self: stretch;
    background: var(--hh-line);
  }
  /* The host's controls keep their own face (the element accepts them, it
     does not define them); the slot lays them out in the cluster's own row
     and rhythm. */
  .controls slot[name='theme'] {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
  }
  @media (max-width: 480px) {
    header {
      grid-template-columns: 1fr;
      justify-items: center;
    }
    .demo-banner-cell {
      justify-self: center;
    }
    /* The band needs the room more than the house line does; the mark and
       the product name stay, the part that identifies the app. */
    .brand-sub {
      display: none;
    }
    .who {
      justify-self: center;
    }
    .controls {
      justify-self: center;
    }
  }
  .controls button {
    min-height: 0;
    padding: 7px 12px;
    font-size: 11px;
    line-height: normal;
    color: var(--hh-text-dim);
  }
  .controls button:hover:not(:disabled) {
    border-color: var(--hh-accent-text);
    color: var(--hh-accent-text);
  }
  /* The demo badge: the till's gold chip over the same token names the
     till's demo-banner carries (the gold is a fill and carries the accent
     ink). No icon: the word is the badge. */
  .demo-banner {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hh-s-8);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-4) var(--hh-s-12);
  }
  .demo-banner .word {
    font-size: var(--hh-s-12);
    line-height: 1.3;
    font-weight: 700;
  }
  .demo-banner .copy {
    font-size: var(--hh-s-12);
    line-height: 1.3;
  }
`;

export class HosAppHeader extends HHElement {
  static styles = [baseStyles, styles];

  // The base's reactive state property stays declared (the spread keeps its
  // converter); the header's own inputs ride beside it. Defaults are assigned
  // through the accessors in the constructor, never as class fields, for the
  // reason base.ts states: a field would shadow the accessor.
  static properties = {
    ...HHElement.properties,
    roleLabel: { attribute: 'role-label' },
    signedIn: { type: Boolean, attribute: 'signed-in' },
    demo: { type: Boolean },
    theme: {},
  };

  /** The centred role name; the default word when empty or unsigned. */
  declare roleLabel: string;
  /** Whether the right cluster (Switch role, Sign out) renders. */
  declare signedIn: boolean;
  /** Whether the demo badge renders; off is the production default. */
  declare demo: boolean;
  /** 'light' or 'dark'; applied to the document root, '' leaves it alone. */
  declare theme: string;

  constructor() {
    super();
    this.roleLabel = '';
    this.signedIn = false;
    this.demo = false;
    this.theme = '';
  }

  /** Whether light DOM children claim the theme slot by name. */
  #themeSlotted = false;
  /** Watches the host's children for a theme-slot claim, from connect to disconnect. */
  #themeObserver: MutationObserver | null = null;

  /** Reads the theme-slot claim off the host's own children and re-renders on a change. */
  #syncThemeSlot(): void {
    const claimed = Array.from(this.children).some((el) => el.slot === 'theme');
    if (claimed !== this.#themeSlotted) {
      this.#themeSlotted = claimed;
      this.requestUpdate();
    }
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.#syncThemeSlot();
    if (typeof MutationObserver === 'function') {
      this.#themeObserver = new MutationObserver(() => this.#syncThemeSlot());
      this.#themeObserver.observe(this, { childList: true, subtree: true, attributeFilter: ['slot'] });
    }
  }

  override disconnectedCallback(): void {
    this.#themeObserver?.disconnect();
    this.#themeObserver = null;
    super.disconnectedCallback();
  }

  /** The theme input is a document concern: the sheet's own attribute
   * contract, applied when the property changes and only in a document
   * (the server render never runs the update loop). */
  protected override willUpdate(changed: PropertyValues): void {
    if (!changed.has('theme')) return;
    if (typeof document === 'undefined') return;
    const root = document.documentElement;
    if (this.theme === 'light') root.setAttribute('data-theme', 'light');
    else if (this.theme === 'dark') root.removeAttribute('data-theme');
  }

  render(): Template {
    const signedIn = this.signedIn === true;
    const word = signedIn && this.roleLabel !== '' ? this.roleLabel : DEFAULT_WORD;
    // The separator sits BETWEEN the buttons and the slotted controls: with
    // no buttons (unsigned) there is nothing to separate, with no slotted
    // content there is nothing to divide; it renders only when both halves
    // are present.
    const themeSep =
      signedIn && this.#themeSlotted
        ? html`<span class="theme-sep" aria-hidden="true"></span>`
        : nothing;
    const themeSlot = this.#themeSlotted ? html`<slot name="theme"></slot>` : nothing;
    return html`
      <header>
        <div class="demo-banner-cell">
          <div class="brand" role="img" aria-label=${BRAND_LABEL}>
            <span class="brand-mark" aria-hidden="true">
              <svg viewBox="0 0 3000 3000">
                <path
                  fill="currentColor"
                  d="M644.94 465.17 H1386.06 A29.15 29.15 0 0 1 1415.21 494.32 V1306.38 A84.79 84.79 0 0 0 1500 1391.17 H2355.05 A29.15 29.15 0 0 1 2384.2 1420.32 V2505.68 A29.15 29.15 0 0 1 2355.05 2534.83 H1613.94 A29.15 29.15 0 0 1 1584.79 2505.68 V1940.1 A84.79 84.79 0 0 0 1415.21 1940.1 V2505.68 A29.15 29.15 0 0 1 1386.06 2534.83 H644.94 A29.15 29.15 0 0 1 615.79 2505.68 V494.32 A29.15 29.15 0 0 1 644.94 465.17 Z M1613.94 465.17 H2355.05 A29.15 29.15 0 0 1 2384.2 494.32 V1192.85 A29.15 29.15 0 0 1 2355.05 1222 H1613.94 A29.15 29.15 0 0 1 1584.79 1192.85 V494.32 A29.15 29.15 0 0 1 1613.94 465.17 Z"
                />
              </svg>
            </span>
            <span class="brand-names">
              <span class="brand-product">HH <span class="accent">Yard</span></span>
              <span class="brand-sub">a Hardscape House product</span>
            </span>
          </div>
          ${this.demo
            ? html`
                <div class="demo-banner" role="note">
                  <span class="word">Demo</span>
                  <span class="copy">Everything on this box is invented demo data: nothing here is real.</span>
                </div>
              `
            : nothing}
        </div>
        <p class="who" data-testid="header-word">${word}</p>
        <div class="controls">
          ${signedIn
            ? html`
                <button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>
                <button type="button" class="btn-secondary" data-action="sign-out">Sign out</button>
              `
            : nothing}
          ${themeSep}
          ${themeSlot}
        </div>
      </header>
    `;
  }
}

defineElement('hos-app-header', HosAppHeader);
