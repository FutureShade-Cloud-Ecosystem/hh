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
import type { DemoFlow } from "@hardscapehouse/api-client";
import { drawIcon, iconForId } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import { widgetLink } from "./landing-links.js";
import { roleWords } from "./role-words.js";
import { gaugeSegments, sparkColumns, sparkLabel } from "./widget-shapes.js";
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
  dateWords,
  figureFor,
  grouped,
  isReceivingDifference,
  kindWord,
  needsRowTarget,
  pickedWords,
  qtyWords,
  rangeWord,
  severityWord,
  shareNote,
  stateNote,
  taskStatusWord,
  welcomeLine,
} from "./manager-logic.js";

// -- the landing -------------------------------------------------------------------------------------

export interface LandingModel {
  readonly phase: "loading" | "ready" | "error";
  readonly profile: string;
  readonly source: string;
  readonly roleLabel: string;
  readonly widgets: readonly WidgetView[];
  readonly data: ReadonlyMap<string, WidgetDatum>;
  readonly needs: NeedsQueue | null;
  readonly error: string;
  /** Whether the landing draws the reporting link: the core's manager roles only, mirrored from the mint's tier. */
  readonly canOpenReporting: boolean;
  /** The needs rows the core could not measure on this profile, said in words (weights-logic.ts). */
  readonly unsourcedNeeds: typeof UNSOURCED_NEEDS;
}

const NOT_HERE = "not on this box yet";

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
  // The profile's widgets in the profile's order. The large tiles (size L) make the board's
  // top block on a twelve column grid; every other tile sits on one strip below it, the board's
  // row of five. The server picks the widgets, their order, shapes and sizes; this only places.
  const shown = model.widgets.filter((w) => w.visible);
  const large = shown.filter((w) => w.size === "L");
  const strip = shown.filter((w) => w.size !== "L");
  const words = roleWords(model.profile);
  return html`
    <header class="page-head">
      <h1>${welcomeLine(model.roleLabel)}</h1>
      <p class="page-sub">${words.sub}</p>
      <div class="head-actions">
        <button type="button" class="btn btn-secondary edit-open" data-action="open-view" data-view="builder">Arrange home</button>
      </div>
    </header>
    <p class="eyebrow">Today</p>
    <div class="bento">
      ${large.map((w, index) => renderWidget(w, model.data.get(w.widget_key), model.needs, model.unsourcedNeeds, index === 0))}
    </div>
    <div class="strip">
      ${strip.map((w) => renderWidget(w, model.data.get(w.widget_key), model.needs, model.unsourcedNeeds, false))}
      ${UNSOURCED_TILES.map((t) => renderUnsourcedTile(t))}
    </div>
    <nav class="view-links" aria-label="Views">
      <span class="nav-label">Open a view</span>
      ${words.views.map((v) => html`
        <button type="button" class="view-link" data-action="open-view" data-view=${v}>${VIEW_TITLE[v]}<span class="chev" aria-hidden="true">›</span></button>
      `)}
      ${model.canOpenReporting ? html`
        <button type="button" class="view-link" data-action="open-view" data-view="reporting">${VIEW_TITLE.reporting}<span class="chev" aria-hidden="true">›</span></button>
      ` : nothing}
    </nav>
  `;
}

/** A tile the widget catalog has no measure for: a tile in the grid with its words, never a number. */
function renderUnsourcedTile(t: { readonly title: string; readonly words: string }): TemplateResult {
  return html`
    <div class="tile">
      <article class="card" data-widget="messages.unread">
        <h2 class="card-title">${t.title}</h2>
        <p class="card-sub">${t.words}</p>
      </article>
    </div>
  `;
}

