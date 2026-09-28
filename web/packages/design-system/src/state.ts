// The five states, designed ONCE. Every kit renders the same state system over
// its own body, which is what "states designed once per pattern" means here:
// the scaffold, the indicators, the banners and the review card live in this
// file, and a kit only supplies its body and its action specs.
//
// The carried rule is structural, and it is tested in test/kits.test.mjs:
// COLOUR NEVER ENCODES STATE ALONE. Each state pairs its colour with an icon,
// a word, and a pattern change (the data-pattern marker on the root), so a
// reader with the colour removed can still tell which state they are in.

import { css, html, type Template } from './render.js';
import { button, chip, spinner, type ButtonSpec } from './primitives.js';

// The kits take their action specs through ShellSpec, so they import the spec
// type from this module; re-exported here, declared once in primitives.ts.
export type { ButtonSpec };
import { icon, type StateIconName } from './state-icons.js';

export const KIT_STATES = ['empty', 'loading', 'offline', 'conflict', 'error'] as const;
export type DesignedState = (typeof KIT_STATES)[number];
export type KitState = DesignedState | 'ready';

export interface StateMeta {
  icon: StateIconName;
  label: string;
  note: string;
  /** The structural change the state makes, carried in data-pattern. */
  pattern: string;
}

export const STATE_META: Record<DesignedState, StateMeta> = {
  empty: {
    icon: 'inbox',
    label: 'Nothing here yet',
    note: 'When work arrives it shows up here first.',
    pattern: 'prompt',
  },
  loading: {
    icon: 'loader',
    label: 'Loading',
    note: 'Reading the latest from the server.',
    pattern: 'progress',
  },
  offline: {
    icon: 'wifiOff',
    label: 'Offline, showing the cached copy',
    note: 'Edits wait in the outbox and send when the connection returns.',
    pattern: 'cached',
  },
  conflict: {
    icon: 'diverge',
    label: 'This row changed on the server too',
    note: 'Review both versions and pick one. Nothing is resolved silently.',
    pattern: 'review',
  },
  error: {
    icon: 'alertOctagon',
    label: 'Something went wrong',
    note: 'The last action did not complete. Nothing was lost.',
    pattern: 'fault',
  },
};

/** The state's own indicator: the icon, the word, and the pattern marker. The
 * icon renders BARE (hidden from assistive tech) because the word sits beside
 * it; a labelled icon would announce the label twice. */
export function stateIndicator(state: DesignedState): Template {
  const meta = STATE_META[state];
  return html`<div class="state-indicator" data-state="${state}" data-pattern="${meta.pattern}">
    ${icon(meta.icon)}
    <span class="state-label">${meta.label}</span>
  </div>`;
}

function stateTop(state: DesignedState): Template {
  const meta = STATE_META[state];
  switch (state) {
    case 'empty':
      return html`<div class="state-block state-empty" data-pattern="prompt">
        ${stateIndicator(state)}
        <p class="state-note">${meta.note}</p>
      </div>`;
    case 'loading':
      return html`<div class="state-block state-loading" data-pattern="progress">
        ${stateIndicator(state)}
        ${spinner()}
        <p class="state-note">${meta.note}</p>
      </div>`;
    case 'offline':
      return html`<div class="banner banner-offline" role="status" data-pattern="cached">
        ${icon(meta.icon)}
        <span class="banner-text">${meta.label}</span>
        ${chip('cached copy', 'neutral')}
      </div>`;
    case 'conflict':
      return html`<div class="review-card" role="alert" data-pattern="review">
        <div class="review-head">
          ${icon(meta.icon)}
          <span class="review-title">${meta.label}</span>
        </div>
        <dl class="review-rows">
          <div class="review-row"><dt>Your edit</dt><dd>Pallet coverage moved to 640 sq ft</dd></div>
          <div class="review-row"><dt>Server copy</dt><dd>Pallet coverage moved to 620 sq ft</dd></div>
        </dl>
        <p class="state-note">${meta.note}</p>
      </div>`;
    case 'error':
      return html`<div class="state-block state-error" role="alert" data-pattern="fault">
        ${stateIndicator(state)}
        <p class="state-note">${meta.note}</p>
        ${button({ label: 'Try again', action: 'retry' })}
      </div>`;
  }
}

export interface ShellSpec {
  /** The kit tag, one-to-one with the element name. */
  kit: string;
  title: string;
  state: KitState;
  body: Template;
  /**
   * The kit's action SPECS, without their disabled state. The scaffold owns
   * when actions are held: offline holds every action, conflict holds all but
   * the review, ready holds nothing. Held actions are the token swap, never a
   * fade (see primitives.ts).
   */
  actions?: ButtonSpec[];
}

