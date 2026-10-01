// The entry view: the tap-a-role chooser. One card per flow the demo routes
// list (the real table holds Sales and Sales manager), then the other eleven
// roles of the decided matrix as coming: shown, labelled Coming, and not
// tappable, because their screens do not exist in this release. When the
// flows route answers nothing (a default-build core or a non-demo box), there
// are no cards at all and the page says so plainly.
import { css, html, nothing, type TemplateResult } from "lit";
import type { DemoFlow } from "@hardscapehouse/api-client";

/**
 * The decided matrix's labels for the roles whose screens are not in this
 * release (ROLE-ACCESS r3, recorded in hh as D48; Outside Sales has no till,
 * so there is no third card either). Coming means coming: a disabled control
 * that drops its fill and goes grey, never a faded one.
 */
export const COMING_ROLES: readonly string[] = [
  "Outside Sales",
  "Yard Team",
  "Yard Manager",
  "Logistics Manager",
  "Driver",
  "Branch Manager",
  "Procurement Manager",
  "Payables and Receivables",
  "Financial Controller",
  "General Manager",
  "HR",
];

export type FlowsState =
  | { kind: "loading" }
  | { kind: "unavailable" }
  | { kind: "ready"; flows: DemoFlow[] };

export interface EntryView {
  flows: FlowsState;
  /** True while a mint is in flight: the cards go disabled and grey. */
  busy: boolean;
  /** A refusal in words, shown beside the cards. */
  message: string | null;
}

export const entryStyles = css`
  .intro {
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-16);
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: var(--hh-s-16);
    max-width: 560px;
    margin: 0 0 var(--hh-s-24);
  }
  .card {
    min-height: 96px;
    font-size: var(--hh-s-16);
    font-weight: 600;
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    text-align: start;
    padding: var(--hh-s-16) var(--hh-s-20);
  }
  .card:hover:not(:disabled) {
    border-color: var(--hh-accent);
  }
  .coming-heading {
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-8);
    color: var(--hh-text);
  }
  .coming {
    list-style: none;
    margin: 0;
    padding: 0;
    max-width: 560px;
  }
  .coming-role {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-12);
    border-bottom: 1px solid var(--hh-line);
    padding: var(--hh-s-8) 0;
  }
  .coming-role button {
    flex: 1;
    text-align: start;
    background: transparent;
    color: var(--hh-disabled-ink);
    border: none;
    padding: var(--hh-s-8) 0;
    min-height: 0;
    cursor: default;
  }
  .coming-tag {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-12);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-pill);
    padding: 2px var(--hh-s-8);
    white-space: nowrap;
  }
  .notice {
    color: var(--hh-text-dim);
  }
`;

export function renderEntry(view: EntryView, handlers: { choose(flow: string): void }): TemplateResult {
  if (view.flows.kind === "loading") {
    return html`<p class="notice" role="status">Loading the demo roles.</p>`;
  }
  if (view.flows.kind === "unavailable") {
    return html`<p class="notice" role="status">
      This deployment does not serve the demo sign-in, so there are no role cards to tap.
    </p>`;
  }
  return html`
    <div class="chooser">
      <p class="intro">Tap a role to open the till:</p>
      <div class="cards">
        ${view.flows.flows.map(
          (flow) => html`
            <button
              type="button"
              class="card"
              data-flow=${flow.flow ?? nothing}
              ?disabled=${view.busy}
              @click=${() => handlers.choose(flow.flow ?? "")}
            >
              ${flow.label ?? flow.flow}
            </button>
          `,
        )}
      </div>
      ${view.message === null ? nothing : html`<p class="notice" role="status">${view.message}</p>`}
      <h2 class="coming-heading">Coming</h2>
      <ul class="coming" aria-label="Coming roles">
        ${COMING_ROLES.map(
          (role) => html`
            <li class="coming-role">
              <button type="button" disabled>${role}</button>
              <span class="coming-tag">Coming</span>
            </li>
          `,
        )}
      </ul>
    </div>
  `;
}
