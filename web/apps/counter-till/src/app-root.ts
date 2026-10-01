// The Counter Till's root element: the thin UI layer the house invariants ask
// for. It renders core and package state and calls package actions; nothing
// domain-shaped lives here (no prices, no money, no order logic: the till's
// sale work builds on this shell later in the cycle).
//
// State: the entry view's flows answer (loading, unavailable, or the flow
// rows), and the signed-in role once a card is tapped. Rendering is Lit, the
// design system supplies the tokens and the primitives, and the auth package
// supplies custody through its injected AccountSurface seam: this app never
// touches the token itself, the demo adapter owns the mint.
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }
      header {
        padding: var(--hh-s-16) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
      }
      h1 {
        font-size: var(--hh-s-16);
        margin: 0;
      }
      main {
        padding: var(--hh-s-20);
      }
    `,
  ];

  render(): TemplateResult {
    return html`
      <header>
        <h1>Counter Till</h1>
      </header>
      <main>
        <p>The way in arrives with the demo sign-in.</p>
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
