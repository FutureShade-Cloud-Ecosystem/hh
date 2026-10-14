// The conflict cards: one element per outcome and the list that holds them.
// The sync engine answers every stale write with one of three outcomes and the
// shell has to show each of them, never silently (plan B.3 guarantee 4): the
// server wins outcome as "Superseded on the server", the last write wins merge
// as a merge report, and the manual outcome as a review card that waits for a
// person. A refused operation carries the module's own words. The words are
// the r4 offline board's (build_r4_offline.py, build_r4_shell.py: the conflict
// cards board), keyed by the store's card kinds in web/packages/offline-store.
//
// Purely presentational: the shell maps the store's open cards onto `cards`
// (one plain row per card, the shape below) and acts on the dismiss control.
// Dismiss is the only exit a card has here, and the store keeps the row: a
// card is dismissed, never deleted (the offline store's own invariant).
//
// The elements carry no listeners: every control rides a data-action name and
// the host's one click listener reads it, the kit pattern the apps and the
// chrome already use.

import { css, html, nothing, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import { stateStyles } from '@hardscapehouse/design-system/src/state.js';

/** The store's card kinds (offline-store cards.ts). */
export type ShellCardKind = 'superseded' | 'merge_report' | 'review' | 'rejected';

/** One open card, mapped from the store's Card by the shell. */
export interface ShellCardInput {
  id: string;
  kind: ShellCardKind;
  scope: string;
  policy: string | null;
  serverState: Record<string, unknown>;
  clientPayload: Record<string, unknown>;
  error: string | null;
}

export interface ShellCardView {
  /** The small caps line naming the outcome family and the scope. */
  tag: string;
  heading: string;
  body: string;
}

/** The scope's own words, for the tag and the body. A scope the map does not
 * name falls back to the change, never to a code. */
const SCOPE_WORDS: Record<string, { label: string; subject: string }> = {
  pick_tasks: { label: 'pick tasks', subject: 'the ticket' },
  shortages: { label: 'shortages', subject: 'the shortage' },
  receiving: { label: 'receiving', subject: 'the receipt' },
  weights: { label: 'weights', subject: 'the skid count' },
  inventory_lookup: { label: 'inventory lookup', subject: 'the stock answer' },
  runs: { label: 'runs', subject: 'the run' },
  stops: { label: 'stops', subject: 'the stop' },
  pod: { label: 'proof of delivery', subject: 'the proof of delivery' },
  attachments: { label: 'attachments', subject: 'the photo' },
};

const scopeWords = (scope: string): { label: string; subject: string } =>
  SCOPE_WORDS[scope] ?? { label: scope, subject: 'the change' };

/** One fact the server row carries, for the card's "what the server holds"
 * line. Words a person can read: the customer or subject words the row
 * carries, never an id alone. */
function serverFact(serverState: Record<string, unknown>): string {
  const candidates = ['customer_name', 'description', 'order_no', 'run_no', 'received_by'];
  for (const key of candidates) {
    const value = serverState[key];
    if (typeof value === 'string' && value !== '') return value;
  }
  const stopNo = serverState['stop_no'];
  if (typeof stopNo === 'number' && Number.isFinite(stopNo)) return `stop ${stopNo}`;
  return '';
}

/** What one card says, by its kind. An unknown kind is a defect at the call
 * site: refused loudly, never painted quietly wrong (the banner's own rule). */
export function conflictCardView(input: ShellCardInput): ShellCardView {
  const scope = scopeWords(input.scope);
  switch (input.kind) {
    case 'superseded':
      return {
        tag: `Server wins · ${scope.label}`,
        heading: 'Superseded on the server',
        body: `${cap(scope.subject)} moved on the server while you were offline. Your queued change was dropped; the server row is shown.`,
      };
    case 'merge_report':
      return {
        tag: `Merge · ${scope.label}`,
        heading: 'Merged with a newer change',
        body: `Your change merged with the server's newer row, field by field: the fields you sent landed, the server's other fields stand.`,
      };
    case 'review':
      return {
        tag: `Manual · ${scope.label}`,
        heading: "Needs a person's review",
        body: `${cap(scope.subject)} waits for a person: it never merges on its own. The right role confirms it before anyone else hears.`,
      };
    case 'rejected':
      return {
        tag: `Refused · ${scope.label}`,
        heading: 'Not accepted',
        body: input.error !== null && input.error !== '' ? input.error : 'The server refused the change.',
      };
    default:
      throw new Error(
        `hh-shell-conflict-card was given the unknown card kind ${JSON.stringify(input.kind)}; ` +
          `expected one of: superseded, merge_report, review, rejected`,
      );
  }
}

const cap = (words: string): string => words.charAt(0).toUpperCase() + words.slice(1);

const styles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  .card {
    background: var(--hh-widget);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16);
    color: var(--hh-text);
  }
  .tag {
    display: block;
    font-size: var(--hh-s-12);
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--hh-text-dim);
    font-weight: 700;
  }
  .heading {
    margin: var(--hh-s-8) 0 0;
    font-size: var(--hh-s-20);
    font-weight: 700;
    color: var(--hh-text);
  }
  .body {
    margin: var(--hh-s-8) 0 0;
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
  }
  .fact {
    margin: var(--hh-s-8) 0 0;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .row {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-8);
    margin-top: var(--hh-s-16);
  }
  .list-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--hh-s-8);
  }
  .list-title {
    margin: 0;
    font-size: var(--hh-s-20);
    font-weight: 700;
    color: var(--hh-text);
  }
  .count {
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
  }
  .rows {
    list-style: none;
    margin: var(--hh-s-8) 0 0;
    padding: 0;
    display: grid;
    gap: var(--hh-s-8);
  }
  .empty {
    margin: var(--hh-s-8) 0 0;
    font-size: var(--hh-s-16);
    color: var(--hh-text-dim);
  }
