// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-builder.ts;
// adapted for HH.
//
// The Quote Desk's shell under the split-list navigation (the CP1 feedback,
// decision log row 941): two screens, one element. The list page is the
// desk's landing page (the title row with the primary New quote button, the
// toolbar with the search and the status tabs, the rows that open a quote);
// the quote page is the work area alone (the stepper, and the Quote detail
// and admin disclosure under it). The address bar carries the person between
// them (#/quotes, #/quotes/new, #/quotes/<id>): the host wires the parsed
// route down, and this shell writes a new hash when a control walks.
//
// The work area and its panels stay mounted on the list page, hidden: a
// panel taken off the page loses the late answers of writes still out for
// the quote it left (the leaving-through-New-Quote rule), so the split is a
// change of view, never a teardown. The work area keeps the quote it held
// while the person walks the list, and coming back to the same quote's route
// finds it as it was left.
//
// The Sales manager's tabs also carry Approvals: the approvals panel stands
// in the list page's content place (one instance, the same panel the fan-out
// reaches), and the quote page's disclosure carries an Approvals way in for
// the requester. Sales sees neither tab; the walls the server owns are
// unchanged.
//
// The binding discipline is the app's own: the templates carry no property or
// event bindings; the host delegates by data-action and data-field, and the
// work area and the panels receive their facts through the wiring pass.
import { LitElement, css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import "./quote-workarea.js";
import "./panel-seams.js";
import "./edit-panel.js";
import "./history-panel.js";
import "./settings-panel.js";
import "./assist-panel.js";
import "./followups-panel.js";
import "./hold-expiry-panel.js";
import "./availability-panel.js";
import "./guard-panel.js";
import "./approvals-panel.js";
import "./floor-refusal-dialog.js";
import {
  SEAM_APPROVALS,
  SEAM_ASSIST,
  SEAM_AVAILABILITY,
  SEAM_EDIT_PANEL,
  SEAM_FOLLOWUPS,
  SEAM_HISTORY,
  SEAM_HOLD_EXPIRY,
  SEAM_SETTINGS,
} from "./panel-seams.js";
import { money } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import { baseStyles, button, chip, segmented, textInput, type ChipTone } from "@hardscapehouse/design-system/src/primitives.js";
import { icon } from "@hardscapehouse/design-system/src/state-icons.js";
import {
  expiryLabel,
  quoteErrorMessage,
  quoteStatusWord,
} from "./_quote-edit.js";
import { spokenRefusal } from "./_quote-workarea.js";
import { OPEN_SETTINGS_EVENT } from "./panel-seams.js";
import { FLOOR_REFUSAL_DROPPED_EVENT } from "./floor-refusal.js";
import {
  COMPOSE_ROUTE,
  LIST_ROUTE,
  quoteRoute,
  routeHash,
  routeIsQuotePage,
  type DeskRoute,
} from "./quote-router.js";
import type { QdClient, QuoteRow } from "./qd-client.js";

interface BranchRow {
  id: string;
  code: string;
  name: string;
  isDefault: boolean;
}

/** The most pages one refresh of the list reads: a runaway cursor ends here. */
const MAX_LIST_PAGES = 40;

/** The status tabs' own options, the closed set the segmented control renders. */
const STATUS_TABS = [
  { label: "All", value: "" },
  { label: "Draft", value: "DRAFT" },
  { label: "Sent", value: "SENT" },
  { label: "Accepted", value: "ACCEPTED" },
  { label: "Expired", value: "EXPIRED" },
  { label: "Converted", value: "CONVERTED" },
  { label: "Lost", value: "LOST" },
];

/** The approvals tab's own value: a page tab beside the statuses, not a status itself. */
const APPROVALS_TAB = { label: "Approvals", value: "APPROVALS" };

/** The chip tone a status reads as (the state colours, never colour alone: the word rides the chip). */
function statusTone(status: string): ChipTone {
  if (status === "ACCEPTED" || status === "CONVERTED") return "good";
  if (status === "SENT") return "watch";
  if (status === "EXPIRED") return "stop";
  return "neutral";
}

@customElement("qd-quote-builder")
export class QdQuoteBuilder extends LitElement {
  /** The host wires the validated client before the first render. */
  @property({ attribute: false }) client: QdClient | null = null;

  /** The desk's route, parsed from the address bar by the host and wired down (never a template binding). */
  @property({ attribute: false }) route: DeskRoute = LIST_ROUTE;

  /** The signed in person's role code, wired by the host: the Sales manager's tabs carry Approvals. */
  @property({ attribute: false }) roleCode = "";

  @state() private rows: QuoteRow[] = [];
  /** The next page of the list, or "" when the server sent no more. */
  @state() private nextCursor = "";
  private listSeq = 0;
  @state() private error = "";
  @state() private busy = false;
  @state() private statusFilter = "";
  /** The toolbar's search, by number or customer. The list read takes no search word, so a search reads on through the pages (searchAllPages) and filters what is loaded. */
  @state() private search = "";
  /** What the list says about a refusal that answered for a quote the person had left (the dialog was not opened for it). */
  @state() private listNotice = "";
  /** True while a search is reading the later pages: the empty answer waits for it. */
  @state() private searching = false;
  @state() private branches: BranchRow[] = [];
  /** The branch this login reaches (the person's own branch): the work area's fallback branch for a new quote. */
  @state() private myBranch = "";
  /** The manager's Approvals tab pressed: the approvals panel stands in the list page's content place. */
  @state() private approvalsOpen = false;
  /**
   * The quote page last shown. The work area keeps that quote loaded (hidden,
   * its panels with it) while the person walks the list, so walking back to
   * the same quote's route finds it as it was left and a write still out for
   * it is not cut in two.
   */
  private lastQuoteId = "";

  // The design system's shared sheet comes first: the toolbar's controls (the
  // search box, the status tabs) and the buttons take their faces from it,
  // and this element's own rules below win where they speak.
  static styles = [
    baseStyles,
    css`
    :host {
      display: block;
      padding: 14px;
      font-family: var(--hos-font-ui, var(--hh-font-ui));
      color: var(--hos-text, var(--hh-text));
    }
    /* The two screens: whichever the route names is the one that shows. */
    .desksplit[data-view="list"] .quotepage,
    .desksplit[data-view="quote"] .listpage {
      display: none;
    }
    .titlerow {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      flex-wrap: wrap;
      margin-bottom: 10px;
    }
    h1 {
      margin: 0;
      font-size: 1.2rem;
    }
    .toolbar {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      flex-wrap: wrap;
      padding: 10px 12px;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 8px;
      background: var(--hos-slate-warm, var(--hh-card));
      margin-bottom: 12px;
    }
    .toolbar .tools {
      display: flex;
      align-items: center;
      gap: 10px;
      flex-wrap: wrap;
    }
    ul {
      list-style: none;
      margin: 0;
      padding: 0;
      display: flex;
      flex-direction: column;
      gap: 6px;
      max-height: 70vh;
      overflow-y: auto;
    }
    /* The row: one wide line of six (number, customer, total, status,
       salesperson, validity), the ERP's own reading order. */
    .rowline {
      display: grid;
      grid-template-columns: minmax(88px, auto) 1fr auto auto minmax(88px, auto) minmax(120px, auto);
      gap: 4px 14px;
      align-items: center;
      width: 100%;
      text-align: left;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 6px;
      padding: 8px 12px;
      cursor: pointer;
      font-size: 0.83rem;
      background: var(--hos-slate-warm, var(--hh-card));
      color: var(--hos-text, var(--hh-text));
    }
    .rowline[aria-current="true"] {
      border-color: var(--hos-stone-amber, var(--hh-accent));
      background: var(--hos-line, var(--hh-line));
    }
    .rowline .c-no {
      font-weight: 600;
      white-space: nowrap;
    }
    .rowline .c-cust {
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .rowline .c-total {
      font-variant-numeric: tabular-nums;
      white-space: nowrap;
    }
    .rowline .c-owner,
    .rowline .c-until {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 0.76rem;
      white-space: nowrap;
    }
    /* The tablet card (the till's own polish: the card face at the card
       radius, the facts stacked in two tracks). */
    @media (max-width: 900px) {
      .rowline {
        grid-template-columns: 1fr 1fr;
        gap: 6px 12px;
        padding: 12px 14px;
        border-radius: var(--hh-radius-card, 16px);
        background: var(--hh-card, var(--hos-slate-warm));
      }
      .rowline .c-cust {
        grid-column: 1 / -1;
        white-space: normal;
      }
      .rowline .c-until {
        grid-column: 1 / -1;
      }
    }
    button:focus-visible,
    input:focus-visible {
      outline: 2px solid var(--hos-stone-amber-text, var(--hh-accent-text));
      outline-offset: 2px;
    }
    .box {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 8px;
      padding: 12px 14px;
      background: var(--hos-slate-warm, var(--hh-card));
    }
    h3 {
      margin: 0 0 10px;
      font-size: 0.95rem;
    }
    .stack {
      display: flex;
      flex-direction: column;
      gap: 12px;
    }
    .stack[data-compose="true"] {
      display: none;
    }
    .row {
      display: flex;
      gap: 8px;
      align-items: flex-end;
      flex-wrap: wrap;
    }
    .err {
      color: var(--hos-safety-red, var(--hh-negative));
      font-size: 0.82rem;
    }
    .empty {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: 0.83rem;
    }
    /* The approvals view stands in the list page's content place; the rows
       stand down while it is the page's content. */
    .approvalsview[data-open="false"] {
      display: none;
    }
    .listmain[data-open="false"] {
      display: none;
    }
    /* The detail disclosure's seam rows: nothing shows today, so the row
       keeps its own height at zero until a panel lands in the seam. */
    .seams qd-panel-seam {
      display: block;
    }
  `,
  ];

  override connectedCallback(): void {
    super.connectedCallback();
    this.startLoads();
    this.renderRoot.addEventListener("click", this.onActionClick as EventListener);
    this.renderRoot.addEventListener("input", this.onFieldInput as EventListener);
    this.renderRoot.addEventListener("quote-changed", this.onQuoteChanged as EventListener);
    this.renderRoot.addEventListener("quote-open", this.onQuoteOpen as EventListener);
    this.renderRoot.addEventListener("all-quotes", this.onAllQuotes as EventListener);
    this.renderRoot.addEventListener("quote-read", this.onQuoteRead as EventListener);
    this.renderRoot.addEventListener(FLOOR_REFUSAL_DROPPED_EVENT, this.onRefusalDropped as EventListener);
    window.addEventListener(OPEN_SETTINGS_EVENT, this.onOpenSettings);
  }

  protected override willUpdate(changed: Map<string, unknown>): void {
    // The host wires the client after this element mounts; the loads start
    // the moment it arrives (the entry page's cards never reach here).
    if (changed.has("client") && this.client !== null) this.startLoads();
    // A quote page takes the approvals view with it: coming back to the list
    // shows the rows, not the view the person left behind.
    if (changed.has("route") && routeIsQuotePage(this.route)) {
      this.approvalsOpen = false;
      this.listNotice = "";
    }
  }

  private startLoads(): void {
    if (this.client === null) return;
    void this.loadBranches();
    void this.loadList();
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    this.renderRoot.removeEventListener("click", this.onActionClick as EventListener);
    this.renderRoot.removeEventListener("input", this.onFieldInput as EventListener);
    this.renderRoot.removeEventListener("quote-changed", this.onQuoteChanged as EventListener);
    this.renderRoot.removeEventListener("quote-open", this.onQuoteOpen as EventListener);
    this.renderRoot.removeEventListener("all-quotes", this.onAllQuotes as EventListener);
    this.renderRoot.removeEventListener("quote-read", this.onQuoteRead as EventListener);
    this.renderRoot.removeEventListener(FLOOR_REFUSAL_DROPPED_EVENT, this.onRefusalDropped as EventListener);
    window.removeEventListener(OPEN_SETTINGS_EVENT, this.onOpenSettings);
  }

  protected override updated(changed: Map<string, unknown>): void {
    // The wiring pass: the work area, the picker's own panels and the
    // approvals panel receive their facts. The work area's quote follows the
    // route on a quote page; on the list page it keeps the quote it held
    // (the split is a change of view, never a teardown of the work in it).
    const route = this.route;
    // The page's own title, and (on a change of route, never on the first draw) the page's heading takes focus.
    document.title = this.pageTitle();
    if (changed.has("route") && changed.get("route") !== undefined) void this.focusHeading();
    // The search box's words are the wiring pass's job like its value (the control's face carries no placeholder).
    const searchBox = this.renderRoot.querySelector<HTMLInputElement>('[data-field="list-search"]');
    if (searchBox !== null) searchBox.placeholder = "Search by number or customer";
    if (route.kind === "quote") this.lastQuoteId = route.id;
    const openQuoteId = route.kind === "quote" ? route.id : route.kind === "compose" ? "" : this.lastQuoteId;
    const branchId = this.selectedBranch() || this.myBranch;
    const area = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-quote-workarea");
    if (area !== null) {
      area.client = this.client;
      area.quoteId = openQuoteId;
      area.branchId = branchId;
      area.branchName = this.branchDisplayName();
      // The address bar is the person's place now: the work area's own
      // resume of a remembered quote stays heard only where a quote page
      // already names one (which needs no resume); a boot at the list or the
      // compose page is the person's own ask.
      area.resumeOpenRef = route.kind === "quote";
    }
    for (const seam of this.renderRoot.querySelectorAll<HTMLElement & Record<string, unknown>>(
      "qd-panel-seam, qd-quote-edit, qd-quote-assist, qd-quote-history, qd-quote-followups, qd-quote-hold-expiry, qd-quote-availability, qd-quote-guard, qd-approvals, qd-quote-settings, qd-floor-refusal",
    )) {
      seam.quoteId = openQuoteId;
      seam.branchId = branchId;
      if (seam.tagName !== "QD-PANEL-SEAM") seam.client = this.client;
    }
    // The floor refusal dialog opens only on a quote page: the hidden work area's late answers open nothing.
    const refusal = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-floor-refusal");
    if (refusal !== null) refusal.onQuotePage = routeIsQuotePage(route);
  }

  // ── loading ────────────────────────────────────────────────────────────────

  /**
   * The list from its first page. After an edit the pages already shown are read again, so a quote opened from
   * page two stays in the list; a fresh list (a new filter, Refresh) starts from page one.
   */
  private async loadList(keepPages = false): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.error = "";
    const seq = ++this.listSeq;
    const shown = keepPages ? this.rows.length : 0;
    const result = await client.listQuotes(this.statusFilter);
    let rows = result.ok ? result.value.rows : [];
    let cursor = result.ok ? (result.value.nextCursor ?? "") : "";
    const seen = new Set<string>();
    for (let page = 0; result.ok && page < MAX_LIST_PAGES && rows.length < shown && cursor !== "" && !seen.has(cursor); page += 1) {
      seen.add(cursor);
      const more = await client.listQuotes(this.statusFilter, cursor);
      if (!more.ok) break;
      const known = new Set(rows.map((r) => r.id));
      rows = [...rows, ...more.value.rows.filter((r) => !known.has(r.id))];
      cursor = more.value.nextCursor ?? "";
    }
    // A newer read of the list began while this one was out (or while it followed the pages): its answer is the one to show.
    if (seq !== this.listSeq) return;
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "list quotes"));
      return;
    }
    this.rows = rows;
    this.nextCursor = cursor;
    void this.searchAllPages();
  }

  /**
   * The list read takes no search word, so a search finds a quote on any page by reading on: while the box holds a word
   * and the server has a next page, the pages are read and added under the rows (the filter then shows what matches).
   * Another list read that begins meanwhile takes over (listSeq) and restarts this when it lands, so no page of an
   * older list is ever added to a newer one.
   */
  private async searchAllPages(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    // The newest call is the one that reads on: a call that finds a newer one gone ahead of it stops.
    const gen = ++this.searchGen;
    this.searching = this.search.trim() !== "" && this.nextCursor !== "";
    try {
      for (let page = 0; page < MAX_LIST_PAGES && this.search.trim() !== "" && this.nextCursor !== ""; page += 1) {
        const seq = ++this.listSeq;
        const result = await client.listQuotes(this.statusFilter, this.nextCursor);
        if (seq !== this.listSeq || gen !== this.searchGen) return;
        if (!result.ok) {
          this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "search the quotes"));
          return;
        }
        const known = new Set(this.rows.map((r) => r.id));
        this.rows = [...this.rows, ...result.value.rows.filter((r) => !known.has(r.id))];
        this.nextCursor = result.value.nextCursor ?? "";
      }
    } finally {
      if (gen === this.searchGen) this.searching = false;
    }
  }

  /** The newest search read's number. */
  private searchGen = 0;

  /** The next page of the same list is added under the rows already shown. */
  private async loadMore(): Promise<void> {
    const client = this.client;
    if (client === null || this.nextCursor === "" || this.busy) return;
    this.error = "";
    const seq = ++this.listSeq;
    const cursor = this.nextCursor;
    this.busy = true;
    const result = await client.listQuotes(this.statusFilter, cursor);
    this.busy = false;
    if (seq !== this.listSeq) return;
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "list more quotes"));
      return;
    }
    const known = new Set(this.rows.map((r) => r.id));
    this.rows = [...this.rows, ...result.value.rows.filter((r) => !known.has(r.id))];
    this.nextCursor = result.value.nextCursor ?? "";
    void this.searchAllPages();
  }

  private async loadBranches(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const result = await client.resolveBranch();
    if (!result.ok) return; // the work area falls back to no branch; the create still validates
    this.branches = [{ id: result.value.id, code: result.value.code, name: result.value.name, isDefault: true }];
    if (this.myBranch === "") this.myBranch = result.value.id;
  }

  // ── the fan-out, the open events and the walks ─────────────────────────────

  /**
   * An edit anywhere reaches the screens that show it. The announcement names the quote it belongs to by its own id
   * (a late answer names the quote the write was made on, not the one that happens to be open), so the list re-reads
   * whichever row changed, the quote bound views re-read only when the change is the quote they are showing, and the
   * panels no single quote owns (the settings and the approvals) re-read on any edit as before. An announcement that
   * names no quote at all re-reads everything: it is the old fan-out, and nothing may be lost by the id's absence.
   */
  private readonly onQuoteChanged = (event: CustomEvent<{ quoteId?: string }>): void => {
    const named = event.detail?.quoteId;
    if (named === undefined || named === this.openQuoteId()) this.reloadDetailPanels();
    else this.reloadSessionPanels();
    void this.loadList(true);
  };

  private readonly onQuoteOpen = (event: CustomEvent<{ quoteId: string }>): void => {
    // The work area's intents walk the routes: a create hands its quote
    // down, New Quote asks for the compose page. On the list page the work
    // area is not the person's place, and its once per mount resume of a
    // remembered quote stays unheard: the address bar names the person's
    // place now.
    if (this.route.kind === "list") {
      // A create that answered while the person was already on the list still made a draft: the list reads it in.
      void this.loadList(true);
      return;
    }
    this.createWalk = event.detail.quoteId !== "";
    this.walk(event.detail.quoteId === "" ? COMPOSE_ROUTE : quoteRoute(event.detail.quoteId));
    void this.loadList();
  };

  /** Set by the walk a create makes (the work area's own hand down), so the heading does not take the caret from the search box. */
  private createWalk = false;

  /** The quote the work area last read (or failed to read): the page title and the unreadable page follow it. */
  private lastRead = { id: "", no: "", ok: true };

  private readonly onQuoteRead = (event: CustomEvent<{ quoteId: string; quoteNo: string; ok: boolean }>): void => {
    this.lastRead = { id: event.detail.quoteId, no: event.detail.quoteNo, ok: event.detail.ok };
    document.title = this.pageTitle();
  };

  /** The document title of the page the route names. */
  private pageTitle(): string {
    const route = this.route;
    if (route.kind === "list") return "Quotes";
    if (route.kind === "compose") return "New quote";
    if (this.lastRead.id === route.id) return this.lastRead.ok ? this.lastRead.no : "This quote could not be opened";
    const row = this.rows.find((r) => r.id === route.id);
    return row?.quoteNo ?? "Quote";
  }

  /** A route change moves the person to the new page: its heading takes focus, so the keyboard and a screen reader start there. */
  private async focusHeading(): Promise<void> {
    // The walk a create makes to its new quote is not the person's own: the work area puts the caret in the search box.
    if (this.createWalk) {
      this.createWalk = false;
      return;
    }
    if (this.route.kind === "list") {
      await this.updateComplete;
      this.renderRoot.querySelector<HTMLElement>('[data-testid="qd-list-title"]')?.focus();
      return;
    }
    const area = this.renderRoot.querySelector<HTMLElement & { updateComplete?: Promise<unknown> }>("qd-quote-workarea");
    await this.updateComplete;
    await area?.updateComplete;
    area?.shadowRoot?.querySelector<HTMLElement>('[data-testid="qb-title"]')?.focus();
  }

  /** A refusal for a quote that is not on screen: said on the list, never as a dialog over another quote. */
  private readonly onRefusalDropped = (event: CustomEvent<{ quoteId: string; lineNo: number }>): void => {
    const row = this.rows.find((r) => r.id === event.detail.quoteId);
    const name = row?.quoteNo ?? "a quote you left";
    this.listNotice = `A change to line ${event.detail.lineNo} of ${name} was refused: it is below the margin floor. Nothing was changed or sent for approval. Open the quote to ask for approval.`;
  };

  private readonly onAllQuotes = (): void => {
    this.walk(LIST_ROUTE);
  };

  /** The requester's way to the approvals view from the quote page's disclosure. */
  private readonly onShowApprovals = (): void => {
    this.approvalsOpen = true;
    this.walk(LIST_ROUTE);
  };

  /**
   * The work area's Screen settings button asks for the settings region: the
   * detail disclosure opens and the settings panel opens its screen settings.
   */
  private readonly onOpenSettings = (): void => {
    const details = this.renderRoot.querySelector<HTMLDetailsElement>('[data-testid="qb-detail"]');
    if (details !== null) details.open = true;
    this.renderRoot.querySelector<HTMLElement & { openScreenSettings?: () => void }>("qd-quote-settings")?.openScreenSettings?.();
  };

  private reloadDetailPanels(): void {
    this.renderRoot
      ?.querySelectorAll<HTMLElement & { reload?: () => void }>(
        "qd-quote-workarea, qd-panel-seam, qd-quote-edit, qd-quote-history, qd-quote-settings, qd-quote-assist, qd-quote-followups, qd-quote-hold-expiry, qd-quote-availability, qd-quote-guard, qd-approvals",
      )
      .forEach((el) => el.reload?.());
  }

  /** The panels no single quote owns: they re-read on any edit, whichever quote it names. */
  private reloadSessionPanels(): void {
    this.renderRoot
      ?.querySelectorAll<HTMLElement & { reload?: () => void }>("qd-quote-settings, qd-approvals")
      .forEach((el) => el.reload?.());
  }

  // ── the host delegation ────────────────────────────────────────────────────

  private readonly onActionClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    if (action === "open") {
      this.walk(quoteRoute(control.getAttribute("data-quote") ?? ""));
    }
    if (action === "refresh") {
      void this.loadList();
    }
    if (action === "more") {
      void this.loadMore();
    }
    if (action === "new-quote") {
      // New quote is always a fresh compose: whatever an abandoned compose left in the work area is cleared first
      // (the route alone does not change the work area's quote when it was already empty).
      this.renderRoot.querySelector<HTMLElement & { beginCompose?: () => void }>("qd-quote-workarea")?.beginCompose?.();
      this.walk(COMPOSE_ROUTE);
    }
    if (action === "tab") {
      const value = control.getAttribute("data-value") ?? "";
      if (value === APPROVALS_TAB.value) {
        this.approvalsOpen = true;
      } else {
        this.approvalsOpen = false;
        this.statusFilter = value;
        void this.loadList();
      }
    }
    if (action === "show-approvals") {
      this.onShowApprovals();
    }
  };

  private readonly onFieldInput = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (!(control instanceof HTMLInputElement)) return;
    if (control.getAttribute("data-field") === "list-search") {
      this.search = control.value;
      void this.searchAllPages();
    }
  };

  /** A control's walk to a route: the address bar is written once, and the hashchange (the host's ear) does the rest. */
  private walk(route: DeskRoute): void {
    const hash = routeHash(route);
    if (location.hash !== hash) location.hash = hash;
  }

  // ── the branch facts ───────────────────────────────────────────────────────

  /** The quote the work area holds (the quote page's route, or the one it kept when the person walked to the list). */
  private openQuoteId(): string {
    const route = this.route;
    if (route.kind === "quote") return route.id;
    if (route.kind === "compose") return "";
    return this.lastQuoteId;
  }

  /**
   * The open quote's branch, read from the list row rather than from the
   * person's own: the person's branch is what the NEXT quote would be created
   * in, and using it here would scope the catalogue to the wrong yard the
   * moment the person changed.
   */
  private selectedBranch(): string {
    return this.rows.find((r) => r.id === this.openQuoteId())?.branchId ?? "";
  }

  private branchDisplayName(): string {
    const id = this.selectedBranch() === "" ? this.myBranch : this.selectedBranch();
    const b = this.branches.find((x) => x.id === id);
    return b !== undefined ? b.name : "";
  }

  // ── render ─────────────────────────────────────────────────────────────────

  /** The status tabs: the closed set, and the Sales manager's Approvals beside them. */
  private tabOptions(): Array<{ label: string; value: string }> {
    return this.roleCode === "sales_manager" ? [...STATUS_TABS, APPROVALS_TAB] : STATUS_TABS;
  }

  /** The rows the search leaves (by number or customer): a filter of what has been read (a search reads on through the pages first). */
  private visibleRows(): QuoteRow[] {
    const needle = this.search.trim().toLowerCase();
    if (needle === "") return this.rows;
    return this.rows.filter(
      (r) => r.quoteNo.toLowerCase().includes(needle) || r.customerName.toLowerCase().includes(needle),
    );
  }

  private renderListPage() {
    const rows = this.visibleRows();
    return html`
      <section class="listpage" data-testid="qd-list-page" aria-label="Quotes">
        <div class="titlerow">
          <h1 tabindex="-1" data-testid="qd-list-title">Quotes</h1>
          ${button({ label: "New quote", kind: "primary", action: "new-quote", iconMarkup: icon("plus") })}
        </div>
        ${this.listNotice !== "" ? html`<p class="empty" role="status" style="margin: 0 0 10px" data-testid="qd-list-notice">${this.listNotice}</p>` : nothing}
        <div class="toolbar" data-testid="qd-list-toolbar">
          <div class="tools">
            ${textInput({ label: "Search quotes", field: "list-search", type: "search" })}
          </div>
          <div class="tools">
            ${segmented({
              label: "Filter by status",
              field: "status-filter",
              action: "tab",
              testid: "qd-list-tabs",
              selected: this.approvalsOpen ? APPROVALS_TAB.value : this.statusFilter,
              options: this.tabOptions(),
            })}
            ${button({ label: "Refresh", kind: "secondary", action: "refresh", disabled: this.busy })}
          </div>
        </div>
        <div class="listmain" data-open=${this.approvalsOpen ? "false" : "true"}>
          ${rows.length === 0
            ? html`<p class="empty" style="margin-top: 10px" data-testid="qb-list">${this.searching ? "Searching all the quotes…" : "No quotes."}</p>`
            : html`<ul style="margin-top: 0" data-testid="qb-list">
                ${rows.map(
                  (r) => html`<li>
                    <button
                      type="button"
                      class="rowline"
                      data-action="open"
                      data-quote=${r.id}
                      data-testid="qb-list-row"
                      aria-current=${r.id === this.openQuoteId() ? "true" : "false"}
                    >
                      <span class="c-no">${r.quoteNo}</span>
                      <span class="c-cust">${r.customerName}</span>
                      <span class="c-total">${money(r.totalCents)}</span>
                      <span class="c-status">${chip(quoteStatusWord(r.status), statusTone(r.status))}</span>
                      <span class="c-owner">${r.ownerName ?? ""}</span>
                      <span class="c-until">${expiryLabel({ valid_until: r.validUntil ?? undefined, expired: r.expired })}</span>
                    </button>
                  </li>`,
                )}
              </ul>`}
          ${this.nextCursor !== ""
            ? html`<div class="row" style="margin-top: 8px"><button type="button" class="btn-secondary" data-action="more" data-testid="qb-list-more" ?disabled=${this.busy}>Load more quotes</button></div>`
            : nothing}
        </div>
        <div class="approvalsview" data-testid="qd-approvals-view" data-open=${this.approvalsOpen ? "true" : "false"}>
          <qd-approvals data-seam=${SEAM_APPROVALS}></qd-approvals>
        </div>
      </section>
    `;
  }

  /** The named empty seams the second and third coders fill. */
  private renderSeam(name: string, label: string) {
    return html`<qd-panel-seam data-seam=${name} data-seam-label=${label}></qd-panel-seam>`;
  }

  override render() {
    return html`
      ${this.error !== "" ? html`<header><span class="err" role="alert">${this.error}</span></header>` : nothing}
      <div class="desksplit" data-view=${routeIsQuotePage(this.route) ? "quote" : "list"}>
        ${this.renderListPage()}
        <section class="quotepage" aria-label="The quote">
          <qd-quote-workarea></qd-quote-workarea>
          <qd-floor-refusal></qd-floor-refusal>
          <details class="box" style="margin-top: 12px" data-testid="qb-detail">
            <summary style="cursor: pointer">Quote detail &amp; admin</summary>
            <div class="stack seams" style="margin-top: 10px">
              ${this.openQuoteId() === ""
                ? html`<div class="box"><p class="empty">Select a quote to open it.</p></div>`
                : nothing}
              <!-- The panels stay mounted through the list page and New Quote (each returns nothing for no
                   quote): a panel taken off the page loses the late answers of writes still out for the quote it left,
                   its announcement dies with it and the panel that comes back is never told what landed. Leaving a quote
                   for another quote was already this way, and the list page and New Quote take that same proven path. -->
              <div class="stack" data-testid="qb-quote-panels" data-compose=${this.openQuoteId() === "" ? "true" : "false"}>
            <qd-quote-edit data-seam=${SEAM_EDIT_PANEL}></qd-quote-edit>
            <qd-quote-assist data-seam=${SEAM_ASSIST}></qd-quote-assist>
            <qd-quote-history data-seam=${SEAM_HISTORY}></qd-quote-history>
            <qd-quote-followups data-seam=${SEAM_FOLLOWUPS}></qd-quote-followups>
            <qd-quote-hold-expiry data-seam=${SEAM_HOLD_EXPIRY}></qd-quote-hold-expiry>
            <qd-quote-availability data-seam=${SEAM_AVAILABILITY}></qd-quote-availability>
            <qd-quote-guard></qd-quote-guard>
          </div>
              <qd-quote-settings data-seam=${SEAM_SETTINGS}></qd-quote-settings>
              <div class="row">
                <button type="button" class="btn-secondary" data-action="show-approvals">Approvals</button>
              </div>
            </div>
          </details>
        </section>
      </div>
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-builder": QdQuoteBuilder;
  }
}
