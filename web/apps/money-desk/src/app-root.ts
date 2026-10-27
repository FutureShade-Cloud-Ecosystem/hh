// The role app's root element: the chrome and the way in. The way in is the
// demo's role cards (the shared kit's entry, the demo adapter behind the auth
// package's AccountSurface seam). After the mint the branch resolves once, the
// ports are wired with the caller's headers, and the work area renders.
//
// The organisation header is the demo org by construction: the staff header
// provider refuses to send a request without X-Org, so no call ever leaves
// this app unresolved.
//
// The one band (hh-shell-header) is wired in updated(): org, demo flag,
// account user, breadcrumb, groups, current key, sync state, variant "band",
// theme, and the account menu. The band applies the theme to the document
// root and renders the actions (theme, switch role, sign out) inside its
// account chip menu; clicks bubble back to the host click listener below.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { announceChromeControls, chromeActionFrom, framedByShell, listenForHostSignOut } from "@hardscapehouse/design-system/src/framed.js";
import { ApiClient, staffFeedback } from "@hardscapehouse/api-client";
import { applyDocumentTheme, framedBandAccount, framedBandBreadcrumb } from "@hardscapehouse/shell-chrome/src/framed-band.js";
import { readDealerName } from "@hardscapehouse/pos-kit/src/tenant-title.js";
import type { ShellHeader } from "@hardscapehouse/shell-chrome/src/shell-header.js";
import "@hardscapehouse/shell-chrome/src/shell-header.js";
import { FRAMED_BAND_APP, framedBandSurface } from "./framed-band.js";
import { browserSignal, noSignalFetch } from "@hardscapehouse/work-offline/guard";
import { HHFeedback, type FeedbackSender } from "@hardscapehouse/design-system/src/feedback.js";
// hh-tanyai-panel: the one TanyAI panel (PANEL-API r1); the host opens it and resolves the records it names.
import "@hardscapehouse/shell-chrome/src/shell-tanyai.js";
import type { ShellTanyai } from "@hardscapehouse/shell-chrome/src/shell-tanyai.js";
import { generatedTransport } from "@hardscapehouse/shell-chrome/src/tanyai-transport.js";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { entryStyles, renderEntry, type EntryView } from "@hardscapehouse/pos-kit/src/ported/entry.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { ArApi } from "./ar/ar-api.js";
import { ArDesk } from "./ar/ar-desk.js";
import { arStyles } from "./ar/ar-styles.js";
import { GlApi } from "./gl/gl-api.js";
import { GlDesk } from "./gl/gl-desk.js";
import { glStyles } from "./gl/gl-styles.js";
import { FinstmtApi } from "./finstmt/finstmt-api.js";
import { FinstmtDesk } from "./finstmt/finstmt-desk.js";
import { finstmtStyles } from "./finstmt/finstmt-styles.js";
import { BankrecApi } from "./bankrec/bankrec-api.js";
import { BankrecDesk } from "./bankrec/bankrec-desk.js";
import { bankrecStyles } from "./bankrec/bankrec-styles.js";
import { TaxApi } from "./tax/tax-api.js";
import { TaxDesk } from "./tax/tax-desk.js";
import { taxStyles } from "./tax/tax-styles.js";
import { LienApi } from "./lien/lien-api.js";
import { LienDesk, type LienHost } from "./lien/lien-desk.js";
import { lienStyles } from "./lien/lien-styles.js";
import { ComplianceApi } from "./compliance/compliance-api.js";
import { ComplianceDesk } from "./compliance/compliance-desk.js";

const DEMO_ORG = "hh-demo";

// The role whose landing is the ledger slice (src/gl): the approvals queue, the journals, the chart, the trial
// balance, the periods, the checks and the policy. The code is the role code the front door's handoff and the
// demo mint both carry. Every other role lands on the receivables desk (src/ar); the controller reaches it from
// the area switch. Payables and Receivables reaches the ledger from the same switch, as a reader.
const CONTROLLER_ROLE = "financial_controller";
const PAYREC_ROLE = "payables_receivables";
const GENERAL_MANAGER_ROLE = "general_manager";

