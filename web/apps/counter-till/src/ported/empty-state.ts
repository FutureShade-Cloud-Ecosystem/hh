// Ported from hardscapeos_dibbits master 87d7941b app/src/empty-state.ts; adapted for HH.

// empty-state.ts: the house style for "there is nothing here" and "this did not load", once.
//
// WHY THIS EXISTS (DIB-425). The W2-W5 surfaces follow a style the older ones do not, and the W6
// state sweep put a number on it: 11 of 22 surfaces render NO empty-state copy at all: a table
// header over blank space: and 4 paint NOTHING on a total API failure, so an operator cannot tell
// "no records" from "the server is unreachable". Those are the same defect twice: a screen that
// declines to say which of the two true things is true.
//
// The rule the app already ships (DIB-443, inventory-on-hand.ts) is a rule about the DIFFERENCE:
//
// EMPTY is not a fault. Quiet prose in the yard-console voice: name what is missing in domain
// terms, say what it means, say what would put something there. Never red. Never role="alert",
// because nothing has gone wrong and an assertive live region interrupting a screen-reader user to
// announce that a filter matched nothing is worse than saying nothing.
//
// A FAULT is a fault. A bordered box with a heading, role="alert", wording that is explicit that
// NOTHING WAS READ (so it can never be misread as a statement about the data), and a retry.
//
// The two must be distinguishable WITHOUT COLOUR: one is a bordered panel with a heading, the other
// is prose: because colour alone carries nothing for a red-green colour-blind reader and carries
// nothing at all for a screen reader.
//
// Codifying it here rather than hand-rolling it per surface is the point: 13 copies of a house style
// are 13 chances to drift, and the drift is invisible until someone sweeps all 22 screens again.
//
// ── THERE IS A THIRD STATE, AND IT IS NOT IN THIS FILE (wave 8) ─────────────────────────────────
//
// EMPTY and FAULT are the two RESTING states: what a screen says about itself when nobody has just
// done anything. The third is what a screen says about a thing that HAS just happened: a save
// landed, a widget was removed, a run was placed. That is the NOTICE, and the house answer is a
// PERSISTENT POLITE LIVE REGION: a `<p role="status" aria-live="polite" aria-atomic="true">`
// rendered UNCONDITIONALLY and empty when there is nothing to say, into which the surface writes a
// sentence. Wave 7 extended it into the ACTION NOTICE: the same region may grow AT MOST ONE real
// `<button>`, inside itself, sharing the sentence's lifetime (home-bento.ts:1365 and its two gates
// in home-bento-wiring.test.mjs). A polite region announces and cannot be operated, so an Undo that
// lived only in the announcement would be an action a keyboard user was told about and could not
// take.
//
// It is deliberately NOT a helper here. A notice belongs to a surface's own render tree: it sits
// where the outcome happened, keeps the surface's own focus order, and dies on the surface's own
// clock. A shared `notice()` would have to be handed a timer and a focus policy by every caller,
// which is five arguments to save one element. What IS shared is the shape, and the shape is
// written down once in.claude/skills/hardscape-ui-design/SKILL.md § "Two notice mechanisms".
//
// THE CUSTOMER PORTAL'S STACKING OVERLAY IS THE ONE EXCEPTION, and it is scoped to the portal:
// see portal/portal-toast.ts, which now carries the ruling in its own header. It is not imported
// into the staff app and may not be: home-bento-wiring.test.mjs pins that too.

import { css, html, nothing } from 'lit';
import { focusRing } from '@hardscapehouse/pos-kit/src/ported/styles/focus-ring.js';
import type { TemplateResult } from 'lit';

/**
 * Adopted by every consuming component. Only existing --hos-* tokens; no literal colours.
 *
 * The class names (`hos-blank`, `hos-fault`) are prefixed because these rules land inside ~13
 * different shadow roots that each already own `.empty`, `.err`, `.muted` and friends, and a bare
 * `.blank` would be a coin toss between this sheet and the component's own.
 *
 * It carries the shared `focusRing` (DIB-414) rather than trusting each consumer to remember: the
 * retry button below is a real focusable control that this module puts inside somebody else's
 * shadow root, so this module is what has to guarantee it is not left on the 1px UA outline.
 */
