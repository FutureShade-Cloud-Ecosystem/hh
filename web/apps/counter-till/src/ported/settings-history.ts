// Ported from hardscapeos_dibbits master 87d7941b app/src/settings-history.ts; adapted for HH.

import { LitElement, css, html } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { emptyState, emptyStateStyles, faultBox } from './empty-state.js';
import { whenWord } from './field-refusal.js';
import {
  ruleWord,
  valueWord,
  type PolicyChangeRow,
  type SettingsVocabulary,
} from './settings-history-logic.js';
import { rowStripe } from './styles/row-stripe.js';

/**
 * `hos-settings-history`: the change history every settings screen shows behind a disclosure.
 *
 * ── WHY IT IS SHARED, AND WHAT IT REPLACED ──────────────────────────────────────────────────────
 *
 * The dispatch settings screen drew this table as a PRIVATE METHOD, so the two histories that came
 * after it: the counter's and receivables': could only have copied it. Three hand-rolled tables of
 * the same five columns is the shape this repo already has elsewhere and already regrets:
 * _order-edit.ts and _quote-edit.ts carry byte-identical helpers under different names, and drift
 * between them is invisible until somebody diffs the two files. Dispatch was migrated onto this
 * component in the same pass that created it, so there has never been a second copy to keep in step.
 *
 * ── A DISCLOSURE IS THE POINT ───────────────────────────────────────────────────────────────────
 *
 * A history is a look-something-up surface, not a next action. The doctrine's progressive-disclosure
 * rule puts occasional controls behind one, and a table of old values sitting above the controls
 * somebody came here to change would be the screen arguing with itself about what it is for.
 *
 * ── EMPTY IS NOT A FAULT, AND THIS COMPONENT KEEPS THEM APART ───────────────────────────────────
 *
 * "Nothing has been changed yet" and "the history could not be read" are different facts with
 * different remedies, and the dispatch original painted the second one as a bare red line: the
 * exact defect the house helpers exist to end. Empty goes through `emptyState` (quiet prose, no live
 * region), a failure goes through `faultBox` (a bordered panel, role="alert", "Nothing was read",
 * and a retry). Neither is red-versus-not: they differ in SHAPE, so the difference survives a
 * colour-blind reader and a screen reader both.
 *
 * ── IT NEVER PRINTS A STORED VALUE RAW ──────────────────────────────────────────────────────────
 *
 * The dispatch history used to render `${h.before} / ${h.after}` verbatim, so a real row read
 * "Who sees a load's dollar value | ALL_STAFF | MANAGER_UP". The app-wide vocabulary gate could not
 * see it: that gate matches by PROPERTY NAME, and a history row's columns are called `before` and
 * `after`, which say nothing about what is in them. Every cell here goes through the owning screen's
 * own word map (see settings-history-logic.ts), so an unmapped value degrades to itself lowercased
 * rather than SHOUTING, and an absent one says "Not set" rather than leaving a cell that reads as a
 * rendering bug.
 *
 * ── IT EVALUATES NO AUTHORITY AND FETCHES NOTHING ───────────────────────────────────────────────
 *
 * Rows, the fault sentence and the retry all come from the host. The server is what decides who may
 * read a history: all three of these endpoints are admin-gated: and a component that fetched for
 * itself would be a second place to get a branch id, a limit and a refusal wrong.
 */
