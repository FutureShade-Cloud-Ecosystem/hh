// scan-and-confirm: point a scanner at a load ticket, read the lines back, and
// confirm the load is what came off the truck. One job: check the physical
// load against the ticket, with gloves on, in one hand.

import { css, defineElement, html, HHElement, type Template } from '../lit.js';
import { baseStyles, chip } from '../primitives.js';
import { kitShell, stateStyles, type ButtonSpec, type KitState } from '../state.js';

const styles = baseStyles + stateStyles + css`
  .scan-frame {
    background: var(--hh-card);
    border: 1px dashed var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-20);
    display: grid;
    gap: var(--hh-s-12);
  }
  .scan-frame:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  ul.lines { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-8); }
  .line {
    display: flex;
    justify-content: space-between;
    gap: var(--hh-s-16);
    padding: var(--hh-s-12);
    background: var(--hh-card);
    border-bottom: 1px solid var(--hh-line);
    color: var(--hh-text);
  }
  .line .qty { font-family: var(--hh-font-mono); color: var(--hh-text-dim); }
  .summary {
    display: flex;
    justify-content: space-between;
    padding-top: var(--hh-s-12);
    border-top: 1px solid var(--hh-line);
    color: var(--hh-text);
    background: var(--hh-card);
  }
`;

const LINES: ReadonlyArray<readonly [string, string]> = [
  ['Paver, 60mm, charcoal', '640 sq ft'],
  ['Edge restraint, 8 ft steel', '24 ea'],
  ['Bedding sand, washed', '9 tonne'],
];

export class ScanConfirmKit extends HHElement {
  static properties = { state: { attribute: true } };
  static styles = styles;
  state: KitState = 'ready';

  private body(): Template {
    const rows = LINES.map(
      ([name, qty]) => html`<li class="line"><span class="name">${name}</span><span class="qty">${qty}</span></li>`,
    );
    return html`<div class="scan-frame" tabindex="0">
      ${chip('scanned: load ticket 4417', 'neutral')}
      <ul class="lines">${rows}</ul>
      <div class="summary"><span>3 lines on the ticket</span><span>all priced</span></div>
    </div>`;
  }

  private actions(): ButtonSpec[] {
    return [
      { label: 'Confirm load', kind: 'primary', action: 'confirm' },
      { label: 'Rescan', kind: 'secondary', action: 'rescan' },
    ];
  }

  render(): Template {
    return kitShell({
      kit: 'hh-scan-and-confirm',
      title: 'Scan and confirm',
      state: this.prop("state") as KitState,
      body: this.body(),
      actions: this.actions(),
    });
  }
}

defineElement('hh-scan-and-confirm', ScanConfirmKit);
