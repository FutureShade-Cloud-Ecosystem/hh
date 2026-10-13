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
import { ConfigApi, type CellConfig } from "./config-api.js";
import { SettingsApi } from "./settings-api.js";
import {
  chipSetError,
  chipsOf,
  counterPolicyRefusalWords,
  policyHistoryRefusalWords,
  restockingFeeError,
  tourLineError,
  tourPlatformWords,
} from "./settings-logic.js";
import {
  CONFIG_SURFACES,
  JOB_SHAPES,
  configRefusalWords,
  itemRowError,
  itemSetError,
  newWorkspaceKey,
  workspaceSplitError,
  type ConfigItem,
} from "./config-logic.js";
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
import {
  renderRoleConfig,
  cellKey,
  type ConfigEditor,
  type RoleConfigModel,
  type WorkspaceDraft,
} from "./views-config.js";
import {
  renderSettings,
  type BrandSection,
  type PaymentsSection,
  type PolicySection,
  type SettingsModel,
  type TourSection,
} from "./views-settings.js";
import { EMPTY_FILTERS, auditRefusalWords, sinceWire, type AuditFilters } from "./audit-logic.js";
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

const emptyRoleConfigModel = (): RoleConfigModel => ({
  phase: "loading",
  cells: new Map(),
  loaded: 0,
  total: JOB_SHAPES.length * CONFIG_SURFACES.length,
  words: "",
  roles: [],
  editor: null,
});

const emptyConfigEditor = (surface: string, profile: string, config: CellConfig): ConfigEditor => ({
  surface,
  profile,
  draft: config.role_items.map((it) => ({ ...it })),
  myDraft: config.user_items.map((it) => ({ ...it })),
  wsDraft: {
    split_pct: config.workspace.split_pct,
    arrangement: config.workspace.arrangement,
    primary_collapsed: config.workspace.primary_collapsed,
    secondary_collapsed: config.workspace.secondary_collapsed,
    cost_hidden: config.workspace.cost_hidden,
    source: config.workspace.source,
  },
  addKind: "panel",
  addId: "",
  myAddKind: "panel",
  myAddId: "",
  error: "",
  notice: "",
  busy: false,
});

