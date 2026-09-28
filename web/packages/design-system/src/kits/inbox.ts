// inbox: the messages a role receives, newest intent first, and the reading
// pane beside them. Messages report work; they are not a chat.

import { css, defineElement, html, HHElement, type Template } from '../lit.js';
import { baseStyles, chip, row } from '../primitives.js';
import { kitShell, stateStyles, type ButtonSpec, type KitState } from '../state.js';

const styles = baseStyles + stateStyles + css`
  .split {
    display: grid;
    grid-template-columns: minmax(240px, 5fr) 7fr;
    gap: var(--hh-s-16);
    align-items: start;
  }
  .list {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    overflow: hidden;
  }
  ul.rows { list-style: none; margin: 0; padding: 0; }
  .msg-main { display: grid; gap: var(--hh-s-4); }
  .msg-sender { color: var(--hh-text); font-weight: 600; }
  .msg-subject { color: var(--hh-text-dim); }
  .reading {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-20);
    display: grid;
    gap: var(--hh-s-12);
  }
  .reading h3 { margin: 0; color: var(--hh-text); background: var(--hh-card); font-size: var(--hh-s-20); }
  .reading .from { color: var(--hh-text-dim); }
  .reading p { margin: 0; color: var(--hh-text); max-width: var(--hh-measure); }
`;

const MESSAGES: ReadonlyArray<readonly [string, string, string]> = [
  ['Weighbridge', 'Load 4417 needs a ticket check', 'new'],
  ['Quote desk', 'Two lines need dealer pricing', 'new'],
  ['Yard crew', 'Bay 5 restack finished', 'read'],
];

export class InboxKit extends HHElement {
  static properties = { state: { attribute: true } };
  static styles = styles;
  state: KitState = 'ready';

  private body(): Template {
    const items = MESSAGES.map(([sender, subject, status], i) =>
      row(
        html`<span class="msg-main"><span class="msg-sender">${sender}</span><span class="msg-subject">${subject}</span></span>${chip(status, status === 'new' ? 'good' : 'neutral')}`,
        { alt: i % 2 === 1, selected: i === 0 },
      ),
    );
    return html`<div class="split">
      <div class="list"><ul class="rows">${items}</ul></div>
      <div class="reading" tabindex="0">
        <h3>Load 4417 needs a ticket check</h3>
        <span class="from">from Weighbridge</span>
        <p>The ticket total is one pallet short of the truck manifest. Confirm against the load before the driver leaves the gate.</p>
      </div>
    </div>`;
  }

  private actions(): ButtonSpec[] {
    return [
      { label: 'Mark read', kind: 'secondary', action: 'mark-read' },
      { label: 'Open load', kind: 'primary', action: 'open-load' },
    ];
  }

  render(): Template {
    return kitShell({
      kit: 'hh-inbox',
      title: 'Inbox',
      state: this.prop("state") as KitState,
      body: this.body(),
      actions: this.actions(),
    });
  }
}

defineElement('hh-inbox', InboxKit);
