// HH Drive's root element: the chrome and the way in. The way in is the
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
import { html, nothing, type TemplateResult } from "lit";
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
import { HOUSE_ZONE, houseDayInZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { DriveApi, type DriverRun, type DriverStop } from "./drive-api.js";
import { roadsideError } from "./drive-logic.js";
import { driveStyles } from "./styles.js";
import { renderRunSheet, renderStopDetail } from "./views.js";

const DEMO_ORG = "hh-demo";

export class DriveRunApp extends HHElement {
  static styles = [baseStyles, entryStyles, driveStyles];

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

  // The driver's calls, over the staff client.
  readonly #drive = new DriveApi(this.#staffClient);

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

  // The run sheet's state. The runs are the server's answer for the dealer's
  // day; the view is which screen shows (the run sheet or one stop).
  #phase: "loading" | "ready" | "error" = "loading";
  #runs: DriverRun[] = [];
  #error = "";
  #notice = "";
  #busy = false;
  #view: { kind: "run" } | { kind: "stop"; id: string } = { kind: "run" };

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

  // The theme: light by default (a driver reads this in daylight), brand
  // (the dark half) one tap away. Held in memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

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
    if (action === "open-stop") {
      this.#openStop(control?.getAttribute("data-stop-id") ?? "");
      return;
    }
    if (action === "back") {
      this.#view = { kind: "run" };
      this.#error = "";
      this.#notice = "";
      this.requestUpdate();
      return;
    }
    if (action === "reload") {
      void this.#loadRuns();
      return;
    }
    if (action === "advance") {
      const to = control?.getAttribute("data-status");
      if (to === "LOADED" || to === "EN_ROUTE") void this.#advance(to);
      return;
    }
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
        void this.#loadRuns();
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
    // A telephone address cannot ride a template binding (the position gate
    // admits only http, https and relative addresses), so the call links take
    // their href here, from a value that is digits and phone punctuation only.
    for (const link of Array.from(this.renderRoot.querySelectorAll("a[data-tel]"))) {
      const number = link.getAttribute("data-tel") ?? "";
      if (/^[0-9+().\-\sx]+$/.test(number)) link.setAttribute("href", `tel:${number.replace(/\s+/g, "")}`);
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

  // The dealer's own day names the run to ask for, never the browser's UTC day.
  async #loadRuns(): Promise<void> {
    this.#phase = "loading";
    this.requestUpdate();
    const result = await this.#drive.myRuns(houseDayInZone(HOUSE_ZONE));
    if (result.ok) {
      this.#runs = result.value;
      this.#phase = "ready";
      this.#error = "";
    } else {
      this.#runs = [];
      this.#phase = "error";
      this.#error = roadsideError(result.error, result.status, "Your run did not load. Check your signal and try again.", {
        notFound: "You are not set up as a driver. Ask your branch manager to link your login.",
      });
    }
    this.requestUpdate();
  }

  #openStop(id: string): void {
    if (id === "") return;
    this.#view = { kind: "stop", id };
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
  }

  #findStop(id: string): { run: DriverRun; stop: DriverStop } | null {
    for (const run of this.#runs) {
      const stop = run.stops.find((candidate) => candidate.id === id);
      if (stop !== undefined) return { run, stop };
    }
    return null;
  }

  // A plain move. The server decides what is legal; a refusal is shown in its
  // own words and the stop stays where it was.
  async #advance(to: "LOADED" | "EN_ROUTE"): Promise<void> {
    if (this.#view.kind !== "stop" || this.#busy) return;
    const stopId = this.#view.id;
    this.#busy = true;
    this.#error = "";
    this.#notice = "";
    this.requestUpdate();
    const result = await this.#drive.advance(stopId, to);
    if (result.ok) {
      await this.#refreshRuns();
    } else {
      this.#error = roadsideError(result.error, result.status, "That did not go through. Check your signal and try again.");
      await this.#refreshRuns();
    }
    this.#busy = false;
    this.requestUpdate();
  }

  // Re-read the day without blanking the screen: the server's order and
  // statuses win, and a failed re-read keeps what is on the glass.
  async #refreshRuns(): Promise<void> {
    const result = await this.#drive.myRuns(houseDayInZone(HOUSE_ZONE));
    if (result.ok) this.#runs = result.value;
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
        void this.#loadRuns();
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
    this.#phase = "loading";
    this.#runs = [];
    this.#error = "";
    this.#notice = "";
    this.#view = { kind: "run" };
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Brand theme" : "Light theme";
    return html`<button slot="theme" type="button" class="btn btn-secondary" data-action="theme">${label}</button>`;
  }

  private renderWork(): TemplateResult {
    if (this.#view.kind === "stop") {
      const found = this.#findStop(this.#view.id);
      if (found !== null) {
        return renderStopDetail({ ...found, busy: this.#busy, error: this.#error, notice: this.#notice });
      }
    }
    return html`
      ${renderRunSheet({ phase: this.#phase, runs: this.#runs, error: this.#error, notice: this.#notice })}
      ${this.#phase === "error" ? html`<button type="button" class="btn btn-secondary big" data-action="reload" data-tap>Try again</button>` : nothing}
    `;
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo>${this.renderThemeControl()}</hos-app-header>
      <main>${this.renderWork()}</main>
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

defineElement("drive-run-app", DriveRunApp);
