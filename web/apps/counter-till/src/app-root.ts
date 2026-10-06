// The Counter Till's root element: the thin UI layer the house invariants ask
// for. It renders server and package state and calls client actions. The
// figures the core states (line prices, the ring's and the refund's committed
// totals, the account's facts) arrive through the ported surface's host seams
// (src/ported/ports.ts) or the desk's own client; the surface's one pre-ring
// assembly is the priced lines' subtotal, and the tax and the total are named
// as the ring's own answer, never computed in the browser (the contract gives
// the till no rate).
// What lives in this element is the counter's own state machine: which
// surface is mounted, what is in flight, and what a refusal says.
//
// The surface after sign-in is the ported terminal (hos-pos-terminal), the
// POS surface carried over from the older product and wired to HH's contract:
// the linear flow and the detached multi-card layout are its own two modes.
// The returns desk stays beside it: the refund approval with the manager's
// own credential is HH's ruled surface, unchanged. The entry page (the role
// cards) stays the way in, and the Demo banner, the role's name and sign out
// stay in this chrome.
//
// Rendering is Lit, the design system supplies the tokens and the primitives,
// and the auth package supplies custody through its injected AccountSurface
// seam: this app never touches the token itself, the demo adapter owns the
// mint.
//
// The organisation header is the demo org by construction: the whole app is
// the demo's till, and the staff header provider refuses to send a request
// without it, so no call ever leaves this app unresolved.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { productTileUrl } from "@hardscapehouse/design-system/src/assets/product-tiles/index.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { clearFrontDoorHandoff, readFrontDoorHandoff } from "./demo-account.js";
import {
  TillClient,
  HOUSE_ZONE,
  houseDayInZone,
  type BranchRef,
} from "./till-client.js";
import { demoBanner, demoBannerStyles } from "./demo-banner.js";
import { THEME_LABEL, ThemeController, themeControlLabel, themeStatusText } from "./theme.js";
import { entryStyles, renderEntry, type FlowsState } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { ReturnsFlow } from "./returns/flow.js";
import { OneTimeCredential } from "./returns/approval.js";
import { RefundApprovalCapture } from "./returns/capture.js";
import { returnsStyles, renderReturns } from "./views/returns.js";
// The ported terminal: its import registers the surface's elements, and the
// host wiring (ports.ts) answers its seams.
import "./ported/pos-terminal.js";
import { clearSessionState, setBranches, wirePorts, type AuthUser } from "@hardscapehouse/pos-kit/src/ported/ports.js";

