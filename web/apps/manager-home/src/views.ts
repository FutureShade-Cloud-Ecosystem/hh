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
import { offlineLine, signInLine } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { widgetLink } from "./landing-links.js";
import { roleWords } from "./role-words.js";
import { gaugeSegments } from "./widget-shapes.js";
import type {
  ApprovalRow,
  LandingDeliveries,
  LandingKpi,
  LandingNeedRow,
  NeedsQueue,
  NeedsRow,
  QueueEntry,
  ReceivingRow,
  WidgetDatum,
  WidgetView,
} from "./manager-api.js";
import { LANDING_NOT_CONNECTED } from "./manager-api.js";
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
  shortageCard,
  cardsToShow,
  headCountWords,
  rangeWord,
  severityWord,
  shareNote,
  stateNote,
  taskStatusWord,
  welcomeLine,
} from "./manager-logic.js";

// -- the landing -------------------------------------------------------------------------------------

/** Whether the four TODAY KPIs render as one combined number each, or stacked per branch. */
export type BranchMode = "combined" | "split";

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
  /** WALK-C10: combined vs side-by-side branch mode on the four money KPIs and deliveries summary. */
  readonly branchMode?: BranchMode;
  /** WALK-C10: the four money KPI cards (Sales today, Gross margin, AR overdue, Cash collected today). */
  readonly kpis?: readonly LandingKpi[];
  /** WALK-C10: the Deliveries today card with branch run columns. */
  readonly deliveries?: LandingDeliveries | null;
  /** WALK-C10: Needs you, by money at stake ranked rows with inline actions. */
  readonly stakeNeeds?: readonly LandingNeedRow[];
  /** WALK-C10: landing-level notice for actions whose destination view is not on this box yet. */
  readonly notice?: string;
}

