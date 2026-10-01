// The Demo banner: HH-7's demo marker, carried inside this app per the plan
// (the plan carries the role cards and the banner inside the Counter Till
// rather than in a shared shell chrome). It marks the box as the demo, on the
// entry page and the till alike. It renders only once the flows route has
// answered with cards: a box that is not the demo does not get to look like
// one. The gold is a fill and carries the accent ink, per the sheet's gold
// rule.
import { css, html, type TemplateResult } from "lit";

export const demoBannerStyles = css`
  .demo-banner {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: var(--hh-s-12);
    background: var(--hh-accent);
    color: var(--hh-accent-ink);
    border-radius: var(--hh-radius-control);
    padding: var(--hh-s-8) var(--hh-s-12);
    margin: 0 0 var(--hh-s-12);
  }
  .demo-banner .word {
    font-weight: 700;
  }
  .demo-banner .copy {
    font-size: var(--hh-s-12);
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
