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
import { areaStyles } from "./area-styles.js";
import { PoApi } from "./po/po-api.js";
import { PoDesk } from "./po/po-desk.js";
import { poStyles } from "./po/po-styles.js";
import { InventoryApi } from "./inventory/inventory-api.js";
import { InventoryDesk } from "./inventory/inventory-desk.js";
import { CountsApi } from "./counts/counts-api.js";
import { CountsDesk } from "./counts/counts-desk.js";

const DEMO_ORG = "hh-demo";

// The app's areas, in the order the bar shows them. A new area is one row
// here, one arm in #onArea, and its own slice folder; landed cost and the
// purchasing back office land the same way.
const AREAS = [
  { id: "purchasing", label: "Purchasing" },
  { id: "inventory", label: "Inventory" },
  { id: "counts", label: "Cycle counts" },
] as const;

type AreaId = (typeof AREAS)[number]["id"];

export class BuyingAndStockApp extends HHElement {
  static styles = [baseStyles, entryStyles, poStyles, areaStyles, css`
    :host { display: block; min-height: 100vh; background: var(--hh-page); color: var(--hh-text); }
    main { padding: var(--hh-s-4) 0; }
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

  // The procurement slice (src/po): its state, its loads, its presses. The
  // fields it reads are looked up in this element's own shadow root.
  #po = this.#newDesk();

  // The area the bar shows; the procurement desk keeps the landing.
  #area: AreaId = "purchasing";

  // The inventory and counts slices (src/inventory, src/counts), each loaded
  // the first time its area is opened.
  #inventory: InventoryDesk | null = null;
  #counts: CountsDesk | null = null;

  #newDesk(): PoDesk {
    return new PoDesk(new PoApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      field: (name) => this.#fieldElement(name)?.value ?? "",
    });
  }

  // The seat the signed-in role holds on this surface, per the role matrix:
  // the Procurement Manager manages, the Yard Manager works, the rest read.
  #seat(): "manage" | "work" | "read" {
    if (this.#roleCode === "procurement_manager") return "manage";
    if (this.#roleCode === "yard_manager") return "work";
    return "read";
  }

  #newInventoryDesk(): InventoryDesk {
    return new InventoryDesk(new InventoryApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      field: (name) => this.#fieldElement(name)?.value ?? "",
      seat: this.#seat(),
    });
  }

  #newCountsDesk(): CountsDesk {
    return new CountsDesk(new CountsApi(this.#staffClient), {
      repaint: () => this.requestUpdate(),
      field: (name, key) => {
        const scope = key === undefined ? "" : `[data-key="${key}"]`;
        return this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"]${scope}`)?.value ?? "";
      },
      seat: this.#seat(),
    });
  }

  #fieldElement(name: string): HTMLInputElement | HTMLSelectElement | null {
    return this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"]`);
  }

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
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

  constructor() {
    super();
    this.#demoAccount = new DemoAccount({ client: this.#demoClient });
    this.#auth = new AuthController({ account: this.#demoAccount, sessions: this.#sessions });
  }

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The signed-in role's code: it picks the seat the refusal words name.
  #roleCode = "";

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
    if (action === "area") {
      const area = control?.getAttribute("data-area");
      if (area === "purchasing" || area === "inventory" || area === "counts") this.#onArea(area);
      return;
    }
    if (this.#area === "inventory" && this.#inventory !== null && control !== undefined && this.#inventory.press(control)) return;
    if (this.#area === "counts" && this.#counts !== null && control !== undefined && this.#counts.press(control)) return;
    if (control !== undefined && this.#po.press(control)) return;
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // Opening an area loads its slice the first time, so the reads run when the
  // reader arrives, not on every sign-in.
  #onArea(area: AreaId): void {
    if (this.#area === area) return;
    this.#area = area;
    if (area === "inventory" && this.#inventory === null) {
      this.#inventory = this.#newInventoryDesk();
      void this.#inventory.load();
    }
    if (area === "counts" && this.#counts === null) {
      this.#counts = this.#newCountsDesk();
      void this.#counts.load();
    }
    this.requestUpdate();
  }

  // The draft's date field: the words under it follow what is typed (the field itself is never
  // bound). A counted figure is kept as typed, so the row's variance and reason select follow.
  readonly #onHostInput = (event: Event): void => {
    const target = event.composedPath()[0];
    if (target instanceof HTMLInputElement && target.getAttribute("data-field") === "expected") this.#po.expectedTyped(target.value);
    if (target instanceof HTMLInputElement && target.getAttribute("data-field") === "counted" && this.#counts !== null) {
      this.#counts.typed(target.getAttribute("data-key") ?? "", target.value);
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("input", this.#onHostInput);
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
        this.#roleLabel = handoff.session.user.name;
        this.#roleCode = handoff.session.roleCode;
        clearFrontDoorHandoff();
        void this.#po.load();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("input", this.#onHostInput);
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
    const widget = this.renderRoot.querySelector("hh-feedback");
    if (widget instanceof HHFeedback && widget.send !== this.#feedbackSender) widget.send = this.#feedbackSender;
    // A draft's starting field values: set once per draft, after the render that drew them.
    this.#po.seed((name, value) => {
      const field = this.#fieldElement(name);
      if (field !== null) field.value = value;
    });
    // The ship box's pre-fill and the point editor's starting figures.
    this.#inventory?.seed((name, value) => {
      const field = this.#fieldElement(name);
      if (field !== null && field.value !== value) field.value = value;
    });
    // The count sheet's seeded figures, per line, set once per open sheet.
    this.#counts?.seed((name, value, key) => {
      const scope = key === undefined ? "" : `[data-key="${key}"]`;
      const field = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${name}"]${scope}`);
      if (field !== null && field.value !== value) field.value = value;
    });
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
        this.#roleLabel = result.value.user.name;
        this.#roleCode = result.value.roleCode;
        void this.#po.load();
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
    this.#po = this.#newDesk();
    this.#area = "purchasing";
    this.#inventory = null;
    this.#counts = null;
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Theme: Light" : "Theme: Brand";
    return html`<button slot="theme" type="button" class="theme-btn" data-action="feedback" title="Send feedback about this screen">Feedback</button><button slot="theme" type="button" class="theme-btn" data-action="theme">${label}</button>`;
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo compact product-name="Yard">${this.renderThemeControl()}</hos-app-header>
      <nav class="areas" aria-label="Buying and Stock areas">
        ${AREAS.map(
          (a) => html`<button type="button" class="area-tab" data-action="area" data-area=${a.id} aria-current="${this.#area === a.id ? "true" : "false"}">${a.label}</button>`,
        )}
      </nav>
      <main class="desk desk-area">
        ${this.#area === "inventory"
          ? (this.#inventory?.render() ?? html`<p class="dim" role="status">Loading the stock reads.</p>`)
          : this.#area === "counts"
            ? (this.#counts?.render() ?? html`<p class="dim" role="status">Loading the count sheets.</p>`)
            : this.#po.render()}
      </main>
      <hh-feedback screen="Buying and Stock" trigger="external"></hh-feedback>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo compact product-name="Yard">${this.renderThemeControl()}</hos-app-header>
      <main class="desk">
        <h1>Buying and Stock</h1>
        <p class="dim">Purchase orders and the reorder worklist for the procurement manager. Demo data only.</p>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("buying-and-stock-app", BuyingAndStockApp);
