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
    white-space: nowrap;
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
  /* The neutral chip: the house's own placeholder pill (the design system
     carries the same class). UX-106 round 3 wears it on a thing the screen
     cannot name: an unknown branch. It says what it does not know, and it
     wears no other colour's meaning.
     UX-106 round 4 (K3 P3): the border takes the design system's own line
     token, --hh-line, the way the design system's chip-neutral wears it
     (design-system/src/primitives.ts). It wore --hh-control-line, which is a
     different token with a different value in both themes, so the claim that
     the two matched was not exact. */
  .chip-neutral {
    color: var(--hh-text-dim);
    border-color: var(--hh-line);
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
    white-space: nowrap;
  }
  tbody tr:last-child td,
  tbody tr:last-child th {
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

  /* -- the card row: a table that cannot fit a phone ------------------------------
     UX-106 round 6: the sweep measured every table on every view the round
     touches, at 390, in light and in charcoal, and found the ones that
     scrolled sideways under a cut column. They all take one pattern: under
     900 px the table's head is gone and each row lays out as a grid of
     labelled pairs, and each cell names itself through the data-label its
     own CSS reads. The house's own card-row pattern, the one the margin
     policy table, the staff register and the audit list already take. The
     desk widths keep the table.

     The label column is the pattern's one number, and it is a table's own:
     --cardrow-label-col sets it, and the default is the narrow one the
     audit list needed, so a long value keeps room to stay whole. */
  @media (max-width: 900px) {
    .cardrow-table thead {
      display: none;
    }
    .cardrow-table,
    .cardrow-table tbody {
      display: block;
    }
    .cardrow-table tr {
      display: grid;
      gap: var(--hh-s-8);
      padding: var(--hh-s-12) 0;
      border-bottom: 1px solid var(--hh-hairline);
    }
    .cardrow-table tbody td {
      display: grid;
      grid-template-columns: var(--cardrow-label-col, minmax(76px, 24%)) minmax(0, 1fr);
      gap: var(--hh-s-8);
      padding: 0;
      border: 0;
      text-align: start;
      white-space: normal;
      align-items: start;
      justify-items: start;
      overflow-wrap: break-word;
    }
    .cardrow-table tbody td::before {
      content: attr(data-label);
      color: var(--hh-text-dim);
      font-size: var(--hh-s-12);
      font-weight: 700;
      letter-spacing: 0.06em;
      text-transform: uppercase;
      grid-column: 1;
      grid-row: 1;
    }
    /* The value never runs off the card: a value too long for its column
       breaks inside its own boundary. */
    .cardrow-table tbody td > * {
      min-width: 0;
      max-width: 100%;
    }
    /* Every part of the value reads in the value column. A cell that holds
       two things (a name and its code, a scope and its detail) would
       otherwise put the second of them in the label column of the next row. */
    .cardrow-table tbody td:not(.cardrow-act) > * {
      grid-column: 2;
    }
    /* A chip is a pill: it reads on one line. UX-106 round 7: the clearance
       chip broke before its last word and took two lines, because this rule
       let a chip's text wrap. The chip takes the value column's own width,
       and the sweep reads every chip's text against the column it sits in,
       so a chip too wide for its column fails the sweep instead of breaking
       inside the pill. */
    .cardrow-table tbody td .chip {
      max-width: 100%;
      white-space: nowrap;
    }
    /* An action cell has no label of its own: its control reads on its own. */
    .cardrow-table tbody td.cardrow-act {
      grid-template-columns: none;
      justify-items: start;
    }
    .cardrow-table tbody td.cardrow-act::before {
      content: none;
    }
    /* The row's own head cell is the row's title: the role, the job shape, the
       record's name. It reads as a name, not as an uppercase column head. */
    .cardrow-table tbody th[scope="row"] {
      display: block;
      font-size: var(--hh-s-16);
      font-weight: 650;
      text-transform: none;
      letter-spacing: normal;
      text-align: start;
      color: var(--hh-text);
    }
    /* One table's labels are wider than the pattern's default column: the
       promotions table's own widest word is PROMOTION, which is 81 px. The
       label column is a table's own number, so that table sets its own.
       UX-106 round 7: the low margin table's own widest label is CLEARED BY,
       which is 82 px, and it sets the same wider column, so each of its
       labels reads on one line beside its value. */
    .cardrow-table.cardrow-wide {
      --cardrow-label-col: minmax(92px, 29%);
    }
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

  /* The trail's entity: the type in words on its own line, and the record's
     handle on the next one, under the caption that names it (UX-106 round 4).
     A handle with no caption reads as a code token that fell out of a
     formatter, and the type and the handle glued together read as one
     string. */
  .audit-entity {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    align-items: flex-start;
  }
  .audit-entity-type {
    white-space: nowrap;
  }
  .audit-entity-id {
    display: inline-flex;
    align-items: baseline;
    gap: var(--hh-s-8);
  }
  .audit-entity-id .k {
    color: var(--hh-text-dim);
  }

  /* -- the audit trail fits a phone ------------------------------------------------
     UX-106 round 5: the list is five columns and it scrolled sideways under a
     cut column at 390, so the Entity column ran off the card's right edge and
     a dealer read half a handle and no entity type at all. The one entity's
     trail, on the same page, cut its Actor column the same way. Under 900 px
     each entry is a card row: the house's own card-row pattern, the one the
     margin policy table and the staff register take. Each cell names itself,
     so the row reads as labelled pairs with no sideways scroll of a cut
     column. The desk widths keep the table. */
  @media (max-width: 900px) {
    .audit-table thead {
      display: none;
    }
    .audit-table,
    .audit-table tbody {
      display: block;
    }
    .audit-table tr {
      display: grid;
      gap: var(--hh-s-8);
      padding: var(--hh-s-12) 0;
      border-bottom: 1px solid var(--hh-hairline);
    }
    .audit-table tbody td {
      display: grid;
      grid-template-columns: minmax(76px, 24%) minmax(0, 1fr);
      gap: var(--hh-s-8);
      padding: 0;
      border: 0;
      text-align: start;
      white-space: normal;
      align-items: start;
      justify-items: start;
    }
    .audit-table tbody td::before {
      content: attr(data-label);
      color: var(--hh-text-dim);
      font-size: var(--hh-s-12);
      font-weight: 700;
      letter-spacing: 0.06em;
      text-transform: uppercase;
    }
    /* The action's chip takes the value column's own width: the longest action
       the trail carries ("ORDER_STATUS_CHANGED") needs 201 of the 232 px the
       value column gives it, and a chip is a pill: it reads on one line. */
    .audit-table tbody td .chip {
      max-width: 100%;
      white-space: nowrap;
    }
    /* The entity's type and its captioned handle: the handle keeps its own
       caption and breaks inside the value column, at the id's own dashes,
       rather than running off the card. */
    .audit-table tbody td.audit-entity-cell .audit-entity {
      min-width: 0;
      max-width: 100%;
    }
    .audit-table tbody td.audit-entity-cell .audit-entity-id {
      min-width: 0;
      max-width: 100%;
    }
    .audit-table tbody td.audit-entity-cell .audit-entity-id .mono {
      min-width: 0;
      overflow-wrap: break-word;
    }
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
  /* UX-106 round 6: at a phone's width one pair is one row. The desk rule's
     own minimum (a 120 px label and a 240 px value) is wider than the card
     a phone gives the drawer, so the pairs ran off the card's right edge. */
  @media (max-width: 900px) {
    .detail .kv {
      grid-template-columns: minmax(76px, 24%) minmax(0, 1fr);
    }
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

  .cell-btn {
    white-space: nowrap;
  }
  tbody tr th[scope="row"] {
    font-size: 14px;
    font-weight: 400;
    text-transform: none;
    letter-spacing: normal;
    white-space: normal;
    color: var(--hh-text);
  }

  /* UX-118: at desk widths (> 900px) each job shape row in the curation grid
     lays out its name and description across the top and its ten surfaces in a
     five-column grid below, so all ten surfaces fit the card without sideways
     scroll or 6-line descriptions. */
  @media (min-width: 901px) {
    .config-grid-table thead {
      display: none;
    }
    .config-grid-table,
    .config-grid-table tbody {
      display: block;
    }
    .config-grid-table tbody tr {
      display: grid;
      grid-template-columns: repeat(5, minmax(0, 1fr));
      gap: var(--hh-s-8);
      padding: var(--hh-s-16) 0;
      border-bottom: 1px solid var(--hh-hairline);
    }
    .config-grid-table tbody tr:last-child {
      border-bottom: none;
    }
    .config-grid-table tbody th[scope="row"] {
      grid-column: 1 / -1;
      display: flex;
      align-items: baseline;
      gap: var(--hh-s-12);
      flex-wrap: wrap;
      padding: 0 0 var(--hh-s-4);
      border: 0;
      font-size: var(--hh-s-16);
    }
    .config-grid-table tbody th[scope="row"] .dim {
      font-size: 14px;
      font-weight: 400;
    }
    .config-grid-table tbody td {
      display: flex;
      flex-direction: column;
      align-items: flex-start;
      gap: var(--hh-s-4);
      padding: var(--hh-s-8) var(--hh-s-12);
      border: 1px solid var(--hh-hairline);
      border-radius: var(--hh-radius-control);
      background: var(--hh-row-alt);
      min-width: 0;
    }
    .config-grid-table tbody td::before {
      content: attr(data-label);
      color: var(--hh-text-dim);
      font-size: var(--hh-s-12);
      font-weight: 600;
      letter-spacing: 0.06em;
      text-transform: uppercase;
      white-space: nowrap;
    }
    .config-grid-table tbody td .cell-btn {
      max-width: 100%;
    }
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
    white-space: nowrap;
  }
  .staff-last-seen {
    font-size: 13px;
    white-space: nowrap;
  }
  @media (min-width: 901px) {
    .staff-table th,
    .staff-table td {
      padding: var(--hh-s-8) var(--hh-s-4);
    }
    .staff-table .person-cell {
      gap: var(--hh-s-8);
    }
    .staff-table td.staff-role .select {
      width: 100%;
      max-width: 168px;
      min-width: 0;
      box-sizing: border-box;
    }
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
       pill reads on one line, the way every chip in a card row does. */
    .staff-table tbody td.staff-branches .staff-branches-values {
      max-width: 100%;
    }
    .staff-table tbody td.staff-branches .chip {
      max-width: 100%;
      white-space: nowrap;
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
    white-space: nowrap;
  }
  .badge-status {
    display: inline-block;
    padding: 2px 8px;
    border-radius: var(--hh-radius-pill);
    font-size: 12px;
    font-weight: 700;
    white-space: nowrap;
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
    white-space: nowrap;
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
