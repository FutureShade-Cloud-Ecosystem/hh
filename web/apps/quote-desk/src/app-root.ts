// The Quote Desk's root element: the way in and the chrome. The way in is
// the demo's role cards (the shared kit's entry, the demo adapter behind the
// auth package's AccountSurface seam: this app never touches the token
// itself, the mint owns it), and after the mint the branch the login reaches
// resolves once, the kit's ported surface is wired with the caller's
// headers, and the builder shell (the work area above the list) IS the desk.
//
// The organisation header is the demo org by construction: the whole app is
// the demo's desk, and the staff header provider refuses to send a request
// without it, so no call ever leaves this app unresolved.
//
// The binding discipline is the app's own (the design system's position gate
// holds its templates to no property or event bindings): the cards and the
// chrome controls delegate through data-action on the host.
import { SIGN_OUT_EVENT, UNSENT_WAIT_MS, waitUpTo, type SignOutDetail } from "./quote-context.js";
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// The shared chrome (item 5, decision log row 938): the element registers
// itself; the app feeds it through the wiring pass below, the same
// no-template-binding discipline the builder's client rides.
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import {
  HHFeedback,
  type FeedbackSender,
} from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, servedConsoleConfig, type AppStart, type Custody } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { entryStyles, renderEntry, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { clearFrontDoorHandoff, readFrontDoorHandoff } from "./demo-account.js";
import { HOUSE_ZONE, houseDayInZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { setBranches, wirePorts, clearSessionState, type AuthUser } from "@hardscapehouse/pos-kit/src/ported/ports.js";
import { QdClient, type BranchRef } from "./qd-client.js";
// The Work shell's offline layer (item 8b): writes are refused with no signal and never queue (money never
// queues); the desk's reads fall back to the copy on the device; a new quote and an expiry date are kept when
// the signal is gone. The panel shows the connection, the copy's age, the drafts waiting and the cards.
import { browserSignal, noSignalFetch, noSignalWebFetch, readThroughFetch, readThroughWebFetch, type CachedRoute, type ReadThroughHooks } from "@hardscapehouse/work-offline";
import { apiClientTransport, DeskOffline, dbNameFor } from "./desk-offline.js";
import { quoteDeskRoutes } from "./offline-reads.js";
// The panel registers itself; the wiring pass hands it the controller.
import "./offline-panel.js";
import type { QdOfflinePanel } from "./offline-panel.js";
import { PricingClient } from "./pricing-client.js";
import { LIST_ROUTE, parseRoute, routeHash, type DeskRoute } from "./quote-router.js";
import { THEME_LABEL, ThemeController, themeControlLabel, themeStatusText } from "./theme.js";
// The builder shell registers the work area and the panel seams.
import "./quote-builder.js";
// The pricing panel (QD-4), mounted here until Dealer Admin has a screen of its own.
import "./pricing-panel.js";

const DEMO_ORG = "hh-demo";

export class QuoteDeskApp extends HHElement {
  static styles = [
    baseStyles,
    entryStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }
      /* The chrome above the work area is the shared header element
         (hos-app-header). The theme control is the app's own, passed into
         the element's named theme slot: it renders inside the right cluster,
         after Switch role and Sign out, and nothing sits between the bar and
         the work area. The control's face is the sheet's secondary button at
         the bar's own compact size. */
      .theme-slot {
        display: inline-flex;
        align-items: center;
      }
      .theme-slot button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        white-space: nowrap;
      }
      .theme-slot button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      main {
        padding: var(--hh-s-4) 0;
      }
      .away {
        display: none;
      }
      .notice {
        margin: var(--hh-s-8) var(--hh-s-20);
      }
      /* The desk tab strip lives below the shared header, not in it. Quotes is
         always here; Pricing is manager only. The strip follows the same token
         rules as the header and keeps the light theme look. */
      .desk-tabs {
        display: flex;
        gap: var(--hh-s-4);
        padding: var(--hh-s-8) var(--hh-s-20) 0;
        background: var(--hh-surface);
        border-bottom: 1px solid var(--hh-line);
      }
      .desk-tab {
        padding: var(--hh-s-8) var(--hh-s-16);
        border: 1px solid transparent;
        border-bottom: none;
        border-radius: var(--hh-radius-control) var(--hh-radius-control) 0 0;
        background: transparent;
        color: var(--hh-text-dim);
        font-weight: 600;
        cursor: pointer;
      }
      .desk-tab[aria-selected="true"] {
        background: var(--hh-page);
        color: var(--hh-text);
        border-color: var(--hh-line);
      }
      .desk-tab:hover:not(:disabled) {
        color: var(--hh-accent-text);
      }
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  /** The custody the served config chose: the demo controller as always, or this page recovering its own session (boot-custody.ts). */
  readonly #custody: Custody;
  readonly #client: QdClient;
  readonly #pricing: PricingClient;
  /** The staff client with no guard: only the offline store's sync transport rides it, and the only writes that reach it are the scopes the core admits a client write to. */
  readonly #syncClient: ApiClient;
  /** The device's connection, read each time it is asked. */
  readonly #signal = browserSignal();
  /** The Work shell's offline layer for the signed in person; null before the desk is up and after sign out. */
  #offline: DeskOffline | null = null;
  /** Which of the desk's two screens shows: the quotes (always mounted, so a draft survives), or the pricing panel (mounted only while open). */
  #view: "quotes" | "pricing" = "quotes";
  #flowsState: FlowsState = { kind: "loading" };
  #busy = false;
  #message: string | null = null;
  /** The role's label from the flow table, held for the chrome. */
  #roleLabel: string | null = null;
  /** The user the mint was for: the chrome's custody check. */
  #deskUserId: string | null = null;
  /** The mint's own IdentityUser block, in the ported surface's session shape. */
  #deskIdentity: AuthUser | null = null;
  #branch: BranchRef | null = null;
  /** The theme choice: Auto follows the system, Light and Brand pin it; tab memory only (see theme.ts). */
  readonly #theme = new ThemeController(() => this.requestUpdate());
  /** What a screen reader hears after a Theme press: focus stays on the button, so the new name is not re-read. */
  #themeStatus = "";
  /** The mounted surface: the entry cards, or the desk. */
  #surface: "entry" | "desk" = "entry";
  /**
   * The desk's route, parsed from the address bar (the split-list
   * navigation): the list page is the desk's landing page, and the quote
   * pages are the work area's own. The hash is the desk's whole navigation:
   * the browser's back and forward move between the routes, and the host
   * wires the parsed route down through the same no-template-binding pass
   * as the client.
   */
  #route: DeskRoute = LIST_ROUTE;
  /** Whether this build is the demo build: the seam rule's own constant (the front door handoff rides it), and the shared header's demo badge rides it too. */
  readonly #demoBuild = import.meta.env.MODE === "demo";

  constructor() {
    super();
    // The demo adapter's own client carries no staff bearer: the demo routes
    // are pre-auth by design (the tap a role sign-in).
    const demoClient = new ApiClient({
      baseUrl: "",
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
    // The Work shell's guard: a write with no signal is refused before it dials (never queued), and a read the
    // device holds a copy of is answered from it when the network does not answer.
    const routes: CachedRoute[] = quoteDeskRoutes(() => this.#offline?.reader() ?? null, () => this.#branch?.id ?? "");
    const hooks: ReadThroughHooks = {
      onCached: (words) => this.#offline?.hooks.onCached?.(words),
      onLive: () => this.#offline?.hooks.onLive?.(),
    };
    const client = new ApiClient({ baseUrl: this.#custody.coreBaseUrl, headers: staffHeaders, fetch: noSignalFetch(readThroughFetch(undefined, this.#signal, routes, hooks), this.#signal) });
    this.#syncClient = new ApiClient({ baseUrl: this.#custody.coreBaseUrl, headers: staffHeaders });
    // The feedback widget's sender rides the staff client: the feedback
    // route is staff guarded and the desk's own custody holds the bearer.
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
    // The caller's own id is read from the live session, not from the sign-in answer kept beside it.
    this.#client = new QdClient(client, () => this.#sessions.current?.user.userId ?? "");
    this.#pricing = new PricingClient(client);
    // The shared kit's host wiring: one door, wired once, reading custody
    // through this element's own closures so the bearer never rests in a
    // second place.
    wirePorts({
      headers: staffHeaders,
      houseDay: () => houseDayInZone(HOUSE_ZONE),
      user: () => this.#deskIdentity,
      token: () => this.#sessions.current?.token ?? "",
      baseUrl: this.#custody.coreBaseUrl,
      fetch: noSignalWebFetch(readThroughWebFetch(undefined, this.#signal, routes, hooks), this.#signal),
    });
  }

  /** The feedback widget's sender; built once beside the staff client. */
  #feedbackSender: FeedbackSender = async () => ({ ok: false, error: "not ready" });

  override connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule: listen on the HOST and read the control off
    // composedPath(), never an event binding in a template.
    this.addEventListener("click", this.#onHostClick);
    // The desk's navigation ear: a hash change (a control's walk, the
    // browser's back or forward) re-reads the address bar.
    window.addEventListener("hashchange", this.#onHashChange);
    // The system's light setting is honoured from the first paint.
    this.#theme.start();
    // Console mode: this page recovers its own session and the demo cards never apply.
    if (this.#custody.mode === "console") {
      this.#custody.begin("quote-desk", (outcome) => this.#onRecovered(outcome));
      return;
    }
    void this.#loadFlows();
    // The seam rule (decision log rows 893 and 899): the front door's
    // handoff is adopted only where the demo is compiled in. Vite
    // substitutes the mode constant at build time, so a default build drops
    // this branch whole and the reader behind it resolves to the empty stub
    // (vite.config.ts), keeping the key name and the adopt call out of the
    // served product; the demo image and the Playwright harness build with
    // the demo mode.
    if (import.meta.env.MODE === "demo") {
      void this.#adoptFromFrontDoor();
    }
  }

  protected override updated(): void {
    // The wiring pass: the builder receives the validated client the same
    // way every shared component receives its facts.
    const builder = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-quote-builder");
    if (builder !== null) {
      builder.client = this.#client;
      // The parsed route and the signed in person's role code ride the same
      // pass: the builder's two screens and the Sales manager's Approvals
      // tab read them, never a template binding.
      builder.route = this.#route;
      builder.roleCode = this.#deskIdentity?.role_code ?? "";
    }
    this.renderRoot.querySelector<QdOfflinePanel>("qd-offline-panel")?.attach(this.#offline);
    const pricing = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-pricing-panel");
    if (pricing !== null) {
      pricing["pricing"] = this.#pricing;
      pricing["branchId"] = this.#branch?.id ?? "";
    }
    // The shared chrome is fed the same way, not through template bindings
    // (the design system's position gate holds this app's templates too):
    // the role's label, the signed-in flag, the demo badge's build flag, and
    // the theme the switcher last applied to the document, which the element
    // re-applies under the sheet's own attribute contract.
    const header = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-app-header");
    if (header !== null) {
      header.roleLabel = this.#signedInRoleLabel();
      header.signedIn = this.#roleLabel !== null;
      header.demo = this.#demoBuild;
      header.theme = document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
    }
    // The feedback widget's sender rides the property the same way, never a
    // template binding: the widget's own seam.
    const feedback = this.renderRoot.querySelector("hh-feedback");
    if (feedback instanceof HHFeedback && feedback.send !== this.#feedbackSender) {
      feedback.send = this.#feedbackSender;
    }
  }

  /** The desk carries no floating pill: the bar's Feedback button opens the shared panel by the widget's show(). */
  #showFeedback(): void {
    const feedback = this.renderRoot.querySelector("hh-feedback");
    if (feedback instanceof HHFeedback) feedback.show();
  }

  override disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    window.removeEventListener("hashchange", this.#onHashChange);
    this.#theme.stop();
    super.disconnectedCallback();
  }

  readonly #onHashChange = (): void => {
    this.#route = parseRoute(location.hash);
    this.requestUpdate();
  };

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    if (control.getAttribute("data-action") === "choose") {
      const flow = control.getAttribute("data-flow");
      if (typeof flow === "string" && flow !== "") void this.#chooseFlow(flow);
    }
    // A saved price whose floor confirmation is not in the pricing log yet holds a leave once
    // (the pricing panel's holdLeave; the SIGN_OUT_EVENT wait of quote/qd-3b-app registers it).
    const leaving = control.getAttribute("data-action");
    if ((leaving === "sign-out" || (leaving === "pricing" && this.#view === "pricing")) && this.#pricingHoldsLeave()) return;
    if (control.getAttribute("data-action") === "sign-out") {
      void this.#signOut();
    }
    if (control.getAttribute("data-action") === "switch-role") {
      void this.#switchRole();
    }
    if (control.getAttribute("data-action") === "desk-tab") {
      const next = control.getAttribute("data-view") as "quotes" | "pricing" | null;
      if (next !== null && next !== this.#view) {
        if (this.#view === "pricing" && this.#pricingHoldsLeave()) return;
        this.#view = next;
        this.requestUpdate();
      }
    }
    if (control.getAttribute("data-action") === "feedback") {
      this.#showFeedback();
    }
    if (control.getAttribute("data-action") === "theme") {
      this.#theme.cycle();
      this.#themeStatus = themeStatusText(this.#theme.choice);
      this.requestUpdate();
    }
  };

  #pricingHoldsLeave(): boolean {
    const panel = this.renderRoot.querySelector<HTMLElement & { holdLeave?: () => boolean }>("qd-pricing-panel");
    return panel?.holdLeave?.() === true;
  }

  async #loadFlows(): Promise<void> {
    const result = await this.#account.listFlows();
    this.#flowsState = result.ok
      ? { kind: "ready", flows: result.value }
      : { kind: "unavailable", status: result.status ?? null };
    this.requestUpdate();
  }

  /**
   * The front door's handoff (HH-DEMO-1, the Quote Desk's adopt, decision
   * log rows 893, 899 and 906): the front door mints the demo session and
   * holds it for the box under its own session storage key; when this app
   * boots from a module tile's navigation, the record is read, handed to
   * AuthController.adopt (custody takes the token into private memory and
   * rewrites the display mirror itself). The key stays in the tab until Sign out or Switch role (decision
   * log row 1040), so a reload or a second visit to a quote's address keeps the session; a record that
   * fails a read, or a session the server refuses, drops it. The
   * desk facts the chrome and the ported surfaces read ride the same record
   * (the front door validated the mint already; this read validates the
   * record again before custody sees any of it), exactly the fields
   * #chooseFlow's mint leaves. An absent or unusable record is the ordinary
   * direct visit; the entry page with the cards comes up, the desk's
   * ordinary way in. The storage touch rides the auth package's throw-safe
   * mirror, this app's sink gate's one sanctioned accessor. The shape is the
   * till's (counter-till app-root on demo/front-door 385cd8d6).
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
      this.#toFrontDoor();
      return;
    }
    this.#deskUserId = handed.user.userId;
    this.#deskIdentity = {
      user_id: handed.user.userId,
      email: handed.user.email,
      display_name: handed.user.name,
      role_code: handed.roleCode,
      role_name: handed.roleName,
      tier: handed.tier,
      scope: handed.scope,
    };
    this.#roleLabel = handed.roleLabel;
    this.requestUpdate();
    await this.#initDesk(true);
  }

  /**
   * Console mode: this page recovered its own session, or could not. The person's role comes from the
   * exchange (not a flow table). An unavailable page shows the entry's unavailable state while the
   * custody retries; a session the front door must settle goes back there.
   */
  #onRecovered(outcome: AppStart): void {
    if (outcome.kind === "ready") {
      const entry = outcome.entry;
      this.#deskUserId = entry.userId;
      this.#deskIdentity = {
        user_id: entry.userId,
        email: entry.email,
        display_name: entry.name,
        role_code: entry.roleCode,
        role_name: entry.roleLabel,
        tier: entry.tier,
        scope: entry.scope,
      };
      this.#roleLabel = entry.roleLabel;
      this.requestUpdate();
      void this.#initDesk(true);
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
    this.#flowsState = { kind: "unavailable", status: null };
    if (status === 401 || status === 403) {
      await this.#custody.leave();
      this.requestUpdate();
      return;
    }
    this.requestUpdate();
    this.#custody.retry("quote-desk", (outcome) => this.#onRecovered(outcome));
  }

  /**
   * A demo build that finds no session sends the person to the front door to pick a role (decision log row
   * 1040), never to the shared entry page. Only where the desk is served under its own prefix (the box:
   * BASE_URL is /quote/): at the origin root the desk's own entry cards are the way in, and the front door
   * is not there.
   */
  #toFrontDoor(): void {
    if (import.meta.env.BASE_URL !== "/") window.location.assign("/");
  }

  async #chooseFlow(flow: string): Promise<void> {
    if (this.#busy || flow === "") return;
    this.#busy = true;
    this.#message = null;
    this.requestUpdate();
    const minted = await this.#account.chooseFlow(flow);
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
    this.#deskUserId = minted.value.user.userId;
    this.#deskIdentity = {
      user_id: minted.value.user.userId,
      email: minted.value.user.email,
      display_name: minted.value.user.name,
      role_code: minted.value.roleCode,
      role_name: minted.value.identity.roleName,
      tier: minted.value.identity.tier,
      scope: minted.value.identity.scope,
    };
    this.#roleLabel =
      this.#flowsState.kind === "ready"
        ? (this.#flowsState.flows.find((row) => row.flow === flow)?.label ?? flow)
        : flow;
    this.requestUpdate();
    await this.#initDesk();
  }

  /**
   * The desk's own start: resolve the branch the login reaches, fill the
   * branch facts the shared components read, then mount the builder. A
   * branch that does not resolve is the one wall that keeps the desk down:
   * without the branch nothing on the surface can fence itself.
   */
  async #initDesk(fromHandoff = false): Promise<void> {
    const branch = await this.#client.resolveBranch();
    if (!branch.ok) {
      this.#message = `The desk did not start: ${branch.error}`;
      this.#roleLabel = null;
      this.#deskUserId = null;
      this.#deskIdentity = null;
      if (this.#custody.mode === "console") {
        await this.#consoleStartFailed(branch.status);
        return;
      }
      await this.#controller.signOut();
      this.requestUpdate();
      // A session the server refuses (ended or revoked) must not be re-adopted by every load: the key goes
      // and the person picks a role again at the front door. Only the handoff's own adopt does this.
      if (fromHandoff) {
        clearFrontDoorHandoff();
        this.#toFrontDoor();
      }
      return;
    }
    this.#branch = branch.value;
    setBranches([{ id: branch.value.id, name: branch.value.name }]);
    // The Work shell's copy on this device, the person's own: opened before the desk mounts so a read that loses
    // the network has something to answer from; the first pull runs behind the desk.
    const offline = new DeskOffline({
      signal: this.#signal,
      qd: this.#client,
      transport: apiClientTransport(this.#syncClient),
      dbName: dbNameFor(this.#sessions.current?.user.userId ?? ""),
    });
    this.#offline = offline;
    await offline.open(branch.value.id);
    void offline.cycle();
    this.#surface = "desk";
    // A sign in starts at the desk's landing page (decision log row 941):
    // whatever the address bar carried in (a stale quote fragment, a handoff
    // boot's address) gives way to the list, written once in place so no
    // stray history entry is left behind.
    this.#route = LIST_ROUTE;
    if (location.hash !== routeHash(LIST_ROUTE)) location.replace(routeHash(LIST_ROUTE));
    this.requestUpdate();
  }

  /**
   * Sign out: the controller ends the mint and wipes custody either way; the
   * entry cards come back. A reload signs out too: the mint lives only in
   * this tab's memory.
   */
  async #signOut(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.requestUpdate();
    // Unsent work (a terms edit waiting on its save, the layout) is sent while the session still stands.
    const detail: SignOutDetail = { waits: [] };
    window.dispatchEvent(new CustomEvent(SIGN_OUT_EVENT, { detail }));
    // Bounded: a save that never answers must not hold the session open for good.
    const sent = await waitUpTo(UNSENT_WAIT_MS, Promise.allSettled(detail.waits));
    if (this.#custody.mode === "console") {
      // Console mode also forgets the persona id and stops the recovery loops, then goes to the front door.
      // The copy on this device is released first, since leaving may walk away from the page.
      await this.#offline?.close();
      this.#offline = null;
      await this.#custody.leave();
    } else {
      await this.#controller.signOut();
      // The copy on this device is released with the session; its outbox stays the person's own for their next sign in.
      await this.#offline?.close();
      this.#offline = null;
    }
    // The front door's key was kept in the tab for the session (row 1040); it goes with the session.
    clearFrontDoorHandoff();
    this.#busy = false;
    this.#roleLabel = null;
    this.#deskUserId = null;
    this.#deskIdentity = null;
    this.#branch = null;
    // What the listeners waited for, took or kept is said on the entry page, with the timeout line
    // first when the bound ran out before every wait had answered.
    const timeoutLine =
      "Signing out did not wait for every unsent change: saving timed out, so a change you made last may not have been saved.";
    const notes = detail.notes ?? [];
    const lines = sent ? notes : [timeoutLine, ...notes];
    this.#message = lines.length > 0 ? lines.join(" ") : null;
    this.#view = "quotes";
    this.#surface = "entry";
    // The tab's title goes back to the entry page's own: it must not keep the last quote's number.
    document.title = "Quote Desk";
    // The shared surface's session-owned module state goes with the session.
    clearSessionState();
    this.requestUpdate();
    if (import.meta.env.MODE === "demo") this.#toFrontDoor();
  }

  /**
   * Switch role: the front door owns the picker, so the walk goes there
   * (decision log row 938: Switch role returns to the front door at /). The
   * session ends the sign-out way first, unsent work included, so no custody
   * mirror survives the walk and the front door comes up at its own picker,
   * not at a session it did not mint.
   */
  async #switchRole(): Promise<void> {
    if (this.#busy) return;
    await this.#signOut();
    window.location.assign("/");
  }

  /** The role's label in the chrome, read from custody rather than cached. */
  #signedInRoleLabel(): string {
    const session = this.#sessions.current;
    if (session === null) return "";
    if (this.#deskUserId !== null && session.user.userId !== this.#deskUserId) {
      return session.user.name;
    }
    return this.#roleLabel ?? session.user.name;
  }

  /** Whether the signed-in user may use the Pricing desk tab. */
  #isManager(): boolean {
    return this.#deskIdentity !== null && this.#deskIdentity.tier <= 2;
  }

  /** The Feedback control of the bar: opens the shared widget's panel. */
  #feedbackControl(): TemplateResult {
    return html`
      <button type="button" class="btn-secondary" data-action="feedback" data-testid="feedback-control" title="Send feedback about this screen">
        Feedback
      </button>
    `;
  }

  /** The Theme control of the bar: one button, its value beside the word. */
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

  /** The desk tab strip: Quotes for every desk user, Pricing for managers. */
  #deskTabs(): TemplateResult {
    const selected = this.#view;
    const pricingAllowed = this.#isManager();
    return html`
      <nav class="desk-tabs" data-testid="desk-tabs" aria-label="Desk views">
        <button
          type="button"
          class="desk-tab"
          data-action="desk-tab"
          data-view="quotes"
          data-testid="desk-tab-quotes"
          aria-selected=${selected === "quotes" ? "true" : "false"}
        >
          Quotes
        </button>
        ${pricingAllowed
          ? html`
              <button
                type="button"
                class="desk-tab"
                data-action="desk-tab"
                data-view="pricing"
                data-testid="desk-tab-pricing"
                aria-selected=${selected === "pricing" ? "true" : "false"}
              >
                Pricing
              </button>
            `
          : nothing}
      </nav>
    `;
  }

  render(): TemplateResult {
    return html`
      <p class="visually-hidden" role="status" aria-live="polite" data-testid="theme-status">${this.#themeStatus}</p>
      <hos-app-header data-testid="qd-bar">
        <div class="theme-slot" slot="theme">${this.#feedbackControl()}</div>
        <div class="theme-slot" slot="theme">${this.#themeControl()}</div>
      </hos-app-header>
      ${this.#surface === "desk" ? this.#deskTabs() : nothing}
      <main>
        ${this.#surface === "desk"
          ? html`
              <qd-offline-panel></qd-offline-panel>
              <div class="surface ${this.#view === "pricing" ? "away" : ""}">
                <qd-quote-builder></qd-quote-builder>
              </div>
              ${this.#view === "pricing"
                ? html`<div class="pricing" data-testid="pricing-screen"><qd-pricing-panel></qd-pricing-panel></div>`
                : nothing}
            `
          : renderEntry({ flows: this.#flowsState, busy: this.#busy, message: this.#message })}
      </main>
      <!-- The feedback widget (Feature Polish): the shared panel, opened by
           the bar's Feedback button (trigger external: no floating pill). -->
      <hh-feedback screen="Quote Desk" trigger="external"></hh-feedback>
    `;
  }
}

defineElement("qd-app-root", QuoteDeskApp);