// The desk's areas, in switch order. A new area is one more entry here, one case in #onHostClick for its presses
// and one in renderArea for its screens.
const AREAS = ["ledger", "receivables", "statements", "bank", "tax", "liens", "compliance"] as const;
type Area = (typeof AREAS)[number];

// The areas each role holds, and the one it lands on (the first listed). The controller holds all six; Payables and
// Receivables lands on receivables and holds the ledger as a reader, the liens and the calendar beside it; the General Manager, who holds the
// release gate of the liens, lands there.
const AREAS_BY_ROLE: Readonly<Record<string, readonly Area[]>> = {
  [CONTROLLER_ROLE]: ["ledger", "receivables", "statements", "bank", "tax", "liens", "compliance"],
  [PAYREC_ROLE]: ["ledger", "receivables", "liens", "compliance"],
  [GENERAL_MANAGER_ROLE]: ["liens", "compliance"],
};
const AREA_LABEL: Readonly<Record<Area, string>> = {
  ledger: "Approvals and ledger", // the reader's switch says just "Ledger"
  receivables: "Receivables",
  statements: "Statements",
  bank: "Bank reconciliation",
  tax: "Tax",
  liens: "Liens",
  compliance: "Compliance",
};

// The demo flows whose modules include the Money Desk. The core lists its whole closed table (the till's pair
// too); the app's own way in offers only the roles that can use it, as the till's entry offers only its pair.
const MONEY_DESK_FLOWS: readonly string[] = ["payables-receivables", "financial-controller"];

