// The Counter Till's root element: the thin UI layer the house invariants ask
// for. It renders core and package state and calls package actions; nothing
// domain-shaped lives here (no prices, no money, no order logic: the till's
// sale work builds on this shell later in the cycle).
//
// State: the entry view's flows answer (loading, unavailable, or the flow
// rows), and the signed-in role once a card is tapped. Rendering is Lit, the
// design system supplies the tokens and the primitives, and the auth package
// supplies custody through its injected AccountSurface seam: this app never
// touches the token itself, the demo adapter owns the mint.
//
// The organisation header is the demo org by construction: the whole app is
// the demo's till, and the staff header provider refuses to send a request
// without it, so no call ever leaves this app unresolved.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "./demo-account.js";
import { TillClient, type BranchRef } from "./till-client.js";
import { formatMoney } from "./money.js";
import { demoBanner, demoBannerStyles } from "./demo-banner.js";
import { entryStyles, renderEntry, type FlowsState } from "./views/entry.js";
import { tillStyles, renderTill, type DrawerPanel, type SearchPanel, type TillView } from "./views/till.js";

const DEMO_ORG = "hh-demo";

const FRESH_DRAWER_PANEL: DrawerPanel = {
  state: "resolving",
  how: null,
  branchName: null,
  station: null,
  float: null,
  message: null,
  busy: false,
};

const FRESH_SEARCH_PANEL: SearchPanel = { state: "idle", hits: [], message: null };

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    demoBannerStyles,
    entryStyles,
    tillStyles,
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
      }
      .role-name {
        margin: 0;
        font-weight: 600;
      }
      main {
        padding: var(--hh-s-20);
      }
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  readonly #tillClient: TillClient;
  #flowsState: FlowsState = { kind: "loading" };
  #busy = false;
  #message: string | null = null;
  #till: { label: string } | null = null;
  #branch: BranchRef | null = null;
  #drawer: DrawerPanel = FRESH_DRAWER_PANEL;
  #search: SearchPanel = FRESH_SEARCH_PANEL;

  constructor() {
    super();
    const client = new ApiClient({
      baseUrl: "",
      headers: authHeaderProvider({
        callerClass: "staff",
        sessions: this.#sessions,
        activeOrg: () => DEMO_ORG,
      }),
    });
    this.#account = new DemoAccount({ client });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    this.#tillClient = new TillClient(client);
  }

  connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule, carried for this app's own templates: listen on
    // the HOST and read the control off composedPath(), never @click in a
    // template and never event.target.closest, because the shadow boundary
    // retargets the target to this element (the design system's README holds
    // both halves).
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("submit", this.#onHostSubmit);
    void this.#loadFlows();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("submit", this.#onHostSubmit);
    super.disconnectedCallback();
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const flow = control.getAttribute("data-flow");
    if (control.getAttribute("data-action") === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
    if (control.getAttribute("data-action") === "sign-out") {
      void this.#signOut();
    }
    if (control.getAttribute("data-action") === "retry-drawer") {
      void this.#ensureDrawer();
    }
  };

  readonly #onHostSubmit = (event: Event): void => {
    const form = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-form"));
    if (form === undefined) return;
    event.preventDefault();
    const kind = form.getAttribute("data-form");
    if (kind === "product-search") {
      const input = form.querySelector('[data-input="product-search"]');
      void this.#searchProducts(input instanceof HTMLInputElement ? input.value : "");
    }
  };

  async #loadFlows(): Promise<void> {
    const result = await this.#account.listFlows();
    this.#flowsState = result.ok ? { kind: "ready", flows: result.value } : { kind: "unavailable" };
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
    this.#till = { label: this.#labelFor(flow) };
    this.requestUpdate();
    void this.#initTill();
  }

  #labelFor(flow: string): string {
    if (this.#flowsState.kind === "ready") {
      const row = this.#flowsState.flows.find((candidate) => candidate.flow === flow);
      if (row !== undefined && typeof row.label === "string") return row.label;
    }
    return flow;
  }

  /**
   * Sign out: the controller ends the mint (the adapter drops it) and wipes
   * custody either way; the entry view with the cards comes back, because the
   * flows answer is still held. A reload signs out too: the mint lives only
   * in this tab's memory.
   */
  async #signOut(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.requestUpdate();
    await this.#controller.signOut();
    this.#busy = false;
    this.#till = null;
    this.#message = null;
    this.#branch = null;
    this.#drawer = FRESH_DRAWER_PANEL;
    this.#search = FRESH_SEARCH_PANEL;
    this.requestUpdate();
  }

  /**
   * The till's own start: resolve the branch the login reaches, then find or
   * open its drawer. Each step renders its own state, so a refusal names
   * itself in words and the screen never invents a branch or a float.
   */
  async #initTill(): Promise<void> {
    this.#drawer = FRESH_DRAWER_PANEL;
    this.requestUpdate();
    const branch = await this.#tillClient.resolveBranch();
    if (!branch.ok) {
      this.#drawer = { ...FRESH_DRAWER_PANEL, state: "refused", message: branch.error };
      this.requestUpdate();
      return;
    }
    this.#branch = branch.value;
    await this.#ensureDrawer();
  }

  async #ensureDrawer(): Promise<void> {
    if (this.#branch === null) return;
    this.#drawer = { ...FRESH_DRAWER_PANEL, busy: true };
    this.requestUpdate();
    const result = await this.#tillClient.ensureDrawer(this.#branch.id);
    if (!result.ok) {
      this.#drawer = {
        ...FRESH_DRAWER_PANEL,
        state: "refused",
        branchName: this.#branch.name,
        message: result.error,
      };
    } else {
      this.#drawer = {
        state: "open",
        how: result.value.how,
        branchName: this.#branch.name,
        station: result.value.drawer.station,
        float: formatMoney(result.value.drawer.floatCents),
        message: null,
        busy: false,
      };
    }
    this.requestUpdate();
  }

  async #searchProducts(query: string): Promise<void> {
    const trimmed = query.trim();
    if (trimmed === "") {
      this.#search = { state: "idle", hits: [], message: null };
      this.requestUpdate();
      return;
    }
    this.#search = { state: "loading", hits: [], message: null };
    this.requestUpdate();
    const result = await this.#tillClient.searchProducts(trimmed);
    this.#search = result.ok
      ? { state: "ready", hits: result.value, message: null }
      : { state: "refused", hits: [], message: result.error };
    this.requestUpdate();
  }

  #tillView(): TillView {
    return { drawer: this.#drawer, search: this.#search };
  }

  render(): TemplateResult {
    const demoPresent = this.#till !== null || this.#flowsState.kind === "ready";
    return html`
      <header>
        ${demoPresent ? demoBanner() : nothing}
        <div class="title-row">
          <h1>Counter Till</h1>
          ${this.#till === null
            ? nothing
            : html`
                <p class="signed-in">
                  <span class="role-name" data-testid="signed-in-role">${this.#till.label}</span>
                  <button type="button" class="btn-secondary" data-action="sign-out" ?disabled=${this.#busy}>
                    Sign out
                  </button>
                </p>
              `}
        </div>
      </header>
      <main>
        ${this.#till === null
          ? renderEntry({ flows: this.#flowsState, busy: this.#busy, message: this.#message })
          : renderTill(this.#tillView())}
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
