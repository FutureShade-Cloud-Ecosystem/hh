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
import {
  PLATFORM_SURFACES,
  dealerFor,
  draftFrom,
  draftToKeys,
  otherSurface,
  platformBadChordSentence,
  platformChordFrom,
  platformValidBinding,
  reservedOf,
  rosterFor,
  serverSentence,
  effectiveVerdict,
  type PlatformHotkeyView,
} from "./hotkey-logic.js";
import { SURFACES, isSurfaceId } from "./surfaces.js";
import { dealerStyles } from "./styles.js";
import { pricingStyles } from "./pricing-styles.js";
import { renderStaff, type StaffModel } from "./views-staff.js";
import { renderHotkeys, type HotkeysModel } from "./views-hotkeys.js";
import { renderAudit, type AuditDetail, type AuditModel, type AuditTrail } from "./views-audit.js";
import { EMPTY_FILTERS, sinceWire, type AuditFilters } from "./audit-logic.js";
import { PricingApi } from "./pricing-api.js";
import { pricingAttrs, PricingController } from "./pricing-controller.js";
import { renderPricing } from "./views-pricing.js";
import type { AuditRow } from "./dealer-api.js";

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

const emptyHotkeysModel = (): HotkeysModel => ({
  phase: "loading",
  view: null,
  drafts: {},
  capturing: null,
  refusals: {},
  notes: {},
  loadError: "",
  saveError: "",
  notice: "",
  busy: false,
});

const emptyAuditModel = (): AuditModel => ({
  phase: "loading",
  rows: [],
  filters: { ...EMPTY_FILTERS },
  error: "",
  detail: null,
  trail: null,
});

export class DealerAdminApp extends HHElement {
  static styles = [baseStyles, dealerStyles, pricingStyles];

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

  // The pricing surface: its own controller, door and model, so this file
  // carries only the wiring (the listener branches and the router line).
  readonly #pricing = new PricingController(new PricingApi(this.#staffClient), () => this.requestUpdate());

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // Which surface of this app shows. The registry names them.
  #surface = SURFACES[0].id;

  // The staff surface's state.
  #staff: StaffModel = emptyStaffModel();

  // The hotkeys surface's state.
  #hotkeys: HotkeysModel = emptyHotkeysModel();

