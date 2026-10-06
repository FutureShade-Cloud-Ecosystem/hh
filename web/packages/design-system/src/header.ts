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
  /* The brand lockup, the ERP shell's own: the HH mark at the left, the
     product name with the house line under it. Gold is a fill, never an ink,
     so the mark's "HH" is the near-black ink the gold fill carries; the
     accent word takes the gold-as-ink token, which is dark gold in the light
     theme. */
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
    display: grid;
    place-items: center;
    border-radius: var(--hh-radius-control);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: var(--hh-s-12);
    font-weight: 800;
    letter-spacing: -0.02em;
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
    return html`
      <header>
        <div class="demo-banner-cell">
          <div class="brand" role="img" aria-label=${BRAND_LABEL}>
            <span class="brand-mark" aria-hidden="true">HH</span>
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
        </div>
      </header>
    `;
  }
}

defineElement('hos-app-header', HosAppHeader);
