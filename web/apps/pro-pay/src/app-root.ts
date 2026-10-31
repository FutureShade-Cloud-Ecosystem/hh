// The Pro Pay root: the Pro kit's session, tenant client, sign in screen and frame, around this app's own surfaces
// (Invoices with the pay sheet, Payment methods, Statements). The kit's ProAppBase runs one landing list; Pro Pay has
// three surfaces and a sheet, so this root carries its own small copy of that wiring and the kit stays untouched.
//
// Money rules held here (the logic is pay-core.ts, which this file only calls): one PaySender per signed in session, so
// one key per attempt and one request per press; an unknown outcome is held, never retried with a new key; no write ever
// queues; nothing is kept in web storage; card data never enters this app (the processor's frame hands up a token).
//
// Position gate: no bound property or event in any template. The root listens on itself and acts on data-action and
// data-id; the child elements and the hosted frame are told what they show by assignment after render (updated()).
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import {
  tenantAuth,
  tenantBilling,
  tenantDashboard,
  tenantPayments,
  type ApiClient,
  type Result,
  type TenantBillingSummary,
  type TenantInvoice,
  type TenantPayment,
  type TenantPaymentMethod,
} from "@hardscapehouse/api-client";
import { houseDayInZone, houseZone, serveHouseZoneFrom } from "@hardscapehouse/pos-kit/src/ported/host-facts.js";
import { CAPABILITIES, InMemoryTenantSession, chipStyles, monoStyles, realExchangeAdapter, demoTenantAdapter, signInFromCard, demoRoleFromAddress, personaRoleHeld, otherAppAddress, roleLabel, stubAdapter, surfaceOf, tenantClient, appOf, type Capability, type TenantSession } from "@hardscapehouse/pro-kit";
import "@hardscapehouse/pro-kit/src/frame.js";
import "@hardscapehouse/pro-kit/src/signin.js";
import "@hardscapehouse/pro-kit/src/state-view.js";
import type { SurfaceSelect } from "@hardscapehouse/pro-kit/src/frame.js";
import type { SignInMethodChoice, SignInSubmit } from "@hardscapehouse/pro-kit/src/signin.js";
import type { HHProSignIn } from "@hardscapehouse/pro-kit/src/signin.js";
import { PaySender, isPayable, payoffCents, readHostedToken, type PayBody, type PayOutcome } from "./pay-core.js";
import { hostedFieldAddress } from "./hosted-field.js";
import { invoiceDetailScreen, invoicesScreen, methodsScreen, sheetView, statementsScreen, type Load, type Sheet, type ViewState } from "./views.js";
import { screenStyles } from "./styles.js";

type SurfaceId = "invoices" | "payment-methods" | "statements";

const NEEDS: Readonly<Record<SurfaceId, Capability>> = { invoices: "view_billing", "payment-methods": "manage_payment_methods", statements: "view_billing" };

/** The dealer's own day (the house zone the kit's words use), as YYYY-MM-DD, for aging. */
function houseToday(): string {
  return houseDayInZone(houseZone());
}

/** A 401 is a lapsed session (sign out and say so, never blame the role); a 403 is the role's refusal; anything else is an error. */
type Failed = "refusal" | "error" | "lapsed";

function failure<T>(answer: Result<T>): Failed {
  if (answer.ok) return "error";
  return answer.status === 401 ? "lapsed" : answer.status === 403 ? "refusal" : "error";
}

const LAPSED_WORDS = "You are signed out. Sign in again.";

export class ProPayApp extends HHElement {
  static styles = [baseStyles, chipStyles, monoStyles, screenStyles];

  readonly #session: TenantSession;
  readonly #client: ApiClient;
  readonly #sender: PaySender;
  #surface: SurfaceId = "invoices";
  #load: Load = "loading";
  #errorWords = "";
  #loads = 0;
  #dealerName = "";
  #message = "";
  #busy = false;
  #personaRole: string | null = null;
  #unsubscribe: (() => void) | null = null;

