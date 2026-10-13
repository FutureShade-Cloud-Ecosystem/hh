// Pro Field's own sheet, over the design system's tokens: the card stack the
// surfaces read from, the tappable order rows, the journey timeline, the form
// rows. Nothing here invents a colour: the platform's stage and status tokens
// carry every state, and the words carry them again (shape and weight), so
// colour never acts alone.
import { css } from "lit";

export const fieldStyles = css`
  :host {
    display: block;
    min-height: 100vh;
    background: var(--hh-page);
    color: var(--hh-text);
  }
  .stack { display: flex; flex-direction: column; gap: var(--hh-s-16); }
  .topline { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--hh-s-12); }
  h1 { margin: 0; font-size: 1.25rem; }
  .sections { display: flex; flex-wrap: wrap; }

  .card {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-12);
    padding: var(--hh-s-16);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    background: var(--hh-card);
  }
  .card h2 { margin: 0; font-size: 1.125rem; }
  .card h3 { margin: 0; font-size: 0.875rem; font-weight: 700; letter-spacing: 0.04em; text-transform: uppercase; color: var(--hh-text-dim); }

  .titlerow { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-12); }
  .promised { display: flex; align-items: baseline; gap: var(--hh-s-8); margin: 0; }
  .promised .when { font-size: 1.125rem; font-weight: 700; }
  .facts { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--hh-s-12); margin: 0; }
  .late {
    margin: 0;
    padding: var(--hh-s-8) var(--hh-s-12);
    border: 1px solid var(--hh-state-stop);
    border-radius: var(--hh-radius-control);
    color: var(--hh-state-stop);
  }
  p.late { font-size: 0.875rem; }
  .dim { color: var(--hh-text-dim); }

  /* The orders list: one tappable row per order. */
  .orows { list-style: none; margin: 0; padding: 0; border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); overflow: hidden; }
  .orow { border-bottom: 1px solid var(--hh-line); }
  .orow:nth-child(even) { background: var(--hh-row-alt); }
  .orow:last-child { border-bottom: 0; }
  .rowbtn {
    display: flex;
    flex-direction: column;
    gap: var(--hh-s-4);
    width: 100%;
    padding: var(--hh-s-12) var(--hh-s-16);
    border: 0;
    background: transparent;
    color: inherit;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .rowbtn:focus-visible { outline: 2px solid var(--hh-focus-ring); outline-offset: -2px; }
  .cell { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-8); }
  .cell .total { margin-inline-start: auto; }
  .name { font-weight: 600; }

  /* The journey. Every step is rendered, not only the ones that happened, so
     the screen answers "what is left" and not only "what happened": the eye
     lands on the live step first. Done steps carry a filled dot, the live one
     a ringed dot, the rest hollow; the words and the shape carry the state
     beside the colour. */
  .timeline { list-style: none; margin: 0; padding: 0; }
  .tstep { display: flex; gap: var(--hh-s-12); }
  .rail { display: flex; flex-direction: column; align-items: center; width: 22px; flex: none; padding-top: var(--hh-s-4); }
  .dot {
    width: 14px;
    height: 14px;
    flex: none;
    border-radius: var(--hh-radius-pill);
    border: 2px solid var(--hh-line);
    background: transparent;
  }
  .rail-line { width: 2px; flex: 1; min-height: var(--hh-s-20); background: var(--hh-line); margin: var(--hh-s-2) 0; }
  .tstep.done .dot { background: var(--hh-state-good); border-color: var(--hh-state-good); }
  .tstep.done .rail-line { background: var(--hh-state-good); opacity: 0.45; }
  .tstep.current .dot { border-color: var(--hh-info); background: var(--hh-card); box-shadow: inset 0 0 0 3px var(--hh-info); }
  .tbody { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--hh-s-8); padding-bottom: var(--hh-s-16); }
  .tstep:last-child .tbody { padding-bottom: 0; }
  .twords { font-size: 0.9375rem; }
  .tstep.current .twords { font-weight: 700; }
  .tstep.upcoming .twords { color: var(--hh-text-dim); }
  .tday { font-size: 0.75rem; }

  /* The items. */
  .lines { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--hh-s-8); }
  .line { display: flex; flex-direction: column; gap: var(--hh-s-2); border-bottom: 1px solid var(--hh-line); padding-bottom: var(--hh-s-8); }
  .line:last-child { border-bottom: 0; padding-bottom: 0; }
  .linehead { display: flex; gap: var(--hh-s-12); justify-content: space-between; }
  .totals { display: flex; flex-wrap: wrap; gap: var(--hh-s-16); margin: 0; padding-top: var(--hh-s-8); border-top: 1px solid var(--hh-line); }

  /* Form rows (the wiring pass sets every control's value; nothing is bound). */
  .form { display: flex; flex-direction: column; gap: var(--hh-s-12); }
  .form label { display: flex; flex-direction: column; gap: var(--hh-s-4); font-size: 0.75rem; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: var(--hh-text-dim); }
  .form .control { width: 100%; min-height: var(--hh-h-input); padding: var(--hh-s-8) var(--hh-s-12); }
  textarea.control { resize: vertical; min-height: 5.5rem; line-height: 1.35; }
  .formrow { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-12); }
  .formrow .grow { flex: 1; }
  .notice { margin: 0; color: var(--hh-state-good); }
  .fault { margin: 0; color: var(--hh-danger); }
  .readonly-note { margin: 0; white-space: pre-wrap; }
`;
