// Manager Home's sheet. Token-only: no literal colours anywhere; light and
// brand (the dark half) both come from the design system's tokens, and the
// header's theme control flips the document root.
//
// THE GRID. The bento is the twelve-column v3.5 grid at the desktop
// breakpoint and six columns below it, on a 132px row unit, the spans the
// server sends (span_cols, span_rows). The arithmetic that decides whether a
// bento fits is the server's; this sheet only places what it is told.
import { css } from "lit";

export const homeStyles = css`
  :host {
    display: block;
    min-height: 100vh;
    background: var(--hh-page);
  }

  main {
    max-width: 1280px;
    margin: 0 auto;
    padding: var(--hh-s-8) var(--hh-s-4) var(--hh-s-16);
  }

  .view-head {
    display: flex;
    align-items: baseline;
    gap: var(--hh-s-4);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-4);
  }
  .view-head h1 {
    font-size: var(--hh-s-24);
    font-weight: 600;
    margin: 0;
  }
  .view-head .where {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
  }

  .error-line,
  .notice-line {
    margin: 0 0 var(--hh-s-4);
    font-size: var(--hh-s-16);
  }
  .error-line {
    color: var(--hh-danger);
  }
  .notice-line {
    color: var(--hh-text-dim);
  }

  /* -- the bento grid ------------------------------------------------------------------ */

  .bento {
    display: grid;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    grid-auto-rows: 132px;
    gap: var(--hh-s-4);
  }
  @media (max-width: 900px) {
    .bento {
      grid-template-columns: repeat(6, minmax(0, 1fr));
    }
  }
  @media (max-width: 560px) {
    .bento {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      grid-auto-rows: auto;
    }
    .card {
      grid-column: span 2 !important;
      grid-row: auto !important;
      min-height: 96px;
    }
  }

  /* The span classes, one per footprint in the closed vocabulary (the snap set the server
     sends). Named classes, never a computed style: the position gate refuses a bound style
     attribute, and the span arithmetic stays the server's. */
  .s3 { grid-column: span 3; }
  .s4 { grid-column: span 4; }
  .s6 { grid-column: span 6; }
  .s8 { grid-column: span 8; }
  .s9 { grid-column: span 9; }
  .s12 { grid-column: span 12; }
  .r1 { grid-row: span 1; }
  .r2 { grid-row: span 2; }
  .r3 { grid-row: span 3; }
  .s3 > .card, .s4 > .card, .s6 > .card, .s8 > .card, .s9 > .card, .s12 > .card,
  .r1 > .card, .r2 > .card, .r3 > .card {
    height: 100%;
  }

  .card {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
    padding: var(--hh-s-4);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    overflow: hidden;
  }

  .card-title {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    margin: 0;
  }
  .card-sub {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: 0;
  }

  .figure {
    font-size: var(--hh-s-40);
    font-weight: 650;
    line-height: 1.05;
    margin: 0;
  }
  .figure.small {
    font-size: var(--hh-s-28);
  }
  .figure.none {
    font-size: var(--hh-s-24);
    color: var(--hh-text-dim);
  }

  /* The hero: the profile's first stat, the day going out. Accent fill, the
     figure large, the window word under it. */
  .card.hero {
    background: var(--brand-fill);
    border-color: var(--brand-fill);
    color: var(--brand-fill-on);
    justify-content: center;
  }
  .card.hero .card-title,
  .card.hero .card-sub {
    color: var(--brand-fill-on);
  }

  /* -- the needs-you queue -------------------------------------------------------------- */

  .needs-total {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: 0;
  }
  .needs-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-4);
    padding: var(--hh-s-2) 0;
    border-top: 1px solid var(--hh-line);
  }
  .needs-row:first-of-type {
    border-top: none;
  }
  .sev {
    width: 10px;
    height: 10px;
    min-width: 10px;
    border-radius: 50%;
    background: var(--hh-text-dim);
  }
  .sev.good {
    background: var(--hh-success);
  }
  .sev.watch {
    background: var(--hh-state-watch);
  }
  .sev.stop {
    background: var(--hh-danger);
  }
  .needs-what {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
  }
  .needs-what .what {
    font-size: var(--hh-s-16);
    margin: 0;
  }
  .needs-what .view-words {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: 0;
  }
  .needs-count {
    font-size: var(--hh-s-24);
    font-weight: 650;
    margin: 0;
  }
  .needs-age {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    min-width: 3.5em;
    text-align: right;
  }
  .needs-empty {
    margin: 0;
    color: var(--hh-text-dim);
  }
  .unsourced {
    padding: var(--hh-s-2) 0;
    border-top: 1px dashed var(--hh-line);
  }
  .unsourced .what {
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-2);
  }
  .unsourced .words {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: 0;
  }

  /* -- boards and bars ------------------------------------------------------------------ */

  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-2);
    margin: 0;
    padding: 0;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-2);
    font-size: var(--hh-s-12);
    padding: var(--hh-s-2) var(--hh-s-4);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-line);
    background: var(--hh-widget-raised);
  }
  .chip .n {
    font-weight: 650;
  }
  .chip.good {
    border-color: var(--hh-success);
  }
  .chip.watch {
    border-color: var(--hh-state-watch);
  }
  .chip.stop {
    border-color: var(--hh-danger);
  }

  .bars {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .bar-row {
    display: grid;
    grid-template-columns: minmax(6em, 1fr) 3fr auto;
    align-items: center;
    gap: var(--hh-s-4);
  }
  .bar-row .label {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .track {
    height: 10px;
    border-radius: 5px;
    background: var(--hh-track);
    overflow: hidden;
  }
  .fill {
    height: 100%;
    border-radius: 5px;
    background: var(--brand-fill);
  }
  /* The fill widths, quantized in steps of five percent: static classes the template can name,
     never a bound style attribute (the position gate refuses one). */
  .w0 { width: 0; }
  .w5 { width: 5%; }
  .w10 { width: 10%; }
  .w15 { width: 15%; }
  .w20 { width: 20%; }
  .w25 { width: 25%; }
  .w30 { width: 30%; }
  .w35 { width: 35%; }
  .w40 { width: 40%; }
  .w45 { width: 45%; }
  .w50 { width: 50%; }
  .w55 { width: 55%; }
  .w60 { width: 60%; }
  .w65 { width: 65%; }
  .w70 { width: 70%; }
  .w75 { width: 75%; }
  .w80 { width: 80%; }
  .w85 { width: 85%; }
  .w90 { width: 90%; }
  .w95 { width: 95%; }
  .w100 { width: 100%; }
  .bar-row .value {
    font-size: var(--hh-s-12);
    font-weight: 600;
    white-space: nowrap;
  }
  .bar-row .share {
    grid-column: 2 / 4;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }

  /* -- the views' lists ----------------------------------------------------------------- */

  .sheet {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }
  .task-card,
  .list-card {
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    padding: var(--hh-s-4);
  }
  .task-head {
    display: flex;
    gap: var(--hh-s-4);
    align-items: baseline;
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-2);
  }
  .task-head .order {
    font-size: var(--hh-s-16);
    font-weight: 650;
    margin: 0;
  }
  .task-head .meta {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .shortage {
    display: flex;
    gap: var(--hh-s-4);
    align-items: flex-start;
    padding: var(--hh-s-4) 0;
    border-top: 1px solid var(--hh-line);
  }
  .shortage .body {
    flex: 1;
    min-width: 0;
  }
  .shortage .line {
    font-size: var(--hh-s-16);
    font-weight: 600;
    margin: 0;
  }
  .shortage .detail {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: var(--hh-s-2) 0 0;
  }
  .resolved-word {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: var(--hh-s-2) 0 0;
  }

  .def-note {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-4);
    max-width: 62ch;
  }
  .who-may {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: var(--hh-s-4) 0 0;
    max-width: 62ch;
  }

  /* -- the resolve sheet ----------------------------------------------------------------- */

  .choices {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
    margin: 0 0 var(--hh-s-4);
    padding: 0;
  }
  .choice {
    display: flex;
    align-items: center;
    gap: var(--hh-s-4);
    text-align: left;
    min-height: 48px;
    padding: 0 var(--hh-s-4);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-line);
    background: var(--hh-card);
    font-size: var(--hh-s-16);
    cursor: pointer;
  }
  .choice .mark {
    width: 16px;
    height: 16px;
    min-width: 16px;
    border-radius: 50%;
    border: 2px solid var(--hh-line);
    background: transparent;
  }
  .choice[aria-pressed="true"] {
    border-color: var(--brand-fill);
  }
  .choice[aria-pressed="true"] .mark {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    box-shadow: inset 0 0 0 3px var(--hh-card);
  }
  .choice .words {
    display: flex;
    flex-direction: column;
  }
  .choice .words .sub {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }

  .text-field {
    font-family: inherit;
    font-size: var(--hh-s-16);
    padding: var(--hh-s-2) var(--hh-s-4);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line, var(--hh-line));
    background: var(--hh-field);
    color: inherit;
    min-height: 44px;
  }
  .text-area {
    width: 100%;
    min-height: 88px;
    resize: vertical;
  }

  .row-actions {
    display: flex;
    gap: var(--hh-s-4);
    flex-wrap: wrap;
    margin: var(--hh-s-4) 0 0;
  }

  /* -- approvals --------------------------------------------------------------------------- */

  .kind-tabs {
    display: flex;
    gap: var(--hh-s-2);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-4);
    padding: 0;
  }
  .kind-tab {
    min-height: 40px;
    padding: 0 var(--hh-s-4);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-line);
    background: var(--hh-card);
    font-size: var(--hh-s-16);
    cursor: pointer;
  }
  .kind-tab[aria-pressed="true"] {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    color: var(--brand-fill-on);
  }
  .approval {
    padding: var(--hh-s-4) 0;
    border-top: 1px solid var(--hh-line);
  }
  .approval .detail {
    font-size: var(--hh-s-16);
    margin: 0;
  }
  .approval .meta {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: var(--hh-s-2) 0 0;
  }
  .approval .amount {
    font-weight: 650;
  }

  .count-line {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin: var(--hh-s-4) 0 0;
  }

  .btn-row-top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--hh-s-4);
    margin: 0 0 var(--hh-s-4);
  }
`;
