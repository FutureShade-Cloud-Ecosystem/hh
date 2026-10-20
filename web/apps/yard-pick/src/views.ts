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
import { html, nothing, type TemplateResult } from "lit";
import type { DemoFlow } from "@hardscapehouse/api-client";
import type { YardChange, YardLine, YardPick, YardPickedLine, YardShortage, YardTask } from "./yard-api.js";
import {
  STATUS_LABEL,
  SHORT_REASONS,
  ageLabel,
  canAcknowledge,
  changeStatusWord,
  claimedByWords,
  isUnclaimed,
  lineIsShort,
  mtoLabel,
  orderHandle,
  pickSummary,
  isLocalLine,
  RECORD_WAITING_WORDS,
  priorityWord,
  qtyLabel,
  qtyText,
  queueOrder,
  reasonWord,
  runLineWords,
  shortageSummary,
  statusTone,
  ticketIsFixture,
} from "./yard-logic.js";

/** The status pill: colour never carries the meaning alone, the word is always there. */
function statusPill(status: string): TemplateResult {
  return html`<span class="status status-${statusTone(status)}" data-task-status data-pick-pill>${STATUS_LABEL[status] ?? status}</span>`;
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
  /** The waiting changes' count as the last read left it, or null when it is not known. */
  readonly changesWaiting: string | null;
  /** How stale the store's yard world is, in words ("Pulled 2 min ago"). */
  readonly staleness: string;
  /** The open conflict cards: the Review door names the count. */
  readonly conflicts: number;
}

/** The staleness words for a scope: reads answer from the device, so the
 * board says when the world last came down. Relative words only. */
export function stalenessWords(staleness: { neverPulled: boolean; ageMs: number | null }): string {
  if (staleness.neverPulled || staleness.ageMs === null) return "Never pulled";
  const minutes = Math.floor(staleness.ageMs / 60_000);
  if (minutes < 1) return "Pulled just now";
  return `Pulled ${minutes} min ago`;
}

function pickCard(entry: YardPick, me: string): TemplateResult {
  const t = entry.task;
  const mine = t.claimed_by !== "" && t.claimed_by === me;
  const claimedWords = t.status === "CANCELLED" ? "" : claimedByWords(t.claimed_by, me);
  const held = entry.ticket?.held_from_picking === true;
  const ticket = entry.ticket;
  const lineCount = ticket?.lines.length ?? 0;
  const summary = shortageSummary(entry.shortages);
  const mto = ticket !== null ? mtoLabel(ticket.mto_class, ticket.fulfillment) : "";
  const priority = priorityWord(ticket?.priority || t.priority);
  const runWords = runLineWords(t, ticket);
  const stripeTone = held
    ? "held"
    : t.status === "IN_PROGRESS" || t.status === "ACCEPTED"
      ? "picking"
      : t.status === "STAGED" || t.status === "DONE"
        ? "staged"
        : "queued";
  const inner = html`
    <div class="pick-head">
      <span class="pick-no" data-order-no>${orderHandle(ticket)}</span>
      <span class="pick-head-pills">
        ${held ? html`<span class="pill pill-watch" data-hold-pill>On hold</span>` : nothing}
        ${t.open_shortages > 0 ? html`<span class="status status-stopped" data-short-pill>Short</span>` : nothing}
        ${statusPill(t.status)}
      </span>
    </div>
    ${ticket?.customer_name ? html`<p class="pick-who">${ticket.customer_name}</p>` : nothing}
    ${runWords !== "" ? html`<p class="pick-card-run" data-pick-run data-run-line>${runWords}</p>` : nothing}
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
    ${claimedWords !== "" ? html`<p class="mine-tag" data-claimed-by>${claimedWords}</p>` : nothing}
  `;
  return html`
    <section class="card pick-card ${stripeTone} ${mine ? "mine" : ""}" data-pick-card data-task-id=${t.id}>
      ${held
        ? html`<div class="pick-card-inner">${inner}</div>`
        : html`<button
            type="button"
            class="pick-card-inner"
            data-action="open-pick"
            data-task-id=${t.id}
            aria-label=${`Open ${orderHandle(ticket)}`}
            data-tap
          >${inner}</button>`}
    </section>
  `;
}

