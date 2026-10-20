// The app root: the chrome the kit shares (sign in screen, Pro frame, states),
// one session seam behind them, and this app's own body: the section row
// (Orders, Projects, Team, Account), the orders list with its filters, and the
// order view with its journey. The kit's ProAppBase runs a single landing
// surface and keeps its state private, so a multi surface app composes the
// kit's pieces directly like this; the seam (TenantSession, tenantClient, the
// port) and every element are the kit's.
//
// The sign in adapter is the stub only where Vite serves a development build
// (import.meta.env.DEV is replaced at build time, so a production bundle
// carries only the named seam, realExchangeAdapter, which refuses in plain
// words until the auth lead's go fills it; the demo build, Vite mode "demo", carries the demo adapter). The shells cycle owns the real
// shell and the offline store, which goes behind this app's FieldPort
// (src/port.ts) without touching a screen.
//
// The design system's position gate binds this app like every dependent: the
// root listens on itself and acts on data- action names, the form fields ride
// one input and one change listener on the render root (change does not cross
// a shadow boundary), and every child element is told what it shows by
// assignment in updated().
import { html, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient, tenantAuth, tenantTeam, type TenantAddress, type TenantInvite, type TenantMe, type TenantOrder, type TenantOrderDetail, type TenantProjectDashboard, type TenantProjectSummary, type TenantReturnEligibility, type TenantReturnRequest, type TenantUser } from "@hardscapehouse/api-client";
import {
  CAPABILITIES,
  InMemoryTenantSession,
  appOf,
  realExchangeAdapter,
  demoTenantAdapter,
  signInFromCard,
  demoRoleFromAddress, personaRoleHeld,
  otherAppAddress,
  roleLabel,
  stubAdapter,
  tenantClient,
  type TenantSession,
} from "@hardscapehouse/pro-kit";
import "@hardscapehouse/pro-kit/src/frame.js";
import "@hardscapehouse/pro-kit/src/signin.js";
import "@hardscapehouse/pro-kit/src/state-view.js";
import type { HHProSignIn, SignInMethodChoice, SignInSubmit } from "@hardscapehouse/pro-kit/src/signin.js";
import type { SurfaceSelect } from "@hardscapehouse/pro-kit/src/frame.js";
import { liveFieldPort, type FieldPort } from "./port.js";
import { ordersSurfaceOf, sectionsFor, type Section } from "./sections.js";
import { MAX_INSTRUCTIONS_LENGTH, houseToday, instructionsCountWords, instructionsEditable, instructionsNote } from "./timeline.js";
import { returnRequestLines } from "./returns.js";
import { instructionsCard, accountView, orderView, ordersList, projectStates, projectView, projectsList, returnsCard, sectionNav, sectionPlaceholder, teamView, type EligibilityState, type Phase, type ReturnFormState } from "./views.js";
import { passwordProblem } from "./account.js";
import { isLastActiveOwnerDemotion } from "./team.js";
import { fieldStyles } from "./styles.js";

interface OrdersState {
  phase: Phase;
  items: TenantOrder[];
  truncated: boolean;
  errorWords?: string;
}

interface InstructionsState {
  draft: string;
  busy: boolean;
  saved: boolean;
  error: string;
  prefill: string | null;
}

interface ReturnsState {
  requests: TenantReturnRequest[] | null;
  listError: boolean;
  eligibility: EligibilityState;
  form: ReturnFormState;
  notice: string;
}

interface OrderState {
  id: string;
  phase: Phase;
  detail: TenantOrderDetail | null;
  instructions: InstructionsState;
  returns: ReturnsState;
}

interface ProjectsState {
  phase: Phase;
  items: TenantProjectSummary[];
}

interface ProjectState {
  id: string;
  phase: Phase;
  data: TenantProjectDashboard | null;
}

interface TeamState {
  phase: Phase;
  users: TenantUser[];
  invites: TenantInvite[];
  roleDraft: { userId: string; role: string } | null;
  inviteEmail: string;
  inviteRole: string;
  inviteBusy: boolean;
  notice: string;
  error: string;
}

interface AccountState {
  phase: Phase;
  me: TenantMe | null;
  addresses: TenantAddress[] | null;
  address: TenantAddress | null;
  addressPhase: Phase;
  name: string;
  nameDirty: boolean;
  namePrefill: string | null;
  profileBusy: boolean;
  profileSaved: boolean;
  profileError: string;
  pwCurrent: string;
  pwNew: string;
  pwConfirm: string;
  passwordBusy: boolean;
  passwordError: string;
}

const CANNOT_RETURN_WORDS = "This order cannot be returned through Pro. Ask the dealer to arrange it.";
const RETURN_REFUSAL_WORDS = "Your role cannot request a return. Ask an Owner, Buyer, or Project manager on your account to request it.";
const RETURN_FAULT_WORDS = "Returns did not load. Try again in a moment.";
// The core's last owner refusal (409) in the words a person reads; the core's
// stored words stay the core's.
const LAST_OWNER_WORDS = "The account needs at least one active owner.";

