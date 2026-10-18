// The Sign in methods surface's screen: Google, Microsoft, Email and password, Magic link.
// Pure template function matching the Change 11 Sign in methods board.
import { html, type TemplateResult } from "lit";

export interface SignInMethod {
  readonly id: string;
  readonly name: string;
  readonly description: string;
  readonly enabled: boolean;
}

export const SIGN_IN_METHODS: readonly SignInMethod[] = [
  {
    id: "google",
    name: "Google",
    description: "People sign in with their Google account.",
    enabled: true,
  },
  {
    id: "microsoft",
    name: "Microsoft",
    description: "People sign in with their Microsoft work account.",
    enabled: false,
  },
  {
    id: "email",
    name: "Email and password",
    description: "A person signs in with the email and the password they chose from the invitation.",
    enabled: true,
  },
  {
    id: "magic-link",
    name: "Magic link",
    description: "A one time link sent to the person's email.",
    enabled: false,
  },
];

export function renderSignInMethods(): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Sign in methods</h1>
      <p class="page-sub">How this dealer's people sign in. New staff follow these.</p>
    </header>

    <section class="card" data-test="sign-in-methods-card">
      <div class="card-head">
        <h2 class="card-title">Methods</h2>
      </div>
      <div class="list-stack">
        ${SIGN_IN_METHODS.map(
          (m) => html`
            <div class="pill-row ${m.enabled ? "pill-row-active" : ""}">
              <div class="pill-words">
                <div class="pill-title"><strong>${m.name}</strong></div>
                <div class="pill-desc">${m.description}</div>
              </div>
              <div class="toggle-wrap">
                <div class="toggle-switch ${m.enabled ? "toggle-switch-on" : ""}" role="switch" aria-checked=${m.enabled ? "true" : "false"}>
                  <span class="toggle-knob"></span>
                </div>
              </div>
            </div>
          `,
        )}
      </div>
    </section>
  `;
}
