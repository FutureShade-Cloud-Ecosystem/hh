// The console mode's screens, as Lit templates over a ConsoleSession: the
// starting moment, the configuration error page, the sign in screen and the
// role entry. They hold no state of their own; the app root renders them and
// routes their clicks (every control carries a data-action, read off the host
// by the root's one listener, the rule the rest of this app follows).
//
// The visual reference is the original app's own sign in card (the lockup
// above two labelled fields, one gold button, an OR divider, the Google
// button), improved: the dealer's own lockup, one primary action, a visible
// label and 44 px target on every control, a polite status line that is
// always rendered, wrong credentials that keep the fields and return focus.
import { html, nothing, type TemplateResult } from "lit";
import type { DealerBrand, RuntimeConfigFailure } from "@hardscapehouse/auth/runtime-config";
import type { ConsoleSession } from "./console.js";
import { CONFIG_ERROR_WORDS, SIGN_IN_WORDS, dealerLine } from "./signin.js";
import type { LiveRole } from "./roles.js";

/** The gable diamond over two bars in a ring: the tenant kit's mark, drawn in the ink the page is on and the house gold. */
function gableMark(): TemplateResult {
  return html`<svg class="dealer-mark" viewBox="0 0 64 64" aria-hidden="true" focusable="false">
    <circle cx="32" cy="32" r="30" fill="none" stroke="currentColor" stroke-width="4"></circle>
    <rect x="24.5" y="10" width="15" height="15" fill="#FFC313" transform="rotate(45 32 17.5)"></rect>
    <rect x="16.5" y="31" width="31" height="5" fill="currentColor"></rect>
    <rect x="21" y="39" width="22" height="5" fill="currentColor"></rect>
  </svg>`;
}

/** The dealer's name split the way the kit sets it: the first word large, the rest tracked beneath. */
export function dealerWords(name: string): { head: string; tail: string } {
  const trimmed = name.trim();
  const space = trimmed.indexOf(" ");
  return space < 0 ? { head: trimmed, tail: "" } : { head: trimmed.slice(0, space), tail: trimmed.slice(space + 1) };
}

export { dealerLine };

export function dealerLockup(dealer: DealerBrand): TemplateResult {
  const words = dealerWords(dealer.name);
  return html`
    <div class="dealer" data-testid="dealer-lockup">
      ${dealer.mark === "gable"
        ? gableMark()
        : html`<span class="dealer-mark initial" aria-hidden="true">${words.head.slice(0, 1).toUpperCase()}</span>`}
      <span class="dealer-names">
        <span class="dealer-name">${words.head}</span>
        ${words.tail === "" ? nothing : html`<span class="dealer-sub">${words.tail}</span>`}
      </span>
    </div>
  `;
}

export function bootScreen(): TemplateResult {
  return html`<div class="stage"><p class="intro" role="status" aria-live="polite">Loading.</p></div>`;
}

/** The page a copy shows when its configuration is unusable. Never the demo cards. */
export function configErrorScreen(reason: RuntimeConfigFailure): TemplateResult {
  const words = CONFIG_ERROR_WORDS[reason];
  return html`
    <div class="stage">
      <div class="config-error" role="alert" data-testid="config-error">
        <h2>${words.title}</h2>
        <p>${words.body}</p>
        <button type="button" class="btn btn-secondary" data-action="reload">Reload the page</button>
      </div>
    </div>
  `;
}

function methodsBlock(session: ConsoleSession, busy: boolean, invalid: boolean): TemplateResult {
  const methods = session.methods;
  if (methods.kind === "loading") {
    return html`<p class="methods-note" aria-busy="true" data-testid="methods-loading">Loading the sign in options.</p>`;
  }
  if (methods.kind === "failed") {
    return html`
      <p class="methods-note" data-testid="methods-failed">${SIGN_IN_WORDS["methods_unavailable"]}</p>
      <button type="button" class="btn btn-secondary" data-action="methods-retry">Try again</button>
    `;
  }
  if (methods.methods.length === 0) {
    return html`<p class="methods-note" data-testid="methods-none">${SIGN_IN_WORDS["no_methods"]}</p>`;
  }
  const parts: TemplateResult[] = methods.methods.map((method) =>
    method === "google" ? googleButton(busy) : emailForm(session, busy, invalid),
  );
  const out: TemplateResult[] = [];
  parts.forEach((part, index) => {
    if (index > 0) out.push(html`<div class="or" role="separator" aria-label="or">or</div>`);
    out.push(part);
  });
  return html`${out}`;
}

function googleButton(busy: boolean): TemplateResult {
  return html`
    <button type="button" class="btn btn-secondary btn-wide" data-action="google" data-testid="google-button" ?disabled=${busy}>
      <span class="google-mark" aria-hidden="true">G</span>
      Continue with Google
    </button>
  `;
}

// The fields are read only while a sign in is in flight so focus and the typed values stay; the
// root sets that in its own update pass (the design system's position gate keeps ?readonly out of
// templates, the same wiring pass the shared header and the widget use).
function emailForm(session: ConsoleSession, busy: boolean, invalid: boolean): TemplateResult {
  const signingIn = session.phase === "signing-in";
  return html`
    <form class="signin-form" data-form="signin" method="post" action="#" novalidate aria-describedby="signin-status">
      <div class="field">
        <label for="signin-email">Email</label>
        <input
          id="signin-email"
          name="email"
          class="control"
          type="email"
          autocomplete="username"
          inputmode="email"
          autocapitalize="none"
          autocorrect="off"
          spellcheck="false"
          required
        />
      </div>
      <div class="field">
        <label for="signin-password">Password</label>
        <input
          id="signin-password"
          name="password"
          class="control"
          type="password"
          autocomplete="current-password"
          required
          aria-invalid=${invalid ? "true" : "false"}
        />
      </div>
      <button type="submit" class="btn btn-primary btn-wide" data-testid="signin-submit" ?disabled=${busy}>
        ${signingIn ? html`<span class="spinner" aria-hidden="true"></span>Signing in` : "Sign in"}
      </button>
    </form>
  `;
}

/** The sign in screen: the dealer, the methods the dealer allows, one status line. */
export function signInScreen(session: ConsoleSession, personaBlock: TemplateResult | typeof nothing): TemplateResult {
  const busy = session.phase === "signing-in";
  const notice = session.notice;
  const invalid = notice.tone === "error" && notice.text === SIGN_IN_WORDS["bad_credentials"];
  return html`
    <div class="signin-stage">
      <section class="signin-card" aria-labelledby="signin-title">
        ${dealerLockup(session.config.dealer)}
        <h1 class="signin-title" id="signin-title">Sign in to Work</h1>
        <p class="signin-lead" data-testid="signin-dealer-line">${dealerLine(session.config.dealer)}</p>
        <p
          id="signin-status"
          class="status ${notice.tone === "error" ? "error" : ""}"
          role="status"
          aria-live="polite"
          aria-atomic="true"
          data-testid="signin-status"
        >${notice.text}</p>
        ${methodsBlock(session, busy, invalid)}
      </section>
      ${personaBlock}
    </div>
  `;
}

/** The persona cards, in the front door's own card style, each with its own refusal. */
export function personaCards(
  session: ConsoleSession,
  card: (role: LiveRole, refusal: string, busy: boolean) => TemplateResult,
  coming: () => TemplateResult,
): TemplateResult {
  return html`
    <div class="cards" data-testid="persona-cards">
      ${session.personaRoles.map((role) => card(role, session.personaRefusal(role.flow), session.personaBusy === role.flow))}
    </div>
    ${coming()}
  `;
}