function freshInstructions(): InstructionsState {
  return { draft: "", busy: false, saved: false, error: "", prefill: null };
}

function freshReturns(): ReturnsState {
  return { requests: null, listError: false, eligibility: { phase: "idle", data: null, words: "", canRetry: false }, form: { qty: new Map(), reason: "DAMAGED", note: "", busy: false, error: "" }, notice: "" };
}

function freshAccount(): AccountState {
  return { phase: "idle", me: null, addresses: null, address: null, addressPhase: "idle", name: "", nameDirty: false, namePrefill: null, profileBusy: false, profileSaved: false, profileError: "", pwCurrent: "", pwNew: "", pwConfirm: "", passwordBusy: false, passwordError: "" };
}

export class ProFieldApp extends HHElement {
  static styles = [baseStyles, fieldStyles];

  readonly #session: TenantSession;
  readonly #client: ApiClient;
  readonly #port: FieldPort;
  #dealerName = "";
  #signinMessage = "";
  #signinBusy = false;
  #section: Section = "orders";
  #surface = "tracking";
  #orders: OrdersState = { phase: "idle", items: [], truncated: false };
  #order: OrderState | null = null;
  #projects: ProjectsState = { phase: "idle", items: [] };
  #project: ProjectState | null = null;
  #team: TeamState = { phase: "idle", users: [], invites: [], roleDraft: null, inviteEmail: "", inviteRole: "buyer", inviteBusy: false, notice: "", error: "" };
  #account: AccountState = freshAccount();
  #ordersSeq = 0;
  #orderSeq = 0;
  #projectsSeq = 0;
  #projectSeq = 0;
  #teamSeq = 0;
  #accountSeq = 0;
  #personaRole: string | null = null;
  #unsubscribe: (() => void) | null = null;

  constructor() {
    super();
    const adapter = import.meta.env.DEV ? stubAdapter() : import.meta.env.MODE === "demo" ? demoTenantAdapter() : realExchangeAdapter();
    this.#session = new InMemoryTenantSession({ adapter, onCustody: () => this.requestUpdate() });
    this.#client = tenantClient(this.#session);
    this.#port = liveFieldPort(this.#client);
  }