export class MoneyDeskApp extends HHElement {
  static styles = [baseStyles, entryStyles, arStyles, glStyles, finstmtStyles, bankrecStyles, taxStyles, lienStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); overflow-x: hidden; }
    main { padding: var(--hh-s-4) 0; }
    main.gl-desk { max-width: 1440px; margin: 0 auto; padding: var(--hh-s-24) var(--hh-s-24) var(--hh-s-48); }
    .areas { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); max-width: 1440px; margin: 0 auto; padding: var(--hh-s-16) var(--hh-s-24) 0; }
    @media (max-width: 520px) {
      .areas { padding-inline: var(--hh-s-12); }
      /* The desk's gutter matches the areas above it on a phone, the way the receivables desk's does. */
      main.gl-desk { padding: var(--hh-s-16) var(--hh-s-12) var(--hh-s-40); }
    }
  `];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // two demo routes. It is the injected AccountSurface for the auth package.
  readonly #demoAccount: AccountSurface = new DemoAccount({ client: this.#demoClient });

  // The custody the served config chose: in demo mode the auth controller over the demo account, as
  // always; in console mode this page recovers its own session on load (boot-custody.ts).
  readonly #custody = bootCustody(servedConsoleConfig(), { sessions: this.#sessions, demoAccount: this.#demoAccount, demoOrg: DEMO_ORG });

  // The auth controller: owns sign-in, sign-out, and adoption.
  readonly #auth: AuthController = this.#custody.controller;

  // The staff client: carries the mint's bearer after adoption.
  readonly #staffClient = new ApiClient({
    baseUrl: this.#custody.coreBaseUrl,
    // The Work shell's rule: money never queues. With no signal a write is refused before it dials, with one honest state.
    fetch: noSignalFetch(undefined, browserSignal()),
    headers: authHeaderProvider({ callerClass: "staff", sessions: this.#sessions, activeOrg: () => this.#custody.org }),
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
  // The ledger is read the first time its area opens, not at sign in.
  #glLoaded = false;

  // The statements slice (src/finstmt): the controller's area for the books as statements, loaded when it is first chosen.
  #stmt = this.#newStmtDesk();

  #newStmtDesk(): FinstmtDesk {
    return new FinstmtDesk(new FinstmtApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      setField: (name, value) => {
        const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"]`);
        if (el === null) return;
        if (el instanceof HTMLSelectElement) {
          const at = [...el.options].findIndex((o) => o.getAttribute("data-id") === value);
          if (at >= 0 && el.selectedIndex !== at) el.selectedIndex = at;
        } else if (el.value !== value) el.value = value;
      },
      reveal: (selector) => this.renderRoot.querySelector(selector)?.scrollIntoView({ block: "start", behavior: "smooth" }),
      download: (text, filename) => {
        const url = URL.createObjectURL(new Blob([text], { type: "text/csv" }));
        const link = document.createElement("a");
        link.href = url;
        link.download = filename;
        link.click();
        URL.revokeObjectURL(url);
      },
      print: () => window.print(),
    });
  }

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

  // The bank reconciliation slice (src/bankrec): the controller's area, loaded when it is first chosen.
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

  // The liens slice (src/lien) and the compliance slice (src/compliance): the fields they read are looked up in
  // this element's own shadow root, a select by the id of its chosen option.
  #lien = this.#newLienDesk();
  #compliance = this.#newComplianceDesk();

  #deskHost(): LienHost {
    const found = (name: string, key?: string) =>
      this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(key === undefined ? `[data-field="${name}"]` : `[data-field="${name}"][data-key="${key}"]`);
    return {
      repaint: () => this.requestUpdate(),
      field: (name, key) => found(name, key)?.value ?? "",
      choice: (name) => {
        const el = found(name);
        return el instanceof HTMLSelectElement ? (el.selectedOptions[0]?.getAttribute("data-id") ?? "") : "";
      },
      setField: (name, value, key) => {
        const el = found(name, key);
        if (el === null) return;
        if (el instanceof HTMLSelectElement) {
          const at = [...el.options].findIndex((o) => o.getAttribute("data-id") === value);
          if (at >= 0 && el.selectedIndex !== at) el.selectedIndex = at;
        } else if (el.value !== value) el.value = value;
      },
      reveal: (selector) => {
        const el = this.renderRoot.querySelector<HTMLElement>(selector);
        if (el === null) return;
        el.scrollIntoView({ block: "start", behavior: "smooth" });
        // A confirm step takes the reading position, so the keyboard and a screen reader land on it.
        if (el.hasAttribute("tabindex") || el.hasAttribute("role")) el.focus({ preventScroll: true });
      },
      userId: () => this.#sessions.current?.user.userId ?? "",
      roleCode: () => this.#roleCode,
    };
  }

  #newLienDesk(): LienDesk {
    return new LienDesk(new LienApi(this.#staffClient), this.#deskHost());
  }

  #newComplianceDesk(): ComplianceDesk {
    return new ComplianceDesk(new ComplianceApi(this.#staffClient), this.#deskHost());
  }

  /** The areas the signed in role holds; none before sign in, and none for a role that holds only the receivables desk. */
  #areas(): readonly Area[] {
    return AREAS_BY_ROLE[this.#roleCode] ?? [];
  }

  /** Every role but the General Manager reads the receivables desk; the General Manager's money work is the liens. */
  #holdsReceivables(): boolean {
    return this.#roleCode !== GENERAL_MANAGER_ROLE;
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
    return this.#roleCode === CONTROLLER_ROLE || this.#roleCode === PAYREC_ROLE;
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
          const found = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"]${scope}`);
          if (found instanceof HTMLSelectElement && [...found.options].some((o) => o.hasAttribute("data-id"))) {
            // A select whose options carry ids takes the option that names the value.
            const at = [...found.options].findIndex((o) => o.getAttribute("data-id") === value);
            if (at >= 0 && found.selectedIndex !== at) found.selectedIndex = at;
            return;
          }
          if (found !== null) {
            if (found.value !== value) found.value = value;
            return;
          }
          const plain = this.renderRoot.querySelector<HTMLElement>(`[data-ro="${name}"]`);
          if (plain !== null && plain.textContent !== value) plain.textContent = value;
        },
        mountDocument: (selector, text) => {
          // The invoice document is the core's own HTML: it is shown in a frame with every permission withheld, never
          // set into this page, and the slot holds nothing else.
          const slot = this.renderRoot.querySelector<HTMLElement>(selector);
          if (slot === null) return;
          const frame = document.createElement("iframe");
          frame.setAttribute("sandbox", "");
          frame.title = "Invoice document";
          frame.srcdoc = text;
          slot.replaceChildren(frame);
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
    this.#feedback.send = this.#feedbackSender;
  }

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // Framed by the Work shell: the desk renders no band of its own.
  readonly #framed = framedByShell();

  // The entry cards carry data-action="choose" and data-flow; one host
  // listener reads them, as the till's does.

  /** The Theme control's press: the band's account menu presses it standalone, the Work shell's menu asks for it framed. */
  #pressTheme(): void {
    this.#theme = this.#theme === "light" ? "dark" : "light";
    this.requestUpdate();
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "switch-role" || action === "sign-out") {
      this.#onSignOut();
      return;
    }
    if (action === "assist") {
      this.#tanyai()?.show();
      return;
    }
    if (action === "theme") {
      this.#pressTheme();
      return;
    }
    if (action === "area") {
      const area = control?.getAttribute("data-area");
      if (AREAS.some((id) => id === area) && this.#areas().includes(area as Area)) {
        this.#area = area as Area;
        this.#openArea();
        this.requestUpdate();
      }
      return;
    }
    if (control !== undefined && this.#holdsLedger() && this.#area === "ledger" && this.#gl.press(control)) return;
    if (control !== undefined && this.#isController() && this.#area === "statements" && this.#stmt.press(control)) return;
    if (control !== undefined && this.#isController() && this.#area === "bank" && this.#bank.press(control)) return;
    if (control !== undefined && this.#isController() && this.#area === "tax" && this.#tax.press(control)) return;
    if (control !== undefined && this.#area === "liens" && this.#lien.press(control)) return;
    if (control !== undefined && this.#area === "compliance" && this.#compliance.press(control)) return;
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
      this.#stmt.input(target);
      this.#bank.input(target);
      this.#tax.input(target);
    }
  };

  readonly #onChange = (event: Event): void => {
    const target = event.target;
    if (target instanceof Element && target.hasAttribute("data-field")) {
      this.#ar.change(target);
      this.#stmt.change(target);
      this.#bank.change(target);
      this.#tax.change(target);
      if (this.#area === "liens") this.#lien.change(target);
      if (this.#area === "compliance") this.#compliance.change(target);
    }
  };

  /** Framed by the Work shell: the host asks this app to run its own sign out when the person switches role (item 8e); standalone, nothing listens. */
  #stopHostSignOut: (() => void) | null = null;

  /** Framed by the Work shell: its account menu carries Theme for this app and asks through one message. */
  readonly #onChromeAction = (event: MessageEvent): void => {
    const action = chromeActionFrom(event, window);
    if (action === "theme") this.#pressTheme();
  };

  /** The dealer's name for the band, from the tenant config (one cached read a session); empty until it lands. */
  #dealerName = "";

  connectedCallback(): void {
    super.connectedCallback();
    void readDealerName().then((name) => {
      if (name !== this.#dealerName) {
        this.#dealerName = name;
        this.requestUpdate();
      }
    });
    if (this.#framed) {
      window.addEventListener("message", this.#onChromeAction);
      announceChromeControls(window, ["theme"]);
    }
    this.#stopHostSignOut = listenForHostSignOut(() => this.#onSignOut());
    this.addEventListener("click", this.#onHostClick);
    // input does not leave this element's own shadow root, so the listener sits on the root itself.
    this.renderRoot.addEventListener("input", this.#onInput);
    this.renderRoot.addEventListener("change", this.#onChange);
    if (this.#custody.mode === "console") {
      this.#custody.begin("money-desk", (outcome) => this.#onRecovered(outcome));
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
        this.#roleCode = handoff.session.roleCode;
        this.#userName = handoff.session.user.name;
        clearFrontDoorHandoff();
        this.#ar = this.#newDesk();
        if (this.#holdsReceivables()) void this.#ar.load();
        this.#startGl();
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
      this.#roleCode = outcome.entry.roleCode;
      this.#userName = outcome.entry.name;
      this.#ar = this.#newDesk();
      void this.#ar.load();
      this.#startGl();
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
    window.removeEventListener("message", this.#onChromeAction);
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
    if (this.#area === "statements") this.#stmt.afterRender();
    if (this.#area === "bank") this.#bank.afterRender();
    if (this.#area === "tax") this.#tax.afterRender();
    if (this.#area === "liens") this.#lien.afterRender();
    if (this.#area === "compliance") this.#compliance.afterRender();
    this.#wireTanyai();
    // The framed band (GAPS-r2 row G1): the board's white band, the ONE band
    // when the desk stands alone; framed by the Work shell, the host's band is
    // the one band. The bell stands quiet: wiring its unread count is a
    // follow-up. The account chip's menu carries what the app-local header
    // used to (theme, sign out): the actions ride the band's accountMenu
    // template and their clicks bubble back to #onHostClick.
    // Framed, no band applies the theme: the app does.
    if (this.#framed) applyDocumentTheme(this.#theme);
    const band = this.renderRoot.querySelector<ShellHeader>("hh-shell-header");
    const signedIn = this.#sessions.current !== null;
    if (band !== null) {
      band.org = this.#dealerName;
      band.demo = true;
      // This desk mounts the TanyAI panel and answers the press, so its band carries the Ask TanyAI button.
      band.assist = true;
      band.user = framedBandAccount(this.#userName, this.#roleLabel);
      band.breadcrumb = framedBandBreadcrumb(FRAMED_BAND_APP, framedBandSurface(this.#area));
      band.groups = [];
      band.currentKey = "";
      band.syncState = "idle";
      band.variant = "band";
      band.theme = this.#theme;
      band.accountMenu = html`
        ${signedIn
          ? html`<p>Signed in as <strong data-testid="header-word">${this.#roleLabel}</strong></p>`
          : nothing}
        ${this.renderThemeControl()}
        ${signedIn
          ? html`
              <button type="button" class="btn-secondary" data-action="switch-role">Switch role</button>
              <button type="button" class="btn-secondary" data-action="sign-out">Sign out</button>
            `
          : nothing}
      `;
    }
  }

  #tanyai(): ShellTanyai | null {
    return this.renderRoot.querySelector<ShellTanyai>("hh-tanyai-panel");
  }

  // The panel gets its transport (the generated client, the staff routes) and the one record kind this desk can
  // open, a customer's account. It is set once; the panel keeps its own state across renders.
  #wireTanyai(): void {
    const panel = this.#tanyai();
    if (panel === null || panel.transport !== null) return;
    panel.transport = generatedTransport(this.#staffClient);
    panel.openableKinds = this.#holdsReceivables() ? ["customer"] : [];
    panel.addEventListener("hh-tanyai-open-ref", this.#onOpenRef as EventListener);
  }

  // A record the panel names opens on this desk's own route: a customer is the receivables account screen.
  readonly #onOpenRef = (event: CustomEvent<{ ref: { kind: string; id: string } }>): void => {
    const ref = event.detail.ref;
    if (ref.kind !== "customer" || !this.#holdsReceivables()) return;
    this.#tanyai()?.hide();
    if (this.#area !== "receivables") {
      this.#area = "receivables";
      this.#openArea();
    }
    const open = document.createElement("button");
    open.setAttribute("data-action", "open-account");
    open.setAttribute("data-customer-id", ref.id);
    open.setAttribute("data-right", "receive");
    this.#ar.press(open);
    this.requestUpdate();
  };

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
        if (this.#holdsReceivables()) void this.#ar.load();
        this.#startGl();
        this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Sign-in did not go through. Try the role again." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Sign-in did not go through. ${result.error}` };
    }
    this.requestUpdate();
  }

  // The controller lands on the ledger, Payables and Receivables on receivables and the General Manager on the liens.
  // The ledger is read the first time its area opens; the liens and compliance desks likewise.
  #startGl(): void {
    this.#gl = this.#newGlDesk();
    this.#stmt = this.#newStmtDesk();
    this.#bank = this.#newBankDesk();
    this.#tax = this.#newTaxDesk();
    this.#lien = this.#newLienDesk();
    this.#compliance = this.#newComplianceDesk();
    this.#glLoaded = false;
    const held = this.#areas();
    this.#area = this.#isController() ? "ledger" : held.includes("receivables") ? "receivables" : (held[0] ?? "receivables");
    this.#openArea();
  }

  #openArea(): void {
    if (this.#area === "ledger" && !this.#glLoaded && this.#holdsLedger()) {
      this.#glLoaded = true;
      void this.#gl.load();
    }
    if (this.#area === "statements") this.#stmt.ensureLoaded();
    if (this.#area === "bank") this.#bank.ensureLoaded();
    if (this.#area === "tax") this.#tax.ensureLoaded();
    if (this.#area === "liens") this.#lien.ensureLoaded();
    if (this.#area === "compliance") this.#compliance.ensureLoaded();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#ar = this.#newDesk();
    this.#gl = this.#newGlDesk();
    this.#stmt = this.#newStmtDesk();
    this.#bank = this.#newBankDesk();
    this.#tax = this.#newTaxDesk();
    this.#lien = this.#newLienDesk();
    this.#compliance = this.#newComplianceDesk();
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  // The theme control now rides the band's account menu; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button type="button" class="btn btn-secondary" data-action="theme" data-testid="theme-control">${label}</button>`;
  }

  // The desks the role holds, as the area switch: the controller's five, Payables and Receivables' four and the General
  // Manager's two. The ledger reads "Ledger" to a reader.
  private renderAreas(): TemplateResult {
    const area = (id: Area): TemplateResult => html`
      <button type="button" class="gl-tab" data-action="area" data-area=${id} aria-current="${this.#area === id ? "true" : "false"}">${id === "ledger" && !this.#isController() ? "Ledger" : AREA_LABEL[id]}</button>
    `;
    if (this.#areas().length === 0) return html``;
    return html`<nav class="areas" aria-label="Money Desk areas">${this.#areas().map((id) => area(id))}</nav>`;
  }

  private renderArea(): TemplateResult {
    switch (this.#area) {
      case "ledger":
        return html`<main class="gl-desk">${this.#gl.render()}</main>`;
      case "statements":
        return html`<main class="fs-desk">${this.#stmt.render()}</main>`;
      case "bank":
        return html`<main class="br-desk">${this.#bank.render()}</main>`;
      case "tax":
        return html`<main class="tx-desk">${this.#tax.render()}</main>`;
      case "liens":
        return html`<main class="li-desk">${this.#lien.render()}</main>`;
      case "compliance":
        return html`<main class="li-desk">${this.#compliance.render()}</main>`;
      default:
        return html`<main class="desk">${this.#ar.render()}</main>`;
    }
  }

  private renderSignedIn(): TemplateResult {
    return html`${this.renderAreas()} ${this.renderArea()}
      <hh-tanyai-panel></hh-tanyai-panel>`;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    return html`
      ${this.#framed ? nothing : html`<hh-shell-header data-testid="framed-band"></hh-shell-header>`}
      ${session !== null ? this.renderSignedIn() : html`
        <main class="desk">
          <h1>Money Desk</h1>
          <p class="dim">Receivables for the whole company: the aging, payments, promises and the bounced queue. Demo data only.</p>
          ${renderEntry(this.#entryView)}
        </main>
      `}
    `;
  }
}

defineElement("money-desk-app", MoneyDeskApp);
