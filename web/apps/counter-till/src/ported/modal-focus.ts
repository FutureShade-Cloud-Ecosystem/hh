// Ported from hardscapeos_dibbits master 87d7941b app/src/modal-focus.ts; adapted for HH.

import type { ReactiveController, ReactiveControllerHost } from 'lit';

/**
 * modal-focus: E6. The one shared focus trap for every modal-class surface in the app.
 *
 * THE PROBLEM IT SOLVES
 * ---------------------
 * The app ships 17 modal dialogs across 14 components (the `.hos-dialog` / `.scrim` / `.backdrop`
 * / `.overlay` idioms). Every one of them paints a full-viewport scrim, and NOT ONE of them did
 * anything about focus. Concretely, before this module, a keyboard user who opened the parked-tickets
 * dialog:
 *
 * - stayed focused on whatever was behind the scrim (the trigger button), so the first Tab moved
 * focus INTO the dimmed page underneath rather than into the dialog;
 * - could Tab straight out of the dialog and keep going through the entire POS terminal behind it,
 * operating controls they could not see, with the scrim still up;
 * - had no Escape: the dialog could only be dismissed by clicking the scrim or hitting a button,
 * both of which are pointer affordances;
 * - on close, was dumped at the top of the document, because the element they came from was gone.
 *
 * That is the whole of the modal a11y contract, missing on every modal at once. WAI-ARIA APG's dialog
 * pattern names exactly these four: initial focus inside, focus contained while open, Escape to
 * dismiss, and focus restored to the invoker on close.
 *
 * WHY A CONTROLLER AND NOT A BASE CLASS
 * -------------------------------------
 * A `HosModalBase extends LitElement` would have been less code here and a worse fit. The 14 modal
 * components do not share a superclass, several of them are NOT modals in their entirety (a
 * `rule-editor` is a full pane that happens to raise two dialogs; `pos-terminal` is the whole POS
 * ring with one overlay in it), and three of them raise more than one dialog from the same element.
 * A base class can only model "this element IS a modal". A ReactiveController models "this element
 * HAS a modal, which is open when this predicate says so": which is what is actually true.
 *
 * WHY THE SHADOW BOUNDARY MAKES THIS NECESSARY RATHER THAN NICE
 * -------------------------------------------------------------
 * `<dialog showModal()>` would give the trap for free from the platform, and it is the right answer
 * in a light-DOM app. It is not reachable here without a rewrite: these dialogs render INSIDE each
 * component's shadow root, already position:fixed against a tokenised scrim, with the open/closed
 * state driven by a Lit @state field that parents read and write. Swapping in the platform element
 * means changing the render tree, the scrim styling and the open/close protocol of 14 components at
 * once: a behaviour change wearing an a11y label. This module adds the missing behaviour without
 * touching any of that, which is also why it is safe to land in a polish wave.
 *
 * The shadow boundary is also why the trap cannot be naive. `document.activeElement` inside a shadow
 * root returns the HOST, not the focused button, so the obvious `items.indexOf(document.activeElement)`
 * is always -1 and the trap wraps to the first element on every Tab. `deepActiveElement` below is the
 * fix, and it is the single most load-bearing line in the file.
 *
 * THE SAME BOUNDARY ALSO STARVES focusablesIn, AND THIS WAS SHIPPED BROKEN (DIB-564 refuter finding,
 * fixed here, cross-cutting to every consumer). `container.querySelectorAll(FOCUSABLE_SELECTOR)`:
 * the whole original implementation: does not pierce shadow boundaries either, so ANY nested custom
 * element with its own focusable content (the "Change history" `<summary>` inside
 * `hos-settings-history`, mounted inside `dispatch-settings.ts`'s own sheet, was the reproduction)
 * was invisible to the trap: not leaked to the page behind: the wrap arithmetic still closes the
 * loop correctly at both ends: but STARVED, permanently unreachable by keyboard. `composedChildren`
 * / `collectComposed` below replace the single `querySelectorAll` call with a manual walk that
 * crosses into every open shadow root it meets, which is the shape every one of this file's other
 * shadow-aware code already takes (`deepActiveElement` does the identical descent for
 * `activeElement`). This is a change to the ONE shared controller 20+ components construct
 * (`app-shell.ts`, `ar-landing.ts`, `pos-customer-picker.ts`, `till-close.ts`, `rule-editor.ts`,
 * `order-lifecycle.ts` and more): the starvation was latent in every one of them that nests a
 * focusable custom element inside its trapped container, not particular to one surface, which is
 * why the fix belongs here and not as a local workaround on the one surface that happened to notice.
 */

