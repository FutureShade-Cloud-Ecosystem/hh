// The app root: the Pro kit's pieces (the TenantSession seam, the tenant
// client, the sign in screen, the Pro frame, the four states) wired to this
// app's own surface registry (registry.ts). The kit's ProAppBase runs one
// landing surface; this app's surfaces are several, with drill-downs and
// modals, so the root here does the wiring itself on the same seams and the
// same delegation rules: the root listens on itself and acts on data-action
// names, views speak only through data-action and data-field attributes, and
// child elements are told what they show by assignment after render (updated),
// the design system's position gate refusing every bound property in a
// template.
//
// The sign in adapter is the stub only where Vite serves a development build
// (import.meta.env.DEV is replaced at build time, so a production bundle
// carries only the named seam, realExchangeAdapter, which refuses in plain
// words until the auth lead's go fills it).
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient, tenantAuth } from "@hardscapehouse/api-client";
import {
  CAPABILITIES,
  InMemoryTenantSession,
  appOf,
  can,
  realExchangeAdapter,
  roleLabel,
  stubAdapter,
  tenantClient,
  chipStyles,
  monoStyles,
  type TenantSession,
} from "@hardscapehouse/pro-kit";
import "@hardscapehouse/pro-kit/src/frame.js";
import "@hardscapehouse/pro-kit/src/signin.js";
import "@hardscapehouse/pro-kit/src/state-view.js";
import type { HHProSignIn, SignInSubmit } from "@hardscapehouse/pro-kit/src/signin.js";
import { SURFACES, LANDING, frameSurfaceOf, surfaceRouteFromCatalogue, type SurfaceId } from "./registry.js";
import { sheetStyles, stateBlock, noticeLine, type LoadState, type Page, type Route, type SurfaceAnswer } from "./views.js";
import { todayLoad, todayView, TODAY_EMPTY } from "./today.js";
import { boardLoad, boardView, boardCommand, BOARD_EMPTY } from "./board.js";
import { projectsLoad, projectsView, projectsCommand, PROJECTS_EMPTY } from "./projects.js";
import { scopeLoad, scopeView, scopeCommand, scopeHeading } from "./scope.js";
import { catalogLoad, catalogView, catalogCommand, catalogInput, CATALOG_WORDS } from "./catalog.js";
import { productLoad, productView, productCommand, productHeading } from "./product.js";
import { quotesLoad, quotesView, quotesCommand, quotesHeading, QUOTES_EMPTY } from "./quotes.js";

export class ProProcurementApp extends HHElement {
  static styles = [
    baseStyles,
    chipStyles,
    monoStyles,
    sheetStyles,
    css`
      :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
      h1 { margin: 0 0 var(--hh-s-12); font-size: 1.25rem; }
      .todaybtn {
        min-height: 44px;
        padding: var(--hh-s-4) var(--hh-s-12);
        border: 1px solid var(--hh-control-line);
        border-radius: var(--hh-radius-control);
        background: transparent;
        color: var(--hh-text);
        font: inherit;
        cursor: pointer;
        white-space: nowrap;
      }
      .board-columns { display: flex; flex-direction: column; gap: var(--hh-s-20); }
      .lane { display: flex; flex-direction: column; gap: var(--hh-s-8); }
      ul.lane-body { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--hh-s-8); }
      ul.lane-body > li { margin: 0; }
      .lane-empty { margin: 0; padding: var(--hh-s-12); border: 1px dashed var(--hh-line); border-radius: var(--hh-radius-card); text-align: center; font-size: 0.875rem; }
      @media (min-width: 64rem) {
        .board-columns { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); }
      }
    `,
  ];

  readonly #session: TenantSession;
  readonly #client: ApiClient;
  readonly #page: Page;
  #route: Route = { surface: LANDING };
  #view: LoadState = { kind: "loading" };
  #data: unknown = null;
  #heading = "";
  #notice: { kind: "ok" | "alert"; words: string } | null = null;
  #modal: { testid: string; build: (page: Page) => TemplateResult } | null = null;
  #busy = false;
  #dealerName = "";
  #signinMessage = "";
  #loads = 0;
  #unsubscribe: (() => void) | null = null;

  constructor() {
    super();
    const adapter = import.meta.env.DEV ? stubAdapter() : realExchangeAdapter();
    this.#session = new InMemoryTenantSession({ adapter });
    this.#client = tenantClient(this.#session);
    this.#page = this.#buildPage();
  }

