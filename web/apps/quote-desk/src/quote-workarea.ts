// Ported from hardscapeos_dibbits master 87d7941b app/src/quote-workarea.ts;
// adapted for HH.
//
// The Quote Builder WORK AREA: the house pattern header bar (bold title, the
// yard and quote number context, the tracked Customer, Products, Review and
// Send stepper, clock, status, Help, the gold New Quote), and a two panel
// workspace: products on one side (the unified scan and search box, the
// curated chips and the product grid, the ticket lines with quantity and
// discount, the suggested add-ons strip), customer, terms and send on the
// other. The panels resize, collapse and swap, saved per person through the
// workspace row (the layout store), and the columns render from the surface
// registry and the dealer's curation: no hard-coded layout list lives here.
//
// WHAT THIS COMPONENT IS NOT. An authority on anything. Every offer is the
// server's (editable_fields, can_convert), every price is the engine's, every
// margin figure arrives role gated from the margin glance (byte absent for a
// caller the cost and margin list refuses: an absent payload removes the
// WHOLE margin surface here, the Columns popover's cost offers included).
// The below-floor refusal is the typed 409: the plain message is spoken in
// the outcome line and the refusal dialog opens through the seam in
// floor-refusal.ts (the third coder's panel).
//
// REUSE LEDGER (the shared kit, consumed not rebuilt): the scan box, the
// product grid, the product thumb, the flow steps, the customer picker, the
// workspace geometry helpers, the icon registry, the money face and the demo
// way in, all from @hardscapehouse/pos-kit by deep path. Nothing is imported
// from the counter till's app.
//
// THE BINDING DISCIPLINE: this app's own templates hold no property or event
// bindings (the design system's position gate holds them at none), so the
// controls stay static in the templates and this root delegates: the kits'
// own data-action and data-field patterns, id and data-keyed reads for the
// inputs, and an updated() wiring pass that hands the shared components
// their properties. The inputs are uncontrolled: the DOM holds what was
// typed, this element hears every edit, and the fresh read's figures are
// brought back onto the fields in the same pass.
import { LitElement, css, html, nothing } from "lit";
import { customElement, property, state } from "lit/decorators.js";
import "@hardscapehouse/pos-kit/src/ported/pos-scan-box.js";
import "@hardscapehouse/pos-kit/src/ported/pos-product-grid.js";
import "@hardscapehouse/pos-kit/src/ported/product-thumb.js";
import "@hardscapehouse/pos-kit/src/ported/pos-flow-steps.js";
import "./customer-picker.js";
import { drawIcon, iconForId } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import { clockLabel } from "@hardscapehouse/pos-kit/src/ported/_pos-rev8.js";
import { money } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import { addonSuggestions, listSalespeople, searchCustomers } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import type { PosProduct } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import type { PickerCustomer } from "@hardscapehouse/pos-kit/src/ported/_customer-picker.js";
import type { Salesperson } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";
import {
  canConvert,
  canEdit,
  EDIT_LINE_ADD,
  EDIT_LINE_QTY,
  EDIT_LINE_REMOVE,
  EDIT_VALID_UNTIL,
  quoteErrorMessage,
  quoteStatusPhrase,
  quoteStatusWord,
  type QuoteView,
  type QuoteViewLine,
} from "./_quote-edit.js";
import {
  collapseNext,
  creditLine,
  customerTypeTag,
  depositCents,
  parseDeliveryFee,
  freshQuoteState,
  initialsOf,
  lineCostCents,
  lineMarginLabel,
  marginPctLabel,
  marginPresent,
  qtyLabel,
  quoteFlowSteps,
  quotePanelTracks,
  quoteSplitFromPointer,
  sendCaption,
  sendChannels,
  sentConfirmation,
  spokenRefusal,
  totalsBreakdown,
  validityDaysOf,
  validUntilFromDays,
  VALIDITY_CHOICES,
  type GlanceIn,
  type QuoteArrangement,
} from "./_quote-workarea.js";
import {
  effectiveItems,
  panelVisible,
  visibleItems,
  QB_ACTIONS,
  QB_COLUMNS,
  QB_PANELS,
  type ResolvedItem,
} from "./quote-surface.js";
import { FLOOR_REFUSAL_CLOSED_EVENT, floorRefusalMessage, openFloorRefusalDialog, parseFloorRefusal } from "./floor-refusal.js";
import { OPEN_SETTINGS_EVENT } from "./panel-seams.js";
import type { Result } from "@hardscapehouse/api-client";
import type { QdClient, SurfaceConfig, TermsRef } from "./qd-client.js";
import { WorkspaceStore } from "./workspace-store.js";
import { QuoteContext, QuoteScope, SIGN_OUT_EVENT, UNSENT_WAIT_MS, waitUpTo, type SignOutDetail } from "./quote-context.js";
import type { AddonSuggestion } from "@hardscapehouse/pos-kit/src/ported/pos-api.js";

/** Said when the read of a quote went ahead while a terms save of that quote was still out. */
const STILL_SAVING = "A terms change you made on this quote is still being saved. What is shown may not include it yet.";

/** How long a scan made in compose waits for the host to hand the quote just made down before it says so. */
const HANDOFF_WAIT_MS = 3000;

/** Thrown inside the send and link paths when the quote changed under them: caught and said nothing. */
class StaleQuoteError extends Error {}

/** The whole terms row as the save sends it (the core takes the row whole). */
function termsBody(t: TermsRef): Parameters<QdClient["putTerms"]>[1] {
  return {
    depositRequired: t.depositRequired,
    depositPct: t.depositPct,
    includeImages: t.includeImages,
    customerNotes: t.customerNotes,
    contactName: t.contactName,
    projectJob: t.projectJob,
    customerPo: t.customerPo,
    salespersonId: t.salespersonId,
    fulfillment: t.fulfillment,
    deliveryAddress: t.deliveryAddress,
    requestedDate: t.requestedDate,
    deliveryFeeCents: t.deliveryFeeCents,
  };
}

interface AddonSuggestionRow {
  product_id: string;
  name: string;
  size_line?: string;
  uom: string;
  default_qty: number;
  list_price_cents?: number;
}

@customElement("qd-quote-workarea")
export class QdQuoteWorkarea extends LitElement {
  /** The open quote ('' means compose: pick a customer to start one). */
  @property({ attribute: "quote-id" }) quoteId = "";
  @property({ attribute: "branch-id" }) branchId = "";
  @property({ attribute: "branch-name" }) branchName = "";

  // The host wires these before the first render (the builder's own wiring
  // pass): the validated client and nothing else; the store is built from
  // the config read here.
  @property({ attribute: false }) client: QdClient | null = null;

  @state() private view: QuoteView | null = null;
  @state() private terms: TermsRef | null = null;
  @state() private config: SurfaceConfig | null = null;
  @state() private glance: GlanceIn | null = null;
  @state() private addons: AddonSuggestionRow[] = [];
  @state() private customer: PickerCustomer | null = null;
  @state() private salespeople: Salesperson[] = [];
  @state() private error = "";
  /** A layout save, reset or column save that was refused, in words: never silently dropped. */
  @state() private layoutError = "";
  /** The column save's own refusal: a layout save that lands must never clear it, nor the reverse. */
  @state() private columnsError = "";
  @state() private notice = "";
  @state() private busy = false;
  /** How many writes are out: Send stays disabled until the last of them has answered, not the first. */
  private busyHolds = 0;
  @state() private term = "";
  @state() private qtyDrafts: Record<number, string> = {};
  @state() private discDrafts: Record<number, string> = {};
  @state() private addonQty: Record<string, string> = {};
  @state() private helpOpen = false;
  @state() private sendOpen = false;
  @state() private sendPick: Record<string, boolean> = { EMAIL: true, SMS: true };
  @state() private columnsOpen = false;
  /** The product grid's view: cards or one dense row per product (tab memory, the grid's own presentation). */
  @state() private presentation: "CARDS" | "ROWS" = "CARDS";
  @state() private converted: { orderNo: string } | null = null;
  @state() private clockNow = new Date();
  /** The panel geometry, held on the store and mirrored for the render. */
  @state() private wsEcho = 0;

  private store: WorkspaceStore | null = null;
  private clockTimer: ReturnType<typeof setInterval> | undefined;
  private termsTimer: ReturnType<typeof setTimeout> | undefined;
  private mintedToken = "";
  /** The context of the quote on screen: every read and write captures it at its start and checks it after each await. */
  private readonly scope = new QuoteScope();
  private dragging = false;
  /** After creating a quote the caret goes to the scan box: the next action is always add. */
  private pendingFocusScan = false;
  private restoreTried = false;
  private wiredOnce = false;