/**
 * What the platform will let a keyboard user land on. `[tabindex="-1"]` is deliberately excluded:
 * it means "programmatically focusable, not in the tab order", which is exactly what a dialog
 * CONTAINER is marked with so it can receive initial focus without becoming a tab stop.
 */
export const FOCUSABLE_SELECTOR = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'summary',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

/**
 * The really-focused element, not the host that contains it.
 *
 * A shadow root reports its own `activeElement` independently: `document.activeElement` is the
 * outermost custom element, and each `shadowRoot.activeElement` narrows one level further in. Descend
 * until a level has no shadow root or no active element inside it, and that is what the user is
 * actually on. Without this, a trap in a shadow root cannot tell WHERE inside itself focus is.
 */
export function deepActiveElement(root: DocumentOrShadowRoot = document): Element | null {
  let el: Element | null = root.activeElement;
  while (el && el.shadowRoot && el.shadowRoot.activeElement) {
    el = el.shadowRoot.activeElement;
  }
  return el;
}

/**
 * Is this element actually reachable right now? `disabled` is in the selector for native controls,
 * but a control can also be hidden (`display: none`, a collapsed step, `hidden`), and a hidden
 * control in the item list breaks the trap: Tab appears to do nothing because focus was sent to
 * something invisible.
 *
 * `getClientRects().length` is the check that works here. `offsetParent === null`: the usual
 * shorthand: reports null for ANY position:fixed element, which is every one of these dialogs, so it
 * would filter out the entire trap contents.
 */
export function isReachable(el: HTMLElement): boolean {
  if (el.hasAttribute('disabled')) return false;
  if (el.getAttribute('aria-hidden') === 'true') return false;
  if (typeof el.getClientRects === 'function' && el.getClientRects().length === 0) return false;
  return true;
}

/**
 * The DOM-shaped subset the composed-tree walk needs from a node: abstracted so the walk itself
 * can be pinned in a plain Node test with hand-built objects, the same reason `nextTrapIndex` and
 * `pickInitialIndex` below are pure integer functions rather than DOM code. `matches`/`children`/
 * `shadowRoot` are real Element/ShadowRoot properties in the browser; a test fakes exactly these
 * (plus a controlled `matches` answer) and touches no layout, no CSS parsing, no real shadow DOM.
 */
interface FocusTreeNode {
  readonly tagName?: string;
  readonly shadowRoot?: { readonly children: ArrayLike<FocusTreeNode> } | null;
  readonly children: ArrayLike<FocusTreeNode>;
  matches(selector: string): boolean;
  assignedElements?(opts: { flatten: boolean }): FocusTreeNode[];
}

/**
 * The composed-tree children of `node`: DOM tree children, UNLESS `node` is a shadow host, in
 * which case its RENDERED content is its shadow root's children and its own (light-DOM) children
 * are not part of the render tree at all. This is the exact boundary a plain
 * `container.querySelectorAll()` cannot cross, and it is why `focusablesIn` used to starve any
 * focusable control that lived inside a nested custom element's shadow root (DIB-564).
 *
 * A `<slot>` is the one place light DOM re-enters a shadow tree: its rendered content is whatever
 * is actually ASSIGNED to it, or its own (fallback) children when nothing is assigned. No shipped
 * ModalFocus consumer slots content into a nested component today, but a walk that got this wrong
 * would silently drop whatever the next one does, so it is handled rather than assumed away.
 */
function composedChildren(node: FocusTreeNode): FocusTreeNode[] {
  if (node.tagName === 'SLOT' && typeof node.assignedElements === 'function') {
    const assigned = node.assignedElements({ flatten: true });
    if (assigned.length > 0) return assigned;
  }
  const shadow = node.shadowRoot;
  if (shadow) return Array.from(shadow.children);
  return Array.from(node.children);
}

/**
 * Depth-first, pre-order walk of the composed tree, collecting every node FOCUSABLE_SELECTOR
 * matches: the shadow-piercing counterpart to `container.querySelectorAll(FOCUSABLE_SELECTOR)`.
 * ORDER matters as much as membership: the trap's wrap arithmetic (`nextTrapIndex`) assumes this
 * list IS tab order, so a nested custom element's own focusables must land exactly where that
 * element sits among its siblings, not appended after everything else.
 */
