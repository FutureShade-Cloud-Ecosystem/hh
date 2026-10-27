// Ported from the ERP's app/src/customer-picker.ts (the inline type-ahead the quote builder uses; the kit carries only the till's modal picker); adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { api } from '@hardscapehouse/pos-kit/src/ported/ports.js';
import { spokenRefusal } from './_quote-workarea.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import {
  customerTypeWord,
  isSearchable,
  nextActive,
  orderResults,
  pickerStatus,
  recentsKey,
  rememberRecent,
  termsLabel,
  type PickerCustomer,
} from '@hardscapehouse/pos-kit/src/ported/_customer-picker.js';

/**
 * `<hos-customer-picker>`, an inline, keyboard-driven account type-ahead over
 * GET /api/v1/customers/search. Replaces the quote builder's raw "Customer id" text box, which
 * asked a human to know a uuid and answered a typo with a bare 422.
 *
 * INLINE, NOT A MODAL. `<hos-pos-customer-picker>` is a modal because the POS ring is a full-screen
 * task with one decision at a time. Building a quote is a form: the customer sits beside the branch
 * and the expiry, and a dialog that covers them would break the Tab order of the form it belongs to.
 * The two share the endpoint, not the chrome.
 *
 * KEYBOARD CONTRACT (ARIA 1.2 combobox):
 *   type          → debounced search, list opens
 *   Down / Up     → move the highlight, wrapping; Down from nothing lands on the FIRST row
 *   Enter         → commit the highlighted row (never a row the user cannot see), or the lone match when there is exactly one
 *   Escape        → close the list, keep what was typed
 *   Tab           → leave the field; an open list closes without committing
 * Nothing here is mouse-only: every row is a real <li role="option"> driven by aria-activedescendant,
 * so the input keeps DOM focus and the ring stays where the user is typing.
 *
 * SCOPE, STATED PLAINLY: the RESULT SET is whatever the server returns, this component does not
 * filter it. Recency is branch-scoped (see _customer-picker.recentsKey), and since U-park E4 the
 * SEARCH is too: the fence (customer/search_scope.go) 403s a branch-scoped caller that names no
 * branch, so this picker sends its branch-id on the search URL (DIB-506 fix pass).
 *
 * DIB-532, inline add-new-customer, PORTED FROM POS, NOT REBUILT. `hos-pos-customer-picker`
 * already has a complete, proven create flow (its own comment: "REUSED, not rebuilt") over the
 * EXISTING manager-gated `POST /api/v1/customers`, same fields (name, account #, type), same
 * server validation, same 403/409 handling. The quote builder's picker had none, so a quote could
 * only ever go to a customer who already existed, the tester's exact question ("is there a way
 * to make a new customer in the quote section?") had no answer here. This is that same create
 * flow, ported into this component: the fields, the wording, and the permission gate are IDENTICAL
 * to POS's, this component does not widen or narrow who may create an account, it only asks the
 * same server the same question from a second screen. The two pickers could not share the create
 * LOGIC itself without a third file (a shared `_customer-picker.ts` helper) outside this lane's
 * file set, so it is ported as component methods here, exactly as it already lives as component
 * methods on `hos-pos-customer-picker`, no new shared module, no second create path.
 */
