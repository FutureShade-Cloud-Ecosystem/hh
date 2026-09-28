// guided-flow: a step rail and one step at a time. The HH Pro shell's pattern:
// the proposal flow a contractor walks a homeowner's job through, one decision
// per step, nothing hidden behind Next.
//
// Penpot mapping: the Penpot component named hh-guided-flow, one-to-one with
// the element tag below.
//
// A rail step that is DONE is not told by colour alone: it carries the check
// icon (a visible, non-colour cue) and a visually hidden word "done" for
// assistive tech, beside the green. The CURRENT step carries
// aria-current="step" beside its ring. Asserted in test/kits.test.mjs.

import { css, defineElement, html, HHElement, type Template } from '../render.js';
import { baseStyles } from '../primitives.js';
import { kitShell, stateStyles, type ButtonSpec, type KitState } from '../state.js';
import { icon } from '../state-icons.js';

const styles = baseStyles + stateStyles + css`
  .flow {
    display: grid;
    grid-template-columns: max-content minmax(0, 1fr);
    gap: var(--hh-s-24);
    align-items: start;
  }
  @container (max-width: 519px) {
    .flow { grid-template-columns: minmax(0, 1fr); }
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
  .rail-step > span:not(.marker):not(.visually-hidden) { min-width: 0; overflow-wrap: anywhere; }
  .rail-step .marker {
    width: 24px;
    height: 24px;
    border-radius: var(--hh-radius-pill);
    display: inline-flex;
    align-items: center;
    justify-content: center;
    flex: none;
    font-size: var(--hh-s-12);
    border: 2px solid var(--hh-line);
  }
  .rail-step .marker .icon { width: 14px; height: 14px; }
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
    min-width: 0;
  }
  .step-card h3 { margin: 0; color: var(--hh-text); font-size: var(--hh-s-20); }
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

type StepKind = 'done' | 'current' | 'todo';
type Step = readonly [kind: StepKind, label: string];

const STEPS: ReadonlyArray<Step> = [
  ['done', 'Site address'],
  ['current', 'Materials'],
  ['todo', 'Review and send'],
];

function railStep([kind, label]: Step, index: number): Template {
  if (kind === 'done') {
    // Colour never encodes state alone: the check icon is the visible cue and
    // the visually hidden word is the AT cue; the green rides beside them.
    return html`<li class="rail-step rail-step-done">
      <span class="marker" aria-hidden="true">${icon('check')}</span>
      <span>${label}</span>
      <span class="visually-hidden">(done)</span>
    </li>`;
  }
  if (kind === 'current') {
    return html`<li class="rail-step rail-step-current" aria-current="step">
      <span class="marker" aria-hidden="true">${index + 1}</span>
      <span>${label}</span>
      <span class="visually-hidden">(current step)</span>
    </li>`;
  }
  return html`<li class="rail-step">
    <span class="marker" aria-hidden="true">${index + 1}</span>
    <span>${label}</span>
  </li>`;
}

export class GuidedFlowKit extends HHElement {
  static properties = { state: { attribute: true } };
  static styles = styles;
  state: KitState = 'ready';

  private body(): Template {
    const rail = STEPS.map(railStep);
    return html`<div class="flow">
      <ol class="rail">${rail}</ol>
      <div class="step-card">
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
