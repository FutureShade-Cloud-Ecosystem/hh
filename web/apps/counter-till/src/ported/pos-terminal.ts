// Ported from hardscapeos_dibbits master 87d7941b app/src/pos-terminal.ts; adapted for HH.

import { LitElement, css, html, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { ifDefined } from 'lit/directives/if-defined.js';
import { api, BRANCHES, branchName, getCurrentUser, isAdmin } from './ports.js';
import {
  availableCreditCents,
  cartSubtotalCents,
  depositCentsFromTyped,
  perLineSavingCents,
  sumLineSavingCents,
  ticketTaxCents,
  ticketTotalCents,
} from './ports.js';
import { drawIcon, iconForId } from './_icon-consumer.js';
// Boundary: the sold and lookup panels embed neighbouring screens by tag
// (hos-till-close, hos-pricing-doc, the hos-order-* family, hos-related-docs).
// The reference registered those elements with side-effect imports; in HH they
// are host screens, so the port drops the registrations and keeps the tags
// (see CLOSURE.md). Until the wiring item lands they render as unknown
// elements and nothing else on the surface depends on them.
import { surfaceConfigUrl } from './surface-config-url.js';
import './price-breakdown.js';
import './pos-product-grid.js';
import './pos-search-drop.js';
import './pos-hot-list.js';
import './pos-hotkey-legend.js';
import './pos-counter-settings.js';
import './pos-customer-strip.js';
import './pos-customer-picker.js';
import './pos-assess-relief.js';
import './pos-signature.js';
import './pos-return.js';
import './pos-parked.js';
// ── W3-A (DIB-165 / the pos-barcode ticket / pos-search / product-images-ui) ──
import './pos-scan-box.js';
import './pos-split-tender.js';
import './pos-lookup.js';
import './product-thumb.js';
import type { RelatedDoc } from './ports.js';
import type { PosCustomer } from './pos-customer-strip.js';
import { parseCompactDate } from './_date.js'; // DIB-200: compact YYYYMMDD → ISO requested-date entry
import {
  restoreParkedCustomer,
  clearSaleNeedsConfirm,
  counterRingMessage, // rev 5: ringErrorMessage in counter wording (module prefix stripped, fixes named)
  creditBlockMessage, // the credit gate's refusal words, by the policy the gate named
  saleConfirmation, // DIB-444: what the confirmation panel may claim
  salePillLabel, // DIB-512: the counter word for a rung sale's pay-state (never the wire status)
  cleanQty, // rev 5: NaN/garbage in a qty box snaps to 1 instead of corrupting the line
  stableRingKey, // rev 5: an identical retry reuses its idempotency key (replay-safe for real)
  customerInitials, // rev 6 (A): the resolved-customer identity card's chip
  accountTypeTag, // rev 6 (A): the account-type tag in counter words
  cashTenderedPlan, // rev 6 (F): the optional cash-tendered quick entry's arithmetic
  tenderLabel, // POS restructure: the chosen tender in counter words, for the collapsed card
  fulfilmentLabel, // and how the material leaves, for the pre-tender read-back
  decideAdd, // DIB-DUPLINE: merge into the existing line, or append and say why
  addOutcomeMessage, // DIB-DUPLINE: what the counter is told about the add
  echoAddMessage, // DIB-553 (bounce 2): and what it is told about the add it SWALLOWED
  repriceStamp, // D17 (round 3): what a line looked like when its pricing round trip was issued
  repriceAnswerIsCurrent, // D17 (round 3): and whether that answer is still about the line that is there
  applyPriceAnswer, // D17 (round 3): the money fields onto the CURRENT line, never a stale snapshot
  type AddDecision,
  type RingKeyMemo,
  type ParkedInfo,
} from './_pos.js'; // DIB-244/246/247/379
// ── DIB-488 ── the tracked (never gated) sale-session flow + the stock-status line.
import './pos-flow-steps.js';
import './pos-step-card.js'; // POS restructure: one accordion card, four times over
import { deriveFlow, type FlowStepId } from './_pos-flow.js';
import {
  summaryFor, // what a COLLAPSED card still says
  advanceOn, // the two moments that move the expansion: and only those two
  type FlowAdvance,
  focusTargetFor, // the first MEANINGFUL control of a step, as a selector
  scrollPlan, // where the page must land so the open step is whole on screen
  type StepSummaryFacts,
} from './_pos-accordion.js';
// ── the counter's F-key roster + the pinned legend (the ask N1) ──
import {
  asHotkeyRoster,
  chordFromEvent,
  keycap,
  resolveHotkey,
  stepAfter,
  HK_STEP_NEXT,
  HK_STEP_BACK,
  HK_PAY,
  HK_PARK,
  HK_SEARCH,
  type PosHotkey,
  type HotkeyState,
} from './_pos-hotkeys.js';
// ── which parts of the counter this person keeps on screen (the R1/R2 user half) ──
import {
  panelView,
  panelWrite,
  asSurfaceItems,
  COUNTER_PANELS,
  PANEL_LABELS,
  PANEL_LEGEND,
  PANEL_HOTLIST,
  POS_COUNTER_SURFACE_ID,
  COUNTER_PANEL_DEFAULTS,
} from './_pos-counter-view.js';
import { pricePicture, basisWord, type CounterQuote } from './_pos-searchdrop.js';
import { stockStatus } from './_pos-stock.js';
// ── DIB-488 rev 2 ── the two-panel WORKSPACE (Inventory lookup ⇄ Sale): geometry, collapse/
// maximize rules and the persistence resolution are pure (_pos-workspace.ts); this file only
// paints the answer and wires the gestures.
import {
  defaultView,
  resolveWorkspaceView,
  workspaceSaveBodyV9, // 0220: the rev-8 body + counter_layout: earlier contracts stand
  mergeWorkspaceForSave, // rev 6 (G): re-read + merge-changed-fields before each debounced save
  type WorkspaceView,
  type CounterLayout,
  type WorkspaceField,
  type Arrangement,
  type LookupPresentation,
} from './_pos-workspace.js';
// ── DIB-488 rev 8 ── the concept slate: unified field, header bar, weight/margin columns, the
// footer transaction bar and the DIB-502 add-ons strip. All decisions pure (_pos-rev8.ts).
// HH note: the margin word routes through the app's reviewed formatMargin
// (money.ts, half away from zero): the same core figure reads the same word
// here as everywhere else in HH (1585 bps is 15.9%, not a float's 15.8%).
import { formatMargin } from "../money.js";
import {
  clockLabel,
  lineWeightLbs,
  formatLbs,
  ticketWeightTotalLbs,
  txbarSegments,
  resolveCuratedChips,
  payTileStep, // DIB-512: the tender tiles as ONE roving keyboard stop
  type LineWeightFacts,
} from './_pos-rev8.js';
import { printReceipt, openBlankWindow } from './receipt-print.js'; // DIB-410 counter receipt
import {
  createSale,
  createPricingOrder,
  getPosPolicy,
  getWorkspacePrefs,
  putWorkspacePrefs,
  findOpenTillSession, // rev 5: mount-time rediscovery of the OPEN drawer
  listSalespeople, // rev 6 (D): the More-options salesperson selector's roster
  marginGlance, // rev 8 (item 10): the SERVER-computed, role-gated margin read
  addonSuggestions, // rev 8 (item 8, DIB-502): the suggested add-ons read
  type MarginGlanceResult,
  type AddonSuggestion,
  type Salesperson,
  listParked,
  listProducts,
  parkTicket,
  posHotkeys, // the dealer's counter key roster
  counterPrices, // the customer-aware price picture (the pricing engine's answer)
  searchCustomers, // the account read the card refreshes through after an account sale rings
  accountStanding, // the Customer step's past-due FACT (role-gated at the marshal layer)
  type AccountStanding,
  resolveLine,
  money,
  newIdempotencyKey,
  type CartLine,
  type ParkedTicket,
  stockGlance,
  type PosProduct,
  type PricingOrder,
  type SaleResult,
  type ReliefOutcome,
  type StockGlance,
} from './pos-api.js';
import './pos-panels.js';
import { focusRing } from './styles/focus-ring.js';
import { ModalFocus } from './modal-focus.js';
// ── DIB-434 ── the global "+ Sale" quick action arrives here as an INTENT, and the in-progress
// guard below is what stops it from ever clobbering a live till.
import './quick-action-start-guard.js';
import { takeIntent, INTENT_EVENT, type PendingIntent } from './quick-action-intent.js';
import { quickStartNeedsGuard, type LiveTicket } from './quick-action-guard.js';
import { INTENT_POS_NEW_TICKET, INTENT_POS_PAY } from './quick-action-registry.js';
// ── end DIB-434 ──

type Ring = { state: 'idle' } | { state: 'busy' } | { state: 'done'; sale: SaleResult } | { state: 'error'; msg: string };

/**
 * DIB-195 pop-out handoff: the in-progress (DRAFT) order carried into a popped-out window so the
 * second-monitor window opens on the SAME ticket instead of empty. This is client-only state:
 * there is no server sale id until the ticket is rung or parked: serialized verbatim: the cart
 * lines already carry their engine breakdown, so the popped terminal shows identical
 * prices/totals with no re-fetch. Excludes transient ring state (the "Sold" banner) and carries
 * the open till session so the moved window can ring against the SAME drawer immediately.
 */
export interface PosOrderSnapshot {
  cart: CartLine[];
  customer: PosCustomer | null;
  branchId: string;
  saleType: string;
  cardType: string;
  notes: string;
  requestedDate: string;
  reliefReason: string;
  reliefRateBps: number | null;
  signatureRef: string;
  depositDollars: string;
  depositMethod: string;
  fulfilment: string;
  tillSessionId: string;
  sessionStartMs: number;
  // ── DIB-488 (both optional so an older snapshot restores cleanly) ──
  /** The operator explicitly confirmed the walk-in posture (sale-session data the flow derives from). */
  walkInConfirmed?: boolean;
  /** The chosen tender tile ('' = none yet). */
  payMethod?: string;
  /** The operator actually PICKED a tender (as opposed to riding the cash default). */
  tenderConfirmed?: boolean;
 // ── DIB-488 rev 6 (all optional: an older snapshot restores cleanly) ──
  /** Item D: the optional PO / job reference typed under More options. */
  poJobRef?: string;
  /** Item D: the credited salesperson ('' = the signed-in operator's default applies). */
  salespersonId?: string;
  /** Item F: the optional cash-tendered quick entry (dollars, as typed). */
  cashTenderedDollars?: string;
 // ── DIB-488 rev 8 (item 11, all optional: an older snapshot restores cleanly) ──
  /** The job-site address typed under More options. */
  jobAddress?: string;
  /** The job reference typed under More options. */
  jobReference?: string;
}

/**
 * A-19 POS terminal (US-A19-01..10). The designed two-pane counter: a product grid (left) and the order
 * panel (right) with the customer strip, the priced line grid (each line shows its <hos-price-breakdown>
 * from the engine: the cashier never types a price), totals (Subtotal / HST 13% / Total), and the three
 * tender buttons (Charge Account / Cash / Card). Every tender mints a client idempotency key (replay-safe)
 * and POSTs /api/v1/pos/sales. All colors are --hos-* tokens; money/SKU/qty are JetBrains Mono: both
 * themes. The top bar is the A-03 till chrome (session timer + End Session); A-19 only renders it.
 */
/**
 * DIB-DUPLINE: how long the affected ticket line rings after an add. Long enough that a clerk
 * whose eye was on the gun still catches the movement when it comes back to the ticket, short
 * enough that it is never the state a screenshot or the next scan lands in.
 */
const LINE_FLASH_MS = 1600;

@customElement('hos-pos-terminal')
export class HosPosTerminal extends LitElement {
  @state() private cart: CartLine[] = [];
  /**
 * DIB-DUPLINE: the ticket line an add just touched, or -1. Presentation only: deliberately NOT
   * a field on CartLine, so the till snapshot never rehydrates mid-flash and the merge rule's
   * line-state guard keeps seeing a clean line shape.
   */
  @state() private flashIdx = -1;
  private flashTimer = 0;
  // ── W3-A ──
  /** DIB-165: the split-tender panel is open (mutually exclusive with a plain tender). */
  @state() private showSplit = false;
  /** DIB-165: the past-sale / till-session lookup overlay. */
  // E6: the terminal is the POS ring itself and stays mounted; the ONE dialog it owns inline is the
  // past-sales sheet (its other five modals are separate elements with their own traps). Escape is
 // safe: the sheet is a read-only lookup over completed sales and holds no unsaved entry.
  //
  // Worth stating because this is the busiest keyboard surface in the app: without the trap, Tab out
  // of the sheet walked straight back into the LIVE CART behind the scrim, where the next Enter
  // could ring or void a line the cashier could not see.
  readonly modalFocus = new ModalFocus(this, {
    container: () => this.renderRoot.querySelector<HTMLElement>('.overlay'),
    isOpen: () => this.showLookup,
    onEscape: () => (this.showLookup = false),
  });

  @state() private showLookup = false;
  @state() private customer: PosCustomer | null = null;
  @state() private branchId = BRANCHES[0].id;
  @state() private saleType = 'COLLECT_NOW';
  @state() private cardType = 'CREDIT';
  @state() private notes = '';
  @state() private requestedDate = '';
  // DIB-200: inline validation hint for the fast (YYYYMMDD) date-entry box. '' = no error.
  @state() private dateError = '';
  // DIB-196 (Ryan Rood): when true, adding a product jumps focus to its quantity box (BisTrack's
  // per-terminal "ask for quantity" posture) instead of silently defaulting the line to qty 1.
 // In-memory only: a per-terminal preference, deliberately never persisted.
  @state() private promptQtyOnAdd = false;
  @state() private ring: Ring = { state: 'idle' };
  @state() private reliefReason = '';
 // The engine-assessed rate for the applied relief (bps). null = standard 13%. Display preview only:
  // the server (M5) computes the BINDING per-line tax at ring time; this keeps the on-screen totals
  // consistent with what the engine will actually charge.
  @state() private reliefRateBps: number | null = null;
  @state() private signatureRef = '';
  @state() private showRelief = false;
  // Customer picker overlay (account-tender UX fix). pickerHint carries the Charge-Account
  // nudge when the picker is opened because a no-account sale was attempted.
  @state() private showPicker = false;
  @state() private pickerHint = '';

  /**
 * POS restructure: THE ONE OPEN STEP. Selecting a step expands its card and collapses the
   * others; '' means every card is collapsed, which is a legitimate whole-sale-at-a-glance view
 * (four summary lines) and never a dead end: any card is one click from open.
   *
   * A fresh sale opens CUSTOMER, deliberately. The client moved that step to the front because the
   * price of everything below it depends on which account is on the sale, and answering it costs
   * one press: focus lands on the Walk-in tile, and resolving it expands Items with the caret in
 * the scan box. Nothing is gated either way: a clerk who wants to scan first clicks Items and
   * the sale rings with the customer question still open.
   */
  @state() private openStep: FlowStepId | '' = 'customer';

  // ── DIB-488 sale-session flow + workspace state ──────────────────────────
  /**
   * The operator explicitly chose walk-in (as opposed to walk-in merely being the default). This is
 * SALE data: it rides the pop-out snapshot and resets with the sale: and it is what lets the
   * flow's Customer step distinguish "resolved as walk-in" from "nobody has answered yet" without
   * the stepper keeping any state of its own.
   */
  @state() private walkInConfirmed = false;
  /** The chosen tender tile. '' until picked; SPLIT opens the split panel (which rings itself). */
  /**
 * THE CHOSEN TENDER: and it starts as CASH, on the client's instruction: "it should default to
   * cash. if you don't pick one, just default it to cash."
   *
   * The consequence, which is the actual feature: A TICKET IS ALWAYS TENDERABLE. There is no state
   * in which a clerk has scanned three items, reached the gold button and been refused it for not
 * having pressed Cash: which is ceremony at a counter where cash is most of the volume. Picking
   * a tile still changes the tender; it just is not a precondition of ringing.
   *
   * '' remains in the type because SPLIT can be cancelled back out of, and because a restored
   * snapshot from before this change can carry it. Every consumer treats '' as "not tenderable",
 * so the old state is safe: it is simply no longer reachable by doing nothing.
   */
  @state() private payMethod: '' | 'ACCOUNT' | 'CASH' | 'CARD' | 'SPLIT' = 'CASH';
  /**
 * THE OPERATOR ACTUALLY PICKED A TENDER: the payment step's twin of walkInConfirmed, and for
   * exactly the same reason.
   *
   * Cash is the DEFAULT posture (the client's instruction, argued above), and a default is not a
 * decision. _pos-flow.ts says so in the walkInConfirmed docstring: "the step reads complete only
 * when someone actually resolved the question": and its Customer rule honours it. Its Payment
   * rule three lines down reads `payMethod !== ''`, which the cash default satisfies at mount, so a
 * cold counter with nothing on it rendered "✓ Payment: done Cash" and a screen reader heard
   * ": done" about a sale that had not begun.
   *
   * THE FIX IS HERE AND NOT IN deriveFlow, DELIBERATELY. deriveFlow's predicate is correct for the
   * fact it is given and is pinned by _pos-flow.test.mjs ({lineCount:1, payMethod:'CASH'} →
   * payment.complete). What was wrong is what this terminal REPORTED into it: the posture, dressed
   * as a choice. So the flow module and every test over it stand untouched and flowSteps() feeds
   * the tender only once someone has chosen it.
   *
   * NOTHING ABOUT THE TENDER ITSELF MOVES. payMethod is still CASH from the first millisecond, the
   * ticket is still tenderable with nobody touching a tile, and the collapsed Payment card still
 * says "Cash": the posture is a fact worth reporting (summaryFor's own reasoning). Only the tick
   * waits for a decision.
   */
  @state() private tenderConfirmed = false;
  /**
 * Items presentation: D-096: the dealer default arrives with pos_policy (line_presentation);
   * the terminal may flip its own view in-memory (the promptQtyOnAdd posture: per-terminal,
   * deliberately not persisted).
   */
  @state() private presentation: 'LINES' | 'CARDS' = 'LINES';
  /** D-096: the dealer-level quick-chip row toggle, from pos_policy. */
  @state() private quickChips = true;
  /** Per-product stock glance cache for the stock-status line (display-only; never blocks adds). */
  @state() private lineStock: Record<string, StockGlance> = {};

  // ── DIB-488 rev 6 (item D): PO / Job # + salesperson, under More options ──
 /** The customer's PO / job reference: optional free text, ≤64 chars (server-validated too). */
  @state() private poJobRef = '';
  /** The credited salesperson. Defaults to the signed-in operator; changeable from the roster. */
  @state() private salespersonId = '';
  /** The active-staff roster for the selector. Empty = load failed → the operator alone is offered. */
  @state() private salespeople: Salesperson[] = [];

  // ── DIB-488 rev 6 (item F): the optional cash-tendered quick entry ──
 /** Dollars as typed on the Cash tile's quick entry. '' = not used: one-click cash unchanged. */
  @state() private cashTenderedDollars = '';

 // ── DIB-488 rev 8 (item 11): the JOB fields under More options: item-D's 0168 pattern, 0170 ──
  @state() private jobAddress = '';
  @state() private jobReference = '';

  // ── DIB-488 rev 8 (item 2): the header bar's live clock. The TIMER lives here; the LABEL is
 // pure (clockLabel: the injected-clock house rule: the Date goes IN, no clock read inside). ──
  @state() private clockNow = new Date();
  private clockTimer = 0;

  /** rev 8 (item 2): the Help popover's open state (session-only chrome). */
  @state() private helpOpen = false;

  /** DIB-512 · the roving tabindex position inside the payment-tile group (one Tab stop). The
 * group's resting stop is CASH: the counter's most frequent tender: so the whole top task
   * is three keystrokes from the scan field: F8 (walk to payment) · Enter (Cash) · F8 (ring). */
  @state() private payTileIdx = 1;

  // ── DIB-488 rev 8 (item 1): the unified smart field's live term, forwarded to the grid ──
  @state() private lookupTerm = '';

  // ── DIB-488 rev 8 (item 4): the curated chip categories off pos_policy (hardened at the edge) ──
  @state() private curatedChips: string[] = [];

  // ── DIB-488 rev 8 (item 10): the SERVER's role-gated margin answer. null = absent (not allowed,
 // not loaded, or the read failed): and ABSENT data renders NOTHING (no column, no footer). ──
  @state() private marginData: MarginGlanceResult | null = null;

  // ── DIB-488 rev 8 (item 8): the DIB-502 suggestion tiles. Empty = the strip does not exist. ──
  @state() private addons: AddonSuggestion[] = [];

  /**
 * The ticket's per-line price picture, keyed by product id. Display only, and server-decided:
   * the LINE's own money still comes from the resolve that put it on the ticket. This read exists
   * so the counter can say what the account is SAVING, which is the number the client says
   * customers always ask for.
   */
  @state() private ticketQuotes: Record<string, CounterQuote> = {};

  /** The selected account's past-due position, when the dealer's policy lets this caller see it. */
  @state() private standing: AccountStanding = {};
  /** Per-tile qty entries (product_id → dollars-free qty string), seeded from default_qty. */
  private addonQty = new Map<string, string>();
  /** Debounce + staleness token for the ticket-facts reads (margin + addons). */
  private ticketFactsTimer = 0;
  private ticketFactsSeq = 0;
  /**
 * Staleness bookkeeping for reprice(): decision D17, round 3. The reasoning is in `_pos.ts`
   * beside `repriceAnswerIsCurrent`; the two counters are here because only the terminal can bump
   * them.
   *
   * `repriceSeq` is PER SLOT rather than one scalar like `ticketFactsSeq`, and deliberately so: two
   * lines can be in flight at once (a counter picking items off a category board does three adds in
   * well under a second) and a single scalar would let each add cancel the one before it, leaving
   * the earlier line reading a hard CA$0.00 until tender. Per slot, an add only ever supersedes
   * ITSELF.
   *
 * `cartGen` is bumped ONLY where the cart is replaced WHOLESALE: new sale, clear, park, resume,
   * apply-snapshot. Not on setQty and not on removeLine: both of those already supersede via the
   * per-slot token or fail the product/qty/uom check, and bumping there would cancel a fresh add's
   * first price because an unrelated line was deleted.
   */
  private repriceSeq: number[] = [];
  private cartGen = 0;

  // ── the per-user workspace row, AFTER the single-column restructure ───────
  //
 // POS no longer RENDERS a divider, a swap, a collapse rail or a per-panel config popover: the
  // counter is one column. But the pos_user_workspace row those gestures wrote is shared state
  // with a real history: people have saved layouts in it, the quote work area reads the same
  // shape, and the server's whole-or-absent PUT contract is unchanged. So the geometry fields are
  // still READ, still HELD, and still WRITTEN BACK untouched by the merge-save below. Dropping
  // them would have silently reset every operator's stored row the first time they opened POS.
  //
  // DEALER (pos_policy) the defaults, written only through the admin policy PUT.
 // PER USER (pos_user_workspace) the ergonomics. Two of them still have a control on POS:
 // the results Cards ⇄ Rows flip and the margin quick-hide:
  // and the geometry three ride along as carried state.
  /** Carried, not rendered: the retired divider's percent share. */
  @state() private wsSplit = defaultView().split;
  @state() private wsArrangement: Arrangement = 'LOOKUP_LEFT';
  @state() private wsLookupCollapsed = false;
  @state() private wsSaleCollapsed = false;
  /**
 * rev 3 (0167): the lookup panel's results presentation: the client's cards ⇄ rows toggle.
   * PERSISTED per user (unlike the sale panel's in-memory Lines/Cards flip): it rides the same
   * whole-or-absent pos_user_workspace row as the split, so it follows the person across tills;
   * the dealer default arrives with the same resolved workspace read.
   */
  @state() private wsLookupPresentation: LookupPresentation = 'CARDS';
  /**
 * rev 8 (0170): the margin quick-hide (eye) toggle: counter privacy, one click. Display-only
   * (the server's ROLE gate owns whether margin data is ever sent); persists per user on the same
   * whole-or-absent workspace row through the same rev-6 merge-save contract.
   */
  @state() private wsMarginHidden = false;
  /**
 * 0220: which counter this person works on: the four-step single column, or all four at once.
   *
   * STEPPER is the standard counter and the shipped default at every level, and nothing about it
   * changes for anybody who never opens the view menu. The account behind that default is in
   * `_pos-accordion.ts` and it still stands: the client used a multi-panel counter for a day and
   * asked for one column, because a person at a till with a customer in front of them is doing one
   * thing and four half-visible panels made them hunt for it.
   *
 * What the commander ruled is that the OTHER answer should also be available: several
   * testers asked for everything visible at once, in different words, and one of them asked for it
   * as a per-user switch ("Maybe if users still want this we give the ability to turn it off?").
   * So this is a preference, not a redesign: it rides the same whole-or-absent workspace row as the
   * split and follows the person across tills.
   */
  @state() private counterLayout: CounterLayout = 'STEPPER';
  /**
   * The dealer's key roster, resolved server-side (user over dealer over default). Held as data,
   * never as a switch statement: the client's people are BisTrack refugees who will want their old
   * keys, and a roster compiled into a component is a roster nobody can change.
   */
  @state() private hotkeyRoster: PosHotkey[] = asHotkeyRoster(null);

  /**
 * Which counter panels this person keeps on screen: the legend and the hot list. Resolved
   * user-over-dealer-over-default through the presentation substrate, because two people at the
   * same yard asked for opposite answers and neither of them is wrong.
   */
  @state() private panels: Record<string, boolean> = { ...COUNTER_PANEL_DEFAULTS };
  /** The dealer's panel curation, kept so a per-user write starts from what they actually see. */
  private panelRoleItems: ReturnType<typeof asSurfaceItems> = [];
 /** The view popover in the header: where a dismissed legend comes back from. */
  @state() private viewOpen = false;
  /** The admin editor for the roster, the board and the panel defaults (R1/R2). */
  @state() private counterSettingsOpen = false;

  private wsPersistTimer = 0;
  /**
 * rev 6 (item G): the fields THIS surface's gestures changed since their last successful save.
   * Each debounced save re-reads the stored row and overlays only these (mergeWorkspaceForSave),
 * so two live surfaces stop flattening each other's ergonomics. Not @state: never painted.
   */
  private wsDirty = new Set<WorkspaceField>();

  /**
 * rev 5: the in-flight ring attempt's idempotency key, keyed by the request fingerprint
   * (stableRingKey in _pos.ts). A retry of the IDENTICAL sale reuses the key verbatim (true
 * replay safety); any edit to the sale re-mints; success clears it. Not @state: it never
   * paints anything.
   */
  private ringKeyMemo: RingKeyMemo | null = null;

  // A-03 till chrome (A-19 renders only).
  @state() private tillSessionId = '';
  @state() private sessionStartMs = 0;
  @state() private elapsed = '00:00';
  private timer = 0;

  // Card-rail mode, from GET /api/v1/payments/config: 'test' = deterministic simulated gateway
  // (no card reader / live Run Payments call), 'live' = real processor, 'unconfigured' = card
  // tender will be refused until an admin configures the processor. '' = not yet loaded.
  @state() private cardRail: '' | 'test' | 'live' | 'unconfigured' = '';

  // The A-03 end-of-day close flow (movements → blind count → Z-report → complete).
  @state() private showTillClose = false;

  // ── W7 counter-v2 state ──
  // COD availability from pos_policy (branch override wins). null = not yet loaded → option disabled.
  @state() private codEnabled: boolean | null = null;
  @state() private fulfilment = 'PICKUP'; // COD only: the payment posture rides any fulfilment
  @state() private depositDollars = '';
  @state() private depositMethod = 'CASH';
  @state() private showReturn = false;
  @state() private showParked = false;
  @state() private parkedCount = 0;
  @state() private pricingDoc: PricingOrder | null = null;
  @state() private utilMsg = '';
  /**
 * rev 5: the utility banner's tone. It painted EVERY message success-green with no role:
 * "Park failed: retry." wore the ok banner and announced to no one. Faults now render.bad
   * with role=alert (the house rule: alert for faults only); good news keeps ok + role=status.
   */
  @state() private utilTone: 'ok' | 'bad' | 'note' = 'ok';
  /** A 'note' is announced only when asked for (a press), never on focus, where the closed control's own
   *  description already speaks: one announcement, not two. */
  @state() private utilLive = false;
  /**
   * UI-3 (visual GAP 1): where the utility message renders. The End Session reason belongs beside
   * its control in the top bar (a press answered about 680px away at the foot of the flow, its
   * lower edge under the hotkey bar with the Items step open); every other message keeps the foot
   * line. One state machine either way, so the next message still replaces the notice.
   */
  @state() private utilWhere: 'foot' | 'session' = 'foot';
  /**
   * Round d: the pickup line's captured-once facts, and only those: the ticket, the resume's own
   * tails (lines the product list dropped, lines the race discarded) and the fault tone. The
   * line's price tail is NOT captured: its words render from the cart's any-line-pending state
   * (pickupLineWords), so a price that answers after the line was set flips it, the way every
   * other marker on the till flips when a price answers. One line at a time: setUtil (any other
   * message) and every direct clear retire the pickup line with the utility message.
   */
  @state() private pickupLine: { ticketNo: string; tails: string; bad: boolean } | null = null;

  /** The pickup line's words: the tail derives from the cart at render time, never from a snapshot. */
  private pickupLineWords(): string {
    const line = this.pickupLine;
    if (line === null) return '';
    const pending = this.cart.some((l) => l.priceMissing);
    return `Resumed ${line.ticketNo}: ${pending ? 're-deriving prices' : 'prices re-derived now'}.${line.tails}`;
  }

  /** One door for the utility banner: message + tone together, so a fault can never wear green. */
  private setUtil(msg: string, tone: 'ok' | 'bad' | 'note' = 'ok', live = true, where: 'foot' | 'session' = 'foot'): void {
    this.utilMsg = msg;
    this.utilTone = tone;
    this.utilLive = live;
    this.utilWhere = where;
    this.pickupLine = null; // one message line at a time: a new message retires the pickup line
  }

  /** One door for the silent clear (a fresh ticket, a park, a pricing doc): the pickup line goes with it. */
  private clearUtil(): void {
    this.utilMsg = '';
    this.pickupLine = null;
  }
  // ── DIB-434 ── the in-progress guard's own state. `quickGuard` is the dialog's open flag; it is
  // set ONLY when quickStartNeedsGuard() says the till holds something to lose, so an empty
  // terminal reached by "+ Sale" starts fresh with no prompt at all.
  @state() private quickGuard = false;
  @state() private quickGuardBusy = false;
  // ── end DIB-434 ──
  // DIB-191: the direct order-id path (B2/C3). lookupOrderId is what the operator is typing;
  // lookupOpenId is the id actually opened, so the panel does not thrash on every keystroke.
  @state() private lookupOrderId = '';
  @state() private lookupOpenId = '';
  // DIB-379/F3: whether hos-order-lifecycle found lookupOpenId. Starts true (optimistic) so nothing
 // is suppressed before the first load resolves; hos-order-history renders only once we know: else
  // both panels independently 404 and paint "order not found" twice for the same bad id/order_no.
  @state() private lookupFound = true;
  // DIB-410: the receipt print flow's operator feedback (a blocked pop-up, a cross-branch refusal).
  @state() private receiptMsg = '';
  @state() private receiptBusy = false;

  /**
 * DIB-444: the confirmation panel is dismissible, and dismissing it must not survive into the
   * NEXT sale. Reset wherever a ring begins or the terminal is cleared, so the counter never rings a
   * sale that confirms silently because someone closed the previous panel.
   */
  @state() private soldPanelDismissed = false;

  /**
 * DIB-410: print the receipt for a committed sale. The document is FETCHED with the staff token
   * and written into a blank window (window.open on the route would send no Authorization header and
 * render a 401 at the customer). `saleRef` is the order id or its visible "#SO-" number: the
   * server resolves both.
   */
  private async printSaleReceipt(saleRef: string): Promise<void> {
    if (!saleRef || this.receiptBusy) return;
    this.receiptBusy = true;
    this.receiptMsg = '';
    try {
      const out = await printReceipt(saleRef, openBlankWindow);
      if (!out.ok) this.receiptMsg = out.message;
    } catch (e) {
      this.receiptMsg = e instanceof Error ? e.message : 'Could not load the receipt.';
    } finally {
      this.receiptBusy = false;
    }
  }

  /**
 * DIB-191: a lifecycle transition appended a row, so refresh every status timeline on screen:
   * otherwise the chip shows the new status above a chain that still ends at the old one.
   */
  private onOrderStatusChanged = (): void => {
    this.refreshOrderPanels();
  };

  /**
 * DIB-175: an edit landed, so refresh the change history AND the status timeline: and re-read the
   * edit panel itself, because an edit moves the order's money and the server recomputes it.
   */
  private onOrderChanged = (): void => {
    this.refreshOrderPanels();
  };

  /**
 * DIB-489/490: a panel asked this surface to OPEN another order: the freshly created duplicate
   * draft ("Open #SO-…"), or DIB-490's "open that order first" on a suspected double-key. Routing
   * it through the existing lookup keeps one order-detail surface: the same lifecycle/history/edit
   * panels open for it that open for any looked-up order.
   */
  private onOpenOrder = (e: CustomEvent<{ orderId: string; orderNo?: string }>): void => {
    const id = e.detail?.orderId ?? '';
    if (id === '') return;
    this.lookupOrderId = e.detail.orderNo || id;
    this.lookupOpenId = id;
    this.lookupFound = true; // reset the suppression for the new lookup, exactly like the Open button
  };

  /**
   * DIB-491: an ORDER chip in a Related documents panel was clicked (a sibling order minted from
 * the same quote). Open it through the existing lookup path: the same door "Open order" uses:
   * so every panel the lookup renders comes along. Other kinds never arrive: open-kinds="ORDER"
   * renders them as labels, but the guard stays because a guard beats an assumption.
   */
  private openRelatedOrder = (e: CustomEvent<RelatedDoc>): void => {
    if (e.detail.kind !== 'ORDER') return;
    this.lookupOrderId = e.detail.number || e.detail.id;
    this.lookupOpenId = e.detail.id;
    this.lookupFound = true; // reset the suppression, exactly as the Open-order button does
  };

  /**
   * Refreshes every order panel on screen without a page reload. A status transition narrows what is
   * editable (line edits die at READY) and an edit appends change rows, so BOTH events have to touch
 * all three panels: refreshing only the one that fired leaves a stale sibling next to it.
   */
  private refreshOrderPanels(): void {
    this.renderRoot
      ?.querySelectorAll('hos-order-history, hos-order-edits, hos-order-edit')
      .forEach((el) => (el as HTMLElement & { reload?: () => void }).reload?.());
  }

  // DIB-195: when this terminal is mounted inside a popped-out window, the shell passes the
  // opener's in-progress order here and we apply it ONCE on first update, so the popped window
 // opens on the SAME ticket instead of empty. null (the default): the normal in-tab mount:
  // is a no-op, so an in-shell terminal is entirely unaffected.
  @property({ attribute: false }) restore: PosOrderSnapshot | null = null;
  private restored = false;

  /**
   * What the Complete-sale CTA's keycap says.
   *
 * UNION TRAIN 14: IT READS THE COUNTER'S OWN ROSTER, and since D-111 that is the ONLY thing it
   * can read.
   *
   * There are two hotkey substrates and they are not the same thing:
   * · the COUNTER roster (`surface_role_items` kind `action` on `pos.counter`, under it the
 * platform roster of 0191, under that `defaultHotkeys` in internal/pos/hotkeys.go): this is
   * what `onPosKey` actually listens to;
 * · the SHELL's map (`dealer_hotkeys` over 0191's `app.shell` roster): a different table for
   * the app-wide nav keys.
   *
 * Until the R1 ruling both bound pay to F8, so nobody noticed this chip was reading the
   * wrong one. R1 moved the counter's pay key to F12 and left the shell alone, so the chip began
 * advertising a key that is deliberately UNBOUND: a clerk who trusts it presses F8 and the sale
   * does not ring. Train 14 fixed the READ (this method) and left the shell's `sale.pay` in place
   * as a fallback; D-111 finished the job by retiring `sale.pay` from the shell map entirely, so
   * there is no longer a second source to fall back TO and no way for the two to disagree again.
   * The chip's whole job is to name the key that rings THIS sale; now only the roster that rings
   * it can answer.
   */
  private payKeycap(): string | null {
    const mine = this.hotkeyRoster.find((r) => r && r.action === HK_PAY);
    return mine?.key ? keycap(mine.key) : null;
  }

  // DIB-246/247: the server's parked row keeps only customer_id and price-less lines, so the
 // terminal holds the richer DISPLAY facts captured at park time: the selected customer and the
 // ticket total: keyed by the server ticket id. Read on resume (restore the customer) and by the
  // parked list (show who + how much). In-memory per terminal; a cold entry degrades to walk-in.
  private parkedInfo = new Map<string, ParkedInfo>();

  static styles = [
    focusRing,
    css`
    :host {
      display: block;
      font-family: var(--hos-font-ui);
      color: var(--hos-text);
      background: var(--hos-deep-earth);
      /* THE COUNTER'S ONE MEASURE, declared once and read by both things that cap: the sale column
         (.flowcol) and the Counter-settings overlay (.cspanel). It was two literals: 980 and 760:
         and two literals that have to agree are a disagreement waiting for one of them to be edited.
         Local, and deliberately NOT --hos-*: the theme gate (_theme-tokens.test.mjs) requires every
         --hos-* name to be declared in tokens.css, and this is one surface's number, not the app's.
         The idiom is the app's own: pos-counter-settings.ts:123-125 already carries --cs-section /
         --cs-body / --cs-caption on its host for the same reason.
         WHY 1180 AND NOT none. The wave-1 layout contract makes 'pos' a work pane, so the shell
         no longer caps this surface at all; the counter therefore has to answer the width question
         itself rather than inherit an answer. It keeps a cap, because a sale is READ top to bottom
         and a 1808px line of sale steps is a worse read than a measured one: the argument at
         .flowcol below, which predates the contract and survives it. What changes is the number:
         980 was chosen against a pane that was itself capped at 1000, so it left 844px of a 1920
         screen dead INSIDE the pane. 1180 is the same argument re-costed against the real pane. */
      --pos-column-max: 1180px;
    }
    /* ── THE HEADER SITS ON THE MEASURE OF THE WORK IT HEADS ─────────────────────────────────────
       Measured before this rule existed: at 1920 the page title started at x=104 and the first
       accordion card at x=418: 314px apart, on one screen, with the bar's own hairline running
       the full 1808px pane above them. At 1440 the same gap was 74px and at 1024 it inverted to 8,
       so the header and the column were never on the same grid; they merely agreed at one width.
       A title that does not line up with the thing it titles is a second, invisible column.

       The BAR stays full-bleed and the CONTENT is capped, which is why there is an inner element:
       the hairline is a page-wide divider (it separates the counter's chrome from its work and
       has to reach both edges to do that), while the words inside it belong to the same 1180px
       measure as .flowcol. Same box model, same side padding, same min()/auto-margin pair, so the
       two left edges resolve to the identical number at every width by construction rather than by
       a matched literal. If --pos-column-max is ever re-costed: or removed: both move together.
       Nothing here re-caps the PAGE: the pane stays fluid and the bar still paints edge to edge. */
    /* ── THE COLD COUNTER'S TYPE SCALE ─────────────────────────────────────────────────────────
       Measured on a cold screen before any panel is opened: 22 / 15 / 13 / 12 / 11 / 10px: SIX
       sizes against a budget of four, and the two extra ones were not carrying a distinction. 12px
       was doing the same job as 11 (a caption, a small control's label, a numbered chip) one pixel
       larger, and 10px was the keycap glyph alone, whispering under the legend word beside it.
       Both fold into 11 and the surface reads 22 (the page's one title) / 15 (a step's name) /
       13 (the body: everything a person reads as a sentence or presses as a full-size control) /
       11 (captions, field labels, small chrome controls, chips and keycaps). Four roles, four
       sizes, and every rule below that says 11 says it because it is one of those roles.
       Sizes above 22 still exist and are not in this count on purpose: they belong to the money
       figures on an OPEN payment step (the totals card's 26px, the sold panel's 30px), which are
       the one thing on this screen that is meant to be read across a counter. */
    .topbar {
      border-bottom: 1px solid var(--hos-line);
    }
    .topbarin {
      box-sizing: border-box;
      display: flex;
      align-items: center;
      justify-content: space-between;
      /* UI-3 (visual GAP 6): the bar fits one row at the column cap with no session open; the
         row gap keeps a wrapped bar (an open session adds the dot and End Session) as airy as before. */
      gap: 16px 12px;
      width: min(100%, var(--pos-column-max));
      margin: 0 auto;
      padding: 12px var(--hos-space-5); /* rev 5: on-scale gutter (was 22px) */
      flex-wrap: wrap;
    }
    .topbar .title {
      font-size: 16px;
      font-weight: 600;
    }
    .topbar .title span {
      color: var(--hos-stone-amber-text);
    }
    .session {
      display: flex;
      align-items: center;
      gap: 8px;
      font-size: 13px;
      color: var(--hos-text-dim);
      /* ui3d P2-1: the End Session notice's positioning context: the notice hangs off the whole
         session region, so its anchored right edge and its 320px of width can never reach past
         the window's left edge the way a button wrap anchor did on a wrapped bar. */
      position: relative;
    }
    .timer {
      font-family: var(--hos-font-mono);
      color: var(--hos-text);
    }
 /* rev 5: the session control used class="util", which only matches the.util CONTAINER rule:
       it rendered as a raw UA button (white chrome, wrapped label) in the top-right of every
       frame. .topbtn is the quiet chrome-button role the top bar actually needs. */
    .topbtn {
      flex: none;
      background: transparent;
      border: 1px solid var(--hos-line);
      color: var(--hos-text-dim);
      border-radius: var(--hos-radius-control);
      padding: 7px 12px;
      font-size: 11px; /* the small-chrome role (see the type-scale note) */
      font-family: inherit;
      white-space: nowrap;
      cursor: pointer;
    }
    /* HH note: disabled drops its fill and goes grey; fade is not a state. */
    .topbtn:disabled,
    .topbtn[aria-disabled='true'] {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
    .topbtn:hover:not(:disabled):not([aria-disabled='true']) {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    /* ── DIB-488 rev 8 (item 2): the concept header bar ─────────────────────────────────────────
       Large bold sale title over the yard context (the brand lockup stays as the eyebrow: the
       DIB-456 gate pins it); clock + session dot + Help + the page-level gold + New Sale on the
       right. All tokens; the flow stepper between is untouched. */
    .headid {
      display: flex;
      flex-direction: column;
      gap: 1px;
      min-width: 0;
    }
    .headid .title {
      font-size: 11px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
    }
    .pagetitle {
      margin: 0;
      font-size: 22px;
      font-weight: 700;
      line-height: 1.15;
      color: var(--hos-text);
      white-space: nowrap;
    }
    .pagectx {
      font-size: 11px; /* a caption under the title (see the type-scale note) */
      color: var(--hos-text-dim);
      white-space: nowrap;
    }
    .session .clock {
      font-family: var(--hos-font-mono);
      font-size: 13px;
      color: var(--hos-text);
    }
    .sessiondot {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-size: 11px; /* small chrome, beside the topbtn it pairs with */
      color: var(--hos-success);
    }
    .sessiondot .dot {
      width: 8px;
      height: 8px;
      border-radius: var(--hos-radius-pill);
      /* DIB-468 rule: a SOLID green fill uses the per-theme fill token, never the text green. */
      background: var(--hos-success-fill);
    }
    .topbtn.help {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 30px;
      height: 30px;
      padding: 0;
 /* row 523: one circle. The.topbtn border is overridden away here; the drawn mark's
         circle is the ring, at the button's 30px size, so the hit area is unchanged */
      border: none;
      text-align: center;
      font-weight: 700;
      border-radius: var(--hos-radius-pill);
    }
    .helpwrap {
      position: relative;
      display: inline-flex;
    }
    .helppop {
      position: absolute;
      top: calc(100% + 8px);
      right: 0;
      z-index: var(--hos-z-modal);
      width: 320px;
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      box-shadow: var(--hos-elev-1);
      padding: 12px 14px;
      font-size: 12px;
      color: var(--hos-text);
      text-align: left;
    }
    .helppop h3 {
      margin: 0 0 6px;
      font-size: 11px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
    }
    .helppop ul {
      margin: 0;
      padding-left: 16px;
      display: grid;
      gap: 6px;
    }
 /* The counter-view popover wears the Help flyout's clothes: same anchor, same panel, one
       set of chrome. What is inside it is switches, not sentences. */
    .viewpop {
      width: 220px;
    }
    .viewpop .sw {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      width: 100%;
      padding: 7px 2px;
      background: transparent;
      border: 0;
      border-top: 1px solid var(--hos-line);
      color: var(--hos-text);
      font: inherit;
      font-size: 13px;
      text-align: left;
      cursor: pointer;
    }
    .viewpop .sw:first-of-type {
      border-top: 0;
    }
    /* 0220 · the layout radios. Same row rhythm as the switches below them so the menu reads as one
       list, but a check mark rather than a pip: a pip promises an independent on/off, and these two
       are one question with two answers. */
    .viewpop .layoutpick {
      display: block;
      margin-bottom: 2px;
      padding-bottom: 4px;
      border-bottom: 1px solid var(--hos-line);
    }
    .viewpop .layoutpick .sw {
      border-top: 0;
      padding: 6px 2px;
    }
    .viewpop .sw .tick {
      flex: none;
      width: 14px;
      text-align: center;
      color: var(--hos-stone-amber-text);
      opacity: 0;
    }
    .viewpop .sw[aria-checked='true'] {
      color: var(--hos-stone-amber-text);
    }
    .viewpop .sw[aria-checked='true'] .tick {
      opacity: 1;
    }
    .viewpop .sw .pip {
      flex: none;
      width: 30px;
      height: 17px;
      border-radius: var(--hos-radius-pill);
      border: 1px solid var(--hos-line);
      background: var(--hos-deep-earth);
      position: relative;
    }
    .viewpop .sw .pip::after {
      content: '';
      position: absolute;
      top: 2px;
      left: 2px;
      width: 11px;
      height: 11px;
      border-radius: 50%;
      background: var(--hos-text-dim);
      transition: transform 120ms ease;
    }
    .viewpop .sw[aria-pressed='true'] .pip {
      border-color: var(--hos-stone-amber);
    }
 /* The admin editor's modal: the quote pilot's Screen settings shape, one screen over. */
    .csoverlay {
      position: fixed;
      inset: 0;
      z-index: var(--hos-z-modal);
      background: var(--hos-scrim);
      display: grid;
      place-items: start center;
      padding: 24px 16px;
      overflow: auto;
    }
    /* COUNTER SETTINGS IS A FORM, and the wave-1 contract says what a form owes the width it is
       given: columns, or it ships as one long column with a 44px keycap stranded at the right-hand
       end of a 1146px row. The panel was min(760px, 100%) in CONTENT-BOX, so it painted 794px and
       handed pos-counter-settings.ts a 760px form column: and a 734px tile row inside it, measured
       on the live stack and IDENTICAL at 1024, 1440, 1680 and 1920, which is the defect stated as
       one number: a screen that answered a 24" monitor and a shop-floor panel with the same row.
       It is now the counter's one measure, and the columns are pos-counter-settings.ts's job (its
       root grid, with the tile board and the history spanning every column because they are the
       widest rows there). The panel is min()'d, not fixed, so a 1024 till still gets 992 of its 1024
       and the overlay never becomes a wall it cannot see the edges of.
       BORDER-BOX because width and padding in the same rule must not add up to something else:
       the old rule promised 760 and painted 794, which is exactly the kind of 34px that goes
       unnoticed until a 1024 till has 4px of gutter on one side. */
    .cspanel {
      box-sizing: border-box;
      width: min(var(--pos-column-max), 100%);
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      box-shadow: var(--hos-elev-1);
      padding: 14px 16px;
    }
    .cshead {
      display: flex;
      align-items: center;
      justify-content: space-between;
      margin-bottom: 10px;
    }
    .cshead h2 {
      margin: 0;
      font-size: 16px;
    }
    .viewpop .sw[aria-pressed='true'] .pip::after {
      background: var(--hos-stone-amber);
      color: var(--hos-stone-amber-ink); /* E6: a theme-stable fill pins its theme-stable ink */
      transform: translateX(13px);
    }
    /* an inline-flex centred row keeps the button the exact height the words
      line box always gave it, the drawn glyph beside them centred (PR 55 review round; the
      quick-action-bar precedent) */
    .newsale {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      background: var(--hos-stone-amber);
      border: 1px solid var(--hos-stone-amber);
      border-radius: var(--hos-radius-control);
      color: var(--hos-stone-amber-ink); /* the ONLY ink gold may carry (D-078) */
      font-family: inherit;
      font-size: 13px;
      font-weight: 700;
      padding: 8px 14px;
      white-space: nowrap;
      cursor: pointer;
    }
    /* vertical-align: middle on the drawn glyph keeps the icon on the
      text middle and the line box the character left behind, so the button keeps its exact
      height (PR 55 review round; the quick-action-bar precedent centers, this keeps the box) */
    .stepper button svg,
    .sold-head .dismiss svg,
    summary .disc svg,
    .viewpop .sw svg {
      vertical-align: middle;
    }
    .newsale:hover {
      background: var(--hos-stone-amber-hover);
      border-color: var(--hos-stone-amber-hover);
      color: var(--hos-stone-amber-ink); /* E6: a theme-stable fill pins its theme-stable ink in the SAME rule */
    }
    /* ── the counter's SINGLE COLUMN (the restructure) ───────────────────────────────────────────
       One measure, centred, four accordion cards down it. The column is width-capped rather than
       full-bleed because a sale is read top-to-bottom: a 1920px-wide line of text is a worse read
       than a 1180px one, and the space either side is where the eye rests. Nothing scrolls inside
       anything: the PAGE scrolls, once, which is what makes "scroll the open step fully into
       view" a single honest gesture instead of two nested ones fighting each other.

       THE CAP SURVIVES; THE NUMBER DOES NOT. 980 was picked when this pane was capped at 1000px by
       the shell, so the counter's column and the page it sat on were within 20px of each other and
       the choice cost nothing. The wave-1 layout contract classifies 'pos' as work and takes the
       shell's cap away, at which point 980 stopped being a reading measure and started being the
       whole defect one level down: measured on the live stack at 1920, .flowcol painted 1020 of
       an 1864px pane: 422px dead either side, 844px in total, a cap the page-level audit could not
       see because it reads the PANE and the pane was already fluid. Raised to --pos-column-max
       (1180) the same argument holds and the arithmetic changes: 1140px of content, and the dead
       page inside the pane at 1920 goes 844 → 684, still centred, still resting space rather than
       waste. The Items step's hot list takes the difference without a line of code: pos-hot-
       list.ts:97 is repeat(auto-fill, minmax(132px, 1fr)), so its board went from 946px in 6 tracks
       to 1106px in 7, the tile itself unchanged at 151px.

       BORDER-BOX, and this is not tidiness. The rule was content-box with 20px side padding, so
       width: min(100%, 980px) painted 1020, and at 1024: where the rule resolves to width:100%
      : the column painted 936 inside a 912px pane content box: 28px of gutter on the left and 4px
       on the right, the difference swallowed by the pane's own padding so nothing scrolled and
       nobody noticed. The cap now means the box, at every width. */
    .flowcol {
      box-sizing: border-box;
      display: flex;
      flex-direction: column;
      gap: var(--hos-space-3);
      width: min(100%, var(--pos-column-max));
      margin: 0 auto;
 /* THE BOTTOM LANE: one number at every width, because the two things it clears are one size
         at every width. Two fixed bars sit on the counter's bottom edge: the pinned hotkey legend
         (pos-hotkey-legend.ts, bottom: 0, measured 35px) and the shell's Feedback pill
         (bottom: var(--hos-fab-inset), 38px tall). They OVERLAP: the pill sits on top of the
         legend's right-hand end, which is why the legend's own dismiss control is deliberately not
         right-flushed (pos-hotkey-legend.ts:113-116): so the clearance a scrolled-to-the-bottom
         column needs is the GREATER of the two, never the sum. --hos-fab-lane is 22 + 38 + 22 = 82
         and the legend is 35, so the lane is the greater at both pointer types and the legend's
         half of the old workaround costs nothing extra. --hos-safe-bottom is added because both
         bars honour it, so on a device with a home indicator both rise by exactly that much. */
      padding: var(--hos-space-4) var(--hos-space-5) calc(var(--hos-fab-lane) + var(--hos-safe-bottom));
    }
    /* The whole-sale utilities (park / parked / clear) and the stat bar sit UNDER the cards, so
       they are reachable from whichever step is open: see renderSaleUtilities. */
    .saleutil {
      display: flex;
      align-items: center;
      flex-wrap: wrap;
      gap: 8px;
    }
 /* ── rev 8 (item 12): the footer transaction bar: pinned under the sale panel's scroll ── */
    .txbar {
      display: flex;
      align-items: center;
      flex-wrap: wrap;
      gap: 6px 0;
      margin-top: var(--hos-space-2);
      padding: 8px 14px;
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      font-size: 11px; /* the footer stat bar is a caption row (see the type-scale note) */
      color: var(--hos-text-dim);
      flex: none;
    }
    .txbar .seg {
      white-space: nowrap;
    }
    .txbar .seg + .seg::before {
      content: '·';
      margin: 0 10px;
      color: var(--hos-line);
    }
    .txbar .seg[data-testid='txbar-profit'] {
      color: var(--hos-success);
      font-family: var(--hos-font-mono);
    }
    .txbar .partial {
      color: var(--hos-text-dim);
    }
    /* A touch till (1024×768 is the shop-floor panel) gets the same column with more air under the
       thumb: bigger tap targets and a tighter gutter, so the column keeps as much of a narrow
       screen as it can. The layout does not CHANGE shape: that was the whole point of one column:
       it just loosens.

       WHAT LEFT THIS RULE, AND WHY. It used to carry two more declarations and neither survives.
         · width: 100% was doing nothing the base rule does not: min(100%, var(--pos-column-max))
           already resolves to 100% below the cap, and restating it was a second place to edit.
         · padding-bottom: 132px was a GUESS at the Feedback pill's size, made by a surface that
           had no way to ask. The pill measures 82px of lane and 35px of that is behind the legend,
           so the touch till was reserving 50px of blank page it did not need: and reserving it
           only under 1180px, as though a 1920px screen had a smaller pill. The lane is now a token
           (--hos-fab-lane, tokens.css) derived from the pill's own declarations, the base rule
           spends it at every width, and this breakpoint has no opinion about the bottom edge left
           to hold. What it kept is the GUTTER: 12px of side and top instead of 20/16: which is
           the touch-till half of the original ruling and is about the thumb, not about the pill.
       Written as three longhands rather than a shorthand on purpose: a shorthand here would reset
       padding-bottom to 0 and silently drop the lane the base rule just reserved. */
    @media (max-width: 1180px) {
      .flowcol {
        padding-top: var(--hos-space-3);
        padding-left: var(--hos-space-3);
        padding-right: var(--hos-space-3);
      }
      /* The header follows the column's touch-till gutter for the same reason it follows its
         measure: below the cap both are full width, so the only thing that can put the title and
         the cards on different left edges is a different side padding: which is precisely what
         the 8px offset measured at 1024 was. The BOTTOM padding is untouched here: the bar has no
         lane to reserve and a shorthand would silently zero the block padding it does keep. */
      .topbarin {
        padding-left: var(--hos-space-3);
        padding-right: var(--hos-space-3);
      }
    }
    @media (pointer: coarse) {
      .fulfil button,
      .paytiles button,
      .custmode button {
        min-height: 56px;
      }
    }
    .panel {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      box-shadow: var(--hos-elev-1);
      padding: var(--hos-space-4); /* rev 5: on-scale (was 18px sides) */
      outline: none; /* panels take programmatic focus from the stepper; the ring is for controls */
    }
    /* Panel head: the step number echoes the header flow so panel and step read as one thing. */
    /* The in-memory Lines/Cards view flip (D-096 sets the DEFAULT; this is the per-terminal view).
       DIB-488 rev 3: the same pill idiom also wears the lookup panel's cards ⇄ rows toggle in its
       bar (that one PERSISTS per user: it rides the workspace row).
       DIB-488 rev 2: lives in the Sale panel's config popover: the panel-config affordance is
       where the existing presentation options moved. */
    /* Browsing is the exception path: one quiet disclosure, never a second search surface open
       beside the first. Closed by default: the counter's default posture is "I know what I want". */
    .browse > summary {
      cursor: pointer;
      font-size: 12px;
      color: var(--hos-text-dim);
      padding: 6px 0;
    }
    .browse > summary:hover {
      color: var(--hos-text);
    }
    /* The read-back: a receipt-shaped block, deliberately not a form. Nothing in it is a control. */
    .readback {
      font-size: 13px;
    }
    .readback .rbwho {
      display: flex;
      align-items: baseline;
      gap: 8px;
      padding-bottom: 8px;
      border-bottom: 1px solid var(--hos-line);
    }
    .readback .rbwho .k {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--hos-text-dim);
    }
    .readback .rbwho .v {
      font-weight: 600;
    }
    .readback .rblines {
      list-style: none;
      margin: 0;
      padding: 8px 0;
    }
    .readback .rblines li {
      display: grid;
      grid-template-columns: 3ch minmax(0, 1fr) auto;
      gap: 10px;
      align-items: baseline;
      padding: 3px 0;
    }
    .readback .q,
    .readback .m {
      font-family: var(--hos-font-mono);
    }
    .readback .m {
      text-align: right;
    }
    .readback .rbsave {
      margin-left: 8px;
      font-size: 10px;
      color: var(--hos-success);
    }
    .readback .rbrow {
      display: flex;
      justify-content: space-between;
      gap: 10px;
      padding: 3px 0;
      color: var(--hos-text-dim);
    }
    .readback .rbrow.total {
      padding-top: 8px;
      border-top: 1px solid var(--hos-line);
      color: var(--hos-text);
      font-weight: 700;
      font-size: 15px;
    }
    .readback .rbrow.saving span {
      color: var(--hos-success);
    }
    .ticketbar {
      display: flex;
      justify-content: flex-end;
      margin-bottom: 6px;
    }
    .viewflip {
      display: inline-flex;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-pill);
      overflow: hidden;
    }
    .viewflip button {
      background: transparent;
      border: 0;
      color: var(--hos-text-dim);
      font-family: inherit;
      font-size: 11px;
      padding: 4px 10px;
      cursor: pointer;
    }
    .viewflip button.active {
      background: color-mix(in srgb, var(--hos-line) 40%, transparent);
      color: var(--hos-text);
    }
    /* ── DIB-488 LINES presentation: the compact thumbnail line table ──
       rev 6 (item B): five working columns (thumb · item · qty · UNIT · money): the unit gets its
       own aligned column (mock parity) instead of riding the price string; unit price + line total
       still stack in the money column (price dim above, line total gold below) so the money keeps
       its prominence. */
    /* rev 8 (item 3): Item | Qty | Unit | Price | Total as SEPARATE aligned columns (price and
       total no longer stacked), plus the item-9 Total Weight column; the item-10 Margin column
       joins ONLY while the role-gated data is present and un-hidden (.withmargin). */
    /* DIB-512 · ONE grid for header + every row (subgrid): each row used to be its own grid, so
       'auto' columns resolved per row and the qty/price clusters drifted left and right with each
       row's unit and price string. The container owns the tracks; rows adopt them: every column
       shares one edge down the whole ticket. */
    .linegrid {
      display: grid;
      grid-template-columns:
        36px minmax(110px, 1fr) auto minmax(28px, auto) minmax(52px, auto) minmax(64px, auto)
        minmax(52px, auto) 24px;
      column-gap: 7px;
    }
    .linegrid.withmargin {
      grid-template-columns:
        36px minmax(110px, 1fr) auto minmax(28px, auto) minmax(52px, auto) minmax(64px, auto)
        minmax(52px, auto) minmax(40px, auto) 24px;
    }
    .linehead,
    .linerow {
      display: grid;
      grid-column: 1 / -1;
      grid-template-columns: subgrid;
      align-items: center;
    }
    .linerow .lweight,
    .linerow .lmargin {
      font-family: var(--hos-font-mono);
      font-size: 11px;
      color: var(--hos-text-dim);
      text-align: right;
      white-space: nowrap;
    }
    .linerow .lunit {
      font-family: var(--hos-font-mono);
      font-size: 11px;
      color: var(--hos-text-dim);
      text-transform: uppercase;
      white-space: nowrap;
    }
    .linehead {
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.09em;
      color: var(--hos-text-dim);
      padding: 8px 0 6px;
      border-bottom: 1px solid var(--hos-line);
    }
    .linehead .r {
      text-align: right;
    }
    .linerow {
      padding: 10px 0;
      border-bottom: 1px solid var(--hos-line);
    }
    .linerow .lmain {
 /* rev 8: the item column keeps a real floor: the NAME must never fall to one word per
         line under the added columns; the SKU/stock line truncate instead of wrapping. */
      min-width: 0;
      overflow: hidden;
    }
    .linerow .lsku,
    .linerow .lmain .stockline {
      white-space: nowrap;
      overflow: hidden;
      text-overflow: ellipsis;
    }
    .linerow .lprice {
      font-family: var(--hos-font-mono);
      font-size: 12px;
      color: var(--hos-text-dim);
      white-space: nowrap;
      text-align: right; /* rev 8 (item 3): its own aligned column now */
    }
 /* The DIB-488 stock-status line: status colours via the existing token roles only. */
    .stockline {
      font-size: 11px;
      margin-top: 2px;
    }
    .stockline.ok {
      color: var(--hos-success);
    }
    .stockline.low {
      color: var(--hos-stone-amber-text);
    }
    .stockline.out {
      color: var(--hos-safety-red);
    }
    /* ── rev 8 (item 8 · DIB-502): the suggested add-ons strip below the ticket ── */
    .addons {
      margin-top: 14px;
      border-top: 1px solid var(--hos-line);
      padding-top: 10px;
    }
    .addonshead {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      margin-bottom: 8px;
    }
    .addongrid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
      gap: 10px;
    }
    .addontile {
      display: flex;
      flex-direction: column;
      gap: 8px;
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 10px 12px;
    }
    .addontop {
      display: flex;
      gap: 10px;
      align-items: center;
      min-width: 0;
    }
    .addontop hos-product-thumb {
      flex: none;
    }
    .addonmain {
      min-width: 0;
    }
    .addonname {
      font-size: 13px;
      font-weight: 600;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .addonsize {
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 1px;
    }
    .addonprice {
      font-family: var(--hos-font-mono);
      font-size: 13px;
      font-weight: 600;
      color: var(--hos-stone-amber-text);
      margin-top: 2px;
    }
    .addonfoot {
      display: flex;
      align-items: center;
      gap: 8px;
      margin-top: auto;
    }
    .addonuom {
      font-family: var(--hos-font-mono);
      font-size: 11px;
      color: var(--hos-text-dim);
      text-transform: uppercase;
    }
    /* DIB-512 · SUGGESTION adds are quiet (accent discipline: gold never decorates, and exactly
       one gold primary per panel: the commit CTA). Same button, same handler, quieter clothes. */
    /* an inline-flex centred row keeps the button the exact height the words
      line box always gave it, the drawn glyph beside them centred (PR 55 review round; the
      quick-action-bar precedent) */
    .addonadd {
      display: inline-flex;
      align-items: center;
      gap: 6px;
      margin-left: auto;
      background: transparent;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      font-family: inherit;
      font-size: 12px;
      font-weight: 600;
      padding: 6px 12px;
      cursor: pointer;
    }
    .addonadd:hover {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    /* ── DIB-488 customer card: the explicit Walk-in / Account resolution tiles ── */
    /* rev 8 (item 5): the customer question wears UNDERLINE TABS (concept parity) instead of the
       filled segment tiles: same two buttons, same handlers, same aria-pressed; only the clothes
       changed. The active tab is the gold ink over an amber underline; the rest keeps a quiet
       hairline base so the pair still reads as one control. */
    .custmode {
      display: grid;
      grid-template-columns: 1fr 1fr;
      border-bottom: 1px solid var(--hos-line);
      margin-bottom: 12px;
    }
    .custmode button {
      background: transparent;
      border: none;
      border-bottom: 2px solid transparent;
      margin-bottom: -1px; /* the active underline sits ON the base hairline */
      color: var(--hos-text-dim);
      font-family: inherit;
      font-size: 13px;
      font-weight: 600;
      padding: 10px 8px;
      cursor: pointer;
    }
    .custmode button:hover {
      color: var(--hos-stone-amber-text);
    }
    .custmode button.active {
      color: var(--hos-stone-amber-text);
      border-bottom-color: var(--hos-stone-amber);
    }
    /* ── DIB-488 fulfilment segment: the EXISTING sale types, worn as tiles ── */
 /* ── THE FOUR OPTIONS GET ONE ROW, AND THE ITEMS STEP GETS 100px BACK ──────────────
       Measured before the change, at 1920 with the Items step open and an empty ticket: the
       fulfilment label plus this group was 158px of a 494px card: 32% of the step: with 140px of
       it the tiles, a 2×2 of icon-over-label at 66px each. The commander asked whether fulfilment
       should come OUT of Items so item search has more room; it cannot move (DIB-551 settled where
       it lives, locked with the client), so it gets smaller instead. Icon BESIDE label on one row
       is the same four buttons, the same gates and the same aria: about 40px instead of 140.

       THE COST IS HIGHEST WHEN SEARCH MATTERS MOST, which is why this was worth doing: an empty
       ticket is exactly when a person is hunting for the first product, and it was the moment the
       tiles took their largest share of the card.

       auto-fit rather than a breakpoint: this group renders in a 1106px column on the standard
       counter and in a panel the person may have dragged to 280px, and minmax lets the same rule
       answer both: four across where they fit, wrapping where they do not. The doctrine's own
       instruction, and the reason there is no media query here. */
    /* UI-3 (visual GAP 9): 112px, not 132px, so all four choices (Collect now, Will-call, Delivery,
       COD) share one row at the Panels layout's default panel width (about 535px inside); the
       tile's gap and side padding below tighten with it so the longest label still fits. */
    .fulfil {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(112px, 1fr));
      gap: 8px;
    }
 /* rev 8 (item 6): all four options as ICON TILES: inline currentColor SVG (the rev-6
       tender-icon discipline: aria-hidden art, the tile's own ink in every state, zero new
       colours), the same buttons and the same gates. */
    .fulfil button {
      display: flex;
      flex-direction: row;
      align-items: center;
      justify-content: center;
      gap: 6px;
      min-height: 40px;
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      font-family: inherit;
      font-size: 13px;
      font-weight: 600;
      padding: 8px 6px;
      cursor: pointer;
    }
    .fulfil button svg {
      flex: none;
      opacity: 0.85;
    }
    .fulfil button:hover:not(:disabled) {
      border-color: var(--hos-stone-amber);
    }
    .fulfil button.active {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
      background: color-mix(in srgb, var(--hos-stone-amber) 10%, var(--hos-deep-earth));
    }
    .fulfil button:disabled {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
 /* ── DIB-488 collapsible more-options (native details: no JS state to desync) ── */
    details.more {
      margin-top: 12px;
      border-top: 1px solid var(--hos-line);
      padding-top: 4px;
    }
    details.more > summary {
      cursor: pointer;
      list-style: none;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      padding: 8px 0 4px;
    }
    /* HH-203: the caret drew from a font character in ::before content; it now draws from the
       registry in the summary markup (.disc span), and these rules swap closed for open exactly
       as the two content rules did. */
    details.more > summary .disc .open {
      display: none;
    }
    details.more[open] > summary .disc .closed {
      display: none;
    }
    details.more[open] > summary .disc .open {
      display: inline;
    }
 /* WAVE 2 · finding 13: the order-administration disclosure under a sold counter sale. It
       borrows the more-options disclosure's clothes on purpose: this counter already has exactly
       one way of saying "there is more here, and it is quieter than what you came for", and a
       second one would be a second vocabulary for one idea. It sits BELOW the sold panel with a
       rule above it, so the thing a clerk came for keeps the top of the card to itself. */
    details.orderadmin {
      margin-top: var(--hos-space-3);
      border-top: 1px solid var(--hos-line);
      padding-top: 4px;
    }
    details.orderadmin > summary {
      cursor: pointer;
      list-style: none;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      padding: 8px 0 4px;
    }
 /* HH-203: same caret move as details.more: the mark draws in the markup (.disc span), and
       the [open] state swaps the two drawings as the content rules once did. */
    details.orderadmin > summary .disc .open {
      display: none;
    }
    details.orderadmin[open] > summary .disc .closed {
      display: none;
    }
    details.orderadmin[open] > summary .disc .open {
      display: inline;
    }
    /* ── DIB-488 payment tiles + the one CTA ── */
    .paytiles {
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 8px;
      margin-top: 10px;
    }
    .paytiles button {
      display: flex;
      align-items: center;
      justify-content: center;
      gap: 8px; /* rev 6 (item C): label sits beside its inline icon */
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      color: var(--hos-text);
      font-family: inherit;
      font-size: 13px;
      font-weight: 600;
      padding: 10px 8px;
      cursor: pointer;
    }
 /* rev 6 (item C): small monochrome tender icons: inline SVG on currentColor, so they take the
       tile's own ink in every state (rest, hover, active gold, disabled) with zero new colours. */
    .paytiles button svg {
      flex: none;
      opacity: 0.85;
    }
    .paytiles button:hover:not(:disabled) {
      border-color: var(--hos-stone-amber);
    }
    .paytiles button.active {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
      background: color-mix(in srgb, var(--hos-stone-amber) 10%, var(--hos-deep-earth));
    }
    .paytiles button:disabled {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
    .complete-cta {
      display: block;
      width: 100%;
      margin-top: 12px;
      background: var(--hos-stone-amber);
      border: 1px solid var(--hos-stone-amber);
      border-radius: var(--hos-radius-control);
      color: var(--hos-stone-amber-ink); /* the ONLY ink gold may carry (D-078) */
      font-family: inherit;
      font-size: 15px;
      font-weight: 700;
      padding: 14px 10px;
      cursor: pointer;
    }
    .complete-cta:hover:not(:disabled) {
      background: var(--hos-stone-amber-hover);
      border-color: var(--hos-stone-amber-hover);
      color: var(--hos-stone-amber-ink); /* E6: a theme-stable fill pins its theme-stable ink in the SAME rule */
    }
    /* HH note: disabled drops its fill and goes grey; fade is not a state. */
    .complete-cta:disabled {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
    /* UI-3 (visual GAP 7): the split state is a band, not a control: the reference's pale yellow
       in both themes, with the theme-stable ink. It is a statement, so it is not greyed, and it is
       not a button either: a disabled button must look disabled (AGENTS.md). */
    .split-band {
      display: block;
      box-sizing: border-box;
      width: 100%;
      margin: 12px 0 0;
      background: var(--hos-band-fill);
      border: 1px solid var(--hos-band-fill);
      border-radius: var(--hos-radius-control);
      color: var(--hos-stone-amber-ink); /* a theme-stable fill pins its theme-stable ink in the SAME rule */
      font-family: inherit;
      font-size: 15px;
      font-weight: 700;
      line-height: normal;
      padding: 14px 10px;
      text-align: center;
    }
    .split-band .band-why {
      display: block;
      margin-top: 4px;
      font-size: 12px;
      font-weight: 400;
    }
    /* UNION-TRAIN11 · the counter's pay key worn as a keycap on the CTA (see payKeycap()).
       Gold-on-gold: the chip borrows the CTA's own ink (D-078: the ONLY ink gold may carry), so
       every CTA state (hover included) keeps the pair legal with zero new colours. */
    .complete-cta .keycap {
      display: inline-block;
      margin-left: var(--hos-space-2);
      padding: 0 6px;
      border: 1px solid currentColor;
      border-radius: var(--hos-radius-pill);
      font-family: var(--hos-font-mono);
      font-size: 11px;
      font-weight: 600;
      line-height: 18px;
      vertical-align: 1px;
      opacity: 0.85;
    }
 /*.cta-hint retired with the sentence it dressed (wave 2, finding 10). The split state is a
       state OF the CTA now, so there is nothing left on this surface that stands where the commit
       button stands and is not the commit button. */
    /* ── DIB-488 rev 6 (item F): the optional cash-tendered quick entry on the Cash tender ── */
    .cashrow {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 8px;
      align-items: center;
      margin-top: 8px;
    }
    .cashrow input {
      font-family: var(--hos-font-mono);
    }
    .cashhint {
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 4px;
    }
    .cashhint .num {
      font-family: var(--hos-font-mono);
      color: var(--hos-success);
    }
 /* ── DIB-488 rev 6 (item J): the running-total chip in the Sale panel's bar: always visible,
       long tickets included (the totals row can be seven screens down a 60-line ticket). ── */
    .draft {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.1em;
      color: var(--hos-stone-amber-text);
      border: 1px solid var(--hos-stone-amber);
      border-radius: var(--hos-radius-pill);
      padding: 2px 8px;
    }
    /* ── DIB-488 rev 6 (item A): the resolved-customer IDENTITY CARD (mock parity) ──
       Once the customer question is answered, the picker area becomes a proper card: initials
       chip, name, account number, account-type tag, the credit facts, and a Change control that
       reopens selection. Walk-in confirmed gets the same clean shape with its own facts. */
    .custcard {
      display: flex;
      align-items: center;
      gap: 12px;
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 12px 14px;
    }
    .custcard .avatar {
      display: inline-grid;
      place-items: center;
      width: 40px;
      height: 40px;
      flex: none;
      border-radius: var(--hos-radius-pill);
      background: color-mix(in srgb, var(--hos-stone-amber) 16%, var(--hos-slate-warm));
      border: 1px solid var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
      font-weight: 700;
      font-size: 14px;
      letter-spacing: 0.02em;
    }
    .custcard.walkin .avatar {
      background: color-mix(in srgb, var(--hos-line) 40%, var(--hos-slate-warm));
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
    }
    .custcard .cmain {
      min-width: 0;
      flex: 1;
    }
    .custcard .cname {
      font-size: 15px;
      font-weight: 600;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
    }
    .custcard .cline {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
      margin-top: 2px;
      font-size: 12px;
      color: var(--hos-text-dim);
    }
    .custcard .cacct {
      font-family: var(--hos-font-mono);
    }
    .custcard .ctag {
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      border: 1px solid var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
      border-radius: var(--hos-radius-pill);
      padding: 1px 8px;
    }
    .custcard.walkin .ctag {
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
    }
    /* The one tag on this card that is not the account's type. Same shape as its neighbour so the
       line still reads as one row of status, in the red this surface already uses for money that
       is past due: the account-type tag keeps the gold. */
    .custcard .ctag.hold {
      border-color: var(--hos-safety-red);
      color: var(--hos-safety-red);
    }
    .custcard .cfacts {
      display: flex;
      gap: 8px 14px;
      flex-wrap: wrap;
      margin-top: 4px;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.06em;
      color: var(--hos-text-dim);
    }
    .custcard .cfacts .num {
      font-family: var(--hos-font-mono);
      text-transform: none;
      letter-spacing: 0;
    }
    .custcard .cfacts .avail {
      color: var(--hos-success);
    }
    .custcard .cfacts .out {
      color: var(--hos-safety-red);
    }
    .custcard .cchange {
      flex: none;
      background: transparent;
      border: 1px solid var(--hos-line);
      color: var(--hos-text-dim);
      border-radius: var(--hos-radius-control);
      padding: 7px 12px;
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
    }
    .custcard .cchange:hover {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    .customerpick {
      position: relative;
      cursor: pointer;
      border-radius: var(--hos-radius-control);
    }
    .customerpick:hover .pickhint {
      color: var(--hos-stone-amber-text);
    }
    .pickhint {
      /* DIB-255: a proper "select customer" label ABOVE the strip with a clear gap, instead of a
         hint cramped into the strip's top-right corner against the chosen customer name. */
      display: block;
      margin-bottom: 8px;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      pointer-events: none;
    }
    .line {
      border-top: 1px solid var(--hos-line);
      padding: 10px 0;
    }
    .lrow {
      display: grid;
      grid-template-columns: 1fr auto auto auto;
      gap: 8px;
      align-items: center;
    }
    .lname {
      font-size: 14px;
    }
    .lsku {
      font-family: var(--hos-font-mono);
      font-size: 11px;
      color: var(--hos-text-dim);
    }
    .stepper {
      display: flex;
      align-items: center;
      gap: 4px;
    }
    /* DIB-409: shadow roots do not inherit the document :focus-visible rule (tokens.css). The POS
       is the most keyboard-driven surface in the ERP (tender entry, steppers, quantity fields), so
       it repeats the same amber ring pos-product-grid.ts already uses on its tiles. */
    button:focus-visible,
    input:focus-visible,
    select:focus-visible,
    textarea:focus-visible,
    [tabindex]:focus-visible {
      outline: 2px solid var(--hos-stone-amber-text);
      outline-offset: 2px;
    }
    .stepper button {
      width: 24px;
      height: 24px;
      border-radius: var(--hos-radius-control);
      border: 1px solid var(--hos-line);
      background: var(--hos-deep-earth);
      color: var(--hos-text);
      cursor: pointer;
    }
    .qty {
      font-family: var(--hos-font-mono);
      width: 44px;
      text-align: center;
      background: var(--hos-deep-earth);
      border: 1px solid var(--hos-line);
      color: var(--hos-text);
      border-radius: var(--hos-radius-control);
      padding: 3px;
    }
    .ltot {
      font-family: var(--hos-font-mono);
      color: var(--hos-stone-amber-text);
      min-width: 84px;
      text-align: right;
    }
 /* rev 8: inside the LINES grid the money columns derive their own width: the CARDS keep the
       84px floor above; the row grid also compacts the stepper so nine columns fit a 45% panel. */
    .linerow .ltot {
      min-width: 0;
      font-size: 13px;
    }
    .linerow .stepper button {
      width: 20px;
      height: 22px;
    }
    .linerow .stepper .qty {
      width: 36px;
      padding: 2px;
    }
 /* rev 8 (item 3): the remove control is a TRASH-CAN icon (concept parity): same button,
       same aria-label naming the product, same handler; currentColor so hover reads red. */
    .rm {
      display: inline-grid;
      place-items: center;
      width: 28px;
      height: 28px;
      background: transparent;
      border: 0;
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      cursor: pointer;
      font-size: 16px;
      padding: 0;
    }
    .rm:hover {
      color: var(--hos-safety-red);
    }
    /* DIB-DUPLINE: the line an add just touched rings for a beat, so a merge is SEEN. This is what
       makes "same product twice raises the quantity" trustworthy at the counter: without it a scan
       that lands on an existing line looks, from the gun, like a scan that did nothing.
       An INSET RING, never a fill: --hos-success is the text/border green (DIB-468), and green is
       the doctrine's "good" role. Colour never carries it alone: the scan box speaks the same
       outcome ("250 → 500 SF") in its role="status" line at the same moment. */
    .linerow.flash,
    .line.flash {
      animation: lineMerged 1.6s ease-out 1;
      border-radius: var(--hos-radius-control);
    }
    @keyframes lineMerged {
      0% {
        box-shadow: inset 0 0 0 2px var(--hos-success);
      }
      70% {
        box-shadow: inset 0 0 0 2px var(--hos-success);
      }
      100% {
        box-shadow: inset 0 0 0 2px transparent;
      }
    }
 /* Reduced motion keeps the ATTENTION and drops the movement: the ring simply stands for the
       same beat and is removed by the same timer. Never a silent no-op. */
    @media (prefers-reduced-motion: reduce) {
      .linerow.flash,
      .line.flash {
        animation: none;
        box-shadow: inset 0 0 0 2px var(--hos-success);
      }
    }
    /* DIB-263: the empty-ticket prompt slow-pulses to draw the eye to the first action, kept gentle
       (2.4s ease, only down to 55% opacity) so it guides without annoying: the client's exact ask
       ("flash slowly… we don't want it to be an annoyance"). Honors prefers-reduced-motion. */
    .empty {
      color: var(--hos-text-dim);
      font-size: 13px;
      padding: 18px 0;
      text-align: center;
      animation: emptyPulse 2.4s ease-in-out infinite;
    }
    @keyframes emptyPulse {
      0%, 100% { opacity: 1; }
      50% { opacity: 0.55; }
    }
    @media (prefers-reduced-motion: reduce) {
      .empty { animation: none; }
    }
    label {
      display: block;
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
      margin: 12px 0 4px;
    }
 /* rev 5: unstyled ::placeholder was the Chromium default gray (3.5-3.9:1 dark): under the
       4.5:1 text bar on the very instructions the counter reads most. */
    input::placeholder,
    textarea::placeholder {
      color: var(--hos-text-dim);
      opacity: 1;
    }
    select,
    input,
    textarea {
      width: 100%;
      background: var(--hos-deep-earth);
      color: var(--hos-text);
      border: 1px solid var(--hos-control-line);
      border-radius: var(--hos-radius-control);
      padding: 8px 10px;
      font-size: 13px;
      font-family: inherit;
      box-sizing: border-box;
    }
    /* rev 8 (item 7): inside its own card the totals block needs no separating border. */
    .totalscard .totals {
      border-top: none;
      margin-top: 0;
      padding-top: 0;
    }
    .totalscard .trow.grand {
      margin-top: 6px;
      padding-top: 8px;
      border-top: 1px solid var(--hos-line);
      align-items: baseline;
    }
 /* The oversized gold Total: the concept's one loud number on the quiet card. */
    .totalscard .trow.grand .num {
      font-size: 26px;
    }
    .totals {
      border-top: 1px solid var(--hos-line);
      margin-top: 12px;
      padding-top: 12px;
    }
    .trow {
      display: flex;
      justify-content: space-between;
      font-size: 13px;
      padding: 3px 0;
    }
    .trow .num {
      font-family: var(--hos-font-mono);
    }
    .trow.grand .num {
      color: var(--hos-stone-amber-text);
      font-size: 18px;
      font-weight: 600;
    }
    /* DIB-165 past-sale lookup sheet. Same scrim primitive the parked/return overlays use
       (--hos-scrim + --hos-z-modal) so the counter's modals stack and dim identically. */
    .overlay {
      position: fixed;
      inset: 0;
      background: var(--hos-scrim);
      display: flex;
      align-items: center;
      justify-content: center;
      z-index: var(--hos-z-modal);
      padding: 24px;
    }
    .sheet {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      padding: 18px;
      width: min(1000px, 100%);
      max-height: 85vh;
      overflow: auto;
    }
    .sheethead {
      display: flex;
      justify-content: space-between;
      align-items: center;
      margin-bottom: 14px;
      font-size: 13px;
      text-transform: uppercase;
      letter-spacing: 0.09em;
      color: var(--hos-text-dim);
    }
    .sheethead button {
      background: transparent;
      border: 1px solid var(--hos-line);
      color: var(--hos-text-dim);
      border-radius: var(--hos-radius-control);
      padding: 6px 12px;
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
    }
    .tenders {
      display: grid;
      grid-template-columns: 1fr 1fr 1fr;
      gap: 8px;
      margin-top: 14px;
    }
    .tenders button {
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      padding: 12px 8px;
      font-size: 13px;
      font-weight: 600;
      font-family: inherit;
      cursor: pointer;
      background: var(--hos-deep-earth);
      color: var(--hos-text);
    }
    .tenders button.account {
      background: var(--hos-stone-amber);
      color: #0b1725;
      border-color: var(--hos-stone-amber);
    }
    .tenders button:disabled {
      background: transparent;
      border-color: var(--hos-line);
      color: var(--hos-text-dim);
      opacity: 1;
      cursor: not-allowed;
    }
    .util {
      display: flex;
      gap: 8px;
      margin-top: 10px;
    }
    .deposit {
      display: grid;
      grid-template-columns: 1fr 110px;
      gap: 8px;
    }
    .deposit input {
      font-family: var(--hos-font-mono);
    }
    .util button {
      flex: 1;
      background: transparent;
      border: 1px solid var(--hos-line);
      color: var(--hos-text-dim);
      border-radius: var(--hos-radius-control);
      padding: 8px;
      font-size: 11px; /* small chrome controls, same role as .topbtn (see the type-scale note) */
      cursor: pointer;
      font-family: inherit;
    }
 /* DIB-244: the explicit destructive "Clear sale" affordance; red text sets it apart from the
       neutral utility buttons so it reads as "empty the whole ticket in one step". */
    /* UI-3 (visual GAP 8): the margin eye is an outlined control like its neighbours, not the UA's
       grey square; it sits at the row's far end with a fixed footprint. */
    .saleutil .eye {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 32px;
      height: 32px;
      margin-left: auto;
      padding: 0;
      background: transparent;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      color: var(--hos-text-dim);
      cursor: pointer;
    }
    .saleutil .eye:hover {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    /* One name with aria-pressed (pressed means the margin is hidden). Pressed is a filled tint
       so a hovered eye (outline only) never reads as pressed; the glyph change carries it too. */
    .saleutil .eye[aria-pressed='true'] {
      border-color: var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
      background: color-mix(in srgb, var(--hos-stone-amber) 16%, transparent);
    }
    .util button.clear {
      color: var(--hos-safety-red);
      border-color: color-mix(in srgb, var(--hos-safety-red) 40%, var(--hos-line));
    }
    /* ui3 visual NOTE 5: a util control that cannot act (Park ticket on an empty ticket, Open
       order and Print receipt with nothing typed) takes the one disabled rule's cursor too:
       the pointer promised an act the control cannot make, and it was the one cursor outside
       the rule. */
    .util button:disabled {
      cursor: not-allowed;
    }
    .util button.clear:hover {
      border-color: var(--hos-safety-red);
    }
    .banner {
      margin-top: 12px;
      border-radius: var(--hos-radius-control);
      padding: 10px 12px;
      font-size: 13px;
    }
    .banner.ok {
      background: rgba(74, 222, 128, 0.12);
      border: 1px solid var(--hos-success);
      color: var(--hos-success);
    }
    /* A notice, neither good news nor a fault: neutral ink on the slate, the control line for its edge. */
    .banner.note {
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      color: var(--hos-text);
    }
    /* UI-3 (visual GAP 1): the End Session notice rides the same note palette, anchored under the
       session region its control lives in, at the popover scale, so the bar's height never moves
       and the words sit in the page's first band in both themes. ui3d P2-1: right-aligned to that
       region (never past it toward the window's left) and clamped to the window minus the bar's
       own padding on each side, so the card stays fully inside the viewport at every width. */
    .esnotice {
      position: absolute;
      top: calc(100% + 8px);
      right: 0;
      z-index: var(--hos-z-modal);
      width: 320px;
      max-width: calc(100vw - 2 * var(--hos-space-5));
      background: var(--hos-slate-warm);
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-card);
      box-shadow: var(--hos-elev-1);
      padding: 10px 12px;
      font-size: 12px;
      color: var(--hos-text);
      text-align: left;
    }
    /* Visually hidden, still in the accessibility tree: a description a closed control points at. */
    .srdesc {
      position: absolute;
      width: 1px;
      height: 1px;
      margin: -1px;
      padding: 0;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
      border: 0;
    }
    .banner.bad {
      background: rgba(244, 63, 94, 0.12);
      border: 1px solid var(--hos-safety-red);
      color: var(--hos-safety-red);
    }
    .banner .num {
      font-family: var(--hos-font-mono);
    }
 /* rev 5: the no-till refusal names its fix: this is that fix, one click, in the banner. */
    .banner .bannerfix {
      display: inline-block;
      margin-left: 10px;
      background: transparent;
      border: 1px solid var(--hos-safety-red);
      color: var(--hos-safety-red);
      border-radius: var(--hos-radius-control);
      padding: 4px 10px;
      font-size: 12px;
      font-family: inherit;
      cursor: pointer;
    }
    .banner .bannerfix:hover {
      background: color-mix(in srgb, var(--hos-safety-red) 12%, transparent);
    }

    /* ── DIB-444: the post-sale confirmation ──────────────────────────────────────────────────────
       "It does not appear to post a sale … it does not really verify: would be nice if you got
       more confirmation." The sale DOES post (order + invoice + payment + two balanced GL journals
       + stock moves, verified against the DB). What it did not do was SAY so: one 13px green line
       between two rows of utility buttons, carrying an order number, an invoice number and the word
       PAID: no amount, no tender, nothing a clerk could hand a customer's eye.
       This panel is additive; the ring and its state machine are untouched. */
    .sold-panel {
      margin-top: 12px;
      border: 2px solid var(--hos-success);
      border-radius: var(--hos-radius-card);
      background: rgba(74, 222, 128, 0.1);
      padding: 14px 16px;
    }
 /* Not paid is not failure: an account sale is a good outcome that simply is not money in the
       till. Amber, not green, and never the word PAID. (House rule: empty state is not error state;
       the same discipline applies to "invoiced but unpaid".) */
    .sold-panel.unsettled {
      border-color: var(--hos-stone-amber);
      background: rgba(232, 167, 78, 0.1);
    }
    .sold-head {
      display: flex;
      align-items: baseline;
      justify-content: space-between;
      gap: 12px;
    }
    .sold-head .verdict {
      font-size: 20px;
      font-weight: 700;
      color: var(--hos-success);
      letter-spacing: 0.01em;
    }
    .sold-panel.unsettled .sold-head .verdict {
      color: var(--hos-stone-amber-text);
    }
    .sold-head .dismiss {
      flex: none;
      background: none;
      border: none;
      color: var(--hos-text-dim);
      font-size: 20px;
      line-height: 1;
      padding: 2px 6px;
      cursor: pointer;
      border-radius: var(--hos-radius-control);
    }
    .sold-amount {
      font-family: var(--hos-font-mono);
      font-size: 30px;
      font-weight: 700;
      color: var(--hos-text);
      margin: 6px 0 2px;
    }
    .sold-amount-label {
      font-size: 11px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
    }
 /* The four facts, side by side, each labelled: this is the part the tester was missing. */
    .sold-facts {
      display: flex;
      flex-wrap: wrap;
      gap: 8px 22px;
      margin-top: 12px;
      padding-top: 10px;
      border-top: 1px solid var(--hos-line);
    }
    .sold-facts .f {
      display: flex;
      flex-direction: column;
      gap: 2px;
    }
    .sold-facts .k {
      font-size: 10px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      color: var(--hos-text-dim);
    }
    .sold-facts .v {
      font-family: var(--hos-font-mono);
      font-size: 14px;
      color: var(--hos-text);
    }
 /* rev 6 (item F): change due: the number the clerk hands back, spoken loudly on the panel. */
    .sold-change {
      margin-top: 8px;
      font-size: 13px;
      color: var(--hos-text);
    }
    .sold-change .num {
      font-family: var(--hos-font-mono);
      font-size: 18px;
      font-weight: 700;
      color: var(--hos-success);
    }
    .sold-change .tnum {
      font-family: var(--hos-font-mono);
    }
    .sold-note {
      margin-top: 10px;
      font-size: 12px;
      color: var(--hos-text-dim);
    }
    .sold-actions {
      display: flex;
      gap: 8px;
      margin-top: 12px;
    }
    .sold-actions button {
      font-family: inherit;
      font-size: 14px;
      font-weight: 600;
      padding: 9px 14px;
      border-radius: var(--hos-radius-control);
      border: 1px solid var(--hos-line);
      background: var(--hos-slate-warm);
      color: var(--hos-text);
      cursor: pointer;
    }
    .sold-actions button.primary {
      background: var(--hos-stone-amber);
      color: #0b1725;
      border-color: var(--hos-stone-amber);
    }
    .reliefchip {
      margin-top: 8px;
      font-size: 12px;
      color: var(--hos-info-blue);
      font-family: var(--hos-font-mono);
    }
    .cardlabel {
      display: flex;
      align-items: center;
      gap: 8px;
      flex-wrap: wrap;
    }
    .railbadge {
      font-family: var(--hos-font-mono);
      font-size: 10px;
      letter-spacing: 0.04em;
      padding: 2px 8px;
      border-radius: var(--hos-radius-pill);
      border: 1px solid var(--hos-stone-amber);
      color: var(--hos-stone-amber-text);
    }
    .railbadge.live {
      border-color: var(--hos-success);
      color: var(--hos-success);
    }
    .railbadge.off {
      border-color: var(--hos-safety-red);
      color: var(--hos-safety-red);
    }
 /* DIB-196: the per-terminal "ask for quantity on add" toggle. DIB-488 rev 2: moved into the
       Sale panel's config popover (a .cfgrow) with the other per-terminal presentation options. */
 /* DIB-198 / DIB-200: requested date: native calendar (left) + fast YYYYMMDD entry (right). */
    /* DIB-259: one date field + a calendar button (was two side-by-side inputs). The text box takes
       the free track; the button is a fixed square; the native date input is visually hidden but
       still focusable-by-script so its picker can open from the button. */
    .daterow {
      display: grid;
      grid-template-columns: 1fr auto;
      gap: 8px;
      align-items: stretch;
    }
    .daterow input.compactdate {
      font-family: var(--hos-font-mono);
    }
    .daterow button.datepick {
      cursor: pointer;
      padding: 0 12px;
      line-height: 1;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      border: 1px solid var(--hos-line);
      border-radius: var(--hos-radius-control);
      background: var(--hos-slate-warm);
      color: var(--hos-text);
      color-scheme: var(--hos-color-scheme, light);
    }
    .daterow button.datepick:hover {
      border-color: var(--hos-stone-amber);
    }
 /* Visually hidden but not display:none: showPicker() needs a laid-out element to anchor to. */
    .daterow input.datehidden {
      position: absolute;
      width: 1px;
      height: 1px;
      padding: 0;
      margin: -1px;
      overflow: hidden;
      clip: rect(0 0 0 0);
      border: 0;
      color-scheme: var(--hos-color-scheme, light);
    }
    .datehint {
      font-size: 11px;
      color: var(--hos-text-dim);
      margin-top: 4px;
    }
    .datehint b {
      color: var(--hos-text);
      font-family: var(--hos-font-mono);
      font-weight: 600;
    }
    .datehint.bad {
      color: var(--hos-safety-red);
    }
  `,
  ];

  connectedCallback(): void {
    super.connectedCallback();
 // rev 8 (item 2): the header clock ticks every 15s: display chrome only, label derived pure.
    this.clockNow = new Date();
    this.clockTimer = window.setInterval(() => (this.clockNow = new Date()), 15000);
    void this.loadCardRail();
    void this.loadPolicy();
    void this.loadWorkspace(); // DIB-488 rev 2: user-over-dealer, resolved server-side
    void this.loadHotkeys(); // the dealer's key roster: the legend renders the fallback until it lands
    void this.loadPanels(); // which panels this person keeps: both on until the read says otherwise
    void this.loadSalespeople(); // rev 6 (D): the More-options salesperson roster
    this.salespersonId = getCurrentUser()?.user_id ?? ''; // rev 6 (D): defaults to the operator
    void this.refreshParkedCount();
    void this.hydrateTillSession(); // rev 5: a reload/second surface rediscovers the OPEN till
    // The Help flyout light-dismisses on any pointer press outside it (capture on window so a
    // click swallowed by another shadow root still counts as "outside").
    window.addEventListener('pointerdown', this.onDocPointerDown, true);
    // ── DIB-434 ── both arrival orders of a "+ Sale" click. If this terminal was ALREADY mounted
    // (kept alive behind another unit tab) the listener catches it; if the click is what caused
    // this mount, takeIntent() finds the one waiting. Whichever runs first consumes it.
 // The counter's F-keys. A WINDOW listener for the same reason the shell's map is one: the key
 // must ring from wherever focus happens to be, including from inside the scan field: and in
    // the CAPTURE phase, which is what makes the counter win a key the shell has also bound.
    //
    // THIS IS NOT A LAND GRAB, IT IS THE ONLY CORRECT ORDER, and the keyboard walkthrough is what
    // proved it: the shipped dealer map already binds F1-F4, F8 and F9 (0171), the shell listens on
 // bubble and calls preventDefault, and the counter's listener: which yields to
 // defaultPrevented: therefore never fired once. Every advertised counter key was dead, and
    // pressing the one on the legend opened a new Sales tab under the clerk mid-sale.
    //
    // The rule is innermost-wins, scoped by mounting: this listener exists only while the counter
    // is on screen, it claims a key ONLY when it actually did something with it (runHotkey), and
    // anything it does not claim propagates to the shell's map completely untouched. F5 still
    // reloads, the shell's other keys still navigate, and the counter's advertised keys do what the
    // legend says they do.
    window.addEventListener('keydown', this.onPosKey, true);
    window.addEventListener(INTENT_EVENT, this.onQuickIntent as EventListener);
    this.applyQuickIntent(takeIntent('pos', [INTENT_POS_NEW_TICKET, INTENT_POS_PAY]));
    // ── end DIB-434 ──
  }

  /**
 * The counter takes the keyboard when it opens: but only if nobody else has it.
   *
   * The keyboard walkthrough opened POS and found focus on <body>: thirteen Tab presses from the
   * top of the document before a counter control, on a screen whose whole premise is that a
   * cashier picks up the gun and shoots. The scan box's own autofocus could not help, because at
   * mount the Items card is CLOSED and the box is not rendered at all.
   *
   * The guard matters as much as the focus. A surface that grabs focus unconditionally will yank
   * it out from under a person who has already clicked something during the mount, which is worse
 * than the problem it fixes: so this acts only while focus is still on the document body.
   */
  protected firstUpdated(): void {
    void this.updateComplete.then(() => {
      const active = document.activeElement;
      if (active && active !== document.body) return;
      if (this.openStep) this.openAccordion(this.openStep);
    });
  }

  // DIB-195: apply the popped-in order exactly once, the first update where `restore` is set.
  // Guarded so a later re-render never re-applies (which would clobber the cashier's edits).
  protected willUpdate(): void {
    if (!this.restored && this.restore) {
      this.restored = true;
      this.applySnapshot(this.restore);
    }
  }


  // DIB-195 (opener side): capture the current in-progress order so a pop-out can carry it into
 // the new window. Verbatim client state (a DRAFT: no server sale id yet); the shell stashes
  // this and the popped terminal restores it via the `restore` property.
  snapshotOrder(): PosOrderSnapshot {
    return {
      cart: this.cart,
      customer: this.customer,
      branchId: this.branchId,
      saleType: this.saleType,
      cardType: this.cardType,
      notes: this.notes,
      requestedDate: this.requestedDate,
      reliefReason: this.reliefReason,
      reliefRateBps: this.reliefRateBps,
      signatureRef: this.signatureRef,
      depositDollars: this.depositDollars,
      depositMethod: this.depositMethod,
      fulfilment: this.fulfilment,
      tillSessionId: this.tillSessionId,
      sessionStartMs: this.sessionStartMs,
      walkInConfirmed: this.walkInConfirmed, // DIB-488: flow facts are sale data, so they move too
      payMethod: this.payMethod,
      tenderConfirmed: this.tenderConfirmed, // …and so does whether anyone chose it
 // rev 6: the More-options facts and the cash quick entry are sale data too.
      poJobRef: this.poJobRef,
      salespersonId: this.salespersonId,
      cashTenderedDollars: this.cashTenderedDollars,
 // rev 8: the job facts are sale data too.
      jobAddress: this.jobAddress,
      jobReference: this.jobReference,
    };
  }

  // DIB-195 (popped side): apply a handoff snapshot into this terminal's live state. Lines carry
  // their own engine breakdown, so totals/prices match the opener with no re-fetch. If a till
  // session was open in the opener, resume its timer here so the moved window is immediately
  // ready to ring against the SAME drawer.
  private applySnapshot(s: PosOrderSnapshot): void {
    this.cart = Array.isArray(s.cart) ? s.cart : [];
    this.cartReplaced(); // D17: a pop-out's snapshot is a different ticket: drop answers in flight
    this.customer = s.customer ?? null;
    void this.refreshStanding(); // a restored ticket carries its account, so it carries its standing
    if (s.branchId) this.branchId = s.branchId;
    this.saleType = s.saleType || 'COLLECT_NOW';
    this.cardType = s.cardType || 'CREDIT';
    this.notes = s.notes ?? '';
    this.requestedDate = s.requestedDate ?? '';
    this.reliefReason = s.reliefReason ?? '';
    this.reliefRateBps = s.reliefRateBps ?? null;
    this.signatureRef = s.signatureRef ?? '';
    this.depositDollars = s.depositDollars ?? '';
    this.depositMethod = s.depositMethod || 'CASH';
    this.fulfilment = s.fulfilment || 'PICKUP';
    // DIB-488: flow facts follow the sale into the popped window; an older snapshot without them
 // restores to the defaults (unresolved customer, no tender picked): never undefined.
    this.walkInConfirmed = s.walkInConfirmed ?? false;
    // A snapshot taken before the cash default (a popped-out window, a parked ticket) carries no
    // tender. It resumes tenderable rather than resuming into the state this change removed.
    this.payMethod = (s.payMethod as typeof this.payMethod) || 'CASH';
    // An older snapshot carries no answer, which restores as the honest default: un-answered. A
    // snapshot that DID name a tender was taken from a terminal where '' meant "nothing picked",
    // so a named tender in it is a decision and restores as one.
    this.tenderConfirmed = s.tenderConfirmed ?? (s.payMethod ? s.payMethod !== '' : false);
    // rev 6: the More-options facts follow the sale; an older snapshot restores to the defaults
    // (no PO ref, the signed-in operator as salesperson, no cash quick entry).
    this.poJobRef = s.poJobRef ?? '';
    this.salespersonId = s.salespersonId || (getCurrentUser()?.user_id ?? '');
    this.cashTenderedDollars = s.cashTenderedDollars ?? '';
    // rev 8: the job facts follow the sale; older snapshots restore to empty.
    this.jobAddress = s.jobAddress ?? '';
    this.jobReference = s.jobReference ?? '';
 // rev 5: a snapshot restoring payMethod SPLIT must also restore the split panel: without it
    // the popped window shows the "compose the split legs below" hint over NO panel and no CTA
    // (a dead-end payment state until the cashier re-taps the tile twice).
    this.showSplit = this.payMethod === 'SPLIT';
    for (const l of this.cart) void this.hydrateLineStock(l.product.id);
    if (s.tillSessionId) {
      this.tillSessionId = s.tillSessionId;
      this.sessionStartMs = s.sessionStartMs || Date.now();
      if (!this.timer) this.timer = window.setInterval(() => this.tick(), 1000);
      this.tick();
    }
  }

  // Resolve pos_policy so the COD sale type is offered honestly (disabled + tooltip when off).
 // DIB-488: the same read now carries the dealer's workspace preferences (D-096): the default
  // items presentation and whether the quick-chip row exists at all.
  private async loadPolicy(): Promise<void> {
    try {
      const p = await getPosPolicy(this.branchId);
      this.codEnabled = p.cod_enabled;
      if (!p.cod_enabled && this.saleType === 'COD') this.saleType = 'COLLECT_NOW';
      this.presentation = p.line_presentation === 'CARDS' ? 'CARDS' : 'LINES';
      this.quickChips = p.quick_chips_enabled !== false;
      // rev 8 (item 4): the curated chip set (dealer-authored, or the server's top-sellers
      // derivation). Hardened at the edge; [] = the grid keeps its full chip row.
      this.curatedChips = resolveCuratedChips(p.quick_chip_set);
    } catch {
      this.codEnabled = null; // unknown: keep the option disabled rather than wrong
 // presentation/chips keep their defaults (LINES / on): a policy outage must not blank the till
    }
  }

  /**
 * rev 6 (item D): the salesperson roster, best-effort: a failed read leaves the list empty and
   * the selector offers just the signed-in operator (empty-never-error; the field is a convenience,
   * never a gate on ringing a sale).
   */
  private async loadSalespeople(): Promise<void> {
    try {
      this.salespeople = await listSalespeople();
    } catch {
      /* the operator-only fallback renders from getCurrentUser() */
    }
  }

  private async refreshParkedCount(): Promise<void> {
    try {
      this.parkedCount = (await listParked(this.branchId)).length;
    } catch {
      /* count is cosmetic */
    }
  }

  disconnectedCallback(): void {
    super.disconnectedCallback();
    if (this.timer) window.clearInterval(this.timer);
    if (this.clockTimer) window.clearInterval(this.clockTimer); // rev 8: the header clock dies with the mount
    if (this.ticketFactsTimer) window.clearTimeout(this.ticketFactsTimer); // rev 8: no orphan facts read
    if (this.wsPersistTimer) window.clearTimeout(this.wsPersistTimer); // a torn-down till saves nothing
    window.removeEventListener('pointerdown', this.onDocPointerDown, true); // rev 5
    // ── DIB-434 ──
    window.removeEventListener('keydown', this.onPosKey, true);
    window.removeEventListener(INTENT_EVENT, this.onQuickIntent as EventListener);
    // ── end DIB-434 ──
  }

  /**
 * rev 5: rehydrate the OPEN till on mount. tillSessionId was in-memory only, so a mid-shift
   * reload (or a Duplicate) showed "Open Session" and rang 409 while the drawer WAS open. The
   * server is the source of truth: ask it for the caller's open drawer at this branch/station and
 * adopt it: id AND opened_at, so the elapsed timer resumes honestly instead of restarting at
   * 00:00. Best-effort: an outage leaves the chrome exactly as before (empty-never-error), and a
   * pop-out snapshot that already carried the session wins (guarded on tillSessionId === '').
   */
  private async hydrateTillSession(): Promise<void> {
    try {
      const open = await findOpenTillSession(this.branchId, 'MAIN');
      if (!open || this.tillSessionId !== '') return;
      this.tillSessionId = open.id;
      const started = Date.parse(open.opened_at);
      this.sessionStartMs = Number.isFinite(started) ? started : Date.now();
      if (!this.timer) this.timer = window.setInterval(() => this.tick(), 1000);
      this.tick();
    } catch {
 /* chrome only: the ring path reports real errors itself */
    }
  }

  // Resolve the card-rail mode so the Card tender is labelled honestly (test vs live vs unconfigured).
  private async loadCardRail(): Promise<void> {
    try {
      const res = await api('/api/v1/payments/config');
      if (!res.ok) return; // leave '': no badge rather than a wrong one
      const body = (await res.json()) as { configured: boolean; simulated: boolean };
 // HH note: the simulated word is value driven on simulated ALONE (the
 // reviewed rule): a rail that answers simulated true says test whatever it
 // says about configured, because the notice must render exactly when the
 // value is true and claim nothing when it is not.
      this.cardRail = body.simulated === true ? 'test' : body.configured ? 'live' : 'unconfigured';
    } catch {
 /* status is cosmetic: the sale path reports real errors itself */
    }
  }

  // ── till chrome (A-03) ────────────────────────────────────────────────
  private async openSession(): Promise<void> {
    try {
      const res = await api('/api/v1/till/sessions', {
        method: 'POST',
        body: JSON.stringify({ branch_id: this.branchId, station: 'MAIN', float_cents: 30000 }),
      });
      if (res.ok) {
        const body = (await res.json()) as { id?: string; session_id?: string };
        this.tillSessionId = body.id ?? body.session_id ?? 'till-open';
      } else {
        this.tillSessionId = 'till-pending'; // A-03 not yet live: chrome still demonstrates the flow
      }
    } catch {
      this.tillSessionId = 'till-offline';
    }
    this.sessionStartMs = Date.now();
    this.timer = window.setInterval(() => this.tick(), 1000);
    this.tick();
  }

  // End Session opens the real A-03 close flow (movements → blind count → Z-report → complete);
  // the local chrome resets only once the till actually closes (or the cashier defers: COUNTED
  // sessions can be completed next morning, per the Dibbits SOP).
  // HH note: the reference's End Session opened its till-close screen
  // (hos-till-close), a host boundary screen this release does not carry. The
  // core refuses a complete unless the session is COUNTED (409), and a drawer
  // that does not balance needs a manager's sign-off (row 500), so the mount
  // cannot honestly end a session from here: the button stays visible but is
  // closed (aria-disabled, focusable, its reason shown in the till's message line), and the gap is named in CLOSURE.md.
  // The reference's handler is dropped for the day the close flow is ported.
  private readonly endSessionClosedReason =
    "Ending the session needs the blind count, the Z report and a manager's sign-off; the drawer close is not on this surface yet.";

  private onTillClosed(): void {
    this.showTillClose = false;
    if (this.timer) window.clearInterval(this.timer);
    this.tillSessionId = '';
    this.sessionStartMs = 0;
    this.elapsed = '00:00';
  }

  private tick(): void {
    const s = Math.floor((Date.now() - this.sessionStartMs) / 1000);
    const mm = String(Math.floor(s / 60)).padStart(2, '0');
    const ss = String(s % 60).padStart(2, '0');
    this.elapsed = `${mm}:${ss}`;
  }

  // ── cart ──────────────────────────────────────────────────────────────
  /**
 * DIB-DUPLINE: adding a product that is ALREADY on the ticket raises the existing line's
   * quantity instead of appending a second line of the same thing. Reported from the live counter:
   * three scans of one SKU used to build three lines of 1.
   *
 * EVERY add path lands here: the scan box's resolve, a search-result row, a browse tile, the
 * add-ons strip's + Add, the quick-add rail: so the rule is written once and cannot be missed by
   * a caller. decideAdd() (pure, _pos.ts) owns the judgement:
   *
   * merge same product, same sell unit, and a target line carrying no line-level work of its
 * own. The quantity is SUMMED and the line goes straight back through reprice(): the
   * same /pricing/resolve call a fresh add makes. No money is ever computed here: a
   * merged 250 + 250 is repriced AT 500, so a volume break that only opens above 400
   * actually opens (client-side arithmetic would have kept the 250-tier unit price).
   * append a different sell unit, or a line the clerk has shaped by hand. A second line is the
   * HONEST answer there, and the outcome line below says why it appeared.
   *
   * The scan-focus contract is untouched: the scan box refocuses itself, and the DIB-196
   * ask-for-quantity jump now lands on the MERGED line's qty box (the affected line) exactly as it
   * has always landed on the affected line.
   */
  /**
 * DIB-553 (bounce 2): the ticket's change counter, handed to every add surface.
   *
   * Each surface holds its own 300ms echo guard, and that guard is only meaningful while the line
   * it created is still on the ticket. It is surface state; the cart is terminal state; nothing
   * connected the two. Driving the grid directly showed it: click (1 line), `cart = []`, click
 * again on the SAME product: ZERO lines, because the surface still thought the second press was
   * an echo. New Sale and Park re-render enough to reset it in practice, and "in practice" is not
   * a rule.
   *
   * Bumped by every cart change that is NOT the add itself. Deliberately NOT bumped by reprice():
   * reprice runs inside addProduct, so a bump there would cancel the guard on the very gesture it
   * exists for.
   */
  @state() private ticketEpoch = 0;

  private ticketChanged(): void {
    this.ticketEpoch++;
  }

  /**
 * The cart was replaced WHOLESALE: a different ticket now occupies these slots.
   *
   * Everything `ticketChanged()` does (the add surfaces' echo guards forget their last press), plus
   * a bump of the D17 reprice generation, which drops every pricing answer still in flight for the
   * ticket that just left. Without it, a slot that happens to be refilled with the same product at
 * the same quantity would accept the PREVIOUS customer's price: the narrow case the per-slot
   * token and the product/qty/uom check cannot see, and the one the round-2 docstring named.
   *
   * Deliberately NOT called from setQty or removeLine. Those change the cart but do not replace it,
   * they already supersede correctly, and cancelling every in-flight answer there would leave a
   * freshly added line reading a hard CA$0.00 because an unrelated line was deleted.
   */
  private cartReplaced(): void {
    this.ticketChanged();
    this.cartGen++;
  }

  /**
   * An add a surface SWALLOWED as the second half of one gesture (DIB-553, bounce 2).
   *
   * The swallow used to be silent, which is the worst possible answer: a real add flashes the line
   * and announces the new quantity, so a suppressed one reading as nothing is indistinguishable
 * from the till dropping the press. It gets the SAME two signals a real add gets: the line
 * flashes, and one sentence lands in the role="status" line the counter is already reading:
   * with the quantity that is actually on the ticket, so the outcome is legible without looking
   * away from the gun.
   */
  private onAddEcho(id: string): void {
    const idx = this.cart.findIndex((l) => l.product.id === id);
    if (idx < 0) return; // an echo implies the first half landed; if it did not, say nothing false
    const line = this.cart[idx];
    void this.flashLine(idx);
    const out = echoAddMessage(line.product.name ?? '', line.qty, line.uom);
    if (!out) return;
    const box = this.renderRoot.querySelector<
      HTMLElement & { announce?: (m: string, k: 'ok' | 'warn' | 'err') => void }
    >('hos-pos-scan-box');
    if (box?.announce) box.announce(out.msg, out.kind);
    else this.setUtil(out.msg, 'bad');
  }

  private async addProduct(p: PosProduct, qty = 1): Promise<void> {
    // Sell UoM: the product's declared sold_by, else its stocking base unit. A hardcoded 'EA' fallback
    // rang up "no uom conversion factor" for any product stocked in tonnes/SF with sold_by unset.
    // rev 8: the optional qty is the add-ons strip's one-click add (its tile carries a qty box);
 // every existing caller still adds 1: the engine prices either way (never client-priced).
    const uom = p.sold_by || p.base_uom || 'EA';
    const d = decideAdd(this.cart, p.id, uom, qty);
    let idx: number;
    if (d.action === 'merge') {
      // Quantity only. Every money field on the line is left exactly as it was and is overwritten
      // wholesale by the engine's answer in reprice() below.
      const next = [...this.cart];
      next[d.index] = { ...next[d.index], qty: d.toQty };
      this.cart = next;
      idx = d.index;
    } else {
      const line: CartLine = {
        product: p,
        qty: cleanQty(qty),
        uom,
        unitPriceCents: 0,
        priceUom: uom,
        lineTotalCents: 0,
        breakdown: null,
        // Pending until the price lookup answers: the summary stays on the
        // marker rather than a zero for a line nobody has priced yet.
        priceMissing: true,
      };
      this.cart = [...this.cart, line];
      idx = this.cart.length - 1;
    }
    // DIB-488: warm the stock-status line for the new item (display-only; never blocks the add).
    void this.hydrateLineStock(p.id);
    await this.reprice(idx);
    // The affected line draws attention to itself, so a counter person SEES 250 → 500 instead of
    // wondering where their scan went. Both are needed: the flash for the eye already on the
    // ticket, the spoken outcome for the eye still on the gun.
    void this.flashLine(idx);
    this.speakAddOutcome(d, p.name, uom);
    // DIB-196: with the terminal's "ask for quantity" toggle on, jump to the affected line's qty
    // box (highlighted, via the DIB-199 select-on-focus) so the clerk types the amount instead of 1.
    if (this.promptQtyOnAdd) void this.focusLineQty(idx);
  }

  /**
 * The add OUTCOME, in the scan box's own role="status" line: the region the counter is already
   * reading, and the one the box itself uses for "Added …". Speaking here rather than adding a
   * second message strip keeps one fact in one place (doctrine bullet 6): on a merge this REPLACES
   * the box's "Added …", which would otherwise be true but useless ("added" where? which line?).
   *
   * Ordering: the box sets its own "Added …" synchronously right after it dispatches
 * pos-scan-resolved, and this runs after the awaited reprice(): so the outcome always lands
   * last. A plain first add says nothing here and the box's own confirmation stands.
   */
  private speakAddOutcome(d: AddDecision, productName: string, uom: string): void {
    const out = addOutcomeMessage(d, productName, uom);
    if (!out) return;
    const box = this.renderRoot.querySelector<
      HTMLElement & { announce?: (m: string, k: 'ok' | 'warn' | 'err') => void }
    >('hos-pos-scan-box');
    if (box?.announce) box.announce(out.msg, out.kind);
    else this.setUtil(out.msg, out.kind === 'warn' ? 'bad' : 'ok'); // no box mounted: never silent
  }

  /**
   * Ring the affected line for a beat. Deliberately NOT a field on CartLine: the cart is
   * serialized verbatim into the till snapshot, and a rehydrated ticket must not come back
   * flashing a merge that happened before lunch. Index-keyed, and a new flash supersedes the old.
   *
 * The animation is RESTARTED explicitly, because the gesture this fix exists for: scan, scan,
 * scan: lands on the SAME line every time. The class is already on that row from the previous
   * add, so the browser has no reason to re-run the keyframes, and the second and third scans
   * would ring nothing: the exact "where did my scan go?" silence the merge is meant to end.
 * getAnimations() is guarded (older engines, the node test runner): a missing restart costs the
   * animation, never the quantity, and reduced-motion has no animation to restart because its ring
   * is static.
   */
  private async flashLine(idx: number): Promise<void> {
    if (this.flashTimer) window.clearTimeout(this.flashTimer);
    this.flashIdx = idx;
    this.flashTimer = window.setTimeout(() => {
      this.flashIdx = -1;
      this.flashTimer = 0;
    }, LINE_FLASH_MS);
    try {
      await this.updateComplete;
      const row = this.renderRoot.querySelectorAll<HTMLElement>('.linerow, .line')[idx];
      row?.getAnimations?.().forEach((a) => {
        a.cancel();
        a.play();
      });
    } catch {
 /* no-animation environments: the ring simply stands for the timer's beat */
    }
  }

  /**
 * DIB-488: fetch (once per product) the glance behind the stock-status line. Best-effort and
   * display-only, exactly like the product grid's own glances: a failed read renders NOTHING under
   * the line (absent data is not an error and must never read as "out of stock").
   */
  private async hydrateLineStock(productId: string): Promise<void> {
    if (this.lineStock[productId]) return;
    try {
      const g = await stockGlance(productId);
      this.lineStock = { ...this.lineStock, [productId]: g };
    } catch {
      /* display-only */
    }
  }

  // DIB-196: focus + select a cart line's quantity box once the DOM has settled, so the default
 // "1" is highlighted and the next keystroke replaces it. Best-effort: a missing node (line
  // removed mid-flight) is simply a no-op.
  private async focusLineQty(idx: number): Promise<void> {
    await this.updateComplete;
    const el = this.renderRoot.querySelectorAll<HTMLInputElement>('input.qty')[idx];
    if (el) {
      el.focus();
      el.select();
    }
  }

  /**
   * Re-derive one line's money from the engine.
   *
   * THE TICKET CAN MOVE WHILE THIS IS IN FLIGHT, and before this guard it did the worst possible
   * thing about it. `resolveLine` is a network round trip; the line object was captured before it
 * and written back after it, into `[...this.cart]`: whatever `this.cart` had become. Found while
   * instrumenting DIB-553's ticket-scoping fix, and reproduced in the browser: add an item, press
   * New Sale before the reprice returns, and the cleared line REAPPEARS on the fresh ticket
 * (measured: cart 1 → 0 on the reset, then back to 1 ~200ms later, and the line stays). The same
   * hole is open for Clear sale, Park, resuming a parked ticket and applying a pop-out snapshot,
   * because every one of them replaces the cart wholesale. A previous customer's product on the
   * next customer's ticket is a money defect, and nothing announced it.
   *
   * ROUND 2 CHECKED THE PRODUCT AND NOTHING ELSE, AND THAT WAS THE WORSE HALF OF THE BUG.
 * The guard asked only whether the slot still held the same product id: but the write-back was
 * `{...line, …money }`, spreading the whole PRE-AWAIT SNAPSHOT. Product identity survived the
   * guard; the operator's QUANTITY was restored from the snapshot. Measured with the first answer
   * held 600ms: add Red Mulch, add it again 800ms later (deliberate, far outside the echo window)
   * and the ticket went 1 → 2 → **1**. A deliberate unit silently deleted. Same for a 7 typed into
   * the line's qty box, for a next customer's ticket, and for a resumed parked ticket's 6.
   *
 * So the guard is now the one `refreshAddons()` has used all along: a sequence token: made
   * per-slot and made PURE so it can actually be tested (`repriceAnswerIsCurrent`, `_pos.ts`);
   * round 2's inline `stillOurs()` had zero coverage in 3311 tests, which is why forcing it true
   * reproduced the original defect with nothing going red. Four independent reasons to drop an
   * answer, each measured biting a different gesture: the cart generation (a previous customer's
 * ticket), the slot's own token (a newer reprice was issued: every quantity change reprices),
   * the product id (round 2's check, kept), and the qty/uom the answer was actually computed for.
   *
   * And the write-back now goes through `applyPriceAnswer`, which copies the money fields onto the
   * line that is THERE. Both halves matter and they are not redundant: the token stops a stale
   * answer applying at all, and not spreading the snapshot means a late answer that somehow does
   * apply still cannot carry old operator state back onto the ticket.
   *
   * A dropped answer costs a repaint and never a price: the engine re-resolves everything
   * authoritatively at tender time.
   */
  private async reprice(idx: number): Promise<void> {
    const line = this.cart[idx];
    if (!line) return;
    const seq = (this.repriceSeq[idx] = (this.repriceSeq[idx] ?? 0) + 1);
    const stamp = repriceStamp(this.cartGen, seq, line);
    /** Is this answer still about the line that is in the slot NOW? */
    const stillOurs = (): boolean =>
      repriceAnswerIsCurrent(stamp, this.cartGen, this.repriceSeq[idx] ?? 0, this.cart[idx]);
    try {
      const b = await resolveLine({
        product_id: line.product.id,
        customer_id: this.customer?.id ?? '',
        customer_type: this.customer?.customer_type ?? 'RETAIL',
        branch_id: this.branchId,
        qty_entered: line.qty,
        uom_entered: line.uom,
        cost_cents: 0,
      });
      const cur = this.cart[idx];
      if (!stillOurs() || !cur) return; // superseded, or the ticket moved while this was in flight
      const next = [...this.cart];
      next[idx] = applyPriceAnswer(cur, {
        unitPriceCents: b.final_cents,
        priceUom: b.price_uom,
        lineTotalCents: b.line_total_cents,
        breakdown: b,
        priceMissing: false,
      });
      this.cart = next;
      this.scheduleTicketFacts(); // rev 8: a repriced ticket refreshes margin + suggestions
    } catch {
      // rev 5: the line stays unpriced (the server still re-resolves authoritatively at tender
 // time), but it must SAY so: a hard "CA$0.00" where a price failed to resolve reads as a
      // real price until the ring refuses it. priceMissing renders "price pending" instead.
      const cur = this.cart[idx];
      if (!stillOurs() || !cur) return; // same reason: a failure for a line that is gone marks nothing
      const next = [...this.cart];
      next[idx] = { ...cur, priceMissing: true };
      this.cart = next;
    }
  }

  private setQty(idx: number, qty: number): void {
 // rev 5: cleanQty (pure, _pos.ts) also catches NaN: a scanner burst landing in a focused qty
    // box used to store a literal NaN on the line (stuck steppers, stale totals, silence).
    const next = [...this.cart];
    next[idx] = { ...next[idx], qty: cleanQty(qty) };
    this.cart = next;
    this.ticketChanged();
    void this.reprice(idx);
  }

  /**
 * rev 5: the qty-entry gesture ENDS back at the scan box. With ask-for-quantity on, focus
   * parked in the qty box after Enter, so the next scanner burst typed a SKU into the quantity
   * (the exact "600,000 pavers" scenario the scan box's docstring warns about). Enter now commits
   * the qty (change fires on blur) and hands the caret back to the gun's input.
   */
  private onQtyKeydown(e: KeyboardEvent): void {
    if (e.key !== 'Enter') return;
    e.preventDefault();
    (e.target as HTMLInputElement).blur(); // commit via the change event first
    this.focusScanBox();
  }

  /** Return the caret to the scan box (the counter's rest state) once the DOM settles. */
  /**
   * The field's Enter fell through to the results (a TYPED miss, never a scanner burst): commit the
   * top row. This is the client's "Enter takes the top hit", and it deliberately lives here rather
 * than in the scan box: the box does not own the result list, and a box that reached into it
   * would be a box that could commit a row nobody could see.
   *
   * No hit means no commit: the term stays in the field and the browse path is one press away.
   * Enter must never invent a product.
   */
  private commitTopHit(): void {
    const drop = this.renderRoot.querySelector<HTMLElement & { commitTop?: () => boolean; topHit?: () => { name: string; sku: string } | null }>(
      'hos-pos-search-drop',
    );
    const top = drop?.topHit?.() ?? null;
    if (!drop || !top || !drop.commitTop?.()) return;
    const box = this.renderRoot.querySelector<HTMLElement & { commitDone?: (m: string) => void }>('hos-pos-scan-box');
    box?.commitDone?.(`Added ${top.name} · ${top.sku}`);
  }

  /** ArrowDown out of the field walks into the result list; an empty list keeps the caret. */
  private enterResultList(): void {
    const drop = this.renderRoot.querySelector<HTMLElement & { focusFirstRow?: () => boolean }>('hos-pos-search-drop');
    drop?.focusFirstRow?.();
  }

  private focusScanBox(): void {
    void this.updateComplete.then(() => {
      this.renderRoot.querySelector<HTMLElement & { focusInput?: () => void }>('hos-pos-scan-box')?.focusInput?.();
    });
  }

  // ── requested-date entry (DIB-198 calendar + DIB-200 compact YYYYMMDD + DIB-259 single box) ──
  // DIB-259: the calendar button sits next to the single date box; clicking it opens the native
  // picker on the visually-hidden <input type="date"> that shares requestedDate. showPicker() is a
 // progressive enhancement: guarded (older browsers lack it) and wrapped, because it throws
  // without user activation. Never fatal: the clerk can still type the date. No-op if the hidden
  // input isn't in the DOM yet (SSR / test runner).
  private openDatePickerFromButton(e: Event): void {
    const btn = e.currentTarget as HTMLElement;
    const hidden = btn.parentElement?.querySelector('input.datehidden') as
      | (HTMLInputElement & { showPicker?: () => void })
      | null;
    if (!hidden || typeof hidden.showPicker !== 'function') return;
    try {
      hidden.showPicker();
    } catch {
 /* not user-activated / unsupported: ignore */
    }
  }

  // DIB-200: commit a compact entry to requestedDate. parseCompactDate (pure, unit-tested) turns
 // a compact run of digits into the ISO form, and passes a valid ISO string straight through, so
 // this box and the native picker agree. Returns whether the raw value was a usable date.
  private commitCompactDate(raw: string): boolean {
    const iso = parseCompactDate(raw);
    if (!iso) return false;
    this.requestedDate = iso;
    this.dateError = '';
    return true;
  }

  // Live path: as the clerk types, file the date the instant a full 8 digits are present (never
  // tabbing year→month→day). DIB-259: this is now the single visible date field, so the committed
 // value STAYS in the box (bound to requestedDate as ISO) rather than being cleared: the box is
  // the one place the chosen date shows.
  private onCompactDateInput(e: Event): void {
    const el = e.target as HTMLInputElement;
    if (this.dateError) this.dateError = '';
    if (el.value.replace(/\D/g, '').length >= 8) this.commitCompactDate(el.value);
  }

  // Commit path (blur / Enter): validate a non-empty entry and show an inline hint if it's not a
  // real YYYYMMDD date. An empty box just clears any prior error (the native picker still owns it).
  private onCompactDateChange(e: Event): void {
    const el = e.target as HTMLInputElement;
    const raw = el.value.trim();
    if (!raw) {
      this.dateError = '';
      this.requestedDate = '';
      return;
    }
    // DIB-259: on a valid entry keep the normalised ISO value in the box (via requestedDate) instead
    // of clearing it; only surface the hint when the 8 digits aren't a real date.
    if (this.commitCompactDate(raw)) el.value = this.requestedDate;
    else {
      this.dateError = 'Type 8 digits: YYYYMMDD.';
 // DIB-259 (ship review): never leave a stale committed ISO behind a visible error: the box
      // text and requestedDate would desync and the sale could ring with a hidden date the clerk
      // never sees. Clear the hidden value; the ring path is also guarded on dateError below.
      this.requestedDate = '';
    }
  }

  private removeLine(idx: number): void {
    this.cart = this.cart.filter((_, i) => i !== idx);
    this.ticketChanged();
    this.scheduleTicketFacts(); // rev 8: fewer lines = a different margin footer + suggestion set
  }

   // HH note: the pre-ring figures are the counter's ESTIMATE. The subtotal is
   // the priced lines' own assembly (host seam), but HH's contract gives the
   // counter no rate (PricingBreakdown carries no tax, and the rate table is
   // money-desk), so the tax line applies this surface's default of 1300 bps
   // unless the relief flow set another; the dealer's rate table may differ.
   // The surface says so: the tax row reads (est.) and the total row carries
   // the estimate mark, and the ring's committed figures are the binding ones.
  private subtotal(): number {
    return cartSubtotalCents(this.cart);
  }
  // Preview rate: the applied relief's engine-assessed bps, else the counter's
  // 13% default. The binding rate is the core's, at the ring.
  private taxRateBps(): number {
    return this.reliefRateBps ?? 1300;
  }
  private tax(): number {
    return ticketTaxCents(this.subtotal(), this.taxRateBps());
  }
  private taxLabel(): string {
    const bps = this.taxRateBps();
    const pct = bps % 100 === 0 ? String(bps / 100) : (bps / 100).toFixed(2);
    const relief = this.reliefRateBps === null ? '' : ' (relief)';
    return `HST ${pct}%${relief} (est.)`;
  }
  private total(): number {
    return ticketTotalCents(this.subtotal(), this.tax());
  }
 // HH note: an empty draft shows the app's blank marker, never a plausible
 // zero (the house rule; the reference showed CA$0.00 here, and HH's rule
 // wins). The summary speaks only when EVERY line is priced: an empty cart, or
 // any line still pending (a just-added line before its price lookup answers,
 // or a line whose reprice failed, which keeps its old total), keeps the whole
 // summary on the marker rather than a confident figure beside a pending word.
  private hasPricedLine(): boolean {
    return this.cart.length > 0 && this.cart.every((l) => !l.priceMissing);
  }
  private moneyOrMarker(cents: number): string {
    return this.hasPricedLine() ? money(cents) : '(not priced)';
  }

  // A deposit rides only an unpaid-at-ring posture: Charge Account tender or a COD sale.
 // HH note: the reference parsed the typed deposit through a float; the house rule converts
 // through the decimal string, so the parse is a host seam.
  private depositCents(): number {
    return depositCentsFromTyped(this.depositDollars || '');
  }

  /** DIB-512 · is this sale going out on a truck? (Delivery tile, or a COD released by delivery.) */
  private deliveryActive(): boolean {
    return this.saleType === 'DELIVERED' || (this.saleType === 'COD' && this.fulfilment === 'DELIVERY');
  }

  /**
   * DIB-512 · a deposit rides only Charge Account or COD (the ring path has always enforced it);
   * the label used to carry that policy as a nine-word clause. The constraint is now the
 * control's STATE: disabled, with the why on its flyout, while a paid-now tender is chosen.
   */
  private depositAllowed(): boolean {
    return this.saleType === 'COD' || this.payMethod === 'ACCOUNT' || this.payMethod === '';
  }

  // ── customer picker (account-tender UX fix) ───────────────────────────────
  private openPicker(hint = ''): void {
    this.pickerHint = hint;
    this.showPicker = true;
  }

  // onCustomerSelected commits the picked account (or null → walk-in) and re-resolves every
  // visible line: a contractor's tier price differs from RETAIL, so the on-screen prices must
  // follow the customer. The server re-prices authoritatively at ring time regardless.
  private async onCustomerSelected(c: PosCustomer | null): Promise<void> {
    this.showPicker = false;
    this.pickerHint = '';
    this.customer = c;
    void this.refreshStanding();
 // DIB-488: either answer RESOLVES the flow's Customer step: an account, or an explicit
    // walk-in choice from the picker. (The untouched default resolves nothing; see _pos-flow.ts.)
    if (c === null) this.walkInConfirmed = true;
    else this.walkInConfirmed = false;
    for (let i = 0; i < this.cart.length; i++) await this.reprice(i);
    // POS restructure: the customer question is ANSWERED, so the next thing anyone does is put
 // items on the ticket: the accordion follows the sale forward and lands the caret in the scan
    // box. It moves only on this and on the ring (advanceOn); no other state change touches it.
    this.advanceAccordion('customer-resolved');
  }

  /**
   * The selected account's past-due position.
   *
   * A failed read and a current account are the SAME empty answer here, on purpose (the reasoning
   * lives in backend/internal/pos/standing.go): a counter must never be told an account is current
   * because a query timed out, and must never be shown a fault banner over a fact nobody asked
   * for. So this never throws, never sets an error, and never announces anything.
   */
  private async refreshStanding(): Promise<void> {
    const id = this.customer?.id ?? '';
    const branch = this.branchId;
    if (!id) {
      this.standing = {};
      return;
    }
    const out = await accountStanding(branch, id);
 // D17, round 3: the SECOND instance of the shape reprice() had. `this.standing = await …` read
    // the customer before the round trip and wrote the answer after it, with nothing in between
    // checking the customer was still the same one. Two quick changes in the picker (a mis-click
    // and a correction, which is the ordinary way a wrong account gets fixed) render an OLDER
 // account's past-due position and hold flag against the account now on screen: on the surface
    // that takes the money, beside the control that refuses a sale.
    //
    // Dropping is the right answer and not merely the safe one: a newer refreshStanding is already
    // in flight for the customer that IS selected, and a standing that is absent renders nothing,
    // which is this surface's documented empty answer (backend/internal/pos/standing.go: a counter
    // must never be told an account is current because a query timed out).
    if ((this.customer?.id ?? '') !== id || this.branchId !== branch) return;
    this.standing = out;
  }

  /**
   * HH: after a sale rings, the picked account's card must show the account's figures as the core
   * now holds them. An account sale grows the open balance (and shrinks the available credit); the
   * card carried the figures read when the account was picked, so it stood beside the new
   * confirmation with the OLD balance. This re-reads the account through the same host seam the
   * picker uses (customer search, branch walled) and replaces the card's credit figures only.
   * A failed read leaves the card as it was, and an answer for a customer no longer selected is
   * dropped, as refreshStanding does.
   */
  private async refreshCustomerAfterRing(): Promise<void> {
    const picked = this.customer;
    if (!picked || !picked.account_no) return;
    try {
      const rows = await searchCustomers(picked.account_no, 20, this.branchId);
      const fresh = rows.find((row) => row.id === picked.id);
      if (!fresh || this.customer?.id !== picked.id) return;
      this.customer = {
        ...picked,
        credit_limit_cents: fresh.credit_limit_cents,
        open_balance_cents: fresh.open_balance_cents,
      };
    } catch {
      // the card keeps the figures it has: no fault banner over a fact nobody asked for
    }
    void this.refreshStanding();
  }

 /** DIB-488: the customer card's explicit Walk-in tile: clears any account and confirms walk-in. */
  private async chooseWalkIn(): Promise<void> {
    this.walkInConfirmed = true;
    if (this.customer !== null) {
      this.customer = null;
      this.standing = {}; // a walk-in has no account and therefore no standing to report
      for (let i = 0; i < this.cart.length; i++) await this.reprice(i); // back to RETAIL pricing
    }
    this.advanceAccordion('customer-resolved');
  }

  /**
 * advanceAccordion: the ONLY way the expansion moves on its own.
   *
   * advanceOn() (pure, tested) answers with a step or with null, and null means "leave the operator
   * where they are". That restraint is the design: an accordion that re-opens itself on every state
   * change fights the person using it, and a clerk who deliberately opened Customer mid-ticket must
   * not have the card yanked shut because a scan landed.
   */
  private advanceAccordion(event: FlowAdvance): void {
    const next = advanceOn(event);
    if (next) this.openAccordion(next);
  }

  // ── ring a sale ─────────────────────────────────────────────────────────
  /**
 * DIB-165: ring the sale across several tenders. Deliberately a thin wrapper over the SAME
   * createSale call the single-tender path uses: a split is not a different kind of sale, it is the
   * same sale with a `tenders` array, and having two ring implementations is how the two drift.
   *
   * `tender` is omitted from the body: the server derives the primary leg (the largest) and would
   * ignore a scalar tender anyway, so sending one would be a lie the next reader has to decode.
   */
  private async ringSplitSale(tenders: { method: string; amount_cents: number; payment_token?: string }[]): Promise<void> {
    if (this.cart.length === 0) return;
    if (this.dateError) {
      this.setUtil('Fix or clear the requested date before ringing the sale.', 'bad');
      return;
    }
    this.soldPanelDismissed = false; // DIB-444: a new ring always gets its own confirmation
    this.ring = { state: 'busy' };
    const payload = {
      branch_id: this.branchId,
      till_session_id: this.tillSessionId,
      customer_id: this.customer?.id ?? '',
      sale_type: this.saleType,
      tender: '', // derived from the legs server-side
      signature_ref: this.signatureRef || undefined,
      requested_date: this.requestedDate || undefined,
      notes: this.notes || undefined,
      // rev 6 (item D): the More-options facts ride the split ring too (a split is the same sale).
      po_job_ref: this.poJobRef.trim() || undefined,
      salesperson_id: this.salespersonId || undefined,
 // rev 8 (item 11): the job facts too: new body fields, so stableRingKey re-mints naturally.
      job_address: this.jobAddress.trim() || undefined,
      job_reference: this.jobReference.trim() || undefined,
      tenders,
      lines: this.cart.map((l) => ({
        product_id: l.product.id,
        qty_entered: l.qty,
        uom_entered: l.uom,
        cost_cents: 0,
      })),
    };
 // rev 5: an IDENTICAL retry reuses the previous attempt's key VERBATIM (stableRingKey): that
    // is what makes "retry; the sale is replay-safe" true. Any edit to the sale re-mints; every
    // leg's payment key still derives from the one sale key server-side.
    this.ringKeyMemo = stableRingKey(this.ringKeyMemo, JSON.stringify(payload), newIdempotencyKey);
    const body = { idempotency_key: this.ringKeyMemo.key, ...payload };
    try {
      const res = await createSale(body);
      if (res.ok) {
        this.ringKeyMemo = null; // committed: the next sale mints fresh
        this.ring = { state: 'done', sale: res.sale };
        this.showSplit = false;
        this.revealSaleOutcome();
        void this.refreshCustomerAfterRing();
        return;
      }
      if (res.err.status === 409 && res.err.error === 'credit_blocked') {
        this.dispatchEvent(
          new CustomEvent('hos-action-blocked', {
            detail: { policy: res.err.policy, error: res.err.error },
            bubbles: true,
            composed: true,
          }),
        );
 // HH note: the split's credit refusal words come from the same helper the
 // single ring's do (creditBlockMessage): the sentence follows the policy the
 // gate named, so a walk-in COD sale is not told about an account it does not
 // have. A price_blocked refusal has no arm (row 481) and falls
 // through to the server's own word below.
        this.ring = {
          state: 'error',
          msg: creditBlockMessage(res.err.policy, !!this.customer),
        };
        return;
      }
      if (res.err.status === 402) {
        // A declined card leg rolls the WHOLE split back server-side, so the panel stays open with
        // the legs intact and the cashier can swap that one leg for cash.
        this.ring = { state: 'error', msg: 'A card leg was declined: the whole sale was rolled back. Try another tender.' };
        return;
      }
      this.ring = { state: 'error', msg: counterRingMessage(res.err) };
    } catch {
      this.ring = { state: 'error', msg: 'Network error: retry; the sale is replay-safe.' };
    }
  }

  private async ringSale(tender: 'ACCOUNT' | 'CASH' | 'CARD'): Promise<void> {
    if (this.cart.length === 0) return;
    // DIB-259 (ship review): refuse to ring while the requested-date box holds an unparseable entry.
    // Otherwise a stale/blank requestedDate would silently go on the order under a visible error.
    if (this.dateError) {
      this.setUtil('Fix or clear the requested date before ringing the sale.', 'bad');
      return;
    }
    // Charge Account needs an account. With no customer selected, open the picker (the
    // commander's requested UX) instead of ringing a nonsensical no-account sale.
    if (tender === 'ACCOUNT' && !this.customer) {
      this.openPicker('Pick the account to charge.');
      return;
    }
    this.soldPanelDismissed = false; // DIB-444: a new ring always gets its own confirmation
    this.ring = { state: 'busy' };
    const isCOD = this.saleType === 'COD';
    const unpaidAtRing = tender === 'ACCOUNT' || isCOD;
    const deposit = unpaidAtRing ? this.depositCents() : 0;
    const cardAtRing = (tender === 'CARD' && !unpaidAtRing) || (deposit > 0 && this.depositMethod === 'CARD');
    // rev 6 (item F): the optional cash quick entry rides the body ONLY when it covers the total
 // on a paid CASH ring (cashTenderedPlan: a short or empty entry sends nothing; one-click cash
    // is byte-identical to before). Being in the BODY, it re-mints the idempotency key naturally
    // through stableRingKey's fingerprint when it changes.
    const cashPlan = cashTenderedPlan(this.total(), this.cashTenderedDollars);
    const sendCashTendered = tender === 'CASH' && !unpaidAtRing && cashPlan.sendable;
    const payload = {
      branch_id: this.branchId,
      till_session_id: this.tillSessionId,
      customer_id: this.customer?.id ?? '',
      sale_type: this.saleType,
      fulfilment: isCOD ? this.fulfilment : undefined,
      tender,
      payment_token: cardAtRing ? 'tok_simulated_runner' : undefined,
      card_type: cardAtRing ? this.cardType : undefined,
      signature_ref: this.signatureRef || undefined,
      requested_date: this.requestedDate || undefined,
      notes: this.notes || undefined,
      // rev 6 (item D): the optional More-options facts.
      po_job_ref: this.poJobRef.trim() || undefined,
      salesperson_id: this.salespersonId || undefined,
 // rev 8 (item 11): the job facts: in the fingerprinted body, so the key re-mints naturally.
      job_address: this.jobAddress.trim() || undefined,
      job_reference: this.jobReference.trim() || undefined,
      cash_tendered_cents: sendCashTendered ? cashPlan.tenderedCents : undefined,
      deposit_cents: deposit > 0 ? deposit : undefined,
      deposit_method: deposit > 0 ? this.depositMethod : undefined,
      lines: this.cart.map((l) => ({
        product_id: l.product.id,
        qty_entered: l.qty,
        uom_entered: l.uom,
        cost_cents: 0,
      })),
    };
 // rev 5: same replay-safety rule as the split path: an identical retry reuses its key
    // verbatim (stableRingKey); any edit to the sale re-mints; success clears the memo.
    this.ringKeyMemo = stableRingKey(this.ringKeyMemo, JSON.stringify(payload), newIdempotencyKey);
    const body = { idempotency_key: this.ringKeyMemo.key, ...payload };
    try {
      const res = await createSale(body);
      if (res.ok) {
        this.ringKeyMemo = null; // committed: the next sale mints fresh
        this.ring = { state: 'done', sale: res.sale };
        this.revealSaleOutcome();
        void this.refreshCustomerAfterRing();
        return;
      }
 // HH note: only the credit gate is worded here, and its words follow the
 // policy the gate named (creditBlockMessage, one rule for both ring arms): a
 // hold or an over-the-limit refusal keeps HH's reviewed account sentence,
 // which is true there; a walk-in COD sale is refused under the COD
 // constraint and says so instead, since there is no account to speak of. A
 // price_blocked refusal has no arm at all (row 481: no margin floor at the
      // till), so it falls through to the server's own word below, like every
      // other refusal.
      if (res.err.status === 409 && res.err.error === 'credit_blocked') {
        this.dispatchEvent(
          new CustomEvent('hos-action-blocked', {
            detail: { policy: res.err.policy, error: res.err.error },
            bubbles: true,
            composed: true,
          }),
        );
        this.ring = {
          state: 'error',
          msg: creditBlockMessage(res.err.policy, !!this.customer),
        };
        return;
      }
      if (res.err.status === 402) {
        this.ring = { state: 'error', msg: 'Card declined: try another tender.' };
        return;
      }
      // DIB-379/F2: surface the server's real detail for EVERY ring failure (409 no-till-session, 503
 // an unwired guard, 422 policy/validation refusals, …), not just 422: see ringErrorMessage.
      this.ring = { state: 'error', msg: counterRingMessage(res.err) };
    } catch {
      // Network error: the key is safe to retry verbatim (idempotent endpoint).
      this.ring = { state: 'error', msg: 'Network error: retry; the sale is replay-safe.' };
    }
  }

  /**
 * rev 5: the confirmation moment must be SEEN and the next scan must LAND. The sold panel
   * renders near the foot of the sale panel's internal scroll, so right after Complete the clerk
   * saw a 60px sliver of it (measured); and focus died on the disabled CTA. Scroll the sold panel
 * into its panel's view AND hand the caret to the scan box: the two panels scroll separately,
   * so both happen. role=status on the panel still announces without stealing focus.
   */
  private revealSaleOutcome(): void {
    // POS restructure: COMPLETING PAYMENT EXPANDS SUMMARY. The confirmation used to render at the
    // foot of the sale panel's own scroll, where a clerk saw a 60px sliver of it (measured) and
 // focus died on the disabled CTA. It is a STEP now, and the ring opens it: which also scrolls
    // it to the top of the column and lands focus on Print receipt, the thing a person finishing a
    // sale reaches for. The old focusScanBox() call is deliberately gone: with Items collapsed
    // there is no scan box mounted to receive the caret, and focusing a control that is not on
    // screen is exactly the focus game the doctrine forbids.
    this.advanceAccordion('sale-rung');
  }

  private newSale(): void {
    this.cart = [];
    this.cartReplaced(); // D17: the next customer must not receive this one's pricing answers
    this.customer = null; // back to walk-in
    this.standing = {};
    this.notes = '';
    this.requestedDate = '';
    this.dateError = '';
    this.reliefReason = '';
    this.reliefRateBps = null;
    this.signatureRef = '';
    this.depositDollars = '';
    this.fulfilment = 'PICKUP';
    this.clearUtil();
    this.soldPanelDismissed = false;
    this.ring = { state: 'idle' };
    // DIB-488: the flow facts reset with the sale they describe.
    this.walkInConfirmed = false;
    this.payMethod = 'CASH'; // the next ticket is tenderable the moment it exists
    this.tenderConfirmed = false; // …and un-answered: the posture returns, the decision does not
    this.showSplit = false;
 // rev 6: the More-options facts and the cash quick entry reset with the sale too: the
    // salesperson returns to its default (the signed-in operator), never to '' mid-shift.
    this.poJobRef = '';
    this.salespersonId = getCurrentUser()?.user_id ?? '';
    this.cashTenderedDollars = '';
    this.jobAddress = ''; // rev 8: the job facts reset with the sale they describe
    this.jobReference = '';
    this.payTileIdx = 1; // DIB-512: the tender group's resting stop returns to Cash with the sale
    this.scheduleTicketFacts(); // rev 8: an empty ticket clears the margin footer + the add-ons strip
    this.ringKeyMemo = null; // rev 5: a fresh sale never inherits a prior attempt's key
    // DIB-254/256: whenever the order resets (New sale / Clear sale / after Park), return to the
    // top so the next customer starts at the head of the terminal, not stranded at the tenders.
    this.resetScroll();
    // rev 5: …and the caret returns to the scan box, so Complete → Next sale → scan is unbroken.
    this.focusScanBox();
  }

  // DIB-254/256: scroll the terminal back to the top. window.scrollTo covers the page-scroll shell;
  // scrollIntoView covers a shell that scrolls an inner container (e.g. a popped-out window). Both
  // are guarded for the no-DOM test runner and detached/older environments.
 // rev 5: the panels own their scroll since rev 2 (.wsbody), so "the top" must include THEM:
  // without it the fresh ticket opened mid-panel, contradicting this very comment (DIB-254/256).
  private resetScroll(): void {
    try {
      this.renderRoot?.querySelectorAll<HTMLElement>('.wsbody').forEach((el) => (el.scrollTop = 0));
    } catch {
 /* no-DOM: non-fatal */
    }
    try {
      this.scrollIntoView?.({ block: 'start', behavior: 'auto' });
    } catch {
 /* detached / unsupported: non-fatal */
    }
    try {
      if (typeof window !== 'undefined') window.scrollTo?.({ top: 0, left: 0, behavior: 'auto' });
    } catch {
 /* no-DOM: non-fatal */
    }
  }

 // DIB-245/262 (ship review): the confirm must fire whenever there's ANYTHING to lose: not just
  // cart lines. A clerk who selected a customer (or typed a requested date / notes) but hasn't added
  // a line yet would otherwise have that work wiped with no prompt, contradicting the button tooltips.
  // clearSaleNeedsConfirm(cartLength) still owns the cart-only rule (unit-tested); this widens it.
  private orderHasContent(): boolean {
 // rev 5: once the ring is DONE the ticket on screen is a committed record: nothing can be
    // lost, so the DIB-245 discard-confirm stands down. Sold-panel "Next sale" (and the util row's
    // "New sale") must not tell a clerk their just-committed order "will be discarded".
    if (this.ringDone()) return false;
    return (
      clearSaleNeedsConfirm(this.cart.length) ||
      !!this.customer ||
      !!this.requestedDate ||
      !!this.notes.trim() ||
      // rev 8 (item 11): a typed job fact is work even before the first line goes on.
      !!this.jobAddress.trim() ||
      !!this.jobReference.trim()
    );
  }

  // DIB-244: one-click clear of the whole SO. Reuses newSale()'s reset (lines, customer, notes,
  // dates, deposit, scroll) rather than duplicating it; the confirm fires whenever the order holds
  // something to lose (orderHasContent), so an empty terminal clears silently.
  private clearSale(): void {
    if (
      this.orderHasContent() &&
      typeof window !== 'undefined' &&
      !window.confirm('Clear this sale? All lines and the selected customer will be removed.')
    ) {
      return;
    }
    this.newSale();
  }

 // DIB-245: the "New sale" button used to reset the terminal silently: a clerk who tapped it
  // mid-order lost the in-progress SO with no warning (reported via the widget). Guard it with the
  // SAME confirm gate as Clear sale (only when the cart holds something to lose). Internal callers
 // of newSale(): clearSale() and parkCurrent(): deliberately bypass this so they don't
  // double-prompt after their own handling.
  private newSaleClicked(): void {
    if (
      this.orderHasContent() &&
      typeof window !== 'undefined' &&
      !window.confirm('Start a new sale? The current order: all lines and the selected customer: will be discarded.')
    ) {
      return;
    }
    this.newSale();
  }

  // ── DIB-434 ── the quick-action entry point: "start a fresh ticket", from anywhere in the OS.
  //
  // THIS IS THE ONLY DOOR THE GLOBAL "+ Sale" HAS INTO THE TERMINAL, and it deliberately does NOT
  // reach for newSale(). The shipped New sale button already guards itself with a window.confirm;
  // a quick action fired from a screen where this till was not even visible needs a stronger answer
  // than a browser confirm whose only outcomes are "lose it" and "cancel". So it asks the pure
  // decision (quick-action-guard.ts) and, if there is anything to lose, raises a dialog whose
 // outcomes are Continue and PARK: the parked-tickets rail (POS Counter V2 / Q-042) is shipped
  // and this integrates with it rather than rebuilding it.
  //
  // The only path that resets the terminal without asking is the one where the till is completely
  // empty, which is the case where there is, by definition, nothing to clobber.

  /** The live ticket, as the three primitives the guard reads. */
  private liveTicket(): LiveTicket {
 // rev 5: a rung ticket is committed, not "in progress": the quick-action guard has nothing
    // to protect (same stand-down as orderHasContent).
    if (this.ringDone()) return { lineCount: 0, hasCustomer: false, hasOtherWork: false };
    return {
      lineCount: this.cart.length,
      hasCustomer: !!this.customer,
      // Same widened rule as orderHasContent() (DIB-245/262): a typed note or a requested date is
      // work even before the first line goes on.
      hasOtherWork: !!this.requestedDate || !!this.notes.trim(),
    };
  }

  private onQuickIntent = (e: Event): void => {
    const detail = (e as CustomEvent<PendingIntent>).detail;
    if (!detail || detail.module !== 'pos') return;
    // Consume through the channel (not straight off the event) so the mounted and mounting paths
    // cannot both act on the same click.
    this.applyQuickIntent(takeIntent('pos', [INTENT_POS_NEW_TICKET, INTENT_POS_PAY]));
  };

  private applyQuickIntent(intent: PendingIntent | null): void {
    if (!intent) return;
    if (intent.intent === INTENT_POS_NEW_TICKET) {
      this.quickStartSale();
      return;
    }
    if (intent.intent === INTENT_POS_PAY) this.hotkeyPay();
  }

  /**
   * UNION-TRAIN11 · the dealer's pay key (the concept's "F8 Pay"). An ARMED till rings through the
 * SAME guarded completeSale() the CTA click takes: every rev-5 protection (busy, the DONE
   * disarm, the SPLIT deferral) holds because the guard IS the method's own first line. An
 * un-armed till is walked to the payment step instead of silently no-opping: the key was
   * pressed on purpose, and the panel it lands on says exactly what is still missing (the CTA's
   * own split state / the tiles). A DONE ring does nothing: the committed basket must never
   * re-ring, and the sold panel's Next sale is the forward path.
   */
  private hotkeyPay(): void {
    if (this.ringBusy() || this.ringDone()) return;
    if (this.cart.length > 0 && this.payMethod !== '' && this.payMethod !== 'SPLIT') {
      this.completeSale();
      return;
    }
    this.onFlowGoto('payment');
  }

 /** Start a fresh ticket: or stop and ask, if the till is holding one. */
  private quickStartSale(): void {
    this.clearUtil();
    if (quickStartNeedsGuard(this.liveTicket())) {
      this.quickGuard = true;
      return;
    }
    this.newSale();
    this.setUtil('New ticket ready.');
  }

  /** Continue: the guard's safe answer. The till is left EXACTLY as it was. */
  private onQuickContinue(): void {
    this.quickGuard = false;
    this.quickGuardBusy = false;
  }

  /**
   * Park it & start new: hand the ticket to the shipped rail, then reset.
   *
   * parkCurrent() already does both (park → snapshot the display facts → newSale) and reports its
   * own failure, so this adds nothing except the dialog's lifecycle. The dialog stays UP while the
 * request is in flight and closes only on success: if the park fails, the ticket is still on the
   * till and the operator is still looking at the choice, which is the correct outcome for a guard
   * whose promise is that nothing is lost.
   */
  private async onQuickPark(): Promise<void> {
    if (this.quickGuardBusy) return;
    this.quickGuardBusy = true;
    const before = this.cart.length;
    await this.parkCurrent();
    this.quickGuardBusy = false;
    // parkCurrent() clears the cart on success (via newSale) and leaves it alone on failure.
    if (this.cart.length === 0 && before > 0) this.quickGuard = false;
  }

  /**
 * Clear it & start new: offered ONLY when there are no lines, because a line-less ticket is not
   * something the parked rail can hold. The button names what it clears and the operator pressed
   * it; that is the difference between this and the silent reset the guard exists to prevent.
   */
  private onQuickClear(): void {
    this.quickGuard = false;
    this.quickGuardBusy = false;
    this.newSale();
    this.setUtil('New ticket ready.');
  }
  // ── end DIB-434 ──

  // ── W7: park / resume / pricing doc ────────────────────────────────────
  private async parkCurrent(): Promise<void> {
    if (this.cart.length === 0) return;
    this.clearUtil();
    try {
      const t = await parkTicket({
        branch_id: this.branchId,
        customer_id: this.customer?.id || undefined,
        sale_type: this.saleType,
        notes: this.notes || undefined,
        lines: this.cart.map((l) => ({ product_id: l.product.id, qty_entered: l.qty, uom_entered: l.uom })),
      });
      // DIB-246/247: snapshot who + how much under the server ticket id BEFORE newSale() clears
      // them, so resume can restore this customer and the parked list can show name + total.
      this.parkedInfo.set(t.id, { customer: this.customer, totalCents: this.hasPricedLine() ? this.total() : null });
      this.newSale();
      this.setUtil(`Parked as ${t.ticket_no}: resume re-prices live.`);
      void this.refreshParkedCount();
    } catch {
      this.setUtil('Park failed: retry.', 'bad');
    }
  }

  // Resume: re-add the saved lines through the NORMAL engine-priced path (never a stale price).
  private async onParkedResume(t: ParkedTicket): Promise<void> {
    this.showParked = false;
    const openLines = this.cart.length; // what the rail's confirm was asked about
    try {
 // D17, round 3: the THIRD instance of the shape. `newSale()` below empties the cart, and
      // anything the operator put on the ticket while this catalogue read was in flight goes with
 // it. The DESTRUCTIVE half is answered before the resume is committed at all: the parked
      // rail asks, holding this line count, before it POSTs /resume (see pos-parked.ts). What is
      // left is the narrow race: lines added AFTER that question and BEFORE this answer.
      //
      // It cannot be answered the same way. By the time this runs the server has already moved the
      // ticket to RESUMED and taken it out of the parked list, so there is nothing to decline back
 // to: refusing here would strand a ticket that is no longer parked and no longer on screen,
      // which is worse than the thing it would be preventing. So the race is ANNOUNCED instead of
      // silent, with the count, in the one place this screen speaks: a person who scanned into the
      // gap is told those lines went and can re-scan them, rather than discovering it at tender.
      const lostToTheRace = this.cart.length - openLines;
      const products = await listProducts();
      const byId = new Map(products.map((p) => [p.id, p]));
      this.newSale();
      // DIB-246: restore the customer captured at park time so the resumed ticket shows the SAME
      // account (and re-prices at that account's tier in the loop below) instead of reverting to
      // walk-in. Set before the cart is built so reprice() sees the right customer.
      this.customer = restoreParkedCustomer(t, this.parkedInfo);
      void this.refreshStanding();
      this.saleType = t.sale_type === 'COD' && !this.codEnabled ? 'COLLECT_NOW' : t.sale_type;
      this.notes = t.notes;
      const cart: CartLine[] = [];
      const missing: string[] = [];
      for (const ln of t.lines) {
        const p = byId.get(ln.product_id);
        if (!p) {
          missing.push(ln.product_id);
          continue;
        }
        cart.push({
          product: p,
          qty: ln.qty_entered,
          uom: ln.uom_entered || p.sold_by || p.base_uom || 'EA',
          unitPriceCents: 0,
          priceUom: ln.uom_entered,
          lineTotalCents: 0,
          breakdown: null,
          // Pending until the price lookup answers, the add path's own rule: the summary stays on
          // the marker rather than a zero for a line nobody has priced yet. Without the flag the
          // resumed ticket painted CA$0.00 totals and margin figures in the instant before the
          // first answer landed (ui3d visual NOTE).
          priceMissing: true,
        });
      }
      this.cart = cart;
      this.cartReplaced(); // D17: the resumed lines are a different ticket's
      for (const l of this.cart) void this.hydrateLineStock(l.product.id); // DIB-488 stock lines
      for (let i = 0; i < this.cart.length; i++) await this.reprice(i);
      // ui3 visual GAP 3, and round d: the words must be true in both states AND STAY true. The
      // line's price tail renders from the cart's any-line-pending state (pickupLineWords), so a
      // price that answers after this line was set flips it; only the resume's own facts are
      // captured here: the ticket, the dropped lines, the discarded race, the fault tone.
      this.pickupLine = {
        ticketNo: t.ticket_no,
        tails:
          (missing.length ? ` ${missing.length} line(s) dropped (product inactive).` : '') +
          (lostToTheRace > 0
            ? ` ${lostToTheRace} line(s) added while this loaded were discarded with the old ticket: re-scan them.`
            : ''),
        bad: lostToTheRace > 0,
      };
      void this.refreshParkedCount();
    } catch {
      this.setUtil(`Resumed ${t.ticket_no}, but the lines could not be reloaded: re-add manually.`, 'bad');
    }
  }

  private async savePricingDoc(): Promise<void> {
    if (this.cart.length === 0) return;
    this.clearUtil();
    try {
      this.pricingDoc = await createPricingOrder({
        branch_id: this.branchId,
        customer_id: this.customer?.id || undefined,
        customer_name: this.customer?.name ?? '',
        notes: this.notes || undefined,
        lines: this.cart.map((l) => ({ product_id: l.product.id, qty_entered: l.qty, uom_entered: l.uom })),
      });
    } catch {
      this.setUtil('Pricing doc failed: a line may be below the margin floor (manager approval).', 'bad');
    }
  }

  // ── DIB-488: the tracked flow + the payment tiles ─────────────────────────

  /** The stepper's steps, derived fresh from the sale data on every render (see _pos-flow.ts). */
  private flowSteps() {
    return deriveFlow({
      lineCount: this.cart.length,
      customerSelected: !!this.customer,
      walkInConfirmed: this.walkInConfirmed,
      // The POSTURE is not the ANSWER. Cash is on the sale from mount; a chosen tender is what
      // completes the step, so an unpressed default reports as no choice at all. See
      // tenderConfirmed for why the correction belongs here and not in deriveFlow.
      payMethod: this.tenderConfirmed ? this.payMethod : '',
      rungDone: this.ringDone(),
    });
  }

  /**
 * A step click SCROLLS AND FOCUSES: that is the entire contract (tracked, never gated). The
   * items step also hands the caret back to the scan box, because "go to items" at a counter means
   * "I want to add something".
   */
  private onFlowGoto(step: FlowStepId): void {
    this.openAccordion(step);
  }

  /**
 * toggleStep: the accordion header's whole contract.
   *
   * Selecting a step expands it and collapses the others. Selecting the OPEN one collapses it,
   * leaving four summary lines and no open card: a legitimate whole-sale-at-a-glance view, and the
   * reason the header button always does something rather than being a control that reports a
 * state it cannot change. Nothing here gates anything: this moves a disclosure, not the sale.
   */
  private toggleStep(step: FlowStepId): void {
    if (this.openStep === step) {
      this.openStep = '';
      // FOCUS SURVIVES THE COLLAPSE. The region and everything in it is unrendered on the next
      // frame, so if the operator closed the card from a control INSIDE it, focus would land on
 // <body>: the keyboard user's version of the screen going blank. It goes to the header they
      // just pressed, which is the control that reopens the card.
      void this.updateComplete.then(() =>
        this.renderRoot.querySelector<HTMLElement>(`[data-testid="step-head-${step}"]`)?.focus({ preventScroll: true }),
      );
      return;
    }
    this.openAccordion(step);
  }

  /**
 * openAccordion: expand a step, scroll it into view, and put the keyboard on the work.
   *
   * FOCUS IS THE POINT. Expanding a card that the operator then has to Tab into is barely better
   * than not collapsing at all, so focus goes to that step's FIRST MEANINGFUL control
 * (focusTargetFor, _pos-accordion.ts): the scan box for Items: a scanner burst must land
 * somewhere useful the instant the card opens: the Walk-in tile for Customer, the tender
   * group's roving stop for Payment, the receipt action for Summary. When that control has not
   * mounted (a busy or already-rung sale disarms the tenders), focus falls back to the disclosed
   * REGION itself, which is honest: the operator is in the right card with the right accessible
   * name announced, just not on a control that would have been wrong to press.
   */
  private openAccordion(step: FlowStepId): void {
    // Is the operator already working inside this step? Then this is a "show me where I am", not a
    // "take me somewhere": the card is scrolled into view and the CARET IS LEFT ALONE. Without this
 // an F-key press for the step you are already on: or an advance that fires while you are
 // already there: would yank the caret out of a half-typed search term and select its contents.
    const alreadyInside = this.openStep === step && this.focusIsInsideStep(step);
    this.openStep = step;
    void this.updateComplete.then(() => {
      this.scrollStepIntoView(step);
      if (alreadyInside) return;
      const region = this.stepRegion(step);
      if (step === 'items') {
        // The scan box owns its own caret (and its select-on-focus), so it is asked rather than
 // focused: the shipped scanner contract, unchanged.
        const box = this.renderRoot.querySelector<HTMLElement & { focusInput?: () => void }>('hos-pos-scan-box');
        if (box?.focusInput) {
          box.focusInput();
          return;
        }
      }
      if (step === 'payment' && !this.ringDone() && !this.ringBusy()) {
        this.focusPayTile(this.payTileIdx);
        return;
      }
      const sel = focusTargetFor(step);
      const target = sel ? this.renderRoot.querySelector<HTMLElement>(sel) : null;
      if (target) {
        target.focus?.({ preventScroll: true });
        return;
      }
      // The honest fallback: the disclosed REGION itself, which carries the header's accessible
 // name through aria-labelledby. Failing even that, the header: never <body>.
      if (region) region.focus?.({ preventScroll: true });
      else this.renderRoot.querySelector<HTMLElement>(`[data-testid="step-head-${step}"]`)?.focus({ preventScroll: true });
    });
  }

  /**
   * The step card element, and the disclosed REGION inside its shadow root.
   *
   * The region is `hos-pos-step-card`'s own markup, so `this.renderRoot.querySelector` cannot see it
 *: a fact that silently disabled the focus fallback until the keyboard walkthrough caught focus
   * sitting on <body> after every F-key step move.
   */
  private stepCard(step: FlowStepId): HTMLElement | null {
    return this.renderRoot.querySelector<HTMLElement>(`[data-step-card="${step}"]`);
  }

  private stepRegion(step: FlowStepId): HTMLElement | null {
    const card = this.stepCard(step);
    return card?.shadowRoot?.querySelector<HTMLElement>(`[data-testid="step-panel-${step}"]`) ?? null;
  }

  /** Is the active element inside this step's card? Walks out through shadow roots honestly. */
  private focusIsInsideStep(step: FlowStepId): boolean {
    const card = this.stepCard(step);
    if (!card) return false;
    let node: Element | null = (this.renderRoot as unknown as DocumentOrShadowRoot).activeElement ?? null;
    while (node) {
      if (node === card) return true;
      const parent: Element | null = node.parentElement;
      if (parent) {
        node = parent;
        continue;
      }
      const root = node.getRootNode();
      node = root instanceof ShadowRoot ? root.host : null;
    }
    return false;
  }

  /**
 * Put the open card FULLY on screen: the client's "always have the step, in view 100%".
   *
   * The arithmetic is scrollPlan()'s (pure, unit-tested); this is only the measuring and the
   * scrolling. A null plan means the card is already whole on screen and nothing moves, which is
   * the case that matters most: a scroll that fires when nothing needed to move is the jank the
   * critique's "waiting and jank" audit exists to catch.
   */
  private scrollStepIntoView(step: FlowStepId): void {
    try {
      const host = this.renderRoot.querySelector<HTMLElement>(`[data-step-card="${step}"]`);
      if (!host) return;
      const r = host.getBoundingClientRect();
      const target = scrollPlan(r.top + window.scrollY, r.height, window.scrollY, window.innerHeight);
      if (target === null) return;
      window.scrollTo({ top: target, behavior: 'smooth' });
    } catch {
 /* detached / no-layout environments: non-fatal */
    }
  }

  /** The disclosed region's DOM id, for the tests and the header's aria-controls pair. */
  /**
   * Pick a tender tile. SPLIT opens the compose panel (which owns its own ring); any plain tender
 * closes it. Picking is only CHOOSING: the sale rings on the Complete-sale CTA, so the flow can
   * honestly track "payment chosen" as a state of the sale rather than a click that already fired.
   */
  private choosePay(method: 'ACCOUNT' | 'CASH' | 'CARD' | 'SPLIT'): void {
 // A press on any tile: Cash included, and the un-choose press included: IS the decision the
    // flow's Payment step waits for. It is set before the early returns below for that reason: a
    // clerk who taps Cash on a sale that was already defaulted to Cash has still answered the
    // question, and the step must read done even though nothing about the tender changed.
    this.tenderConfirmed = true;
    if (this.payMethod === method) {
 // Tapping the chosen tile again UNDOES the choice: which now means falling back to cash, not
      // to nothing. "Un-choose" used to leave the ticket untenderable, and a gesture that can
      // disarm the gold button is a gesture that will disarm it by accident on a touch till.
      // Cash is already chosen, so tapping Cash again is simply a no-op.
      if (method === 'CASH') return;
      this.payMethod = 'CASH';
      this.payTileIdx = 1;
      this.showSplit = false;
      return;
    }
    this.payMethod = method;
    this.showSplit = method === 'SPLIT';
  }

  // ── DIB-512 · the payment tiles as ONE keyboard stop ────────────────────────────────────────
  //
  // The critique's keyboard-only run put the Cash tile SIXTY Tab stops from the scan field (out
  // through the chips, the grid, the Feedback pill and the whole app chrome). The group is now a
 // roving-tabindex composite (one Tab stop; arrows move inside: the grid/rail house rule), and
 // the pay key's un-armed walk (hotkeyPay → onFlowGoto('payment')) lands focus ON the group:
  // so from the scan field: F8 · Enter (choose the focused tender) · F8 rings the sale.

  /** Arrow/Home/End navigation inside the tender-tile group (payTileStep is pure, unit-tested). */
  private onPayTilesKey(e: KeyboardEvent): void {
    const next = payTileStep(this.payTileIdx, e.key, 4);
    if (next === null) return;
    e.preventDefault();
    this.payTileIdx = next;
    this.focusPayTile(next);
  }

  /** Focus the tile at index i once the roving tabindex has re-rendered. */
  private focusPayTile(i: number): void {
    void this.updateComplete.then(() =>
      this.renderRoot.querySelectorAll<HTMLElement>('.paytiles button')[i]?.focus(),
    );
  }

  /**
   * The one CTA. Split rings from its own panel (legs must add up first), so the CTA defers to it.
   *
 * WAVE 2 · finding 11: THE SHUT DRAWER IS HANDLED BEFORE THE PRESS, NOT AFTER IT. The button's
   * disabled predicate carried no till term, so a clerk on a register with no session pressed a
   * fully armed "Complete sale · CA$7,661.40" and was answered by a server refusal in a red
 * banner. The state was knowable the whole time: `tillSessionId === ''` is the exact predicate
 * the banner's own repair button tests: so the CTA now names the session in its label and does
   * it here, in that order. The banner and its "Open session now" repair stay exactly as they
   * were: this removes the trap, not the safety net, and a ring that fails for any other reason
   * still lands in the same place.
   */
  private completeSale(): void {
 // rev 5: ringDone() disarms the CTA: the committed basket must never re-ring as a duplicate
    // order on a second "did it take?" tap. The forward path is the sold panel's Next sale.
    if (this.cart.length === 0 || this.ringBusy() || this.ringDone()) return;
    if (this.payMethod === '' || this.payMethod === 'SPLIT') return;
    if (this.tillSessionId === '') {
      void this.openThenRing();
      return;
    }
    void this.ringSale(this.payMethod);
  }

  /**
   * The shut-drawer path, kept out of completeSale() so that method stays the SYNCHRONOUS guard it
 * has always been: every caller (the CTA, the pay key, the quick-action intent) reads the same
   * four refusals in the same order before anything asynchronous can happen.
   *
   * openSession() never throws: it answers with a real session id, or with the 'till-pending' /
   * 'till-offline' chrome placeholders. So the ring always follows, and a drawer that could not
 * actually be opened lands in the ring's own error banner: the same banner, carrying the same
   * "Open session now" repair, that this path exists to stop being the FIRST thing a clerk sees.
   */
  private async openThenRing(): Promise<void> {
    await this.openSession();
    if (this.payMethod === '' || this.payMethod === 'SPLIT') return;
    void this.ringSale(this.payMethod);
  }

 // ── DIB-488 rev 8: the ticket facts: margin glance (item 10) + add-on suggestions (item 8) ──

  /**
   * Debounced (a burst of adds/qty edits is one refresh) and sequence-guarded (a stale answer for
 * an older ticket is dropped when it lands: the grid's searchSeq idiom). Both reads are
   * display-only and BEST-EFFORT: any failure renders as ABSENT data (no margin column, no strip),
   * never as an error surface.
   */
  private scheduleTicketFacts(): void {
    if (this.ticketFactsTimer) window.clearTimeout(this.ticketFactsTimer);
    this.ticketFactsTimer = window.setTimeout(() => {
      this.ticketFactsTimer = 0;
      const seq = ++this.ticketFactsSeq;
      void this.refreshMarginGlance(seq);
      void this.refreshAddons(seq);
      void this.refreshTicketPrices(seq);
    }, 350);
  }

  /**
 * rev 8 (item 10): ask the SERVER for the ticket's margins. The server computes from its own
   * engine revenue × its WAC cost source and GATES AT THE MARSHAL LAYER: a non-allowed role
   * receives `{}`, so this surface simply has no data and renders no margin anywhere. The UI
   * never computes a margin itself.
   */
  private async refreshMarginGlance(seq: number): Promise<void> {
    if (this.cart.length === 0) {
      this.marginData = null;
      return;
    }
    try {
      const out = await marginGlance({
        branch_id: this.branchId,
        customer_id: this.customer?.id ?? '',
        customer_type: this.customer?.customer_type ?? 'RETAIL',
        lines: this.cart.map((l) => ({ product_id: l.product.id, qty_entered: l.qty, uom_entered: l.uom })),
      });
      if (seq !== this.ticketFactsSeq) return; // an older ticket's answer: drop it
      this.marginData = out;
    } catch {
      if (seq === this.ticketFactsSeq) this.marginData = null; // absent, never an error banner
    }
  }

 /** rev 8 (item 8, DIB-502): the suggestion tiles for the current ticket. Empty = no strip. */
  /**
   * The ticket's CUSTOMER-AWARE price picture, one line at a time, from the pricing engine.
   *
   * This is the ask's other half. The search rows and the hot-list tiles show what a product costs
   * this account BEFORE it goes on the ticket; this shows it AFTER, which is where the number is
 * actually read out loud: Ryan Rood wanted the saving visible and printable, and Ryan Bostock
   * reads the whole ticket back before the customer pays.
   *
   * Same read, same authority, same rules: retail struck only where they pay less, no zero saving,
 * no arithmetic here. Best-effort: an outage costs the SAVING line, never the ticket.
   */
  private async refreshTicketPrices(seq: number): Promise<void> {
    if (this.cart.length === 0) {
      this.ticketQuotes = {};
      return;
    }
    try {
      const { prices } = await counterPrices(
        this.branchId,
        this.customer?.id ?? '',
        this.cart.map((l) => ({ product_id: l.product.id, qty_entered: l.qty, uom_entered: l.uom })),
      );
      if (seq !== this.ticketFactsSeq) return;
      const map: Record<string, CounterQuote> = {};
      for (const q of prices) if (q && typeof q.product_id === 'string') map[q.product_id] = q;
      this.ticketQuotes = map;
    } catch {
      if (seq === this.ticketFactsSeq) this.ticketQuotes = {};
    }
  }

  /**
   * What this ticket saves against retail, as the SERVER computed it line by line.
   *
 * Built from per-line saving figures times their quantities, but the multiplication and the
 * addition are money arithmetic: HH's core computes them, so the port hands the per-line entries
 * to a host seam and renders its answer (ports.ts, named in CLOSURE.md). A line the engine could
 * not answer contributes nothing: it does not contribute a guess, and it does not blank the total
 * for the lines that DID answer.
   */
  private ticketSavingCents(): number {
    const entries: { saving_cents: number; qty: number }[] = [];
    for (const l of this.cart) {
      const q = this.ticketQuotes[l.product.id];
      const p = pricePicture(q);
      if (!p.unpriceable && p.showSaving) entries.push({ saving_cents: p.savingCents, qty: l.qty });
    }
    return sumLineSavingCents(entries);
  }

  private async refreshAddons(seq: number): Promise<void> {
    if (this.cart.length === 0) {
      this.addons = [];
      return;
    }
    try {
      const out = await addonSuggestions(this.cart.map((l) => l.product.id));
      if (seq !== this.ticketFactsSeq) return;
      this.addons = out;
      // Seed each new tile's qty box from the server's sensible default; keep any typed entry.
      for (const a of out) {
        if (!this.addonQty.has(a.product_id)) this.addonQty.set(a.product_id, String(a.default_qty));
      }
    } catch {
      if (seq === this.ticketFactsSeq) this.addons = []; // empty-never-error: the strip hides
    }
  }

  /** rev 8: the margin data the ROLE gate actually sent us (footer pair present). */
  private marginPresent(): boolean {
    return this.marginData !== null && this.marginData.margin_bps !== undefined;
  }

 /** rev 8: whether the margin COLUMN/footers render: data present AND the quick-hide is off. */
  private marginVisible(): boolean {
    return this.marginPresent() && !this.wsMarginHidden;
  }

  // ── DIB-488 rev 2: workspace gestures + persistence ───────────────────────

 /** The whole view state as one value: everything below derives through _pos-workspace.ts. */
  private wsView(): WorkspaceView {
    return {
      split: this.wsSplit,
      arrangement: this.wsArrangement,
      lookupCollapsed: this.wsLookupCollapsed,
      saleCollapsed: this.wsSaleCollapsed,
      lookupPresentation: this.wsLookupPresentation,
      marginHidden: this.wsMarginHidden, // rev 8 (0170)
      counterLayout: this.counterLayout, // 0220
      maximized: '',
    };
  }

  private applyWsView(v: WorkspaceView): void {
    this.wsSplit = v.split;
    this.wsArrangement = v.arrangement;
    this.wsLookupCollapsed = v.lookupCollapsed;
    this.wsSaleCollapsed = v.saleCollapsed;
    this.wsLookupPresentation = v.lookupPresentation;
    this.wsMarginHidden = v.marginHidden; // rev 8 (0170)
    this.counterLayout = v.counterLayout; // 0220
  }

  /**
   * Resolve the workspace: the server answers user-over-dealer (whole-or-absent) and the edge
 * hardens the wire (resolveWorkspaceView). A prefs outage keeps the shipped defaults: the
   * empty-never-error posture; a till must never fail to open over a layout preference.
   */
  private async loadWorkspace(): Promise<void> {
    try {
      const row = await getWorkspacePrefs(this.branchId);
      this.applyWsView(resolveWorkspaceView(row));
    } catch {
      /* defaults hold */
    }
  }

  /**
   * The dealer's key roster. A failure is not an error state and does not surface: the shipped
   * fallback roster is already on the legend, and a counter whose keys stopped working because a
   * settings read timed out is a counter nobody trusts again.
   */
  private async loadHotkeys(): Promise<void> {
    try {
      this.hotkeyRoster = asHotkeyRoster(await posHotkeys(this.branchId));
    } catch {
      /* the fallback roster stands */
    }
  }

  /**
   * Which counter panels this person keeps on screen. Failure is not an error state: the shipped
   * defaults are already rendering, and a counter that lost its hot list because a settings read
 * timed out is a counter nobody trusts again: the same posture the roster read takes.
   */
  private async loadPanels(): Promise<void> {
    try {
      const res = await api(
        surfaceConfigUrl(POS_COUNTER_SURFACE_ID, { branchId: this.branchId }),
      );
      if (!res.ok) return;
      const body = (await res.json()) as unknown;
      this.panelRoleItems = asSurfaceItems(body, 'role_items');
      this.panels = panelView(this.panelRoleItems, asSurfaceItems(body, 'user_items'));
    } catch {
      /* the shipped defaults stand */
    }
  }

  /**
 * Turn one panel on or off FOR THIS PERSON. Optimistic: the switch answers the finger before
 * the round trip, because a toggle that waits on a network is a toggle people press twice: and
   * a failed write puts the screen back where it was rather than lying about what is stored.
   */
  private async setPanel(id: string, on: boolean): Promise<void> {
    const before = this.panels;
    const items = panelWrite(this.panels, id, on);
    this.panels = { ...this.panels, [id]: on };
    try {
      const res = await api(`/api/v1/surfaces/${POS_COUNTER_SURFACE_ID}/my-items`, {
        method: 'PUT',
        body: JSON.stringify({ items }),
      });
      if (!res.ok) this.panels = before;
    } catch {
      this.panels = before;
    }
  }

  /**
 * 0220 · setCounterLayout: the other counter, chosen and kept.
   *
   * Applied locally FIRST, like every other workspace gesture: the layout changes under the press
   * rather than after a round trip, and a failed save puts it back. It rides the SAME merge-save the
   * split and the margin quick-hide ride (wsDirty → schedulePersist → mergeWorkspaceForSave), so
   * choosing a layout at this till cannot flatten a preference the same person set at the one next
   * door.
   *
   * It is not a `panel` row on the surface kit, deliberately: the kit's panel rows answer "does this
   * pane render" for the legend and the hot list. This answers "which counter is this", which is the
   * workspace's own question, and the workspace row is the thing that already follows a person
   * across tills.
   */
  private setCounterLayout(next: CounterLayout): void {
    if (this.counterLayout === next) return;
    this.counterLayout = next;
    this.viewOpen = false;
    // Leaving the panels: the operator's attention goes back to a single open step, and the step
 // they were last working in is the honest one to open: not step 1, which would throw away
    // where they were.
    if (next === 'STEPPER' && !this.openStep) this.openStep = 'customer';
    this.wsDirty.add('counterLayout');
    this.schedulePersist();
  }

  /**
   * Dismissing the legend from the legend itself. It turns OFF THE STRIP, never the keys: the
   * roster is untouched, F4 still advances and F12 still pays. Focus lands on the view control
   * that brings it back, so the way back is where the hand already is.
   */
  private dismissLegend(): void {
    void this.setPanel(PANEL_LEGEND, false);
    void this.updateComplete.then(() =>
      this.renderRoot.querySelector<HTMLElement>('[data-testid="pos-view"]')?.focus(),
    );
  }

 /** The facts the legend dims on: the SAME ones the controls themselves disable on. */
  private hotkeyState(): HotkeyState {
    return {
      step: this.openStep,
      lineCount: this.cart.length,
      rungDone: this.ringDone(),
      ringBusy: this.ringBusy(),
      tenderable: this.payMethod !== '' && this.payMethod !== 'SPLIT',
    };
  }

  /**
   * The counter's own key listener.
   *
   * THREE THINGS IT REFUSES TO DO, EACH ONE A REAL BUG IT WOULD OTHERWISE CAUSE:
   *
   * 1. It never handles a key the SHELL already claimed. The app shell owns its own dealer
   * hotkey map on the same window; `defaultPrevented` is how the two maps stay one map from
   * the operator's point of view instead of both firing.
   * 2. It never matches anything but an exact chord. That IS the browser escape: bind F12 to
   * payment and Shift+F12 still opens dev tools, because `shift-f12` is not `f12` and an
   * unmatched key never gets preventDefault called on it.
   * 3. It never steals an ordinary keystroke. Only function keys produce a chord at all, so
 * every letter, digit and punctuation mark still reaches the scan field: which is where a
   * counter expects everything it types to go.
   */
  private onPosKey = (e: KeyboardEvent): void => {
    if (e.defaultPrevented) return;
    const action = resolveHotkey(chordFromEvent(e), this.hotkeyRoster);
    if (!action) return;
    // THE COUNTER OWNS A KEY ITS OWN LEGEND ADVERTISES, available or not. The two halves of that
    // sentence are two different calls, and conflating them is a real defect this fixed:
    //
 // stopPropagation: ALWAYS, for a chord on this roster. The shell's dealer map is on the
    // same window and binds F1-F4 / F9 (0164/0171). Before this, a DIMMED counter key fell
 // through to it: pressing F4 on the payment step: where "Next" is legitimately unavailable:
    // navigated the clerk to the product catalogue mid-sale. A key the legend shows must never
    // do a third thing nobody asked for.
    //
 // preventDefault: only when the action actually RAN. The browser's own meaning is not ours
    // to suppress on a key that did nothing: bind F12 to payment and, on a rung sale where Pay is
    // dimmed, F12 still opens dev tools. That is the escape rule, and it is the reason the client
    // could have his F12 at all.
    e.stopPropagation();
    if (!this.runHotkey(action)) return;
    e.preventDefault();
  };

  /**
   * Carry out a bound action. Returns whether it DID anything: an action that is not available
   * right now must not swallow the key, because a dimmed keycap and a key that silently does
   * nothing while the browser's meaning is also suppressed are two different kinds of broken.
   */
  private runHotkey(action: string): boolean {
    const s = this.hotkeyState();
    switch (action) {
      case HK_STEP_NEXT:
      case HK_STEP_BACK: {
        const next = stepAfter(this.openStep, action === HK_STEP_NEXT ? 1 : -1);
        if (!next) return false;
        this.openAccordion(next);
        return true;
      }
      case HK_PAY:
        if (s.rungDone || s.ringBusy) return false;
        this.hotkeyPay();
        return true;
      case HK_PARK:
        if (s.lineCount === 0 || s.rungDone || s.ringBusy) return false;
        void this.parkCurrent();
        return true;
      case HK_SEARCH:
        if (s.rungDone) return false;
        this.openAccordion('items');
        return true;
      default:
        return false;
    }
  }

 /** Debounced save: a drag emits dozens of positions; only the settled one is worth a write. */
  private schedulePersist(): void {
    if (this.wsPersistTimer) window.clearTimeout(this.wsPersistTimer);
    this.wsPersistTimer = window.setTimeout(() => {
      this.wsPersistTimer = 0;
      void this.persistWorkspace();
    }, 400);
  }

  /**
   * PER-USER persistence (the ergonomics half of the 0166 ruling). One idempotency key is minted
 * per save (the settings-write idiom: the server replays a duplicated request instead of
   * double-applying it); workspacePayload deliberately excludes the transient maximize. Failure is
   * silent by design: the screen already shows what the operator chose, and the next gesture
   * saves again.
   *
   * rev 6 (item G, the commander-approved opinion #2 fix): before each debounced save the stored
   * row is RE-READ and only the fields THIS surface's gestures changed are overlaid on it
 * (mergeWorkspaceForSave): two live surfaces stop flattening each other's divider/pills. The
   * wire contract is untouched: one complete row still goes over the PUT (whole-or-absent), and
   * the server's whole-or-absent ruling stands. A failed re-read degrades to the pre-rev-6
 * behavior (the whole local view: best-effort either way).
   */
  private async persistWorkspace(): Promise<void> {
    const dirty = [...this.wsDirty];
    const sent = this.wsView(); // what the local fields were when this save composed
    let body: WorkspaceView;
    try {
      const stored = await getWorkspacePrefs(this.branchId);
      body = mergeWorkspaceForSave(stored, sent, dirty);
    } catch {
      body = { ...sent, maximized: '' }; // re-read failed: the whole local view, as before
    }
    try {
      // rev 3→8: workspaceSaveBodyV8 = the rev-3 body + margin_hidden (0170), one whole-or-absent
      // row either way; maximize stays excluded by construction; the earlier composers stand.
      await putWorkspacePrefs({ idempotency_key: newIdempotencyKey(), ...workspaceSaveBodyV9(body) });
 // A field is clean once saved: unless a gesture moved it again while this save was in
      // flight (its queued save must still carry it).
      const now = this.wsView();
      for (const f of dirty) {
        if (now[f] === sent[f]) this.wsDirty.delete(f);
      }
    } catch {
 /* best-effort: the fields stay dirty and the next gesture saves again */
    }
  }

  /**
   * rev 3: the lookup panel's cards ⇄ rows toggle (the quick pill in its bar). An ergonomic
   * preference exactly like the divider position, so it persists per user through the same
   * debounced whole-or-absent save; choosing the already-active shape is a no-op, not a write.
   */
  private wsSetLookupPresentation(p: LookupPresentation): void {
    if (this.wsLookupPresentation === p) return;
 // rev 5: keep the reader's place: the flip re-renders every result at a different height and
    // used to snap the lookup scroll back to 0 mid-browse. Exact restoration is impossible (row
    // heights differ) so the offset is clamped by the browser; better than losing the place.
    const body = this.renderRoot.querySelector<HTMLElement>('[data-testid="step-panel-items"]');
    const keep = body?.scrollTop ?? 0;
    this.wsLookupPresentation = p;
    this.wsDirty.add('lookupPresentation'); // rev 6 (G)
    this.schedulePersist();
    if (body && keep > 0) void this.updateComplete.then(() => (body.scrollTop = keep));
  }

  /**
   * rev 8 (item 10): the one-click quick-hide (eye): counter privacy while a customer watches
   * the screen. An ergonomic exactly like the divider position: per-user, debounced merge-save.
   */
  private wsToggleMarginHidden(): void {
    this.wsMarginHidden = !this.wsMarginHidden;
    this.wsDirty.add('marginHidden');
    this.schedulePersist();
  }

  /**
   * The eye's words in each state, one source for the hover title AND the accessible name
   * (ui3 visual NOTE 1: the name stayed "Hide margin and est. profit" while pressed, so the
   * name asked for an act the state had already performed, and name and title disagreed).
   */
  private marginEyeWords(): string {
    return this.wsMarginHidden
      ? 'Margin and est. profit are hidden: press to show them (counter privacy)'
      : 'Hide margin and est. profit (counter privacy)';
  }

  /**
 * rev 5: light dismiss for the Help popover: a pointer press anywhere OUTSIDE its wrap closes
   * it (composedPath, so presses inside other shadow roots count as outside honestly). The pressed
 * control keeps focus: only Escape refocuses the trigger.
   *
   * The panel-config popovers this listener also served retired with the two-panel workspace; the
   * Help flyout is the last light-dismissed surface POS owns.
   */
  private onDocPointerDown = (e: Event): void => {
    // Both flyouts in the utility cluster light-dismiss on the same listener; each one closes only
    // when the click landed outside ITS OWN wrapper, so opening one closes the other.
    const path = e.composedPath();
    const wraps = [...this.renderRoot.querySelectorAll('.helpwrap')];
    if (this.helpOpen) {
      const own = wraps.find((w) => w.querySelector('[data-testid="pos-help"]'));
      if (!(own && path.includes(own))) this.helpOpen = false;
    }
    if (this.viewOpen) {
      const own = wraps.find((w) => w.querySelector('[data-testid="pos-view"]'));
      if (!(own && path.includes(own))) this.viewOpen = false;
    }
  };

  private onReliefApplied(e: CustomEvent<{ outcome: ReliefOutcome; signatureRef: string }>): void {
    this.reliefReason = e.detail.outcome.reason_code;
    this.reliefRateBps = e.detail.outcome.rate_bps;
    if (e.detail.signatureRef) this.signatureRef = e.detail.signatureRef;
    this.showRelief = false;
  }

  // ── render ────────────────────────────────────────────────────────────
  render() {
    const sessionOpen = this.tillSessionId !== '';
 // rev 8 (item 2): the concept HEADER BAR: one compact page bar. Large bold sale title with
 // the yard context under it (left), the tracked 1→2→3 flow (middle: the rev-1 contract,
    // untouched), and the utility cluster (right): live clock (label pure, clock injected),
 // green session-status dot when the till is OPEN (pairs with the rev-5 rehydration: a reload
    // rediscovers the drawer and the dot survives it), Help, the session control, and the
    // page-level gold "+ New Sale" (the same guarded newSaleClicked path as the util button).
    // The brand lockup stays as the eyebrow line (the DIB-456 brand gate pins it).
    return html`
      <div class="topbar">
        ${/* WAVE 2 · finding 15: the bar is full-bleed (its hairline divides the whole page) and
           its CONTENTS ride the work column's measure. See the .topbar/.topbarin rules. */ ''}
        <div class="topbarin">
        <div class="headid">
          <span class="title">HH <span>Yard</span> · Point of Sale</span>
          <h1 class="pagetitle" data-testid="pos-page-title">${this.ringDone() ? 'Counter Sale' : 'New Counter Sale'}</h1>
          ${/* WAVE 2 · finding 16: THE SHELL OWNS IDENTITY, SO THIS LINE DOES NOT REPEAT IT.
             The operator's display name used to close this line, and the shell prints the same
             string: "UAT Tier-3 (Inside Sales, Trenton)": in its top-right at y=16, on screen at
             the same moment. Who is signed in is the shell's fact and it is already answered above
             this bar; what the SALE needs to know is which drawer it is ringing into, which is what
             is left. (The eyebrow above still repeats the shell's brand lockup and the nav tab, and
             that is not fixable from here: two committed gates pin it verbatim: see the report.) */ ''}
          <div class="pagectx" data-testid="pagectx">${branchName(this.branchId)} Yard · Register 01</div>
        </div>
        ${/* DIB-488: the tracked flow: 1 Items → 2 Customer → 3 Payment. Derived from the sale
           data every render (deriveFlow); clicking a step scrolls/focuses its panel and locks
           NOTHING: every panel below stays editable at all times. */ ''}
        <hos-pos-flow-steps
          .steps=${this.flowSteps()}
          @pos-flow-goto=${(e: CustomEvent<{ step: FlowStepId }>) => this.onFlowGoto(e.detail.step)}
        ></hos-pos-flow-steps>
        <div class="session">
          <span class="clock" data-testid="pos-clock" title="Local time">${clockLabel(this.clockNow)}</span>
          ${sessionOpen
            ? html`<span class="sessiondot" data-testid="session-dot" title="Till session open: elapsed ${this.elapsed}">
                  <span class="dot" aria-hidden="true"></span> Open
                </span>
                <span class="helpwrap">
                  <button
                    class="topbtn"
                    data-testid="end-session"
                    aria-disabled="true"
                    aria-describedby="end-session-reason"
                    title=${this.endSessionClosedReason}
                    @click=${() => this.setUtil(this.endSessionClosedReason, 'note', true, 'session')}
                    @focus=${() => this.setUtil(this.endSessionClosedReason, 'note', false, 'session')}
                  >End Session</button>
                </span>
                ${/* UI-3 (visual GAP 1): the reason renders beside its control, anchored under the
                     session region the button lives in, in the note palette (never the success
                     green), so a press answers where the hand is: the foot line sat at the flow's
                     end, half under the hotkey bar. The status region arrives only on a press
                     (announced once, polite); focus shows it silently, per the recorded ruling. */ ''}
                ${/* ui3d P2-1: the card hangs off the SESSION region, not the button's own wrap: a
                     wrap anchor right-aligns a 320px card to the button's right edge, and on a
                     wrapped bar that edge sits a few hundred pixels from the window's left, so the
                     card ran off screen below about 1415px wide. The session region is at least a
                     card wide plus the bar's padding at every width the till supports, so its band
                     keeps both edges of the card on screen. */ ''}
                ${this.utilMsg !== '' && this.utilWhere === 'session'
                  ? html`<span
                      data-testid="end-session-notice"
                      class="esnotice"
                      role="${ifDefined(this.utilLive ? 'status' : undefined)}"
                      >${this.utilMsg}</span
                    >`
                  : nothing}
                <span id="end-session-reason" class="srdesc" data-testid="end-session-reason">${this.endSessionClosedReason}</span>`
            : html`<button class="topbtn" @click=${this.openSession}>Open Session</button>`}
          ${/* COUNTER VIEW (R1/R2, the user half). The way back from a dismissed legend
               and the on/off for the hot list, in one place a counter person can reach without an
               administrator. An icon, not a word: the header's chrome budget is already spent. */ ''}
          <span class="helpwrap" @keydown=${(e: KeyboardEvent) => this.onViewKey(e)}>
            <button
              class="topbtn"
              data-testid="pos-view"
              aria-haspopup="true"
              aria-expanded=${this.viewOpen ? 'true' : 'false'}
              aria-label="Counter view"
              title="Show or hide the key legend and the hot list"
              @click=${() => (this.viewOpen = !this.viewOpen)}
            >
              <svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                <path d="M4 6h16M4 12h16M4 18h16"></path>
                <circle cx="9" cy="6" r="2" fill="currentColor" stroke="none"></circle>
                <circle cx="15" cy="12" r="2" fill="currentColor" stroke="none"></circle>
                <circle cx="8" cy="18" r="2" fill="currentColor" stroke="none"></circle>
              </svg>
            </button>
            ${this.viewOpen ? this.renderViewMenu() : nothing}
          </span>
          <span class="helpwrap" @keydown=${(e: KeyboardEvent) => this.onHelpKey(e)}>
            <button
              class="topbtn help"
              data-testid="pos-help"
              aria-haspopup="true"
              aria-expanded=${this.helpOpen ? 'true' : 'false'}
              title="How the counter works"
              aria-label="Help"
              @click=${() => (this.helpOpen = !this.helpOpen)}
            >
              ${drawIcon(iconForId('pencil:help'), 29)}
            </button>
            ${this.helpOpen ? this.renderHelp() : nothing}
          </span>
          <button
            class="newsale"
            data-testid="pos-new-sale"
            title="Starts a fresh sale: asks to confirm when the current order isn't empty."
            @click=${this.newSaleClicked}
          >
            ${drawIcon(iconForId('pencil:add'), 14)} New Sale
          </button>
        </div>
        </div>
      </div>

      ${this.renderFlowColumn()}
      ${this.renderOverlays()}
      ${this.counterSettingsOpen ? this.renderCounterSettings() : nothing}
      ${/* THE PINNED LEGEND (N1). Always there, always the same keys in the same order, with the
         unavailable ones dimmed: a legend whose contents rearrange is a legend nobody can build
         muscle memory against, which is the one thing it was asked for. */ ''}
      ${this.panels[PANEL_LEGEND] === false
        ? nothing
        : html`<hos-pos-hotkey-legend
            .roster=${this.hotkeyRoster}
            .state=${this.hotkeyState()}
            @pos-legend-dismiss=${() => this.dismissLegend()}
          ></hos-pos-hotkey-legend>`}
    `;
  }

  /**
   * The counter-view menu: two switches, no sentences.
   *
   * THIS IS THE WAY BACK, and it deliberately does not need an administrator. Ryan Bostock does not
   * want the legend; Ryan Rood does. Whoever is standing at this till decides, here, in one gesture,
   * and the choice follows them to the next till because it is stored against them and not against
   * the machine.
   */
  private renderViewMenu() {
    return html`<div class="helppop viewpop" role="group" aria-label="Counter view" data-testid="pos-view-menu">
      <h3>Counter view</h3>
      ${/* 0220 · the layout choice, above the two panel switches because it is the bigger of the
           three and because the switches below it describe chrome INSIDE whichever one is chosen.
           A radio group, not a switch: "Steps" and "Panels" are two answers to one question, and a
           toggle labelled "Panels" would leave the standard counter with no name at all. */ ''}
      <div class="layoutpick" role="radiogroup" aria-label="Counter layout" data-testid="pos-layout-pick">
        ${/* Written out rather than mapped over a pair, so both test ids are literal strings a
             person can grep for: the house convention leans on that, and a computed id is a
             control no source scan can find. Two options is not a list. */ ''}
        <button
          class="sw"
          type="button"
          role="radio"
          data-testid="pos-layout-stepper"
          aria-checked=${this.counterLayout === 'STEPPER' ? 'true' : 'false'}
          @click=${() => this.setCounterLayout('STEPPER')}
        >
          Steps<span class="tick" aria-hidden="true">${drawIcon(iconForId('pencil:tick-m'), 14)}</span>
        </button>
        <button
          class="sw"
          type="button"
          role="radio"
          data-testid="pos-layout-panels"
          aria-checked=${this.counterLayout === 'PANELS' ? 'true' : 'false'}
          @click=${() => this.setCounterLayout('PANELS')}
        >
          Panels<span class="tick" aria-hidden="true">${drawIcon(iconForId('pencil:tick-m'), 14)}</span>
        </button>
      </div>
      ${COUNTER_PANELS.map((id) => {
        const on = this.panels[id] !== false;
        return html`<button
          class="sw"
          type="button"
          data-testid=${`pos-view-${id}`}
          aria-pressed=${on ? 'true' : 'false'}
          @click=${() => void this.setPanel(id, !on)}
        >
          ${PANEL_LABELS[id]}<span class="pip" aria-hidden="true"></span>
        </button>`;
      })}
      ${isAdmin()
        ? html`<button
            class="sw"
            type="button"
            data-testid="pos-counter-settings-open"
            aria-haspopup="dialog"
            @click=${() => {
              this.viewOpen = false;
              this.counterSettingsOpen = true;
            }}
          >
            Counter settings<span aria-hidden="true">${drawIcon(iconForId('pencil:right'), 14)}</span>
          </button>`
        : nothing}
    </div>`;
  }

  /**
   * The admin editor (R1's second half, R2's first). Rendered only for an administrator, and the
 * SERVER refuses the write regardless: this gate spares a counter person a control they cannot
   * use, it is not what makes the write safe.
   */
  private renderCounterSettings() {
    return html`<div
      class="csoverlay"
      @click=${(e: Event) => {
        if ((e.target as HTMLElement).classList?.contains('csoverlay')) this.closeCounterSettings();
      }}
      @keydown=${(e: KeyboardEvent) => {
        if (e.key === 'Escape') {
          e.stopPropagation();
          this.closeCounterSettings();
        }
      }}
    >
      <div class="cspanel" role="dialog" aria-modal="true" aria-label="Counter settings" tabindex="-1" data-testid="pos-counter-settings-host">
        <div class="cshead">
          <h2>Counter settings</h2>
          <button class="topbtn" data-testid="pos-counter-settings-close" @click=${() => this.closeCounterSettings()}>
            Close
          </button>
        </div>
        <hos-pos-counter-settings branch-id=${this.branchId}></hos-pos-counter-settings>
      </div>
    </div>`;
  }

  /** Closing re-reads the roster and the panels, so a save shows on the counter without a reload. */
  private closeCounterSettings(): void {
    this.counterSettingsOpen = false;
    void this.loadHotkeys();
    void this.loadPanels();
    void this.updateComplete.then(() =>
      this.renderRoot.querySelector<HTMLElement>('[data-testid="pos-view"]')?.focus(),
    );
  }

 /** Escape closes the view popover from the trigger or inside it: the Help flyout's contract. */
  private onViewKey(e: KeyboardEvent): void {
    if (e.key === 'Escape' && this.viewOpen) {
      e.stopPropagation();
      this.viewOpen = false;
      this.renderRoot.querySelector<HTMLElement>('[data-testid="pos-view"]')?.focus();
    }
  }

  /** rev 8 (item 2): Escape closes the Help popover from the trigger or inside it. */
  private onHelpKey(e: KeyboardEvent): void {
    if (e.key === 'Escape' && this.helpOpen) {
      e.stopPropagation();
      this.helpOpen = false;
      this.renderRoot.querySelector<HTMLElement>('[data-testid="pos-help"]')?.focus();
    }
  }

 /** rev 8 (item 2): the Help popover: counter basics in counter words; chrome, not a manual. */
  private renderHelp() {
    return html`<div class="helppop" role="group" aria-label="Counter help">
      <h3>Counter basics</h3>
      <ul>
        <li><b>Scan or search</b>: shoot any barcode into the one box, or type a name / SKU and pick from the results. Enter on an exact SKU adds it straight to the ticket.</li>
        <li><b>Customer</b>: Walk-in rings without an account; Account opens the picker (the + button there adds a new account).</li>
        <li><b>Complete sale</b>: pick the payment method, then the gold button rings it: order, invoice and payment post together.</li>
        <li><b>Park ticket</b> keeps an order for later; <b>New Sale</b> discards it (and asks first).</li>
      </ul>
    </div>`;
  }

  // ── the counter's single column ───────────────────────────────────────────────────────────────
  //
  // WHAT REPLACED WHAT. The two-panel workspace (Inventory lookup ⇄ Sale, with the draggable
  // divider, the collapse rails, the swap/maximize controls and the per-panel config popovers) is
  // GONE FROM POS. The client used it for a day and asked for one column: a person standing at a
  // till with a customer in front of them is doing one thing, and four half-visible panels made
  // them hunt for it. What stands here instead is four accordion cards in the order the sale
 // actually happens: Customer, Items, Payment, Summary: with exactly one open.
  //
  // WHAT WAS RETIRED IS THE USE, NOT THE MACHINERY. _pos-workspace.ts is shared: the quote builder
  // and the settings kit still resolve layouts through it, so the module, its tests and the
  // pos_user_workspace wire contract are all untouched. POS simply stops offering the geometry
  // controls. The two per-user ergonomics that still MEAN something in one column ride on exactly
 // as before through the same merge-save: the results Cards ⇄ Rows flip, and the margin
 // quick-hide: so nobody's saved preferences were thrown away to get here.
  //
  // THE STEPPER AND THE ACCORDION SAY DIFFERENT THINGS ON PURPOSE. deriveFlow reports what the
  // SALE still needs; openStep reports where the OPERATOR is working. They usually agree and are
  // allowed not to: a clerk who opens Payment to check a tender has not answered the customer
  // question, and neither indicator should pretend otherwise.

  /**
 * renderFlowColumn: the four cards, top to bottom, one of them open.
   *
   * The body of a closed card is not rendered AT ALL (the `${open ? … : nothing}` below), which is
   * what makes the disclosure real rather than cosmetic: nothing inside a collapsed step is
 * painted, tab-reachable, announced, or: for the Items card: still holding a live product grid
   * open. hos-pos-step-card carries the aria-expanded/aria-controls pair against a region that
   * genuinely comes and goes.
   */
  private renderFlowColumn() {
    if (this.counterLayout === 'PANELS') return this.renderFlowPanels();
    const steps = this.flowSteps();
    const facts = this.stepFacts();
    return html`<div class="flowcol" data-testid="pos-flow-column">
      ${steps.map((s) => {
        const open = this.openStep === s.id;
        return html`<hos-pos-step-card
          data-step-card=${s.id}
          .step=${s.id}
          .n=${s.n}
          .label=${s.label}
          .complete=${s.complete}
          .open=${open}
          .summary=${this.stepSummary(s.id, facts)}
          @pos-step-toggle=${() => this.toggleStep(s.id)}
        >
          ${open ? this.renderStepBody(s.id) : nothing}
        </hos-pos-step-card>`;
      })}
      ${this.renderSaleUtilities()}
      ${/* The footer stat bar (count · weight · the role-gated figures) belongs to the SALE, not to
         any one step, so it sits under the column and stays legible from all four. */ ''}
      ${this.renderTxBar()}
    </div>`;
  }

  /**
 * renderFlowPanels: 0220: the same four steps, all open at once, in boxes the person arranged.
   *
   * THE BODIES COME FROM THE SAME SWITCH the accordion renders through (renderStepBody), which is
   * the rule that file states and the reason this is fifteen lines rather than a second counter: a
   * step rendered by two paths is a step that drifts, and the one that drifts is always the one
   * nobody is looking at.
   *
   * NO SUMMARY LINES HERE. A collapsed card carries a summary because its body is gone; an open
   * panel carries its body, and repeating the total in the header of the panel showing the total is
   * the say-it-once rule broken in the smallest possible space.
   *
   * The utilities and the footer stat bar stay BELOW the panel surface rather than inside a panel:
   * they belong to the sale, not to any one step, which is exactly why they sit under the column in
   * the other layout too.
   */
  private renderFlowPanels() {
    const steps = this.flowSteps();
    return html`<div class="flowcol" data-testid="pos-flow-panels">
      <hos-pos-panels
        .steps=${steps}
        .active=${this.openStep}
        @pos-panel-focus=${(e: CustomEvent<{ step: FlowStepId }>) => (this.openStep = e.detail.step)}
      >
        ${steps.map((s) => html`<div slot=${s.id}>${this.renderStepBody(s.id)}</div>`)}
      </hos-pos-panels>
      ${this.renderSaleUtilities()}
      ${this.renderTxBar()}
    </div>`;
  }

  /**
 * stepSummary: summaryFor's line, with the separator this terminal's own withholding orphaned.
   *
   * summaryFor builds the Items line as `${count} ${items} · ${totalLabel}` and is right for every
   * input it was designed to take. The input it was never given is the one stepFacts() invents:
   * while Payment is open the terminal blanks `totalLabel` on purpose (the say-it-once rule
 * argued at stepFacts), and the template dutifully rendered the middot with nothing after it:
 * "✓ Items: done 1 item ·", a dangling separator in the header button's accessible name.
   *
   * The trim lives HERE, at the feed point, because the withholding lives here: the pure module is
   * not the thing that decided to hand it half a line. (The better long-term shape is for
   * summaryFor to JOIN its parts rather than template them, which would make the empty half
   * disappear at the source. That file is outside this lane's fences, so it is reported rather
 * than edited, and this trim is correct either way: a joined line has no trailing separator to
   * find.)
   */
  private stepSummary(step: FlowStepId, f: StepSummaryFacts): string {
    return summaryFor(step, f).replace(/\s*·\s*$/, '');
  }

  /** The open card's contents. One switch so a step can never be rendered by two different paths. */
  private renderStepBody(step: FlowStepId) {
    switch (step) {
      case 'customer':
        return this.renderCustomerBody();
      case 'items':
        return this.renderItemsBody();
      case 'payment':
        return this.renderPaymentBody();
      case 'summary':
        return this.renderSummaryBody();
    }
  }

  /**
   * The facts the COLLAPSED cards summarise (summaryFor, _pos-accordion.ts: pure and tested).
   *
   * `totalLabel` is withheld while Payment is open pre-ring: the totals card inside that step is
   * the running total's SOURCE, and the doctrine's say-it-once rule means the Items summary must
   * not echo a number the operator is already looking at. Closed Payment, and the echo comes back:
   * which is the same source-and-echo rule the old header chip followed, now with a better echo.
   *
   * The echo is an ESTIMATE before the ring (the browser's own arithmetic; the ring is the binding
   * price), so it carries the same mark the totals rows and both ring buttons carry. After the ring
   * the summary stops estimating entirely: it reads the sale's committed total, the server's own
   * figure, with no mark, so the estimate cannot stand on screen beside the confirmation's
   * committed figure (and they would disagree at any rate other than the preview's).
   */
  private stepFacts(): StepSummaryFacts {
    return {
      customerName: this.customer ? this.customer.name : '',
      walkInConfirmed: this.walkInConfirmed,
      lineCount: this.cart.length,
      totalLabel: this.ringDone()
        ? money(this.doneSale().total_cents)
        : this.openStep === 'payment'
          ? ''
          : `${this.moneyOrMarker(this.total())}${this.hasPricedLine() ? ' (est.)' : ''}`,
      // DIB-512 (8) · one word per surface. Before the ring the collapsed Payment card says which
      // tender is CHOSEN; after it, salePillLabel decides the counter word for what the sale
 // actually is ('Paid' / 'On account' / 'COD' / 'Deposit'): never the wire status, which is
      // the whole point of that helper and the reason COMMITTED never reaches chrome.
      tenderLabel: this.ringDone() ? salePillLabel(this.doneSale()) : this.payMethod ? tenderLabel(this.payMethod) : '',
      orderNo: this.ringDone() ? this.doneSale().order_no : '',
      rungDone: this.ringDone(),
    };
  }

  /**
 * STEP 1 · CUSTOMER: who is on the sale, and nothing else.
   *
   * Fulfilment used to live here and has moved to the end of Items (the client's ask, and the right
   * adjacency: how the material leaves is a fact about the GOODS, decided once they are on the
   * ticket). What is left is the question this step exists to answer plus the quiet order facts.
   */
  private renderCustomerBody() {
    return html`

            ${/* The walk-in vs account distinction, resolved EXPLICITLY: walk-in is the terminal's
               default posture, but a default is not a decision: the flow's step 2 completes when
               someone answers (either tile). Nothing is gated on it. */ ''}
            ${/* rev 5: aria-pressed on every tile group: the selected state was class-only. */ ''}
            <div class="custmode" data-testid="pos-customer-mode" role="group" aria-label="Customer type">
              <button
                class="${this.walkInConfirmed && !this.customer ? 'active' : ''}"
                aria-pressed=${this.walkInConfirmed && !this.customer ? 'true' : 'false'}
                title="Cash / walk-in customer: no account on the sale."
                @click=${() => void this.chooseWalkIn()}
              >
                Walk-in
              </button>
              <button
                class="${this.customer ? 'active' : ''}"
                aria-pressed=${this.customer ? 'true' : 'false'}
                title="Charge-account customer: opens the account picker."
                @click=${() => this.openPicker()}
              >
                Account
              </button>
            </div>

            ${this.renderCustomerIdentity()}

            

${/* DIB-488: the quieter order facts fold away (native details: no state to desync).
   rev 6 (item D): PO / Job # and salesperson now EXIST on the POS sale (0168) and
   live here per the mock: optional and unobtrusive; the salesperson defaults to
   the signed-in operator. */ ''}
            ${/* DIB-512 · label diet: everything inside a "More options" disclosure is optional
               BY CONSTRUCTION, so three "(optional)" suffixes said nothing; the deposit label's
               nine-word policy clause became the control's own disabled state + flyout. */ ''}
            <details class="more">
              <summary
                ><span class="disc" aria-hidden="true"
                  ><span class="closed">${drawIcon(iconForId('pencil:right'), 12)}</span
                  ><span class="open">${drawIcon(iconForId('pencil:bottom'), 12)}</span>
                </span>
                More options</summary
              >
              <label>PO / Job #</label>
              <input
                data-testid="po-job-ref"
                maxlength="64"
                autocomplete="off"
                placeholder="e.g. PO 4471 or SMITH DECK"
                .value=${this.poJobRef}
                title="The customer's purchase-order or job reference: printed with the order."
                @change=${(e: Event) => (this.poJobRef = (e.target as HTMLInputElement).value)}
              />

              <label>Salesperson</label>
              <select
                data-testid="salesperson-select"
                .value=${this.salespersonId}
                title="Who gets credit for this sale: defaults to you."
                @change=${(e: Event) => (this.salespersonId = (e.target as HTMLSelectElement).value)}
              >
                ${this.renderSalespersonOptions()}
              </select>

              ${/* rev 8 (item 11): the JOB fields: 0170, the exact 0168 persistence pattern
                 (validated lengths, 422-in-words, replay-verbatim, order + audit Extra, sold
                 panel). DIB-512: while Delivery is active the address surfaces INLINE beside
                 the fulfilment tiles (one fact, one control): this row stands down there. */ ''}
              ${this.deliveryActive()
                ? nothing
                : html`<label>Job address</label>
                    <input
                      data-testid="job-address"
                      maxlength="200"
                      autocomplete="off"
                      placeholder="e.g. 128 Front St, Trenton"
                      .value=${this.jobAddress}
                      title="Where the material is going: printed with the order."
                      @change=${(e: Event) => (this.jobAddress = (e.target as HTMLInputElement).value)}
                    />`}

              <label>Job reference</label>
              <input
                data-testid="job-reference"
                maxlength="64"
                autocomplete="off"
                placeholder="e.g. SMITH-DECK-2026"
                .value=${this.jobReference}
                title="Your own job / project reference: printed with the order."
                @change=${(e: Event) => (this.jobReference = (e.target as HTMLInputElement).value)}
              />

              <label>Deposit (CA$)</label>
              <div
                class="deposit"
                title=${this.depositAllowed()
                  ? 'Deposit held against the order: Charge Account and COD sales.'
                  : 'Deposits ride Charge Account or COD sales: this sale is being paid in full now.'}
              >
                <input
                  type="number"
                  min="0"
                  step="0.01"
                  placeholder="0.00"
                  ?disabled=${!this.depositAllowed()}
                  .value=${this.depositDollars}
                  @change=${(e: Event) => (this.depositDollars = (e.target as HTMLInputElement).value)}
                />
                <select
                  ?disabled=${!this.depositAllowed()}
                  .value=${this.depositMethod}
                  @change=${(e: Event) => (this.depositMethod = (e.target as HTMLSelectElement).value)}
                >
                  <option value="CASH">Cash</option>
                  <option value="CARD">Card</option>
                  <option value="CHEQUE">Cheque</option>
                </select>
              </div>

              <label>Notes</label>
              <textarea
                rows="2"
                .value=${this.notes}
                @change=${(e: Event) => (this.notes = (e.target as HTMLTextAreaElement).value)}
              ></textarea>
            </details>
          </section>

          `;
  }

  /**
 * STEP 2 · ITEMS: the whole add-to-ticket surface in one card.
   *
   * THE SEARCH IS NOT A SEPARATE PANEL ANY MORE. One scan-or-search field feeds the ticket
   * directly: scanner bursts and exact SKUs commit on Enter exactly as they always did, anything
   * else is forwarded live to the grid's search, and the chips and results render inline beneath
 * it. Every one of those paths: the scan resolve, a result row, a browse tile, a quick-pick
 * tile, the add-ons strip: goes through the SAME addProduct(), so the one-product-one-line merge
   * rule it inherited keeps working no matter which gesture added the item.
   *
   * The browse grid is auto-collapsing here: with no search term the space belongs to the
   * quick-picks (the tiles a yard taps all day), and "Browse products" opens the full catalogue
   * when someone actually wants it. Nothing became unreachable; the frequent thing simply got the
   * pixels.
   */
  private renderItemsBody() {
    return html`
      ${/* ONE field, both muscles: a scanner burst resolves and adds; typing searches. Same box,
         same resolve, same add path, same post-add refocus contract as the shipped scan rail. */ ''}
      <hos-pos-scan-box
        .branchId=${this.branchId}
        ?autofocus-scan=${true}
        ?unified=${true}
        @pos-term-changed=${(e: CustomEvent<{ term: string }>) => (this.lookupTerm = e.detail.term)}
        @pos-term-commit-top=${() => this.commitTopHit()}
        @pos-term-enter-list=${() => this.enterResultList()}
        @pos-scan-resolved=${(e: CustomEvent<{ product: PosProduct }>) => this.addProduct(e.detail.product)}
      ></hos-pos-scan-box>

      ${/* THE RESULTS, as about five rows under the field. This replaced the grid-first lookup:
         the counter types three letters of a name it already knows and takes the first answer,
         so the default surface is a glance and browsing moved behind its own disclosure. Enter
         in the field commits the top row (commitTopHit); a SCAN never reaches this list at all. */ ''}
      <hos-pos-search-drop
        data-testid="pos-search-drop-host"
        .branchId=${this.branchId}
        .customerId=${this.customer?.id ?? ''}
        .term=${this.lookupTerm}
        .ticketEpoch=${this.ticketEpoch}
        @pos-drop-exit=${() => this.focusScanBox()}
        @pos-add-product=${(e: CustomEvent<{ product: PosProduct }>) => this.addProduct(e.detail.product)}
        @pos-add-echo=${(e: CustomEvent<{ id: string }>) => this.onAddEcho(e.detail.id)}
      ></hos-pos-search-drop>

      ${/* THE HOT LIST: the yard's high-frequency path, drilled by picture: category →
         subcategory → item. Stands down entirely while a search term is up, because two
         competing ways to pick a product is the "two add paths at once" finding again. */ ''}
      ${this.lookupTerm.trim().length >= 2 || this.panels[PANEL_HOTLIST] === false
        ? nothing
        : html`<hos-pos-hot-list
              .branchId=${this.branchId}
              .customerId=${this.customer?.id ?? ''}
              .ticketEpoch=${this.ticketEpoch}
              @pos-add-product=${(e: CustomEvent<{ product: PosProduct }>) => this.addProduct(e.detail.product)}
              @pos-add-echo=${(e: CustomEvent<{ id: string }>) => this.onAddEcho(e.detail.id)}
            ></hos-pos-hot-list>`}

      ${/* BROWSING IS THE EXCEPTION PATH. The full catalogue grid keeps every capability it had:
         chips, favourites, categories, both presentations: one quiet disclosure away. The
         results Cards ⇄ Rows flip lives IN here now, beside the grid it actually governs (it
         never governed the dropdown), and is still the persisted per-user ergonomic it was. */ ''}
      <details class="browse" data-testid="pos-browse">
        <summary>Browse products</summary>
        <span class="viewflip" role="group" aria-label="Results view">
          <button
            class="${this.wsLookupPresentation === 'CARDS' ? 'active' : ''}"
            data-testid="lookup-view-cards"
            aria-pressed=${this.wsLookupPresentation === 'CARDS' ? 'true' : 'false'}
            title="Card view: the product-card grid"
            @click=${() => this.wsSetLookupPresentation('CARDS')}
          >
            Cards
          </button>
          <button
            class="${this.wsLookupPresentation === 'ROWS' ? 'active' : ''}"
            data-testid="lookup-view-rows"
            aria-pressed=${this.wsLookupPresentation === 'ROWS' ? 'true' : 'false'}
            title="Row view: one product per compact row: name, SKU, price and branch stock"
            @click=${() => this.wsSetLookupPresentation('ROWS')}
          >
            Rows
          </button>
        </span>
        ${/* STILL ONE FIELD. The browse grid takes the SAME forwarded term as the dropdown and
           therefore renders no input of its own: the rev-8 unified-field floor, unchanged by
           the move. The dropdown is the glance at that term; the grid, when someone opens it, is
           the whole of it. Two boxes searching one catalogue is the defect that floor exists
           for, and putting the grid behind a disclosure would not have made a second box
           acceptable, only easier to miss. */ ''}
        <hos-pos-product-grid
          .homeBranchId=${this.branchId}
          .showChips=${this.quickChips}
          .presentation=${this.wsLookupPresentation}
          .externalTerm=${this.lookupTerm}
          .curated=${this.curatedChips}
          .ticketEpoch=${this.ticketEpoch}
          @pos-add-product=${(e: CustomEvent<{ product: PosProduct }>) => this.addProduct(e.detail.product)}
          @pos-add-echo=${(e: CustomEvent<{ id: string }>) => this.onAddEcho(e.detail.id)}
        ></hos-pos-product-grid>
      </details>

      ${/* THE TICKET. Below the ways of adding, because that is the reading order of the job: you
         reach for the gun, then you look at what you have.

         The ticket's own Lines ⇄ Cards flip (D-096 sets the dealer default; this is the
         per-terminal view) sits ON the ticket. It used to live in the retired sale panel's
         config popover, which is how it nearly vanished with the restructure: a presentation
         choice belongs beside the thing it presents, not in a settings drawer for a panel that
         no longer exists. Offered only while there are lines to present. */ ''}
      ${this.cart.length === 0
        ? html`<div class="empty">Scan a barcode, search above, or tap a quick pick to start the ticket.</div>`
        : html`<div class="ticketbar">
              <span class="viewflip" role="group" aria-label="Ticket view">
                <button
                  class="${this.presentation === 'LINES' ? 'active' : ''}"
                  data-testid="ticket-view-lines"
                  aria-pressed=${this.presentation === 'LINES' ? 'true' : 'false'}
                  title="Line view: one dense row per item"
                  @click=${() => (this.presentation = 'LINES')}
                >
                  Lines
                </button>
                <button
                  class="${this.presentation === 'CARDS' ? 'active' : ''}"
                  data-testid="ticket-view-cards"
                  aria-pressed=${this.presentation === 'CARDS' ? 'true' : 'false'}
                  title="Card view: one card per item, with its picture"
                  @click=${() => (this.presentation = 'CARDS')}
                >
                  Cards
                </button>
              </span>
            </div>
            ${this.presentation === 'LINES' ? this.renderLinesTable() : this.renderCardLines()}`}
      ${this.renderAddonsStrip()}

${/* Fulfilment: the EXISTING sale types (counter / will-call / delivered / COD with its
   policy gate: the M3 will-call machinery), worn as tiles instead of a select. */ ''}
            <label id="fulfil-label">Fulfilment</label>
            <div class="fulfil" role="group" aria-labelledby="fulfil-label">
              <button
                class="${this.saleType === 'COLLECT_NOW' ? 'active' : ''}"
                aria-pressed=${this.saleType === 'COLLECT_NOW' ? 'true' : 'false'}
                title="Collect now (counter)"
                @click=${() => (this.saleType = 'COLLECT_NOW')}
              >
                <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M5 8h14l-1 12H6L5 8z"></path>
                  <path d="M9 11V6a3 3 0 016 0v5"></path>
                </svg>
                Collect now
              </button>
              <button
                class="${this.saleType === 'COLLECT_LATER' ? 'active' : ''}"
                aria-pressed=${this.saleType === 'COLLECT_LATER' ? 'true' : 'false'}
                title="Collect later (will-call)"
                @click=${() => (this.saleType = 'COLLECT_LATER')}
              >
                <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <circle cx="12" cy="12" r="8.5"></circle>
                  <path d="M12 7.5V12l3 2.5"></path>
                </svg>
                Will-call
              </button>
              <button
                class="${this.saleType === 'DELIVERED' ? 'active' : ''}"
                aria-pressed=${this.saleType === 'DELIVERED' ? 'true' : 'false'}
                title="Delivered"
                @click=${() => (this.saleType = 'DELIVERED')}
              >
                <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M2.5 6.5h12v10h-12zM14.5 10h4l2.5 3v3.5h-6.5"></path>
                  <circle cx="6.5" cy="18" r="1.8"></circle>
                  <circle cx="17" cy="18" r="1.8"></circle>
                </svg>
                Delivery
              </button>
              <button
                class="${this.saleType === 'COD' ? 'active' : ''}"
                aria-pressed=${this.saleType === 'COD' ? 'true' : 'false'}
                ?disabled=${this.codEnabled !== true}
                title=${this.codEnabled === false
                  ? 'Cash on demand is disabled by policy for this branch.'
                  : this.codEnabled === null
                    ? 'Cash-on-demand availability not confirmed: policy unreachable.'
                    : 'Unpaid at ring: invoice number issues now, goods release once paid.'}
                @click=${() => (this.saleType = 'COD')}
              >
                <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <rect x="2.5" y="8" width="13" height="9" rx="1.5"></rect>
                  <circle cx="9" cy="12.5" r="2"></circle>
                  <path d="M17.5 3.5a4.6 4.6 0 013 4.3M17.5 6a2.2 2.2 0 011.6 2"></path>
                </svg>
                COD${this.codEnabled === false ? ': off' : ''}
              </button>
            </div>

            ${this.saleType === 'COD'
            ? html`<label>Fulfilment (released once paid)</label>
                <select .value=${this.fulfilment} @change=${(e: Event) => (this.fulfilment = (e.target as HTMLSelectElement).value)}>
                  <option value="PICKUP">Pickup</option>
                  <option value="WILL_CALL">Will-call</option>
                  <option value="DELIVERY">Delivery</option>
                </select>`
            : nothing}
          ${this.deliveryActive()
            ? html`<label>Delivery address</label>
                ${/* DIB-512 · the field delivery NEEDS sits BESIDE the choice that needs it
                   (adjacency): it used to hide behind More options as "Job address (optional)"
                   while a delivery sale completed with no address at all. Same sale fact, same
                   wire field (job_address): surfaced where Delivery is chosen. */ ''}
                <input
                  data-testid="delivery-address"
                  maxlength="200"
                  autocomplete="off"
                  placeholder="e.g. 128 Front St, Trenton"
                  .value=${this.jobAddress}
                  title="Where the material is going: printed with the order."
                  @change=${(e: Event) => (this.jobAddress = (e.target as HTMLInputElement).value)}
                />`
            : nothing}
          ${this.saleType !== 'COLLECT_NOW'
            ? html`<label>Requested date</label>
                ${/* DIB-259: one box for the date instead of two. The clerk types the date (YYYYMMDD,
                   the format frozen in parseCompactDate) directly into the single visible field, OR
                   taps the calendar button to pick it. Both routes land in the same requestedDate,
                   and whatever is chosen shows back in this one box: no more parallel text/picker
                   inputs. A visually-hidden native date input holds the calendar so the browser's
                   picker still opens from the button. */ ''}
                <div class="daterow">
                  <input
                    class="compactdate"
                    inputmode="numeric"
                    autocomplete="off"
                    placeholder="YYYYMMDD"
                    .value=${this.requestedDate}
                    aria-label="Requested date: type eight digits YYYYMMDD, or use the calendar button"
                    title="Type all 8 digits in order (YYYYMMDD), or tap the calendar"
                    @input=${this.onCompactDateInput}
                    @change=${this.onCompactDateChange}
                  />
                  <button
                    type="button"
                    class="datepick"
                    aria-label="Open calendar to pick the requested date"
                    title="Open the calendar"
                    @click=${this.openDatePickerFromButton}
                  >
                    ${/* DIB-259 (ship review): monochrome inline SVG (currentColor) instead of a
                       full-color 📅 emoji, so the icon responds to the token/theme + hover like every
                       other affordance in the app. */ ''}
                    <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                      <rect x="3" y="4.5" width="18" height="16.5" rx="2"></rect>
                      <path d="M3 9.5h18M8 2.5v4M16 2.5v4"></path>
                    </svg>
                  </button>
                  <input
                    class="datehidden"
                    type="date"
                    tabindex="-1"
                    aria-hidden="true"
                    .value=${this.requestedDate}
                    @change=${(e: Event) => (this.requestedDate = (e.target as HTMLInputElement).value)}
                  />
                </div>
                ${/* DIB-512 · the always-on format sentence is gone: the placeholder says the
                   format, the calendar button IS the no-typing path, and the hint line now
                   exists only as the refusal it was designed for (a bad entry). */ ''}
                ${this.dateError ? html`<div class="datehint bad">${this.dateError}</div>` : nothing}`
              : nothing}

            `;
  }

  /**
 * STEP 3 · PAYMENT: the totals card, the tender tiles and the one gold commit.
   *
   * The totals card stays adjacent to the tender it justifies (it is the number the clerk reads out
   * loud), and it is the running total's one source; the collapsed Items card echoes it only while
   * this step is closed.
   */
  private renderPaymentBody() {
    return html`
${/* rev 8 (item 7): the SPLIT TOTALS CARD: a quiet, distinct card of its own, ABOVE a
   clearly separate Payment section (concept parity). Same numbers, same derivations;
   the oversized gold Total is the card's one loud element. The rev-6 total chip in
   the panel bar stands for the scroll cases. */ ''}
          <section class="panel totalscard" aria-label="Totals" data-testid="pos-totals-card">
            ${this.reliefReason
              ? html`<div class="reliefchip">tax relief: ${this.reliefReason}</div>`
              : nothing}
            <div class="totals">
              <div class="trow"><span>Subtotal</span><span class="num">${this.moneyOrMarker(this.subtotal())}</span></div>
              <div class="trow"><span>${this.taxLabel()}</span><span class="num">${this.moneyOrMarker(this.tax())}</span></div>
              <div class="trow grand"><span>Total (est.)</span><span class="num">${this.moneyOrMarker(this.total())}</span></div>
            </div>
          </section>

          


            ${/* DIB-488: tender is CHOSEN here (a state of the sale the flow can track), and the
               sale rings on the one Complete-sale CTA below. Same ring, same idempotency key
               minting, same server path as the old ring-on-click tenders. */ ''}
            ${/* rev 5: aria-pressed carries the chosen tender to assistive tech (the class alone
               said nothing), and a DONE ring disarms the tiles with the CTA: the committed
               basket takes no further tender. */ ''}
            ${/* DIB-512: ONE roving Tab stop for the whole group: arrows move inside (the house
               grid/rail rule); Tab passes straight through to the CTA. */ ''}
            <div class="paytiles" role="group" aria-label="Payment method" @keydown=${(e: KeyboardEvent) => this.onPayTilesKey(e)} data-testid="pos-paytiles">
              <button
                class="${this.payMethod === 'ACCOUNT' ? 'active' : ''}"
                aria-pressed=${this.payMethod === 'ACCOUNT' ? 'true' : 'false'}
                tabindex=${this.payTileIdx === 0 ? '0' : '-1'}
                ?disabled=${this.ringBusy() || this.ringDone()}
                title=${this.customer
                  ? `Charge ${this.customer.name}`
                  : 'Charge Account: Complete sale will ask you to pick the account.'}
                @click=${() => {
                  this.payTileIdx = 0;
                  this.choosePay('ACCOUNT');
                }}
              >
                ${/* rev 6 (item C): small inline tender icons: currentColor strokes, tokens only,
                   aria-hidden (the labels already name the tiles). */ ''}
                <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M4 20V9l8-5 8 5v11"></path>
                  <path d="M4 20h16M9 20v-5h6v5"></path>
                </svg>
                Charge Account
              </button>
              <button
                class="${this.payMethod === 'CASH' ? 'active' : ''}"
                aria-pressed=${this.payMethod === 'CASH' ? 'true' : 'false'}
                tabindex=${this.payTileIdx === 1 ? '0' : '-1'}
                ?disabled=${this.ringBusy() || this.ringDone()}
                @click=${() => {
                  this.payTileIdx = 1;
                  this.choosePay('CASH');
                }}
              >
                <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <rect x="2.5" y="6.5" width="19" height="11" rx="1.5"></rect>
                  <circle cx="12" cy="12" r="2.6"></circle>
                  <path d="M5.5 9.5v0M18.5 14.5v0"></path>
                </svg>
                Cash
              </button>
              <button
                class="${this.payMethod === 'CARD' ? 'active' : ''}"
                aria-pressed=${this.payMethod === 'CARD' ? 'true' : 'false'}
                tabindex=${this.payTileIdx === 2 ? '0' : '-1'}
                ?disabled=${this.ringBusy() || this.ringDone()}
                @click=${() => {
                  this.payTileIdx = 2;
                  this.choosePay('CARD');
                }}
              >
                <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
                  <rect x="2.5" y="5.5" width="19" height="13" rx="2"></rect>
                  <path d="M2.5 9.5h19M6 15h4"></path>
                </svg>
                Card
              </button>
              ${/* DIB-165: split opens a PANEL rather than ringing, because a split is composed
                 before it is committed: there is nothing to ring until the legs add up. */ ''}
              <button
                data-testid="tender-split"
                class="${this.payMethod === 'SPLIT' ? 'active' : ''}"
                aria-pressed=${this.payMethod === 'SPLIT' ? 'true' : 'false'}
                tabindex=${this.payTileIdx === 3 ? '0' : '-1'}
                ?disabled=${this.ringBusy() || this.ringDone()}
                title="Settle this sale across several tenders (cash + card + account)."
                @click=${() => {
                  this.payTileIdx = 3;
                  this.choosePay('SPLIT');
                }}
              >
                <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                  <path d="M4 12h6M14 6l6-0M14 18l6 0M10 12c3 0 4-6 7-6M10 12c3 0 4 6 7 6"></path>
                </svg>
                Split…
              </button>
            </div>
            ${this.renderCashTendered()}

            ${this.payMethod === 'CARD' || (this.depositCents() > 0 && this.depositMethod === 'CARD')
              ? html`<label class="cardlabel">
                    Card type
                    ${this.cardRail === 'test'
                      ? html`<span class="railbadge test" title="Deterministic simulated gateway: no card reader or live Run Payments call. Flip to the live client at cutover; the sale flow is identical.">TEST MODE: simulated terminal</span>`
                      : nothing}
                    ${this.cardRail === 'live'
                      ? html`<span class="railbadge live">LIVE: Run Payments</span>`
                      : nothing}
                    ${this.cardRail === 'unconfigured'
                      ? html`<span class="railbadge off" title="An admin must configure the card processor (PUT /api/v1/payments/config) before Card tender works.">card processor not configured</span>`
                      : nothing}
                  </label>
                  <select .value=${this.cardType} @change=${(e: Event) => (this.cardType = (e.target as HTMLSelectElement).value)}>
                    <option value="CREDIT">Credit</option>
                    <option value="DEBIT">Debit</option>
                    <option value="PREPAID">Prepaid</option>
                  </select>`
              : nothing}

            ${/* rev 5: after a DONE ring the CTA disarms and SAYS the sale is complete: one more
               "did it take?" tap used to mint a fresh key and ring the same basket again as a
               duplicate order (a double charge on Card). Next sale is the forward path. */ ''}
            ${/* WAVE 2 · findings 10 + 11: THE CTA IS ALWAYS THE CTA, AND IT SAYS WHAT IT WILL DO.
               Two things used to be wrong with this slot and both were about the same thing: the
               counter's single loudest control telling the truth about the next press.

               (10) Choosing Split replaced the button with a `.cta-hint` sentence: "Compose the
               split legs below: the split panel completes the sale.": so the loudest thing on a
               screen whose job is completing a sale became ten words of prose, and the text budget
               allows a sentence in three places (a confirmation, a refusal, an empty state) of
               which this was none. The button stays, disabled, and SAYS the state; the panel it
               points at is directly beneath it, which is why the sentence was redundant as well as
               loud.

               (11) With the till shut the button was fully armed and said "Complete sale ·
               CA$7,661.40": the refusal existed only AFTER the press, as an error banner with an
               "Open session now" button in it, testing the very predicate that was knowable before
               the press. Disabling it would be worse (it strands a clerk with no way forward and
               no reason given), so the button keeps its arm and changes its promise: it will open
               the drawer, then ring. completeSale() does exactly that, in that order. */ ''}
            ${this.payMethod !== 'SPLIT'
              ? html`<button
                    class="complete-cta"
                    data-testid="pos-complete-sale"
                    ?disabled=${this.cart.length === 0 || this.ringBusy() || this.ringDone()}
                    title=${this.ringDone()
                      ? 'This sale is already complete: start the next one from the confirmation below.'
                      : this.tillSessionId === ''
                        ? 'Opens a till session on this register, then rings the sale: order, invoice and payment post together.'
                        : 'Rings the sale: order, invoice and payment post together.'}
                    @click=${() => this.completeSale()}
                  >
                    ${this.ringDone()
                      ? `Sale complete: ${this.doneSale().order_no}`
                      : this.ringBusy()
                        ? 'Ringing…'
                        : this.tillSessionId === ''
                          ? `Open session · then complete · ${this.moneyOrMarker(this.total())}${this.hasPricedLine() ? ' (est.)' : ''}`
                          : `Complete sale · ${this.moneyOrMarker(this.total())}${this.hasPricedLine() ? ' (est.)' : ''}`}
                    ${!this.ringDone() && this.payKeycap()
                      ? html`<span class="keycap" data-testid="pay-hotkey-chip" aria-hidden="true"
                          >${this.payKeycap()}</span
                        >`
                      : nothing}
                  </button>
                  `
              : html`<p
                  class="split-band"
                  role="status"
                  data-testid="cta-split-state"
                >
                  Split: compose the legs below
                  <span class="band-why" data-testid="cta-split-why"
                    >The legs must add up to the total first; the panel below rings the split.</span
                  >
                </p>`}

          ${this.showSplit
            ? html`<hos-pos-split-tender
                .totalCents=${this.hasPricedLine() ? this.total() : null}
                .hasCustomer=${!!this.customer}
                .customerName=${this.customer?.name ?? ''}
                .busy=${this.ringBusy()}
                @pos-split-ring=${(e: CustomEvent<{ tenders: { method: string; amount_cents: number }[] }>) =>
                  void this.ringSplitSale(e.detail.tenders)}
                @pos-split-cancel=${() => {
                  // Cancelling the split falls back to CASH rather than to no tender at all: the
                  // ticket must stay tenderable, and the legs that were being composed are gone.
                  this.showSplit = false;
                  this.payMethod = 'CASH';
 // WAVE 2 · finding 18: THE ROVING GROUP'S DECLARED STOP AND ITS FOCUSED MEMBER
                  // MUST BE THE SAME TILE. This set the index to Cash (1) and then focused Split
                  // (3), so Cash held tabindex="0" while the keyboard sat on a tabindex="-1"
                  // button: Shift+Tab out and Tab back relocated the user to a tile they had not
 // chosen, silently. The focus move below is right and stays: the control that
 // opened the panel is where the panel's Cancel belongs: so it is the index that
                  // has to agree with it. The TENDER still falls back to CASH (the ticket must
                  // stay tenderable, the composed legs are gone); only the group's keyboard seat
                  // follows the focus.
                  this.payTileIdx = 3;
 // rev 5: the panel unmounts under the keyboard: hand focus back to the Split
                  // tile (the control that opened it) instead of dropping it on <body>.
                  void this.updateComplete.then(() =>
                    this.renderRoot.querySelector<HTMLElement>('[data-testid="tender-split"]')?.focus(),
                  );
                }}
              ></hos-pos-split-tender>`
            : nothing}

          

${this.ring.state === 'error'
            ? html`<div class="banner bad" role="alert">
                ${this.ring.msg}
                ${this.tillSessionId === '' && /till session/i.test(this.ring.msg)
                  ? html`<button class="bannerfix" @click=${() => void this.openSession()}>
                      Open session now
                    </button>`
                  : nothing}
              </div>`
            : nothing}`;
  }

  /**
 * STEP 4 · SUMMARY: the receipt moment, promoted out from under the payment block into a step of
   * its own, because that is what it always was: what sold, what was paid and how, the change due,
   * the numbers the customer will quote back, and the two things anyone does next.
   *
   * Completing payment expands this card itself (revealSaleOutcome → advanceOn('sale-rung')), so
   * the confirmation is never a sliver at the foot of a scroll the way it was measured to be.
   */
  private renderSummaryBody() {
    return html`
      ${this.ringDone() ? nothing : this.renderReadBack()}
${this.ring.state === 'done'
            ? html`${this.renderSoldPanel(this.ring.sale)}
                ${/* WAVE 2 · finding 13: ORDER ADMINISTRATION IS DISCLOSED, NOT DELETED.
                   Measured on a real carry-out (#SO-TRT-3 / #INV-2, CA$7,661.40, cash): 58 visible
                   strings in this card, of which the sold panel above is the first twelve and is
                   the whole of what a counter person came for: Paid, the amount, the sale and
                   invoice numbers, the tender, Print receipt, Next sale. The other ~46 were order
                   administration in one unbroken column under it: status transitions, an activity
                   drawer, a status-history table, an edit panel, a change-order form, shipment
                   groups, an invoicing plan, a duplicate action and a related-documents list:
                   six of those panels showing nothing but their own empty state, three full
                   sentences of chrome, and a truncated internal order id standing as a section
                   header. None of it is wrong; all of it is somebody else's minute. A customer is
                   holding a card at the counter and the next thing that happens is the next sale.

                   A DISCLOSURE MAKES NOTHING UNREACHABLE, which is why it is the answer and
                   deletion is not: a clerk who does need to mark this order picking is one press
                   away, and the press is labelled with what is behind it. It renders CLOSED:
                   <details> without `open`: and the sub-panels inside it are the shipped
                   components, untouched. (Each sub-panel suppressing itself when it has no content
                   is the other half of this finding and lives in the order-*.ts files, which are
                   outside this lane; it is reported rather than done.) */ ''}
                <details class="orderadmin" data-testid="pos-order-admin">
                <summary
                  ><span class="disc" aria-hidden="true"
                    ><span class="closed">${drawIcon(iconForId('pencil:right'), 12)}</span
                    ><span class="open">${drawIcon(iconForId('pencil:bottom'), 12)}</span>
                  </span>
                  Order admin</summary
                >
                ${/* DIB-191: the committed order's lifecycle controls: status chip + whichever manual
                   transitions the SERVER says this caller may take (legal_actions). A rung sale is
                   already CONFIRMED (or INVOICED once the outbox drains), so Confirm never applies
                   here; picking/ready/cancel do. */ ''}
                <hos-order-lifecycle
                  order-id=${this.ring.sale.order_id}
                  @order-status-changed=${this.onOrderStatusChanged}
                  @open-order=${this.onOpenOrder}
                ></hos-order-lifecycle>
                ${/* DIB-492/493: the one-click order audit: a SMALL affordance, not a primary
                   element: who changed what, when, plus the team's notes, in one compact drawer. */ ''}
                <hos-order-activity order-id=${this.ring.sale.order_id}></hos-order-activity>
                ${/* DIB-159: the committed order's status timeline, next to where the sale + its payment
                   facts show: how the order moved from→to, by whom, when (staff-gated read). */ ''}
                <hos-order-history order-id=${this.ring.sale.order_id}></hos-order-history>
                ${/* DIB-175: the staff edit controls the SERVER offers for this order + the who/what/when
                   change trail. A just-rung sale carries a finalized invoice, so the money predicate
                   omits every line edit here and the panel renders nothing: which is the ruling made
                   visible, not a missing feature. */ ''}
                <hos-order-edit
                  order-id=${this.ring.sale.order_id}
                  @order-changed=${this.onOrderChanged}
                ></hos-order-edit>
                <hos-order-edits order-id=${this.ring.sale.order_id}></hos-order-edits>
                <hos-order-change order-id=${this.ring.sale.order_id}></hos-order-change>
                <hos-order-shipments order-id=${this.ring.sale.order_id}></hos-order-shipments>
                ${/* DIB-489: same order again (multi-truck, or with a tweak) without re-keying it. */ ''}
                <hos-order-duplicate
                  order-id=${this.ring.sale.order_id}
                  @open-order=${this.onOpenOrder}
                ></hos-order-duplicate>
                ${/* DIB-491: where this order came from (its quote, sibling orders, invoices).
                   open-kinds="ORDER": this surface can open orders (the lookup below); quote and
                   invoice chips render as labels rather than dead buttons. */ ''}
                <hos-related-docs
                  kind="order"
                  doc-id=${this.ring.sale.order_id}
                  open-kinds="ORDER"
                  @related-doc-open=${this.openRelatedOrder}
                ></hos-related-docs>
                </details>`
            : nothing}

          

${this.receiptMsg ? html`<div class="banner bad" role="alert">${this.receiptMsg}</div>` : nothing}
          ${this.lookupOpenId
            ? html`<hos-order-lifecycle
                  order-id=${this.lookupOpenId}
                  @order-status-changed=${this.onOrderStatusChanged}
                  @open-order=${this.onOpenOrder}
                  @order-lifecycle-loaded=${(e: CustomEvent<{ found: boolean }>) => {
                    this.lookupFound = e.detail.found;
                  }}
                ></hos-order-lifecycle>
                ${this.lookupFound
                  ? html`<hos-order-activity order-id=${this.lookupOpenId}></hos-order-activity>
                      <hos-order-history order-id=${this.lookupOpenId}></hos-order-history>
                      <hos-order-edit
                        order-id=${this.lookupOpenId}
                        @order-changed=${this.onOrderChanged}
                      ></hos-order-edit>
                      <hos-order-edits order-id=${this.lookupOpenId}></hos-order-edits>
                      <hos-order-change order-id=${this.lookupOpenId}></hos-order-change>
                      <hos-order-shipments order-id=${this.lookupOpenId}></hos-order-shipments>
                      ${/* DIB-489: duplicate the looked-up order (a past order or a draft) as a
                         fresh DRAFT with a line picker + quantity tweaks. */ ''}
                      <hos-order-duplicate
                        order-id=${this.lookupOpenId}
                        @open-order=${this.onOpenOrder}
                      ></hos-order-duplicate>
                      <hos-related-docs
                        kind="order"
                        doc-id=${this.lookupOpenId}
                        open-kinds="ORDER"
                        @related-doc-open=${this.openRelatedOrder}
                      ></hos-related-docs>`
                  : nothing}`
            : nothing}
          `;
  }

  /**
 * THE READ-BACK: the Summary step BEFORE the money moves.
   *
   * Ryan Bostock asked for the summary and said exactly when he uses it: "before the person pays
   * I'm going over all the details." So Summary is not the receipt's card. The receipt is what it
   * BECOMES; what it is for most of its life is the thing a counter person reads aloud with a
 * customer leaning on the counter: and it has to exist while the sale is still changeable,
   * because the whole point of reading it back is to catch what is wrong.
   *
   * WHAT IT SAYS, IN THE ORDER IT IS SAID: who it is for, what is on it, how it leaves, what it
   * comes to, and what they are saving. Nothing else. Every one of those is a thing the customer
   * will say "no, wait" about; nothing here is a control, because a step whose job is reading is
 * not a step to also be editing in: the card that owns each fact is one header press away and
   * still open for business.
   *
   * The totals here are the same derivations the Payment card's totals card shows, and only one
   * card is open at a time, so the say-it-once rule is kept structurally rather than by suppression.
   */
  private renderReadBack() {
    if (this.cart.length === 0) {
      // Empty is never an error. One calm line: what is missing, what that means, the way forward.
      return html`<div class="empty" data-testid="summary-empty">
        Nothing on this ticket yet: add items and the whole sale reads back here before you take payment.
      </div>`;
    }
    const saving = this.ticketSavingCents();
    return html`<div class="readback" data-testid="pos-readback">
      <div class="rbwho">
        <span class="k">For</span>
        <span class="v">${this.customer ? this.customer.name : 'Walk-in customer'}</span>
      </div>
      <ul class="rblines">
        ${this.cart.map((l) => {
          const p = pricePicture(this.ticketQuotes[l.product.id]);
          const word = basisWord(p.basis);
          return html`<li>
            <span class="q">${l.qty}</span>
            <span class="n"
              >${l.product.name}
              ${p.showSaving
                ? html`<span class="rbsave" data-testid="readback-saving"
                    >Save ${money(perLineSavingCents(p.savingCents, l.qty))}${word ? ` · ${word}` : ''}</span
                  >`
                : nothing}</span
            >
            <span class="m">${l.priceMissing ? 'price pending' : money(l.lineTotalCents)}</span>
          </li>`;
        })}
      </ul>
      <div class="rbrow"><span>${fulfilmentLabel(this.saleType)}</span><span></span></div>
      ${this.requestedDate ? html`<div class="rbrow"><span>Requested</span><span>${this.requestedDate}</span></div>` : nothing}
      <div class="rbrow"><span>Subtotal</span><span class="m">${this.moneyOrMarker(this.subtotal())}</span></div>
      <div class="rbrow"><span>${this.taxLabel()}</span><span class="m">${this.moneyOrMarker(this.tax())}</span></div>
      <div class="rbrow total"><span>Total (est.)</span><span class="m">${this.moneyOrMarker(this.total())}</span></div>
      <div class="rbrow"><span>${tenderLabel(this.payMethod || 'CASH')}</span><span></span></div>
      ${/* "how much am I saving?": the client's words for the thing customers always ask. Server
         arithmetic, summed per line; absent entirely when there is nothing to claim. */ ''}
      ${saving > 0
        ? html`<div class="rbrow saving" data-testid="readback-saving-total">
            <span>You save</span><span class="m">${money(saving)}</span>
          </div>`
        : nothing}
    </div>`;
  }

  /**
   * The sale-session utilities, DELIBERATELY OUTSIDE the accordion.
   *
 * Park ticket, the parked rail and Clear sale are not steps: they act on the whole sale, and a
   * clerk parks a ticket most often while looking at the ITEMS. Leaving them inside the payment
   * card (where they shipped) would have made parking unreachable from the step you park from,
   * which is the kind of capability loss a restructure is supposed to avoid. Everything occasional
   * stays one quiet disclosure away exactly as before.
   */
  private renderSaleUtilities() {
    return html`<div class="saleutil">
      ${/* rev 8 (item 10): the one-click margin quick-hide (eye), re-homed from the retired sale
         panel's bar. It sits with the utilities because the figures it hides live on the ticket
         LINES and in the stat bar, both of which are readable from every step. Offered only while
         the role-gated data is actually PRESENT: there is nothing to hide otherwise, and the
         role gate itself is server-side (the payload omits the fields; this is counter privacy,
         not security). */ ''}
${/* DIB-512 · the utility diet. Ten always-on buttons + a 13-word order-id input used to
   render below the CTA on EVERY sale (empty included). What earns the always-on row:
   Park / Parked (the mid-rush pair) and: only while the order holds something to
   lose: the destructive Clear. The util "New sale" is GONE outright (the header's
   gold + New Sale owns that verb; three controls said it at once). Everything
   occasional lives one quiet disclosure away, capability untouched. */ ''}
          <div class="util">
            <button ?disabled=${this.cart.length === 0} @click=${this.parkCurrent}>Park ticket</button>
            <button @click=${() => (this.showParked = true)}>Parked (${this.parkedCount})</button>
            ${this.orderHasContent()
              ? html`<button
                  class="clear"
                  title="Empties the current sale in one step (all lines + the selected customer). The order is NOT parked. Asks to confirm."
                  @click=${this.clearSale}
                >
                  Clear sale
                </button>`
              : nothing}
          </div>
          <details class="more">
            <summary
              ><span class="disc" aria-hidden="true"
                ><span class="closed">${drawIcon(iconForId('pencil:right'), 12)}</span
                ><span class="open">${drawIcon(iconForId('pencil:bottom'), 12)}</span>
              </span>
              More</summary
            >
            <div class="util">
              ${/* HH note (lead rulings 15 and 16): the reference's More entries are closed in
                 the mount. Return: HH's ruled desk (the approval dialog, the simulated word,
                 the clerk-and-approver receipt) is the one returns path, and this reference
                 dialog carries none of it. Past sales, Pricing doc and Assess tax relief are
                 live call paths outside the counter-sale row, unruled and untested in HH.
                 CLOSURE.md records each; the polish lane may open them with tests. */ ''}
            </div>
            ${this.renderOrderOpenRow()}
          </details>
          ${/* UI-3 (visual GAP 8): the eye rides the END of the utility row, so it appearing when the
             first priced line lands never moves Park ticket; it is an outlined control, as the reference's. */ ''}
          ${this.marginPresent()
            ? html`<button
                class="eye"
                data-testid="margin-eye"
                aria-pressed=${this.wsMarginHidden ? 'true' : 'false'}
                title=${this.marginEyeWords()}
                aria-label=${this.marginEyeWords()}
                @click=${() => this.wsToggleMarginHidden()}
              >
                ${this.wsMarginHidden
                  ? html`<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M3 3l18 18M10.6 10.7a2.6 2.6 0 003.7 3.7M6.6 6.8C4.5 8 3 9.9 2 12c1.8 3.7 5.5 6 10 6 1.6 0 3.1-.3 4.4-.9M9.9 4.2C10.6 4.1 11.3 4 12 4c4.5 0 8.2 2.3 10 8-.5 1.1-1.2 2.1-2 3"></path></svg>`
                  : html`<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><path d="M2 12c1.8-5.7 5.5-8 10-8s8.2 2.3 10 8c-1.8 3.7-5.5 6-10 6S3.8 15.7 2 12z"></path><circle cx="12" cy="12" r="2.6"></circle></svg>`}
              </button>`
            : nothing}
          ${this.pickupLine !== null
            ? html`<div id="till-message" class="banner ${this.pickupLine.bad ? 'bad' : 'ok'}" role="${ifDefined(this.pickupLine.bad ? 'alert' : 'status')}">${this.pickupLineWords()}</div>`
            : this.utilMsg && this.utilWhere === 'foot'
              ? html`<div id="till-message" class="banner ${this.utilTone}" role="${ifDefined(this.utilTone === 'bad' ? 'alert' : this.utilTone === 'note' && !this.utilLive ? undefined : 'status')}">${this.utilMsg}</div>`
              : nothing}

          
    </div>`;
  }


  /**
 * DIB-191 B2/C3 · the direct order-id path: DRAFT orders (quote-convert, portal reorder) never
   * reach the Sold panel, so this lookup is what makes DRAFT→CONFIRMED demonstrable, and DIB-410's
   * any-past-sale receipt rides beside it. DIB-512: it lives inside the payment block's More
   * disclosure (an occasional task, not counter chrome), and the placeholder says "#SO or order
   * id" instead of a 13-word tutorial. Same state, same handlers, same panels open below.
   */
  private renderOrderOpenRow() {
    return html`<div class="util">
      <input
        style="font: inherit; min-width: 16rem"
        placeholder="#SO or order id"
        aria-label="Open order by id or #SO number"
        .value=${this.lookupOrderId}
        @input=${(e: Event) => (this.lookupOrderId = (e.target as HTMLInputElement).value.trim())}
      />
      <button
        ?disabled=${this.lookupOrderId === ''}
        @click=${() => {
          this.lookupOpenId = this.lookupOrderId;
          this.lookupFound = true; // reset the suppression for the new lookup
        }}
      >
        Open order
      </button>
      <button
        ?disabled=${this.lookupOrderId === '' || this.receiptBusy}
        title="Prints the receipt for that sale (order id or #SO number)."
        @click=${() => this.printSaleReceipt(this.lookupOrderId)}
      >
        ${this.receiptBusy ? 'Loading receipt…' : 'Print receipt'}
      </button>
      ${this.lookupOpenId
        ? html`<button @click=${() => { this.lookupOpenId = ''; this.lookupOrderId = ''; }}>
            Close
          </button>`
        : nothing}
    </div>`;
  }

  /**
 * rev 8 (item 12): the FOOTER TRANSACTION BAR: the ticket panel's bottom line of truth.
   * "N Items · Total Weight X lbs" always (the blank word when NO line has a weight);
   * "Margin Y% · Est. Profit CA$Z" segments ONLY while the role-gated server data is present AND
   * the quick-hide is off. Sits OUTSIDE the panel's internal scroll, so it never leaves view.
   */
  private renderTxBar() {
    const weights = this.cart.map((l) => this.lineWeight(l));
    const { totalLbs, allKnown } = ticketWeightTotalLbs(weights);
    const anyKnown = weights.some((w) => w !== null);
    // ui3 visual GAP 2's rule at the ticket line too (ui3d P2-2): the glance derives its figures
    // from the server's own prices, so while any line's price has not answered neither segment
    // shows a figure. The building window with the same lines shows neither (its glance never
    // ran): the two windows agree by the same condition the margin cells read.
    const ticketPriced = this.hasPricedLine();
    const segs = txbarSegments(
      {
        itemCount: this.cart.length,
        totalWeightLbs: anyKnown ? totalLbs : null,
        marginBps: ticketPriced ? this.marginData?.margin_bps : undefined,
        estProfitCents: ticketPriced ? this.marginData?.est_profit_cents : undefined,
        marginHidden: this.wsMarginHidden,
      },
      money,
    );
    return html`<div class="txbar" data-testid="pos-txbar">
      ${segs.map(
        (seg) => html`<span
          class="seg"
          data-testid="txbar-${seg.id}"
          title=${seg.id === 'weight' && anyKnown && !allKnown
            ? 'Some lines have no weight on file and are excluded from this total.'
            : (seg.id === 'margin' || seg.id === 'profit') && this.marginData?.partial
              ? 'Some lines have no cost on file and are excluded: computed server-side, visible to allowed roles only.'
              : seg.id === 'margin' || seg.id === 'profit'
                ? 'Computed server-side: visible to allowed roles only.'
                : ''}
          >${seg.text}${(seg.id === 'weight' && anyKnown && !allKnown) ||
          ((seg.id === 'margin' || seg.id === 'profit') && this.marginData?.partial)
            ? html`<span class="partial" aria-hidden="true">*</span>`
            : nothing}</span
        >`,
      )}
    </div>`;
  }

  /**
 * DIB-488 rev 6 (item A): the customer question's IDENTITY states (mock parity):
   *
 * resolved account a proper identity card: initials chip, name, account number, the
   * account-type tag, the credit facts the old strip carried (Terms /
 * Available / Outstanding: losing them would be a regression), and a
   * Change control that reopens selection.
   * walk-in confirmed the same clean card shape with its own honest facts (Cash tag, no
 * account on the order): an explicit answer deserves better than the
   * default's placeholder strip.
 * unresolved the existing "Select customer" pick affordance, unchanged: nobody has
   * answered yet, so there is no identity to card.
   */
  /**
 * rev 6 (item F, the commander-approved opinion #1): the OPTIONAL cash-tendered quick entry.
   * Shown only once Cash is the chosen tender and FULLY skippable: an untouched box changes
   * nothing (Complete rings the exact total, one-click cash intact). An entry covering the total
   * previews the change due here and rides the ring body (the server confirms; the sold panel
   * shows the committed answer); a short entry says so honestly and sends nothing.
   */
  /**
 * rev 8 (item 8: DIB-502): the SUGGESTED ADD-ONS strip below the ticket. Up to 3 tiles from
   * the server's category-level co-purchase pairing map (0169), items already on the ticket
   * excluded server-side; an EMPTY answer means the strip does not render at all
 * (empty-never-error). Adds go through the EXISTING add path: the engine prices the line;
   * the tile's list price is display only.
   */
  private renderAddonsStrip() {
    if (this.addons.length === 0 || this.ringDone()) return nothing;
    return html`<div class="addons" data-testid="pos-addons">
      <div class="addonshead">Suggested add-ons</div>
      <div class="addongrid">
        ${this.addons.map((a) => {
          const qty = this.addonQty.get(a.product_id) ?? String(a.default_qty);
          return html`<div class="addontile" data-testid="addon-tile" data-sku=${a.sku}>
            <div class="addontop">
              <hos-product-thumb .productId=${a.product_id} .hasImage=${a.has_image} alt="" .size=${44}></hos-product-thumb>
              <div class="addonmain">
                <div class="addonname">${a.name}</div>
                ${a.size_line ? html`<div class="addonsize">${a.size_line}</div>` : nothing}
                ${a.list_price_cents !== undefined
                  ? html`<div class="addonprice">${money(a.list_price_cents)}</div>`
                  : nothing}
              </div>
            </div>
            <div class="addonfoot">
              <input
                class="qty"
                data-testid="addon-qty"
                .value=${qty}
                aria-label="Quantity: ${a.name}"
                @focus=${(e: Event) => (e.target as HTMLInputElement).select()}
                @change=${(e: Event) => this.addonQty.set(a.product_id, (e.target as HTMLInputElement).value)}
              />
              <span class="addonuom">${a.uom}</span>
              <button
                class="addonadd"
                data-testid="addon-add"
                title="Add ${a.name} to the ticket: priced by the engine on the way in."
                @click=${() => void this.addAddon(a)}
              >
                ${drawIcon(iconForId('pencil:add'), 12)} Add
              </button>
            </div>
          </div>`;
        })}
      </div>
    </div>`;
  }

 /** rev 8: one-click add from a suggestion tile: the EXISTING add path with the tile's qty. */
  private async addAddon(a: AddonSuggestion): Promise<void> {
    const qty = cleanQty(Number(this.addonQty.get(a.product_id) ?? a.default_qty));
    const p: PosProduct = {
      id: a.product_id,
      sku: a.sku,
      name: a.name,
      color_family: '',
      application: a.application ?? '',
      sold_by: a.uom,
      base_uom: a.uom,
      is_active: true,
    };
    await this.addProduct(p, qty);
  }

  private renderCashTendered() {
    if (this.payMethod !== 'CASH' || this.ringDone()) return nothing;
    const plan = cashTenderedPlan(this.total(), this.cashTenderedDollars);
    return html`<div class="cashrow">
        <input
          type="number"
          min="0"
          step="0.01"
          inputmode="decimal"
          data-testid="cash-tendered"
          placeholder="Cash tendered (optional, CA$)"
          aria-label="Cash tendered in dollars: optional; leave empty to ring the exact total"
          title="Type what the customer hands you and the change due shows here and on the confirmation. Leave empty to ring the exact total."
          .value=${this.cashTenderedDollars}
          @input=${(e: Event) => (this.cashTenderedDollars = (e.target as HTMLInputElement).value)}
        />
      </div>
      ${this.cashTenderedDollars.trim() !== ''
        ? html`<div class="cashhint" data-testid="cash-change-preview">
            ${!this.hasPricedLine()
              ? html`Change due <span class="num">(not priced)</span>`
              : plan.sendable
                ? html`Change due <span class="num">${money(plan.changeDueCents)} (est.)</span>`
                : 'Less than the total: the sale rings for the exact total; add more or clear the box.'}
          </div>`
        : nothing}`;
  }

  /**
 * rev 6 (item D): the salesperson options: the active-staff roster, with the signed-in operator
   * guaranteed present even when the roster read failed (or the operator is not in it), so the
   * default is always a real, selectable option. Empty-never-error: a roster outage degrades to
   * the one-person list, never a broken select.
   */
  private renderSalespersonOptions() {
    const me = getCurrentUser();
    const roster = [...this.salespeople];
    if (me && me.user_id && !roster.some((s) => s.id === me.user_id)) {
      roster.unshift({ id: me.user_id, name: me.display_name || 'Me (signed in)' });
    }
    return roster.map((s) => html`<option value=${s.id} ?selected=${s.id === this.salespersonId}>${s.name}</option>`);
  }

  private renderCustomerIdentity() {
    const c = this.customer;
    if (c) {
       // HH note: the reference subtracted the balance from the limit here; the core computes
       // credit in HH, so the port asks the host seam (CLOSURE.md names it), already clamped.
      const available = availableCreditCents(c);
      return html`<div class="custcard" data-testid="customer-card">
        <span class="avatar" aria-hidden="true">${customerInitials(c.name)}</span>
        <div class="cmain">
          <div class="cname">${c.name}</div>
          <div class="cline">
            <span class="cacct">${c.account_no}</span>
            <span class="ctag">${accountTypeTag(c.customer_type)}</span>
            ${/* CREDIT HOLD. The server has been computing this for the counter all along:
               standing.go says OnHold exists "so the counter does not have to infer a hold from a
               balance": and nothing on the counter rendered it. The customer could read it on
               their own portal dashboard; the person serving them could not, and found out when
               the tender was refused at the ring, with the whole sale already built.

               A TAG, not a figure, so it sits beside the account type where status lives rather
               than in the row of numbers underneath. Two words, which is the budget for a status
               compound, and the colour is paired with the word because colour never carries
               meaning alone. It is still NOT a refusal: the ring is where a hold is enforced, and
               this card has no banner, no sentence and no call to action by design. */ ''}
            ${this.standing.on_hold
              ? html`<span class="ctag hold" data-testid="cust-on-hold">Credit hold</span>`
              : nothing}
          </div>
          <div class="cfacts">
            <span>Terms <span class="num">${c.payment_terms_days}d</span></span>
            <span>Available (derived) <span class="num avail">${money(available)}</span></span>
            ${c.open_balance_cents > 0
              ? html`<span>Outstanding <span class="num out">${money(c.open_balance_cents)}</span></span>`
              : nothing}
            ${/* THE PAST-DUE FACT (N4). Ryan Bostock starts a counter sale partly because "it also
               gives me an opportunity to ask them to pay their bill": this is the thing that was
               missing when he did. It is one more entry in the same quiet fact row, in the same
               clothes as Terms and Outstanding: no banner, no sentence, no call to action, and
               nothing at all when the account is current. The overdue colour is paired with the
               word "Past due", because colour never carries meaning alone. Whether the sale may
               proceed remains the credit gate's question, not this row's. */ ''}
            ${this.standing.past_due_cents
              ? html`<span data-testid="cust-past-due"
                  >Past due
                  <span class="num out"
                    >${money(this.standing.past_due_cents)}${this.standing.oldest_bucket
                      ? ` · ${this.standing.oldest_bucket}d`
                      : ''}</span
                  >
                </span>`
              : nothing}
          </div>
        </div>
        <button
          class="cchange"
          data-testid="customer-change"
          title="Choose a different customer for this sale"
          @click=${() => this.openPicker()}
        >
          Change
        </button>
      </div>`;
    }
    if (this.walkInConfirmed) {
      return html`<div class="custcard walkin" data-testid="customer-card">
        <span class="avatar" aria-hidden="true">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true">
            <circle cx="12" cy="8" r="3.5"></circle>
            <path d="M5 20c1.4-3.4 3.9-5 7-5s5.6 1.6 7 5"></path>
          </svg>
        </span>
        <div class="cmain">
          <div class="cname">Walk-in customer</div>
          <div class="cline">
            <span>Cash sale: no account on the order</span>
            <span class="ctag">Cash</span>
          </div>
        </div>
        <button
          class="cchange"
          data-testid="customer-change"
          title="Pick an account customer instead"
          @click=${() => this.openPicker()}
        >
          Change
        </button>
      </div>`;
    }
    return html`<div
      class="customerpick"
      role="button"
      tabindex="0"
      title="Select or change the account customer"
      @click=${() => this.openPicker()}
      @keydown=${(e: KeyboardEvent) => (e.key === 'Enter' || e.key === ' ') && this.openPicker()}
    >
      <span class="pickhint">Select customer</span>
      <hos-pos-customer-strip .customer=${this.customer}></hos-pos-customer-strip>
    </div>`;
  }

  /**
 * The modal overlays stay at render()'s top level: OUTSIDE both workspace panels: so a
   * collapse or maximize can never unmount an open dialog mid-interaction.
   */
  private renderOverlays() {
    return html`
      ${/* DIB-506: the picker is branch-fenced like every other read on this counter: the scan
         box (:branchId), the search drop, the hot list, the browse grid and the past-sales lookup
         all take this same property from this same terminal. This mount was the one that did not,
         and the omission 403'd every account search a branch-scoped clerk ran, which made the
         account sale unreachable for the role this screen is for. */ ''}
      ${this.showPicker
        ? html`<hos-pos-customer-picker
            .branchId=${this.branchId}
            .hint=${this.pickerHint}
            @hos-customer-selected=${(e: CustomEvent<PosCustomer | null>) => this.onCustomerSelected(e.detail)}
            @hos-customer-picker-close=${() => {
              this.showPicker = false;
              this.pickerHint = '';
            }}
          ></hos-pos-customer-picker>`
        : nothing}
      ${this.showRelief
        ? html`<hos-pos-relief
            .orderId=${this.ringDone() ? this.doneSale().order_id : ''}
            .subtotalCents=${this.subtotal()}
            @pos-relief-close=${() => (this.showRelief = false)}
            @pos-relief-applied=${(e: CustomEvent<{ outcome: ReliefOutcome; signatureRef: string }>) =>
              this.onReliefApplied(e)}
          ></hos-pos-relief>`
        : nothing}
      ${this.showTillClose
        ? html`<hos-till-close
            .sessionId=${this.tillSessionId}
            .branchId=${this.branchId}
            @till-close-cancel=${() => (this.showTillClose = false)}
            @till-closed=${() => this.onTillClosed()}
          ></hos-till-close>`
        : nothing}
      ${this.showReturn
        ? html`<hos-pos-return
            .tillSessionId=${this.tillSessionId}
            @pos-return-close=${() => (this.showReturn = false)}
          ></hos-pos-return>`
        : nothing}
      ${this.showParked
        ? html`<hos-pos-parked
            .branchId=${this.branchId}
            .info=${this.parkedInfo}
            .openLines=${this.cart.length}
            @pos-parked-close=${() => {
              this.showParked = false;
              void this.refreshParkedCount();
            }}
            @pos-parked-resume=${(e: CustomEvent<{ ticket: ParkedTicket }>) => this.onParkedResume(e.detail.ticket)}
          ></hos-pos-parked>`
        : nothing}
      ${this.pricingDoc
        ? html`<hos-pricing-doc .doc=${this.pricingDoc} @pricing-doc-close=${() => (this.pricingDoc = null)}></hos-pricing-doc>`
        : nothing}
      ${/* ── DIB-434 ── the in-progress guard. Mounted only while it is up, so "connected" is
         "open" for its focus trap, and never mounted at all for an empty till. */ ''}
      ${this.quickGuard
        ? html`<hos-quick-start-guard
            .ticket=${this.liveTicket()}
            ?busy=${this.quickGuardBusy}
            @quick-start-continue=${() => this.onQuickContinue()}
            @quick-start-park=${() => void this.onQuickPark()}
            @quick-start-clear=${() => this.onQuickClear()}
          ></hos-quick-start-guard>`
        : nothing}
      ${/* ── end DIB-434 ── */ ''}
      ${this.showLookup
        ? html`<div class="overlay" role="dialog" aria-modal="true" aria-label="Past sales">
            <div class="sheet">
              <div class="sheethead">
                <span>Past sales</span>
                <button @click=${() => (this.showLookup = false)}>Close</button>
              </div>
              <hos-pos-lookup .branchId=${this.branchId} .tillSessionId=${this.tillSessionId}></hos-pos-lookup>
            </div>
          </div>`
        : nothing}
    `;
  }

  // ── DIB-488: the two line presentations ───────────────────────────────────

  /**
   * The stock-status line under an item (DIB-488 scope 4). All the honesty rules live in
   * _pos-stock.ts (stockStatus); this only paints the answer with the existing status token roles.
 * No glance (yet, or at all) renders NOTHING: absent data is not an error and not a stock claim.
   */
  private renderStockLine(l: CartLine) {
    const g = this.lineStock[l.product.id];
    if (!g) return nothing;
    const st = stockStatus({
      availableBase: g.on_hand?.[this.branchId],
      lowBase: g.low_base?.[this.branchId],
      baseUom: g.base_uom ?? '',
      sellUom: g.sell_uom ?? '',
      sellFactor: g.sell_factor ?? 0,
    });
    if (!st) return nothing;
    return html`<div class="stockline ${st.tone}">${st.label}</div>`;
  }

  /** The shared qty stepper for a cart line (identical behavior in both presentations). */
  private renderQtyStepper(l: CartLine, i: number) {
 // rev 5: the controls carry the PRODUCT in their names: three lines of "Decrease quantity /
    // Quantity / Increase quantity" were indistinguishable to a screen reader (the Remove button
    // on the same row already did this right). Enter in the box hands focus back to the scan box.
    return html`<div class="stepper">
      <button @click=${() => this.setQty(i, l.qty - 1)} aria-label="Decrease quantity: ${l.product.name}">${drawIcon(iconForId('lucide:minus'), 14)}</button>
      <input
        class="qty"
        .value=${String(l.qty)}
        aria-label="Quantity: ${l.product.name}"
        @focus=${(e: Event) => (e.target as HTMLInputElement).select()}
        @click=${(e: Event) => (e.target as HTMLInputElement).select()}
        @keydown=${(e: KeyboardEvent) => this.onQtyKeydown(e)}
        @change=${(e: Event) => this.setQty(i, Number((e.target as HTMLInputElement).value))}
      />
      <button @click=${() => this.setQty(i, l.qty + 1)} aria-label="Increase quantity: ${l.product.name}">${drawIcon(iconForId('pencil:add'), 14)}</button>
    </div>`;
  }

  /**
 * LINES: the D-096 default: one compact row per item (thumbnail, name, SKU, stock-status line,
   * qty stepper, unit, unit price, line total, remove). The engine still owns every number; the
   * full per-line breakdown remains one view-flip away in the Cards presentation.
   */
  /**
 * rev 8 (item 9): one line's Total Weight in lbs, from the glance's DISCOVERED catalogue facts
   * (weight_kg_per_base + the unit meta the stock line already uses). null = cannot be said
   * honestly (no weight on file / no usable conversion / glance not landed) → the column shows an
   * the blank word and the line is excluded from the footer total.
   */
  private lineWeight(l: CartLine): number | null {
    const g = this.lineStock[l.product.id];
    const facts: LineWeightFacts = {
      weightKgPerBase: g?.weight_kg_per_base,
      baseUom: g?.base_uom ?? '',
      sellUom: g?.sell_uom ?? '',
      sellFactor: g?.sell_factor ?? 0,
    };
    return lineWeightLbs(l.qty, l.uom, facts);
  }

 /** rev 8 (item 10): the SERVER's margin answer for line i (absent = no cell content). */
  private lineMarginBps(i: number): number | undefined {
    return this.marginData?.lines?.[i]?.margin_bps;
  }

  private renderLinesTable() {
    // rev 8: the margin column exists only while the role-gated data is present AND un-hidden.
    const m = this.marginVisible();
    const cols = m ? 'withmargin' : '';
 // DIB-512 · the column is 'Weight' (the LINE's own weight): 'Total Weight' is the footer
    // bar's word for the ticket total, and the same words for two different numbers on one
    // screen was the exact duplicate the critique flagged. The container grid owns the tracks
    // (subgrid rows) so every row's columns share one edge.
    return html`<div data-testid="pos-lines" class="linegrid ${cols}">
      <div class="linehead ${cols}" aria-hidden="true">
        <span></span><span>Item</span><span>Qty</span><span>Unit</span><span class="r">Price</span><span class="r">Total</span><span class="r">Weight</span>${m ? html`<span class="r">Margin</span>` : nothing}<span></span>
      </div>
      ${this.cart.map((l, i) => {
        const w = this.lineWeight(l);
        const mb = m ? this.lineMarginBps(i) : undefined;
        return html`<div
          class="linerow ${cols} ${i === this.flashIdx ? 'flash' : ''}"
          data-testid="pos-line-row"
          data-flash=${i === this.flashIdx ? '1' : nothing}
        >
          <hos-product-thumb .productId=${l.product.id} .alt=${l.product.name} .size=${36}></hos-product-thumb>
          <div class="lmain">
            <div class="lname">${l.product.name}</div>
            <div class="lsku">${l.product.sku}</div>
            ${this.renderStockLine(l)}
          </div>
          ${this.renderQtyStepper(l, i)}
          ${/* rev 6 (item B): the unit is its own aligned column (mock parity). */ ''}
          <div class="lunit" data-testid="pos-line-unit">${l.uom}</div>
          ${/* rev 8 (item 3): Price and Total are SEPARATE aligned columns now (concept parity);
             rev 5's honesty stands: an unresolved price says "price pending", never CA$0.00. */ ''}
          ${l.priceMissing
            ? html`<div class="lprice">price pending</div>
                <div class="ltot">(not priced)</div>`
            : html`<div class="lprice" title=${l.priceUom && l.priceUom !== l.uom ? `Priced per ${l.priceUom}` : 'Unit price'}>
                  ${money(l.unitPriceCents)}${l.priceUom && l.priceUom !== l.uom ? ` / ${l.priceUom}` : ''}
                </div>
                <div class="ltot">${money(l.lineTotalCents)}</div>`}
          ${/* rev 8 (item 9): line qty × unit weight, lbs: the explicit blank word when the data cannot say. */ ''}
          <div class="lweight" data-testid="pos-line-weight" title=${w === null ? 'No weight on file for this item' : 'Line weight: quantity × unit weight'}>
            ${w === null ? '(none)' : formatLbs(w)}
          </div>
          ${/* ui3 visual GAP 2: one rule, both windows agree. The margin glance derives its figures
               from the server's own prices, so on a pickup whose prices never answer it still
               returns a percentage for the line: while the line's price has not answered, the cell
               reads the marker, never a stale or derived figure (the building window's rule). */ ''}
          ${m
            ? html`<div class="lmargin" data-testid="pos-line-margin" title="Gross margin: computed server-side">
                ${mb === undefined || l.priceMissing ? '(none)' : formatMargin(mb)}
              </div>`
            : nothing}
          <button class="rm" @click=${() => this.removeLine(i)} title="Remove line" aria-label="Remove ${l.product.name}">
            <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
              <path d="M4 7h16M10 7V5h4v2M6.5 7l1 13h9l1-13M10 11v6M14 11v6"></path>
            </svg>
          </button>
        </div>`;
      })}
    </div>`;
  }

  /**
 * CARDS: the pre-DIB-488 presentation, kept as the alternate (D-096): each line as a block
   * carrying its full engine price breakdown. Markup unchanged from the shipped terminal.
   */
  private renderCardLines() {
    return html`<div data-testid="pos-cards">
      ${this.cart.map(
        (l, i) => html`<div
          class="line ${i === this.flashIdx ? 'flash' : ''}"
          data-flash=${i === this.flashIdx ? '1' : nothing}
        >
          <div class="lrow">
            <div>
              <div class="lname">${l.product.name}</div>
              <div class="lsku">${l.product.sku}</div>
              ${this.renderStockLine(l)}
            </div>
            ${this.renderQtyStepper(l, i)}
            <div class="ltot">${l.priceMissing ? 'price pending' : money(l.lineTotalCents)}</div>
            <button class="rm" @click=${() => this.removeLine(i)} title="Remove line" aria-label="Remove ${l.product.name}">
              <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <path d="M4 7h16M10 7V5h4v2M6.5 7l1 13h9l1-13M10 11v6M14 11v6"></path>
              </svg>
            </button>
          </div>
          ${l.breakdown
            ? html`<hos-price-breakdown .breakdown=${l.breakdown}></hos-price-breakdown>`
            : nothing}
        </div>`,
      )}
    </div>`;
  }

  /**
 * DIB-444: the confirmation moment.
   *
   * What this replaces: `Sold · #SO-TRT-32 · #INV-20 · PAID` as one 13px line in the middle of the
   * utility buttons. It carried three facts, none of them the amount, and announced nothing.
   *
   * What it does NOT change: the ring. `this.ring` and its state machine, the idempotency key, the
 * error branches and every panel below are exactly as they were: this renders the same
   * `SaleResult`, more loudly. `saleConfirmation()` (in _pos.ts) decides what may be CLAIMED, so an
   * account or COD sale cannot be dressed up as money received.
   *
   * role="status" (not alert): a completed sale is not an error, and role="status" announces the
 * headline politely to a screen reader without stealing focus from the product grid: the counter
   * flow must not slow down. Dismissible for the same reason: one click, or ring the next sale.
   */
  private renderSoldPanel(sale: SaleResult) {
    if (this.soldPanelDismissed) return nothing;
    const c = saleConfirmation(sale);
    return html`<div
      class="sold-panel ${c.settled ? '' : 'unsettled'}"
      role="status"
      aria-live="polite"
      data-testid="sold-panel"
    >
      <div class="sold-head">
        <span class="verdict">${c.headline}</span>
        <button
          class="dismiss"
          aria-label="Dismiss the sale confirmation"
          title="Dismiss: the sale is committed either way; the receipt stays reachable from Past sales."
          @click=${() => (this.soldPanelDismissed = true)}
        >
          ${drawIcon(iconForId('pencil:close-m'), 16)}
        </button>
      </div>
      ${/* DIB-512 · one word per surface: a settled sale's amountLabel is empty ('Paid' above the
         number already says what it is); only the load-bearing 'Balance due' caption survives. */ ''}
      ${c.amountLabel ? html`<div class="sold-amount-label">${c.amountLabel}</div>` : nothing}
      <div class="sold-amount">${money(c.amountCents)}</div>
      ${(sale.change_due_cents ?? 0) > 0 || (sale.cash_tendered_cents ?? 0) > 0
        ? html`<div class="sold-change" data-testid="sold-change-due">
            Tendered <span class="tnum">${money(sale.cash_tendered_cents ?? 0)}</span>: change due
            <span class="num">${money(sale.change_due_cents ?? 0)}</span>
          </div>`
        : nothing}
      ${/* DIB-512 · the Status row is deleted: the card's headline + the panel pill already carry
         the pay-state, and the raw PAID / FINALIZED wire words were a third and fourth vocabulary
         for the same fact. A taken deposit keeps its own fact row (that number is load-bearing). */ ''}
      <div class="sold-facts">
        <div class="f"><span class="k">Sale</span><span class="v">${sale.order_no}</span></div>
        <div class="f"><span class="k">Invoice</span><span class="v">${sale.invoice_no}</span></div>
        <div class="f"><span class="k">Tender</span><span class="v">${c.tenderLabel}</span></div>
        ${/* HH note: the simulated card notice rides the rail's own status value, exactly when it
           answers simulated true, on a card sale's confirmation: the reviewed rule, carried onto
           this surface's own confirmation panel (the tender step carries the same word as its
           TEST MODE badge). A rail that answers false, or answers nothing, claims nothing. */ ''}
        ${(sale.tender === 'CARD' || sale.tenders?.some((leg) => leg.method === 'CARD')) && this.cardRail === 'test'
          ? html`<div class="f" data-testid="sold-simulated-notice"><span class="k">Card</span><span class="v">Simulated card: no card was charged</span></div>`
          : nothing}
        ${(sale.deposit_cents ?? 0) > 0
          ? html`<div class="f"><span class="k">Deposit</span><span class="v">${money(sale.deposit_cents ?? 0)}</span></div>`
          : nothing}
        ${sale.po_job_ref
          ? html`<div class="f"><span class="k">PO / Job</span><span class="v">${sale.po_job_ref}</span></div>`
          : nothing}
        ${sale.job_address
          ? html`<div class="f"><span class="k">Job address</span><span class="v">${sale.job_address}</span></div>`
          : nothing}
        ${sale.job_reference
          ? html`<div class="f"><span class="k">Job ref</span><span class="v">${sale.job_reference}</span></div>`
          : nothing}
        ${/* WAVE 2 · finding 16: a receipt panel that reads the operator their own name back.
           The credited salesperson is a real fact when it is SOMEONE ELSE (the rev-6 More-options
           selector exists precisely so a counter person can credit the rep who sold the job), and
           it is chrome when it is the person who just pressed the button: the third printing of
           the same string on one screen, after the shell's top-right and this header's own
           context line. So the row renders on the fact, not on the field. */ ''}
        ${this.creditedElsewhere(sale)
          ? html`<div class="f"><span class="k">Salesperson</span><span class="v">${sale.salesperson_name || this.salespersonNameFor(sale.salesperson_id ?? '')}</span></div>`
          : nothing}
      </div>
      ${c.note ? html`<div class="sold-note">${c.note}</div>` : nothing}
      <div class="sold-actions">
        ${/* DIB-410's receipt action, moved out of the anonymous utility row into the panel where a
           clerk finishing a sale is already looking. */ ''}
        <button
          class="primary"
          data-testid="sold-print"
          ?disabled=${this.receiptBusy}
          title="Opens the printable receipt for this sale in a new window and starts the print dialog."
          @click=${() => this.printSaleReceipt(sale.order_id)}
        >
          ${this.receiptBusy ? 'Loading receipt…' : 'Print receipt'}
        </button>
        <button @click=${this.newSaleClicked}>Next sale</button>
      </div>
    </div>`;
  }

  /**
   * Is the sale credited to somebody OTHER than the person standing at this till?
   *
   * Both halves are honest when the answer is unknowable: a sale with no salesperson at all is not
   * credited elsewhere (nothing to say), and a sale whose id we cannot match to a signed-in
   * operator IS reported, because an unresolvable credit is exactly the case a counter person
   * needs to see rather than have hidden.
   */
  private creditedElsewhere(sale: SaleResult): boolean {
    const id = sale.salesperson_id ?? '';
    const name = sale.salesperson_name ?? '';
    if (!id && !name) return false;
    const me = getCurrentUser();
    if (!me) return true;
    if (id && me.user_id && id === me.user_id) return false;
    if (!id && name && me.display_name && name === me.display_name) return false;
    return true;
  }

 /** rev 6 (item D): a salesperson id's display name, from the roster (or the operator), else the id's head. */
  private salespersonNameFor(id: string): string {
    if (!id) return '';
    const hit = this.salespeople.find((s) => s.id === id);
    if (hit) return hit.name;
    const me = getCurrentUser();
    if (me && me.user_id === id && me.display_name) return me.display_name;
    return id.slice(0, 8);
  }

  private ringBusy(): boolean {
    return this.ring.state === 'busy';
  }
  private ringDone(): boolean {
    return this.ring.state === 'done';
  }
  private doneSale(): SaleResult {
    return this.ring.state === 'done' ? this.ring.sale : ({} as SaleResult);
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'hos-pos-terminal': HosPosTerminal;
  }
}