const NOT_HERE = "Not available yet";
// The tile foot under a REAL figure: the figure is on the card, what the tile
// has none of is the trend the original's stat tiles carried, so the words
// name the trend and never the figure's availability (FROM-PRINCIPAL 158,
// round 6 P2-1). A needs row's not here span keeps NOT_HERE: there the missing
// thing is the row's own action, which is what the words deny.
const NO_TREND = "No trend yet";

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
  const branchMode: BranchMode = model.branchMode ?? "combined";
  const kpis = model.kpis ?? [];
  const deliveries = model.deliveries ?? null;
  const stakeNeeds = model.stakeNeeds ?? [];
  const notice = model.notice ?? "";
  return html`
    <header class="page-head">
      <h1>${welcomeLine(model.roleLabel)}</h1>
      <p class="page-sub">${words.sub}</p>
      <div class="head-actions">
        <button type="button" class="btn btn-secondary edit-open" data-action="open-view" data-view="builder">Arrange home</button>
      </div>
    </header>
    ${notice !== "" ? html`<p class="notice-line landing-notice" role="status">${notice}</p>` : nothing}
    <p class="eyebrow">Today</p>
    <div class="bento">
      ${large.map((w, index) => renderWidget(w, model.data.get(w.widget_key), model.needs, model.unsourcedNeeds, index === 0, model.profile))}
    </div>
    <div class="strip">
      ${strip.map((w) => renderWidget(w, model.data.get(w.widget_key), model.needs, model.unsourcedNeeds, false, model.profile))}
      ${UNSOURCED_TILES.map((t) => renderUnsourcedTile(t))}
    </div>
    ${renderC10Landing(branchMode, kpis, deliveries, stakeNeeds)}
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

export function renderC10Landing(
  branchMode: BranchMode,
  kpis: readonly LandingKpi[],
  deliveries: LandingDeliveries | null,
  stakeNeeds: readonly LandingNeedRow[],
): TemplateResult | typeof nothing {
  if (kpis.length === 0 && deliveries === null && stakeNeeds.length === 0) return nothing;
  // P2-2 (FROM-PRINCIPAL 158 round 6): the strip mixes a today figure, an all time rate and a
  // standing balance, so the heading says what the strip is, and the page keeps one Today heading
  // (the bento's, P2-3). Each card's own words carry its scope.
  return html`
    <div class="c10-landing" data-test="c10-landing">
      <section class="quick-actions" data-test="c10-quick-actions" aria-label="Quick actions">
        <h2 class="section-eyebrow">Quick actions</h2>
        <div class="quick-actions-row">
          <button type="button" class="qa-pill primary" data-action="qa-action" data-qa="approve-credit-hold">Approve credit hold</button>
          <button type="button" class="qa-pill" data-action="qa-action" data-qa="issue-warranty">Issue warranty claim</button>
          <button type="button" class="qa-pill" data-action="qa-action" data-qa="approve-margin">Approve margin</button>
          <button type="button" class="qa-pill" data-action="qa-action" data-qa="message-branch">Message a branch</button>
        </div>
      </section>
      <section class="today-section" data-test="c10-today-kpis" aria-label="Branch figures at a glance">
        <div class="today-head">
          <h2 class="section-eyebrow">At a glance</h2>
          <div class="branch-toggle" role="group" aria-label="Applies to">
            <button type="button" class="toggle-pill" data-action="toggle-branch" data-mode="combined" aria-pressed=${branchMode === "combined" ? "true" : "false"}>All branches together</button>
            <button type="button" class="toggle-pill" data-action="toggle-branch" data-mode="split" aria-pressed=${branchMode === "split" ? "true" : "false"}>Branch beside branch</button>
          </div>
        </div>
        <div class="kpi-row kpi-row-${branchMode}">
          ${kpis.map((kpi) => renderKpiCard(kpi, branchMode))}
        </div>
      </section>
      ${deliveries === null ? nothing : renderDeliveriesCard(deliveries, branchMode)}
      ${stakeNeeds.length === 0 ? nothing : renderNeedsYouCard(stakeNeeds)}
    </div>
  `;
}

export function renderKpiCard(kpi: LandingKpi, mode: BranchMode): TemplateResult {
  if (!kpi.connected) {
    return html`
      <article class="kpi-card kpi-card-combined" data-test="kpi-not-connected">
        <h3 class="kpi-title">${kpi.title}</h3>
        <p class="kpi-compare">${LANDING_NOT_CONNECTED}</p>
      </article>
    `;
  }
  if (mode === "split") {
    const rows = kpi.branches.length > 0 ? kpi.branches : [{ label: "All branches", value: kpi.value }];
    return html`
      <article class="kpi-card kpi-card-split">
        <h3 class="kpi-title">${kpi.title}</h3>
        ${rows.map((branch) => html`
          <div class="kpi-branch">
            <span class="kpi-branch-label">${branch.label}</span>
            <span class="kpi-branch-value ${kpi.kind}">${branch.value}</span>
          </div>
        `)}
      </article>
    `;
  }
  return html`
    <article class="kpi-card kpi-card-combined">
      <h3 class="kpi-title">${kpi.title}</h3>
      <span class="kpi-value ${kpi.kind}">${kpi.value}</span>
      <p class="kpi-compare">${kpi.note}</p>
    </article>
  `;
}

// FROM-PRINCIPAL 127: this card and the hero's deliveries.today tile measure different things
// (the whole book of the day's stops off the dispatch board, against the stops still to do), so
// they never share one heading. The card says Stops today; the hero tile keeps its own words.
export function renderDeliveriesCard(d: LandingDeliveries, mode: BranchMode): TemplateResult {
  void mode;
  return html`
    <section class="deliveries-card" data-test="c10-deliveries-today" aria-label="Stops today">
      <div class="deliveries-head">
        <h2 class="section-eyebrow">Stops today</h2>
        <span class="deliveries-count">${d.count}</span>
      </div>
      <p class="deliveries-summary">${d.summary}</p>
      <div class="runs-row">
        ${d.runs.map((run) => html`
          <div class="run">
            <span class="run-id">${run.runId} ${run.branch}</span>
            <span class="run-when">${run.when}</span>
            <span class="run-stops">${run.stops}</span>
          </div>
        `)}
      </div>
    </section>
  `;
}

export function renderNeedsYouCard(rows: readonly LandingNeedRow[]): TemplateResult {
  return html`
    <section class="needs-stake-card" data-test="c10-needs-stake" aria-label="Needs you, by money at stake">
      <h2 class="section-eyebrow">Needs you, by money at stake</h2>
      <ol class="needs-stake-list">
        ${rows.map((row) => renderNeedStakeRow(row))}
      </ol>
    </section>
  `;
}

export function renderNeedStakeRow(row: LandingNeedRow): TemplateResult {
  const accentClass = `accent-${row.accent}`;
  return html`
    <li class="need-row ${accentClass}">
      <div class="need-body">
        <p class="need-title">${row.title}</p>
        <p class="need-words">${row.body}</p>
      </div>
      <span class="need-amount">${row.amountLabel}</span>
      <button type="button" class="need-action" data-action="need-action" data-need=${row.id} data-target-view=${row.targetView} data-target-ref=${row.targetRef}>${row.actionLabel}</button>
    </li>
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
  profile: string,
): TemplateResult {
  if (w.measure === "needs.you") {
    return placed(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${needs === null ? html`<p class="needs-empty">The queue did not load.</p>` : renderNeedsList(needs, unsourced, profile)}
      </article>
    `);
  }
  const figure = figureFor(datum, w.kind);
  const caption = rangeWord(datum?.range !== "" && datum !== undefined ? datum.range : w.range);
  const hero = first && w.shape === "stat" && w.span_cols >= 6;
  const foot = renderFoot(w, profile);
  if (w.shape === "board" || w.shape === "bars") {
    return placed(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        ${renderBars(datum, w, w.shape === "bars")}
        ${foot}
      </article>
    `);
  }
  // A trend stays its figure: the core measures it over the metric's first dimension, which is the
  // branch for the sales measures, so its points are a comparison and no time series to draw.
  if (w.shape === "gauge" && figure !== null && datum !== undefined) {
    return placed(w, html`
      <article class="card" data-widget=${w.widget_key}>
        <h2 class="card-title">${w.title}</h2>
        <p class="figure small">${figure}</p>
        <p class="card-sub">${caption}</p>
        ${renderGauge(datum, w)}
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
 * missing trend's words only where no app exists. A link never carries a figure of its own.
 */
function renderFoot(w: WidgetView, profile: string): TemplateResult {
  const link = widgetLink(w, profile);
  if (link.kind === "app") {
    return html`<p class="tile-foot"><button type="button" class="foot-link" data-action="open-app" data-href=${link.href}>Open ${link.app}<span class="chev" aria-hidden="true">›</span></button></p>`;
  }
  if (link.kind === "view") {
    return html`<p class="tile-foot"><button type="button" class="foot-link" data-action="open-view" data-view=${link.view}>Open ${VIEW_TITLE[link.view]}<span class="chev" aria-hidden="true">›</span></button></p>`;
  }
  return html`<p class="tile-foot">${NO_TREND}</p>`;
}

/**
 * The gauge: the points' shares of their own sum as one segmented meter, with the tone the server
 * named, and a legend that carries every exact figure as text. The core measures this as a count
 * with no ceiling, so the meter shows composition, never "how full": no invented scale.
 */
function renderGauge(datum: WidgetDatum, w: WidgetView): TemplateResult | typeof nothing {
  const segments = gaugeSegments(datum.points, w.kind);
  if (segments === null) return nothing;
  const spoken = `${w.title}: ${segments.map((g) => `${g.label} ${g.figure}`).join(", ")}`;
  return html`
    <div class="meter" role="img" aria-label=${spoken}>
      ${segments.map((g) => html`<span class="seg ${g.widthClass} ${g.toneClass}"></span>`)}
    </div>
    <ul class="legend">
      ${segments.map((g) => html`<li class="legend-row"><span class="swatch ${g.toneClass}" aria-hidden="true"></span><span class="label">${g.label}</span><span class="value">${g.figure}</span></li>`)}
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

export function renderNeedsList(needs: NeedsQueue, unsourced: typeof UNSOURCED_NEEDS, profile?: string): TemplateResult {
  const rows = needs.rows;
  // The queue link opens the first view a row names; with none on this box it is not drawn.
  const first = rows.map((r) => needsRowTarget(r, profile)).find((t) => t.view !== null);
  return html`
    ${rows.length === 0 ? html`<p class="needs-empty">Nothing is waiting on you right now.</p>` : rows.map((row) => renderNeedsRow(row, profile))}
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

function renderNeedsRow(row: NeedsRow, profile?: string): TemplateResult {
  const target = needsRowTarget(row, profile);
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
  /** Picks the manager settled this visit: their cards stay, done rows and all, until the sheet is next opened. */
  readonly kept: readonly string[];
  readonly error: string;
  readonly notice: string;
  readonly resolverWords: string;
  /** The instant the list was read; the cards' "ago" words count from it. */
  readonly nowMs: number;
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
  const withOpen = cardsToShow(model.entries, model.kept);
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
  const orderLabel = entry.order_no !== "" ? entry.order_no : entry.task.order_id;
  const status = taskStatusWord(entry.task.status);
  return html`
    <article class="task-card" data-task=${entry.task.id}>
      <div class="task-head">
        <p class="order">${orderLabel}</p>
        ${entry.customer_name !== "" ? html`<span class="meta">${entry.customer_name}</span>` : nothing}
        ${status !== "" ? html`<span class="meta">${status}</span>` : nothing}
        ${entry.task.stage_location !== "" ? html`<span class="meta">staged at ${entry.task.stage_location}</span>` : nothing}
        <span class="meta">${headCountWords(open.length)}</span>
      </div>
      ${entry.shortages.map((s) => s.resolved_at === null ? renderShortageRow(entry, s, model) : renderDoneRow(s, model))}
    </article>
  `;
}

/** A settled shortage, calmer than an open row: the status word, what it was, and the resolution's words. */
function renderDoneRow(s: QueueEntry["shortages"][number], model: ShortagesModel): TemplateResult {
  return html`
    <div class="shortage" data-shortage=${s.id}>
      <div class="body">
        <p class="status status-done">${shortageCard(s, model.nowMs).status}</p>
        <p class="line">${s.description}</p>
        <p class="detail">${shortageLine(s)}</p>
        <p class="resolved-word">${s.resolution}</p>
      </div>
    </div>
  `;
}

function renderShortageRow(entry: QueueEntry, s: QueueEntry["shortages"][number], model: ShortagesModel): TemplateResult {
  const draft = model.resolving;
  if (draft !== null && draft.shortageId === s.id) {
    return renderResolvePanel(draft, entry, s, model.error);
  }
  const card = shortageCard(s, model.nowMs);
  return html`
    <div class="shortage" data-shortage=${s.id} data-card="shortage-review">
      <div class="body">
        <p class="status status-open">${card.status}</p>
        <p class="line">${card.what}</p>
        <p class="detail">${card.howMuch}</p>
        <p class="who-when">${card.when === "" ? card.reporter : `${card.reporter} · ${card.when}`}</p>
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
          <p class="reported-who">Reported by staff</p>
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

export { renderApprovals, type ApprovalsModel } from "./approvals-views.js";

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
  readonly flows: { kind: "loading" } | { kind: "unavailable"; status: number | null } | { kind: "signin" } | { kind: "offline"; reason: "offline" | "unavailable" | "rate_limited" } | { kind: "ready"; flows: DemoFlow[] };
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
  // The line doors carry the app's name as their title, the way the shared
  // door does (UX-143); the stage sits on the page's own left gutter.
  if (view.flows.kind === "loading") {
    return html`<div class="stage"><h1 class="headline">Manager Home</h1><p class="notice-line" role="status">Loading the demo roles.</p></div>`;
  }
  if (view.flows.kind === "signin") {
    return html`<div class="stage"><h1 class="headline">Manager Home</h1>${signInLine("Manager Home", "notice-line")}</div>`;
  }
  if (view.flows.kind === "offline") {
    return html`<div class="stage"><h1 class="headline">Manager Home</h1>${offlineLine("Manager Home", view.flows.reason, "notice-line")}</div>`;
  }
  if (view.flows.kind === "unavailable") {
    const words =
      view.flows.status === 404
        ? "This deployment does not serve the demo sign-in, so there are no role cards to tap."
        : view.flows.status === null
          ? "The demo roles could not be loaded: the call did not go through. Reload to try again."
          : "The demo roles could not be loaded. Reload to try again.";
    return html`<div class="stage"><h1 class="headline">Manager Home</h1><p class="notice-line" role="status">${words}</p></div>`;
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
