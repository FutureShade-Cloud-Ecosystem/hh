// ProAppBase: what the three Pro apps share, so each app's root is a few lines
// of its own words. It wires the TenantSession seam, the tenant client, the
// sign in screen and the Pro frame, and runs the landing surface's loading,
// empty, error and refusal states. An app subclasses it, answers config() with
// its app key, its sign in adapter and its landing surface, and registers the
// element.
//
// Its single landing: the base runs ONE landing list and keeps its state
// private. That is its whole job and it stays so. Pro Procurement, Pro Pay and
// Pro Field each have several surfaces with drill-downs and sheets, so each
// wires the kit's elements (session, tenant client, hh-pro-signin, hh-pro-frame,
// hh-pro-state) in its own root and lets the catalogue in surfaces.ts name the
// selector entries. Nothing here is generalised for them; the base serves a new
// app that needs one list and the kit's tests.
//
// The flow: signed out shows hh-pro-signin; the person submits, the session
// signs in (the adapter proves who they are, tenant me answers the role and the
// capabilities), and the frame appears around the landing surface. The landing
// surface loads through the tenant client. A role that does not hold the
// surface's capability sees the refusal state without a call; a 403 from the
// server is the same refusal, a 401 signs the person out; any other failure is the error state with a retry; an empty answer is the empty state in the app's own words.
//
// Position gate: no bound property or event in any template; the root listens
// on itself and acts on data- action names, and tells the child elements what
// they show by assignment after render (updated()).
import { html, css, type TemplateResult } from "lit";
import { HHElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient, tenantAuth, type Result } from "@hardscapehouse/api-client";
import { CAPABILITIES, roleLabel, type Capability } from "./capabilities.js";
import { tenantClient } from "./client.js";
import { InMemoryTenantSession, type TenantSession, type TenantSignInAdapter } from "./session.js";
import { appOf, surfaceOf, type ProAppKey } from "./surfaces.js";
import { chipStyles } from "./chips.js";
import { monoStyles } from "./format.js";
import "./frame.js";
import "./signin.js";
import "./state-view.js";
import type { HHProFrame, SurfaceSelect } from "./frame.js";
import type { HHProSignIn, SignInSubmit } from "./signin.js";

/** What an app's landing surface is: one list, read through the tenant client, shown row by row. */
export interface LandingSpec<T> {
  /** The surface id (a surface of this app in surfaces.ts). */
  surface: string;
  /** The capability the surface needs; a role without it sees the refusal state with no call made. */
  capability: Capability;
  load(client: ApiClient): Promise<Result<readonly T[]>>;
  row(item: T): TemplateResult;
  /** The words under the empty state, in the app's own voice. */
  emptyWords: string;
}

export interface ProAppConfig<T> {
  app: ProAppKey;
  adapter: TenantSignInAdapter;
  landing: LandingSpec<T>;
}

type View<T> =
  | { kind: "idle" }
  | { kind: "loading" }
  | { kind: "ready"; items: readonly T[] }
  | { kind: "empty" }
  | { kind: "error" }
  | { kind: "refusal" };

export abstract class ProAppBase<T> extends HHElement {
  static styles = [
    baseStyles,
    chipStyles,
    monoStyles,
    css`
      :host { display: block; min-height: 100vh; }
      h1 { margin: 0 0 var(--hh-s-12); font-size: 1.25rem; }
      .seg-btn { color: var(--hh-text); }
      ul.rows { list-style: none; margin: 0; padding: 0; border: 1px solid var(--hh-line); border-radius: var(--hh-radius-card); overflow: hidden; }
      li.row { display: flex; flex-wrap: wrap; align-items: center; gap: var(--hh-s-12); padding: var(--hh-s-12) var(--hh-s-16); background: var(--hh-row); border-bottom: 1px solid var(--hh-line); }
      .name { font-weight: 600; }
      li.row:nth-child(even) { background: var(--hh-row-alt); }
    `,
  ];

  /** The app's key, its sign in adapter and its landing surface. */
  protected abstract config(): ProAppConfig<T>;

  readonly #config: ProAppConfig<T>;
  readonly #session: TenantSession;
  readonly #client: ApiClient;
  #view: View<T> = { kind: "idle" };
  #surface: string;
  #dealerName = "";
  #message = "";
  #busy = false;
  #loads = 0;
  #unsubscribe: (() => void) | null = null;

