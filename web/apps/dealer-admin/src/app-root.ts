// Dealer Admin's root element: the chrome, the way in, and the surface the
// registry shows. One app of admin surfaces: the left list is this app's own
// table of contents (the registry names them), the work area is the active
// surface's view. The next surface is one registry row and one views file;
// app-root's only change is the router line below.
//
// The way in is the demo's role cards (the demo adapter behind the auth
// package's AccountSurface seam). After the mint the branches resolve once,
// the ports are wired with the caller's headers, and the work area renders.
//
// The organisation header is the demo org by construction: the staff header
// provider refuses to send a request without X-Org, so no call ever leaves
// this app unresolved.
//
// The header is fed through plain attributes (signed-in, demo) and, for the
// role label, an assignment in updated(): the design system's position gate
// refuses every "." and "@" binding and every bound value outside the data-
// and aria- attributes in a dependent package. Its controls ride data-action
// names and the host's click listener below acts on them.
import { html, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// hos-app-header: the shared chrome element (decision log row 938).
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient } from "@hardscapehouse/api-client";
import type { DemoFlow } from "@hardscapehouse/api-client";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { drawIcon, iconForId } from "@hardscapehouse/pos-kit/src/ported/_icon-consumer.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { DealerApi, type BranchRow, type ResetLink } from "./dealer-api.js";
import { errorWords, staffRefusalWords } from "./dealer-logic.js";
import { SURFACES, isSurfaceId } from "./surfaces.js";
import { dealerStyles } from "./styles.js";
import { renderStaff, type StaffModel } from "./views-staff.js";

const DEMO_ORG = "hh-demo";

const emptyStaffModel = (): StaffModel => ({
  phase: "loading",
  rows: [],
  roles: [],
  branches: [],
  error: "",
  assignBusyId: "",
  resetBusyId: "",
  links: new Map(),
  resetErrors: new Map(),
  copiedId: "",
});

export class DealerAdminApp extends HHElement {
  static styles = [baseStyles, dealerStyles];

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // demo routes. It is the injected AccountSurface for the auth package.
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

  // The dealer's calls, over the staff client.
  readonly #api = new DealerApi(this.#staffClient);

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // Which surface of this app shows. The registry names them.
  #surface = SURFACES[0].id;

  // The staff surface's state.
  #staff: StaffModel = emptyStaffModel();

