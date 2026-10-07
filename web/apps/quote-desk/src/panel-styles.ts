// The panels' shared styles: the second coder's panels (the edit panel, the
// change history and the three settings screens) draw from one sheet so the
// quote detail region reads as one surface. Tokens are the design system's
// roles through the ported surface's --hos-* names, with the base --hh-*
// fallbacks the work area uses. A disabled control drops its fill and goes
// grey (colour is the state, not fade): the house rule.
import { css } from "lit";

export const panelStyles = css`
  :host {
    display: block;
    font-family: var(--hos-font-ui, var(--hh-font-ui));
    color: var(--hos-text, var(--hh-text));
    --qp-section: 0.95rem;
    --qp-body: 0.83rem;
    --qp-caption: 0.72rem;
    font-size: var(--qp-body);
  }
  .box {
    border: 1px solid var(--hos-line, var(--hh-line));
    border-radius: var(--hos-radius-card, var(--hh-radius-card));
    padding: var(--hos-s-12, 12px) var(--hos-s-16, 16px);
    background: var(--hos-slate-warm, var(--hh-card));
  }
  h3 {
    margin: 0 0 4px;
    font-size: var(--qp-section);
    font-weight: 600;
  }
  .sub {
    font-size: var(--qp-caption);
    color: var(--hos-text-dim, var(--hh-text-dim));
    margin-bottom: var(--hos-s-12, 12px);
  }
  .dim {
    font-size: var(--qp-body);
    color: var(--hos-text-dim, var(--hh-text-dim));
    margin: 6px 0 0;
  }
  button:focus-visible,
  input:focus-visible,
  select:focus-visible,
  summary:focus-visible,
  [tabindex]:focus-visible {
    outline: 2px solid var(--hos-stone-amber-text, var(--hh-accent-text));
    outline-offset: 2px;
  }
  /* A wide table scrolls inside its own box: the page never scrolls sideways for it (phone widths). */
  .tscroll {
    max-width: 100%;
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--qp-body);
  }
  th {
    text-align: left;
    font-weight: 600;
    color: var(--hos-text-dim, var(--hh-text-dim));
    border-bottom: 1px solid var(--hos-line, var(--hh-line));
    padding: 4px 6px;
  }
  td {
    padding: 5px 6px;
    border-bottom: 1px solid var(--hos-line, var(--hh-line));
    vertical-align: middle;
  }
  tbody tr:nth-child(even) td {
    background: var(--hos-row-alt, var(--hh-row-alt));
  }
  td.num,
  th.num {
    text-align: right;
  }
  input,
  select {
    font: inherit;
    font-size: var(--qp-body);
    padding: 3px 6px;
    border: 1px solid var(--hos-control-line, var(--hh-control-line));
    border-radius: var(--hos-radius-control, var(--hh-radius-control));
    background: var(--hos-deep-earth, var(--hh-page));
    color: var(--hos-text, var(--hh-text));
  }
  input.qty,
  input.disc {
    width: 5.5rem;
    text-align: right;
  }
  input.disc {
    width: 4rem;
  }
  input.wide {
    width: 12rem;
  }
  /* The customer link box takes the row, so a link is not cut off. */
  .row input.wide {
    flex: 1 1 22rem;
    min-width: 0;
    width: auto;
  }
  input[type="checkbox"] {
    accent-color: var(--hos-stone-amber, var(--hh-accent));
  }
  input:disabled,
  select:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
  }
  button {
    font: inherit;
    font-size: var(--qp-body);
    padding: 3px 10px;
    border: 1px solid var(--hos-line, var(--hh-line));
    border-radius: var(--hos-radius-control, var(--hh-radius-control));
    background: var(--hos-slate-warm, var(--hh-card));
    color: var(--hos-text, var(--hh-text));
    cursor: pointer;
  }
  button:hover:not(:disabled) {
    border-color: var(--hos-stone-amber, var(--hh-accent));
    color: var(--hos-stone-amber-text, var(--hh-accent-text));
  }
  button:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
    cursor: default;
  }
  button.danger {
    color: var(--hos-safety-red, var(--hh-danger));
  }
  button.primary {
    background: var(--hos-stone-amber, var(--hh-accent));
    border-color: var(--hos-stone-amber, var(--hh-accent));
    color: var(--hos-stone-amber-ink, var(--hh-accent-ink));
    font-weight: 600;
  }
  button.primary:hover:not(:disabled) {
    background: var(--hos-stone-amber-hover, var(--hh-accent-hover));
    border-color: var(--hos-stone-amber-hover, var(--hh-accent-hover));
    color: var(--hos-stone-amber-ink, var(--hh-accent-ink));
  }
  button.primary:disabled {
    background: var(--hh-disabled-fill);
    border-color: var(--hh-disabled-line);
    color: var(--hh-disabled-ink);
  }
  label {
    display: block;
    font-size: var(--qp-caption);
    color: var(--hos-text-dim, var(--hh-text-dim));
    margin: 8px 0 4px;
  }
  .row {
    display: flex;
    gap: var(--hos-s-8, 8px);
    align-items: center;
    flex-wrap: wrap;
    margin-top: 10px;
  }
  .totals {
    margin-top: 10px;
    display: flex;
    gap: 14px;
    justify-content: flex-end;
  }
  .totals b {
    font-weight: 600;
  }
  fieldset {
    border: 1px solid var(--hos-line, var(--hh-line));
    border-radius: 6px;
    margin: 12px 0 0;
    padding: 8px 10px;
  }
  legend {
    font-size: var(--qp-caption);
    color: var(--hos-text-dim, var(--hh-text-dim));
    padding: 0 4px;
  }
  .hits {
    list-style: none;
    margin: 4px 0 0;
    padding: 0;
    max-height: 9rem;
    overflow-y: auto;
    border: 1px solid var(--hos-line, var(--hh-line));
    border-radius: 5px;
  }
  .hits li {
    padding: 3px 6px;
    cursor: pointer;
  }
  .hits li:hover {
    background: var(--hos-line, var(--hh-line));
  }
  .hits li[aria-selected="true"] {
    background: color-mix(in srgb, var(--hos-stone-amber, var(--hh-accent)) 22%, transparent);
    box-shadow: inset 2px 0 0 var(--hos-stone-amber, var(--hh-accent));
  }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip: rect(0 0 0 0);
    white-space: nowrap;
  }
  .picked {
    font-size: var(--qp-caption);
    color: var(--hos-text-dim, var(--hh-text-dim));
  }
  .err {
    color: var(--hos-safety-red, var(--hh-danger));
    font-size: var(--qp-body);
    margin-top: 8px;
  }
  .ok {
    color: var(--hos-success, var(--hh-success));
    font-size: var(--qp-body);
    margin-top: 8px;
  }
  .err:empty,
  .ok:empty {
    margin: 0;
    height: 0;
    overflow: hidden;
  }
  .tag {
    display: inline-block;
    font-size: var(--qp-caption);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    border: 1px solid var(--hos-line, var(--hh-line));
    border-radius: 999px;
    padding: 1px 7px;
    color: var(--hos-text-dim, var(--hh-text-dim));
  }
  .lock {
    border: 1px solid var(--hh-disabled-line);
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-radius: var(--hos-radius-control, var(--hh-radius-control));
    padding: 6px 10px;
    margin-top: 8px;
    font-size: var(--qp-body);
  }
  /* Spacing utilities, doubled in specificity so a component's own margin rules never win over them. */
  .flush-top.flush-top {
    margin-top: 0;
  }
  .flush.flush {
    margin: 0;
  }
  .gap-s.gap-s {
    margin-top: 8px;
  }
  .gap-m.gap-m {
    margin-top: 10px;
  }
`;
