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
import { CONFIG_ERROR_WORDS, SIGN_IN_WORDS, dealerLine, firstNameOf, pickerGreeting } from "./signin.js";
import { primaryTileFor, type LiveRole } from "./roles.js";

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

function googleMark(): TemplateResult {
  return html`<svg class="google-mark" viewBox="0 0 18 18" width="18" height="18" aria-hidden="true" focusable="false">
    <path fill="#4285F4" d="M17.64 9.2c0-.637-.057-1.251-.164-1.84H9v3.481h4.844a4.14 4.14 0 0 1-1.796 2.716v2.259h2.908c1.702-1.567 2.684-3.875 2.684-6.615Z"></path>
    <path fill="#34A853" d="M9 18c2.43 0 4.467-.806 5.956-2.18l-2.908-2.259c-.806.54-1.837.86-3.048.86-2.344 0-4.328-1.584-5.036-3.711H.957v2.332A8.997 8.997 0 0 0 9 18Z"></path>
    <path fill="#FBBC05" d="M3.964 10.71A5.41 5.41 0 0 1 3.682 9c0-.593.102-1.17.282-1.71V4.958H.957A8.996 8.996 0 0 0 0 9c0 1.452.348 2.827.957 4.042l3.007-2.332Z"></path>
    <path fill="#EA4335" d="M9 3.58c1.321 0 2.508.454 3.44 1.345l2.582-2.58C13.463.891 11.426 0 9 0A8.997 8.997 0 0 0 .957 4.958L3.964 7.29C4.672 5.163 6.656 3.58 9 3.58Z"></path>
  </svg>`;
}

function googleButton(busy: boolean): TemplateResult {
  return html`
    <button type="button" class="btn btn-secondary btn-wide" data-action="google" data-testid="google-button" ?disabled=${busy}>
      ${googleMark()}
      Continue with Google
    </button>
  `;
}

function statusLine(session: ConsoleSession): TemplateResult {
  const notice = session.notice;
  return html`
    <p
      id="signin-status"
      class="status ${notice.tone === "error" ? "error" : ""}"
      role="status"
      aria-live="polite"
      aria-atomic="true"
      data-testid="signin-status"
    >${notice.text}</p>
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
      ${statusLine(session)}
      <button type="submit" class="btn btn-primary btn-wide" data-testid="signin-submit" ?disabled=${busy}>
        ${signingIn ? html`<span class="spinner" aria-hidden="true"></span>Signing in` : "Sign in"}
      </button>
    </form>
  `;
}