  // The way in: this app's own door, one card per demo role.
  #entryView: { flows: { kind: "loading" } | { kind: "unavailable" } | { kind: "ready"; flows: DemoFlow[] }; busy: boolean; message: string | null } = {
    flows: { kind: "loading" },
    busy: false,
    message: null,
  };

  // The copied mark's timer, cleared on disconnect and sign out.
  #copiedTimer = 0;

  constructor() {
    super();
    this.#demoAccount = new DemoAccount({ client: this.#demoClient });
    this.#auth = new AuthController({ account: this.#demoAccount, sessions: this.#sessions });
  }

  // Every control rides data-action names; one host listener reads them, as
  // the till's and Manager Home's do.
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
    if (action === "open-surface") {
      const surface = control?.getAttribute("data-surface") ?? "";
      if (isSurfaceId(surface)) {
        this.#surface = surface;
        this.requestUpdate();
      }
      return;
    }
    if (action === "reload-staff") {
      void this.#loadStaff();
      return;
    }
    if (action === "request-reset") {
      void this.#requestReset(control?.getAttribute("data-user-id") ?? "");
      return;
    }
    if (action === "copy-reset") {
      void this.#copyReset(control?.getAttribute("data-user-id") ?? "");
      return;
    }
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // The register's role selects answer on change: the element carries
  // data-action="assign-role" and the staff member's id. The option list
  // carries the catalog's names, so the answer is mapped back to the code
  // here; an empty answer (Unassigned) is skipped, as the original's own
  // assign was.
  readonly #onHostChange = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof Element) || target.getAttribute("data-action") !== "assign-role") return;
    const id = target.getAttribute("data-user-id") ?? "";
    const roleName = target instanceof HTMLSelectElement ? target.value : "";
    const role = this.#staff.roles.find((r) => r.name === roleName);
    void this.#assignRole(id, role?.code ?? "");
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    // change does not leave this element's own shadow root, so the select
    // listener sits on the root itself.
    this.renderRoot.addEventListener("change", this.#onHostChange);
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
        void this.#start();
      }
      this.requestUpdate();
    } else {
      void this.loadFlows();
    }
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("change", this.#onHostChange);
    window.clearTimeout(this.#copiedTimer);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render. The register's role selects
  // take the same road: the option list is the catalog's names, and the
  // row's current role is mirrored onto the element here.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
    for (const select of this.renderRoot.querySelectorAll<HTMLSelectElement>('select[data-action="assign-role"]')) {
      const id = select.getAttribute("data-user-id") ?? "";
      const row = this.#staff.rows.find((m) => m.user_id === id);
      if (row !== undefined) select.value = row.role_name;
    }
  }

  private async loadFlows(): Promise<void> {
    this.#entryView = { flows: { kind: "loading" }, busy: false, message: null };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).listFlows();
    if (result.ok) {
      this.#entryView = { flows: { kind: "ready", flows: result.value }, busy: false, message: null };
    } else {
      this.#entryView = { flows: { kind: "unavailable" }, busy: false, message: null };
    }
    this.requestUpdate();
  }

  // The signed-in start: the branches once (the grant chips' names), then the
  // register and the catalog.
  async #start(): Promise<void> {
    await this.#loadStaff();
  }

  async #loadStaff(): Promise<void> {
    this.#staff = emptyStaffModel();
    this.requestUpdate();
    const [branches, staff, roles] = await Promise.all([this.#api.branches(), this.#api.staff(), this.#api.roles()]);
    const branchRows: readonly BranchRow[] = branches.ok ? branches.value : [];
    // A refusal of the reads is the phase the words name; the branches read
    // failing only costs the chips their names (the ids still show).
    const fail = (error: string, status: number | undefined): void => {
      if (status === 403) {
        this.#staff = {
          ...this.#staff,
          phase: "refused",
          branches: branchRows,
          error: staffRefusalWords(errorWords(error), status),
        };
      } else {
        this.#staff = {
          ...this.#staff,
          phase: "error",
          branches: branchRows,
          error: `The staff register did not load. ${errorWords(error)}`,
        };
      }
      this.requestUpdate();
    };
    if (!staff.ok) {
      fail(staff.error, staff.status);
      return;
    }
    if (!roles.ok) {
      fail(roles.error, roles.status);
      return;
    }
    this.#staff = {
      ...this.#staff,
      phase: "ready",
      rows: staff.value,
      roles: roles.value,
      branches: branchRows,
      error: "",
    };
    this.requestUpdate();
  }

  async #assignRole(id: string, roleCode: string): Promise<void> {
    if (id === "" || roleCode === "" || this.#staff.assignBusyId !== "") return;
    this.#staff = { ...this.#staff, assignBusyId: id, error: "" };
    this.requestUpdate();
    const result = await this.#api.assignRole(id, roleCode);
    if (!result.ok) {
      this.#staff = {
        ...this.#staff,
        assignBusyId: "",
        error: `The role did not change. ${staffRefusalWords(errorWords(result.error), result.status)}`,
      };
      this.requestUpdate();
      return;
    }
    await this.#loadStaff();
  }

  async #requestReset(id: string): Promise<void> {
    if (id === "" || this.#staff.resetBusyId !== "") return;
    const resetErrors = new Map(this.#staff.resetErrors);
    resetErrors.set(id, "");
    this.#staff = { ...this.#staff, resetBusyId: id, resetErrors };
    this.requestUpdate();
    const result = await this.#api.requestPasswordReset(id);
    if (!result.ok) {
      const failures = new Map(this.#staff.resetErrors);
      failures.set(id, staffRefusalWords(errorWords(result.error), result.status));
      this.#staff = { ...this.#staff, resetBusyId: "", resetErrors: failures };
      this.requestUpdate();
      return;
    }
    const links = new Map<string, ResetLink>(this.#staff.links);
    links.set(id, result.value);
    this.#staff = { ...this.#staff, resetBusyId: "", links };
    this.requestUpdate();
  }

  async #copyReset(id: string): Promise<void> {
    const link = this.#staff.links.get(id);
    if (link === undefined) return;
    try {
      await navigator.clipboard.writeText(link.reset_url);
      this.#staff = { ...this.#staff, copiedId: id };
      this.requestUpdate();
      window.clearTimeout(this.#copiedTimer);
      this.#copiedTimer = window.setTimeout(() => {
        if (this.#staff.copiedId === id) {
          this.#staff = { ...this.#staff, copiedId: "" };
          this.requestUpdate();
        }
      }, 2000);
    } catch {
      // Clipboard unavailable: the link renders as selectable text beside the button.
    }
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
        await this.#start();
      } else {
        this.#entryView = { ...this.#entryView, busy: false, message: "Demo sign-in refused: the mint's session was not usable." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Demo sign-in refused. ${errorWords(result.error)}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#surface = SURFACES[0].id;
    this.#staff = emptyStaffModel();
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Brand theme" : "Light theme";
    return html`<button slot="theme" type="button" class="btn btn-secondary" data-action="theme">${label}</button>`;
  }

  // The app's own left list: one row per registry surface.
  private renderSurfaceList(): TemplateResult {
    return html`
      <nav class="surface-list" aria-label="Dealer Admin surfaces">
        ${SURFACES.map(
          (s) => html`
            <button
              type="button"
              class="surface-link"
              data-action="open-surface"
              data-surface=${s.id}
              aria-current=${this.#surface === s.id ? "true" : "false"}
            >
              <span class="title">${s.title}</span>
              <span class="blurb">${s.blurb}</span>
            </button>
          `,
        )}
      </nav>
    `;
  }

  private renderWork(): TemplateResult {
    // The router line: one case per registry surface. The next coder adds
    // theirs beside this one; nothing else in this file changes.
    if (this.#surface === "staff") return renderStaff(this.#staff);
    return renderStaff(this.#staff);
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo product-name="Admin" product-sub="Dealer Admin · HH Work shell">${this.renderThemeControl()}</hos-app-header>
      <main>
        <div class="app-body">
          ${this.renderSurfaceList()}
          <div class="surface-work">${this.renderWork()}</div>
        </div>
      </main>
    `;
  }

  // -- the way in (the demo door) -----------------------------------------------------------------

  /** The marks by demo flow id: what this app opens for the role. */
  private static readonly FLOW_ICONS: Readonly<Record<string, string>> = {
    "dealer-admin": "lucide:shield-check",
  };

  private renderEntry(): TemplateResult {
    const view = this.#entryView;
    if (view.flows.kind === "loading") {
      return html`<div class="stage"><p class="notice-line" role="status">Loading the demo roles.</p></div>`;
    }
    if (view.flows.kind === "unavailable") {
      return html`<div class="stage"><p class="notice-line" role="status">The demo roles could not be loaded. Reload to try again.</p></div>`;
    }
    if (view.flows.flows.length === 0) {
      return html`<div class="stage"><p class="notice-line" role="status">The demo sign-in answered with no roles: there is nothing to tap.</p></div>`;
    }
    return html`
      <div class="stage">
        <h1 class="headline">Dealer Admin</h1>
        <p class="intro">Tap a role to open the dealer's administration:</p>
        <div class="role-cards">
          ${view.flows.flows.map((flow) => html`
            <button
              type="button"
              class="role-card"
              data-action="choose"
              data-flow=${flow.flow ?? nothing}
              aria-label=${flow.label ?? flow.flow ?? nothing}
              ?disabled=${view.busy}
            >
              <span class="mark">${drawIcon(iconForId(DealerAdminApp.FLOW_ICONS[flow.flow ?? ""] ?? "lucide:circle-user"), 29)}</span>
              <span class="words">
                <span class="role">${flow.label ?? flow.flow}</span>
                <span class="what">Staff, hotkeys and the audit trail.</span>
              </span>
            </button>
          `)}
        </div>
        ${view.message === null ? nothing : html`<p class="notice-line" role="status">${view.message}</p>`}
      </div>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo product-name="Admin" product-sub="Dealer Admin · HH Work shell">${this.renderThemeControl()}</hos-app-header>
      <main>
        ${this.renderEntry()}
      </main>
    `;
  }
}

defineElement("dealer-admin-app", DealerAdminApp);
