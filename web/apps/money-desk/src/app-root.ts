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
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import { HHFeedback, type FeedbackSender } from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { entryStyles, renderEntry, type EntryView } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { GlApi } from "./gl/gl-api.js";
import { GlDesk } from "./gl/gl-desk.js";
import { glStyles } from "./gl/gl-styles.js";

const DEMO_ORG = "hh-demo";

// The role whose desk is the ledger slice (src/gl): the approvals queue, the
// journals and the trial balance. The label is the role name the mint and the
// front door's handoff both carry.
const CONTROLLER_ROLE = "Financial Controller";

export class MoneyDeskApp extends HHElement {
  static styles = [baseStyles, entryStyles, glStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { padding: var(--hh-s-4) 0; }
    main.gl-desk { max-width: var(--hh-content-max, 1180px); margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
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
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => DEMO_ORG }),
  });

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // The ledger slice (src/gl): its state, its loads, its presses. The field
  // it reads is looked up in this element's own shadow root.
  #gl = this.#newGlDesk();

  #newGlDesk(): GlDesk {
    return new GlDesk(new GlApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      field: (name) => this.renderRoot.querySelector<HTMLTextAreaElement>(`[data-field="${name}"]`)?.value ?? "",
      userId: () => this.#sessions.current?.user.userId ?? "",
    });
  }

  readonly #feedback = new HHFeedback();

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
    this.#feedback.send = this.#feedbackSender;
  }

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

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
    if (action === "theme") {
      this.#theme = this.#theme === "light" ? "dark" : "light";
      this.requestUpdate();
      return;
    }
    if (control !== undefined && this.#isController() && this.#gl.press(control)) return;
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
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
        this.#roleLabel = handoff.session.roleName;
        clearFrontDoorHandoff();
        if (this.#isController()) void this.#gl.load();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
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
        this.#roleLabel = result.value.identity.roleName;
        if (this.#isController()) void this.#gl.load();
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Demo sign-in refused: the mint's session was not usable." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Demo sign-in refused: ${result.error}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#gl = this.#newGlDesk();
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // current theme.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button slot="theme" type="button" class="btn btn-secondary" data-action="theme">${label}</button>`;
  }

  #isController(): boolean {
    return this.#roleLabel === CONTROLLER_ROLE;
  }

  private renderSignedIn(): TemplateResult {
    const session = this.#sessions.current;
    const profile = session?.user;
    if (this.#isController()) {
      return html`
        <hos-app-header signed-in demo compact product-name="Money Desk" product-sub="Approvals and ledger · HH Work shell">${this.renderThemeControl()}</hos-app-header>
        <main class="gl-desk">
          ${this.#gl.render()}
        </main>
      `;
    }
    return html`
      <hos-app-header signed-in demo></hos-app-header>
      <main>
        <p>Money Desk: ${profile?.name ?? "?"}, ${this.#roleLabel}</p>
      </main>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo>${this.renderThemeControl()}</hos-app-header>
      <main>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("money-desk-app", MoneyDeskApp);
