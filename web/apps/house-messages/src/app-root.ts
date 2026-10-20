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
import { fieldTabsForRole, setFieldFrame, type ShellSyncState } from "@hardscapehouse/shell-chrome";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import { HHFeedback, type FeedbackSender } from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { entryStyles, renderEntry, type EntryView, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { MessagesApi } from "./messages-api.js";
import { MessagesDesk } from "./desk.js";
import { messagesStyles } from "./styles.js";

const DEMO_ORG = "hh-demo";

// The stream URL: the same base the ticket mint rides (the custody's coreBaseUrl, the empty string
// in demo mode) plus the stream path the feed hands over. A trailing slash on the configured base
// is dropped so a split-origin console never opens the stream on a double slash, and a page with no
// configured core keeps the path relative.
export function streamUrlOnCore(coreBaseUrl: string | null | undefined, path: string): string {
  return `${(coreBaseUrl ?? "").replace(/\/+$/, "")}${path}`;
}

// Every flow the box mints holds House Messages (the decided matrix gives the cell to all thirteen
// roles), so the entry offers every card the flows route answers.
export class HouseMessagesApp extends HHElement {
  static styles = [baseStyles, entryStyles, messagesStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { max-width: 1440px; margin: 0 auto; }
    main.entry { padding: var(--hh-s-24); }
  `];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's two demo routes. It is
  // the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface = new DemoAccount({ client: this.#demoClient });

  // The custody the served config chose: in demo mode the auth controller over the demo account, as
  // always; in console mode this page recovers its own session on load (boot-custody.ts).
  readonly #custody = bootCustody(servedConsoleConfig(), { sessions: this.#sessions, demoAccount: this.#demoAccount, demoOrg: DEMO_ORG });

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController = this.#custody.controller;

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: this.#custody.coreBaseUrl,
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => this.#custody.org }),
  });

  // The desk (src/desk.ts): its state, its reads, its presses. The fields it reads are looked up in
  // this element's own shadow root. Built once a session is adopted, dropped on sign out.
  #desk: MessagesDesk | null = null;
  #callerRole: { roleCode?: string; tier?: number } | null = null;

  #newDesk(): MessagesDesk | null {
    const session = this.#sessions.current;
    if (session === null) return null;
    return new MessagesDesk(
      new MessagesApi(this.#staffClient),
      {
        repaint: () => this.requestUpdate(),
        find: (selector) => this.renderRoot.querySelector(selector),
        now: () => Date.now(),
        openStream: (url) => new EventSource(streamUrlOnCore(this.#custody.coreBaseUrl, url)),
        timers: { set: (fn, ms) => window.setTimeout(fn, ms), clear: (handle) => window.clearTimeout(handle as number) },
      },
      session.user.userId,
      this.#callerRole,
    );
  }

  #startDesk(): void {
    this.#desk?.dispose();
    this.#desk = this.#newDesk();
    void this.#desk?.load();
  }

  #theme: "light" | "dark" = "light";
  #roleLabel = "";

  // The dealer's name for the frame's lockup, from the public tenant config; empty until it lands.
  #org = "";
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
    if (target instanceof HTMLTextAreaElement || target instanceof HTMLInputElement) this.#desk?.typed(target);
  };

  connectedCallback(): void {
    super.connectedCallback();
    void readDealerName().then((name) => {
      this.#org = name;
      this.requestUpdate();
    });
    this.addEventListener("click", this.#onHostClick);
    this.renderRoot.addEventListener("input", this.#onHostInput);
    if (this.#custody.mode === "console") {
      this.#custody.begin("house-messages", (outcome) => this.#onRecovered(outcome));
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
        this.#roleLabel = handoff.session.roleName;
        this.#callerRole = { roleCode: handoff.session.roleCode, tier: handoff.session.tier };
        clearFrontDoorHandoff();
        this.#startDesk();
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
      this.#callerRole = { roleCode: outcome.entry.roleCode, tier: outcome.entry.tier };
      this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      this.#startDesk();
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
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onHostInput);
    this.#desk?.dispose();
    super.disconnectedCallback();
  }

  // The sync chip's state from the live feed: a stream or poll that answers is synced; a feed still opening is
  // pulling; a dropped one is reconnecting; a feed that cannot reach the server is offline.
  #syncState(): ShellSyncState {
    const live = this.#desk?.live ?? null;
    if (live === "reconnecting") return "unreachable";
    if (live === "offline") return "offline";
    if (live === "connecting") return "pulling";
    return "idle";
  }

  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    setFieldFrame(this.renderRoot.querySelector("hh-field-frame"), {
      org: this.#org,
      appName: "House Messages",
      surface: "Channels",
      roleLabel: this.#roleLabel,
      syncState: this.#syncState(),
      theme: this.#theme,
      signedIn: this.#sessions.current !== null,
      demo: true,
      feedback: this.#sessions.current !== null,
      current: "messages",
      tabs: fieldTabsForRole(this.#callerRole?.roleCode ?? ""),
    });
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
        this.#callerRole = { roleCode: result.value.roleCode, tier: result.value.identity?.tier };
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
    // The frame's account chip names no role once the person is out.
    this.#roleLabel = "";
    this.#desk?.dispose();
    this.#desk = null;
    this.#callerRole = null;
    this.#sessions.clear();
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) {
      return html`
        <hh-field-frame></hh-field-frame>
        <main class="desk-host">${this.#desk?.render()}</main>
        <hh-feedback screen="House Messages" trigger="external"></hh-feedback>
      `;
    }
    return html`
      <hh-field-frame></hh-field-frame>
      <main class="entry">
        <h1>House Messages</h1>
        <p>Read what the house is saying and acknowledge what needs you. Demo data only.</p>
        ${renderEntry({ ...this.#entryView, flows: this.#entryFlows() })}
      </main>
    `;
  }
}

defineElement("house-messages-app", HouseMessagesApp);
