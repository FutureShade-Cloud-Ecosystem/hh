// Ported from hardscapeos_dibbits master 87d7941b app/src/styles/row-stripe.ts; adapted for HH.

import { css } from 'lit';

/**
 * rowStripe: DIB-584. The one alternating-row treatment, for every list in the app.
 *
 * THE PROBLEM IT SOLVES
 * ---------------------
 * "IN ALL LISTS - we need alternating colours for rows, it makes it so much easier to read and
 * follow the row over." There was no shared table primitive to put that in. Every register in this
 * app hand-rolls its own <table> inside its own shadow root: the AR register, the catalogue, the
 * four tables in the purchasing console, the GL accounts and trial balance, order and payment
 * history, bank rec, the audit log, and eighty more: and each one had hand-copied its own row
 * padding, its own border and its own hover. A document stylesheet cannot reach any of them.
 *
 * So the choice was one rule imported everywhere, or ninety copies of a rule. This is the first.
 *
 * WHY :where(): THE LOAD-BEARING DETAIL
 * --------------------------------------
 * Every selector below is wrapped in :where(), which zeroes its specificity. That is not tidiness;
 * it is the only reason this can be dropped into ninety components without auditing ninety files
 * for interaction with it. A plain `tbody tr:nth-child(even)` weighs (0,1,2) and would have BEATEN
 * every one-class row state the app already ships: `tr.selected` in the portal table (0,1,1),
 * `tr.inactive` in the GL chart of accounts and the rule editor: silently killing a selection
 * highlight on every other row. At specificity zero the stripe is the floor: a component's own
 * hover, selected, inactive or fault row wins by existing, in any source order, and no register
 * has to know this file exists in order to keep the states it already had.
 *
 * The two rules below are equal to each other (both zero), so the alternate wins on source order:
 * which is why they are stated in this order and must stay in it.
 *
 * WHY `tbody > tr` AND NOT `tr`
 * -----------------------------
 * A header row and a totals row are not list rows. `thead` and `tfoot` are excluded by the child
 * combinator rather than by a :not() list, so a table that grows a footer later does not quietly
 * acquire a stripe on its total. `nth-child` then counts within the tbody, which is what makes the
 * first data row the unstriped one on every table regardless of how much chrome sits above it.
 *
 * WHY A SHARED CSSResult AND NOT A TOKEN ALONE
 * --------------------------------------------
 * Same reason focusRing is one: a token would carry only the colour, and every component would
 * still write the selector: so a component that wrote `tr:nth-child(odd)` instead would stripe
 * against the grain of every other list in the app, which is worse than no stripe at all. What
 * needed sharing is the SELECTOR plus the declaration. The colours are --hos-row / --hos-row-alt,
 * declared in tokens.css in the :root (dark) and [data-theme="light"] blocks and deliberately NOT
 * in the loud block; no literal may appear in this file.
 */
export const rowStripe = css`
  /*
 * The resting row. This declares "transparent": which is what an un-styled row already is: and
   * it is here on purpose rather than as a no-op: it gives the pair a second half, so a surface
   * that wants a different resting row has to overwrite a NAMED token instead of quietly adding a
   * background nobody can find. Both halves of the stripe are one thing you can grep for.
   */
  :where(tbody > tr) {
    background: var(--hos-row);
  }
  :where(tbody > tr:nth-child(even)) {
    background: var(--hos-row-alt);
  }
`;