  static styles = css`
    :host {
      display: block;
      font-family: var(--hos-font-ui, var(--hh-font-ui));
      color: var(--hos-text, var(--hh-text));
      /* The reference's four-step type scale: title, section, body, caption. */
      --qb-title: 1.35rem;
      --qb-section: 0.95rem;
      --qb-body: 0.83rem;
      --qb-caption: 0.72rem;
    }
    /* The house header bar. */
    .topbar {
      display: flex;
      align-items: center;
      gap: var(--hos-s-16, 16px);
      flex-wrap: wrap;
      padding: var(--hos-s-8, 8px) var(--hos-s-12, 12px);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      margin-bottom: var(--hos-s-12, 12px);
    }
    .headid .pagetitle {
      margin: 0;
      font-size: var(--qb-title);
      font-weight: 700;
      line-height: 1.2;
    }
    .headid .pagectx {
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .session {
      margin-left: auto;
      display: flex;
      align-items: center;
      gap: var(--hos-s-8, 8px);
      flex-wrap: wrap;
    }
    .clock {
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      font-size: var(--qb-body);
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .statusdot {
      display: inline-flex;
      align-items: center;
      gap: var(--hos-s-4, 4px);
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
      text-transform: capitalize;
    }
    .statusdot .dot {
      width: 8px;
      height: 8px;
      border-radius: 50%;
      background: var(--hos-text-dim, var(--hh-text-dim));
    }
    .statusdot.live .dot {
      background: var(--hos-success-fill, var(--hh-positive));
    }
    .statusdot.warm .dot {
      background: var(--hos-state-watch, var(--hh-watch));
    }
    .topbtn {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 33px;
      height: 26px;
      padding: 0;
      font: inherit;
      font-size: var(--qb-body);
      border: none;
      border-radius: var(--hos-radius-control, 6px);
      background: transparent;
      color: var(--hos-text, var(--hh-text));
      cursor: pointer;
    }
    .topbtn:hover {
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .newquote {
      font: inherit;
      font-size: var(--qb-body);
      font-weight: 600;
      padding: var(--hos-s-8, 8px) var(--hos-s-12, 12px);
      border: 1px solid var(--hos-stone-amber, var(--hh-accent));
      border-radius: var(--hos-radius-control, 6px);
      background: var(--hos-stone-amber, var(--hh-accent));
      color: var(--hos-stone-amber-ink, var(--hh-accent-ink));
      cursor: pointer;
    }
    .newquote:hover {
      background: var(--hos-stone-amber-hover, var(--hh-accent));
      border-color: var(--hos-stone-amber-hover, var(--hh-accent));
    }
    .helpwrap {
      position: relative;
      display: inline-flex;
    }
    .helppop {
      position: absolute;
      right: 0;
      top: calc(100% + 6px);
      z-index: 30;
      width: 320px;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      box-shadow: var(--hos-elev-1, 0 4px 14px rgba(0, 0, 0, 0.25));
      padding: var(--hos-s-12, 12px);
      font-size: var(--qb-body);
    }
    .helppop h3 {
      margin: 0 0 var(--hos-s-4, 4px);
      font-size: var(--qb-body);
    }
    .helppop ul {
      margin: 0;
      padding-left: var(--hos-s-16, 16px);
      display: flex;
      flex-direction: column;
      gap: var(--hos-s-4, 4px);
    }
    /* The workspace: each panel scrolls internally; the right panel pins its
       totals and actions at the foot. */
    .workspace {
      display: grid;
      gap: 0;
      align-items: stretch;
      height: calc(100vh - var(--qb-chrome, 250px));
      min-height: 430px;
    }
    .panel {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      padding: var(--hos-s-12, 12px);
      min-width: 0;
      display: flex;
      flex-direction: column;
      overflow: hidden;
      position: relative;
    }
    .scrollzone {
      flex: 1;
      min-height: 0;
      overflow-y: auto;
      container: zone / inline-size;
    }
    .foot {
      flex: none;
      padding-top: var(--hos-s-8, 8px);
    }
    .panelbar {
      display: flex;
      align-items: center;
      gap: var(--hos-s-8, 8px);
      margin-bottom: var(--hos-s-8, 8px);
      flex-wrap: wrap;
      padding-right: var(--hos-s-64, 64px);
    }
    .panelbar h2 {
      margin: 0;
      font-size: var(--qb-section);
    }
    /* The summary panel's buttons ride the header's own row: the title
       truncates with an ellipsis before it can sit under a button, and the
       buttons wrap to a row of their own when the panel is narrow (visual
       review round 1, P2-3). */
    .panelbar.inflow {
      padding-right: 0;
      justify-content: space-between;
    }
    .panelbar.inflow h2 {
      flex: 1 1 auto;
      min-width: 0;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .panelbar.inflow .panelbtns {
      position: static;
      margin-left: auto;
    }
    .viewflip {
      display: inline-flex;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 999px;
      overflow: hidden;
    }
    .viewflip button {
      font: inherit;
      font-size: var(--qb-caption);
      padding: var(--hos-s-2, 2px) var(--hos-s-8, 8px);
      border: none;
      background: transparent;
      color: var(--hos-text-dim, var(--hh-text-dim));
      cursor: pointer;
    }
    .viewflip button.active {
      background: var(--hos-line, var(--hh-line));
      color: var(--hos-text, var(--hh-text));
    }
    .panelbtns {
      position: absolute;
      top: 10px;
      right: 10px;
      display: flex;
      gap: var(--hos-s-4, 4px);
      white-space: nowrap;
    }
    .pbtn {
      font: inherit;
      font-size: var(--qb-caption);
      padding: var(--hos-s-4, 4px) var(--hos-s-8, 8px);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-control, 6px);
      background: transparent;
      color: var(--hos-text-dim, var(--hh-text-dim));
      cursor: pointer;
    }
    .pbtn:hover {
      border-color: var(--hos-stone-amber, var(--hh-accent));
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    /* The closed control's rule: no fill, grey ink and line, and the reason
       on the title and the description. */
    .pbtn[aria-disabled="true"] {
      background: transparent;
      border-color: var(--hos-line, var(--hh-line));
      color: var(--hos-text-dim, var(--hh-text-dim));
      cursor: default;
    }
    .divider {
      width: 10px;
      cursor: col-resize;
      align-self: stretch;
      display: flex;
      align-items: center;
      justify-content: center;
      touch-action: none;
    }
    .divider .grip {
      width: 3px;
      border-radius: 2px;
      height: 56px;
      background: var(--hos-text-dim, var(--hh-text-dim));
    }
    .divider:hover .grip,
    .divider:focus-visible .grip {
      background: var(--hos-stone-amber, var(--hh-accent));
    }
    .rail {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      display: flex;
      align-items: flex-start;
      justify-content: center;
      padding: var(--hos-s-8, 8px) var(--hos-s-4, 4px);
      min-height: 220px;
    }
    /* The ticket lines table. */
    table.lines {
      width: 100%;
      border-collapse: collapse;
      font-size: var(--qb-body);
      margin-top: var(--hos-s-8, 8px);
    }
    table.lines th {
      text-align: left;
      font-weight: 600;
      font-size: var(--qb-caption);
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--hos-text-dim, var(--hh-text-dim));
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
      padding: var(--hos-s-4, 4px);
      white-space: nowrap;
    }
    /* The ERP stripes its ticket rows: every second line takes the row-alt tint. */
    table.lines tbody tr:nth-child(even) {
      background: var(--hos-row-alt, var(--hh-row-alt));
    }
    table.lines td {
      padding: var(--hos-s-8, 8px) var(--hos-s-4, 4px);
      border-bottom: 1px solid var(--hos-line, var(--hh-line));
      vertical-align: middle;
    }
    /* A narrow panel (the tablet width, or the split dragged in): the photo
       goes, the item takes its own line and the figures wrap under it, each
       with its column's name above it, so Disc %, Total and the remove control
       stay on screen instead of scrolling away out of sight (visual review
       round 1, P2-2). */
    @container zone (max-width: 560px) {
      table.lines,
      table.lines tbody {
        display: block;
      }
      table.lines thead {
        position: absolute;
        width: 1px;
        height: 1px;
        overflow: hidden;
        clip-path: inset(50%);
      }
      table.lines tr {
        display: flex;
        flex-wrap: wrap;
        align-items: flex-end;
        gap: var(--hos-s-4, 4px) var(--hos-s-8, 8px);
        padding: var(--hos-s-8, 8px) var(--hos-s-4, 4px);
        border-bottom: 1px solid var(--hos-line, var(--hh-line));
      }
      table.lines td {
        padding: 0;
        border: none;
      }
      table.lines td[data-role="thumb"] {
        display: none;
      }
      table.lines td[data-role="item"] {
        flex: 1 1 100%;
      }
      table.lines td[data-label]::before {
        content: attr(data-label);
        display: block;
        font-size: var(--qb-caption);
        text-transform: uppercase;
        letter-spacing: 0.06em;
        text-align: left;
        color: var(--hos-text-dim, var(--hh-text-dim));
      }
      table.lines td.num::before {
        text-align: right;
      }
      table.lines .stepper input {
        width: 3rem;
      }
      table.lines input.disc {
        width: 2.8rem;
      }
      table.lines td[data-role="remove"] {
        margin-left: auto;
      }
      table.lines td[data-role="empty-cell"] {
        flex: 1 1 100%;
      }
    }
    .num {
      text-align: right;
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      white-space: nowrap;
    }
    th.num {
      font-family: inherit;
    }
    .iname {
      font-weight: 600;
      min-width: 110px;
    }
    .isku {
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    /* The plan's ruled mark on a line whose own rule price sits under the
       floor: a small caption, not an alert, exactly as CP0-3 answer A words
       it (accepted with no manager, marked for the salesperson). */
    .floormark {
      display: inline-block;
      margin-left: var(--hos-s-4, 4px);
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 999px;
      padding: 0 var(--hos-s-8, 8px);
    }
    .clearedby {
      display: inline-block;
      margin-left: var(--hos-s-4, 4px);
      font-size: var(--qb-caption);
      color: var(--hos-success, var(--hh-positive));
    }
    .stepper {
      display: inline-flex;
      align-items: center;
      gap: var(--hos-s-4, 4px);
    }
    .stepper button {
      font: inherit;
      width: 24px;
      height: 24px;
      line-height: 1;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-control, 6px);
      background: transparent;
      color: var(--hos-text, var(--hh-text));
      cursor: pointer;
    }
    .stepper button:disabled {
      opacity: 0.4;
      cursor: default;
    }
    .stepper input,
    input.disc {
      width: 4.2rem;
      text-align: right;
      font: inherit;
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      padding: var(--hos-s-4, 4px);
      border: 1px solid var(--hos-control-line, var(--hh-control-line, var(--hh-line)));
      border-radius: var(--hos-radius-control, 6px);
      background: var(--hos-deep-earth, var(--hh-page));
      color: var(--hos-text, var(--hh-text));
    }
    input.disc {
      width: 3.4rem;
    }
    button.trash {
      font: inherit;
      border: 1px solid var(--hos-line, var(--hh-line));
      background: transparent;
      color: var(--hos-text-dim, var(--hh-text-dim));
      border-radius: var(--hos-radius-control, 6px);
      width: 26px;
      height: 26px;
      cursor: pointer;
    }
    button.trash:hover {
      color: var(--hos-safety-red, var(--hh-negative));
      border-color: var(--hos-safety-red, var(--hh-negative));
    }
    .addanother {
      display: flex;
      width: 100%;
      align-items: center;
      gap: var(--hos-s-8, 8px);
      font: inherit;
      font-size: var(--qb-body);
      margin-top: var(--hos-s-8, 8px);
      padding: var(--hos-s-8, 8px);
      border: 1px dashed var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-control, 6px);
      background: transparent;
      color: var(--hos-text-dim, var(--hh-text-dim));
      cursor: pointer;
    }
    .addanother:hover {
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
      border-color: var(--hos-stone-amber, var(--hh-accent));
    }
    /* The add-ons strip. */
    .addons {
      margin-top: var(--hos-s-12, 12px);
    }
    .addons h3 {
      margin: 0 0 var(--hos-s-8, 8px);
      font-size: var(--qb-body);
    }
    .addonrow {
      display: flex;
      gap: var(--hos-s-8, 8px);
      overflow-x: auto;
    }
    .addontile {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-deep-earth, var(--hh-page));
      padding: var(--hos-s-8, 8px);
      min-width: 190px;
      display: flex;
      flex-direction: column;
      gap: var(--hos-s-4, 4px);
      font-size: var(--qb-caption);
    }
    .addontile .aname {
      font-weight: 600;
      font-size: var(--qb-body);
    }
    .addontile .actrl {
      display: flex;
      gap: var(--hos-s-4, 4px);
      align-items: center;
    }
    .addontile input {
      width: 3.2rem;
      font: inherit;
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      text-align: right;
      padding: var(--hos-s-4, 4px);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-control, 6px);
      background: var(--hos-slate-warm, var(--hh-card));
      color: var(--hos-text, var(--hh-text));
    }
    .addonadd {
      font: inherit;
      font-size: var(--qb-caption);
      font-weight: 600;
      padding: var(--hos-s-4, 4px) var(--hos-s-8, 8px);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-control, 6px);
      background: transparent;
      color: var(--hos-text, var(--hh-text));
      cursor: pointer;
    }
    .addonadd:hover:not(:disabled) {
      border-color: var(--hos-stone-amber, var(--hh-accent));
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .addonadd:disabled {
      opacity: 0.5;
      cursor: default;
    }
    /* The right panel's cards. */
    .card {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-deep-earth, var(--hh-page));
      padding: var(--hos-s-12, 12px);
      margin-bottom: var(--hos-s-8, 8px);
    }
    .identity {
      display: flex;
      gap: var(--hos-s-8, 8px);
      align-items: flex-start;
    }
    .avatar {
      font-size: var(--qb-section);
      width: 40px;
      height: 40px;
      border-radius: 50%;
      background: var(--hos-slate-warm, var(--hh-card));
      border: 1px solid var(--hos-line, var(--hh-line));
      display: flex;
      align-items: center;
      justify-content: center;
      font-weight: 700;
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
      flex: none;
    }
    .identity .cname {
      font-weight: 600;
      font-size: var(--qb-body);
    }
    .identity .cmeta {
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-family: var(--hos-font-mono, ui-monospace, monospace);
    }
    .identity .credit {
      font-size: var(--qb-caption);
      color: var(--hos-success, var(--hh-positive));
      margin-top: 2px;
    }
    .identity .chg {
      margin-left: auto;
    }
    .tag {
      display: inline-block;
      font-size: var(--qb-caption);
      text-transform: uppercase;
      letter-spacing: 0.08em;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 999px;
      padding: 1px var(--hos-s-8, 8px);
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .fields {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: var(--hos-s-8, 8px);
    }
    .fields .wide {
      grid-column: 1 / -1;
    }
    label.f {
      display: block;
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
      margin-bottom: var(--hos-s-4, 4px);
    }
    .fields input,
    .fields select,
    .termsrow input,
    .termsrow select {
      width: 100%;
      box-sizing: border-box;
      font: inherit;
      font-size: var(--qb-body);
      padding: var(--hos-s-4, 4px) var(--hos-s-8, 8px);
      border: 1px solid var(--hos-control-line, var(--hh-control-line, var(--hh-line)));
      border-radius: var(--hos-radius-control, 6px);
      background: var(--hos-slate-warm, var(--hh-card));
      color: var(--hos-text, var(--hh-text));
    }
    .fulfil {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: var(--hos-s-8, 8px);
      margin-bottom: var(--hos-s-8, 8px);
    }
    .tile {
      font: inherit;
      font-size: var(--qb-body);
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: var(--hos-s-4, 4px);
      padding: var(--hos-s-8, 8px);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      color: var(--hos-text, var(--hh-text));
      cursor: pointer;
    }
    .tile[aria-pressed="true"] {
      border-color: var(--hos-stone-amber, var(--hh-accent));
      background: color-mix(in srgb, var(--hos-stone-amber, var(--hh-accent)) 10%, var(--hos-slate-warm, var(--hh-card)));
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .tile svg {
      width: 18px;
      height: 18px;
    }
    .totals {
      font-size: var(--qb-body);
      display: flex;
      flex-direction: column;
      gap: var(--hos-s-4, 4px);
    }
    .trow {
      display: flex;
      justify-content: space-between;
      gap: var(--hos-s-12, 12px);
    }
    .trow.sep {
      border-top: 1px solid var(--hos-line, var(--hh-line));
      padding-top: var(--hos-s-4, 4px);
    }
    .trow .neg {
      color: var(--hos-success, var(--hh-positive));
    }
    .grand {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: var(--hos-s-8, 8px);
      margin-top: var(--hos-s-4, 4px);
      flex-wrap: wrap;
    }
    .grand .lbl {
      font-size: var(--qb-section);
      font-weight: 700;
    }
    .grand .amt {
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      font-size: var(--qb-title);
      font-weight: 700;
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .marginpill {
      font-size: var(--qb-caption);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 999px;
      padding: 2px var(--hos-s-8, 8px);
      color: var(--hos-success, var(--hh-positive));
      white-space: nowrap;
    }
    .termsrow {
      display: grid;
      grid-template-columns: repeat(2, 1fr);
      gap: var(--hos-s-8, 8px);
      align-items: end;
    }
    .check {
      display: inline-flex;
      gap: var(--hos-s-4, 4px);
      align-items: center;
      font-size: var(--qb-caption);
      color: var(--hos-text, var(--hh-text));
    }
    .sr-only {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
      border: 0;
    }
    .drafthint {
      margin: var(--hos-s-8, 8px) 0 0;
      font-size: var(--qb-caption);
      color: var(--hos-text-dim, var(--hh-text-dim));
    }
    .actions {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: var(--hos-s-8, 8px);
      margin-top: var(--hos-s-8, 8px);
    }
    .abtn {
      font: inherit;
      font-size: var(--qb-body);
      padding: var(--hos-s-8, 8px);
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-control, 6px);
      background: var(--hos-slate-warm, var(--hh-card));
      color: var(--hos-text, var(--hh-text));
      cursor: pointer;
    }
    .abtn:hover:not(:disabled) {
      border-color: var(--hos-stone-amber, var(--hh-accent));
      color: var(--hos-stone-amber-text, var(--hh-accent-text));
    }
    .abtn:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .sendwrap {
      position: relative;
      grid-column: 1 / -1;
      display: flex;
      gap: var(--hos-s-8, 8px);
    }
    .sendbtn {
      flex: 1;
      font: inherit;
      font-size: var(--qb-section);
      font-weight: 700;
      letter-spacing: 0.02em;
      padding: var(--hos-s-12, 12px);
      border: 1px solid var(--hos-stone-amber, var(--hh-accent));
      border-radius: var(--hos-radius-control, 6px);
      background: var(--hos-stone-amber, var(--hh-accent));
      color: var(--hos-stone-amber-ink, var(--hh-accent-ink));
      cursor: pointer;
    }
    .sendbtn:hover:not(:disabled) {
      background: var(--hos-stone-amber-hover, var(--hh-accent));
    }
    .sendbtn:disabled {
      opacity: 0.5;
      cursor: default;
    }
    .key {
      font-family: var(--hos-font-mono, ui-monospace, monospace);
      font-size: var(--qb-caption);
      font-weight: 600;
      border: 1px solid var(--hos-stone-amber-ink, var(--hh-accent-ink));
      border-radius: var(--hos-radius-control, 6px);
      padding: 1px var(--hos-s-4, 4px);
      margin-left: var(--hos-s-8, 8px);
      opacity: 0.85;
    }
    .sendpop {
      position: absolute;
      bottom: calc(100% + 6px);
      left: 0;
      z-index: 20;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      box-shadow: var(--hos-elev-1, 0 4px 14px rgba(0, 0, 0, 0.25));
      padding: var(--hos-s-8, 8px) var(--hos-s-12, 12px);
      display: flex;
      flex-direction: column;
      gap: var(--hos-s-4, 4px);
      font-size: var(--qb-body);
      min-width: 220px;
    }
    .colpop {
      position: absolute;
      right: 0;
      top: calc(100% + 6px);
      z-index: 25;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      box-shadow: var(--hos-elev-1, 0 4px 14px rgba(0, 0, 0, 0.25));
      padding: var(--hos-s-8, 8px) var(--hos-s-12, 12px);
      display: flex;
      flex-direction: column;
      gap: var(--hos-s-4, 4px);
      font-size: var(--qb-body);
      min-width: 220px;
    }
    .colpop .colrow {
      display: flex;
      align-items: center;
      gap: var(--hos-s-4, 4px);
    }
    .colpop .colrow .mv {
      margin-left: auto;
      display: flex;
      gap: 2px;
    }
    .colwrap {
      position: relative;
    }
    .err {
      color: var(--hos-safety-red, var(--hh-negative));
      font-size: var(--qb-body);
      margin-top: var(--hos-s-8, 8px);
    }
    .ok {
      color: var(--hos-success, var(--hh-positive));
      font-size: var(--qb-body);
      margin-top: var(--hos-s-8, 8px);
    }
    .err:empty,
    .ok:empty {
      margin: 0;
      height: 0;
      overflow: hidden;
    }
    .empty {
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-size: var(--qb-body);
    }
    .gridwrap {
      margin-top: var(--hos-s-8, 8px);
    }
    .composewrap {
      position: relative;
      display: flex;
      justify-content: center;
      align-items: flex-start;
      padding-top: var(--hos-s-48, 48px);
      min-height: 320px;
    }
    .composecard {
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: var(--hos-radius-card, 10px);
      background: var(--hos-slate-warm, var(--hh-card));
      padding: var(--hos-s-20, 20px);
      width: min(30rem, 92%);
    }
    .viewflip {
      display: inline-flex;
      border: 1px solid var(--hos-line, var(--hh-line));
      border-radius: 999px;
      overflow: hidden;
    }
    .viewflip button {
      background: transparent;
      border: 0;
      color: var(--hos-text-dim, var(--hh-text-dim));
      font-family: inherit;
      font-size: var(--qb-caption);
      padding: var(--hos-s-4, 4px) var(--hos-s-8, 8px);
      cursor: pointer;
    }
    .viewflip button.active {
      background: color-mix(in srgb, var(--hos-line, var(--hh-line)) 40%, transparent);
      color: var(--hos-text, var(--hh-text));
    }
    button:focus-visible,
    input:focus-visible,
    select:focus-visible,
    [tabindex]:focus-visible {
      outline: 2px solid var(--hos-stone-amber-text, var(--hh-accent-text));
      outline-offset: 2px;
    }
  `;

