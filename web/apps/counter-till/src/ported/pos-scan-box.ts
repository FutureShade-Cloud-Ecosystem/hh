// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-scan-box.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { resolveScan, counterHitToProduct, type CounterHit } from './pos-api.js';
import { enterMissOutcome } from './_pos-rev8.js'; // rev 8: the scanner-vs-typist question, pure

/**
 * hos-pos-scan-box: the pos-barcode ticket. The scan input that turns a barcode into a priced line.
 *
 * WHAT A SCANNER ACTUALLY IS. A counter scanner is a keyboard. It types the code's digits as fast as
 * the USB HID stack allows and then presses Enter. That means the whole feature is: an input that is
 * focused, a submit on Enter, and a resolve that is unambiguous. There is no scanner API to
 * integrate with and no device permission to request: and any design that needs one is wrong.
 *
 * FOCUS IS THE FEATURE. A cashier does not click into the scan box before every scan; they pick up
 * the gun and shoot. So the box takes focus when the terminal mounts and takes it BACK after every
 * resolve, success or failure. Without that, scan #2 goes into whatever field the last click left
 * focused: most alarmingly a quantity box, where "0621483..." becomes an order for six hundred
 * thousand pavers.
 *
 * WHY IT ALSO ACCEPTS A TYPED SKU. Half the catalogue is bulk material with no label to scan. Making
 * the cashier choose between two boxes ("did this come from a gun or my fingers?") is a decision
 * they should never have to make, so the server tries barcode first and SKU second and this box
 * takes both.
 *
 * AN UNKNOWN CODE IS LOUD. It clears the box, keeps focus, and says so: never a silent no-op that
 * leaves the cashier believing a line was added.
 */
@customElement('hos-pos-scan-box')
export class HosPosScanBox extends LitElement {
 /** Branch for the on-hand shown on the resolved hit. */
  @property({ attribute: 'branch-id' }) branchId = '';
 /** When true the box grabs focus on first render (the counter's default posture). */
  @property({ type: Boolean, attribute: 'autofocus-scan' }) autofocusScan = false;
 /**
 * DIB-488: render the Add button as the workspace's PRIMARY action (gold fill). The scan bar is
 * the head of the items panel in the counter workspace, and the concept's hierarchy makes its add
 * the loudest control there. Off by default so every other mount is untouched.
 */
  @property({ type: Boolean, attribute: 'primary-add' }) primaryAdd = false;

 /**
 * rev 8 (item 1): the UNIFIED smart field: one box that is BOTH the scan box and the search
 * box. Off by default (every other mount keeps the plain scan box). In unified mode:
 *
 * typing every keystroke is forwarded (`pos-term-changed`) so the host feeds it to the
 * live search: debounce, chips interplay and the roving grid all live THERE,
 * untouched; this box adds nothing but the forwarding.
 * Enter tries the EXACT resolve first (barcode, then exact SKU: resolveScan, the same
 * server door as before). A hit commits instantly: same event, same confirmation
 * line, same clear + refocus (the whole scan contract stands verbatim). A MISS asks
 * the pure classifier (enterMissOutcome): a scanner burst keeps the LOUD
 * unknown-code answer; a typed entry falls through quietly to the search results
 * already on screen.
 *
 * The old two-box world is one property away: nothing about the plain mode changed.
 */
  @property({ type: Boolean, attribute: 'unified' }) unified = false;

 /**
 * DIB-506 fix pass: OPT-IN deferred outcome. On the POS ring a resolve IS the add (the line
 * lands client-side in the same gesture), so "Added …" on resolution is the truth. On the quote
 * work area the add is a server POST that can be REFUSED (margin floor, frozen status): there,
 * "Added" on resolution is a lie the cashier acts on. With this flag the box stays silent on a
 * successful resolve and the HOST speaks the actual add outcome through announce(). Off by
 * default: every existing mount (all of POS) is byte-identical.
 */
  @property({ type: Boolean, attribute: 'defer-outcome' }) deferOutcome = false;

 /** announce lets the host speak an add OUTCOME in the box's own message line (see deferOutcome). */
  announce(msg: string, kind: 'ok' | 'warn' | 'err'): void {
    this.msg = msg;
    this.msgKind = kind;
  }

 /**
 * commitDone: the host took the term's top hit (pos-term-commit-top), so the field behaves
 * exactly as it does after a successful scan: cleared, the search stood down, announced, and the
 * caret back in the box for the next item. Same ending for both muscles is the whole point of a
 * unified field.
 */
  commitDone(msg: string): void {
    this.code = '';
    this.msg = msg;
    this.msgKind = 'ok';
    this.emitTerm('');
    void this.updateComplete.then(() => this.focusInput());
  }

