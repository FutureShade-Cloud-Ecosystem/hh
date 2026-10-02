// Ported from hardscapeos_dibbits master 87d7941b app/src/styles/focus-ring.ts; adapted for HH.

import { css } from 'lit';

/**
 * focusRing: DIB-414. The one shared :focus-visible treatment, for every shadow root in the app.
 *
 * THE PROBLEM IT SOLVES
 * ---------------------
 * tokens.css declares `:focus-visible { outline: 2px solid var(--hos-stone-amber-text) }` at the
 * document level. That rule reaches the light DOM and every custom-element HOST, and stops dead at
 * the shadow boundary: a document stylesheet does not style a shadow root's contents. So the ~70
 * components that render their own buttons, inputs and selects inside a shadow root got the
 * USER-AGENT ring instead: a thin platform default that is nearly invisible on --hos-deep-earth in
 * dark theme, and that changes shape between Chromium and WebKit. Seven surfaces had already
 * repeated the rule by hand (app-shell, pos-terminal, pos-product-grid, quote-builder, quote-edit,
 * quote-public, tab-strip); the rest had nothing, so a keyboard user tabbing from the shell into a
 * pane watched the ring disappear.
 *
 * WHY A SHARED CSSResult AND NOT A NEW TOKEN
 * ------------------------------------------
 * A `--hos-focus-ring` token would only carry the COLOUR; each component would still have to write
 * the selector list, and a component that forgot one selector (the `[tabindex]` rows, the `summary`
 * elements) would still have a hole. What actually needed sharing is the SELECTOR SET plus the
 * declaration. This is that, as one importable CSSResult: and it consumes a token the design system
 * already declares, so the _theme-tokens phantom gate stays at zero tolerance.
 *
 * D-078 / DIB-456: that token is now --hos-stone-amber-TEXT rather than --hos-stone-amber. In dark
 * theme the two hold the same value and the ring is unchanged. In LIGHT theme the brand gold FILL
 * holds while gold-as-INK becomes dark-gold, and the ring follows the ink: a gold ring on the light
 * page measures 1.50:1 and fails WCAG 1.4.11's 3:1 non-text bar outright (the old amber ring failed
 * it too, at 1.80:1), where the dark-gold measures 4.96:1 on the page and 5.33:1 on a card. This is
 * the brand's own answer to gold on a light background: the GTM site never sets gold as ink on a
 * light section: not a colour reached for from outside the palette. The values live in tokens.css;
 * no literal may appear in THIS file, and the gate below the ring in _theme-tokens.test.mjs enforces
 * that, because this is the one declaration ~70 shadow roots inherit.
 *
 * The values are byte-identical to tokens.css and to the seven hand-written copies, deliberately:
 * a keyboard user must not be able to tell which surface they are on from the shape of the ring.
 *
 * outline (not box-shadow, not border) is the right primitive here: it does not affect layout, so
 * a focused control never reflows the row it sits in, and it survives `overflow: hidden` ancestors
 * that would clip a box-shadow.
 */
export const focusRing = css`
  /* The host itself, for components used as a focusable unit (e.g. with delegatesFocus). A bare
     :focus-visible below does NOT match the host: shadow styles reach descendants only. */
  :host(:focus-visible) {
    outline: 2px solid var(--hos-stone-amber-text);
    outline-offset: 2px;
  }

  /*
 * UNIVERSAL, exactly like the document-level rule in tokens.css: and for a reason learned the
   * hard way. This started as an enumerated selector list (a, button, input, select, textarea,
   * summary, [tabindex], [role=button]) which looked exhaustive and was not: Chromium makes a
   * SCROLL CONTAINER with no focusable children keyboard-focusable on its own, with no tabindex
   * attribute to match on. The quote builder's UL of quotes is one, and the keyboard walk caught it
   * sitting on the 1px user-agent outline.
   *
   * Any enumerated list is a coverage question that has to be re-audited every time the platform
   * adds a focusable. The :focus-visible pseudo-class already encodes "the browser thinks a keyboard
 * user needs to see this", so matching all of it is both simpler and complete: and the browser's
   * own heuristics keep it off programmatically-focused containers after a pointer interaction.
   */
  :focus-visible {
    outline: 2px solid var(--hos-stone-amber-text);
    outline-offset: 2px;
    /* A control inside a scrolling pane must not have its ring clipped by the pane's edge; 2px of
       offset needs 2px of room when the browser scrolls it into view. */
    scroll-margin: 4px;
  }
`;