/** One widget card, by the shape the profile named. The server picked the shape, split and window. */
function renderWidget(
  w: WidgetView,
  datum: WidgetDatum | undefined,
  needs: NeedsQueue | null,
  unsourced: typeof UNSOURCED_NEEDS,
  first: boolean,
): TemplateResult {
  if (w.measure === "needs.you") {
    return placed(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${needs === null ? html`<p class="needs-empty">The queue did not load.</p>` : renderNeedsList(needs, unsourced)}
      </article>
    `);
  }
  const figure = figureFor(datum, w.kind);
  const caption = rangeWord(datum?.range !== "" && datum !== undefined ? datum.range : w.range);
  const hero = first && w.shape === "stat" && w.span_cols >= 6;
  const foot = renderFoot(w);
  if (w.shape === "board" || w.shape === "bars") {
    return placed(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${renderBars(datum, w, w.shape === "bars")}
        ${foot}
      </article>
    `);
  }
  if ((w.shape === "trend" || w.shape === "gauge") && figure !== null && datum !== undefined) {
    return placed(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        <p class="figure small">${figure}</p>
        <p class="card-sub">${caption}</p>
        ${w.shape === "trend" ? renderTrend(datum, w) : renderGauge(datum, w)}
        ${foot}
      </article>
    `);
  }
  // A gauge with no ceiling from the server renders its count and no fill. The core measures
  // this measure as a count; inventing a scale here would paint a severity the server never
  // sent, which is the confident wrong number this app exists to never show.
  return placed(w, html`
    <article class="card${hero ? " hero" : ""}" data-widget=${w.widget_key}>
      <h2 class="card-title">${w.title}</h2>
      ${figure === null
        ? html`<p class="figure none">-</p><p class="card-sub">${stateNote(datum, w.module)}</p>`
        : html`<p class="figure${hero ? "" : " small"}">${figure}</p><p class="card-sub">${caption}</p>`}
      ${foot}
    </article>
  `);
}

/**
 * The foot of a card: a link to the live app (or this app's view) that owns the measure, and the
 * honest coming words only where no app exists. A link never carries a figure of its own.
 */
function renderFoot(w: WidgetView): TemplateResult {
  const link = widgetLink(w);
  if (link.kind === "app") {
    return html`<p class="tile-foot"><button type="button" class="foot-link" data-action="open-app" data-href=${link.href}>Open ${link.app}<span class="chev" aria-hidden="true">›</span></button></p>`;
  }
  if (link.kind === "view") {
    return html`<p class="tile-foot"><button type="button" class="foot-link" data-action="open-view" data-view=${link.view}>Open ${VIEW_TITLE[link.view]}<span class="chev" aria-hidden="true">›</span></button></p>`;
  }
  return html`<p class="tile-foot">${NOT_HERE}</p>`;
}

/**
 * The trend: a column per point the server sent, the tallest at full height. With fewer than two
 * honest points there is no line, and the card is its figure alone (never an invented series).
 * The first and last point's labels sit under it as text; nothing animates, so there is no motion
 * to reduce.
 */
function renderTrend(datum: WidgetDatum, w: WidgetView): TemplateResult | typeof nothing {
  const columns = sparkColumns(datum.points);
  if (columns === null) return nothing;
  return html`
    <div class="spark" role="img" aria-label=${sparkLabel(datum.points, w.kind)}>
      ${columns.map((c) => html`<span class="spark-col ${c.heightClass}"></span>`)}
    </div>
    <p class="spark-ends"><span>${columns[0].label}</span><span>${columns[columns.length - 1].label}</span></p>
  `;
}

/**
 * The gauge: the points' shares of their own sum as one segmented meter, with the tone the server
 * named, and a legend that carries every exact figure as text. The core measures this as a count
 * with no ceiling, so the meter shows composition, never "how full": no invented scale.
 */
function renderGauge(datum: WidgetDatum, w: WidgetView): TemplateResult | typeof nothing {
  const segments = gaugeSegments(datum.points);
  if (segments === null) return nothing;
  const spoken = `${w.title}: ${segments.map((g) => `${g.label} ${grouped(g.number)}`).join(", ")}`;
  return html`
    <div class="meter" role="img" aria-label=${spoken}>
      ${segments.map((g) => html`<span class="seg ${g.widthClass} ${g.toneClass}"></span>`)}
    </div>
    <ul class="legend">
      ${segments.map((g) => html`<li class="legend-row"><span class="swatch ${g.toneClass}" aria-hidden="true"></span><span class="label">${g.label}</span><span class="value">${grouped(g.number)}</span></li>`)}
    </ul>
  `;
}

function placed(w: WidgetView, body: TemplateResult): TemplateResult {
  // Static classes only: the position gate refuses a bound style attribute.
  if (w.size === "L") {
    return html`<div class="cell ${w.span_cols >= 12 ? "full" : "half"}${w.shape === "list" ? " tall" : ""}">${body}</div>`;
  }
  return html`<div class="tile${w.span_cols >= 6 ? " wide" : ""}">${body}</div>`;
}

function renderBars(datum: WidgetDatum | undefined, w: WidgetView, withShare: boolean): TemplateResult {
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
          ${withShare ? html`<span class="share">${shareNote(p.number, datum.total ?? 0)}</span>` : nothing}
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

export function renderNeedsList(needs: NeedsQueue, unsourced: typeof UNSOURCED_NEEDS): TemplateResult {
  const rows = needs.rows;
  // The queue link opens the first view a row names; with none on this box it is not drawn.
  const first = rows.map((r) => needsRowTarget(r)).find((t) => t.view !== null);
  return html`
    ${rows.length === 0 ? html`<p class="needs-empty">Nothing is waiting on you right now.</p>` : rows.map((row) => renderNeedsRow(row))}
    ${unsourced.map((u) => html`
      <div class="needs-row unsourced" data-unsourced=${u.key}>
        <span class="sev" aria-hidden="true"></span>
        <div class="needs-body">
          <p class="what">${u.what}</p>
          <p class="words">${u.words}</p>
        </div>
      </div>
    `)}
    ${rows.length === 0
      ? nothing
      : html`
          <div class="needs-foot">
            <p class="needs-total">${grouped(needs.total)} waiting · ${severityWord(needs.worst)}</p>
            ${first !== undefined && first.view !== null
              ? html`<button type="button" class="queue-link" data-action="open-view" data-view=${first.view}>Open the queue<span class="chev" aria-hidden="true">›</span></button>`
              : nothing}
          </div>
        `}
  `;
}

function renderNeedsRow(row: NeedsRow): TemplateResult {
  const target = needsRowTarget(row);
  return html`
    <div class="needs-row" data-needs=${row.source_key}>
      <span class="sev ${row.severity}" aria-hidden="true"></span>
      <span class="visually-hidden">${severityWord(row.severity)}</span>
      <div class="needs-body">
        <p class="what">${row.what}</p>
        <p class="view-words">${row.view !== "" ? row.view : row.screen}</p>
        <p class="needs-meta"><span class="needs-count">${grouped(row.count)}</span> waiting${row.age !== "" ? html` · oldest ${row.age}` : nothing}</p>
      </div>
      ${target.view !== null
        ? html`<button type="button" class="row-chip" data-action="open-view" data-view=${target.view}>${row.action}<span class="chev" aria-hidden="true">›</span></button>`
        : "href" in target
          ? html`<button type="button" class="row-chip" data-action="open-app" data-href=${target.href} aria-label=${`${row.action}, opens ${target.app}`}>${row.action}<span class="chev" aria-hidden="true">›</span></button>`
          : html`<span class="not-here">${NOT_HERE}</span>`}
    </div>
  `;
}

// -- the views' shared header ----------------------------------------------------------------------------

export function viewHeader(view: "shortages" | "receiving" | "approvals" | "drivers", where: string, sub: string): TemplateResult {
  return html`
    <div class="view-head">
      <button type="button" class="btn btn-secondary back" data-action="back"><span class="chev" aria-hidden="true">‹</span>Back to Home</button>
      <h1>${VIEW_TITLE[view]}</h1>
      ${where !== "" ? html`<span class="where">${where}</span>` : nothing}
      ${sub !== "" ? html`<p class="page-sub">${sub}</p>` : nothing}
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
  // A refusal of the resolve sits in the sheet that refused it, above its buttons; any other
  // failure stands above the list.
  const inSheet = model.resolving !== null;
  return html`
    ${viewHeader("shortages", model.branchName, "The yard team flags a short pick; the yard manager settles it here.")}
    ${model.error !== "" && !inSheet ? html`<p class="error-line" data-test="shortages-error">${model.error}</p>` : nothing}
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
      ${withOpen.map((entry) => renderShortageTask(entry, model))}
    </section>
  `;
}

function renderShortageTask(entry: QueueEntry, model: ShortagesModel): TemplateResult {
  const open = entry.shortages.filter((s) => s.resolved_at === null);
  const done = entry.shortages.filter((s) => s.resolved_at !== null);
  const orderLabel = entry.order_no !== "" ? entry.order_no : entry.task.order_id;
  const status = taskStatusWord(entry.task.status);
  return html`
    <article class="task-card" data-task=${entry.task.id}>
      <div class="task-head">
        <p class="order">${orderLabel}</p>
        ${entry.customer_name !== "" ? html`<span class="meta">${entry.customer_name}</span>` : nothing}
        ${status !== "" ? html`<span class="meta">${status}</span>` : nothing}
        ${entry.task.stage_location !== "" ? html`<span class="meta">staged at ${entry.task.stage_location}</span>` : nothing}
        <span class="meta">${entry.task.open_shortages} open</span>
      </div>
      ${open.map((s) => renderShortageRow(entry, s, model))}
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

function renderShortageRow(entry: QueueEntry, s: QueueEntry["shortages"][number], model: ShortagesModel): TemplateResult {
  const draft = model.resolving;
  if (draft !== null && draft.shortageId === s.id) {
    return renderResolvePanel(draft, entry, s, model.error);
  }
  return html`
    <div class="shortage" data-shortage=${s.id}>
      <div class="body">
        <p class="line">${s.description}</p>
        <p class="detail">${shortageLine(s)}</p>
        ${s.note !== "" ? html`<p class="detail">${s.note}</p>` : nothing}
      </div>
      <div class="row-actions">
        <button type="button" class="btn btn-primary" data-action="open-resolve" data-task-id=${entry.task.id} data-shortage-id=${s.id}>Resolve</button>
      </div>
    </div>
  `;
}

function shortageLine(s: QueueEntry["shortages"][number]): string {
  const line = s.order_line_no === null ? "" : `line ${s.order_line_no}, `;
  return `${line}short ${qtyWords(s.qty_short)} ${s.uom}`;
}

/** The board's quantity line: ordered and picked only when the ticket's line says what was ordered. */
function quantityLine(entry: QueueEntry, s: QueueEntry["shortages"][number]): string {
  const line = s.order_line_no === null ? undefined : entry.lines.find((l) => l.line_no === s.order_line_no);
  const unit = s.uom;
  const short = `Short ${qtyWords(s.qty_short)} ${unit}`;
  if (line === undefined || line.qty_ordered === "") return short;
  const picked = pickedWords(line.qty_ordered, s.qty_short);
  if (picked === null) return short;
  return `Ordered ${qtyWords(line.qty_ordered)} ${unit} · Picked ${picked} ${unit} · ${short}`;
}

function renderResolvePanel(draft: ResolveDraft, entry: QueueEntry, s: QueueEntry["shortages"][number], error: string): TemplateResult {
  const orderLabel = entry.order_no !== "" ? entry.order_no : entry.task.order_id;
  return html`
    <div class="shortage resolve-panel" data-shortage=${s.id} data-resolving="true">
      <div class="body">
        <h2 class="panel-title">Resolve shortage</h2>
        <p class="panel-line">${orderLabel} · ${s.description}</p>
        <p class="panel-qty">${quantityLine(entry, s)}</p>
        <div class="reported">
          <p class="reported-who">Reported by the yard team</p>
          ${s.note !== "" ? html`<p class="reported-note">${s.note}</p>` : nothing}
        </div>
        <div class="choices">
          ${RESOLUTION_CHOICES.map((c) => html`
            <button type="button" class="choice" data-action="pick-resolution" data-choice=${c.key} aria-pressed=${draft.choice === c.key ? "true" : "false"}>
              <span class="mark" aria-hidden="true"></span>
              <span class="words">
                <span class="title">${c.label}</span>
                <span class="sub">${c.consequence}</span>
              </span>
            </button>
          `)}
        </div>
        <textarea class="text-field text-area" rows="2" data-field="resolution-note" placeholder="Note (optional)" aria-label="Note (optional)" ?disabled=${draft.busy}></textarea>
        ${error !== "" ? html`<p class="error-line" data-test="shortages-error">${error}</p>` : nothing}
        <div class="row-actions">
          <button type="button" class="btn btn-primary" data-action="confirm-resolve" ?disabled=${draft.busy || draft.choice === ""}>Resolve</button>
          <button type="button" class="btn btn-secondary" data-action="cancel-resolve" ?disabled=${draft.busy}>Cancel</button>
        </div>
        <p class="hm-note">The order's rep sees the short pick in House Messages.</p>
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
    ${viewHeader("receiving", model.branchName, "")}
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
                : differences.map((row) => renderReceivingCard(row))}
              ${rest > 0 ? html`<p class="count-line">${grouped(rest)} purchase order${rest === 1 ? "" : "s"} on the dock landed everything ordered.</p>` : nothing}
            </section>
          `}
  `;
}

function renderReceivingCard(row: ReceivingRow): TemplateResult {
  const expected = dateWords(row.expected_at);
  return html`
    <article class="list-card" data-po=${row.po_id}>
      <div class="task-head">
        <p class="order">${row.po_no}</p>
        <span class="meta">${row.vendor_name}</span>
        <span class="chip chip-watch">Partial</span>
      </div>
      <div class="facts">
        <p class="detail">${row.open_lines} line${row.open_lines === 1 ? "" : "s"} still short</p>
        ${row.value_open_cents !== null
          ? html`<p class="detail"><span class="amount">${money(row.value_open_cents)}</span> still open on this order</p>`
          : nothing}
        ${expected !== "" ? html`<p class="detail">expected ${expected}</p>` : nothing}
      </div>
    </article>
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
    ${viewHeader("approvals", "", "")}
    <div class="kind-tabs" role="tablist" aria-label="Approval kinds">
      ${APPROVAL_KINDS.map((k) => html`
        <button type="button" class="kind-tab" role="tab" data-action="pick-kind" data-kind=${k.kind} aria-selected=${model.kind === k.kind ? "true" : "false"}>
          ${k.word}
        </button>
      `)}
    </div>
    ${model.error !== "" ? html`<p class="error-line">${model.error}</p>` : nothing}
    ${model.refusal !== "" ? html`<p class="refusal" data-test="approvals-refusal">${model.refusal}</p>` : nothing}
    <div role="tabpanel" aria-label=${kindWord(model.kind)}>
      ${model.phase === "loading"
        ? html`<p class="notice-line">Loading ${kindWord(model.kind)} approvals…</p>`
        : model.phase === "error"
          ? html`<div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-view">Try again</button></div>`
          : model.phase === "refused"
            ? nothing
            : model.rows.length === 0
              ? html`<section class="sheet"><div class="list-card"><p class="notice-line">No ${kindWord(model.kind)} approvals waiting.</p></div></section>`
              : html`
                  <section class="sheet">
                    <div class="list-card">
                      ${model.rows.map((row) => renderApprovalRow(row, model.busyId))}
                    </div>
                  </section>
                `}
    </div>
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

export interface EntryView {
  readonly flows: { kind: "loading" } | { kind: "unavailable"; status: number | null } | { kind: "ready"; flows: DemoFlow[] };
  /** True while a mint is in flight: the cards go disabled and grey. */
  readonly busy: boolean;
  /** A refusal in words, shown beside the cards. */
  readonly message: string | null;
}

/** The marks and one line each, by demo flow id: what this app opens for the role, never the till's words. */
const FLOW_ICONS: Readonly<Record<string, string>> = {
  sales: "lucide:receipt",
  "sales-manager": "lucide:badge-check",
  "yard-manager": "lucide:warehouse",
};
const FLOW_BLURBS: Readonly<Record<string, string>> = {
  "sales-manager": "What needs you today, and the approvals you decide.",
  "yard-manager": "The yard: shortages, receiving and what needs you today.",
};

/** The way in: this app's own door, one card per demo role, the home each role opens. */
export function renderEntry(view: EntryView): TemplateResult {
  if (view.flows.kind === "loading") {
    return html`<div class="stage"><p class="notice-line" role="status">Loading the demo roles.</p></div>`;
  }
  if (view.flows.kind === "unavailable") {
    const words =
      view.flows.status === 404
        ? "This deployment does not serve the demo sign-in, so there are no role cards to tap."
        : view.flows.status === null
          ? "The demo roles could not be loaded: the call did not go through. Reload to try again."
          : "The demo roles could not be loaded. Reload to try again.";
    return html`<div class="stage"><p class="notice-line" role="status">${words}</p></div>`;
  }
  if (view.flows.flows.length === 0) {
    return html`<div class="stage"><p class="notice-line" role="status">The demo sign-in answered with no roles: there is nothing to tap.</p></div>`;
  }
  return html`
    <div class="stage">
      <h1 class="headline">Who is working today?</h1>
      <p class="intro">Tap a role to open their home:</p>
      <div class="role-cards">
        ${view.flows.flows.map((flow) => html`
          <button
            type="button"
            class="role-card"
            data-action="choose"
            data-flow=${flow.flow ?? nothing}
            aria-label=${flow.label ?? flow.flow ?? nothing}
            ?disabled=${view.busy}
          >
            <span class="mark">${drawIcon(iconForId(FLOW_ICONS[flow.flow ?? ""] ?? "lucide:circle-user"), 29)}</span>
            <span class="words">
              <span class="role">${flow.label ?? flow.flow}</span>
              ${FLOW_BLURBS[flow.flow ?? ""] === undefined ? nothing : html`<span class="what">${FLOW_BLURBS[flow.flow ?? ""]}</span>`}
            </span>
          </button>
        `)}
      </div>
      ${view.message === null ? nothing : html`<p class="notice-line" role="status">${view.message}</p>`}
    </div>
  `;
}
