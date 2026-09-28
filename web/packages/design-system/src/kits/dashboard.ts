// dashboard: the yard's figures at a glance, on the widget layer. A widget sits
// one step off the page on its own ground, which is why this kit carries the
// ground/widget pair and not the card pair.

import { css, defineElement, html, HHElement, type Template } from '../lit.js';
import { baseStyles, chip } from '../primitives.js';
import { kitShell, stateStyles, type ButtonSpec, type KitState } from '../state.js';

const styles = baseStyles + stateStyles + css`
  .bento {
    background: var(--hh-ground);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-20);
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(min(var(--hh-form-col), 100%), 1fr));
    gap: var(--hh-s-16);
  }
  .widget {
    background: var(--hh-widget);
    border: 1px solid var(--hh-hairline);
    border-radius: var(--hh-radius-widget);
    box-shadow: var(--hh-widget-shadow);
    padding: var(--hh-s-20);
    display: grid;
    gap: var(--hh-s-8);
  }
  .widget-label { color: var(--hh-text-dim); font-size: var(--hh-s-12); }
  .widget-figure {
    color: var(--hh-text);
    font-size: var(--hh-s-40);
    font-variant-numeric: tabular-nums;
    line-height: 1;
  }
  .widget .chip { align-self: start; }
  .widget-note { color: var(--hh-text-dim); margin: 0; }
`;

export class DashboardKit extends HHElement {
  static properties = { state: { attribute: true } };
  static styles = styles;
  state: KitState = 'ready';

  private body(): Template {
    return html`<div class="bento">
      <div class="widget">
        <span class="widget-label">Pallet coverage on hand</span>
        <span class="widget-figure">640 sq ft</span>
        ${chip('on hand', 'good')}
      </div>
      <div class="widget">
        <span class="widget-label">Loads confirmed this week</span>
        <span class="widget-figure">12</span>
        <p class="widget-note">3 awaiting the weighbridge</p>
      </div>
      <div class="widget">
        <span class="widget-label">Pick queue</span>
        <span class="widget-figure">7</span>
        ${chip('2 stalled', 'watch')}
      </div>
    </div>`;
  }

  private actions(): ButtonSpec[] {
    return [
      { label: 'Open pick queue', kind: 'primary', action: 'open-queue' },
      { label: 'Refresh figures', kind: 'secondary', action: 'refresh' },
    ];
  }

  render(): Template {
    return kitShell({
      kit: 'hh-dashboard',
      title: 'Dashboard',
      state: this.prop("state") as KitState,
      body: this.body(),
      actions: this.actions(),
    });
  }
}

defineElement('hh-dashboard', DashboardKit);
