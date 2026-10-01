// The Counter Till's root element: the thin UI layer the house invariants ask
// for. It renders core and package state and calls package actions; nothing
// domain-shaped lives here (no prices, no money, no order logic: the till's
// sale work builds on this shell later in the cycle).
//
// State: the entry view's flows answer (loading, unavailable, or the flow
// rows), and the signed-in role once a card is tapped. Rendering is Lit, the
// design system supplies the tokens and the primitives, and the auth package
// supplies custody through its injected AccountSurface seam: this app never
// touches the token itself, the demo adapter owns the mint.
//
// The organisation header is the demo org by construction: the whole app is
// the demo's till, and the staff header provider refuses to send a request
// without it, so no call ever leaves this app unresolved.
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "./demo-account.js";
import {
  TillClient,
  DEMO_CARD_TOKEN,
  parseQuantity,
  type BranchRef,
  type CommittedSale,
  type CustomerRef,
  type ProductRef,
  type QuoteRef,
  type SaleRing,
  type StandingRef,
} from "./till-client.js";
import { formatMargin, formatMoney, parseAmountToCents } from "./money.js";
import { demoBanner, demoBannerStyles } from "./demo-banner.js";
import { entryStyles, renderEntry, type FlowsState } from "./views/entry.js";
import {
  checkoutStyles,
  renderReceipt,
  renderTender,
  type ReceiptView,
  type TenderChoice,
} from "./views/checkout.js";
import {
  tillStyles,
  renderTill,
  type CustomerPanel,
  type DrawerPanel,
  type PickedCustomer,
  type SearchPanel,
  type TicketLineView,
  type TicketPanel,
  type TillView,
} from "./views/till.js";

const DEMO_ORG = "hh-demo";

const FRESH_DRAWER_PANEL: DrawerPanel = {
  state: "resolving",
  how: null,
  branchName: null,
  station: null,
  float: null,
  message: null,
  busy: false,
};

const FRESH_SEARCH_PANEL: SearchPanel = { state: "idle", hits: [], message: null };

const WALK_IN_CUSTOMER: PickedCustomer = { kind: "walkin" };