  @state() private code = '';
  @state() private busy = false;
  @state() private msg = '';
  @state() private msgKind: 'ok' | 'warn' | 'err' | '' = '';

 /** rev 8: inter-keystroke gaps (ms) for the current entry: feeds the burst classifier. */
  private keyGapsMs: number[] = [];
  private lastKeyAt = 0;

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      form {
        display: flex;
        gap: 8px;
        align-items: center;
      }
 /* DIB-512 · the visible label said what the placeholder already says ("Scan / Search" beside
         "Scan barcode or search product / SKU": the same instruction twice). Visually hidden, still
         the input's accessible name (the classic clip idiom); the placeholder carries the eye. */
      label {
        position: absolute;
        width: 1px;
        height: 1px;
        padding: 0;
        margin: -1px;
        overflow: hidden;
        clip: rect(0 0 0 0);
        white-space: nowrap;
        border: 0;
      }
      input {
        flex: 1;
        min-width: 0;
        background: var(--hos-deep-earth);
        color: var(--hos-text);
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-control);
        padding: 9px 11px;
        font-size: 14px;
        font-family: var(--hos-font-mono);
        letter-spacing: 0.04em;
      }
      input:disabled {
        opacity: 0.6;
      }
      input::placeholder {
        color: var(--hos-text-dim); /* rev 5: the UA default gray measured under 4.5:1 */
        opacity: 1;
      }
      button {
        background: transparent;
        border: 1px solid var(--hos-line);
        color: var(--hos-text-dim);
        border-radius: var(--hos-radius-control);
        padding: 8px 14px;
        font-size: 13px;
        font-family: inherit;
        cursor: pointer;
      }
      button:hover:not(:disabled) {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
      button:disabled {
        opacity: 0.5;
        cursor: default;
      }
 /* DIB-488: the primary-add posture: brand gold FILL carrying ONLY the near-black gold ink
         (the D-078 gold rule), hover to the gold hover fill. Same ink on hover: the fill family
         does not flip its ink. */
      button.primary {
        background: var(--hos-stone-amber);
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-ink);
        font-weight: 600;
      }
      button.primary:hover:not(:disabled) {
        background: var(--hos-stone-amber-hover);
        border-color: var(--hos-stone-amber-hover);
        color: var(--hos-stone-amber-ink);
      }
      .msg {
        margin-top: 6px;
        font-size: 12px;
        min-height: 16px;
      }
      .msg.ok {
        color: var(--hos-success);
      }
      .msg.warn {
        color: var(--hos-stone-amber-text);
      }
      .msg.err {
        color: var(--hos-safety-red);
      }
    `,
  ];

  firstUpdated(): void {
    if (this.autofocusScan) this.focusInput();
  }

 /** focusInput returns the caret to the scan box: called after every resolve and by the parent. */
  focusInput(): void {
    const el = this.renderRoot?.querySelector('input');
 // Guarded: the terminal calls this from a keyboard shortcut that can fire before first render.
    try {
      el?.focus();
      el?.select();
    } catch {
 /* detached: non-fatal */
    }
  }

 /**
 * rev 8: one input door for both modes. Records the inter-key gap (the burst classifier's food),
 * updates the code, and: unified only: forwards the term so the host drives the live search.
 * The forwarding is the ENTIRE search integration on this side: debounce/sequencing stay in the
 * grid, byte-identical to the two-box world.
 */
  private onInput(e: Event): void {
    const value = (e.target as HTMLInputElement).value;
    const now = Date.now();
    if (value.length > this.code.length && this.code.length > 0 && this.lastKeyAt > 0) {
      this.keyGapsMs.push(now - this.lastKeyAt);
    } else if (value.length <= 1) {
      this.keyGapsMs = []; // a fresh entry starts a fresh measurement
    }
    this.lastKeyAt = now;
    this.code = value;
    if (this.unified) this.emitTerm(value);
  }

 /**
 * ArrowDown walks OUT of the field and into the result list (the host owns the list, so the host
 * is told rather than reached into). Only in unified mode, and only downward: ArrowUp inside a
 * text field is a caret gesture the browser owns, and stealing it would break the one thing a
 * counter person does most in this box: correcting a typo.
 */
  private onFieldKey(e: KeyboardEvent): void {
    if (!this.unified || e.key !== 'ArrowDown') return;
    e.preventDefault();
    this.dispatchEvent(new CustomEvent('pos-term-enter-list', { bubbles: true, composed: true }));
  }

 /** rev 8: tell the host what the unified field holds (the host feeds the grid's live search). */
  private emitTerm(term: string): void {
    this.dispatchEvent(new CustomEvent('pos-term-changed', { detail: { term }, bubbles: true, composed: true }));
  }

  private async submit(e: Event): Promise<void> {
    e.preventDefault();
    const code = this.code.trim();
    if (code === '' || this.busy) return;
    const gaps = this.keyGapsMs;
    this.keyGapsMs = [];
    this.lastKeyAt = 0;
    this.busy = true;
    this.msg = '';
    this.msgKind = '';
    try {
      const hit = await resolveScan(code, this.branchId);
      if (!hit) {
 // rev 8 (unified): a MISSED Enter is two different moments. A scanner burst keeps the
 // scan box's LOUD contract below. A typed entry falls through QUIETLY: the live search
 // results (already on screen from the forwarded term) are the answer, and shouting
 // "no product" over them would call a working search broken.
        if (this.unified && enterMissOutcome(gaps, code.length) === 'fall-through-to-search') {
 // THE CLIENT'S ASK: "Enter takes the top hit." The box does not know what the top hit is
 //: the results list does: so it hands the moment to the host and the host commits.
 // A host that has no hit to give simply leaves the term standing, which is the pre-
 // existing quiet fall-through and still the right answer for a term nothing matches.
          this.dispatchEvent(
            new CustomEvent('pos-term-commit-top', {
              detail: { term: code },
              bubbles: true,
              composed: true,
            }),
          );
          return; // the term stays in the box and in the search: the results panel answers
        }
 // Loud, specific, and the code stays visible in the message so the cashier can compare it to
 // the label they just shot.
        this.msg = `No product for ${code}: check the label or search by name.`;
        this.msgKind = 'warn';
        this.code = '';
        if (this.unified) this.emitTerm(''); // a cleared box clears the search too
        return;
      }
      this.dispatchEvent(
        new CustomEvent('pos-scan-resolved', {
          detail: { hit, product: counterHitToProduct(hit) },
          bubbles: true,
          composed: true,
        }),
      );
      if (!this.deferOutcome) {
 // The POS contract, untouched: on the ring, resolution IS the add.
        this.msg = `Added ${hit.name} · ${hit.sku}`;
        this.msgKind = 'ok';
      }
      this.code = '';
      if (this.unified) this.emitTerm(''); // the commit ends the search: results stand down
    } catch (err) {
 // NOT "unknown barcode": a network or auth failure must never read as a bad label, or the
 // cashier will spend the outage re-shooting a perfectly good product.
      this.msg = err instanceof Error ? `Scan failed: ${err.message}` : 'Scan failed.';
      this.msgKind = 'err';
    } finally {
      this.busy = false;
 // The gun is already pointed at the next item. WAIT for the re-render: busy=false only
 // un-disables the input on the next update, and focus() on a disabled input is a silent
 // no-op: which is exactly how consecutive scanning was losing focus after every add.
      void this.updateComplete.then(() => this.focusInput());
    }
  }

  render() {
    return html`
      <form @submit=${this.submit}>
        <label for="scan">${this.unified ? 'Scan / Search' : 'Scan'}</label>
        <input
          id="scan"
          data-testid="pos-scan-input"
          .value=${this.code}
          ?disabled=${this.busy}
          placeholder=${this.unified
            ? 'Scan barcode or search product / SKU'
            : 'Scan a barcode or type a SKU, then Enter'}
          autocomplete="off"
          spellcheck="false"
          inputmode="text"
          aria-describedby="scan-msg"
          role=${this.unified ? 'combobox' : nothing}
          aria-autocomplete=${this.unified ? 'list' : nothing}
          aria-expanded=${this.unified ? (this.code.trim().length >= 2 ? 'true' : 'false') : nothing}
          @input=${(e: Event) => this.onInput(e)}
          @keydown=${(e: KeyboardEvent) => this.onFieldKey(e)}
        />
        <button type="submit" class=${this.primaryAdd ? 'primary' : ''} ?disabled=${this.busy || this.code.trim() === ''}>
          ${this.busy ? 'Looking up…' : 'Add'}
        </button>
      </form>
      ${/* aria-live so a screen-reader user hears the outcome without moving focus off the box. */ ''}
      ${/* Faults (network/auth: the 'err' kind) announce as alerts per the house rule; ok/warn
         outcomes stay a polite status so the scanning rhythm is never interrupted. */ ''}
      <div
        id="scan-msg"
        class="msg ${this.msgKind}"
        role=${this.msgKind === 'err' ? 'alert' : 'status'}
        aria-live=${this.msgKind === 'err' ? 'assertive' : 'polite'}
        data-testid="pos-scan-msg"
      >
        ${this.msg || nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-scan-box': HosPosScanBox;
  }
}

export type { CounterHit };
