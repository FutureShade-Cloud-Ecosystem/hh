// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-step-card.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { drawIcon, iconForId } from './_icon-consumer.js';
import { headerId, panelId } from './_pos-accordion.js';
import type { FlowStepId } from './_pos-flow.js';

/**
 * hos-pos-step-card: ONE step of the counter's single-column accordion.
 *
 * The client used the two-panel counter for a day and asked for one column with one step open at a
 * time: "selecting a step expands its card and collapses the others". This component is that card,
 * and it is deliberately the ONLY place the disclosure semantics live: four cards, one
 * implementation, so a step cannot quietly grow a different keyboard contract from its neighbours.
 *
 * WHAT IT OWNS: the header button and its aria-expanded/aria-controls pair, the collapsed one-line
 * summary, and the region the body sits in. WHAT IT DOES NOT OWN: which step is open (the terminal
 * holds that: one open step is a property of the SALE SESSION, not of any one card), and the body
 * itself, which arrives as light-DOM children.
 *
 * REAL DISCLOSURE, NOT display:none. When the card is closed it renders NO region and therefore no
 * <slot>: so the terminal's slotted body has no place in the flattened tree, is not painted, is
 * not in the accessibility tree, and cannot be reached by Tab. That last part is the whole point: a
 * CSS-hidden accordion leaves every control inside it tabbable while invisible, which strands a
 * keyboard user in a card they cannot see. The terminal ALSO declines to render the body's children
 * while closed, so a collapsed Items card is not holding a live product grid open either: the
 * disclosure is honest at both layers rather than merely at the one that shows.
 *
 * A CLOSED CARD STILL SPEAKS: AND AN OPEN ONE DOES NOT REPEAT ITSELF. Collapsing may hide chrome;
 * it may never hide facts. The `summary` property carries the one line a counter person would
 * otherwise have to open the card to see (who the customer is, how many items and what they come
 * to, the chosen tender): decided by summaryFor() in _pos-accordion.ts, rendered here, and
 * rendered ONLY while the card is closed. Open, the body is that fact's source and standing beside
 * it; a header that echoed it put the same string on screen twice and into this button's
 * accessible name twice.
 */
@customElement('hos-pos-step-card')
export class HosPosStepCard extends LitElement {
 /** Which step this card is: supplies the header/region ids the ARIA pair points at. */
  @property({ attribute: false }) step: FlowStepId = 'customer';
 /** The step's number in the flow (1-4). Presentational; the flow derives it. */
  @property({ type: Number }) n = 1;
 /** The step's name. 1-2 words, per the text budget. */
  @property({ type: String }) label = '';
 /** The sale data satisfies this step: a CHECK MARK, never a lock (deriveFlow decides). */
  @property({ type: Boolean }) complete = false;
 /** This card is the expanded one. */
  @property({ type: Boolean, reflect: true }) open = false;
 /** The one fact a CLOSED card still shows. Empty renders nothing at all: never a placeholder. */
  @property({ type: String }) summary = '';

