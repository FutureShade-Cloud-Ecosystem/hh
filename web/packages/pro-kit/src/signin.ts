// hh-pro-signin: the contractor's way in. Visual reference: the FB Design board
// HH: Pro, shell-signin-phone-brand (the dealer's navy ground, the HH Pro
// lockup, one card titled Sign in), with the form the brief asks for: email and
// password. Every word is the kind a contractor reads: nothing here names a
// console, a core, a token or a session.
//
// The element reads its two fields itself and hands them to the app as one
// "pro-signin-submit" event ({email, password}); it clears the password field
// the moment it has been handed over, so a password never rests in the page.
// It stores nothing. The app tells it the dealer's name, whether a sign in is
// in flight and the last refusal, by plain property assignment after render
// (the design system's position gate refuses every bound property).
import { html, css, type TemplateResult } from "lit";
import { HHElement, defineElement } from "@hardscapehouse/design-system/src/base.js";
import { baseStyles, button } from "@hardscapehouse/design-system/src/primitives.js";

export interface SignInSubmit {
  email: string;
  password: string;
}

export class HHProSignIn extends HHElement {
  static properties = {
    ...HHElement.properties,
    dealerName: { state: true },
    message: { state: true },
    busy: { state: true },
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
      .foot { margin: 0; color: var(--hh-text-dim); font-size: 0.875rem; text-align: center; }
    `,
  ];

  declare dealerName: string;
  declare message: string;
  declare busy: boolean;

  constructor() {
    super();
    this.dealerName = "";
    this.message = "";
    this.busy = false;
  }

  readonly #onClick = (event: Event): void => {
    const control = event.composedPath().find((node): node is Element => node instanceof Element && node.getAttribute("data-action") === "sign-in");
    if (control !== undefined) this.#submit();
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

  render(): TemplateResult {
    const dealer = this.dealerName === "" ? "Your dealer" : this.dealerName;
    return html`<main>
      <div class="lockup">
        <span class="mark" aria-hidden="true">H</span>
        <h1>HH <span class="pro">Pro</span></h1>
        <p class="sub">${dealer} · the Pro portal for contractor accounts</p>
      </div>
      <section class="card" aria-labelledby="signin-title">
        <h2 id="signin-title">Sign in</h2>
        <p class="hint">Use the email your contractor account knows.</p>
        <label>Email
          <input class="control" data-control="text" data-field="email" data-testid="signin-email" type="email" autocomplete="username" inputmode="email" />
        </label>
        <label>Password
          <input class="control" data-control="text" data-field="password" data-testid="signin-password" type="password" autocomplete="current-password" />
        </label>
        <p class="error" role="alert" data-testid="signin-message">${this.message}</p>
        ${button({ label: this.busy ? "Signing in" : "Sign in", action: "sign-in", disabled: this.busy })}
      </section>
      <p class="foot">No account yet? ${dealer} invites you.</p>
    </main>`;
  }
}

defineElement("hh-pro-signin", HHProSignIn);
