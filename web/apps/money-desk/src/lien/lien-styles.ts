// The liens and compliance slices' shared styles. Every value is a token of the design system; nothing here is a
// literal colour. Status is a word before it is a colour, figures sit in tabular columns and line up on the right, a
// wide table scrolls inside its own card so the page never scrolls sideways, and on a phone each table gives way to
// a stack of cards. Class names carry a li- prefix so the slices share an element with the others without either's
// rules reaching the other. The cards are border first with no shadow, as the Money Desk references draw them.
import { css } from "lit";

export const lienStyles = css`
  main.li-desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
  .li-head { margin-bottom: var(--hh-s-16); }
  .li-head h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  .li-head p { margin: var(--hh-s-4) 0 0; }
  .li-dim { color: var(--hh-text-dim); }
  .li-mono { font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 13px; overflow-wrap: anywhere; }

  .li-tabs { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .li-tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .li-tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }
  .li-count { font-variant-numeric: tabular-nums; margin-inline-start: var(--hh-s-8); }

  .li-split { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .li-panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); min-width: 0; margin-bottom: var(--hh-s-16); }
  .li-split-month { grid-template-columns: minmax(0, 0.85fr) minmax(0, 1.15fr); }
  .li-split > .li-panel { margin-bottom: 0; }
  .li-panel h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  .li-panel h3 { margin: 0; font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  .li-panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }

  .li-alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); overflow-wrap: anywhere; }
  .li-alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; background: color-mix(in srgb, var(--hh-state-stop) 12%, var(--hh-card)); }
  .li-alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }
  .li-alert-watch { border-color: var(--hh-state-watch); border-inline-start-width: 4px; }
  .li-note { margin: var(--hh-s-8) 0 0; padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-accent) 18%, var(--hh-card)); color: var(--hh-text); font-size: 14px; }
  .li-later { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; vertical-align: middle; }
  .li-later-line { margin: var(--hh-s-12) 0 0; font-size: 14px; }

  .li-status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-line); white-space: nowrap; }
  .li-status::before { content: ""; width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: currentColor; }
  .li-status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .li-status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .li-status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .li-status-plain { color: var(--hh-text-dim); }
  .li-tag { display: inline-block; padding: 0 var(--hh-s-8); border: 1px solid var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; font-weight: 700; color: var(--hh-text-dim); }

  /* The accounts: one button per account, a coloured edge and a word. */
  .li-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-8); }
  .li-account { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-12); width: 100%; text-align: start; min-height: 64px; padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-line); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); }
  .li-account[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .li-account-open { border-inline-start-color: var(--hh-state-watch); }
  .li-account-clear { border-inline-start-color: var(--hh-state-good); }
  .li-account-main { display: grid; gap: var(--hh-s-4); min-width: 0; }
  .li-account-name { font-weight: 700; }
  .li-account-meta { font-size: 14px; color: var(--hh-text-dim); overflow-wrap: anywhere; }

  /* Forms. */
  .li-form { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: var(--hh-s-12); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .li-field { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; min-width: 0; }
  .li-field-wide { grid-column: 1 / -1; }
  .li-field input, .li-field select, .li-field textarea { font: inherit; font-size: 16px; min-height: 44px; padding: var(--hh-s-8) var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  .li-field textarea { min-height: 180px; font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 14px; line-height: 1.45; }
  .li-field input:focus-visible, .li-field select:focus-visible, .li-field textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .li-field input[type="date"] { min-width: 0; max-width: 100%; }
  .li-hint { font-size: 13px; font-weight: 400; color: var(--hh-text-dim); }
  .li-hint-bad { color: var(--hh-state-stop); font-weight: 700; }
  .li-actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin: var(--hh-s-12) 0; }
  .li-toolbar { display: flex; flex-wrap: wrap; gap: var(--hh-s-12) var(--hh-s-16); align-items: end; margin-bottom: var(--hh-s-12); }
  .li-toolbar .li-field { min-width: 180px; }
  .li-filters { display: flex; flex-wrap: wrap; gap: var(--hh-s-4); }
  .li-filter { min-height: 36px; padding: 0 var(--hh-s-12); border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); font-size: 14px; }
  .li-filter[aria-pressed="true"] { border-color: var(--hh-focus-ring); font-weight: 700; background: var(--hh-row-alt); }
  .li-link { white-space: nowrap; background: transparent; border: 0; padding: var(--hh-s-4) 0; min-height: 36px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; }
  .li-choices { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .li-choice { position: relative; display: grid; gap: var(--hh-s-4); text-align: start; padding: var(--hh-s-12) var(--hh-s-16) var(--hh-s-12) calc(var(--hh-s-16) + 28px); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); min-height: 44px; }
  .li-choice::before { content: ""; position: absolute; inset-inline-start: var(--hh-s-16); top: var(--hh-s-16); width: 16px; height: 16px; border-radius: var(--hh-radius-pill); border: 2px solid var(--hh-control-line); box-sizing: border-box; }
  .li-choice[aria-checked="true"] { border-color: var(--hh-focus-ring); border-width: 2px; background: color-mix(in srgb, var(--hh-accent) 16%, var(--hh-card)); }
  .li-choice[aria-checked="true"]::before { border-color: var(--hh-text); background: radial-gradient(circle, var(--hh-text) 0 4px, transparent 5px); }
  .li-choice-title { font-weight: 700; }
  .li-choice-line { font-size: 13px; color: var(--hh-text-dim); }

  /* Tables and, on a phone, cards. */
  .li-scroll { overflow-x: auto; }
  .li-panel table { width: 100%; border-collapse: collapse; font-size: 15px; }
  .li-panel th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  .li-panel td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  .li-panel th.num, .li-panel td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .li-panel tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .li-sub { display: block; font-size: 13px; color: var(--hh-text-dim); overflow-wrap: anywhere; }
  .li-nowrap { white-space: nowrap; }
  .li-fact { margin: 0 0 var(--hh-s-8); font-weight: 600; overflow-wrap: anywhere; }
  .li-credit { color: var(--hh-text); }
  .li-debit { color: var(--hh-text); }
  .li-cards { display: none; list-style: none; margin: 0; padding: 0; gap: var(--hh-s-8); }
  .li-card { display: grid; gap: var(--hh-s-8); padding: var(--hh-s-12) var(--hh-s-16); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); background: var(--hh-card); }
  .li-card[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .li-card-top { display: flex; align-items: baseline; justify-content: space-between; gap: var(--hh-s-12); }
  .li-card-amount { font-weight: 700; font-variant-numeric: tabular-nums; }
  .li-facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin: 0 0 var(--hh-s-12); }
  .li-facts dt { color: var(--hh-text-dim); }
  .li-facts dd { margin: 0; overflow-wrap: anywhere; }
  .li-empty { padding: var(--hh-s-24) var(--hh-s-8); display: grid; gap: var(--hh-s-4); }
  .li-empty strong { font-size: 18px; }
  .li-history { list-style: none; margin: 0 0 var(--hh-s-12); padding: 0; display: grid; gap: var(--hh-s-4); font-size: 14px; }
  .li-banner { margin: 0 0 var(--hh-s-16); padding: var(--hh-s-8) var(--hh-s-16); border-radius: var(--hh-radius-control); border: 1px solid var(--hh-line); border-inline-start-width: 4px; background: var(--hh-card); font-size: 14px; overflow-wrap: anywhere; }
  .li-banner-watch { border-color: var(--hh-state-watch); }
  .li-banner-good { border-color: var(--hh-state-good); }
  .li-banner-stop { border-color: var(--hh-state-stop); }
  .li-pre { margin: 0; padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); font-family: var(--hh-font-mono, ui-monospace, monospace); font-size: 13px; line-height: 1.5; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 420px; overflow: auto; }
  .li-panel:focus { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .li-confirm { margin: var(--hh-s-12) 0; padding: var(--hh-s-12) var(--hh-s-16); border: 2px solid var(--hh-focus-ring); border-radius: var(--hh-radius-control); font-weight: 700; overflow-wrap: anywhere; }
  .li-summary { margin: 0 0 var(--hh-s-12); font-weight: 600; }

  @media (max-width: 900px) {
    .li-split, .li-split-month { grid-template-columns: minmax(0, 1fr); }
  }
  /* Four areas do not fit one phone row: the row stays one row and scrolls inside its own strip. */
  @media (max-width: 520px) {
    nav.areas { flex-wrap: nowrap; overflow-x: auto; }
    nav.areas .gl-tab { flex: none; white-space: nowrap; }
  }
  @media (max-width: 640px) {
    .li-tabs { gap: var(--hh-s-4); }
    .li-tab { padding: 0 var(--hh-s-12); font-size: 14px; }
    .li-panel { padding: var(--hh-s-16); }
    .li-hide-narrow { display: none; }
    .li-cards { display: grid; }
    main.li-desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    .li-toolbar .li-field { min-width: 0; flex: 1 1 100%; }
  }
`;