  // The one object a surface's code sees: its route, the session and client,
  // the fields the person typed, and the moves it may make (navigate, refresh,
  // say a notice, open and close the modal, hold the busy flag).
  #buildPage(): Page {
    const page: Page = {
      session: this.#session,
      client: this.#client,
      route: this.#route,
      busy: false,
    field: (name) => {
      const el = this.renderRoot.querySelector(`[data-field="${name}"]`);
      return el instanceof HTMLInputElement || el instanceof HTMLSelectElement || el instanceof HTMLTextAreaElement ? el : null;
    },
    goto: (route) => this.#goto(route),
    refresh: () => void this.#loadRoute(),
    say: (kind, words) => {
      this.#notice = { kind, words };
      this.requestUpdate();
    },
    hold: (busy) => {
      this.#busy = busy;
      this.#page.busy = busy;
      this.requestUpdate();
    },
    touch: () => this.requestUpdate(),
    openModal: (testid, build) => {
      this.#modal = { testid, build };
      this.requestUpdate();
    },
    closeModal: () => this.#closeModal(),
    };
    return page;
  }

  // ---- navigation and loading -------------------------------------------------------------------

  #goto(route: Route): void {
    this.#route = route;
    this.#notice = null;
    this.#modal = null;
    this.#busy = false;
    this.#view = { kind: "loading" };
    this.#data = null;
    this.#heading = "";
    this.#page.route = route;
    void this.#loadRoute();
  }

  #closeModal(): void {
    this.#modal = null;
    this.#busy = false;
    this.#page.busy = false;
    this.requestUpdate();
  }

  async #loadRoute(): Promise<void> {
    const route = this.#route;
    const sequence = (this.#loads += 1);
    const surface = SURFACES[route.surface];
    if (!this.#session.can(surface.capability)) {
      if (sequence === this.#loads) {
        this.#view = { kind: "refusal" };
        this.requestUpdate();
      }
      return;
    }
    this.#view = { kind: "loading" };
    this.requestUpdate();
    const loaders: Record<SurfaceId, (page: Page) => Promise<SurfaceAnswer>> = {
      today: todayLoad,
      board: boardLoad,
      projects: projectsLoad,
      catalog: catalogLoad,
      quotes: quotesLoad,
      scope: scopeLoad,
      product: productLoad,
    };
    const answer = await loaders[route.surface](this.#page);
    if (sequence !== this.#loads || this.#session.profile() === null) return;
    if (answer.kind === "ready") {
      this.#view = { kind: "ready" };
      this.#data = answer.data;
      this.#heading = answer.heading ?? "";
    } else if (answer.kind === "error" && answer.status === 401) {
      // The session is gone (expired, or the person was deactivated): sign out and say so, never blame the role.
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else {
      this.#view = { kind: answer.kind };
    }
    this.requestUpdate();
  }

  // ---- delegation ---------------------------------------------------------------------------------

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action") ?? "";
    const routeAction = SURFACE_ACTIONS[action];
    if (routeAction !== undefined) {
      this.#goto({ surface: routeAction });
      return;
    }
    switch (action) {
      case "retry":
        void this.#loadRoute();
        return;
      case "open-scope":
        this.#goto({ surface: "scope", projectId: control.getAttribute("data-project") ?? "", from: (control.getAttribute("data-from") as SurfaceId | null) ?? undefined });
        return;
      case "open-product":
        this.#goto({ surface: "product", productId: control.getAttribute("data-product") ?? "" });
        return;
      case "open-quote":
        this.#goto({ surface: "quotes", quoteId: control.getAttribute("data-quote") ?? "" });
        return;
      case "dismiss-modal":
        if (!this.#busy) this.#closeModal();
        return;
      default:
        break;
    }
    const commands: Record<SurfaceId, ((action: string, control: Element, page: Page) => boolean) | undefined> = {
      today: undefined,
      board: boardCommand,
      projects: projectsCommand,
      catalog: catalogCommand,
      quotes: quotesCommand,
      scope: scopeCommand,
      product: productCommand,
    };
    if (commands[this.#route.surface]?.(action, control, this.#page) === true) return;
  };

  readonly #onKey = (event: KeyboardEvent): void => {
    if (event.key === "Escape" && this.#modal !== null && !this.#busy) {
      this.#closeModal();
      return;
    }
  };

  readonly #onInput = (event: Event): void => {
    const target = event.composedPath()[0];
    if (!(target instanceof Element)) return;
    const name = target.getAttribute("data-field");
    if (name === null) return;
    const value = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement ? target.value : "";
    const inputs: Record<SurfaceId, ((name: string, value: string, page: Page) => void) | undefined> = {
      today: undefined,
      board: undefined,
      projects: undefined,
      catalog: catalogInput,
      quotes: undefined,
      scope: undefined,
      product: undefined,
    };
    inputs[this.#route.surface]?.(name, value, this.#page);
  };

  readonly #onSignIn = (event: Event): void => {
    void this.#signIn((event as CustomEvent<SignInSubmit>).detail);
  };

  readonly #onSelect = (event: Event): void => {
    const { app, surface } = (event as CustomEvent<{ app: string; surface: string }>).detail;
    if (app !== "procurement") {
      // Another app's surface: that app serves it on its own address.
      const target = appOf(app)?.path;
      if (target !== undefined) window.location.assign(target);
      return;
    }
    const route = surfaceRouteFromCatalogue(surface);
    if (route !== null) this.#goto(route);
  };

  readonly #onSignOut = (): void => {
    void this.#session.signOut();
  };

  async #signIn(input: SignInSubmit): Promise<void> {
    this.#busy = true;
    this.#signinMessage = "";
    this.requestUpdate();
    const result = await this.#session.signIn(input.email, input.password);
    this.#busy = false;
    if (result.ok) {
      this.#route = { surface: LANDING };
      this.#page.route = this.#route;
      this.#signinMessage = "";
      void this.#loadRoute();
    } else {
      this.#signinMessage = result.error;
    }
    this.requestUpdate();
  }

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
    this.addEventListener("keydown", this.#onKey);
    this.addEventListener("input", this.#onInput);
    this.addEventListener("pro-signin-submit", this.#onSignIn);
    this.addEventListener("pro-surface-select", this.#onSelect);
    this.addEventListener("pro-sign-out", this.#onSignOut);
    this.#unsubscribe = this.#session.subscribe(() => {
      if (this.#session.profile() === null) {
        this.#route = { surface: LANDING };
        this.#page.route = this.#route;
        this.#view = { kind: "loading" };
        this.#data = null;
        this.#modal = null;
        this.#notice = null;
      }
      this.requestUpdate();
    });
    void this.#loadDealer();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("keydown", this.#onKey);
    this.removeEventListener("input", this.#onInput);
    this.removeEventListener("pro-signin-submit", this.#onSignIn);
    this.removeEventListener("pro-surface-select", this.#onSelect);
    this.removeEventListener("pro-sign-out", this.#onSignOut);
    this.#unsubscribe?.();
    this.#unsubscribe = null;
    super.disconnectedCallback();
  }

  async #loadDealer(): Promise<void> {
    const answer = await tenantAuth.tenantGetConfig(this.#client);
    if (answer.ok) {
      this.#dealerName = answer.value.dealer_name;
      this.requestUpdate();
    }
  }

  // The children are told what they show by assignment, after render: the
  // design system's position gate refuses every bound property in a template.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const root = document.documentElement;
    root.setAttribute("data-brand", "hh-pro");
    if (this.#session.profile() === null) root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", "light");
    const signin = this.renderRoot.querySelector("hh-pro-signin") as HHProSignIn | null;
    if (signin !== null) {
      signin.dealerName = this.#dealerName;
      signin.message = this.#signinMessage;
      signin.busy = this.#busy;
    }
  }

  #headingFor(): string {
    if (this.#heading !== "") return this.#heading;
    switch (this.#route.surface) {
      case "scope": return scopeHeading(this.#data);
      case "product": return productHeading(this.#data);
      default: return SURFACES[this.#route.surface].label;
    }
  }

  #renderRoute(): TemplateResult {
    const surface = SURFACES[this.#route.surface];
    if (!this.#session.can(surface.capability)) {
      return html`<hh-pro-state data-state="refusal" data-testid="surface-state"></hh-pro-state>`;
    }
    if (this.#view.kind !== "ready") {
      const empties: Record<SurfaceId, string> = {
        today: TODAY_EMPTY,
        board: BOARD_EMPTY,
        projects: PROJECTS_EMPTY,
        catalog: CATALOG_WORDS.empty,
        quotes: QUOTES_EMPTY,
        scope: "",
        product: "",
      };
      return stateBlock(this.#view, { empty: empties[this.#route.surface] }, "surface-state");
    }
    const views: Record<SurfaceId, (page: Page) => TemplateResult> = {
      today: (page) => todayView(this.#data, page),
      board: (page) => boardView(this.#data, page),
      projects: (page) => projectsView(this.#data, page),
      catalog: (page) => catalogView(this.#data, page),
      quotes: (page) => quotesView(this.#data, page),
      scope: (page) => scopeView(this.#data, page),
      product: (page) => productView(this.#data, page),
    };
    return views[this.#route.surface](this.#page);
  }

  render(): TemplateResult {
    const profile = this.#session.profile();
    if (profile === null) return html`<hh-pro-signin></hh-pro-signin>`;
    const top = frameSurfaceOf(this.#route);
    const held = CAPABILITIES.filter((c) => this.#session.can(c)).join(" ");
    return html`<hh-pro-frame data-app="procurement" data-surface="${top}" data-dealer="${this.#dealerName}" data-role="${roleLabel(profile.role)}" data-capabilities="${held}">
      <span slot="actions">${top !== "today" ? html`<button type="button" class="todaybtn" data-action="open-today" data-testid="open-today">Today</button>` : nothing}</span>
      <h1 data-testid="surface-title">${this.#headingFor()}</h1>
      ${noticeLine(this.#notice)}
      ${this.#renderRoute()}
      ${this.#modal === null ? nothing : this.#modal.build(this.#page)}
    </hh-pro-frame>`;
  }
}

const SURFACE_ACTIONS: Readonly<Record<string, SurfaceId>> = {
  "open-today": "today",
  "open-board": "board",
  "open-projects": "projects",
  "open-catalog": "catalog",
  "open-quotes": "quotes",
};

defineElement("pro-procurement-app", ProProcurementApp);
