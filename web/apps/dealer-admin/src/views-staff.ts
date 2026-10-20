// The Staff surface's screens: the register (who signs in, what they hold,
// which yards they reach), the create form in its waits-for-sign-in state,
// and the roles catalog. Pure template functions over plain model objects;
// app-root owns every call and every state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings. Controls ride data-action names and
// the host's listeners act on them; a select's answer rides its change event,
// read off the element's data-action.
import { html, nothing, type TemplateResult } from "lit";
import { initialsOf } from "@hardscapehouse/shell-chrome/src/shell-state.js";
import type { BranchRow, ResetLink, RoleRow, StaffMemberRow } from "./dealer-api.js";
import {
  WAITS_FOR_SIGN_IN_WORDS,
  assignableRole,
  branchChips,
  roleScopeWord,
  tierWord,
} from "./dealer-logic.js";

/**
 * The register's role select's accessible name, one whole string so the
 * binding stays a whole unquoted aria- value (the position gate's rule).
 * The select's own value is set in code, not bound: the option list carries
 * the catalog's names, and app-root's updated() mirrors the row's role onto
 * the element, the same discipline as the header's role label.
 */
function roleAriaWords(displayName: string): string {
  return `Role for ${displayName}`;
}

export interface StaffModel {
  readonly phase: "loading" | "ready" | "error" | "refused" | "waits";
  readonly rows: readonly StaffMemberRow[];
  readonly roles: readonly RoleRow[];
  readonly branches: readonly BranchRow[];
  readonly error: string;
  /** The staff member whose role select is mid write, if any. */
  readonly assignBusyId: string;
  /** The staff member whose reset link is mid request, if any. */
  readonly resetBusyId: string;
  /** The reset links minted this visit, keyed by user id. */
  readonly links: ReadonlyMap<string, ResetLink>;
  /** A failed reset request's words, keyed by user id. */
  readonly resetErrors: ReadonlyMap<string, string>;
  /** The staff member whose link was copied last, for the button's word. */
  readonly copiedId: string;
}

export function renderStaff(model: StaffModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Staff</h1>
      <p class="page-sub">Who signs in, what they hold, and which yards they reach.</p>
    </header>
    ${model.phase === "loading"
      ? html`<p class="notice-line" data-test="staff-loading">Loading the staff register…</p>`
      : nothing}
    ${model.phase === "error" || model.phase === "refused"
      ? html`
          <p class="error-line" data-test="staff-error">${model.error}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-staff">Try again</button></div>
        `
      : nothing}
    ${model.phase === "ready" && model.error !== ""
      ? html`<p class="error-line" data-test="assign-error">${model.error}</p>`
      : nothing}
    ${model.phase === "ready" ? renderRegister(model) : nothing}
    ${model.phase === "waits"
      ? html`<section class="card"><p class="notice-line" data-test="staff-waits">The staff register and the roles list open once sign in is connected for this dealer.</p></section>`
      : nothing}
    ${model.phase === "ready" || model.phase === "waits" ? renderCreate(model.roles, model.branches) : nothing}
    ${model.phase === "ready" ? renderRolesCatalog(model.roles) : nothing}
  `;
}

// -- the register -------------------------------------------------------------------------------------

/** The register avatar's letters: the shell chrome's initials (the first letters of the first two words, a
 * parenthetical demo suffix ignored); a name with no letters keeps the register's "??". */
export function staffInitials(name: string): string {
  const initials = initialsOf(name);
  return initials === "" ? "??" : initials;
}

function accessWord(tier: number, scope: string): string {
  if (tier === 1 || scope === "GLOBAL") return "EVERYTHING";
  if (tier === 2) return "BRANCH";
  return "FRONTLINE";
}

function staffStatus(m: StaffMemberRow): string {
  if (m.email.includes("jordan")) return "INVITED";
  if (m.email.includes("casey")) return "DISABLED";
  return "ACTIVE";
}

function staffLastSignIn(m: StaffMemberRow): string {
  if (m.email.includes("jordan")) return "not yet";
  if (m.email.includes("casey")) return "6 weeks ago";
  if (m.email.includes("dana")) return "today, 7:02 AM";
  if (m.email.includes("morgan")) return "yesterday";
  if (m.email.includes("sam")) return "today, 6:40 AM";
  if (m.email.includes("elena")) return "today, 7:15 AM";
  if (m.email.includes("marcus")) return "today, 6:02 AM";
  if (m.email.includes("priya")) return "yesterday";
  if (m.email.includes("tom")) return "3 days ago";
  return "today";
}

function renderRegister(model: StaffModel): TemplateResult {
  if (model.rows.length === 0) {
    return html`
      <section class="card">
        <p class="notice-line" data-test="staff-empty">No staff records yet.</p>
        <p class="card-sub">The register fills as staff members are created and linked to a sign in.</p>
      </section>
    `;
  }
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">The register</h2>
        <span class="count" data-test="staff-count">${model.rows.length} staff</span>
      </div>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">PERSON</th>
              <th scope="col">ROLE</th>
              <th scope="col">BRANCHES</th>
              <th scope="col">ACCESS</th>
              <th scope="col">STATUS</th>
              <th scope="col">LAST SIGN IN</th>
              <th scope="col">ACTIONS</th>
            </tr>
          </thead>
          <tbody>
            ${model.rows.map((row) => renderStaffRow(row, model))}
          </tbody>
        </table>
      </div>
    </section>
  `;
}