@customElement('hos-settings-history')
export class HosSettingsHistory extends LitElement {
  static styles = [
    focusRing,
    rowStripe,
    emptyStateStyles,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
        --sh-caption: 12px;
        --sh-body: 14px;
      }
      details {
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card, 12px);
        padding: var(--hos-space-4, 18px);
        margin-bottom: var(--hos-space-3, 14px);
      }
      summary {
        cursor: pointer;
        font-size: var(--sh-caption);
        color: var(--hos-text-dim);
        padding: 6px 0;
      }
      /* Wide content scrolls INSIDE its own box. Five columns of words on a 390px surface must not
         push the settings page sideways under the reader's thumb. */
      .wrap {
        overflow-x: auto;
      }
      table {
        width: 100%;
        border-collapse: collapse;
        font-size: var(--sh-caption);
        margin-top: 6px;
      }
      th,
      td {
        text-align: left;
        padding: 5px 8px;
        border-bottom: 1px solid var(--hos-line);
        /* TOP, not baseline. Four of the five columns wrap, and rows of different heights that
           bottom-align turn a dense table into a ragged one. */
        vertical-align: top;
      }
      /*
       * THE COLUMN WIDTHS ARE MINIMUMS, NOT FIXED SHARES, and they were measured on the narrowest
       * place this panel renders: the counter settings modal, which is roughly half the width of the
 * receivables page. With no minimums the word columns shredded to one word a line: "Every /
 * staff / member" over three rows: while the By column, holding a full display name plus a
       * role in parentheses, stayed on one line and pushed the timestamp out of the panel entirely.
       * A minimum per column lets the browser wrap where wrapping is cheap and keeps the table
       * inside the panel; the scroll container above is the honest fallback for the widths that
       * genuinely do not fit.
       */
      td.rule {
        min-width: 12ch;
      }
      td.v {
        min-width: 9ch;
      }
      td.who {
        min-width: 14ch;
      }
 /* A timestamp is one token to a reader. Breaking a timestamp across two lines
         makes it look like two facts. */
      th.when,
      td.when {
        white-space: nowrap;
      }
      th {
        color: var(--hos-text-dim);
        font-weight: 500;
      }
      tr:last-of-type td {
        border-bottom: 0;
      }
    `,
  ];

 /** The recorded changes, newest first: exactly as all three servers serve them. */
  @property({ attribute: false }) rows: PolicyChangeRow[] = [];

  /** This screen's own words for its knobs and their values. */
  @property({ attribute: false }) vocabulary: SettingsVocabulary = { rule: {}, value: {} };

  /** The server's sentence when the read failed. '' means it did not. */
  @property({ type: String }) fault = '';

  /** Re-runs the host's read. Omitted only where there is genuinely nothing to retry. */
  @property({ attribute: false }) retry?: () => void;

  /** True while the host is re-reading, so the retry cannot be pressed twice. */
  @property({ type: Boolean }) busy = false;

  /**
 * What these rules ARE, in the dealer's terms: "the counter rules", "the receivables rules".
   *
   * It is one noun phrase and not a whole empty-state sentence because the sentence is the same on
   * every settings screen and saying it once here is the point of sharing the component; what
   * legitimately differs is which rules the reader is standing in front of.
   */
  @property({ type: String }) subject = 'these rules';

 /** The disclosure's own label. One or two words: it is chrome, not copy. */
  @property({ type: String }) label = 'Change history';

  render() {
    return html`<details data-testid="settings-history">
      <summary>${this.label}</summary>
      ${this.fault
        ? faultBox('The change history did not load.', this.fault, this.retry, this.busy)
        : this.rows.length === 0
          ? emptyState(
              'Nothing has been changed yet.',
              `Every change to ${this.subject} is recorded here with who made it and what it was before.`,
            )
          : this.table()}
    </details>`;
  }

  private table() {
    return html`<div class="wrap">
      <table>
        <thead>
          <tr>
            <th scope="col">Rule</th>
            <th scope="col">Was</th>
            <th scope="col">Now</th>
            <th scope="col">By</th>
            <th scope="col" class="when">When</th>
          </tr>
        </thead>
        <tbody>
          ${this.rows.map(
            (h) => html`<tr>
              <td class="rule">${ruleWord(this.vocabulary, h.field)}</td>
              <td class="v">${valueWord(this.vocabulary, h.field, h.before)}</td>
              <td class="v">${valueWord(this.vocabulary, h.field, h.after)}</td>
              ${/* `who` is already a person, composed on the server. There is nothing to look up
                   here and nothing to fall back to: the ladder that ends at "somebody" ran before
                   this row was serialised. */ ''}
              <td class="who">${h.who}</td>
              <td class="when">${whenWord(h.when)}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-settings-history': HosSettingsHistory;
  }
}
