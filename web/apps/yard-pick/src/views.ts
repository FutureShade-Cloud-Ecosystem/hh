// Yard Pick's screens, as plain render functions of a model. They hold no
// state and own no events: every control is a plain element carrying a
// data-action name, and the root element's one click listener acts on it
// (the design system's position rules allow no @ or . binding here, and the
// shared header takes the same shape). Every number on a screen is a count,
// a line number or a quantity: there is no money field to bind.
//
// THE POSITION GATE'S SHAPE, and why the forms look the way they do. A
// dependent package may bind text content, class, role, data- and aria-
// attribute values, whole unquoted data- and aria- values, and ?disabled,
// and nothing else. So no control here carries a bound value, a selected
// option or label text inside a textarea: the root element mirrors every
// field from input and change events and writes each field's value back in
// its own updated() pass, the same shape HH Drive's proof of delivery uses.
// The reasons are the board's big radio rows (buttons carrying role=radio and
// an aria-checked value), drawn from the core's closed CHECK list
// ("add, never rename") with the picker's words, never the codes; the unit
// leg holds the rows against reasonWord.
//
// References: the FB Design boards "HH: field apps" (the queue in light and
// brand, the pick with the skid stepper and the reason, the short pick sheet)
// and the original app's phone screens, drawn to HH's design system under
// row 563 (take the existing and improve it).
import { html, nothing, type TemplateResult } from "lit";
import type { DemoFlow } from "@hardscapehouse/api-client";
import type { YardLine, YardPick, YardPickedLine, YardShortage, YardTask } from "./yard-api.js";
import {
  STATUS_LABEL,
  SHORT_REASONS,
  actionLabel,
  ageLabel,
  isUnclaimed,
  lineIsShort,
  mtoLabel,
  nextAction,
  orderHandle,
  pickSummary,
  priorityWord,
  qtyLabel,
  qtyText,
  reasonWord,
  shortageSummary,
  statusTone,
  ticketIsFixture,
  type PickAction,
} from "./yard-logic.js";

/** The status pill: colour never carries the meaning alone, the word is always there. */
function statusPill(status: string): TemplateResult {
  return html`<span class="status status-${statusTone(status)}" data-task-status>${STATUS_LABEL[status] ?? status}</span>`;
}

/** The board's own board footnote, under the cards. */
const QUEUE_FOOTNOTE = "The queue is fed by dispatched orders. Newest work sits on top; a short pick stays on the board until the yard manager clears it.";

const REASON_GROUP_LABEL: Record<string, string> = {
  line: "Why the line is short",
  sheet: "Why it is short",
  skid: "The reason for the skid count",
};

/**
 * The board's reason rows: one big tappable row per reason, a radio circle at
 * the left, the chosen one outlined. The core's closed list rides the
 * data-reason value; the picker reads words and never a code.
 */
function reasonRows(scope: "line" | "sheet" | "skid", chosen: string, busy: boolean, lineNo = 0): TemplateResult {
  return html`
    <div class="reason-rows" role="radiogroup" aria-label=${REASON_GROUP_LABEL[scope] ?? "The reason"} data-reason-group=${scope}>
      ${SHORT_REASONS.map(
        (code) => html`
          <button
            type="button"
            class="reason-row ${chosen === code ? "chosen" : ""}"
            role="radio"
            aria-checked=${chosen === code ? "true" : "false"}
            data-action="pick-reason"
            data-scope=${scope}
            data-reason=${code}
            data-line-no=${lineNo}
            ?disabled=${busy}
            data-tap
          >
            <span class="reason-dot" aria-hidden="true"></span>
            <span class="reason-word">${reasonWord(code)}</span>
          </button>
        `,
      )}
    </div>
  `;
}

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

