// The Counter Till's root element: the thin UI layer the house invariants ask
// for. It renders server and package state and calls client actions; no
// figure on this screen is computed here: prices, margins, totals, tax and
// change due all arrive from the core, and the one money helper formats them.
// What lives in this element is the counter's own state machine: which panel
// is in which state, what is in flight, and what a refusal says. The
// boundary conversions it owns (a typed quantity, a typed amount, the house
// zone's day) sit in plain modules beside it, held by unit tests.
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
  HOUSE_ZONE,
  houseDayInZone,
  parseQuantity,
  type BranchRef,
  type CommittedSale,
  type CustomerRef,
  type ProductRef,
  type QuoteRef,
  type SaleRing,
  type StandingRef,
  type TicketItem,
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
import { parkedStyles, renderParked, type ParkedPanel, type ParkedRow } from "./views/parked.js";
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
    parkedStyles,
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
  /**
   * The account the committed sale was rung for, frozen at commit: the
   * receipt speaks for the sale, not for whoever the counter is serving now.
   */
  #committedAccount: { name: string; accountNo: string; customerId: string } | null = null;
  /**
   * The ring's idempotency key, minted at the first ring attempt and held for
   * the ticket: a lost answer followed by a second press must replay the same
   * sale, never commit twice. Cleared when the ticket changes or a sale is
   * known to have committed.
   */
  #ringKey: string | null = null;
  /** Monotonic ticket pricing sequence: an answer older than the newest ask is discarded. */
  #pricingSeq = 0;
  #parkedState: "loading" | "ready" | "refused" = "loading";
  #parkedRows: ParkedRow[] = [];
  #parkedMessage: string | null = null;
  #parkedBusy = false;
  #parkMessage: string | null = null;
  /** The accounts this till has met, by id: the parked list labels tickets with these. */
  #knownCustomers: Map<string, CustomerRef> = new Map();
  /** The products this till has met, by id: a resumed ticket shows the name it searched. */
  #knownProducts: Map<string, ProductRef> = new Map();

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
    // both halves). Click is composed, so the host hears it; the search
    // panels are therefore plain buttons with data-action, never forms (a
    // form's submit is not composed, and a host listener would never see it:
    // measured, not assumed). Keydown IS composed, so Enter in a search field
    // reaches the host and runs that field's search.
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("keydown", this.#onHostKeydown);
    void this.#loadFlows();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("keydown", this.#onHostKeydown);
    super.disconnectedCallback();
  }

  readonly #onHostKeydown = (event: KeyboardEvent): void => {
    if (event.key !== "Enter") return;
    const input = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-input"));
    if (input === undefined) return;
    event.preventDefault();
    const kind = input.getAttribute("data-input");
    if (kind === "product-search" && input instanceof HTMLInputElement) {
      void this.#searchProducts(input.value);
    }
    if (kind === "customer-search" && input instanceof HTMLInputElement) {
      void this.#searchCustomers(input.value);
    }
  };

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
    if (control.getAttribute("data-action") === "search-products") {
      const input = this.renderRoot.querySelector<HTMLInputElement>('input[data-input="product-search"]');
      void this.#searchProducts(input instanceof HTMLInputElement ? input.value : "");
    }
    if (control.getAttribute("data-action") === "search-customers") {
      const input = this.renderRoot.querySelector<HTMLInputElement>('input[data-input="customer-search"]');
      void this.#searchCustomers(input instanceof HTMLInputElement ? input.value : "");
    }
    if (control.getAttribute("data-action") === "retry-drawer") {
      void this.#ensureDrawer();
    }
    if (control.getAttribute("data-action") === "add-line") {
      const productId = control.getAttribute("data-product-id");
      if (typeof productId === "string" && productId !== "") void this.#addLine(productId);
    }
    if (control.getAttribute("data-action") === "remove-line") {
      const key = control.getAttribute("data-line-key");
      if (typeof key === "string" && key !== "") {
        this.#lines = this.#lines.filter((line) => line.key !== key);
        this.#forgetRingKey();
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
      this.#committedAccount = null;
      this.#commitMessage = null;
      this.requestUpdate();
    }
    if (control.getAttribute("data-action") === "park-ticket") {
      void this.#parkTicket();
    }
    if (control.getAttribute("data-action") === "resume-ticket") {
      const ticketId = control.getAttribute("data-ticket-id");
      if (typeof ticketId === "string" && ticketId !== "") void this.#resumeTicket(ticketId);
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
    this.#committedAccount = null;
    this.#commitMessage = null;
    this.#commitBusy = false;
    this.#ringKey = null;
    this.#pricingSeq += 1;
    this.#parkedState = "loading";
    this.#parkedRows = [];
    this.#parkedMessage = null;
    this.#parkedBusy = false;
    this.#parkMessage = null;
    // The display memory of accounts and products this till has met stays:
    // the one hand-over the till's rules name is a parked ticket picked up by
    // a different login at the same counter, and the names are how the
    // resumed ticket says whose it is. Nothing money-shaped hangs on them:
    // prices always come from the engine, for the customer the ticket names.
    // They are tab memory, so a reload starts a fresh till.
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
    await this.#refreshParked();
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
    if (result.ok) {
      for (const hit of result.value) this.#knownProducts.set(hit.productId, hit);
      this.#search = { state: "ready", hits: result.value, message: null };
    } else {
      this.#search = { state: "refused", hits: [], message: result.error };
    }
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
    if (result.ok) {
      for (const row of result.value) this.#knownCustomers.set(row.id, row);
      this.#customerSearch = { state: "ready", hits: result.value, message: null };
    } else {
      this.#customerSearch = { state: "refused", hits: [], message: result.error };
    }
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
    await this.#adoptCustomer(hit);
  }

  async #adoptCustomer(ref: CustomerRef): Promise<void> {
    this.#knownCustomers.set(ref.id, ref);
    this.#customer = { kind: "account", customer: ref };
    this.#standing = null;
    this.#standingMessage = null;
    this.#forgetRingKey();
    this.requestUpdate();
    if (this.#branch !== null) {
      const standing = await this.#tillClient.accountStanding(this.#branch.id, ref.id);
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
    this.#forgetRingKey();
    this.requestUpdate();
    void this.#priceTicket();
  }

  async #addLine(productId: string): Promise<void> {
    const hit = this.#search.hits.find((candidate) => candidate.productId === productId);
    if (hit === undefined) return;
    this.#knownProducts.set(hit.productId, hit);
    const key = crypto.randomUUID();
    this.#lines = [
      ...this.#lines,
      { key, product: hit, qty: 1, uom: hit.soldBy, quote: null, marginBps: null },
    ];
    this.#forgetRingKey();
    this.requestUpdate();
    await this.updateComplete;
    // The quantity input starts at one: set once, imperatively, after the
    // element exists. The gate refuses a bound value attribute in a template,
    // and the input keeps its own DOM value across re-renders, so this is
    // also the only write it needs: every later value is the tester's typed
    // one, read back at re-price.
    const input = this.renderRoot.querySelector<HTMLInputElement>(`input[data-qty-key="${key}"]`);
    if (input !== null) input.value = "1";
    void this.#priceTicket();
  }

  /**
   * Read the quantity inputs off the ticket: the inputs are the shelf's
   * words, and the quantities the till sends anywhere (a ring, a park) are
   * THESE, never the last priced ones. A value that is not a quantity is a
   * refusal in words, nothing sent.
   */
  #gatherTypedQuantities(): { ok: true; typed: Map<string, number>; changed: boolean } | { ok: false; refusal: string } {
    const inputs = this.renderRoot.querySelectorAll<HTMLInputElement>("input[data-qty-key]");
    const typed: Map<string, number> = new Map();
    let changed = false;
    for (const input of inputs) {
      const key = input.getAttribute("data-qty-key");
      if (key === null) continue;
      const parsed = parseQuantity(input.value);
      if (parsed === null) {
        return { ok: false, refusal: "A quantity is a number of units, like 3 or 2.5: fix it and try again." };
      }
      typed.set(key, parsed);
      const line = this.#lines.find((candidate) => candidate.key === key);
      if (line !== undefined && line.qty !== parsed) changed = true;
    }
    return { ok: true, typed, changed };
  }

  /**
   * Read the typed quantities and, where they differ from the priced ones,
   * re-price first: the engine prices by quantity, so a ring or a park whose
   * quantities changed must hear the engine again before it sends. The
   * quantities on the ticket afterwards are the typed ones.
   */
  async #settleTypedQuantities(): Promise<{ ok: true; lines: TicketLine[] } | { ok: false; refusal: string }> {
    const gathered = this.#gatherTypedQuantities();
    if (!gathered.ok) return { ok: false, refusal: gathered.refusal };
    const lines: TicketLine[] = this.#lines.map((line) => ({
      ...line,
      qty: gathered.typed.get(line.key) ?? line.qty,
    }));
    if (gathered.changed) {
      this.#lines = lines;
      this.#forgetRingKey();
      await this.#priceTicket();
      if (this.#pricing !== "priced") {
        return { ok: false, refusal: "The ticket re-priced for the new quantities and the price did not answer: read the ticket and try again." };
      }
    }
    return { ok: true, lines };
  }

  /**
   * Re-price the ticket from the typed quantities. Nothing is priced here:
   * the ask goes to the engine, and the answer lands only if it is still the
   * newest one.
   */
  async #repriceFromInputs(): Promise<void> {
    const settled = await this.#settleTypedQuantities();
    if (!settled.ok) {
      this.#pricing = "refused";
      this.#pricingMessage = settled.refusal;
      this.requestUpdate();
    }
  }

  #forgetRingKey(): void {
    this.#ringKey = null;
  }

  /**
   * Price the whole ticket through the engine, then ask the margin glance
   * about the same lines. No price is ever sent, no price is ever computed
   * here. The ask carries a sequence number: two asks can be in flight (add,
   * remove and a customer pick each start one), and an answer that is no
   * longer the newest ask is discarded whole, whatever it says, so a late
   * answer never lands its figures on lines it was not asked about. A refused
   * ask (one that is still newest) blanks the ticket's figures rather than
   * leaving stale ones beside changed quantities: an unpriced line shows its
   * placeholder until the engine answers again.
   */
  async #priceTicket(): Promise<void> {
    if (this.#branch === null || this.#drawer.state !== "open") return;
    if (this.#lines.length === 0) {
      this.#pricingSeq += 1;
      this.#pricing = "idle";
      this.#pricingMessage = null;
      this.#pricedForType = null;
      this.requestUpdate();
      return;
    }
    const seq = ++this.#pricingSeq;
    const customerId = this.#customer.kind === "account" ? this.#customer.customer.id : "";
    const items = this.#lines.map((line) => ({ productId: line.product.productId, qty: line.qty, uom: line.uom }));
    this.#pricing = "pricing";
    this.#pricingMessage = null;
    this.requestUpdate();
    const priced = await this.#tillClient.priceTicket(this.#branch.id, customerId, items);
    if (seq !== this.#pricingSeq) return; // a newer ask owns the ticket now
    if (!priced.ok) {
      this.#lines = this.#lines.map((line) => ({ ...line, quote: null, marginBps: null }));
      this.#pricing = "refused";
      this.#pricingMessage = priced.error;
      this.#pricedForType = null;
      this.requestUpdate();
      return;
    }
    // The quotes ride index-aligned with the ask; each one names its own
    // product, and an answer that does not match line for line is refused
    // whole rather than realigned by position.
    const misaligned = priced.value.quotes.some((quote, index) => quote.productId !== items[index]?.productId);
    if (misaligned) {
      this.#lines = this.#lines.map((line) => ({ ...line, quote: null, marginBps: null }));
      this.#pricing = "refused";
      this.#pricingMessage = "The price read answered for different products than the ticket asked about.";
      this.#pricedForType = null;
      this.requestUpdate();
      return;
    }
    this.#lines = this.#lines.map((line, index) => ({ ...line, quote: priced.value.quotes[index] }));
    this.#pricedForType = priced.value.customerType;
    const glanced = await this.#tillClient.glanceMargins(
      this.#branch.id,
      customerId,
      priced.value.customerType,
      items,
    );
    if (seq !== this.#pricingSeq) return; // a newer ask owns the ticket now
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
   * drawer, priced lines, the typed quantities, a typed cash amount): every
   * business rule, the change due included, is the server's. The quantities
   * sent are the ones on the screen: where the typed ones differ from the
   * priced ones, the ticket re-prices first, because the engine prices by
   * quantity. The ring's key is minted once and held for the ticket, so a
   * lost answer and a second press replay the same sale instead of committing
   * twice. A refusal lands in words, the credit gate's above all, and nothing
   * is cleared: the ticket stands until a sale actually commits.
   */
  async #commitSale(): Promise<void> {
    if (this.#branch === null || this.#drawerId === null) return;
    if (this.#lines.length === 0) return;
    const settled = await this.#settleTypedQuantities();
    if (!settled.ok) {
      this.#commitMessage = settled.refusal;
      this.requestUpdate();
      return;
    }
    const unpriced = settled.lines.some((line) => line.quote === null || line.quote.unitCents === null);
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
      idempotencyKey: this.#ringKey ?? (this.#ringKey = crypto.randomUUID()),
      branchId: this.#branch.id,
      tillSessionId: this.#drawerId,
      customerId,
      tender: this.#tender,
      cashTenderedCents: this.#tender === "CASH" ? cashCents : null,
      requestedDate: houseDayInZone(HOUSE_ZONE),
      items: settled.lines.map((line) => ({ productId: line.product.productId, qty: line.qty, uom: line.uom })),
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
    // Committed: the receipt is the sale's, with the customer it was rung for
    // frozen into it, and the ticket's key is spent.
    this.#committed = result.value;
    this.#receiptBalance = null;
    this.#committedAccount =
      this.#customer.kind === "account"
        ? {
            name: this.#customer.customer.name,
            accountNo: this.#customer.customer.accountNo,
            customerId: this.#customer.customer.id,
          }
        : null;
    this.#ringKey = null;
    this.#lines = [];
    this.#pricing = "idle";
    this.#pricingMessage = null;
    this.#pricedForType = null;
    this.#commitBusy = false;
    this.requestUpdate();
    // An account sale bills the account: the receipt's balance is a fresh
    // read, not the sale total added to a remembered one, so the till never
    // does that arithmetic. Until the read answers, the balance row says so.
    if (this.#committedAccount !== null) {
      const accountNo = this.#committedAccount.accountNo;
      const refreshedCustomerId = this.#committedAccount.customerId;
      const refreshed = await this.#tillClient.searchCustomers(accountNo);
      const row = refreshed.ok ? refreshed.value.find((candidate) => candidate.id === refreshedCustomerId) : undefined;
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

  /**
   * Park the ticket and list this branch's parked tickets. The ticket parks
   * as quantities and units: no price leaves this app on it, so nothing
   * stale can come back. The quantities parked are the typed ones on the
   * screen; where they differ from the priced ones the ticket re-prices
   * first, the same as a ring.
   */
  async #parkTicket(): Promise<void> {
    if (this.#branch === null) return;
    if (this.#lines.length === 0) {
      this.#parkMessage = "Nothing to park: the ticket has no lines.";
      this.requestUpdate();
      return;
    }
    const settled = await this.#settleTypedQuantities();
    if (!settled.ok) {
      this.#parkMessage = settled.refusal;
      this.requestUpdate();
      return;
    }
    const customerId = this.#customer.kind === "account" ? this.#customer.customer.id : "";
    this.#parkedBusy = true;
    this.#parkMessage = null;
    this.requestUpdate();
    const parked = await this.#tillClient.parkTicket(
      this.#branch.id,
      customerId,
      settled.lines.map((line) => ({ productId: line.product.productId, qty: line.qty, uom: line.uom })),
    );
    if (!parked.ok) {
      this.#parkedBusy = false;
      this.#parkMessage = `The ticket did not park: ${parked.error}`;
      this.requestUpdate();
      return;
    }
    this.#lines = [];
    this.#pricing = "idle";
    this.#pricingMessage = null;
    this.#pricedForType = null;
    this.#ringKey = null;
    this.#parkedBusy = false;
    this.requestUpdate();
    await this.#refreshParked();
  }

  /**
   * Put a parked ticket back on the counter. The ticket on the counter comes
   * first: a resume replaces it, so with lines already on the ticket the
   * resume refuses in words rather than throwing someone's work away. The
   * resumed ticket's lines arrive without prices, so the till prices them
   * exactly as it prices any ticket: through the engine, for the customer the
   * ticket names.
   */
  async #resumeTicket(ticketId: string): Promise<void> {
    if (this.#branch === null) return;
    if (this.#lines.length > 0) {
      this.#parkMessage = "Lines are on the ticket: ring them or park them before resuming another ticket.";
      this.requestUpdate();
      return;
    }
    this.#parkedBusy = true;
    this.#parkMessage = null;
    this.requestUpdate();
    const resumed = await this.#tillClient.resumeTicket(ticketId);
    if (!resumed.ok) {
      this.#parkedBusy = false;
      this.#parkMessage = `The ticket did not resume: ${resumed.error}`;
      this.requestUpdate();
      return;
    }
    const ticket = resumed.value;
    this.#lines = ticket.lines.map((item: TicketItem) => {
      const known = this.#knownProducts.get(item.productId);
      return {
        key: crypto.randomUUID(),
        product: known ?? { productId: item.productId, sku: item.productId, name: item.productId, soldBy: item.uom },
        qty: item.qty,
        uom: item.uom,
        quote: null,
        marginBps: null,
      };
    });
    this.#ringKey = null;
    this.#parkedBusy = false;
    this.requestUpdate();
    await this.updateComplete;
    // The resumed quantities land in the inputs the same way an added line's
    // one does: one imperative write after the elements exist, since the
    // ticket's quantities are state, not template bindings.
    for (const line of this.#lines) {
      const input = this.renderRoot.querySelector<HTMLInputElement>(`input[data-qty-key="${line.key}"]`);
      if (input !== null) input.value = String(line.qty);
    }
    if (ticket.customerId === "") {
      this.#customer = WALK_IN_CUSTOMER;
      this.#standing = null;
      this.#standingMessage = null;
      this.requestUpdate();
      await this.#priceTicket();
    } else {
      const known = this.#knownCustomers.get(ticket.customerId);
      if (known !== undefined) {
        await this.#adoptCustomer(known);
      } else {
        // The ticket names an account this till has not read: it prices for
        // nobody else, and says so. The lines stand unpriced (and the ring
        // holds) until someone picks the account and the engine prices for
        // the account it names.
        this.#customer = WALK_IN_CUSTOMER;
        this.#standing = null;
        this.#standingMessage = null;
        this.#pricingMessage =
          "The ticket belongs to an account this till has not read, so it is not priced: search the account, pick it, and the engine prices the lines for it.";
        this.requestUpdate();
      }
    }
    // The ticket has left the parked list: read it again so the row goes with
    // it.
    await this.#refreshParked();
  }

  async #refreshParked(): Promise<void> {
    this.#parkedState = "loading";
    this.requestUpdate();
    const result = await this.#tillClient.listParked();
    if (!result.ok) {
      this.#parkedState = "refused";
      this.#parkedMessage = result.error;
    } else {
      this.#parkedState = "ready";
      this.#parkedMessage = null;
      this.#parkedRows = result.value.map((ticket) => {
        if (ticket.customerId === "") {
          return { id: ticket.id, ticketNo: ticket.ticketNo, customerLabel: "Walk-in", lineCount: ticket.lines.length };
        }
        const known = this.#knownCustomers.get(ticket.customerId);
        return {
          id: ticket.id,
          ticketNo: ticket.ticketNo,
          customerLabel: known !== undefined ? `${known.name} (${known.accountNo})` : "An account this till has not read",
          lineCount: ticket.lines.length,
        };
      });
    }
    this.requestUpdate();
  }

  #parkedPanel(): ParkedPanel {
    return {
      state: this.#parkedState,
      branchName: this.#branch?.name ?? null,
      rows: this.#parkedRows,
      message: this.#parkedMessage,
      busy: this.#parkedBusy,
      parkMessage: this.#parkMessage,
    };
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
      // The account frozen at commit, not the counter's current customer: the
      // receipt speaks for the sale that was rung.
      account:
        sale.tender === "ACCOUNT" && this.#committedAccount !== null
          ? {
              name: this.#committedAccount.name,
              accountNo: this.#committedAccount.accountNo,
              invoiceNo: sale.invoiceNo,
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
                ${renderParked(this.#parkedPanel())}
              </div>
            `}
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