const DEMO_ORG = "hh-demo";

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    demoBannerStyles,
    entryStyles,
    returnsStyles,
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
      /* The terminal paints its own full-bleed chrome; the app's padding would
         letterbox it, so the mounted surface sits edge to edge. */
      .surface {
        margin: 0 calc(-1 * var(--hh-s-20));
      }
      /* The mounted surface's shell bar: HH's content (the demo banner, the
         role, the pop-out, sign out) in the reference's own top-bar place,
         above the till, so the carried demo marker is on screen while a sale
         is rung. */
      .surfacebar {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--hh-s-16);
        flex-wrap: wrap;
        padding: var(--hh-s-12) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
        margin: 0 calc(-1 * var(--hh-s-20)) var(--hh-s-16);
      }
      /* UI-3 (visual NOTE 3): the bar is on the till's own compact scale: a bar of about 48px
         (the reference shell bar's), the till's small-chrome button face and type size, and a
         one-line banner that stays visible and legible. The design system's 44px control floor
         is for the entry page; this bar's controls take the till's chrome role. */
      main.on-till {
        padding-top: 0;
      }
      .surfacebar {
        min-height: 48px;
        padding: var(--hh-s-8) var(--hh-s-20);
        margin-bottom: var(--hh-s-8);
        gap: var(--hh-s-12);
      }
      /* The entry page's header takes the same compact scale (visual NOTE 2): one row of about 48px,
         the banner beside the title and the chrome buttons, the same small button face. */
      header {
        display: flex;
        align-items: center;
        justify-content: space-between;
        flex-wrap: wrap;
        gap: var(--hh-s-12);
        min-height: 48px;
        box-sizing: border-box;
        padding: var(--hh-s-8) var(--hh-s-20);
      }
      header .title-row {
        gap: var(--hh-s-12);
        margin-left: auto;
      }
      header .signed-in {
        margin: 0;
        gap: var(--hh-s-8);
      }
      header .role-name {
        font-size: 13px;
      }
      header button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        color: var(--hh-text-dim);
      }
      header button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      header .demo-banner {
        margin: 0;
        padding: var(--hh-s-4) var(--hh-s-12);
        align-items: center;
        gap: var(--hh-s-8);
      }
      header .demo-banner .word,
      header .demo-banner .copy {
        font-size: 12px;
        line-height: 1.3;
      }
      header h1 {
        font-size: 15px;
      }
      .surfacebar .signed-in {
        margin: 0;
        gap: var(--hh-s-8);
      }
      .surfacebar .role-name {
        font-size: 13px;
      }
      .surfacebar button {
        min-height: 0;
        padding: 7px 12px;
        font-size: 11px;
        line-height: normal;
        color: var(--hh-text-dim);
      }
      .surfacebar button:hover:not(:disabled) {
        border-color: var(--hh-accent-text);
        color: var(--hh-accent-text);
      }
      .surfacebar .demo-banner {
        margin: 0;
        padding: var(--hh-s-4) var(--hh-s-12);
        align-items: center;
        gap: var(--hh-s-8);
      }
      .surfacebar .demo-banner .word,
      .surfacebar .demo-banner .copy {
        font-size: 12px;
        line-height: 1.3;
      }
      .chrome-row {
        margin: var(--hh-s-16) 0 0;
      }
      .visually-hidden {
        position: absolute;
        width: 1px;
        height: 1px;
        margin: -1px;
        padding: 0;
        overflow: hidden;
        clip: rect(0 0 0 0);
        white-space: nowrap;
        border: 0;
      }
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  readonly #approverCredential: OneTimeCredential;
  readonly #tillClient: TillClient;
  /** The theme choice: Auto follows the system, Light and Dark pin it; tab memory only (see theme.ts). */
  readonly #theme = new ThemeController(() => this.requestUpdate());
  /** What a screen reader hears after a Theme press: focus stays on the button, so the new name is not re-read. */
  #themeStatus = "";
  #flowsState: FlowsState = { kind: "loading" };
  /** The one flows read this element makes; the pop-out's tap waits for it. */
  #flowsLoaded: Promise<void> = Promise.resolve();
  #busy = false;
  #message: string | null = null;
  /** The role's label from the flow table, held for the chrome and the desk's clerk words. */
  #till: { label: string } | null = null;
  /** The flow card this session was minted from: the pop-out's address names it. */
  #activeFlow: string | null = null;
  /** The user the till's own mint was for: custody is checked against it. */
  #clerkUserId: string | null = null;
  /** The role code the clerk's mint stated, for the receipt's display words. */
  #clerkRoleCode: string | null = null;
  /**
   * The mint's own IdentityUser block, in the ported surface's session shape:
   * the facts the terminal reads (the role's name, the tier and scope behind
   * the platform-admin gate's shape), handed over unchanged from the mint.
   */
  #clerkIdentity: AuthUser | null = null;
  readonly #returns: ReturnsFlow;
  #branch: BranchRef | null = null;
  /**
   * The mounted surface. The entry page (the cards) is the way in; once a
   * mint is adopted and the branch the login reaches is resolved, the ported
   * terminal IS the till, in both its modes (the linear flow and the detached
   * multi-card layout, the terminal's own choice to switch).
   */
  #surface: "entry" | "terminal" = "entry";
  /** A refusal in words for the chrome while the terminal is mounted (the pop-out's, say). */
  #chromeMessage: string | null = null;

  constructor() {
    super();
    // The till's transport, wrapped so the refund approval refusal's body
    // (the amount and the approver roles beside the envelope) reaches the
    // returns flow: the generated client carries the error string alone.
    const capture = new RefundApprovalCapture();
    const staffHeaders = authHeaderProvider({
      callerClass: "staff",
      sessions: this.#sessions,
      activeOrg: () => DEMO_ORG,
    });
    const client = new ApiClient({ baseUrl: "", fetch: capture.fetch, headers: staffHeaders });
    // The demo adapter's own client carries no staff bearer: the demo routes
    // are pre-auth by design (the tap-a-role sign-in), and the served gate
    // holds the pages and the demo mint behind basic authentication, where a
    // request already carrying an Authorization header of the wrong kind is
    // refused. The adapter's asks go out bare of a credential, the browser
    // answers the gate's challenge the way it answered the page, and the org
    // header still rides every call the app makes.
    const demoClient = new ApiClient({
      baseUrl: "",
      fetch: capture.fetch,
      headers: async () => ({ "X-Org": DEMO_ORG }),
    });
    this.#account = new DemoAccount({ client: demoClient });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    // The approver's client exists for the one return call that may carry the
    // approver's credential (D49): its provider takes the token out of the
    // one time slot during that send, and taking is dropping, so the token
    // rests nowhere before and survives nothing after. The clerk's client is
    // a different instance and never carries the credential, so no other call
    // this app makes can leak it. Nothing here touches storage or logs.
    const approverCredential = new OneTimeCredential();
    this.#approverCredential = approverCredential;
    const approverClient = new ApiClient({
      baseUrl: "",
      headers: async () => {
        const headers = await staffHeaders();
        const token = approverCredential.take();
        if (token !== null) {
          headers["X-Approver-Authorization"] = `Bearer ${token}`;
        }
        return headers;
      },
    });
    this.#returns = new ReturnsFlow({
      client,
      approverClient,
      account: this.#account,
      credential: approverCredential,
      clerk: () => {
        const session = this.#sessions.current;
        if (session === null) return null;
        return {
          name: session.user.name,
          roleCode: this.#clerkRoleCode,
          roleLabel: this.#till?.label ?? null,
        };
      },
      capture,
      onChange: () => this.requestUpdate(),
    });
    this.#tillClient = new TillClient(client);
    // The ported surface's host wiring: one door, wired once, reading custody
    // through this element's own closures so the bearer never rests in a
    // second place. The org header is the demo org by construction (the same
    // provider the staff client carries), and the requested-date default is
    // the house zone's day, the conversion the sale's ring already rides.
    wirePorts({
      headers: staffHeaders,
      houseDay: () => houseDayInZone(HOUSE_ZONE),
      user: () => this.#clerkIdentity,
      token: () => this.#sessions.current?.token ?? "",
      productTile: productTileUrl,
    });
  }

  connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule, carried for this app's own templates: listen on
    // the HOST and read the control off composedPath(), never @click in a
    // template and never event.target.closest, because the shadow boundary
    // retargets the target to this element (the design system's README holds
    // both halves). Click is composed, so the host hears it.
    this.addEventListener("click", this.#onHostClick);
    // The returns desk's fields ride the same host delegation: one input
    // listener on the host, the control read off composedPath() by its
    // data-field name, so a re-render strands no listener. The inputs are
    // uncontrolled: the DOM holds what was typed, the flow hears every edit
    // here, and a draft's fresh mount shows the flow's own defaults.
    this.addEventListener("input", this.#onHostInput);
    // The system's light setting is honoured from the first paint; the Theme
    // control in the account area cycles Auto, Light and Dark.
    this.#theme.start();
    // The flows read happens once; the pop-out, when this window is one,
    // chains on the same answer rather than asking the route twice.
    this.#flowsLoaded = this.#loadFlows();
    // The pop-out: a second window opened at this app with ?popout=<flow>
    // signs itself in through the same public cards route, the same tap a
    // person would make, and lands directly on the terminal. The flow name is
    // the only thing that rides the address: never a token, never a
    // credential (this app's custody is memory per tab by design, and its
    // committed sink gate refuses every storage and channel path a handshake
    // would dial). The flow table answers first (its label is the role's name
    // in the chrome), then the flow runs. A window that is not a pop-out,
    // on a demo build, takes the front door's handoff instead: the mint the
    // front door held for the box lands signed in, no second picker. The
    // seam rule (decision log row 893) keeps that read a demo seam: Vite
    // substitutes MODE at build time, so this branch exists only in a build
    // that asked for the demo mode, and a default build carries no adopt
    // path at all (test/no-default-adopt.test.mjs reads the built files).
    const popped = new URLSearchParams(window.location.search).get("popout");
    if (typeof popped === "string" && popped !== "") {
      void this.#flowsLoaded.then(() => this.#chooseFlow(popped));
    } else if (import.meta.env.MODE === "demo") {
      void this.#adoptFromFrontDoor();
    }
  }

  /**
   * The front door's handoff (HH-DEMO-1 D2 polish, item 6): the front door
   * mints the demo session and holds it for the box under its own session
   * storage key; when this app boots from a module tile's navigation,
   * the record is read, handed to AuthController.adopt (custody takes the
   * token into private memory and rewrites the display mirror itself), and
   * the key is dropped in the same step, so a later visit to this app never
   * re-adopts a spent handoff. The clerk facts the chrome and the receipt
   * read ride the same record (the front door validated the mint already;
   * this read validates the record again before custody sees any of it). An
   * absent or unusable record is the ordinary direct visit; the entry page
   * with the cards comes up, the till's ordinary way in. A pop-out window
   * skips this: its own ?popout flow signs in by the cards route above. The
   * storage touch rides the auth package's throw-safe mirror, this app's
   * sink gate's one sanctioned accessor.
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
    this.#clerkUserId = handed.user.userId;
    this.#clerkRoleCode = handed.roleCode;
    this.#clerkIdentity = {
      user_id: handed.user.userId,
      email: handed.user.email,
      display_name: handed.user.name,
      role_code: handed.roleCode,
      role_name: handed.roleName,
      tier: handed.tier,
      scope: handed.scope,
    };
    this.#till = { label: handed.roleLabel };
    this.#activeFlow = handed.flow;
    this.requestUpdate();
    await this.#initTill();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("input", this.#onHostInput);
    this.#theme.stop();
    super.disconnectedCallback();
  }

  readonly #onHostInput = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (!(control instanceof HTMLInputElement) && !(control instanceof HTMLTextAreaElement)) return;
    const field = control.getAttribute("data-field");
    if (field === "order-no") this.#returns.setOrderNo(control.value);
    if (field === "qty" && control instanceof HTMLInputElement) {
      const line = control.getAttribute("data-line") ?? "";
      this.#returns.setQty(line, control.value);
      // The flow keeps whole units; the field is brought to the same answer,
      // so a typed 1.5 never sits on the screen while a 1 rides the draft.
      const stored = this.#returns.qtyOf(line);
      const canonical = stored === 0 ? "" : String(stored);
      if (control.value !== canonical) control.value = canonical;
    }
    if (field === "note") this.#returns.setNote(control.value);
    if (field === "restock" && control instanceof HTMLInputElement) {
      this.#returns.setRestock(control.checked);
    }
    if (field === "reason-code") this.#returns.setReasonCode(control.value);
  };

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const flow = control.getAttribute("data-flow");
    if (control.getAttribute("data-action") === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
    if (control.getAttribute("data-action") === "theme") {
      this.#theme.cycle();
      this.#themeStatus = themeStatusText(this.#theme.choice);
    }
    if (control.getAttribute("data-action") === "sign-out") {
      void this.#signOut();
    }
    if (control.getAttribute("data-action") === "pop-out" && typeof flow === "string" && flow !== "") {
      this.#popOut(flow);
    }
    if (control.getAttribute("data-action") === "returns-find") {
      void this.#returns.findSale();
    }
    if (control.getAttribute("data-action") === "returns-method") {
      const method = control.getAttribute("data-method");
      if (method !== null) this.#returns.chooseMethod(method);
    }
    if (control.getAttribute("data-action") === "returns-submit") {
      void this.#returns.submit();
    }
    if (control.getAttribute("data-action") === "returns-another") {
      this.#returns.startAnotherReturn();
    }
    if (control.getAttribute("data-action") === "returns-approve") {
      void this.#returns.openApprovalDialog();
    }
    if (control.getAttribute("data-action") === "returns-approve-cancel") {
      this.#returns.closeApprovalDialog();
    }
    if (control.getAttribute("data-action") === "returns-approver-flow") {
      const approverFlow = control.getAttribute("data-flow");
      if (approverFlow !== null && approverFlow !== "") {
        void this.#returns.approveWithFlow(approverFlow);
      }
    }
  };

  /**
   * Open the till on a second window: the same app, the same flow card's
   * sign-in, the same drawer (the till session is the branch's, server-side).
   * A blocked pop-up is a real counter event; it lands in words, never a
   * silent no-click.
   */
  #popOut(flow: string): void {
    const popped = window.open(`/?popout=${encodeURIComponent(flow)}`, "ct-popout");
    if (popped === null) {
      this.#chromeMessage =
        "The browser blocked the second window: allow pop-ups for this till and press Pop out again.";
      this.requestUpdate();
    }
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
    this.#clerkUserId = minted.value.user.userId;
    this.#clerkRoleCode = minted.value.roleCode;
    this.#clerkIdentity = {
      user_id: minted.value.user.userId,
      email: minted.value.user.email,
      display_name: minted.value.user.name,
      role_code: minted.value.roleCode,
      role_name: minted.value.identity.roleName,
      tier: minted.value.identity.tier,
      scope: minted.value.identity.scope,
    };
    this.#till = { label: this.#labelFor(flow) };
    this.#activeFlow = flow;
    this.requestUpdate();
    await this.#initTill();
  }

  /**
   * The role label in the chrome, read from custody rather than cached: while
   * the live session is the one this till minted for, the entry card's label
   * stands; if custody were ever replaced quietly (the review round's M3b),
   * the chrome names who the session actually carries, so the till can never
   * show one person's role over another person's session.
   */
  #signedInRoleLabel(): string {
    const session = this.#sessions.current;
    if (session === null) return "";
    if (this.#clerkUserId !== null && session.user.userId !== this.#clerkUserId) {
      return session.user.name;
    }
    return this.#till?.label ?? session.user.name;
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
   * in this tab's memory. The returns desk is blanked with the session: the
   * till is the next person's, and no draft, refusal or receipt of the last
   * one stays on it.
   */
  async #signOut(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.requestUpdate();
    await this.#controller.signOut();
    this.#busy = false;
    this.#till = null;
    this.#activeFlow = null;
    this.#message = null;
    this.#chromeMessage = null;
    this.#clerkUserId = null;
    this.#clerkRoleCode = null;
    this.#clerkIdentity = null;
    this.#returns.reset();
    this.#branch = null;
    this.#surface = "entry";
    // The ported surface's session-owned module state goes with the session:
    // the next person's boxes and branch facts are earned by their own login.
    clearSessionState();
    this.requestUpdate();
  }

  /**
   * The till's own start: resolve the branch the login reaches, fill the
   * branch facts the terminal reads, then mount the terminal. The drawer is
   * the terminal's own business from here (it finds or opens the session at
   * its own pace), so a drawer refusal surfaces through the terminal's chrome,
   * not through a wall before it. A branch that does not resolve is the one
   * wall that keeps the terminal down: without the branch nothing on the
   * surface can fence itself, so the refusal says so and the cards come back.
   */
  async #initTill(): Promise<void> {
    const branch = await this.#tillClient.resolveBranch();
    if (!branch.ok) {
      this.#message = `The till did not start: ${branch.error}`;
      this.#till = null;
      this.#activeFlow = null;
      this.#clerkUserId = null;
      this.#clerkRoleCode = null;
      this.#clerkIdentity = null;
      await this.#controller.signOut();
      this.requestUpdate();
      return;
    }
    this.#branch = branch.value;
    // The branch facts the ported surface reads (the branch default, the yard
    // names beside stock): filled in place from the route's own answer, before
    // anything mounts.
    setBranches([{ id: branch.value.id, name: branch.value.name }]);
    this.#surface = "terminal";
    this.requestUpdate();
  }

  /** The Theme control of the account area: one button, its value beside the word. */
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
    // The banner's rule is "only once the flows route has answered with
    // cards": an error answer and an empty answer alike are not the demo
    // showing itself, so neither gets to look like one.
    const demoPresent =
      this.#surface === "terminal" ||
      (this.#flowsState.kind === "ready" && this.#flowsState.flows.length > 0);
    return html`
      <p class="visually-hidden" role="status" aria-live="polite" data-testid="theme-status">${this.#themeStatus}</p>
      ${this.#surface === "terminal"
        ? nothing
        : html`
            <header>
              ${demoPresent ? demoBanner() : nothing}
              <div class="title-row">
                <h1>Counter Till</h1>
                ${this.#till === null
                  ? html`<p class="signed-in">${this.#themeControl()}</p>`
                  : html`
                      <p class="signed-in">
                        <span class="role-name" data-testid="signed-in-role">${this.#signedInRoleLabel()}</span>
                        ${this.#themeControl()}
                        <button type="button" class="btn-secondary" data-action="sign-out" ?disabled=${this.#busy}>
                          Sign out
                        </button>
                      </p>
                    `}
              </div>
            </header>
          `}
      <main class="${this.#surface === "terminal" ? "on-till" : ""}">
        ${this.#surface === "terminal"
          ? html`
              <h2 class="visually-hidden">Till</h2>
              <div class="surfacebar">
                ${demoPresent ? demoBanner() : nothing}
                <p class="signed-in">
                  <span class="role-name" data-testid="signed-in-role">${this.#signedInRoleLabel()}</span>
                  ${this.#themeControl()}
                  <button
                    type="button"
                    class="btn-secondary"
                    data-action="pop-out"
                    data-flow=${this.#activeFlow ?? nothing}
                    title="Open the till on a second window"
                  >
                    Pop out
                  </button>
                  <button type="button" class="btn-secondary" data-action="sign-out" ?disabled=${this.#busy}>
                    Sign out
                  </button>
                </p>
              </div>
              <div class="surface">
                <hos-pos-terminal></hos-pos-terminal>
              </div>
              ${this.#chromeMessage === null ? nothing : html`<p class="notice" role="status">${this.#chromeMessage}</p>`}
              ${renderReturns(this.#returns)}
            `
          : renderEntry({ flows: this.#flowsState, busy: this.#busy, message: this.#message })}
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