  // The audit surface's state, and the detail fetch's generation counter: a
  // response whose generation is no longer current is dropped, so opening a
  // second row, filtering or closing voids the answer still in flight.
  #audit: AuditModel = emptyAuditModel();
  #auditDetailGen = 0;

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
        // A surface loads its own reads the first time it is opened.
        if (surface === "hotkeys" && this.#hotkeys.phase === "loading") void this.#loadHotkeys();
        if (surface === "audit" && this.#audit.phase === "loading") void this.#loadAudit();
        if (surface === "pricing") void this.#pricing.open();
        this.requestUpdate();
      }
      return;
    }
    // The pricing surface's controls: every action rides a pr- prefixed name
    // and the controller owns the whole state machine behind them.
    if (typeof action === "string" && action.startsWith("pr-")) {
      if (control !== undefined) void this.#pricing.click(action, pricingAttrs(control));
      return;
    }
    if (action === "reload-staff") {
      void this.#loadStaff();
      return;
    }
    if (action === "reload-hotkeys") {
      void this.#loadHotkeys();
      return;
    }
    if (action === "reload-audit") {
      void this.#loadAudit();
      return;
    }
    if (action === "apply-audit-filters") {
      void this.#loadAudit();
      return;
    }
    if (action === "clear-audit-filters") {
      this.#audit = { ...this.#audit, filters: { ...EMPTY_FILTERS } };
      this.requestUpdate();
      void this.#loadAudit();
      return;
    }
    if (action === "open-audit-entry") {
      void this.#openAuditEntry(control?.getAttribute("data-entry-id") ?? "");
      return;
    }
    if (action === "close-audit-detail") {
      this.#closeAuditDetail();
      return;
    }
    if (action === "open-audit-trail") {
      void this.#openAuditTrail(
        control?.getAttribute("data-entity-type") ?? "",
        control?.getAttribute("data-entity-id") ?? "",
      );
      return;
    }
    if (action === "ph-set") {
      const surface = control?.getAttribute("data-surface") ?? "";
      const keyAction = control?.getAttribute("data-key-action") ?? "";
      if (surface !== "" && keyAction !== "") this.#armCapture(surface, keyAction);
      return;
    }
    if (action === "ph-clear") {
      const surface = control?.getAttribute("data-surface") ?? "";
      const keyAction = control?.getAttribute("data-key-action") ?? "";
      if (surface !== "" && keyAction !== "") this.#clearKey(surface, keyAction);
      return;
    }
    if (action === "ph-save") {
      void this.#saveRoster(control?.getAttribute("data-surface") ?? "");
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

  // The register's role selects and the pricing editors' selects and
  // checkboxes answer on change: the element carries a data-field (pricing)
  // or data-action (the register), and the answer is mapped back here. A
  // checkbox rides as yes or no; a select's answer is the option's own word,
  // which the controller maps back to the id it stands for.
  readonly #onHostChange = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const field = target.getAttribute("data-field") ?? "";
    if (field.startsWith("pr-")) {
      let value = target instanceof HTMLInputElement || target instanceof HTMLSelectElement ? target.value : "";
      if (target instanceof HTMLInputElement && target.type === "checkbox") value = target.checked ? "yes" : "no";
      this.#pricing.change(field.slice(3), value);
      return;
    }
    if (target.getAttribute("data-action") !== "assign-role") return;
    const id = target.getAttribute("data-user-id") ?? "";
    const roleName = target instanceof HTMLSelectElement ? target.value : "";
    const role = this.#staff.roles.find((r) => r.name === roleName);
    void this.#assignRole(id, role?.code ?? "");
  };

  // The audit filters' and the pricing editors' fields mirror from input
  // events, never bound.
  readonly #onHostInput = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const field = target.getAttribute("data-field") ?? "";
    if (field.startsWith("pr-")) {
      const value =
        target instanceof HTMLInputElement || target instanceof HTMLSelectElement || target instanceof HTMLTextAreaElement
          ? target.value
          : "";
      this.#pricing.input(field.slice(3), value);
      return;
    }
    if (!field.startsWith("audit-")) return;
    const value = target instanceof HTMLInputElement || target instanceof HTMLSelectElement ? target.value : "";
    const filters = this.#audit.filters;
    if (field === "audit-entity-type") this.#audit = { ...this.#audit, filters: { ...filters, entity_type: value } };
    if (field === "audit-action") this.#audit = { ...this.#audit, filters: { ...filters, action: value } };
    if (field === "audit-actor-id") this.#audit = { ...this.#audit, filters: { ...filters, actor_id: value } };
    if (field === "audit-since") this.#audit = { ...this.#audit, filters: { ...filters, since: value } };
  };

  // Enter opens the focused trail row; Escape closes the drawer.
  readonly #onHostKeydown = (event: Event): void => {
    if (!(event instanceof KeyboardEvent)) return;
    const target = event.target;
    if (!(target instanceof Element)) return;
    if (event.key === "Enter" && target.getAttribute("data-action") === "open-audit-entry") {
      event.preventDefault();
      void this.#openAuditEntry(target.getAttribute("data-entry-id") ?? "");
      return;
    }
    if (event.key === "Escape" && this.#audit.detail !== null) {
      event.preventDefault();
      this.#closeAuditDetail();
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    // change, input and keydown do not leave this element's own shadow root,
    // so their listeners sit on the root itself.
    this.renderRoot.addEventListener("change", this.#onHostChange);
    this.renderRoot.addEventListener("input", this.#onHostInput);
    this.renderRoot.addEventListener("keydown", this.#onHostKeydown);
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
    this.renderRoot.removeEventListener("input", this.#onHostInput);
    this.renderRoot.removeEventListener("keydown", this.#onHostKeydown);
    if (this.#hotkeys.capturing !== null) window.removeEventListener("keydown", this.#onCapture, true);
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
    // The audit filters' fields mirror the model too, so a Clear empties
    // what is on the glass and a reload keeps the person's words.
    const fields: Record<string, string> = {
      "audit-entity-type": this.#audit.filters.entity_type,
      "audit-action": this.#audit.filters.action,
      "audit-actor-id": this.#audit.filters.actor_id,
      "audit-since": this.#audit.filters.since,
    };
    for (const [field, value] of Object.entries(fields)) {
      const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${field}"]`);
      if (el !== null && el.value !== value) el.value = value;
    }
    // The pricing editors' fields mirror the controller's model the same
    // way, checkbox included.
    for (const el of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-field^="pr-"]'))) {
      const field = el.getAttribute("data-field") ?? "";
      const value = this.#pricing.fieldValue(field.slice(3));
      if (el instanceof HTMLInputElement && el.type === "checkbox") {
        const checked = value === "yes";
        if (el.checked !== checked) el.checked = checked;
      } else if (el.value !== value) {
        el.value = value;
      }
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

  // -- the hotkeys surface ---------------------------------------------------------------------------

  async #loadHotkeys(): Promise<void> {
    // The save's notice outlives the re-read that follows it: the reload
    // answers the read, not the write, and the person is mid work.
    this.#hotkeys = { ...emptyHotkeysModel(), notice: this.#hotkeys.notice };
    this.requestUpdate();
    const result = await this.#api.platformHotkeys();
    if (!result.ok) {
      this.#hotkeys = {
        ...this.#hotkeys,
        phase: "error",
        loadError: `The shortcut keys did not load. ${errorWords(serverSentence(result.error))}`,
      };
      this.requestUpdate();
      return;
    }
    const drafts: Record<string, Record<string, string>> = {};
    for (const surface of PLATFORM_SURFACES) {
      drafts[surface] = draftFrom(rosterFor(result.value, surface));
    }
    this.#hotkeys = { ...this.#hotkeys, phase: "ready", view: result.value, drafts };
    this.requestUpdate();
  }

  /**
   * Arm one row for a keystroke. The listener goes on the window in the
   * CAPTURE phase, for the same reason the counter's own capture does: an
   * F-key pressed with focus anywhere must reach it, and nothing downstream,
   * the shell's own hotkey map very much included, may act on a keystroke
   * that is being recorded rather than obeyed.
   */
  #armCapture(surface: string, action: string): void {
    if (this.#hotkeys.view?.canEdit !== true) return;
    const armed =
      this.#hotkeys.capturing !== null && this.#hotkeys.capturing.surface === surface && this.#hotkeys.capturing.action === action;
    this.#stopCapture();
    if (armed) return;
    this.#hotkeys = {
      ...this.#hotkeys,
      capturing: { surface, action },
      refusals: { ...this.#hotkeys.refusals, [surface]: "" },
    };
    this.requestUpdate();
    window.addEventListener("keydown", this.#onCapture, true);
  }

  #stopCapture(): void {
    if (this.#hotkeys.capturing !== null) window.removeEventListener("keydown", this.#onCapture, true);
    if (this.#hotkeys.capturing !== null) {
      this.#hotkeys = { ...this.#hotkeys, capturing: null };
      this.requestUpdate();
    }
  }

  readonly #onCapture = (e: KeyboardEvent): void => {
    const armed = this.#hotkeys.capturing;
    if (armed === null) return;
    const { surface, action } = armed;
    if (e.key === "Escape" || e.key === "Tab") {
      // Escape abandons and Tab walks on: a capture nobody can leave is a trap.
      e.preventDefault();
      this.#stopCapture();
      return;
    }
    const chord = platformChordFrom(surface, e);
    if (chord === "") {
      if (e.key === "Enter" || e.key === " ") return; // the press that armed it, arriving late
      e.preventDefault();
      e.stopPropagation();
      this.#hotkeys = { ...this.#hotkeys, refusals: { ...this.#hotkeys.refusals, [surface]: platformBadChordSentence(surface) } };
      this.requestUpdate();
      return;
    }
    e.preventDefault();
    e.stopPropagation();
    if (!platformValidBinding(surface, chord)) {
      this.#hotkeys = { ...this.#hotkeys, refusals: { ...this.#hotkeys.refusals, [surface]: platformBadChordSentence(surface) } };
      this.requestUpdate();
      return;
    }
    const draft = this.#hotkeys.drafts[surface] ?? {};
    const verdict = effectiveVerdict(surface, action, chord, {
      draft,
      dealer: dealerFor(this.#hotkeys.view, surface),
      reserved: reservedOf(this.#hotkeys.view),
      other: rosterFor(this.#hotkeys.view, otherSurface(surface)),
    });
    if (verdict.kind === "taken") {
      // REFUSED BEFORE THE SAVE, named by what holds the key.
      this.#hotkeys = { ...this.#hotkeys, refusals: { ...this.#hotkeys.refusals, [surface]: verdict.sentence } };
      this.requestUpdate();
      return;
    }
    this.#hotkeys = {
      ...this.#hotkeys,
      drafts: { ...this.#hotkeys.drafts, [surface]: { ...draft, [action]: chord } },
      refusals: { ...this.#hotkeys.refusals, [surface]: "" },
      // The bind stands; the sentence, if there is one, says which other
      // surface holds the key and why sharing it is safe.
      notes: { ...this.#hotkeys.notes, [surface]: verdict.kind === "shared" ? verdict.sentence : "" },
    };
    this.#stopCapture();
  };

  /** One action, unbound. An action with no key simply draws no keycap anywhere. */
  #clearKey(surface: string, action: string): void {
    const next = { ...(this.#hotkeys.drafts[surface] ?? {}) };
    delete next[action];
    this.#hotkeys = {
      ...this.#hotkeys,
      drafts: { ...this.#hotkeys.drafts, [surface]: next },
      refusals: { ...this.#hotkeys.refusals, [surface]: "" },
      notes: { ...this.#hotkeys.notes, [surface]: "" },
    };
    this.requestUpdate();
  }

  /** One surface's roster, saved whole. */
  async #saveRoster(surface: string): Promise<void> {
    if (surface === "" || this.#hotkeys.busy) return;
    const keys = draftToKeys(surface, this.#hotkeys.drafts[surface] ?? {});
    if (keys.length === 0) {
      this.#hotkeys = { ...this.#hotkeys, saveError: "Bind at least one key, or every terminal inherits a keyboard that does nothing." };
      this.requestUpdate();
      return;
    }
    this.#hotkeys = { ...this.#hotkeys, busy: true, saveError: "", notice: "" };
    this.requestUpdate();
    const result = await this.#api.putPlatformHotkeys(surface, keys);
    if (!result.ok) {
      // The server's own sentence is the message: a 4xx arrives in words.
      this.#hotkeys = { ...this.#hotkeys, busy: false, saveError: serverSentence(result.error) || "The keys were not saved." };
      this.requestUpdate();
      return;
    }
    this.#hotkeys = {
      ...this.#hotkeys,
      busy: false,
      notice: "Saved. Every dealer who has not set their own now uses these.",
    };
    this.requestUpdate();
    await this.#loadHotkeys();
  }

  // -- the audit surface ------------------------------------------------------------------------------

  async #loadAudit(): Promise<void> {
    // A new list is a new question: the drawer's in-flight answer is void.
    this.#auditDetailGen += 1;
    this.#audit = { ...emptyAuditModel(), filters: this.#audit.filters };
    this.requestUpdate();
    const f = this.#audit.filters;
    const result = await this.#api.audit({
      entity_type: f.entity_type.trim() === "" ? undefined : f.entity_type.trim(),
      action: f.action === "" ? undefined : f.action,
      actor_id: f.actor_id.trim() === "" ? undefined : f.actor_id.trim(),
      since: sinceWire(f.since) === "" ? undefined : sinceWire(f.since),
    });
    if (!result.ok) {
      if (result.status === 403) {
        this.#audit = {
          ...this.#audit,
          phase: "refused",
          error: staffRefusalWords(errorWords(result.error), result.status),
        };
      } else {
        this.#audit = {
          ...this.#audit,
          phase: "error",
          error: `The trail did not load. ${errorWords(result.error)}`,
        };
      }
      this.requestUpdate();
      return;
    }
    this.#audit = { ...this.#audit, phase: "ready", rows: result.value };
    this.requestUpdate();
  }

  /**
   * Open one entry's masked detail. The drawer paints the list's projection
   * first and swaps in Get's answer when it lands; a response whose
   * generation is no longer current is dropped, so opening a second row,
   * filtering or closing voids the answer still in flight.
   */
  async #openAuditEntry(id: string): Promise<void> {
    if (id === "") return;
    this.#auditDetailGen += 1;
    const gen = this.#auditDetailGen;
    const row: AuditRow | undefined = this.#audit.rows.find((r) => r.id === id)
      ?? this.#audit.trail?.rows.find((r) => r.id === id);
    const detail: AuditDetail = { forId: id, state: "loading", entry: row ?? null };
    this.#audit = { ...this.#audit, detail };
    this.requestUpdate();
    const result = await this.#api.auditEntry(id);
    if (gen !== this.#auditDetailGen) return;
    if (!result.ok) {
      this.#audit = { ...this.#audit, detail: { ...detail, state: "error" } };
      this.requestUpdate();
      return;
    }
    this.#audit = { ...this.#audit, detail: { forId: id, state: "ready", entry: result.value } };
    this.requestUpdate();
  }

  #closeAuditDetail(): void {
    // Closing is an answer to the question: the in-flight answer is void.
    this.#auditDetailGen += 1;
    this.#audit = { ...this.#audit, detail: null };
    this.requestUpdate();
  }

  async #openAuditTrail(entityType: string, entityId: string): Promise<void> {
    if (entityType === "" || entityId === "") return;
    const trail: AuditTrail = { entityType, entityId, phase: "loading", rows: [], error: "" };
    this.#audit = { ...this.#audit, trail };
    this.requestUpdate();
    const result = await this.#api.auditTransaction(entityType, entityId);
    if (result.ok) {
      this.#audit = { ...this.#audit, trail: { ...trail, phase: "ready", rows: result.value } };
    } else {
      this.#audit = {
        ...this.#audit,
        trail: { ...trail, phase: "error", error: `The entity's trail did not load. ${errorWords(result.error)}` },
      };
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
    if (this.#hotkeys.capturing !== null) window.removeEventListener("keydown", this.#onCapture, true);
    this.#surface = SURFACES[0].id;
    this.#staff = emptyStaffModel();
    this.#hotkeys = emptyHotkeysModel();
    this.#audit = emptyAuditModel();
    this.#auditDetailGen += 1;
    this.#pricing.reset();
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
    if (this.#surface === "hotkeys") return renderHotkeys(this.#hotkeys);
    if (this.#surface === "audit") return renderAudit(this.#audit);
    if (this.#surface === "pricing") return renderPricing(this.#pricing.model);
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
