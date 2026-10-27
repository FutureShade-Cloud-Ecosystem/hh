// The bank reconciliation slice's own styles. Every value is a token of the design system; nothing here is a
// literal colour. Status is a word before it is a colour, figures sit in tabular columns and line up on the right,
// a wide table scrolls inside its own card so the page never scrolls sideways, and on a phone each table gives
// way to a stack of cards. Class names carry a br- prefix so the slice shares an element with the others without
// either's rules reaching the other.
import { css } from "lit";

export const bankrecStyles = css`
  main.br-desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  .br-head { margin-bottom: var(--hh-s-16); }
  .br-head h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  .br-head p { margin: var(--hh-s-4) 0 0; }
  .br-dim { color: var(--hh-text-dim); }
  .br-mono { font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 13px; overflow-wrap: anywhere; }

  .br-tabs { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .br-tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .br-tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }
  .br-count { font-variant-numeric: tabular-nums; margin-inline-start: var(--hh-s-8); }

  .br-split { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .br-panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); min-width: 0; margin-bottom: var(--hh-s-16); }
  .br-split-month { grid-template-columns: minmax(0, 0.85fr) minmax(0, 1.15fr); }
  .br-split > .br-panel { margin-bottom: 0; }
  .br-panel h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  .br-panel h3 { margin: 0; font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  .br-panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }

  .br-alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); overflow-wrap: anywhere; }
  .br-alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; background: color-mix(in srgb, var(--hh-state-stop) 12%, var(--hh-card)); }
  .br-alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }
  .br-alert-watch { border-color: var(--hh-state-watch); border-inline-start-width: 4px; }
  .br-note { margin: var(--hh-s-8) 0 0; padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-accent) 18%, var(--hh-card)); color: var(--hh-text); font-size: 14px; }
  .br-later { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; vertical-align: middle; }
  .br-later-line { margin: var(--hh-s-12) 0 0; font-size: 14px; }

  .br-status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-line); white-space: nowrap; }
  .br-status::before { content: ""; width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: currentColor; }
  .br-status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .br-status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .br-status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .br-status-plain { color: var(--hh-text-dim); }
  .br-tag { display: inline-block; padding: 0 var(--hh-s-8); border: 1px solid var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; color: var(--hh-text-dim); }

  /* The accounts: one button per account, a coloured edge and a word. */
  .br-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-8); }
  .br-account { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-12); width: 100%; text-align: start; min-height: 64px; padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-line); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); }
  .br-account[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .br-account-open { border-inline-start-color: var(--hh-state-watch); }
  .br-account-clear { border-inline-start-color: var(--hh-state-good); }
  .br-account-main { display: grid; gap: var(--hh-s-4); min-width: 0; }
  .br-account-name { font-weight: 700; }
  .br-account-meta { font-size: 14px; color: var(--hh-text-dim); overflow-wrap: anywhere; }

  /* Forms. */
  .br-form { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: var(--hh-s-12); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .br-field { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; min-width: 0; }
  .br-field-wide { grid-column: 1 / -1; }
  .br-field input, .br-field select, .br-field textarea { font: inherit; font-size: 16px; min-height: 44px; padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  .br-field textarea { min-height: 180px; font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 14px; line-height: 1.45; }
  .br-field input:focus-visible, .br-field select:focus-visible, .br-field textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .br-hint { font-size: 13px; font-weight: 400; color: var(--hh-text-dim); }
  .br-hint-bad { color: var(--hh-state-stop); font-weight: 700; }
  .br-actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin: var(--hh-s-12) 0; }
  .br-toolbar { display: flex; flex-wrap: wrap; gap: var(--hh-s-12) var(--hh-s-16); align-items: end; margin-bottom: var(--hh-s-12); }
  .br-toolbar .br-field { min-width: 180px; }
  .br-filters { display: flex; flex-wrap: wrap; gap: var(--hh-s-4); }
  .br-filter { min-height: 36px; padding: 0 var(--hh-s-12); border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); font-size: 14px; }
  .br-filter[aria-pressed="true"] { border-color: var(--hh-focus-ring); font-weight: 700; background: var(--hh-row-alt); }
  .br-link { white-space: nowrap; background: transparent; border: 0; padding: var(--hh-s-4) 0; min-height: 36px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; }
  .br-choices { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .br-choice { position: relative; display: grid; gap: var(--hh-s-4); text-align: start; padding: var(--hh-s-12) var(--hh-s-16) var(--hh-s-12) calc(var(--hh-s-16) + 28px); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); min-height: 44px; }
  .br-choice::before { content: ""; position: absolute; inset-inline-start: var(--hh-s-16); top: var(--hh-s-16); width: 16px; height: 16px; border-radius: var(--hh-radius-pill); border: 2px solid var(--hh-control-line); box-sizing: border-box; }
  .br-choice[aria-checked="true"] { border-color: var(--hh-focus-ring); border-width: 2px; background: color-mix(in srgb, var(--hh-accent) 16%, var(--hh-card)); }
  .br-choice[aria-checked="true"]::before { border-color: var(--hh-text); background: radial-gradient(circle, var(--hh-text) 0 4px, transparent 5px); }
  .br-choice-title { font-weight: 700; }
  .br-choice-line { font-size: 13px; color: var(--hh-text-dim); }

  /* Tables and, on a phone, cards. */
  .br-scroll { overflow-x: auto; }
  .br-panel table { width: 100%; border-collapse: collapse; font-size: 15px; }
  .br-panel th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  .br-panel td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  .br-panel th.num, .br-panel td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .br-panel tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .br-sub { display: block; font-size: 13px; color: var(--hh-text-dim); overflow-wrap: anywhere; }
  .br-nowrap { white-space: nowrap; }
  .br-fact { margin: 0 0 var(--hh-s-8); font-weight: 600; overflow-wrap: anywhere; }
  .br-credit { color: var(--hh-text); }
  .br-debit { color: var(--hh-text); }
  .br-cards { display: none; list-style: none; margin: 0; padding: 0; gap: var(--hh-s-8); }
  .br-card { display: grid; gap: var(--hh-s-8); padding: var(--hh-s-12) var(--hh-s-16); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); background: var(--hh-card); }
  .br-card[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .br-card-top { display: flex; align-items: baseline; justify-content: space-between; gap: var(--hh-s-12); }
  .br-card-amount { font-weight: 700; font-variant-numeric: tabular-nums; }
  .br-facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin: 0 0 var(--hh-s-12); }
  .br-facts dt { color: var(--hh-text-dim); }
  .br-facts dd { margin: 0; overflow-wrap: anywhere; }
  .br-empty { padding: var(--hh-s-24) var(--hh-s-8); display: grid; gap: var(--hh-s-4); }
  .br-empty strong { font-size: 18px; }
  .br-history { list-style: none; margin: 0 0 var(--hh-s-12); padding: 0; display: grid; gap: var(--hh-s-4); font-size: 14px; }

  /* The starting point form (round 4): two fields stacked, one row per entry with the checkbox leading it as
     its label and the whole row as the tap target, and figures that never wrap mid number. */
  .br-form-stack { grid-template-columns: 1fr; }
  label.br-tick-row { display: flex; align-items: center; gap: var(--hh-s-12); min-height: 44px; padding: var(--hh-s-4) 0; font-size: 14px; font-weight: 400; color: var(--hh-text); cursor: pointer; }
  .br-tick-row input[type="checkbox"] { width: 18px; height: 18px; min-height: 18px; margin: 0; padding: 0; flex: none; accent-color: var(--hh-accent); }
  .br-tick-date { flex: none; color: var(--hh-text-dim); white-space: nowrap; font-variant-numeric: tabular-nums; }
  .br-tick-memo { flex: 1 1 auto; min-width: 0; overflow-wrap: anywhere; }
  .br-tick-amount { flex: none; font-weight: 700; text-align: right; white-space: nowrap; font-variant-numeric: tabular-nums; }
  [data-cutover-result] .br-facts { grid-template-columns: minmax(0, 1fr) auto; gap: var(--hh-s-6) var(--hh-s-12); align-items: center; }
  [data-cutover-result] dd { text-align: right; white-space: nowrap; font-variant-numeric: tabular-nums; overflow-wrap: normal; }

  @media (max-width: 900px) {
    .br-split, .br-split-month { grid-template-columns: minmax(0, 1fr); }
  }
  /* Three areas do not fit one phone row: the row stays one row and scrolls inside its own strip. */
  @media (max-width: 520px) {
    nav.areas { flex-wrap: nowrap; overflow-x: auto; }
    nav.areas .gl-tab { flex: none; white-space: nowrap; }
  }
  @media (max-width: 640px) {
    .br-tabs { gap: var(--hh-s-4); }
    .br-tab { padding: 0 var(--hh-s-12); font-size: 14px; }
    .br-panel { padding: var(--hh-s-16); }
    .br-hide-narrow { display: none; }
    .br-cards { display: grid; }
    main.br-desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .br-toolbar .br-field { min-width: 0; flex: 1 1 100%; }
  }
`;
