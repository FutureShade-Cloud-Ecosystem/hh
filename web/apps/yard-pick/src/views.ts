// Yard Pick's screens, as plain render functions of a model. They hold no
// state and own no events: every control is a plain element carrying a
// data-action name, and the root element's one click listener acts on it
// (the design system's position rules allow no @ or . binding here, and the
// shared header takes the same shape). Every number on a screen is a count,
// a line number or a quantity: there is no money field to bind.
//
// References: the FB Design boards "HH: field apps" (the queue in light and
// brand) and the original app's phone screens, drawn to HH's design system
// under row 563 (take the existing and improve it).
import { html, nothing, type TemplateResult } from "lit";
import type { DemoFlow } from "@hardscapehouse/api-client";

/** Messages the yard must see: a refusal in the server's words, or an outcome. */
export function renderMessages(error: string, notice: string): TemplateResult | typeof nothing {
  if (error === "" && notice === "") return nothing;
  return html`
    ${error !== "" ? html`<p class="alert alert-error" role="alert" data-error>${error}</p>` : nothing}
    ${notice !== "" ? html`<p class="alert alert-ok" role="status" data-notice>${notice}</p>` : nothing}
  `;
}

// ---- the pick queue, the landing ----

export interface QueueModel {
  readonly phase: "loading" | "ready" | "error";
  readonly branchName: string;
  readonly error: string;
  readonly notice: string;
}

/** The landing: the branch's board. The picks themselves land with the queue's own item. */
export function renderQueue(model: QueueModel): TemplateResult {
  let body: TemplateResult;
  if (model.phase === "loading") {
    body = html`<p class="dim" role="status">Checking the yard.</p>`;
  } else if (model.phase === "ready") {
    body = html`
      <div class="card empty" data-empty>
        <p class="empty-head">Nothing to pick at this branch.</p>
        <p class="dim">When the counter queues a pick it lands here, and the oldest one is the one somebody is standing around for.</p>
      </div>
    `;
  } else {
    body = html``;
  }
  return html`
    <div class="branch-row">
      <span class="pill" data-branch>${model.branchName !== "" ? model.branchName : "This yard"}</span>
    </div>
    <h1>Pick queue</h1>
    ${renderMessages(model.error, model.notice)}
    ${body}
  `;
}

// ---- the way in ----

export type EntryFlows =
  | { kind: "loading" }
  | { kind: "unavailable" }
  | { kind: "ready"; flows: readonly DemoFlow[] };

export interface EntryView {
  readonly flows: EntryFlows;
  /** True while a mint is in flight: the card goes grey. */
  readonly busy: boolean;
  /** A refusal in words, shown above the card. */
  readonly message: string | null;
}

/** The one flow this app signs in: the Yard Team's. */
export const YARD_FLOW = "yard-team";

/**
 * The demo way in. It is this app's own card, not the shared entry view: the
 * shared one draws every role's card and a list of the roles still to come,
 * which means nothing to a picker, and carries every other role's one liner
 * into the yard's bytes.
 */
export function renderEntry(view: EntryView): TemplateResult {
  let body: TemplateResult;
  if (view.flows.kind === "loading") {
    body = html`<p class="dim" role="status">Loading the demo.</p>`;
  } else if (view.flows.kind === "unavailable") {
    body = html`<p class="alert alert-error" role="alert">The demo way in is not available on this box.</p>`;
  } else {
    const flow = view.flows.flows.find((f) => f.flow === YARD_FLOW);
    body =
      flow === undefined
        ? html`<p class="alert alert-error" role="alert">The Yard Team card is not on this box yet.</p>`
        : html`<button type="button" class="btn btn-primary big" data-action="choose" data-flow=${flow.flow} ?disabled=${view.busy} data-tap>${flow.label}</button>`;
  }
  return html`
    <h1>Yard Pick</h1>
    <p class="dim">The pick queue, what was pulled, skids, short picks and staging. On your phone, in the yard. Demo data only.</p>
    ${view.message !== null ? html`<p class="alert alert-error" role="alert">${view.message}</p>` : nothing}
    ${body}
  `;
}
