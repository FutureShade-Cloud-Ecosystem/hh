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
// words until the auth lead's go fills it). The shells cycle owns the real
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
import { ApiClient, tenantAuth, type TenantOrder, type TenantOrderDetail, type TenantReturnEligibility, type TenantReturnRequest } from "@hardscapehouse/api-client";
import {
  CAPABILITIES,
  InMemoryTenantSession,
  appOf,
  realExchangeAdapter,
  roleLabel,
  stubAdapter,
  tenantClient,
  type TenantSession,
} from "@hardscapehouse/pro-kit";
import "@hardscapehouse/pro-kit/src/frame.js";
import "@hardscapehouse/pro-kit/src/signin.js";
import "@hardscapehouse/pro-kit/src/state-view.js";
import type { SignInSubmit } from "@hardscapehouse/pro-kit/src/signin.js";
import type { SurfaceSelect } from "@hardscapehouse/pro-kit/src/frame.js";
import { liveFieldPort, type FieldPort } from "./port.js";
import { ordersSurfaceOf, sectionsFor, type Section } from "./sections.js";
import { houseToday, instructionsEditable, instructionsNote } from "./timeline.js";
import { returnRequestLines } from "./returns.js";
import { instructionsCard, orderView, ordersList, returnsCard, sectionNav, sectionPlaceholder, type EligibilityState, type Phase, type ReturnFormState } from "./views.js";
import { fieldStyles } from "./styles.js";

