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
import { warrantyStyles } from "./warranty-styles.js";
import { quickStyles } from "./quick-styles.js";
import { QuickApi, type QuickActionConfig } from "./quick-api.js";
import { publishIntent } from "./quick-intent.js";
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
  type EntryView,
  type LandingModel,
  type ReceivingModel,
  type ShortagesModel,
} from "./views.js";

const DEMO_ORG = "hh-demo";

export class ManagerHomeApp extends HHElement {
  static styles = [baseStyles, homeStyles, warrantyStyles, quickStyles];

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

  // The quick actions' calls (src/quick-*): the caller's resolved registry
  // rows, over the same staff client.
  readonly #quickApi = new QuickApi(this.#staffClient);

  // The warranty desk (src/warranty-*): its own door over the same staff
  // client, its fields read from this element's shadow root by name.
  readonly #warranty = new WarrantyDesk(new WarrantyApi(this.#staffClient), {
    repaint: () => this.requestUpdate(),
    field: (name) => this.renderRoot.querySelector<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>(`[data-field="${name}"]`)?.value ?? "",
    index: (name) => this.renderRoot.querySelector<HTMLSelectElement>(`select[data-field="${name}"]`)?.selectedIndex ?? -1,
  });

  // The role label the header shows; set from the handoff or the mint.
  #roleLabel = "";

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

  // The landing's state.
  #landing: LandingModel = { phase: "loading", profile: "", source: "", roleLabel: "", widgets: [], data: new Map(), needs: null, error: "" };

  // The landing's quick-action bar: the caller's resolved registry rows.
  #quickBar: QuickBarModel = { phase: "loading", actions: [] };

  // The admin's quick-actions config: the draft rows are local until Save.
  #quickAdmin: QuickAdminModel = { phase: "loading", actions: [], profiles: [], error: "", refusal: "", savedFor: "", busyId: "" };

  // The views' states.
  #shortages: ShortagesModel = {
    phase: "loading", branchName: "", entries: [], resolving: null, error: "", notice: "",
    resolverWords: SHORTAGE_RESOLVER_WORDS,
  };
  #receiving: ReceivingModel = { phase: "loading", branchName: "", rows: [], error: "", definitionWords: RECEIVING_DIFFERENCE_WORDS };
  #approvals: ApprovalsModel = { phase: "loading", kind: APPROVAL_KINDS[0].kind, rows: [], error: "", refusal: "", busyId: "" };

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
      if (view === "shortages" || view === "receiving" || view === "approvals" || view === "warranty" || view === "quick-actions") {
        this.#openView(view);
      }
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
    if (action === "back") {
      this.#openView("home");
      return;
    }
    if (action === "reload-view") {
      if (this.#view === "shortages") void this.#loadShortages();
      if (this.#view === "receiving") void this.#loadReceiving();
      if (this.#view === "approvals") void this.#loadApprovals();
      if (this.#view === "quick-actions") void this.#loadQuickAdmin();
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

  // The resolve note field: mirrored from input events, never bound. The
  // warranty desk hears only the one field whose shape it changes (the chase
  // form's action pick); every other field is read at press time.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (target instanceof HTMLTextAreaElement && target.getAttribute("data-field") === "resolution-note") {
      if (this.#shortages.resolving !== null) {
        this.#shortages = {
          ...this.#shortages,
          resolving: { ...this.#shortages.resolving, note: target.value },
        };
      }
    }
    if (target instanceof Element) {
      const field = target.getAttribute("data-field") ?? "";
      if (field.startsWith("w-")) this.#warranty.onField(field);
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
  // set on the element here, after each render. The warranty desk's seeds ride
  // the same rule: a form's starting values and a photo's object address are
  // set on their elements after the render that drew them, never bound.
  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const header = this.renderRoot.querySelector("hos-app-header") as (HTMLElement & { roleLabel: string; theme: string }) | null;
    if (header !== null) {
      header.roleLabel = this.#roleLabel;
      header.theme = this.#theme;
    }
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
    } else {
      this.#branch = null;
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
    this.#warranty.reset();
    this.#landing = { phase: "loading", profile: "", source: "", roleLabel: "", widgets: [], data: new Map(), needs: null, error: "" };
    this.#quickBar = { phase: "loading", actions: [] };
    this.#quickAdmin = { phase: "loading", actions: [], profiles: [], error: "", refusal: "", savedFor: "", busyId: "" };
    this.#shortages = {
      phase: "loading", branchName: "", entries: [], resolving: null, error: "", notice: "",
      resolverWords: SHORTAGE_RESOLVER_WORDS,
    };
    this.#receiving = { phase: "loading", branchName: "", rows: [], error: "", definitionWords: RECEIVING_DIFFERENCE_WORDS };
    this.#approvals = { phase: "loading", kind: APPROVAL_KINDS[0].kind, rows: [], error: "", refusal: "", busyId: "" };
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
  // not make an admin gets the shared Forbidden body, shown as words naming who configures in the
  // view (configRefusalWords), never paraphrased into a softer wall.
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
    if (view === "warranty") this.#warranty.open();
    if (view === "quick-actions") void this.#loadQuickAdmin();
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
