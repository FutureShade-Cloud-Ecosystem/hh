// Dealer Admin's sheet. Token-only: no literal colours anywhere; light and
// brand (the dark half) both come from the design system's tokens, and the
// header's theme control flips the document root. Every ink is named: the
// host sets the text colour, and every control the browser would paint in
// its own button ink sets its own, because an unset ink is black, and black
// on the brand theme's navy fails contrast.
//
// THE APP'S SHAPE. One app, a left list of the surfaces the registry names,
// and the active surface's work on the right. The list is the app's own
// table of contents, not the shell's rail: the shell rail carries the user's
// pinned surfaces across every micro app they hold.
import { css } from "lit";

export const dealerStyles = css`
  :host {
    display: block;
    min-height: 100vh;
    background: var(--hh-page);
    color: var(--hh-text);
  }

  main {
    max-width: 1280px;
    margin: 0 auto;
    padding: var(--hh-s-24) var(--hh-s-16) var(--hh-s-64);
  }

  h1,
  h2,
  p {
    margin: 0;
  }

  /* -- the app's own left list of surfaces ---------------------------------- */

  .app-body {
    display: grid;
    grid-template-columns: 220px minmax(0, 1fr);
    gap: var(--hh-s-24);
    align-items: start;
  }
  @media (max-width: 900px) {
    .app-body {
      grid-template-columns: minmax(0, 1fr);
    }
  }

  /* The desk widths pin the list and the work to their own columns, so the
     phone's control and the DOM order that serves it change nothing there. */
  .surface-list {
    grid-column: 1;
    grid-row: 1;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-16);
    margin: 0;
    padding: var(--hh-s-16);
    list-style: none;
    background: var(--hh-card);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
  }
  .surface-work {
    grid-column: 2;
    grid-row: 1;
    min-width: 0;
  }
  /* The phone's Settings control (UX-104): the section's own work comes
     first on a phone, so the left list waits folded behind this control
     until it is asked for. A native button, so keyboard and touch arrive
     the same way; the sheet holds it to a 44px touch target, and
     aria-expanded with aria-controls announce the folded list. The desk
     widths hide it and never fold the list. */
  .surface-list-toggle {
    display: none;
  }
  @media (max-width: 900px) {
    .surface-list {
      grid-column: 1;
      grid-row: auto;
    }
    .surface-work {
      grid-column: 1;
      grid-row: auto;
    }
    .surface-list-toggle {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-height: 44px;
      padding: 0 var(--hh-s-16);
      border: 1px solid var(--hh-control-line);
      border-radius: var(--hh-radius-control);
      background: var(--hh-card);
      color: var(--hh-text);
      font-size: 14px;
      font-weight: 600;
      cursor: pointer;
    }
    .app-body:not(.list-open) .surface-list {
      display: none;
    }
  }
  .nav-group {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }
  .nav-group-title {
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.05em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    padding: 0 var(--hh-s-8) var(--hh-s-4);
  }
  .nav-group-items {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .surface-link {
    display: flex;
    align-items: center;
    width: 100%;
    text-align: start;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid transparent;
    border-left: 3px solid transparent;
    border-radius: var(--hh-radius-control);
    background: transparent;
    color: var(--hh-text);
    cursor: pointer;
  }
  .surface-link:hover {
    background: var(--hh-row-alt);
  }
  .surface-link[aria-current="true"] {
    border-left-color: var(--hh-accent);
    background: var(--hh-row-alt);
  }
  .surface-link .title {
    font-size: 14px;
    font-weight: 500;
  }
  .surface-link[aria-current="true"] .title {
    color: var(--hh-text);
    font-weight: 700;
  }

  /* -- the page's own head ---------------------------------------------------- */

  .page-head {
    margin: 0 0 var(--hh-s-24);
  }
  .page-head h1 {
    font-size: var(--hh-s-28);
    font-weight: 650;
    line-height: 1.15;
  }
  .page-sub {
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
    margin-top: var(--hh-s-4);
  }

  .error-line,
  .notice-line {
    margin: 0 0 var(--hh-s-16);
    font-size: var(--hh-s-16);
  }
  /* A notice that ends its card adds nothing to the card's own bottom padding. */
  .card > .notice-line:last-child {
    margin-bottom: 0;
  }
  .error-line {
    color: var(--hh-danger);
  }
  .notice-line {
    color: var(--hh-text-dim);
  }

  .row-actions {
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }

  /* -- the cards ---------------------------------------------------------------- */

  .card {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    padding: var(--hh-s-20);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    box-shadow: var(--hh-widget-shadow);
    margin: 0 0 var(--hh-s-16);
  }
  .card-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }
  .card-title {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .card-sub {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .count {
    font-size: 14px;
    color: var(--hh-text-dim);
  }

  /* -- the buttons ---------------------------------------------------------------- */

  .btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) {
    border-color: var(--hh-accent-text);
  }
  .btn:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .btn-primary {
    background: var(--hh-accent);
    border-color: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-weight: 600;
  }
  .btn-primary:hover:not(:disabled) {
    background: var(--hh-accent-hover);
    border-color: var(--hh-accent-hover);
    color: var(--hh-accent-ink);
  }
  .btn-small {
    min-height: 28px;
    padding: 0 var(--hh-s-12);
    font-size: 14px;
  }

  /* -- the chips ------------------------------------------------------------------ */

  .chip {
    display: inline-block;
    padding: 2px var(--hh-s-8);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    font-size: 14px;
    color: var(--hh-text);
    white-space: nowrap;
  }
  .chip-wait {
    border-style: dashed;
    color: var(--hh-text-dim);
  }

  /* -- the register's table ---------------------------------------------------------- */

  .tablewrap {
    overflow-x: auto;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 14px;
  }
  th,
  td {
    text-align: left;
    padding: var(--hh-s-8) var(--hh-s-8);
    border-bottom: 1px solid var(--hh-hairline);
    vertical-align: top;
  }
  th {
    font-size: var(--hh-s-12);
    text-transform: uppercase;
    letter-spacing: 0.08em;
    color: var(--hh-text-dim);
    font-weight: 600;
  }
  tbody tr:last-child td {
    border-bottom: none;
  }
  .person {
    font-weight: 650;
  }
  .mono {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
  }
  .dim {
    color: var(--hh-text-dim);
  }

  /* -- the fields ---------------------------------------------------------------------- */

  .select,
  .text-field {
    font-family: inherit;
    font-size: var(--hh-s-16);
    color: var(--hh-text);
    background: var(--hh-field);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    min-height: 36px;
  }
  .select {
    font-size: 14px;
    min-height: 32px;
    padding: var(--hh-s-4) var(--hh-s-8);
  }

  .form-grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
    gap: var(--hh-s-16);
  }
  .field {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
  }
  .field-full {
    grid-column: 1 / -1;
  }
  .field-label {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .text-field:disabled,
  .select:disabled {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
  }

  .branch-choices {
    display: flex;
    gap: var(--hh-s-16);
    flex-wrap: wrap;
    font-size: 14px;
  }
  .branch-choice {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
  }

  /* -- the reset link out ------------------------------------------------------------------ */

  .link-out {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto;
    gap: var(--hh-s-8);
    align-items: center;
    margin-top: var(--hh-s-8);
    max-width: 420px;
  }
  .link-out code {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    padding: var(--hh-s-4) var(--hh-s-8);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
    overflow-wrap: anywhere;
    color: var(--hh-text);
  }
  .link-out .chip {
    grid-column: 1 / -1;
    justify-self: start;
  }

  /* -- the roles catalog ---------------------------------------------------------------------- */

  .role-list {
    display: flex;
    flex-direction: column;
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .role-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-8) ;
    flex-wrap: wrap;
    padding: var(--hh-s-8) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .role-row:first-child {
    border-top: none;
  }
  .role-name {
    font-weight: 650;
    margin-inline-end: var(--hh-s-8);
  }

  /* -- the hotkeys' two rosters -------------------------------------------------------- */

  /* The two rosters sit side by side: the reason to read them is to compare
     "Everywhere in the app" against "At the counter" key by key, and the F4
     that means two different things in the two scopes is visible as the one
     collision it is. */
  .rosters {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
    gap: var(--hh-s-16);
    align-items: start;
    margin: 0 0 var(--hh-s-16);
  }
  .rosters .card {
    margin: 0;
  }
  .krow {
    display: grid;
    grid-template-columns: minmax(0, 1fr) auto auto;
    align-items: center;
    gap: var(--hh-s-8);
    padding: var(--hh-s-4) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .klabel {
    font-size: 14px;
  }
  .cap {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    padding: 2px var(--hh-s-8);
    white-space: nowrap;
  }
  /* A key the browser also claims. Dashed, not red: it is a fact, not a
     refusal; the escape rule is what keeps the browser's gesture reachable. */
  .cap-host {
    border-style: dashed;
  }
  .cap-none {
    color: var(--hh-text-dim);
  }
  .kcontrols {
    display: inline-flex;
    gap: var(--hh-s-8);
  }

  /* -- the admin-rest surfaces (assist first; the rest land beside it) -- */

  /* The allowance meter: a track and a fill whose width the host sets in
     code (the position gate binds no style), the original's own two box
     model on HH's tokens. A person at their limit is not an error: dimmed,
     never red. */
  .meter {
    display: grid;
    gap: var(--hh-s-4);
    min-width: 150px;
  }
  .meter-track {
    height: 6px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-widget-raised);
    border: 1px solid var(--hh-hairline);
    overflow: hidden;
  }
  .meter-fill {
    height: 100%;
    width: 0;
    background: var(--hh-accent);
  }
  .meter-spent {
    background: var(--hh-text-dim);
  }
  .edit-row {
    display: flex;
    gap: var(--hh-s-8);
    align-items: center;
    flex-wrap: wrap;
  }
  .budget-field {
    max-width: 140px;
    min-height: 32px;
    font-size: 14px;
  }
  .toggle {
    display: flex;
    align-items: flex-start;
    gap: var(--hh-s-12);
    padding: var(--hh-s-12) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .toggle:first-of-type {
    border-top: none;
  }
  .toggle input {
    margin-top: 2px;
    accent-color: var(--hh-accent);
  }
  .toggle-text {
    display: grid;
    gap: var(--hh-s-2);
  }
  .toggle-name {
    font-size: 14px;
  }
  .toggle-why {
    font-size: 14px;
    color: var(--hh-text-dim);
    max-width: 60ch;
  }
  .kv {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--hh-s-4) var(--hh-s-16);
    font-size: 14px;
    margin: var(--hh-s-8) 0 0;
  }
  .kv dt {
    color: var(--hh-text-dim);
  }
  .kv dd {
    margin: 0;
  }

  /* -- the audit trail ------------------------------------------------------------------ */
  .filters {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8);
    align-items: center;
    margin: 0 0 var(--hh-s-12);
  }
  .filters .text-field {
    min-width: 180px;
    min-height: 36px;
    font-size: 14px;
  }

  .chip-sensitive {
    color: var(--hh-danger);
    border-color: var(--hh-danger);
  }

  /* The drawer's pairs flow across the width instead of stranding a column
     of it: the label/value pair is the repeating unit. */
  .detail .kv {
    display: grid;
    grid-template-columns: repeat(auto-fill, 120px minmax(240px, 1fr));
    gap: var(--hh-s-8) var(--hh-s-16);
    font-size: 14px;
  }
  .detail .k {
    color: var(--hh-text-dim);
  }
  .detail .v {
    overflow-wrap: anywhere;
  }
  .detail .changes {
    margin: 0;
    padding: var(--hh-s-12);
    background: var(--hh-widget-raised);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    overflow-x: auto;
    color: var(--hh-text);
  }

  /* -- the role by surface grid -------------------------------------------------- */

  /* The grid is an eleven column table (the job shapes' own column plus the
     ten surfaces), so the wrap scrolls rather than squeezing a column into
     nothing. A cell button keeps its words short: the count by kind, or the
     registry default. */
  .cell-btn {
    white-space: nowrap;
  }
  tbody tr th[scope="row"] {
    font-weight: 400;
  }

  /* The editor's field rows: the add row's fields and its buttons ride one
     line while the width holds, and wrap on a narrow card. */
  .editor-actions {
    display: flex;
    align-items: end;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }
  .editor-actions .field {
    min-width: 140px;
  }
  .editor-actions .field-label {
    display: block;
  }
  .section-title {
    font-size: var(--hh-s-16);
    font-weight: 650;
    margin: 0;
  }

  /* -- the way in (the demo door) ------------------------------------------------------------------ */

  .stage {
    max-width: 680px;
    margin: 0 auto;
    padding: var(--hh-s-40) 0 var(--hh-s-64);
  }
  .stage .headline {
    font-size: var(--hh-s-28);
    font-weight: 650;
    margin: 0 0 var(--hh-s-8);
  }
  .stage .intro {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-24);
  }
  .role-cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
    gap: var(--hh-s-16);
    margin: 0 0 var(--hh-s-24);
  }
  .role-card {
    display: flex;
    align-items: flex-start;
    gap: var(--hh-s-12);
    min-height: 96px;
    text-align: start;
    padding: var(--hh-s-20);
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    box-shadow: var(--hh-widget-shadow);
  }
  .role-card:hover:not(:disabled) {
    border-color: var(--hh-accent-text);
  }
  .role-card:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .role-card .mark {
    color: var(--hh-text-dim);
    flex: none;
  }
  .role-card:hover:not(:disabled) .mark,
  .role-card:focus-visible .mark {
    color: var(--hh-accent-text);
  }
  .role-card .words {
    display: grid;
    gap: var(--hh-s-4);
  }
  .role-card .role {
    font-size: var(--hh-s-16);
    font-weight: 600;
  }
  .role-card .what {
    font-size: 14px;
    line-height: 1.4;
    color: var(--hh-text-dim);
    font-weight: 400;
  }

  /* -- change 11: staff register and invitation ------------------------------------- */

  .person-cell {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
  }
  .initials-disc {
    width: 32px;
    height: 32px;
    border-radius: var(--hh-radius-pill);
    background: var(--hh-row-alt);
    border: 1px solid var(--hh-control-line);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-weight: 700;
    font-size: 12px;
    color: var(--hh-text);
    flex: none;
  }
  .staff-email {
    font-size: 12px;
  }
  .staff-last-seen {
    font-size: 13px;
  }
  /* -- the register's branch values --------------------------------------------------
     UX-106 round 2: the values sit in one group, so the card row's grid has
     the label in the first column and the group in the second. A cell with
     more than one value auto placed the second one under the label, so the
     yard persona's second branch, "Julian", sat at the card's left edge and
     read as a label. Inside the group the values wrap on their own line,
     still inside the value column. */
  .staff-branches-values {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8);
    align-items: center;
  }
  .staff-branches-values > * {
    /* The values never squeeze: a pill that shrank would print its words
       outside its own boundary. */
    flex: none;
  }
  .link-out-wrap {
    margin-top: var(--hh-s-8);
  }

  /* -- the register at a phone's width ------------------------------------------------
     UX-106: the register is seven columns, and at 390 the table scrolled
     sideways under a cut column: the role select ran off the card's right
     edge, so "General Manager" and "Platform Administrator" were cut in half.
     Under 900 px each staff member is one card: the person, the role select
     under the name at the card's own width, then the branches, the access,
     the status and the last sign in as labelled pairs. The house's own
     card-row pattern (the money desk's register). */
  @media (max-width: 900px) {
    .staff-table thead {
      display: none;
    }
    .staff-table,
    .staff-table tbody {
      display: block;
    }
    .staff-table tr {
      display: grid;
      gap: var(--hh-s-8);
      padding: var(--hh-s-12) 0;
      border-bottom: 1px solid var(--hh-hairline);
    }
    .staff-table tbody td {
      display: grid;
      grid-template-columns: minmax(92px, 38%) minmax(0, 1fr);
      gap: var(--hh-s-8);
      padding: 0;
      border: 0;
      text-align: start;
      white-space: normal;
      align-items: start;
      justify-items: start;
    }
    .staff-table tbody td::before {
      content: attr(data-label);
      color: var(--hh-text-dim);
      font-size: var(--hh-s-12);
      font-weight: 700;
      letter-spacing: 0.06em;
      text-transform: uppercase;
    }
    .staff-table tbody td.staff-person {
      display: block;
    }
    .staff-table tbody td.staff-person::before {
      content: none;
    }
    /* The role select: the label above it, the select under it, the card's
       own width. The min-width is the whole fix: a select's own minimum is
       the widest option, which is what ran off the edge. */
    .staff-table tbody td.staff-role {
      grid-template-columns: none;
    }
    .staff-table tbody td.staff-role .select {
      width: 100%;
      max-width: 100%;
    }
    .staff-table tbody td.staff-actions {
      grid-template-columns: none;
    }
    .staff-table tbody td.staff-actions::before {
      content: none;
    }
    .staff-table tbody td.staff-actions .link-out-wrap {
      max-width: 100%;
      overflow-wrap: anywhere;
    }
    /* The branch values: the group takes the value column's own width, and a
       pill whose words are longer than the column breaks inside its own
       boundary instead of running off the card. */
    .staff-table tbody td.staff-branches .staff-branches-values {
      max-width: 100%;
    }
    .staff-table tbody td.staff-branches .chip {
      max-width: 100%;
      white-space: normal;
      overflow-wrap: anywhere;
    }
  }
  .auth-note {
    margin-bottom: var(--hh-s-16);
  }
  .invitation-type-toggle {
    display: flex;
    gap: var(--hh-s-8);
    margin-bottom: var(--hh-s-16);
  }
  .btn-pill {
    padding: var(--hh-s-4) var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    font-size: 13px;
    font-weight: 500;
    border: 1px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
  }
  /* The selected pill of a pair (the invitation type's own toggle): the accent
     word, the accent boundary and the inset rule, so the chosen one reads as
     chosen without a fill. UX-106: this mark used to be the pricing board's
     active pill, and the board is gone; the mark itself is shared. */
  .pill-selected {
    color: var(--hh-accent-text);
    border-color: var(--hh-accent-text);
    box-shadow: inset 0 -2px 0 var(--hh-accent-text);
    font-weight: 600;
  }
  .sign-in-method-box {
    margin-top: var(--hh-s-16);
    padding: var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    background: var(--hh-row-alt);
    border: 1px solid var(--hh-hairline);
  }
  .sign-in-method-label {
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    margin-bottom: 4px;
  }
  .sign-in-method-val {
    font-size: 14px;
    font-weight: 500;
  }
  .staff-create-actions {
    margin-top: var(--hh-s-16);
    display: flex;
    gap: var(--hh-s-8);
  }
  .badge-outline {
    display: inline-block;
    padding: 2px 8px;
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    font-size: 12px;
  }
  .badge-status {
    display: inline-block;
    padding: 2px 8px;
    border-radius: var(--hh-radius-pill);
    font-size: 12px;
    font-weight: 700;
  }
  .badge-active {
    background: color-mix(in srgb, var(--hh-success) 15%, transparent);
    color: var(--hh-success);
  }
  .badge-invited {
    background: color-mix(in srgb, var(--hh-accent-text) 15%, transparent);
    color: var(--hh-accent-text);
  }
  .badge-disabled {
    background: var(--hh-row-alt);
    color: var(--hh-text-dim);
  }

  /* -- change 11: role matrix ---------------------------------------------------- */

  .role-matrix-table th {
    text-align: center;
  }
  .role-matrix-table td {
    text-align: center;
  }
  .role-matrix-table .role-col {
    text-align: start;
  }
  .matrix-pill {
    display: inline-block;
    padding: 2px 10px;
    border-radius: var(--hh-radius-pill);
    font-size: 12px;
    font-weight: 600;
    text-align: center;
    min-width: 48px;
  }
  .matrix-cell-full {
    background: color-mix(in srgb, var(--hh-success) 15%, transparent);
    color: var(--hh-success);
  }
  .matrix-cell-view {
    background: color-mix(in srgb, var(--hh-accent-text) 15%, transparent);
    color: var(--hh-accent-text);
  }
  .matrix-cell-none {
    background: var(--hh-row-alt);
    color: var(--hh-text-dim);
  }
  .matrix-legend {
    margin-top: var(--hh-s-12);
  }

  /* -- change 11: delivery, sign in methods and warranty lists ---------------------- */

  .list-stack {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .pill-row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: var(--hh-s-12) var(--hh-s-16);
    background: var(--hh-card);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    gap: var(--hh-s-16);
  }
  .pill-name {
    font-size: 14px;
    min-width: 140px;
  }
  .pill-detail {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .pill-coverage {
    font-size: 14px;
    color: var(--hh-text-dim);
    flex: 1;
  }
  .pill-fee {
    font-size: 14px;
    font-weight: 600;
    text-align: right;
  }
  .pill-words {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }
  .pill-title {
    font-size: 14px;
  }
  .pill-desc {
    font-size: 13px;
    color: var(--hh-text-dim);
  }
  .toggle-wrap {
    display: flex;
    align-items: center;
  }
  .toggle-switch {
    width: 40px;
    height: 24px;
    border-radius: 12px;
    background: var(--hh-control-line);
    position: relative;
    cursor: pointer;
  }
  .toggle-switch-on {
    background: var(--hh-accent);
  }
  .toggle-knob {
    position: absolute;
    top: 2px;
    left: 2px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: var(--hh-card);
    box-shadow: var(--hh-widget-shadow);
  }
  .toggle-switch-on .toggle-knob {
    transform: translateX(16px);
  }
  .segmented-control {
    display: inline-flex;
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-pill);
    overflow: hidden;
    background: var(--hh-row-alt);
  }
  .seg-btn {
    padding: 4px 12px;
    border: none;
    background: transparent;
    font-size: 13px;
    color: var(--hh-text-dim);
    cursor: pointer;
    border-radius: 0;
    min-height: 28px;
  }
  .seg-btn:hover {
    color: var(--hh-text);
  }
  .seg-btn-active {
    background: var(--hh-card);
    color: var(--hh-text);
    font-weight: 600;
    box-shadow: var(--hh-widget-shadow);
  }
  .threshold-form {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .input-action-row {
    display: flex;
    gap: var(--hh-s-8);
    max-width: 320px;
    align-items: center;
  }
  .card-actions {
    display: flex;
    gap: var(--hh-s-8);
    margin-top: var(--hh-s-16);
  }
`;
