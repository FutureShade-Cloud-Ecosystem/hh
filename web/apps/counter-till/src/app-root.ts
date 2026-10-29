// The Counter Till's root element: the thin UI layer the house invariants ask
// for. It renders server and package state and calls client actions. The
// figures the core states (line prices, the ring's and the refund's committed
// totals, the account's facts) arrive through the ported surface's host seams
// (src/ported/ports.ts) or the desk's own client; the surface's one pre-ring
// assembly is the priced lines' subtotal, and the tax and the total are named
// as the ring's own answer, never computed in the browser (the contract gives
// the till no rate).
// What lives in this element is the counter's own state machine: which
// surface is mounted, what is in flight, and what a refusal says.
//
// The surface after sign-in is the ported terminal (hos-pos-terminal), the
// POS surface carried over from the older product and wired to HH's contract:
// the linear flow and the detached multi-card layout are its own two modes.
// The returns desk stays beside it: the refund approval with the manager's
// own credential is HH's ruled surface, unchanged. The entry page (the role
// cards) stays the way in, and the Demo banner, the role's name and sign out
// stay in this chrome.
//
// Rendering is Lit, the design system supplies the tokens and the primitives,
// and the auth package supplies custody through its injected AccountSurface
// seam: this app never touches the token itself, the demo adapter owns the
// mint.
//
// The organisation header is the demo org by construction: the whole app is
// the demo's till, and the staff header provider refuses to send a request
// without it, so no call ever leaves this app unresolved.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { productTileUrl } from "@hardscapehouse/design-system/src/assets/product-tiles/index.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient, ok, staffFeedback, staffWarranty, type Result } from "@hardscapehouse/api-client";
import {
  HHFeedback,
  type FeedbackSender,
} from "@hardscapehouse/design-system/src/feedback.js";
import { announceChromeControls, chromeActionFrom, framedByShell, listenForHostSignOut } from "@hardscapehouse/design-system/src/framed.js";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, servedConsoleConfig, type AppStart, type Custody } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { bandIdentity } from "./band-identity.js";
import { clearFrontDoorHandoff, readFrontDoorHandoff } from "./demo-account.js";
import {
  TillClient,
  houseDay,
  type BranchRef,
} from "./till-client.js";
import { framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import type { ShellHeader } from "@hardscapehouse/shell-chrome/src/shell-header.js";
import "@hardscapehouse/shell-chrome/src/shell-header.js";
import { FRAMED_BAND_APP, framedBandSurface } from "./framed-band.js";
import { readPanesSwapped, writePanesSwapped } from "./pane-memory.js";
import { THEME_LABEL, ThemeController, themeControlLabel, themeStatusText } from "./theme.js";
import { entryStyles, renderEntry, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import "@hardscapehouse/pos-kit/src/branch-chooser.js";
import { clearSessionBranchIds } from "@hardscapehouse/pos-kit/src/session-branches.js";

// The flows this app offers a card for. The flow table is the box's whole
// mint set (the Counter Till's pair plus the personas the roles roadmap
// mints), and an app offers a card only for the roles whose screens live in
// it, the front door's own live-role rule: a tappable card that led into a
// refused surface would be a login to nothing. A flow outside the pair
// still mints through the public route; it has no card on the till.
const TILL_ENTRY_FLOWS = new Set(["sales", "sales-manager"]);
import { ReturnsFlow } from "./returns/flow.js";
import { OneTimeCredential } from "./returns/approval.js";
import { RefundApprovalCapture } from "./returns/capture.js";
import { returnsStyles, renderReturns } from "./views/returns.js";
import { formatMoney } from "./money.js";
import { openBlankWindow, printReceiptInto } from "./ported/receipt-print.js";
import { heldCardFromOwing, holdSlipHtml } from "./pay-balance/hold.js";
import { methodWord } from "./pay-balance/words.js";
import { PayBalanceCapture } from "./pay-balance/capture.js";
import { PayBalanceFlow } from "./pay-balance/flow.js";
import { payBalanceStyles, renderPayBalance } from "./views/pay-balance.js";
// The ported terminal: its import registers the surface's elements, and the
// host wiring (ports.ts) answers its seams.
import "./ported/pos-terminal.js";
import { clearSessionState, setBranches, wirePorts, type AuthUser } from "@hardscapehouse/pos-kit/src/ported/ports.js";
// The Work shell's offline layer (item 8b): the till is READ CACHE ONLY. Its client is built on the no signal
// guard, so every write (the ring, every tender, a refund, a payment, a park) is refused with no signal and
// never queues; its reads fall back to the copy on the device, with the copy's age shown.
import { browserSignal, noSignalFetch, noSignalWebFetch, readThroughFetch, readThroughWebFetch, type CachedRoute, type ReadThroughHooks, type WorkDesk } from "@hardscapehouse/work-offline";
import { apiClientTransport, createTillDesk, dbNameFor, tillRoutes } from "./till-offline.js";
// The strip registers itself; the wiring pass hands it the controller.
import "./offline-strip.js";
import type { CtOfflineStrip } from "./offline-strip.js";

const DEMO_ORG = "hh-demo";

/** The till's own pair of the closed flow table: the flows route answers every
 * live role login the box mints, and a card for a role whose surface the
 * server refuses here would lead into a refusal. The pair is the till's own
 * claim, the same seam as the front door's live role list.
 */
const TILL_FLOWS: ReadonlySet<string> = new Set(["sales", "sales-manager"]);

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    entryStyles,
    returnsStyles,
    payBalanceStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }
      header {
        padding: var(--hh-s-16) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
      }
      .title-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--hh-s-16);
        flex-wrap: wrap;
      }
      h1 {
        font-size: var(--hh-s-16);
        margin: 0;
      }
      .signed-in {
        display: flex;
        align-items: center;
        gap: var(--hh-s-12);
        /* The bar's controls wrap onto a second line at phone width instead of widening the page. */
        flex-wrap: wrap;
        min-width: 0;
      }
      .role-name {
        margin: 0;
        font-weight: 600;
      }
      main {
        padding: var(--hh-s-20);
      }
      /* THE PANELS VIEW IS A SCREEN: when the terminal's counter layout is
         the detached panels, its surface gets the window's height below this
         app's own bar (the surfacebar's 48px plus its 8px margin), so the
         grid inside fills down to the pinned bottom bars and the page below
         (the returns desk) flows on past the fold. The stepper layout keeps
         the natural page height it has always had: the rule is scoped to the
         terminal's own layout pick, which the terminal reflects as an
         attribute when it changes. The Quote Desk's workarea is the
         reference pattern. */
      main.on-till:has(hos-pos-terminal[data-counter-layout="PANELS"]) .till-screen {
        /* The window is a column: the bar takes the height it really has (the badge and the
           wrapped controls move it, so no guessed pixel figure) and the surface takes the rest.
           What follows the screen (a notice, the returns desk) flows on below the fold. */
        display: flex;
        flex-direction: column;
        height: calc(100dvh - var(--band-h, 61px));
        box-sizing: border-box;
      }
      /* The screen is the window, so the main's own foot padding would push the page 20px past it:
         the Panels screen carries none (the stepper keeps its breathing room under a long page). */
      main.on-till:has(hos-pos-terminal[data-counter-layout="PANELS"]) {
        padding-bottom: 0;
      }
      .surface[data-hidden="true"] {
        display: none !important;
      }
      /* Returns is its own screen: it takes what the bar leaves and scrolls inside itself. */
      .till-screen > .returns {
        flex: 1 1 0;
        min-height: 0;
        overflow-y: auto;
        margin-top: 0;
        padding-top: var(--hh-s-16);
      }
      /* The Returns link wears the bar's button face (an anchor takes none of the button rules). */
      .nav-link {
        display: inline-flex;
        align-items: center;
        text-decoration: none;
        font-family: inherit;
        font-size: 11px;
        line-height: normal;
        padding: 7px 12px;
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-control);
        color: var(--hh-text-dim);
        background: transparent;
      }
      .nav-link:hover {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      .nav-link:focus-visible {
        outline: 2px solid var(--hh-focus-ring);
        outline-offset: 2px;
      }
      .till-screen > .surfacebar {
        flex: none;
      }
      main.on-till:has(hos-pos-terminal[data-counter-layout="PANELS"]) .till-screen > .surface {
        flex: 1 1 0;
        min-height: 0;
      }
      /* The terminal paints its own full-bleed chrome; the app's padding would
         letterbox it, so the mounted surface sits edge to edge. */
      .surface {
        margin: 0 calc(-1 * var(--hh-s-20));
      }
      /* The mounted surface's shell bar: HH's content (the demo banner, the
         role, the pop-out, sign out) in the reference's own top-bar place,
         above the till, so the carried demo marker is on screen while a sale
         is rung. */
      .surfacebar {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--hh-s-16);
        flex-wrap: wrap;
        padding: var(--hh-s-12) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
        margin: 0 calc(-1 * var(--hh-s-20)) var(--hh-s-16);
      }
      /* UI-3 (visual NOTE 3): the bar is on the till's own compact scale: a bar of about 48px
         (the reference shell bar's), the till's small-chrome button face and type size, and a
         one-line banner that stays visible and legible. The design system's 44px control floor
         is for the entry page; this bar's controls take the till's chrome role. */
      main.on-till {
        padding-top: 0;
        /* The one band takes 56px above the bar, so the stepper's tallest state at 1440x900 gives
           the page's foot breathing room back (the counter PC's window is the target). */
        padding-bottom: var(--hh-s-8);
      }
      .surfacebar {
        min-height: 36px;
        padding: var(--hh-s-4) var(--hh-s-20);
        margin-bottom: var(--hh-s-4);
        gap: var(--hh-s-12);
      }
      /* The entry page's header takes the same compact scale (visual NOTE 2): one row of about 48px,
         the banner beside the title and the chrome buttons, the same small button face. */
      header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        flex-wrap: wrap;
        gap: var(--hh-s-12);
        min-height: 48px;
        box-sizing: border-box;
        padding: var(--hh-s-8) var(--hh-s-20);
      }
      header .title-row {
        gap: var(--hh-s-12);
        margin-left: auto;
      }
      /* The till's header badge: same mark and size as the front door's lockup,
         at the left edge before the Demo pill. Gold is a fill, never an ink. */
      header .brand,
      .surfacebar .brand {
        display: flex;
        align-items: center;
        gap: 10px;
        min-width: 0;
      }
      header .brand-mark,
      .surfacebar .brand-mark {
        width: 30px;
        height: 30px;
        flex: none;
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
      }
      header .brand-mark svg,
      .surfacebar .brand-mark svg {
        width: 100%;
        height: 100%;
        display: block;
      }
      header .brand-names,
      .surfacebar .brand-names {
        display: flex;
        flex-direction: column;
        gap: 1px;
        min-width: 0;
      }
      header .brand-product,
      .surfacebar .brand-product {
        font-size: 16px;
        font-weight: 700;
        letter-spacing: -0.02em;
        line-height: 1.05;
      }
      header .brand-product .accent,
      .surfacebar .brand-product .accent {
        color: var(--hh-accent-text);
      }
      header .brand-sub,
      .surfacebar .brand-sub {
        font-size: 10px;
        font-weight: 600;
        letter-spacing: 0.055em;
        line-height: 1.2;
        color: var(--hh-text-dim);
        white-space: nowrap;
      }
      header .signed-in {
        margin: 0;
        gap: var(--hh-s-8);
      }
      header .role-name {
        font-size: 13px;
      }
      header button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        color: var(--hh-text-dim);
      }
      header button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      header .demo-banner {
        margin: 0;
        padding: var(--hh-s-4) var(--hh-s-12);
        align-items: center;
        gap: var(--hh-s-8);
      }
      header .demo-banner .word,
      header .demo-banner .copy {
        font-size: 12px;
        line-height: 1.3;
      }
      header h1 {
        font-size: 15px;
      }
      .surfacebar .signed-in {
        margin: 0;
        gap: var(--hh-s-8);
      }
      .surfacebar .role-name {
        font-size: 13px;
      }
      .surfacebar button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        color: var(--hh-text-dim);
      }
      .surfacebar button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      .surfacebar .demo-banner {
        margin: 0;
        padding: var(--hh-s-4) var(--hh-s-12);
        align-items: center;
        gap: var(--hh-s-8);
      }
      .surfacebar .demo-banner .word,
      .surfacebar .demo-banner .copy {
        font-size: 12px;
        line-height: 1.3;
      }
      .chrome-row {
        margin: var(--hh-s-16) 0 0;
      }
      .visually-hidden {
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
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  /** The custody the served config chose: the demo controller as always, or this page recovering its own session (boot-custody.ts). */
  readonly #custody: Custody;
  readonly #tillClient: TillClient;
  /** The staff client with no guard: only the offline store's pull rides it (the till pushes nothing). */
  readonly #syncClient: ApiClient;
  /** The device's connection, read each time it is asked. */
  readonly #signal = browserSignal();
  /** The Work shell's read cache for the signed in person; null before the till is up and after sign out. */
  #offline: WorkDesk | null = null;
  /** The theme choice: Auto follows the system, Light and Brand pin it; tab memory only (see theme.ts). */
  readonly #theme = new ThemeController(() => this.requestUpdate());
  /** What a screen reader hears after a Theme press: focus stays on the button, so the new name is not re-read. */
  #themeStatus = "";
  /** Row 976: the till's layout (the terminal reports it) and whether the two panes are swapped. */
  #counterLayout = "";
  #panesSwapped = readPanesSwapped();
  /** Round 7: this window is a pop-out (opened with ?popout=<flow>): it offers no Pop out of its own. */
  readonly #isPopout = new URLSearchParams(window.location.search).get("popout") !== null;
  /** The layout the opener was in, carried on the pop-out's address (a layout word, never a credential). */
  readonly #layoutHint = ((): string => {
    const hint = new URLSearchParams(window.location.search).get("layout");
    return hint === "PANELS" || hint === "STEPPER" ? hint : "";
  })();
  #flowsState: FlowsState = { kind: "loading" };
  
  /** The one flows read this element makes; the pop-out's tap waits for it. */
  #flowsLoaded: Promise<void> = Promise.resolve();
  #busy = false;
  #message: string | null = null;
  /** The role's label from the flow table, held for the chrome and the desk's clerk words. */
  #till: { label: string } | null = null;
  /** The flow card this session was minted from: the pop-out's address names it. */
  #activeFlow: string | null = null;
  /** The user the till's own mint was for: custody is checked against it. */
  #clerkUserId: string | null = null;
  /** The role code the clerk's mint stated, for the receipt's display words. */
  #clerkRoleCode: string | null = null;
  /**
   * The mint's own IdentityUser block, in the ported surface's session shape:
   * the facts the terminal reads (the role's name, the tier and scope behind
   * the platform-admin gate's shape), handed over unchanged from the mint.
   */
  #clerkIdentity: AuthUser | null = null;
  readonly #returns: ReturnsFlow;
  readonly #payBalance: PayBalanceFlow;
  #branch: BranchRef | null = null;
  /**
   * The mounted surface. The entry page (the cards) is the way in; once a
   * mint is adopted and the branch the login reaches is resolved, the ported
   * terminal IS the till, in both its modes (the linear flow and the detached
   * multi-card layout, the terminal's own choice to switch).
   */
  #surface: "entry" | "branch" | "terminal" = "entry";
  /** The granted branches the in desk chooser offers while the surface is "branch"; empty otherwise. */
  #branchChoices: BranchRef[] = [];
  /** Whether the start that stopped to ask came from the front door's handoff, so the re-start keeps its failure rules. */
  #branchAskedFromHandoff = false;
  /**
   * Row 955: which surface of the till is up. The sale is the till; Returns is its own surface at
   * `#/returns`, reached by the Returns link in the bar (a plain anchor: no script writes the
   * address). The sale stays mounted while Returns is up, so a ticket in progress is never lost.
   */
  #route: "sale" | "returns" | "pay-balance" = "sale";
  readonly #onHashChange = (): void => {
    this.#route = this.#readRoute();
    this.#openPayBalanceOnOrder();
    this.requestUpdate();
  };

  /** `#/pay-balance?order=<number>` opens the surface on that order (the held card's action). */
  #openPayBalanceOnOrder(): void {
    if (this.#route !== "pay-balance") return;
    const query = new URL(window.location.href).hash.split("?")[1] ?? "";
    const orderNo = new URLSearchParams(query).get("order");
    if (orderNo !== null && orderNo !== "" && orderNo !== this.#payBalance.term) void this.#payBalance.openOn(orderNo);
  }

  #readRoute(): "sale" | "returns" | "pay-balance" {
    const hash = new URL(window.location.href).hash;
    if (hash.startsWith("#/returns")) return "returns";
    return hash.startsWith("#/pay-balance") ? "pay-balance" : "sale";
  }
  /** A refusal in words for the chrome while the terminal is mounted (the pop-out's, say). */
  #chromeMessage: string | null = null;

  constructor() {
    super();
    // The till's transport, wrapped so the refund approval refusal's body
    // (the amount and the approver roles beside the envelope) reaches the
    // returns flow: the generated client carries the error string alone.
    const capture = new RefundApprovalCapture();
    // The demo adapter's own client carries no staff bearer: the demo routes
    // are pre-auth by design (the tap-a-role sign-in), and the served gate
    // holds the pages and the demo mint behind basic authentication, where a
    // request already carrying an Authorization header of the wrong kind is
    // refused. The adapter's asks go out bare of a credential, the browser
    // answers the gate's challenge the way it answered the page, and the org
    // header still rides every call the app makes.
    const demoClient = new ApiClient({
      baseUrl: "",
      fetch: capture.fetch,
      headers: async () => ({ "X-Org": DEMO_ORG }),
    });
    this.#account = new DemoAccount({ client: demoClient });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    this.#custody = bootCustody(servedConsoleConfig(), { sessions: this.#sessions, demoAccount: this.#account, demoOrg: DEMO_ORG });
    this.#controller = this.#custody.controller;
    const staffHeaders = authHeaderProvider({
      callerClass: "staff",
      sessions: this.#sessions,
      activeOrg: () => this.#custody.org,
    });
    // The pay balance refusal's money fields ride the same transport: one more wrapper, same rule.
    const payBalanceCapture = new PayBalanceCapture(capture.fetch);
    // The Work shell's guard: a write with no signal is refused before it dials (never queued: money never
    // queues), and a read the device holds a copy of is answered from it when the network does not answer.
    const routes: CachedRoute[] = tillRoutes(() => this.#offline?.reader() ?? null);
    const hooks: ReadThroughHooks = {
      onCached: (words) => this.#offline?.hooks.onCached?.(words),
      onLive: () => this.#offline?.hooks.onLive?.(),
    };
    const client = new ApiClient({
      baseUrl: this.#custody.coreBaseUrl,
      fetch: noSignalFetch(readThroughFetch(payBalanceCapture.fetch, this.#signal, routes, hooks), this.#signal),
      headers: staffHeaders,
    });
    this.#syncClient = new ApiClient({ baseUrl: this.#custody.coreBaseUrl, headers: staffHeaders });
    // The feedback widget's sender rides the staff client: the feedback
    // route is staff guarded and the till's own custody holds the bearer.
    this.#feedbackSender = async (request) => {
      const answer = await staffFeedback.feedbackCreateMultipart(client, {
        type: request.type,
        message: request.message,
        screen: request.screen,
        url: request.url,
        user_agent: request.userAgent,
        images: request.images.map((image) => image.blob),
      });
      if (answer.ok) {
        return { ok: true, issueNumber: answer.value.issue_number ?? "" };
      }
      return { ok: false, error: answer.error };
    };
    // The approver's client exists for the one credit note call that may carry the approver's credential:
    // its provider takes the token out of the one time slot during that send, and taking is dropping, so
    // the token rests nowhere before and survives nothing after. The clerk's client is a different
    // instance and never carries it. Nothing here touches storage or logs.
    const approverCredential = new OneTimeCredential();
    const approverClient = new ApiClient({
      baseUrl: this.#custody.coreBaseUrl,
      // The credit note is a money write: it is refused with no signal like every other.
      fetch: noSignalFetch(undefined, this.#signal),
      headers: async () => {
        const headers = await staffHeaders();
        const token = approverCredential.take();
        if (token !== null) {
          headers["X-Approver-Authorization"] = `Bearer ${token}`;
        }
        return headers;
      },
    });
    this.#returns = new ReturnsFlow({
      client,
      approverClient,
      account: this.#account,
      credential: approverCredential,
      clerk: () => {
        const session = this.#sessions.current;
        if (session === null) return null;
        return {
          name: session.user.name,
          roleCode: this.#clerkRoleCode,
          roleLabel: this.#till?.label ?? null,
        };
      },
      capture,
      searchProducts: async (query) => {
        const found = await this.#tillClient.searchProducts(query);
        return found.ok
          ? { ok: true, value: found.value.map((row) => ({ productId: row.productId, name: row.name, soldBy: row.soldBy })) }
          : found;
      },
      branchId: () => this.#branch?.id ?? null,
      onChange: () => this.requestUpdate(),
    });
    this.#payBalance = new PayBalanceFlow({
      client,
      capture: payBalanceCapture,
      branchId: () => this.#branch?.id ?? null,
      onChange: () => this.requestUpdate(),
    });
    this.#tillClient = new TillClient(client);
    // The ported surface's host wiring: one door, wired once, reading custody
    // through this element's own closures so the bearer never rests in a
    // second place. The org header is the demo org by construction (the same
    // provider the staff client carries), and the requested-date default is
    // the house zone's day, the conversion the sale's ring already rides.
    wirePorts({
      headers: staffHeaders,
      houseDay: () => houseDay(),
      user: () => this.#clerkIdentity,
      token: () => this.#sessions.current?.token ?? "",
      // The drawn category tiles ship in every build (decision log row 1057): a product with no
      // picture shows its category's tile for every dealer, and keeps the stone-stack glyph only
      // where the tiles do not know the category (test/product-tiles-build.test.mjs).
      productTile: productTileUrl,
      baseUrl: this.#custody.coreBaseUrl,
      // The ported surface's request door: the ring and every tender go through it, so the guard rides it too.
      fetch: noSignalWebFetch(readThroughWebFetch(undefined, this.#signal, routes, hooks), this.#signal),
    });
  }

  /** Framed by the Work shell: the host asks this app to run its own sign out when the person switches role (item 8e); standalone, nothing listens. */
  #stopHostSignOut: (() => void) | null = null;

  /** The dealer's name for the band, from the tenant config (one cached read a session); empty until it lands. */
  #dealerName = "";

  connectedCallback(): void {
    super.connectedCallback();
    void readDealerName().then((name) => {
      if (name !== this.#dealerName) {
        this.#dealerName = name;
        this.requestUpdate();
      }
    });
    this.#stopHostSignOut = listenForHostSignOut(() => this.#signOut());
    this.#route = this.#readRoute();
    window.addEventListener("hashchange", this.#onHashChange);
    // Framed by the Work shell the till wears no band: the screen is the whole window.
    if (this.#framed) this.style.setProperty("--band-h", "0px");
    if (this.#framed) {
      window.addEventListener("message", this.#onChromeAction);
      announceChromeControls(window, ["theme", "feedback"]);
    }
    // The kits' wiring rule, carried for this app's own templates: listen on
    // the HOST and read the control off composedPath(), never @click in a
    // template and never event.target.closest, because the shadow boundary
    // retargets the target to this element (the design system's README holds
    // both halves). Click is composed, so the host hears it.
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("hh-branch-chosen", this.#onBranchChosen);
    // The terminal reports which layout it is in (composed, so the host hears it): the design
    // system's position gate allows no @ binding here, so the listener lives in code.
    this.addEventListener("pos-counter-layout", this.#onCounterLayout as EventListener);
    // The returns desk's fields ride the same host delegation: one input
    // listener on the host, the control read off composedPath() by its
    // data-field name, so a re-render strands no listener. The inputs are
    // uncontrolled: the DOM holds what was typed, the flow hears every edit
    // here, and a draft's fresh mount shows the flow's own defaults.
    this.addEventListener("input", this.#onHostInput);
    this.addEventListener("keydown", this.#onHostKey);
    // The system's light setting is honoured from the first paint; the Theme
    // control in the account area cycles Auto, Light and Brand.
    this.#theme.start();
    // Console mode: this page recovers its own session (the pop-out included: it opens from this
    // tab, so it carries the same recovery) and the demo cards never apply.
    if (this.#custody.mode === "console") {
      this.#custody.begin("counter-till", (outcome) => this.#onRecovered(outcome));
      return;
    }
    // The flows read happens once; the pop-out, when this window is one,
    // chains on the same answer rather than asking the route twice.
    this.#flowsLoaded = this.#loadFlows();
    // The pop-out: a second window opened at this app with ?popout=<flow>
    // signs itself in through the same public cards route, the same tap a
    // person would make, and lands directly on the terminal. The flow name is
    // the only thing that rides the address: never a token, never a
    // credential (this app's custody is memory per tab by design, and its
    // committed sink gate refuses every storage and channel path a handshake
    // would dial). The flow table answers first (its label is the role's name
    // in the chrome), then the flow runs. A window that is not a pop-out,
    // on a demo build, takes the front door's handoff instead: the mint the
    // front door held for the box lands signed in, no second picker. The
    // seam rule (decision log row 893) keeps that read a demo seam: Vite
    // substitutes MODE at build time, so this branch exists only in a build
    // that asked for the demo mode, and a default build carries no adopt
    // path at all (test/no-default-adopt.test.mjs reads the built files).
    const popped = new URLSearchParams(window.location.search).get("popout");
    if (typeof popped === "string" && popped !== "") {
      void this.#flowsLoaded.then(() => this.#chooseFlow(popped, true));
    } else if (import.meta.env.MODE === "demo") {
      void this.#adoptFromFrontDoor();
    }
  }

  /**
   * The front door's handoff (HH-DEMO-1 D2 polish, item 6): the front door
   * mints the demo session and holds it for the box under its own session
   * storage key; when this app boots from a module tile's navigation,
   * the record is read and handed to AuthController.adopt (custody takes the
   * token into private memory and rewrites the display mirror itself). The
   * key stays in the tab until Sign out or Switch role (decision log row
   * 1040), so a reload, a second visit or a visit after the Quote Desk keeps
   * the session; a record that fails a read is dropped. The clerk facts the chrome and the receipt
   * read ride the same record (the front door validated the mint already;
   * this read validates the record again before custody sees any of it). An
   * absent or unusable record is the ordinary direct visit; the entry page
   * with the cards comes up, the till's ordinary way in. A pop-out window
   * skips this: its own ?popout flow signs in by the cards route above. The
   * storage touch rides the auth package's throw-safe mirror, this app's
   * sink gate's one sanctioned accessor.
   */
  async #adoptFromFrontDoor(): Promise<void> {
    if (this.#sessions.current !== null) return;
    const read = readFrontDoorHandoff();
    if (read.kind === "absent") {
      this.#toFrontDoor();
      return;
    }
    if (read.kind === "unusable") {
      clearFrontDoorHandoff();
      clearSessionBranchIds();
      this.#toFrontDoor();
      return;
    }
    const handed = read.session;
    const adopted = this.#controller.adopt({
      token: handed.token,
      tokenExpiresAtMs: handed.tokenExpiresAtMs,
      user: handed.user,
    });
    if (!adopted) {
      clearFrontDoorHandoff();
      clearSessionBranchIds();
      this.#toFrontDoor();
      return;
    }
    this.#clerkUserId = handed.user.userId;
    this.#clerkRoleCode = handed.roleCode;
    this.#clerkIdentity = {
      user_id: handed.user.userId,
      email: handed.user.email,
      display_name: handed.user.name,
      role_code: handed.roleCode,
      role_name: handed.roleName,
      tier: handed.tier,
      scope: handed.scope,
    };
    this.#till = { label: handed.roleLabel };
    this.#activeFlow = handed.flow;
    this.requestUpdate();
    await this.#initTill(true);
  }

  /**
   * Console mode: this page recovered its own session, or could not. The person's role comes from
   * the exchange (not a flow table): the chrome, the receipt's clerk and the ported surface read it.
   * An unavailable page shows the entry's unavailable state while the custody retries; a session the
   * front door must settle goes back there.
   */
  #onRecovered(outcome: AppStart): void {
    if (outcome.kind === "ready") {
      const entry = outcome.entry;
      this.#clerkUserId = entry.userId;
      this.#clerkRoleCode = entry.roleCode;
      this.#clerkIdentity = {
        user_id: entry.userId,
        email: entry.email,
        display_name: entry.name,
        role_code: entry.roleCode,
        role_name: entry.roleLabel,
        tier: entry.tier,
        scope: entry.scope,
      };
      this.#till = { label: entry.roleLabel };
      this.#activeFlow = entry.flow;
      this.requestUpdate();
      void this.#initTill(true);
      return;
    }
    if (outcome.kind === "front-door" && outcome.why === "ended") {
      void this.#signOut();
      return;
    }
    this.#flowsState = { kind: "unavailable", status: null };
    if (outcome.kind === "front-door") this.#custody.toFrontDoor();
    this.requestUpdate();
  }

  /**
   * Console mode, the branch did not resolve. The person's console session is theirs across every app, so a
   * core blip must not end it: an ending refusal (401, 403) leaves cleanly; anything else shows the
   * unavailable entry and the custody tries the start again on its backoff.
   */
  async #consoleStartFailed(status: number | undefined): Promise<void> {
    if (status === 401 || status === 403) {
      await this.#custody.leave();
      this.#flowsState = { kind: "unavailable", status: null };
      this.requestUpdate();
      return;
    }
    this.#flowsState = { kind: "unavailable", status: null };
    this.requestUpdate();
    this.#custody.retry("counter-till", (outcome) => this.#onRecovered(outcome));
  }

  /**
   * A demo build that finds no session sends the person to the front door to pick a role (decision
   * log row 1040). Only where the till is served under its own prefix (the box: BASE_URL is /till/):
   * at the origin root the till's own entry cards are the way in, and the front door is not there.
   */
  #toFrontDoor(): void {
    if (import.meta.env.BASE_URL !== "/") window.location.assign("/");
  }

  #bandWatch: ResizeObserver | null = null;

  disconnectedCallback(): void {
    this.#stopHostSignOut?.();
    this.#stopHostSignOut = null;
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("hh-branch-chosen", this.#onBranchChosen);
    this.removeEventListener("pos-counter-layout", this.#onCounterLayout as EventListener);
    this.removeEventListener("input", this.#onHostInput);
    this.removeEventListener("keydown", this.#onHostKey);
    window.removeEventListener("hashchange", this.#onHashChange);
    window.removeEventListener("message", this.#onChromeAction);
    this.#theme.stop();
    this.#bandWatch?.disconnect();
    this.#bandWatch = null;
    super.disconnectedCallback();
  }

  /** Item 9.8: the hold slip prints through the same seam the receipt prints through. */
  #printHoldSlip(): void {
    const order = this.#payBalance.order;
    if (order === null) return;
    const card = heldCardFromOwing(order, this.#payBalance.hold, methodWord);
    const out = printReceiptInto(
      openBlankWindow(),
      holdSlipHtml(
        { orderNo: card.orderNo, customerName: card.customerName, balanceDueCents: card.balanceDueCents, holdWords: card.holdWords },
        formatMoney,
      ),
    );
    this.#payBalance.notice = out.ok ? null : out.message;
    this.requestUpdate();
  }

  /** Enter in the Pay balance find field finds, the way the Find button does. */
  readonly #onHostKey = (event: KeyboardEvent): void => {
    if (event.key !== "Enter") return;
    const path = event.composedPath();
    const walkIn = path.find((node): node is Element => node instanceof Element && node.getAttribute("data-field") === "returns-walkin-number");
    if (walkIn !== undefined) {
      event.preventDefault();
      void this.#returns.findWalkInSale();
      return;
    }
    const control = path.find((node): node is Element => node instanceof Element && node.getAttribute("data-field") === "pay-balance-term");
    if (control === undefined) return;
    event.preventDefault();
    void this.#payBalance.find();
  };

  readonly #onHostInput = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (!(control instanceof HTMLInputElement) && !(control instanceof HTMLTextAreaElement)) return;
    const field = control.getAttribute("data-field");
    if (field === "pay-balance-term" && control instanceof HTMLInputElement) this.#payBalance.setTerm(control.value);
    if (field === "returns-customer" && control instanceof HTMLInputElement) this.#returns.setTerm(control.value);
    if (field === "returns-product" && control instanceof HTMLInputElement) this.#returns.setProductTerm(control.value);
    if (field === "returns-replacement-qty" && control instanceof HTMLInputElement) {
      this.#returns.setReplacementQty(control.getAttribute("data-product") ?? "", control.value);
    }
    if (field === "returns-walkin-number" && control instanceof HTMLInputElement) this.#returns.setWalkInNumber(control.value);
    if (field === "returns-filter" && control instanceof HTMLInputElement) this.#returns.setFilter(control.value);
    if (field === "returns-qty" && control instanceof HTMLInputElement) {
      const line = control.getAttribute("data-line") ?? "";
      // A refused quantity (above what is open) leaves the draft as it was; the field is brought back
      // to the draft's own answer after the render (#syncReturnsFields), so a refused number never
      // sits on the screen as if it rode.
      this.#returns.setQty(line, control.value);
    }
  };

  readonly #onCounterLayout = (event: CustomEvent<{ layout: string }>): void => {
    this.#counterLayout = event.detail.layout;
    this.requestUpdate();
  };

  /** The feedback widget's sender; built once beside the staff client. */
  #feedbackSender: FeedbackSender = async () => ({ ok: false, error: "not ready" });

  /** The Theme control's press: the shell's account menu asks for it too, when this till is framed. */
  #pressTheme(): void {
    this.#theme.cycle();
    this.#themeStatus = themeStatusText(this.#theme.choice);
  }

  /** The Feedback control's press: the shell's account menu asks for it too, when this till is framed. */
  #openFeedback(): void {
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback) widget.show();
  }

  /**
   * Framed by the Work shell (item 8d, row 1520: one header band): the till wears no band of its own, because
   * the shell's band carries the app selector, the Demo badge, Theme, Feedback and the account. Read once, at
   * boot, from the frame's address; a till opened on its own never takes this path.
   */
  readonly #framed = framedByShell();

  /** The shell asked the till to press Theme or Feedback, which it carries for a framed app. */
  readonly #onChromeAction = (event: MessageEvent): void => {
    const action = chromeActionFrom(event, window);
    if (action === "theme") this.#pressTheme();
    if (action === "feedback") this.#openFeedback();
  };

  /** The in desk chooser wrote the one branch to the session key: start the till again, which now reads it. */
  readonly #onBranchChosen = (): void => {
    this.#branchChoices = [];
    this.#surface = "entry";
    void this.#initTill(this.#branchAskedFromHandoff);
  };

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const flow = control.getAttribute("data-flow");
    if (control.getAttribute("data-action") === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
    if (control.getAttribute("data-action") === "swap-panes") {
      this.#panesSwapped = !this.#panesSwapped;
      writePanesSwapped(this.#panesSwapped);
      this.requestUpdate();
    }
    if (control.getAttribute("data-action") === "theme") this.#pressTheme();
    if (control.getAttribute("data-action") === "feedback") this.#openFeedback();
    if (control.getAttribute("data-action") === "sign-out") {
      void this.#signOut();
    }
    if (control.getAttribute("data-action") === "pop-out" && typeof flow === "string" && flow !== "") {
      this.#popOut(flow);
    }
    if (control.getAttribute("data-action") === "pay-balance-find") {
      void this.#payBalance.find();
    }
    if (control.getAttribute("data-action") === "pay-balance-pick") {
      void this.#payBalance.pick(control.getAttribute("data-order") ?? "");
    }
    if (control.getAttribute("data-action") === "pay-balance-method") {
      this.#payBalance.chooseMethod(control.getAttribute("data-method") ?? "");
    }
    if (control.getAttribute("data-action") === "pay-balance-take") {
      void this.#payBalance.take(formatMoney);
    }
    if (control.getAttribute("data-action") === "held-print-slip") {
      this.#printHoldSlip();
    }
    if (control.getAttribute("data-action") === "pay-balance-another") {
      this.#payBalance.another();
    }
    const returnsAction = control.getAttribute("data-action") ?? "";
    const returnsLine = control.getAttribute("data-line") ?? "";
    if (returnsAction === "returns-customer") void this.#returns.chooseCustomer(control.getAttribute("data-customer") ?? "");
    if (returnsAction === "returns-qty-up") this.#returns.stepQty(returnsLine, 1);
    if (returnsAction === "returns-qty-down") this.#returns.stepQty(returnsLine, -1);
    if (returnsAction === "returns-disposition") {
      this.#returns.setDisposition(returnsLine, control.getAttribute("data-disposition") ?? "");
    }
    if (returnsAction === "returns-add-replacement") this.#returns.addReplacement(control.getAttribute("data-product") ?? "");
    if (returnsAction === "returns-replacement-up") this.#returns.stepReplacement(control.getAttribute("data-product") ?? "", 1);
    if (returnsAction === "returns-replacement-down") this.#returns.stepReplacement(control.getAttribute("data-product") ?? "", -1);
    if (returnsAction === "returns-swap-tender") this.#returns.chooseSwapTender(control.getAttribute("data-tender") ?? "");
    if (returnsAction === "returns-ring-swap") void this.#returns.ringSwap();
    if (returnsAction === "returns-walkin") this.#returns.toggleWalkIn();
    if (returnsAction === "returns-walkin-find") void this.#returns.findWalkInSale();
    if (returnsAction === "returns-continue") void this.#returns.toRefund();
    if (returnsAction === "returns-method") this.#returns.chooseMethod(control.getAttribute("data-method") ?? "");
    if (returnsAction === "returns-submit") void this.#returns.submit();
    if (returnsAction === "returns-approve") void this.#returns.openApprovalDialog();
    if (returnsAction === "returns-approve-cancel") this.#returns.closeApprovalDialog();
    if (returnsAction === "returns-approver-flow") {
      const approverFlow = control.getAttribute("data-flow");
      if (approverFlow !== null && approverFlow !== "") void this.#returns.approveWithFlow(approverFlow);
    }
    if (returnsAction === "returns-back-to-lines") this.#returns.toLines();
    if (returnsAction === "returns-another") this.#returns.startAnother();
  };

  /**
   * Open the till on a second window: the same app, the same flow card's
   * sign-in, the same drawer (the till session is the branch's, server-side).
   * A blocked pop-up is a real counter event; it lands in words, never a
   * silent no-click.
   */
  #popOut(flow: string): void {
    // The opener's layout rides along so the window keeps the layout the person was using; the
    // server's workspace row may not have it yet (a save still on its way, a second mint).
    const layout = this.#counterLayout === "PANELS" || this.#counterLayout === "STEPPER" ? `&layout=${this.#counterLayout}` : "";
    const popped = window.open(
      `${import.meta.env.BASE_URL}?popout=${encodeURIComponent(flow)}${layout}`,
      "ct-popout",
      `popup=yes,width=${window.outerWidth},height=${window.outerHeight}`,
    );
    if (popped === null) {
      this.#chromeMessage =
        "The browser blocked the second window: allow pop-ups for this till and press Pop out again.";
      this.requestUpdate();
    } else {
      popped.focus();
    }
  }

  async #loadFlows(): Promise<void> {
    const result = await this.#account.listFlows();
    this.#flowsState = result.ok
      ? { kind: "ready", flows: result.value.filter((account) => TILL_FLOWS.has(account.flow ?? "")) }
      : { kind: "unavailable", status: result.status ?? null };
    this.requestUpdate();
  }

  /**
   * The flows state as the entry view renders it: the table filtered to the
   * till's own pair (TILL_ENTRY_FLOWS). The unfiltered answer stays held for
   * the label lookups and the pop-out, which name flows the cards need not
   * offer; only the offering is the app's claim about whose screens live here.
   */
  #entryFlowsState(): FlowsState {
    if (this.#flowsState.kind !== "ready") return this.#flowsState;
    return {
      kind: "ready",
      flows: this.#flowsState.flows.filter((flow) => flow.flow !== undefined && TILL_ENTRY_FLOWS.has(flow.flow)),
    };
  }

  async #chooseFlow(flow: string, samePerson = false): Promise<void> {
    if (this.#busy || flow === "") return;
    this.#busy = true;
    this.#message = null;
    this.requestUpdate();
    const minted = await this.#account.chooseFlow(flow);
    // A person signing in from the cards is a change of person: the last person's branch choice goes. A pop-out
    // window is the same person on a second window, and keeps the choice its opener was given.
    if (!samePerson) clearSessionBranchIds();
    if (!minted.ok) {
      this.#busy = false;
      this.#message = `Sign-in did not go through. ${minted.error}`;
      this.requestUpdate();
      return;
    }
    const adopted = this.#controller.adopt(minted.value);
    this.#busy = false;
    if (!adopted) {
      this.#message = "Sign-in did not go through. Try the role again.";
      this.requestUpdate();
      return;
    }
    this.#clerkUserId = minted.value.user.userId;
    this.#clerkRoleCode = minted.value.roleCode;
    this.#clerkIdentity = {
      user_id: minted.value.user.userId,
      email: minted.value.user.email,
      display_name: minted.value.user.name,
      role_code: minted.value.roleCode,
      role_name: minted.value.identity.roleName,
      tier: minted.value.identity.tier,
      scope: minted.value.identity.scope,
    };
    this.#till = { label: this.#labelFor(flow) };
    this.#activeFlow = flow;
    this.requestUpdate();
    await this.#initTill();
  }

  /**
   * The role label in the chrome, read from custody rather than cached: while
   * the live session is the one this till minted for, the entry card's label
   * stands; if custody were ever replaced quietly (the review round's M3b),
   * the chrome names who the session actually carries, so the till can never
   * show one person's role over another person's session.
   */
  #signedInRoleLabel(): string {
    const session = this.#sessions.current;
    if (session === null) return "";
    if (this.#clerkUserId !== null && session.user.userId !== this.#clerkUserId) {
      return session.user.name;
    }
    return this.#till?.label ?? session.user.name;
  }

  #labelFor(flow: string): string {
    if (this.#flowsState.kind === "ready") {
      const row = this.#flowsState.flows.find((candidate) => candidate.flow === flow);
      if (row !== undefined && typeof row.label === "string") return row.label;
    }
    return flow;
  }

  /**
   * Sign out: the controller ends the mint (the adapter drops it), wipes
   * custody, and clears the front door handoff key in the tab's session
   * storage; the entry view with the cards comes back, because the flows
   * answer is still held. A reload in the same tab keeps the session: the
   * mint stays where the front door put it (this tab's session storage) until
   * Sign out, Switch role, or a refused session clears it. The returns desk
   * is blanked with the session: the till is the next person's, and no draft,
   * refusal or receipt of the last one stays on it.
   */
  async #signOut(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.requestUpdate();
    if (this.#custody.mode === "console") {
      // Console mode also forgets the persona id and stops the recovery loops, then goes to the front door.
      // The copy on this device is released first, since leaving may walk away from the page.
      await this.#offline?.close();
      this.#offline = null;
      await this.#custody.leave();
    } else {
      await this.#controller.signOut();
      // The copy on this device is released with the session.
      await this.#offline?.close();
      this.#offline = null;
    }
    // The front door's key was kept in the tab for the session (row 1040); it goes with the session.
    clearFrontDoorHandoff();
    clearSessionBranchIds();
    this.#busy = false;
    this.#till = null;
    this.#activeFlow = null;
    this.#message = null;
    this.#chromeMessage = null;
    this.#clerkUserId = null;
    this.#clerkRoleCode = null;
    this.#clerkIdentity = null;
    this.#returns.reset();
    this.#payBalance.reset();
    this.#branch = null;
    this.#surface = "entry";
    this.#branchChoices = [];
    // The ported surface's session-owned module state goes with the session:
    // the next person's boxes and branch facts are earned by their own login.
    clearSessionState();
    this.requestUpdate();
    if (import.meta.env.MODE === "demo") this.#toFrontDoor();
  }

  /**
   * The till's own start: resolve the branch the login reaches, fill the
   * branch facts the terminal reads, then mount the terminal. The drawer is
   * the terminal's own business from here (it finds or opens the session at
   * its own pace), so a drawer refusal surfaces through the terminal's chrome,
   * not through a wall before it. A branch that does not resolve is the one
   * wall that keeps the terminal down: without the branch nothing on the
   * surface can fence itself, so the refusal says so and the cards come back.
   */
  async #initTill(fromHandoff = false): Promise<void> {
    const resolved = await this.#tillClient.resolveBranchChoice();
    let branch: Result<BranchRef>;
    if (!resolved.ok) {
      branch = resolved;
    } else if (resolved.value.kind === "ask") {
      // Several branches granted and the front door's key named not exactly one: ask once, here, then start again.
      this.#branchChoices = resolved.value.choices;
      this.#branchAskedFromHandoff = fromHandoff;
      this.#message = null;
      this.#surface = "branch";
      this.requestUpdate();
      return;
    } else {
      branch = ok(resolved.value.branch);
    }
    if (!branch.ok) {
      this.#message = `The till did not start: ${branch.error}`;
      this.#till = null;
      this.#activeFlow = null;
      this.#clerkUserId = null;
      this.#clerkRoleCode = null;
      this.#clerkIdentity = null;
      if (this.#custody.mode === "console") {
        await this.#consoleStartFailed(branch.status);
        return;
      }
      await this.#controller.signOut();
      this.requestUpdate();
      // A session the server refuses (ended or revoked) must not be re-adopted by every load: the
      // key goes, and the person picks a role again at the front door (no loop: the front door
      // shows the picker once its key is gone). Only the handoff's own adopt does this; a session
      // minted by the cards route has no key to drop.
      if (fromHandoff) {
        clearFrontDoorHandoff();
        clearSessionBranchIds();
        this.#toFrontDoor();
      }
      return;
    }
    this.#branch = branch.value;
    this.#returns.claimThresholdCents = await this.#readClaimThresholdCents(this.#syncClient);
    // The branch facts the ported surface reads (the branch default, the yard
    // names beside stock): filled in place from the route's own answer, before
    // anything mounts.
    setBranches([{ id: branch.value.id, name: branch.value.name }]);
    // The Work shell's read cache on this device, the person's own: opened before the terminal mounts so a read
    // that loses the network has something to answer from; the first pull runs behind the till.
    const offline = createTillDesk({
      signal: this.#signal,
      transport: apiClientTransport(this.#syncClient),
      dbName: dbNameFor(this.#sessions.current?.user.userId ?? ""),
    });
    this.#offline = offline;
    await offline.open(branch.value.id);
    void offline.cycle();
    this.#surface = "terminal";
    this.#openPayBalanceOnOrder();
    this.requestUpdate();
  }

  /** Row 976: the Swap panes icon button, shown only while the till is in its two-pane view. */
  #swapControl(): TemplateResult | typeof nothing {
    if (this.#counterLayout !== "PANELS") return nothing;
    return html`
      <button
        type="button"
        class="btn-secondary icon-btn"
        data-action="swap-panes"
        data-testid="swap-panes"
        aria-label="Swap panes"
        aria-pressed=${this.#panesSwapped ? "true" : "false"}
        title="Swap panes"
      >
        <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M4 8h15M15 4l4 4-4 4" />
          <path d="M20 16H5M9 12l-4 4 4 4" />
        </svg>
      </button>
    `;
  }

  /** The Feedback control of the bar: opens the shared widget's panel. */
  #feedbackControl(): TemplateResult {
    return html`
      <button
        type="button"
        class="btn-secondary"
        data-action="feedback"
        data-testid="feedback-button"
        title="Send feedback about this screen"
      >
        Feedback
      </button>
    `;
  }

  /** The Theme control of the account area: one button, its value beside the word. */
  #themeControl(): TemplateResult {
    const choice = this.#theme.choice;
    return html`
      <button
        type="button"
        class="btn-secondary"
        data-action="theme"
        data-testid="theme-control"
        aria-label=${themeControlLabel(choice)}
        title="Cycle the theme: Auto, Light, Brand"
      >
        Theme: ${THEME_LABEL[choice]}
      </button>
    `;
  }

  render(): TemplateResult {
    // The flows route's answer drives the entry cards: an error answer and an
    // empty answer alike are not the demo showing itself. The cards are the
    // till's own pair, not the box's whole table.
    const entryFlows = this.#entryFlowsState();
    return html`
      <p class="visually-hidden" role="status" aria-live="polite" data-testid="theme-status">${this.#themeStatus}</p>
      ${this.#framed
        ? nothing
        : html`<hh-shell-header data-testid="framed-band"></hh-shell-header>`}
      ${this.#surface === "terminal" ? nothing : html`<h1 class="visually-hidden">Counter Till</h1>`}
      <main class="${this.#surface === "terminal" ? "on-till" : ""}">
        ${this.#surface === "terminal"
          ? html`
              <h2 class="visually-hidden">Till</h2>
              <div class="till-screen">
              <div class="surfacebar">
                <p class="signed-in">
                  ${this.#route !== "sale"
                    ? html`<a class="btn-secondary nav-link" href="#/" data-testid="till-sale-entry">Back to the sale</a>`
                    : html`<a class="btn-secondary nav-link" href="#/returns" data-testid="till-returns-entry">Returns</a>
                        <a class="btn-secondary nav-link" href="#/pay-balance" data-testid="till-pay-balance-entry">Pay balance</a>`}
                  ${this.#route === "sale" ? this.#swapControl() : nothing}
                  ${this.#isPopout
                    ? nothing
                    : html`<button
                        type="button"
                        class="btn-secondary"
                        data-action="pop-out"
                        data-flow=${this.#activeFlow ?? nothing}
                        title="Open the till on a second window"
                      >
                        Pop out
                      </button>`}
                </p>
              </div>
              <ct-offline-strip></ct-offline-strip>
              <div class="surface" data-hidden="${this.#route !== "sale" ? "true" : "false"}">
                <hos-pos-terminal
                  data-paused="${this.#route !== "sale" ? "true" : "false"}"
                  data-panes-swapped="${this.#panesSwapped ? "true" : "false"}"
                  data-layout-hint="${this.#isPopout ? this.#layoutHint : ""}"
                ></hos-pos-terminal>
              </div>
              ${this.#route === "returns" ? renderReturns(this.#returns) : nothing}
              ${this.#route === "pay-balance" ? renderPayBalance(this.#payBalance) : nothing}
              </div>
              ${this.#chromeMessage === null ? nothing : html`<p class="notice" role="status">${this.#chromeMessage}</p>`}
            `
          : this.#surface === "branch"
            ? html`<hh-branch-chooser
                data-branches=${JSON.stringify(this.#branchChoices.map((row) => ({ id: row.id, name: row.name })))}
                action-label="Open the Counter Till"
              ></hh-branch-chooser>`
            : renderEntry({ flows: entryFlows, busy: this.#busy, message: this.#message })}
      </main>
      <!-- The feedback widget: the shared panel, opened by the bar's Feedback
           button (trigger external: no floating pill, so nothing of the
           till's own surface is ever under it). The lift attribute keeps the
           open panel above the pinned hotkey legend. -->
      <hh-feedback screen="Counter Till" trigger="external" lift></hh-feedback>
    `;
  }

  /**
   * The Returns surface's fields are uncontrolled (the design system's position gate allows no
   * value binding in a template), so the flow's own answer is carried to them here: each field
   * names it in `data-value`. A text field is left alone while it has focus (the person is
   * typing it); a number field is brought back whenever the typed number differs from the flow's,
   * which is how a refused quantity returns to the one the draft kept, and "1." stays as typed.
   */
  #syncReturnsFields(): void {
    const fields = this.renderRoot.querySelectorAll<HTMLInputElement>("input[data-value]");
    for (const field of fields) {
      const wanted = field.getAttribute("data-value") ?? "";
      if (field.value === wanted) continue;
      if (field.type === "number") {
        if (field.value !== "" && wanted !== "" && Number(field.value) === Number(wanted)) continue;
      } else if (this.shadowRoot?.activeElement === field) {
        continue;
      }
      field.value = wanted;
    }
  }

  /**
   * Read the dealer's warranty claim threshold once at sign in, for the Counter Till Return
   * Drawer's manufacturer defect sentence. The 0396 seed is $50.00 (5000 cents); a refused read
   * keeps that seed so the desk still reads in a coherent unit, never a fabricated zero that
   * would mis-classify an over-threshold defect as shrinkage. The live policy read makes the
   * claim threshold sentence agree with the core's `CostOverClaimThreshold` end to end.
   */
  async #readClaimThresholdCents(client: ApiClient): Promise<number> {
    const FALLBACK_CENTS = 5000;
    const branchId = this.#branch?.id;
    const result = await staffWarranty.warrantyGetPolicy(client, branchId === undefined ? {} : { branch_id: branchId });
    if (!result.ok) return FALLBACK_CENTS;
    const threshold = result.value.claim_threshold_cents;
    if (typeof threshold !== "number" || !Number.isFinite(threshold) || threshold < 0) return FALLBACK_CENTS;
    return Math.trunc(threshold);
  }

  /** The widget's sender rides the property, never a template binding (the
   * design system's position gate refuses . bindings in dependents too). */
  protected override updated(): void {
    // The Pay balance find field is uncontrolled (no value binding is allowed in a template): a
    // term the flow set itself (the held card's Pay balance action) is carried to the field here.
    const termField = this.renderRoot.querySelector('[data-field="pay-balance-term"]');
    if (termField instanceof HTMLInputElement && termField.value !== this.#payBalance.term && this.#payBalance.term !== "") {
      termField.value = this.#payBalance.term;
    }
    this.#syncReturnsFields();
    this.renderRoot.querySelector<CtOfflineStrip>("ct-offline-strip")?.attach(this.#offline);
    // The framed band (GAPS-r2 row G1): the board's white band, the ONE band
    // when the till stands alone; framed by the Work shell, the host's band
    // is the one band. The bell stands quiet: wiring its unread count is a
    // follow-up. The account chip's menu carries what the app-local header
    // used to (theme, feedback, sign out): the actions ride the band's
    // accountMenu template and their clicks bubble back to #onHostClick.
    const band = this.renderRoot.querySelector<ShellHeader>("hh-shell-header");
    if (band !== null) {
      // The Panels screen is the window below the band: the band's real height (it wraps at phone
      // width) rides --band-h, so the screen never pushes the page past the fold.
      if (this.#bandWatch === null && typeof ResizeObserver !== "undefined") {
        this.#bandWatch = new ResizeObserver(() => {
          this.style.setProperty("--band-h", `${Math.round(band.getBoundingClientRect().height)}px`);
        });
        this.#bandWatch.observe(band);
      }
      const identity = bandIdentity(servedConsoleConfig(), this.#dealerName);
      band.org = identity.org;
      // The DEMO marker keeps the old banner's rule on a demo page: only once the flows route has answered with
      // cards, or on the till itself. A dealer's own box (console mode) is never DEMO (item 371).
      const demoFlows = this.#entryFlowsState();
      band.demo = identity.demo && (this.#surface === "terminal" || (demoFlows.kind === "ready" && demoFlows.flows.length > 0));
      band.user = framedBandAccount(this.#sessions.current?.user.name ?? "", this.#signedInRoleLabel());
      band.breadcrumb = framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface(this.#route));
      band.groups = [];
      band.currentKey = "";
      band.syncState = "idle";
      band.variant = "band";
      band.accountMenu = html`
        ${this.#till !== null
          ? html`<p>Signed in as <strong data-testid="signed-in-role">${this.#signedInRoleLabel()}</strong></p>`
          : nothing}
        ${this.#themeControl()}
        ${this.#feedbackControl()}
        ${this.#till !== null
          ? html`<button type="button" class="btn-secondary" data-action="sign-out" ?disabled=${this.#busy}>Sign out</button>`
          : nothing}
      `;
    }
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) {
      widget.send = this.#feedbackSender;
    }
  }
}

defineElement("ct-app-root", CounterTillApp);
