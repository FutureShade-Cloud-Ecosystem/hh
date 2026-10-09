// Manager Home's screens: the landing bento, the needs-you card inside it,
// the two views the needs links name (Shortages · open, Receiving · short),
// and the approvals list by kind. Pure template functions over plain model
// objects; app-root owns every call and every state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings. Controls ride data-action names and
// the host's listeners act on them; field values are mirrored from input
// events, never bound.
import { html, nothing, type TemplateResult } from "lit";
import { renderEntry, type EntryView } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import type {
  ApprovalRow,
  NeedsQueue,
  NeedsRow,
  QueueEntry,
  ReceivingRow,
  WidgetDatum,
  WidgetView,
} from "./manager-api.js";
import {
  APPROVAL_KINDS,
  RESOLUTION_CHOICES,
  UNSOURCED_NEEDS,
  UNSOURCED_TILES,
  VIEW_TITLE,
  figureFor,
  grouped,
  isReceivingDifference,
  kindWord,
  needsRowTarget,
  rangeWord,
  severityWord,
  shareNote,
  stateNote,
} from "./manager-logic.js";

// -- the landing -------------------------------------------------------------------------------------

export interface LandingModel {
  readonly phase: "loading" | "ready" | "error";
  readonly profile: string;
  readonly source: string;
  readonly widgets: readonly WidgetView[];
  readonly data: ReadonlyMap<string, WidgetDatum>;
  readonly needs: NeedsQueue | null;
  readonly error: string;
}

export function renderLanding(model: LandingModel): TemplateResult {
  if (model.phase === "loading") {
    return html`<section class="sheet"><p class="notice-line">Loading your home…</p></section>`;
  }
  if (model.phase === "error") {
    return html`
      <section class="sheet">
        <p class="error-line">${model.error}</p>
        <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload">Try again</button></div>
      </section>
    `;
  }
  return html`
    <div class="bento">
      ${model.widgets.filter((w) => w.visible).map((w, index) => renderWidget(w, model.data.get(w.widget_key), model.needs, index))}
    </div>
    ${UNSOURCED_TILES.length > 0
      ? html`${UNSOURCED_TILES.map((t) => html`<p class="who-may">${t.title}: ${t.words}</p>`)}`
      : nothing}
  `;
}

/** One widget card, by the shape the profile named. The server picked the shape, split and window. */
function renderWidget(
  w: WidgetView,
  datum: WidgetDatum | undefined,
  needs: NeedsQueue | null,
  index: number,
): TemplateResult {
  if (w.measure === "needs.you") {
    return withSpan(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${needs === null ? html`<p class="needs-empty">The queue did not load.</p>` : renderNeedsList(needs)}
      </article>
    `);
  }
  const figure = figureFor(datum, w.kind);
  const caption = rangeWord(datum?.range !== "" && datum !== undefined ? datum.range : w.range);
  const hero = index === 0 && w.shape === "stat" && w.span_cols >= 6;
  if (w.shape === "board") {
    return withSpan(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${figure === null
          ? html`<p class="figure none">-</p><p class="card-sub">${stateNote(datum, w.module)}</p>`
          : html`<div class="chips">${datum?.points.map((p) => renderStatusChip(p))}</div>`}
      </article>
    `);
  }
  if (w.shape === "bars") {
    return withSpan(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${renderBars(datum, w)}
      </article>
    `);
  }
  if (w.shape === "gauge") {
    // A GAUGE WITH NO CEILING FROM THE SERVER RENDERS ITS COUNT AND NO FILL. The core measures
    // this measure as a count; inventing a scale here would paint a severity the server never
    // sent, which is the confident wrong number this app exists to never show.
    return withSpan(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${figure === null
          ? html`<p class="figure none">-</p><p class="card-sub">${stateNote(datum, w.module)}</p>`
          : html`<p class="figure small">${figure}</p><p class="card-sub">${caption}</p>`}
      </article>
    `);
  }
  return withSpan(w, html`
    <article class="card${hero ? " hero" : ""}" data-widget=${w.widget_key}>
      <h2 class="card-title">${w.title}</h2>
      ${figure === null
        ? html`<p class="figure none">-</p><p class="card-sub">${stateNote(datum, w.module)}</p>`
        : html`<p class="figure${hero ? "" : " small"}">${figure}</p><p class="card-sub">${caption}</p>`}
    </article>
  `);
}

function withSpan(w: WidgetView, body: TemplateResult): TemplateResult {
  // Static span classes from the closed vocabulary (columns 3/4/6/8/9/12, rows 1 to 3): the
  // position gate refuses a bound style attribute, and the server's span arithmetic stays the
  // only arithmetic by naming a class per footprint instead of computing one.
  const cols = Math.max(1, Math.min(12, w.span_cols));
  const rows = Math.max(1, Math.min(3, w.span_rows));
  return html`<div class="s${cols === 3 || cols === 4 || cols === 6 || cols === 8 || cols === 9 || cols === 12 ? cols : 6} r${rows}">${body}</div>`;
}

