// Warranty's own sheet, beside the app's homeStyles: the board's columns, the
// stat cards, the badges, the tables, the timeline, the chase and settle
// forms. Token-only, no literal colours anywhere; state ink comes from the
// state tokens and the word is always the carrier. Gold stays the action
// colour: a badge, a bar or a bucket is data and never wears it.
import { css } from "lit";

export const warrantyStyles = css`
  /* -- the scope row and the tabs reuse .kind-tab from homeStyles; the scope row groups it -- */

  .scope-row {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }

  .toolbar {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-12);
    align-items: center;
    justify-content: space-between;
    margin: 0 0 var(--hh-s-16);
  }
  .toolbar .def-note {
    margin: 0;
    flex: 1 1 24em;
  }
  .toolbar .row-actions {
    margin: 0;
  }

  .btn-small {
    min-height: 32px;
    padding: 0 var(--hh-s-12);
    font-size: 14px;
  }

  /* -- the board ------------------------------------------------------------------------------ */

  .board {
    display: flex;
    gap: var(--hh-s-12);
    overflow-x: auto;
    padding-bottom: var(--hh-s-8);
  }
  .col {
    flex: 0 0 220px;
    min-width: 220px;
    display: flex;
    flex-direction: column;
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
  }
  .col-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--hh-s-8) var(--hh-s-12);
    border-bottom: 1px solid var(--hh-hairline);
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .col-head .count {
    font-family: var(--hh-font-mono, monospace);
  }
  .cards {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-8);
    min-height: var(--hh-s-40);
  }
  .col-empty {
    margin: 0;
    text-align: center;
    color: var(--hh-text-dim);
    font-size: var(--hh-s-16);
  }
  .claim-card {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
    text-align: start;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .claim-card:hover {
    border-color: var(--hh-accent-text);
  }
  .claim-card .no {
    font-family: var(--hh-font-mono, monospace);
    font-size: var(--hh-s-12);
    color: var(--hh-info);
  }
  .claim-card .vendor {
    color: var(--hh-text);
  }
  .claim-card .meta {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-8);
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
  }

  .claim-link {
    padding: 0;
    min-height: 0;
    border: none;
    background: none;
    color: var(--hh-info);
    font-family: var(--hh-font-mono, monospace);
    font-size: 14px;
    cursor: pointer;
    text-decoration: underline;
  }
  .claim-link:hover {
    color: var(--hh-text);
  }

  /* -- forms ----------------------------------------------------------------------------------- */

  .form-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
    gap: var(--hh-s-12);
    padding: var(--hh-s-20);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-widget-raised);
    margin: 0 0 var(--hh-s-16);
  }
  .form-title {
    grid-column: 1 / -1;
    margin: 0;
    font-size: var(--hh-s-16);
    font-weight: 650;
  }
  .form-actions {
    grid-column: 1 / -1;
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }
  .form-note {
    grid-column: 1 / -1;
    margin: 0;
    font-size: 14px;
    color: var(--hh-text-dim);
    max-width: 70ch;
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .select-field {
    font-family: inherit;
    font-size: var(--hh-s-16);
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-field);
    color: var(--hh-text);
    min-height: 44px;
  }
  .grow {
    flex: 1;
    min-width: 12em;
  }

  /* -- badges ----------------------------------------------------------------------------------- */

  .badge {
    display: inline-block;
    padding: 2px var(--hh-s-8);
    border-radius: var(--hh-radius-pill);
    font-size: var(--hh-s-12);
    font-weight: 600;
    border: 1px solid var(--hh-control-line);
    color: var(--hh-text-dim);
    white-space: nowrap;
  }
  .badge.ok {
    color: var(--hh-success);
    border-color: var(--hh-success);
  }
  .badge.warn {
    color: var(--hh-state-watch);
    border-color: var(--hh-state-watch);
  }
  .badge.stop {
    color: var(--hh-danger);
    border-color: var(--hh-danger);
  }
  .badge.dim {
    color: var(--hh-text-dim);
    border-color: var(--hh-control-line);
  }
  .badge.work {
    color: var(--hh-info);
    border-color: var(--hh-info);
  }

  /* -- stat cards --------------------------------------------------------------------------------- */

  .stat-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
    gap: var(--hh-s-12);
    margin: 0 0 var(--hh-s-16);
  }
  .stat {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
  }
  .stat .n {
    font-family: var(--hh-font-mono, monospace);
    font-size: var(--hh-s-24);
    font-weight: 650;
    color: var(--hh-text);
  }
  .stat .l {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    text-transform: uppercase;
    letter-spacing: 0.06em;
  }
  .stat.bad .n {
    color: var(--hh-danger);
  }
  .stat.warn .n {
    color: var(--hh-state-watch);
  }
  .stat.headline .n {
    color: var(--hh-accent-text);
  }

  /* -- tables -------------------------------------------------------------------------------------- */

  .table-card {
    overflow-x: auto;
  }
  .table {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
  }
  .table th,
  .table td {
    text-align: start;
    padding: var(--hh-s-8) var(--hh-s-12);
    border-bottom: 1px solid var(--hh-hairline);
    vertical-align: top;
  }
  .table th {
    font-size: var(--hh-s-12);
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--hh-text-dim);
  }
  .table tr:last-child td {
    border-bottom: none;
  }
  .table .num {
    text-align: end;
  }
  .table .panel-row td {
    padding: 0 var(--hh-s-12) var(--hh-s-16);
    background: var(--hh-row-alt);
  }
  .mono {
    font-family: var(--hh-font-mono, monospace);
  }
  .vendor-no {
    display: block;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  td .vendor-no {
    display: inline;
  }
  .coverage-cell {
    max-width: 24em;
  }

  /* -- checklist and checkbox rows -------------------------------------------------------------------- */

  .check-row {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    min-height: 36px;
    padding: 0;
    border: none;
    background: none;
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .check-row .mark {
    flex: none;
    width: 16px;
    height: 16px;
    border-radius: 4px;
    border: 2px solid var(--hh-control-line);
    background: transparent;
  }
  .check-row[aria-checked="true"] .mark {
    border-color: var(--hh-success);
    background: var(--hh-success);
  }
  .check-row[aria-checked="true"] {
    color: var(--hh-text-dim);
  }
  .check-row .req {
    color: var(--hh-danger);
    font-size: var(--hh-s-12);
  }
  .check-list {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    align-items: flex-start;
  }

  /* -- boxes and the detail grid ------------------------------------------------------------------------- */

  .box {
    padding: var(--hh-s-16) var(--hh-s-20);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
  }
  .detail-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }
  .detail-id {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }
  .claim-no {
    font-family: var(--hh-font-mono, monospace);
    font-size: var(--hh-s-24);
    color: var(--hh-info);
  }
  .detail-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: var(--hh-s-16);
  }
  .kv {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-12);
    font-size: 14px;
    padding: 3px 0;
  }
  .kv > span:first-child {
    color: var(--hh-text-dim);
  }
  .description {
    margin: var(--hh-s-8) 0 0;
    max-width: 60ch;
  }
  .block-title {
    margin: 0 0 var(--hh-s-8);
    font-size: 14px;
    font-weight: 650;
  }
  .settle-form,
  .override-form {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: var(--hh-s-12);
    margin: var(--hh-s-8) 0 0;
  }
  .settle-form .full,
  .override-form .full {
    grid-column: 1 / -1;
  }

  /* -- photos -------------------------------------------------------------------------------------------- */

  .photo-row {
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }
  .photo {
    margin: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }
  .photo img {
    width: 168px;
    height: 126px;
    object-fit: cover;
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-hairline);
    background: var(--hh-track);
  }

  /* -- timeline --------------------------------------------------------------------------------------------- */

  .timeline {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .timeline li {
    border-left: 2px solid var(--hh-hairline);
    padding: 0 0 var(--hh-s-12) var(--hh-s-12);
    position: relative;
  }
  .timeline li::before {
    content: "";
    position: absolute;
    left: -5px;
    top: 6px;
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: var(--hh-info);
  }
  .timeline .when {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }

  /* -- recovery -------------------------------------------------------------------------------------------- */

  .buckets {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }
  .bucket {
    font-size: var(--hh-s-12);
    padding: 2px var(--hh-s-8);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    color: var(--hh-text-dim);
    font-family: var(--hh-font-mono, monospace);
  }
  .bucket.hot {
    border-color: var(--hh-state-watch);
    color: var(--hh-state-watch);
  }
  .vendor-panel-card {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
  }
  .vendor-head {
    display: flex;
    align-items: baseline;
    gap: var(--hh-s-8) var(--hh-s-16);
    flex-wrap: wrap;
  }
  .vendor-head .buckets {
    margin: 0;
  }
  .vendor-head .panel-title {
    margin: 0;
    font-size: var(--hh-s-16);
  }
  .amount {
    font-weight: 650;
  }
  .amount.big {
    font-family: var(--hh-font-mono, monospace);
    font-size: var(--hh-s-20);
    color: var(--hh-accent-text);
  }
  .vendor-panel {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
    gap: var(--hh-s-16);
    padding: var(--hh-s-16);
  }
  .vendor-panel-col {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    min-width: 0;
  }
  .plain-list {
    margin: 0;
    padding: 0 0 0 1.2em;
  }
  .chase {
    padding: var(--hh-s-12) var(--hh-s-16);
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .chase-log {
    margin: 0 0 var(--hh-s-8);
    padding-left: 1.2em;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }
  .chase-form {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
    gap: var(--hh-s-12);
    align-items: end;
  }

  /* -- reports ------------------------------------------------------------------------------------------------ */

  .report-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(340px, 1fr));
    gap: var(--hh-s-16);
    margin: 0 0 var(--hh-s-16);
  }
  .funnel .label {
    font-size: 14px;
    color: var(--hh-text);
  }

  /* -- small screens: the board keeps its scroll, everything else stacks -------------------------------------- */

  @media (max-width: 700px) {
    .detail-grid,
    .report-grid,
    .vendor-panel {
      grid-template-columns: 1fr;
    }
  }
`;
