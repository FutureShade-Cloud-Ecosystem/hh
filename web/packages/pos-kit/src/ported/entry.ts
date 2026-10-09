// The entry view: the tap-a-role chooser, staged to the till's own polish
// (the train review's P3-5): a centered column with a headline and a one-line
// intro, one card per flow the demo routes list (the real table holds Sales
// and Sales manager) wearing the ported token sheet's surfaces and borders,
// an icon from the ported icon registry and one line of what the role opens,
// then the other eleven roles of the decided matrix as coming: shown in a
// ruled card, labelled Coming, and not tappable, because their screens do not
// exist in this release. When the flows route answers nothing (a default-build
// core or a non-demo box), there are no cards at all and the page says so
// plainly, on the same stage.
//
// The cards' accessible names stay the role labels alone: the mark is
// aria-hidden (a drawing never names a control) and the one-liner sits inside
// the card without joining the name, which rides aria-label (the gate's own
// rule admits no bound id here), so the demo adapter's contract with
// app-root and every name-based selector keep reading exactly "Sales" and
// "Sales manager".
import { css, html, nothing, type TemplateResult } from "lit";
import type { DemoFlow } from "@hardscapehouse/api-client";
import { drawIcon, iconForId } from "./_icon-consumer.js";

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
  | { kind: "unavailable"; status: number | null }
  | { kind: "ready"; flows: DemoFlow[] };

export interface EntryView {
  flows: FlowsState;
  /** True while a mint is in flight: the cards go disabled and grey. */
  busy: boolean;
  /** A refusal in words, shown beside the cards. */
  message: string | null;
}

/**
 * The role cards' marks and one-liners, keyed by the demo flow id. The marks
 * are the ported registry's own ids: the pos module's default mark for Sales,
 * the approvals mark for the manager. They resolve through iconForId, so a
 * flow the map does not know still draws the neutral square, never an empty
 * box, and carries no one-liner.
 */
const FLOW_ICONS: Record<string, string> = {
  sales: "lucide:receipt",
  "sales-manager": "lucide:badge-check",
  "procurement-manager": "lucide:package",
};
const FLOW_BLURBS: Record<string, string> = {
  sales: "The counter sale flow.",
  "sales-manager": "The counter plus approvals and margins.",
  "procurement-manager": "Buy the branch's stock.",
};

export const entryStyles = css`
  .stage {
    max-width: 680px;
    margin: 0 auto;
    padding: var(--hh-s-40) var(--hh-s-8) var(--hh-s-64);
  }
  .headline {
    font-size: var(--hos-stat-size);
    line-height: 1.15;
    font-weight: 600;
    margin: 0 0 var(--hh-s-8);
  }
  .intro {
    color: var(--hh-text-dim);
    font-size: var(--hh-s-16);
    margin: 0 0 var(--hh-s-32);
  }
  .cards {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
    gap: var(--hh-s-16);
    margin: 0 0 var(--hh-s-24);
  }
  /* The role card is the till's card: the ported sheet's surface and control
     boundary, the card radius, the first elevation at rest and the second on
     hover. Hover and focus speak in the accent: the boundary takes the gold
     fill and the mark flips to the gold-as-ink role, which is the one accent
     role that holds in both themes. The busy state needs no rule here: the
     shared disabled rule drops the fill and goes grey, a token swap, never a
     fade. */
  .card {
    display: flex;
    align-items: flex-start;
    gap: var(--hh-s-12);
    min-height: 96px;
    text-align: start;
    padding: var(--hh-s-20);
    background: var(--hh-card);
    color: var(--hh-text);
    border: 1px solid var(--hh-control-line);
    border-radius: var(--hh-radius-card);
    box-shadow: var(--hh-widget-shadow);
    transition: border-color 0.15s ease, box-shadow 0.15s ease;
  }
  .card:hover:not(:disabled) {
    border-color: var(--hh-accent);
    box-shadow: var(--hos-widget-shadow-raised);
  }
  .card .mark {
    color: var(--hh-text-dim);
    flex: none;
  }
  .card:hover:not(:disabled) .mark,
  .card:focus-visible .mark {
    color: var(--hh-accent-text);
  }
  .card .words {
    display: grid;
    gap: var(--hh-s-4);
  }
  .card .role {
    font-size: var(--hh-s-16);
    font-weight: 600;
  }
  .card .what {
    font-size: 13px;
    line-height: 1.4;
    color: var(--hh-text-dim);
    font-weight: 400;
  }
  /* The Coming list wears the same card, ruled inside with hairlines: one
     surface, its rows divided, its tag the surface's own chip. */
  .coming-card {
    background: var(--hh-card);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-card);
    padding: var(--hh-s-16) var(--hh-s-20) var(--hh-s-4);
    margin: 0;
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
  }
  .coming-role {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--hh-s-12);
    border-bottom: 1px solid var(--hh-line);
    padding: var(--hh-s-8) 0;
  }
  .coming li:last-child .coming-role {
    border-bottom: none;
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
    background: var(--hh-row);
    border: 1px solid var(--hh-line);
    border-radius: var(--hh-radius-pill);
    padding: 2px var(--hh-s-8);
    white-space: nowrap;
  }
  .notice {
    color: var(--hh-text-dim);
    margin: 0 0 var(--hh-s-24);
  }
`;

