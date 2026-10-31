// The dispatch board's root element: the chrome and the way in. The way in is the demo's role cards
// (the shared kit's entry, the demo adapter behind the auth package's AccountSurface seam), offered
// for the two roles whose screens live here. After the mint the branch resolves once, the ports
// are wired with the caller's headers, and the desk renders.
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
import { framedByShell, listenForHostSignOut } from "@hardscapehouse/design-system/src/framed.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { demoNote } from "./band-identity.js";
import { entryStyles, renderEntry, type EntryView, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { DispatchApi } from "./dispatch-api.js";
import { DispatchDesk } from "./desk.js";
import { todayISO } from "./dispatch-logic.js";
import { boardStyles } from "./styles.js";

const DEMO_ORG = "hh-demo";

// The flows this app offers a card for: the roles whose screens live here (the front door's own
// live-role rule). A tappable card that led into a refused surface would be a login to nothing.
const ENTRY_FLOWS = new Set(["logistics-manager", "yard-manager"]);

export class DispatchBoardApp extends HHElement {
  static styles = [baseStyles, entryStyles, boardStyles, css`
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

  // The desk (src/desk.ts): its state, its loads, its presses. The fields it reads are looked up in
  // this element's own shadow root.
  #desk = this.#newDesk();

  #newDesk(): DispatchDesk {
    return new DispatchDesk(new DispatchApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      find: (selector) => this.renderRoot.querySelector(selector),
      findAll: (selector) => [...this.renderRoot.querySelectorAll(selector)],
      today: () => todayISO(),
    });
  }

  // The theme: light by default, brand (the dark half) one tap away. Held in memory only: nothing
  // here persists.
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

  // A phone: the header band carries only the brand, the role and Switch role and Sign out; the
  // theme and feedback buttons move into the page so the band stays one compact band.
  #narrow = false;
  readonly #phone = window.matchMedia("(max-width: 520px)");
  readonly #onPhone = (): void => {
    this.#narrow = this.#phone.matches;
    this.requestUpdate();
  };

  // The entry cards carry data-action="choose" and data-flow; one host listener reads them, as the
  // till's does.
  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    // A select acts on change, not on the click that opens it.
    if (control instanceof HTMLSelectElement) return;
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
    if (this.#desk.press(control)) return;
    const flow = control.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // Enter in a field the desk owns (the will call order box).
  readonly #onHostKey = (event: KeyboardEvent): void => {
    const target = event.composedPath()[0];
    if (target instanceof Element) this.#desk.key(target, event.key);
  };

  // A select or the date field changed: the desk acts on it.
  readonly #onHostChange = (event: Event): void => {
    const target = event.target;
    if (target instanceof Element) this.#desk.change(target);
  };

  /** Framed by the Work shell: the host asks this app to run its own sign out when the person switches role (item 8e); standalone, nothing listens. */
  #stopHostSignOut: (() => void) | null = null;

  #dealerName = "";

  connectedCallback(): void {
    super.connectedCallback();
    // The dealer leads the header once the served config names one (row 1858); until then no lockup is drawn.
    void readDealerName().then((name) => {
      this.#dealerName = name;
      this.requestUpdate();
    });
    this.#stopHostSignOut = listenForHostSignOut(() => this.#onSignOut());
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("keydown", this.#onHostKey);
    this.#phone.addEventListener("change", this.#onPhone);
    this.#narrow = this.#phone.matches;
    // A change event is not composed, so it stops at the shadow root and never reaches the host.
    this.renderRoot.addEventListener("change", this.#onHostChange);
    if (this.#custody.mode === "console") {
      this.#custody.begin("dispatch-board", (outcome) => this.#onRecovered(outcome));
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
        clearFrontDoorHandoff();
        void this.#desk.load();
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
      void this.#desk.load();
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
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("keydown", this.#onHostKey);
    this.#phone.removeEventListener("change", this.#onPhone);
    this.renderRoot.removeEventListener("change", this.#onHostChange);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a template, so it is set
  // on the element here, after each render. The desk's seeding (the date field, the selects that
  // name a current value) rides the same pass.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string; dealerName: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.dealerName = this.#dealerName;
      header.theme = this.#theme;
    }
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) widget.send = this.#feedbackSender;
    this.#desk.seed();
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

  /** The flows state as the entry view renders it: the table filtered to the roles whose screens live here. */
  #entryFlows(): FlowsState {
    const flows = this.#entryView.flows;
    if (flows.kind !== "ready") return flows;
    return { kind: "ready", flows: flows.flows.filter((f) => f.flow !== undefined && ENTRY_FLOWS.has(f.flow)) };
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
        void this.#desk.load();
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
    this.#desk = this.#newDesk();
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  // The theme and feedback controls ride the header's named slot; the theme word names the theme a
  // tap moves to.
  private renderThemeControl(): TemplateResult {
    // Framed by the Work shell the band is hidden and the shell carries both controls, pressing these two in the app,
    // so they stay in the header's light DOM at every width and the in-page pair below is not shown.
    if (this.#narrow && !framedByShell()) return html``;
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button slot="theme" type="button" class="theme-btn" data-action="feedback">Feedback</button><button slot="theme" type="button" class="theme-btn" data-action="theme">${label}</button>`;
  }

  #mobileTools(): TemplateResult {
    if (framedByShell()) return html``;
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<div class="mob-tools"><button type="button" class="theme-btn" data-action="feedback">Feedback</button><button type="button" class="theme-btn" data-action="theme">${label}</button></div>`;
  }

  // The app header's demo banner belongs to a demo page only (UX-127): a dealer's own box, served a
  // console config, renders the header without the demo attribute. Two whole templates rather than a
  // bound boolean, because the template position gate allows no boolean binding but ?disabled.
  #header(signedIn: boolean): TemplateResult {
    const tools = this.renderThemeControl();
    const demo = servedConsoleConfig() === null;
    if (signedIn) {
      return demo
        ? html`<hos-app-header signed-in demo compact product-name="Dispatch">${tools}</hos-app-header>`
        : html`<hos-app-header signed-in compact product-name="Dispatch">${tools}</hos-app-header>`;
    }
    return demo
      ? html`<hos-app-header demo compact product-name="Dispatch">${tools}</hos-app-header>`
      : html`<hos-app-header compact product-name="Dispatch">${tools}</hos-app-header>`;
  }

  private renderSignedIn(): TemplateResult {
    return html`
      ${this.#header(true)}
      <main class="desk">
        ${this.#narrow ? this.#mobileTools() : html``}
        ${this.#desk.render()}
      </main>
      <hh-feedback screen="Dispatch and Load Building" trigger="external"></hh-feedback>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      ${this.#header(false)}
      <main class="desk">
        ${this.#narrow ? this.#mobileTools() : html``}
        <h1>Dispatch and Load Building</h1>
        <p class="dim">Build the day's loads, crew the trucks and send them out.${demoNote(servedConsoleConfig())}</p>
        ${renderEntry({ ...this.#entryView, flows: this.#entryFlows() }, { headline: "Who is dispatching?", intro: "Tap a role to open the dispatch board.", coming: false })}
      </main>
    `;
  }
}

defineElement("dispatch-board-app", DispatchBoardApp);
