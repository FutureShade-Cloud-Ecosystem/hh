// The Field sign-in card: a single centered card carrying the tenant monogram
// and name from config, the Sign in to Field heading, the Work email or phone
// and Passcode or PIN fields, the primary Sign in button, and the shared yard
// tablet PIN hint.
//
// Purely presentational: the host supplies the tenant config (org, optional
// monogram override, optional branch) and handles the hh-field-sign-in event
// dispatched through the design system's data-action delegation pattern.
// Tokens only (no hex literals); 44 px minimum target heights.

import { css, html, nothing, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import { monogramOf } from './shell-state.js';

const styles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  * { box-sizing: border-box; }

  .sign-in-shell {
    display: grid;
    place-items: center;
    padding: var(--hh-s-24) var(--hh-s-16);
    background: var(--hh-page);
    color: var(--hh-text);
  }

  .sign-in-card {
    width: 100%;
    max-width: 380px;
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-24);
    display: grid;
    gap: var(--hh-s-16);
  }

  .tenant-lockup {
    display: grid;
    justify-items: center;
    gap: var(--hh-s-8);
    text-align: center;
  }

  .tenant-monogram {
    display: inline-grid;
    place-items: center;
    min-width: 48px;
    height: 48px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-pill);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    font-size: var(--hh-s-16);
    font-weight: 700;
    line-height: 1;
  }

  .heading {
    margin: 0;
    font-size: var(--hh-s-24);
    font-weight: 700;
    line-height: 1.2;
    color: var(--hh-text);
    text-align: center;
  }

  .tenant-subtitle {
    margin: 0;
    font-size: var(--hh-s-14);
    color: var(--hh-text-dim);
    text-align: center;
  }

  .field {
    display: grid;
    gap: var(--hh-s-4);
  }

  .field-label {
    font-size: var(--hh-s-12);
    font-weight: 600;
    color: var(--hh-text-dim);
  }

  .field-input {
    width: 100%;
    min-height: 44px;
    padding: 0 var(--hh-s-12);
    border-radius: var(--hh-radius-control);
    border: 1px solid var(--hh-control-line);
    background: var(--hh-field);
    color: var(--hh-text);
    font-family: inherit;
    font-size: var(--hh-s-16);
  }

  .field-input:focus-visible {
    outline: 2px solid var(--hh-focus-ring);
    outline-offset: -2px;
  }

  .sign-in-btn {
    width: 100%;
    min-height: 44px;
    justify-content: center;
    font-weight: 700;
  }

  .yard-pin-hint {
    margin: 0;
    font-size: var(--hh-s-12);
    color: var(--hh-text-dim);
    text-align: center;
  }
`;

export interface FieldSignInSpec {
  org?: string;
  monogram?: string;
  branch?: string;
}

export function renderFieldSignIn(spec: FieldSignInSpec = {}): Template {
  const org = spec.org ?? '';
  const branch = spec.branch ?? '';
  const monogram = monogramOf(org, spec.monogram ?? '');
  const subtitle = [org, branch].filter((part) => part !== '').join(' · ');
  return html`
    <section class="sign-in-shell" role="region" aria-label="Sign in to Field">
      <div class="sign-in-card">
        <div class="tenant-lockup" data-tenant=${org}>
          ${monogram !== ''
            ? html`<span class="tenant-monogram" data-monogram=${monogram} aria-hidden="true">${monogram}</span>`
            : nothing}
          <h1 class="heading">Sign in to Field</h1>
          ${subtitle !== '' ? html`<p class="tenant-subtitle">${subtitle}</p>` : nothing}
        </div>
        <label class="field">
          <span class="field-label">Work email or phone</span>
          <input class="field-input" type="text" name="identifier" autocomplete="username" />
        </label>
        <label class="field">
          <span class="field-label">Passcode or PIN</span>
          <input class="field-input" type="password" name="secret" autocomplete="current-password" />
        </label>
        <button class="btn btn-primary sign-in-btn" type="button" data-action="sign-in">
          Sign in
        </button>
        <p class="yard-pin-hint">On a shared yard tablet? Use a 4-digit yard PIN</p>
      </div>
    </section>
  `;
}

export class FieldSignIn extends HHElement {
  static styles = [baseStyles, styles];

  static properties = {
    ...HHElement.properties,
    org: { attribute: 'org' },
    monogram: { attribute: 'monogram' },
    branch: { attribute: 'branch' },
  };

  /** Tenant or dealer name from config. */
  declare org: string;
  /** Optional explicit monogram from tenant config; falls back to initials of org. */
  declare monogram: string;
  /** Optional branch name from config. */
  declare branch: string;

  constructor() {
    super();
    this.org = '';
    this.monogram = '';
    this.branch = '';
    this.state = 'ready';
  }

  override connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener('click', this.#onClick);
  }

  override disconnectedCallback(): void {
    this.removeEventListener('click', this.#onClick);
    super.disconnectedCallback();
  }

  readonly #onClick = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    if (control.getAttribute('data-action') === 'sign-in') {
      this.dispatchEvent(new CustomEvent('hh-field-sign-in', { bubbles: true, composed: true }));
    }
  };

  render(): Template {
    return renderFieldSignIn({
      org: this.org,
      monogram: this.monogram,
      branch: this.branch,
    });
  }
}

defineElement('hh-field-sign-in', FieldSignIn);