  static styles = [
    focusRing,
    css`
      :host {
        display: block;
        font-family: var(--hos-font-ui);
        color: var(--hos-text);
      }
      .card {
        border: 1px solid var(--hos-line);
        border-radius: var(--hos-radius-card);
        background: var(--hos-slate-warm);
        box-shadow: var(--hos-elev-1);
        overflow: hidden;
      }
 /* The OPEN card is the work; the closed ones are a list. One hairline of extra emphasis
         (the stone-amber edge) says which is which without a second accent: gold stays with the
         one commit action on the screen. */
      :host([open]) .card {
        border-color: var(--hos-stone-amber);
      }
      h3 {
        margin: 0;
        font: inherit;
      }
      .head {
        display: flex;
        align-items: center;
        gap: var(--hos-space-3);
        width: 100%;
        padding: var(--hos-space-3) var(--hos-space-4);
        background: transparent;
        border: 0;
        color: var(--hos-text);
        font-family: inherit;
        font-size: 15px;
        font-weight: 600;
        text-align: left;
        cursor: pointer;
      }
 /* The hover is the page ground showing through the card, not a new colour: it reads as the
         header lifting off the card rather than as a second surface entering the palette. */
      .head:hover {
        background: var(--hos-deep-earth);
      }
 /* The step number, or a check once the sale data satisfies the step. The check is the
         success ink and is paired with the step's own label: colour never carries it alone. */
      .no {
        flex: none;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 24px;
        height: 24px;
        border-radius: var(--hos-radius-pill);
        border: 1px solid var(--hos-line);
        font-family: var(--hos-font-mono);
 /* wave 2 · finding 19: the chip role is 11px across the counter (the header stepper's
           number chip matches this one exactly, which is the point of them being the same size). */
        font-size: 11px;
        color: var(--hos-text-dim);
      }
      .no.done {
        border-color: var(--hos-success);
        color: var(--hos-success);
      }
      :host([open]) .no {
        border-color: var(--hos-stone-amber);
        color: var(--hos-text);
      }
      .label {
        flex: none;
      }
 /* The collapsed fact. Dim and right-aligned so the open card's content still reads first,
         but never truncated to nothing: a summary that says half a customer name is worse than
         none. Money and counts ride the mono face like every other figure in the app. */
      .sum {
        flex: 1 1 auto;
        min-width: 0;
        text-align: right;
        font-size: 13px;
        font-weight: 500;
        color: var(--hos-text-dim);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
      }
      .chev {
        flex: none;
        color: var(--hos-text-dim);
        transition: transform 0.15s ease;
      }
      :host([open]) .chev {
        transform: rotate(180deg);
      }
      @media (prefers-reduced-motion: reduce) {
        .chev {
          transition: none;
        }
      }
 /* The step's state in words, for a reader who cannot see the check mark or the open edge.
         Colour and shape never carry meaning alone: the doctrine's a11y non-negotiable. */
      .visually-hidden {
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
      .body {
        padding: 0 var(--hos-space-4) var(--hos-space-4);
        border-top: 1px solid var(--hos-line);
 /* The region takes programmatic focus (the honest fallback when a step's first meaningful
           control has not mounted) and must not paint a ring for a mouse click that landed in it. */
        outline: none;
      }
    `,
  ];

  render() {
    const hid = headerId(this.step);
    const pid = panelId(this.step);
    return html`<section class="card">
      <h3>
        <button
          class="head"
          id=${hid}
          data-testid="step-head-${this.step}"
          aria-expanded=${this.open ? 'true' : 'false'}
          aria-controls=${pid}
          @click=${() => this.dispatchEvent(new CustomEvent('pos-step-toggle', { detail: { step: this.step }, bubbles: true, composed: true }))}
        >
          <span class="no ${this.complete ? 'done' : ''}" aria-hidden="true">${this.complete ? drawIcon(iconForId('pencil:tick-m'), 12) : this.n}</span>
          <span class="label">${this.label}</span>
          ${this.complete ? html`<span class="visually-hidden">: done</span>` : nothing}
          ${/* A CLOSED card still speaks; an OPEN one has no need to. The summary rendered
             unconditionally, so an open card said its one fact twice: header "✓ Payment: done
             Cash" over the pressed Cash tile, "1 Customer Walk-in" over the Walk-in tile, and
             "Split tender" over the split panel's own <h3>. It sits inside this <button>, so the
             duplication was in the button's accessible name as well as on screen. The empty span
             stays either way: it is the flex spacer that keeps the chevron in its right-hand
             seat, and losing it would move the chevron every time a card opened. */ ''}
          ${!this.open && this.summary
            ? html`<span class="sum" data-testid="step-summary-${this.step}">${this.summary}</span>`
            : html`<span class="sum"></span>`}
          <svg class="chev" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M6 9l6 6 6-6"></path>
          </svg>
        </button>
      </h3>
      ${this.open
        ? html`<div class="body" id=${pid} role="region" aria-labelledby=${hid} tabindex="-1" data-testid="step-panel-${this.step}">
            <slot></slot>
          </div>`
        : nothing}
    </section>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-step-card': HosPosStepCard;
  }
}
