// The receivables slice's own styles. Every value is a token of the design
// system; nothing here is a literal colour. Status is a word before it is a
// colour, figures sit in tabular columns and line up on the right, and a wide
// table scrolls inside its own panel so the page never scrolls sideways. A held
// control keeps the grey fill and ink of a disabled one, never a fade.
import { css } from "lit";

const receivablesStyles = css`
  .desk { font-size: 14px; max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  h1 { margin: 0; font-size: 24px; line-height: 1.2; font-weight: 700; }
  h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  h3 { margin: 0; font-size: 12px; letter-spacing: 0.1em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  p { margin: 0 0 var(--hh-s-8); }
  .dim { color: var(--hh-text-dim); }
  .small { font-size: 13px; }
  .head { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: var(--hh-s-12) var(--hh-s-16); margin-bottom: var(--hh-s-16); }
  .head p { margin: var(--hh-s-4) 0 0; }
  .meta { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-4) var(--hh-s-8); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; }
  .btn { white-space: nowrap; min-height: 36px; padding: 0 var(--hh-s-16); font-size: 14px; }
  .small-btn { min-height: 32px; padding: 0 var(--hh-s-12); font-size: 13px; }
  .tile p.dim { font-size: 13px; }
  td:first-child .who { white-space: nowrap; }
  .tab { font-size: 14px; }
  .nowrap { white-space: nowrap; }
  .gap { margin-top: var(--hh-s-16); }
  td.placed { min-width: 150px; }
  .ro { display: block; min-height: 44px; padding: var(--hh-s-12); box-sizing: border-box; border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); color: var(--hh-text); font-size: 14px; font-weight: 400; overflow-wrap: anywhere; line-height: 1.3; }
  .pick { width: 22px; height: 22px; min-height: 0; padding: 0; border-radius: 6px; border: 2px solid var(--hh-control-line); background: transparent; cursor: pointer; }
  .pick[aria-checked="true"] { background: var(--hh-accent); border-color: var(--hh-accent-ink); color: var(--hh-accent-ink); display: inline-flex; align-items: center; justify-content: center; font-size: 14px; font-weight: 700; line-height: 1; }
  .pick[aria-checked="true"]::after { content: "\\2713"; }
  .applied-cell { color: var(--hh-text); font-weight: 700; }
  .dimrow td { color: var(--hh-text-dim); }
  .foot-row { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); margin-top: var(--hh-s-12); }
  .foot-row p { margin: 0; }
  .hint { color: var(--hh-state-stop); font-weight: 600; font-size: 13px; }
  .callout { background: color-mix(in srgb, var(--hh-state-watch) 14%, var(--hh-card)); border: 1px solid var(--hh-state-watch); border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: var(--hh-s-12) 0; }
  .ghost { height: 44px; margin-top: var(--hh-s-12); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); }
  .held-form { border: 0; padding: 0; margin: var(--hh-s-12) 0; display: grid; gap: var(--hh-s-12); min-width: 0; }
  .blocks { margin: 0; padding-inline-start: var(--hh-s-20); color: var(--hh-text-dim); }
  .seg { flex-wrap: wrap; border-radius: var(--hh-radius-control); }
  .head p, .head .dim { max-width: 70ch; }
  .facts { font-size: 14px; }
  .panel .actions { margin-top: var(--hh-s-12); }
  label.gap { margin-top: var(--hh-s-16); }
  .seg { max-width: 100%; overflow-x: auto; }
  td:nth-child(4) .small { white-space: normal; }
  .link { min-height: 0; background: none; border: 0; padding: 0; color: var(--hh-accent-text); font-weight: 700; font-size: 14px; cursor: pointer; }
  .link:hover { text-decoration: underline; }
  .back { margin-bottom: var(--hh-s-8); }
  .link-row { min-height: 0; background: none; border: 0; padding: 0; text-align: start; cursor: pointer; font: inherit; color: inherit; }
  .link-row:hover .who { text-decoration: underline; }
  .held, .held:hover { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); cursor: default; }

  .alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; }
  .alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }
  .note { background: var(--hh-row-alt); border-inline-start: 4px solid var(--hh-state-watch); border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: var(--hh-s-12) 0; }
  .ok-note { background: color-mix(in srgb, var(--hh-state-good) 16%, var(--hh-card)); border: 1px solid var(--hh-state-good); border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin-top: var(--hh-s-16); }

  /* Tabs: a rounded strip, the open one raised. */
  .tabs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-4); margin: 0 0 var(--hh-s-16); }
  .tab { min-height: 40px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text-dim); border: 1px solid transparent; padding: 0 var(--hh-s-20); display: inline-flex; align-items: center; gap: var(--hh-s-8); font-size: 15px; }
  .tab[aria-current="true"] { background: var(--hh-card); color: var(--hh-text); border-color: var(--hh-control-line); font-weight: 700; }
  .btn .later { color: inherit; border-color: currentColor; }
  .tab-later { color: var(--hh-text-dim); cursor: default; }
  .later { display: inline-block; border: 1px dashed var(--hh-control-line); border-radius: var(--hh-radius-pill); padding: 0 var(--hh-s-8); font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); }

  .cols { display: grid; grid-template-columns: minmax(0, 1fr) 460px; gap: var(--hh-s-16); align-items: start; }
  .col-main, .col-side { min-width: 0; }
  .panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); margin-bottom: var(--hh-s-16); min-width: 0; box-shadow: var(--hh-elev-1); }
  .panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .tools { max-width: 100%; display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); }
  .foot { margin-top: var(--hh-s-12); }

  /* The four aging tiles. */
  .tiles { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--hh-s-16); margin-bottom: var(--hh-s-16); }
  .tile { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); box-shadow: var(--hh-elev-1); min-width: 0; }
  .tile p { margin: var(--hh-s-4) 0; }
  .tile-figure { font-size: 26px; font-weight: 700; font-variant-numeric: tabular-nums; }
  .tile-escalation { color: var(--hh-state-stop); }
  .tile-late { color: var(--hh-state-watch); }

  /* Pills and segmented controls. */
  .marks { display: inline-flex; flex-wrap: wrap; gap: var(--hh-s-4); }
  .pill { display: inline-flex; align-items: center; padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-control-line); white-space: nowrap; }
  .pill-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .pill-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .pill-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .pill-info { color: var(--hh-info); border-color: var(--hh-info); }
  .pill-plain { color: var(--hh-text); }
  .pill-dashed { border-style: dashed; }
  .seg { display: inline-flex; border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-pill); padding: 2px; gap: 2px; }
  .seg-item { min-height: 32px; padding: 0 var(--hh-s-12); border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text-dim); border: 1px solid transparent; font-size: 14px; }
  .seg-item[aria-pressed="true"] { background: var(--hh-card); color: var(--hh-text); border-color: var(--hh-text-dim); font-weight: 700; }

  /* Tables. */
  .scroll { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 14px; }
  th { text-align: start; font-weight: 700; font-size: 11px; letter-spacing: 0.1em; text-transform: uppercase; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  tbody tr:last-child td { border-bottom: 0; }
  th.num, td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td.act { text-align: end; }
  td.late { color: var(--hh-state-stop); font-weight: 700; }
  td > span.small, td > .small { display: block; }
  tr[aria-current="true"] td { background: var(--hh-row-alt); }
  /* The selected row's ground is the row stripe, and the stop mark's colour does not hold AA text on
     it in brand (the visual review measured 4.01). The sheet's danger ink is the red tuned to be READ
     as text in each half, and it holds on the stripe in both, so the selected row's late cells and
     stop pills read in it; the unselected rows keep the state colour the marks share everywhere. */
  tr[aria-current="true"] td.late,
  tr[aria-current="true"] td .pill-stop { color: var(--hh-danger); }
  tfoot td { border-top: 1px solid var(--hh-text); border-bottom: 0; }
  .who { display: block; font-weight: 700; }
  .empty { padding: var(--hh-s-24) var(--hh-s-8); display: grid; gap: var(--hh-s-8); justify-items: start; }
  .empty strong { font-size: 18px; }
  .holdbtns { display: inline-grid; gap: var(--hh-s-8); }

  /* Needs you. */
  .needlist { list-style: none; margin: var(--hh-s-12) 0; padding: 0; display: grid; gap: var(--hh-s-8); }
  .need { display: flex; align-items: center; gap: var(--hh-s-8); border: 1px solid var(--hh-line); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); padding: var(--hh-s-8) var(--hh-s-12); }
  .need-stop { border-inline-start-color: var(--hh-state-stop); }
  .need-watch { border-inline-start-color: var(--hh-state-watch); }
  .need-info { border-inline-start-color: var(--hh-info); }
  .need-body { flex: 1; min-width: 0; display: grid; gap: var(--hh-s-2); text-align: start; background: none; border: 0; padding: var(--hh-s-4) 0; cursor: pointer; font: inherit; color: inherit; min-height: 0; }
  .need-body:hover strong { text-decoration: underline; }

  /* Working a bounced payment, lifting a hold. */
  .facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-16); margin: 0 0 var(--hh-s-12); }
  .facts dt { color: var(--hh-text-dim); }
  .facts dd { margin: 0; font-weight: 600; overflow-wrap: anywhere; }
  dl { margin: 0; }
  .choices { display: grid; gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .choice { display: flex; align-items: flex-start; gap: var(--hh-s-12); text-align: start; width: 100%; min-height: 0; padding: var(--hh-s-12); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); cursor: pointer; line-height: 1.35; }
  .choice span span { display: block; font-weight: 400; }
  .choice[aria-checked="true"] { background: var(--hh-row-alt); border-color: var(--hh-state-watch); }
  .radio { flex: none; width: 16px; height: 16px; margin-top: 3px; border-radius: var(--hh-radius-pill); border: 2px solid var(--hh-control-line); }
  .choice[aria-checked="true"] .radio { border-color: var(--hh-text); background: radial-gradient(circle, var(--hh-text) 40%, transparent 45%); }

  /* Forms. */
  form { display: grid; gap: var(--hh-s-12); margin: 0; }
  label { display: grid; gap: var(--hh-s-4); font-size: 13px; color: var(--hh-text-dim); font-weight: 700; }
  .label { margin: var(--hh-s-8) 0 var(--hh-s-4); font-size: 13px; color: var(--hh-text-dim); font-weight: 700; }
  input, select, textarea { font: inherit; font-size: 15px; font-weight: 400; min-height: 44px; padding: 0 var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  textarea { padding: var(--hh-s-8) var(--hh-s-12); }
  input:disabled { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); }
  
  input.amount, input.apply { text-align: end; font-variant-numeric: tabular-nums; }
  input.apply { min-height: 36px; width: 110px; }
  input:focus-visible, select:focus-visible, textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .two { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-12); margin-bottom: var(--hh-s-8); }
  .inline-form { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: var(--hh-s-8); align-items: end; }
  .alloc { list-style: none; margin: var(--hh-s-8) 0; padding: 0; }
  .alloc li { display: flex; flex-wrap: wrap; justify-content: space-between; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-8) 0; border-bottom: 1px solid var(--hh-line); font-variant-numeric: tabular-nums; }
  .applied { font-size: 15px; display: flex; justify-content: space-between; margin: var(--hh-s-8) 0 var(--hh-s-12); padding-top: var(--hh-s-8); border-top: 2px solid var(--hh-text); font-size: 16px; font-variant-numeric: tabular-nums; }

  /* The statement document. */
  .toolbar { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin-bottom: var(--hh-s-16); }
  .doc { max-width: 880px; padding: var(--hh-s-24); }
  .doc-head { display: flex; justify-content: space-between; align-items: baseline; border-bottom: 2px solid var(--hh-text); padding-bottom: var(--hh-s-8); margin-bottom: var(--hh-s-12); }
  .strip { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: var(--hh-s-8); background: var(--hh-row-alt); border-radius: var(--hh-radius-control); padding: var(--hh-s-12); margin: var(--hh-s-16) 0; text-align: center; }
  .strip dt { font-size: 11px; letter-spacing: 0.1em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .strip dd { margin: var(--hh-s-4) 0 0; font-weight: 700; font-variant-numeric: tabular-nums; }
  .overdue-note { background: var(--hh-row-alt); border-inline-start: 4px solid var(--hh-state-stop); border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); }

  @media (max-width: 1100px) {
    .cols { grid-template-columns: minmax(0, 1fr); }
    .tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (max-width: 520px) {
    table.stack thead { display: none; }
    table.stack, table.stack tbody { display: block; }
    table.stack tr { display: grid; gap: var(--hh-s-4) var(--hh-s-12); padding: var(--hh-s-12) 0; border-bottom: 1px solid var(--hh-line); }
    table.stack tbody td { display: block; padding: 0; border: 0; }
    table.stack tr[aria-current="true"] { background: var(--hh-row-alt); }
    table.stack tr[aria-current="true"] td { background: none; }
    table.stack tfoot { display: table; width: 100%; }
    table.stack tfoot td:empty { display: none; }
    table.reg tr { grid-template-columns: minmax(0, 1fr) auto; grid-template-areas: "inv bal" "acct acct" "due due" "marks marks" "act act"; }
    table.reg tbody td:nth-child(1) { grid-area: inv; }
    table.reg tbody td:nth-child(2) { grid-area: acct; }
    table.reg tbody td:nth-child(3) { display: none; }
    table.reg tbody td:nth-child(4) { grid-area: due; }
    table.reg tbody td:nth-child(5) { grid-area: marks; }
    table.reg tbody td:nth-child(6) { grid-area: bal; }
    table.reg tbody td:nth-child(7) { grid-area: act; text-align: start; }
    table.inv tr { grid-template-columns: minmax(0, 1fr) auto auto; grid-template-areas: "inv bal pick" "due due due" "marks marks marks" "applied applied applied"; }
    table.inv tbody td:nth-child(1) { grid-area: inv; }
    table.inv tbody td:nth-child(2), table.inv tbody td:nth-child(5) { display: none; }
    table.inv tbody td:nth-child(3) { grid-area: due; }
    table.inv tbody td:nth-child(4) { grid-area: marks; }
    table.inv tbody td:nth-child(6) { grid-area: bal; }
    table.inv tbody td:nth-child(7) { grid-area: pick; }
    table.inv tbody td:nth-child(8) { grid-area: applied; text-align: start; }
    table.inv tbody td.applied-cell:empty { display: none; }
    table.stmt tr { grid-template-columns: minmax(0, 1fr) auto; grid-template-areas: "acct bal" "last last" "act act"; }
    table.stmt tbody td:nth-child(1) { grid-area: acct; }
    table.stmt tbody td:nth-child(2) { grid-area: bal; }
    table.stmt tbody td:nth-child(3) { grid-area: last; }
    table.stmt tbody td:nth-child(4) { grid-area: act; text-align: start; }
    table.holdtbl tr { grid-template-columns: minmax(0, 1fr); }
    table.holdtbl td.act { text-align: start; }
    .holdbtns { display: flex; }
    .desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .two, .inline-form { grid-template-columns: minmax(0, 1fr); }
    .strip { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .tile { padding: var(--hh-s-16); }
    .tile-figure { font-size: 24px; }
  }
`;

