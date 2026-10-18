// HH Drive's own styles: one column, one hand, gloves and sunlight. Every
// value is a token of the design system; nothing here is a literal colour.
// Targets are at least 56 px tall (the design system's own floor is 44), body
// text is never under 16 px, status is a word before it is a colour, and the
// page never scrolls sideways.
import { css } from "lit";

export const driveStyles = css`
  :host {
    display: block;
    min-height: 100vh;
    background: var(--hh-page);
    color: var(--hh-text);
    padding-bottom: calc(var(--hh-s-32) + var(--hh-safe-bottom));
  }
  main {
    max-width: 620px;
    margin: 0 auto;
    padding: var(--hh-s-16) var(--hh-s-16) 0;
    overflow-wrap: anywhere;
    font-size: 16px;
    line-height: 1.45;
  }
  h1 {
    margin: 0 0 var(--hh-s-12);
    font-size: 28px;
    line-height: 1.2;
    font-weight: 700;
  }
  h2, h3 { margin: 0; line-height: 1.25; }
  h3 { font-size: 16px; font-weight: 700; margin-bottom: var(--hh-s-8); }
  p { margin: 0 0 var(--hh-s-8); }
  .dim { color: var(--hh-text-dim); }
  .visually-hidden {
    position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px;
    overflow: hidden; clip: rect(0, 0, 0, 0); white-space: nowrap; border: 0;
  }

  [data-tap] {
    min-height: 56px;
  }

  .card {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16);
    margin-bottom: var(--hh-s-16);
  }

  /* The run sheet. */
  .run-title { font-size: 20px; font-weight: 700; }
  .run-count { color: var(--hh-text-dim); margin: var(--hh-s-4) 0 0; }
  .run-progress { font-weight: 700; margin: var(--hh-s-4) 0 var(--hh-s-12); }
  .run-subtitle { font-size: 14px; color: var(--hh-text-dim); margin: 0 0 var(--hh-s-8); }
  .stop-list { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-12); }
  .stop-item { margin: 0; }
  .stop-row {
    display: grid;
    grid-template-columns: 36px 1fr auto;
    align-items: start;
    gap: var(--hh-s-12);
    width: 100%;
    min-height: 88px;
    padding: var(--hh-s-16);
    margin-bottom: 0;
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-left: 4px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .stop-row.stop-tone-good { border-left-color: var(--hh-state-good); }
  .stop-row.stop-tone-watch { border-left-color: var(--hh-state-watch); }
  .stop-row.stop-tone-stop { border-left-color: var(--hh-state-stop); }
  .stop-row.next {
    border: 2px solid var(--hh-state-watch);
    border-left-width: 4px;
  }
  .stop-row:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .stop-no { font-size: 22px; font-weight: 700; color: var(--hh-text); text-align: center; line-height: 1.2; }
  .stop-main { display: flex; flex-direction: column; gap: var(--hh-s-4); min-width: 0; }
  .stop-who { font-weight: 700; font-size: 18px; display: flex; align-items: center; gap: var(--hh-s-8); flex-wrap: wrap; }
  .next-tag {
    font-size: 12px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: var(--hh-s-2) var(--hh-s-8);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-state-watch);
    color: var(--hh-state-watch);
  }
  .stop-where { color: var(--hh-text-dim); font-size: 14px; }
  .stop-meta { font-weight: 700; font-size: 15px; }
  .stop-gate { color: var(--hh-text-dim); font-size: 14px; }

  .status {
    display: inline-block;
    padding: var(--hh-s-4) var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 2px solid var(--hh-control-line);
    font-size: 14px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }
  .status-good { color: var(--hh-state-good); border-color: var(--hh-state-good); }
  .status-watch { color: var(--hh-state-watch); border-color: var(--hh-state-watch); }
  .status-stop { color: var(--hh-state-stop); border-color: var(--hh-state-stop); }
  .status-plain { color: var(--hh-text-dim); border-color: var(--hh-control-line); }

  /* The "Loaded at the yard" card. */
  .loaded-at-yard {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
  }
  .loaded-title {
    text-transform: uppercase;
    letter-spacing: 0.06em;
    font-size: 13px;
    font-weight: 700;
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-4);
  }
  .loaded-body { font-weight: 700; margin: 0 0 var(--hh-s-12); }

  .empty-head { font-size: 20px; font-weight: 700; }

  /* A stop's detail. */
  .run-top { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-8); margin-bottom: var(--hh-s-8); }
  .run-top h1 { margin: 0; }
  .run-top-actions { display: flex; align-items: center; gap: var(--hh-s-8); }
  .run-top-actions .btn { min-height: 56px; }
  .stall-strip { border-top: 2px solid var(--hh-state-watch); padding: var(--hh-s-8) var(--hh-s-16); }
  .stall-strip p { margin: 0; font-size: 14px; color: var(--hh-text-dim); }
  .stall-strip p + p { margin-top: var(--hh-s-4); }
  .back, .link-button {
    display: inline-flex;
    align-items: center;
    min-height: 56px;
    padding: 0 var(--hh-s-16);
    margin-bottom: var(--hh-s-8);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font: inherit;
    font-weight: 700;
    text-decoration: none;
    cursor: pointer;
  }
  .back.back-modal { display: inline-flex; }
  .back:focus-visible, .link-button:focus-visible, .phone:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .detail-head { display: flex; justify-content: space-between; align-items: center; gap: var(--hh-s-12); margin-bottom: var(--hh-s-4); }
  .detail-head h1 { margin: 0; }
  .detail-who { font-size: 20px; font-weight: 700; margin: 0; }
  .detail-where { font-size: 16px; font-weight: 600; margin: 0 0 var(--hh-s-4); }
  .detail-note { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); }
  .phone {
    display: inline-flex;
    align-items: center;
    min-height: 56px;
    padding: 0 var(--hh-s-8);
    color: var(--hh-quote-link);
    font-weight: 700;
    text-decoration: underline;
  }
  .signed { font-weight: 700; color: var(--hh-success); }
  .lines { list-style: none; margin: 0; padding: 0; }
  .lines li { display: flex; justify-content: space-between; gap: var(--hh-s-12); padding: var(--hh-s-8) 0; border-bottom: 1px solid var(--hh-line); }
  .lines li:last-child { border-bottom: 0; }
  .line-desc { display: flex; flex-direction: column; }
  .line-sku { color: var(--hh-text-dim); font-size: 14px; }
  .line-qty { font-weight: 700; white-space: nowrap; }

  /* The step tracker: five pills in a row, current one filled. */
  .step-tracker {
    list-style: none;
    margin: 0 0 var(--hh-s-16);
    padding: var(--hh-s-12) var(--hh-s-12);
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    display: grid;
    grid-template-columns: repeat(5, 1fr);
    gap: var(--hh-s-4);
    align-items: center;
  }
  .step {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--hh-s-4);
    font-size: 12px;
    font-weight: 700;
    color: var(--hh-text-dim);
  }
  .step-circle {
    width: 32px;
    height: 32px;
    border-radius: 999px;
    background: transparent;
    border: 2px solid var(--hh-control-line);
    display: grid;
    place-items: center;
    color: var(--hh-text-dim);
  }
  .step-circle-inner { line-height: 1; font-size: 14px; font-weight: 700; }
  .step-label { line-height: 1.2; text-align: center; }
  .step-todo .step-circle { border-color: var(--hh-control-line); color: var(--hh-text-dim); }
  .step-current .step-circle {
    background: var(--brand-fill);
    border-color: var(--brand-fill-on);
    color: var(--brand-fill-on);
  }
  .step-current .step-circle-inner { color: var(--brand-fill-on); }
  .step-current { color: var(--hh-text); }
  .step-done .step-circle {
    background: var(--hh-state-good);
    border-color: var(--hh-state-good);
    color: var(--hh-card);
  }
  .step-done .step-circle-inner { color: var(--hh-card); }
  .step-done { color: var(--hh-state-good); }

  /* The gate and drop card. */
  .gate {
    border: 2px solid var(--hh-state-watch);
    background: var(--hh-row-alt);
  }
  .gate-code {
    font-size: 24px;
    font-weight: 700;
    margin: 0 0 var(--hh-s-8);
  }
  .gate-instructions { font-weight: 600; margin: 0; }

  /* Call row: the call button and the contact line. */
  .call-row { display: flex; flex-direction: column; gap: var(--hh-s-8); }
  .contact-name { margin: 0; display: flex; align-items: center; flex-wrap: wrap; gap: var(--hh-s-4); }
  .contact-name a { color: var(--hh-quote-link); text-decoration: underline; font-weight: 700; }

  /* Items: a thumbnail and a name + qty + position row. */
  .items { list-style: none; margin: 0; padding: 0; }
  .items li { display: grid; grid-template-columns: 56px 1fr; gap: var(--hh-s-12); padding: var(--hh-s-8) 0; border-bottom: 1px solid var(--hh-line); align-items: start; }
  .items li:last-child { border-bottom: 0; }
  .item-thumb {
    width: 56px; height: 56px;
    border-radius: var(--hh-radius-control);
    background: var(--hh-row-alt);
    display: grid;
    place-items: center;
    overflow: hidden;
  }
  .item-thumb-glyph {
    display: grid;
    place-items: center;
    width: 100%; height: 100%;
    font-weight: 700;
    color: var(--hh-card);
    font-size: 14px;
  }
  .item-thumb-stone { background: var(--hh-state-stop); }
  .item-thumb-sand { background: var(--hh-state-watch); }
  .item-thumb-neutral { background: var(--hh-text-dim); }
  .item-main { display: flex; flex-direction: column; gap: var(--hh-s-2); min-width: 0; }
  .item-desc { font-weight: 700; }
  .item-qty { color: var(--hh-text-dim); }
  .item-pos { color: var(--hh-state-watch); font-weight: 700; font-size: 14px; }

  /* The moves. Big, full width, one hand. */
  .actions { display: flex; flex-direction: column; gap: var(--hh-s-12); margin-bottom: var(--hh-s-16); }
  .big, button.big, a.big {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    min-height: 64px;
    font-size: 18px;
    font-weight: 700;
    text-decoration: none;
    box-sizing: border-box;
  }
  button.big:disabled { border: 2px solid var(--hh-control-line); }
  /* The board's second door: an outlined red, lighter than the yellow beside it. */
  .btn-door-fail { background: transparent; color: var(--hh-danger); border: 2px solid var(--hh-danger); }
  .btn-door-fail:hover:not(:disabled) { background: var(--hh-row-alt); }
  button.big + button.big { margin-top: var(--hh-s-12); }
  /* The navigate button is a link to maps: it lives in the stop view as a
   * full-width primary button outside the actions row. */
  a.navigate.btn.btn-primary.big { text-align: center; text-decoration: none; margin-bottom: var(--hh-s-12); }

  /* The bottom primary action container. */
  .sticky-action {
    margin-top: var(--hh-s-8);
    padding: var(--hh-s-8) 0 calc(var(--hh-safe-bottom) + var(--hh-s-8));
  }
  .sticky-action .btn.big { margin: 0; }

  /* Messages. */
  .alert { padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); border: 2px solid; font-weight: 600; }
  .alert-error { color: var(--hh-danger); border-color: var(--hh-danger); background: var(--hh-card); }
  .alert-ok { color: var(--hh-success); border-color: var(--hh-success); background: var(--hh-card); }

  .theme-button { min-height: 44px; }

  /* Proof of delivery. */
  .field-label { display: block; font-weight: 700; margin-bottom: var(--hh-s-8); }
  .text-field {
    width: 100%;
    min-height: 56px;
    padding: 0 var(--hh-s-16);
    margin-bottom: var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-field);
    color: var(--hh-text);
    font: inherit;
    font-size: 18px;
    box-sizing: border-box;
  }
  .text-field:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .confirm { display: grid; grid-template-columns: 32px 1fr; gap: var(--hh-s-12); align-items: start; margin-bottom: var(--hh-s-16); }
  .confirm-box {
    appearance: none;
    width: 32px;
    height: 32px;
    margin: 0;
    border-radius: var(--hh-s-8);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-field);
    display: grid;
    place-items: center;
    cursor: pointer;
  }
  .confirm-box:checked { background: var(--brand-fill); border-color: var(--brand-fill-on); }
  .confirm-box:checked::after { content: "\\2713"; color: var(--brand-fill-on); font-size: 22px; font-weight: 800; line-height: 1; }
  .confirm-box:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .hint { color: var(--hh-text-dim); margin: var(--hh-s-8) 0 var(--hh-s-12); }
  .small { font-size: 14px; }
  .section-label { text-transform: uppercase; letter-spacing: 0.06em; color: var(--hh-text-dim); font-size: 13px; font-weight: 700; margin: 0 0 var(--hh-s-8); }
  .photo-tiles { list-style: none; margin: 0 0 var(--hh-s-8); padding: 0; display: grid; grid-template-columns: repeat(3, 1fr); gap: var(--hh-s-8); }
  .photo-tile, .photo-tile-add { margin: 0; }
  .photo-tile {
    position: relative;
    aspect-ratio: 1;
    overflow: hidden;
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: var(--hh-card);
    display: grid;
    place-items: end stretch;
  }
  .thumb { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
  .photo-tile-word {
    position: relative;
    padding: var(--hh-s-4) var(--hh-s-8);
    font-size: 12px;
    font-weight: 700;
    color: var(--hh-success);
    background: var(--hh-card);
    text-align: center;
  }
  .photo-add {
    display: flex;
    align-items: center;
    justify-content: center;
    flex-direction: column;
    gap: var(--hh-s-4);
    height: 100%;
    aspect-ratio: 1;
    min-height: 96px;
    text-align: center;
    font-size: 14px;
    border-radius: var(--hh-radius-control);
    border: 2px dashed var(--hh-control-line);
    background: var(--hh-card);
    font-weight: 700;
    cursor: pointer;
  }
  .photo-add:focus-within { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .photo-plus { font-size: 28px; line-height: 1; }
  .text-area { padding: var(--hh-s-12) var(--hh-s-16); min-height: 112px; resize: vertical; }
  .signature-note { margin: 0 0 var(--hh-s-12); font-size: 14px; }
  .could-not { color: var(--hh-danger); border-color: var(--hh-danger); background: transparent; margin-bottom: var(--hh-s-16); }
  .could-not:hover:not(:disabled) { background: var(--hh-row-alt); }
  .handback { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); border: 2px solid var(--hh-state-stop); }
  .handback-why { font-weight: 700; color: var(--hh-state-stop); }
  .handback-words { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); margin: var(--hh-s-12) 0 0; }
  .recap { list-style: none; margin: var(--hh-s-12) 0; padding: 0; border-top: 1px solid var(--hh-line); }
  .recap li { padding: var(--hh-s-8) 0; border-bottom: 1px solid var(--hh-line); }
  .recap li:last-child { border-bottom: 0; }
  .recap-table { display: grid; gap: var(--hh-s-8); margin-bottom: var(--hh-s-16); }
  .recap-row { display: flex; justify-content: space-between; gap: var(--hh-s-12); margin: 0; padding: var(--hh-s-4) 0; border-bottom: 1px solid var(--hh-line); }
  .recap-label { color: var(--hh-text-dim); }
  .recap-value { font-weight: 700; }
  .recap-next {
    border-left: 4px solid var(--hh-state-good);
  }
  .recap-next-title { font-weight: 700; font-size: 18px; margin: 0 0 var(--hh-s-4); }
  .blocked { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); border: 2px solid var(--hh-control-line); margin: 0; }
  .run-sheet-end { margin-top: var(--hh-s-8); }
  .card .btn + .hint, .card .big + .big { margin-top: var(--hh-s-12); }

  /* The could not deliver sheet. */
  .fail-backdrop { pointer-events: none; user-select: none; }
  .reasons { list-style: none; padding: 0; margin: 0; display: grid; gap: var(--hh-s-8); }
  .reason {
    display: grid;
    grid-template-columns: 32px 1fr;
    gap: var(--hh-s-12);
    align-items: center;
    min-height: 56px;
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 2px solid var(--hh-control-line);
    border-radius: var(--hh-radius-control);
    background: transparent;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
    width: 100%;
  }
  .reason:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .reason-selected {
    background: var(--hh-row-alt);
    border-color: var(--brand-fill);
  }
  .reason-radio {
    width: 24px;
    height: 24px;
    margin: 0;
    border-radius: 999px;
    border: 2px solid var(--hh-control-line);
    background: var(--hh-field);
    display: grid;
    place-items: center;
    cursor: pointer;
  }
  .reason-radio-dot {
    display: none;
    width: 8px;
    height: 8px;
    border-radius: 999px;
    background: var(--brand-fill-on);
  }
  .reason-selected .reason-radio {
    background: var(--brand-fill);
    border-color: var(--brand-fill-on);
  }
  .reason-selected .reason-radio-dot { display: block; }
  .reason-text { font-weight: 700; }
  .other-note { display: block; margin-top: var(--hh-s-8); }
  .other-note .text-field { margin-bottom: 0; }
  .reason-echo { display: block; margin-top: var(--hh-s-4); }
`;
