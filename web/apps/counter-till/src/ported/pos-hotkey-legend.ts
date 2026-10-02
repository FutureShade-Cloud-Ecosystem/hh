// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-hotkey-legend.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property } from 'lit/decorators.js';
import { focusRing } from './styles/focus-ring.js';
import { legendKeys, type PosHotkey, type HotkeyState } from './_pos-hotkeys.js';

/**
 * hos-pos-hotkey-legend: the counter's key legend, pinned across the foot of the screen.
 *
 * THE ASK, VERBATIM: a persistent legend "so nobody memorises". So it is always there, it is chrome
 *: keycaps and one-word labels, no sentences: and it shows the CURRENT step's availability with
 * the unavailable keys dimmed.
 *
 * WHY THE KEYS DO NOT MOVE. Every bound action appears at every step. Only the DIMMING changes. A
 * legend whose contents rearrange as the sale progresses is a legend nobody can build muscle memory
 * against, which would defeat the one thing it was asked for. Dimmed means exactly what a disabled
 * control means: pressing it does nothing, on purpose.
 *
 * IT IS NOT A TOOLBAR. The keycaps are not clickable and not focusable: every action on it has a
 * real control on the screen, and duplicating those controls as a second row of buttons would put
 * five more tab stops between the scan field and the ticket. The caps are `aria-hidden` for the
 * same reason: a screen-reader user reaches these actions through the controls themselves, and
 * hearing the whole key map announced between every step would be noise, not help.
 *
 * THE FALLBACK IS THE `?` OVERLAY, AND AS OF ITEM 4d IT IS ACTUALLY ONE. This paragraph used to end
 * "(the dealer key map also remains readable in the shell's `?` overlay, which is a real, focusable
 * list)" and that was not true: the overlay listed the app-wide NAVIGATION map: a different table,
 * a different action namespace, a different label set: and never carried these five keys at all.
 * So the only route to the counter's key map was a strip deliberately hidden from assistive tech,
 * and the one control on it turns that strip off. The overlay now draws this roster through
 * counterHelpRows(), which runs THIS component's own legendKeys() over THIS component's own roster,
 * so the two cannot hold two opinions. One honest caveat remains and is not ours to remove: `?`
 * types, so the shell's typing guard still swallows it while the caret is in the scan box: the
 * overlay is reached from anywhere that is not a text field, including the legend's own dismiss
 * button and the counter-view control it hands focus to.
 *
 * ONE THING ON IT IS A CONTROL: DISMISS. The back-office lead, asked whether the yard is
 * hotkey-heavy: "No, I don't even know what that is." The same side asked for screen real estate
 * back, so a strip of keycaps it will never press is rent paid for nothing. The × is a real focusable
 * button with a real name, it is the ONLY tab stop this bar adds, and it turns off THE STRIP and
 * not the keys: the roster is untouched, F4 still advances and F12 still pays. The way back is the
 * counter-view control in the header, which is where the × puts focus.
 */
@customElement('hos-pos-hotkey-legend')
export class HosPosHotkeyLegend extends LitElement {
  /** The dealer's roster, resolved server-side. */
  @property({ attribute: false }) roster: PosHotkey[] = [];
 /** The sale facts availability derives from: the same ones the controls disable on. */
  @property({ attribute: false }) state: HotkeyState = {
    step: '',
    lineCount: 0,
    rungDone: false,
    ringBusy: false,
    tenderable: false,
  };

  static styles = [
    focusRing,
    css`
    :host {
      position: fixed;
      left: 0;
      right: 0;
      bottom: 0;
      z-index: var(--hos-z-nav);
      display: block;
      font-family: var(--hos-font-ui);
      pointer-events: none;
    }
    .bar {
      display: flex;
      align-items: center;
      flex-wrap: wrap;
      gap: 4px 14px;
      padding: 6px calc(var(--hos-space-5) + var(--hos-safe-right)) calc(6px + var(--hos-safe-bottom))
        calc(var(--hos-space-5) + var(--hos-safe-left));
      background: var(--hos-slate-warm);
      border-top: 1px solid var(--hos-line);
      font-size: 11px;
      color: var(--hos-text-dim);
    }
    .k {
      display: inline-flex;
      align-items: center;
      gap: 5px;
      white-space: nowrap;
    }
 /* An unavailable key is dimmed, never removed: the legend's contents must not move. */
    .k.off {
      opacity: 0.42;
    }
    .cap {
      display: inline-grid;
      place-items: center;
      min-width: 26px;
      padding: 1px 5px;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      font-family: var(--hos-font-mono);
 /* wave 2 · finding 19: the keycap was the counter's SIXTH type size and the only thing on a
         cold screen wearing it, one pixel under the legend word it sits beside. A key is the thing
         a person is meant to read off this bar, so it joins the 11px chip/caption role rather than
         whispering beneath its own label (the counter's four sizes are noted in pos-terminal.ts). */
      font-size: 11px;
      color: var(--hos-text);
    }
    .k.off .cap {
      color: var(--hos-text-dim);
    }
    /* A key the browser also owns wears a dotted cap: the dealer chose it, the escape rule (any
       extra modifier falls through) still applies, and the mark is how a counter person knows. */
    .k.host .cap {
      border-style: dashed;
    }
 /* The one control on the bar. Quiet: it is an exit, not an action: and it is the only thing
       here that takes pointer events back from the pass-through host. */
    /* It sits immediately after the last keycap, NOT flushed right. The shell's Feedback pill lives
       in the bottom-right corner and would sit on top of a right-flushed control at a touch width:
       an exit you cannot reach is worse than no exit. Beside the keys it is also beside the thing it
       dismisses, which is where a person looks for it. */
    .x {
      margin-left: 6px;
      pointer-events: auto;
      display: inline-grid;
      place-items: center;
      width: 22px;
      height: 22px;
      padding: 0;
      background: transparent;
      border: 1px solid transparent;
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      font: inherit;
      cursor: pointer;
    }
    .x:hover {
      border-color: var(--hos-line);
      color: var(--hos-text);
    }
    @media (pointer: coarse) {
      .bar {
        font-size: 12px;
        padding-top: 8px;
      }
      .cap {
        min-width: 32px;
        font-size: 11px;
      }
      .x {
        width: 44px;
        height: 44px;
      }
    }
  `,
  ];

  render() {
    const keys = legendKeys(this.roster, this.state);
    if (keys.length === 0) return nothing;
    return html`<div class="bar" data-testid="pos-hotkey-legend">
      ${keys.map(
        (k) => html`<span
          class="k ${k.live ? '' : 'off'} ${k.browserOwned ? 'host' : ''}"
          data-action=${k.action}
          aria-hidden="true"
          >
          <span class="cap">${k.keycap}</span>${k.label}
        </span>`,
      )}
      <button
        class="x"
        type="button"
        data-testid="pos-legend-dismiss"
        aria-label="Hide the key legend"
        title="Hides this bar. The keys keep working: bring it back under Counter view."
        @click=${() => this.dispatchEvent(new CustomEvent('pos-legend-dismiss', { bubbles: true, composed: true }))}
      >
        <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
          <path d="M6 6l12 12M18 6L6 18"></path>
        </svg>
      </button>
    </div>`;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-hotkey-legend': HosPosHotkeyLegend;
  }
}
