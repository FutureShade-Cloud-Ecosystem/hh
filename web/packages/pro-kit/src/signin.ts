// hh-pro-signin: the contractor's way in. Visual reference: the FB Design board
// HH: Pro, shell-signin-phone-brand (the dealer's navy ground, the HH Pro
// lockup, one card titled Sign in) and its states (build_signin_states.py):
// the dealer's methods (decision rows 1524 and 1592: the email link and
// Google for contractors, FutureBuild's own email and password and Microsoft
// when a dealer allows them), one method only, and the method the dealer
// refuses. What the card shows is signInPlan's answer (signin-methods.ts), so
// a node test reads it. Every word is the kind a contractor reads: nothing
// here names a console, a core, a token or a session.
//
// The element reads its fields itself. FutureBuild's own way hands the app
// one "pro-signin-submit" event ({email, password}) and clears the password
// field at once, so a password never rests in the page; every other way hands
// one "pro-signin-method" event ({method, email} or, for a demo role,
// {method: "persona", roleLabel}). It stores nothing. The app tells it the
// dealer's name, the methods, a refused method, the demo roles, whether a
// sign in is in flight and the last refusal, by plain property assignment
// after render (the design system's position gate refuses every bound
// property).
import { html, css, nothing, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles, button } from "@hardscapehouse/design-system/src/primitives.js";
import { DEMO_METHODS, signInPlan, type SignInMethod, type SignInMethodChoice, type SignInSubmit } from "./signin-methods.js";

export type { SignInMethodChoice, SignInSubmit };

export class HHProSignIn extends HHElement {
  static properties = {
    ...HHElement.properties,
    dealerName: { state: true },
    message: { state: true },
    busy: { state: true },
    methods: { state: true },
    refused: { state: true },
    personas: { state: true },
  };

  static styles = [
    baseStyles,
    css`
      :host {
        display: block;
        min-height: 100vh;
        background: var(--hh-ground);
        color: var(--hh-text);
      }
      main {
        display: flex;
        flex-direction: column;
        align-items: center;
        gap: var(--hh-s-24);
        max-width: 28rem;
        margin: 0 auto;
        padding: var(--hh-s-48) var(--hh-s-20) var(--hh-s-32);
      }
      .lockup { display: flex; flex-direction: column; align-items: center; gap: var(--hh-s-8); text-align: center; }
      .mark {
        display: grid;
        place-items: center;
        width: 52px;
        height: 52px;
        border-radius: var(--hh-radius-control);
        background: var(--hh-accent);
        color: var(--hh-accent-ink);
        font-weight: 800;
        font-size: 1.5rem;
      }
      h1 { margin: 0; font-size: 1.75rem; font-weight: 800; }
      h1 .pro { color: var(--hh-accent-text); }
      .sub { margin: 0; color: var(--hh-text-dim); font-size: 0.875rem; }
      .card {
        width: 100%;
        display: flex;
        flex-direction: column;
        gap: var(--hh-s-12);
        padding: var(--hh-s-20);
        border: 1px solid var(--hh-line);
        border-radius: var(--hh-radius-card);
        background: var(--hh-card);
      }
      h2 { margin: 0; font-size: 1.125rem; }
      .hint { margin: 0; color: var(--hh-text-dim); font-size: 0.875rem; }
      label { display: flex; flex-direction: column; gap: var(--hh-s-4); font-size: 0.75rem; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; color: var(--hh-text-dim); }
      .error { margin: 0; min-height: 1.25rem; color: var(--hh-danger); font-size: 0.875rem; }
      /* The board's primary action is the dealer's gold with its own ink (HH: Pro, shell-signin-phone-brand). */
      .btn-primary { background: var(--hh-accent); color: var(--hh-accent-ink); border-color: var(--hh-accent); }
      .btn-primary:hover { background: var(--hh-accent-hover); }
      .btn-primary:disabled { background: var(--hh-disabled-fill); color: var(--hh-disabled-ink); border-color: var(--hh-disabled-line); }
      .or { display: flex; align-items: center; gap: var(--hh-s-12); color: var(--hh-text-dim); font-size: 0.75rem; }
      .or::before, .or::after { content: ""; flex: 1; border-top: 1px solid var(--hh-line); }
      .note { margin: 0; color: var(--hh-text-dim); font-size: 0.75rem; text-align: center; }
      .refusal { margin: 0; padding: var(--hh-s-12); border-radius: var(--hh-radius-control); background: var(--hh-widget); border: 1px solid var(--hh-state-watch); color: var(--hh-state-watch); font-size: 0.875rem; font-weight: 600; }
      .demo { display: flex; flex-direction: column; gap: var(--hh-s-8); }
      .demo p { margin: 0; color: var(--hh-text-dim); font-size: 0.75rem; text-align: center; }
      .roles { display: flex; flex-wrap: wrap; gap: var(--hh-s-8); justify-content: center; }
      .foot { margin: 0; color: var(--hh-text-dim); font-size: 0.875rem; text-align: center; }
    `,
  ];

  declare dealerName: string;
  declare message: string;
  declare busy: boolean;
  /** The dealer's allowed methods; the demo dealer's set until the app says otherwise. */
  declare methods: readonly string[];
  /** A method the dealer refuses, or null. */
  declare refused: string | null;
  /** The demo roles on offer, by label; empty outside the demo build. */
  declare personas: readonly string[];

