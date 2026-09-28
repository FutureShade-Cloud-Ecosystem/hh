// list-detail: a striped master list and the detail pane beside it. The work
// pattern for registers, catalogues and queues: pick from the list, read or
// act on the detail.
//
// Penpot mapping: the Penpot component named hh-list-detail, one-to-one with
// the element tag below.

import { css, defineElement, html, HHElement, type Template } from '../render.js';
import { baseStyles, row } from '../primitives.js';
import { kitShell, stateStyles, type ButtonSpec, type KitState } from '../state.js';

const styles = baseStyles + stateStyles + css`
  .split {
    display: grid;
    /* minmax(0, ..) so the panes shrink and wrap instead of pushing past the
       card they sit on; the container query stacks them when the kit itself
       is too narrow to carry both panes. */
    grid-template-columns: minmax(0, 5fr) minmax(0, 7fr);
    gap: var(--hh-s-16);
    align-items: start;
  }
  @container (max-width: 519px) {
    .split { grid-template-columns: minmax(0, 1fr); }
  }
  .master {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    overflow: hidden;
    min-width: 0;
  }
  ul.rows { list-style: none; margin: 0; padding: 0; }
  .detail {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-20);
    display: grid;
    gap: var(--hh-s-12);
    min-width: 0;
  }
  .detail h3 { margin: 0; color: var(--hh-text); font-size: var(--hh-s-20); }
  dl.facts { margin: 0; display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-8) var(--hh-s-24); }
  dl.facts dt { color: var(--hh-text-dim); }
  dl.facts dd { margin: 0; color: var(--hh-text); overflow-wrap: anywhere; }
  .row .cell-main { color: var(--hh-text); min-width: 0; overflow-wrap: anywhere; }
  .row .cell-sub { color: var(--hh-text-dim); margin-inline-start: auto; min-width: 0; overflow-wrap: anywhere; }
`;

const ITEMS: ReadonlyArray<readonly [string, string]> = [
  ['North yard, bay 4', 'pallets: 18'],
  ['North yard, bay 5', 'pallets: 6'],
  ['South yard, bay 1', 'pallets: 31'],
];

export class ListDetailKit extends HHElement {
  static properties = { state: { attribute: true } };
  static styles = styles;
  state: KitState = 'ready';

  private body(): Template {
    const rows = ITEMS.map(([name, sub], i) =>
      row(
        html`<span class="cell-main">${name}</span><span class="cell-sub">${sub}</span>`,
        { alt: i % 2 === 1, selected: i === 1 },
      ),
    );
    return html`<div class="split">
      <div class="master"><ul class="rows">${rows}</ul></div>
      <div class="detail">
        <h3>North yard, bay 5</h3>
        <dl class="facts">
          <dt>Contents</dt><dd>Charcoal paver, 60mm</dd>
          <dt>On hand</dt><dd>6 pallets</dd>
          <dt>Reserved</dt><dd>1 pallet</dd>
        </dl>
      </div>
    </div>`;
  }

  private actions(): ButtonSpec[] {
    return [
      { label: 'Open detail', kind: 'primary', action: 'open' },
      { label: 'Assign crew', kind: 'secondary', action: 'assign' },
    ];
  }

  render(): Template {
    return kitShell({
      kit: 'hh-list-detail',
      title: 'List and detail',
      state: this.prop("state") as KitState,
      body: this.body(),
      actions: this.actions(),
    });
  }
}

defineElement('hh-list-detail', ListDetailKit);
