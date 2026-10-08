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
  button.big { width: 100%; min-height: 64px; font-size: 18px; font-weight: 700; }

  /* Messages. */
  .alert { padding: var(--hh-s-12) var(--hh-s-16); border-radius: var(--hh-radius-control); border: 2px solid; font-weight: 600; }
  .alert-error { color: var(--hh-danger); border-color: var(--hh-danger); background: var(--hh-card); }
  .alert-ok { color: var(--hh-success); border-color: var(--hh-success); background: var(--hh-card); }

  .theme-button { min-height: 44px; }
`;
