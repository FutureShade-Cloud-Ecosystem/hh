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
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// hos-app-header: the shared chrome element (decision log row 938).
import "@hardscapehouse/design-system/src/header.js";
import { listenForHostSignOut } from "@hardscapehouse/design-system/src/framed.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import { browserSignal, noSignalFetch } from "@hardscapehouse/work-offline/guard";
import { HHFeedback, type FeedbackSender } from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { entryStyles, renderEntry, type EntryView } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { PoApi } from "./po/po-api.js";
import { PoDesk } from "./po/po-desk.js";
import { poStyles } from "./po/po-styles.js";
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
  static styles = [baseStyles, entryStyles, poStyles, areaStyles, purchStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { padding: var(--hh-s-4) 0; }
  `];

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // two demo routes. It is the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface;

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController;

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: "",
    // The Work shell's rule: money never queues. With no signal a write is refused before it dials, with one honest state.
    fetch: noSignalFetch(undefined, browserSignal()),
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => DEMO_ORG }),
  });

  // The areas, each a desk of its own (src/po, src/landed, ...): its state, its loads, its
  // presses. The fields a desk reads are looked up in this element's own shadow root. The
  // purchase orders are first and load at sign in; the others load when first shown.
  #areas: readonly AreaEntry[] = this.#newAreas();
  #area = "orders";
  readonly #shown = new Set<string>(["orders"]);

  #newAreas(): AreaEntry[] {
    const host = {
      repaint: () => this.requestUpdate(),
      field: (name: string, key?: string) => fieldValue(this.#fieldElement(name, key)),
      role: () => this.#roleCode,
    };
    return [
      { id: "orders", label: "Purchase orders", desk: new PoDesk(new PoApi(this.#staffClient), host) },
      { id: "receiving", label: "Receiving", desk: new ReceivingDesk(new ReceivingApi(this.#staffClient), host) },
      { id: "vendors", label: "Vendors", desk: new VendorsDesk(new VendorsApi(this.#staffClient), host) },
      { id: "bills", label: "Vendor bills", desk: new BillsDesk(new BillsApi(this.#staffClient), host) },
      { id: "landed", label: "Landed cost", desk: new LandedDesk(new LandedApi(this.#staffClient), host) },
      { id: "inventory", label: "Inventory", desk: new InventoryDesk(new InventoryApi(this.#staffClient), host) },
      { id: "counts", label: "Cycle counts", desk: new CountsDesk(new CountsApi(this.#staffClient), host) },
    ];
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

  constructor() {
    super();
    this.#demoAccount = new DemoAccount({ client: this.#demoClient });
    this.#auth = new AuthController({ account: this.#demoAccount, sessions: this.#sessions });
  }

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The signed in role's code: it decides which controls an area offers.
  #roleCode = "";

  // The entry cards carry data-action="choose" and data-flow; one host
  // listener reads them, as the till's does.
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
      const widget = this.renderRoot.querySelector("hh-feedback");
      if (widget instanceof HHFeedback) widget.show();
      return;
    }
    if (action === "theme") {
      this.#theme = this.#theme === "light" ? "dark" : "light";
      this.requestUpdate();
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

  connectedCallback(): void {
    super.connectedCallback();
    this.#stopHostSignOut = listenForHostSignOut(() => this.#onSignOut());
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("input", this.#onHostInput);
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
        this.#roleCode = handoff.session.roleCode;
        clearFrontDoorHandoff();
        void this.#areas[0]!.desk.load();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.#stopHostSignOut?.();
    this.#stopHostSignOut = null;
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("input", this.#onHostInput);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) widget.send = this.#feedbackSender;
    // A draft's starting field values: set once per draft, after the render that drew them.
    this.#desk.seed?.((name, value, key) => {
      const field = this.#fieldElement(name, key);
      if (field !== null && field.value !== value) field.value = value;
    });
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
        this.#roleCode = result.value.roleCode;
        void this.#areas[0]!.desk.load();
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
    this.#areas = this.#newAreas();
    this.#area = "orders";
    this.#shown.clear();
    this.#shown.add("orders");
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button slot="theme" type="button" class="theme-btn" data-action="feedback" title="Send feedback about this screen">Feedback</button><button slot="theme" type="button" class="theme-btn" data-action="theme">${label}</button>`;
  }

  // The area switcher: one tab per area, the open one a pill. The words are the areas' own.
  private renderAreas(): TemplateResult {
    return html`<nav class="areas" aria-label="Buying and Stock areas">${this.#areas.map(
      (a) => html`<button type="button" class="tab" data-action="area" data-area-id=${a.id} aria-current="${this.#area === a.id ? "true" : "false"}">${a.label}</button>`,
    )}</nav>`;
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo compact product-name="Yard">${this.renderThemeControl()}</hos-app-header>
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
      <hos-app-header demo compact product-name="Yard">${this.renderThemeControl()}</hos-app-header>
      <main class="desk">
        <h1>Buying and Stock</h1>
        <p class="dim">Purchase orders and the reorder worklist for the procurement manager. Demo data only.</p>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("buying-and-stock-app", BuyingAndStockApp);
