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
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { entryStyles, renderEntry, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { demoBanner, demoBannerStyles } from "./demo-banner.js";
import { HOUSE_ZONE, houseDayInZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { setBranches, wirePorts, clearSessionState, type AuthUser } from "@hardscapehouse/pos-kit/src/ported/ports.js";
import { QdClient, type BranchRef } from "./qd-client.js";
import { PricingClient } from "./pricing-client.js";
import { THEME_LABEL, ThemeController, themeControlLabel, themeStatusText } from "./theme.js";
// The builder shell registers the work area and the panel seams.
import "./quote-builder.js";
// The pricing panel (QD-4), mounted here until Dealer Admin has a screen of its own.
import "./pricing-panel.js";

const DEMO_ORG = "hh-demo";

export class QuoteDeskApp extends HHElement {
  static styles = [
    baseStyles,
    demoBannerStyles,
    entryStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }
      header.bar {
        display: flex;
        align-items: center;
        flex-wrap: wrap;
        gap: var(--hh-s-8) var(--hh-s-16);
        min-height: 48px;
        box-sizing: border-box;
        padding: var(--hh-s-8) var(--hh-s-20);
        background: var(--hh-card);
        border-bottom: 1px solid var(--hh-line);
      }
      .brand {
        display: flex;
        align-items: center;
        gap: var(--hh-s-12);
      }
      .mark {
        display: inline-flex;
        align-items: center;
        justify-content: center;
        width: 30px;
        height: 30px;
        border-radius: var(--hh-radius-control);
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
        font-size: 12px;
        font-weight: 700;
        letter-spacing: 0.02em;
      }
      .brand h1 {
        font-size: 17px;
        font-weight: 600;
        margin: 0;
        white-space: nowrap;
      }
      .bar-demo {
        display: flex;
        min-width: 0;
      }
      .bar-user {
        margin: 0 0 0 auto;
        display: flex;
        align-items: center;
        gap: var(--hh-s-8);
      }
      .role-name {
        margin: 0;
        font-weight: 600;
        font-size: 13px;
        padding: var(--hh-s-4) var(--hh-s-12);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-pill);
        background: var(--hh-page);
        white-space: nowrap;
      }
      header.bar button {
        min-height: 0;
        padding: 7px 14px;
        font-size: 12px;
        line-height: normal;
        white-space: nowrap;
      }
      header.bar button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      @media (max-width: 1100px) {
        .bar-demo {
          order: 3;
          flex-basis: 100%;
        }
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
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  readonly #client: QdClient;
  readonly #pricing: PricingClient;
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
  /** The theme choice: Auto follows the system, Light and Dark pin it; tab memory only (see theme.ts). */
  readonly #theme = new ThemeController(() => this.requestUpdate());
  /** What a screen reader hears after a Theme press: focus stays on the button, so the new name is not re-read. */
  #themeStatus = "";
  /** The mounted surface: the entry cards, or the desk. */
  #surface: "entry" | "desk" = "entry";

  constructor() {
    super();
    const staffHeaders = authHeaderProvider({
      callerClass: "staff",
      sessions: this.#sessions,
      activeOrg: () => DEMO_ORG,
    });
    const client = new ApiClient({ baseUrl: "", headers: staffHeaders });
    // The demo adapter's own client carries no staff bearer: the demo routes
    // are pre-auth by design (the tap a role sign-in).
    const demoClient = new ApiClient({
      baseUrl: "",
      headers: async () => ({ "X-Org": DEMO_ORG }),
    });
    this.#account = new DemoAccount({ client: demoClient });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    this.#client = new QdClient(client, () => this.#deskUserId ?? "");
    this.#pricing = new PricingClient(client);
    // The shared kit's host wiring: one door, wired once, reading custody
    // through this element's own closures so the bearer never rests in a
    // second place.
    wirePorts({
      headers: staffHeaders,
      houseDay: () => houseDayInZone(HOUSE_ZONE),
      user: () => this.#deskIdentity,
      token: () => this.#sessions.current?.token ?? "",
    });
  }

  override connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule: listen on the HOST and read the control off
    // composedPath(), never an event binding in a template.
    this.addEventListener("click", this.#onHostClick);
    // The system's light setting is honoured from the first paint.
    this.#theme.start();
    void this.#loadFlows();
  }

  protected override updated(): void {
    // The wiring pass: the builder receives the validated client the same
    // way every shared component receives its facts.
    const builder = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-quote-builder");
    if (builder !== null) builder.client = this.#client;
    const pricing = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-pricing-panel");
    if (pricing !== null) {
      pricing["pricing"] = this.#pricing;
      pricing["branchId"] = this.#branch?.id ?? "";
    }
  }

  override disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.#theme.stop();
    super.disconnectedCallback();
  }

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
    if (control.getAttribute("data-action") === "pricing") {
      this.#view = this.#view === "pricing" ? "quotes" : "pricing";
      this.requestUpdate();
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

  async #chooseFlow(flow: string): Promise<void> {
    if (this.#busy || flow === "") return;
    this.#busy = true;
    this.#message = null;
    this.requestUpdate();
    const minted = await this.#account.chooseFlow(flow);
    if (!minted.ok) {
      this.#busy = false;
      this.#message = `Demo sign-in refused: ${minted.error}`;
      this.requestUpdate();
      return;
    }
    const adopted = this.#controller.adopt(minted.value);
    this.#busy = false;
    if (!adopted) {
      this.#message = "Demo sign-in refused: the mint's session was not usable.";
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
  async #initDesk(): Promise<void> {
    const branch = await this.#client.resolveBranch();
    if (!branch.ok) {
      this.#message = `The desk did not start: ${branch.error}`;
      this.#roleLabel = null;
      this.#deskUserId = null;
      this.#deskIdentity = null;
      await this.#controller.signOut();
      this.requestUpdate();
      return;
    }
    this.#branch = branch.value;
    setBranches([{ id: branch.value.id, name: branch.value.name }]);
    this.#surface = "desk";
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
    await this.#controller.signOut();
    this.#busy = false;
    this.#roleLabel = null;
    this.#deskUserId = null;
    this.#deskIdentity = null;
    this.#branch = null;
    this.#message = null;
    this.#view = "quotes";
    this.#surface = "entry";
    // The shared surface's session-owned module state goes with the session.
    clearSessionState();
    this.requestUpdate();
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
        title="Cycle the theme: Auto, Light, Dark"
      >
        Theme: ${THEME_LABEL[choice]}
      </button>
    `;
  }

  render(): TemplateResult {
    const demoPresent =
      this.#surface === "desk" || (this.#flowsState.kind === "ready" && this.#flowsState.flows.length > 0);
    return html`
      <p class="visually-hidden" role="status" aria-live="polite" data-testid="theme-status">${this.#themeStatus}</p>
      <header class="bar" data-testid="qd-bar">
        <div class="brand">
          <span class="mark" data-testid="qd-mark" aria-hidden="true">QD</span>
          <h1 data-testid="qd-title">Quote Desk</h1>
        </div>
        ${demoPresent ? html`<div class="bar-demo">${demoBanner()}</div>` : nothing}
        ${this.#roleLabel === null
          ? html`<p class="bar-user">${this.#themeControl()}</p>`
          : html`
              <p class="bar-user">
                <span class="role-name" data-testid="signed-in-role">${this.#signedInRoleLabel()}</span>
                ${this.#surface === "desk"
                  ? html`<button type="button" class="btn-secondary" data-action="pricing" data-testid="pricing-toggle" aria-pressed=${this.#view === "pricing" ? "true" : "false"}>
                      ${this.#view === "pricing" ? "Quotes" : "Pricing"}
                    </button>`
                  : nothing}
                ${this.#themeControl()}
                <button type="button" class="btn-secondary" data-action="sign-out" ?disabled=${this.#busy}>
                  Sign out
                </button>
              </p>
            `}
      </header>
      <main>
        ${this.#surface === "desk"
          ? html`
              <div class="surface ${this.#view === "pricing" ? "away" : ""}">
                <qd-quote-builder></qd-quote-builder>
              </div>
              ${this.#view === "pricing"
                ? html`<div class="pricing" data-testid="pricing-screen"><qd-pricing-panel></qd-pricing-panel></div>`
                : nothing}
            `
          : renderEntry({ flows: this.#flowsState, busy: this.#busy, message: this.#message })}
      </main>
    `;
  }
}

defineElement("qd-app-root", QuoteDeskApp);
