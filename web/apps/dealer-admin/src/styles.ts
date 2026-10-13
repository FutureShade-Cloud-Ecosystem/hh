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

  .surface-list {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .surface-link {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
    width: 100%;
    text-align: start;
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 1px solid transparent;
    border-radius: var(--hh-radius-control);
    background: transparent;
    color: var(--hh-text);
    cursor: pointer;
  }
  .surface-link:hover {
    background: var(--hh-row-alt);
  }
  .surface-link[aria-current="true"] {
    border-color: var(--hh-hairline);
    background: var(--hh-card);
  }
  .surface-link .title {
    font-size: var(--hh-s-16);
    font-weight: 650;
  }
  .surface-link[aria-current="true"] .title {
    color: var(--hh-accent-text);
  }
  .surface-link .blurb {
    font-size: 14px;
    color: var(--hh-text-dim);
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
`;