function pickCard(entry: YardPick, me: string, busy: boolean): TemplateResult {
  const t = entry.task;
  const mine = t.claimed_by !== "" && t.claimed_by === me;
  const held = entry.ticket?.held_from_picking === true;
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
        <span class="pick-head-pills">${t.open_shortages > 0 ? html`<span class="status status-stopped" data-short-pill>Short</span>` : nothing}${statusPill(t.status)}</span>
      </div>
      ${ticket?.customer_name ? html`<p class="pick-who">${ticket.customer_name}</p>` : nothing}
      <div class="pick-facts">
        <span>${lineCount} ${lineCount === 1 ? "line" : "lines"}</span>
        ${priority !== "" ? html`<span class="pill pill-watch">${priority}</span>` : nothing}
        <span>waiting ${ageLabel(t.created_at, new Date())}</span>
        ${ticket !== null && ticketIsFixture(ticket) ? html`<span class="pill">fixture ticket</span>` : nothing}
      </div>
      ${mto !== "" ? html`<p class="pick-facts">${mto}</p>` : nothing}
      ${held
        ? html`<p class="held" data-held>${ticket?.hold_words || "This order waits: it cannot be picked yet."}</p>`
        : nothing}
      ${summary !== "" ? html`<p class="shortage-open" data-queue-shortage>${summary}</p>` : nothing}
      ${mine ? html`<p class="mine-tag">Claimed by you</p>` : nothing}
      ${held
        ? // A held order waits: no control the server would refuse.
          html`<span class="pill pill-watch" data-hold-pill>On hold</span>`
        : unclaimed
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
      <span class="pill branch-pill" data-branch>${model.branchName !== "" ? model.branchName : "This yard"}</span>
      <button type="button" class="btn btn-secondary" data-action="reload" data-tap>Refresh</button>
    </div>
    <h1>Pick queue</h1>
    ${renderMessages(model.error, model.notice)}
    ${body}
    ${model.picks.length > 0 || model.phase === "ready"
      ? html`<p class="dim small queue-footnote" data-queue-footnote>${QUEUE_FOOTNOTE}</p>`
      : nothing}
  `;
}

// ---- one pick: the pull list, the record, the shortage sheet, the skids, the stage ----

export interface PickModel {
  readonly pick: YardPick;
  /** The drafted quantities and reasons, one per ticket line, mirrored from the fields. */
  readonly drafts: ReadonlyArray<{
    order_line_no: number;
    qty_picked: string;
    short_reason: string;
    short_note: string;
  }>;
  /** The line whose quantity editor is open, by line number; null when none. */
  readonly editing: number | null;
  /** The line the shortage sheet is open for; null when none. */
  readonly sheetFor: number | null;
  /** What the sheet's quantity field holds, mirrored, so the SHORT tile reads live. */
  readonly sheetQty: string;
  /** The reason chosen on the sheet, mirrored, so the radio row shows its chosen state. */
  readonly sheetReason: string;
  /** The shortage whose resolution form is open; empty when none. */
  readonly resolveFor: string;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  /** The skid count as the stepper holds it; null when nobody has counted yet. */
  readonly skids: number | null;
  /** The skid reason chosen, mirrored, so the radio row shows its chosen state. */
  readonly skidReason: string;
}

function draftFor(model: PickModel, lineNo: number): { order_line_no: number; qty_picked: string; short_reason: string; short_note: string } | null {
  return model.drafts.find((d) => d.order_line_no === lineNo) ?? null;
}

/** The recorded pick indexed by the ticket's own line number. */
function pickedByLine(picked: readonly YardPickedLine[]): Map<number, YardPickedLine> {
  const out = new Map<number, YardPickedLine>();
  for (const l of picked) out.set(l.order_line_no, l);
  return out;
}

function lineRow(model: PickModel, line: YardLine, recorded: YardPickedLine | undefined): TemplateResult {
  const draft = draftFor(model, line.line_no);
  const pickedRaw = draft !== null ? draft.qty_picked : (recorded?.qty_picked ?? "");
  const short = pickedRaw !== "" && lineIsShort(line.qty_ordered, pickedRaw);
  const full = pickedRaw !== "" && !short;
  const editing = model.editing === line.line_no;
  const reason = draft !== null ? draft.short_reason : (recorded?.short_reason ?? "");
  const note = draft !== null ? draft.short_note : (recorded?.short_note ?? "");
  const backordered = line.qty_backordered;
  return html`
    <li data-line data-line-no=${line.line_no}>
      <button
        type="button"
        class="line-row"
        data-action="edit-line"
        data-line-no=${line.line_no}
        aria-label=${`Line ${line.line_no}, ${line.description}`}
        data-tap
      >
        <span class="line-check ${full ? "full" : short ? "short" : ""}" aria-hidden="true">${full ? "\u2713" : short ? "!" : ""}</span>
        <span class="line-main">
          <span class="line-desc">${line.description}</span>
          <span class="line-sub">${line.sku !== "" ? `${line.sku} · ` : ""}${line.location !== "" ? line.location : "no bin on file"}</span>
          ${reason !== "" ? html`<span class="line-sub line-reason">${reasonWord(reason)}${note !== "" ? `: ${note}` : ""}</span>` : nothing}
          ${backordered !== "" ? html`<span class="line-sub backordered">${qtyLabel(backordered, line.uom)} on backorder</span>` : nothing}
        </span>
        <span class="line-qty" data-line-qty>
          ${short ? html`<span class="picked-short">${qtyText(pickedRaw)}</span>` : html`${qtyText(pickedRaw)}`}
          <span class="dim"> / ${qtyText(line.qty_ordered)} ${line.uom}</span>
        </span>
      </button>
      ${editing ? renderQtyEditor(model, line, draft, recorded) : nothing}
    </li>
  `;
}

/**
 * The quantity editor: big, one hand, gloves. The field's value is never
 * bound (the position rules refuse it); the root mirrors what is typed and
 * writes the value back on its own updated() pass, so a re-render that opens
 * the reason select never eats a typed number.
 */
function renderQtyEditor(
  model: PickModel,
  line: YardLine,
  draft: { order_line_no: number; qty_picked: string; short_reason: string; short_note: string } | null,
  recorded: YardPickedLine | undefined,
): TemplateResult {
  const current = draft !== null ? draft.qty_picked : (recorded?.qty_picked ?? "");
  const short = current !== "" && lineIsShort(line.qty_ordered, current);
  return html`
    <div class="qty-edit" data-qty-edit>
      <div class="qty-row">
        <button type="button" class="stepper" data-action="qty-step" data-line-no=${line.line_no} data-step="-1" ?disabled=${model.busy} data-tap aria-label="Take one off">-</button>
        <input
          class="qty-field"
          type="text"
          inputmode="decimal"
          data-field="qty"
          data-line-no=${line.line_no}
          ?disabled=${model.busy}
          aria-label=${`How many ${line.description} came off the shelf`}
        />
        <button type="button" class="stepper" data-action="qty-step" data-line-no=${line.line_no} data-step="1" ?disabled=${model.busy} data-tap aria-label="Add one on">+</button>
      </div>
      <div class="qty-quick">
        <button type="button" class="stepper" data-action="qty-full" data-line-no=${line.line_no} ?disabled=${model.busy} data-tap>All ${qtyText(line.qty_ordered)}</button>
        <button type="button" class="stepper" data-action="qty-none" data-line-no=${line.line_no} ?disabled=${model.busy} data-tap>None</button>
        <button type="button" class="stepper" data-action="qty-done" data-line-no=${line.line_no} ?disabled=${model.busy} data-tap>Done</button>
      </div>
      ${short
        ? html`
            <span class="field-label">Why short (required)</span>
            ${reasonRows("line", draft?.short_reason ?? "", model.busy, line.line_no)}
            ${(draft?.short_reason ?? "") === "OTHER"
              ? html`
                  <span class="field-label">Say what happened</span>
                  <textarea
                    class="text-field text-area"
                    rows="2"
                    data-field="short-note"
                    data-line-no=${line.line_no}
                    placeholder="Note for the counter (optional)"
                    ?disabled=${model.busy}
                    aria-label=${`What happened on line ${line.line_no}`}
                  ></textarea>
                `
              : nothing}
          `
        : nothing}
    </div>
  `;
}

function pullList(model: PickModel): TemplateResult {
  const pick = model.pick;
  if (pick.ticket === null) {
    return html`
      <section class="card" data-no-ticket>
        <h3>Pull list</h3>
        <p class="dim">The ticket for this pick did not come through. The task is still on the board: tell the yard lead, and pull from the paperwork.</p>
      </section>
    `;
  }
  const recorded = pickedByLine(pick.picked);
  const summary = pickSummary(pick.picked);
  return html`
    <section class="card" data-pull-list>
      <h3>Pull list · ${pick.ticket.lines.length} ${pick.ticket.lines.length === 1 ? "line" : "lines"}</h3>
      ${summary !== "" ? html`<p class="dim" data-pick-summary>${summary}</p>` : nothing}
      <ul class="lines">${pick.ticket.lines.map((l) => lineRow(model, l, recorded.get(l.line_no)))}</ul>
    </section>
  `;
}

function shortageRow(model: PickModel, s: YardShortage): TemplateResult {
  const open = s.resolved_at === "";
  const resolving = model.resolveFor === s.id;
  return html`
    <li data-shortage data-shortage-id=${s.id}>
      <p class="shortage-what">Short ${qtyLabel(s.qty_short, s.uom)} of ${s.description}${s.note !== "" ? html`<span class="dim small"> · ${s.note}</span>` : nothing}</p>
      ${open
        ? html`<p class="shortage-open">Open: the counter has not settled it.</p>`
        : html`<p class="shortage-cleared">Cleared: ${s.resolution}</p>`}
      ${open && !resolving
        ? html`<button type="button" class="btn btn-secondary" data-action="resolve" data-shortage-id=${s.id} ?disabled=${model.busy} data-tap>Mark it settled</button>`
        : nothing}
      ${open && resolving
        ? html`
            <span class="field-label">How was it settled? (required)</span>
            <input class="text-field" type="text" autocomplete="off" data-field="resolution" ?disabled=${model.busy} aria-label="How the shortage was settled" />
            <div class="actions">
              <button type="button" class="btn btn-primary big" data-action="send-resolve" data-shortage-id=${s.id} ?disabled=${model.busy} data-tap>Send</button>
              <button type="button" class="btn btn-secondary big" data-action="cancel-resolve" ?disabled=${model.busy} data-tap>Never mind</button>
            </div>
            <p class="hint">Resolving a shortage is held to the shortage resolvers list: the Yard Team and the Yard Manager.</p>
          `
        : nothing}
    </li>
  `;
}

function shortageList(model: PickModel): TemplateResult | typeof nothing {
  const list = model.pick.shortages;
  if (list.length === 0) return nothing;
  return html`
    <section class="card" data-shortages>
      <h3>Shortages on this pick</h3>
      <ul class="shortage-list">
        ${list.map((s) => shortageRow(model, s))}
      </ul>
    </section>
  `;
}

/** The shortage sheet: the board's own shape. The asked and short figures as two labelled tiles, the reasons as big radio rows, a labelled note, and the banner that says what sending does. */
function shortageSheet(model: PickModel): TemplateResult | typeof nothing {
  if (model.sheetFor === null || model.pick.ticket === null) return nothing;
  const line = model.pick.ticket.lines.find((l) => l.line_no === model.sheetFor);
  if (line === undefined) return nothing;
  const shortQty = model.sheetQty.trim();
  return html`
    <section class="card sheet" data-shortage-sheet>
      <p class="sheet-head">Short pick · line ${line.line_no}</p>
      <p class="dim">${line.description}${line.sku !== "" ? ` · ${line.sku}` : ""}</p>
      <div class="figure-tiles">
        <div class="tile" data-tile="asked">
          <span class="tile-label">ASKED</span>
          <span class="tile-value">${qtyLabel(line.qty_ordered, line.uom)}</span>
        </div>
        <div class="tile tile-short" data-tile="short">
          <span class="tile-label">SHORT</span>
          <span class="tile-value">${shortQty !== "" ? qtyLabel(shortQty, line.uom) : html`<span class="dim">not said yet</span>`}</span>
        </div>
      </div>
      <span class="field-label">What is short</span>
      <input class="text-field" type="text" inputmode="decimal" data-field="sheet-qty" data-line-no=${line.line_no} ?disabled=${model.busy} aria-label="How many are short" />
      <span class="field-label">Why is it short?</span>
      ${reasonRows("sheet", model.sheetReason, model.busy)}
      <textarea
        class="text-field text-area"
        rows="2"
        data-field="sheet-note"
        placeholder="Note for the counter (optional)"
        ?disabled=${model.busy}
        aria-label="Say what happened, when the reason is Something else"
      ></textarea>
      <p class="sheet-banner" data-sheet-banner>When you send, the line is backordered and the order's rep is told.</p>
      <div class="actions">
        <button type="button" class="btn btn-primary big" data-action="send-shortage" data-line-no=${line.line_no} ?disabled=${model.busy} data-tap>Report short pick</button>
        <button type="button" class="btn btn-secondary big" data-action="close-sheet" ?disabled=${model.busy} data-tap>Cancel</button>
      </div>
    </section>
  `;
}

function skidsCard(model: PickModel): TemplateResult {
  const counted = model.skids !== null;
  return html`
    <section class="card" data-skids>
      <h3>Skids</h3>
      <div class="skid-row">
        <button type="button" class="stepper" data-action="skid-step" data-step="-1" ?disabled=${model.busy} data-tap aria-label="One skid fewer">-</button>
        <span class="skid-count ${counted ? "" : "unknown"}" data-skid-count>${counted ? String(model.skids) : "Not counted"}</span>
        <button type="button" class="stepper" data-action="skid-step" data-step="1" ?disabled=${model.busy} data-tap aria-label="One skid more">+</button>
      </div>
      ${counted
        ? html`
            <span class="field-label">Reason (required when you change it)</span>
            ${reasonRows("skid", model.skidReason, model.busy)}
            <textarea
              class="text-field text-area"
              rows="2"
              data-field="skid-note"
              placeholder="Note for the counter (optional)"
              ?disabled=${model.busy}
              aria-label="Say what happened, when the reason is Something else"
            ></textarea>
            <div class="actions">
              <button type="button" class="btn btn-secondary big" data-action="save-skids" ?disabled=${model.busy} data-tap>Save skid count</button>
            </div>
          `
        : html`<p class="hint">Step the count when you know it; the reason comes with the change.</p>`}
    </section>
  `;
}

function stageBox(t: YardTask): TemplateResult {
  return html`
    <section class="card staged-box" data-staged>
      <p class="staged-where">Staged at ${t.stage_location}</p>
      ${t.stage_ref !== "" ? html`<p class="staged-ref">Ref ${t.stage_ref} · tell the counter, it is what they read out</p>` : nothing}
    </section>
  `;
}

function stageCard(): TemplateResult {
  return html`
    <section class="card" data-stage-form>
      <h3>Stage this pick</h3>
      <span class="field-label">Where did you put it?</span>
      <input class="text-field" type="text" autocomplete="off" data-field="stage-where" placeholder="Bay 3" aria-label="The stage location" />
      <p class="hint">A spot nobody can find is worse than an unstaged pick. The ref is generated for you if you leave it blank.</p>
      <div class="actions">
        <button type="button" class="btn btn-primary big" data-action="stage" data-tap>Stage</button>
        <button type="button" class="btn btn-secondary big" data-action="close-stage" data-tap>Not yet</button>
      </div>
    </section>
  `;
}

/** One pick: the pull list on the left (tablet) and the skids, stage and shortages beside it. */
export function renderPick(model: PickModel): TemplateResult {
  const pick = model.pick;
  const t: YardTask = pick.task;
  const action: PickAction | null = nextAction(t.status);
  const ticket = pick.ticket;
  const recordable = t.status === "IN_PROGRESS" || t.status === "STAGED";
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to the queue</button>
    <h1 data-pick-order>${orderHandle(ticket)}</h1>
    ${renderMessages(model.error, model.notice)}
    <section class="card">
      <div class="detail-head">
        <p class="detail-who">${ticket?.customer_name || "Customer"}</p>
        ${statusPill(t.status)}
      </div>
      <div class="pick-facts">
        ${ticket !== null && (ticket.fulfillment === "PICKUP" || ticket.fulfillment === "WILL_CALL")
          ? html`<span class="pill">Customer pickup</span>`
          : ticket !== null && ticket.fulfillment === "DELIVERY"
            ? html`<span class="pill">Delivery</span>`
            : nothing}
        ${priorityWord(ticket?.priority || t.priority) !== "" ? html`<span class="pill pill-watch">${priorityWord(ticket?.priority || t.priority)}</span>` : nothing}
        ${ticket !== null && mtoLabel(ticket.mto_class, ticket.fulfillment) !== "" ? html`<span>${mtoLabel(ticket.mto_class, ticket.fulfillment)}</span>` : nothing}
      </div>
      ${ticket?.held_from_picking === true
        ? html`<p class="held" data-held>${ticket.hold_words || "This order waits: it cannot be picked yet."}</p>`
        : nothing}
      ${action !== null && action !== "stage"
        ? html`<div class="actions">
            <button type="button" class="btn btn-primary big" data-action=${action} data-task-id=${t.id} ?disabled=${model.busy} data-tap>${actionLabel(action)}</button>
          </div>`
        : nothing}
    </section>
    <div class="detail-grid">
      <div>
        ${pullList(model)}
        ${recordable ? skidsCard(model) : nothing}
        ${recordable
          ? html`<div class="actions" data-record-door>
              <button type="button" class="btn btn-primary big" data-action="save-pick" ?disabled=${model.busy} data-tap>Save pick</button>
              <button type="button" class="btn btn-secondary big btn-door" data-action="open-sheet" data-tap>Report a shortage</button>
            </div>`
          : nothing}
        ${shortageSheet(model)}
      </div>
      <div>
        ${shortageList(model)}
        ${t.status === "STAGED" || t.status === "DONE" ? stageBox(t) : t.status === "IN_PROGRESS" ? stageCard() : nothing}
      </div>
    </div>
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