/**
 * The kit scaffold: the root that names kit and state, the state's top block,
 * the body, and the action row. The body renders for ready, offline (the cached
 * copy) and conflict (the local copy under review); the designed states that
 * replace the body are empty, loading and error.
 */
export function kitShell(spec: ShellSpec): Template {
  // An unknown state must fail loudly, never render as a quietly empty kit:
  // a typo'd state name is a defect at the call site, and an empty region
  // would hide it from everyone but the person using the screen.
  if (!(KIT_STATES as readonly string[]).includes(spec.state) && spec.state !== 'ready') {
    throw new Error(
      `kitShell: kit ${spec.kit} was asked for the unknown state ${JSON.stringify(spec.state)}; ` +
        `expected ready or one of: ${KIT_STATES.join(', ')}`,
    );
  }
  const designed = spec.state === 'ready' ? undefined : (spec.state as DesignedState);
  const meta = designed ? STATE_META[designed] : undefined;
  const bodyHeld = spec.state === 'offline' || spec.state === 'conflict';
  const showBody = !designed || bodyHeld;

  const actions = spec.actions ?? [];
  let actionRow: Template | '';
  if (bodyHeld) {
    // Held actions are the token swap (primitives.ts). In conflict the ONE live
    // control is the review decision, which the scaffold owns: designed once,
    // the same control for every kit, and never a kit action that would resolve
    // the conflict as a side effect.
    const held = actions.map((a) => button({ ...a, disabled: true }));
    const row = spec.state === 'conflict'
      ? [button({ label: 'Review changes', kind: 'primary', action: 'review' }), ...held]
      : held;
    actionRow = html`<div class="kit-actions" data-live="false">${row}</div>`;
  } else if (spec.state === 'ready') {
    actionRow = html`<div class="kit-actions" data-live="true">${actions.map((a) => button(a))}</div>`;
  } else {
    actionRow = '';
  }

  return html`<div
    class="kit-root"
    data-kit="${spec.kit}"
    data-state="${spec.state}"
    data-pattern="${meta ? meta.pattern : 'ready'}"
    role="region"
    aria-label="${spec.title}"
    aria-busy="${spec.state === 'loading' ? 'true' : 'false'}"
  >
    ${designed ? stateTop(designed) : ''}
    ${showBody ? spec.body : ''}
    ${actionRow}
  </div>`;
}

export const stateStyles = css`
  .kit-root {
    background: var(--hh-page);
    color: var(--hh-text);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-24);
    display: grid;
    gap: var(--hh-s-16);
    /* The kit is its own layout container, so a kit's sheets can query the
       width the KIT actually gets, not the window's; min-width 0 keeps the
       grid child from refusing to shrink below its content. */
    container-type: inline-size;
    min-width: 0;
    overflow-wrap: break-word;
  }
  /* Grid and flex items default to min-width: auto, which lets a body's
     min-content width push past the kit card; the scaffold refuses on behalf
     of every kit. */
  .kit-root > * { min-width: 0; }
  .kit-actions {
    display: flex;
    gap: var(--hh-s-12);
    flex-wrap: wrap;
  }

  .state-block {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-32);
    display: grid;
    justify-items: center;
    gap: var(--hh-s-12);
    text-align: center;
  }
  .state-note {
    margin: 0;
    color: var(--hh-text-dim);
    max-width: var(--hh-measure);
  }

  .state-indicator {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    color: var(--hh-text);
    font-weight: 600;
  }
  .state-indicator .icon { width: 20px; height: 20px; }
  .state-empty .state-indicator { color: var(--hh-state-watch); }
  .state-loading .state-indicator { color: var(--hh-info); }
  .state-error .state-indicator { color: var(--hh-danger); }

  .banner {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-12);
    padding: var(--hh-s-12) var(--hh-s-16);
    border-radius: var(--hh-radius-control);
    background: var(--hh-card);
    min-width: 0;
  }
  .banner-offline { color: var(--hh-info); border: 1px solid var(--hh-info); }
  .banner .icon { width: 18px; height: 18px; flex: none; }
  .banner-text { color: var(--hh-text); font-weight: 600; min-width: 0; overflow-wrap: anywhere; }

  .review-card {
    background: var(--hh-card);
    border: 1px solid var(--hh-state-watch);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16);
    display: grid;
    gap: var(--hh-s-12);
  }
  .review-head { display: flex; align-items: center; gap: var(--hh-s-8); color: var(--hh-state-watch); font-weight: 600; }
  .review-head .icon { width: 18px; height: 18px; }
  .review-title { color: var(--hh-text); }
  .review-rows { margin: 0; display: grid; gap: var(--hh-s-8); }
  .review-row { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: var(--hh-s-16); }
  .review-row dt { color: var(--hh-text-dim); }
  .review-row dd { margin: 0; color: var(--hh-text); overflow-wrap: anywhere; }
`;
