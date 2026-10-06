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
import { SIGN_OUT_EVENT, UNSENT_WAIT_MS, waitUpTo, type SignOutDetail } from "./quote-context.js";
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// The shared chrome (item 5, decision log row 938): the element registers
// itself; the app feeds it through the wiring pass below, the same
// no-template-binding discipline the builder's client rides.
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import {
  HHFeedback,
  type FeedbackSender,
} from "@hardscapehouse/design-system/src/feedback.js";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { entryStyles, renderEntry, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { clearFrontDoorHandoff, readFrontDoorHandoff } from "./demo-account.js";
import { HOUSE_ZONE, houseDayInZone } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { setBranches, wirePorts, clearSessionState, type AuthUser } from "@hardscapehouse/pos-kit/src/ported/ports.js";
import { QdClient, type BranchRef } from "./qd-client.js";
import { THEME_LABEL, ThemeController, themeControlLabel, themeStatusText } from "./theme.js";
// The builder shell registers the work area and the panel seams.
import "./quote-builder.js";

const DEMO_ORG = "hh-demo";

export class QuoteDeskApp extends HHElement {
  static styles = [
    baseStyles,
    entryStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }
      /* The chrome above the work area is the shared header element
         (hos-app-header). The theme control is the app's own, passed into
         the element's named theme slot: it renders inside the right cluster,
         after Switch role and Sign out, and nothing sits between the bar and
         the work area. The control's face is the sheet's secondary button at
         the bar's own compact size. */
      .theme-slot {
        display: inline-flex;
        align-items: center;
      }
      .theme-slot button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        white-space: nowrap;
      }
      .theme-slot button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      main {
        padding: var(--hh-s-4) 0;
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
  /** Whether this build is the demo build: the seam rule's own constant (the front door handoff rides it), and the shared header's demo badge rides it too. */
  readonly #demoBuild = import.meta.env.MODE === "demo";

  constructor() {
    super();
    const staffHeaders = authHeaderProvider({
      callerClass: "staff",
      sessions: this.#sessions,
      activeOrg: () => DEMO_ORG,
    });
    const client = new ApiClient({ baseUrl: "", headers: staffHeaders });
    // The feedback widget's sender rides the staff client: the feedback
    // route is staff guarded and the desk's own custody holds the bearer.
    this.#feedbackSender = async (request) => {
      const answer = await staffFeedback.feedbackCreateMultipart(client, {
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
    // The demo adapter's own client carries no staff bearer: the demo routes
    // are pre-auth by design (the tap a role sign-in).
    const demoClient = new ApiClient({
      baseUrl: "",
      headers: async () => ({ "X-Org": DEMO_ORG }),
    });
    this.#account = new DemoAccount({ client: demoClient });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    // The caller's own id is read from the live session, not from the sign-in answer kept beside it.
    this.#client = new QdClient(client, () => this.#sessions.current?.user.userId ?? "");
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

  /** The feedback widget's sender; built once beside the staff client. */
  #feedbackSender: FeedbackSender = async () => ({ ok: false, error: "not ready" });

  override connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule: listen on the HOST and read the control off
    // composedPath(), never an event binding in a template.
    this.addEventListener("click", this.#onHostClick);
    // The system's light setting is honoured from the first paint.
    this.#theme.start();
    void this.#loadFlows();
    // The seam rule (decision log rows 893 and 899): the front door's
    // handoff is adopted only where the demo is compiled in. Vite
    // substitutes the mode constant at build time, so a default build drops
    // this branch whole and the reader behind it resolves to the empty stub
    // (vite.config.ts), keeping the key name and the adopt call out of the
    // served product; the demo image and the Playwright harness build with
    // the demo mode.
    if (import.meta.env.MODE === "demo") {
      void this.#adoptFromFrontDoor();
    }
  }

  protected override updated(): void {
    // The wiring pass: the builder receives the validated client the same
    // way every shared component receives its facts.
    const builder = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("qd-quote-builder");
    if (builder !== null) builder.client = this.#client;
    // The shared chrome is fed the same way, not through template bindings
    // (the design system's position gate holds this app's templates too):
    // the role's label, the signed-in flag, the demo badge's build flag, and
    // the theme the switcher last applied to the document, which the element
    // re-applies under the sheet's own attribute contract.
    const header = this.renderRoot.querySelector<HTMLElement & Record<string, unknown>>("hos-app-header");
    if (header !== null) {
      header.roleLabel = this.#signedInRoleLabel();
      header.signedIn = this.#roleLabel !== null;
      header.demo = this.#demoBuild;
      header.theme = document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark";
    }
    // The feedback widget's sender rides the property the same way, never a
    // template binding: the widget's own seam.
    const feedback = this.renderRoot.querySelector("hh-feedback");
    if (feedback instanceof HHFeedback && feedback.send !== this.#feedbackSender) {
      feedback.send = this.#feedbackSender;
    }
  }

  /** The desk carries no floating pill: the bar's Feedback button opens the shared panel by the widget's show(). */
  #showFeedback(): void {
    const feedback = this.renderRoot.querySelector("hh-feedback");
    if (feedback instanceof HHFeedback) feedback.show();
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
    if (control.getAttribute("data-action") === "sign-out") {
      void this.#signOut();
    }
    if (control.getAttribute("data-action") === "switch-role") {
      void this.#switchRole();
    }
    if (control.getAttribute("data-action") === "feedback") {
      this.#showFeedback();
    }
    if (control.getAttribute("data-action") === "theme") {
      this.#theme.cycle();
      this.#themeStatus = themeStatusText(this.#theme.choice);
      this.requestUpdate();
    }
  };

  async #loadFlows(): Promise<void> {
    const result = await this.#account.listFlows();
    this.#flowsState = result.ok
      ? { kind: "ready", flows: result.value }
      : { kind: "unavailable", status: result.status ?? null };
    this.requestUpdate();
  }

  /**
   * The front door's handoff (HH-DEMO-1, the Quote Desk's adopt, decision
   * log rows 893, 899 and 906): the front door mints the demo session and
   * holds it for the box under its own session storage key; when this app
   * boots from a module tile's navigation, the record is read, handed to
   * AuthController.adopt (custody takes the token into private memory and
   * rewrites the display mirror itself), and the key is dropped in the same
   * step, so a later visit to this app never re-adopts a spent handoff. The
   * desk facts the chrome and the ported surfaces read ride the same record
   * (the front door validated the mint already; this read validates the
   * record again before custody sees any of it), exactly the fields
   * #chooseFlow's mint leaves. An absent or unusable record is the ordinary
   * direct visit; the entry page with the cards comes up, the desk's
   * ordinary way in. The storage touch rides the auth package's throw-safe
   * mirror, this app's sink gate's one sanctioned accessor. The shape is the
   * till's (counter-till app-root on demo/front-door 385cd8d6).
   */
  async #adoptFromFrontDoor(): Promise<void> {
    if (this.#sessions.current !== null) return;
    const read = readFrontDoorHandoff();
    if (read.kind === "absent") return;
    clearFrontDoorHandoff();
    if (read.kind === "unusable") return;
    const handed = read.session;
    const adopted = this.#controller.adopt({
      token: handed.token,
      tokenExpiresAtMs: handed.tokenExpiresAtMs,
      user: handed.user,
    });
    if (!adopted) return;
    this.#deskUserId = handed.user.userId;
    this.#deskIdentity = {
      user_id: handed.user.userId,
      email: handed.user.email,
      display_name: handed.user.name,
      role_code: handed.roleCode,
      role_name: handed.roleName,
      tier: handed.tier,
      scope: handed.scope,
    };
    this.#roleLabel = handed.roleLabel;
    this.requestUpdate();
    await this.#initDesk();
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
    // Unsent work (a terms edit waiting on its save, the layout) is sent while the session still stands.
    const detail: SignOutDetail = { waits: [] };
    window.dispatchEvent(new CustomEvent(SIGN_OUT_EVENT, { detail }));
    // Bounded: a save that never answers must not hold the session open for good.
    const sent = await waitUpTo(UNSENT_WAIT_MS, Promise.allSettled(detail.waits));
    await this.#controller.signOut();
    this.#busy = false;
    this.#roleLabel = null;
    this.#deskUserId = null;
    this.#deskIdentity = null;
    this.#branch = null;
    this.#message = sent ? null : "Signing out did not wait for every unsent change: saving timed out, so a change you made last may not have been saved.";
    this.#surface = "entry";
    // The shared surface's session-owned module state goes with the session.
    clearSessionState();
    this.requestUpdate();
  }

  /**
   * Switch role: the front door owns the picker, so the walk goes there
   * (decision log row 938: Switch role returns to the front door at /). The
   * session ends the sign-out way first, unsent work included, so no custody
   * mirror survives the walk and the front door comes up at its own picker,
   * not at a session it did not mint.
   */
  async #switchRole(): Promise<void> {
    if (this.#busy) return;
    await this.#signOut();
    window.location.assign("/");
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

  /** The Feedback control of the bar: opens the shared widget's panel. */
  #feedbackControl(): TemplateResult {
    return html`
      <button type="button" class="btn-secondary" data-action="feedback" data-testid="feedback-control" title="Send feedback about this screen">
        Feedback
      </button>
    `;
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
    return html`
      <p class="visually-hidden" role="status" aria-live="polite" data-testid="theme-status">${this.#themeStatus}</p>
      <hos-app-header data-testid="qd-bar">
        <div class="theme-slot" slot="theme">${this.#feedbackControl()}</div>
        <div class="theme-slot" slot="theme">${this.#themeControl()}</div>
      </hos-app-header>
      <main>
        ${this.#surface === "desk"
          ? html`
              <div class="surface">
                <qd-quote-builder></qd-quote-builder>
              </div>
            `
          : renderEntry({ flows: this.#flowsState, busy: this.#busy, message: this.#message })}
      </main>
      <!-- The feedback widget (Feature Polish): the shared panel, opened by
           the bar's Feedback button (trigger external: no floating pill). -->
      <hh-feedback screen="Quote Desk" trigger="external"></hh-feedback>
    `;
  }
}

defineElement("qd-app-root", QuoteDeskApp);
