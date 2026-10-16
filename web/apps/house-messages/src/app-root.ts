// The House Messages root element: the chrome and the way in. The way in is the demo's role cards
// (the shared kit's entry, the demo adapter behind the auth package's AccountSurface seam), offered
// for every role that holds House Messages and whose login the box mints. After the mint, or the
// front door's handoff, the ports are wired with the caller's headers and the desk renders.
//
// The organisation header is the demo org by construction: the staff header provider refuses to
// send a request without X-Org, so no call ever leaves this app unresolved.
//
// The header is fed through plain attributes (signed-in, demo) and, for the role label, an
// assignment in updated(): the design system's position gate refuses every "." and "@" binding and
// every bound value outside the data-, aria-, class and role attributes in a dependent package. The
// controls ride data-action names and the host's click and change listeners below act on them.
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// hos-app-header: the shared chrome element (decision log row 938).
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import { HHFeedback, type FeedbackSender } from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { entryStyles, renderEntry, type EntryView, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { MessagesApi } from "./messages-api.js";
import { MessagesDesk } from "./desk.js";
import { messagesStyles } from "./styles.js";

const DEMO_ORG = "hh-demo";

// Every flow the box mints holds House Messages (the decided matrix gives the cell to all thirteen
// roles), so the entry offers every card the flows route answers.
export class HouseMessagesApp extends HHElement {
  static styles = [baseStyles, entryStyles, messagesStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { max-width: 1440px; margin: 0 auto; }
    main.entry { padding: var(--hh-s-24); }
  `];

  readonly #demoAccount: AccountSurface;
  readonly #sessions = new SessionStore();
  readonly #auth: AuthController;

  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  readonly #staffClient = new ApiClient({
    baseUrl: "",
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => DEMO_ORG }),
  });

  // The desk (src/desk.ts): its state, its reads, its presses. The fields it reads are looked up in
  // this element's own shadow root. Built once a session is adopted, dropped on sign out.
  #desk: MessagesDesk | null = null;

  #newDesk(): MessagesDesk | null {
    const session = this.#sessions.current;
    if (session === null) return null;
    return new MessagesDesk(
      new MessagesApi(this.#staffClient),
      {
        repaint: () => this.requestUpdate(),
        find: (selector) => this.renderRoot.querySelector(selector),
        now: () => Date.now(),
        openStream: (url) => new EventSource(url),
        timers: { set: (fn, ms) => window.setTimeout(fn, ms), clear: (handle) => window.clearTimeout(handle as number) },
      },
      session.user.userId,
    );
  }

  #startDesk(): void {
    this.#desk?.dispose();
    this.#desk = this.#newDesk();
    void this.#desk?.load();
  }

  #theme: "light" | "dark" = "light";
  #roleLabel = "";
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  readonly #feedbackSender: FeedbackSender = async (request) => {
    const answer = await staffFeedback.feedbackCreateMultipart(this.#staffClient, {
      type: request.type,
      message: request.message,
      screen: request.screen,
      url: request.url,
      user_agent: request.userAgent,
      images: request.images.map((image) => image.blob),
    });
    if (answer.ok) return { ok: true, issueNumber: answer.value.issue_number ?? "" };
    return { ok: false, error: answer.error };
  };

  constructor() {
    super();
    this.#demoAccount = new DemoAccount({ client: this.#demoClient });
    this.#auth = new AuthController({ account: this.#demoAccount, sessions: this.#sessions });
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
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
    if (this.#desk?.press(control) === true) return;
    const flow = control.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") void this.#chooseFlow(flow);
  };

  // The composer's text changed: the desk follows every keystroke. An input event is composed, and
  // listening on the shadow root keeps its real target.
  readonly #onHostInput = (event: Event): void => {
    const target = event.target;
    if (target instanceof HTMLTextAreaElement) this.#desk?.typed();
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    this.renderRoot.addEventListener("input", this.#onHostInput);
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
        this.#startDesk();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onHostInput);
    this.#desk?.dispose();
    super.disconnectedCallback();
  }

  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) widget.send = this.#feedbackSender;
    this.#desk?.seed();
  }

  private async loadFlows(): Promise<void> {
    this.#entryView = { flows: { kind: "loading" }, busy: false, message: null };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).listFlows();
    this.#entryView = result.ok
      ? { flows: { kind: "ready", flows: result.value }, busy: false, message: null }
      : { flows: { kind: "unavailable", status: null }, busy: false, message: null };
    this.requestUpdate();
  }

  #entryFlows(): FlowsState {
    return this.#entryView.flows;
  }

  async #chooseFlow(flow: string): Promise<void> {
    this.#entryView = { ...this.#entryView, busy: true };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).chooseFlow(flow);
    if (result.ok) {
      const adopted = this.#auth.adopt(result.value);
      if (adopted) {
        this.#roleLabel = result.value.identity.roleName;
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
        this.#startDesk();
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Demo sign-in refused: the mint's session was not usable." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Demo sign-in refused: ${result.error}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#desk?.dispose();
    this.#desk = null;
    this.#sessions.clear();
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button slot="theme" type="button" data-action="feedback">Feedback</button><button slot="theme" type="button" data-action="theme">${label}</button>`;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) {
      return html`
        <hos-app-header signed-in demo compact product-name="House Messages">${this.renderThemeControl()}</hos-app-header>
        <main class="desk-host">${this.#desk?.render()}</main>
        <hh-feedback screen="House Messages" trigger="external"></hh-feedback>
      `;
    }
    return html`
      <hos-app-header demo compact product-name="House Messages">${this.renderThemeControl()}</hos-app-header>
      <main class="entry">
        <h1>House Messages</h1>
        <p>Read what the house is saying and acknowledge what needs you. Demo data only.</p>
        ${renderEntry({ ...this.#entryView, flows: this.#entryFlows() })}
      </main>
    `;
  }
}

defineElement("house-messages-app", HouseMessagesApp);