function renderStatusChip(p: { label: string; number: number; tone: string }): TemplateResult {
  return html`<span class="chip ${p.tone}">${p.label} <span class="n">${grouped(p.number)}</span></span>`;
}

function renderBars(datum: WidgetDatum | undefined, w: WidgetView): TemplateResult {
  if (figureFor(datum, w.kind) === null || datum === undefined || datum.points.length === 0) {
    return html`<p class="figure none">-</p><p class="card-sub">${stateNote(datum, w.module)}</p>`;
  }
  const max = datum.points.reduce((high, p) => (p.number > high ? p.number : high), 0);
  return html`
    <ul class="bars">
      ${datum.points.map((p) => html`
        <li class="bar-row">
          <span class="label">${p.label}</span>
          <span class="track"><span class="fill ${barWidthClass(p.number, max)}"></span></span>
          <span class="value">${grouped(p.number)}</span>
          <span class="share">${shareNote(p.number, datum.total ?? 0)}</span>
        </li>
      `)}
    </ul>
  `;
}

/**
 * The bar's fill, quantized to the sheet's static width classes in steps of five percent: the
 * position gate refuses a bound style attribute, and the EXACT figures ride the row as text
 * (the value and the share note), so the track is the approximation a glance reads and never
 * the number a person quotes.
 */
function barWidthClass(part: number, max: number): string {
  const share = max > 0 ? Math.round((part / max) * 20) * 5 : 0;
  return `w${Math.max(0, Math.min(100, share))}`;
}

// -- the needs-you card --------------------------------------------------------------------------------

export function renderNeedsList(needs: NeedsQueue): TemplateResult {
  const rows = needs.rows;
  return html`
    ${rows.length === 0
      ? html`<p class="needs-empty">Nothing is waiting on you right now.</p>`
      : html`
          ${rows.map((row) => renderNeedsRow(row))}
          <p class="needs-total">${grouped(needs.total)} waiting · ${severityWord(needs.worst)}</p>
        `}
    ${UNSOURCED_NEEDS.map((u) => html`
      <div class="unsourced" data-unsourced=${u.key}>
        <p class="what">${u.what}</p>
        <p class="words">${u.words}</p>
      </div>
    `)}
  `;
}

function renderNeedsRow(row: NeedsRow): TemplateResult {
  const target = needsRowTarget(row);
  return html`
    <div class="needs-row" data-needs=${row.source_key}>
      <span class="sev ${row.severity}" aria-hidden="true"></span>
      <span class="visually-hidden">${severityWord(row.severity)}</span>
      <span class="needs-what">
        <p class="what">${row.what}</p>
        <p class="view-words">${row.view !== "" ? row.view : row.screen}</p>
      </span>
      <p class="needs-count">${grouped(row.count)}</p>
      ${row.age !== "" ? html`<span class="needs-age">oldest ${row.age}</span>` : nothing}
      ${target.view !== null
        ? html`<button type="button" class="btn btn-secondary" data-action="open-view" data-view=${target.view}>${row.action}</button>`
        : html`<span class="view-words">not on this box yet</span>`}
    </div>
  `;
}

// -- the views' shared header ----------------------------------------------------------------------------

export function viewHeader(view: "shortages" | "receiving" | "approvals", where: string): TemplateResult {
  return html`
    <div class="view-head">
      <h1>${VIEW_TITLE[view]}</h1>
      ${where !== "" ? html`<span class="where">${where}</span>` : nothing}
      <button type="button" class="btn btn-secondary" data-action="back">Back to Home</button>
    </div>
  `;
}

// -- Shortages · open --------------------------------------------------------------------------------------

export interface ResolveDraft {
  readonly taskId: string;
  readonly shortageId: string;
  readonly choice: string;
  readonly note: string;
  readonly busy: boolean;
}

export interface ShortagesModel {
  readonly phase: "loading" | "ready" | "error";
  readonly branchName: string;
  readonly entries: readonly QueueEntry[];
  readonly resolving: ResolveDraft | null;
  readonly error: string;
  readonly notice: string;
  readonly resolverWords: string;
}

export function renderShortages(model: ShortagesModel): TemplateResult {
  return html`
    ${viewHeader("shortages", model.branchName)}
    ${model.error !== "" ? html`<p class="error-line" data-test="shortages-error">${model.error}</p>` : nothing}
    ${model.notice !== "" ? html`<p class="notice-line" data-test="shortages-notice">${model.notice}</p>` : nothing}
    ${model.phase === "loading"
      ? html`<p class="notice-line">Loading open shortages…</p>`
      : model.phase === "error"
        ? html`<section class="sheet"><div class="row-actions">
            <button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button>
          </div></section>`
        : renderShortageList(model)}
    <p class="who-may" data-test="resolver-words">${model.resolverWords}</p>
  `;
}

