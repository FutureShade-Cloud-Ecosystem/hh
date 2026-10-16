// The Pricing surface's own sheet, beside the app's: the tab strip and the
// pieces the twelve tabs share that the app's sheet does not already carry.
// Token only: no literal colours anywhere; light and brand both come from
// the design system's tokens.
//
// The active tab is STATE, not an action: a gold filled tab competed with
// the panel's real primary in the original (its DIB-513 note), so the strip
// says "you are here" with the accent's text colour and an inset rule, and
// saves the accent fill for the panel's own primary button.
import { css } from "lit";

export const pricingStyles = css`
  /* -- the tab strip ---------------------------------------------------------- */

  .pr-tabs {
    display: flex;
    gap: var(--hh-s-4);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
    padding: 0;
  }
  .pr-tab {
    min-height: 32px;
    padding: 0 var(--hh-s-12);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    background: transparent;
    color: var(--hh-text-dim);
    font-size: 14px;
    cursor: pointer;
  }
  .pr-tab:hover {
    color: var(--hh-text);
    border-color: var(--hh-accent-text);
  }
  .pr-tab[aria-current="true"] {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
    box-shadow: inset 0 -2px 0 var(--hh-accent-text);
    font-weight: 600;
  }

  /* -- the floor warning ---------------------------------------------------------
     A confirmation, not a fault: the danger colour carries the rule and the
     wash at a light mix, the quote desk's own floor list treatment, because
     the grading is a question the editor is being asked, not a failure. */

  .pr-warn {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-12);
    border: 1px solid var(--hh-hairline);
    border-left: 3px solid var(--hh-danger);
    border-radius: var(--hh-radius-control);
    background: color-mix(in srgb, var(--hh-danger) 6%, transparent);
  }
  .pr-warn .pr-warn-title {
    font-weight: 650;
    color: var(--hh-text);
  }
  .pr-warn p {
    margin: 0;
    font-size: 14px;
    color: var(--hh-text-dim);
  }

  /* -- the bps cells ------------------------------------------------------------------ */

  td.num,
  th.num {
    text-align: right;
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    white-space: nowrap;
  }
  .num {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
  }
  .chip-danger {
    color: var(--hh-danger);
    border-color: var(--hh-danger);
  }
  .chip-good {
    color: var(--hh-success);
    border-color: var(--hh-success);
  }
  .chip-dim {
    color: var(--hh-text-dim);
  }
  .chip-warn-word {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
  }

  /* -- the editor's row of buttons and its hint ------------------------------------------ */

  .pr-actions {
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    align-items: center;
  }
  .pr-hint {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: 0;
  }
  .pr-stack {
    display: grid;
    gap: var(--hh-s-16);
    align-items: start;
  }
  .pr-stack .card {
    margin: 0;
  }

  /* -- the probe (the UoM preview) and the wide key/value readouts -------------------------- */

  .pr-readout {
    display: grid;
    gap: var(--hh-s-4);
    padding: var(--hh-s-12);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-16);
    color: var(--hh-text);
  }
  .pr-readout .warn {
    color: var(--hh-danger);
  }

  /* -- change 11: payment terms and deposits --------------------------------------- */

  .pr-board-pills {
    display: flex;
    gap: var(--hh-s-8);
    margin: 0 0 var(--hh-s-16);
    flex-wrap: wrap;
  }
  .pr-board-pill {
    min-height: 32px;
    padding: 0 var(--hh-s-12);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    background: transparent;
    color: var(--hh-text-dim);
    font-size: 14px;
    cursor: pointer;
  }
  .pr-board-pill:hover {
    color: var(--hh-text);
    border-color: var(--hh-accent-text);
  }
  .pr-board-pill-active {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
    box-shadow: inset 0 -2px 0 var(--hh-accent-text);
    font-weight: 600;
  }
  .pr-tabs-hidden {
    display: none !important;
  }
  .badge-always {
    display: inline-block;
    padding: 2px 8px;
    border-radius: var(--hh-radius-pill);
    font-size: 11px;
    font-weight: 700;
    background: color-mix(in srgb, var(--hh-accent-text) 15%, transparent);
    color: var(--hh-accent-text);
  }
  .badge-not-default {
    display: inline-block;
    padding: 2px 8px;
    border-radius: var(--hh-radius-pill);
    font-size: 11px;
    font-weight: 700;
    background: var(--hh-chip);
    color: var(--hh-text-dim);
  }
  .badge-cod {
    display: inline-block;
    padding: 2px 8px;
    border-radius: var(--hh-radius-pill);
    font-size: 11px;
    font-weight: 700;
    background: color-mix(in srgb, var(--hh-danger) 15%, transparent);
    color: var(--hh-danger);
  }
`;