/** The landing: the branch's open picks in departure order. */
export function renderQueue(model: QueueModel): TemplateResult {
  const ordered = queueOrder(model.picks);
  let body: TemplateResult;
  if (model.phase === "loading" && ordered.length === 0) {
    body = html`<p class="dim" role="status">Checking the yard.</p>`;
  } else if (model.phase === "ready" && ordered.length === 0) {
    body = html`
      <div class="card empty" data-empty>
        <p class="empty-head">Nothing to pick here.</p>
        <p class="dim">When the counter queues a pick it lands here, and the oldest one is the one somebody is standing around for.</p>
      </div>
    `;
  } else if (model.phase === "ready" || ordered.length > 0) {
    body = html`
      <div class="pick-grid" data-pick-list>
        ${ordered.map((p) => pickCard(p, model.me))}
      </div>
      ${model.moreBehind
        ? html`<button type="button" class="btn btn-secondary big" data-action="more" data-tap>Show more behind</button>`
        : nothing}
    `;
  } else {
    body = html``;
  }
  return html`
    <h1 data-queue-head>Pick queue</h1>
    <p class="dim small queue-sub" data-queue-sub>
      <span data-branch>${model.branchName !== "" ? model.branchName : "This yard"}</span> · ${ordered.length} ${ordered.length === 1 ? "order" : "orders"}, in departure order
    </p>
    ${model.staleness !== "" ? html`<p class="dim small" data-staleness>${model.staleness}</p>` : nothing}
    ${renderMessages(model.error, model.notice)}
    ${body}
    ${ordered.length > 0 || model.phase === "ready"
      ? html`<p class="dim small queue-footnote" data-queue-footnote>${QUEUE_FOOTNOTE}</p>`
      : nothing}
  `;
}

/** The conflict card list: every outcome the sync came back with, dismissible,
 * never deletable (the dismiss is the store's own, and it keeps the row). */
export function renderConflicts(model: { cards: number }): TemplateResult {
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to the queue</button>
    <h1>Review</h1>
    <p class="dim small" data-conflict-sub>
      What the server answered while you worked. A card is dismissed when you have read it; the record stays. (${model.cards})
    </p>
    <hh-shell-conflict-list data-conflict-list></hh-shell-conflict-list>
  `;
}

// ---- one pick: the pull list, the record, the shortage sheet, the skids, the stage ----

export interface PickModel {
  readonly pick: YardPick;
  readonly me: string;
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
  /** True after the current skid count was saved, closing the reason sheet until the count is stepped again. */
  readonly skidSaved?: boolean;
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
  const held = model.pick.ticket?.held_from_picking === true;
  const productId = line.product_id !== "" ? line.product_id : line.sku;
  return html`
    <li class="line ${short ? "short" : full ? "full" : ""}" data-line data-line-no=${line.line_no} data-product-id=${productId}>
      <div class="line-bar">
        <button
          type="button"
          class="line-row"
          data-action="pull-line"
          data-line-no=${line.line_no}
          aria-label=${full ? `Undo line ${line.line_no}, ${line.description}` : `Line ${line.line_no}, ${line.description}`}
          ?disabled=${held}
          data-tap
        >
          <span class="line-thumb" aria-hidden="true">
            <span class="line-thumb-fallback"></span>
            <img class="line-img" alt="" data-line-img data-product-id=${productId} />
            <span class="line-check ${full ? "full" : short ? "short" : ""}">${full ? "\u2713" : short ? "!" : ""}</span>
          </span>
          <span class="line-main">
            <span class="line-desc">${line.description}</span>
            <span class="line-sub">${line.sku !== "" ? `${line.sku} · ` : ""}${line.location !== "" ? line.location : "no bin on file"}</span>
            <span class="line-qty" data-line-qty>
              ${short ? html`<span class="picked-short">${qtyText(pickedRaw)}</span>` : html`${qtyText(pickedRaw)}`}
              <span class="dim"> / ${qtyText(line.qty_ordered)} ${line.uom}</span>
            </span>
            ${reason !== "" ? html`<span class="line-sub line-reason">${reasonWord(reason)}${note !== "" ? `: ${note}` : ""}</span>` : nothing}
            ${backordered !== "" ? html`<span class="line-sub backordered">${qtyLabel(backordered, line.uom)} on backorder</span>` : nothing}
          </span>
        </button>
        <button
          type="button"
          class="btn btn-secondary line-short"
          data-action="edit-line"
          data-line-no=${line.line_no}
          ?disabled=${held}
          data-tap
        >Short</button>
      </div>
      ${editing && !held ? renderQtyEditor(model, line, draft, recorded) : nothing}
    </li>
  `;
}

/**
 * The quantity editor: big, one hand, gloves. The field's value is never
 * bound (the position rules refuse it); the root mirrors what is typed and
 * writes the value back on its own updated() pass, so a re-render that opens
 * the reason rows never eats a typed number.
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
      ${pick.picked.some(isLocalLine) ? html`<p class="dim" data-pick-pending role="status">${RECORD_WAITING_WORDS}</p>` : nothing}
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
      <hh-sheet heading="Report short pick" open>
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
        <div slot="actions" class="actions">
          <button type="button" class="btn btn-primary big" data-action="send-shortage" data-line-no=${line.line_no} ?disabled=${model.busy} data-tap>Report short pick</button>
          <button type="button" class="btn btn-secondary big" data-action="close-sheet" ?disabled=${model.busy} data-tap>Cancel</button>
        </div>
      </hh-sheet>
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
      ${counted && !model.skidSaved
        ? html`
            <hh-sheet heading="Why did the skid count change?" data-skid-sheet open>
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
              <button slot="actions" type="button" class="btn btn-secondary big" data-action="save-skids" ?disabled=${model.busy} data-tap>
                Save skid count
              </button>
            </hh-sheet>
          `
        : counted && model.skidSaved
          ? html`<p class="hint" data-skid-saved>Skid count saved · ${model.skids} ${model.skids === 1 ? "skid" : "skids"}</p>`
          : html`<p class="hint">Step the count when you know it; the reason comes with the change.</p>`}
    </section>
  `;
}