  /** The floor refusal's plain message as spoken in the outcome line: the dialog repeats it, so closing the dialog takes it down. */
  private floorSpoken = "";

  private readonly onFloorDialogClosed = (): void => {
    if (this.floorSpoken !== "" && this.error === this.floorSpoken) this.error = "";
    this.floorSpoken = "";
  };

  override connectedCallback(): void {
    super.connectedCallback();
    this.detached = false;
    window.addEventListener(FLOOR_REFUSAL_CLOSED_EVENT, this.onFloorDialogClosed);
    window.addEventListener(SIGN_OUT_EVENT, this.onSignOut);
    this.startLoads();
    this.clockTimer = setInterval(() => {
      this.clockNow = new Date();
    }, 15000);
    this.renderRoot.addEventListener("click", this.onActionClick as EventListener);
    this.renderRoot.addEventListener("input", this.onFieldInput as EventListener);
    this.renderRoot.addEventListener("change", this.onFieldChange as EventListener);
    this.renderRoot.addEventListener("keydown", this.onKey as EventListener);
    this.renderRoot.addEventListener("focusin", this.onFieldFocus as EventListener);
    this.renderRoot.addEventListener("pointerdown", this.onDividerPointerDown as EventListener);
    this.renderRoot.addEventListener("pointermove", this.onDividerPointerMove as EventListener);
    this.renderRoot.addEventListener("pointerup", this.onDividerPointerUp as EventListener);
    this.renderRoot.addEventListener("pointercancel", this.onDividerPointerUp as EventListener);
    this.renderRoot.addEventListener("pointerdown", this.onOutsidePointerDown, true);
    this.renderRoot.addEventListener("pos-term-changed", this.onTermChanged as EventListener);
    this.renderRoot.addEventListener("pos-scan-resolved", this.onScanResolved as EventListener);
    this.renderRoot.addEventListener("pos-add-product", this.onAddProduct as EventListener);
    this.renderRoot.addEventListener("pos-flow-goto", this.onFlowGoto as EventListener);
    this.renderRoot.addEventListener("customer-picked", this.onCustomerPicked as EventListener);
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    window.removeEventListener(FLOOR_REFUSAL_CLOSED_EVENT, this.onFloorDialogClosed);
    window.removeEventListener(SIGN_OUT_EVENT, this.onSignOut);
    this.renderRoot.removeEventListener("click", this.onActionClick as EventListener);
    this.renderRoot.removeEventListener("input", this.onFieldInput as EventListener);
    this.renderRoot.removeEventListener("change", this.onFieldChange as EventListener);
    this.renderRoot.removeEventListener("keydown", this.onKey as EventListener);
    this.renderRoot.removeEventListener("focusin", this.onFieldFocus as EventListener);
    this.renderRoot.removeEventListener("pointerdown", this.onDividerPointerDown as EventListener);
    this.renderRoot.removeEventListener("pointermove", this.onDividerPointerMove as EventListener);
    this.renderRoot.removeEventListener("pointerup", this.onDividerPointerUp as EventListener);
    this.renderRoot.removeEventListener("pointercancel", this.onDividerPointerUp as EventListener);
    this.renderRoot.removeEventListener("pointerdown", this.onOutsidePointerDown, true);
    this.renderRoot.removeEventListener("pos-term-changed", this.onTermChanged as EventListener);
    this.renderRoot.removeEventListener("pos-scan-resolved", this.onScanResolved as EventListener);
    this.renderRoot.removeEventListener("pos-add-product", this.onAddProduct as EventListener);
    this.renderRoot.removeEventListener("pos-flow-goto", this.onFlowGoto as EventListener);
    this.renderRoot.removeEventListener("customer-picked", this.onCustomerPicked as EventListener);
    if (this.clockTimer !== undefined) clearInterval(this.clockTimer);
    if (this.termsTimer !== undefined) clearTimeout(this.termsTimer);
    // A write still out when the work area leaves the page (sign out) must not go on to send follow-ups under the next session.
    this.detached = true;
    this.scope.open("");
    this.releaseHandoffWaiters();
  }

  /** True from the moment this work area leaves the page: its context is then cancelled for good. */
  private detached = false;

  protected override willUpdate(changed: Map<string, unknown>): void {
    // The host wires the client after this element mounts; the loads start
    // the moment it arrives.
    if (changed.has("quoteId")) this.leaveQuote(typeof changed.get("quoteId") === "string" ? (changed.get("quoteId") as string) : "");
    if (changed.has("quoteId") && this.quoteId !== "") this.releaseHandoffWaiters();
    if (changed.has("client") && this.client !== null) this.startLoads();
    if (changed.has("quoteId")) {
      this.opening = this.loadAll();
      // Remember what is open so a refresh resumes it, sent now rather than left to the debounce.
      if (changed.get("quoteId") !== undefined || this.quoteId) {
        if (this.store !== null) this.store.state.openRef = this.quoteId;
        this.store?.scheduleSave();
        void this.store?.flush();
      }
    }
  }

  private releaseHandoffWaiters(): void {
    const waiting = this.handoffWaiters;
    this.handoffWaiters = [];
    for (const release of waiting) release();
  }

  /** The context of the quote on screen; a quote id that moved without its context following gets a fresh one. */
  private get ctx(): QuoteContext {
    if (this.detached) return QuoteContext.cancelled(this.quoteId);
    const live = this.scope.current;
    return live.id === this.quoteId && !live.stale ? live : this.scope.open(this.quoteId);
  }

  /**
   * The quote on screen changed. What the person left on the old quote goes to
   * that quote (an unsaved terms edit is sent to the id it was typed on) or is
   * dropped; the old context is cancelled, so no read or write still out for it
   * can touch this one; and the work area starts the new quote clean: no error,
   * notice, busy flag, draft, link or figure of the old quote remains.
   */
  private leaveQuote(previousId: string): void {
    this.flushTermsOf(previousId);
    this.scope.open(this.quoteId);
    this.converted = null;
    this.mintedToken = "";
    this.error = "";
    this.notice = "";
    this.floorSpoken = "";
    this.renderRoot.querySelector<HTMLElement & { announce?: (msg: string, kind: "ok") => void }>("hos-pos-scan-box")?.announce?.("", "ok"); // the gun's outcome line is the old quote's
    this.busyHolds = 0;
    this.busy = false;
    this.sendOpen = false;
    this.view = null;
    this.terms = null;
    this.glance = null;
    this.customer = null; // the card shows nothing of the old account until the new one is read
    this.addons = [];
    this.qtyDrafts = {};
    this.discDrafts = {};
    this.addonQty = {};
    this.ticketMoved();
  }

