// The placeholder till: what a tapped card lands on in this slice. The sale at
// the counter builds the real till later in the cycle; until then the view
// holds nothing that looks like a figure, because a confident wrong number is
// the one thing a till must never show.
import { css, html, type TemplateResult } from "lit";

export const tillStyles = css`
  .till h2 {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
  }
  .till .placeholder {
    color: var(--hh-text-dim);
    max-width: 560px;
  }
`;

export function renderTill(): TemplateResult {
  return html`
    <section class="till">
      <h2>Till</h2>
      <p class="placeholder">
        The counter sale is not built in this slice yet: this build stops at the
        way in, so nothing is rung, tendered or counted here, and no figure is
        shown that the till cannot prove.
      </p>
    </section>
  `;
}