function collectComposed(node: FocusTreeNode, out: FocusTreeNode[]): void {
  for (const child of composedChildren(node)) {
    if (child.matches(FOCUSABLE_SELECTOR)) out.push(child);
    collectComposed(child, out);
  }
}

/** Tab-order-ordered, currently-reachable focusables inside a dialog container. */
export function focusablesIn(container: ParentNode | null | undefined): HTMLElement[] {
  if (!container) return [];
  const found: FocusTreeNode[] = [];
  collectComposed(container as unknown as FocusTreeNode, found);
  return (found as unknown as HTMLElement[]).filter(isReachable);
}

/**
 * The trap, as arithmetic: deliberately separated from the DOM so it can be tested without one.
 *
 * Returns the index the trap must MOVE focus to, or null to mean "the browser's own Tab handling is
 * already correct here, do not preventDefault". Returning null in the common case matters: a trap
 * that preventDefaults every Tab and re-implements the whole order gets the order wrong the moment a
 * dialog contains something with a positive tabindex, a radio group, or a nested shadow root. The
 * browser is better at this than we are; the trap's only job is the two WRAP points.
 */
export function nextTrapIndex(count: number, activeIndex: number, shiftKey: boolean): number | null {
  if (count <= 0) return null;
 // Focus is outside the dialog (or on the container itself): pull it back to the near edge.
  if (activeIndex < 0) return shiftKey ? count - 1 : 0;
  if (shiftKey) return activeIndex === 0 ? count - 1 : null;
  return activeIndex === count - 1 ? 0 : null;
}

/**
 * Where focus goes when the dialog opens. An explicit `data-autofocus` wins so a component can name
 * the control a person actually wants (the search box in the customer picker, the count field in a
 * till close); otherwise the first focusable, which for these dialogs is the first real control.
 *
 * Returns null only when the dialog has no focusable content at all, in which case the caller falls
 * back to the container.
 */
export function pickInitialIndex(count: number, autofocusIndex: number): number | null {
  if (count <= 0) return null;
  if (autofocusIndex >= 0 && autofocusIndex < count) return autofocusIndex;
  return 0;
}

export interface ModalFocusOptions {
 /** The dialog's outermost element (the scrim/backdrop), resolved fresh each update. */
  container: () => HTMLElement | null | undefined;
 /** Is the dialog open? Defaults to "whenever the host is connected", for parent-mounted modals. */
  isOpen?: () => boolean;
 /**
 * Called when Escape is pressed. Omit for a dialog that must NOT be escapable: a modal that is
 * blocking on an irreversible acknowledgement (kiosk takeover) or on a document the operator has
 * to read (the till-close Z-report) is not "dismissable", and giving it an Escape would lose the
 * thing it exists to force.
 */
  onEscape?: () => void;
 /**
 * Per-STEP escapability, for wizard dialogs whose safety changes as they progress. Consulted only
 * when `onEscape` is present. Defaults to always-safe.
 */
  escapeSafe?: () => boolean;
}

/**
 * ModalFocus: attach one per dialog. Two lifecycles are supported because the app has both:
 *
 * parent-mounted the parent renders `${open ? html`<hos-thing>` : nothing}`, so connected IS open.
 * Omit `isOpen`.
 * self-managed the element is always in the tree and an @state field opens it. Pass `isOpen`.
 */
export class ModalFocus implements ReactiveController {
  private readonly host: ReactiveControllerHost & HTMLElement;
  private readonly opts: ModalFocusOptions;
  private armed = false;
  private restoreTo: HTMLElement | null = null;

  constructor(host: ReactiveControllerHost & HTMLElement, opts: ModalFocusOptions) {
    this.host = host;
    this.opts = opts;
    host.addController(this);
  }

  hostConnected(): void {
 // keydown is composed, so it retargets to the host and bubbles out of the shadow root: one
 // listener on the host sees every key pressed anywhere inside the dialog.
    this.host.addEventListener('keydown', this.onKeydown);
  }

  hostDisconnected(): void {
    this.host.removeEventListener('keydown', this.onKeydown);
    this.disarm();
  }

  hostUpdated(): void {
 // After render, so the container and its controls exist to be measured and focused.
    const open = this.opts.isOpen ? this.opts.isOpen() : true;
    if (open && !this.armed) this.arm();
    else if (open && this.armed) this.recoverIfDropped();
    else if (!open && this.armed) this.disarm();
  }