  /**
   * What a guarded write does with its answer when the quote was left while it was out. It is never applied to the
   * quote on screen, but a write that succeeded did change its quote: the screens are told, by that quote's own id,
   * so the list and every open view of it re-read and the person who comes back finds what the server holds. A refusal
   * is said only on the quote it belongs to, when the person is on it again.
   */
  private lateWrite(ctx: QuoteContext, what: string): (late: Result<unknown>) => void {
    return (late) => {
      if (late.ok) {
        this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: ctx.id }, bubbles: true, composed: true }));
        return;
      }
      if (this.ctx.id !== ctx.id) return;
      this.error = spokenRefusal(quoteErrorMessage(late.status, late.error, what));
    };
  }

  /** Hold the Send and add controls while a write is out; the last write to answer lets go. A stale hold was reset with the quote. */
  private holdBusy(ctx: QuoteContext): () => void {
    this.busyHolds += 1;
    this.busy = true;
    let released = false;
    return () => {
      if (released || ctx.stale) return;
      released = true;
      this.busyHolds = Math.max(0, this.busyHolds - 1);
      this.busy = this.busyHolds > 0;
    };
  }

  /**
   * The terms save of each quote the person left with an unsaved edit, until the server has answered it. Coming
   * back to that quote waits on it, so the read never beats the save and the edit is not lost.
   */
  private readonly flushes = new Map<string, Promise<void>>();

  /** The unsaved terms edit of the quote being left is sent to the quote it was typed on, then cleared. */
  private flushTermsOf(previousId: string): void {
    const client = this.client;
    const terms = this.terms;
    const dirty = this.termsPendingCount() > 0;
    const label = this.view?.quote_no ?? previousId;
    if (this.termsTimer !== undefined) {
      clearTimeout(this.termsTimer);
      this.termsTimer = undefined;
    }
    this.termsPending = {};
    this.termsUnsaved = false;
    this.termsEditSeq += 1;
    if (!dirty || client === null || terms === null || previousId === "") return;
    const body = termsBody(terms);
    const sent = client.putTerms(previousId, body).then((result) => {
      // Said plainly, naming the quote it was for, because the person has moved on to another.
      if (!result.ok) this.error = `The terms you changed on ${label} were not saved: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "save the quote terms"))}`;
    });
    this.flushes.set(previousId, sent);
    void sent.then(() => {
      if (this.flushes.get(previousId) === sent) this.flushes.delete(previousId);
    });
  }

  /** Sign out: the unsaved terms edit and the layout are sent while the session still stands. */
  private readonly onSignOut = (event: Event): void => {
    const detail = (event as CustomEvent<SignOutDetail>).detail;
    detail.waits.push(this.flushForSignOut());
  };

  private async flushForSignOut(): Promise<void> {
    const ctx = this.ctx;
    if (this.termsPendingCount() > 0 && ctx.id !== "") await this.putTermsNow();
    await this.store?.flush();
    // A terms save started by a switch is still the person's unsent work: sign out waits for it too (bounded by the host).
    await Promise.all([...this.flushes.values()]);
  }

  private startLoads(): void {
    if (this.client === null) return;
    void this.loadConfig();
    void this.loadSalespeople();
    void this.loadAll();
  }

  /**
   * The wiring pass: the shared components receive their properties here
   * (the position gate holds this app's templates to no property or event
   * bindings), and the uncontrolled inputs are brought to the fresh read's
   * figures. Idempotent on every update.
   */
  protected override updated(): void {
    this.wireShared();
    this.syncFields();
  }

  private wireShared(): void {
    const scan = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-pos-scan-box");
    if (scan !== null) {
      scan.branchId = this.branchId;
    }
    const grid = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-pos-product-grid");
    if (grid !== null) {
      grid.homeBranchId = this.branchId;
      grid.externalTerm = this.term;
      grid.curated = this.config?.policy?.effectiveChips ?? [];
      grid.autoCollapse = true;
      grid.presentation = this.presentation;
      grid.ticketEpoch = this.ticketEpoch;
    }
    const steps = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-pos-flow-steps");
    if (steps !== null) {
      const v = this.view;
      steps.steps = quoteFlowSteps({
        hasCustomer: Boolean(v || this.customer),
        lineCount: v?.lines.length ?? 0,
        sentOrBeyond: Boolean(v && v.status !== "DRAFT"),
      });
      steps.sequential = false;
      steps.navLabel = "Quote sections";
    }
    const picker = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-customer-picker");
    if (picker !== null) {
      picker.branchId = this.branchId;
      if (picker.labelText === undefined || picker.labelText === "") picker.labelText = "Customer";
    }
    // The ticket lines' thumbnails: one per row, keyed by the row's product.
    for (const slot of this.renderRoot.querySelectorAll<HTMLElement>("[data-thumb]")) {
      const productId = slot.getAttribute("data-thumb") ?? "";
      const existing = slot.querySelector("hos-product-thumb");
      if (existing === null) {
        const thumb = document.createElement("hos-product-thumb") as unknown as HTMLElement & Record<string, unknown>;
        thumb.productId = productId;
        thumb.size = 34;
        thumb.alt = "";
        slot.append(thumb);
      }
    }
  }

  /** Bring the fresh read's figures onto the uncontrolled fields. */
  private syncFields(): void {
    const view = this.view;
    // The workspace geometry is this element's own state, set imperatively
    // (the position gate holds the template's style attribute to none).
    const workspace = this.renderRoot.querySelector<HTMLElement>('[data-testid="qb-workspace"]');
    if (workspace !== null) {
      const ws = this.ws;
      workspace.style.gridTemplateColumns = quotePanelTracks(ws.splitPct, ws.arrangement, ws.primaryCollapsed, ws.secondaryCollapsed);
      workspace.setAttribute("aria-label", "Quote workspace");
    }
    // The empty ticket's single cell spans the table whatever the column count.
    const emptyCell = this.renderRoot.querySelector<HTMLElement>('[data-role="empty-cell"]');
    if (emptyCell !== null) emptyCell.setAttribute("colspan", String(this.renderableColumns().length + 1));
    // The checkbox states, owned here rather than in the template.
    const deposit = this.renderRoot.querySelector<HTMLInputElement>('[data-field="deposit-toggle"]');
    if (deposit !== null && this.terms !== null && deposit.checked !== this.terms.depositRequired) {
      deposit.checked = this.terms.depositRequired;
    }
    const images = this.renderRoot.querySelector<HTMLInputElement>('[data-field="include-images"]');
    if (images !== null && this.terms !== null && images.checked !== this.terms.includeImages) {
      images.checked = this.terms.includeImages;
    }
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="channel"]')) {
      const channel = input.getAttribute("data-channel") ?? "";
      if (input.checked !== (this.sendPick[channel] === true)) input.checked = this.sendPick[channel] === true;
    }
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="column"]')) {
      const column = input.getAttribute("data-column") ?? "";
      const row = this.resolvedColumns().find((c) => c.id === column);
      if (row !== undefined && input.checked !== row.visible) input.checked = row.visible;
    }
    const showCost = this.renderRoot.querySelector<HTMLInputElement>('[data-field="show-cost"]');
    if (showCost !== null && showCost.checked !== !this.ws.costHidden) showCost.checked = !this.ws.costHidden;
    // The salesperson select's options follow the roster read.
    const salespeople = this.renderRoot.querySelector<HTMLSelectElement>('[data-field="salesperson"]');
    if (salespeople !== null) {
      const shape = this.salespeople.map((s) => `${s.id}:${s.name}`).join("|");
      if ((salespeople as HTMLElement & { lastShape?: string }).lastShape !== shape) {
        (salespeople as HTMLElement & { lastShape?: string }).lastShape = shape;
        while (salespeople.options.length > 1) salespeople.remove(1);
        for (const s of this.salespeople) {
          const option = document.createElement("option");
          option.value = s.id;
          option.textContent = s.name;
          salespeople.append(option);
        }
      }
    }
    this.buildValiditySelect();
    this.syncTermsBoxes();
    if (view === null) return;
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="qty"]')) {
      const lineNo = Number(input.getAttribute("data-line"));
      const line = view.lines.find((row) => row.line_no === lineNo);
      if (line === undefined) continue;
      const canonical = this.qtyDrafts[lineNo] ?? qtyLabel(line.qty_entered);
      if (input.value !== canonical) input.value = canonical;
    }
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="disc"]')) {
      const lineNo = Number(input.getAttribute("data-line"));
      const extra = this.terms?.lines.find((row) => row.lineNo === lineNo);
      const canonical = this.discDrafts[lineNo] ?? (extra && extra.discPct > 0 ? String(extra.discPct) : "");
      if (input.value !== canonical) input.value = canonical;
    }
    const salesperson = this.renderRoot.querySelector<HTMLSelectElement>('[data-field="salesperson"]');
    if (salesperson !== null && this.terms !== null && salesperson.value !== this.terms.salespersonId) {
      salesperson.value = this.terms.salespersonId;
    }
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-field="addon-qty"]')) {
      const productId = input.getAttribute("data-product") ?? "";
      const addon = this.addons.find((row) => row.product_id === productId);
      if (addon === undefined) continue;
      const canonical = this.addonQty[productId] ?? String(addon.default_qty);
      if (input.value !== canonical) input.value = canonical;
    }
  }

  /**
   * The terms boxes hold the open quote's figures: the server's values once the terms are read, and nothing
   * while they are not (so a box never keeps what the quote before it held). The box under the caret is left alone.
   */
  private syncTermsBoxes(): void {
    for (const input of this.renderRoot.querySelectorAll<HTMLInputElement>('[data-terms-field]')) {
      const canonical = this.termsTextOf(input.getAttribute("data-terms-field") ?? "") ?? "";
      if (input.value !== canonical && !this.isTypingIn(input)) input.value = canonical;
    }
    const deliveryFee = this.renderRoot.querySelector<HTMLInputElement>('[data-field="delivery-fee"]');
    if (deliveryFee !== null) {
      const canonical = this.terms !== null && this.terms.deliveryFeeCents > 0 ? (this.terms.deliveryFeeCents / 100).toFixed(2) : "";
      if (deliveryFee.value !== canonical && !this.isTypingIn(deliveryFee)) deliveryFee.value = canonical;
    }
    const depositPct = this.renderRoot.querySelector<HTMLInputElement>('[data-field="deposit-pct"]');
    if (depositPct !== null) {
      const canonical = this.terms !== null && this.terms.depositPct > 0 ? String(this.terms.depositPct) : "";
      if (depositPct.value !== canonical && !this.isTypingIn(depositPct)) depositPct.value = canonical;
    }
  }

  /** True while the box holds the caret: a render or a late read never rewrites what is being typed. */
  private isTypingIn(input: HTMLInputElement): boolean {
    return this.renderRoot instanceof ShadowRoot && this.renderRoot.activeElement === input;
  }

  private termsTextOf(field: string): string | null {
    const t = this.terms;
    if (t === null) return null;
    if (field === "contact") return t.contactName;
    if (field === "project") return t.projectJob;
    if (field === "po") return t.customerPo;
    if (field === "notes") return t.customerNotes;
    if (field === "delivery-address") return t.deliveryAddress;
    if (field === "requested-date") return t.requestedDate;
    return null;
  }

  // ── loading ────────────────────────────────────────────────────────────────

  private async loadConfig(): Promise<void> {
    if (this.client === null) return;
    const result = await this.client.surfaceConfig(this.branchId);
    if (!result.ok) {
      // Configuration never blocks the screen: the shipped defaults stand, and the store still exists, so a
      // layout change is saved and a refusal of that save is shown rather than silently dropped.
      this.store = this.newStore(null);
      this.layoutError = `The saved layout could not be read, so the shipped layout is shown: ${spokenRefusal(quoteErrorMessage(result.status, result.error, "read the layout"))}`;
      this.wsEcho++;
      return;
    }
    this.config = result.value;
    this.store = this.newStore(result.value);
    this.wsEcho++;
    void this.maybeRestoreOpenQuote(this.store.state.openRef);
  }

  private newStore(config: SurfaceConfig | null): WorkspaceStore {
    const store = new WorkspaceStore(this.client as QdClient, config);
    store.onError = (line) => {
      this.layoutError = line;
    };
    return store;
  }

  /**
   * The resume: a refresh mid-quote comes back to the quote, not to compose.
   * Once per mount, only while nothing is open, and only after the config's
   * own open ref says one was remembered.
   */
  private async maybeRestoreOpenQuote(ref: string): Promise<void> {
    if (this.restoreTried || ref === "" || this.quoteId !== "") return;
    this.restoreTried = true;
    // A quote is being made (or was just made and not yet handed down): that is where the person is going, not the remembered one.
    if (this.creating !== null || this.createdQuoteId !== "") return;
    this.dispatchEvent(new CustomEvent("quote-open", { detail: { quoteId: ref }, bubbles: true, composed: true }));
  }

  private async loadSalespeople(): Promise<void> {
    try {
      this.salespeople = await listSalespeople();
    } catch {
      this.salespeople = []; // the selector degrades to the empty option, never an error
    }
  }

  /** The quantity or discount box holding focus, with what is in it, or null. */
  private typingDraft(): { field: "qty" | "disc"; line: number; value: string } | null {
    const active = this.renderRoot.querySelector<HTMLInputElement>('[data-field="qty"]:focus, [data-field="disc"]:focus');
    if (active === null) return null;
    const field = active.getAttribute("data-field") === "disc" ? "disc" : "qty";
    return { field, line: Number(active.getAttribute("data-line")), value: active.value };
  }

  /** The newest read of the quote on screen: a read that finds itself superseded hands its caller this one to wait on. */
  private newestRead: Promise<void> = Promise.resolve();

  async loadAll(): Promise<void> {
    const read = this.readAll();
    this.newestRead = read;
    return read;
  }

  private async readAll(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const ctx = this.ctx;
    if (ctx.stale) return;
    if (ctx.id === "") {
      this.view = null;
      this.terms = null;
      this.termsUnsaved = false;
      this.termsPending = {};
      this.glance = null;
      this.addons = [];
      return;
    }
    // Each edit reads twice (this panel's own and the builder's fan-out): the later read supersedes the earlier,
    // so an older answer that arrives last never lands over a newer one.
    const seq = ctx.startRead();
    // A refresh never CLEARS the error here: a refusal the user has not read
    // yet must survive the re-read that follows it; errors clear on the next
    // user gesture. An answer for a quote that is no longer open says nothing.
    const view = await ctx.guardedRead(seq, client.readQuote(ctx.id));
    if (view === undefined) return this.supersededBy(ctx, seq);
    if (!view.ok) {
      this.error = "Could not read this quote.";
      return;
    }
    this.view = view.value;
    // Drafts return to the server's truth after a re-read, except the box the
    // person is typing in right now: a late re-read must never wipe the figure
    // under their hands, or the blur that should save it sees an empty box.
    const typing = this.typingDraft();
    this.qtyDrafts = typing !== null && typing.field === "qty" ? { [typing.line]: typing.value } : {};
    this.discDrafts = typing !== null && typing.field === "disc" ? { [typing.line]: typing.value } : {};
    if (this.pendingFocusScan) {
      // The mandatory first action (picking the customer) just finished: the
      // next action is always add products, so the scanner gun's box takes
      // the caret.
      this.pendingFocusScan = false;
      void this.updateComplete.then(() => this.focusScan());
    }
    // A terms edit made on this quote before the person left it may still be on its way to the server: the read waits for it.
    const unsent = this.flushes.get(ctx.id);
    if (unsent !== undefined) {
      const landed = await waitUpTo(UNSENT_WAIT_MS, unsent);
      if (ctx.stale || !ctx.isLatestRead(seq)) return this.supersededBy(ctx, seq);
      if (!landed) {
        this.error = STILL_SAVING;
        // The read went ahead without the save: when the save does answer, the quote is read again, so the change appears and the line above stops being true.
        void unsent.then(() => {
          if (ctx.stale) return;
          if (this.error === STILL_SAVING) this.error = "";
          void this.loadAll();
        });
      }
    }
    const terms = await ctx.guardedRead(seq, client.readTerms(ctx.id));
    if (terms === undefined) return this.supersededBy(ctx, seq);
    if (terms.ok) {
      // A read that was in flight when the person edited must not land over the edit: the unsaved edits ride on top.
      this.terms = { ...terms.value, ...this.termsPending };
      this.termsUnsaved = this.termsPendingCount() > 0;
    }
    // The margin glance: byte absent fields for a refused caller. An empty
    // object means no margin surface for you, never an error.
    const glance = await ctx.guardedRead(seq, client.marginGlance(ctx.id));
    if (glance === undefined) return this.supersededBy(ctx, seq);
    if (glance.ok) this.glance = glance.value;
    void this.loadAddons(ctx, seq);
    void this.hydrateCustomer(ctx, seq);
  }

  /** A read that was dropped because a newer read of the same quote began finishes when that newer read does; one dropped because the quote was left finishes at once. */
  private supersededBy(ctx: QuoteContext, seq: number): Promise<void> {
    return !ctx.stale && !ctx.isLatestRead(seq) ? this.newestRead : Promise.resolve();
  }

  /** The identity card's credit facts: best effort search by name matched on id. */
  private async hydrateCustomer(ctx: QuoteContext, seq: number): Promise<void> {
    const v = this.view;
    if (v === null) return;
    if (this.customer?.id === v.customer_id) return;
    this.customer = null;
    const name = v.customer_name.trim();
    if (name === "") return;
    try {
      const hits = await ctx.guardedRead(seq, searchCustomers(name, 10, this.branchId));
      if (hits === undefined) return;
      this.customer = hits.find((h) => h.id === v.customer_id) ?? null;
    } catch {
      if (!ctx.stale && ctx.isLatestRead(seq)) this.customer = null; // the card renders without the credit line, honest absence
    }
  }

  /** The add-ons strip: the quote's lines feed the same suggestions read the counter uses. */
  private async loadAddons(ctx: QuoteContext, seq: number): Promise<void> {
    const ids = (this.view?.lines ?? []).map((l) => l.product_id).filter(Boolean);
    if (ids.length === 0) {
      this.addons = [];
      return;
    }
    try {
      const rows = await ctx.guardedRead(seq, addonSuggestions(ids));
      if (rows !== undefined) this.addons = rows;
    } catch {
      if (!ctx.stale && ctx.isLatestRead(seq)) this.addons = []; // empty never error: the strip hides entirely
    }
  }

  // ── the quote-changed fan-out ──────────────────────────────────────────────

  private quoteChanged(): void {
    this.dispatchEvent(new CustomEvent("quote-changed", { detail: { quoteId: this.quoteId }, bubbles: true, composed: true }));
  }

  /** Public: the builder's fan-out reaches this panel too. */
  reload(): void {
    void this.loadAll();
  }

  // ── the host delegation: one click reader, one input reader ────────────────

  private readonly onActionClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    const line = Number(control.getAttribute("data-line") ?? "");
    const product = control.getAttribute("data-product") ?? "";
    switch (action) {
      case "new-quote":
        this.startNewQuote();
        break;
      case "help":
        this.helpOpen = !this.helpOpen;
        break;
      case "columns":
        this.columnsOpen = !this.columnsOpen;
        break;
      case "view-cards":
        this.presentation = "CARDS";
        break;
      case "view-rows":
        this.presentation = "ROWS";
        break;
      case "columns-reset":
        void this.resetColumns();
        break;
      case "column-move": {
        const column = control.getAttribute("data-column") ?? "";
        this.moveColumn(column, control.getAttribute("data-dir") === "up" ? -1 : 1);
        break;
      }
      case "swap":
        this.swapPanels();
        break;
      case "collapse-primary":
        this.toggleCollapse("primary");
        break;
      case "collapse-secondary":
        this.toggleCollapse("secondary");
        break;
      case "restore-primary":
        this.toggleCollapse("primary");
        break;
      case "restore-secondary":
        this.toggleCollapse("secondary");
        break;
      case "reset-layout":
        void this.resetWorkspace();
        break;
      case "settings":
        // The settings region lives under the quote detail disclosure; the
        // builder answers this event by opening it.
        window.dispatchEvent(new CustomEvent(OPEN_SETTINGS_EVENT));
        break;
      case "add-another":
        this.focusScan();
        this.renderRoot.querySelector("hos-pos-scan-box")?.scrollIntoView({ behavior: "smooth", block: "center" });
        this.notice = "Ready for the next item.";
        break;
      case "addon-add": {
        const addon = this.addons.find((row) => row.product_id === product);
        if (addon !== undefined) {
          void this.addProduct({ id: addon.product_id, uom: addon.uom, name: addon.name }, Math.max(1, Number(this.addonQty[product] ?? addon.default_qty) || 1));
        }
        break;
      }
      case "qty-step": {
        const dir = control.getAttribute("data-dir") === "up" ? 1 : -1;
        const lineRow = this.view?.lines.find((row) => row.line_no === line);
        if (lineRow !== undefined) this.stepQty(lineRow, dir);
        break;
      }
      case "line-remove": {
        const lineRow = this.view?.lines.find((row) => row.line_no === line);
        if (lineRow !== undefined) void this.removeLine(lineRow);
        break;
      }
      case "identity-change":
        this.startNewQuote();
        break;
      case "fulfil-pickup":
        this.termsEdit({ fulfillment: "PICKUP" });
        break;
      case "fulfil-delivery":
        this.termsEdit({ fulfillment: "DELIVERY" });
        break;
      case "save-draft":
        void this.loadAll();
        this.notice = "Saved. Every change on this screen saves as you make it.";
        break;
      case "copy-link":
        void this.doCopyLink();
        break;
      case "preview":
        void this.doPreview(false);
        break;
      case "print":
        void this.doPreview(true);
        break;
      case "send":
        void this.doSend();
        break;
      case "send-channel":
        this.sendOpen = !this.sendOpen;
        break;
      case "convert":
        void this.doConvert();
        break;
      default:
        break;
    }
  };

  private readonly onFieldInput = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (!(control instanceof HTMLInputElement)) return;
    const field = control.getAttribute("data-field") ?? "";
    const line = Number(control.getAttribute("data-line") ?? "");
    const product = control.getAttribute("data-product") ?? "";
    if (field === "qty") this.qtyDrafts = { ...this.qtyDrafts, [line]: control.value };
    if (field === "disc") this.discDrafts = { ...this.discDrafts, [line]: control.value };
    if (field === "addon-qty") this.addonQty = { ...this.addonQty, [product]: control.value };
    if (field.startsWith("terms-")) this.termsEditPatch(control.getAttribute("data-terms-field") ?? "", control.value);
  };

  private readonly onFieldChange = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (control === undefined) return;
    const field = control.getAttribute("data-field") ?? "";
    const line = Number(control.getAttribute("data-line") ?? "");
    if (control instanceof HTMLInputElement) {
      if (field === "qty") {
        const lineRow = this.view?.lines.find((row) => row.line_no === line);
        if (lineRow !== undefined) void this.saveQty(lineRow, control.value);
      }
      if (field === "disc") {
        const lineRow = this.view?.lines.find((row) => row.line_no === line);
        if (lineRow !== undefined) void this.saveDisc(lineRow, control.value);
      }
      if (field === "channel") {
        const channel = control.getAttribute("data-channel") ?? "";
        this.sendPick = { ...this.sendPick, [channel]: control.checked };
      }
      if (field === "delivery-fee") {
        const fee = parseDeliveryFee(control.value);
        if ("error" in fee) {
          // Never saved as zero: refused in words, and the box returns to the saved figure.
          this.error = fee.error;
          this.requestUpdate();
        } else {
          this.termsEdit({ deliveryFeeCents: fee.cents });
        }
      }
      if (field === "deposit-pct") {
        const n = Number(control.value);
        if (this.terms !== null) {
          this.termsEdit({ depositPct: Number.isInteger(n) && n >= 0 && n <= 100 ? n : this.terms.depositPct });
        }
      }
      if (field === "deposit-toggle") {
        const on = control.checked;
        const patch: Partial<TermsRef> = { depositRequired: on };
        if (on && this.terms !== null && this.terms.depositPct === 0 && this.config?.policy?.defaultDepositPct) {
          (patch as { depositPct?: number }).depositPct = this.config.policy.defaultDepositPct;
        }
        this.termsEdit(patch);
      }
      if (field === "include-images") {
        this.termsEdit({ includeImages: control.checked });
      }
      if (field === "column") {
        this.toggleColumn(control.getAttribute("data-column") ?? "");
      }
      if (field === "show-cost") {
        this.toggleCost();
      }
      return;
    }
    if (control instanceof HTMLSelectElement) {
      if (field === "salesperson") this.termsEdit({ salespersonId: control.value });
      if (field === "validity") void this.saveValidity(Number(control.value.replace(" days", "")));
      if (field.startsWith("terms-")) this.termsEditPatch(control.getAttribute("data-terms-field") ?? "", control.value);
      if (field.startsWith("terms-")) this.termsEditPatch(control.getAttribute("data-terms-field") ?? "", control.value);
    }
  };

  private readonly onKey = (event: KeyboardEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-role"));
    if (control?.getAttribute("data-role") === "divider") this.onDividerKey(event);
    // Escape closes an open popover (help, columns, send channels) and returns to the control that opened it.
    if (event.key === "Escape" && (this.columnsOpen || this.sendOpen || this.helpOpen)) {
      const opener = this.columnsOpen ? "qb-columns-btn" : this.sendOpen ? "qb-send-channel" : "qb-help";
      this.columnsOpen = false;
      this.sendOpen = false;
      this.helpOpen = false;
      event.stopPropagation();
      void this.updateComplete.then(() => this.renderRoot.querySelector<HTMLElement>(`[data-testid="${opener}"]`)?.focus());
      return;
    }
    // The commit chord, the counter's muscle memory: Ctrl or Cmd and Enter
    // sends, from inside any field.
    if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && this.isConnected && this.offsetParent !== null) {
      const v = this.view;
      if (v !== null && (v.status === "DRAFT" || v.status === "SENT") && !v.expired) {
        event.preventDefault();
        void this.doSend();
      }
    }
  };

  private readonly onOutsidePointerDown = (event: Event): void => {
    if (!this.columnsOpen && !this.sendOpen && !this.helpOpen) return;
    const path = event.composedPath();
    const inside = (selector: string) => {
      const el = this.renderRoot.querySelector(selector);
      return el !== null && path.includes(el);
    };
    if (this.columnsOpen && !inside(".colwrap")) this.columnsOpen = false;
    if (this.sendOpen && !inside(".sendwrap")) this.sendOpen = false;
    if (this.helpOpen && !inside(".helpwrap")) this.helpOpen = false;
  };

  /** Select all on focus for the keyed draft fields, so typing replaces. */
  private readonly onFieldFocus = (event: FocusEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (control instanceof HTMLInputElement) {
      const field = control.getAttribute("data-field") ?? "";
      if (field === "qty" || field === "disc" || field === "addon-qty" || field === "delivery-fee" || field === "deposit-pct") {
        control.select();
      }
    }
  };

  // ── the shared components' events ──────────────────────────────────────────

  private readonly onTermChanged = (event: CustomEvent<{ term: string }>): void => {
    this.term = event.detail.term;
  };

  private readonly onScanResolved = (event: CustomEvent<{ product: PosProduct }>): void => {
    void this.addProduct({ id: event.detail.product.id, uom: event.detail.product.base_uom, name: event.detail.product.name });
  };

  private readonly onAddProduct = (event: CustomEvent<{ product: PosProduct }>): void => {
    void this.addProduct({ id: event.detail.product.id, uom: event.detail.product.base_uom, name: event.detail.product.name });
  };

  private readonly onFlowGoto = (event: CustomEvent<{ step: string }>): void => {
    const target =
      event.detail.step === "customer"
        ? '[data-testid="qb-identity"]'
        : event.detail.step === "items"
          ? ".panel.lines"
          : '[data-testid="qb-totals"]';
    this.renderRoot.querySelector<HTMLElement>(target)?.scrollIntoView({ behavior: "smooth", block: "start" });
  };

  /**
   * The picker's pick: a quote needs the account, and the inline picker
   * has no walk-in row.
   */
  private readonly onCustomerPicked = (event: CustomEvent<PickerCustomer>): void => {
    void this.createFor(event.detail);
  };

  // ── quote mutations ────────────────────────────────────────────────────────

  private focusScan(): void {
    const box = this.renderRoot.querySelector<HTMLElement & { focusInput?: () => void }>("hos-pos-scan-box");
    box?.focusInput?.();
  }

  /**
   * The one outcome line beside the gun: the scan box's own message region
   * when it is mounted, the notice lines otherwise.
   */
  private speakAdd(msg: string, kind: "ok" | "warn" | "err"): void {
    const box = this.renderRoot.querySelector<HTMLElement & { announce?: (msg: string, kind: "ok" | "warn" | "err") => void }>("hos-pos-scan-box");
    if (box?.announce) {
      box.announce(msg, kind);
    } else if (kind === "ok") {
      this.notice = msg;
    } else {
      this.error = msg;
    }
  }

  /** The quote being created and the read of the quote just opened: a scan that arrives first waits for them. */
  private creating: Promise<void> | null = null;
  private opening: Promise<void> = Promise.resolve();
  private createdQuoteId = "";

  /** Scans waiting for the host to hand a quote id down: released the moment this.quoteId changes. */
  private handoffWaiters: Array<() => void> = [];

  private handedDown(): Promise<void> {
    return new Promise<void>((resolve) => {
      this.handoffWaiters.push(resolve);
    });
  }

  private async addProduct(p: { id: string; uom: string; name?: string }, qty = 1): Promise<void> {
    const client = this.client;
    if (client === null) return;
    // A scan right after the customer is picked lands while the quote is still being made or read: it waits
    // for them rather than being told to pick a customer who has just been picked.
    // Only while compose is on screen: a scan on an open quote is for that quote and waits for no create.
    if (this.creating !== null && this.quoteId === "") {
      await this.creating;
      // The host hands the new quote id down on its own render: wait for it to arrive, however long that takes (bounded, and said when it runs out).
      if (this.createdQuoteId !== "" && this.quoteId === "" && !(await waitUpTo(HANDOFF_WAIT_MS, this.handedDown()))) {
        this.speakAdd("The new quote did not open in time, so that scan was not added. Scan it again once the quote is on screen.", "warn");
        return;
      }
      await this.updateComplete;
      // The person opened a quote that is not the one just made while the scan waited: it was meant for the new quote.
      if (this.quoteId !== "" && this.quoteId !== this.createdQuoteId) {
        this.speakAdd("That scan was meant for the new quote, which another quote overtook, so it was not added. Scan it again on the quote you want.", "warn");
        return;
      }
    }
    const ctx = this.ctx;
    if (ctx.id !== "" && this.view === null) {
      await this.opening;
      if (ctx.stale) return;
    }
    if (ctx.id === "" || this.view === null) {
      this.speakAdd("Pick a customer first. The quote starts there.", "warn");
      return;
    }
    if (!canEdit(this.view, EDIT_LINE_ADD)) {
      this.speakAdd(`This quote is ${quoteStatusPhrase(this.view.status)} and can no longer be changed.`, "err");
      return;
    }
    const release = this.holdBusy(ctx);
    this.error = "";
    this.notice = "";
    const result = await ctx.guarded(client.addLine(ctx.id, p.id, String(qty), p.uom), this.lateWrite(ctx, "add this line"));
    if (result === undefined) return;
    release();
    if (result.ok) {
      this.speakAdd(p.name !== undefined ? `Added ${p.name}` : "Line added.", "ok");
      await this.afterCartChange(ctx);
    } else {
      // The refusal belongs beside the gun, once.
      this.speakAdd(spokenRefusal(quoteErrorMessage(result.status, result.error, "add this line")), "err");
      await this.loadAll();
    }
  }

  private async saveQty(line: QuoteViewLine, raw: string): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const qty = raw.trim();
    if (qty === "" || Number(qty) <= 0 || !Number.isFinite(Number(qty))) {
      this.error = "Quantity must be a positive number.";
      return;
    }
    const ctx = this.ctx;
    const release = this.holdBusy(ctx);
    this.error = "";
    const result = await ctx.guarded(client.editLine(ctx.id, line.line_no, { qty }), this.lateWrite(ctx, "change this quantity"));
    if (result === undefined) return;
    release();
    if (result.ok) {
      this.ticketMoved();
      await this.afterCartChange(ctx);
    } else {
      // The core may answer the typed floor refusal on this arm too: it is parsed like the discount arm's.
      const refusal = result.status === 409 ? parseFloorRefusal(result.error) : null;
      if (refusal !== null) {
        this.error = floorRefusalMessage(refusal);
        this.floorSpoken = this.error;
        openFloorRefusalDialog(refusal, ctx.id, line.line_no, this.termsLine(line.line_no)?.discPct ?? 0);
      } else {
        this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "change this quantity"));
      }
      await this.loadAll();
    }
  }

  private stepQty(line: QuoteViewLine, dir: 1 | -1): void {
    const cur = Number(this.qtyDrafts[line.line_no] ?? line.qty_entered);
    const next = Math.max(1, (Number.isFinite(cur) ? cur : 1) + dir);
    void this.saveQty(line, String(next));
  }

  /**
   * The discount arm: where the floor decision runs. A 409 may carry the
   * typed refusal body as the error string; it is parsed field by field,
   * spoken as the plain message (the approvers, the branch manager way
   * forward, the figures when the server sent them), and handed to the
   * refusal dialog seam (the third coder's panel).
   */
  private async saveDisc(line: QuoteViewLine, raw: string): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const n = Number(raw === "" ? 0 : raw);
    if (!Number.isInteger(n) || n < 0 || n > 100) {
      this.error = "Discount must be a whole percent from 0 to 100.";
      return;
    }
    const ctx = this.ctx;
    const release = this.holdBusy(ctx);
    this.error = "";
    const result = await ctx.guarded(client.editLine(ctx.id, line.line_no, { discPct: n }), this.lateWrite(ctx, "change this discount"));
    if (result === undefined) return;
    release();
    if (result.ok) {
      this.ticketMoved();
      await this.afterCartChange(ctx);
      return;
    }
    if (result.status === 409) {
      const refusal = parseFloorRefusal(result.error);
      if (refusal !== null) {
        this.error = floorRefusalMessage(refusal);
        this.floorSpoken = this.error;
        openFloorRefusalDialog(refusal, ctx.id, line.line_no, n);
        await this.loadAll();
        return;
      }
    }
    this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "change this discount"));
    await this.loadAll();
  }

  /**
   * The quote's change counter, handed to the product grid: the grid's echo
   * guard is only meaningful while the line it created is still there.
   * Adding does not bump it; every other line level change and a new
   * document does.
   */
  @state() private ticketEpoch = 0;

  private ticketMoved(): void {
    this.ticketEpoch++;
  }

  private async removeLine(line: QuoteViewLine): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const ctx = this.ctx;
    const release = this.holdBusy(ctx);
    this.error = "";
    const result = await ctx.guarded(client.removeLine(ctx.id, line.line_no), this.lateWrite(ctx, "remove this line"));
    if (result === undefined) return;
    release();
    if (result.ok) {
      this.ticketMoved();
      await this.afterCartChange(ctx);
    } else {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "remove this line"));
      await this.loadAll();
    }
  }

  // ── terms ──────────────────────────────────────────────────────────────────

  /** True from a terms edit until the server has answered its save: the totals card then shows local figures, labelled. */
  @state() private termsUnsaved = false;

  /** The edits the server has not yet answered, laid over every read until its save lands. */
  private termsPending: Partial<TermsRef> = {};
  private termsEditSeq = 0;

  private termsPendingCount(): number {
    return Object.keys(this.termsPending).length;
  }

  private termsEdit(patch: Partial<TermsRef>): void {
    if (this.terms === null) return;
    this.termsUnsaved = true;
    this.termsEditSeq += 1;
    this.termsPending = { ...this.termsPending, ...patch };
    this.terms = { ...this.terms, ...patch };
    if (this.termsTimer !== undefined) clearTimeout(this.termsTimer);
    this.termsTimer = setTimeout(() => void this.putTermsNow(true), 600);
  }

  /** The validity select's options: the shipped choices plus the stored day when it sits off the list. */
  private buildValiditySelect(): void {
    const select = this.renderRoot.querySelector<HTMLSelectElement>('[data-field="validity"]');
    if (select === null) return;
    const days = this.view !== null ? validityDaysOf(this.view.valid_until, this.clockNow) : null;
    const choices: number[] = [...VALIDITY_CHOICES];
    if (days !== null && !choices.includes(days)) choices.push(days);
    const wanted = days !== null ? `${days} days` : "";
    const shape = choices.map((d) => `${d} days`).join("|") + "#" + (select.value === "" ? wanted : select.value);
    if ((select as HTMLElement & { lastShape?: string }).lastShape !== shape) {
      (select as HTMLElement & { lastShape?: string }).lastShape = shape;
      select.textContent = "";
      for (const d of choices) {
        const option = document.createElement("option");
        option.value = `${d} days`;
        option.textContent = `${d} days`;
        select.append(option);
      }
    }
    const canonical = select.value === "" || !choices.map((d) => `${d} days`).includes(select.value) ? wanted : select.value;
    if (canonical !== "" && select.value !== canonical) select.value = canonical;
  }

  private termsEditPatch(field: string, value: string): void {
    if (field === "contact") this.termsEdit({ contactName: value });
    if (field === "project") this.termsEdit({ projectJob: value });
    if (field === "po") this.termsEdit({ customerPo: value });
    if (field === "notes") this.termsEdit({ customerNotes: value });
    if (field === "delivery-address") this.termsEdit({ deliveryAddress: value });
    if (field === "requested-date") this.termsEdit({ requestedDate: value });
  }

  private async putTermsNow(reloadAfter = false): Promise<void> {
    const client = this.client;
    const t = this.terms;
    const ctx = this.ctx;
    if (client === null || t === null || ctx.id === "") return;
    if (this.termsTimer !== undefined) {
      clearTimeout(this.termsTimer);
      this.termsTimer = undefined;
    }
    const sent = this.termsEditSeq;
    const result = await ctx.guarded(client.putTerms(ctx.id, termsBody(t)), this.lateWrite(ctx, "save the quote terms"));
    // The quote changed while this save was out: its answer is not this screen's to show (leaving the quote already sent any later edit to its own quote).
    if (result === undefined) return;
    // What was edited while this save was in flight is still unsaved and stays laid over the answer.
    if (this.termsEditSeq === sent) this.termsPending = {};
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "save the quote terms"));
      return;
    }
    this.terms = { ...result.value, ...this.termsPending };
    this.termsUnsaved = this.termsPendingCount() > 0;
    if (reloadAfter) {
      await this.loadAll();
      if (ctx.stale) return;
      this.quoteChanged();
    }
  }

  private async saveValidity(days: number): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const ctx = this.ctx;
    const release = this.holdBusy(ctx);
    this.error = "";
    const result = await ctx.guarded(client.setValidUntil(ctx.id, validUntilFromDays(days, this.clockNow)), this.lateWrite(ctx, "change the expiry date"));
    if (result === undefined) return;
    release();
    if (result.ok) {
      await this.loadAll();
      if (ctx.stale) return;
      this.quoteChanged();
    } else {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "change the expiry date"));
    }
  }

  /** After any cart change: refresh the terms (the server restamps the totals), then re-read. */
  private async afterCartChange(ctx: QuoteContext): Promise<void> {
    if (this.terms !== null && this.view !== null && canEdit(this.view, EDIT_LINE_QTY)) {
      await this.putTermsNow();
      if (ctx.stale) return;
    }
    await this.loadAll();
    if (ctx.stale) return;
    this.quoteChanged();
  }

  // ── the workspace (resize, collapse, swap; saved per person) ───────────────

  private get ws(): WorkspaceStore["state"] {
    return this.store?.state ?? {
      splitPct: 58,
      arrangement: "PRIMARY_LEFT" as QuoteArrangement,
      primaryCollapsed: false,
      secondaryCollapsed: false,
      costHidden: false,
      openRef: "",
      source: "dealer" as const,
    };
  }

  private wsTouch(): void {
    if (this.store !== null) this.store.state.openRef = this.quoteId;
    this.store?.scheduleSave();
    this.wsEcho++;
  }

  private swapPanels(): void {
    const ws = this.ws;
    ws.arrangement = ws.arrangement === "PRIMARY_LEFT" ? "PRIMARY_RIGHT" : "PRIMARY_LEFT";
    this.wsTouch();
  }

  private toggleCollapse(which: "primary" | "secondary"): void {
    const ws = this.ws;
    const next = collapseNext(which, ws.primaryCollapsed, ws.secondaryCollapsed);
    ws.primaryCollapsed = next.primaryCollapsed;
    ws.secondaryCollapsed = next.secondaryCollapsed;
    this.wsTouch();
  }

  private toggleCost(): void {
    this.ws.costHidden = !this.ws.costHidden;
    this.wsTouch();
  }

  private async resetWorkspace(): Promise<void> {
    await this.store?.reset();
    this.wsEcho++; // a refused reset left the screen as it was; the store said why
  }

  private readonly onDividerPointerDown = (event: PointerEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.getAttribute("data-role") === "divider");
    if (control === undefined) return;
    this.dragging = true;
    try {
      (control as HTMLElement).setPointerCapture?.(event.pointerId);
    } catch {
      /* capture refused: the drag still tracks through the host listener */
    }
  };

  private readonly onDividerPointerMove = (event: PointerEvent): void => {
    if (!this.dragging) return;
    const box = this.renderRoot.querySelector<HTMLElement>(".workspace");
    if (box === null) return;
    const r = box.getBoundingClientRect();
    const next = quoteSplitFromPointer(event.clientX, r.left, r.width, this.ws.arrangement);
    if (next !== null && next !== this.ws.splitPct) {
      this.ws.splitPct = next;
      this.requestUpdate();
    }
  };

  private readonly onDividerPointerUp = (): void => {
    if (!this.dragging) return;
    this.dragging = false;
    this.wsTouch();
  };

  private onDividerKey(e: KeyboardEvent): void {
    const coarse = e.shiftKey || e.key === "PageUp" || e.key === "PageDown";
    const step = coarse ? 10 : 2;
    let next: number | null = null;
    const leftIsPrimary = this.ws.arrangement === "PRIMARY_LEFT";
    if (e.key === "ArrowLeft" || e.key === "PageDown") next = this.ws.splitPct + (leftIsPrimary ? -step : step);
    else if (e.key === "ArrowRight" || e.key === "PageUp") next = this.ws.splitPct + (leftIsPrimary ? step : -step);
    else if (e.key === "Home") next = 20;
    else if (e.key === "End") next = 80;
    if (next === null) return;
    e.preventDefault();
    this.ws.splitPct = Math.min(80, Math.max(20, next));
    this.requestUpdate();
    this.wsTouch();
  }

  // ── per person column curation (whole or absent, reset is a delete) ────────

  /** The columns as asked for, held from the press until the server has answered, so a render in between keeps the box as pressed. */
  private columnsDraft: ResolvedItem[] | null = null;

  private resolvedColumns(): ResolvedItem[] {
    if (this.columnsDraft !== null) return this.columnsDraft;
    return effectiveItems("column", QB_COLUMNS, this.config?.roleItems, this.config?.userItems);
  }

  private async putColumns(next: ResolvedItem[]): Promise<void> {
    const client = this.client;
    if (client === null) return;
    this.columnsDraft = next;
    this.requestUpdate();
    const saved = await client.putMyItems(next.map((c) => ({ kind: "column", id: c.id, position: c.position, visible: c.visible })));
    if (!saved.ok) {
      this.columnsDraft = null;
      this.columnsError = `The columns were not saved: ${spokenRefusal(quoteErrorMessage(saved.status, saved.error, "save the columns"))}`;
      return;
    }
    this.columnsError = "";
    // The save landed. The box keeps what was saved until a good read of the config brings the same figures:
    // a failed re-read must not hand the box back to the state before the save.
    const config = await client.surfaceConfig(this.branchId);
    if (config.ok) {
      this.config = config.value;
      this.columnsDraft = null;
    }
  }

  private toggleColumn(id: string): void {
    const cols = this.resolvedColumns().map((c) => (c.id === id ? { ...c, visible: !c.visible } : c));
    void this.putColumns(cols);
  }

  private moveColumn(id: string, dir: -1 | 1): void {
    const cols = this.resolvedColumns();
    const i = cols.findIndex((c) => c.id === id);
    const j = i + dir;
    if (i < 0 || j < 0 || j >= cols.length) return;
    const next = [...cols];
    [next[i], next[j]] = [next[j], next[i]];
    void this.putColumns(next);
  }

  private async resetColumns(): Promise<void> {
    const client = this.client;
    if (client === null) return;
    const reset = await client.deleteMyItems();
    if (!reset.ok) {
      this.columnsError = `The columns were not reset: ${spokenRefusal(quoteErrorMessage(reset.status, reset.error, "reset the columns"))}`;
      return;
    }
    this.columnsError = "";
    const config = await client.surfaceConfig(this.branchId);
    if (config.ok) {
      this.config = config.value;
      this.columnsDraft = null; // a draft left by an earlier press must not outlive the reset
    } else {
      // The delete landed but the read did not: the columns are the role's own now, and the box says so until a good read.
      this.columnsDraft = effectiveItems("column", QB_COLUMNS, this.config?.roleItems, undefined);
    }
  }

  /** The visible columns for THIS render: curation first, then the server's cost gate on top. */
  private renderableColumns(): ResolvedItem[] {
    const costOn = marginPresent(this.glance) && !this.ws.costHidden;
    return visibleItems(this.resolvedColumns()).filter((c) => (c.cls === "cost" ? costOn : true));
  }

  // ── compose (a new quote) and the send and convert paths ───────────────────

  private createFor(cust: PickerCustomer): Promise<void> {
    const made = this.createForNow(cust).finally(() => {
      if (this.creating === made) this.creating = null;
    });
    this.creating = made;
    return made;
  }

  private async createForNow(cust: PickerCustomer): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    const release = this.holdBusy(ctx);
    this.error = "";
    this.createdQuoteId = "";
    const days = this.config?.policy?.defaultValidityDays || 30;
    const result = await ctx.guarded(client.createQuote(cust.id, this.branchId, validUntilFromDays(days, this.clockNow)));
    if (result === undefined) {
      // The person opened another quote while this one was being made: the screen stays where they went, and
      // the list is told so the new draft shows in it.
      this.quoteChanged();
      return;
    }
    release();
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "create a quote"));
      return;
    }
    this.customer = cust;
    // A DIFFERENT document now sits under the grid: everything the grid's
    // guard is holding belongs to the quote that just left.
    this.ticketMoved();
    this.pendingFocusScan = true;
    this.createdQuoteId = result.value;
    this.dispatchEvent(new CustomEvent("quote-open", { detail: { quoteId: result.value }, bubbles: true, composed: true }));
  }

  /** A new quote starts from nothing: the quote's own state resets, the operator's settings stay. */
  private startNewQuote(): void {
    Object.assign(this, freshQuoteState());
    this.dispatchEvent(new CustomEvent("quote-open", { detail: { quoteId: "" }, bubbles: true, composed: true }));
  }

  /**
   * The quote's share link, minted once. It is minted for the quote the context
   * names, and kept only while that quote is still the one on screen: a mint
   * that lands after the person moved on is dropped, never held for the next quote.
   */
  private async ensureLink(ctx: QuoteContext): Promise<string> {
    const client = this.client;
    if (client === null || this.mintedToken !== "") return this.mintedToken;
    const result = await ctx.guarded(client.mintShareLink(ctx.id), this.lateWrite(ctx, "create a share link"));
    if (result === undefined) throw new StaleQuoteError();
    if (!result.ok) {
      throw new Error(spokenRefusal(quoteErrorMessage(result.status, result.error, "create a share link")));
    }
    this.mintedToken = result.value;
    return result.value;
  }

  /**
   * The share URL's shape is the reference's own (the customer quote page on
   * its token). Where the public desk's bundle is served from is the deploy
   * lane's to settle; the token is the credential and it never rides a
   * place a credential should not.
   */
  private shareUrl(token: string, print = false): string {
    return `${location.origin}/quote.html?t=${encodeURIComponent(token)}${print ? "&print=1" : ""}`;
  }

  private async doCopyLink(): Promise<void> {
    const ctx = this.ctx;
    try {
      const token = await this.ensureLink(ctx);
      // The clipboard is only there on a secure page: say so rather than claim a copy that did not happen.
      if (navigator.clipboard === undefined) throw new Error("The link was created but could not be copied from this page. Use Preview to open it and copy it from there.");
      await navigator.clipboard.writeText(this.shareUrl(token));
      // Creating a link emails nothing: sending is a different button.
      if (ctx.stale) return;
      this.notice = "Link copied. Nothing was emailed. Paste the link, or use Send quote.";
      await this.loadAll();
      if (ctx.stale) return;
      this.quoteChanged();
    } catch (e) {
      if (ctx.stale) return;
      this.error = e instanceof Error ? e.message : "Could not create the link.";
    }
  }

  private async doPreview(print: boolean): Promise<void> {
    const ctx = this.ctx;
    try {
      // ensureLink throws once the quote has been left, so the lines below only run on the quote that asked.
      const token = await this.ensureLink(ctx);
      window.open(this.shareUrl(token, print), "_blank", "noopener");
      await this.loadAll();
      if (ctx.stale) return;
      this.quoteChanged();
    } catch (e) {
      if (ctx.stale) return;
      this.error = e instanceof Error ? e.message : "Could not open the preview.";
    }
  }

  private async doSend(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy) return;
    const ctx = this.ctx;
    // One send at a time: a second press while the first is out would mint a second live link and start the cadence twice.
    const release = this.holdBusy(ctx);
    this.sendOpen = false;
    this.error = "";
    this.notice = "";
    const channels = sendChannels(this.#sendPolicy()).filter((c) => this.sendPick[c]);
    try {
      // An edit still waiting on its save goes first: the quote the customer is sent holds what was typed.
      if (this.termsPendingCount() > 0) {
        await this.putTermsNow();
        if (ctx.stale) return;
        if (this.error !== "") return;
      }
      await this.ensureLink(ctx); // the core mints a link, moves DRAFT to SENT and starts the cadence; it sends no email (it throws once the quote was left)
      let smsOk = false;
      let smsWhy = "";
      const smsChosen = channels.includes("SMS");
      if (smsChosen) {
        const sms = await ctx.guarded(client.shareBySMS(ctx.id), this.lateWrite(ctx, "send the text"));
        if (sms === undefined) return;
        if (sms.ok) {
          smsOk = true;
        } else {
          smsWhy = sms.error;
        }
      }
      this.notice = sentConfirmation(smsChosen, smsOk, smsWhy);
      await this.loadAll();
      if (ctx.stale) return;
      this.quoteChanged();
    } catch (e) {
      if (ctx.stale) return;
      this.error = e instanceof Error ? e.message : "Could not send the quote.";
    } finally {
      release();
    }
  }

  private async doConvert(): Promise<void> {
    const client = this.client;
    if (client === null || this.busy || this.quoteId === "") return;
    const ctx = this.ctx;
    const release = this.holdBusy(ctx);
    this.error = "";
    const result = await ctx.guarded(client.convertQuote(ctx.id), this.lateWrite(ctx, "convert this quote"));
    if (result === undefined) return;
    release();
    if (!result.ok) {
      this.error = spokenRefusal(quoteErrorMessage(result.status, result.error, "convert this quote"));
      await this.loadAll();
      return;
    }
    this.converted = { orderNo: result.value.orderNo };
    await this.loadAll();
    if (ctx.stale) return;
    this.quoteChanged();
  }

  // ── render ─────────────────────────────────────────────────────────────────

  /** The dealer's send policy, in the pure module's own shape. */
  #sendPolicy(): { send_email_enabled: boolean; send_sms_enabled: boolean } | null {
    const policy = this.config?.policy;
    if (policy === undefined || policy === null) return null;
    return { send_email_enabled: policy.sendEmailEnabled, send_sms_enabled: policy.sendSmsEnabled };
  }

  private statusToneClass(status: string): string {
    if (status === "ACCEPTED" || status === "CONVERTED") return "live";
    if (status === "SENT") return "warm";
    return "";
  }

  private renderTopbar() {
    const v = this.view;
    const status = v?.status ?? "NEW";
    const statusText = v !== null ? quoteStatusWord(v.status) : "New";
    return html`
      <div class="topbar">
        <div class="headid">
          <h1 class="pagetitle" data-testid="qb-title">${v !== null ? "Quote" : "New Quote"}</h1>
          <div class="pagectx" data-testid="qb-context">
            ${this.branchName === "" ? "Yard" : this.branchName}${v !== null ? html` · ${v.quote_no}` : nothing}
          </div>
        </div>
        <hos-pos-flow-steps></hos-pos-flow-steps>
        <div class="session">
          <span class="clock" data-testid="qb-clock">${clockLabel(this.clockNow)}</span>
          <span class="statusdot ${this.statusToneClass(status)}" data-testid="qb-status">
            <span class="dot" aria-hidden="true"></span> ${statusText}
          </span>
          <span class="helpwrap">
            <button
              type="button"
              class="topbtn"
              data-action="help"
              data-testid="qb-help"
              aria-haspopup="true"
              aria-expanded=${this.helpOpen ? "true" : "false"}
              aria-label="Help"
            >
              ${drawIcon(iconForId("pencil:help"), 25)}
            </button>
            ${this.helpOpen ? this.renderHelp() : nothing}
          </span>
          <button type="button" class="newquote" data-action="new-quote" data-testid="qb-new-quote">
            <span aria-hidden="true">${drawIcon(iconForId("pencil:add"), 13)}</span> New Quote
          </button>
        </div>
      </div>
    `;
  }

  private renderHelp() {
    return html`<div class="helppop" role="group" aria-label="Quote builder help">
      <h3>Quote basics</h3>
      <ul>
        <li><b>Customer first</b>: pick the account (or start typing a name); the quote opens on it.</li>
        <li><b>Scan or search</b>: the one box takes a barcode, a SKU or a name; the add puts it on the quote at engine prices.</li>
        <li><b>Terms</b>: validity, deposit and notes ride the quote and print on what the customer sees.</li>
        <li><b>Send</b>: emails (and texts) the customer their quote link; follow up reminders are automatic. Convert turns an accepted quote into an order.</li>
      </ul>
    </div>`;
  }

  private renderLinesPanel() {
    const v = this.view;
    const frozen = v === null || !canEdit(v, EDIT_LINE_ADD);
    return html`
      <section class="panel lines" aria-label="Products">
        <div class="panelbar">
          <h2>Products</h2>
          <span class="viewflip" role="group" aria-label="Results view">
            <button
              type="button"
              class="${this.presentation === "CARDS" ? "active" : ""}"
              data-action="view-cards"
              data-testid="qb-view-cards"
              aria-pressed=${this.presentation === "CARDS" ? "true" : "false"}
              title="Card view: the product-card grid"
            >
              Cards
            </button>
            <button
              type="button"
              class="${this.presentation === "ROWS" ? "active" : ""}"
              data-action="view-rows"
              data-testid="qb-view-rows"
              aria-pressed=${this.presentation === "ROWS" ? "true" : "false"}
              title="Row view: one product per compact row"
            >
              Rows
            </button>
          </span>
        </div>
        <hos-pos-scan-box unified defer-outcome autofocus-scan></hos-pos-scan-box>
        <div class="scrollzone">
          <div class="gridwrap">
            <hos-pos-product-grid></hos-pos-product-grid>
          </div>
          ${this.renderLinesTable()}
          <button type="button" class="addanother" data-action="add-another" data-testid="qb-add-another">
            <span aria-hidden="true">${drawIcon(iconForId("pencil:add"), 13)}</span> Add another item
          </button>
          ${this.renderAddons(frozen)}
        </div>
        <div class="panelbtns">
          <span class="colwrap">
            <button
              type="button"
              class="pbtn"
              data-action="columns"
              data-testid="qb-columns-btn"
              aria-haspopup="true"
              aria-expanded=${this.columnsOpen ? "true" : "false"}
            >
              Columns
            </button>
            ${this.columnsOpen ? this.renderColumnsPopover() : nothing}
          </span>
          <button type="button" class="pbtn" data-action="swap" aria-label="Swap panels">
            <span aria-hidden="true">${drawIcon(iconForId("lucide:arrow-left-right"), 12)}</span>
          </button>
          <button type="button" class="pbtn" data-action="collapse-primary" aria-label="Collapse this panel">
            <span aria-hidden="true">${drawIcon(iconForId("pencil:left"), 12)}</span>
          </button>
        </div>
      </section>
    `;
  }

  private renderColumnsPopover() {
    // Cost class rows are OFFERED only while the server actually sent margin
    // data: a checkbox that could only ever produce an empty column
    // advertises gated data. The checkboxes themselves carry no bindings:
    // their checked state is brought onto them by the wiring pass, and their
    // changes arrive through the host's change reader.
    const costOffered = marginPresent(this.glance);
    const cols = this.resolvedColumns().filter((c) => (c.cls === "cost" ? costOffered : true));
    return html`<div class="colpop" role="group" aria-label="Line table columns">
      ${cols.map(
        (c) => html`<label class="colrow">
          <input type="checkbox" data-field="column" data-column=${c.id} />
          ${c.label}${c.cls === "cost" ? html` <span class="tag">cost</span>` : nothing}
          <span class="mv">
            <button
              type="button"
              class="pbtn"
              aria-label="Move ${c.label} up"
              data-action="column-move"
              data-column=${c.id}
              data-dir="up"
            >
              <span aria-hidden="true">${drawIcon(iconForId("lucide:chevron-up"), 12)}</span>
            </button>
            <button
              type="button"
              class="pbtn"
              aria-label="Move ${c.label} down"
              data-action="column-move"
              data-column=${c.id}
              data-dir="down"
            >
              <span aria-hidden="true">${drawIcon(iconForId("pencil:bottom"), 12)}</span>
            </button>
          </span>
        </label>`,
      )}
      ${costOffered
        ? html`<label class="colrow">
            <input type="checkbox" data-field="show-cost" data-testid="qb-cost-toggle" />
            Show cost
          </label>`
        : nothing}
      <button type="button" class="pbtn" data-action="columns-reset" data-testid="qb-columns-reset">
        Reset to company default
      </button>
    </div>`;
  }

  private termsLine(lineNo: number) {
    return this.terms?.lines.find((l) => l.lineNo === lineNo);
  }

  private renderLinesTable() {
    const v = this.view;
    const cols = this.renderableColumns();
    const discEnabled = this.config?.policy?.lineDiscountsEnabled !== false;
    const mayQty = v !== null && canEdit(v, EDIT_LINE_QTY);
    const mayRemove = v !== null && canEdit(v, EDIT_LINE_REMOVE);
    const lines = v?.lines ?? [];
    return html`
      <table class="lines" data-testid="qb-lines">
        <thead>
          <tr>
            ${cols.map(
              (c) => html`<th
                class="${c.id === "line.qty" || c.id === "line.unit_price" || c.id === "line.disc_pct" || c.id === "line.total" || c.id === "line.unit_cost" || c.id === "line.margin_pct"
                  ? "num"
                  : ""}"
                data-column=${c.id}
              >
                ${c.id === "line.thumb" ? "" : c.label}
              </th>`,
            )}
            ${mayRemove ? html`<th></th>` : nothing}
          </tr>
        </thead>
        <tbody>
          ${lines.map((l) => this.renderLineRow(l, cols, { mayQty, mayRemove, discEnabled }))}
          ${lines.length === 0
            ? html`<tr>
                <td data-role="empty-cell">
                  <span class="empty">No lines yet. Scan or search a product above.</span>
                </td>
              </tr>`
            : nothing}
        </tbody>
      </table>
    `;
  }

  private renderLineRow(
    l: QuoteViewLine,
    cols: ResolvedItem[],
    o: { mayQty: boolean; mayRemove: boolean; discEnabled: boolean },
  ) {
    const extra = this.termsLine(l.line_no);
    const cells = cols.map((c) => {
      switch (c.id) {
        case "line.thumb":
          return html`<td data-role="thumb"><span data-thumb=${l.product_id}></span></td>`;
        case "line.item":
          return html`<td data-role="item">
            <div class="iname">
              ${l.description === "" ? l.product_id.slice(0, 8) : l.description}
              ${l.rule_price_below_floor === true
                ? html`<span class="floormark" data-testid="qb-rule-under-floor" title="This product's own rule price sits under the margin floor; the pricing editors see it in the log.">rule under floor</span>`
                : nothing}
              ${l.floor_cleared_by !== undefined
                ? html`<span class="clearedby" title="The price below the floor was cleared by this manager.">cleared by ${l.floor_cleared_by}</span>`
                : nothing}
            </div>
            ${extra?.sku != null ? html`<div class="isku">${extra.sku}</div>` : nothing}
          </td>`;
        case "line.qty":
          return html`<td class="num" data-label=${c.label}>
            ${o.mayQty
              ? html`<span class="stepper">
                  <button
                    type="button"
                    aria-label="Decrease quantity of ${l.description}"
                    data-action="qty-step"
                    data-line=${l.line_no}
                    data-dir="down"
                  >
                    <span aria-hidden="true">${drawIcon(iconForId("lucide:minus"), 14)}</span>
                  </button>
                  <input
                    data-field="qty"
                    data-testid="qb-line-qty"
                    data-line=${l.line_no}
                    inputmode="decimal"
                    aria-label="Quantity of ${l.description}"
                  />
                  <button
                    type="button"
                    aria-label="Increase quantity of ${l.description}"
                    data-action="qty-step"
                    data-line=${l.line_no}
                    data-dir="up"
                  >
                    <span aria-hidden="true">${drawIcon(iconForId("pencil:add"), 14)}</span>
                  </button>
                </span>`
              : qtyLabel(l.qty_entered)}
          </td>`;
        case "line.uom":
          return html`<td data-label=${c.label}>${l.uom_entered}</td>`;
        case "line.unit_price":
          return html`<td class="num" data-label=${c.label}>${money(l.unit_price_cents)}</td>`;
        case "line.disc_pct":
          // No discount renders EMPTY, never a 0 for typed digits to append
          // to, and focus selects all: typing replaces.
          return html`<td class="num" data-label=${c.label}>
            ${o.mayQty && o.discEnabled
              ? html`<input
                    class="disc"
                    data-field="disc"
                    data-testid="qb-line-disc"
                    data-line=${l.line_no}
                    inputmode="numeric"
                    aria-label="Discount percent on ${l.description}"
                  />%`
              : extra !== undefined && extra.discPct > 0
                ? `${extra.discPct}%`
                : "(none)"}
          </td>`;
        case "line.total":
          return html`<td class="num" data-label=${c.label} data-testid="qb-line-total">${money(l.line_total_cents)}</td>`;
        case "line.unit_cost": {
          const cost = lineCostCents(this.glance, l.line_no);
          return html`<td class="num" data-label=${c.label}>${cost === null ? "(none)" : money(cost)}</td>`;
        }
        case "line.margin_pct": {
          const m = lineMarginLabel(this.glance, l.line_no);
          return html`<td class="num" data-label=${c.label} data-testid="qb-line-margin">${m === "" ? "(none)" : m}</td>`;
        }
        default:
          return nothing; // an id this build does not know renders NOTHING, silently
      }
    });
    return html`<tr data-testid="qb-line">
      ${cells}
      ${o.mayRemove
        ? html`<td data-role="remove">
            <button
              type="button"
              class="trash"
              data-action="line-remove"
              data-testid="qb-line-trash"
              data-line=${l.line_no}
              aria-label="Remove ${l.description}"
            >
              <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <path d="M3 6h18M8 6V4h8v2m-9 3v9m5-9v9M5 6l1 14h12l1-14"></path>
              </svg>
            </button>
          </td>`
        : nothing}
    </tr>`;
  }

  // The margin caption line is GONE: the margin pill on the totals card is
  // the one place the fact appears. BYTE ABSENCE stays the gate everywhere:
  // no glance data means no pill, no toggle, no cost columns.

  private renderAddons(frozen: boolean) {
    const panels = effectiveItems("panel", QB_PANELS, this.config?.roleItems, this.config?.userItems);
    if (!panelVisible(panels as ResolvedItem[], "addons")) return nothing; // the dealer curated the strip off
    if (this.addons.length === 0) return nothing; // empty is a configured absence, never an error
    return html`<div class="addons" data-testid="qb-addons">
      <h3>Suggested add-ons</h3>
      <div class="addonrow">
        ${this.addons.map(
          (a) => html`<div class="addontile" data-testid="qb-addon-tile">
            <span class="aname">${a.name}</span>
            ${a.size_line !== undefined ? html`<span class="empty">${a.size_line}</span>` : nothing}
            ${typeof a.list_price_cents === "number" ? html`<span class="num">${money(a.list_price_cents)}</span>` : nothing}
            <span class="actrl">
              <input
                data-field="addon-qty"
                data-product=${a.product_id}
                inputmode="numeric"
                aria-label="Quantity of ${a.name}"
              />
              <button
                type="button"
                class="addonadd"
                data-action="addon-add"
                data-testid="qb-addon-add"
                data-product=${a.product_id}
                ?disabled=${frozen}
              >
                <span aria-hidden="true">${drawIcon(iconForId("pencil:add"), 12)}</span> Add
              </button>
            </span>
          </div>`,
        )}
      </div>
    </div>`;
  }

  // ── the right panel ────────────────────────────────────────────────────────

  private renderSummaryPanel() {
    return html`
      <section class="panel summary" aria-label="Customer and terms">
        <div class="panelbar inflow">
          <h2>Customer &amp; terms</h2>
          <div class="panelbtns">
            <button type="button" class="pbtn" data-action="settings" data-testid="qb-settings-btn">Screen settings</button>
            <button
              type="button"
              class="pbtn"
              data-action="reset-layout"
              title="Reset layout to company default"
            >
              Reset layout
            </button>
            <button type="button" class="pbtn" data-action="collapse-secondary" aria-label="Collapse this panel">
              <span aria-hidden="true">${drawIcon(iconForId("pencil:right"), 12)}</span>
            </button>
          </div>
        </div>
        <div class="scrollzone">
          ${this.renderIdentity()}
          ${this.view !== null ? this.renderFieldsCard() : nothing}
          ${this.view !== null ? this.renderFulfilment() : nothing}
          ${this.view !== null ? this.renderTermsStrip() : nothing}
        </div>
        <div class="foot">
          ${this.view !== null ? this.renderTotalsCard() : nothing}
          ${this.view !== null ? this.renderActions() : nothing}
          ${this.renderOutcomeLines()}
        </div>
      </section>
    `;
  }

  /** The outcome pair: both regions always exist, empty when they have nothing to say. */
  private renderOutcomeLines() {
    return html`
      <p class="err" role="alert" data-testid="qb-error">${this.error}</p>
      <p class="err" role="alert" data-testid="qb-layout-error">${[this.layoutError, this.columnsError].filter((line) => line !== "").join(" ")}</p>
      <p class="ok" role="status" aria-live="polite" aria-atomic="true" data-testid="qb-notice">${this.notice}</p>
    `;
  }

  private renderIdentity() {
    const v = this.view;
    if (v === null) return nothing; // compose renders its own single card
    const c = this.customer;
    return html`<div class="card identity" data-testid="qb-identity">
      <span class="avatar" aria-hidden="true">${initialsOf(v.customer_name === "" ? "?" : v.customer_name)}</span>
      <div>
        <div class="cname">${v.customer_name === "" ? v.customer_id.slice(0, 8) : v.customer_name}</div>
        <div class="cmeta">
          ${c != null ? html`${c.account_no} · ${customerTypeTag(c.customer_type)}` : nothing}
        </div>
        ${c != null ? html`<div class="credit" data-testid="qb-identity-credit">${creditLine(c, money)}</div>` : nothing}
      </div>
      <span class="chg">
        <button
          type="button"
          class="pbtn"
          data-action="identity-change"
          data-testid="qb-identity-change"
          aria-label="Start over with a different customer"
          ?disabled=${v.lines.length > 0}
        >
          Change
        </button>
      </span>
    </div>`;
  }

  private renderFieldsCard() {
    const t = this.terms;
    if (t === null) return nothing;
    return html`<div class="card">
      <div class="fields">
        <div>
          <label class="f" for="qb-contact">Contact</label>
          <input
            id="qb-contact"
            data-field="terms-contact"
            data-terms-field="contact"
            data-testid="qb-contact"
            maxlength="120"
          />
        </div>
        <div>
          <label class="f" for="qb-project">Project / Job</label>
          <input
            id="qb-project"
            data-field="terms-project"
            data-terms-field="project"
            data-testid="qb-project"
            maxlength="64"
          />
        </div>
        <div>
          <label class="f" for="qb-po">Customer PO (optional)</label>
          <input id="qb-po" data-field="terms-po" data-terms-field="po" data-testid="qb-po" maxlength="64" />
        </div>
        <div>
          <label class="f" for="qb-sp">Salesperson</label>
          <select id="qb-sp" data-field="salesperson" data-testid="qb-salesperson">
            <option value="">(not set)</option>
          </select>
        </div>
      </div>
    </div>`;
  }

  private renderFulfilment() {
    const t = this.terms;
    if (t === null) return nothing;
    const pickup = t.fulfillment !== "DELIVERY";
    return html`<div class="card">
      <div class="fulfil">
        <button
          type="button"
          class="tile"
          data-action="fulfil-pickup"
          data-testid="qb-fulfil-pickup"
          aria-pressed=${pickup ? "true" : "false"}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M4 9h16l-1.5 11h-13L4 9Zm4 0V6a4 4 0 0 1 8 0v3"></path>
          </svg>
          Pickup
        </button>
        <button
          type="button"
          class="tile"
          data-action="fulfil-delivery"
          data-testid="qb-fulfil-delivery"
          aria-pressed=${pickup ? "false" : "true"}
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <path d="M1 7h12v9H1V7Zm12 3h5l3 3v3h-8v-6ZM6 19a2 2 0 1 0 0-.01M18 19a2 2 0 1 0 0-.01"></path>
          </svg>
          Delivery
        </button>
      </div>
      ${pickup
        ? nothing
        : html`
            <div class="fields">
              <div class="wide">
                <label class="f" for="qb-daddr">Deliver to</label>
                <input
                  id="qb-daddr"
                  data-field="terms-address"
                  data-terms-field="delivery-address"
                  data-testid="qb-delivery-address"
                  maxlength="200"
                />
              </div>
              <div>
                <label class="f" for="qb-ddate">Requested date</label>
                <input
                  id="qb-ddate"
                  type="date"
                  data-field="terms-date"
                  data-terms-field="requested-date"
                  data-testid="qb-delivery-date"
                />
              </div>
              <div>
                <label class="f" for="qb-dfee">Delivery fee (CA$)</label>
                <input
                  id="qb-dfee"
                  data-field="delivery-fee"
                  data-testid="qb-delivery-fee"
                  inputmode="decimal"
                />
              </div>
            </div>
          `}
    </div>`;
  }

  private renderTotalsCard() {
    const v = this.view;
    const t = this.terms;
    if (v === null || t === null) return nothing;
    const b = totalsBreakdown(
      v.lines.map((l) => ({ line_no: l.line_no, line_total_cents: l.line_total_cents })),
      t.lines.map((l) => ({ line_no: l.lineNo, disc_pct: l.discPct, pre_discount_total_cents: l.preDiscountTotalCents })),
      t.deliveryFeeCents,
      t.taxCents,
      t.totalCents,
    );
    // The deposit is the server's figure (deposit_cents). Only while a terms edit is unsaved is the local
    // figure shown, and it is labelled as an estimate, because the server has not seen the edit yet.
    const dep = this.termsUnsaved
      ? depositCents(t.depositRequired, t.depositPct, b.totalCents)
      : t.depositRequired && t.depositCents > 0
        ? t.depositCents
        : null;
    const showMargin = marginPresent(this.glance) && !this.ws.costHidden;
    return html`<div class="card totals" data-testid="qb-totals">
      <div class="trow"><span>Products</span><span class="num">${money(b.productsCents)}</span></div>
      ${b.discountCents > 0
        ? html`<div class="trow"><span>Discount</span><span class="num neg">-${money(b.discountCents)}</span></div>`
        : nothing}
      ${b.deliveryCents > 0
        ? html`<div class="trow"><span>Delivery</span><span class="num">${money(b.deliveryCents)}</span></div>`
        : nothing}
      <div class="trow sep"><span>Subtotal</span><span class="num">${money(b.subtotalCents + b.deliveryCents)}</span></div>
      <div class="trow"><span>HST</span><span class="num">${money(b.taxCents)}</span></div>
      <div class="grand">
        <span class="lbl">Quote total</span>
        ${showMargin
          ? html`<span class="marginpill" data-testid="qb-margin-pill">${marginPctLabel(this.glance?.margin_bps)} margin</span>`
          : nothing}
        <span class="amt" data-testid="qb-total">${money(b.totalCents)}</span>
      </div>
      ${dep !== null
        ? html`<div class="trow">
            <span data-testid="qb-deposit-label">${this.termsUnsaved ? "Deposit due (estimate, not yet saved)" : "Deposit due"}</span>
            <span class="num">${money(dep)}</span>
          </div>`
        : nothing}
    </div>`;
  }

  private renderTermsStrip() {
    const t = this.terms;
    const v = this.view;
    if (t === null || v === null) return nothing;
    return html`<div class="card">
      <div class="termsrow">
        <div>
          <label class="f" for="qb-validity">Valid for</label>
          <select id="qb-validity" data-field="validity" data-testid="qb-terms-validity"></select>
        </div>
        <div>
          <label class="check">
            <input
              type="checkbox"
              data-field="deposit-toggle"
              data-testid="qb-terms-deposit-toggle"
            />
            Deposit
          </label>
          ${t.depositRequired
            ? html`<span style="display: flex; gap: 6px; align-items: center; margin-top: 6px; font-size: var(--qb-body)">
                <input
                  id="qb-dep"
                  data-field="deposit-pct"
                  data-testid="qb-terms-deposit"
                  inputmode="numeric"
                  aria-label="Deposit percent"
                  style="width: 4rem"
                />%
              </span>`
            : nothing}
        </div>
        <div>
          <label class="check">
            <input type="checkbox" data-field="include-images" data-testid="qb-terms-images" />
            Include product images
          </label>
        </div>
        <div>
          <label class="f" for="qb-notes">Customer notes</label>
          <input
            id="qb-notes"
            data-field="terms-notes"
            data-terms-field="notes"
            data-testid="qb-terms-notes"
            maxlength="500"
            placeholder="Printed on the quote"
          />
        </div>
      </div>
    </div>`;
  }

  private renderActions() {
    const v = this.view;
    if (v === null) return nothing;
    const actions = visibleItems(effectiveItems("action", QB_ACTIONS, this.config?.roleItems, this.config?.userItems));
    const has = (id: string) => actions.some((a) => a.id === id);
    const shareable = (v.status === "DRAFT" || v.status === "SENT") && !v.expired;
    const channels = sendChannels(this.#sendPolicy());
    const chosen = channels.filter((c) => this.sendPick[c]);
    return html`
      ${v.status === "DRAFT"
        ? html`<p class="drafthint" data-testid="qb-draft-hint">A draft is a quote you have not sent yet.</p>`
        : nothing}
      <div class="actions">
        ${has("quote.save_draft")
          ? html`<button type="button" class="abtn" data-action="save-draft" data-testid="qb-save-draft">Save</button>`
          : nothing}
        ${has("quote.preview")
          ? html`<button
              type="button"
              class="abtn"
              data-action="preview"
              data-testid="qb-preview"
              ?disabled=${!shareable && this.mintedToken === ""}
            >
              Preview
            </button>`
          : nothing}
        ${has("quote.print")
          ? html`<button
              type="button"
              class="abtn"
              data-action="print"
              data-testid="qb-print"
              ?disabled=${!shareable && this.mintedToken === ""}
            >
              Print
            </button>`
          : nothing}
        ${has("quote.copy_link")
          ? html`<button
              type="button"
              class="abtn"
              data-action="copy-link"
              data-testid="qb-copy-link"
              ?disabled=${!shareable && this.mintedToken === ""}
            >
              Copy Link
            </button>`
          : nothing}
        ${has("quote.send")
          ? html`<span class="sendwrap">
              <button
                type="button"
                class="sendbtn"
                data-action="send"
                data-testid="qb-send"
                aria-keyshortcuts="Control+Enter"
                ?disabled=${!shareable || this.busy}
              >
                <span aria-hidden="true">${drawIcon(iconForId("lucide:send"), 15)}</span> SEND QUOTE<span class="key" aria-hidden="true">Ctrl Enter</span>
              </button>
              <button
                type="button"
                class="abtn"
                data-action="send-channel"
                data-testid="qb-send-channel"
                aria-haspopup="true"
                aria-expanded=${this.sendOpen ? "true" : "false"}
                title="Choose channels"
              >
                ${sendCaption(chosen)}
                <span aria-hidden="true">${drawIcon(iconForId("pencil:bottom"), 13)}</span>
              </button>
              ${this.sendOpen
                ? html`<div class="sendpop" role="group" aria-label="Send channels">
                    ${channels.map(
                      (c) => html`<label class="check">
                        <input
                          type="checkbox"
                          data-field="channel"
                          data-channel=${c}
                          data-testid="qb-channel-${c === "EMAIL" ? "email" : "sms"}"
                        />
                        ${c === "EMAIL" ? "Email" : "Text"}
                      </label>`,
                    )}
                  </div>`
                : nothing}
            </span>`
          : nothing}
        ${has("quote.convert_order") && canConvert(v)
          ? html`<button
              type="button"
              class="abtn"
              data-action="convert"
              data-testid="qb-convert"
              style="grid-column: 1 / -1"
              ?disabled=${this.busy}
            >
              Convert to Order
            </button>`
          : nothing}
      </div>
      ${this.converted !== null
        ? html`<p class="ok" role="status" data-testid="qb-convert-done">Converted to order ${this.converted.orderNo}.</p>`
        : nothing}
    `;
  }

  /**
   * COMPOSE (no quote yet): one card, one question, the customer picker IS
   * the screen. No dead product grid, no disabled gold.
   */
  private renderCompose() {
    return html`<div class="composewrap">
      <div class="composecard" data-testid="qb-identity">
        <hos-customer-picker></hos-customer-picker>
      </div>
    </div>`;
  }

  override render() {
    if (this.quoteId === "") {
      return html`
        ${this.renderTopbar()}
        ${this.renderCompose()}
        ${this.renderOutcomeLines()}
      `;
    }
    const ws = this.ws;
    const primary =
      ws.primaryCollapsed
        ? html`<div class="rail">
            <button
              type="button"
              class="pbtn"
              data-action="restore-primary"
              aria-label="Restore products panel"
            >
              <span aria-hidden="true">${drawIcon(iconForId("pencil:right"), 12)}</span>
            </button>
          </div>`
        : this.renderLinesPanel();
    const secondary =
      ws.secondaryCollapsed
        ? html`<div class="rail">
            <button
              type="button"
              class="pbtn"
              data-action="restore-secondary"
              aria-label="Restore customer panel"
            >
              <span aria-hidden="true">${drawIcon(iconForId("pencil:left"), 12)}</span>
            </button>
          </div>`
        : this.renderSummaryPanel();
    const divider = html`<div
      class="divider"
      data-testid="qb-divider"
      data-role="divider"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize panels"
      tabindex="0"
    >
      <span class="grip" aria-hidden="true"></span>
    </div>`;
    const pair =
      ws.arrangement === "PRIMARY_RIGHT" ? html`${secondary}${divider}${primary}` : html`${primary}${divider}${secondary}`;
    return html`
      ${this.renderTopbar()}
      <div class="workspace" data-testid="qb-workspace" data-arrangement=${ws.arrangement}>
        ${pair}
      </div>
      ${ws.secondaryCollapsed ? this.renderOutcomeLines() : nothing}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    "qd-quote-workarea": QdQuoteWorkarea;
  }
}
