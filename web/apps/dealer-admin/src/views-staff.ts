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
  readonly phase: "loading" | "ready" | "error" | "refused";
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
    ${model.phase === "ready" ? renderCreate(model.roles, model.branches) : nothing}
    ${model.phase === "ready" ? renderRolesCatalog(model.roles) : nothing}
  `;
}

// -- the register -------------------------------------------------------------------------------------

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
              <th scope="col">Person</th>
              <th scope="col">Role</th>
              <th scope="col">Rank</th>
              <th scope="col">Reach</th>
              <th scope="col">Branches</th>
              <th scope="col">Password reset</th>
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
  return html`
    <tr data-user=${m.user_id}>
      <td>
        <div class="person">${m.display_name}</div>
        <div class="mono dim">${m.email}</div>
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
              ${model.assignBusyId === m.user_id ? html`<span class="dim">Saving…</span>` : nothing}
            `
          : html`<span class="chip" title="Platform role, managed by a platform administrator">${m.role_name}</span>`}
      </td>
      <td><span class="chip">${tierWord(m.tier)}</span></td>
      <td><span class="chip">${roleScopeWord(m.scope)}</span></td>
      <td>
        ${chips.length === 0
          ? html`<span class="dim">None</span>`
          : chips.map((c) => html`<span class="chip ${c.known ? nothing : "mono"}">${c.label}</span>`)}
      </td>
      <td>
        <button
          type="button"
          class="btn btn-secondary btn-small"
          data-action="request-reset"
          data-user-id=${m.user_id}
          ?disabled=${model.resetBusyId !== ""}
        >
          ${model.resetBusyId === m.user_id ? "Requesting…" : "Reset link"}
        </button>
        ${link === undefined
          ? nothing
          : html`
              <div class="link-out">
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
  // THE CORE HAS NO CREATE STAFF ROUTE YET. The parity ledger holds the staff
  // identity admin surface for the auth lane, so the form renders whole and
  // disabled, and the marker words say what waits. No submit exists to fake.
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">New staff member</h2>
        <span class="chip chip-wait" data-test="waits-for-sign-in">Waits for sign in</span>
      </div>
      <p class="card-sub" data-test="waits-words">${WAITS_FOR_SIGN_IN_WORDS}</p>
      <div class="form-grid">
        <label class="field">
          <span class="field-label">Full name</span>
          <input class="text-field" type="text" disabled />
        </label>
        <label class="field">
          <span class="field-label">Email</span>
          <input class="text-field" type="email" disabled />
        </label>
        <label class="field">
          <span class="field-label">Role</span>
          <select class="select" disabled>
            <option value="">Choose a role</option>
            ${roles.map((r) => html`<option>${r.name}</option>`)}
          </select>
        </label>
        <label class="field">
          <span class="field-label">Initial password</span>
          <input class="text-field" type="password" disabled />
        </label>
        <div class="field field-full">
          <span class="field-label">Branch access</span>
          <div class="branch-choices" role="group" aria-label="Branch access">
            ${branches.map((b) => html`
              <label class="branch-choice">
                <input type="checkbox" disabled />
                ${b.name}
              </label>
            `)}
          </div>
        </div>
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
