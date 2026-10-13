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
import { ArApi } from "./ar/ar-api.js";
import { ArDesk } from "./ar/ar-desk.js";
import { arStyles } from "./ar/ar-styles.js";
import { GlApi } from "./gl/gl-api.js";
import { GlDesk } from "./gl/gl-desk.js";
import { glStyles } from "./gl/gl-styles.js";
import { BankrecApi } from "./bankrec/bankrec-api.js";
import { BankrecDesk } from "./bankrec/bankrec-desk.js";
import { bankrecStyles } from "./bankrec/bankrec-styles.js";
import { TaxApi } from "./tax/tax-api.js";
import { TaxDesk } from "./tax/tax-desk.js";
import { taxStyles } from "./tax/tax-styles.js";

const DEMO_ORG = "hh-demo";

// The role whose landing is the ledger slice (src/gl): the approvals queue, the journals, the chart, the trial
// balance, the periods, the checks and the policy. The code is the role code the front door's handoff and the
// demo mint both carry. Every other role lands on the receivables desk (src/ar); the controller reaches it from
// the area switch. Payables and Receivables reaches the ledger from the same switch, as a reader.
const CONTROLLER_ROLE = "financial_controller";
const READER_ROLE = "payables_receivables";

// The desk's areas, in switch order. A new area is one more entry here, one case in #onHostClick for its
// presses and one in renderArea for its screens.
const AREAS = ["ledger", "receivables", "bank", "tax"] as const;
type Area = (typeof AREAS)[number];

// The demo flows whose modules include the Money Desk. The core lists its whole closed table (the till's pair
// too); the app's own way in offers only the roles that can use it, as the till's entry offers only its pair.
const MONEY_DESK_FLOWS: readonly string[] = ["payables-receivables", "financial-controller"];

export class MoneyDeskApp extends HHElement {
  static styles = [baseStyles, entryStyles, arStyles, glStyles, bankrecStyles, taxStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { padding: var(--hh-s-4) 0; }
    main.gl-desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
    .areas { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); max-width: 1440px; margin: 0 auto; padding: var(--hh-s-16) var(--hh-s-24) 0; }
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

  // The receivables slice (src/ar): its state, its loads, its presses. The
  // fields it reads are looked up in this element's own shadow root.
  // Who is signed in, as the front door's handoff or the demo mint named them.
  #roleCode = "";
  #userName = "";

  #ar = this.#newDesk();

  // The ledger slice (src/gl): its state, its loads, its presses. The field it reads is looked up in
  // this element's own shadow root. The controller holds both desks and switches between them.
  #gl = this.#newGlDesk();
  #area: Area = "ledger";
  // A reader's ledger is read the first time its area opens, not at sign in.
  #glLoaded = false;

  // The tax slice (src/tax): the controller's tax administration area, loaded when it is first chosen.
  #tax = this.#newTaxDesk();

