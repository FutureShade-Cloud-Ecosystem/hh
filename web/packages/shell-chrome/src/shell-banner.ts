// The offline banner: the strip under the header band that says the truth
// about the connection while the person keeps working. Four states, the r4
// offline board's words: offline (with the durable outbox's depth), the
// server unreachable, read cache only (a second tab of the same shell owns
// the sync), and the sign in expired. The banner renders nothing at all when
// the device is online, owned and signed in; a chrome that says nothing is
// the point of the states.
//
// The offline store's invariants this strip carries forward: the count is
// the durable outbox's depth (health().waiting), reads answer from the local
// store while offline, and a parked session loses nothing. The stall's own
// words ride the sync chip in the band; this strip is only the connection's
// truth.
//
// Purely presentational: the shell maps the platform's network state, the
// backend's ownership and the session's state onto `mode`, and the outbox's
// health onto `waiting`. The sign in action rides a data-action name and
// emits hh-shell-sign-in; the shell owns what signing in means (item 7).

import { css, html, nothing, type TemplateResult as Template } from 'lit';
import { HHElement, defineElement } from '@hardscapehouse/design-system/src/base.js';
import { baseStyles } from '@hardscapehouse/design-system/src/primitives.js';
import { stateStyles } from '@hardscapehouse/design-system/src/state.js';
import { icon, type StateIconName } from '@hardscapehouse/design-system/src/state-icons.js';

export const SHELL_BANNER_MODES = ['', 'offline', 'unreachable', 'read-only', 'auth-expired'] as const;

export type ShellBannerMode = (typeof SHELL_BANNER_MODES)[number];

interface BannerWords {
  iconName: StateIconName;
  words: string;
}

const WORDS: Record<Exclude<ShellBannerMode, ''>, BannerWords> = {
  offline: {
    iconName: 'wifiOff',
    words: "You're offline · writes wait for the connection",
  },
  unreachable: {
    iconName: 'wifiOff',
    words: "The server can't be reached · your changes are safe and send in order",
  },
  'read-only': {
    iconName: 'inbox',
    words: 'This tab is read only · another tab is syncing',
  },
  'auth-expired': {
    iconName: 'alertOctagon',
    words: 'Sign in expired · your offline work is safe',
  },
};

/** The words of an app that only reads from the copy on the device (no outbox, no keeper): nothing is said to wait or to send later. */
const CACHE_ONLY_WORDS: Record<'offline' | 'unreachable', string> = {
  offline: "You're offline · showing the copy on this device",
  unreachable: "The server can't be reached · showing the copy on this device",
};

const styles = css`
  :host {
    display: block;
    font-family: var(--hh-font-ui);
    color-scheme: var(--hh-color-scheme);
  }
  /* The board's strip: a full width band under the header, ink on the widget
   * ground, no rounding (it is the frame, not a card). */
  .banner.banner-shell {
    background: var(--hh-widget);
    border: none;
    border-bottom: 1px solid var(--hh-line);
    border-radius: 0;
    padding: var(--hh-s-8) var(--hh-s-16);
    color: var(--hh-text);
    align-items: center;
  }
  .banner.banner-shell .icon {
    color: var(--hh-text);
  }
  .count {
    flex: none;
    display: inline-grid;
    place-items: center;
    min-width: 24px;
    height: 22px;
    padding: 0 var(--hh-s-8);
    border: 1px solid currentColor;
    border-radius: var(--hh-radius-pill);
    font-size: var(--hh-s-12);
    font-weight: 700;
    margin-left: auto;
  }
  .count.count-gold {
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    border-color: transparent;
  }
  .review-action {
    flex: none;
    min-height: 44px;
    margin-left: var(--hh-s-8);
  }
  .sign-in {
    margin-left: auto;
  }
`;

export function fieldOfflineWords(waiting: number): { words: string; action: string } {
  return {
    words:
      waiting > 0
        ? `Offline · ${waiting} waiting to send`
        : 'Offline · writes wait for the connection',
    action: 'Tap to review',
  };
}

export class ShellBanner extends HHElement {
  static styles = [baseStyles, stateStyles, styles];

  static properties = {
    ...HHElement.properties,
    mode: { attribute: 'mode' },
    waiting: { type: Number, attribute: 'waiting' },
    cacheOnly: { type: Boolean, attribute: 'cache-only' },
    field: { type: Boolean, attribute: 'field' },
    variant: { attribute: 'variant' },
  };

  /** The connection's truth: offline, unreachable, read-only (a second
   * tab), auth-expired, or '' for nothing to say. */
  declare mode: string;
  /** The durable outbox's depth, health().waiting. */
  declare waiting: number;
  /** An app with a read cache and no outbox: the offline and unreachable
   * words say what the screen shows, never that writes wait or send later. */
  declare cacheOnly: boolean;
  /** Field offline strip mode: shows Offline · N waiting to send, gold pill count, and Tap to review. */
  declare field: boolean;
  /** Optional variant attribute ('field' enables Field offline strip words). */
  declare variant: string;

  constructor() {
    super();
    this.mode = '';
    this.waiting = 0;
    this.cacheOnly = false;
    this.field = false;
    this.variant = '';
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
    const action = control.getAttribute('data-action');
    if (action === 'review') {
      this.dispatchEvent(new CustomEvent('hh-shell-review', { bubbles: true, composed: true }));
    }
  };

  render(): Template {
    if (this.mode === '') return html``;
    if (!(SHELL_BANNER_MODES as readonly string[]).includes(this.mode)) {
      // An unknown mode is a defect at the call site: refuse loudly, never
      // paint a quietly wrong state (the kit scaffold's own rule).
      throw new Error(
        `hh-shell-offline-banner was given the unknown mode ${JSON.stringify(this.mode)}; ` +
          `expected '' or one of: ${SHELL_BANNER_MODES.filter((m) => m !== '').join(', ')}`,
      );
    }
    const known = this.mode as Exclude<ShellBannerMode, ''>;
    const meta = WORDS[known];
    const isField = this.field || this.variant === 'field';
    const words =
      this.cacheOnly && (known === 'offline' || known === 'unreachable')
        ? CACHE_ONLY_WORDS[known]
        : known === 'offline' && isField
          ? fieldOfflineWords(this.waiting).words
          : known === 'offline' && this.waiting > 0
            ? `You're offline · ${this.waiting} changes in the outbox · they send when you're back`
            : meta.words;
    return html`
      <div class="banner banner-shell" role="status" data-mode=${this.mode}>
        ${icon(meta.iconName)}
        <span class="banner-text">${words}</span>
        ${known === 'offline' && this.waiting > 0 && !this.cacheOnly
          ? html`<span class="count ${isField ? 'count-gold' : ''}" aria-hidden="true">${this.waiting}</span>`
          : nothing}
        ${known === 'offline' && this.waiting > 0 && !this.cacheOnly && isField
          ? html`<button class="btn btn-secondary review-action" type="button" data-action="review">Tap to review</button>`
          : nothing}
        ${known === 'auth-expired'
          ? html`<button class="btn btn-primary sign-in" type="button" data-action="sign-in">Sign in</button>`
          : nothing}
      </div>
    `;
  }
}

// The sign in action rides the host delegation the design system documents:
// the shell listens on the element for a click on data-action="sign-in" and
// emits its own flow from there.
defineElement('hh-shell-offline-banner', ShellBanner);

