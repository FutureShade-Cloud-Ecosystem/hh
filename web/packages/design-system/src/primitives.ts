// The primitive layer: the seam where the Ionic primitives land.
//
// The source brief names Ionic as the kits' primitive library. The web
// workspace carries no dependencies and this package may add none, so the few
// primitives the kits need exist here as small template helpers over plain
// markup, with the call sites the Ionic elements will occupy. When Ionic lands
// as a workspace dependency, the BODIES of these helpers swap to the ion-*
// primitives and the kits do not change; that is the whole point of the seam.
// This deviation is recorded in the package README.
//
// Two carried conduct rules are implemented here, once, so every kit inherits
// them and no kit can get them wrong:
//
//   1. Disabled must LOOK disabled. A filled control at reduced opacity still
//      reads as clickable in daylight, so a disabled control DROPS ITS FILL AND
//      GOES GREY through the --hh-disabled-* token swap. There is no fade
//      anywhere in this package and the gate in test/kits.test.mjs fails if one
//      appears.
//   2. The focus ring is tokens or it does not exist. Every primitive carries
//      the :focus-visible ring from --hh-focus-ring, because a shadow root does
//      not inherit the document's ring rule.

import { css, html, type Template, type UnsafeHtml } from './lit.js';

export type ButtonKind = 'primary' | 'secondary' | 'danger';

/** The shared stylesheet every kit adopts. Token-only: no literal colours. */
export const baseStyles = css`
  :host { display: block; font-family: var(--hh-font-ui); }
  * { box-sizing: border-box; }

  button {
    font-family: inherit;
    font-size: var(--hh-s-16);
    line-height: 1;
    min-height: 44px;
    padding: 0 var(--hh-s-20);
    border-radius: var(--hh-radius-control);
    border: 1px solid transparent;
    cursor: pointer;
  }
  button:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: 2px;
  }
  .btn-primary {
    background: var(--brand-fill);
    color: var(--brand-fill-on);
    border-color: var(--brand-fill-on);
  }
  .btn-primary:hover { background: var(--brand-fill-hover); }
  .btn-secondary {
    background: transparent;
    color: var(--hh-text);
    border-color: var(--hh-line);
  }
  .btn-danger {
    background: var(--hh-danger-fill);
    color: var(--hh-danger-fill-ink);
  }
  /* THE disabled rule, stated once: a token swap, never a fade. Filled kinds
     drop their fill and go grey; the grey pair clears AA in both halves. */
  button:disabled,
  button:disabled:hover {
    background: var(--hh-disabled-fill);
    color: var(--hh-disabled-ink);
    border-color: var(--hh-disabled-line);
    cursor: default;
  }

  .spinner-wrap {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-12);
  }
  .spinner {
    width: 28px;
    height: 28px;
    border-radius: var(--hh-radius-pill);
    border: 3px solid var(--hh-track);
    /* The moving arc is gold-as-INK, per theme: the gold FILL is 1.50:1 against
       the light page and a spinner drawn with it would vanish in Light Mode. */
    border-top-color: var(--hh-accent-text);
    animation: hh-spin 0.9s linear infinite;
  }
  @media (prefers-reduced-motion: reduce) {
    .spinner { animation: none; border-inline-end-color: var(--hh-accent-text); }
  }

  .chip {
    display: inline-flex;
    align-items: center;
    gap: var(--hh-s-8);
    padding: var(--hh-s-4) var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-card);
    font-size: var(--hh-s-12);
  }
  .chip .icon { width: 14px; height: 14px; }
  .chip-neutral { color: var(--hh-text-dim); border: 1px solid var(--hh-line); }
  .chip-good { color: var(--hh-state-good); border: 1px solid var(--hh-state-good); }
  .chip-watch { color: var(--hh-state-watch); border: 1px solid var(--hh-state-watch); }
  .chip-stop { color: var(--hh-state-stop); border: 1px solid var(--hh-state-stop); }

  ul.rows { list-style: none; margin: 0; padding: 0; }
  .row {
    display: flex;
    align-items: center;
    gap: var(--hh-s-12);
    padding: var(--hh-s-12) var(--hh-s-16);
    background: var(--hh-row);
    border-bottom: 1px solid var(--hh-line);
    color: var(--hh-text);
  }
  .row-alt { background: var(--hh-row-alt); }
  .row-selected { border-left: 3px solid var(--hh-focus-ring); font-weight: 600; }

  @keyframes hh-spin { to { transform: rotate(360deg); } }
`;

export interface ButtonSpec {
  label: string;
  kind?: ButtonKind;
  disabled?: boolean;
  /** The action name for the shell to wire; rendered as data-action. */
  action?: string;
}

export function button(spec: ButtonSpec): Template {
  const kind = spec.kind ?? 'primary';
  // Attribute VALUES go through template interpolation so the engine escapes
  // them; only closed-union constants and flags are composed directly.
  const actionAttr = spec.action ? html` data-action="${spec.action}"` : '';
  return html`<button type="button" class="btn btn-${kind}"${spec.disabled ? ' disabled' : ''}${actionAttr}>${spec.label}</button>`;
}

/** The motion half of the loading pattern. The WORD lives in the state
 * indicator, which pairs the spinner with the loader icon and the label, so the
 * ring carries no text of its own and hides from assistive tech. */
export function spinner(): Template {
  return html`<span class="spinner-wrap"><span class="spinner" role="presentation" aria-hidden="true"></span></span>`;
}

export type ChipTone = 'neutral' | 'good' | 'watch' | 'stop';

export function chip(text: string, tone: ChipTone = 'neutral', iconMarkup?: Template | UnsafeHtml): Template {
  const toneClass = tone === 'neutral' ? 'chip-neutral' : `chip-${tone}`;
  return html`<span class="chip ${toneClass}">${iconMarkup ?? ''}<span class="chip-text">${text}</span></span>`;
}

/** A list row on the striped pair, the role the row-stripe tokens exist for. */
export function row(content: Template, opts: { alt?: boolean; selected?: boolean } = {}): Template {
  const cls = `row${opts.alt ? ' row-alt' : ''}${opts.selected ? ' row-selected' : ''}`;
  return html`<li class="${cls}">${content}</li>`;
}