function stageBox(t: YardTask): TemplateResult {
  return html`
    <section class="card staged-box" data-staged>
      <p class="staged-where">Staged at ${t.stage_location}</p>
      ${t.stage_ref !== "" ? html`<p class="staged-ref">Ref ${t.stage_ref} · tell the counter, it is what they read out · The counter has been told it is ready.</p>` : html`<p class="staged-ref">The counter has been told it is ready.</p>`}
    </section>
  `;
}

const BAY_CHIPS = ["Bay 1", "Bay 2", "Bay 3", "Dock A"] as const;

function stageCard(): TemplateResult {
  return html`
    <section class="card" data-stage-form>
      <hh-sheet heading="Stage this pick" data-stage-sheet open>
        <div class="bay-chips" role="group" aria-label="Stage bay shortcuts">
          ${BAY_CHIPS.map(
            (bay) => html`
              <button type="button" class="btn btn-secondary bay-chip" data-action="pick-bay" data-bay=${bay} data-tap>
                ${bay}
              </button>
            `,
          )}
        </div>
        <span class="field-label">Where did you put it?</span>
        <input class="text-field" type="text" autocomplete="off" data-field="stage-where" placeholder="Bay 3" aria-label="The stage location" />
        <p class="hint">A spot nobody can find is worse than an unstaged pick. The ref is generated for you if you leave it blank.</p>
        <div slot="actions" class="actions">
          <button type="button" class="btn btn-primary big" data-action="stage" data-tap>Stage</button>
          <button type="button" class="btn btn-secondary big" data-action="close-stage" data-tap>Not yet</button>
        </div>
      </hh-sheet>
    </section>
  `;
}