@customElement('hos-customer-picker')
export class HosCustomerPicker extends LitElement {
  /**
   * delegatesFocus, used deliberately and narrowly.
   *
   * Without it, `picker.focus()` from the host form focuses the HOST element, which has no
   * caret, and the user is left looking at a field they cannot type into. With it, focus lands on
   * the first focusable node in the shadow root (the search input, or the Change button once an
   * account is chosen), and `:host(:focus-visible)` paints the shared ring on the right thing.
   *
   * It is NOT applied blanket across the app: delegatesFocus also changes click-to-focus (a click
   * anywhere in the host focuses that first control), which is right for a single-purpose field
   * like this one and wrong for a panel full of unrelated controls. Each additional use needs its
   * own keyboard check, so they are added one at a time rather than swept in.
   */
  static shadowRootOptions = { ...LitElement.shadowRootOptions, delegatesFocus: true };

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        position: relative;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      .field {
        display: flex;
        flex-direction: column;
        gap: 4px;
      }
      label {
        font-size: 0.76rem;
        color: var(--hos-text-dim);
      }
      input {
        font: inherit;
        font-size: 0.83rem;
        padding: 5px 7px;
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        background: var(--hos-deep-earth);
        color: var(--hos-text);
        min-width: 15rem;
        /* The row's one control height (demo polish round 2): the search
           input stands at the design system's input height token, the same
           height the branch select, the expiry date input and the Create
           button beside it carry, instead of shrinking to its content. */
        box-sizing: border-box;
        min-height: var(--hh-h-input, 44px);
      }
      /* The chosen account, shown as a chip in place of the raw uuid the field used to hold. */
      .chosen {
        display: flex;
        align-items: center;
        gap: 8px;
        border: 1px solid var(--hos-stone-amber);
        border-radius: var(--hos-radius-control);
        padding: 4px 6px 4px 9px;
        font-size: 0.83rem;
        min-width: 15rem;
      }
      .chosen .who {
        display: flex;
        flex-direction: column;
        line-height: 1.25;
      }
      .chosen .sub {
        color: var(--hos-text-dim);
        font-size: 0.72rem;
      }
      .chosen button {
        margin-left: auto;
        font: inherit;
        font-size: 0.76rem;
        background: transparent;
        color: var(--hos-text-dim);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        padding: 2px 8px;
        cursor: pointer;
      }
      /* the search input plus the add-new-customer affordance beside it */
      .inputrow {
        display: flex;
        gap: 6px;
        align-items: center;
      }
      .inputrow input {
        flex: 1 1 auto;
        min-width: 0;
      }
      button.addnew {
        flex: 0 0 auto;
        display: inline-grid;
        place-items: center;
        width: 28px;
        height: 28px;
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        background: var(--hos-deep-earth);
        color: var(--hos-text-dim);
        cursor: pointer;
        padding: 0;
      }
      button.addnew:hover,
      button.addnew[aria-expanded='true'] {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
      /* the inline create form, ported from the POS picker's own createform */
      .createform {
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card);
        background: var(--hos-slate-warm);
        padding: 10px 11px;
        margin-top: 6px;
        display: flex;
        flex-direction: column;
        gap: 3px;
        min-width: 15rem;
      }
      .createform label {
        font-size: 0.72rem;
        text-transform: uppercase;
        letter-spacing: 0.06em;
        color: var(--hos-text-dim);
        margin-top: 7px;
      }
      .createform label:first-child {
        margin-top: 0;
      }
      .createform input,
      .createform select {
        font: inherit;
        font-size: 0.83rem;
        padding: 5px 7px;
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        background: var(--hos-deep-earth);
        color: var(--hos-text);
      }
      .createform .actions {
        display: flex;
        justify-content: flex-end;
        gap: 8px;
        margin-top: 10px;
      }
      .createform button.cancel {
        font: inherit;
        font-size: 0.76rem;
        background: transparent;
        color: var(--hos-text-dim);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        padding: 5px 10px;
        cursor: pointer;
      }
      .createform button.primary {
        font: inherit;
        font-size: 0.76rem;
        font-weight: 600;
        border: 1px solid var(--hos-stone-amber);
        border-radius: var(--hos-radius-control);
        background: var(--hos-stone-amber);
        color: var(--hos-stone-amber-ink);
        padding: 5px 12px;
        cursor: pointer;
      }
      .createform button.primary:hover:not(:disabled) {
        background: var(--hos-stone-amber-hover);
        border-color: var(--hos-stone-amber-hover);
        color: var(--hos-stone-amber-ink);
      }
      .createform button.primary:disabled {
        opacity: 0.45;
        cursor: not-allowed;
      }
      .createform .err {
        color: var(--hos-safety-red);
        font-size: 0.74rem;
      }
      .createform .hint {
        color: var(--hos-text-dim);
        font-size: 0.7rem;
        margin-top: 7px;
      }
      ul {
        position: absolute;
        z-index: var(--hos-z-dropdown);
        left: 0;
        /* DIB-531, THE LIST SITS BELOW THE FIELD, and it must be SAID rather than left to the
           static position. This rule shipped with left and no top, which is not "wherever the list
           would have been in flow": an absolutely-positioned child of a FLEX container takes its
           static position from the flex container's own content-box corner, so the list resolved to
           top:0 of .field and painted itself over the label and the whole 28px of the input. The
           tester's words were exact, "the pop up blocks the spot that i'm typing", and a probe at
           1440 and 1024 measured the list covering all 28px of the input, with elementFromPoint at
           the caret returning a result row rather than the input. product-picker.ts always said its
           top out loud; this is the same line, and the reason it is not optional. */
        top: calc(100% + 2px);
        /* The picker often sits in a narrow column (the quote builder's 340px sidebar). Letting the
           list take the field's width there wraps every account name across three lines and makes
           the list unreadable, so it is allowed to overhang, capped at the viewport so it cannot
           run off-screen on a phone. */
        min-width: min(26rem, 86vw);
        margin: 0;
        padding: 4px;
        list-style: none;
        max-height: 17rem;
        overflow-y: auto;
        background: var(--hos-slate-warm);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card);
        box-shadow: var(--hos-elev-2);
      }
      li {
        padding: 7px 8px;
        border-radius: var(--hos-radius-control);
        cursor: pointer;
        display: flex;
        gap: 10px;
        align-items: baseline;
      }
      /* The keyboard highlight. aria-activedescendant moves; DOM focus stays in the input, so this
         is the ONLY signal of where Enter will land and it has to be unmistakable. */
      li[aria-selected='true'] {
        background: color-mix(in srgb, var(--hos-stone-amber) 22%, transparent);
        box-shadow: inset 2px 0 0 var(--hos-stone-amber);
      }
      li .who {
        display: flex;
        flex-direction: column;
        line-height: 1.3;
        min-width: 0;
      }
      li .name {
        font-weight: 600;
      }
      li .meta {
        color: var(--hos-text-dim);
        font-size: 0.74rem;
      }
      li .terms {
        margin-left: auto;
        color: var(--hos-text-dim);
        font-size: 0.74rem;
        white-space: nowrap;
      }
      li .recent {
        color: var(--hos-stone-amber-text);
        font-size: 0.68rem;
        text-transform: uppercase;
        letter-spacing: 0.08em;
      }
      .note {
        font-size: 0.74rem;
        color: var(--hos-text-dim);
        padding: 6px 8px;
      }
      .note.bad {
        color: var(--hos-safety-red);
      }
      /* Visually hidden but announced. The list changes as the user types; without this a screen
         reader says nothing at all. */
      .sr {
        position: absolute;
        width: 1px;
        height: 1px;
        overflow: hidden;
        clip: rect(0 0 0 0);
        white-space: nowrap;
      }
    `,
  ];

  /** Scopes the recently-used list. Empty is a valid, separate bucket (see recentsKey). */
  @property({ attribute: 'branch-id' }) branchId = '';

  @property({ attribute: 'label-text' }) labelText = 'Customer';

  /** The currently chosen account, or null. The host reads `.selected.id` to build its payload. */
  @property({ attribute: false }) selected: PickerCustomer | null = null;

  @state() private query = '';
  @state() private results: PickerCustomer[] = [];
  @state() private open = false;
  @state() private active = -1;
  @state() private loading = false;
  @state() private error = '';
  @state() private recents: string[] = [];

  // ── DIB-532: the add-new-customer flow, PORTED from hos-pos-customer-picker, not rebuilt. The
  // create door is the EXISTING manager-gated POST /api/v1/customers; this is only a second
  // screen's way to knock on it. No second create path exists.
  @state() private creating = false;
  @state() private newName = '';
  @state() private newAccountNo = '';
  @state() private newType = 'CONTRACTOR';
  @state() private createBusy = false;
  /**
   * DIB-636. Set when the create form is left while a POST is still in flight. The request is not
   * cancellable and the row is created regardless; this only decides whether the answer is allowed
   * to select itself into a form the user has already walked away from.
   *
   * IN PRACTICE ONE GESTURE REACHES THIS, and an earlier comment here claimed three. Escape does
   * not (it closes the results list, and the search field is disabled while creating), and the
   * host closing the picker unmounts the element rather than calling toggleCreate. What is left
   * is the create toggle itself, which stays enabled while the two buttons below it do not -
   * so it is the one control a person can still press mid-flight, and it is the one that needed
   * this.
   */
  private createAbandoned = false;
  /** Set when an abandoned create nevertheless succeeded; shown once on the next open. */
  @state() private abandonedNotice = '';
  @state() private createError = '';

  private debounce = 0;
  /** Monotonic request id, a slow earlier search must not overwrite a fast later one. */
  private seq = 0;

  /** DIB-442: the results list overhangs its narrow column; while it was open, the first click on
   *  anything beneath the overhang only dismissed the list, the click itself was eaten. Closing on
   *  outside POINTERDOWN (capture) means the list is gone before the click dispatches, so that
   *  first click lands where the user aimed. */
  private readonly onOutsidePointerDown = (e: Event) => {
    if (this.open && !e.composedPath().includes(this)) this.open = false;
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.recents = this.readRecents();
    document.addEventListener('pointerdown', this.onOutsidePointerDown, true);
    this.renderRoot.addEventListener('click', this.onHostClick as EventListener);
    this.renderRoot.addEventListener('input', this.onHostInput as EventListener);
    this.renderRoot.addEventListener('change', this.onHostChange as EventListener);
    this.renderRoot.addEventListener('keydown', this.onHostKeyDown as EventListener);
    this.renderRoot.addEventListener('focusin', this.onHostFocusIn as EventListener);
    this.renderRoot.addEventListener('mousedown', this.onHostMouseDown as EventListener);
  }

  disconnectedCallback(): void {
    if (this.debounce) clearTimeout(this.debounce);
    document.removeEventListener('pointerdown', this.onOutsidePointerDown, true);
    this.renderRoot.removeEventListener('click', this.onHostClick as EventListener);
    this.renderRoot.removeEventListener('input', this.onHostInput as EventListener);
    this.renderRoot.removeEventListener('change', this.onHostChange as EventListener);
    this.renderRoot.removeEventListener('keydown', this.onHostKeyDown as EventListener);
    this.renderRoot.removeEventListener('focusin', this.onHostFocusIn as EventListener);
    this.renderRoot.removeEventListener('mousedown', this.onHostMouseDown as EventListener);
    super.disconnectedCallback();
  }

  willUpdate(changed: Map<string, unknown>): void {
    // Switching branch switches which recency list applies. Set before the render, not after it:
    // state set in updated() schedules a second update (Lit's "update after an update completed").
    if (changed.has('branchId')) this.recents = this.readRecents();
  }

  updated(): void {
    this.syncFields();
  }

  /**
   * The values and the option ids are set here, not bound in the template:
   * the app's binding gate refuses the property forms and a bound id, so the
   * element carries them after each render instead.
   */
  private syncFields(): void {
    const root = this.renderRoot;
    const setValue = (field: string, value: string): void => {
      const el = root.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${field}"]`);
      if (el !== null && el.value !== value) el.value = value;
    };
    setValue('search', this.query);
    setValue('new-name', this.newName);
    setValue('new-account', this.newAccountNo);
    setValue('new-type', this.newType);
    root.querySelectorAll<HTMLElement>('li[data-index]').forEach((li) => {
      li.id = `opt-${li.getAttribute('data-index') ?? ''}`;
    });
    const search = root.querySelector<HTMLInputElement>('[data-field="search"]');
    if (search !== null) {
      if (this.active >= 0) search.setAttribute('aria-activedescendant', `opt-${this.active}`);
      else search.removeAttribute('aria-activedescendant');
    }
  }

  private readonly onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-action'));
    if (control === undefined) return;
    switch (control.getAttribute('data-action')) {
      case 'clear':
        this.clear();
        break;
      case 'toggle-create':
        this.toggleCreate();
        break;
      case 'save-create':
        void this.createCustomer();
        break;
    }
  };

  private readonly onHostInput = (event: Event): void => {
    const field = (event.target as HTMLElement | null)?.getAttribute?.('data-field');
    const value = (event.target as HTMLInputElement).value;
    if (field === 'search') this.onInput(event);
    else if (field === 'new-name') this.newName = value;
    else if (field === 'new-account') this.newAccountNo = value;
  };

  private readonly onHostChange = (event: Event): void => {
    const field = (event.target as HTMLElement | null)?.getAttribute?.('data-field');
    if (field === 'new-name') this.suggestAccountNo();
    else if (field === 'new-type') this.newType = (event.target as HTMLSelectElement).value;
  };

  private readonly onHostKeyDown = (event: KeyboardEvent): void => {
    if ((event.target as HTMLElement | null)?.getAttribute?.('data-field') === 'search') this.onKeyDown(event);
  };

  private readonly onHostFocusIn = (event: Event): void => {
    if ((event.target as HTMLElement | null)?.getAttribute?.('data-field') !== 'search') return;
    if (!this.creating && this.results.length > 0) this.open = true;
  };

  /** mousedown, not click: click fires after blur, and blur has already closed the list. */
  private readonly onHostMouseDown = (event: MouseEvent): void => {
    const row = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute('data-index'));
    if (row === undefined) return;
    event.preventDefault();
    const customer = this.results[Number(row.getAttribute('data-index'))];
    if (customer !== undefined) this.choose(customer);
  };

  /**
   * The recency lists live in memory for the session, one per branch key. The
   * ERP kept them in browser storage; this app's sink gate holds the tree free
   * of any browser store, so a reload starts the list empty (recency is a
   * convenience, never a precondition).
   */
  private static readonly sessionRecents = new Map<string, string[]>();

  private readRecents(): string[] {
    return [...(HosCustomerPicker.sessionRecents.get(recentsKey(this.branchId)) ?? [])];
  }

  private writeRecents(list: string[]): void {
    HosCustomerPicker.sessionRecents.set(recentsKey(this.branchId), [...list]);
  }

  private onInput(e: Event): void {
    // The create form owns input focus while it is open (the input is also `disabled` then, so
    // this is the second of four layers, not the only one, see toggleCreate's doc comment).
    if (this.creating) return;
    this.query = (e.target as HTMLInputElement).value;
    this.active = -1;
    this.error = '';
    if (this.debounce) clearTimeout(this.debounce);
    if (!isSearchable(this.query)) {
      this.results = [];
      this.loading = false;
      this.open = false;
      return;
    }
    this.open = true;
    this.loading = true;
    this.debounce = window.setTimeout(() => void this.run(this.query), 220);
  }

  private async run(q: string): Promise<void> {
    const mine = ++this.seq;
    let failure = '';
    let rows: PickerCustomer[] = [];
    try {
      // DIB-506 fix pass: the branch rides the URL. The U-park E4 fence (customer/search_scope.go)
      // 403s any branch-SCOPED caller that names no branch, without this, only GLOBAL callers
      // could ever pick a customer here. branch-id was already a property (it scoped recency);
      // now it scopes the search itself, exactly as the fence expects.
      const branch = this.branchId ? `&branch_id=${encodeURIComponent(this.branchId)}` : '';
      const res = await api(`/api/v1/customers/search?q=${encodeURIComponent(q.trim())}&limit=20${branch}`);
      let body: unknown = null;
      try {
        body = await res.json();
      } catch {
        /* non-JSON body */
      }
      if (!res.ok) {
        // Speak the SERVER's sentence, a status code is not a way forward. Fall back to plain
        // words when the body carries none.
        failure = (body as { error?: string } | null)?.error || 'Could not search accounts, try again.';
      } else {
        rows = (body as PickerCustomer[] | null) ?? [];
      }
    } catch (e) {
      failure = e instanceof Error ? e.message : 'Could not search accounts.';
    }
    // A stale response must not repaint the list under a user who has typed on. Comparing a
    // sequence number is what makes the debounce correct rather than merely usual.
    if (mine !== this.seq) return;
    this.loading = false;
    if (failure !== '') {
      this.results = [];
      this.error = failure;
      return;
    }
    this.results = orderResults(rows, this.recents);
    this.error = '';
  }

  private onKeyDown(e: KeyboardEvent): void {
    // Same guard as onInput, the disabled input cannot dispatch this while creating, but the
    // handler stays defensive on its own (see toggleCreate's doc comment).
    if (this.creating) return;
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      // Only preventDefault for keys we actually handle, hijacking others would break the form.
      e.preventDefault();
      if (!this.open && this.results.length > 0) this.open = true;
      this.active = nextActive(this.active, e.key === 'ArrowDown' ? 1 : -1, this.results.length);
      this.scrollActiveIntoView();
      return;
    }
    if (e.key === 'Enter') {
      if (this.open && this.active >= 0 && this.active < this.results.length) {
        e.preventDefault(); // else the surrounding form would submit on the same keystroke
        this.choose(this.results[this.active]);
      } else if (this.open && this.active < 0 && this.results.length === 1 && !this.loading) {
        // A lone match is the one the person means: Enter takes it. With several and none highlighted Enter picks
        // nobody, so the picker never guesses an account.
        e.preventDefault();
        this.choose(this.results[0]);
      }
      return;
    }
    if (e.key === 'Escape') {
      if (this.open) {
        e.stopPropagation(); // a dialog host must not also close on this Escape
        this.open = false;
        this.active = -1;
      }
      return;
    }
    if (e.key === 'Tab') {
      // Leaving the field closes the list WITHOUT committing. Committing on blur is how pickers
      // silently pick the wrong account for someone who was tabbing past.
      this.open = false;
      this.active = -1;
    }
  }

  private scrollActiveIntoView(): void {
    void this.updateComplete.then(() => {
      this.renderRoot
        ?.querySelector('li[aria-selected="true"]')
        ?.scrollIntoView({ block: 'nearest' });
    });
  }

  private choose(c: PickerCustomer): void {
    this.selected = c;
    this.query = '';
    this.results = [];
    this.open = false;
    this.active = -1;
    // A freshly created account resolves the create form too, the next open of this picker
    // starts clean, not mid-form for an account that is already picked.
    this.creating = false;
    this.newName = '';
    this.newAccountNo = '';
    this.newType = 'CONTRACTOR';
    this.createError = '';
    const next = rememberRecent(this.recents, c.id);
    this.recents = next;
    this.writeRecents(next);
    this.dispatchEvent(
      new CustomEvent<PickerCustomer>('customer-picked', {
        detail: c,
        bubbles: true,
        composed: true,
      }),
    );
  }

  // ── DIB-532: drive the EXISTING create endpoint, then select the new account ──────────────────
  // Ported verbatim in behavior from hos-pos-customer-picker's createCustomer/suggestAccountNo/
  // toggleCreate, same request body, same status-code handling, same wording.

  /**
   * Adversarial-review fix (post-DIB-532): "never a search list AND a create form open at once"
   * was a COMMENT, not an invariant, `this.open` had three OTHER setters (onInput, the ArrowUp/
   * Down branch of onKeyDown, and the input's own @focus handler) that reopened the dropdown with
   * no `creating` check at all, so typing or refocusing the search box while the create form was up
   * reopened the list OVER it. A click on a result then ran `choose()`, which resets `creating` and
   * wipes the half-typed name/account/type, with no confirmation, and fires `customer-picked` for
   * the WRONG account, which `quote-workarea.ts`'s `createFor()` turns straight into a quote.
   *
   * Made true, not merely asserted, four ways: (1) this method also clears `results`, so even a
   * bypass shows nothing to click; (2) the three other setters are gated on `!this.creating` below;
   * (3) the list render itself is gated on `!this.creating` too, belt and suspenders, the render
   * cannot show a list no code path meant to open; (4) the search input is `disabled` while
   * creating, which is the browser's own guarantee that none of onInput/onKeyDown/@focus can even
   * fire from user interaction. Four layers because a comment was already "the fix" once.
   */
  private toggleCreate(): void {
    // DIB-636 · second layer. Save and Cancel are both disabled mid-flight; the create TOGGLE is
    // not, because it is how you leave the form, so it is the one control still reachable while a
    // POST is in the air. Leaving that way ABANDONS the create: the account is still made on the
    // server, nothing here can stop that, but it does not select itself behind the user's back,
    // and the field values go with it so a reopen cannot ask the server to make it twice.
    if (this.createBusy && this.creating) this.createAbandoned = true;
    this.creating = !this.creating;
    // A notice from a create the person walked away from outlives this reset, and is shown
    // once on the next open. Everything else about the previous attempt is cleared.
    this.createError = this.abandonedNotice;
    this.abandonedNotice = '';
    if (this.creating) {
      this.open = false;
      this.results = [];
      void this.updateComplete.then(() =>
        this.renderRoot.querySelector<HTMLInputElement>('[data-testid="customer-new-name"]')?.focus(),
      );
    }
  }

  /**
   * The account number offered by default: derived from the name (a recognisable "CUST-" slug)
   * but fully editable, the SERVER is the authority (409 on a duplicate, in words).
   */
  private suggestAccountNo(): void {
    if (this.newAccountNo.trim() !== '') return;
    const slug = this.newName
      .toUpperCase()
      .replace(/[^A-Z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 12);
    if (slug) this.newAccountNo = `CUST-${slug}`;
  }

  private async createCustomer(): Promise<void> {
    const name = this.newName.trim();
    const acct = this.newAccountNo.trim();
    if (name === '' || acct === '') {
      this.createError = 'Name and account # are both needed.';
      return;
    }
    this.createBusy = true;
    this.createError = '';
    try {
      const res = await api('/api/v1/customers', {
        method: 'POST',
        body: JSON.stringify({ account_no: acct, name, customer_type: this.newType }),
      });
      // The server's own words come first; the counter wording below is only the fallback when it sent none.
      const serverWords = async (fallback: string): Promise<string> => {
        try {
          const body = (await res.clone().json()) as { error?: string; detail?: string };
          const said = (body.detail || body.error || '').trim();
          return said === '' ? fallback : spokenRefusal(said);
        } catch {
          return fallback;
        }
      };
      if (res.status === 403) {
        // The write guard is the manager gate: change nothing else.
        this.createError = await serverWords('Creating accounts needs a manager sign-in.');
        return;
      }
      if (res.status === 409) {
        this.createError = await serverWords(`Account # ${acct} already exists, pick another.`);
        return;
      }
      if (!res.ok) {
        this.createError = await serverWords('Could not create the account.');
        return;
      }
      const acctBody = (await res.json()) as {
        customer_id: string;
        account_no: string;
        name: string;
        customer_type: string;
        payment_terms_days?: number;
        credit_limit_cents?: number;
        balance_cents?: number;
        available_credit_cents?: number;
      };
      // The new account is the answer to the picker's own question, select it, exactly the path
      // choosing an existing row takes (same event, same recents, same host wiring).
      // ...unless nobody is waiting for the answer any more (DIB-636).
      if (this.createAbandoned) {
        // The account EXISTS now. Leaving the form did not stop the request and nothing here
        // can, so the only question is what the person finds when they come back. Before this,
        // the fields kept their values: reopening and pressing Create again asked the server to
        // make an account that had just been made, and the counter person was told to "pick
        // another account number" for an account they were never told about. The form is
        // cleared and the next open says what happened, once.
        this.abandonedNotice = `${acctBody.name} was created while you were away. Search for it by name.`;
        this.newName = '';
        this.newAccountNo = '';
        return;
      }
      this.choose({
        id: acctBody.customer_id,
        name: acctBody.name,
        account_no: acctBody.account_no,
        customer_type: acctBody.customer_type,
        payment_terms_days: acctBody.payment_terms_days ?? 0,
        credit_limit_cents: acctBody.credit_limit_cents,
        open_balance_cents: acctBody.balance_cents,
        available_credit_cents: acctBody.available_credit_cents,
      });
    } catch {
      this.createError = 'Could not reach the server, try again.';
    } finally {
      this.createBusy = false;
      this.createAbandoned = false;
    }
  }

  /** The host starts a new quote on this same picker: nothing of the last attempt stays (no pick, no word, no list). */
  reset(): void {
    this.selected = null;
    this.query = '';
    this.results = [];
    this.open = false;
    this.active = -1;
  }

  private clear(): void {
    this.selected = null;
    this.query = '';
    this.results = [];
    this.open = false;
    this.active = -1;
    this.dispatchEvent(
      new CustomEvent('customer-cleared', { bubbles: true, composed: true }),
    );
    void this.updateComplete.then(() => {
      this.renderRoot?.querySelector<HTMLInputElement>('input')?.focus();
    });
  }

  private renderRow(c: PickerCustomer, i: number) {
    const isRecent = this.recents.includes(c.id);
    return html`<li
      data-index=${i}
      role="option"
      aria-selected=${this.active === i ? 'true' : 'false'}
    >
      <span class="who">
        <span class="name">${c.name}</span>
        <span class="meta">${c.account_no} · ${customerTypeWord(c.customer_type)}</span>
      </span>
      ${isRecent ? html`<span class="recent">recent</span>` : nothing}
      <span class="terms">${termsLabel(c.payment_terms_days)}</span>
    </li>`;
  }

  /** DIB-532: the minimal create form over the existing endpoint, name, account #, type. */
  private renderCreateForm() {
    return html`<div
      class="createform"
      data-testid="customer-create-form"
      role="group"
      aria-label="New customer account"
    >
      ${this.createError ? html`<p class="err" role="alert">${this.createError}</p>` : nothing}
      <label for="cp-new-name">Customer name</label>
      <input
        id="cp-new-name"
        data-testid="customer-new-name"
        data-field="new-name"
        autocomplete="off"
        placeholder="e.g. Greenridge Landscaping"
      />
      <label for="cp-new-account">Account #</label>
      <input
        id="cp-new-account"
        data-testid="customer-new-account"
        data-field="new-account"
        autocomplete="off"
        placeholder="e.g. CUST-1042"
      />
      <label for="cp-new-type">Account type</label>
      <select
        id="cp-new-type"
        data-testid="customer-new-type"
        data-field="new-type"
      >
        <option value="CONTRACTOR">Contractor</option>
        <option value="RETAIL">Retail</option>
        <option value="BUILDER">Builder</option>
        <option value="GOVERNMENT">Government</option>
      </select>
      <div class="actions">
        ${/* DIB-636, Cancel is disabled while the create is in flight, exactly as Save is.
             A request already on the wire cannot be recalled: the account gets made whether or not
             this form is still on screen. A Cancel that stays pressable through that is telling the
             user it will undo something it cannot undo, and the tester who filed this watched it
             create and select an account he had just cancelled. */ ''}
        <button
          type="button"
          class="cancel"
          data-testid="customer-create-cancel"
          data-action="toggle-create"
          ?disabled=${this.createBusy}
        >
          Cancel
        </button>
        <button
          type="button"
          class="primary"
          data-testid="customer-create-save"
          data-action="save-create"
          ?disabled=${this.createBusy}
        >
          ${this.createBusy ? 'Creating…' : 'Create & select'}
        </button>
      </div>
      <p class="hint">
        Creates the account through the standard customer-create flow (manager-gated; billing
        address and credit can be completed later in the account's own screens).
      </p>
    </div>`;
  }

  render() {
    const status = pickerStatus({
      query: this.query,
      loading: this.loading,
      error: this.error,
      count: this.results.length,
    });
    if (this.selected) {
      return html`
        <div class="field">
          <label>${this.labelText}</label>
          <div class="chosen">
            <span class="who">
              <span>${this.selected.name}</span>
              <span class="sub"
                >${this.selected.account_no} · ${customerTypeWord(this.selected.customer_type)} ·
                ${termsLabel(this.selected.payment_terms_days)}</span
              >
            </span>
            <button
              type="button"
              data-action="clear"
              aria-label=${`Change customer, currently ${this.selected.name}`}
            >
              Change
            </button>
          </div>
        </div>
      `;
    }
    return html`
      <div class="field">
        <label id="cp-label">${this.labelText}</label>
        <div class="inputrow">
          <input
            type="text"
            role="combobox"
            aria-expanded=${this.open ? 'true' : 'false'}
            aria-controls="cp-list"
            aria-labelledby="cp-label"
            aria-autocomplete="list"
            autocomplete="off"
            placeholder="Search name or account #…"
            data-field="search"
            ?disabled=${this.creating}
          />
          ${/* DIB-532, the add-new-customer affordance beside the search, opening the EXISTING
             create flow (the manager-gated POST /api/v1/customers), never a new one. */ ''}
          <button
            type="button"
            class="addnew"
            data-testid="customer-add-new"
            aria-expanded=${this.creating ? 'true' : 'false'}
            title="Add a new customer account"
            aria-label="Add a new customer account"
            data-action="toggle-create"
          >
            <svg
              viewBox="0 0 24 24"
              width="15"
              height="15"
              fill="none"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              aria-hidden="true"
            >
              <circle cx="10" cy="8" r="3.4"></circle>
              <path d="M4 19.5c1.2-3 3.4-4.5 6-4.5 1.1 0 2.1.25 3 .74"></path>
              <path d="M18 13.5v6M15 16.5h6"></path>
            </svg>
          </button>
        </div>
        ${this.creating ? this.renderCreateForm() : nothing}
        <span class="sr" role="status" aria-live="polite">${status}</span>
        ${this.open && !this.creating
          ? html`<ul id="cp-list" role="listbox" aria-label=${this.labelText}>
              ${this.error
                ? html`<li class="note bad" role="presentation">${this.error}</li>`
                : this.loading
                  ? html`<li class="note" role="presentation">Searching accounts…</li>`
                  : this.results.length === 0
                    ? html`<li class="note" role="presentation">
                        No accounts match “${this.query.trim()}”.
                      </li>`
                    : this.results.map((c, i) => this.renderRow(c, i))}
            </ul>`
          : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-customer-picker': HosCustomerPicker;
  }
}
