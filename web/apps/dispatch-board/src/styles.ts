// The dispatch board's own styles. Every value is a token of the design system; nothing here is
// a literal colour. Status is a word before it is a colour, figures sit in tabular columns, a
// control is at least 44 pixels tall, a wide table scrolls inside its own card and the page never
// scrolls sideways. A gauge's fill is one of twenty one width steps (a class), because the design
// system's position rules allow no bound style.
import { css } from "lit";

export const boardStyles = css`
  .desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  h3 { margin: 0; font-size: 16px; line-height: 1.25; font-weight: 700; }
  h4 { margin: 0 0 var(--hh-s-8); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  p { margin: 0 0 var(--hh-s-8); }
  ul { list-style: none; margin: 0; padding: 0; }
  .dim { color: var(--hh-text-dim); }
  .small { font-size: 14px; }
  .good { color: var(--hh-state-good); font-weight: 700; }
  .bad { color: var(--hh-state-stop); font-weight: 700; }
  .code { font-family: var(--hh-font-mono); }
  .head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-8); }
  .spacer { flex: 1; }
  .count { margin-inline-start: var(--hh-s-8); font-variant-numeric: tabular-nums; color: var(--hh-text-dim); font-weight: 600; }

  /* Tabs: the open one is a white pill, the rest are plain words; the tabs that are not yet are dashed. */
  .tabs { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-16); }
  .tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text-dim); border: 1px solid transparent; padding: 0 var(--hh-s-20); }
  .tab[aria-current="true"] { background: var(--hh-card); color: var(--hh-text); border-color: var(--hh-control-line); font-weight: 700; }
  .tag-later { display: inline-flex; align-items: center; gap: var(--hh-s-8); min-height: 44px; padding: 0 var(--hh-s-12); color: var(--hh-text-dim); }
  .tag-later em { font-style: normal; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); }

  /* The toolbar: the yard, the day, the tools. */
  .toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-12) var(--hh-s-24); padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); margin-bottom: var(--hh-s-16); }
  .yard { font-weight: 700; font-size: 18px; }
  .when { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); }
  .when input[type="date"] { width: auto; min-width: 160px; }
  .day-btn { min-width: 44px; padding: 0 var(--hh-s-12); font-size: 22px; line-height: 1; }
  .day-words { font-weight: 600; margin-inline-start: var(--hh-s-8); }
  .tools { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin-inline-start: auto; }
  .inline { display: flex; align-items: center; gap: var(--hh-s-8); font-weight: 600; }

  .alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; }
  .alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }
  .alert ul { margin: var(--hh-s-8) 0 0; padding-inline-start: var(--hh-s-20); list-style: disc; }

  /* The quick cards. */
  .qcards { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); gap: var(--hh-s-12); margin-bottom: var(--hh-s-16); }
  .tile { display: flex; flex-direction: column; align-items: flex-start; gap: var(--hh-s-2); padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-widget, var(--hh-card)); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-widget, var(--hh-radius-card)); min-width: 0; text-align: start; color: var(--hh-text); font: inherit; }
  .tile-btn { cursor: pointer; min-height: 88px; }
  .tile-btn:hover { border-color: var(--hh-control-line); }
  .tile-btn[aria-pressed="true"] { border: 2px solid var(--hh-text); background: var(--hh-row-alt); padding: calc(var(--hh-s-12) - 1px) calc(var(--hh-s-16) - 1px); }
  .tile .kicker, .day .kicker { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .tile .figure { font-size: 32px; font-weight: 700; line-height: 1.1; font-variant-numeric: tabular-nums; }
  .tile .small { color: var(--hh-text-dim); font-size: 13px; }
  .qcards-as-of { grid-column: 1 / -1; margin: 0; font-variant-numeric: tabular-nums; }
  .filter { margin: 0 0 var(--hh-s-12); }
  .link { background: transparent; border: 0; padding: 0 var(--hh-s-4); min-height: 44px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; cursor: pointer; }
  .link:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* The week ahead. */
  .week { display: grid; grid-template-columns: repeat(7, minmax(0, 1fr)); gap: var(--hh-s-8); margin-bottom: var(--hh-s-16); }
  .day { display: flex; flex-direction: column; align-items: flex-start; gap: var(--hh-s-2); min-height: 72px; padding: var(--hh-s-8) var(--hh-s-12); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); text-align: start; font: inherit; cursor: pointer; min-width: 0; }
  .day[aria-current="true"] { background: var(--hh-card); border: 2px solid var(--hh-text); padding: calc(var(--hh-s-8) - 1px) calc(var(--hh-s-12) - 1px); }
  .day-no { font-weight: 700; font-variant-numeric: tabular-nums; }
  .day .small { font-size: 12px; color: var(--hh-text-dim); }

  .board-bar { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .segs { display: flex; flex-wrap: nowrap; gap: var(--hh-s-4); padding: var(--hh-s-4); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-pill); background: var(--hh-row-alt); }
  .seg { min-height: 44px; padding: 0 var(--hh-s-16); white-space: nowrap; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid transparent; }
  .seg[aria-pressed="true"] { background: var(--hh-text); color: var(--hh-page); font-weight: 700; }

  .bulk { position: sticky; top: var(--hh-s-8); z-index: var(--hh-z-nav, 10); display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); background: var(--hh-card); border: 1px solid var(--hh-control-line); border-inline-start: 4px solid var(--hh-text); border-radius: var(--hh-radius-control); }

  /* The board: the staging lane beside the trucks. */
  .board { display: grid; grid-template-columns: minmax(300px, 380px) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; margin-bottom: var(--hh-s-16); }
  .staging { background: var(--hh-row-alt); border: 1px dashed var(--hh-control-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); min-width: 0; margin-bottom: var(--hh-s-16); }
  .board .staging { margin-bottom: 0; }
  .staging h3, .lane h3 { display: flex; align-items: baseline; margin-bottom: var(--hh-s-4); }
  .trucks { display: grid; gap: var(--hh-s-16); min-width: 0; }
  .truck { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); min-width: 0; }
  .truck-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-12); margin-bottom: var(--hh-s-8); }
  .truck-head h3 { font-size: 18px; font-family: var(--hh-font-mono); }
  .crew { display: flex; flex-wrap: wrap; gap: var(--hh-s-12) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .crew label { flex: 1 1 200px; }

  /* A gauge: a word for the verdict, the figure, and a bar. */
  .gauge { margin: var(--hh-s-8) 0; }
  .gauge-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-4) var(--hh-s-12); margin-bottom: var(--hh-s-4); }
  .gauge-kind { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; min-width: 64px; }
  .gauge-figure { font-variant-numeric: tabular-nums; font-weight: 600; flex: 1 1 auto; }
  .bar { height: 10px; border-radius: var(--hh-radius-pill); background: var(--hh-track); overflow: hidden; }
  .fill { height: 100%; border-radius: var(--hh-radius-pill); background: var(--hh-text-dim); }
  .fill-good { background: var(--hh-state-good); }
  .fill-watch { background: var(--hh-state-watch); }
  .fill-stop { background: var(--hh-state-stop); }
  .w-0 { width: 0%; }
  .w-5 { width: 5%; }
  .w-10 { width: 10%; }
  .w-15 { width: 15%; }
  .w-20 { width: 20%; }
  .w-25 { width: 25%; }
  .w-30 { width: 30%; }
  .w-35 { width: 35%; }
  .w-40 { width: 40%; }
  .w-45 { width: 45%; }
  .w-50 { width: 50%; }
  .w-55 { width: 55%; }
  .w-60 { width: 60%; }
  .w-65 { width: 65%; }
  .w-70 { width: 70%; }
  .w-75 { width: 75%; }
  .w-80 { width: 80%; }
  .w-85 { width: 85%; }
  .w-90 { width: 90%; }
  .w-95 { width: 95%; }
  .w-100 { width: 100%; }

  /* One load. */
  .loads { display: grid; gap: var(--hh-s-8); margin: var(--hh-s-8) 0; }
  .loads.wide { grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); }
  .load { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); padding: var(--hh-s-12); min-width: 0; }
  .truck .load { background: var(--hh-page); }
  .load-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); }
  .order { font-family: var(--hh-font-mono); font-weight: 700; }
  .who { display: block; font-weight: 700; }
  .facts { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-4) var(--hh-s-8); margin: var(--hh-s-8) 0; }
  .fact { font-variant-numeric: tabular-nums; font-size: 14px; font-weight: 600; }
  .fact.dim { font-weight: 400; }
  .hold-words { padding: var(--hh-s-8) var(--hh-s-12); border-inline-start: 4px solid var(--hh-state-stop); background: var(--hh-row-alt); border-radius: var(--hh-radius-control); }
  .empty-lane { padding: var(--hh-s-8) 0; }
  .nodate { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-state-watch); border-radius: var(--hh-radius-pill); color: var(--hh-state-watch); font-size: 13px; font-weight: 700; }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); margin-top: var(--hh-s-8); }
  .small-btn { min-height: 44px; padding: 0 var(--hh-s-12); font-size: 14px; white-space: nowrap; }
  .pick { min-width: 0; max-width: 100%; }
  .btn { white-space: nowrap; }

  /* The pick box: a button that is a checkbox, so the position gate has nothing to bind. */
  .box { width: 28px; height: 28px; min-height: 28px; padding: 0; border-radius: var(--hh-radius-control); border: 2px solid var(--hh-control-line); background: var(--hh-field); position: relative; flex: none; }
  .box[aria-checked="true"] { background: var(--brand-fill, var(--hh-accent)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); }
  .box[aria-checked="true"]::after { content: ""; position: absolute; inset-inline-start: 8px; top: 3px; width: 6px; height: 12px; border: solid var(--brand-fill-on, var(--hh-accent-ink)); border-width: 0 3px 3px 0; transform: rotate(45deg); }
  .box:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* Status words: outline pills, never buttons. */
  .status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; letter-spacing: 0.06em; text-transform: uppercase; border: 1px solid var(--hh-line); white-space: nowrap; }
  .status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .status-plain { color: var(--hh-text-dim); }

  /* The day board: a run by stage. */
  .day-run { margin-bottom: var(--hh-s-16); }
  .columns { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--hh-s-12); }
  .col { min-width: 0; padding: var(--hh-s-8); background: var(--hh-row-alt); border-radius: var(--hh-radius-control); }
  .col .load { background: var(--hh-card); }
  .did-not { margin-top: var(--hh-s-12); }
  .status-lanes { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--hh-s-12); margin-bottom: var(--hh-s-16); align-items: start; }
  .lane { background: var(--hh-row-alt); border-radius: var(--hh-radius-card); padding: var(--hh-s-12); min-width: 0; }

  /* Panels and tables. */
  .panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); margin-bottom: var(--hh-s-16); min-width: 0; }
  .panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .panel.empty strong { display: block; font-size: 18px; margin-bottom: var(--hh-s-4); }
  .scroll { overflow-x: auto; position: relative; }
  table { width: 100%; border-collapse: collapse; font-size: 15px; }
  th { text-align: start; font-weight: 700; padding: var(--hh-s-8); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; }
  td { padding: var(--hh-s-8); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  tbody tr:last-child td { border-bottom: 0; }
  th.num, td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  td.num.stop { color: var(--hh-state-stop); font-weight: 700; }
  td .small { display: block; }

  /* The inline sheet. */
  .sheet { margin-top: var(--hh-s-12); padding: var(--hh-s-12); background: var(--hh-row-alt); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); display: grid; gap: var(--hh-s-8); }
  .sheet h4 { margin: 0; color: var(--hh-text); font-size: 14px; letter-spacing: 0; text-transform: none; }
  .sheet-grid .wide { grid-column: 1 / -1; }
  .raise { border-inline-start: 4px solid var(--hh-state-watch); }
  .sheet-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-8); }
  .sheet .alert { margin: 0; }
  label { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; }
  label.inline { display: flex; }
  input, select { font: inherit; font-size: 16px; min-height: 44px; width: 100%; box-sizing: border-box; }
  input:focus-visible, select:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .theme-btn { font: inherit; font-size: 13px; padding: 7px 12px; min-height: 0; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); cursor: pointer; }
  .theme-btn:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }

  /* The schedule form: two columns of fields, the long ones across; one column at 700 and under. */
  .sched-form { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-12) var(--hh-s-16); }
  .sched-form .wide { grid-column: 1 / -1; }
  .sched-form .alert, .sched-form p { margin: 0; }
  .map-pin { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; }
  .map-pin-pair { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--hh-s-12) var(--hh-s-16); }

  /* The scan trail: five stages in one row per pallet; reached ones are filled and the furthest is ringed, so it reads in greyscale. */
  .trail-summary { margin: 0 0 var(--hh-s-8); font-weight: 700; font-size: 18px; }
  .trail-load { margin: var(--hh-s-8) 0; }
  .spine { display: grid; grid-template-columns: repeat(5, minmax(0, 1fr)); gap: var(--hh-s-4); }
  .stage { display: flex; align-items: center; justify-content: center; text-align: center; min-height: 44px; padding: var(--hh-s-4); font-size: 12px; font-weight: 600; border: 1px dashed var(--hh-control-line); border-radius: var(--hh-radius-control); color: var(--hh-text-dim); overflow-wrap: normal; }
  .stage.reached { border-style: solid; border-color: var(--hh-text); background: var(--hh-card); color: var(--hh-text); }
  .stage[data-furthest="true"] { outline: 3px solid var(--hh-text); outline-offset: 1px; font-weight: 700; }

  /* The rules: a name, its value in words, and the one sentence that keeps it apart from its neighbour. */
  .rules { display: grid; gap: var(--hh-s-12); }
  .rules li { display: grid; grid-template-columns: minmax(180px, 1fr) minmax(140px, 1fr) minmax(0, 2fr); gap: var(--hh-s-4) var(--hh-s-16); align-items: baseline; padding-bottom: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); }
  .rules li:last-child { border-bottom: 0; padding-bottom: 0; }
  .rule-name { font-weight: 600; }

  /* Will call: the order box beside its two buttons, and the one time tag. */
  .wc-form { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--hh-s-12) var(--hh-s-16); align-items: end; }
  .wc-form .actions { margin-top: 0; }
  .tag { border: 2px solid var(--hh-text); }
  .tag .once { margin: 0 0 var(--hh-s-12); }
  .tag-order { margin: 0; font-family: var(--hh-font-mono); font-weight: 700; font-size: 20px; }
  .tag-code { margin: var(--hh-s-8) 0; font-family: var(--hh-font-mono); font-size: 22px; font-weight: 700; letter-spacing: 0.04em; word-break: break-all; }
  @media print {
    .tabs, .toolbar, .head, .alert, [data-willcall], [data-wc-codes], .tag .actions { display: none; }
    .tag { border: 0; }
  }

  .matrix p { max-width: 70ch; }

  /* WALK-C16: the problems strip sits above the load board and disappears when nothing is wrong. */
  .problems { background: var(--hh-card); border: 1px solid var(--hh-line); border-inline-start: 4px solid var(--hh-state-watch); border-radius: var(--hh-radius-card); padding: var(--hh-s-12) var(--hh-s-16); margin-bottom: var(--hh-s-16); }
  .problems-head { display: flex; align-items: baseline; gap: var(--hh-s-8); margin: 0 0 var(--hh-s-8); }
  .problems-head h3 { font-size: 14px; letter-spacing: 0.04em; text-transform: uppercase; color: var(--hh-text-dim); }
  .problem-list { display: flex; flex-direction: column; gap: var(--hh-s-4); }
  .problem { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-4) 0; }
  .problem.applied .problem-line { text-decoration: line-through; color: var(--hh-text-dim); }
  .problem-line { flex: 1 1 auto; font-size: 14px; font-weight: 600; }

  /* WALK-C16: the time axis the redesigned board draws. Hours across the top, one row per run,
     stops laid out by stop_no. Each cell is a token-sized square, never a hand-rolled px. */
  .time-axis { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-12); margin-bottom: var(--hh-s-16); overflow-x: auto; }
  .time-axis-grid { display: grid; grid-template-columns: minmax(120px, 160px) repeat(12, minmax(48px, 1fr)); gap: var(--hh-s-4); min-width: 720px; }
  .time-axis-row { display: contents; }
  .time-axis-cell { display: flex; align-items: center; justify-content: center; min-height: 36px; padding: var(--hh-s-4); border-radius: var(--hh-radius-control); border: 1px dashed var(--hh-line); background: transparent; color: var(--hh-text-dim); font-size: 12px; font-weight: 600; font-variant-numeric: tabular-nums; }
  .time-axis-cell.head-run, .time-axis-cell.head-hour { border: 0; background: transparent; color: var(--hh-text-dim); text-transform: uppercase; letter-spacing: 0.04em; font-size: 11px; }
  .time-axis-cell.has-stop { background: color-mix(in srgb, var(--hh-state-good) 18%, var(--hh-card)); color: var(--hh-text); border-color: var(--hh-state-good); border-style: solid; font-weight: 700; }
  .time-axis-cell.axis-run { background: transparent; border: 0; color: var(--hh-text); font-family: var(--hh-font-mono); font-weight: 700; font-size: 14px; }

  /* WALK-C16: the run-detail drawer. It opens beside the board on desktop and at the top on
     phone, and never carries the same content twice. */
  .run-detail { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-16); margin-bottom: var(--hh-s-16); }
  .run-detail-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .run-detail .spacer { flex: 1; }

  /* WALK-C16: the suggest-runs panel. The list is what the dispatcher reads to decide; no
     suggestion carries its own sentence. */
  .suggest { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-12) var(--hh-s-16); margin-bottom: var(--hh-s-16); }
  .suggest-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-8); }
  .suggest .spacer { flex: 1; }
  .suggest-list { display: flex; flex-direction: column; gap: var(--hh-s-4); }
  .suggest-row { display: grid; grid-template-columns: minmax(80px, 1fr) minmax(120px, 1fr) minmax(0, 2fr); gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-4) 0; align-items: baseline; }

  /* WALK-C16: the live indicator. A small dot and one word: green when the board is ready,
     amber while a read is in flight. */
  .live { display: flex; align-items: center; gap: var(--hh-s-8); margin-bottom: var(--hh-s-12); color: var(--hh-text-dim); font-size: 13px; font-weight: 600; }
  .live-dot { width: 10px; height: 10px; border-radius: var(--hh-radius-pill); background: var(--hh-state-good); display: inline-block; }
  .live[data-busy="true"] .live-dot { background: var(--hh-state-watch); }
  .strip { display: none; flex-wrap: wrap; gap: var(--hh-s-4) var(--hh-s-16); margin: var(--hh-s-8) 0; color: var(--hh-text-dim); font-size: 14px; }
  .strip-cell strong { color: var(--hh-text); font-variant-numeric: tabular-nums; }
  .mob-tools { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin-bottom: var(--hh-s-8); }
  .mob-tools .theme-btn { min-height: 44px; padding: 0 var(--hh-s-16); }

  @media (max-width: 1279px) {
    .qcards { grid-template-columns: repeat(4, minmax(0, 1fr)); }
    .columns, .status-lanes { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (max-width: 1099px) {
    .board { grid-template-columns: minmax(0, 1fr); }
  }
  @media (max-width: 700px) {
    .rules li { grid-template-columns: minmax(0, 1fr); }
    .week { display: flex; overflow-x: auto; scroll-snap-type: x proximity; padding-bottom: var(--hh-s-4); }
    .day { flex: 0 0 132px; scroll-snap-align: start; }
    .qcards { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .columns, .status-lanes, .sheet-grid, .sched-form, .map-pin-pair, .wc-form { grid-template-columns: minmax(0, 1fr); }
    .strip { display: flex; }
    .col.none { display: none; }
    .did-not .count { margin-inline-start: var(--hh-s-8); }
    .tools { margin-inline-start: 0; }
    .toolbar { gap: var(--hh-s-8) var(--hh-s-16); }
    /* A table marked stack becomes a card per row: every cell keeps its label, none is hidden. */
    table.stack, .stack tbody { display: block; }
    .stack thead { display: none; }
    .stack tbody tr { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--hh-s-8) var(--hh-s-16); padding: var(--hh-s-12) var(--hh-s-8); border-bottom: 1px solid var(--hh-line); }
    .stack td { display: block; padding: 0; border: 0; text-align: start; min-width: 0; }
    .stack td.num { text-align: start; }
    .stack td::before { content: attr(data-label); display: block; font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  }
  @media (max-width: 520px) {
    .spine { gap: var(--hh-s-2); }
    .stage { font-size: 10px; padding: var(--hh-s-2); letter-spacing: -0.01em; }
    .desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    h1 { font-size: 24px; }
    .actions .btn, .actions select { flex: 1 1 140px; }
    .tabs { flex-wrap: nowrap; overflow-x: auto; }
    .tab, .tag-later { flex: none; }
  }
`;