  #invoices: readonly TenantInvoice[] = [];
  #summary: TenantBillingSummary | null = null;
  #selected = new Set<string>();
  #open: ViewState["openInvoice"] = null;
  #sheet: Sheet | null = null;
  #heldBy: string | null = null;
  #methods: readonly TenantPaymentMethod[] = [];
  #methodAdd: ViewState["methodAdd"] = { open: false, makeDefault: false, busy: false, words: "", nonce: null, frameAvailable: hostedFieldAddress() !== null };
  #confirmRemove: string | null = null;
  #methodWords = "";
  #payments: readonly TenantPayment[] = [];
  #framed: HTMLIFrameElement | null = null;

  constructor() {
    super();
    this.#session = new InMemoryTenantSession({
      adapter: import.meta.env.DEV ? stubAdapter() : import.meta.env.MODE === "demo" ? demoTenantAdapter() : realExchangeAdapter(),
      onCustody: () => this.requestUpdate(),
    });
    this.#client = tenantClient(this.#session);
    this.#sender = new PaySender(this.#client, { canPay: () => this.#session.can("pay_invoices") });
  }

  #inFrame(): boolean {
    return this.#session.current() !== null || this.#personaRole !== null;
  }

  // ---- events: one host listener, acting on data- names ----------------------------------------------------------

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action") ?? "";
    const id = control.getAttribute("data-id") ?? "";
    if (control instanceof HTMLButtonElement && control.disabled) return;
    this.#act(action, id);
  };

  readonly #onKey = (event: Event): void => {
    if ((event as KeyboardEvent).key === "Escape" && this.#sheet !== null && this.#sheet.phase !== "sending") this.#closeSheet();
  };

  // The hosted frame's token. The sender must be the frame this app mounted, and the shape is checked; anything else is ignored.
  readonly #onMessage = (event: MessageEvent): void => {
    const frame = this.renderRoot.querySelector("iframe[data-hosted]") as HTMLIFrameElement | null;
    if (frame === null || event.source !== frame.contentWindow) return;
    const token = readHostedToken(event.data);
    if (token === null) return;
    if (this.#sheet !== null && this.#sheet.choice === "new" && this.#sheet.phase === "choose") this.#sheet = { ...this.#sheet, nonce: token, words: "" };
    else if (this.#methodAdd.open) this.#methodAdd = { ...this.#methodAdd, nonce: token, words: "" };
    this.requestUpdate();
  };

  readonly #onSignIn = (event: Event): void => {
    void this.#signIn((event as CustomEvent<SignInSubmit | SignInMethodChoice>).detail);
  };

  readonly #onSelect = (event: Event): void => {
    const { app, surface } = (event as CustomEvent<SurfaceSelect>).detail;
    if (app === "pay") {
      if (surface === "invoices" || surface === "payment-methods" || surface === "statements") this.#go(surface);
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
    this.addEventListener("keydown", this.#onKey);
    this.addEventListener("pro-signin-submit", this.#onSignIn);
    this.addEventListener("pro-signin-method", this.#onSignIn);
    this.addEventListener("pro-surface-select", this.#onSelect);
    this.addEventListener("pro-sign-out", this.#onSignOut);
    window.addEventListener("message", this.#onMessage);
    this.#unsubscribe = this.#session.subscribe(() => {
      const profile = this.#session.profile();
      if (profile === null) this.#reset();
      else if (this.#heldBy !== null && this.#heldBy !== profile.userId) {
        // A different person signed in: the held payment and the receipt were not theirs.
        this.#sender.forget();
        this.#heldBy = null;
      }
      this.requestUpdate();
    });
    void this.#loadDealer();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("keydown", this.#onKey);
    this.removeEventListener("pro-signin-submit", this.#onSignIn);
    this.removeEventListener("pro-signin-method", this.#onSignIn);
    this.removeEventListener("pro-surface-select", this.#onSelect);
    this.removeEventListener("pro-sign-out", this.#onSignOut);
    window.removeEventListener("message", this.#onMessage);
    this.#unsubscribe?.();
    this.#unsubscribe = null;
    super.disconnectedCallback();
  }

  /** Signed out: forget everything shown, selected or in a sheet. A held payment stays with the sender for the same person. */
  #reset(): void {
    this.#surface = "invoices";
    this.#load = "loading";
    this.#errorWords = "";
    this.#loads += 1;
    this.#invoices = [];
    this.#summary = null;
    this.#selected = new Set();
    this.#open = null;
    this.#sheet = null;
    this.#sender.clearSettled();
    this.#methods = [];
    this.#payments = [];
    this.#methodAdd = { ...this.#methodAdd, open: false, nonce: null, words: "", busy: false };
    this.#confirmRemove = null;
    this.#methodWords = "";
  }

  async #loadDealer(): Promise<void> {
    const answer = await tenantAuth.tenantGetConfig(this.#client);
    if (answer.ok) {
      serveHouseZoneFrom(answer.value);
      this.#dealerName = answer.value.dealer_name;
      this.requestUpdate();
    }
  }

  async #signIn(input: SignInSubmit | SignInMethodChoice): Promise<void> {
    this.#busy = true;
    this.#message = "";
    this.#errorWords = "";
    this.#personaRole = personaRoleHeld(input, import.meta.env.MODE === "demo" || import.meta.env.DEV);
    this.#load = "loading";
    this.requestUpdate();
    const result = await signInFromCard(this.#session, input);
    this.#busy = false;
    if (result.ok) {
      this.#message = "";
      void this.#loadSurface();
    } else if (result.status === 401 || result.status === 403 || !this.#inFrame()) {
      this.#personaRole = null;
      this.#message = result.error;
    } else {
      this.#load = "error";
      this.#errorWords = result.error;
    }
    this.requestUpdate();
  }

  async #retrySession(): Promise<void> {
    if (this.#session.current() === null && this.#personaRole !== null) {
      await this.#signIn({ method: "persona", roleLabel: this.#personaRole });
      return;
    }
    this.#load = "loading";
    this.#errorWords = "";
    this.requestUpdate();
    const result = await this.#session.check();
    if (result.ok) {
      this.#message = "";
      void this.#loadSurface();
    } else if (result.status === 401 || result.status === 403 || !this.#inFrame()) {
      this.#personaRole = null;
      this.#message = result.error;
    } else {
      this.#load = "error";
      this.#errorWords = result.error;
    }
    this.requestUpdate();
  }

  // ---- loading ---------------------------------------------------------------------------------------------------

  #go(surface: SurfaceId): void {
    if (surface === this.#surface && this.#open === null) return;
    this.#surface = surface;
    this.#open = null;
    this.#confirmRemove = null;
    this.#methodWords = "";
    void this.#loadSurface();
  }

  async #loadSurface(): Promise<void> {
    const sequence = (this.#loads += 1);
    const surface = this.#surface;
    if (!this.#session.can(NEEDS[surface])) {
      this.#load = "refusal";
      this.requestUpdate();
      return;
    }
    this.#load = "loading";
    this.#errorWords = "";
    this.requestUpdate();
    const state = surface === "invoices" ? await this.#fetchInvoices() : surface === "payment-methods" ? await this.#fetchMethods() : await this.#fetchStatements();
    if (sequence !== this.#loads || this.#session.profile() === null) return;
    if (state === "lapsed") {
      await this.#lapse();
      return;
    }
    this.#load = state;
    this.requestUpdate();
  }

  async #fetchInvoices(): Promise<Load | "lapsed"> {
    const [summary, rows] = await Promise.all([tenantDashboard.tenantGetBillingSummary(this.#client), this.#allInvoices()]);
    if (!rows.ok) {
      if (rows.status === undefined) this.#errorWords = "Pro is not reachable right now. Try again in a moment.";
      return failure(rows);
    }
    this.#invoices = rows.value;
    this.#summary = summary.ok ? summary.value : null;
    const payable = new Set(rows.value.filter(isPayable).map((i) => i.id));
    this.#selected = new Set([...this.#selected].filter((id) => payable.has(id)));
    return "ready";
  }

  async #allInvoices(): Promise<Result<TenantInvoice[]>> {
    const rows: TenantInvoice[] = [];
    let cursor: string | undefined;
    for (let page = 0; page < 25; page += 1) {
      const answer = await tenantBilling.tenantListInvoices(this.#client, cursor === undefined ? { limit: 200 } : { cursor, limit: 200 });
      if (!answer.ok) return answer;
      rows.push(...answer.value.items);
      cursor = answer.value.next_cursor ?? undefined;
      if (cursor === undefined) break;
    }
    return { ok: true, value: rows };
  }

  async #fetchMethods(): Promise<Load | "lapsed"> {
    const answer = await tenantPayments.tenantListPaymentMethods(this.#client);
    if (!answer.ok) {
      if (answer.status === undefined) this.#errorWords = "Pro is not reachable right now. Try again in a moment.";
      return failure(answer);
    }
    this.#methods = answer.value;
    return "ready";
  }

  async #fetchStatements(): Promise<Load | "lapsed"> {
    const [payments, summary, rows] = await Promise.all([tenantPayments.tenantListPayments(this.#client), tenantDashboard.tenantGetBillingSummary(this.#client), this.#allInvoices()]);
    if (!payments.ok) {
      if (payments.status === undefined) this.#errorWords = "Pro is not reachable right now. Try again in a moment.";
      return failure(payments);
    }
    this.#payments = payments.value;
    this.#summary = summary.ok ? summary.value : null;
    if (rows.ok) this.#invoices = rows.value;
    return "ready";
  }

  async #openInvoice(id: string): Promise<void> {
    this.#open = { id, load: "loading", detail: null };
    this.requestUpdate();
    const answer = await tenantBilling.tenantGetInvoice(this.#client, id);
    if (this.#open === null || this.#open.id !== id) return;
    if (!answer.ok && failure(answer) === "lapsed") {
      await this.#lapse();
      return;
    }
    this.#open = answer.ok ? { id, load: "ready", detail: answer.value } : { id, load: failure(answer) as Load, detail: null };
    this.requestUpdate();
  }

  /** The session is gone (expired, or the person was deactivated): sign out and say so. */
  async #lapse(): Promise<void> {
    this.#personaRole = null;
    this.#message = LAPSED_WORDS;
    await this.#session.signOut();
  }

  // ---- actions ---------------------------------------------------------------------------------------------------

  #act(action: string, id: string): void {
    switch (action) {
      case "retry":
        if (this.#session.profile() === null) void this.#retrySession();
        else void this.#loadSurface();
        break;
      case "open":
        void this.#openInvoice(id);
        break;
      case "back":
        this.#open = null;
        this.requestUpdate();
        break;
      case "toggle":
        this.#toggle(id);
        break;
      case "select-all":
        this.#selectAll();
        break;
      case "open-sheet":
        void this.#openSheet([...this.#selected]);
        break;
      case "pay-this":
        void this.#openSheet([id]);
        break;
      case "check-waiting":
        void this.#reopenWaiting();
        break;
      case "close-sheet":
        this.#closeSheet();
        break;
      case "choose":
        this.#choose(id);
        break;
      case "pay":
        void this.#pay();
        break;
      case "done":
        this.#done();
        break;
      case "open-add":
        this.#methodAdd = { ...this.#methodAdd, open: true, nonce: null, words: "", makeDefault: this.#methods.length === 0 };
        this.requestUpdate();
        break;
      case "cancel-add":
        this.#methodAdd = { ...this.#methodAdd, open: false, nonce: null, words: "" };
        this.requestUpdate();
        break;
      case "toggle-default":
        this.#methodAdd = { ...this.#methodAdd, makeDefault: !this.#methodAdd.makeDefault };
        this.requestUpdate();
        break;
      case "save-method":
        void this.#saveMethod();
        break;
      case "make-default":
        void this.#methodStep(() => tenantPayments.tenantSetDefaultPaymentMethod(this.#client, id), "The default did not change. Try again.");
        break;
      case "remove":
        this.#confirmRemove = id;
        this.requestUpdate();
        break;
      case "keep":
        this.#confirmRemove = null;
        this.requestUpdate();
        break;
      case "confirm-remove":
        this.#confirmRemove = null;
        void this.#methodStep(() => tenantPayments.tenantRemovePaymentMethod(this.#client, id), "The method was not removed. Try again.");
        break;
      default:
        break;
    }
  }

  #toggle(id: string): void {
    if (!this.#session.can("pay_invoices")) return;
    const row = this.#invoices.find((i) => i.id === id);
    if (row === undefined || !isPayable(row)) return;
    const next = new Set(this.#selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    this.#selected = next;
    this.requestUpdate();
  }

  #selectAll(): void {
    if (!this.#session.can("pay_invoices")) return;
    const payable = this.#invoices.filter(isPayable);
    this.#selected = payable.every((i) => this.#selected.has(i.id)) ? new Set() : new Set(payable.map((i) => i.id));
    this.requestUpdate();
  }

  /** The advisory sum of the core's own payable cents. The core derives the amount it charges; the receipt shows it. */
  #sumOf(ids: readonly string[]): number {
    return this.#invoices.filter((i) => ids.includes(i.id)).reduce((sum, i) => sum + payoffCents(i), 0);
  }

  async #openSheet(ids: string[]): Promise<void> {
    if (!this.#session.can("pay_invoices") || ids.length === 0) return;
    if (this.#sender.waiting !== null) {
      await this.#reopenWaiting();
      return;
    }
    this.#sheet = { phase: "choose", ids, expectedCents: this.#sumOf(ids), choice: "", nonce: null, methods: [], methodsNote: "", words: "", receipt: null, differs: false, frameAvailable: hostedFieldAddress() !== null };
    this.requestUpdate();
    const answer = await tenantPayments.tenantListPaymentMethods(this.#client);
    if (!answer.ok && failure(answer) === "lapsed") {
      await this.#lapse();
      return;
    }
    if (this.#sheet === null || this.#sheet.phase !== "choose") return;
    if (answer.ok) {
      const preferred = answer.value.find((m) => m.is_default) ?? answer.value[0];
      this.#sheet = { ...this.#sheet, methods: answer.value, choice: preferred === undefined ? "" : preferred.id };
    } else {
      this.#sheet = { ...this.#sheet, methodsNote: "Your saved methods could not be listed. You can pay with a new card." };
    }
    this.requestUpdate();
  }

  /** A held attempt comes back as a sheet in its unknown phase, carrying the same body so Check payment is the same request. */
  async #reopenWaiting(): Promise<void> {
    const waiting = this.#sender.waiting;
    if (waiting === null) return;
    const methods = this.#sheet?.methods ?? this.#methods;
    this.#sheet = {
      phase: "unknown",
      ids: [...waiting.invoiceIds],
      expectedCents: waiting.expectedCents,
      choice: waiting.methodId ?? "new",
      nonce: waiting.nonce ?? null,
      methods,
      methodsNote: "",
      words: "We could not confirm this payment. It may have gone through. Check it before paying again.",
      receipt: null,
      differs: false,
      frameAvailable: hostedFieldAddress() !== null,
    };
    this.requestUpdate();
  }

  #choose(id: string): void {
    const sheet = this.#sheet;
    if (sheet === null || sheet.phase !== "choose") return;
    this.#sheet = { ...sheet, choice: id, words: "", nonce: id === "new" ? sheet.nonce : null };
    this.requestUpdate();
  }

  #closeSheet(): void {
    if (this.#sheet === null || this.#sheet.phase === "sending") return;
    if (this.#sheet.phase === "paid") return this.#done();
    // A held attempt stays held: the invoices screen offers Check payment, and the same key and body ride the check.
    this.#sheet = null;
    this.requestUpdate();
  }

  #done(): void {
    this.#sheet = null;
    this.#sender.clearSettled();
    this.#selected = new Set();
    this.#open = null;
    void this.#loadSurface();
  }

  async #pay(): Promise<void> {
    const sheet = this.#sheet;
    if (sheet === null || sheet.phase === "sending" || sheet.phase === "paid") return;
    const body: PayBody = sheet.choice === "new" ? { invoiceIds: sheet.ids, nonce: sheet.nonce ?? "" } : { invoiceIds: sheet.ids, methodId: sheet.choice };
    if (sheet.choice === "" || (sheet.choice === "new" && sheet.nonce === null)) return;
    this.#sheet = { ...sheet, phase: "sending", words: "" };
    this.requestUpdate();
    const by = this.#session.profile()?.userId ?? null;
    const outcome = await this.#sender.pay(body, sheet.expectedCents);
    this.#heldBy = this.#sender.waiting === null ? null : (this.#heldBy ?? by);
    this.#settle(body, outcome);
  }

  #settle(body: PayBody, outcome: PayOutcome): void {
    const sheet = this.#sheet;
    if (this.#session.profile() === null) return;
    if (outcome.kind === "refused" && outcome.status === 401) {
      void this.#lapse();
      return;
    }
    if (sheet === null) {
      // The sheet was closed while the send was out: the screen still learns what happened.
      if (outcome.kind === "paid" || outcome.kind === "unknown") this.#sheet = this.#sheetFor(body, outcome);
      this.requestUpdate();
      return;
    }
    switch (outcome.kind) {
      case "paid":
        this.#sheet = { ...sheet, phase: "paid", receipt: outcome.payment, differs: outcome.differs, words: "" };
        break;
      case "unknown":
        this.#sheet = { ...sheet, phase: "unknown", words: outcome.words };
        break;
      case "declined":
        // No money moved. A card token is spent by a decline, so the person adds a card again.
        this.#sheet = { ...sheet, phase: "choose", words: outcome.words, nonce: null };
        break;
      case "refused":
      case "blocked":
        // A held attempt stays the held one: the sheet keeps offering Check payment, never a new choice.
        this.#sheet = { ...sheet, phase: this.#sender.waiting !== null ? "unknown" : "choose", words: outcome.words };
        break;
    }
    // A 400 or a 404 says the request was not accepted, and a selection may no longer be payable: read the books again.
    if (outcome.kind === "refused" && (outcome.status === 400 || outcome.status === 404)) void this.#loadSurface();
    this.requestUpdate();
  }

  #sheetFor(body: PayBody, outcome: PayOutcome): Sheet {
    return {
      phase: outcome.kind === "paid" ? "paid" : "unknown",
      ids: [...body.invoiceIds],
      expectedCents: this.#sumOf(body.invoiceIds),
      choice: body.methodId ?? "new",
      nonce: body.nonce ?? null,
      methods: this.#methods,
      methodsNote: "",
      words: outcome.kind === "unknown" ? outcome.words : "",
      receipt: outcome.kind === "paid" ? outcome.payment : null,
      differs: outcome.kind === "paid" ? outcome.differs : false,
      frameAvailable: hostedFieldAddress() !== null,
    };
  }

  async #saveMethod(): Promise<void> {
    const add = this.#methodAdd;
    if (add.nonce === null || add.busy) return;
    this.#methodAdd = { ...add, busy: true, words: "" };
    this.requestUpdate();
    // A token is one time: it is handed over once and never kept for a retry.
    const answer = await tenantPayments.tenantAddPaymentMethod(this.#client, { nonce: add.nonce, make_default: add.makeDefault });
    if (answer.ok) {
      this.#methodAdd = { ...this.#methodAdd, open: false, busy: false, nonce: null, words: "" };
      await this.#fetchMethods();
    } else if (failure(answer) === "lapsed") {
      await this.#lapse();
      return;
    } else {
      this.#methodAdd = { ...this.#methodAdd, busy: false, nonce: null, words: "The card was not added. Add it again." };
    }
    this.requestUpdate();
  }

  async #methodStep(step: () => Promise<Result<unknown>>, failed: string): Promise<void> {
    this.#methodWords = "";
    const answer = await step();
    if (!answer.ok && failure(answer) === "lapsed") {
      await this.#lapse();
      return;
    }
    if (!answer.ok) this.#methodWords = failed;
    await this.#fetchMethods();
    this.requestUpdate();
  }

  // ---- render ----------------------------------------------------------------------------------------------------

  protected override updated(changed: Map<PropertyKey, unknown>): void {
    super.updated(changed);
    const root = document.documentElement;
    root.setAttribute("data-brand", "hh-pro");
    if (!this.#inFrame()) root.removeAttribute("data-theme");
    else root.setAttribute("data-theme", "light");
    const signin = this.renderRoot.querySelector("hh-pro-signin") as HHProSignIn | null;
    if (signin !== null) {
      signin.dealerName = this.#dealerName;
      signin.message = this.#message;
      signin.busy = this.#busy;
      const options = this.#session.options();
      signin.methods = options.methods;
      signin.refused = options.refused;
      signin.personas = options.personas;
    }
    // The hosted frame's address is assigned once, here: the position gate refuses a bound src.
    const frame = this.renderRoot.querySelector("iframe[data-hosted]") as HTMLIFrameElement | null;
    const address = hostedFieldAddress();
    if (frame !== null && address !== null && frame !== this.#framed) frame.src = address;
    this.#framed = frame;
    const sheet = this.renderRoot.querySelector(".sheet") as HTMLElement | null;
    if (sheet !== null && !sheet.contains(this.shadowRoot?.activeElement ?? null)) sheet.focus();
  }

  #state(): ViewState {
    return {
      today: houseToday(),
      canPay: this.#session.can("pay_invoices"),
      invoices: this.#invoices,
      summary: this.#summary,
      selected: this.#selected,
      openInvoice: this.#open,
      sheet: this.#sheet,
      waiting: this.#sender.waiting !== null,
      methods: this.#methods,
      methodAdd: this.#methodAdd,
      confirmRemove: this.#confirmRemove,
      methodWords: this.#methodWords,
      payments: this.#payments,
    };
  }

  #body(state: ViewState): TemplateResult {
    switch (this.#load) {
      case "ready":
        break;
      case "refusal":
        return html`<hh-pro-state data-state="refusal" data-testid="landing-state"></hh-pro-state>`;
      case "error":
        return html`<hh-pro-state data-state="error" data-testid="landing-state">${this.#errorWords !== "" ? this.#errorWords : "This did not load. Nothing was changed."}</hh-pro-state>`;
      default:
        return html`<hh-pro-state data-state="loading" data-testid="landing-state"></hh-pro-state>`;
    }
    switch (this.#surface) {
      case "invoices":
        if (state.openInvoice !== null) return invoiceDetailScreen(state);
        return state.invoices.length === 0
          ? html`<hh-pro-state data-state="empty" data-testid="landing-state">No invoices yet. They appear here when an order is billed.</hh-pro-state>`
          : invoicesScreen(state);
      case "payment-methods":
        return methodsScreen(state);
      default:
        return state.payments.length === 0
          ? html`<hh-pro-state data-state="empty" data-testid="landing-state">No payments yet. They appear here once an invoice is paid.</hh-pro-state>`
          : statementsScreen(state);
    }
  }

  render(): TemplateResult {
    if (!this.#inFrame()) return html`<hh-pro-signin></hh-pro-signin>`;
    const profile = this.#session.profile();
    const surface = surfaceOf("pay", this.#surface);
    const held = CAPABILITIES.filter((c) => this.#session.can(c)).join(" ");
    const state = this.#state();
    return html`<hh-pro-frame data-app="pay" data-surface="${this.#surface}" data-dealer="${this.#dealerName}" data-role="${profile === null ? (this.#personaRole ?? "") : roleLabel(profile.role)}" data-capabilities="${held}">
      <h1 data-testid="surface-title">${surface === undefined ? "" : surface.label}</h1>
      <div class="screen">${this.#body(state)}</div>
      ${state.sheet === null ? nothing : sheetView(state)}
    </hh-pro-frame>`;
  }
}

defineElement("pro-pay-app", ProPayApp);
