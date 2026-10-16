// Approvals view styles: the kind pill tabs, the split list and detail
// layout, metric bands, lines tables, metadata rows, and decision cards.
import { css } from "lit";

export const approvalsStyles = css`
  /* -- kind pill tabs -------------------------------------------------------- */

  .kind-tabs {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-20);
  }

  .kind-tab {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 14px;
    font-weight: 500;
    cursor: pointer;
    transition: background 0.15s ease, border-color 0.15s ease, color 0.15s ease;
  }

  .kind-tab:hover {
    border-color: var(--brand-fill);
  }

  .kind-tab.active,
  .kind-tab[aria-selected="true"] {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    font-weight: 700;
  }

  /* -- split list/detail layout ---------------------------------------------- */

  .approvals-layout {
    display: flex;
    gap: var(--hh-s-20);
    align-items: flex-start;
  }

  @media (max-width: 1024px) {
    .approvals-layout {
      flex-direction: column;
    }
  }

  /* -- left request list ----------------------------------------------------- */

  .approvals-list {
    width: 360px;
    flex-shrink: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
  }

  @media (max-width: 1024px) {
    .approvals-list {
      width: 100%;
    }
  }

  .approval-card {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16);
    cursor: pointer;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-6, 6px);
    transition: border-color 0.15s ease, box-shadow 0.15s ease;
    box-sizing: border-box;
    text-align: left;
  }

  .approval-card:hover {
    border-color: var(--hh-control-line);
  }

  .approval-card.selected,
  .approval-card[aria-selected="true"] {
    border: 2px solid var(--brand-fill);
    box-shadow: 0 0 0 1px var(--brand-fill);
  }

  .approval-card-head {
    display: flex;
    justify-content: space-between;
    align-items: baseline;
    gap: var(--hh-s-8);
  }

  .card-title {
    font-size: 15px;
    font-weight: 700;
    color: var(--hh-text);
    line-height: 1.3;
  }

  .card-amount {
    font-size: 15px;
    font-weight: 700;
    color: var(--hh-text);
    white-space: nowrap;
  }

  .card-summary {
    font-size: 13px;
    color: var(--hh-text);
    margin: 0;
    line-height: 1.35;
  }

  .card-meta {
    font-size: 12px;
    color: var(--hh-text-dim);
    margin: 0;
  }

  .status-approved {
    color: var(--hh-state-good);
    font-weight: 600;
  }

  .status-rejected {
    color: var(--hh-state-stop);
    font-weight: 600;
  }

  /* -- right detail card ----------------------------------------------------- */

  .approvals-detail {
    flex: 1;
    min-width: 0;
    width: 100%;
  }

  .decision-card {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-24);
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-20);
    box-sizing: border-box;
  }

  .decision-card-head {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }

  .decision-title {
    font-size: var(--hh-s-20, 20px);
    font-weight: 700;
    color: var(--hh-text);
    line-height: 1.25;
    margin: 0;
  }

  .decision-subtitle {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: 0;
    line-height: 1.4;
  }

  /* -- metric band ----------------------------------------------------------- */

  .metric-band {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: var(--hh-s-12);
    padding: var(--hh-s-16) var(--hh-s-20);
    border-radius: 12px;
    border: 1px solid var(--hh-line);
    background: var(--hh-field);
    box-sizing: border-box;
  }

  @media (max-width: 768px) {
    .metric-band {
      grid-template-columns: repeat(2, 1fr);
    }
  }

  .metric-col {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }

  .metric-eyebrow {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }

  .metric-val {
    font-size: 22px;
    font-weight: 700;
    color: var(--hh-text);
    line-height: 1.2;
  }

  .tone-stop {
    color: var(--hh-state-stop) !important;
  }

  .tone-watch {
    color: var(--hh-state-watch) !important;
  }

  .tone-good {
    color: var(--hh-state-good) !important;
  }

  /* -- table section --------------------------------------------------------- */

  .table-section {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }

  .section-heading {
    font-size: 16px;
    font-weight: 700;
    color: var(--hh-text);
    margin: 0 0 var(--hh-s-4);
  }

  .approval-table {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
  }

  .approval-table th {
    text-align: left;
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    border-bottom: 1px solid var(--hh-line);
    padding: var(--hh-s-8) var(--hh-s-6, 6px);
  }

  .approval-table td {
    padding: var(--hh-s-12) var(--hh-s-6, 6px);
    border-bottom: 1px solid var(--hh-line);
    vertical-align: top;
    color: var(--hh-text);
  }

  .approval-table .th-qty,
  .approval-table .td-qty,
  .approval-table .th-price,
  .approval-table .td-price,
  .approval-table .th-cost,
  .approval-table .td-cost,
  .approval-table .th-margin,
  .approval-table .td-margin,
  .approval-table .th-floor,
  .approval-table .td-floor,
  .approval-table .th-debit,
  .approval-table .td-debit,
  .approval-table .th-credit,
  .approval-table .td-credit {
    text-align: right;
  }

  .table-line-name {
    display: block;
    font-weight: 700;
    color: var(--hh-text);
  }

  .table-line-sku {
    display: block;
    font-size: 12px;
    color: var(--hh-text-dim);
    margin-top: 2px;
  }

  .font-bold {
    font-weight: 700;
  }

  /* -- metadata rows --------------------------------------------------------- */

  .metadata-section {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    font-size: 14px;
    padding-top: var(--hh-s-12);
    border-top: 1px dashed var(--hh-line);
  }

  .meta-row {
    display: grid;
    grid-template-columns: 160px 1fr;
    gap: var(--hh-s-12);
    line-height: 1.5;
  }

  @media (max-width: 640px) {
    .meta-row {
      grid-template-columns: 1fr;
      gap: 2px;
    }
  }

  .meta-label {
    color: var(--hh-text-dim);
  }

  .meta-value {
    color: var(--hh-text);
  }

  /* -- decision section ------------------------------------------------------ */

  .decision-section {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    padding-top: var(--hh-s-16);
    border-top: 1px solid var(--hh-line);
  }

  .decision-eyebrow {
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }

  .decision-actions {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }

  .approve-btn {
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    border: none;
    font-weight: 600;
    padding: 10px 24px;
    border-radius: var(--hh-radius-control);
    cursor: pointer;
    font-size: 14px;
  }

  .approve-btn:hover {
    background: var(--brand-fill-hover);
  }

  .custom-price-btn,
  .custom-limit-btn,
  .message-btn {
    background: var(--hh-card);
    border: 1px solid var(--hh-control-line);
    color: var(--hh-text);
    font-weight: 600;
    padding: 10px 18px;
    border-radius: var(--hh-radius-control);
    cursor: pointer;
    font-size: 14px;
  }

  .custom-price-btn:hover,
  .custom-limit-btn:hover,
  .message-btn:hover {
    border-color: var(--hh-text);
  }

  .decline-btn,
  .open-entry-btn {
    background: transparent;
    border: none;
    color: var(--hh-quote-link);
    font-weight: 600;
    padding: 10px 14px;
    cursor: pointer;
    font-size: 14px;
    text-decoration: none;
  }

  .decline-btn:hover,
  .open-entry-btn:hover {
    text-decoration: underline;
  }

  .decline-reason-group {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-6, 6px);
    margin-top: var(--hh-s-8);
  }

  .reason-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--hh-text-dim);
  }

  .reason-input {
    padding: 10px 14px;
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    background: var(--hh-field);
    color: var(--hh-text);
    font-size: 14px;
    width: 100%;
    box-sizing: border-box;
  }

  .reason-input:focus {
    outline: none;
    border-color: var(--hh-focus-ring);
  }

  /* -- not yours to decide section ------------------------------------------- */

  .not-yours-section {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-16);
    padding-top: var(--hh-s-16);
    border-top: 1px solid var(--hh-line);
  }

  .not-yours-badge {
    display: inline-flex;
    align-self: flex-start;
    padding: 4px 12px;
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-state-watch);
    color: var(--hh-state-watch);
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }

  .not-yours-actions {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }

  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border-width: 0;
  }
`;
