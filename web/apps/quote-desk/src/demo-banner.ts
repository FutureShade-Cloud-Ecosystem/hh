// Ported from the Counter Till's demo banner (web/apps/counter-till/src/
// demo-banner.ts, itself HH-7's demo marker), carried inside this app the
// same way: the plan carries the role cards and the banner inside each app
// rather than in a shared shell chrome. It marks the box as the demo, on the
// entry page and the desk alike. It renders only once the flows route has
// answered with cards: a box that is not the demo does not get to look like
// one. The gold is an outline and text on the page (the sheet's accent-text), a
// slim strip, not a fill.
import { css, html, type TemplateResult } from "lit";

export const demoBannerStyles = css`
  /* A slim strip in the signed-in bar, the gold as an outline and ink rather
     than a full-bleed fill (visual review round 1, P2-1). */
  .demo-banner {
    display: inline-flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--hh-s-8);
    border: 1px solid var(--hh-accent);
    color: var(--hh-accent-text);
    border-radius: var(--hh-radius-pill);
    padding: var(--hh-s-2) var(--hh-s-12);
    margin: 0;
    font-size: 12px;
    line-height: 1.3;
  }
  .demo-banner .word {
    font-weight: 700;
  }
  .demo-banner .copy {
    font-size: 12px;
    color: var(--hh-text-dim);
  }
`;

export function demoBanner(): TemplateResult {
  return html`
    <div class="demo-banner" role="note">
      <span class="word">Demo</span>
      <span class="copy">Everything on this box is invented demo data: nothing here is real.</span>
    </div>
  `;
}
