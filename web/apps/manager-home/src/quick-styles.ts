// Manager Home's quick-actions sheet, beside styles.ts's own. Token only: no
// literal colours anywhere; light and brand (the dark half) both come from the
// design system's tokens.
//
// THE BAR'S CLOTHES. The original's band action wore the house gold until the
// union train ruling stood it down: gold is the "this is the one thing to do
// here" signal, one per panel, and the landing already spends it on the day's
// hero. So the action wears chrome clothes (the original's quiet variant):
// full-strength ink and a border you can see, the accent arriving on hover,
// same words, same one click, no capability traded.
import { css } from "lit";

export const quickStyles = css`
  /* -- the landing's quick-action bar ---------------------------------------------------- */

  .quick-bar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-8);
    margin: 0 0 var(--hh-s-16);
  }
  .quick-run {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-card);
    color: var(--hh-text);
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
  }
  .quick-run .mark {
    display: inline-flex;
    color: var(--hh-text-dim);
  }
  .quick-run:hover {
    border-color: var(--hh-accent-text);
  }
  .quick-run:hover .mark {
    color: var(--hh-accent-text);
  }
  /* A destination this box does not serve is words, never a dead button. */
  .quick-run.quick-absent {
    cursor: default;
    border-style: dashed;
  }
  .quick-absent .not-here {
    font-size: 14px;
    font-weight: 400;
    color: var(--hh-text-dim);
  }

  /* -- the admin's config view ------------------------------------------------------------ */

  .quick-config .qa-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
    margin: 0 0 var(--hh-s-4);
  }
  .qa-name {
    font-size: var(--hh-s-16);
    font-weight: 650;
  }
  .qa-id {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .qa-lead {
    font-size: 14px;
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-16);
    max-width: 62ch;
  }
  .qa-switch {
    min-height: 36px;
    padding: 0 var(--hh-s-16);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font-size: 14px;
    font-weight: 600;
    cursor: pointer;
  }
  .qa-switch[aria-pressed="true"] {
    border-color: var(--brand-fill);
    background: var(--brand-fill);
    color: var(--brand-fill-on);
  }
  /* An off global switch does not erase the per-shape settings: it makes them
     moot. The ticks go still (the browser's own disabled ink) instead of
     hidden, so switching the action back on holds no surprises. */
  .qa-shapes {
    display: flex;
    flex-wrap: wrap;
    gap: var(--hh-s-8);
    margin: var(--hh-s-12) 0 0;
    padding: var(--hh-s-12) 0 0;
    border-top: 1px dashed var(--hh-hairline);
  }
  .qa-shape {
    min-height: 30px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    border: 1px solid var(--hh-control-line);
    background: transparent;
    color: var(--hh-text);
    font-size: 14px;
    cursor: pointer;
  }
  .qa-shape[aria-pressed="true"] {
    border-color: var(--hh-accent-text);
    background: var(--brand-tint);
    font-weight: 600;
  }
  .qa-shape:disabled {
    cursor: not-allowed;
  }
  .qa-foot {
    margin-top: var(--hh-s-16);
    align-items: center;
  }
  .qa-default {
    font-family: var(--hh-font-mono);
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-pill);
    padding: var(--hh-s-4) var(--hh-s-8);
  }
  .qa-saved {
    margin: 0;
  }
`;
