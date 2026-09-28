// guided-flow: a step rail and one step at a time. The HH Pro shell's pattern:
// the proposal flow a contractor walks a homeowner's job through, one decision
// per step, nothing hidden behind Next.

import { css, defineElement, html, HHElement, type Template } from '../lit.js';
import { baseStyles } from '../primitives.js';
import { kitShell, stateStyles, type ButtonSpec, type KitState } from '../state.js';

const styles = baseStyles + stateStyles + css`
  .flow {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: var(--hh-s-24);
    align-items: start;
  }
  ol.rail { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--hh-s-12); }
  .rail-step {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    color: var(--hh-text-dim);
    background: var(--hh-card);
    padding: var(--hh-s-8) var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
  }
  .rail-step .marker {
    width: 24px;
    height: 24px;
    border-radius: var(--hh-radius-pill);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    font-size: var(--hh-s-12);
    border: 2px solid var(--hh-line);
  }
  .rail-step-current { color: var(--hh-text); border: 1px solid var(--hh-focus-ring); }
  .rail-step-current .marker { border-color: var(--hh-focus-ring); color: var(--hh-text); }
  .rail-step-done { color: var(--hh-state-good); }
  .rail-step-done .marker { border-color: var(--hh-state-good); color: var(--hh-state-good); }
  .step-card {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-24);
    display: grid;
    gap: var(--hh-s-12);
  }
  .step-card h3 { margin: 0; color: var(--hh-text); background: var(--hh-card); font-size: var(--hh-s-20); }
  .step-card p { margin: 0; color: var(--hh-text-dim); max-width: var(--hh-measure); }
  .field-label { color: var(--hh-text); font-weight: 600; }
  .field-value {
    font-family: var(--hh-font-mono);
    color: var(--hh-text);
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-12);
    min-height: 44px;
    display: flex;
    align-items: center;
  }
  .field-value:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
`;

const STEPS: ReadonlyArray<readonly [string, string]> = [
  ['done', 'Site address'],
  ['current', 'Materials'],
  ['todo', 'Review and send'],
];

export class GuidedFlowKit extends HHElement {
  static properties = { state: { attribute: true } };
  static styles = styles;
  state: KitState = 'ready';

  private body(): Template {
    const rail = STEPS.map(
      ([kind, label], i) =>
        html`<li class="rail-step rail-step-${kind}"><span class="marker">${i + 1}</span><span>${label}</span></li>`,
    );
    return html`<div class="flow">
      <ol class="rail">${rail}</ol>
      <div class="step-card" tabindex="0">
        <h3>Materials for 14 Alder Court</h3>
        <p>The coverage this job quotes from. The supplier holds these figures, so they re-check when the job reaches the quote desk.</p>
        <span class="field-label">Paver coverage</span>
        <div class="field-value">640 sq ft</div>
      </div>
    </div>`;
  }

  private actions(): ButtonSpec[] {
    return [
      { label: 'Back', kind: 'secondary', action: 'back' },
      { label: 'Next', kind: 'primary', action: 'next' },
    ];
  }

  render(): Template {
    return kitShell({
      kit: 'hh-guided-flow',
      title: 'Guided flow',
      state: this.prop("state") as KitState,
      body: this.body(),
      actions: this.actions(),
    });
  }
}

defineElement('hh-guided-flow', GuidedFlowKit);
