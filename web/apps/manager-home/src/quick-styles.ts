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
`;
