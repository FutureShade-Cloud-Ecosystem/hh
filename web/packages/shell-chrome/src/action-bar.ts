// The action bar: the sticky row that holds one primary action above the tab
// bar on a phone, and at the very bottom on a tablet. The host paints the
// action (the design system's primary button, or the host's own full-width
// button); this row offers the slot, holds the hairline above, and sticks
// to the bottom.
//
// What the Field boards draw: a slot for one primary action, sticky at the bottom
// above the tab bar on a phone and at the very bottom on a tablet, with the
// hairline above it. The slotted button wears the design system's button
// face (full width, 44 px min-height, the inherited font); the host paints
// its own action the way it paints any primary action.
//
// Tokens only (no hex literals); no opacity for state; the host's button
// carries the disabled token swap, never a fade.

import { css, html, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';

const styles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  /* The bar: sticky at the bottom of the host's scrolling region, the
   * hairline above. The host's tab bar (the phone) is fixed; this bar sits
   * inside the host's scrollable content and rises with it on a tablet
   * where there is no tab bar. The z-index keeps the bar above a long
   * list, below a sheet's scrim. */
  .bar {
    position: sticky;
    bottom: 0;
    z-index: var(--hh-z-nav);
    display: block;
    padding: var(--hh-s-12) var(--hh-s-16) calc(var(--hh-s-12) + env(safe-area-inset-bottom, 0px));
    background: var(--hh-card);
    border-top: 1px solid var(--hh-line);
  }
  /* On a phone the tab bar is fixed over the bottom 60 px (tab-bar.ts holds the same number), so the action rides
   * just above it; on a tablet there is no tab bar and the action sits at the very bottom. */
  @media (max-width: 599px) {
    .bar { bottom: calc(60px + env(safe-area-inset-bottom, 0px)); }
  }

  /* The slotted action: full width, the design system's button face. A
   * consumer that wants a different button can set their own styles in
   * light DOM and the rule below yields to a higher-specificity author
   * rule. */
  ::slotted(button) {
    width: 100%;
    min-height: 44px;
    padding: 0 var(--hh-s-20);
    border-radius: var(--hh-radius-control);
    border: 1px solid transparent;
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    font-family: inherit;
    font-size: var(--hh-s-16);
    font-weight: 600;
    cursor: pointer;
  }
  ::slotted(button:hover:not(:disabled)) {
    background: var(--brand-fill-hover);
  }
  ::slotted(button:focus-visible) {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
`;

export class ActionBar extends HHElement {
  static styles = [baseStyles, styles];

  constructor() {
    super();
    this.state = 'ready';
  }

  render(): Template {
    return html`
      <div class="bar" role="region" aria-label="Primary action">
        <slot></slot>
      </div>
    `;
  }
}

defineElement('hh-action-bar', ActionBar);