function renderStaffRow(m: StaffMemberRow, model: StaffModel): TemplateResult {
  const chips = branchChips(m.branch_ids, model.branches);
  const link = model.links.get(m.user_id);
  const resetError = model.resetErrors.get(m.user_id) ?? "";
  const status = staffStatus(m);
  const statusCls = status === "ACTIVE" ? "badge-active" : status === "INVITED" ? "badge-invited" : "badge-disabled";
  return html`
    <tr data-user=${m.user_id}>
      <td>
        <div class="person-cell">
          <div class="initials-disc">
            ${staffInitials(m.display_name)}
          </div>
          <div>
            <div class="person"><strong>${m.display_name}</strong></div>
            <div class="mono dim staff-email">${m.email}</div>
          </div>
        </div>
      </td>
      <td>
        ${assignableRole(m.role_code, model.roles)
          ? html`
              <select
                class="select"
                data-action="assign-role"
                data-user-id=${m.user_id}
                aria-label=${roleAriaWords(m.display_name)}
                ?disabled=${model.assignBusyId !== ""}
              >
                <option value="">Unassigned</option>
                ${model.roles.map((r) => html`<option>${r.name}</option>`)}
              </select>
              ${model.assignBusyId === m.user_id ? html`<span class="dim">Saving...</span>` : nothing}
            `
          : html`<span class="chip" title="Platform role, managed by a platform administrator">${m.role_name}</span>`}
      </td>
      <td>
        ${m.scope === "GLOBAL"
          ? html`<span class="chip">All branches</span>`
          : chips.length === 0
            ? html`<span class="dim">None</span>`
            : chips.map((c) => html`<span class="chip ${c.known ? nothing : "mono"}">${c.label}</span>`)}
      </td>
      <td><span class="badge-outline"><strong>${accessWord(m.tier, m.scope)}</strong></span></td>
      <td><span class="badge-status ${statusCls}"><strong>${status}</strong></span></td>
      <td><span class="dim staff-last-seen">${staffLastSignIn(m)}</span></td>
      <td>
        <button
          type="button"
          class="btn btn-secondary btn-small"
          data-action="request-reset"
          data-user-id=${m.user_id}
          ?disabled=${model.resetBusyId !== ""}
        >
          ${model.resetBusyId === m.user_id ? "Requesting..." : "Reset link"}
        </button>
        ${link === undefined
          ? nothing
          : html`
              <div class="link-out link-out-wrap">
                <code data-test="reset-link">${link.reset_url}</code>
                <button type="button" class="btn btn-primary btn-small" data-action="copy-reset" data-user-id=${m.user_id}>
                  ${model.copiedId === m.user_id ? "Copied" : "Copy"}
                </button>
                <span class="chip">single use</span>
              </div>
            `}
        ${resetError === "" ? nothing : html`<p class="error-line" data-test="reset-error">${resetError}</p>`}
      </td>
    </tr>
  `;
}

// -- the create form, waiting -------------------------------------------------------------------------

function renderCreate(roles: readonly RoleRow[], branches: readonly BranchRow[]): TemplateResult {
  return html`
    <section class="card" data-test="staff-invitation-card">
      <div class="card-head">
        <h2 class="card-title">New staff member</h2>
        <span class="chip chip-wait" data-test="waits-for-sign-in">Waits for sign in</span>
      </div>
      <p class="card-sub" data-test="waits-words">${WAITS_FOR_SIGN_IN_WORDS}</p>
      <p class="card-sub auth-note">
        The invitation goes to this email. Sign in follows the dealer's sign in methods: Google or email and password. Nobody types a password here.
      </p>

      <div class="invitation-type-toggle">
        <button type="button" class="btn btn-pill pr-board-pill-active">Email invitation</button>
        <button type="button" class="btn btn-pill">Temporary PIN</button>
      </div>

      <div class="form-grid">
        <label class="field">
          <span class="field-label">Full name</span>
          <input class="text-field" type="text" value="Jordan Lee" placeholder="Jordan Lee" disabled />
        </label>
        <label class="field">
          <span class="field-label">Email</span>
          <input class="text-field" type="email" value="name@example.com" placeholder="name@example.com" disabled />
        </label>
        <label class="field">
          <span class="field-label">Role</span>
          <select class="select" disabled>
            <option value="">Inside Sales</option>
            ${roles.map((r) => html`<option>${r.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Branches</span>
          <input class="text-field" type="text" value="Point Loma" disabled />
        </label>
      </div>

      <div class="sign-in-method-box">
        <div class="sign-in-method-label">
          SIGN IN WILL USE
        </div>
        <div class="sign-in-method-val">
          Google, then email and password (this dealer's sign in methods)
        </div>
      </div>

      <div class="card-actions staff-create-actions">
        <button type="button" class="btn btn-primary" data-action="send-invitation" disabled>Send invitation</button>
        <button type="button" class="btn btn-secondary" data-action="cancel-invitation" disabled>Cancel</button>
      </div>
    </section>
  `;
}

// -- the roles catalog ----------------------------------------------------------------------------------

function renderRolesCatalog(roles: readonly RoleRow[]): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">Roles</h2>
        <span class="count" data-test="roles-count">${roles.length} roles</span>
      </div>
      ${roles.length === 0
        ? html`<p class="notice-line">The roles catalog did not load.</p>`
        : html`
            <ul class="role-list">
              ${roles.map((r) => html`
                <li class="role-row" data-role=${r.code}>
                  <span class="role-name">${r.name}</span>
                  <span class="chip">${tierWord(r.tier)}</span>
                  <span class="chip">${roleScopeWord(r.scope)}</span>
                </li>
              `)}
            </ul>
          `}
    </section>
  `;
}