function renderShortageList(model: ShortagesModel): TemplateResult {
  const withOpen = model.entries.filter((e) => e.task.open_shortages > 0 || e.shortages.some((s) => s.resolved_at === null));
  if (withOpen.length === 0) {
    return html`<section class="sheet"><div class="list-card"><p class="notice-line">No open shortages at this branch.</p></div></section>`;
  }
  return html`
    <section class="sheet">
      ${withOpen.map((entry) => renderShortageTask(entry, model.resolving))}
    </section>
  `;
}

function renderShortageTask(entry: QueueEntry, resolving: ResolveDraft | null): TemplateResult {
  const open = entry.shortages.filter((s) => s.resolved_at === null);
  const done = entry.shortages.filter((s) => s.resolved_at !== null);
  const orderLabel = entry.order_no !== "" ? entry.order_no : entry.task.order_id;
  return html`
    <article class="task-card" data-task=${entry.task.id}>
      <div class="task-head">
        <p class="order">${orderLabel}</p>
        ${entry.customer_name !== "" ? html`<span class="meta">${entry.customer_name}</span>` : nothing}
        <span class="meta">${entry.task.status}</span>
        ${entry.task.stage_location !== "" ? html`<span class="meta">staged at ${entry.task.stage_location}</span>` : nothing}
        <span class="meta">${entry.task.open_shortages} open</span>
      </div>
      ${open.map((s) => renderShortageRow(entry.task.id, s, resolving))}
      ${done.map((s) => html`
        <div class="shortage" data-shortage=${s.id}>
          <div class="body">
            <p class="line">${s.description}</p>
            <p class="detail">${shortageLine(s)}</p>
            <p class="resolved-word">Resolved: ${s.resolution}</p>
          </div>
        </div>
      `)}
    </article>
  `;
}

function renderShortageRow(taskId: string, s: QueueEntry["shortages"][number], resolving: ResolveDraft | null): TemplateResult {
  const active = resolving !== null && resolving.shortageId === s.id;
  if (active && resolving !== null) {
    return renderResolveSheet(resolving, s);
  }
  return html`
    <div class="shortage" data-shortage=${s.id}>
      <div class="body">
        <p class="line">${s.description}</p>
        <p class="detail">${shortageLine(s)}</p>
        ${s.note !== "" ? html`<p class="detail">${s.note}</p>` : nothing}
      </div>
      <div class="row-actions">
        <button type="button" class="btn btn-primary" data-action="open-resolve" data-task-id=${taskId} data-shortage-id=${s.id}>Resolve</button>
      </div>
    </div>
  `;
}

function shortageLine(s: QueueEntry["shortages"][number]): string {
  const line = s.order_line_no === null ? "" : `line ${s.order_line_no}, `;
  return `${line}short ${s.qty_short} ${s.uom}`;
}

function renderResolveSheet(draft: ResolveDraft, s: QueueEntry["shortages"][number]): TemplateResult {
  return html`
    <div class="shortage" data-shortage=${s.id} data-resolving="true">
      <div class="body">
        <p class="line">${s.description}</p>
        <p class="detail">${shortageLine(s)}</p>
        ${s.note !== "" ? html`<p class="detail">${s.note}</p>` : nothing}
        <div class="choices">
          ${RESOLUTION_CHOICES.map((c) => html`
            <button type="button" class="choice" data-action="pick-resolution" data-choice=${c.key} aria-pressed=${draft.choice === c.key ? "true" : "false"}>
              <span class="mark" aria-hidden="true"></span>
              <span class="words">${c.label}</span>
            </button>
          `)}
        </div>
        <textarea class="text-field text-area" rows="2" data-field="resolution-note" placeholder="Note (optional)" ?disabled=${draft.busy}></textarea>
        <div class="row-actions">
          <button type="button" class="btn btn-primary" data-action="confirm-resolve" ?disabled=${draft.busy || draft.choice === ""}>Resolve</button>
          <button type="button" class="btn btn-secondary" data-action="cancel-resolve" ?disabled=${draft.busy}>Cancel</button>
        </div>
      </div>
    </div>
  `;
}

// -- Receiving · short ---------------------------------------------------------------------------------------

export interface ReceivingModel {
  readonly phase: "loading" | "ready" | "error";
  readonly branchName: string;
  readonly rows: readonly ReceivingRow[];
  readonly error: string;
  readonly definitionWords: string;
}

