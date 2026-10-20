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
  /* The Field frame's phone tab bar is fixed to the foot: the page clears it (the frame's FIELD_TABBAR_CLEARANCE). */
  @media (max-width: 767.98px) {
    :host { padding-bottom: calc(var(--hh-s-64) + var(--hh-s-24) + var(--hh-safe-bottom)); }
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
  .run-progress { font-weight: 700; margin: var(--hh-s-4) 0 var(--hh-s-8); }
  .run-load-order {
    margin: 0 0 var(--hh-s-12);
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    background: var(--hh-row-alt);
    border: 1px solid var(--hh-line);
    color: var(--hh-text-dim);
    font-size: 14px;
  }
  .stop-group { margin-top: var(--hh-s-12); }
  .stop-group-empty { display: none; }
  .stop-group-head {
    margin: 0 0 var(--hh-s-8);
    font-family: var(--hh-font-mono);
    font-size: 12px;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
  }
  .stop-group[data-stop-group="NEXT STOP"] .stop-group-head {
    color: var(--hh-accent-text);
  }
  .stop-group[data-stop-group="NEXT STOP"] .stop-list {
    border-left: 3px solid var(--hh-accent);
    padding-left: var(--hh-s-12);
    background: var(--hh-row-alt);
    border-radius: var(--hh-radius-control);
  }
  .stop-list { list-style: none; margin: 0; padding: 0; border-top: 1px solid var(--hh-line); }
  .stop-list li { border-bottom: 1px solid var(--hh-line); }
  .stop-list li:last-child { border-bottom: 0; }
  .stop-row {
    display: grid;
    grid-template-columns: 40px 1fr auto;
    align-items: center;
    gap: var(--hh-s-12);
    width: 100%;
    min-height: 72px;
    padding: var(--hh-s-12) 0;
    background: transparent;
    border: 0;
    border-radius: 0;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .stop-row:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .stop-no { font-size: 24px; font-weight: 700; color: var(--hh-accent-text); text-align: center; }
  .stop-main { display: flex; flex-direction: column; gap: var(--hh-s-2); min-width: 0; }
  .stop-who { font-weight: 700; }
  .stop-where { color: var(--hh-text-dim); font-size: 14px; }

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

  .empty-head { font-size: 20px; font-weight: 700; }

  /* A stop's detail. */
  .run-top { display: flex; align-items: center; justify-content: space-between; gap: var(--hh-s-8); }
  .run-top h1 { margin: 0; }
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
  .back:focus-visible, .link-button:focus-visible, .phone:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .detail-head { display: flex; justify-content: space-between; align-items: center; gap: var(--hh-s-12); }
  .detail-who { font-size: 20px; font-weight: 700; margin: 0; }
  .detail-where { font-size: 18px; }
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

  /* The moves. Big, full width, one hand. */
  .actions { display: flex; flex-direction: column; gap: var(--hh-s-12); margin-bottom: var(--hh-s-16); }
  button.big { display: block; width: 100%; min-height: 64px; font-size: 18px; font-weight: 700; }
  /* A control that cannot be used yet keeps a visible edge, the sheet's control line (3:1), so it can be found. */
  button.big:disabled { border: 2px solid var(--hh-control-line); }
  /* The board's second door: an outlined red, lighter than the yellow beside it. */
  .btn-door-fail { background: transparent; color: var(--hh-danger); border: 2px solid var(--hh-danger); }
  .btn-door-fail:hover:not(:disabled) { background: var(--hh-row-alt); }
  button.big + button.big { margin-top: var(--hh-s-12); }

  /* Messages. */
  .alert { padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); border: 2px solid; font-weight: 600; }
  .alert-error { color: var(--hh-danger); border-color: var(--hh-danger); background: var(--hh-card); }
  .alert-ok { color: var(--hh-success); border-color: var(--hh-success); background: var(--hh-card); }

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
  }
  .text-field:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .confirm { display: grid; grid-template-columns: 32px 1fr; gap: var(--hh-s-12); align-items: start; margin-bottom: var(--hh-s-16); }
  /* The box is drawn, not the platform's: its edge is the sheet's control line
     (3:1 against the card in both themes) and its tick is the gold fill's ink. */
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
  .section-label { text-transform: uppercase; letter-spacing: 0.06em; color: var(--hh-text-dim); font-size: 14px; }
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
    gap: var(--hh-s-12);
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
  .handback { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); border: 2px solid var(--hh-state-stop); }
  .handback-why { font-weight: 700; color: var(--hh-state-stop); }
  .handback-words { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); background: var(--hh-row-alt); margin-bottom: var(--hh-s-16); }
  .recap { list-style: none; margin: var(--hh-s-12) 0; padding: 0; border-top: 1px solid var(--hh-line); }
  .recap li { padding: var(--hh-s-8) 0; border-bottom: 1px solid var(--hh-line); }
  .recap li:last-child { border-bottom: 0; }
  .blocked { padding: var(--hh-s-12); border-radius: var(--hh-radius-control); border: 2px solid var(--hh-control-line); margin: 0; }
  .run-sheet-end { margin-top: var(--hh-s-8); }
  .card .btn + .hint, .card .big + .big, .sheet-body .btn + .hint, .sheet-body .big + .big { margin-top: var(--hh-s-12); }

  /* Modal sheets for proof of delivery and could not deliver: bottom sheet on phone, centred dialog on tablet. */
  .sheet-scrim {
    position: fixed;
    inset: 0;
    z-index: var(--hh-z-modal);
    background: var(--hh-scrim);
    display: flex;
    align-items: flex-end;
    justify-content: center;
  }
  .sheet {
    width: 100%;
    max-width: 620px;
    max-height: calc(100vh - 96px);
    overflow-y: auto;
    background: var(--hh-card);
    color: var(--hh-text);
    border-top: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card) var(--hh-radius-card) 0 0;
    box-shadow: var(--hh-elev-3);
    padding: var(--hh-s-16) var(--hh-s-16) calc(var(--hh-s-24) + var(--hh-safe-bottom));
    margin: 0;
  }
  .reason-group {
    border: 0;
    margin: 0 0 var(--hh-s-12);
    padding: 0;
    min-width: 0;
  }
  .reason-group > legend {
    padding: 0;
    margin-bottom: var(--hh-s-8);
    font-size: 18px;
    font-weight: 700;
    color: var(--hh-text);
  }
  .reason-list {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-8);
  }
  .reason-row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    min-height: 48px;
    padding: var(--hh-s-8) var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 16px;
    font-weight: 500;
    cursor: pointer;
  }
  .reason-row:has(input:focus-visible) {
    outline: 3px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .reason-radio:focus {
    outline: none;
  }
  .reason-row-selected {
    border: 2px solid var(--hh-accent-text);
    background: var(--hh-row-alt);
    font-weight: 700;
  }
  .reason-radio {
    width: 20px;
    height: 20px;
    margin: 0;
    accent-color: var(--hh-accent-text);
    flex: none;
  }
  .reason-text {
    min-width: 0;
  }
  .sheet-grab {
    width: var(--hh-s-40);
    height: var(--hh-s-4);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-control-line);
    margin: 0 auto var(--hh-s-12);
  }
  .sheet-head {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: var(--hh-s-12);
    margin-bottom: var(--hh-s-12);
  }
  .sheet-titles { min-width: 0; }
  .sheet-titles h1 {
    margin: 0 0 var(--hh-s-4);
    font-size: 24px;
    line-height: 1.2;
  }
  .sheet-titles p { margin: 0; }
  .sheet-close {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-height: 44px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 2px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font: inherit;
    font-size: 14px;
    font-weight: 700;
    cursor: pointer;
    flex: none;
  }
  .sheet-close:focus-visible { outline: 3px solid var(--hh-focus-ring); outline-offset: 2px; }
  .sheet-body { margin-bottom: var(--hh-s-16); }
  .sheet-photos {
    border-top: 1px solid var(--hh-line);
    padding-top: var(--hh-s-12);
  }
  @media (min-width: 768px) {
    .sheet-scrim {
      align-items: center;
      padding: var(--hh-s-24);
    }
    .sheet {
      max-width: 560px;
      max-height: calc(100vh - var(--hh-s-64));
      border: 1px solid var(--hh-line);
      border-radius: var(--hh-radius-card);
      padding: var(--hh-s-24);
    }
    .sheet-grab { display: none; }
  }
`;