  #newTaxDesk(): TaxDesk {
    const named = (name: string): HTMLInputElement | HTMLSelectElement | null => this.renderRoot.querySelector(`[data-field="${name}"]`);
    return new TaxDesk(new TaxApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      setField: (name, value) => {
        const el = named(name);
        if (el === null) return;
        if (el instanceof HTMLSelectElement) {
          const at = [...el.options].findIndex((o) => o.getAttribute("data-id") === value);
          if (at >= 0 && el.selectedIndex !== at) el.selectedIndex = at;
        } else if (el.type === "checkbox") {
          const want = value === "true";
          if (el.checked !== want) el.checked = want;
        } else if (el.value !== value) el.value = value;
      },
      field: (name) => named(name)?.value ?? "",
      reveal: (selector) => this.renderRoot.querySelector(selector)?.scrollIntoView({ block: "center", behavior: "smooth" }),
      focus: (selector) => this.renderRoot.querySelector<HTMLElement>(selector)?.focus({ preventScroll: true }),
    });
  }

  // The bank reconciliation slice (src/bankrec): the controller's third area, loaded when it is first chosen.
  #bank = this.#newBankDesk();

  #newBankDesk(): BankrecDesk {
    return new BankrecDesk(new BankrecApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      setField: (name, value) => {
        const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[data-field="${name}"]`);
        if (el === null) return;
        if (el instanceof HTMLSelectElement) {
          const at = [...el.options].findIndex((o) => o.getAttribute("data-id") === value);
          if (at >= 0 && el.selectedIndex !== at) el.selectedIndex = at;
        } else if (el instanceof HTMLInputElement && el.type === "file") {
          if (value === "") el.value = "";
        } else if (el.value !== value) el.value = value;
      },
      reveal: (selector) => this.renderRoot.querySelector(selector)?.scrollIntoView({ block: "start", behavior: "smooth" }),
    });
  }

  #newGlDesk(): GlDesk {
    const scoped = (name: string, key?: string): string => (key === undefined ? `[data-field="${name}"]` : `[data-field="${name}"][data-key="${key}"]`);
    return new GlDesk(
      new GlApi(this.#staffClient),
      {
        repaint: () => this.requestUpdate(),
        field: (name, key) => this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(scoped(name, key))?.value ?? "",
        setField: (name, value, key) => {
          const found = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(scoped(name, key));
          if (found !== null && found.value !== value) found.value = value;
        },
        download: (text, filename) => {
          const url = URL.createObjectURL(new Blob([text], { type: "text/csv" }));
          const link = document.createElement("a");
          link.href = url;
          link.download = filename;
          link.click();
          URL.revokeObjectURL(url);
        },
        userId: () => this.#sessions.current?.user.userId ?? "",
        reveal: (selector) => {
          const el = this.renderRoot.querySelector<HTMLElement>(selector);
          if (el === null) return;
          el.scrollIntoView({ block: "start", behavior: "smooth" });
          // A confirm step or a refusal takes the reading position, so the keyboard and a screen reader land on it.
          if (el.hasAttribute("tabindex") || el.hasAttribute("role")) el.focus({ preventScroll: true });
        },
      },
      this.#isController() ? "controller" : "reader",
    );
  }

  #isController(): boolean {
    return this.#roleCode === CONTROLLER_ROLE;
  }

  // The roles that hold the ledger area: the controller in full, Payables and Receivables as a reader.
  #holdsLedger(): boolean {
    return this.#roleCode === CONTROLLER_ROLE || this.#roleCode === READER_ROLE;
  }

  #newDesk(): ArDesk {
    return new ArDesk(
      new ArApi(this.#staffClient),
      {
        repaint: () => this.requestUpdate(),
        reveal: (selector) => {
          // A payment's outcome words sit in the page's banner, far above the Record payment button
          // on a phone: bring the banner into view and put the screen reader's reading position on it.
          const el = this.renderRoot.querySelector<HTMLElement>(selector);
          if (el === null) return;
          el.scrollIntoView({ block: "center", behavior: "smooth" });
          el.focus({ preventScroll: true });
        },
        field: (name, key) => {
          const scope = key === undefined ? "" : `[data-key="${key}"]`;
          const found = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[data-field="${name}"]${scope}`);
          return found?.value ?? "";
        },
        setField: (name, value, key) => {
          const scope = key === undefined ? "" : `[data-key="${key}"]`;
          const found = this.renderRoot.querySelector<HTMLInputElement>(`[data-field="${name}"]${scope}`);
          if (found !== null) {
            if (found.value !== value) found.value = value;
            return;
          }
          const plain = this.renderRoot.querySelector<HTMLElement>(`[data-ro="${name}"]`);
          if (plain !== null && plain.textContent !== value) plain.textContent = value;
        },
        download: (text, filename) => {
          const url = URL.createObjectURL(new Blob([text], { type: "text/csv" }));
          const link = document.createElement("a");
          link.href = url;
          link.download = filename;
          link.click();
          URL.revokeObjectURL(url);
        },
      },
      { roleCode: this.#roleCode, userName: this.#userName, userId: this.#sessions.current?.user.userId ?? "" },
    );
  }

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

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
    if (action === "area") {
      const area = control?.getAttribute("data-area");
      if (AREAS.some((id) => id === area) && (area !== "ledger" || this.#holdsLedger()) && (area !== "bank" || this.#isController()) && (area !== "tax" || this.#isController())) {
        this.#area = area as Area;
        this.#openLedger();
        if (area === "bank") this.#bank.ensureLoaded();
        if (area === "tax") this.#tax.ensureLoaded();
        this.requestUpdate();
      }
      return;
    }
    if (control !== undefined && this.#holdsLedger() && this.#area === "ledger" && this.#gl.press(control)) return;
    if (control !== undefined && this.#isController() && this.#area === "bank" && this.#bank.press(control)) return;
    if (control !== undefined && this.#isController() && this.#area === "tax" && this.#tax.press(control)) return;
    if (control !== undefined && this.#ar.press(control)) return;
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  readonly #onInput = (event: Event): void => {
    const target = event.target;
    if (target instanceof Element && target.hasAttribute("data-field")) {
      this.#ar.input(target);
      this.#bank.input(target);
      this.#tax.input(target);
    }
  };

  readonly #onChange = (event: Event): void => {
    const target = event.target;
    if (target instanceof Element && target.hasAttribute("data-field")) {
      this.#ar.change(target);
      this.#bank.change(target);
      this.#tax.change(target);
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    // input does not leave this element's own shadow root, so the listener sits on the root itself.
    this.renderRoot.addEventListener("input", this.#onInput);
    this.renderRoot.addEventListener("change", this.#onChange);
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
        this.#roleCode = handoff.session.roleCode;
        this.#userName = handoff.session.user.name;
        clearFrontDoorHandoff();
        this.#ar = this.#newDesk();
        void this.#ar.load();
        this.#startGl();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onInput);
    this.renderRoot.removeEventListener("change", this.#onChange);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    this.#ar.afterRender();
    this.#gl.afterRender();
    if (this.#area === "bank") this.#bank.afterRender();
    if (this.#area === "tax") this.#tax.afterRender();
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
      this.#entryView = { flows: { kind: "ready", flows: result.value.filter((f) => f.flow !== undefined && MONEY_DESK_FLOWS.includes(f.flow)) }, busy: false, message: null };
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
        // The session's own role code decides the landing and the walls, never the display name:
        // a renamed role must not move the controller off the ledger or widen the holds.
        this.#roleCode = result.value.roleCode;
        this.#userName = this.#sessions.current?.user.name ?? "";
        this.#ar = this.#newDesk();
        void this.#ar.load();
        this.#startGl();
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Demo sign-in refused: the mint's session was not usable." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Demo sign-in refused: ${result.error}` };
    }
    this.requestUpdate();
  }

  // The controller lands on the ledger and its desk loads once it is signed in; Payables and Receivables
  // lands on receivables and its reader desk loads the first time the ledger area opens.
  #startGl(): void {
    this.#gl = this.#newGlDesk();
    this.#bank = this.#newBankDesk();
    this.#tax = this.#newTaxDesk();
    this.#glLoaded = false;
    this.#area = this.#isController() ? "ledger" : "receivables";
    this.#openLedger();
  }

  #openLedger(): void {
    if (this.#area !== "ledger" || this.#glLoaded || !this.#holdsLedger()) return;
    this.#glLoaded = true;
    void this.#gl.load();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#ar = this.#newDesk();
    this.#gl = this.#newGlDesk();
    this.#bank = this.#newBankDesk();
    this.#tax = this.#newTaxDesk();
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button slot="theme" type="button" class="btn btn-secondary" data-action="theme">${label}</button>`;
  }

  // The desks the role holds, as the area switch: the controller's ledger and receivables, and the reader's.
  private renderAreas(): TemplateResult {
    const label = (id: Area): string => (id === "ledger" ? (this.#isController() ? "Approvals and ledger" : "Ledger") : id === "bank" ? "Bank reconciliation" : id === "tax" ? "Tax" : "Receivables");
    const shown = AREAS.filter((id) => (id !== "bank" && id !== "tax") || this.#isController());
    const area = (id: Area): TemplateResult => html`
      <button type="button" class="gl-tab" data-action="area" data-area=${id} aria-current="${this.#area === id ? "true" : "false"}">${label(id)}</button>
    `;
    return html`<nav class="areas" aria-label="Money Desk areas">${shown.map((id) => area(id))}</nav>`;
  }

  private renderArea(): TemplateResult {
    if (this.#area === "bank") return html`<main class="br-desk">${this.#bank.render()}</main>`;
    if (this.#area === "tax") return html`<main class="tx-desk">${this.#tax.render()}</main>`;
    return this.#area === "ledger" ? html`<main class="gl-desk">${this.#gl.render()}</main>` : html`<main class="desk">${this.#ar.render()}</main>`;
  }

  private renderSignedIn(): TemplateResult {
    if (this.#holdsLedger()) {
      return html`
        ${this.#isController()
          ? html`<hos-app-header signed-in demo product-name="Money Desk" product-sub="Approvals and ledger · HH Work shell">${this.renderThemeControl()}</hos-app-header>`
          : html`<hos-app-header signed-in demo product-name="Money Desk" product-sub="Receivables and ledger · HH Work shell">${this.renderThemeControl()}</hos-app-header>`}
        ${this.renderAreas()}
        ${this.renderArea()}
      `;
    }
    return html`
      <hos-app-header signed-in demo product-name="Money Desk">${this.renderThemeControl()}</hos-app-header>
      <main class="desk">
        ${this.#ar.render()}
      </main>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo product-name="Money Desk">${this.renderThemeControl()}</hos-app-header>
      <main class="desk">
        <h1>Money Desk</h1>
        <p class="dim">Receivables for the whole company: the aging, payments, promises and the bounced queue. Demo data only.</p>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("money-desk-app", MoneyDeskApp);