export function renderReceiving(model: ReceivingModel): TemplateResult {
  const differences = model.rows.filter(isReceivingDifference);
  const rest = model.rows.length - differences.length;
  return html`
    ${viewHeader("receiving", model.branchName)}
    ${model.error !== "" ? html`<p class="error-line">${model.error}</p>` : nothing}
    <p class="def-note" data-test="receiving-definition">${model.definitionWords}</p>
    ${model.phase === "loading"
      ? html`<p class="notice-line">Loading the receiving dock…</p>`
      : model.phase === "error"
        ? html`<section class="sheet"><div class="row-actions">
            <button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button>
          </div></section>`
        : html`
            <section class="sheet">
              ${differences.length === 0
                ? html`<div class="list-card"><p class="notice-line">No purchase order landed short at this branch.</p></div>`
                : differences.map((row) => html`
                    <article class="list-card" data-po=${row.po_id}>
                      <div class="task-head">
                        <p class="order">${row.po_no}</p>
                        <span class="meta">${row.vendor_name}</span>
                        <span class="chip watch">Partial</span>
                      </div>
                      <p class="detail">${row.open_lines} line${row.open_lines === 1 ? "" : "s"} still short</p>
                      ${row.expected_at !== "" ? html`<p class="detail">expected ${row.expected_at}</p>` : nothing}
                    </article>
                  `)}
              ${rest > 0 ? html`<p class="count-line">${grouped(rest)} purchase order${rest === 1 ? "" : "s"} on the dock landed everything ordered.</p>` : nothing}
            </section>
          `}
  `;
}

// -- Approvals -------------------------------------------------------------------------------------------------

export interface ApprovalsModel {
  readonly phase: "loading" | "ready" | "error" | "refused";
  readonly kind: string;
  readonly rows: readonly ApprovalRow[];
  readonly error: string;
  readonly refusal: string;
  readonly busyId: string;
}

export function renderApprovals(model: ApprovalsModel): TemplateResult {
  return html`
    ${viewHeader("approvals", "")}
    <div class="kind-tabs" role="tablist">
      ${APPROVAL_KINDS.map((k) => html`
        <button type="button" class="kind-tab" role="tab" data-action="pick-kind" data-kind=${k.kind} aria-pressed=${model.kind === k.kind ? "true" : "false"}>
          ${k.word}
        </button>
      `)}
    </div>
    ${model.error !== "" ? html`<p class="error-line">${model.error}</p>` : nothing}
    ${model.refusal !== "" ? html`<p class="who-may" data-test="approvals-refusal">${model.refusal}</p>` : nothing}
    ${model.phase === "loading"
      ? html`<p class="notice-line">Loading ${kindWord(model.kind)} approvals…</p>`
      : model.phase === "error"
        ? html`<div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button></div>`
        : model.rows.length === 0
          ? html`<section class="sheet"><div class="list-card"><p class="notice-line">No ${kindWord(model.kind)} approvals waiting.</p></div></section>`
          : html`
              <section class="sheet">
                <div class="list-card">
                  ${model.rows.map((row) => renderApprovalRow(row, model.busyId))}
                </div>
              </section>
            `}
  `;
}

function renderApprovalRow(row: ApprovalRow, busyId: string): TemplateResult {
  const pending = row.status === "PENDING";
  return html`
    <div class="approval" data-approval=${row.id}>
      <p class="detail">${row.detail}</p>
      <p class="meta">
        ${kindWord(row.policy_type)}
        ${row.amount_cents > 0 ? html` · <span class="amount">${money(row.amount_cents)}</span>` : nothing}
        ${row.subject_ref !== "" ? html` · ${row.subject_ref}` : nothing}
        ${pending ? nothing : html` · ${row.status === "APPROVED" ? "Approved" : "Rejected"}`}
      </p>
      ${pending
        ? html`<div class="row-actions">
            <button type="button" class="btn btn-primary" data-action="decide" data-approval-id=${row.id} data-decision="approve" ?disabled=${busyId !== ""}>Approve</button>
            <button type="button" class="btn btn-danger" data-action="decide" data-approval-id=${row.id} data-decision="reject" ?disabled=${busyId !== ""}>Reject</button>
          </div>`
        : nothing}
    </div>
  `;
}

function money(cents: number): string {
  // The only money rendering in this app: the core's integer cents, formatted for a person.
  // Computed nowhere, summed nowhere, converted nowhere.
  const sign = cents < 0 ? "-" : "";
  const magnitude = Math.abs(cents);
  const dollars = Math.trunc(magnitude / 100);
  const remainder = magnitude % 100;
  return `${sign}$${grouped(dollars)}.${String(remainder).padStart(2, "0")}`;
}

// -- the entry (the demo way in) -------------------------------------------------------------------------------

export type { EntryView };

export { renderEntry };