const emptySettingsModel = (): SettingsModel => ({
  brand: {
    phase: "loading",
    displayName: "",
    footerText: "",
    primaryColor: "",
    busy: false,
    notice: "",
    error: "",
  },
  payments: {
    phase: "loading",
    status: null,
    error: "",
  },
  policy: {
    phase: "loading",
    words: "",
    fee: 0,
    notes: "",
    chips: "",
    split: 58,
    cod: false,
    quickChips: false,
    linePresentation: "LINES",
    lookupPresentation: "CARDS",
    arrangement: "LOOKUP_LEFT",
    marginVisibility: "MANAGER_UP",
    balanceVisibility: "ALL_STAFF",
    supplierDepositCancel: "CREDIT_BACK",
    history: [],
    historyPhase: "loading",
    historyWords: "",
    busy: false,
    notice: "",
    error: "",
  },
  tour: {
    phase: "loading",
    words: "",
    view: null,
    editChapter: "",
    editStep: "",
    editLine: "",
    editRoute: "",
    editAnchor: "",
    busy: false,
    notice: "",
    error: "",
  },
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

  // The role configuration's calls, over the staff client.
  readonly #configApi = new ConfigApi(this.#staffClient);

  // The deployment settings' calls, over the staff client.
  readonly #settingsApi = new SettingsApi(this.#staffClient);
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

  // The role configuration surface's state.
  #roleConfig: RoleConfigModel = emptyRoleConfigModel();

  // The deployment settings surface's state.
  #settings: SettingsModel = emptySettingsModel();

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
        if (surface === "role-config" && this.#roleConfig.phase === "loading") void this.#loadRoleConfig();
        if (surface === "deployment" && this.#settings.brand.phase === "loading") void this.#loadSettings();
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
    if (action === "reload-role-config") {
      void this.#loadRoleConfig();
      return;
    }
    if (action === "open-config-editor") {
      this.#openConfigEditor(control?.getAttribute("data-surface") ?? "", control?.getAttribute("data-profile") ?? "");
      return;
    }
    if (action === "close-config-editor") {
      this.#patchRoleConfig({ editor: null });
      return;
    }
    if (action === "config-add-row") {
      this.#configAddRow(false);
      return;
    }
    if (action === "config-my-add-row") {
      this.#configAddRow(true);
      return;
    }
    if (action === "config-toggle-visible" || action === "config-my-toggle-visible") {
      this.#configToggleVisible(action.startsWith("config-my"), Number(control?.getAttribute("data-index")));
      return;
    }
    if (action === "config-row-up" || action === "config-my-row-up") {
      this.#configMoveRow(action.startsWith("config-my"), Number(control?.getAttribute("data-index")), -1);
      return;
    }
    if (action === "config-row-down" || action === "config-my-row-down") {
      this.#configMoveRow(action.startsWith("config-my"), Number(control?.getAttribute("data-index")), 1);
      return;
    }
    if (action === "config-remove-row" || action === "config-my-remove-row") {
      this.#configRemoveRow(action.startsWith("config-my"), Number(control?.getAttribute("data-index")));
      return;
    }
    if (action === "config-save") {
      void this.#saveRoleItems();
      return;
    }
    if (action === "config-reset-kind") {
      void this.#resetRoleItems(control?.getAttribute("data-kind") ?? "");
      return;
    }
    if (action === "config-save-my") {
      void this.#saveMyItems();
      return;
    }
    if (action === "config-reset-my") {
      void this.#resetMyItems();
      return;
    }
    if (action === "configws-flag") {
      const field = control?.getAttribute("data-field") ?? "";
      const on = control instanceof HTMLInputElement ? control.checked : false;
      this.#configWsFlag(field, on);
      return;
    }
    if (action === "configws-save") {
      void this.#saveMyWorkspace();
      return;
    }
    if (action === "configws-reset") {
      void this.#resetMyWorkspace();
      return;
    }
    if (action === "reload-settings") {
      void this.#loadSettings();
      return;
    }
    if (action === "settings-save-brand") {
      void this.#saveBrand();
      return;
    }
    if (action === "policy-flag") {
      const field = control?.getAttribute("data-field") ?? "";
      const on = control instanceof HTMLInputElement ? control.checked : false;
      this.#policyFlag(field, on);
      return;
    }
    if (action === "settings-save-policy") {
      void this.#saveCounterPolicy();
      return;
    }
    if (action === "settings-tour-demo-on") {
      void this.#saveTourPolicy(true);
      return;
    }
    if (action === "settings-tour-demo-off") {
      void this.#saveTourPolicy(false);
      return;
    }
    if (action === "settings-save-tour-step") {
      void this.#saveTourStep();
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
  // assign was. The role configuration's kind and arrangement selects take
  // the same road, their answers mirrored into the editor's draft.
  readonly #onHostChange = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const field = target.getAttribute("data-field") ?? "";
    if (field.startsWith("pr-")) {
      // A file input's answer is the chosen file read whole: the text hands
      // off to the controller with its name, so the wire carries both.
      if (target instanceof HTMLInputElement && target.type === "file") {
        const file = target.files?.[0];
        if (file !== undefined) {
          const name = file.name;
          void file.text().then((text) => this.#pricing.inputFile(field.slice(3), text, name));
        }
        return;
      }
      // A checkbox rides as yes or no; a select's answer is the option's own
      // word, which the controller maps back to the id it stands for.
      let value = target instanceof HTMLInputElement || target instanceof HTMLSelectElement ? target.value : "";
      if (target instanceof HTMLInputElement && target.type === "checkbox") value = target.checked ? "yes" : "no";
      this.#pricing.change(field.slice(3), value);
      return;
    }
    const action = target.getAttribute("data-action") ?? "";
    if (action === "assign-role") {
      const id = target.getAttribute("data-user-id") ?? "";
      const roleName = target instanceof HTMLSelectElement ? target.value : "";
      const role = this.#staff.roles.find((r) => r.name === roleName);
      void this.#assignRole(id, role?.code ?? "");
      return;
    }
    const editor = this.#roleConfig.editor;
    if (editor !== null) {
      const value = target instanceof HTMLSelectElement ? target.value : "";
      if (action === "config-add-kind") this.#patchEditor({ addKind: value });
      if (action === "config-my-add-kind") this.#patchEditor({ myAddKind: value });
      if (action === "configws-arrangement") this.#patchEditor({ wsDraft: { ...editor.wsDraft, arrangement: value } });
    }
    // The counter policy's selects answer with their static wire values; the
    // walkthrough's chapter and step selects answer with the keys, and the
    // step's stored copy follows the pick.
    if (!(target instanceof HTMLSelectElement)) return;
    const value = target.value;
    const policySelects: Record<string, keyof PolicySection> = {
      "policy-line": "linePresentation",
      "policy-lookup": "lookupPresentation",
      "policy-arrangement": "arrangement",
      "policy-margin": "marginVisibility",
      "policy-balance": "balanceVisibility",
    };
    const policyKey = policySelects[action];
    if (policyKey !== undefined) {
      this.#patchSettingsPolicy({ [policyKey]: value } as Partial<PolicySection>);
      return;
    }
    if (action === "tour-chapter") {
      const chapter = this.#settings.tour.view?.chapters.find((c) => c.chapter_key === value);
      this.#patchSettingsTour({
        editChapter: value,
        editStep: chapter?.steps[0]?.step_key ?? "",
        editLine: chapter?.steps[0]?.line ?? "",
        editRoute: "",
        editAnchor: "",
      });
      return;
    }
    if (action === "tour-step") {
      const chapter = this.#settings.tour.view?.chapters.find((c) => c.chapter_key === this.#settings.tour.editChapter);
      const step = chapter?.steps.find((st: { step_key: string }) => st.step_key === value);
      this.#patchSettingsTour({ editStep: value, editLine: step?.line ?? "", editRoute: "", editAnchor: "" });
    }
  };

  // The audit filters', the pricing editors' and the role configuration's and
  // deployment settings' text and number fields mirror from input events,
  // never bound.
  readonly #onHostInput = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const field = target.getAttribute("data-field") ?? "";
    if (field.startsWith("audit-")) {
      const value = target instanceof HTMLInputElement || target instanceof HTMLSelectElement ? target.value : "";
      const filters = this.#audit.filters;
      if (field === "audit-entity-type") this.#audit = { ...this.#audit, filters: { ...filters, entity_type: value } };
      if (field === "audit-action") this.#audit = { ...this.#audit, filters: { ...filters, action: value } };
      if (field === "audit-actor-id") this.#audit = { ...this.#audit, filters: { ...filters, actor_id: value } };
      if (field === "audit-since") this.#audit = { ...this.#audit, filters: { ...filters, since: value } };
      return;
    }
    if (field.startsWith("pr-")) {
      const value =
        target instanceof HTMLInputElement || target instanceof HTMLSelectElement || target instanceof HTMLTextAreaElement
          ? target.value
          : "";
      this.#pricing.input(field.slice(3), value);
      return;
    }
    const editor = this.#roleConfig.editor;
    if (editor !== null) {
      const value = target instanceof HTMLInputElement ? target.value : "";
      if (field === "config-add-id") this.#patchEditor({ addId: value });
      if (field === "config-my-add-id") this.#patchEditor({ myAddId: value });
      if (field === "configws-split") {
        const split = Number(value);
        if (Number.isFinite(split)) this.#patchEditor({ wsDraft: { ...editor.wsDraft, split_pct: Math.trunc(split) } });
      }
    }
    // The deployment settings' text and number fields mirror the same way.
    const value = target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement ? target.value : "";
    if (field === "brand-name") this.#patchSettingsBrand({ displayName: value });
    if (field === "brand-color") this.#patchSettingsBrand({ primaryColor: value });
    if (field === "brand-footer") this.#patchSettingsBrand({ footerText: value });
    if (field === "policy-notes") this.#patchSettingsPolicy({ notes: value });
    if (field === "policy-chips") this.#patchSettingsPolicy({ chips: value });
    if (field === "policy-fee") {
      const fee = Number(value);
      if (Number.isFinite(fee)) this.#patchSettingsPolicy({ fee: Math.trunc(fee) });
    }
    if (field === "policy-split") {
      const split = Number(value);
      if (Number.isFinite(split)) this.#patchSettingsPolicy({ split: Math.trunc(split) });
    }
    if (field === "tour-line") this.#patchSettingsTour({ editLine: value });
    if (field === "tour-route") this.#patchSettingsTour({ editRoute: value });
    if (field === "tour-anchor") this.#patchSettingsTour({ editAnchor: value });
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
    // The role configuration editor's fields mirror the model the same way:
    // a fresh editor, a reload after a save, or a cleared add row empties
    // what is on the glass.
    const editor = this.#roleConfig.editor;
    if (editor !== null) {
      const words: Record<string, string> = {
        "config-add-kind": editor.addKind,
        "config-add-id": editor.addId,
        "config-my-add-kind": editor.myAddKind,
        "config-my-add-id": editor.myAddId,
        "configws-split": String(editor.wsDraft.split_pct),
        "configws-arrangement": editor.wsDraft.arrangement,
      };
      for (const [field, value] of Object.entries(words)) {
        const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement>(`[data-field="${field}"]`);
        if (el !== null && el.value !== value) el.value = value;
      }
      const flags: Record<string, boolean> = {
        primary_collapsed: editor.wsDraft.primary_collapsed,
        secondary_collapsed: editor.wsDraft.secondary_collapsed,
        cost_hidden: editor.wsDraft.cost_hidden,
      };
      for (const [field, on] of Object.entries(flags)) {
        const el = this.renderRoot.querySelector<HTMLInputElement>(`input[data-field="${field}"]`);
        if (el !== null && el.checked !== on) el.checked = on;
      }
    }
    // The deployment settings' fields mirror the model the same way: a
    // reload answers the read, never the person's stale draft, and the
    // counters stay whole words on the glass.
    const s = this.#settings;
    const settingsWords: Record<string, string> = {
      "brand-name": s.brand.displayName,
      "brand-color": s.brand.primaryColor,
      "brand-footer": s.brand.footerText,
      "policy-fee": String(s.policy.fee),
      "policy-split": String(s.policy.split),
      "policy-notes": s.policy.notes,
      "policy-chips": s.policy.chips,
      "policy-line": s.policy.linePresentation,
      "policy-lookup": s.policy.lookupPresentation,
      "policy-arrangement": s.policy.arrangement,
      "policy-margin": s.policy.marginVisibility,
      "policy-balance": s.policy.balanceVisibility,
      "tour-chapter": s.tour.editChapter,
      "tour-step": s.tour.editStep,
      "tour-line": s.tour.editLine,
      "tour-route": s.tour.editRoute,
      "tour-anchor": s.tour.editAnchor,
    };
    for (const [field, value] of Object.entries(settingsWords)) {
      const el = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[data-field="${field}"]`);
      if (el !== null && el.value !== value) el.value = value;
    }
    const policyFlags: Record<string, boolean> = {
      cod: s.policy.cod,
      quickChips: s.policy.quickChips,
    };
    for (const [field, on] of Object.entries(policyFlags)) {
      const el = this.renderRoot.querySelector<HTMLInputElement>(`input[data-field="${field}"]`);
      if (el !== null && el.checked !== on) el.checked = on;
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
      if (status === 404) {
        // The staff family is not served yet: that is the waits-for-sign-in
        // state, not a failed read and never the mux's raw words.
        this.#staff = { ...this.#staff, phase: "waits", branches: branchRows, error: "" };
      } else if (status === 403) {
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

  // -- the role configuration surface -----------------------------------------------------------------

  #patchRoleConfig(patch: Partial<RoleConfigModel>): void {
    this.#roleConfig = { ...this.#roleConfig, ...patch };
    this.requestUpdate();
  }

  #patchEditor(patch: Partial<ConfigEditor>): void {
    if (this.#roleConfig.editor === null) return;
    this.#patchRoleConfig({ editor: { ...this.#roleConfig.editor, ...patch } });
  }

  /**
   * The whole grid: one config read per surface and job shape, the read half
   * of the same admin act the writes are. The first refusal of a read is the
   * core's own sentence, kept whole; the roles catalog failing only empties
   * the catalog card, the way the branches read failing only costs the
   * register its chips.
   */
  async #loadRoleConfig(): Promise<void> {
    this.#roleConfig = { ...emptyRoleConfigModel(), phase: "loading" };
    this.requestUpdate();
    const cells = new Map<string, { config: CellConfig }>();
    let loaded = 0;
    let refusal = "";
    let fault = "";
    const rolesRead = this.#api.roles();
    await Promise.all(
      CONFIG_SURFACES.flatMap((s) =>
        JOB_SHAPES.map(async (shape) => {
          const result = await this.#configApi.surfaceConfig(s.id, shape.profile);
          loaded += 1;
          if (!result.ok) {
            if (result.status === 403) {
              if (refusal === "") refusal = configRefusalWords(result.error, result.status);
            } else if (fault === "") {
              fault = `The role grid did not load. ${errorWords(result.error)}`;
            }
            return;
          }
          cells.set(cellKey(s.id, shape.profile), { config: result.value });
          if (this.#roleConfig.phase === "loading") {
            this.#roleConfig = { ...this.#roleConfig, loaded, cells: new Map(cells) };
            this.requestUpdate();
          }
        }),
      ),
    );
    const roles = await rolesRead;
    if (refusal !== "") {
      this.#patchRoleConfig({ phase: "refused", words: refusal, loaded, roles: roles.ok ? roles.value : [] });
      return;
    }
    if (fault !== "") {
      this.#patchRoleConfig({ phase: "error", words: fault, loaded, roles: roles.ok ? roles.value : [] });
      return;
    }
    this.#patchRoleConfig({ phase: "ready", loaded, cells, roles: roles.ok ? roles.value : [] });
  }

  /** Open one cell's editor from the grid's own read; no second fetch. */
  #openConfigEditor(surface: string, profile: string): void {
    if (surface === "" || profile === "") return;
    const cell = this.#roleConfig.cells.get(cellKey(surface, profile));
    if (cell === undefined) return;
    this.#patchRoleConfig({ editor: emptyConfigEditor(surface, profile, cell.config) });
  }

  #configAddRow(mine: boolean): void {
    const editor = this.#roleConfig.editor;
    if (editor === null) return;
    const id = mine ? editor.myAddId : editor.addId;
    const kind = mine ? editor.myAddKind : editor.addKind;
    const draft = mine ? editor.myDraft : editor.draft;
    const row: ConfigItem = { kind, id: id.trim(), position: draft.length, visible: true };
    const rowFault = itemRowError(row);
    const setFault = itemSetError([...draft, row]);
    if (rowFault !== "" || setFault !== "") {
      this.#patchEditor({ error: rowFault !== "" ? rowFault : setFault });
      return;
    }
    this.#patchEditor({
      error: "",
      notice: "",
      draft: mine ? editor.draft : [...editor.draft, row],
      myDraft: mine ? [...editor.myDraft, row] : editor.myDraft,
      addId: mine ? editor.addId : "",
      myAddId: mine ? "" : editor.myAddId,
    });
  }

  #configToggleVisible(mine: boolean, index: number): void {
    this.#configMapDraft(mine, (rows) =>
      rows.map((it, i) => (i === index ? { ...it, visible: !it.visible } : it)),
    );
  }

  #configMoveRow(mine: boolean, index: number, delta: number): void {
    this.#configMapDraft(mine, (rows) => {
      const to = index + delta;
      if (to < 0 || to >= rows.length) return rows;
      const next = [...rows];
      const moved = { ...next[index], position: next[to].position };
      next[index] = { ...next[to], position: next[index].position };
      next[to] = moved;
      return next;
    });
  }

  #configRemoveRow(mine: boolean, index: number): void {
    this.#configMapDraft(mine, (rows) => rows.filter((_, i) => i !== index).map((it, i) => ({ ...it, position: i })));
  }

  #configMapDraft(mine: boolean, map: (rows: readonly ConfigItem[]) => readonly ConfigItem[]): void {
    const editor = this.#roleConfig.editor;
    if (editor === null) return;
    this.#patchEditor({
      draft: mine ? editor.draft : map(editor.draft),
      myDraft: mine ? map(editor.myDraft) : editor.myDraft,
      error: "",
      notice: "",
    });
  }

  /** Save the dealer's curation whole; the server's words are the refusal. */
  async #saveRoleItems(): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null || editor.busy) return;
    const setFault = itemSetError(editor.draft);
    const rowFault = editor.draft.map((it) => itemRowError(it)).find((f) => f !== "") ?? "";
    if (setFault !== "" || rowFault !== "") {
      this.#patchEditor({ error: rowFault !== "" ? rowFault : setFault });
      return;
    }
    this.#patchEditor({ busy: true, error: "", notice: "" });
    const result = await this.#configApi.putRoleItems(editor.surface, editor.profile, editor.draft);
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    await this.#refreshConfigCell("Saved. The shape's view on this surface follows your curation.");
  }

  /** Reset one kind: the reset IS a DELETE, the level beneath answers again. */
  async #resetRoleItems(kind: string): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null || editor.busy || kind === "") return;
    this.#patchEditor({ busy: true, error: "", notice: "" });
    const result = await this.#configApi.resetRoleItems(editor.surface, editor.profile, kind);
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    await this.#refreshConfigCell(`Reset. The ${kind}s on this surface answer to the level beneath the dealer again.`);
  }

  async #saveMyItems(): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null || editor.busy) return;
    this.#patchEditor({ busy: true, error: "", notice: "" });
    const result = await this.#configApi.putMyItems(editor.surface, editor.myDraft);
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    await this.#refreshConfigCell("Saved. This surface follows your own set.");
  }

  async #resetMyItems(): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null || editor.busy) return;
    this.#patchEditor({ busy: true, error: "", notice: "" });
    const result = await this.#configApi.resetMyItems(editor.surface);
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    await this.#refreshConfigCell("Reset. You follow the dealer's curation again.");
  }

  #configWsFlag(field: string, on: boolean): void {
    const editor = this.#roleConfig.editor;
    if (editor === null) return;
    if (field === "primary_collapsed") this.#patchEditor({ wsDraft: { ...editor.wsDraft, primary_collapsed: on } });
    if (field === "secondary_collapsed") this.#patchEditor({ wsDraft: { ...editor.wsDraft, secondary_collapsed: on } });
    if (field === "cost_hidden") this.#patchEditor({ wsDraft: { ...editor.wsDraft, cost_hidden: on } });
  }

  async #saveMyWorkspace(): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null || editor.busy) return;
    const splitFault = workspaceSplitError(editor.wsDraft.split_pct);
    if (splitFault !== "") {
      this.#patchEditor({ error: splitFault });
      return;
    }
    this.#patchEditor({ busy: true, error: "", notice: "" });
    const result = await this.#configApi.putMyWorkspace(editor.surface, {
      split_pct: editor.wsDraft.split_pct,
      arrangement: editor.wsDraft.arrangement,
      primary_collapsed: editor.wsDraft.primary_collapsed,
      secondary_collapsed: editor.wsDraft.secondary_collapsed,
      cost_hidden: editor.wsDraft.cost_hidden,
      open_ref: "",
      idempotency_key: newWorkspaceKey(),
    });
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    const ws = result.value;
    this.#patchEditor({
      busy: false,
      notice: "Saved. Your workspace on this surface follows your row.",
      wsDraft: {
        split_pct: ws.split_pct,
        arrangement: ws.arrangement,
        primary_collapsed: ws.primary_collapsed,
        secondary_collapsed: ws.secondary_collapsed,
        cost_hidden: ws.cost_hidden,
        source: ws.source,
      },
    });
  }

  async #resetMyWorkspace(): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null || editor.busy) return;
    this.#patchEditor({ busy: true, error: "", notice: "" });
    const result = await this.#configApi.resetMyWorkspace(editor.surface);
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    await this.#refreshConfigCell("Reset. Your workspace follows the dealer's defaults again.");
  }

  /**
   * One cell re-read after a write: the grid's cell and the open editor's
   * drafts both answer the read, never the write.
   */
  async #refreshConfigCell(notice: string): Promise<void> {
    const editor = this.#roleConfig.editor;
    if (editor === null) return;
    const result = await this.#configApi.surfaceConfig(editor.surface, editor.profile);
    if (!result.ok) {
      this.#patchEditor({ busy: false, error: configRefusalWords(result.error, result.status) });
      return;
    }
    const config = result.value;
    const cells = new Map(this.#roleConfig.cells);
    cells.set(cellKey(editor.surface, editor.profile), { config });
    this.#roleConfig = {
      ...this.#roleConfig,
      cells,
      editor: {
        ...editor,
        busy: false,
        notice,
        draft: config.role_items.map((it) => ({ ...it })),
        myDraft: config.user_items.map((it) => ({ ...it })),
        wsDraft: {
          split_pct: config.workspace.split_pct,
          arrangement: config.workspace.arrangement,
          primary_collapsed: config.workspace.primary_collapsed,
          secondary_collapsed: config.workspace.secondary_collapsed,
          cost_hidden: config.workspace.cost_hidden,
          source: config.workspace.source,
        },
      },
    };
    this.requestUpdate();
  }



  // -- the deployment settings surface ------------------------------------------------------------------

  #patchSettings(patch: Partial<SettingsModel>): void {
    this.#settings = { ...this.#settings, ...patch };
    this.requestUpdate();
  }

  #patchSettingsBrand(patch: Partial<BrandSection>): void {
    this.#patchSettings({ brand: { ...this.#settings.brand, ...patch } });
  }

  #patchSettingsPayments(patch: Partial<PaymentsSection>): void {
    this.#patchSettings({ payments: { ...this.#settings.payments, ...patch } });
  }

  #patchSettingsPolicy(patch: Partial<PolicySection>): void {
    this.#patchSettings({ policy: { ...this.#settings.policy, ...patch } });
  }

  #patchSettingsTour(patch: Partial<TourSection>): void {
    this.#patchSettings({ tour: { ...this.#settings.tour, ...patch } });
  }

  /**
   * The four sections' reads, in parallel. The brand pair is the parity
   * ledger's own hold: the core answers the mux's bare 404, and that is the
   * waits state, not a failed read. A refused history read costs only the
   * history card its rows; the policy form stands.
   */
  async #loadSettings(): Promise<void> {
    this.#settings = emptySettingsModel();
    this.requestUpdate();
    const brandRead = this.#settingsApi.brand(DEMO_ORG);
    const paymentsRead = this.#settingsApi.paymentsConfig();
    const policyRead = this.#settingsApi.counterPolicy();
    const historyRead = this.#settingsApi.counterPolicyHistory();
    const tourRead = this.#settingsApi.tour();

    const brand = await brandRead;
    if (!brand.ok) {
      if (brand.status === 404) {
        this.#patchSettingsBrand({ phase: "waits" });
      } else {
        this.#patchSettingsBrand({ phase: "error", error: `The dealer's brand did not load. ${errorWords(brand.error)}` });
      }
    } else {
      this.#patchSettingsBrand({
        phase: "ready",
        displayName: brand.value.display_name,
        footerText: brand.value.footer_text,
        primaryColor: brand.value.primary_color ?? "",
      });
    }

    const payments = await paymentsRead;
    if (!payments.ok) {
      this.#patchSettingsPayments({ phase: "error", error: `The card rail's configuration did not load. ${errorWords(payments.error)}` });
    } else {
      this.#patchSettingsPayments({
        phase: "ready",
        status: payments.value,
      });
    }

    const policy = await policyRead;
    if (!policy.ok) {
      this.#patchSettingsPolicy({
        phase: policy.status === 403 ? "refused" : "error",
        words: counterPolicyRefusalWords(policy.error, policy.status),
      });
    } else {
      const p = policy.value;
      this.#patchSettingsPolicy({
        phase: "ready",
        fee: p.restocking_fee_bps,
        notes: p.notes,
        chips: p.quick_chip_set.join("\n"),
        split: p.workspace_split_pct,
        cod: p.cod_enabled,
        quickChips: p.quick_chips_enabled,
        linePresentation: p.line_presentation,
        lookupPresentation: p.lookup_presentation,
        arrangement: p.workspace_arrangement,
        marginVisibility: p.margin_visibility,
        balanceVisibility: p.balance_visibility,
        supplierDepositCancel: p.supplier_deposit_cancel,
      });
    }

    const history = await historyRead;
    if (!history.ok) {
      this.#patchSettingsPolicy({
        historyPhase: history.status === 403 ? "refused" : "error",
        historyWords: policyHistoryRefusalWords(history.error, history.status),
      });
    } else {
      this.#patchSettingsPolicy({ historyPhase: "ready", history: history.value });
    }

    const tour = await tourRead;
    if (!tour.ok) {
      this.#patchSettingsTour({ phase: "error", words: `The walkthrough did not load. ${errorWords(tour.error)}` });
    } else {
      this.#patchSettingsTour({
        phase: "ready",
        view: tour.value,
        editChapter: tour.value.chapters[0]?.chapter_key ?? "",
        editStep: tour.value.chapters[0]?.steps[0]?.step_key ?? "",
        editLine: tour.value.chapters[0]?.steps[0]?.line ?? "",
      });
    }
  }

  async #saveBrand(): Promise<void> {
    const s = this.#settings.brand;
    if (s.phase !== "ready" || s.busy) return;
    this.#patchSettingsBrand({ busy: true, notice: "", error: "" });
    const result = await this.#settingsApi.putBrand(DEMO_ORG, {
      tenant_id: DEMO_ORG,
      display_name: s.displayName,
      logo_attachment_id: null,
      primary_color: s.primaryColor === "" ? null : s.primaryColor,
      footer_text: s.footerText,
    });
    if (!result.ok) {
      this.#patchSettingsBrand({ busy: false, error: `The brand did not save. ${errorWords(result.error)}` });
      return;
    }
    this.#patchSettingsBrand({ busy: false, notice: "Saved. The dealer's brand follows your words." });
  }

  #policyFlag(field: string, on: boolean): void {
    if (field === "cod") this.#patchSettingsPolicy({ cod: on });
    if (field === "quickChips") this.#patchSettingsPolicy({ quickChips: on });
  }

  /** One counter policy fault to refuse on, the server's own bounds in words. */
  #policyFault(): string {
    const s = this.#settings.policy;
    return restockingFeeError(s.fee) || workspaceSplitError(s.split) || chipSetError(chipsOf(s.chips));
  }

  async #saveCounterPolicy(): Promise<void> {
    const s = this.#settings.policy;
    if (s.phase !== "ready" || s.busy) return;
    const fault = this.#policyFault();
    if (fault !== "") {
      this.#patchSettingsPolicy({ error: fault });
      return;
    }
    this.#patchSettingsPolicy({ busy: true, notice: "", error: "" });
    // A policy write is a policy write: it goes to the server now and never
    // queues.
    const result = await this.#settingsApi.putCounterPolicy({
      branch_id: "",
      cod_enabled: s.cod,
      restocking_fee_bps: s.fee,
      notes: s.notes,
      line_presentation: s.linePresentation,
      quick_chips_enabled: s.quickChips,
      workspace_split_pct: s.split,
      workspace_arrangement: s.arrangement,
      lookup_presentation: s.lookupPresentation,
      margin_visibility: s.marginVisibility,
      balance_visibility: s.balanceVisibility,
      supplier_deposit_cancel: s.supplierDepositCancel,
      quick_chip_set: chipsOf(s.chips),
    });
    if (!result.ok) {
      this.#patchSettingsPolicy({ busy: false, error: counterPolicyRefusalWords(errorWords(result.error), result.status) });
      return;
    }
    await this.#refreshSettingsPolicy("Saved. The counter follows the dealer's policy.");
  }

  /** The policy and its history re-read after a write: the read answers. */
  async #refreshSettingsPolicy(notice: string): Promise<void> {
    const [policy, history] = await Promise.all([this.#settingsApi.counterPolicy(), this.#settingsApi.counterPolicyHistory()]);
    const s = this.#settings.policy;
    let next: PolicySection = { ...s, busy: false, notice };
    if (policy.ok) {
      const p = policy.value;
      next = {
        ...next,
        fee: p.restocking_fee_bps,
        notes: p.notes,
        chips: p.quick_chip_set.join("\n"),
        split: p.workspace_split_pct,
        cod: p.cod_enabled,
        quickChips: p.quick_chips_enabled,
        linePresentation: p.line_presentation,
        lookupPresentation: p.lookup_presentation,
        arrangement: p.workspace_arrangement,
        marginVisibility: p.margin_visibility,
        balanceVisibility: p.balance_visibility,
        supplierDepositCancel: p.supplier_deposit_cancel,
      };
    } else {
      next = { ...next, error: counterPolicyRefusalWords(errorWords(policy.error), policy.status) };
    }
    if (history.ok) {
      next = { ...next, historyPhase: "ready", history: history.value };
    } else {
      next = {
        ...next,
        historyPhase: history.status === 403 ? "refused" : "error",
        historyWords: policyHistoryRefusalWords(history.error, history.status),
      };
    }
    this.#patchSettingsPolicy(next);
  }

  async #saveTourPolicy(demoMode: boolean): Promise<void> {
    const s = this.#settings.tour;
    if (s.phase !== "ready" || s.busy) return;
    this.#patchSettingsTour({ busy: true, notice: "", error: "" });
    const result = await this.#settingsApi.putTourPolicy("", demoMode);
    if (!result.ok) {
      // The platform gate's own sentence, whole.
      this.#patchSettingsTour({ busy: false, error: tourPlatformWords(result.error) });
      return;
    }
    const fresh = await this.#settingsApi.tour();
    this.#patchSettingsTour({
      busy: false,
      notice: demoMode
        ? "Saved. The demo switch is on: the walkthrough shows where the dealer's switch allows it."
        : "Saved. The demo switch is off: the walkthrough waits.",
      view: fresh.ok ? fresh.value : s.view,
    });
  }

  async #saveTourStep(): Promise<void> {
    const s = this.#settings.tour;
    if (s.phase !== "ready" || s.busy) return;
    if (s.editChapter === "" || s.editStep === "") {
      this.#patchSettingsTour({ error: "Pick a chapter and a step first." });
      return;
    }
    const lineError = tourLineError(s.editLine);
    if (lineError !== "") {
      this.#patchSettingsTour({ error: lineError });
      return;
    }
    this.#patchSettingsTour({ busy: true, notice: "", error: "" });
    const result = await this.#settingsApi.putTourStepCopy({
      chapter_key: s.editChapter,
      step_key: s.editStep,
      line: s.editLine,
      route: s.editRoute,
      anchor: s.editAnchor,
    });
    if (!result.ok) {
      // The platform gate's own sentence, whole.
      this.#patchSettingsTour({ busy: false, error: tourPlatformWords(result.error) });
      return;
    }
    const fresh = await this.#settingsApi.tour();
    this.#patchSettingsTour({ busy: false, notice: "Saved. The step says your line.", view: fresh.ok ? fresh.value : s.view });
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
          error: auditRefusalWords(errorWords(result.error), result.status),
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
    this.#roleConfig = emptyRoleConfigModel();
    this.#settings = emptySettingsModel();
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
    if (this.#surface === "role-config") return renderRoleConfig(this.#roleConfig);
    if (this.#surface === "deployment") return renderSettings(this.#settings);
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
                <span class="what">Staff, the role configuration, the deployment's settings, and the audit trail.</span>
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