 /**
 * Re-establish focus when the browser has DROPPED it to the document.
 *
 * Found by the Chromium keyboard walk, on the till-close wizard: advancing from the count step to
 * the Z-report replaces the whole card, and the control that had focus is removed with it. The
 * browser's response is to reset focus to <body>: outside the host. The trap survives (the next
 * Tab pulls focus back, because nextTrapIndex treats "outside" as a wrap to the near edge), but in
 * the meantime the dialog has NO focus, a screen reader announces nothing, and: the part that
 * actually bites: ESCAPE STOPS WORKING, because the keydown listener is on the host and a keydown
 * on <body> never reaches it. A wizard could strand a keyboard user on a step they could not leave.
 *
 * The recovery is deliberately narrow: it fires ONLY when focus is on <body> or nowhere. Any other
 * location means something else legitimately owns focus, and stealing it back would be worse than
 * the bug: the three panes that raise two dialogs at once (rule-editor, quantity-breaks,
 * customer-pricing) would have their two controllers pulling focus off each other on every update,
 * a tug-of-war with no stable state.
 */
  private recoverIfDropped(): void {
    const active = deepActiveElement(document);
    if (active && active !== document.body) return;
    const container = this.opts.container();
    if (!container) return;
    this.focusInitial(container);
  }

  private arm(): void {
    const container = this.opts.container();
    if (!container) return; // not painted yet; hostUpdated will come round again
    this.armed = true;
 // Whoever opened this is where focus goes back to. Captured BEFORE we move it.
    const previous = deepActiveElement(document);
    this.restoreTo = previous instanceof HTMLElement ? previous : null;
    this.focusInitial(container);
  }

  private focusInitial(container: HTMLElement): void {
    const items = focusablesIn(container);
    const autofocusIndex = items.findIndex((el) => el.hasAttribute('data-autofocus'));
    const index = pickInitialIndex(items.length, autofocusIndex);
    if (index !== null) {
      items[index].focus();
      return;
    }
 // No controls at all (a pure message dialog): focus the container so a screen reader announces
 // the dialog and Escape still reaches us, rather than leaving focus behind the scrim.
    if (!container.hasAttribute('tabindex')) container.setAttribute('tabindex', '-1');
    container.focus();
  }

  private disarm(): void {
    if (!this.armed) return;
    this.armed = false;
    const target = this.restoreTo;
    this.restoreTo = null;
 // Only restore if it is still in the document: a dialog that replaced its own invoker (a row
 // that got deleted) must not throw, and must not steal focus to a detached node.
    if (target && target.isConnected && typeof target.focus === 'function') target.focus();
  }

  private readonly onKeydown = (e: KeyboardEvent): void => {
    const open = this.opts.isOpen ? this.opts.isOpen() : true;
    if (!open) return;

    if (e.key === 'Escape') {
      if (!this.opts.onEscape) return; // deliberately non-dismissable
      if (this.opts.escapeSafe && !this.opts.escapeSafe()) return;
      e.preventDefault();
 // A dialog is the innermost thing on screen; a parent must not ALSO act on this Escape.
      e.stopPropagation();
      this.opts.onEscape();
      return;
    }

    if (e.key !== 'Tab') return;
    const container = this.opts.container();
    if (!container) return;
    const items = focusablesIn(container);

 // A dialog with NOTHING focusable in it still has to hold the keyboard. Found by the Chromium
 // walk on till-close's Z-report step, which renders as prose while its report loads: the trap
 // had no item to move to, returned "let the browser handle it", and the browser handled it by
 // tabbing out of the modal into the shell behind the scrim: Home, Log out, the theme toggle.
 // Swallowing the Tab and holding focus on the container is the only correct answer here; there
 // is nowhere else inside the dialog for focus to legitimately go.
    if (items.length === 0) {
      e.preventDefault();
      if (!container.hasAttribute('tabindex')) container.setAttribute('tabindex', '-1');
      container.focus();
      return;
    }

    const active = deepActiveElement(document);
    const activeIndex = active instanceof HTMLElement ? items.indexOf(active) : -1;
    const target = nextTrapIndex(items.length, activeIndex, e.shiftKey);
    if (target === null) return; // the browser's own Tab is correct here
    e.preventDefault();
    items[target].focus();
  };
}