/** One line on the ticket, with its own engine answers. */
interface TicketLine {
  key: string;
  product: ProductRef;
  qty: number;
  uom: string;
  quote: QuoteRef | null;
  marginBps: number | null;
}

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    demoBannerStyles,
    entryStyles,
    tillStyles,
    checkoutStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-page);
        color: var(--hh-text);
      }
      header {
        padding: var(--hh-s-16) var(--hh-s-20);
        border-bottom: 1px solid var(--hh-line);
      }
      .title-row {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--hh-s-16);
        flex-wrap: wrap;
      }
      h1 {
        font-size: var(--hh-s-16);
        margin: 0;
      }
      .signed-in {
        display: flex;
        align-items: center;
        gap: var(--hh-s-12);
      }
      .role-name {
        margin: 0;
        font-weight: 600;
      }
      main {
        padding: var(--hh-s-20);
      }
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  readonly #tillClient: TillClient;
  #flowsState: FlowsState = { kind: "loading" };
  #busy = false;
  #message: string | null = null;
  #till: { label: string } | null = null;
  #branch: BranchRef | null = null;
  #drawer: DrawerPanel = FRESH_DRAWER_PANEL;
  #search: SearchPanel = FRESH_SEARCH_PANEL;
  #customer: { kind: "walkin" } | { kind: "account"; customer: CustomerRef } = WALK_IN_CUSTOMER;
  #customerSearch: { state: "idle" | "loading" | "ready" | "refused"; hits: CustomerRef[]; message: string | null } = {
    state: "idle",
    hits: [],
    message: null,
  };
  #standing: StandingRef | null = null;
  #standingMessage: string | null = null;
  #lines: TicketLine[] = [];
  #pricing: "idle" | "pricing" | "priced" | "refused" = "idle";
  #pricingMessage: string | null = null;
  #pricedForType: string | null = null;
  #drawerId: string | null = null;
  #simulated: boolean | null = null;
  #tender: TenderChoice = "CASH";
  #committed: CommittedSale | null = null;
  #receiptBalance: string | null = null;
  #commitMessage: string | null = null;
  #commitBusy = false;

  constructor() {
    super();
    const client = new ApiClient({
      baseUrl: "",
      headers: authHeaderProvider({
        callerClass: "staff",
        sessions: this.#sessions,
        activeOrg: () => DEMO_ORG,
      }),
    });
    this.#account = new DemoAccount({ client });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    this.#tillClient = new TillClient(client);
  }

  connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule, carried for this app's own templates: listen on
    // the HOST and read the control off composedPath(), never @click in a
    // template and never event.target.closest, because the shadow boundary
    // retargets the target to this element (the design system's README holds
    // both halves).
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("submit", this.#onHostSubmit);
    void this.#loadFlows();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("submit", this.#onHostSubmit);
    super.disconnectedCallback();
  }

  readonly #onHostClick = (event: MouseEvent): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const flow = control.getAttribute("data-flow");
    if (control.getAttribute("data-action") === "choose" && typeof flow === "string" && flow !== "") {
      void this.#chooseFlow(flow);
    }
    if (control.getAttribute("data-action") === "sign-out") {
      void this.#signOut();
    }
    if (control.getAttribute("data-action") === "retry-drawer") {
      void this.#ensureDrawer();
    }
    if (control.getAttribute("data-action") === "add-line") {
      const productId = control.getAttribute("data-product-id");
      if (typeof productId === "string" && productId !== "") this.#addLine(productId);
    }
    if (control.getAttribute("data-action") === "remove-line") {
      const key = control.getAttribute("data-line-key");
      if (typeof key === "string" && key !== "") {
        this.#lines = this.#lines.filter((line) => line.key !== key);
        void this.#priceTicket();
      }
    }
    if (control.getAttribute("data-action") === "reprice") {
      void this.#repriceFromInputs();
    }
    if (control.getAttribute("data-action") === "pick-customer") {
      const customerId = control.getAttribute("data-customer-id");
      if (typeof customerId === "string" && customerId !== "") void this.#pickCustomer(customerId);
    }
    if (control.getAttribute("data-action") === "use-walkin") {
      this.#useWalkIn();
    }
    if (control.getAttribute("data-action") === "set-tender") {
      const tender = control.getAttribute("data-tender");
      if (tender === "CASH" || tender === "CARD" || tender === "ACCOUNT") {
        this.#tender = tender;
        this.requestUpdate();
      }
    }
    if (control.getAttribute("data-action") === "commit-sale") {
      void this.#commitSale();
    }
    if (control.getAttribute("data-action") === "clear-receipt") {
      this.#committed = null;
      this.#receiptBalance = null;
      this.#commitMessage = null;
      this.requestUpdate();
    }
  };

  readonly #onHostSubmit = (event: Event): void => {
    const form = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-form"));
    if (form === undefined) return;
    event.preventDefault();
    const kind = form.getAttribute("data-form");
    if (kind === "product-search") {
      const input = form.querySelector('[data-input="product-search"]');
      void this.#searchProducts(input instanceof HTMLInputElement ? input.value : "");
    }
    if (kind === "customer-search") {
      const input = form.querySelector('[data-input="customer-search"]');
      void this.#searchCustomers(input instanceof HTMLInputElement ? input.value : "");
    }
  };

  async #loadFlows(): Promise<void> {
    const result = await this.#account.listFlows();
    this.#flowsState = result.ok ? { kind: "ready", flows: result.value } : { kind: "unavailable" };
    this.requestUpdate();
  }

  async #chooseFlow(flow: string): Promise<void> {
    if (this.#busy || flow === "") return;
    this.#busy = true;
    this.#message = null;
    this.requestUpdate();
    const minted = await this.#account.chooseFlow(flow);
    if (!minted.ok) {
      this.#busy = false;
      this.#message = `Demo sign-in refused: ${minted.error}`;
      this.requestUpdate();
      return;
    }
    const adopted = this.#controller.adopt(minted.value);
    this.#busy = false;
    if (!adopted) {
      this.#message = "Demo sign-in refused: the mint's session was not usable.";
      this.requestUpdate();
      return;
    }
    this.#till = { label: this.#labelFor(flow) };
    this.requestUpdate();
    void this.#initTill();
  }

  #labelFor(flow: string): string {
    if (this.#flowsState.kind === "ready") {
      const row = this.#flowsState.flows.find((candidate) => candidate.flow === flow);
      if (row !== undefined && typeof row.label === "string") return row.label;
    }
    return flow;
  }

  /**
   * Sign out: the controller ends the mint (the adapter drops it) and wipes
   * custody either way; the entry view with the cards comes back, because the
   * flows answer is still held. A reload signs out too: the mint lives only
   * in this tab's memory.
   */
  async #signOut(): Promise<void> {
    if (this.#busy) return;
    this.#busy = true;
    this.requestUpdate();
    await this.#controller.signOut();
    this.#busy = false;
    this.#till = null;
    this.#message = null;
    this.#branch = null;
    this.#drawer = FRESH_DRAWER_PANEL;
    this.#search = FRESH_SEARCH_PANEL;
    this.#customer = WALK_IN_CUSTOMER;
    this.#customerSearch = { state: "idle", hits: [], message: null };
    this.#standing = null;
    this.#standingMessage = null;
    this.#lines = [];
    this.#pricing = "idle";
    this.#pricingMessage = null;
    this.#pricedForType = null;
    this.#drawerId = null;
    this.#simulated = null;
    this.#tender = "CASH";
    this.#committed = null;
    this.#receiptBalance = null;
    this.#commitMessage = null;
    this.#commitBusy = false;
    this.requestUpdate();
  }

  /**
   * The till's own start: resolve the branch the login reaches, then find or
   * open its drawer. Each step renders its own state, so a refusal names
   * itself in words and the screen never invents a branch or a float.
   */
  async #initTill(): Promise<void> {
    this.#drawer = FRESH_DRAWER_PANEL;
    this.requestUpdate();
    const branch = await this.#tillClient.resolveBranch();
    if (!branch.ok) {
      this.#drawer = { ...FRESH_DRAWER_PANEL, state: "refused", message: branch.error };
      this.requestUpdate();
      return;
    }
    this.#branch = branch.value;
    await this.#ensureDrawer();
    // The card rail's own report: the simulated notices ride exactly this
    // value, true only, so an unanswered status means no notice, never a
    // guessed one.
    const status = await this.#tillClient.paymentStatus();
    this.#simulated = status.ok ? status.value : null;
    this.requestUpdate();
  }

  async #ensureDrawer(): Promise<void> {
    if (this.#branch === null) return;
    this.#drawer = { ...FRESH_DRAWER_PANEL, busy: true };
    this.requestUpdate();
    const result = await this.#tillClient.ensureDrawer(this.#branch.id);
    if (!result.ok) {
      this.#drawerId = null;
      this.#drawer = {
        ...FRESH_DRAWER_PANEL,
        state: "refused",
        branchName: this.#branch.name,
        message: result.error,
      };
    } else {
      this.#drawerId = result.value.drawer.id;
      this.#drawer = {
        state: "open",
        how: result.value.how,
        branchName: this.#branch.name,
        station: result.value.drawer.station,
        float: formatMoney(result.value.drawer.floatCents),
        message: null,
        busy: false,
      };
    }
    this.requestUpdate();
  }

  async #searchProducts(query: string): Promise<void> {
    const trimmed = query.trim();
    if (trimmed === "") {
      this.#search = { state: "idle", hits: [], message: null };
      this.requestUpdate();
      return;
    }
    this.#search = { state: "loading", hits: [], message: null };
    this.requestUpdate();
    const result = await this.#tillClient.searchProducts(trimmed);
    this.#search = result.ok
      ? { state: "ready", hits: result.value, message: null }
      : { state: "refused", hits: [], message: result.error };
    this.requestUpdate();
  }

  async #searchCustomers(query: string): Promise<void> {
    const trimmed = query.trim();
    if (trimmed === "") {
      this.#customerSearch = { state: "idle", hits: [], message: null };
      this.requestUpdate();
      return;
    }
    this.#customerSearch = { state: "loading", hits: [], message: null };
    this.requestUpdate();
    const result = await this.#tillClient.searchCustomers(trimmed);
    this.#customerSearch = result.ok
      ? { state: "ready", hits: result.value, message: null }
      : { state: "refused", hits: [], message: result.error };
    this.requestUpdate();
  }

  /**
   * Pick an account: the name shows at once, the standing read answers beside
   * it, and the ticket re-prices, because the customer IS the price: the
   * engine answers a different rule and tier for this account than it did for
   * the walk-in.
   */
  async #pickCustomer(customerId: string): Promise<void> {
    const hit = this.#customerSearch.hits.find((candidate) => candidate.id === customerId);
    if (hit === undefined) return;
    this.#customer = { kind: "account", customer: hit };
    this.#standing = null;
    this.#standingMessage = null;
    this.requestUpdate();
    if (this.#branch !== null) {
      const standing = await this.#tillClient.accountStanding(this.#branch.id, customerId);
      if (standing.ok) {
        this.#standing = standing.value;
      } else {
        this.#standingMessage = `The account's standing did not answer: ${standing.error}`;
      }
    }
    this.requestUpdate();
    await this.#priceTicket();
  }

  #useWalkIn(): void {
    this.#customer = WALK_IN_CUSTOMER;
    this.#standing = null;
    this.#standingMessage = null;
    this.requestUpdate();
    void this.#priceTicket();
  }

  #addLine(productId: string): void {
    const hit = this.#search.hits.find((candidate) => candidate.productId === productId);
    if (hit === undefined) return;
    this.#lines = [
      ...this.#lines,
      { key: crypto.randomUUID(), product: hit, qty: 1, uom: hit.soldBy, quote: null, marginBps: null },
    ];
    void this.#priceTicket();
  }

  /**
   * Read the quantity inputs off the ticket and re-price: the inputs are the
   * shelf's words, so a value that is not a quantity refuses in words and
   * prices nothing, rather than sending a guess to the engine.
   */
  async #repriceFromInputs(): Promise<void> {
    const inputs = this.renderRoot.querySelectorAll<HTMLInputElement>("input[data-qty-key]");
    const typed: Map<string, number> = new Map();
    for (const input of inputs) {
      const key = input.getAttribute("data-qty-key");
      if (key === null) continue;
      const parsed = parseQuantity(input.value);
      if (parsed === null) {
        this.#pricing = "refused";
        this.#pricingMessage = "A quantity is a number of units, like 3 or 2.5: fix it and re-price.";
        this.requestUpdate();
        return;
      }
      typed.set(key, parsed);
    }
    this.#lines = this.#lines.map((line) => ({ ...line, qty: typed.get(line.key) ?? line.qty }));
    await this.#priceTicket();
  }

  /**
   * Price the whole ticket through the engine, then ask the margin glance
   * about the same lines. No price is ever sent, no price is ever computed
   * here, and a refused read blanks the ticket's figures rather than leaving
   * stale ones beside changed quantities: an unpriced line shows its
   * placeholder until the engine answers again.
   */
  async #priceTicket(): Promise<void> {
    if (this.#branch === null || this.#drawer.state !== "open") return;
    if (this.#lines.length === 0) {
      this.#lines = [];
      this.#pricing = "idle";
      this.#pricingMessage = null;
      this.#pricedForType = null;
      this.requestUpdate();
      return;
    }
    const customerId = this.#customer.kind === "account" ? this.#customer.customer.id : "";
    const items = this.#lines.map((line) => ({ productId: line.product.productId, qty: line.qty, uom: line.uom }));
    this.#pricing = "pricing";
    this.#pricingMessage = null;
    this.requestUpdate();
    const priced = await this.#tillClient.priceTicket(this.#branch.id, customerId, items);
    if (!priced.ok) {
      this.#lines = this.#lines.map((line) => ({ ...line, quote: null, marginBps: null }));
      this.#pricing = "refused";
      this.#pricingMessage = priced.error;
      this.#pricedForType = null;
      this.requestUpdate();
      return;
    }
    const quotes = priced.value.quotes;
    this.#lines = this.#lines.map((line, index) => ({ ...line, quote: quotes[index] ?? null }));
    this.#pricedForType = priced.value.customerType;
    const glanced = await this.#tillClient.glanceMargins(
      this.#branch.id,
      customerId,
      priced.value.customerType,
      items,
    );
    if (glanced.ok) {
      this.#lines = this.#lines.map((line, index) => ({ ...line, marginBps: glanced.value.marginBps[index] ?? null }));
      this.#pricing = "priced";
      this.#pricingMessage = null;
    } else {
      this.#lines = this.#lines.map((line) => ({ ...line, marginBps: null }));
      this.#pricing = "priced";
      this.#pricingMessage = `The margin glance did not answer: ${glanced.error}`;
    }
    this.requestUpdate();
  }

  #ticketPanel(): TicketPanel {
    const lines: TicketLineView[] = this.#lines.map((line) => ({
      key: line.key,
      sku: line.product.sku,
      name: line.product.name,
      uom: line.uom,
      qty: line.qty,
      unit: line.quote?.unitCents != null ? formatMoney(line.quote.unitCents) : null,
      basis: line.quote?.basis ?? null,
      margin: line.marginBps !== null ? formatMargin(line.marginBps) : null,
    }));
    return {
      lines,
      pricing: this.#pricing,
      message: this.#pricingMessage,
      customerType: this.#pricedForType,
    };
  }

  #customerPanel(): CustomerPanel {
    let picked: PickedCustomer = WALK_IN_CUSTOMER;
    if (this.#customer.kind === "account") {
      const customer = this.#customer.customer;
      picked = {
        kind: "account",
        customer,
        standing:
          this.#standing === null
            ? null
            : {
                // The search row's own facts, validated integers by the time
                // they are here; the standing read's past-due and hold stay
                // optional, unknown where the read left them out.
                balance: formatMoney(customer.openBalanceCents),
                limit: formatMoney(customer.creditLimitCents),
                pastDue: this.#standing.pastDueCents === null ? null : formatMoney(this.#standing.pastDueCents),
                onHold: this.#standing.onHold,
              },
        standingMessage: this.#standingMessage,
      };
    }
    return {
      picked,
      searchState: this.#customerSearch.state,
      hits: this.#customerSearch.hits,
      message: this.#customerSearch.message,
    };
  }

  /**
   * Ring the sale. The till checks only what it must before the call (a
   * drawer, priced lines, a typed cash amount): every business rule, the
   * change due included, is the server's. A refusal lands in words, the
   * credit gate's above all, and nothing is cleared: the ticket stands until
   * a sale actually commits.
   */
  async #commitSale(): Promise<void> {
    if (this.#branch === null || this.#drawerId === null) return;
    if (this.#lines.length === 0) return;
    const unpriced = this.#lines.some((line) => line.quote === null || line.quote.unitCents === null);
    if (unpriced) {
      this.#commitMessage = "A line on the ticket has no price yet: re-price the ticket first.";
      this.requestUpdate();
      return;
    }
    const customerId = this.#customer.kind === "account" ? this.#customer.customer.id : "";
    if (this.#tender === "ACCOUNT" && customerId === "") {
      this.#commitMessage = "The walk-in has no account to bill: pick an account, or take cash or card.";
      this.requestUpdate();
      return;
    }
    let cashCents: number | null = null;
    if (this.#tender === "CASH") {
      const input = this.renderRoot.querySelector<HTMLInputElement>('input[data-input="cash-tendered"]');
      cashCents = parseAmountToCents(input?.value ?? "");
      if (cashCents === null) {
        this.#commitMessage = "Type the cash tendered as an amount in dollars, like 50 or 40.55.";
        this.requestUpdate();
        return;
      }
    }
    const ring: SaleRing = {
      idempotencyKey: crypto.randomUUID(),
      branchId: this.#branch.id,
      tillSessionId: this.#drawerId,
      customerId,
      tender: this.#tender,
      cashTenderedCents: this.#tender === "CASH" ? cashCents : null,
      requestedDate: new Date().toISOString().slice(0, 10),
      items: this.#lines.map((line) => ({ productId: line.product.productId, qty: line.qty, uom: line.uom })),
    };
    this.#commitBusy = true;
    this.#commitMessage = null;
    this.requestUpdate();
    const result = await this.#tillClient.commitSale(ring);
    if (!result.ok) {
      this.#commitBusy = false;
      this.#commitMessage = this.#saleRefusalWords(result.error, result.status);
      this.requestUpdate();
      return;
    }
    this.#committed = result.value;
    this.#receiptBalance = null;
    this.#lines = [];
    this.#pricing = "idle";
    this.#pricingMessage = null;
    this.#pricedForType = null;
    this.#commitBusy = false;
    this.requestUpdate();
    // An account sale bills the account: the receipt's balance is a fresh
    // read, not the sale total added to a remembered one, so the till never
    // does that arithmetic. Until the read answers, the balance row says so.
    if (this.#tender === "ACCOUNT" && this.#customer.kind === "account") {
      const accountNo = this.#customer.customer.accountNo;
      const refreshed = await this.#tillClient.searchCustomers(accountNo);
      const row = refreshed.ok ? refreshed.value.find((candidate) => candidate.id === customerId) : undefined;
      this.#receiptBalance = row !== undefined ? formatMoney(row.openBalanceCents) : null;
      this.requestUpdate();
    }
  }

  /**
   * The server's refusal, in words a person at a counter can act on. The
   * credit gate's word is the one this screen translates; everything else is
   * the server's own message, shown as it arrived.
   */
  #saleRefusalWords(error: string, status: number | undefined): string {
    if (status === 409 && error === "credit_blocked") {
      return (
        "This account cannot carry this sale on account: the credit gate refused it (over the limit, or on a hold). " +
        "Cash or card still works, and accounts receivable can release or override a hold."
      );
    }
    return `The sale did not ring: ${error}`;
  }

  #receiptView(): ReceiptView | null {
    const sale = this.#committed;
    if (sale === null) return null;
    return {
      orderId: sale.orderId,
      orderNo: sale.orderNo,
      invoiceNo: sale.invoiceNo,
      subtotal: formatMoney(sale.subtotalCents),
      tax: formatMoney(sale.taxCents),
      total: formatMoney(sale.totalCents),
      tenders: sale.tenders.map((leg) => ({ method: leg.method, amount: formatMoney(leg.amountCents) })),
      tendered: sale.cashTenderedCents === null ? null : formatMoney(sale.cashTenderedCents),
      changeDue: sale.changeDueCents === null ? null : formatMoney(sale.changeDueCents),
      // The notice belongs to the card rail: it rides the status route's
      // value, and only on a card sale's receipt.
      simulated: sale.tender === "CARD" ? this.#simulated : null,
      account:
        sale.tender === "ACCOUNT" && this.#customer.kind === "account"
          ? {
              name: this.#customer.customer.name,
              accountNo: this.#customer.customer.accountNo,
              balance: this.#receiptBalance,
            }
          : null,
    };
  }

  #tenderPanel() {
    return {
      tender: this.#tender,
      simulated: this.#simulated,
      lineCount: this.#lines.length,
      priced: this.#lines.length > 0 && this.#lines.every((line) => line.quote !== null && line.quote.unitCents !== null),
      canBillAccount: this.#customer.kind === "account",
      busy: this.#commitBusy,
      message: this.#commitMessage,
    };
  }

  #tillView(): TillView {
    return {
      drawer: this.#drawer,
      search: this.#search,
      customer: this.#customerPanel(),
      ticket: this.#ticketPanel(),
    };
  }

  render(): TemplateResult {
    const demoPresent = this.#till !== null || this.#flowsState.kind === "ready";
    const receipt = this.#receiptView();
    return html`
      <header>
        ${demoPresent ? demoBanner() : nothing}
        <div class="title-row">
          <h1>Counter Till</h1>
          ${this.#till === null
            ? nothing
            : html`
                <p class="signed-in">
                  <span class="role-name" data-testid="signed-in-role">${this.#till.label}</span>
                  <button type="button" class="btn-secondary" data-action="sign-out" ?disabled=${this.#busy}>
                    Sign out
                  </button>
                </p>
              `}
        </div>
      </header>
      <main>
        ${this.#till === null
          ? renderEntry({ flows: this.#flowsState, busy: this.#busy, message: this.#message })
          : html`
              ${renderTill(this.#tillView())}
              <div class="panels till-lower">
                ${renderTender(this.#tenderPanel())}
                ${receipt === null ? nothing : renderReceipt(receipt)}
              </div>
            `}
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