  constructor() {
    super();
    this.#config = this.config();
    this.#surface = this.#config.landing.surface;
    this.#session = new InMemoryTenantSession({ adapter: this.#config.adapter });
    this.#client = tenantClient(this.#session);
  }

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.getAttribute("data-action") === "retry");
    if (control !== undefined) void this.#loadLanding();
  };

  readonly #onSignIn = (event: Event): void => {
    void this.#signIn((event as CustomEvent<SignInSubmit>).detail);
  };

  readonly #onSelect = (event: Event): void => {
    const { app, surface } = (event as CustomEvent<SurfaceSelect>).detail;
    if (app === this.#config.app) {
      this.#surface = surface;
      this.requestUpdate();
      return;
    }
    const target = appOf(app);
    if (target !== undefined) window.location.assign(target.path);
  };

  readonly #onSignOut = (): void => {
    void this.#session.signOut();
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
    this.addEventListener("pro-signin-submit", this.#onSignIn);
    this.addEventListener("pro-surface-select", this.#onSelect);
    this.addEventListener("pro-sign-out", this.#onSignOut);
    this.#unsubscribe = this.#session.subscribe(() => {
      if (this.#session.profile() === null) {
        this.#view = { kind: "idle" };
        this.#surface = this.#config.landing.surface;
      }
      this.requestUpdate();
    });
    void this.#loadDealer();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
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

  async #signIn(input: SignInSubmit): Promise<void> {
    this.#busy = true;
    this.#message = "";
    this.requestUpdate();
    const result = await this.#session.signIn(input.email, input.password);
    this.#busy = false;
    if (result.ok) {
      this.#message = "";
      void this.#loadLanding();
    } else {
      this.#message = result.error;
    }
    this.requestUpdate();
  }

  async #loadLanding(): Promise<void> {
    const sequence = (this.#loads += 1);
    const landing = this.#config.landing;
    if (!this.#session.can(landing.capability)) {
      this.#view = { kind: "refusal" };
      this.requestUpdate();
      return;
    }
    this.#view = { kind: "loading" };
    this.requestUpdate();
    const answer = await landing.load(this.#client);
    if (sequence !== this.#loads || this.#session.profile() === null) return;
    if (answer.ok) {
      this.#view = answer.value.length === 0 ? { kind: "empty" } : { kind: "ready", items: answer.value };
    } else if (answer.status === 401) {
      // The session is gone (expired, or the person was deactivated): sign out and say so, never blame the role.
      this.#message = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else if (answer.status === 403) {
      this.#view = { kind: "refusal" };
    } else {
      this.#view = { kind: "error" };
    }
    this.requestUpdate();
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
      signin.message = this.#message;
      signin.busy = this.#busy;
    }
  }

  #renderLanding(): TemplateResult {
    const landing = this.#config.landing;
    const view = this.#view;
    switch (view.kind) {
      case "ready":
        return html`<ul class="rows" data-testid="landing-rows">${view.items.map((item) => html`<li class="row">${landing.row(item)}</li>`)}</ul>`;
      case "empty":
        return html`<hh-pro-state data-state="empty" data-testid="landing-state">${landing.emptyWords}</hh-pro-state>`;
      case "error":
        return html`<hh-pro-state data-state="error" data-testid="landing-state">This did not load. Nothing was changed.</hh-pro-state>`;
      case "refusal":
        return html`<hh-pro-state data-state="refusal" data-testid="landing-state"></hh-pro-state>`;
      default:
        return html`<hh-pro-state data-state="loading" data-testid="landing-state"></hh-pro-state>`;
    }
  }

  render(): TemplateResult {
    const profile = this.#session.profile();
    if (profile === null) return html`<hh-pro-signin></hh-pro-signin>`;
    const appKey = this.#config.app;
    const surface = surfaceOf(appKey, this.#surface);
    const held = CAPABILITIES.filter((c) => this.#session.can(c)).join(" ");
    const isLanding = this.#surface === this.#config.landing.surface;
    return html`<hh-pro-frame data-app="${appKey}" data-surface="${this.#surface}" data-dealer="${this.#dealerName}" data-role="${roleLabel(profile.role)}" data-capabilities="${held}">
      <h1 data-testid="surface-title">${surface === undefined ? "" : surface.label}</h1>
      ${isLanding ? this.#renderLanding() : html`<hh-pro-state data-state="empty" data-testid="landing-state">This surface is not open yet.</hh-pro-state>`}
    </hh-pro-frame>`;
  }
}
