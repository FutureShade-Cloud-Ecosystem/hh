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
import { demoBanner, demoBannerStyles } from "./demo-banner.js";
import { entryStyles, renderEntry, type FlowsState } from "./views/entry.js";
import { tillStyles, renderTill } from "./views/till.js";
import { ReturnsFlow } from "./returns/flow.js";
import { OneTimeCredential } from "./returns/approval.js";
import { RefundApprovalCapture } from "./returns/capture.js";
import { returnsStyles, renderReturns } from "./views/returns.js";

const DEMO_ORG = "hh-demo";

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    demoBannerStyles,
    entryStyles,
    tillStyles,
    returnsStyles,
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
  readonly #approverCredential: OneTimeCredential;
  #flowsState: FlowsState = { kind: "loading" };
  #busy = false;
  #message: string | null = null;
  #till: { label: string } | null = null;
  readonly #returns: ReturnsFlow;

  constructor() {
    super();
    // The till's transport, wrapped so the refund approval refusal's body
    // (the amount and the approver roles beside the envelope) reaches the
    // returns flow: the generated client carries the error string alone.
    const capture = new RefundApprovalCapture();
    const staffHeaders = authHeaderProvider({
      callerClass: "staff",
      sessions: this.#sessions,
      activeOrg: () => DEMO_ORG,
    });
    const client = new ApiClient({ baseUrl: "", fetch: capture.fetch, headers: staffHeaders });
    this.#account = new DemoAccount({ client });
    this.#controller = new AuthController({ account: this.#account, sessions: this.#sessions });
    // The approver's client exists for the one return call that may carry the
    // approver's credential (D49): its provider takes the token out of the
    // one time slot during that send, and taking is dropping, so the token
    // rests nowhere before and survives nothing after. The clerk's client is
    // a different instance and never carries the credential, so no other call
    // this app makes can leak it. Nothing here touches storage or logs.
    const approverCredential = new OneTimeCredential();
    this.#approverCredential = approverCredential;
    const approverClient = new ApiClient({
      baseUrl: "",
      headers: async () => {
        const headers = await staffHeaders();
        const token = approverCredential.take();
        if (token !== null) {
          headers["X-Approver-Authorization"] = `Bearer ${token}`;
        }
        return headers;
      },
    });
    this.#returns = new ReturnsFlow({
      client,
      approverClient,
      account: this.#account,
      credential: approverCredential,
      clerkName: () => this.#sessions.current?.user.name ?? null,
      capture,
      onChange: () => this.requestUpdate(),
    });
  }

  connectedCallback(): void {
    super.connectedCallback();
    // The kits' wiring rule, carried for this app's own templates: listen on
    // the HOST and read the control off composedPath(), never @click in a
    // template and never event.target.closest, because the shadow boundary
    // retargets the target to this element (the design system's README holds
    // both halves).
    this.addEventListener("click", this.#onHostClick);
    this.addEventListener("input", this.#onHostInput);
    void this.#loadFlows();
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onHostClick);
    this.removeEventListener("input", this.#onHostInput);
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
    if (control.getAttribute("data-action") === "returns-find") {
      void this.#returns.findSale();
    }
    if (control.getAttribute("data-action") === "returns-method") {
      const method = control.getAttribute("data-method");
      if (method !== null) this.#returns.chooseMethod(method);
    }
    if (control.getAttribute("data-action") === "returns-submit") {
      void this.#returns.submit();
    }
    if (control.getAttribute("data-action") === "returns-another") {
      this.#returns.startAnotherReturn();
    }
    if (control.getAttribute("data-action") === "returns-approve") {
      void this.#returns.openApprovalDialog();
    }
    if (control.getAttribute("data-action") === "returns-approve-cancel") {
      this.#returns.closeApprovalDialog();
    }
    if (control.getAttribute("data-action") === "returns-approver-flow") {
      const approverFlow = control.getAttribute("data-flow");
      if (approverFlow !== null && approverFlow !== "") {
        void this.#returns.approveWithFlow(approverFlow);
      }
    }
  };

  // The returns desk's fields ride the same host delegation as the buttons:
  // one input listener on the host, the control read off composedPath() by
  // its data-field name, so a re-render strands no listener. The inputs are
  // uncontrolled: the DOM holds what was typed, the flow hears every edit
  // here, and a draft's fresh mount shows the flow's own defaults.
  readonly #onHostInput = (event: Event): void => {
    const control = event
      .composedPath()
      .find((node): node is Element => node instanceof Element && node.hasAttribute("data-field"));
    if (!(control instanceof HTMLInputElement) && !(control instanceof HTMLTextAreaElement)) return;
    const field = control.getAttribute("data-field");
    if (field === "order-no") this.#returns.setOrderNo(control.value);
    if (field === "qty" && control instanceof HTMLInputElement) {
      this.#returns.setQty(control.getAttribute("data-line") ?? "", control.value);
    }
    if (field === "note") this.#returns.setNote(control.value);
    if (field === "restock" && control instanceof HTMLInputElement) {
      this.#returns.setRestock(control.checked);
    }
    if (field === "reason-code") this.#returns.setReasonCode(control.value);
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
    this.requestUpdate();
  }

  render(): TemplateResult {
    const demoPresent = this.#till !== null || this.#flowsState.kind === "ready";
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
          : html`${renderTill()}${renderReturns(this.#returns)}`}
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
