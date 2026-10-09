// The ledger slice's own styles. Every value is a token of the design system;
// nothing here is a literal colour. Status is a word before it is a colour,
// figures sit in tabular columns and line up on the right, and a wide table
// scrolls inside its own card so the page never scrolls sideways. Class names
// carry a gl- prefix so the slice shares an element with the receivables
// slice without either's rules reaching the other.
import { css } from "lit";

export const glStyles = css`
  .gl-head { margin-bottom: var(--hh-s-16); }
  .gl-head h1 { margin: 0; font-size: 28px; line-height: 1.2; font-weight: 700; }
  .gl-dim { color: var(--hh-text-dim); }
  .gl-head p { margin: var(--hh-s-4) 0 0; }

  .gl-tabs { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .gl-tab { min-height: 44px; border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); padding: 0 var(--hh-s-20); }
  .gl-tab[aria-current="true"] { background: var(--brand-fill, var(--hh-accent)); color: var(--brand-fill-on, var(--hh-accent-ink)); border-color: var(--brand-fill-on, var(--hh-accent-ink)); font-weight: 700; }
  .gl-count { font-variant-numeric: tabular-nums; margin-inline-start: var(--hh-s-8); }

  .gl-split { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--hh-s-16); align-items: start; }
  .gl-panel { background: var(--hh-card); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); padding: var(--hh-s-20); min-width: 0; }
  .gl-panel h2 { margin: 0; font-size: 20px; line-height: 1.25; font-weight: 700; }
  .gl-panel h3 { margin: 0; font-size: 14px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-accent-text); font-weight: 700; }
  .gl-panel-head { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-8) var(--hh-s-16); margin-bottom: var(--hh-s-12); }
  .gl-waiting { padding: var(--hh-s-2) var(--hh-s-12); border: 1px solid var(--hh-state-watch); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; color: var(--hh-state-watch); }

  .gl-alert { border-radius: var(--hh-radius-control); padding: var(--hh-s-12) var(--hh-s-16); margin: 0 0 var(--hh-s-16); border: 1px solid var(--hh-line); background: var(--hh-card); }
  .gl-alert-error { border-color: var(--hh-state-stop); border-inline-start-width: 4px; background: color-mix(in srgb, var(--hh-state-stop) 12%, var(--hh-card)); }
  .gl-alert-ok { border-color: var(--hh-state-good); border-inline-start-width: 4px; }

  /* The queue: one button per request, the kind a coloured edge and a word. */
  .gl-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-8); }
  .gl-row { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-12); width: 100%; text-align: start; min-height: 56px; padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-line); border-inline-start-width: 4px; border-radius: var(--hh-radius-control); }
  .gl-row[aria-current="true"] { background: var(--hh-row-alt); outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .gl-kind-quote { border-inline-start-color: var(--hh-state-stop); }
  .gl-kind-journal { border-inline-start-color: var(--hh-state-watch); }
  .gl-kind-freight { border-inline-start-color: var(--hh-state-good); }
  .gl-row-main { display: grid; gap: var(--hh-s-4); min-width: 0; }
  .gl-row-title { font-weight: 700; }
  .gl-row-detail { display: block; font-size: 14px; line-height: 1.45; color: var(--hh-text-dim); overflow-wrap: anywhere; }
  .gl-row-own { font-size: 13px; font-weight: 700; color: var(--hh-text); }
  .gl-row-own::before { content: ""; display: inline-block; width: 8px; height: 8px; margin-inline-end: var(--hh-s-8); border-radius: var(--hh-radius-pill); background: var(--hh-state-stop); }
  .gl-pill { flex: none; padding: var(--hh-s-2) var(--hh-s-12); border: 1px solid var(--hh-text); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; }
  .gl-empty { padding: var(--hh-s-24) var(--hh-s-8); display: grid; gap: var(--hh-s-4); }
  .gl-empty strong { font-size: 18px; }

  /* The floor request's figures, read out of the core's own sentence. */
  .gl-tiles { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--hh-s-8); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .gl-tiles div { padding: var(--hh-s-12); border: 1px solid var(--hh-line); border-radius: var(--hh-radius-control); min-width: 0; }
  .gl-tiles div:first-child { grid-column: span 2; }
  .gl-tiles dt { font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; }
  .gl-tiles dd { margin: var(--hh-s-4) 0 0; font-size: 20px; font-weight: 700; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .gl-tiles dd.gl-stop { color: var(--hh-state-stop); }
  .gl-later { display: inline-block; padding: 0 var(--hh-s-8); border: 1px dashed var(--hh-text-dim); border-radius: var(--hh-radius-pill); font-size: 12px; letter-spacing: 0.08em; text-transform: uppercase; color: var(--hh-text-dim); font-weight: 700; vertical-align: middle; }
  .gl-later-line { margin: var(--hh-s-12) 0 0; font-size: 14px; }
  .gl-nowrap { white-space: nowrap; }
  .gl-actions-spaced { margin-top: var(--hh-s-16); }

  /* The decision. */
  .gl-fact { margin: 0 0 var(--hh-s-8); font-weight: 600; overflow-wrap: anywhere; }
  .gl-figure { margin: 0 0 var(--hh-s-12); font-size: 18px; }
  .gl-choices { display: grid; gap: var(--hh-s-8); margin: var(--hh-s-16) 0; }
  .gl-choice { display: grid; gap: var(--hh-s-4); width: 100%; text-align: start; padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-card); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); }
  .gl-choice { position: relative; padding-inline-start: calc(var(--hh-s-16) + 28px); }
  .gl-choice::before { content: ""; position: absolute; inset-inline-start: var(--hh-s-16); top: var(--hh-s-16); width: 16px; height: 16px; border-radius: var(--hh-radius-pill); border: 2px solid var(--hh-control-line); box-sizing: border-box; }
  .gl-choice[aria-checked="true"] { border-color: var(--hh-focus-ring); border-width: 2px; background: color-mix(in srgb, var(--hh-accent) 16%, var(--hh-card)); }
  .gl-choice[aria-checked="true"]::before { border-color: var(--hh-text); background: radial-gradient(circle, var(--hh-text) 0 4px, transparent 5px); }
  .gl-choice-held::before { border-style: dashed; }
  .gl-choice-title { font-weight: 700; }
  .gl-choice-line { font-size: 14px; color: var(--hh-text-dim); }
  .gl-choice-held { background: var(--hh-field); color: var(--hh-text-dim); border-style: dashed; }
  .gl-field { display: grid; gap: var(--hh-s-4); font-size: 14px; color: var(--hh-text-dim); font-weight: 600; margin-bottom: var(--hh-s-12); }
  .gl-field textarea { font: inherit; font-size: 16px; padding: var(--hh-s-12); background: var(--hh-field); color: var(--hh-text); border: 1px solid var(--hh-control-line); border-radius: var(--hh-radius-control); width: 100%; box-sizing: border-box; }
  .gl-field textarea:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: 2px; }
  .gl-actions { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); align-items: center; margin-bottom: var(--hh-s-12); }
  .gl-note { margin: var(--hh-s-8) 0 0; padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); background: color-mix(in srgb, var(--hh-accent) 18%, var(--hh-card)); color: var(--hh-text); font-size: 14px; }

  /* Journals and the trial balance. */
  .gl-filters { display: flex; flex-wrap: wrap; gap: var(--hh-s-4); }
  .gl-filter { min-height: 36px; padding: 0 var(--hh-s-12); border-radius: var(--hh-radius-pill); background: transparent; color: var(--hh-text); border: 1px solid var(--hh-control-line); font-size: 14px; }
  .gl-filter[aria-pressed="true"] { border-color: var(--hh-focus-ring); font-weight: 700; background: var(--hh-row-alt); }
  .gl-link { white-space: nowrap; background: transparent; border: 0; padding: var(--hh-s-4) 0; min-height: 36px; color: var(--hh-accent-text); font-weight: 700; text-decoration: underline; }
  .gl-facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-4) var(--hh-s-24); margin: 0 0 var(--hh-s-12); }
  .gl-facts dt { color: var(--hh-text-dim); }
  .gl-facts dd { margin: 0; overflow-wrap: anywhere; }
  .gl-scroll { overflow-x: auto; }
  .gl-panel table { width: 100%; border-collapse: collapse; font-size: 15px; }
  .gl-panel th { text-align: start; font-weight: 700; padding: var(--hh-s-8) var(--hh-s-12); border-bottom: 1px solid var(--hh-line); color: var(--hh-text-dim); white-space: nowrap; }
  .gl-panel td { padding: var(--hh-s-12); border-bottom: 1px solid var(--hh-line); vertical-align: middle; }
  .gl-panel th.num, .gl-panel td.num { text-align: end; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .gl-panel tr[aria-current="true"] td { background: var(--hh-row-alt); }
  .gl-panel tr.gl-total td { border-top: 2px solid var(--hh-line); border-bottom: 0; }
  .gl-status { display: inline-flex; align-items: center; gap: var(--hh-s-8); padding: var(--hh-s-2) var(--hh-s-12); border-radius: var(--hh-radius-pill); font-size: 13px; font-weight: 700; border: 1px solid var(--hh-line); white-space: nowrap; }
  .gl-status::before { content: ""; width: 8px; height: 8px; border-radius: var(--hh-radius-pill); background: currentColor; }
  .gl-status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .gl-status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .gl-status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .gl-status-plain { color: var(--hh-text-dim); }

  @media (max-width: 900px) {
    .gl-tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); }
    .gl-split { grid-template-columns: minmax(0, 1fr); }
  }
  .gl-name-narrow { display: none; }
  /* A phone shows the figures: the type column steps aside so debit and credit stay in view. */
  @media (max-width: 520px) {
    .gl-col-type, .gl-col-name { display: none; }
    .gl-name-narrow { display: block; color: var(--hh-text-dim); font-size: 12px; }
    .gl-panel { padding: var(--hh-s-16); }
    .gl-panel table { font-size: 13px; }
    .gl-panel th, .gl-panel td { padding: var(--hh-s-8) var(--hh-s-4); }
    .gl-panel th { white-space: normal; }
    main.gl-desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
  }
`;
