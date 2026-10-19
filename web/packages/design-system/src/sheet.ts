// THE BOTTOM SHEET PRIMITIVE.
//
// One bottom sheet for the phone first Field apps the WALK-C13 boards draw it in (the Yard Pick skid
// reasons, the Drive "Could not deliver" reasons, the House Messages New sheet): a sheet on a phone,
// never a dropdown or a centred modal. Built once in the design system so each app mounts <hh-sheet>
// and listens for one close event. The contract:
//
//   * One element: <hh-sheet> (registered with defineElement, the package's
//     own helper, not a decorator).
//   * Structural contract: scrim + bottom-anchored panel, top corners rounded,
//     grabber bar, title (heading attribute or title slot), optional sentence
//     (description slot), default slot for rows, actions slot for the full-
//     width primary action.
//   * Close paths: Escape, scrim tap, close(); fires hh-sheet-close (bubbles,
//     composed) with { reason: "escape" | "scrim" | "method" }.
//   * Idempotent: closing twice fires no second event.
//   * Focus: moves into the sheet on open (first focusable, else the sheet
//     itself, tabindex -1); returns to the previous holder on close; Tab and
//     Shift Tab cycle inside the sheet.
//   * Reduced motion: no slide animation when prefers-reduced-motion: reduce.
//   * Width: phone (<600) spans the viewport; tablet or wider is a centred
//     bottom-anchored panel of at most 560px.
//   * Safe-area-inset-bottom for the bottom inset.
//
// Every token it reads is defined in src/tokens.css (the phantom gate); it adds none.

import { css, html, type PropertyValues, type TemplateResult as Template } from 'lit';
import { defineElement, HHElement } from './base.js';
import { baseStyles } from './primitives.js';

/** The one tag the consumer mounts. */
export const hhSheetTag = 'hh-sheet';

/** The event names the sheet fires, the set the consumer listens for. */
export const hhSheetEvents = ['hh-sheet-close'] as const;

/** Why the sheet closed: the consumer reads this off event.detail.reason. */
export type HHSheetCloseReason = 'escape' | 'scrim' | 'method';

/** The detail shape that rides the hh-sheet-close event. */
export interface HHSheetCloseDetail {
  readonly reason: HHSheetCloseReason;
}

/** The custom event the consumer listens for. */
export class HHSheetCloseEvent extends CustomEvent<HHSheetCloseDetail> {
  constructor(reason: HHSheetCloseReason) {
    super('hh-sheet-close', {
      detail: { reason },
      bubbles: true,
      composed: true,
    });
  }
}

export class HHSheet extends HHElement {
  static properties = {
    ...HHElement.properties,
    /** Plain heading text; takes the place of the title slot when set. */
    heading: { attribute: 'heading' },
    /** Optional sentence under the title; falls back to the description slot. */
    description: { attribute: 'description' },
    /** Whether the sheet is open; closed is the default. */
    open: { type: Boolean, reflect: true },
  };

  declare heading: string;
  declare description: string;
  declare open: boolean;

  /** The element that held focus before the sheet opened; focus returns here
   * on close. Set when open transitions false -> true, read when open goes
   * back to false. The element may be detached later; focus restoration is
   * best-effort (the browser will not throw). */
  #focusReturn: Element | null = null;

  /** The reason a close transition is firing, set by the close path that
   * triggered it (Escape, scrim, method). updated() reads and clears this
   * field when the open transition is observed. The field is the single
   * source of truth for which reason the close event carries, and the single
   * place the event is dispatched: a double-fire would mean two close paths
   * stacked, and the consumer would observe the same close twice. */
  #pendingCloseReason: HHSheetCloseReason | null = null;

  constructor() {
    super();
    this.heading = '';
    this.description = '';
    this.open = false;
  }