/** One pick: the pull list on the left (tablet) and the skids, stage and shortages beside it. */
export function renderPick(model: PickModel): TemplateResult {
  const pick = model.pick;
  const t: YardTask = pick.task;
  const ticket = pick.ticket;
  const held = ticket?.held_from_picking === true;
  const unclaimed = !held && isUnclaimed(t.status);
  const recordable = !held && (t.status === "IN_PROGRESS" || t.status === "STAGED" || t.status === "ACCEPTED" || unclaimed);
  const claimedWords = t.status === "CANCELLED" ? "" : claimedByWords(t.claimed_by, model.me);
  const runWords = runLineWords(t, ticket);
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to the queue</button>
    <h1 data-pick-order>${orderHandle(ticket)}</h1>
    ${renderMessages(model.error, model.notice)}
    <section class="card">
      <div class="detail-head">
        <p class="detail-who">${ticket?.customer_name || "Customer"}</p>
        ${statusPill(t.status)}
      </div>
      ${runWords !== "" ? html`<p class="pick-card-run" data-pick-run>${runWords}</p>` : nothing}
      <div class="pick-facts">
        ${ticket !== null && (ticket.fulfillment === "PICKUP" || ticket.fulfillment === "WILL_CALL")
          ? html`<span class="pill">Customer pickup</span>`
          : ticket !== null && ticket.fulfillment === "DELIVERY"
            ? html`<span class="pill">Delivery</span>`
            : nothing}
        ${priorityWord(ticket?.priority || t.priority) !== "" ? html`<span class="pill pill-watch">${priorityWord(ticket?.priority || t.priority)}</span>` : nothing}
        ${ticket !== null && mtoLabel(ticket.mto_class, ticket.fulfillment) !== "" ? html`<span>${mtoLabel(ticket.mto_class, ticket.fulfillment)}</span>` : nothing}
      </div>
      ${claimedWords !== "" ? html`<p class="mine-tag" data-claimed-by>${claimedWords}</p>` : nothing}
      ${held
        ? html`<p class="held" data-held>${ticket?.hold_words || "This order waits: it cannot be picked yet."}</p>`
        : nothing}
      ${unclaimed
        ? html`<div class="actions">
            <button type="button" class="btn btn-primary big" data-action="take-pick" ?disabled=${model.busy} data-tap>Take this pick</button>
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
        ${t.status === "STAGED" || t.status === "DONE" ? stageBox(t) : !held && t.status === "IN_PROGRESS" ? stageCard() : nothing}
      </div>
    </div>
  `;
}

// ---- the changes waiting on the yard (change orders, the yard's stage) ----

export interface ChangesModel {
  readonly phase: "loading" | "ready" | "error";
  readonly changes: readonly YardChange[];
  readonly moreBehind: boolean;
  readonly busy: boolean;
  readonly error: string;
  readonly notice: string;
  /** The change whose sheet is open, or null. */
  readonly sheet: YardChange | null;
  /** What the note field holds, mirrored, so a re-render never eats a typed word. */
  readonly sheetNote: string;
  /** A refused acknowledgement's own line, shown in the sheet. */
  readonly sheetError: string;
}

/**
 * The change sheet: the row's facts, and the acknowledge. The yard answers
 * one question here, "can we still do this", so the sheet carries what the
 * row carried and no figure; the button is the server's own offer (it
 * renders only where next_actions names yard_ack), and the refusal lands in
 * the sheet, in the server's words.
 */
function changeSheet(model: ChangesModel, change: YardChange): TemplateResult {
  return html`
    <section class="card sheet change-sheet" data-change-sheet data-change-id=${change.id}>
      <p class="sheet-head" tabindex="-1">Change ${change.change_no} · ${change.order_no}</p>
      <p class="dim">"${change.reason}"</p>
      <p class="dim small">asked by ${change.requested_by_role !== "" ? change.requested_by_role : "the counter"} · waiting ${ageLabel(change.requested_at, new Date())}</p>
      <p class="pill" data-change-status>${changeStatusWord(change.status)}</p>
      <p class="sheet-banner">Acknowledging tells the counter the yard can still do this. It does not change the order.</p>
      ${model.sheetError !== "" ? html`<p class="alert alert-error" role="alert" data-change-error>${model.sheetError}</p>` : nothing}
      ${canAcknowledge(change)
        ? html`
            <span class="field-label">Note for the counter (optional)</span>
            <input
              class="text-field"
              type="text"
              autocomplete="off"
              data-field="change-note"
              placeholder="Something the counter should know"
              ?disabled=${model.busy}
              aria-label="A note for the counter, optional"
            />
            <div class="actions">
              <button type="button" class="btn btn-primary big" data-action="ack-change" ?disabled=${model.busy} data-tap>Acknowledge</button>
              <button type="button" class="btn btn-secondary big" data-action="close-change-sheet" ?disabled=${model.busy} data-tap>Not now</button>
            </div>
          `
        : html`<p class="hint">This change is waiting on someone else.</p>`}
    </section>
  `;
}

/** One waiting change, the board's row shape: what it is, who asked, how long it has waited. */
function changeRow(change: YardChange): TemplateResult {
  return html`
    <section class="card change-card" data-change-row data-change-id=${change.id}>
      <button
        type="button"
        class="line-row"
        data-action="open-change"
        data-change-id=${change.id}
        aria-label=${`Change ${change.change_no} on ${change.order_no}`}
        data-tap
      >
        <span class="line-main">
          <span class="line-desc" data-change-no>${change.order_no}</span>
          <span class="line-sub">"${change.reason}"</span>
          <span class="line-sub">asked by ${change.requested_by_role !== "" ? change.requested_by_role : "the counter"} · waiting ${ageLabel(change.requested_at, new Date())}</span>
        </span>
        <span class="status status-active" data-change-status>${changeStatusWord(change.status)}</span>
      </button>
    </section>
  `;
}

/** The yard's own stage of the ladder: the changes the counter has asked the yard to answer. */
export function renderChanges(model: ChangesModel): TemplateResult {
  let body: TemplateResult;
  if (model.phase === "loading" && model.changes.length === 0) {
    body = html`<p class="dim" role="status">Checking for changes.</p>`;
  } else if (model.phase === "ready" && model.changes.length === 0) {
    body = html`
      <div class="card empty" data-changes-empty>
        <p class="empty-head">Nothing is waiting on the yard.</p>
        <p class="dim">The counter raises a change on an order once the pick has started. It lands here for you to acknowledge.</p>
      </div>
    `;
  } else {
    body = html`
      ${model.changes.map(changeRow)}
      ${model.moreBehind
        ? html`<button type="button" class="btn btn-secondary big" data-action="more-changes" data-tap>Show more behind</button>`
        : nothing}
    `;
  }
  return html`
    <button type="button" class="back" data-action="back" data-tap>Back to the queue</button>
    <h1>Changes waiting on the yard</h1>
    ${renderMessages(model.error, model.notice)}
    ${model.sheet !== null ? changeSheet(model, model.sheet) : nothing}
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