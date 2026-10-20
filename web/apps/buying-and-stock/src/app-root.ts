// The role app's root element: the chrome and the way in. The way in is the
// demo's role cards (the shared kit's entry, the demo adapter behind the auth
// package's AccountSurface seam). After the mint the branch resolves once, the
// ports are wired with the caller's headers, and the work area renders.
//
// The organisation header is the demo org by construction: the staff header
// provider refuses to send a request without X-Org, so no call ever leaves
// this app unresolved.
//
// The header is fed through plain attributes (signed-in, demo) and, for the
// role label, an assignment in updated(): the design system's position gate
// refuses every "." and "@" binding and every bound value outside the data-
// and aria- attributes in a dependent package. Its two buttons ride data-action names
// and the host's click listener below acts on them.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { announceChromeControls, chromeActionFrom, framedByShell, listenForHostSignOut } from "@hardscapehouse/design-system/src/framed.js";
import { applyDocumentTheme, framedBandAccount, framedBandBreadcrumb, FRAMED_BAND_ORG } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import type { ShellHeader } from "@hardscapehouse/shell-chrome/src/shell-header.js";
import "@hardscapehouse/shell-chrome/src/shell-header.js";
import { FRAMED_BAND_APP, framedBandSurface } from "./framed-band.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import { browserSignal, noSignalFetch } from "@hardscapehouse/work-offline/guard";
import { HHFeedback, type FeedbackSender } from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { entryStyles, renderEntry, type EntryView } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { PoApi } from "./po/po-api.js";
import { PoDesk } from "./po/po-desk.js";
import { poStyles } from "./po/po-styles.js";
import { IncomingDesk, SalesIncomingDesk } from "./incoming/incoming-desk.js";
import { areaStyles } from "./area-styles.js";
import { InventoryApi } from "./inventory/inventory-api.js";
import { InventoryDesk } from "./inventory/inventory-desk.js";
import { CountsApi } from "./counts/counts-api.js";
import { CountsDesk } from "./counts/counts-desk.js";
import type { AreaDesk, AreaEntry } from "./purch/area.js";
import { purchStyles } from "./purch/purch-styles.js";
import { LandedApi } from "./landed/landed-api.js";
import { LandedDesk } from "./landed/landed-desk.js";
import { BillsApi } from "./bills/bills-api.js";
import { BillsDesk } from "./bills/bills-desk.js";
import { ReceivingApi } from "./receiving/receiving-api.js";
import { ReceivingDesk } from "./receiving/receiving-desk.js";
import { VendorsApi } from "./vendors/vendors-api.js";
import { VendorsDesk } from "./vendors/vendors-desk.js";
import { LookupDesk } from "./lookup/lookup-desk.js";
import { lookupStyles } from "./lookup/lookup-styles.js";
import { stockLookupSeat } from "./lookup/lookup-logic.js";

const DEMO_ORG = "hh-demo";

/**
 * A field's value as the desks read it. An option's value cannot be bound (the
 * design system's rule), so a select whose options carry the id in data-value
 * answers that id; every other field answers its own value.
 */
function fieldValue(el: HTMLInputElement | HTMLSelectElement | null): string {
  if (el === null) return "";
  if (el instanceof HTMLSelectElement) {
    const chosen = el.selectedOptions[0]?.getAttribute("data-value");
    if (chosen !== null && chosen !== undefined) return chosen;
  }
  return el.value;
}