  connectedCallback(): void {
    super.connectedCallback();
    // The host delegation rule: read the control off composedPath(). A click
    // on the scrim rides composedPath through the shadow root and finds the
    // scrim by data-action="close-scrim".
    this.addEventListener('click', this.#onHostClick);
    // Escape closes the sheet when open; keydown bubbles up from anywhere
    // inside the shadow root (composed: true by default for a click target
    // inside the host, but the listener on the host catches it regardless).
    this.addEventListener('keydown', this.#onKeydown);
    // The focus trap: a Tab at the last focusable inside the sheet wraps
    // to the first; a Shift Tab at the first wraps to the last. The browser
    // does not natively trap focus, so the host catches keydown and pulls
    // focus back when Tab would have escaped.
    this.addEventListener('keydown', this.#onFocusTrapKeydown);
  }

  disconnectedCallback(): void {
    this.removeEventListener('click', this.#onHostClick);
    this.removeEventListener('keydown', this.#onKeydown);
    this.removeEventListener('keydown', this.#onFocusTrapKeydown);
    this.#focusReturn = null;
    this.#pendingCloseReason = null;
    super.disconnectedCallback();
  }

  /** The imperative close; closes once and fires the event with reason
   * "method". A second call while already closed does nothing: the contract
   * is one event per user-driven close. */
  close(): void {
    if (!this.open) return;
    this.#pendingCloseReason = 'method';
    this.open = false;
  }

  /** Imperative focus-into-the-sheet; a consumer can call it from outside
   * after the open transition (e.g. when the trigger was a non-text input). */
  focusInside(): void {
    this.#moveFocusInside();
  }

  protected override updated(changed: PropertyValues): void {
    super.updated(changed);
    if (!changed.has('open')) return;
    const wasOpen = changed.get('open') === true;
    if (this.open && !wasOpen) {
      // The transition is false -> true. Remember the focus holder so the
      // close transition can return focus; then move focus into the sheet.
      const active = this.#ownerDocument.activeElement;
      this.#focusReturn = active instanceof Element && active !== this ? active : null;
      // updated() fires synchronously; the focus pass waits one microtask so
      // the freshly painted dialog is in the layout tree (and can receive
      // focus). updateComplete has not resolved yet, but the focus call
      // itself does not require the element to be focusable: the tabindex
      // is set on render, and a subsequent focus() call resolves it.
      queueMicrotask(() => this.#moveFocusInside());
    } else if (!this.open && wasOpen) {
      // The transition is true -> false: read the reason the close path
      // staked on the pending field, dispatch the event with that reason,
      // and return focus. The single dispatch path is the contract: a close
      // path that sets #pendingCloseReason first and toggles open second
      // never double-fires, and a path that forgets to set the reason falls
      // back to "method" (the consumer's imperative close).
      const reason: HHSheetCloseReason = this.#pendingCloseReason ?? 'method';
      this.#pendingCloseReason = null;
      this.dispatchEvent(new HHSheetCloseEvent(reason));
      const ret = this.#focusReturn;
      this.#focusReturn = null;
      if (ret !== null && ret.isConnected) {
        // focus() can throw if the element is no longer focusable; swallow
        // the throw: the contract is best-effort restoration.
        try {
          (ret as HTMLElement).focus();
        } catch {
          // No-op: the trigger may have been removed between open and close.
        }
      }
    }
  }

  /** The render. The scrim lives behind the dialog and closes on tap. The
   * dialog lives at the bottom of the viewport on phone (full width) and at
   * the bottom-centre on tablet and up (at most 560px). The
   * grabber, the title (heading attribute or title slot), the description
   * (description attribute or description slot), the default slot for rows,
   * and the actions slot for the full-width primary action are stacked
   * top-to-bottom.
   *
   * The sheet is shown when the class includes 'hh-sheet--open'; the CSS
   * uses that class to set the scrim and dialog's display. This is the
   * gate-allowed way to flip visibility: the gate allows `class` as a
   * quoted value binding and refuses the boolean `?hidden` form, so the
   * class-based toggle is the contract the package holds. */
  override render(): Template {
    const rootClass = this.open ? 'hh-sheet--open' : 'hh-sheet--closed';
    return html`
      <div class="scrim ${rootClass}" data-action="close-scrim" part="scrim"></div>
      <div
        class="dialog ${rootClass}"
        part="dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="hh-sheet-title"
        tabindex="-1"
      >
        <div class="grabber" part="grabber" aria-hidden="true"></div>
        ${this.heading
          ? html`<h2 class="title" part="title" id="hh-sheet-title">${this.heading}</h2>`
          : html`<h2 class="title" part="title" id="hh-sheet-title"><slot name="title"></slot></h2>`}
        ${this.description
          ? html`<p class="description" part="description">${this.description}</p>`
          : html`<p class="description" part="description"><slot name="description"></slot></p>`}
        <div class="rows" part="rows">
          <slot></slot>
        </div>
        <div class="actions" part="actions">
          <slot name="actions"></slot>
        </div>
      </div>
    `;
  }

  static styles = [baseStyles, css`
    :host {
      /* The host is a fixed positioning context for the scrim and the dialog.
       * display: contents lets the scrim and the dialog position themselves
       * against the viewport without an extra box the host contributes.
       * The closing and opening of the sheet do not change the host's box. */
      display: contents;
    }
    .scrim {
      /* The full-viewport dim layer; sits behind the dialog. The fill is
       * the scrim token, so the page reads dimmed; the overlay's visibility rides display: none vs the layout on the
       * .hh-sheet--closed class, not an opacity transition (the package's
       * no-opacity gate keeps the disabled colour swap as the only state
       * indicator). */
      position: fixed;
      inset: 0;
      z-index: 90;
      background: var(--hh-scrim);
    }
    .scrim.hh-sheet--closed {
      /* The sheet is closed: the scrim is removed from layout and does not
       * capture clicks. The class drives the toggle so the template's
       * binding position stays on the gate-allowed class form. */
      display: none;
    }
    .dialog {
      /* The bottom-anchored panel. On phone (<600) it spans the viewport
       * width; on tablet and up it is centred and capped at 560px.
       * The radius rounds the top corners; the bottom corners stay square
       * (the panel sits at the bottom edge). */
      position: fixed;
      z-index: 91;
      left: 0;
      right: 0;
      bottom: 0;
      max-width: 100vw;
      width: 100vw;
      max-height: 90vh;
      overflow: auto;
      background: var(--hh-card);
      color: var(--hh-text);
      border-top-left-radius: var(--hh-radius-card);
      border-top-right-radius: var(--hh-radius-card);
      padding: calc(var(--hh-s-8) + env(safe-area-inset-top, 0px))
               var(--hh-s-20)
               calc(var(--hh-s-20) + env(safe-area-inset-bottom, 0px))
               var(--hh-s-20);
      box-shadow: 0 -8px 24px rgba(0, 0, 0, 0.18);
      transform: translateY(0);
      transition: transform 220ms cubic-bezier(0.2, 0.8, 0.2, 1);
    }
    .dialog.hh-sheet--closed {
      /* The sheet is closed: the dialog is removed from layout. The
       * transition would not play on a display: none element anyway. */
      display: none;
    }
    @media (min-width: 600px) {
      .dialog {
        /* Centred bottom-anchored panel: at most 560px wide,
         * sit at the bottom of the viewport, soft-cornered all round
         * (no edge sits against the page chrome at this size). */
        left: 50%;
        right: auto;
        transform: translate(-50%, 0);
        width: calc(100vw - 2 * var(--hh-s-20));
        max-width: 560px;
        border-top-left-radius: var(--hh-radius-card);
        border-top-right-radius: var(--hh-radius-card);
        border-bottom-left-radius: var(--hh-radius-card);
        border-bottom-right-radius: var(--hh-radius-card);
        margin-bottom: calc(var(--hh-s-20) + env(safe-area-inset-bottom, 0px));
      }
    }
    @media (prefers-reduced-motion: reduce) {
      /* Reduced motion: the dialog appears in place, with no transform transition. */
      .dialog {
        transition: none;
      }
    }
    .grabber {
      /* The grabber bar: a small pill the sheet wears at the top so the
       * user has a visual cue they can drag. The drag is not wired (the
       * close paths are Escape, the scrim and close()), but the cue is the
       * standard affordance a phone sheet carries. */
      width: 36px;
      height: 4px;
      border-radius: 999px;
      background: var(--hh-control-line, currentColor);
      margin: 0 auto var(--hh-s-16);
    }
    .title {
      margin: 0 0 var(--hh-s-8) 0;
      font-size: var(--hh-s-20);
      font-weight: 700;
      line-height: 1.3;
    }
    .title:empty {
      /* A sheet without a title keeps no extra blank: the description
       * slot, when present, takes the same vertical slot. */
      display: none;
    }
    .description {
      margin: 0 0 var(--hh-s-16) 0;
      font-size: var(--hh-s-16);
      line-height: 1.4;
      color: var(--hh-text-dim);
    }
    .description:empty {
      display: none;
    }
    .rows {
      /* The default slot: whatever the consumer puts in (a list, a
       * paragraph, a form) paints here. The max-height keeps long rows
       * scrollable inside the sheet, not the page. */
      margin-bottom: var(--hh-s-20);
      max-height: 60vh;
      overflow: auto;
    }
    .rows:empty {
      margin-bottom: 0;
    }
    .actions {
      /* The full-width primary action slot. The flex column with the
       * default align-items: stretch pulls the slotted button to the
       * container's width on the cross axis. */
      display: flex;
      flex-direction: column;
      gap: var(--hh-s-8);
    }
    /* The slotted button: the consumer's light-DOM button is what the user
     * taps. The slot moves it into the sheet's layout, but the slot does
     * not carry the design system's button styles (they live in
     * baseStyles, which only paints inside the sheet's own shadow DOM). The
     * ::slotted(button) selector applies the styles the consumer would
     * have written themselves: full width, the input height, the design
     * system's font. A consumer who wants a different button can set their
     * own styles in light DOM and the rule below yields to a higher-
     * specificity author rule. */
    .actions::slotted(button) {
      width: 100%;
      min-height: var(--hh-h-input, 44px);
      font-family: inherit;
      font-size: var(--hh-s-16, 16px);
      font-weight: 600;
      border-radius: var(--hh-radius-control, 8px);
      border: 1px solid transparent;
      cursor: pointer;
    }
    .actions:empty {
      display: none;
    }
  `];

  // ── internals ────────────────────────────────────────────────────────────────

  /** The cross-shadow click handler: the consumer's clicks land here; the
   * data-action="close-scrim" path rides composedPath() up the shadow
   * boundary and resolves here. Other data-action values are ignored (the
   * sheet owns no other actions). The handler only flips the property and
   * staked the close reason; the dispatch happens in updated(), on the
   * open transition, so this handler cannot double-fire. */
  readonly #onHostClick = (event: MouseEvent): void => {
    const path = event.composedPath();
    for (const node of path) {
      if (!(node instanceof Element)) continue;
      const action = node.getAttribute('data-action');
      if (action === 'close-scrim') {
        if (!this.open) return;
        this.#pendingCloseReason = 'scrim';
        this.open = false;
        return;
      }
    }
  };

  /** The keydown handler: Escape closes the sheet (when open) and stops
   * propagation so a parent dialog-stack does not also react. The dispatch
   * happens in updated(), on the open transition, so this handler cannot
   * double-fire. */
  readonly #onKeydown = (event: KeyboardEvent): void => {
    if (event.key !== 'Escape') return;
    if (!this.open) return;
    event.stopPropagation();
    this.#pendingCloseReason = 'escape';
    this.open = false;
  };

  /** The focus trap: when Tab would leave the sheet, pull it back to the
   * first focusable inside; Shift Tab walks to the last. The handler
   * listens on the host and inspects the focus position relative to the
   * dialog; it does not depend on any DOM mutation. */
  readonly #onFocusTrapKeydown = (event: KeyboardEvent): void => {
    if (!this.open) return;
    if (event.key !== 'Tab') return;
    const focusables = this.#focusables();
    if (focusables.length === 0) return;
    const active = this.#ownerDocument.activeElement;
    const first = focusables[0];
    const last = focusables[focusables.length - 1];
    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  };

  /** The focusables inside the sheet, in tab order. The list lives at the
   * time of the call; the focus trap does not snapshot. The list is built
   * from the shadow root (the dialog itself and its slots) and the assigned
   * nodes of those slots (the consumer's rows and action button). */
  #focusables(): HTMLElement[] {
    const root = this.shadowRoot;
    if (root === null) return [];
    const dialog = root.querySelector<HTMLElement>('.dialog');
    if (dialog === null) return [];
    // The dialog itself is focusable (tabindex -1) when nothing else is
    // available; we do not list it unless nothing else is.
    const candidates: HTMLElement[] = [];
    // The assigned nodes of the default slot and the actions slot.
    for (const slot of Array.from(dialog.querySelectorAll<HTMLSlotElement>('slot'))) {
      const assigned = slot.assignedElements({ flatten: true });
      for (const el of assigned) {
        if (el instanceof HTMLElement) collectFocusable(el, candidates);
      }
    }
    // The dialog itself, if no candidates were found.
    if (candidates.length === 0) candidates.push(dialog);
    // Stable order: the slots iterate in DOM order, so the candidates are
    // already in tab order as long as the consumer's slot children are in
    // tab order.
    return candidates.filter((el) => !el.hasAttribute('disabled'));
  }

  /** Moves focus into the sheet: the first focusable, else the dialog. The
   * call is idempotent: calling it on an already-open sheet is a no-op. */
  #moveFocusInside(): void {
    const focusables = this.#focusables();
    if (focusables.length > 0) {
      focusables[0].focus();
      return;
    }
    const root = this.shadowRoot;
    if (root === null) return;
    const dialog = root.querySelector<HTMLElement>('.dialog');
    if (dialog !== null) dialog.focus();
  }

  /** The document the host belongs to: defaults to globalThis.document in the
   * browser, but resolving off the host lets the element work in a Node-side
   * render (the SSR DOM shim). */
  get #ownerDocument(): Document {
    return this.ownerDocument ?? globalThis.document;
  }
}

/** Recursively collects every focusable element under a root. The order is
 * depth-first; the recursion stops at elements that cannot contain focusable
 * children. */
function collectFocusable(root: HTMLElement, out: HTMLElement[]): void {
  // The host might be the element itself; collectFocusable starts on a real
  // element, not a shadow root, so the walker inspects the host's children
  // and recurses through assigned slots.
  if (root.tabIndex >= 0 && !root.hasAttribute('disabled')) {
    out.push(root);
  }
  for (const child of Array.from(root.children)) {
    if (child instanceof HTMLElement) collectFocusable(child, out);
  }
}

defineElement(hhSheetTag, HHSheet);

declare global {
  interface HTMLElementTagNameMap {
    'hh-sheet': HHSheet;
  }
}