  constructor() {
    super();
    this.dealerName = "";
    this.message = "";
    this.busy = false;
    this.methods = DEMO_METHODS;
    this.refused = null;
    this.personas = [];
  }

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.hasAttribute("data-action"));
    if (control === undefined) return;
    const action = control.getAttribute("data-action");
    if (action === "sign-in") this.#submit();
    else if (action === "email_link" || action === "google" || action === "microsoft") this.#choose(action);
    else if (action === "persona") this.#persona(control.getAttribute("data-role") ?? "");
  };

  readonly #onKey = (event: KeyboardEvent): void => {
    if (event.key !== "Enter") return;
    const target = event.composedPath()[0];
    if (target instanceof HTMLInputElement) this.#submit();
  };

  connectedCallback(): void {
    super.connectedCallback();
    this.addEventListener("click", this.#onClick);
    this.addEventListener("keydown", this.#onKey);
  }

  disconnectedCallback(): void {
    this.removeEventListener("click", this.#onClick);
    this.removeEventListener("keydown", this.#onKey);
    super.disconnectedCallback();
  }

  #field(name: string): HTMLInputElement | null {
    return this.renderRoot.querySelector(`input[data-field="${name}"]`);
  }

  #submit(): void {
    if (this.busy) return;
    const email = this.#field("email");
    const password = this.#field("password");
    if (email === null || password === null) return;
    const detail: SignInSubmit = { email: email.value.trim(), password: password.value };
    password.value = "";
    this.dispatchEvent(new CustomEvent<SignInSubmit>("pro-signin-submit", { detail, bubbles: true, composed: true }));
  }

  #choose(method: "email_link" | "google" | "microsoft"): void {
    if (this.busy) return;
    const email = this.#field("email");
    const detail: SignInMethodChoice = method === "email_link" ? { method, email: (email?.value ?? "").trim() } : { method };
    this.dispatchEvent(new CustomEvent<SignInMethodChoice>("pro-signin-method", { detail, bubbles: true, composed: true }));
  }

  #persona(roleLabel: string): void {
    if (this.busy || roleLabel === "") return;
    const detail: SignInMethodChoice = { method: "persona", roleLabel };
    this.dispatchEvent(new CustomEvent<SignInMethodChoice>("pro-signin-method", { detail, bubbles: true, composed: true }));
  }

  // A button of the card: FutureBuild's own way is the plain Sign in (which reads Signing in while it runs).
  #button(entry: { method: SignInMethod; label: string; primary: boolean }): TemplateResult {
    const action = entry.method === "futurebuild" ? "sign-in" : entry.method;
    const label = entry.method === "futurebuild" && this.busy ? "Signing in" : entry.label;
    return button({ label, action, disabled: this.busy, kind: entry.primary ? "primary" : "secondary" });
  }

  render(): TemplateResult {
    const dealer = this.dealerName === "" ? "Your dealer" : this.dealerName;
    const plan = signInPlan({ methods: this.methods, refused: this.refused, dealer: this.dealerName });
    const emailButtons = plan.buttons.filter((b) => b.method === "email_link" || b.method === "futurebuild");
    const providerButtons = plan.buttons.filter((b) => b.method === "google" || b.method === "microsoft");
    return html`<main>
      <div class="lockup">
        <span class="mark" aria-hidden="true">H</span>
        <h1>HH <span class="pro">Pro</span></h1>
        <p class="sub">${dealer} · the Pro portal for contractor accounts</p>
      </div>
      <section class="card" aria-labelledby="signin-title">
        <h2 id="signin-title">Sign in</h2>
        <p class="hint" data-testid="signin-hint">${plan.hint}</p>
        ${plan.refusal === null ? nothing : html`<p class="refusal" role="alert" data-testid="signin-refusal">${plan.refusal}</p>`}
        ${plan.email
          ? html`<label>Email
              <input class="control" data-control="text" data-field="email" data-testid="signin-email" type="email" autocomplete="username" inputmode="email" />
            </label>`
          : nothing}
        ${plan.password
          ? html`<label>Password
              <input class="control" data-control="text" data-field="password" data-testid="signin-password" type="password" autocomplete="current-password" />
            </label>`
          : nothing}
        <p class="error" role="alert" data-testid="signin-message">${this.message}</p>
        ${emailButtons.map((b) => this.#button(b))}
        ${plan.divider ? html`<p class="or" aria-hidden="true">or</p>` : nothing}
        ${providerButtons.map((b) => this.#button(b))}
        ${plan.googleNote === "" ? nothing : html`<p class="note">${plan.googleNote}</p>`}
        ${this.personas.length === 0
          ? nothing
          : html`<div class="demo" data-testid="signin-demo-roles">
              <p>Or pick a demo role</p>
              <div class="roles">${this.personas.map((label) => html`<button type="button" class="btn btn-secondary" data-action="persona" data-role="${label}" ?disabled=${this.busy}>${label}</button>`)}</div>
            </div>`}
      </section>
      <p class="foot">No account yet? ${dealer} invites you.</p>
    </main>`;
  }
}

defineElement("hh-pro-signin", HHProSignIn);
