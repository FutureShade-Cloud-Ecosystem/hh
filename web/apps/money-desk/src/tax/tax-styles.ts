// The tax slice's own styles. Every value is a token of the design system; nothing here is a literal colour.
// Status is a word before it is a colour, figures sit in tabular columns and line up on the right, a wide table
// scrolls inside its own card so the page never scrolls sideways, and on a phone each table gives way to a stack
// of cards. Class names carry a tx- prefix so the slice shares an element with the others without either's rules
// reaching the other.
import { css } from "lit";

export const taxStyles = css`
  main.tx-desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  .tx-head { margin-bottom: var(--hh-s-16); }
  .tx-head h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  .tx-head p { margin: var(--hh-s-4) 0 0; }
  .tx-dim { color: var(--hh-text-dim); }
  .tx-mono { font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 13px; overflow-wrap: anywhere; }

  .tx-tabs { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .tx-tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .tx-tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }
  .tx-count { font-variant-numeric: tabular-nums; margin-inline-start: var(--hh-s-8); }

  .tx-split { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .tx-panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); min-width: 0; margin-bottom: var(--hh-s-16); }
  .tx-split-month { grid-template-columns: minmax(0, 0.85fr) minmax(0, 1.15fr); }
  .tx-split > .tx-panel { margin-bottom: 0; }
  .tx-panel h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  .tx-panel h3 { margin: 0; font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  .tx-panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }

  .tx-alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); overflow-wrap: anywhere; }
  .tx-alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; background: color-mix(in srgb, var(--hh-state-stop) 12%, var(--hh-card)); }
  .tx-alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }
  .tx-alert-watch { border-color: var(--hh-state-watch); border-inline-start-width: 4px; }
  .tx-note { margin: var(--hh-s-8) 0 0; padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-state-watch) 14%, var(--hh-card)); border: 1px solid var(--hh-state-watch); color: var(--hh-text); font-size: 14px; }
  .tx-later { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; vertical-align: middle; }
  .tx-later-line { margin: var(--hh-s-12) 0 0; font-size: 14px; }

  .tx-status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-line); white-space: nowrap; }
  .tx-status::before { content: ""; width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: currentColor; }
  .tx-status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .tx-status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .tx-status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .tx-status-plain { color: var(--hh-text-dim); }
  .tx-tag { display: inline-block; padding: 0 var(--hh-s-8); border: 1px solid var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; color: var(--hh-text-dim); }

  /* The accounts: one button per account, a coloured edge and a word. */
  .tx-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-8); }
  .tx-account { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-12); width: 100%; text-align: start; min-height: 64px; padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-line); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); }
  .tx-account[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .tx-account-open { border-inline-start-color: var(--hh-state-watch); }
  .tx-account-clear { border-inline-start-color: var(--hh-state-good); }
  .tx-account-main { display: grid; gap: var(--hh-s-4); min-width: 0; }
  .tx-account-name { font-weight: 700; }
  .tx-account-meta { font-size: 14px; color: var(--hh-text-dim); overflow-wrap: anywhere; }

  /* Forms. */
  .tx-form { align-items: start; display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: var(--hh-s-12); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .tx-field { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; min-width: 0; }
  .tx-field-wide { grid-column: 1 / -1; }
  .tx-field input, .tx-field select, .tx-field textarea { font: inherit; font-size: 16px; min-height: 44px; padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  .tx-field textarea { min-height: 180px; font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 14px; line-height: 1.45; }
  .tx-field input:focus-visible, .tx-field select:focus-visible, .tx-field textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .tx-hint { font-size: 13px; font-weight: 400; color: var(--hh-text-dim); }
  .tx-hint-bad { color: var(--hh-state-stop); font-weight: 700; }
  .tx-actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin: var(--hh-s-12) 0; }
  .tx-toolbar { display: flex; flex-wrap: wrap; gap: var(--hh-s-12) var(--hh-s-16); align-items: end; margin-bottom: var(--hh-s-12); }
  .tx-toolbar .tx-field { min-width: 180px; }
  .tx-filters { display: flex; flex-wrap: wrap; gap: var(--hh-s-4); }
  .tx-filter { min-height: 36px; padding: 0 var(--hh-s-12); border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); font-size: 14px; }
  .tx-filter[aria-pressed="true"] { border-color: var(--hh-focus-ring); font-weight: 700; background: var(--hh-row-alt); }
  .tx-link { white-space: nowrap; background: transparent; border: 0; padding: var(--hh-s-4) 0; min-height: 36px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; }
  .tx-choices { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .tx-choice { position: relative; display: grid; gap: var(--hh-s-4); text-align: start; padding: var(--hh-s-12) var(--hh-s-16) var(--hh-s-12) calc(var(--hh-s-16) + 28px); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); min-height: 44px; }
  .tx-choice::before { content: ""; position: absolute; inset-inline-start: var(--hh-s-16); top: var(--hh-s-16); width: 16px; height: 16px; border-radius: var(--hh-radius-pill); border: 2px solid var(--hh-control-line); box-sizing: border-box; }
  .tx-choice[aria-checked="true"] { border-color: var(--hh-focus-ring); border-width: 2px; background: color-mix(in srgb, var(--hh-accent) 16%, var(--hh-card)); }
  .tx-choice[aria-checked="true"]::before { border-color: var(--hh-text); background: radial-gradient(circle, var(--hh-text) 0 4px, transparent 5px); }
  .tx-choice-title { font-weight: 700; }
  .tx-choice-line { font-size: 13px; color: var(--hh-text-dim); }

  /* Tables and, on a phone, cards. */
  .tx-scroll { overflow-x: auto; }
  .tx-panel table { width: 100%; border-collapse: collapse; font-size: 15px; }
  .tx-panel th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  .tx-panel td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  .tx-panel th.num, .tx-panel td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .tx-panel tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .tx-sub { display: block; font-size: 13px; color: var(--hh-text-dim); overflow-wrap: anywhere; }
  .tx-nowrap { white-space: nowrap; }
  .tx-fact { margin: 0 0 var(--hh-s-8); font-weight: 600; overflow-wrap: anywhere; }
  .tx-credit { color: var(--hh-text); }
  .tx-debit { color: var(--hh-text); }
  .tx-cards { display: none; list-style: none; margin: 0; padding: 0; gap: var(--hh-s-8); }
  .tx-card { display: grid; gap: var(--hh-s-8); padding: var(--hh-s-12) var(--hh-s-16); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); background: var(--hh-card); }
  .tx-card[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .tx-card-top { display: flex; align-items: baseline; justify-content: space-between; gap: var(--hh-s-12); }
  .tx-card-amount { font-weight: 700; font-variant-numeric: tabular-nums; }
  .tx-facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin: 0 0 var(--hh-s-12); }
  .tx-facts dt { color: var(--hh-text-dim); }
  .tx-facts dd { margin: 0; overflow-wrap: anywhere; }
  .tx-empty { padding: var(--hh-s-24) var(--hh-s-8); display: grid; gap: var(--hh-s-4); }
  .tx-empty strong { font-size: 18px; }
  .tx-history { list-style: none; margin: 0 0 var(--hh-s-12); padding: 0; display: grid; gap: var(--hh-s-4); font-size: 14px; }

  @media (max-width: 900px) {
    .tx-split, .tx-split-month { grid-template-columns: minmax(0, 1fr); }
  }
  /* Three areas do not fit one phone row: the row stays one row and scrolls inside its own strip. */
  @media (max-width: 520px) {
    nav.areas { flex-wrap: nowrap; overflow-x: auto; }
    nav.areas .gl-tab { flex: none; white-space: nowrap; }
  }
  @media (max-width: 640px) {
    .tx-tabs { gap: var(--hh-s-4); }
    .tx-tab { padding: 0 var(--hh-s-12); font-size: 14px; }
    .tx-panel { padding: var(--hh-s-16); }
    .tx-hide-narrow { display: none; }
    .tx-cards { display: grid; }
    main.tx-desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .tx-toolbar .tx-field { min-width: 0; flex: 1 1 100%; }
  }

  /* A box to tick, with its words beside it. */
  .tx-check { display: flex; align-items: flex-start; gap: var(--hh-s-12); min-height: 44px; font-size: 15px; color: var(--hh-text); font-weight: 600; }
  .tx-check input { width: 24px; height: 24px; min-height: 24px; margin: var(--hh-s-2) 0 0; flex: none; accent-color: var(--hh-accent-text); }
  .tx-check input:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .tx-checks { display: grid; gap: var(--hh-s-4); margin: var(--hh-s-4) 0 var(--hh-s-8); grid-column: 1 / -1; }

  /* The step that asks before a change that reaches every sale: what changes, in words, then the two buttons. */
  .tx-confirm { border: 1px solid var(--hh-state-watch); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-state-watch) 10%, var(--hh-card)); padding: var(--hh-s-12) var(--hh-s-16); margin: var(--hh-s-12) 0; display: grid; gap: var(--hh-s-8); }
  .tx-confirm strong { font-size: 16px; }
  .tx-confirm p { margin: 0; overflow-wrap: anywhere; }

  /* The figures of a reconciliation: a label above its amount. */
  .tx-stats { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: var(--hh-s-12); margin: var(--hh-s-12) 0; }
  .tx-stat { border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); background: var(--hh-card); padding: var(--hh-s-12) var(--hh-s-16); display: grid; gap: var(--hh-s-4); }
  .tx-stat-label { font-size: 13px; color: var(--hh-text-dim); font-weight: 600; line-height: 1.3; }
  .tx-hintline { font-weight: 400; font-size: 12px; white-space: nowrap; }
  /* A fixed label height (three lines) so the figures of a row of tiles sit on one line. */
  @media (min-width: 521px) { .tx-stat-label { min-height: 3.9em; } }
  .tx-stat-figure { font-size: 22px; font-weight: 700; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .tx-rowbtns { display: flex; flex-wrap: wrap; gap: var(--hh-s-4) var(--hh-s-12); }
`;