export class BuyingAndStockApp extends HHElement {
  static styles = [baseStyles, entryStyles, poStyles, areaStyles, purchStyles, lookupStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { padding: var(--hh-s-4) 0; }
  `];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // two demo routes. It is the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface = new DemoAccount({ client: this.#demoClient });

  // The custody the served config chose: in demo mode the auth controller over the demo account, as
  // always; in console mode this page recovers its own session on load (boot-custody.ts).
  readonly #custody = bootCustody(servedConsoleConfig(), { sessions: this.#sessions, demoAccount: this.#demoAccount, demoOrg: DEMO_ORG });

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController = this.#custody.controller;

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: this.#custody.coreBaseUrl,
    // The Work shell's rule: money never queues. With no signal a write is refused before it dials, with one honest state.
    fetch: noSignalFetch(undefined, browserSignal()),
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => this.#custody.org }),
  });

  // The areas, each a desk of its own (src/po, src/landed, ...): its state, its loads, its
  // presses. The fields a desk reads are looked up in this element's own shadow root. The
  // purchase orders are first and load at sign in; the others load when first shown.
  // A sales seat's set is the read only one (ruling R1): Stock lookup first, Incoming beside it;
  // every other seat keeps the buyers' seven, unchanged.
  #areas: readonly AreaEntry[] = this.#newAreas();
  #area = "orders";
  #shown = new Set<string>(["orders"]);

  #deskHost() {
    return {
      repaint: () => this.requestUpdate(),
      field: (name: string, key?: string) => fieldValue(this.#fieldElement(name, key)),
      role: () => this.#roleCode,
    };
  }

  #newAreas(): AreaEntry[] {
    const host = this.#deskHost();
    return [
      { id: "orders", label: "Purchase orders", desk: new PoDesk(new PoApi(this.#staffClient), host) },
      { id: "incoming", label: "Incoming", desk: new IncomingDesk(new PoApi(this.#staffClient), host) },
      { id: "receiving", label: "Receiving", desk: new ReceivingDesk(new ReceivingApi(this.#staffClient), host) },
      { id: "vendors", label: "Vendors", desk: new VendorsDesk(new VendorsApi(this.#staffClient), host) },
      { id: "bills", label: "Vendor bills", desk: new BillsDesk(new BillsApi(this.#staffClient), host) },
      { id: "landed", label: "Landed cost", desk: new LandedDesk(new LandedApi(this.#staffClient), host) },
      { id: "inventory", label: "Inventory", desk: new InventoryDesk(new InventoryApi(this.#staffClient), host) },
      { id: "counts", label: "Cycle counts", desk: new CountsDesk(new CountsApi(this.#staffClient), host) },
    ];
  }

  #salesAreas(): AreaEntry[] {
    const host = this.#deskHost();
    return [
      { id: "lookup", label: "Stock lookup", desk: new LookupDesk(new InventoryApi(this.#staffClient), host, this.#custody.coreBaseUrl) },
      { id: "incoming", label: "Incoming", desk: new SalesIncomingDesk(new PoApi(this.#staffClient), host) },
    ];
  }

  #areasFor(roleCode: string): AreaEntry[] {
    return stockLookupSeat(roleCode) ? this.#salesAreas() : this.#newAreas();
  }

  /** One way in for every sign-in: the seat decides the areas, the first one lands and loads. */
  #enterAs(roleCode: string): void {
    this.#roleCode = roleCode;
    this.#areas = this.#areasFor(roleCode);
    const landing = this.#areas[0]!.id;
    this.#area = landing;
    this.#shown = new Set([landing]);
    void this.#areas[0]!.desk.load();
  }

  get #desk(): AreaDesk {
    return (this.#areas.find((a) => a.id === this.#area) ?? this.#areas[0]!).desk;
  }

  #showArea(id: string): void {
    const entry = this.#areas.find((a) => a.id === id);
    if (entry === undefined) return;
    this.#area = id;
    if (!this.#shown.has(id)) {
      this.#shown.add(id);
      void entry.desk.load();
    }
    this.requestUpdate();
  }

  // A repeated row's field (one count line's quantity) is found by the row's key as well.
  #fieldElement(name: string, key?: string): HTMLInputElement | HTMLSelectElement | null {
    const scope = key === undefined ? "" : `[data-key="${key}"]`;
    return this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"]${scope}`);
  }

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  readonly #feedbackSender: FeedbackSender = async (request) => {
    const answer = await staffFeedback.feedbackCreateMultipart(this.#staffClient, {
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

  // The entry view state, compatible with renderEntry's EntryView.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The signed in role's code: it decides which controls an area offers.
  #roleCode = "";

  // Framed by the Work shell: the host's band is the one band, so this app renders none of its own.
  readonly #framed = framedByShell();

  // The entry cards carry data-action="choose" and data-flow; one host
  // listener reads them, as the till's does.

  /** The Theme control's press: the band's account menu presses it standalone, the Work shell's menu asks for it framed. */
  #pressTheme(): void {
    this.#theme = this.#theme === "light" ? "dark" : "light";
    this.requestUpdate();
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "switch-role" || action === "sign-out") {
      this.#onSignOut();
      return;
    }
    if (action === "feedback") {
      this.#openFeedback();
      return;
    }
    if (action === "theme") {
      this.#pressTheme();
      return;
    }
    if (action === "area") {
      this.#showArea(control?.getAttribute("data-area-id") ?? "");
      return;
    }
    if (control !== undefined && this.#desk.press(control)) return;
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // A field changed: the area keeps the words under it in step with what is typed (the field itself is never bound).
  readonly #onHostInput = (event: Event): void => {
    const target = event.composedPath()[0];
    if (target instanceof HTMLInputElement || target instanceof HTMLSelectElement) {
      const name = target.getAttribute("data-field");
      if (name !== null) this.#desk.typed?.(name, target.value, target.getAttribute("data-key") ?? undefined);
    }
  };

  /** Framed by the Work shell: the host asks this app to run its own sign out when the person switches role (item 8e); standalone, nothing listens. */
  #stopHostSignOut: (() => void) | null = null;

  /** Framed by the Work shell: its account menu carries Theme and Feedback for this app and asks through one message. */
  readonly #onChromeAction = (event: MessageEvent): void => {
    const action = chromeActionFrom(event, window);
    if (action === "theme") this.#pressTheme();
    if (action === "feedback") this.#openFeedback();
  };

  /** Feedback's press: the shell's account menu asks for it too, when this desk is framed. */
  #openFeedback(): void {
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback) widget.show();
  }

  connectedCallback(): void {
    super.connectedCallback();
    if (this.#framed) {
      window.addEventListener("message", this.#onChromeAction);
      announceChromeControls(window, ["theme", "feedback"]);
    }
    this.#stopHostSignOut = listenForHostSignOut(() => this.#onSignOut());
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("input", this.#onHostInput);
    if (this.#custody.mode === "console") {
      this.#custody.begin("buying-and-stock", (outcome) => this.#onRecovered(outcome));
      return;
    }
    const handoff = readFrontDoorHandoff();
    if (handoff.kind === "ready") {
      const adopted = this.#auth.adopt({
        token: handoff.session.token,
        tokenExpiresAtMs: handoff.session.tokenExpiresAtMs,
        user: {
          userId: handoff.session.user.userId,
          email: handoff.session.user.email,
          name: handoff.session.user.name,
        },
      });
      if (adopted) {
        this.#roleLabel = handoff.session.user.name;
        clearFrontDoorHandoff();
        this.#enterAs(handoff.session.roleCode);
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  // Console mode: this page recovered its own session, or could not. A role the matrix gives this
  // app lands on its screen; an unavailable page keeps the unavailable entry while the custody retries;
  // a session the front door must settle goes back there.
  #onRecovered(outcome: AppStart): void {
    if (outcome.kind === "ready") {
      this.#roleLabel = outcome.entry.roleLabel;
      this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      this.#enterAs(outcome.entry.roleCode);
    } else if (outcome.kind === "front-door" && outcome.why === "ended") {
      this.#onSignOut();
      return;
    } else {
      this.#entryView = entryUnavailable();
      if (outcome.kind === "front-door") this.#custody.toFrontDoor();
    }
    this.requestUpdate();
  }

  disconnectedCallback(): void {
    this.#stopHostSignOut?.();
    this.#stopHostSignOut = null;
    window.removeEventListener("message", this.#onChromeAction);
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("input", this.#onHostInput);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) widget.send = this.#feedbackSender;
    // A draft's starting field values: set once per draft, after the render that drew them.
    this.#desk.seed?.((name, value, key) => {
      const field = this.#fieldElement(name, key);
      if (field !== null && field.value !== value) field.value = value;
    });
    // The framed band (GAPS-r2 row G1): the board's white band, the ONE band
    // when the app stands alone; framed by the Work shell, the host's band is
    // the one band. The bell stands quiet: wiring its unread count is a
    // follow-up. The account chip's menu carries what the app-local header
    // used to (theme, feedback, switch role, sign out): the actions ride the
    // band's accountMenu template and their clicks bubble back to #onHostClick.
    // Framed, no band applies the theme: the app does.
    if (this.#framed) applyDocumentTheme(this.#theme);
    const band = this.renderRoot.querySelector<ShellHeader>("hh-shell-header");
    if (band !== null) {
      band.org = FRAMED_BAND_ORG;
      band.demo = true;
      band.user = framedBandAccount(this.#sessions.current?.user.name ?? "", this.#roleLabel);
      band.breadcrumb = framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface(this.#area));
      band.groups = [];
      band.currentKey = "";
      band.syncState = "idle";
      band.variant = "band";
      band.theme = this.#theme;
      const signedIn = this.#sessions.current !== null;
      band.accountMenu = html`
        ${signedIn
          ? html`<p>Signed in as <strong data-testid="signed-in-role">${this.#roleLabel}</strong></p>`
          : nothing}
        ${this.renderThemeControl()}
        ${signedIn
          ? html`
              <button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>
              <button type="button" class="btn-secondary" data-action="sign-out">Sign out</button>
            `
          : nothing}
      `;
    }
  }

  private async loadFlows(): Promise<void> {
    this.#entryView = { flows: { kind: "loading" }, busy: false, message: null };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).listFlows();
    if (result.ok) {
      this.#entryView = { flows: { kind: "ready", flows: result.value }, busy: false, message: null };
    } else {
      this.#entryView = { flows: { kind: "unavailable", status: null }, busy: false, message: null };
    }
    this.requestUpdate();
  }

  async #chooseFlow(flow: string): Promise<void> {
    this.#entryView = { ...this.#entryView, busy: true };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).chooseFlow(flow);
    if (result.ok) {
      const adopted = this.#auth.adopt(result.value);
      if (adopted) {
        this.#roleLabel = result.value.user.name;
        this.#enterAs(result.value.roleCode);
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Sign-in did not go through. Try the role again." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Sign-in did not go through. ${result.error}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#roleCode = "";
    this.#areas = this.#newAreas();
    this.#area = "orders";
    this.#shown.clear();
    this.#shown.add("orders");
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  // The theme control now rides the band's account menu; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button type="button" class="theme-btn" data-action="feedback" title="Send feedback about this screen">Feedback</button><button type="button" class="theme-btn" data-action="theme" data-testid="theme-control">${label}</button>`;
  }

  // The area switcher: one tab per area, the open one a pill. The words are the areas' own.
  private renderAreas(): TemplateResult {
    return html`<nav class="areas" aria-label="Buying and Stock areas">${this.#areas.map(
      (a) => html`<button type="button" class="tab" data-action="area" data-area-id=${a.id} aria-current="${this.#area === a.id ? "true" : "false"}">${a.label}</button>`,
    )}</nav>`;
  }

  private renderSignedIn(): TemplateResult {
    return html`
      ${this.#framed ? nothing : html`<hh-shell-header data-testid="framed-band"></hh-shell-header>`}
      <main class="desk">
        ${this.renderAreas()}
        ${this.#desk.render()}
      </main>
      <hh-feedback screen="Buying and Stock" trigger="external"></hh-feedback>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      ${this.#framed ? nothing : html`<hh-shell-header data-testid="framed-band"></hh-shell-header>`}
      <main class="desk">
        <h1>Buying and Stock</h1>
        <p class="dim">Purchase orders and the reorder worklist for the buying seats; the stock lookup, read only, for Sales. Demo data only.</p>
        ${renderEntry(this.#entryView)}
      </main>
      <hh-feedback screen="Buying and Stock" trigger="external"></hh-feedback>
    `;
  }
}

defineElement("buying-and-stock-app", BuyingAndStockApp);
