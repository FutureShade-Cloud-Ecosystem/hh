// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-flow-steps.ts; adapted for HH.

import { LitElement, css, html } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { drawIcon, iconForId } from './_icon-consumer.js';
import type { FlowStep, FlowStepId } from './_pos-flow.js';

/**
 * hos-pos-flow-steps: DIB-488. The counter's 1 Items → 2 Customer → 3 Payment stepper.
 *
 * A REPORT, not a wizard: the steps come in fully derived (deriveFlow in _pos-flow.ts reads the
 * sale data; this component computes nothing) and every step is a plain button whose only action
 * is `pos-flow-goto`: the terminal scrolls the matching panel into view and focuses it. Nothing
 * here can lock, gate, or advance anything, by construction: there is no state to advance.
 *
 * TWO SCREENS MOUNT THIS, AND ONLY ONE OF THEM HAS AN ORDER (DIB-535). At the counter the four
 * steps label an accordion that shows one card at a time, so the numerals describe something real.
 * The quote builder mounts the same header over ONE continuous page: `gotoStep` there only scrolls
 * and focuses: so a numeral on that screen claims a sequence the screen does not have. A tester
 * read it exactly as drawn ("the tabs at the top saying 1 2 3 is not necessary. It should all just
 * be at the same page at once") and he was right: it was already one page, and the header was
 * telling him otherwise. `sequential=false` drops the numeral and leaves the pill: an empty ring
 * for open, a check for done, which is a CHECKLIST: state without an ordinal claim. The numbers are
 * not restyled away, they are not rendered, because the complaint was the claim and not the glyph.
 *
 * Presentation follows the concept's calm hierarchy with the approved palette: the current step
 * wears the stone-amber EDGE: the same hairline the open accordion card carries, never the gold
 * FILL it used to, because a fill is the commit action's clothes and this is an indicator (wave 2,
 * finding 19): completed steps show a check in the success ink, open steps stay dim. The
 * connectors are ordinary --hos-line hairlines.
 */
@customElement('hos-pos-flow-steps')
export class HosPosFlowSteps extends LitElement {
  /** The derived steps (deriveFlow output). Order is the render order. */
  @property({ attribute: false }) steps: FlowStep[] = [];

  /**
   * Does this mount have an order? True at the counter, where the steps front an accordion that
   * opens one card at a time; false on the quote builder, which is one page. See the file header.
   */
  @property({ type: Boolean }) sequential = true;

  /**
   * The nav's accessible name. It defaults to the counter's, because the counter is what this was
 * built for: but a screen that is not a sale must say so, or a screen reader announces "Sale
   * progress" over a quote.
   */
  @property() navLabel = 'Sale progress';

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      nav {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: var(--hos-space-2);
      }
      .connector {
        width: 18px;
        height: 1px;
        background: var(--hos-line);
        flex: none;
      }
      button {
        display: inline-flex;
        align-items: center;
        gap: 8px;
        background: transparent;
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-pill);
        color: var(--hos-text-dim);
        font-family: inherit;
        font-size: 13px;
        font-weight: 600;
        padding: 6px 14px 6px 6px;
        cursor: pointer;
        transition: border-color 0.15s ease, color 0.15s ease, background-color 0.15s ease; /* rev 5: the fill eases WITH its ink */
      }
      button:hover {
        border-color: var(--hos-stone-amber);
        color: var(--hos-text);
      }
      .num {
        display: inline-grid;
        place-items: center;
        width: 22px;
        height: 22px;
        border-radius: var(--hos-radius-pill);
        border: 1px solid var(--hos-line);
        font-family: var(--hos-font-mono);
 /* wave 2 · finding 19: the numbered chip is the 11px role, the same one the step card's
           own number chip wears one level down. It was 12 and the card's was 12, and the pair
           read as a size the counter's scale did not otherwise have a use for. */
        font-size: 11px;
        color: var(--hos-text-dim);
      }
      /* CURRENT IS A STATE, AND A STATE IS NOT THE COMMIT ACTION.
         This was the gold PILL: --hos-stone-amber as a background fill. Measured on a cold
         counter that put exactly three gold fills on one screen (the shell's brand mark, this chip
         and + New Sale), two of them belonging to POS, and one of those two was an indicator
         wearing the surface's action accent. The doctrine is "gold never decorates" and "exactly
         one gold primary action per panel", so the fill goes and the EDGE stays: the same
         stone-amber hairline the open accordion card wears one level down, which makes one accent
         family carry one meaning per weight: the edge says HERE, the fill says COMMIT. Nothing is
         lost for anyone who cannot see either: aria-current="step" and the .sr text carry it. */
      button.current {
        background: transparent;
        border-color: var(--hos-stone-amber);
        color: var(--hos-text);
      }
      button.current .num {
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
      /* Complete (and not current): the check in success ink; the label brightens to full text. */
      button.complete {
        color: var(--hos-text);
      }
      button.complete .num {
        border-color: var(--hos-success);
        color: var(--hos-success);
      }
      button.current.complete .num {
 /* rev 5: current AND complete: the "ready to ring" moment, and the tie the later
           .complete rule would otherwise win. Its original resolution was --hos-stone-amber-ink,
           because the chip was a gold FILL and D-078 says a gold fill carries only that ink; what
           it replaced was success-green on gold at a measured 1.14:1. Wave 2 took the fill off
           (see button.current above), which makes that ink exactly the wrong answer now: the
           near-black gold ink on the page ground fails in dark theme the way the green failed on
           gold. The TIE-BREAK is what the rule was for and it still stands, resolved to the amber
           TEXT role, which is the pair that is legible on both grounds. */
        border-color: var(--hos-stone-amber);
        color: var(--hos-stone-amber-text);
      }
 /* rev 5: a REAL visually-hidden class: the state text used the hidden attribute, which
         removes it from the accessibility tree entirely (announced to no one). */
      .sr {
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
    `,
  ];

  private goto(id: FlowStepId): void {
    this.dispatchEvent(new CustomEvent('pos-flow-goto', { detail: { step: id }, bubbles: true, composed: true }));
  }

  render() {
    return html`<nav aria-label=${this.navLabel}>
      ${this.steps.map(
        (s, i) => html`${i > 0 ? html`<span class="connector" aria-hidden="true"></span>` : ''}<button
            class="${s.current ? 'current' : ''} ${s.complete ? 'complete' : ''}"
            aria-current=${s.current ? 'step' : 'false'}
            title="Jump to the ${s.label} panel: every panel stays editable at all times."
            @click=${() => this.goto(s.id)}
          >
            ${/* A completed step shows the check INSTEAD of its number: current included (rev 5:
               the ready-to-ring moment must read finished); the .sr clip text keeps both facts
               in the accessibility tree. */ ''}
            <span class="num" aria-hidden="true">${s.complete ? drawIcon(iconForId('pencil:tick-m'), 12) : this.sequential ? s.n : ''}</span>
            <span>${s.label}</span>
            <span class="sr">${s.complete ? '(complete)' : '(open)'}</span>
          </button>`,
      )}
    </nav>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-flow-steps': HosPosFlowSteps;
  }
}
