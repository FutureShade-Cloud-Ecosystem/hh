// The Warranty surface's own sheet, beside the app's: the segmented control
// the vendor rows carry, and the row's grid. Token only: no literal colours
// anywhere; light and brand both come from the design system's tokens.
//
// THE SELECTED SEGMENT IS THE THEME INVERTED (both boards): a dark fill with
// light ink in the light half, a light fill with dark ink in the charcoal
// half. The pair is the app's own text-on-card pair flipped, so the measured
// ratio carries: fill var(--hh-text), ink var(--hh-card).
import { css } from "lit";

export const warrantyStyles = css`
  /* -- the vendor rows --------------------------------------------------------- */

  .wa-rows {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
  }
  .wa-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-16);
    flex-wrap: wrap;
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-field);
  }
  .wa-vendor {
    font-weight: 650;
    min-width: 160px;
    flex: none;
  }
  .wa-controls {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    flex: 1 1 auto;
  }
  .wa-row-error {
    flex-basis: 100%;
    margin: 0;
  }

  /* -- the segmented control -----------------------------------------------------
     One control, three kinds, the chosen one the theme inverted: the boards'
     own treatment, the text-on-card pair flipped so the measured ratio
     carries in both halves. */

  .seg {
    display: inline-flex;
    align-items: center;
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-field);
    overflow: hidden;
  }
  .seg button {
    min-height: 32px;
    padding: 0 var(--hh-s-16);
    border: 0;
    background: transparent;
    color: var(--hh-text);
    font-size: 14px;
    font-family: inherit;
    cursor: pointer;
    white-space: nowrap;
  }
  .seg button + button {
    border-left: 1px solid var(--hh-control-line);
  }
  .seg button:hover:not([aria-pressed="true"]) {
    color: var(--hh-accent-text);
  }
  .seg button[aria-pressed="true"],
  .seg button[aria-pressed="true"]:hover {
    background: var(--hh-text);
    color: var(--hh-card);
    font-weight: 600;
  }
  .seg button:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
    cursor: default;
  }
  .seg button[aria-pressed="true"]:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
  }

  /* -- the threshold's field row ------------------------------------------------- */

  .wa-threshold {
    display: flex;
    align-items: center;
    gap: var(--hh-s-16);
    flex-wrap: wrap;
  }
  .wa-threshold .text-field {
    max-width: 280px;
  }
`;
