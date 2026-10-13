// The arrange view's sheet. Token-only, as the landing's sheet is: no literal
// colours anywhere; light and brand both come from the design system's tokens.
// The grid is the landing's twelve column bento with a static span class per
// snap width, and every control a person can reach is at least 36px tall.
import { css } from "lit";

export const builderStyles = css`
  /* -- the edit grid -------------------------------------------------------------------- */

  .edit-grid {
    display: grid;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    gap: var(--hh-s-16);
    margin: 0 0 var(--hh-s-24);
  }
  .edit-cell {
    min-width: 0;
    display: flex;
  }
  .c3 { grid-column: span 3; }
  .c4 { grid-column: span 4; }
  .c6 { grid-column: span 6; }
  .c8 { grid-column: span 8; }
  .c9 { grid-column: span 9; }
  .c12 { grid-column: span 12; }
  .r2 { grid-row: span 2; }
  .r3 { grid-row: span 3; }
  @media (max-width: 900px) {
    .edit-grid .edit-cell {
      grid-column: 1 / -1;
      grid-row: auto;
    }
  }

  .edit-card {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-16);
    border: 1px dashed var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    color: var(--hh-text);
  }
  .edit-card .card-title {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .edit-card .words-line {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .edit-card .controls {
    margin-top: auto;
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    align-items: center;
  }

  .span-tag {
    font-family: var(--hh-font-mono, monospace);
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-pill);
    padding: 1px var(--hh-s-8);
    white-space: nowrap;
  }
  .warn-tag {
    font-size: var(--hh-s-12);
    color: var(--hh-state-watch);
    border: 1px solid var(--hh-state-watch);
    border-radius: var(--hh-radius-pill);
    padding: 1px var(--hh-s-8);
    white-space: nowrap;
  }

  /* -- the arrange bar and the actions rows ----------------------------------------------- */

  .arrange-bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    background: var(--brand-tint);
    margin: 0 0 var(--hh-s-16);
  }
  .arrange-bar .say {
    font-size: 14px;
    color: var(--hh-text);
    max-width: 70ch;
  }
  .actions-row {
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    align-items: center;
    margin: 0 0 var(--hh-s-16);
  }
  .actions-row .spacer {
    flex: 1;
  }
  .source-chip {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .kbd {
    font-family: var(--hh-font-mono, monospace);
    font-size: var(--hh-s-12);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    padding: 0 var(--hh-s-4);
  }

  /* -- the scope tabs --------------------------------------------------------------------- */

  .scope-tabs {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-20);
  }
  .scope-tab {
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .scope-tab[aria-selected="true"] {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    font-weight: 600;
  }

  /* -- the gallery sheet ------------------------------------------------------------------- */

  .sheet-block {
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-widget-raised);
    padding: var(--hh-s-20);
    margin: 0 0 var(--hh-s-20);
  }
  .sheet-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-12);
  }
  .sheet-head h2 {
    font-size: var(--hh-s-20);
    font-weight: 650;
  }
  .gallery-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
    gap: var(--hh-s-12);
    margin: 0 0 var(--hh-s-12);
  }
  .gallery-card {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-16);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    color: var(--hh-text);
  }
  .gallery-card .title {
    font-size: var(--hh-s-16);
    font-weight: 650;
  }
  .gallery-card .blurb {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .gallery-card .words-line {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .gallery-card .add {
    margin-top: auto;
  }

  /* -- the builder form --------------------------------------------------------------------- */

  .form-grid {
    display: grid;
    grid-template-columns: 1fr minmax(240px, 380px);
    gap: var(--hh-s-20);
    align-items: start;
  }
  @media (max-width: 900px) {
    .form-grid {
      grid-template-columns: 1fr;
    }
  }
  .field-groups {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-16);
  }
  .field-group {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .field-group .label {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .choices {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
  }
  .choice-pill {
    min-height: 36px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .choice-pill[aria-pressed="true"] {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    font-weight: 600;
  }
  .preview-box {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-16);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    min-height: 120px;
  }
  .preview-box .label {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .preview-box .figure {
    font-size: var(--hh-s-40);
    font-weight: 650;
    line-height: 1.05;
  }
  .preview-box .figure.none {
    font-size: var(--hh-s-20);
    color: var(--hh-text-dim);
  }
  .preview-box .caption {
    font-size: 14px;
    color: var(--hh-text-dim);
  }

  .preview-bars {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .preview-bars .bar-row {
    display: grid;
    grid-template-columns: minmax(4em, 1fr) 2fr auto;
    align-items: center;
    column-gap: var(--hh-s-12);
  }
  .preview-bars .label {
    font-size: 14px;
  }
  .preview-bars .track {
    height: 8px;
    border-radius: 4px;
    background: var(--hh-track);
    overflow: hidden;
  }
  .preview-bars .fill {
    display: block;
    height: 100%;
    border-radius: 4px;
    background: var(--hh-success-fill);
  }
  .preview-bars .value {
    font-size: 14px;
    font-weight: 650;
    white-space: nowrap;
  }
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

  /* -- the list blocks (pins, tiles, role gallery, profiles) ---------------------------------- */

  .block {
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    box-shadow: var(--hh-widget-shadow);
    padding: var(--hh-s-20);
    margin: 0 0 var(--hh-s-20);
  }
  .block > h2 {
    font-size: var(--hh-s-16);
    font-weight: 650;
    margin: 0 0 var(--hh-s-4);
  }
  .block > .block-sub {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-12);
  }
  .rows-list {
    display: flex;
    flex-direction: column;
  }
  .edit-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    padding: var(--hh-s-12) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .edit-row:first-child {
    border-top: none;
  }
  .edit-row .name {
    flex: 1;
    min-width: 10em;
    font-size: var(--hh-s-16);
    font-weight: 600;
  }
  .edit-row .sub {
    display: block;
    font-size: 14px;
    font-weight: 400;
    color: var(--hh-text-dim);
  }
  .profile-picker {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-20);
  }

  .empty-edit {
    border: 1px dashed var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-32) var(--hh-s-20);
    text-align: center;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    align-items: center;
    margin: 0 0 var(--hh-s-24);
  }
  .empty-edit p {
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
    margin: 0;
  }

  .who-note {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: var(--hh-s-16) 0 0;
    max-width: 62ch;
  }
`;