// The office's own additions: a checkbox row, the "what would go" box, the paper path rows and the history card forms.
const officeStyles = css`
  .check-row { display: flex; align-items: flex-start; gap: var(--hh-s-12); }
  .check-row > span { display: grid; gap: 2px; }
  .check-row > span > span { font-weight: 400; }
  .preview { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); padding: var(--hh-s-12) var(--hh-s-16); margin: var(--hh-s-12) 0; }
  .preview h3 { margin-bottom: var(--hh-s-8); }
  .preview p:last-child { margin-bottom: 0; }
  .alert-warn { border-inline-start: 4px solid var(--hh-state-watch); background: color-mix(in srgb, var(--hh-state-watch) 14%, var(--hh-card)); padding: var(--hh-s-8) var(--hh-s-12); border-radius: var(--hh-radius-control); }
  .btn.held { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); cursor: not-allowed; }
  .paper-list li { align-items: flex-start; }
  .who-block { display: grid; gap: 2px; min-width: 0; }
  .close-form { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; }
  .close-form label { min-width: 220px; flex: 1 1 220px; }
  .inline-select { min-width: 200px; }
  @media (max-width: 1100px) {
    .remind-cols .col-side { order: -1; }
  }
  .inline-select select { min-height: 36px; }
  @media (max-width: 520px) {
    table.hist tr { grid-template-columns: minmax(0, 1fr) auto; grid-template-areas: "acct sent" "goes goes" "age status" "opened opened"; }
    table.hist tbody td:nth-child(1) { grid-area: sent; text-align: end; }
    table.hist tbody td:nth-child(2) { grid-area: acct; font-weight: 700; }
    table.hist tbody td:nth-child(3) { grid-area: goes; }
    table.hist tbody td:nth-child(4) { grid-area: age; }
    table.hist tbody td:nth-child(5) { grid-area: status; text-align: end; }
    table.hist tbody td:nth-child(6) { grid-area: opened; color: var(--hh-text-dim); }
  }
`;

export const arStyles = [receivablesStyles, officeStyles];