/** The sign in screen: the dealer, the methods the dealer allows, one status line. */
export function signInScreen(
  session: ConsoleSession,
  personaBlock: TemplateResult | typeof nothing,
  footControls: TemplateResult | typeof nothing = nothing,
): TemplateResult {
  const busy = session.phase === "signing-in";
  const notice = session.notice;
  const invalid = notice.tone === "error" && notice.text === SIGN_IN_WORDS["bad_credentials"];
  const hasEmail = session.methods.kind === "ready" && session.methods.methods.includes("email_password");
  return html`
    <div class="signin-stage">
      <section class="signin-card" aria-labelledby="signin-title">
        ${dealerLockup(session.config.dealer)}
        <h1 class="signin-title" id="signin-title">Sign in to Work</h1>
        <p class="signin-lead" data-testid="signin-dealer-line">${dealerLine(session.config.dealer)}</p>
        ${hasEmail ? nothing : statusLine(session)}
        ${methodsBlock(session, busy, invalid)}
      </section>
      <footer class="signin-footer" data-testid="signin-footer">
        <p class="signin-footer-copy">Powered by HardscapeOS</p>
        ${footControls}
      </footer>
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

function radioCheckMark(): TemplateResult {
  return html`<svg class="picker-check-mark" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true" focusable="false">
    <path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="M2.5 6.2 5 8.7 9.5 3.5"></path>
  </svg>`;
}

/** The role and branch picker after sign in: greet by first name and period in houseZone, choose role and branch, and open the chosen role's primary app. */
export function roleEntryScreen(
  session: ConsoleSession,
  personaBlock: TemplateResult | typeof nothing,
  footControls: TemplateResult | typeof nothing = nothing,
): TemplateResult {
  const who = session.who;
  const chosenRole = session.chosenRole;
  const initial = firstNameOf(who?.name ?? "").slice(0, 1).toUpperCase() || "?";
  const multiBranch = session.isMultiBranchRole && session.branches.length > 1;
  const activeBranchOption = session.activeBranchOptionId;
  const primary = chosenRole === null ? null : primaryTileFor(chosenRole);
  return html`
    <div class="picker-stage">
      ${dealerLockup(session.config.dealer)}
      <section class="entry-card" aria-labelledby="entry-title" data-testid="role-entry">
        <h1 class="entry-greeting" id="entry-title">${pickerGreeting(who?.name ?? "")}</h1>
        <div class="account-row">
          <div class="account-who">
            <span class="account-avatar" aria-hidden="true">${initial}</span>
            <span class="account-name" data-testid="entry-name"
              >${who?.name ?? ""}${chosenRole !== null ? ` · ${chosenRole.label}` : ""}</span
            >
          </div>
          <button type="button" class="not-you-btn" data-action="sign-out" data-testid="entry-sign-out">
            Not you? Sign out
          </button>
        </div>
        ${chosenRole === null || primary === null
          ? html`<p data-testid="entry-no-screens">This site has no screens for your role yet. Ask your administrator.</p>`
          : html`
              <div class="picker-group">
                <span class="picker-group-label" id="role-group-label">YOUR ROLE</span>
                <div class="picker-radio-list" role="radiogroup" aria-labelledby="role-group-label" data-testid="role-radiogroup">
                  ${session.heldRoles.map((role) => {
                    const checked = chosenRole.id === role.id;
                    const rolePrimary = primaryTileFor(role);
                    return html`
                      <button
                        type="button"
                        class="picker-option ${checked ? "checked" : ""}"
                        role="radio"
                        aria-checked=${checked ? "true" : "false"}
                        data-tabstop=${checked ? "true" : "false"}
                        data-action="select-role"
                        data-role=${role.id}
                      >
                        <span class="picker-radio-dot" aria-hidden="true">${checked ? radioCheckMark() : nothing}</span>
                        <span class="picker-option-words">
                          <span class="picker-option-title">${role.label}</span>
                          <span class="picker-option-sub">Opens ${rolePrimary.name}</span>
                        </span>
                      </button>
                    `;
                  })}
                </div>
              </div>
              <div class="picker-group">
                <span class="picker-group-label" id="branch-group-label">${multiBranch ? "YOUR BRANCHES" : "YOUR BRANCH"}</span>
                ${multiBranch
                  ? html`
                      <div
                        class="picker-branch-list multi"
                        role="group"
                        aria-labelledby="branch-group-label"
                        data-testid="branch-group"
                      >
                        <button
                          type="button"
                          class="picker-option branch-option ${session.allBranchesChosen ? "checked" : ""}"
                          aria-pressed=${session.allBranchesChosen ? "true" : "false"}
                          data-tabstop=${activeBranchOption === "__all__" ? "true" : "false"}
                          data-action="select-branch"
                          data-branch-id="__all__"
                        >
                          <span class="picker-radio-dot" aria-hidden="true"
                            >${session.allBranchesChosen ? radioCheckMark() : nothing}</span
                          >
                          <span class="picker-option-words">
                            <span class="picker-option-title">All branches</span>
                          </span>
                        </button>
                        ${session.branches.map((branch) => {
                          const checked = session.chosenBranchIds.includes(branch.id);
                          return html`
                            <button
                              type="button"
                              class="picker-option branch-option ${checked ? "checked" : ""}"
                              aria-pressed=${checked ? "true" : "false"}
                              data-tabstop=${activeBranchOption === branch.id ? "true" : "false"}
                              data-action="select-branch"
                              data-branch-id=${branch.id}
                            >
                              <span class="picker-radio-dot" aria-hidden="true">${checked ? radioCheckMark() : nothing}</span>
                              <span class="picker-option-words">
                                <span class="picker-option-title">${branch.name}</span>
                              </span>
                            </button>
                          `;
                        })}
                      </div>
                    `
                  : html`
                      <div
                        class="picker-branch-list"
                        role="radiogroup"
                        aria-labelledby="branch-group-label"
                        data-testid="branch-radiogroup"
                      >
                        ${session.branches.map((branch) => {
                          const checked = session.chosenBranchIds.includes(branch.id);
                          return html`
                            <button
                              type="button"
                              class="picker-option branch-option ${checked ? "checked" : ""}"
                              role="radio"
                              aria-checked=${checked ? "true" : "false"}
                              data-tabstop=${activeBranchOption === branch.id ? "true" : "false"}
                              data-action="select-branch"
                              data-branch-id=${branch.id}
                            >
                              <span class="picker-radio-dot" aria-hidden="true">${checked ? radioCheckMark() : nothing}</span>
                              <span class="picker-option-words">
                                <span class="picker-option-title">${branch.name}</span>
                              </span>
                            </button>
                          `;
                        })}
                      </div>
                    `}
              </div>
              <button
                type="button"
                class="btn btn-primary btn-wide picker-submit"
                data-action="continue-own"
                data-testid="continue-own"
                data-href=${primary.href ?? nothing}
              >
                Open ${primary.name} <span aria-hidden="true">›</span>
              </button>
            `}
      </section>
      ${personaBlock}
      <footer class="signin-footer" data-testid="entry-footer">
        <p class="signin-footer-copy">Powered by HardscapeOS</p>
        ${footControls}
      </footer>
    </div>
  `;
}
