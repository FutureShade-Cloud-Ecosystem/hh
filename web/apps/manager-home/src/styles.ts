// Manager Home's sheet. Token-only: no literal colours anywhere; light and
// brand (the dark half) both come from the design system's tokens, and the
// header's theme control flips the document root. Every ink is named: the
// host sets the text colour, and every control that the browser would paint
// in its own button ink sets its own, because an unset ink is black, and
// black on the brand theme's navy is 1.42 to 1 (the round 1 review's P1-1).
//
// THE LANDING'S PLACEMENT. The large tiles (size L) make the board's top
// block on a twelve column grid: the hero and the pick queue stack on the
// left, the needs-you card spans both rows on the right. Every other tile sits
// on one strip below it, the board's row of five, the bars tile twice the
// width of the figure tiles. The server sends the widgets, their order, shapes
// and sizes; this sheet only places them.
import { css } from "lit";

export const homeStyles = css`
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

  .chev {
    margin-inline-start: var(--hh-s-8);
  }
  .back .chev {
    margin-inline: 0 var(--hh-s-8);
  }

  /* -- the page's own head -------------------------------------------------------------- */

  .page-head {
    margin: 0 0 var(--hh-s-24);
  }
  .page-head h1,
  .view-head h1,
  .headline {
    font-size: var(--hh-s-28);
    font-weight: 650;
    line-height: 1.15;
  }
  /* The one edit affordance on the landing: it opens the arrange view. */
  .head-actions {
    display: flex;
    margin-top: var(--hh-s-12);
  }
  .edit-open {
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    font-size: 14px;
    border-radius: var(--hh-radius-pill);
  }
  .page-sub {
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
    margin-top: var(--hh-s-4);
  }
  .eyebrow,
  .nav-label {
    font-size: var(--hh-s-12);
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .eyebrow {
    margin: 0 0 var(--hh-s-8);
  }

  .view-head {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--hh-s-8);
    margin: 0 0 var(--hh-s-24);
  }
  .view-head .where {
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
  }
  .view-head .back {
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    font-size: 14px;
    border-radius: var(--hh-radius-pill);
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

  /* -- the way in ------------------------------------------------------------------------ */

  .stage {
    max-width: 680px;
    margin: 0 auto;
    padding: var(--hh-s-40) 0 var(--hh-s-64);
  }
  .stage .headline {
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

  /* -- the landing's grid ----------------------------------------------------------------- */

  .bento {
    display: grid;
    grid-template-columns: repeat(12, minmax(0, 1fr));
    gap: var(--hh-s-16);
    margin: 0 0 var(--hh-s-16);
  }
  .cell {
    display: flex;
    min-width: 0;
  }
  .cell.half {
    grid-column: span 6;
  }
  .cell.full {
    grid-column: span 12;
  }
  .cell.tall {
    grid-row: span 2;
  }
  .cell > .card,
  .tile > .card {
    flex: 1;
    min-width: 0;
  }
  @media (max-width: 900px) {
    .cell.half {
      grid-column: span 12;
    }
    .cell.tall {
      grid-row: auto;
    }
  }

  .strip {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-16);
  }
  .tile {
    display: flex;
    flex: 1 1 150px;
    min-width: 0;
  }
  .tile.wide {
    flex: 2 1 300px;
  }

  .card {
    position: relative;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    padding: var(--hh-s-20);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    color: var(--hh-text);
    box-shadow: var(--hh-widget-shadow);
    overflow: hidden;
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
  .tile-foot {
    margin-top: auto;
    padding-top: var(--hh-s-8);
    font-size: 14px;
    color: var(--hh-text-dim);
  }

  .figure {
    font-size: var(--hh-s-40);
    font-weight: 650;
    line-height: 1.05;
  }
  .figure.small {
    font-size: var(--hh-s-32);
  }
  .figure.none {
    font-size: var(--hh-s-24);
    color: var(--hh-text-dim);
  }

  /* The hero: the profile's first large stat, the day going out. Accent fill, the figure at
     the top, the window word under it, its foot at the bottom. */
  .card.hero {
    background: var(--brand-fill);
    border-color: var(--brand-fill);
    color: var(--brand-fill-on);
    min-height: 170px;
  }
  .card.hero .card-title,
  .card.hero .card-sub,
  .card.hero .tile-foot,
  .card.hero .figure {
    color: var(--brand-fill-on);
  }

  /* -- the needs-you queue -------------------------------------------------------------- */

  .needs-row {
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    padding: var(--hh-s-12) var(--hh-s-12) var(--hh-s-12) var(--hh-s-16);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-control);
    background: var(--hh-widget-raised);
    overflow: hidden;
  }
  .sev {
    flex: none;
    align-self: stretch;
    width: 4px;
    margin: calc(var(--hh-s-12) * -1) 0 calc(var(--hh-s-12) * -1) calc(var(--hh-s-16) * -1);
    background: var(--hh-text-dim);
  }
  .sev.good {
    background: var(--hh-state-good);
  }
  .sev.watch {
    background: var(--hh-state-watch);
  }
  .sev.stop {
    background: var(--hh-state-stop);
  }
  .needs-body {
    flex: 1;
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
  }
  .needs-body .what {
    font-size: var(--hh-s-16);
    font-weight: 650;
    color: var(--hh-text);
  }
  .needs-body .view-words,
  .needs-body .words,
  .needs-meta {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .needs-count {
    font-size: 14px;
    font-weight: 650;
    color: var(--hh-text);
  }
  .row-chip,
  .view-link {
    display: inline-flex;
    align-items: center;
    flex: none;
    min-height: 36px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .row-chip:hover,
  .view-link:hover {
    border-color: var(--hh-accent-text);
  }
  .not-here {
    flex: none;
    max-width: 7.5em;
    text-align: end;
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .needs-empty {
    color: var(--hh-text-dim);
  }
  .needs-row.unsourced {
    border-style: dashed;
  }
  .needs-foot {
    margin-top: auto;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    padding-top: var(--hh-s-8);
  }
  .needs-total {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .queue-link {
    min-height: 36px;
    padding: 0;
    border: none;
    background: transparent;
    color: var(--hh-accent-text);
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
  }

  /* -- bars ----------------------------------------------------------------------------- */

  .bars {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    margin: 0;
    padding: 0;
    list-style: none;
  }
  .bar-row {
    display: grid;
    grid-template-columns: minmax(5em, 1fr) 3fr auto;
    align-items: center;
    column-gap: var(--hh-s-16);
    row-gap: var(--hh-s-2);
  }
  .bar-row .label {
    font-size: 14px;
    color: var(--hh-text);
  }
  .track {
    height: 8px;
    border-radius: 4px;
    background: var(--hh-track);
    overflow: hidden;
  }
  .fill {
    display: block;
    height: 100%;
    border-radius: 4px;
    background: var(--hh-success-fill);
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
    font-size: 14px;
    font-weight: 650;
    color: var(--hh-text);
    white-space: nowrap;
  }
  .bar-row .share {
    grid-column: 2 / 4;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }

  /* -- the views' links ------------------------------------------------------------------ */

  /* The landing's own way into its three views: small chips with chevrons. The needs rows link
     into them when their counts are live, but a manager whose queue is empty still reaches the
     approvals surface: the links are the floor, the rows are the highlight. */
  .view-links {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    align-items: center;
    margin: var(--hh-s-24) 0 0;
  }
  .view-link {
    background: var(--hh-card);
  }

  /* -- the views' lists ----------------------------------------------------------------- */

  .sheet {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-16);
  }
  .task-card,
  .list-card {
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
    box-shadow: var(--hh-widget-shadow);
    padding: var(--hh-s-20);
  }
  .task-head {
    display: flex;
    gap: var(--hh-s-8) var(--hh-s-16);
    align-items: baseline;
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-8);
  }
  .task-head .order {
    font-size: var(--hh-s-20);
    font-weight: 650;
  }
  .task-head .meta {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .shortage {
    display: flex;
    gap: var(--hh-s-16);
    align-items: center;
    justify-content: space-between;
    flex-wrap: wrap;
    padding: var(--hh-s-16) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .shortage .body {
    flex: 1;
    min-width: 12em;
  }
  .shortage .line {
    font-size: var(--hh-s-16);
    font-weight: 600;
  }
  .shortage .detail {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin-top: var(--hh-s-4);
  }
  .resolved-word {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin-top: var(--hh-s-4);
  }

  .def-note {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-16);
    max-width: 62ch;
  }
  .who-may {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: var(--hh-s-16) 0 0;
    max-width: 62ch;
  }
  .refusal {
    margin: 0 0 var(--hh-s-16);
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 1px solid var(--hh-danger);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    color: var(--hh-danger);
    font-size: var(--hh-s-16);
    max-width: 62ch;
  }

  /* -- the resolve panel ------------------------------------------------------------------ */

  .shortage.resolve-panel {
    display: block;
    margin: var(--hh-s-8) 0;
    padding: var(--hh-s-20);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-widget-raised);
  }
  .resolve-panel .body {
    min-width: 0;
  }
  .panel-title {
    font-size: var(--hh-s-20);
    font-weight: 650;
    margin-bottom: var(--hh-s-12);
  }
  .panel-line {
    font-size: var(--hh-s-16);
    font-weight: 650;
  }
  .panel-qty {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin-top: var(--hh-s-4);
  }
  .reported {
    margin: var(--hh-s-16) 0;
    padding: var(--hh-s-12) var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    background: var(--hh-row-alt);
  }
  .reported-who {
    font-size: var(--hh-s-12);
    font-weight: 600;
    color: var(--hh-text-dim);
  }
  .reported-note {
    font-size: var(--hh-s-16);
    margin-top: var(--hh-s-4);
  }
  .choices {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
    margin: 0 0 var(--hh-s-16);
  }
  .choice {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    width: 100%;
    text-align: start;
    min-height: 64px;
    padding: var(--hh-s-12) var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: var(--hh-s-16);
    cursor: pointer;
  }
  .choice .mark {
    flex: none;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    border: 2px solid var(--hh-control-line);
    background: transparent;
  }
  .choice[aria-pressed="true"] {
    border-color: var(--hh-accent-text);
    background: var(--brand-tint);
  }
  .choice[aria-pressed="true"] .mark {
    border-color: var(--hh-accent-text);
    background: var(--hh-accent-text);
    box-shadow: inset 0 0 0 3px var(--brand-tint);
  }
  .choice .words {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-2);
  }
  .choice .title {
    font-weight: 600;
    color: var(--hh-text);
  }
  .choice .sub {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
  .hm-note {
    margin-top: var(--hh-s-16);
    padding: var(--hh-s-12) var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    background: var(--brand-tint);
    color: var(--hh-text);
    font-size: 14px;
  }

  .text-field {
    font-family: inherit;
    font-size: var(--hh-s-16);
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-field);
    color: var(--hh-text);
    min-height: 44px;
  }
  .text-area {
    width: 100%;
    box-sizing: border-box;
    min-height: 88px;
    resize: vertical;
    margin: 0 0 var(--hh-s-16);
  }

  .row-actions {
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }

  /* -- receiving ------------------------------------------------------------------------- */

  .facts {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8) var(--hh-s-24);
  }
  .facts .detail {
    font-size: var(--hh-s-16);
    color: var(--hh-text);
  }

  /* -- approvals --------------------------------------------------------------------------- */

  .kind-tabs {
    display: flex;
    gap: var(--hh-s-8);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-16);
  }
  .kind-tab {
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .kind-tab[aria-selected="true"] {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    font-weight: 600;
  }
  .approval {
    padding: var(--hh-s-16) 0;
    border-top: 1px solid var(--hh-hairline);
  }
  .approval:first-child {
    border-top: none;
    padding-top: 0;
  }
  .approval .detail {
    font-size: var(--hh-s-16);
    font-weight: 600;
  }
  .approval .meta {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: var(--hh-s-4) 0 var(--hh-s-12);
  }
  .amount {
    font-weight: 650;
  }

  .count-line {
    font-size: 14px;
    color: var(--hh-text-dim);
  }
`;