export const emptyStateStyles = [
  focusRing,
  css`
  .hos-blank {
    padding: var(--hos-space-4);
    color: var(--hos-text-dim);
    font-size: 13px;
    line-height: 1.55;
    max-width: 62ch;
  }
  .hos-blank h4 {
    margin: 0 0 6px;
    font-size: 14px;
    font-weight: 600;
    color: var(--hos-text);
  }
  .hos-blank p {
    margin: 0 0 6px;
  }
  .hos-blank p:last-child {
    margin-bottom: 0;
  }

 /* A BOX, not a bare red line: see the file header.
   *
 * WAVE 8: THE TWIN TAKES THE TOKEN AND THE ANCHOR KEEPS THE LITERAL, deliberately.
 * --hos-measure was EXTRACTED from the.hos-blank rule above, and _layout-contract.test.mjs pins
   * the pair so that re-tuning either half fails loudly with a sentence about the layout contract.
 * That makes.hos-blank the ANCHOR, and an anchor that reads its value back out of the thing it
   * anchors proves nothing. This rule has no such job: it was simply a second copy of the same
 * number: the "the twin had drifted" failure inventory-on-hand.ts:144 records about its own
 * pair: and a second copy is exactly what a token is for. Zero pixels move: both resolve 62ch
   * against a 13px font, measured at 528.7px on the live stack at 1920. */
  .hos-fault {
    border: 1px solid var(--hos-safety-red);
    background: color-mix(in srgb, var(--hos-safety-red) 12%, transparent);
    border-radius: var(--hos-radius-card);
    padding: var(--hos-space-3) var(--hos-space-4);
    margin-bottom: var(--hos-space-4);
    font-size: 13px;
    line-height: 1.55;
    max-width: var(--hos-measure);
  }
  .hos-fault h4 {
    margin: 0 0 6px;
    font-size: 14px;
    font-weight: 600;
    color: var(--hos-safety-red);
  }
  .hos-fault p {
    margin: 0 0 8px;
    color: var(--hos-text);
  }
  .hos-fault .detail {
    font-family: var(--hos-font-mono);
    font-size: 12px;
    color: var(--hos-text-dim);
    overflow-wrap: anywhere;
  }
  .hos-fault button {
    font-family: inherit;
    font-size: 12.5px;
    padding: 6px 12px;
    border-radius: var(--hos-radius-control);
    border: 1px solid var(--hos-line);
    background: var(--hos-slate-warm);
    color: var(--hos-text);
    cursor: pointer;
  }
  .hos-fault button:hover:not(:disabled) {
    border-color: var(--hos-safety-red);
  }
  .hos-fault button:disabled {
    background: transparent;
    border-color: var(--hos-line);
    color: var(--hos-text-dim);
    opacity: 1;
    cursor: not-allowed;
  }
`,
];

/**
 * "There is nothing here, and that is fine."
 *
 * @param title what is missing, in domain terms: "Nothing queued for this branch.", not "No data".
 * @param body what that means. This is the sentence that stops a reader wondering whether the
 * screen is broken.
 * @param hint what would put something here. Optional, and omit it honestly rather than inventing
 * an action: a filter that matched nothing needs "widen the filter", but a genuinely
 * new install needs "receive a PO", and guessing wrong is worse than staying quiet.
 *
 * NO role, NO aria-live, and that is deliberate. This is the resting state of a screen, not an
 * event. See DIB-425 / the gate in _empty-states.test.mjs.
 *
 * ── THE TIGHT FORM, AND WHY IT LIVES HERE ───────────────────────────────────────────────────────
 *
 * `tight` draws the TITLE ONLY, keeping the body reachable as the block's title attribute rather
 * than deleting it. It exists for a surface that measured its own smallest card and found the two
 * sentences did not fit: a one-grid-row widget has a sixty-pixel body and this renders a hundred, so
 * deliveries.board was cutting forty pixels off its own explanation with `overflow: hidden` making
 * that silent.
 *
 * IT IS A PARAMETER HERE RATHER THAN A DIV THERE because the first attempt was the div: widget-body
 * hand-rolled `<div class="hos-blank"><h4>…</h4></div>` and a gate caught it in one run:
 * "widget-body.ts hand-rolls a state the house helpers own". That gate is right, and the reason is
 * this comment: the no-role, no-aria-live ruling and the.hos-blank contract are decisions this file
 * owns, and a caller reproducing the markup is a caller who will not hear about the next one.
 */
export function emptyState(title: string, body: string, hint?: string, tight = false): TemplateResult {
  if (tight) {
    return html`
      <div class="hos-blank" title=${body}>
        <h4>${title}</h4>
      </div>
    `;
  }
  return html`
    <div class="hos-blank">
      <h4>${title}</h4>
      <p>${body}</p>
      ${hint ? html`<p>${hint}</p>` : nothing}
    </div>
  `;
}

/**
 * "This did not load." A real fault, announced.
 *
 * @param title what failed, as a whole sentence: "The ledger did not load".
 * @param detail the message from the API. Rendered in mono, small, secondary: useful to whoever is
 * asked to look at it, never the main sentence.
 * @param onRetry re-runs the read. Omit only where there is genuinely nothing to retry.
 *
 * The body sentence is FIXED, and it is the load-bearing part of this whole ticket: an operator
 * looking at a blank table cannot tell "no records" from "the server is unreachable", and the only
 * cure is a screen that says which one it is. "Nothing was read" is that sentence.
 */
export function faultBox(title: string, detail: string | null, onRetry?: () => void, busy = false): TemplateResult {
  return html`
    <div class="hos-fault" role="alert">
      <h4>${title}</h4>
      <p>Nothing was read, so this is not a statement about your data: it is a statement about the request.</p>
      ${detail ? html`<p class="detail">${detail}</p>` : nothing}
      ${onRetry ? html`<button type="button" ?disabled=${busy} @click=${onRetry}>Try again</button>` : nothing}
    </div>
  `;
}
