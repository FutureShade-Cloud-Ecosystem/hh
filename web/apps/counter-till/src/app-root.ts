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
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles } from "@hardscapehouse/design-system/src/primitives.js";
import { ApiClient } from "@hardscapehouse/api-client";
import { AuthController, SessionStore, authHeaderProvider } from "@hardscapehouse/auth";
import { DemoAccount } from "./demo-account.js";
import { entryStyles, renderEntry, type FlowsState } from "./views/entry.js";
import { tillStyles, renderTill } from "./views/till.js";

const DEMO_ORG = "hh-demo";

export class CounterTillApp extends HHElement {
  static styles = [
    baseStyles,
    entryStyles,
    tillStyles,
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
      h1 {
        font-size: var(--hh-s-16);
        margin: 0;
      }
      main {
        padding: var(--hh-s-20);
      }
    `,
  ];

  readonly #sessions = new SessionStore();
  readonly #account: DemoAccount;
  readonly #controller: AuthController;
  #flowsState: FlowsState = { kind: "loading" };
  #busy = false;
  #message: string | null = null;
  #till: { label: string } | null = null;

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
  }

  connectedCallback(): void {
    super.connectedCallback();
    void this.#loadFlows();
  }

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

  render(): TemplateResult {
    return html`
      <header>
        <h1>Counter Till</h1>
      </header>
      <main>
        ${this.#till === null
          ? renderEntry(
              { flows: this.#flowsState, busy: this.#busy, message: this.#message },
              { choose: (flow) => void this.#chooseFlow(flow) },
            )
          : renderTill()}
      </main>
    `;
  }
}

defineElement("ct-app-root", CounterTillApp);
