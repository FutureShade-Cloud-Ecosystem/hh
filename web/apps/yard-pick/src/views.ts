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
import type { YardPick } from "./yard-api.js";
import {
  STATUS_LABEL,
  ageLabel,
  isUnclaimed,
  mtoLabel,
  nextAction,
  orderHandle,
  priorityWord,
  shortageSummary,
  statusTone,
  ticketIsFixture,
} from "./yard-logic.js";

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
  readonly picks: readonly YardPick[];
  readonly me: string;
  readonly error: string;
  readonly notice: string;
  readonly moreBehind: boolean;
}

/** The status pill: colour never carries the meaning alone, the word is always there. */
function statusPill(status: string): TemplateResult {
  return html`<span class="status status-${statusTone(status)}" data-task-status>${STATUS_LABEL[status] ?? status}</span>`;
}

function pickCard(entry: YardPick, me: string, busy: boolean): TemplateResult {
  const t = entry.task;
  const mine = t.claimed_by !== "" && t.claimed_by === me;
  const unclaimed = isUnclaimed(t.status);
  const ticket = entry.ticket;
  const lineCount = ticket?.lines.length ?? 0;
  const summary = shortageSummary(entry.shortages);
  const mto = ticket !== null ? mtoLabel(ticket.mto_class, ticket.fulfillment) : "";
  const priority = priorityWord(ticket?.priority || t.priority);
  const action = nextAction(t.status);
  return html`
    <section class="card pick-card ${mine ? "mine" : ""}" data-pick-card data-task-id=${t.id}>
      <div class="pick-head">
        <span class="pick-no" data-order-no>${orderHandle(ticket)}</span>
        ${statusPill(t.status)}
      </div>
      ${ticket?.customer_name ? html`<p class="pick-who">${ticket.customer_name}</p>` : nothing}
      <div class="pick-facts">
        <span>${lineCount} ${lineCount === 1 ? "line" : "lines"}</span>
        ${priority !== "" ? html`<span class="pill pill-watch">${priority}</span>` : nothing}
        <span>waiting ${ageLabel(t.created_at, new Date())}</span>
        ${ticket !== null && ticketIsFixture(ticket) ? html`<span class="pill">fixture ticket</span>` : nothing}
      </div>
      ${mto !== "" ? html`<p class="pick-facts">${mto}</p>` : nothing}
      ${ticket?.held_from_picking === true
        ? html`<p class="held" data-held>${ticket.hold_words || "This order waits: it cannot be picked yet."}</p>`
        : nothing}
      ${summary !== "" ? html`<p class="shortage-open" data-queue-shortage>${summary}</p>` : nothing}
      ${mine ? html`<p class="mine-tag">Claimed by you</p>` : nothing}
      ${unclaimed
        ? html`<button type="button" class="btn btn-primary big" data-action="claim" data-task-id=${t.id} ?disabled=${busy} data-tap>Claim</button>`
        : action !== null
          ? html`<button type="button" class="btn btn-primary big" data-action="open-pick" data-task-id=${t.id} data-tap>Open</button>`
          : html`<button type="button" class="btn btn-secondary big" data-action="open-pick" data-task-id=${t.id} data-tap>Open</button>`}
    </section>
  `;
}

/** The landing: the branch's open picks, the unclaimed half first, claimed and yours marked. */
export function renderQueue(model: QueueModel): TemplateResult {
  let body: TemplateResult;
  if (model.phase === "loading" && model.picks.length === 0) {
    body = html`<p class="dim" role="status">Checking the yard.</p>`;
  } else if (model.phase === "ready" && model.picks.length === 0) {
    body = html`
      <div class="card empty" data-empty>
        <p class="empty-head">Nothing to pick at this branch.</p>
        <p class="dim">When the counter queues a pick it lands here, and the oldest one is the one somebody is standing around for.</p>
      </div>
    `;
  } else if (model.phase === "ready" || model.picks.length > 0) {
    const unclaimed = model.picks.filter((p) => isUnclaimed(p.task.status));
    const claimed = model.picks.filter((p) => !isUnclaimed(p.task.status));
    body = html`
      ${unclaimed.length > 0
        ? html`<p class="section-label" data-unclaimed>${unclaimed.length} unclaimed</p>
            <div class="pick-grid">${unclaimed.map((p) => pickCard(p, model.me, false))}</div>`
        : nothing}
      ${claimed.length > 0
        ? html`<p class="section-label" data-claimed>${claimed.length} claimed</p>
            <div class="pick-grid">${claimed.map((p) => pickCard(p, model.me, false))}</div>`
        : nothing}
      ${model.moreBehind
        ? html`<button type="button" class="btn btn-secondary big" data-action="more" data-tap>Show more behind</button>`
        : nothing}
    `;
  } else {
    body = html``;
  }
  return html`
    <div class="branch-row">
      <span class="pill" data-branch>${model.branchName !== "" ? model.branchName : "This yard"}</span>
      <button type="button" class="btn btn-secondary" data-action="reload" data-tap>Refresh</button>
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
