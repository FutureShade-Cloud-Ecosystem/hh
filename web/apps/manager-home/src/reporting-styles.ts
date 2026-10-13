// Manager Home's reporting sheet, beside the app's own: the catalogue nav and
// the generated parameter form, the result grids with sticky heads and totals
// feet, the saved-views strip, and the commission register's tables. Token
// only, like the app's sheet: no literal colours anywhere, both themes come
// from the design system's tokens.
import { css } from "lit";

export const reportingStyles = css`
  /* -- the templates pane's two columns -------------------------------------------------- */

  .report-layout {
    display: grid;
    grid-template-columns: 240px minmax(0, 1fr);
    gap: var(--hh-s-16);
    align-items: start;
  }
  @media (max-width: 900px) {
    .report-layout {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  .report-nav {
    padding: var(--hh-s-8) 0;
  }
  .nav-cat {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    padding: var(--hh-s-12) var(--hh-s-16) var(--hh-s-4);
  }
  .nav-item {
    display: block;
    width: 100%;
    text-align: start;
    background: none;
    border: none;
    color: var(--hh-text);
    font-size: 14px;
    padding: var(--hh-s-8) var(--hh-s-16);
    cursor: pointer;
  }
  .nav-item:hover {
    background: var(--hh-row-alt);
  }
  .nav-item[aria-current="true"] {
    color: var(--hh-accent-text);
    font-weight: 600;
  }

  .report-main {
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    box-shadow: var(--hh-widget-shadow);
    padding: var(--hh-s-20);
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-16);
  }
  .report-head h2 {
    font-size: var(--hh-s-20);
    font-weight: 650;
  }
  .report-head .page-sub {
    margin-top: var(--hh-s-4);
  }
  .legacy-name {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    margin-top: var(--hh-s-4);
  }

  /* -- the generated parameter form ------------------------------------------------------- */

  .report-form {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-12);
    align-items: end;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }
  .field-label {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .field .text-field {
    min-width: 160px;
  }
  .share-field {
    flex-direction: row;
    align-items: center;
    gap: var(--hh-s-8);
  }
  .share-field input[type="checkbox"] {
    width: 18px;
    height: 18px;
    accent-color: var(--hh-accent-text);
  }

  /* -- saved views ------------------------------------------------------------------------- */

  .saved-views {
    border-top: 1px solid var(--hh-hairline);
    padding-top: var(--hh-s-12);
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .saved-views .nav-cat {
    padding: 0;
  }
  .saved-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .saved-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
  }
  .saved-name {
    font-size: 14px;
  }
  .saved-save {
    display: flex;
    align-items: end;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }

  /* -- the grids ------------------------------------------------------------------------------ */

  .grid-wrap {
    overflow-x: auto;
  }
  .grid-wrap table {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
  }
  .grid-wrap th,
  .grid-wrap td {
    text-align: left;
    padding: var(--hh-s-8) var(--hh-s-12);
    border-bottom: 1px solid var(--hh-hairline);
    white-space: nowrap;
  }
  .grid-wrap th {
    color: var(--hh-text-dim);
    font-weight: 600;
    font-size: var(--hh-s-12);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .grid-wrap .num {
    text-align: right;
    font-family: var(--hh-font-mono);
  }
  .grid-wrap tfoot td {
    border-top: 2px solid var(--hh-text-dim);
    border-bottom: none;
    font-weight: 650;
  }

  /* -- the runs pane ---------------------------------------------------------------------------- */

  .run-row {
    padding: var(--hh-s-12) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .run-row:first-child {
    border-top: none;
    padding-top: 0;
  }
  .run-expand {
    display: flex;
    align-items: baseline;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    width: 100%;
    text-align: start;
    background: none;
    border: none;
    padding: 0;
    color: var(--hh-text);
    font-size: var(--hh-s-16);
    font-weight: 600;
    cursor: pointer;
  }
  .run-expand:hover {
    color: var(--hh-accent-text);
  }
  .run-expand .meta {
    font-size: 14px;
    font-weight: 400;
    color: var(--hh-text-dim);
  }
  .run-detail {
    margin-top: var(--hh-s-8);
    padding: var(--hh-s-12) var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .run-detail .detail {
    font-size: 14px;
    color: var(--hh-text-dim);
    font-family: var(--hh-font-mono);
  }

  /* -- the commission register -------------------------------------------------------------------- */

  .lines-row > td {
    background: var(--hh-row-alt);
    padding: var(--hh-s-12);
  }
  .lines-table {
    font-size: 13px;
  }
  .unpaid td {
    color: var(--hh-text-dim);
    font-style: italic;
  }
  .warn-line {
    font-size: 14px;
    color: var(--hh-accent-text);
    margin: 0 0 var(--hh-s-8);
    max-width: 62ch;
  }
  .rule-form .nav-cat {
    padding: 0 0 var(--hh-s-8);
  }
`;
