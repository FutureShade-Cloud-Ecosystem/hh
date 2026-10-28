// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-signature.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { captureTypedSignature } from './pos-signature-api.js';
import {
  canSubmitAttestation,
  attestationBlockedReason,
  normalizeTypedName,
} from './pos-signature-logic.js';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';

/**
 * DIB-386: typed-name attestation capture.
 *
 * WHAT CHANGED. This component used to render a dashed box that set `drawn = true` on pointerdown
 * and then POSTed a capture whose sealed payload was the literal string
 * `SIMULATED:<purpose>:<ref>`. It looked like a signature pad and captured nothing: no name, no
 * consent, no act. A pad implies ink nobody recorded.
 *
 * WHAT IT IS NOW. The ruled act (, the same ruling the DIB-57 quote acceptance ships): a
 * typed full name plus an explicit affirmative, presented honestly as an electronic attestation. The
 * COMPONENT API IS UNCHANGED: same tag, same `purpose` / `refType` / `refId` properties, same
 * `pos-signature` event carrying `{ ref }`: so pos-terminal and pos-assess-relief need no edit.
 *
 * The submit RULE lives in pos-signature-logic.ts and is unit-tested there (this class is DOM-bound).
 */
@customElement('hos-pos-signature')
export class HosPosSignature extends LitElement {
  @property() purpose = 'ORDER_SIGNOFF';
  @property() refType = 'order';
  @property() refId = '';

  @state() private typedName = '';
  @state() private affirmed = false;
  @state() private busy = false;
  @state() private ref = '';
  @state() private error = '';

  static styles = [
    focusRing,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
    }
    .attest {
      border: 1px solid var(--hos-line);
      border-radius: 10px;
      background: var(--hos-deep-earth);
      padding: 10px 12px;
    }
    .lede {
      font-size: 12px;
      color: var(--hos-text-dim);
      margin-bottom: 8px;
    }
    label {
      display: block;
      font-size: 12px;
      color: var(--hos-text-dim);
      margin-bottom: 4px;
    }
    input[type='text'] {
      width: 100%;
      font: inherit;
      font-family: var(--hos-font-ui);
      padding: 8px 10px;
      border-radius: 8px;
      border: 1px solid var(--hos-line);
      background: transparent;
      color: var(--hos-text);
    }
    .check {
      display: flex;
      gap: 8px;
      align-items: flex-start;
      margin-top: 10px;
      font-size: 12px;
      line-height: 1.4;
      color: var(--hos-text);
    }
    .check input {
      margin-top: 2px;
    }
    .row {
      display: flex;
      gap: 10px;
      align-items: center;
      margin-top: 10px;
      flex-wrap: wrap;
    }
    button {
      background: var(--hos-stone-amber);
      color: #0b1725;
      border: 0;
      border-radius: 8px;
      padding: 8px 14px;
      font-weight: 600;
      font-family: inherit;
      cursor: pointer;
    }
    button.ghost {
      background: transparent;
      color: var(--hos-text-dim);
      border: 1px solid var(--hos-line);
    }
    button:disabled {
      background: var(--hh-disabled-fill);
      border-color: var(--hh-disabled-line);
      color: var(--hh-disabled-ink);
      opacity: 1;
      cursor: not-allowed;
    }
    .hint {
      font-size: 12px;
      color: var(--hos-text-dim);
    }
    .ref {
      font-family: var(--hos-font-mono);
      font-size: 12px;
      color: var(--hos-success);
    }
    .err {
      color: var(--hos-safety-red);
      font-size: 12px;
      margin-top: 6px;
    }
  `,
  ];

  private get draft() {
    return { typedName: this.typedName, affirmed: this.affirmed, busy: this.busy };
  }

  private async capture(): Promise<void> {
    // The gate is enforced HERE as well as on the button's disabled state: a click that arrives some
 // other way must not produce an unaffirmed attestation. (The server refuses it either way: this
    // is the courtesy, not the authority.)
    if (!canSubmitAttestation(this.draft)) return;
    this.busy = true;
    this.error = '';
    try {
      const out = await captureTypedSignature(
        this.purpose,
        this.refType,
        this.refId,
        normalizeTypedName(this.typedName),
        this.affirmed,
      );
      this.ref = out.signature_ref;
      this.dispatchEvent(
        new CustomEvent('pos-signature', { detail: { ref: this.ref }, bubbles: true, composed: true }),
      );
    } catch (e) {
      this.error = e instanceof Error ? e.message : 'capture failed';
    } finally {
      this.busy = false;
    }
  }

  private clear(): void {
    this.typedName = '';
    this.affirmed = false;
    this.ref = '';
    this.error = '';
  }

  render() {
    const ready = canSubmitAttestation(this.draft);
    const blocked = attestationBlockedReason(this.draft);
    return html`
      <div class="attest">
        <div class="lede">
          Electronic attestation: type the signer's full name and confirm. This is a signed
          statement, not a captured handwritten signature.
        </div>
        <label for="typed-name">Full name of the person signing</label>
        <input
          id="typed-name"
          type="text"
          autocomplete="off"
          spellcheck="false"
          placeholder="The customer's full name"
          .value=${this.typedName}
          ?disabled=${this.busy}
          @input=${(e: Event) => (this.typedName = (e.target as HTMLInputElement).value)}
        />
        <label class="check">
          <input
            type="checkbox"
            .checked=${this.affirmed}
            ?disabled=${this.busy}
            @change=${(e: Event) => (this.affirmed = (e.target as HTMLInputElement).checked)}
          />
          <span>
            I confirm that I am the person named above and that typing my name here is my signature
            for this ${this.purpose.toLowerCase().replace(/_/g, ' ')}.
          </span>
        </label>
        <div class="row">
          <button ?disabled=${!ready} @click=${this.capture}>
            ${this.busy ? 'Signing…' : 'Sign'}
          </button>
          <button class="ghost" ?disabled=${this.busy} @click=${this.clear}>Clear</button>
          ${blocked && !this.ref ? html`<span class="hint">${blocked}</span>` : nothing}
          ${this.ref ? html`<span class="ref">Signed · ${this.ref}</span>` : nothing}
        </div>
        ${this.error ? html`<div class="err" role="alert">${this.error}</div>` : nothing}
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-signature': HosPosSignature;
  }
}
