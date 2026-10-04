// The pricing panel's sheet, over the panels' shared one (panel-styles.ts). The structure
// and density follow the ERP's pricing console (a bordered card with a small caps heading,
// a toolbar, striped rows, mono figures, dialogs for the editors), drawn in the design
// system's tokens through the same --hos-* names with --hh-* fallbacks, so light and dark
// both work. A disabled control drops its fill and goes grey: colour is the state.
import { css } from "lit";
import { panelStyles } from "./panel-styles.js";

export const pricingStyles = [
  panelStyles,
  css`
    :host {
      display: block;
      --pr-mono: var(--hos-font-mono, var(--hh-font-mono, ui-monospace, monospace));
    }
    .panel {
      background: var(--hos-slate-warm, var(--hh-card));
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 12px;
      overflow: hidden;
    }
    .head {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 12px;
      flex-wrap: wrap;
      padding: 16px 20px;
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
    }
    .head h2 {
      margin: 0;
      font-size: 13px;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 0.14em;
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .count {
      font-family: var(--pr-mono);
      font-size: 12px;
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .toolbar {
      display: flex;
      gap: 10px;
      align-items: center;
      flex-wrap: wrap;
      padding: 12px 20px;
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
    }
    .toolbar .push {
      margin-left: auto;
    }
    .toolbar select,
    .toolbar input[type="date"] {
      padding: 8px 10px;
      font-size: 13px;
    }
    .toolbar label.range {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      margin: 0;
      font-size: 12px;
    }
    .panel table {
      font-size: 14px;
    }
    .panel th {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.1em;
      padding: 11px 20px;
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
    }
    .panel td {
      padding: 11px 20px;
    }
    .panel tbody tr:last-child td {
      border-bottom: none;
    }
    .panel tbody tr.inactive {
      opacity: 0.55;
    }
    td.r,
    th.r {
      text-align: right;
      white-space: nowrap;
    }
    .mono {
      font-family: var(--pr-mono);
    }
    .amber {
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .dim {
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .small {
      font-size: 12px;
    }
    .msg {
      padding: 18px 20px;
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 13px;
      margin: 0;
    }
    .msg.err {
      color: var(--hos-safety-red, var(--hh-danger));
    }
    .badge {
      display: inline-block;
      font-size: 11px;
      font-family: var(--pr-mono);
      padding: 2px 8px;
      border-radius: 999px;
      border: 1px solid var(--hos-line, var(--hh-line));
      color: var(--hos-text-dim, var(--hh-text-dim));
      white-space: nowrap;
    }
    .badge.red {
      color: var(--hos-safety-red, var(--hh-danger));
      border-color: var(--hos-safety-red, var(--hh-danger));
    }
    .badge.green {
      color: var(--hos-success, var(--hh-success));
      border-color: var(--hos-success, var(--hh-success));
    }
    .scopechip {
      font-family: var(--pr-mono);
      font-size: 11px;
      white-space: nowrap;
      color: var(--hos-info-blue, var(--hh-accent-text));
    }
    /* buttons: the console's sizes, over the shared sheet's compact ones */
    button {
      padding: 7px 14px;
      font-size: 13px;
    }
    .rowbtns {
      display: inline-flex;
      gap: 6px;
    }
    .rowbtns button,
    .sched-head .acts button {
      padding: 4px 10px;
      font-size: 12px;
    }
    button.danger {
      border-color: var(--hos-safety-red, var(--hh-danger));
    }
    /* the console's inputs are filled boxes, not outlined pills: the striped row's own tint
       fills them, so they read in light and dark alike */
    .panel td input,
    .field input,
    .toolbar input {
      border-radius: 6px;
      background: color-mix(in srgb, var(--hos-text, var(--hh-text)) 5%, var(--hos-row-alt, var(--hh-row-alt)));
    }
    input.mono,
    td input.mono {
      font-family: var(--pr-mono);
      text-align: left;
    }
    /* figures sit right, ids and codes sit left */
    input.mono[inputmode],
    td input.mono[inputmode],
    input.cell {
      text-align: right;
    }
    input.bad {
      border-color: var(--hos-safety-red, var(--hh-danger));
    }
    .saved {
      color: var(--hos-success, var(--hh-success));
      font-size: 12px;
      font-family: var(--pr-mono);
    }
    .rowerr {
      color: var(--hos-safety-red, var(--hh-danger));
      font-size: 11px;
      margin-top: 4px;
    }
    .pct {
      color: color-mix(in srgb, var(--hos-text, var(--hh-text)) 78%, transparent);
      font-family: var(--pr-mono);
      font-size: 13px;
    }
    .cellnum {
      display: inline-flex;
      align-items: center;
      gap: 6px;
    }
    input.cell {
      width: 100px;
      padding: 8px 10px;
      font-size: 13px;
    }
    .flagrow {
      display: flex;
      align-items: center;
      gap: 7px;
      font-size: 13px;
      margin: 0;
      color: var(--hos-text, var(--hh-text));
    }
    tr.focused > td {
      background: color-mix(in srgb, var(--hos-stone-amber, var(--hh-accent)) 14%, transparent);
    }
    tr.focused > td:first-child {
      box-shadow: inset 3px 0 0 var(--hos-stone-amber, var(--hh-accent));
    }
    /* the console's pill sub navigation */
    .subnav {
      display: flex;
      gap: 6px;
      flex-wrap: wrap;
      margin: 0 0 16px;
    }
    .subnav button {
      border-radius: 999px;
      padding: 7px 15px;
      color: var(--hos-text-dim, var(--hh-text-dim));
      background: transparent;
    }
    .subnav button.active {
      color: var(--hos-text, var(--hh-text));
      border-color: var(--hos-stone-amber, var(--hh-accent));
      box-shadow: inset 0 -2px 0 var(--hos-stone-amber, var(--hh-accent));
      font-weight: 600;
    }
    /* the quantity break schedules */
    .sched {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 10px;
      margin: 14px 20px;
      overflow: hidden;
    }
    .sched.inactive {
      opacity: 0.55;
    }
    .sched-head {
      display: flex;
      align-items: baseline;
      gap: 10px;
      flex-wrap: wrap;
      padding: 12px 16px;
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
    }
    .sched-head .code {
      font-family: var(--pr-mono);
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
      font-size: 13px;
    }
    .sched-head .scope {
      font-family: var(--pr-mono);
      font-size: 11px;
      color: var(--hos-info-blue, var(--hh-accent-text));
    }
    .sched-head .acts {
      margin-left: auto;
      display: inline-flex;
      gap: 6px;
    }
    .sched table th,
    .sched table td {
      padding: 10px 16px;
    }
    details.info {
      padding: 10px 20px 0;
      font-size: 12px;
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    details.info summary {
      cursor: pointer;
      display: inline-block;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 999px;
      padding: 1px 9px;
      font-size: 11px;
      list-style: none;
      user-select: none;
    }
    details.info summary::-webkit-details-marker {
      display: none;
    }
    details.info[open] summary {
      border-color: var(--hos-stone-amber, var(--hh-accent));
    }
    .note {
      margin: 14px 20px 0;
      padding: 10px 12px;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-left: 3px solid var(--hos-stone-amber, var(--hh-accent));
      border-radius: var(--hos-radius-control, var(--hh-radius-control));
      font-size: 12px;
      color: var(--hos-text, var(--hh-text));
    }
    /* the pricing log's pager and links */
    .pager {
      display: flex;
      align-items: center;
      gap: 12px;
      padding: 12px 20px;
      border-top: 1px solid var(--hos-line, var(--hh-line));
      font-size: 12px;
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .pager button {
      padding: 6px 12px;
      font-size: 12px;
    }
    .pager .window {
      margin-left: auto;
      font-style: italic;
    }
    .delta {
      font-family: var(--pr-mono);
      font-size: 12px;
    }
    .delta .old {
      color: var(--hos-text-dim, var(--hh-text-dim));
      text-decoration: line-through;
    }
    .delta .arrow {
      color: var(--hos-text-dim, var(--hh-text-dim));
      margin: 0 4px;
    }
    .ts {
      font-family: var(--pr-mono);
      font-size: 12px;
      color: var(--hos-text-dim, var(--hh-text-dim));
      white-space: nowrap;
    }
    button.link {
      border: none;
      background: transparent;
      padding: 0;
      font-size: 12px;
      color: var(--hos-info-blue, var(--hh-accent-text));
      border-bottom: 1px dotted currentColor;
      border-radius: 0;
    }
    .no-ref {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 12px;
    }
    /* the customer lookup row */
    .lookup {
      display: flex;
      gap: 10px;
      align-items: flex-end;
      padding: 14px 20px;
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
      flex-wrap: wrap;
    }
    .lookup hos-customer-picker {
      flex: 1;
      min-width: 280px;
    }
    /* the refusal the server gave, shown in place of the controls */
    .refusal {
      border: 1px solid var(--hos-safety-red, var(--hh-danger));
      border-radius: 12px;
      padding: 16px 20px;
      background: color-mix(in srgb, var(--hos-safety-red, var(--hh-danger)) 6%, transparent);
    }
    .refusal h2 {
      margin: 0 0 6px;
      font-size: 13px;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 0.14em;
      color: var(--hos-safety-red, var(--hh-danger));
    }
    .refusal p {
      margin: 4px 0 0;
    }
    .readonly-note {
      margin: 0 0 12px;
      padding: 10px 12px;
      border: 1px solid var(--hos-safety-red, var(--hh-danger));
      border-radius: var(--hos-radius-control, var(--hh-radius-control));
      font-size: 13px;
    }
    /* dialogs */
    .backdrop {
      position: fixed;
      inset: 0;
      z-index: 1000;
      display: flex;
      align-items: flex-start;
      justify-content: center;
      padding: 48px 16px;
      overflow-y: auto;
      background: color-mix(in srgb, var(--hos-deep-earth, var(--hh-page)) 62%, transparent);
    }
    .backdrop.top {
      z-index: 1100;
    }
    .dialog {
      width: min(460px, 100%);
      background: var(--hos-slate-warm, var(--hh-card));
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 12px;
      box-shadow: 0 12px 40px rgba(0, 0, 0, 0.35);
      font-size: var(--qp-body);
    }
    .dialog.wide {
      width: min(620px, 100%);
    }
    .dialog.narrow {
      width: min(400px, 100%);
    }
    .dialog-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 16px 20px;
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
    }
    .dialog-head h3 {
      margin: 0;
      font-size: 15px;
      font-weight: 700;
    }
    .dialog-head .x {
      border: none;
      background: transparent;
      font-size: 18px;
      padding: 0 4px;
      line-height: 1;
    }
    .dialog-body {
      padding: 20px;
      display: flex;
      flex-direction: column;
      gap: 14px;
    }
    .dialog-foot {
      display: flex;
      justify-content: flex-end;
      gap: 10px;
      padding: 14px 20px;
      border-top: 1px solid var(--hos-line, var(--hh-line));
    }
    .field {
      display: flex;
      flex-direction: column;
      gap: 5px;
    }
    .field label {
      margin: 0;
      font-size: 12px;
    }
    .field input,
    .field select {
      padding: 9px 10px;
      font-size: 13px;
    }
    .hint {
      font-size: 11px;
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .confirm-note {
      border: 1px solid var(--hos-safety-red, var(--hh-danger));
      border-radius: 8px;
      padding: 10px 12px;
      font-size: 12px;
      color: var(--hos-safety-red, var(--hh-danger));
      background: color-mix(in srgb, var(--hos-safety-red, var(--hh-danger)) 6%, transparent);
    }
    .tier-row {
      display: grid;
      grid-template-columns: 64px 64px minmax(0, 1.5fr) minmax(0, 1fr) 62px 32px;
      gap: 8px;
      align-items: end;
      margin-bottom: 8px;
    }
    .tier-row .field {
      min-width: 0;
    }
    .tier-row .field input,
    .tier-row .field select {
      min-width: 0;
      width: 100%;
      box-sizing: border-box;
      padding: 8px 6px;
    }
    .tier-row .field label {
      font-size: 10px;
    }
    .tier-row.unapplied .field input,
    .tier-row.unapplied .field select {
      border-style: dashed;
    }
    .tier-mark {
      grid-column: 1 / -1;
      font-size: 11px;
      color: var(--hos-text-dim, var(--hh-text-dim));
      margin-top: -4px;
    }
    .floor-list {
      list-style: none;
      margin: 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
      max-height: 14rem;
      overflow-y: auto;
    }
    .settled {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 12px;
    }
    .floor-list li {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-left: 3px solid var(--hos-safety-red, var(--hh-danger));
      border-radius: var(--hos-radius-control, var(--hh-radius-control));
      padding: 6px 10px;
      font-size: 12px;
    }
  `,
];