`;

/** The one template both the single card element and the list render, so the
 * words live in exactly one place. The dismiss control rides a data-action
 * name the shell's one click listener acts on; the card is dismissed, never
 * deleted, which is the store's own rule and not this template's to enforce. */
function conflictCardTemplate(card: ShellCardInput): Template {
  const view = conflictCardView(card);
  const fact = serverFact(card.serverState);
  return html`
    <article class="card" data-card-id=${card.id} data-card-kind=${card.kind}>
      <span class="tag">${view.tag}</span>
      <p class="heading">${view.heading}</p>
      <p class="body">${view.body}</p>
      ${fact !== '' ? html`<p class="fact">The server holds: ${fact}</p>` : nothing}
      <div class="row">
        <button class="btn btn-secondary" type="button" data-action="dismiss-card" data-card-id=${card.id} aria-label=${`Dismiss: ${view.heading}`}>
          Dismiss
        </button>
      </div>
    </article>
  `;
}

export class ShellConflictCard extends HHElement {
  static styles = [baseStyles, stateStyles, styles];

  static properties = {
    ...HHElement.properties,
    card: { attribute: false },
  };

  /** The open card the shell mapped from the store. */
  declare card: ShellCardInput | null;

  constructor() {
    super();
    this.card = null;
  }

  render(): Template {
    if (this.card === null) return html``;
    return conflictCardTemplate(this.card);
  }
}

export class ShellConflictList extends HHElement {
  static styles = [baseStyles, stateStyles, styles];

  static properties = {
    ...HHElement.properties,
    cards: { attribute: false },
  };

  /** The open cards, the store's listOpen() mapped onto ShellCardInput. */
  declare cards: ShellCardInput[];

  constructor() {
    super();
    this.cards = [];
  }

  render(): Template {
    const count = this.cards.length;
    return html`
      <section class="card" role="region" aria-label="Cards waiting for review">
        <div class="list-head">
          <h2 class="list-title">Waiting for review</h2>
          <span class="count">${count === 0 ? nothing : `${count} to review`}</span>
        </div>
        ${count === 0
          ? html`<p class="empty">Nothing waits for review. Conflicts land here when the server answers a queued change.</p>`
          : html` <ol class="rows">${this.cards.map((card) => html`<li>${conflictCardTemplate(card)}</li>`)}</ol> `}
      </section>
    `;
  }
}

defineElement('hh-shell-conflict-card', ShellConflictCard);
defineElement('hh-shell-conflict-list', ShellConflictList);