  #inFrame(): boolean {
    return this.#session.current() !== null || this.#personaRole !== null;
  }

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "retry") {
      if (this.#session.profile() === null) {
        void this.#retrySession();
      } else if (this.#section === "projects") {
        if (this.#project !== null) void this.#loadProject(this.#project.id);
        else void this.#loadProjects();
      } else if (this.#section === "team") {
        void this.#loadTeam();
      } else if (this.#section === "account") {
        void this.#loadAccount();
      } else if (this.#order !== null) {
        void this.#loadOrder(this.#order.id);
      } else {
        void this.#loadOrders();
      }
      return;
    }
    if (action === "open-order") {
      if (this.#section !== "orders") {
        this.#section = "orders";
        this.#project = null;
      }
      this.#openOrder(control?.getAttribute("data-order-id") ?? "");
      return;
    }
    if (action === "open-project") {
      this.#openProject(control?.getAttribute("data-project-id") ?? "");
      return;
    }
    if (action === "back-to-projects") {
      this.#project = null;
      this.requestUpdate();
      return;
    }
    if (action === "back-to-orders") {
      this.#order = null;
      this.requestUpdate();
      return;
    }
    if (action === "save-instructions") {
      void this.#saveInstructions();
      return;
    }
    if (action === "request-return") {
      void this.#openReturnFlow();
      return;
    }
    if (action === "cancel-return") {
      this.#closeReturnFlow();
      return;
    }
    if (action === "submit-return") {
      void this.#submitReturn();
      return;
    }
    if (action === "create-invite") {
      void this.#createInvite();
      return;
    }
    if (action === "set-status") {
      void this.#setUserStatus(control?.getAttribute("data-user-id") ?? "", control?.getAttribute("data-status") ?? "");
      return;
    }
    if (action === "confirm-role") {
      const draft = this.#team.roleDraft;
      const userId = control?.getAttribute("data-user-id") ?? "";
      if (draft !== null && draft.userId === userId) {
        this.#team.roleDraft = null;
        void this.#setUserRole(draft.userId, draft.role);
      }
      return;
    }
    if (action === "cancel-role") {
      this.#team.roleDraft = null;
      this.requestUpdate();
      return;
    }
    if (action === "save-profile") {
      void this.#saveProfile();
      return;
    }
    if (action === "change-password") {
      void this.#changePassword();
      return;
    }
    if (action === "open-address") {
      void this.#openAddress(control?.getAttribute("data-address-id") ?? "");
      return;
    }
    if (action === "back-to-addresses") {
      this.#account.address = null;
      this.#account.addressPhase = "idle";
      this.requestUpdate();
      return;
    }
    if (action === "section") {
      this.#selectSection(control?.getAttribute("data-value") ?? "");
    }
  };

  // The form fields: input and change do not leave this element's shadow root,
  // so the listeners sit on the render root. The drafts mirror the controls;
  // the controls are never bound.
  readonly #onField = (event: Event): void => {
    const target = event.target;
    if (!(target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement)) return;
    const field = target.getAttribute("data-field");
    if (field === "invite-email" && target instanceof HTMLInputElement) {
      this.#team.inviteEmail = target.value;
      this.requestUpdate();
      return;
    }
    if (field === "invite-role" && target instanceof HTMLSelectElement) {
      this.#team.inviteRole = target.value;
      return;
    }
    if (field === "member-role" && target instanceof HTMLSelectElement) {
      this.#stageUserRole(target.getAttribute("data-user-id") ?? "", target.value);
      return;
    }
    if (field === "name" && target instanceof HTMLInputElement) {
      this.#account.name = target.value;
      this.#account.nameDirty = true;
      this.#account.profileSaved = false;
      this.requestUpdate();
      return;
    }
    if (field === "pw-current" && target instanceof HTMLInputElement) {
      this.#account.pwCurrent = target.value;
      return;
    }
    if (field === "pw-new" && target instanceof HTMLInputElement) {
      this.#account.pwNew = target.value;
      return;
    }
    if (field === "pw-confirm" && target instanceof HTMLInputElement) {
      this.#account.pwConfirm = target.value;
      return;
    }
    const order = this.#order;
    if (order === null || order.detail === null) return;
    if (field === "instructions") {
      order.instructions.draft = target.value;
      order.instructions.saved = false;
      this.requestUpdate();
      return;
    }
    if (field === "return-qty" && target instanceof HTMLInputElement) {
      const lineId = target.getAttribute("data-line-id") ?? "";
      order.returns.form.qty.set(lineId, target.value);
      return;
    }
    if (field === "return-reason" && target instanceof HTMLSelectElement) {
      order.returns.form.reason = target.value;
      return;
    }
    if (field === "return-note" && target instanceof HTMLTextAreaElement) {
      order.returns.form.note = target.value;
    }
  };

  readonly #onSignIn = (event: Event): void => {
    void this.#signIn((event as CustomEvent<SignInSubmit | SignInMethodChoice>).detail);
  };

  readonly #onSelect = (event: Event): void => {
    const { app, surface } = (event as CustomEvent<SurfaceSelect>).detail;
    if (app === "field") {
      if (surface !== this.#surface) {
        this.#surface = surface;
        this.#section = "orders";
        this.#order = null;
        this.requestUpdate();
        void this.#loadOrders();
      }
      return;
    }
    const target = appOf(app);
    if (target !== undefined) window.location.assign(otherAppAddress(target.path, this.#session.profile()?.role, import.meta.env.MODE === "demo"));
  };

  readonly #onSignOut = (): void => {
    this.#personaRole = null;
    void this.#session.signOut();
  };

  connectedCallback(): void {
    super.connectedCallback();
    // The demo build: a front door contractor card opens this app with its role, and the persona signs in at once.
    if (import.meta.env.MODE === "demo" || import.meta.env.DEV) {
      const asked = demoRoleFromAddress(window.location.search);
      if (asked !== null) {
        this.#personaRole = asked;
        void this.#signIn({ method: "persona", roleLabel: asked });
      }
    }
    this.addEventListener("click", this.#onClick);
    this.addEventListener("pro-signin-submit", this.#onSignIn);
    this.addEventListener("pro-signin-method", this.#onSignIn);
    this.addEventListener("pro-surface-select", this.#onSelect);
    this.addEventListener("pro-sign-out", this.#onSignOut);
    this.renderRoot.addEventListener("input", this.#onField);
    this.renderRoot.addEventListener("change", this.#onField);
    this.#unsubscribe = this.#session.subscribe(() => {
      if (this.#session.profile() === null) {
        this.#section = "orders";
        this.#surface = "tracking";
        this.#orders = { phase: "idle", items: [], truncated: false };
        this.#order = null;
        this.#projects = { phase: "idle", items: [] };
        this.#project = null;
        this.#team = { phase: "idle", users: [], invites: [], roleDraft: null, inviteEmail: "", inviteRole: "buyer", inviteBusy: false, notice: "", error: "" };
        this.#account = freshAccount();
      }
      this.requestUpdate();
    });
    void this.#loadDealer();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("pro-signin-submit", this.#onSignIn);
    this.removeEventListener("pro-signin-method", this.#onSignIn);
    this.removeEventListener("pro-surface-select", this.#onSelect);
    this.removeEventListener("pro-sign-out", this.#onSignOut);
    this.renderRoot.removeEventListener("input", this.#onField);
    this.renderRoot.removeEventListener("change", this.#onField);
    this.#unsubscribe?.();
    this.#unsubscribe = null;
    super.disconnectedCallback();
  }

  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const root = document.documentElement;
    root.setAttribute("data-brand", "hh-pro");
    if (!this.#inFrame()) root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", "light");
    const signin = this.renderRoot.querySelector("hh-pro-signin") as HHProSignIn | null;
    if (signin !== null) {
      signin.dealerName = this.#dealerName;
      signin.message = this.#signinMessage;
      signin.busy = this.#signinBusy;
      const options = this.#session.options();
      signin.methods = options.methods;
      signin.refused = options.refused;
      signin.personas = options.personas;
    }
    // The instructions field's first value, set once when the order opens: the
    // control is never bound, so its typed words would otherwise be lost on a
    // re-render that assigns over them.
    const order = this.#order;
    if (order !== null && order.instructions.prefill !== null) {
      const field = this.renderRoot.querySelector<HTMLTextAreaElement>("textarea[data-field=instructions]");
      if (field !== null) {
        field.value = order.instructions.prefill;
        order.instructions.draft = order.instructions.prefill;
        order.instructions.prefill = null;
      }
    }
    if (order !== null && order.returns.eligibility.phase === "done") {
      const reason = this.renderRoot.querySelector<HTMLSelectElement>("select[data-field=return-reason]");
      if (reason !== null) reason.value = order.returns.form.reason;
    }
    // The selects' selections are the wiring pass's job, never a binding.
    if (this.#section === "team" && this.#team.phase === "ready") {
      for (const select of Array.from(this.renderRoot.querySelectorAll<HTMLSelectElement>("select[data-field=member-role]"))) {
        const user = this.#team.users.find((candidate) => candidate.id === select.getAttribute("data-user-id"));
        if (user !== undefined) {
          select.value = this.#team.roleDraft !== null && this.#team.roleDraft.userId === user.id ? this.#team.roleDraft.role : user.role;
        }
      }
      const inviteRole = this.renderRoot.querySelector<HTMLSelectElement>("select[data-field=invite-role]");
      if (inviteRole !== null) inviteRole.value = this.#team.inviteRole;
    }
    // The profile name's first value, set once when the account opens.
    if (this.#section === "account" && this.#account.namePrefill !== null) {
      const name = this.renderRoot.querySelector<HTMLInputElement>("input[data-field=name]");
      if (name !== null) {
        name.value = this.#account.namePrefill;
        this.#account.name = this.#account.namePrefill;
        this.#account.namePrefill = null;
      }
    }
  }

  async #loadDealer(): Promise<void> {
    const answer = await tenantAuth.tenantGetConfig(this.#client);
    if (answer.ok) {
      this.#dealerName = answer.value.dealer_name;
      this.requestUpdate();
    }
  }

  async #signIn(input: SignInSubmit | SignInMethodChoice): Promise<void> {
    this.#signinBusy = true;
    this.#signinMessage = "";
    this.#personaRole = personaRoleHeld(input, import.meta.env.MODE === "demo" || import.meta.env.DEV);
    this.#orders = { phase: "loading", items: [], truncated: false };
    this.requestUpdate();
    const result = await signInFromCard(this.#session, input);
    this.#signinBusy = false;
    if (result.ok) {
      this.#signinMessage = "";
      void this.#loadOrders();
    } else if (result.status === 401 || result.status === 403 || !this.#inFrame()) {
      this.#personaRole = null;
      this.#signinMessage = result.error;
    } else {
      this.#orders = { phase: "error", items: [], truncated: false, errorWords: result.error };
    }
    this.requestUpdate();
  }

  async #retrySession(): Promise<void> {
    if (this.#session.current() === null && this.#personaRole !== null) {
      await this.#signIn({ method: "persona", roleLabel: this.#personaRole });
      return;
    }
    this.#orders = { phase: "loading", items: [], truncated: false };
    this.requestUpdate();
    const result = await this.#session.check();
    if (result.ok) {
      this.#signinMessage = "";
      void this.#loadOrders();
    } else if (result.status === 401 || result.status === 403 || !this.#inFrame()) {
      this.#personaRole = null;
      this.#signinMessage = result.error;
    } else {
      this.#orders = { phase: "error", items: [], truncated: false, errorWords: result.error };
    }
    this.requestUpdate();
  }

  async #loadOrders(): Promise<void> {
    const sequence = (this.#ordersSeq += 1);
    this.#orders = { phase: "loading", items: this.#orders.items, truncated: this.#orders.truncated };
    this.requestUpdate();
    const answer = await this.#port.listOrders();
    if (sequence !== this.#ordersSeq || this.#session.profile() === null) return;
    if (answer.ok) {
      const filter = ordersSurfaceOf(this.#surface)?.fulfillments ?? null;
      const items = filter === null ? answer.value : answer.value.filter((order) => filter.includes(order.fulfillment));
      this.#orders = { phase: items.length === 0 ? "empty" : "ready", items, truncated: answer.truncated === true };
    } else if (answer.status === 401) {
      // The session is gone (expired, or the person was deactivated): sign out and say so, never blame the role.
      this.#personaRole = null;
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else if (answer.status === 403) {
      this.#orders = { phase: "refusal", items: [], truncated: false };
    } else {
      this.#orders = {
        phase: "error",
        items: [],
        truncated: false,
        errorWords: answer.status === undefined ? "Pro is not reachable right now. Try again in a moment." : undefined,
      };
    }
    this.requestUpdate();
  }

  #openOrder(id: string): void {
    if (id === "") return;
    this.#order = { id, phase: "loading", detail: null, instructions: freshInstructions(), returns: freshReturns() };
    this.requestUpdate();
    void this.#loadOrder(id);
  }

  async #loadOrder(id: string): Promise<void> {
    const sequence = (this.#orderSeq += 1);
    this.#order = { id, phase: "loading", detail: null, instructions: freshInstructions(), returns: freshReturns() };
    this.requestUpdate();
    const answer = await this.#port.getOrder(id);
    if (sequence !== this.#orderSeq || this.#session.profile() === null) return;
    if (answer.ok) {
      const detail = answer.value;
      this.#order = { id, phase: "ready", detail, instructions: { ...freshInstructions(), prefill: detail.delivery_instructions }, returns: freshReturns() };
      void this.#loadReturns();
    } else if (answer.status === 401) {
      this.#personaRole = null;
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else if (answer.status === 403) {
      this.#order = { id, phase: "refusal", detail: null, instructions: freshInstructions(), returns: freshReturns() };
    } else {
      this.#order = { id, phase: "error", detail: null, instructions: freshInstructions(), returns: freshReturns() };
    }
    this.requestUpdate();
  }

  // The order's return requests: the list is the account's, so it is filtered
  // to this order here, exactly as the portal screen did. A fault here is not
  // fatal: the order renders without the list.
  async #loadReturns(): Promise<void> {
    const order = this.#order;
    if (order === null || order.detail === null) return;
    const answer = await this.#port.listReturnRequests();
    if (this.#order !== order || this.#session.profile() === null) return;
    if (answer.ok) {
      order.returns.requests = answer.value.filter((request) => request.order_id === order.id);
      order.returns.listError = false;
    } else {
      order.returns.requests = [];
      order.returns.listError = true;
    }
    this.requestUpdate();
  }

  async #signOutOnWriteExpiry(): Promise<void> {
    this.#signinMessage = "You were signed out, so this was not saved. Sign in again and make the change again.";
    await this.#session.signOut();
  }

  async #saveInstructions(): Promise<void> {
    const order = this.#order;
    if (order === null || order.detail === null || order.instructions.busy) return;
    if (order.instructions.draft.length > MAX_INSTRUCTIONS_LENGTH) {
      order.instructions.error = `Keep delivery instructions to ${MAX_INSTRUCTIONS_LENGTH} characters or fewer.`;
      this.requestUpdate();
      return;
    }
    order.instructions.busy = true;
    order.instructions.error = "";
    this.requestUpdate();
    const answer = await this.#port.setDeliveryInstructions(order.id, order.instructions.draft);
    order.instructions.busy = false;
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#order !== order) return;
    if (answer.ok) {
      order.detail.delivery_instructions = order.instructions.draft;
      order.instructions.saved = true;
    } else {
      order.instructions.error = answer.error;
    }
    this.requestUpdate();
  }

  async #openReturnFlow(): Promise<void> {
    const order = this.#order;
    if (order === null || order.detail === null || order.returns.eligibility.phase === "loading") return;
    order.returns.eligibility = { phase: "loading", data: null, words: "", canRetry: false };
    order.returns.form = { qty: new Map(), reason: "DAMAGED", note: "", busy: false, error: "" };
    order.returns.notice = "";
    this.requestUpdate();
    const answer = await this.#port.getReturnEligibility(order.id);
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#order !== order || this.#session.profile() === null) return;
    if (answer.ok) {
      order.returns.eligibility = { phase: "done", data: answer.value, words: "", canRetry: false };
    } else if (answer.status === 404) {
      // The core answers not found for anything that was not a counter sale, on purpose
      // (IDOR safe), so this is a plain no, not a fault to retry.
      order.returns.eligibility = { phase: "error", data: null, words: CANNOT_RETURN_WORDS, canRetry: false };
    } else if (answer.status === 403) {
      order.returns.eligibility = { phase: "error", data: null, words: RETURN_REFUSAL_WORDS, canRetry: false };
    } else {
      order.returns.eligibility = { phase: "error", data: null, words: RETURN_FAULT_WORDS, canRetry: true };
    }
    this.requestUpdate();
  }

  #closeReturnFlow(): void {
    const order = this.#order;
    if (order === null) return;
    order.returns.eligibility = { phase: "idle", data: null, words: "", canRetry: false };
    order.returns.form = { qty: new Map(), reason: "DAMAGED", note: "", busy: false, error: "" };
    this.requestUpdate();
  }

  async #submitReturn(): Promise<void> {
    const order = this.#order;
    if (order === null || order.detail === null || order.returns.eligibility.data === null || order.returns.form.busy) return;
    const built = returnRequestLines(order.returns.form.qty, order.returns.eligibility.data.lines);
    if (!built.ok) {
      order.returns.form.error = built.words;
      this.requestUpdate();
      return;
    }
    order.returns.form.busy = true;
    order.returns.form.error = "";
    this.requestUpdate();
    const answer = await this.#port.createReturnRequest(order.id, {
      reason_code: order.returns.form.reason as "DAMAGED" | "DEFECT" | "WRONG_ITEM" | "NO_LONGER_NEEDED" | "OTHER",
      note: order.returns.form.note,
      lines: built.lines,
    });
    order.returns.form.busy = false;
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#order !== order) return;
    if (answer.ok) {
      this.#closeReturnFlow();
      order.returns.notice = `Return request ${answer.value.request_no} sent. The dealer decides it.`;
      void this.#loadReturns();
    } else {
      order.returns.form.error = answer.status === 403 ? RETURN_REFUSAL_WORDS : answer.error;
    }
    this.requestUpdate();
  }

  #selectSection(to: string): void {
    if (to !== "orders" && to !== "projects" && to !== "team" && to !== "account") return;
    if (to === this.#section) return;
    // Leaving the account leaves its drafts behind: the controls are never
    // bound, so anything the state kept would ride the next submit of a form
    // the person no longer sees (a password set, a name saved from a blank
    // field).
    if (this.#section === "account") {
      this.#account.pwCurrent = "";
      this.#account.pwNew = "";
      this.#account.pwConfirm = "";
      this.#account.name = "";
      this.#account.nameDirty = false;
      this.#account.profileBusy = false;
      this.#account.passwordBusy = false;
    }
    if (this.#section === "team") {
      this.#team.inviteBusy = false;
      this.#team.roleDraft = null;
    }
    this.#section = to;
    this.#order = null;
    this.#project = null;
    this.requestUpdate();
    if (to === "projects") void this.#loadProjects();
    if (to === "team") {
      this.#team.notice = "";
      this.#team.error = "";
      this.#team.roleDraft = null;
      void this.#loadTeam();
    }
    if (to === "account") {
      this.#account.address = null;
      this.#account.addressPhase = "idle";
      void this.#loadAccount();
    }
  }

  async #loadAccount(): Promise<void> {
    const sequence = (this.#accountSeq += 1);
    // The account form's controls are never bound, so the state must hold
    // nothing the visible form does not: the password fields start empty at
    // every load, exactly as the fresh inputs the person sees.
    this.#account.pwCurrent = "";
    this.#account.pwNew = "";
    this.#account.pwConfirm = "";
    this.#account.phase = "loading";
    this.requestUpdate();
    const [me, addresses] = await Promise.all([this.#port.getMe(), this.#port.listAddresses()]);
    if (sequence !== this.#accountSeq || this.#session.profile() === null || this.#section !== "account") return;
    const refused = me.ok === false ? me : addresses.ok === false ? addresses : null;
    if (refused !== null) {
      if (refused.status === 401) {
        this.#signinMessage = "You are signed out. Sign in again.";
        await this.#session.signOut();
        return;
      }
      this.#account.phase = refused.status === 403 ? "refusal" : "error";
    } else if (me.ok && addresses.ok) {
      // The fresh inputs render empty, so the name is prefilled at every
      // account load the person has not typed into; a draft typed on screen
      // keeps the field alone.
      this.#account = {
        ...this.#account,
        phase: "ready",
        me: me.value,
        addresses: addresses.value,
        namePrefill: this.#account.nameDirty ? this.#account.namePrefill : me.value.name,
      };
    }
    this.requestUpdate();
  }

  async #saveProfile(): Promise<void> {
    const account = this.#account;
    if (account.me === null || account.profileBusy || !account.nameDirty) return;
    account.profileBusy = true;
    account.profileError = "";
    this.requestUpdate();
    const answer = await tenantAuth.tenantUpdateMe(this.#client, { name: account.name.trim() });
    account.profileBusy = false;
    this.#account.profileBusy = false;
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#section !== "account" || this.#session.profile() === null) return;
    if (answer.ok) {
      account.me = answer.value as typeof account.me;
      account.nameDirty = false;
      account.profileSaved = true;
    } else {
      account.profileError = answer.error;
    }
    this.requestUpdate();
  }

  async #changePassword(): Promise<void> {
    const account = this.#account;
    if (account.passwordBusy) return;
    const problem = passwordProblem(account.pwCurrent, account.pwNew, account.pwConfirm);
    if (problem !== "") {
      account.passwordError = problem;
      this.requestUpdate();
      return;
    }
    account.passwordBusy = true;
    account.passwordError = "";
    this.requestUpdate();
    const answer = await tenantAuth.tenantChangePassword(this.#client, { current_password: account.pwCurrent, new_password: account.pwNew });
    account.passwordBusy = false;
    this.#account.passwordBusy = false;
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#section !== "account" || this.#session.profile() === null) return;
    if (answer.ok) {
      account.pwCurrent = "";
      account.pwNew = "";
      account.pwConfirm = "";
      for (const field of Array.from(this.renderRoot.querySelectorAll<HTMLInputElement>("input[data-field^=pw-]"))) field.value = "";
      account.passwordError = "Your password is changed. Use it next time you sign in.";
    } else {
      account.passwordError = answer.error;
    }
    this.requestUpdate();
  }

  async #openAddress(id: string): Promise<void> {
    if (id === "") return;
    this.#account.address = null;
    this.#account.addressPhase = "loading";
    this.requestUpdate();
    const answer = await this.#port.getAddress(id);
    if (this.#section !== "account" || this.#session.profile() === null) return;
    if (answer.ok) {
      this.#account.address = answer.value;
      this.#account.addressPhase = "ready";
    } else if (answer.status === 401) {
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else {
      this.#account.addressPhase = "error";
    }
    this.requestUpdate();
  }

  // The team reads ride the port; the team's writes stay off it on purpose
  // (management actions never queue) and call the client directly. The words
  // an action said survive its reload.
  async #loadTeam(): Promise<void> {
    const sequence = (this.#teamSeq += 1);
    this.#team = { ...this.#team, phase: "loading" };
    this.requestUpdate();
    const [users, invites] = await Promise.all([this.#port.listUsers(), this.#port.listInvites()]);
    if (sequence !== this.#teamSeq || this.#session.profile() === null || this.#section !== "team") return;
    const refused = users.ok === false ? users : invites.ok === false ? invites : null;
    if (refused !== null) {
      if (refused.status === 401) {
        this.#signinMessage = "You are signed out. Sign in again.";
        await this.#session.signOut();
        return;
      }
      this.#team = { ...this.#team, phase: refused.status === 403 ? "refusal" : "error" };
    } else if (users.ok && invites.ok) {
      this.#team = { ...this.#team, phase: "ready", users: users.value, invites: invites.value };
    }
    this.requestUpdate();
  }

  #stageUserRole(id: string, role: string): void {
    if (id === "" || role === "") return;
    const target = this.#team.users.find((user) => user.id === id);
    if (target === undefined) return;
    this.#team.notice = "";
    this.#team.error = "";
    if (role === target.role) {
      this.#team.roleDraft = null;
      this.requestUpdate();
      return;
    }
    if (isLastActiveOwnerDemotion(this.#team.users, id, role)) {
      this.#team.roleDraft = null;
      this.#team.error = LAST_OWNER_WORDS;
      this.requestUpdate();
      return;
    }
    this.#team.roleDraft = { userId: id, role };
    this.requestUpdate();
  }

  async #setUserRole(id: string, role: string): Promise<void> {
    if (id === "" || role === "") return;
    this.#team.notice = "";
    this.#team.error = "";
    this.requestUpdate();
    const answer = await tenantTeam.tenantUpdateUserRole(this.#client, id, { role: role as "account_admin" | "buyer" | "buyer_no_pay" | "ap_finance" | "field_crew" });
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#section !== "team" || this.#session.profile() === null) return;
    if (answer.ok) {
      this.#team.notice = "Role saved.";
    } else {
      this.#team.error = answer.status === 409 ? LAST_OWNER_WORDS : answer.error;
    }
    await this.#loadTeam();
  }

  async #setUserStatus(id: string, status: string): Promise<void> {
    if (id === "" || (status !== "active" && status !== "inactive")) return;
    const target = this.#team.users.find((u) => u.id === id);
    if (target !== undefined && target.status === "pending") return;
    this.#team.notice = "";
    this.#team.error = "";
    this.requestUpdate();
    const answer = await tenantTeam.tenantUpdateUserStatus(this.#client, id, { status });
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#section !== "team" || this.#session.profile() === null) return;
    if (answer.ok) {
      this.#team.notice = status === "inactive" ? "The member is deactivated. They cannot sign in until reactivated." : "The member is active again.";
    } else {
      this.#team.error = answer.status === 409 ? LAST_OWNER_WORDS : answer.error;
    }
    await this.#loadTeam();
  }

  async #createInvite(): Promise<void> {
    const email = this.#team.inviteEmail.trim();
    if (email === "" || this.#team.inviteBusy) return;
    this.#team.inviteBusy = true;
    this.#team.notice = "";
    this.#team.error = "";
    this.requestUpdate();
    const answer = await tenantTeam.tenantCreateInvite(this.#client, { email, role: this.#team.inviteRole as "account_admin" | "buyer" | "buyer_no_pay" | "ap_finance" | "field_crew" });
    this.#team.inviteBusy = false;
    if (!answer.ok && answer.status === 401) {
      await this.#signOutOnWriteExpiry();
      return;
    }
    if (this.#section !== "team" || this.#session.profile() === null) return;
    if (answer.ok) {
      this.#team.inviteEmail = "";
      const field = this.renderRoot.querySelector<HTMLInputElement>("input[data-field=invite-email]");
      if (field !== null) field.value = "";
      this.#team.notice = "Invite created. The dealer's email carries the link.";
      await this.#loadTeam();
    } else {
      this.#team.error = answer.error;
    }
    this.requestUpdate();
  }

  async #loadProjects(): Promise<void> {
    const sequence = (this.#projectsSeq += 1);
    this.#projects = { phase: "loading", items: this.#projects.items };
    this.requestUpdate();
    const answer = await this.#port.listProjects();
    if (sequence !== this.#projectsSeq || this.#session.profile() === null || this.#section !== "projects") return;
    if (answer.ok) {
      this.#projects = { phase: answer.value.length === 0 ? "empty" : "ready", items: answer.value };
    } else if (answer.status === 401) {
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else if (answer.status === 403) {
      this.#projects = { phase: "refusal", items: [] };
    } else {
      this.#projects = { phase: "error", items: [] };
    }
    this.requestUpdate();
  }

  #openProject(id: string): void {
    if (id === "") return;
    this.#project = { id, phase: "loading", data: null };
    this.requestUpdate();
    void this.#loadProject(id);
  }

  async #loadProject(id: string): Promise<void> {
    const sequence = (this.#projectSeq += 1);
    this.#project = { id, phase: "loading", data: null };
    this.requestUpdate();
    const answer = await this.#port.getProject(id);
    if (sequence !== this.#projectSeq || this.#session.profile() === null) return;
    if (answer.ok) {
      this.#project = { id, phase: "ready", data: answer.value };
    } else if (answer.status === 401) {
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else if (answer.status === 403) {
      this.#project = { id, phase: "refusal", data: null };
    } else {
      this.#project = { id, phase: "error", data: null };
    }
    this.requestUpdate();
  }

  #title(): string {
    if (this.#section === "orders") {
      if (this.#order !== null) return "Order";
      return ordersSurfaceOf(this.#surface)?.label ?? "Orders";
    }
    if (this.#section === "projects") {
      if (this.#project !== null) return this.#project.data?.name ?? "Project";
      return "Projects";
    }
    if (this.#section === "team") return "Team";
    return "Account";
  }

  #renderBody(): TemplateResult {
    if (this.#section === "orders") {
      if (this.#order !== null) {
        const order = this.#order;
        const mayEdit = this.#session.can("edit_delivery_instructions");
        const mayRequest = this.#session.can("create_orders");
        return orderView({
          detail: order.detail,
          phase: order.phase,
          today: houseToday(),
          instructions:
            order.phase === "ready" && order.detail !== null
              ? instructionsCard({
                  detail: order.detail,
                  editable: instructionsEditable(order.detail, mayEdit),
                  note: instructionsNote(order.detail, mayEdit),
                  countWords: instructionsCountWords(order.instructions.draft),
                  dirty: order.instructions.draft !== order.detail.delivery_instructions,
                  busy: order.instructions.busy,
                  saved: order.instructions.saved,
                  error: order.instructions.error,
                })
              : undefined,
          returns:
            order.phase === "ready"
              ? returnsCard({
                  mayRequest,
                  requests: order.returns.requests,
                  eligibility: order.returns.eligibility,
                  form: order.returns.form,
                  notice: order.returns.notice,
                })
              : undefined,
        });
      }
      return ordersList({
        phase: this.#orders.phase,
        orders: this.#orders.items,
        emptyWords: ordersSurfaceOf(this.#surface)?.emptyWords ?? "",
        errorWords: this.#orders.errorWords,
        truncated: this.#orders.truncated,
      });
    }
    if (this.#section === "projects") {
      if (this.#project !== null) {
        return this.#project.phase === "ready" && this.#project.data !== null
          ? projectView(this.#project.data)
          : projectStates(this.#project.phase);
      }
      return projectsList({ phase: this.#projects.phase, projects: this.#projects.items });
    }
    if (this.#section === "team") {
      return teamView({
        phase: this.#team.phase,
        users: this.#team.users,
        invites: this.#team.invites,
        currentUserId: this.#session.profile()?.userId ?? "",
        roleDraft: this.#team.roleDraft,
        inviteBusy: this.#team.inviteBusy,
        inviteEmail: this.#team.inviteEmail,
        notice: this.#team.notice,
        error: this.#team.error,
      });
    }
    if (this.#section === "account") {
      return accountView({
        phase: this.#account.phase,
        me: this.#account.me,
        addresses: this.#account.addresses,
        address: this.#account.addressPhase === "ready" ? this.#account.address : null,
        nameDirty: this.#account.nameDirty,
        profileBusy: this.#account.profileBusy,
        profileSaved: this.#account.profileSaved,
        profileError: this.#account.profileError,
        passwordBusy: this.#account.passwordBusy,
        passwordError: this.#account.passwordError,
      });
    }
    return sectionPlaceholder();
  }

  render(): TemplateResult {
    if (!this.#inFrame()) return html`<hh-pro-signin></hh-pro-signin>`;
    const profile = this.#session.profile();
    const held = CAPABILITIES.filter((capability) => this.#session.can(capability)).join(" ");
    const sections = sectionsFor((capability) => this.#session.can(capability));
    return html`<hh-pro-frame
      data-app="field"
      data-surface="${this.#surface}"
      data-dealer="${this.#dealerName}"
      data-role="${profile === null ? (this.#personaRole ?? "") : roleLabel(profile.role)}"
      data-capabilities="${held}"
    >
      <div class="stack">
        <div class="topline">
          <h1 data-testid="surface-title">${this.#title()}</h1>
          ${this.#order === null ? sectionNav(sections, this.#section) : nothing}
        </div>
        ${this.#renderBody()}
      </div>
    </hh-pro-frame>`;
  }
}

defineElement("pro-field-app", ProFieldApp);