interface OrdersState {
  phase: Phase;
  items: TenantOrder[];
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

const CANNOT_RETURN_WORDS = "This order cannot be returned through Pro. Ask the dealer to arrange it.";
const RETURN_FAULT_WORDS = "Returns did not load. Try again in a moment.";

function freshInstructions(): InstructionsState {
  return { draft: "", busy: false, saved: false, error: "", prefill: null };
}

function freshReturns(): ReturnsState {
  return { requests: null, listError: false, eligibility: { phase: "idle", data: null, words: "", canRetry: false }, form: { qty: new Map(), reason: "DAMAGED", note: "", busy: false, error: "" }, notice: "" };
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
  #orders: OrdersState = { phase: "idle", items: [] };
  #order: OrderState | null = null;
  #ordersSeq = 0;
  #orderSeq = 0;
  #unsubscribe: (() => void) | null = null;

  constructor() {
    super();
    const adapter = import.meta.env.DEV ? stubAdapter() : realExchangeAdapter();
    this.#session = new InMemoryTenantSession({ adapter });
    this.#client = tenantClient(this.#session);
    this.#port = liveFieldPort(this.#client);
  }

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    const action = control?.getAttribute("data-action");
    if (action === "retry") {
      if (this.#order !== null) void this.#loadOrder(this.#order.id);
      else void this.#loadOrders();
      return;
    }
    if (action === "open-order") {
      this.#openOrder(control?.getAttribute("data-order-id") ?? "");
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
    void this.#signIn((event as CustomEvent<SignInSubmit>).detail);
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
    if (target !== undefined) window.location.assign(target.path);
  };

  readonly #onSignOut = (): void => {
    void this.#session.signOut();
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
    this.addEventListener("pro-signin-submit", this.#onSignIn);
    this.addEventListener("pro-surface-select", this.#onSelect);
    this.addEventListener("pro-sign-out", this.#onSignOut);
    this.renderRoot.addEventListener("input", this.#onField);
    this.renderRoot.addEventListener("change", this.#onField);
    this.#unsubscribe = this.#session.subscribe(() => {
      if (this.#session.profile() === null) {
        this.#section = "orders";
        this.#surface = "tracking";
        this.#orders = { phase: "idle", items: [] };
        this.#order = null;
      }
      this.requestUpdate();
    });
    void this.#loadDealer();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("pro-signin-submit", this.#onSignIn);
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
    if (this.#session.profile() === null) root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", "light");
    const signin = this.renderRoot.querySelector("hh-pro-signin") as (HTMLElement & { dealerName: string; message: string; busy: boolean }) | null;
    if (signin !== null) {
      signin.dealerName = this.#dealerName;
      signin.message = this.#signinMessage;
      signin.busy = this.#signinBusy;
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
  }

  async #loadDealer(): Promise<void> {
    const answer = await tenantAuth.tenantGetConfig(this.#client);
    if (answer.ok) {
      this.#dealerName = answer.value.dealer_name;
      this.requestUpdate();
    }
  }

  async #signIn(input: SignInSubmit): Promise<void> {
    this.#signinBusy = true;
    this.#signinMessage = "";
    this.requestUpdate();
    const result = await this.#session.signIn(input.email, input.password);
    this.#signinBusy = false;
    if (result.ok) {
      this.#signinMessage = "";
      void this.#loadOrders();
    } else {
      this.#signinMessage = result.error;
    }
    this.requestUpdate();
  }

  async #loadOrders(): Promise<void> {
    const sequence = (this.#ordersSeq += 1);
    this.#orders = { phase: "loading", items: this.#orders.items };
    this.requestUpdate();
    const answer = await this.#port.listOrders();
    if (sequence !== this.#ordersSeq || this.#session.profile() === null) return;
    if (answer.ok) {
      const filter = ordersSurfaceOf(this.#surface)?.fulfillments ?? null;
      const items = filter === null ? answer.value : answer.value.filter((order) => filter.includes(order.fulfillment));
      this.#orders = { phase: items.length === 0 ? "empty" : "ready", items };
    } else if (answer.status === 401) {
      // The session is gone (expired, or the person was deactivated): sign out and say so, never blame the role.
      this.#signinMessage = "You are signed out. Sign in again.";
      await this.#session.signOut();
      return;
    } else if (answer.status === 403) {
      this.#orders = { phase: "refusal", items: [] };
    } else {
      this.#orders = { phase: "error", items: [] };
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

  async #saveInstructions(): Promise<void> {
    const order = this.#order;
    if (order === null || order.detail === null || order.instructions.busy) return;
    order.instructions.busy = true;
    order.instructions.error = "";
    this.requestUpdate();
    const answer = await this.#port.setDeliveryInstructions(order.id, order.instructions.draft);
    if (this.#order !== order) return;
    order.instructions.busy = false;
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
    if (this.#order !== order || this.#session.profile() === null) return;
    if (answer.ok) {
      order.returns.eligibility = { phase: "done", data: answer.value, words: "", canRetry: false };
    } else if (answer.status === 404) {
      // The core answers not found for anything that was not a counter sale, on purpose
      // (IDOR safe), so this is a plain no, not a fault to retry.
      order.returns.eligibility = { phase: "error", data: null, words: CANNOT_RETURN_WORDS, canRetry: false };
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
    if (this.#order !== order) return;
    order.returns.form.busy = false;
    if (answer.ok) {
      this.#closeReturnFlow();
      order.returns.notice = `Return request ${answer.value.request_no} sent. The dealer decides it.`;
      void this.#loadReturns();
    } else {
      order.returns.form.error = answer.error;
    }
    this.requestUpdate();
  }

  #selectSection(to: string): void {
    if (to !== "orders" && to !== "projects" && to !== "team" && to !== "account") return;
    if (to === this.#section) return;
    this.#section = to;
    this.#order = null;
    this.requestUpdate();
  }

  #title(): string {
    if (this.#section === "orders") {
      if (this.#order !== null) return "Order";
      return ordersSurfaceOf(this.#surface)?.label ?? "Orders";
    }
    if (this.#section === "projects") return "Projects";
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
      });
    }
    return sectionPlaceholder();
  }

  render(): TemplateResult {
    const profile = this.#session.profile();
    if (profile === null) return html`<hh-pro-signin></hh-pro-signin>`;
    const held = CAPABILITIES.filter((capability) => this.#session.can(capability)).join(" ");
    const sections = sectionsFor((capability) => this.#session.can(capability));
    return html`<hh-pro-frame
      data-app="field"
      data-surface="${this.#surface}"
      data-dealer="${this.#dealerName}"
      data-role="${roleLabel(profile.role)}"
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
