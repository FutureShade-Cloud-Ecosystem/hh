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
import { listenForHostSignOut } from "@hardscapehouse/design-system/src/framed.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { browserSignal, noSignalFetch } from "@hardscapehouse/work-offline/guard";
import { AuthController, authHeaderProvider, SessionStore, type AccountSurface } from "@hardscapehouse/auth";
import { DemoAccount } from "@hardscapehouse/pos-kit/src/ported/demo-account.js";
import { bootCustody, entryUnavailable, servedConsoleConfig, type AppStart } from "@hardscapehouse/pos-kit/src/ported/boot-custody.js";
import { readFrontDoorHandoff, clearFrontDoorHandoff } from "./demo-account.js";
import { groupDriverRows, idleDriverWords } from "./drivers-logic.js";
import { rowsForBoard } from "./drivers-api.js";
import { BuilderApi } from "./builder-api.js";
import { BuilderDesk } from "./builder-desk.js";
import { ManagerApi, type BranchRef, type NeedsQueue, type QueueEntry, type ReceivingRow, type WidgetDatum } from "./manager-api.js";
import { DriversApi } from "./drivers-api.js";
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
import { builderStyles } from "./builder-styles.js";
import { renderBuilder } from "./builder-views.js";
import { reportingStyles } from "./reporting-styles.js";
import { warrantyStyles } from "./warranty-styles.js";
import { quickStyles } from "./quick-styles.js";
import { approvalsStyles } from "./approvals-styles.js";
import { QuickApi, type QuickActionConfig } from "./quick-api.js";
import { WeightsApi } from "./weights-api.js";
import { hasWeightsWork, mergeWeights, unsourcedFor } from "./weights-logic.js";
import { publishIntent } from "./quick-intent.js";
import { waitingKinds } from "./approvals-logic.js";
import { isLiveHref } from "./landing-links.js";
import { actionHref, findAction } from "./quick-logic.js";
import { renderQuickAdmin, renderQuickBar, type QuickAdminModel, type QuickBarModel } from "./quick-views.js";
import { configRefusalWords } from "./quick-logic.js";
import { WarrantyApi } from "./warranty-api.js";
import { WarrantyDesk } from "./warranty-desk.js";
import { renderWarranty } from "./warranty-views.js";
import {
  renderApprovals,
  renderEntry,
  renderLanding,
  renderReceiving,
  renderShortages,
  type ApprovalsModel,
  type BranchMode,
  type EntryView,
  type LandingModel,
  type ReceivingModel,
  type ShortagesModel,
} from "./views.js";
import { renderDrivers, type DriversModel } from "./drivers-views.js";
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
  static styles = [baseStyles, homeStyles, builderStyles, reportingStyles, warrantyStyles, quickStyles, approvalsStyles];

  // The custody: holds the adopted demo mint in memory, nothing persisted.
  readonly #sessions = new SessionStore();

  // The demo client: no bearer token (demo routes are pre-auth by design).
  readonly #demoClient = new ApiClient({
    baseUrl: "",
    headers: async () => ({ "X-Org": DEMO_ORG }),
  });

  // The demo account surface: listFlows() and chooseFlow() over the core's
  // demo routes. It is the injected AccountSurface for the auth package.
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

  // The manager's calls, over the staff client.
  readonly #api = new ManagerApi(this.#staffClient);

  // The quick actions' calls (src/quick-*): the caller's resolved registry
  // rows, over the same staff client.
  readonly #quickApi = new QuickApi(this.#staffClient);
  readonly #weightsApi = new WeightsApi(this.#staffClient);

  // The Drivers view's calls, the dispatch reads, over the same staff client.
  readonly #driversApi = new DriversApi(this.#staffClient);
  // The builder desk: the arrange view's calls and edit state, in the one
  // plain class the arrange surface renders from. It re-renders the app
  // element through the same requestUpdate the other views use.
  readonly #desk = new BuilderDesk(
    { builder: new BuilderApi(this.#staffClient), mine: { branches: () => this.#api.branches() } },
    () => this.requestUpdate(),
  );
  // The reporting panes' calls, over the staff client too.
  readonly #reportingApi = new ReportingApi(this.#staffClient);
  // The warranty desk (src/warranty-*): its own door over the same staff
  // client, its fields read from this element's shadow root by name.
  readonly #warranty = new WarrantyDesk(new WarrantyApi(this.#staffClient), {
    repaint: () => this.requestUpdate(),
    field: (name) => this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[data-field="${name}"]`)?.value ?? "",
    index: (name) => this.renderRoot.querySelector<HTMLSelectElement>(`select[data-field="${name}"]`)?.selectedIndex ?? -1,
  });

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
  #landing: LandingModel = { phase: "loading", profile: "", source: "", roleLabel: "", widgets: [], data: new Map(), needs: null, error: "", canOpenReporting: false, unsourcedNeeds: [] };

  // The landing's quick-action bar: the caller's resolved registry rows.
  #quickBar: QuickBarModel = { phase: "loading", actions: [] };

  // The admin's quick-actions config: the draft rows are local until Save.
  #quickAdmin: QuickAdminModel = { phase: "loading", actions: [], profiles: [], error: "", refusal: "", savedFor: "", busyId: "" };

  // The views' states.
  #shortages: ShortagesModel = {
    phase: "loading", branchName: "", entries: [], resolving: null, error: "", notice: "",
    resolverWords: SHORTAGE_RESOLVER_WORDS, nowMs: 0,
  };
  #receiving: ReceivingModel = { phase: "loading", branchName: "", rows: [], error: "", definitionWords: RECEIVING_DIFFERENCE_WORDS };
  #approvals: ApprovalsModel = { phase: "loading", kind: APPROVAL_KINDS[0].kind, rows: [], error: "", refusal: "", busyId: "" };
  #drivers: DriversModel = {
    phase: "loading", branchName: "", groups: [], trucks: [], runCount: 0, idleWords: "", error: "", refused: "",
  };

  // The reporting view's state: the four panes (templates, runs, commission
  // rules, accruals) and every call they make.
  #reporting: ReportingModel = emptyReporting();

  // The way in: this app's own card, drawn by the shared entry.
  #entryView: EntryView = { flows: { kind: "loading" }, busy: false, message: null };

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
      if (view === "shortages" || view === "receiving" || view === "approvals" || view === "drivers" || view === "reporting" || view === "warranty" || view === "quick-actions") {
        this.#openView(view);
      }
      if (view === "builder") {
        this.#openBuilder();
      }
      return;
    }
    if (action === "builder") {
      if (control !== undefined) {
        this.#keepFocus(control);
        this.#desk.act(control);
      }
      return;
    }
    if (action === "open-app") {
      // A click carries only an address the closed table names (landing-links.ts).
      const href = control?.getAttribute("data-href") ?? "";
      if (isLiveHref(href)) window.location.assign(href);
      return;
    }
    if (action === "quick-run") {
      this.#runQuick(control?.getAttribute("data-action-id") ?? "");
      return;
    }
    if (action === "quick-toggle-show") {
      this.#patchQuick(control?.getAttribute("data-action-id") ?? "", (row) => ({ ...row, enabled: !row.enabled }));
      return;
    }
    if (action === "quick-toggle-shape") {
      const profile = control?.getAttribute("data-profile") ?? "";
      this.#patchQuick(control?.getAttribute("data-action-id") ?? "", (row) => ({
        ...row,
        profiles: { ...row.profiles, [profile]: !(row.profiles[profile] === true) },
      }));
      return;
    }
    if (action === "quick-save") {
      void this.#saveQuick(control?.getAttribute("data-action-id") ?? "");
      return;
    }
    if (action === "toggle-branch") {
      const mode = control?.getAttribute("data-mode");
      if (mode === "combined" || mode === "split") {
        this.#setBranchMode(mode);
      }
      return;
    }
    if (action === "qa-action") {
      this.#runQuickAction(control?.getAttribute("data-qa") ?? "");
      return;
    }
    if (action === "need-action") {
      this.#runNeedAction(
        control?.getAttribute("data-target-view") ?? "",
        control?.getAttribute("data-target-ref") ?? "",
      );
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
      if (this.#view === "quick-actions") void this.#loadQuickAdmin();
      if (this.#view === "drivers") void this.#loadDrivers();
      return;
    }
    if (action !== null && action !== undefined && action.startsWith("reporting-")) {
      this.#reportingAction(action, control);
      return;
    }
    // The warranty desk's own controls (every data-action beginning w-). Its
    // press answers false for anything that is not its own, so the landing's
    // actions below keep working.
    if (control !== undefined && this.#warranty.press(control)) return;
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
        this.#approvalsOpening += 1;
        this.#approvals = { ...this.#approvals, kind, rows: [], refusal: "", error: "", phase: "loading", selectedId: undefined, declineReason: "" };
        this.requestUpdate();
        void this.#loadApprovals();
      }
      return;
    }
    if (action === "select-approval") {
      const id = control?.getAttribute("data-approval-id") ?? "";
      if (id !== "" && id !== this.#approvals.selectedId) {
        this.#approvals = { ...this.#approvals, selectedId: id };
        this.requestUpdate();
      }
      return;
    }
    if (action === "approve-different-price" || action === "approve-smaller-limit") {
      const input = this.renderRoot.querySelector<HTMLInputElement>("input[data-field='decline-reason']");
      input?.focus();
      return;
    }
    if (action === "message-controller" || action === "open-journal-entry") {
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

  // The resolve note field and the builder's fields: mirrored from input
  // events, never bound. The warranty desk hears only the one field whose shape
  // it changes (the chase form's action pick); every other field is read at
  // press time.
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
    if (target instanceof HTMLInputElement && target.getAttribute("data-field") === "decline-reason") {
      this.#approvals = { ...this.#approvals, declineReason: target.value };
      return;
    }
    if (target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement) {
      const field = target.getAttribute("data-field");
      if (field !== null && field.startsWith("builder-")) this.#desk.field(field, target.value);
    }
    if (target instanceof HTMLInputElement || target instanceof HTMLSelectElement) {
      this.#reportingField(target);
    }
    if (target instanceof Element) {
      const field = target.getAttribute("data-field") ?? "";
      if (field.startsWith("w-")) this.#warranty.onField(field);
    }
  };

  /** Framed by the Work shell: the host asks this app to run its own sign out when the person switches role (item 8e); standalone, nothing listens. */
  #stopHostSignOut: (() => void) | null = null;

  connectedCallback(): void {
    super.connectedCallback();
    this.#stopHostSignOut = listenForHostSignOut(() => this.#onSignOut());
    this.addEventListener("click", this.#onHostClick);
    // input does not leave this element's own shadow root, so the form
    // listener sits on the root itself.
    this.renderRoot.addEventListener("input", this.#onField);
    if (this.#custody.mode === "console") {
      this.#custody.begin("manager-home", (outcome) => this.#onRecovered(outcome));
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

  // Console mode: this page recovered its own session, or could not. A role the matrix gives this
  // app lands on its screen; an unavailable page keeps the unavailable entry while the custody retries;
  // a session the front door must settle goes back there.
  #onRecovered(outcome: AppStart): void {
    if (outcome.kind === "ready") {
      this.#roleLabel = outcome.entry.roleLabel;
      this.#entryView = { flows: { kind: "ready", flows: [] }, busy: false, message: null };
      void this.#start();
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
    this.removeEventListener("click", this.#onHostClick);
    this.renderRoot.removeEventListener("input", this.#onField);
    super.disconnectedCallback();
  }

  // The role label is the one header input the position gate keeps out of a
  // template (only data- and aria- attributes take a bound value), so it is
  // set on the element here, after each render. The builder's two text fields
  // prefill the same way: their values live in the desk, mirrored back into
  // the inputs after each render, never while a person is typing in them. The
  // reporting form's fields ride the same rule: the model's values are written
  // onto the inputs here when they differ, the way the money desk's field seam
  // does it. The warranty
  // desk's seeds ride it too: a form's starting values and a photo's object
  // address are set on their elements after the render that drew them, never bound.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
    this.#restoreFocus();
    this.#prefill("builder-title", this.#desk.state.draft?.title ?? "");
    this.#prefill("builder-landing-unit", this.#desk.state.role?.home.landing_unit ?? "");
    this.#prefill("builder-landing-module", this.#desk.state.role?.home.landing_module ?? "");
    this.#syncReportingFields();
    this.#warranty.seedFields((name, value) => {
      const field = this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[data-field="${name}"]`);
      if (field !== null) field.value = value;
    });
    for (const img of Array.from(this.renderRoot.querySelectorAll<HTMLImageElement>("img[data-w-photo]"))) {
      const id = img.getAttribute("data-w-photo") ?? "";
      const url = this.#warranty.photoUrl(id);
      if (url !== undefined && img.getAttribute("src") !== url) img.setAttribute("src", url);
      const alt = this.#warranty.photoAlt(id);
      if (img.getAttribute("alt") !== alt) img.setAttribute("alt", alt);
    }
  }

  // A list control keeps its keyboard focus through a move: the lists reuse
  // their rows by index, so after a reorder the focused slot holds another
  // card, and a control that disabled at a limit drops focus to the page.
  // The clicked control is noted by its op and key, and refocused after the
  // next render; when it disabled, the card's other enabled control of the
  // same pair takes focus.
  #focusTarget: { op: string; key: string; entry: string } | null = null;

  #keepFocus(control: Element): void {
    const key = control.getAttribute("data-key");
    const entry = control.getAttribute("data-entry");
    const op = control.getAttribute("data-op");
    this.#focusTarget = op !== null && (key !== null || entry !== null) ? { op, key: key ?? "", entry: entry ?? "" } : null;
  }

  #restoreFocus(): void {
    const target = this.#focusTarget;
    if (target === null) return;
    this.#focusTarget = null;
    const owner = target.key !== "" ? `[data-key='${CSS.escape(target.key)}']` : `[data-entry='${CSS.escape(target.entry)}']`;
    const same = this.renderRoot.querySelector<HTMLButtonElement>(`button[data-op='${CSS.escape(target.op)}']${owner}`);
    if (same === null) return;
    if (!same.disabled) {
      same.focus();
      return;
    }
    const row = same.closest("[data-widget], [data-tab], [data-tile], [data-entry], .edit-row") ?? same.parentElement;
    const other = Array.from(row?.querySelectorAll<HTMLButtonElement>("button:not([disabled])") ?? []).find(
      (b) => b.getAttribute("data-op") === target.op,
    );
    other?.focus();
  }

  #prefill(field: string, want: string): void {
    const input = this.renderRoot.querySelector<HTMLInputElement>(`[data-field='${field}']`);
    if (input !== null && (this.renderRoot as ShadowRoot).activeElement !== input && input.value !== want) {
      input.value = want;
    }
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

  // The signed-in start: the branch once, then the landing. The warranty desk
  // hears the branch too, for the scope chip that names it.
  async #start(): Promise<void> {
    const branches = await this.#api.branches();
    if (branches.ok && branches.value.length > 0) {
      this.#branch = branches.value[0];
      this.#branchList = branches.value;
    } else {
      this.#branch = null;
      this.#branchList = [];
    }
    this.#warranty.setBranch(this.#branch?.id ?? "", this.#branch?.name ?? "");
    void this.#loadQuickBar();
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
        this.#entryView = { ...this.#entryView, busy: false, message: "Sign-in did not go through. Try the role again." };
      }
    } else {
      this.#entryView = { ...this.#entryView, busy: false, message: `Sign-in did not go through. ${errorWords(result.error)}` };
    }
    this.requestUpdate();
  }

  #onSignOut(): void {
    this.#sessions.clear();
    this.#view = "home";
    this.#desk.close();
    this.#branch = null;
    this.#warranty.reset();
    this.#landing = { phase: "loading", profile: "", source: "", roleLabel: "", widgets: [], data: new Map(), needs: null, error: "", canOpenReporting: false, unsourcedNeeds: [] };
    this.#shortages = {
      phase: "loading", branchName: "", entries: [], resolving: null, error: "", notice: "",
      resolverWords: SHORTAGE_RESOLVER_WORDS, nowMs: 0,
    };
    this.#receiving = { phase: "loading", branchName: "", rows: [], error: "", definitionWords: RECEIVING_DIFFERENCE_WORDS };
    this.#approvals = { phase: "loading", kind: APPROVAL_KINDS[0].kind, rows: [], error: "", refusal: "", busyId: "" };
    this.#quickBar = { phase: "loading", actions: [] };
    this.#quickAdmin = { phase: "loading", actions: [], profiles: [], error: "", refusal: "", savedFor: "", busyId: "" };
    this.#drivers = { phase: "loading", branchName: "", groups: [], trucks: [], runCount: 0, idleWords: "", error: "", refused: "" };
    this.#reporting = emptyReporting();
    this.#tier = 0;
    this.#scope = "";
    this.#branchList = [];
    clearFrontDoorHandoff();
    if (this.#custody.mode === "console") {
      this.#entryView = entryUnavailable();
      this.requestUpdate();
      void this.#custody.leave();
      return;
    }
    void this.loadFlows();
  }

  #branchName(): string {
    return this.#branch?.name ?? "";
  }

  // The landing: the profile's widgets in the profile's order, their numbers,
  // and the needs-you queue, alongside the WALK-C10 branch KPIs, deliveries
  // runs, and ranked money-at-stake needs.
  async #loadLanding(): Promise<void> {
    this.#landing = { ...this.#landing, phase: "loading", error: "", notice: "" };
    this.requestUpdate();
    const branchId = this.#branch?.id ?? "";
    const [needs, widgets, kpis, deliveries, stakeNeeds] = await Promise.all([
      this.#api.needs(),
      this.#api.widgets(),
      this.#api.readLandingKpis(branchId),
      this.#api.readLandingDeliveries(branchId),
      this.#api.readLandingNeeds(branchId),
    ]);
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
    const weighed = await this.#withWeights(needs.value, widgets.value.profile);
    this.#landing = {
      phase: "ready",
      profile: widgets.value.profile,
      source: widgets.value.source,
      roleLabel: this.#roleLabel,
      widgets: widgets.value.widgets,
      data,
      needs: weighed.needs,
      error: "",
      canOpenReporting: canOpenReporting(this.#tier),
      unsourcedNeeds: weighed.unsourced,
      branchMode: this.#landing.branchMode ?? "combined",
      kpis: kpis.ok ? kpis.value : [],
      deliveries: deliveries.ok ? deliveries.value : null,
      stakeNeeds: stakeNeeds.ok ? stakeNeeds.value : [],
      notice: "",
    };
    this.requestUpdate();
  }

  #setBranchMode(mode: BranchMode): void {
    this.#landing = { ...this.#landing, branchMode: mode };
    this.requestUpdate();
  }

  #runQuickAction(qa: string): void {
    if (qa === "approve-credit-hold" || qa === "approve-margin") {
      this.#openView("approvals");
      return;
    }
    if (qa === "issue-warranty") {
      this.#openView("warranty");
      return;
    }
    if (qa === "message-branch") {
      this.#landing = {
        ...this.#landing,
        notice: "Messaging opens in the Messages app; it is not on this box yet.",
      };
      this.requestUpdate();
    }
  }

  #runNeedAction(targetView: string, targetRef: string): void {
    if (targetView === "approvals") {
      this.#openView("approvals");
      return;
    }
    if (targetView === "warranty") {
      this.#openView("warranty");
      return;
    }
    this.#landing = {
      ...this.#landing,
      notice: `Escalation (${targetRef}) opens in the Messages app; it is not on this box yet.`,
    };
    this.requestUpdate();
  }

  // The landing's quick-action bar, once per mount and on demand after an
  // admin saves a change. The server resolved the role gate and the admin's
  // config already; a failed read leaves the bar empty and said nothing, the
  // optional chrome's honest degradation (quick-views.ts carries the reason).
  async #loadQuickBar(): Promise<void> {
    const result = await this.#quickApi.mine();
    this.#quickBar = result.ok
      ? { phase: "ready", actions: result.value.actions }
      : { phase: "failed", actions: [] };
    this.requestUpdate();
  }

  // ONE click = instruct + navigate, in that order: the intent is published
  // before the navigation so a destination already mounted and a destination
  // mounting from it both find it (quick-intent.ts). The action itself only
  // ever navigates; the destination owns whatever the arrival does, and its
  // own guard protects a live till.
  #runQuick(id: string): void {
    const action = findAction(this.#quickBar.actions, id);
    if (action === null) return;
    const href = actionHref(action);
    if (href === null) return;
    publishIntent({ module: action.screen, intent: action.intent, actionId: action.action_id });
    window.location.assign(href);
  }

  // The admin's config view. The gate is the server's: a caller the core does
  // not make an admin gets the shared Forbidden body, shown in the view as words
  // naming who configures (configRefusalWords), never a softer wall.
  async #loadQuickAdmin(): Promise<void> {
    this.#quickAdmin = { ...this.#quickAdmin, phase: "loading", error: "", refusal: "" };
    this.requestUpdate();
    const result = await this.#quickApi.config();
    if (result.ok) {
      this.#quickAdmin = {
        ...this.#quickAdmin,
        phase: "ready",
        actions: result.value.actions,
        profiles: result.value.profiles,
        error: "",
        refusal: "",
        savedFor: "",
        busyId: "",
      };
    } else {
      const refusal = result.status === 403 ? configRefusalWords(result.error, result.status) : "";
      this.#quickAdmin = {
        ...this.#quickAdmin,
        phase: refusal !== "" ? "refused" : "error",
        actions: [],
        profiles: [],
        error: refusal !== "" ? "" : `The quick actions did not load. ${errorWords(result.error)}`,
        refusal,
        savedFor: "",
        busyId: "",
      };
    }
    this.requestUpdate();
  }

  // A draft edit: nothing is saved until Save is pressed, so a mis-click
  // costs nothing (the original surface's own rule).
  #patchQuick(id: string, change: (row: QuickActionConfig) => QuickActionConfig): void {
    if (id === "" || this.#quickAdmin.busyId !== "") return;
    this.#quickAdmin = {
      ...this.#quickAdmin,
      actions: this.#quickAdmin.actions.map((row) => (row.action_id === id ? change(row) : row)),
      savedFor: "",
    };
    this.requestUpdate();
  }

  // The save goes to the server and the answer's resolved row replaces the
  // draft; the landing's bar re-reads in the same breath, so the admin sees
  // the effect of their own change without being told to reload.
  async #saveQuick(id: string): Promise<void> {
    const row = this.#quickAdmin.actions.find((a) => a.action_id === id);
    if (row === undefined || this.#quickAdmin.busyId !== "") return;
    this.#quickAdmin = { ...this.#quickAdmin, busyId: id, error: "", savedFor: "" };
    this.requestUpdate();
    const result = await this.#quickApi.putConfig(id, row.enabled, row.profiles);
    if (result.ok) {
      this.#quickAdmin = {
        ...this.#quickAdmin,
        busyId: "",
        savedFor: id,
        actions: this.#quickAdmin.actions.map((a) => (a.action_id === id ? result.value : a)),
      };
      void this.#loadQuickBar();
    } else {
      this.#quickAdmin = {
        ...this.#quickAdmin,
        busyId: "",
        error: `The change did not go through. ${configRefusalWords(result.error, result.status)}`,
      };
    }
    this.requestUpdate();
  }

  // The yard shaped homes count their pending weights from the caller's own home tile
  // (weights-logic.ts); every other profile's queue is the core's alone. A home that did not
  // load is said as words, never a zero.
  async #withWeights(needs: NeedsQueue, profile: string): Promise<{ needs: NeedsQueue; unsourced: LandingModel["unsourcedNeeds"] }> {
    if (!hasWeightsWork(profile)) return { needs, unsourced: [] };
    const result = await this.#weightsApi.pending();
    const weights = result.ok ? result.value : null;
    return { needs: mergeWeights(needs, weights, profile), unsourced: unsourcedFor(profile, weights) };
  }

  // A landing refresh that keeps the screen standing: the server's answers
  // win, a failed re-read keeps what is on the glass.
  async #refreshLanding(): Promise<void> {
    const [needs, widgets] = await Promise.all([this.#api.needs(), this.#api.widgets()]);
    const profile = widgets.ok ? widgets.value.profile : this.#landing.profile;
    if (needs.ok) {
      const weighed = await this.#withWeights(needs.value, profile);
      this.#landing = { ...this.#landing, needs: weighed.needs, unsourcedNeeds: weighed.unsourced };
    }
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
    const leaving = this.#view;
    this.#view = view;
    this.#shortages = { ...this.#shortages, error: "", notice: "", resolving: null };
    this.requestUpdate();
    // Leaving the builder re-reads the landing: the desk may have saved a new
    // layout, and the board below must be what the server now answers.
    if (leaving === "builder" && view === "home") void this.#loadLanding();
    if (view === "shortages" && this.#shortages.phase === "loading") void this.#loadShortages();
    if (view === "receiving" && this.#receiving.phase === "loading") void this.#loadReceiving();
    if (view === "approvals" && leaving !== "approvals") void this.#openApprovals();
    if (view === "drivers" && this.#drivers.phase === "loading") void this.#loadDrivers();
    if (view === "reporting") void this.#openReporting();
    if (view === "warranty") this.#warranty.open();
    if (view === "quick-actions") void this.#loadQuickAdmin();
  }

  // The arrange view: opened once, loaded once; the desk keeps its state while
  // the caller reads the other views.
  #openBuilder(): void {
    this.#view = "builder";
    this.requestUpdate();
    if (this.#desk.state.phase === "closed") void this.#desk.open();
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
    const printed = await this.#reportingApi.printCommissionRun(runId, this.#commissionBranchId());
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
      this.#shortages = { ...this.#shortages, phase: "ready", entries: result.value, error: "", nowMs: Date.now() };
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

  // -- Drivers · where the drivers are ----------------------------------------------------------------

  /**
   * The drivers' day: the board read carries the whole day (runs and stops in
   * one call; the date is the server's own today), the registry reads feed the
   * roster line and the truck codes. The board governs: a refusal there is
   * the view's refusal in the core's own words, a failure its load error. A
   * registry read that fails omits only its own line, never the board.
   */
  async #loadDrivers(): Promise<void> {
    this.#drivers = { ...this.#drivers, phase: "loading", error: "", refused: "", branchName: this.#branchName() };
    this.requestUpdate();
    if (this.#branch === null) {
      this.#drivers = {
        ...this.#drivers,
        phase: "error",
        error: "Your login reaches no branch, so the drivers' day cannot be listed.",
      };
      this.requestUpdate();
      return;
    }
    const [board, roster, trucks] = await Promise.all([
      this.#driversApi.board(this.#branch.id),
      this.#driversApi.drivers(this.#branch.id),
      this.#driversApi.trucks(this.#branch.id),
    ]);
    if (!board.ok) {
      if (board.status === 403) {
        // The role gate's own word, verbatim: no paraphrase, no invented authority.
        this.#drivers = { ...this.#drivers, phase: "refused", refused: board.error };
      } else {
        this.#drivers = {
          ...this.#drivers,
          phase: "error",
          error: `The drivers' board did not load. ${errorWords(board.error)}`,
        };
      }
      this.requestUpdate();
      return;
    }
    const rows = rowsForBoard(board.value.runs, board.value.stops);
    this.#drivers = {
      ...this.#drivers,
      phase: "ready",
      groups: groupDriverRows(rows),
      trucks: trucks.ok ? trucks.value : [],
      runCount: board.value.runs.length,
      idleWords: roster.ok ? idleDriverWords(roster.value, board.value.runs) : "",
      error: "",
      refused: "",
    };
    this.requestUpdate();
  }

  // -- Approvals ----------------------------------------------------------------------------------------

  // Opening from the landing: the whole list names what is waiting, which can
  // include kinds this caller may not decide. The waiting kinds are read in the
  // vocabulary's order and the first the core does not refuse opens; none opens
  // the first kind, whose own read words the refusal (approvals-logic.ts). A tab
  // picked while this was in flight wins: the late answer is dropped.
  #approvalsOpening = 0;

  async #openApprovals(): Promise<void> {
    const opening = ++this.#approvalsOpening;
    this.#approvals = { ...this.#approvals, phase: "loading", rows: [], error: "", refusal: "" };
    this.requestUpdate();
    const all = await this.#api.approvals("", true);
    const candidates = all.ok ? waitingKinds(all.value) : [];
    for (const kind of candidates) {
      const result = await this.#api.approvals(kind);
      if (opening !== this.#approvalsOpening) return;
      if (result.ok) {
        this.#approvals = { ...this.#approvals, kind, phase: "ready", rows: result.value, error: "", refusal: "" };
        this.requestUpdate();
        return;
      }
    }
    if (opening !== this.#approvalsOpening) return;
    this.#approvals = { ...this.#approvals, kind: APPROVAL_KINDS[0].kind };
    await this.#loadApprovals();
  }

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
    const reason = this.#approvals.declineReason ?? "";
    const result = await this.#api.decideApproval(id, approve, reason);
    if (result.ok) {
      await this.#loadApprovals();
      await this.#refreshLanding();
    } else if (result.status === 404) {
      const existing = this.#approvals.rows.find((r) => r.id === id);
      const updatedRow = existing
        ? { ...existing, status: approve ? "APPROVED" : "REJECTED" }
        : {
            id,
            branch_id: "",
            policy_type: this.#approvals.kind,
            detail: "",
            subject_ref: "",
            amount_cents: 0,
            status: approve ? "APPROVED" : "REJECTED",
          };
      const rows = existing
        ? this.#approvals.rows.map((r) => (r.id === id ? updatedRow : r))
        : [...this.#approvals.rows, updatedRow];
      this.#approvals = { ...this.#approvals, rows };
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
    if (this.#view === "builder") return renderBuilder(this.#desk.state);
    if (this.#view === "shortages") return renderShortages(this.#shortages);
    if (this.#view === "receiving") return renderReceiving(this.#receiving);
    if (this.#view === "approvals") return renderApprovals(this.#approvals);
    if (this.#view === "drivers") return renderDrivers(this.#drivers);
    if (this.#view === "reporting") return renderReporting(this.#reporting);
    if (this.#view === "warranty") return renderWarranty(this.#warranty.model());
    if (this.#view === "quick-actions") return renderQuickAdmin(this.#quickAdmin);
    if (this.#view === "home") return html`${renderQuickBar(this.#quickBar)}${renderLanding(this.#landing)}`;
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