/** The stage column: every state the entry shows lives on it, centred. */
const stage = (content: TemplateResult): TemplateResult => html`<div class="stage">${content}</div>`;

export function renderEntry(view: EntryView): TemplateResult {
  if (view.flows.kind === "loading") {
    return stage(html`<p class="notice" role="status">Loading the demo roles.</p>`);
  }
  if (view.flows.kind === "unavailable") {
    // The failures say different things, so the page does not claim more than
    // it knows: a 404 is the shape of a box without the demo sign-in (a
    // default-build core answers exactly that); anything else is an error
    // worth its status, because the demo may be one reload away.
    if (view.flows.status === 404) {
      return stage(html`<p class="notice" role="status">
        This deployment does not serve the demo sign-in, so there are no role cards to tap.
      </p>`);
    }
    if (view.flows.status === null) {
      return stage(html`<p class="notice" role="status">
        The demo roles could not be loaded: the call did not go through. Reload to try again.
      </p>`);
    }
    return stage(html`<p class="notice" role="status">
      The demo roles could not be loaded (HTTP ${view.flows.status}). Reload to try again.
    </p>`);
  }
  if (view.flows.flows.length === 0) {
    return stage(html`<p class="notice" role="status">
      The demo sign-in answered with no roles: there is nothing to tap.
    </p>`);
  }
  return stage(html`
    <h2 class="headline">Who is at the counter?</h2>
    <p class="intro">Tap a role to open the till:</p>
    <div class="cards">
      ${view.flows.flows.map(
        (flow) => html`
          <button
            type="button"
            class="card"
            data-action="choose"
            data-flow=${flow.flow ?? nothing}
            aria-label=${flow.label ?? flow.flow ?? nothing}
            ?disabled=${view.busy}
          >
            <span class="mark">${drawIcon(iconForId(FLOW_ICONS[flow.flow ?? ""] ?? "lucide:circle-user"), 29)}</span>
            <span class="words">
              <span class="role">
                ${flow.label ?? flow.flow}
              </span>
              ${FLOW_BLURBS[flow.flow ?? ""] === undefined
                ? nothing
                : html`<span class="what">${FLOW_BLURBS[flow.flow ?? ""]}</span>`}
            </span>
          </button>
        `,
      )}
    </div>
    ${view.message === null ? nothing : html`<p class="notice" role="status">${view.message}</p>`}
    <section class="coming-card">
      <h2 class="coming-heading">Coming</h2>
      <ul class="coming" aria-label="Coming roles">
        ${COMING_ROLES.map(
          (role) => html`
            <li>
              <div class="coming-role">
                <button type="button" disabled>${role}</button>
                <span class="coming-tag">Coming</span>
              </div>
            </li>
          `,
        )}
      </ul>
    </section>
  `);
}
