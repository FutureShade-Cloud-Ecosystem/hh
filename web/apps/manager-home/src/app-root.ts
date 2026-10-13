// Manager Home's root element: the chrome, the way in, and the landing the
// caller's dashboard profile assembles. One app for every manager role
// (decision 5): the needs-you queue and the widget bento come from the core's
// per-role profile rows, never from code per role, so the yard manager's
// landing and any later manager's landing are the same code reading different
// rows.
//
// The way in is the demo's role cards (the shared kit's entry, the demo
// adapter behind the auth package's AccountSurface seam). After the mint the
// branch resolves once through the fenced branches route, because the yard
// enumeration routes name a branch the caller must reach; the ports are wired
// with the caller's headers, and the work area renders.
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
import { html, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
// hos-app-header: the shared chrome element (decision log row 938).
import "@hardscapehouse/design-system/src/header.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { ManagerApi, type BranchRef, type QueueEntry, type ReceivingRow, type WidgetDatum } from "./manager-api.js";
import {
  APPROVAL_KINDS,
  RECEIVING_DIFFERENCE_WORDS,
  SHORTAGE_RESOLVER_WORDS,
  approvalsRefusalWords,
  canResolve,
  decideRefusalWords,
  errorWords,
  resolutionText,
  resolveRefusalWords,
  type ViewKind,
} from "./manager-logic.js";
import { homeStyles } from "./styles.js";
import { reportingStyles } from "./reporting-styles.js";
import {
  renderApprovals,
  renderEntry,
  renderLanding,
  renderReceiving,
  renderShortages,
  type ApprovalsModel,
  type EntryView,
  type LandingModel,
  type ReceivingModel,
  type ShortagesModel,
} from "./views.js";
import { ReportingApi, openReportDocument, saveCsv, type RunView } from "./reporting-api.js";
import {
  BASIS_CHOICES,
  canAdminCommissionRules,
  canOpenReporting,
  commissionRuleRefusalWords,
  parseRateToBps,
  csvName,
  defaultParams,
  missingRequired,
  orderCategories,
  reportingRefusalWords,
  runParams,
} from "./reporting-logic.js";
import { emptyReporting, renderReporting, type CommissionRegister, type ReportingModel } from "./reporting-views.js";

/** The template sheet's one guard: the required parameters left empty, in the labels the form shows. */
function missingRequiredWords(params: readonly { name: string; required: boolean; label: string }[], values: Record<string, string>): string {
  const missing = missingRequired(params, values);
  return missing.length === 0 ? "" : `Fill in: ${missing.join(", ")}`;
}

/** The basis a person picked in the form's own words, read back to the wire's closed pair. */
function basisFromWord(word: string): string {
  return BASIS_CHOICES.find((b) => b.word === word)?.value ?? BASIS_CHOICES[0].value;
}

const DEMO_ORG = "hh-demo";

export class ManagerHomeApp extends HHElement {
  static styles = [baseStyles, homeStyles, reportingStyles];

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

  // The manager's calls, over the staff client.
  readonly #api = new ManagerApi(this.#staffClient);

  // The reporting panes' calls, over the staff client too.
  readonly #reportingApi = new ReportingApi(this.#staffClient);

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

  // The tier and scope the mint's resolver stamped, the same facts the core's
  // guards read: they decide only whether the landing draws the reporting
  // door and whether the rules pane draws its write controls. The core still
  // answers every request itself.
  #tier = 0;
  #scope = "";

  // The theme: light by default, brand (the dark half) one tap away. Held in
  // memory only: nothing here persists.
  #theme: "light" | "dark" = "light";

  // Which screen shows. Home is the bento; the other three are the views the
  // needs links name.
  #view: ViewKind = "home";

  // The branch this login works, resolved once through the fenced branches
  // route. The yard enumeration routes name it; the needs and widgets rides
  // need nothing (the server applies the caller's grants itself).
  #branch: BranchRef | null = null;

  // Every branch the caller reaches: the reporting selects show their names
  // and the reporting actions resolve a picked name back to its id.
  #branchList: readonly BranchRef[] = [];

  // The landing's state.
  #landing: LandingModel = { phase: "loading", profile: "", source: "", roleLabel: "", widgets: [], data: new Map(), needs: null, error: "", canOpenReporting: false };

  // The views' states.
  #shortages: ShortagesModel = {
    phase: "loading", branchName: "", entries: [], resolving: null, error: "", notice: "",
    resolverWords: SHORTAGE_RESOLVER_WORDS,
  };
  #receiving: ReceivingModel = { phase: "loading", branchName: "", rows: [], error: "", definitionWords: RECEIVING_DIFFERENCE_WORDS };
  #approvals: ApprovalsModel = { phase: "loading", kind: APPROVAL_KINDS[0].kind, rows: [], error: "", refusal: "", busyId: "" };

  // The reporting view's state: the four panes (templates, runs, commission
  // rules, accruals) and every call they make.
  #reporting: ReportingModel = emptyReporting();

  // The way in: this app's own card, drawn by the shared entry.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

  constructor() {
    super();
    this.#demoAccount = new DemoAccount({ client: this.#demoClient });
    this.#auth = new AuthController({ account: this.#demoAccount, sessions: this.#sessions });
  }

  // The entry cards and every control ride data-action names; one host
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
    if (action === "reload") {
      void this.#loadLanding();
      return;
    }
    if (action === "open-view") {
      const view = control?.getAttribute("data-view");
      if (view === "shortages" || view === "receiving" || view === "approvals" || view === "reporting") {
        this.#openView(view);
      }
      return;
    }
    if (action === "back") {
      this.#openView("home");
      return;
    }
    if (action === "reload-view") {
      if (this.#view === "shortages") void this.#loadShortages();
      if (this.#view === "receiving") void this.#loadReceiving();
      if (this.#view === "approvals") void this.#loadApprovals();
      return;
    }
    if (action !== null && action !== undefined && action.startsWith("reporting-")) {
      this.#reportingAction(action, control);
      return;
    }
    if (action === "open-resolve") {
      this.#openResolve(control?.getAttribute("data-task-id") ?? "", control?.getAttribute("data-shortage-id") ?? "");
      return;
    }
    if (action === "pick-resolution") {
      if (this.#shortages.resolving !== null) {
        this.#shortages = {
          ...this.#shortages,
          resolving: { ...this.#shortages.resolving, choice: control?.getAttribute("data-choice") ?? "" },
        };
        this.requestUpdate();
      }
      return;
    }
    if (action === "cancel-resolve") {
      if (this.#shortages.resolving !== null && !this.#shortages.resolving.busy) {
        this.#shortages = { ...this.#shortages, resolving: null };
        this.requestUpdate();
      }
      return;
    }
    if (action === "confirm-resolve") {
      void this.#confirmResolve();
      return;
    }
    if (action === "pick-kind") {
      const kind = control?.getAttribute("data-kind") ?? "";
      if (kind !== this.#approvals.kind && APPROVAL_KINDS.some((k) => k.kind === kind)) {
        this.#approvals = { ...this.#approvals, kind, rows: [], refusal: "", error: "", phase: "loading" };
        this.requestUpdate();
        void this.#loadApprovals();
      }
      return;
    }
    if (action === "decide") {
      void this.#decide(control?.getAttribute("data-approval-id") ?? "", control?.getAttribute("data-decision") === "approve");
      return;
    }
    const flow = control?.getAttribute("data-flow");
    if (action === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
  };

  // The resolve note field: mirrored from input events, never bound.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (target instanceof HTMLTextAreaElement && target.getAttribute("data-field") === "resolution-note") {
      if (this.#shortages.resolving !== null) {
        this.#shortages = {
          ...this.#shortages,
          resolving: { ...this.#shortages.resolving, note: target.value },
        };
      }
      return;
    }
    if (target instanceof HTMLInputElement || target instanceof HTMLSelectElement) {
      this.#reportingField(target);
    }
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onHostClick);
    // input does not leave this element's own shadow root, so the form
    // listener sits on the root itself.
    this.renderRoot.addEventListener("input", this.#onField);
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
        this.#tier = handoff.session.tier;
        this.#scope = handoff.session.scope;
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
    this.renderRoot.removeEventListener("input", this.#onField);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render. The reporting form's fields
  // ride the same rule: the model's values are written onto the inputs here
  // when they differ, the way the money desk's field seam does it.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
    this.#syncReportingFields();
  }

  #syncReportingFields(): void {
    if (this.#view !== "reporting") return;
    const fields = this.renderRoot.querySelectorAll<HTMLInputElement | HTMLSelectElement>("input[data-param], select[data-param], input[data-field], select[data-field]");
    const active = (this.renderRoot as ShadowRoot).activeElement;
    for (const el of fields) {
      if (el === active) continue;
      const param = el.getAttribute("data-param");
      if (param !== null) {
        const value = this.#reporting.values[param] ?? (el instanceof HTMLSelectElement ? el.options[0]?.text ?? "" : "");
        if (el.value !== value) el.value = value;
        continue;
      }
      const field = el.getAttribute("data-field") ?? "";
      if (el instanceof HTMLInputElement && el.type === "checkbox") {
        const checked = field === "save-shared" ? this.#reporting.saveShared : false;
        if (el.checked !== checked) el.checked = checked;
        continue;
      }
      const value = this.#reportingFieldValue(field);
      if (value !== null && el.value !== value) el.value = value;
    }
  }

  #reportingFieldValue(field: string): string | null {
    const r = this.#reporting;
    switch (field) {
      case "print-at":
        return r.printAt === "" ? "Not picked" : r.printAt;
      case "save-name":
        return r.saveName;
      case "rule-rep":
        return r.ruleDraft.repUserId;
      case "rule-basis":
        return r.ruleDraft.basis;
      case "rule-rate":
        return r.ruleDraft.ratePercent;
      case "rule-from":
        return r.ruleDraft.effectiveFrom;
      case "rule-to":
        return r.ruleDraft.effectiveTo;
      case "rule-branch":
        return r.ruleDraft.branchName === "" ? "Every branch" : r.ruleDraft.branchName;
      case "rule-note":
        return r.ruleDraft.note;
      case "commission-from":
        return r.commissionFrom;
      case "commission-to":
        return r.commissionTo;
      case "commission-branch":
        return r.commissionBranchName === "" ? "All branches" : r.commissionBranchName;
      default:
        return null;
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

  // The signed-in start: the branch once, then the landing.
  async #start(): Promise<void> {
    const branches = await this.#api.branches();
    if (branches.ok && branches.value.length > 0) {
      this.#branch = branches.value[0];
      this.#branchList = branches.value;
    } else {
      this.#branch = null;
      this.#branchList = [];
    }
    await this.#loadLanding();
  }

  async #chooseFlow(flow: string): Promise<void> {
    this.#entryView = { ...this.#entryView, busy: true };
    this.requestUpdate();
    const result = await (this.#demoAccount as DemoAccount).chooseFlow(flow);
    if (result.ok) {
      const adopted = this.#auth.adopt(result.value);
      if (adopted) {
        this.#roleLabel = result.value.identity.roleName;
        this.#tier = result.value.identity.tier;
        this.#scope = result.value.identity.scope;
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
    this.#view = "home";
    this.#branch = null;
    this.#landing = { phase: "loading", profile: "", source: "", roleLabel: "", widgets: [], data: new Map(), needs: null, error: "", canOpenReporting: false };
    this.#shortages = {
      phase: "loading", branchName: "", entries: [], resolving: null, error: "", notice: "",
      resolverWords: SHORTAGE_RESOLVER_WORDS,
    };
    this.#receiving = { phase: "loading", branchName: "", rows: [], error: "", definitionWords: RECEIVING_DIFFERENCE_WORDS };
    this.#approvals = { phase: "loading", kind: APPROVAL_KINDS[0].kind, rows: [], error: "", refusal: "", busyId: "" };
    this.#reporting = emptyReporting();
    this.#tier = 0;
    this.#scope = "";
    this.#branchList = [];
    clearFrontDoorHandoff();
    void this.loadFlows();
  }

  #branchName(): string {
    return this.#branch?.name ?? "";
  }

  // The landing: the profile's widgets in the profile's order, their numbers,
  // and the needs-you queue. One failure words the error; the needs ride the
  // widget whose measure is needs.you.
  async #loadLanding(): Promise<void> {
    this.#landing = { ...this.#landing, phase: "loading", error: "" };
    this.requestUpdate();
    const [needs, widgets] = await Promise.all([this.#api.needs(), this.#api.widgets()]);
    if (!needs.ok || !widgets.ok) {
      const failureError = !needs.ok ? needs.error : !widgets.ok ? widgets.error : "";
      this.#landing = {
        ...this.#landing,
        phase: "error",
        error: `Your home did not load. ${errorWords(failureError)}`,
      };
      this.requestUpdate();
      return;
    }
    const keys = widgets.value.widgets.filter((w) => w.visible).map((w) => w.widget_key);
    const data = new Map<string, WidgetDatum>();
    if (keys.length > 0) {
      const measured = await this.#api.widgetData(keys);
      if (measured.ok) {
        for (const datum of measured.value) data.set(datum.key, datum);
      }
    }
    this.#landing = {
      phase: "ready",
      profile: widgets.value.profile,
      source: widgets.value.source,
      roleLabel: this.#roleLabel,
      widgets: widgets.value.widgets,
      data,
      needs: needs.value,
      error: "",
      canOpenReporting: canOpenReporting(this.#tier),
    };
    this.requestUpdate();
  }

  // A landing refresh that keeps the screen standing: the server's answers
  // win, a failed re-read keeps what is on the glass.
  async #refreshLanding(): Promise<void> {
    const [needs, widgets] = await Promise.all([this.#api.needs(), this.#api.widgets()]);
    if (needs.ok) this.#landing = { ...this.#landing, needs: needs.value };
    if (widgets.ok) {
      const keys = widgets.value.widgets.filter((w) => w.visible).map((w) => w.widget_key);
      const data = new Map(this.#landing.data);
      if (keys.length > 0) {
        const measured = await this.#api.widgetData(keys);
        if (measured.ok) {
          for (const datum of measured.value) data.set(datum.key, datum);
        }
      }
      this.#landing = { ...this.#landing, widgets: widgets.value.widgets, data };
    }
    this.requestUpdate();
  }

  #openView(view: ViewKind): void {
    this.#view = view;
    this.#shortages = { ...this.#shortages, error: "", notice: "", resolving: null };
    this.requestUpdate();
    if (view === "shortages" && this.#shortages.phase === "loading") void this.#loadShortages();
    if (view === "receiving" && this.#receiving.phase === "loading") void this.#loadReceiving();
    if (view === "approvals" && this.#approvals.phase === "loading") void this.#loadApprovals();
    if (view === "reporting") void this.#openReporting();
  }

  // -- Reporting · the four panes -------------------------------------------------------------------

  // The view's way in: the caller's branches name the selects, the catalogue
  // loads once, the first template is selected, its saved views ride along.
  async #openReporting(): Promise<void> {
    this.#reporting = {
      ...emptyReporting(),
      branchNames: this.#branchList.map((b) => b.name),
      canWriteRules: canAdminCommissionRules(this.#tier, this.#scope),
    };
    this.requestUpdate();
    await this.#loadReportingCatalog();
  }

  #patchReporting(patch: Partial<ReportingModel>): void {
    this.#reporting = { ...this.#reporting, ...patch };
    this.requestUpdate();
  }

  async #loadReportingCatalog(): Promise<void> {
    this.#patchReporting({ catalogPhase: "loading", catalogError: "" });
    const result = await this.#reportingApi.templates();
    if (!result.ok) {
      this.#patchReporting({ catalogPhase: "error", catalogError: `The report catalogue did not load. ${reportingRefusalWords(result.error, result.status)}` });
      return;
    }
    const categories = orderCategories(result.value);
    const first = categories[0]?.templates[0];
    this.#patchReporting({ catalogPhase: "ready", categories, catalogError: "" });
    if (first !== undefined && this.#reporting.selectedCode === "") {
      await this.#selectTemplate(first.code);
    }
  }

  async #selectTemplate(code: string): Promise<void> {
    const result = await this.#reportingApi.template(code);
    if (!result.ok) {
      this.#patchReporting({ sheetError: `The report did not load. ${reportingRefusalWords(result.error, result.status)}` });
      return;
    }
    this.#patchReporting({
      selectedCode: code,
      template: result.value,
      values: defaultParams(result.value.params, new Date()),
      printAt: "",
      result: null,
      formError: "",
      sheetError: "",
      sheetNotice: "",
      saveName: "",
      saveShared: false,
      saveError: "",
    });
    await this.#loadSavedViews(code);
  }

  async #loadSavedViews(code: string): Promise<void> {
    this.#patchReporting({ savedPhase: "loading", savedError: "", savedViews: [] });
    const result = await this.#reportingApi.savedViews(code);
    if (!result.ok) {
      this.#patchReporting({ savedPhase: "error", savedError: `The saved views did not load. ${reportingRefusalWords(result.error, result.status)}` });
      return;
    }
    this.#patchReporting({ savedPhase: "ready", savedViews: result.value });
  }

  // The parameters a call sends: the displayed values with the branch pick
  // resolved back to the id the server names, empties dropped.
  #paramsForSend(): Record<string, string> {
    const values = { ...this.#reporting.values };
    const branchName = (values.branch_id ?? "").trim();
    if (branchName !== "") {
      const branch = this.#branchList.find((b) => b.name === branchName);
      values.branch_id = branch?.id ?? branchName;
    }
    return runParams(values);
  }

  #printBranchId(): string {
    // The report's own branch parameter wins; the "print at" pick answers
    // only when the effective parameters name no branch (the server refuses a
    // consolidated print with nowhere to queue the paper).
    const sent = this.#paramsForSend();
    if (sent.branch_id !== undefined) return "";
    const branch = this.#branchList.find((b) => b.name === this.#reporting.printAt);
    return branch?.id ?? "";
  }

  #reportingGuard(): string {
    const tmpl = this.#reporting.template;
    if (tmpl === null) return "";
    const missing = missingRequiredWords(tmpl.params, this.#reporting.values);
    return missing;
  }

  async #runReportingTemplate(): Promise<void> {
    if (this.#reporting.template === null || this.#reporting.busy) return;
    const guard = this.#reportingGuard();
    if (guard !== "") {
      this.#patchReporting({ formError: guard, sheetError: "" });
      return;
    }
    this.#patchReporting({ busy: true, formError: "", sheetError: "", sheetNotice: "" });
    const result = await this.#reportingApi.runTemplate(this.#reporting.selectedCode, this.#paramsForSend());
    if (result.ok) {
      this.#patchReporting({ busy: false, result: result.value });
    } else {
      this.#patchReporting({ busy: false, result: null, sheetError: reportingRefusalWords(result.error, result.status) });
    }
  }

  async #exportReportingTemplate(): Promise<void> {
    if (this.#reporting.template === null || this.#reporting.busy) return;
    const guard = this.#reportingGuard();
    if (guard !== "") {
      this.#patchReporting({ formError: guard, sheetError: "" });
      return;
    }
    this.#patchReporting({ busy: true, formError: "", sheetError: "", sheetNotice: "" });
    const result = await this.#reportingApi.exportTemplateCsv(this.#reporting.selectedCode, this.#paramsForSend());
    if (result.ok) {
      const name = csvName(this.#reporting.selectedCode, this.#paramsForSend());
      saveCsv(result.value, name);
      this.#patchReporting({ busy: false, sheetNotice: `Exported ${name}.` });
    } else {
      this.#patchReporting({ busy: false, sheetError: reportingRefusalWords(result.error, result.status) });
    }
  }

  async #printReportingTemplate(): Promise<void> {
    if (this.#reporting.template === null || this.#reporting.busy) return;
    const guard = this.#reportingGuard();
    if (guard !== "") {
      this.#patchReporting({ formError: guard, sheetError: "" });
      return;
    }
    this.#patchReporting({ busy: true, formError: "", sheetError: "", sheetNotice: "" });
    const result = await this.#reportingApi.printTemplate(this.#reporting.selectedCode, this.#paramsForSend(), this.#printBranchId());
    if (!result.ok) {
      this.#patchReporting({ busy: false, sheetError: reportingRefusalWords(result.error, result.status) });
      return;
    }
    await this.#openRunDocument(result.value, "sheet");
    await this.#loadRuns({ silent: true });
    this.#patchReporting({ busy: false });
  }

  // The frozen document opens at the paper. `target` says which sheet's
  // notice carries the outcome words: the template sheet or the runs pane.
  async #openRunDocument(run: RunView, target: "sheet" | "runs"): Promise<void> {
    const doc = await this.#reportingApi.runDocument(run.id);
    if (!doc.ok) {
      const words = reportingRefusalWords(doc.error, doc.status);
      if (target === "sheet") this.#patchReporting({ sheetError: words });
      else this.#patchReporting({ expandedDocPhase: "error", expandedDocError: words });
      return;
    }
    const outcome = openReportDocument(doc.value);
    const words = outcome.ok
      ? "Print job queued and the document opened at the paper."
      : "Print job queued: allow pop-ups to open the document at the paper.";
    if (target === "sheet") this.#patchReporting({ sheetNotice: words });
    else this.#patchReporting({ docNotice: words });
  }

  // -- Reporting · saved views ---------------------------------------------------------------------

  async #saveReportingView(): Promise<void> {
    if (this.#reporting.template === null || this.#reporting.saveBusy) return;
    const name = this.#reporting.saveName.trim();
    if (name === "") {
      this.#patchReporting({ saveError: "A saved view needs a name." });
      return;
    }
    this.#patchReporting({ saveBusy: true, saveError: "" });
    const result = await this.#reportingApi.saveSavedView(this.#reporting.selectedCode, name, runParams(this.#reporting.values), this.#reporting.saveShared);
    if (!result.ok) {
      this.#patchReporting({ saveBusy: false, saveError: reportingRefusalWords(result.error, result.status) });
      return;
    }
    this.#patchReporting({ saveBusy: false, saveName: "", saveShared: false, sheetNotice: `Saved view ${name}.` });
    await this.#loadSavedViews(this.#reporting.selectedCode);
  }

  #applyReportingView(id: string): void {
    const view = this.#reporting.savedViews.find((v) => v.id === id);
    if (view === undefined || this.#reporting.template === null) return;
    this.#patchReporting({
      values: { ...defaultParams(this.#reporting.template.params, new Date()), ...view.params },
      result: null,
      formError: "",
      sheetError: "",
      sheetNotice: "",
    });
  }

  async #deleteReportingView(id: string): Promise<void> {
    if (this.#reporting.viewBusyId !== "") return;
    this.#patchReporting({ viewBusyId: id, saveError: "" });
    const result = await this.#reportingApi.deleteSavedView(id);
    if (!result.ok) {
      this.#patchReporting({ viewBusyId: "", saveError: reportingRefusalWords(result.error, result.status) });
      return;
    }
    this.#patchReporting({ viewBusyId: "" });
    await this.#loadSavedViews(this.#reporting.selectedCode);
  }

  // -- Reporting · the runs pane ---------------------------------------------------------------------

  async #loadRuns(opts: { silent?: boolean } = {}): Promise<void> {
    if (!opts.silent) this.#patchReporting({ runsPhase: "loading", runsError: "" });
    const result = await this.#reportingApi.runs(25);
    if (!result.ok) {
      this.#patchReporting({ runsPhase: "error", runsError: reportingRefusalWords(result.error, result.status) });
      return;
    }
    this.#patchReporting({ runsPhase: "ready", runs: result.value });
  }

  async #expandReportingRun(id: string): Promise<void> {
    if (this.#reporting.expandedDocId === id) {
      this.#patchReporting({ expandedDocId: "", expandedDoc: null, expandedDocPhase: "ready", expandedDocError: "", docNotice: "" });
      return;
    }
    this.#patchReporting({ expandedDocId: id, expandedDocPhase: "loading", expandedDocError: "", docNotice: "" });
    const result = await this.#reportingApi.run(id);
    if (!result.ok) {
      this.#patchReporting({ expandedDocPhase: "error", expandedDocError: reportingRefusalWords(result.error, result.status) });
      return;
    }
    this.#patchReporting({ expandedDocPhase: "ready", expandedDoc: result.value });
  }

  // -- Reporting · the commission rules pane -------------------------------------------------------------

  async #loadCommissionRules(): Promise<void> {
    this.#patchReporting({ rulesPhase: "loading", rulesError: "", rulesRefusal: "" });
    const result = await this.#reportingApi.commissionRules();
    if (!result.ok) {
      const status = result.status;
      const refusal = status === 403 ? commissionRuleRefusalWords(result.error, status) : "";
      this.#patchReporting({
        rulesPhase: "error",
        rulesError: refusal !== "" ? "" : reportingRefusalWords(result.error, result.status),
        rulesRefusal: refusal,
      });
      return;
    }
    this.#patchReporting({ rulesPhase: "ready", rules: result.value });
  }

  #reportingField(el: HTMLInputElement | HTMLSelectElement): void {
    const r = this.#reporting;
    const param = el.getAttribute("data-param");
    if (param !== null) {
      this.#patchReporting({ values: { ...r.values, [param]: el.value } });
      return;
    }
    const field = el.getAttribute("data-field") ?? "";
    if (field === "print-at") this.#patchReporting({ printAt: el.value === "Not picked" ? "" : el.value });
    if (field === "save-name") this.#patchReporting({ saveName: el.value });
    if (field === "save-shared" && el instanceof HTMLInputElement) this.#patchReporting({ saveShared: el.checked });
    if (field === "rule-rep") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, repUserId: el.value } });
    if (field === "rule-rate") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, ratePercent: el.value } });
    if (field === "rule-basis") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, basis: basisFromWord(el.value) } });
    if (field === "rule-from") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, effectiveFrom: el.value } });
    if (field === "rule-to") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, effectiveTo: el.value } });
    if (field === "rule-branch") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, branchName: el.value === "Every branch" ? "" : el.value } });
    if (field === "rule-note") this.#patchReporting({ ruleDraft: { ...r.ruleDraft, note: el.value } });
    if (field === "commission-from") this.#patchReporting({ commissionFrom: el.value });
    if (field === "commission-to") this.#patchReporting({ commissionTo: el.value });
    if (field === "commission-branch") this.#patchReporting({ commissionBranchName: el.value === "All branches" ? "" : el.value });
  }

  async #createCommissionRule(): Promise<void> {
    if (!this.#reporting.canWriteRules || this.#reporting.ruleDraft.busy) return;
    const d = this.#reporting.ruleDraft;
    const rate = parseRateToBps(d.ratePercent);
    if ("error" in rate) {
      this.#patchReporting({ ruleDraft: { ...d, error: rate.error } });
      return;
    }
    if (d.effectiveFrom.trim() === "") {
      this.#patchReporting({ ruleDraft: { ...d, error: "The rule needs the day it takes effect." } });
      return;
    }
    const branch = this.#branchList.find((b) => b.name === d.branchName);
    this.#patchReporting({ ruleDraft: { ...d, busy: true, error: "" } });
    const result = await this.#reportingApi.createCommissionRule({
      repUserId: d.repUserId.trim(),
      basis: d.basis,
      rateBps: rate.bps,
      effectiveFrom: d.effectiveFrom.trim(),
      ...(d.effectiveTo.trim() !== "" ? { effectiveTo: d.effectiveTo.trim() } : {}),
      ...(branch !== undefined ? { branchId: branch.id } : {}),
      ...(d.note.trim() !== "" ? { note: d.note.trim() } : {}),
    });
    if (!result.ok) {
      this.#patchReporting({ ruleDraft: { ...this.#reporting.ruleDraft, busy: false, error: commissionRuleRefusalWords(result.error, result.status) } });
      return;
    }
    this.#patchReporting({ ruleDraft: { ...emptyReporting().ruleDraft, effectiveFrom: d.effectiveFrom } });
    await this.#loadCommissionRules();
  }

  async #retireCommissionRule(id: string): Promise<void> {
    if (!this.#reporting.canWriteRules || this.#reporting.retireBusyId !== "") return;
    this.#patchReporting({ retireBusyId: id });
    const result = await this.#reportingApi.retireCommissionRule(id);
    if (!result.ok) {
      this.#patchReporting({ retireBusyId: "", rulesRefusal: commissionRuleRefusalWords(result.error, result.status) });
      return;
    }
    this.#patchReporting({ retireBusyId: "" });
    await this.#loadCommissionRules();
  }

  // -- Reporting · the accruals pane (the commission register) ---------------------------------------------

  #commissionBranchId(): string {
    if (this.#reporting.commissionBranchName === "") return "";
    return this.#branchList.find((b) => b.name === this.#reporting.commissionBranchName)?.id ?? "";
  }

  async #previewCommission(): Promise<void> {
    const from = this.#reporting.commissionFrom;
    const to = this.#reporting.commissionTo;
    if (from.trim() === "" || to.trim() === "") {
      this.#patchReporting({ register: { ...this.#reporting.register, phase: "ready", ran: true, error: "The register needs the period's two dates.", periods: [] } });
      return;
    }
    this.#patchReporting({ register: { ...this.#reporting.register, phase: "loading", ran: true, error: "", notice: "", expandedRepId: "", expandedPeriod: null, expandedRunId: "", expandedRun: null } });
    const branchId = this.#commissionBranchId();
    const [preview, accrual, attribution] = await Promise.all([
      this.#reportingApi.commissionPreview({ from, to, ...(branchId !== "" ? { branchId } : {}) }),
      this.#reportingApi.accruals(from, to),
      this.#reportingApi.attributionCheck(from, to, branchId),
    ]);
    if (!preview.ok) {
      this.#patchReporting({
        register: {
          ...this.#reporting.register,
          phase: "error",
          error: reportingRefusalWords(preview.error, preview.status),
          periods: [],
          runsItems: [],
          runsPhase: "ready",
          accrual: null,
          accrualError: "",
          attribution: null,
          attributionError: "",
        },
      });
      return;
    }
    const runs = await this.#reportingApi.commissionRuns(from, to);
    this.#patchReporting({
      register: {
        ...this.#reporting.register,
        phase: "ready",
        periods: preview.value.items,
        postsToGl: preview.value.postsToGl,
        note: preview.value.note,
        accrual: accrual.ok ? accrual.value : null,
        accrualError: accrual.ok ? "" : reportingRefusalWords(accrual.error, accrual.status),
        attribution: attribution.ok ? attribution.value : null,
        attributionError: attribution.ok ? "" : reportingRefusalWords(attribution.error, attribution.status),
        runsPhase: runs.ok ? "ready" : "error",
        runsError: runs.ok ? "" : reportingRefusalWords(runs.error, runs.status),
        runsItems: runs.ok ? runs.value : [],
      },
    });
  }

  async #computeCommissionPeriod(repId: string): Promise<void> {
    const reg = this.#reporting.register;
    if (reg.computeBusyRep !== "") return;
    this.#patchReporting({ register: { ...reg, computeBusyRep: repId, notice: "", error: "" } });
    const result = await this.#reportingApi.computeCommissionRun(repId, this.#reporting.commissionFrom, this.#reporting.commissionTo, this.#commissionBranchId());
    if (!result.ok) {
      this.#patchReporting({ register: { ...this.#reporting.register, computeBusyRep: "", error: reportingRefusalWords(result.error, result.status) } });
      return;
    }
    const frozen = result.value;
    this.#patchReporting({
      register: {
        ...this.#reporting.register,
        computeBusyRep: "",
        notice: `Computed ${frozen.repName !== "" ? frozen.repName : frozen.repUserId}: the accrual register carries the figure.`,
        periods: this.#reporting.register.periods.map((p) => (p.repUserId === repId ? { ...p, runId: frozen.runId } : p)),
      },
    });
    await this.#previewCommissionRefresh();
  }

  // A register refresh that keeps the screen standing: the periods stay, the
  // stored runs and the accrual register re-read.
  async #previewCommissionRefresh(): Promise<void> {
    const from = this.#reporting.commissionFrom;
    const to = this.#reporting.commissionTo;
    const [runs, accrual] = await Promise.all([
      this.#reportingApi.commissionRuns(from, to),
      this.#reportingApi.accruals(from, to),
    ]);
    this.#patchReporting({
      register: {
        ...this.#reporting.register,
        runsPhase: runs.ok ? "ready" : "error",
        runsError: runs.ok ? "" : reportingRefusalWords(runs.error, runs.status),
        runsItems: runs.ok ? runs.value : this.#reporting.register.runsItems,
        accrual: accrual.ok ? accrual.value : null,
        accrualError: accrual.ok ? "" : reportingRefusalWords(accrual.error, accrual.status),
      },
    });
  }

  async #expandCommissionPeriod(repId: string): Promise<void> {
    const reg = this.#reporting.register;
    if (reg.expandedRepId === repId) {
      this.#patchReporting({ register: { ...reg, expandedRepId: "", expandedPeriod: null, expandedPhase: "ready", expandedError: "" } });
      return;
    }
    const period = reg.periods.find((p) => p.repUserId === repId);
    if (period === undefined) return;
    if (period.runId === "") {
      // A preview already carries its lines; the drill needs no round trip.
      this.#patchReporting({ register: { ...reg, expandedRepId: repId, expandedPhase: "ready", expandedPeriod: period, expandedError: "" } });
      return;
    }
    this.#patchReporting({ register: { ...reg, expandedRepId: repId, expandedPhase: "loading", expandedError: "" } });
    const result = await this.#reportingApi.commissionRun(period.runId);
    if (!result.ok) {
      this.#patchReporting({ register: { ...this.#reporting.register, expandedPhase: "error", expandedError: reportingRefusalWords(result.error, result.status) } });
      return;
    }
    this.#patchReporting({ register: { ...this.#reporting.register, expandedPhase: "ready", expandedPeriod: result.value } });
  }

  async #expandCommissionStoredRun(runId: string): Promise<void> {
    const reg = this.#reporting.register;
    if (reg.expandedRunId === runId) {
      this.#patchReporting({ register: { ...reg, expandedRunId: "", expandedRun: null, expandedRunPhase: "ready", expandedRunError: "" } });
      return;
    }
    this.#patchReporting({ register: { ...reg, expandedRunId: runId, expandedRunPhase: "loading", expandedRunError: "" } });
    const result = await this.#reportingApi.commissionRun(runId);
    if (!result.ok) {
      this.#patchReporting({ register: { ...this.#reporting.register, expandedRunPhase: "error", expandedRunError: reportingRefusalWords(result.error, result.status) } });
      return;
    }
    this.#patchReporting({ register: { ...this.#reporting.register, expandedRunPhase: "ready", expandedRun: result.value } });
  }

  async #exportCommissionStoredRun(runId: string): Promise<void> {
    const reg = this.#reporting.register;
    if (reg.runBusyId !== "") return;
    this.#patchReporting({ register: { ...reg, runBusyId: runId, error: "", notice: "" } });
    const result = await this.#reportingApi.exportCommissionRunCsv(runId);
    if (!result.ok) {
      this.#patchReporting({ register: { ...this.#reporting.register, runBusyId: "", error: reportingRefusalWords(result.error, result.status) } });
      return;
    }
    saveCsv(result.value, `commission-run-${runId}.csv`);
    this.#patchReporting({ register: { ...this.#reporting.register, runBusyId: "", notice: `Exported commission-run-${runId}.csv.` } });
  }

  async #printCommissionStoredRun(runId: string): Promise<void> {
    const reg = this.#reporting.register;
    if (reg.runBusyId !== "") return;
    this.#patchReporting({ register: { ...reg, runBusyId: runId, error: "", notice: "" } });
    const printed = await this.#reportingApi.printCommissionRun(runId, this.#printBranchId());
    if (!printed.ok) {
      this.#patchReporting({ register: { ...this.#reporting.register, runBusyId: "", error: reportingRefusalWords(printed.error, printed.status) } });
      return;
    }
    const doc = await this.#reportingApi.runDocument(printed.value.id);
    if (!doc.ok) {
      this.#patchReporting({ register: { ...this.#reporting.register, runBusyId: "", error: reportingRefusalWords(doc.error, doc.status) } });
      return;
    }
    const outcome = openReportDocument(doc.value);
    this.#patchReporting({
      register: {
        ...this.#reporting.register,
        runBusyId: "",
        notice: outcome.ok
          ? "Print job queued and the statement opened at the paper."
          : "Print job queued: allow pop-ups to open the statement.",
      },
    });
  }

  // The reporting actions' one dispatch: every data-action the panes carry.
  #reportingAction(action: string, control: Element | undefined): void {
    const attr = (name: string): string => control?.getAttribute(name) ?? "";
    switch (action) {
      case "reporting-pane": {
        const pane = attr("data-pane");
        this.#patchReporting({ pane: pane as ReportingModel["pane"] });
        if (pane === "runs" && this.#reporting.runsPhase === "loading") void this.#loadRuns();
        if (pane === "rules" && this.#reporting.rulesPhase === "loading") void this.#loadCommissionRules();
        return;
      }
      case "reporting-reload":
        if (attr("data-what") === "catalog") void this.#loadReportingCatalog();
        if (attr("data-what") === "runs") void this.#loadRuns();
        if (attr("data-what") === "rules") void this.#loadCommissionRules();
        return;
      case "reporting-pick":
        void this.#selectTemplate(attr("data-code"));
        return;
      case "reporting-run":
        void this.#runReportingTemplate();
        return;
      case "reporting-export":
        void this.#exportReportingTemplate();
        return;
      case "reporting-print":
        void this.#printReportingTemplate();
        return;
      case "reporting-save-view":
        void this.#saveReportingView();
        return;
      case "reporting-apply-view":
        this.#applyReportingView(attr("data-view-id"));
        return;
      case "reporting-delete-view":
        void this.#deleteReportingView(attr("data-view-id"));
        return;
      case "reporting-expand-run":
        void this.#expandReportingRun(attr("data-run-id"));
        return;
      case "reporting-open-doc": {
        const run = this.#reporting.expandedDoc;
        if (run !== null) void this.#openRunDocument(run, "runs");
        return;
      }
      case "reporting-rule-create":
        void this.#createCommissionRule();
        return;
      case "reporting-rule-retire":
        void this.#retireCommissionRule(attr("data-rule-id"));
        return;
      case "reporting-preview":
        void this.#previewCommission();
        return;
      case "reporting-compute":
        void this.#computeCommissionPeriod(attr("data-rep-id"));
        return;
      case "reporting-expand-period":
        void this.#expandCommissionPeriod(attr("data-rep-id"));
        return;
      case "reporting-expand-crun":
        void this.#expandCommissionStoredRun(attr("data-run-id"));
        return;
      case "reporting-crun-csv":
        void this.#exportCommissionStoredRun(attr("data-run-id"));
        return;
      case "reporting-crun-print":
        void this.#printCommissionStoredRun(attr("data-run-id"));
        return;
      default:
        return;
    }
  }

  // -- Shortages · open -----------------------------------------------------------------------------

  async #loadShortages(): Promise<void> {
    this.#shortages = { ...this.#shortages, phase: "loading", error: "", branchName: this.#branchName() };
    this.requestUpdate();
    const result = await this.#pickQueue();
    if (result.ok) {
      this.#shortages = { ...this.#shortages, phase: "ready", entries: result.value, error: "" };
    } else {
      this.#shortages = { ...this.#shortages, phase: "error", error: `The shortage list did not load. ${errorWords(result.error)}` };
    }
    this.requestUpdate();
  }

  // The yard board needs a branch the caller reaches. With no branch grant
  // the view says so in words; it never asks for another branch by name.
  async #pickQueue(): Promise<{ ok: true; value: readonly QueueEntry[] } | { ok: false; error: string }> {
    if (this.#branch === null) {
      return { ok: false, error: "Your login reaches no branch, so the yard board cannot be listed." };
    }
    const result = await this.#api.pickQueue(this.#branch.id);
    return result.ok ? { ok: true, value: result.value } : { ok: false, error: result.error };
  }

  #openResolve(taskId: string, shortageId: string): void {
    if (taskId === "" || shortageId === "") return;
    this.#shortages = {
      ...this.#shortages,
      error: "",
      resolving: { taskId, shortageId, choice: "", note: "", busy: false },
    };
    this.requestUpdate();
  }

  async #confirmResolve(): Promise<void> {
    const draft = this.#shortages.resolving;
    if (draft === null || draft.busy || !canResolve(draft.choice, draft.note)) return;
    this.#shortages = { ...this.#shortages, resolving: { ...draft, busy: true }, error: "" };
    this.requestUpdate();
    const result = await this.#api.resolveShortage(
      draft.taskId,
      draft.shortageId,
      resolutionText(draft.choice, draft.note),
    );
    if (result.ok) {
      this.#shortages = { ...this.#shortages, resolving: null };
      await this.#loadShortages();
      await this.#refreshLanding();
      const remaining = this.#landing.needs?.rows.find((r) => r.source_key === "tasks.open_shortages")?.count;
      this.#shortages = {
        ...this.#shortages,
        notice:
          remaining === undefined
            ? "Shortage cleared. Nothing is waiting on you in that queue now."
            : `Shortage cleared. ${remaining} still open.`,
      };
    } else {
      this.#shortages = {
        ...this.#shortages,
        resolving: { ...draft, busy: false },
        error: resolveRefusalWords(result.error, result.status),
      };
    }
    this.requestUpdate();
  }

  // -- Receiving · short ------------------------------------------------------------------------------

  async #loadReceiving(): Promise<void> {
    this.#receiving = { ...this.#receiving, phase: "loading", error: "", branchName: this.#branchName() };
    this.requestUpdate();
    let rows: readonly ReceivingRow[] = [];
    let error = "";
    if (this.#branch === null) {
      error = "Your login reaches no branch, so the receiving dock cannot be listed.";
    } else {
      const result = await this.#api.receivingWorklist(this.#branch.id);
      if (result.ok) {
        rows = result.value;
      } else {
        error = `The receiving dock did not load. ${errorWords(result.error)}`;
      }
    }
    this.#receiving = {
      ...this.#receiving,
      phase: error === "" ? "ready" : "error",
      rows,
      error,
    };
    this.requestUpdate();
  }

  // -- Approvals ----------------------------------------------------------------------------------------

  async #loadApprovals(): Promise<void> {
    this.#approvals = { ...this.#approvals, phase: "loading", error: "", refusal: "" };
    this.requestUpdate();
    const result = await this.#api.approvals(this.#approvals.kind);
    if (result.ok) {
      this.#approvals = { ...this.#approvals, phase: "ready", rows: result.value, error: "", refusal: "" };
    } else {
      // A kind this caller does not decide is the core's own words plus the
      // plain sentence naming who decides it; anything else is a load error.
      const refusal = result.status === 403 ? approvalsRefusalWords(this.#approvals.kind, result.error, result.status) : "";
      this.#approvals = {
        ...this.#approvals,
        phase: refusal !== "" ? "refused" : "error",
        rows: [],
        error: refusal !== "" ? "" : `The approvals list did not load. ${errorWords(result.error)}`,
        refusal,
      };
    }
    this.requestUpdate();
  }

  async #decide(id: string, approve: boolean): Promise<void> {
    if (id === "" || this.#approvals.busyId !== "") return;
    this.#approvals = { ...this.#approvals, busyId: id, error: "", refusal: "" };
    this.requestUpdate();
    const result = await this.#api.decideApproval(id, approve, "");
    if (result.ok) {
      await this.#loadApprovals();
      await this.#refreshLanding();
    } else {
      this.#approvals = {
        ...this.#approvals,
        error: decideRefusalWords(
          this.#approvals.rows.find((r) => r.id === id)?.policy_type ?? this.#approvals.kind,
          result.error,
          result.status,
        ),
      };
    }
    this.#approvals = { ...this.#approvals, busyId: "" };
    this.requestUpdate();
  }

  // The theme control rides the header's named slot; its word names the
  // theme a tap moves to.
  private renderThemeControl(): TemplateResult {
    const label = this.#theme === "light" ? "Brand theme" : "Light theme";
    return html`<button slot="theme" type="button" class="btn btn-secondary" data-action="theme">${label}</button>`;
  }

  private renderWork(): TemplateResult {
    if (this.#view === "shortages") return renderShortages(this.#shortages);
    if (this.#view === "receiving") return renderReceiving(this.#receiving);
    if (this.#view === "approvals") return renderApprovals(this.#approvals);
    if (this.#view === "reporting") return renderReporting(this.#reporting);
    return renderLanding(this.#landing);
  }

  private renderSignedIn(): TemplateResult {
    return html`
      <hos-app-header signed-in demo product-name="Home" product-sub="Manager Home · HH Work shell">${this.renderThemeControl()}</hos-app-header>
      <main>${this.renderWork()}</main>
    `;
  }

  render(): TemplateResult {
    const session = this.#sessions.current;
    if (session !== null) return this.renderSignedIn();
    return html`
      <hos-app-header demo product-name="Home" product-sub="Manager Home · HH Work shell">${this.renderThemeControl()}</hos-app-header>
      <main>
        ${renderEntry(this.#entryView)}
      </main>
    `;
  }
}

defineElement("manager-home-app", ManagerHomeApp);
