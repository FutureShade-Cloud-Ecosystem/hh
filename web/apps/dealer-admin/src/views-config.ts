// The Role configuration surface's screens: the role by surface grid (what
// each job shape has curated on every surface), the curation editor for one
// cell, the caller's own override and workspace, and the job shape catalog.
// Pure template functions over plain model objects; app-root owns every call
// and every state change.
//
// THE POSITION RULES (the design system's gate scans this package): attribute
// bindings only, no "." or "@" bindings. Controls ride data-action names and
// the host's listeners act on them; a select's answer rides its change event,
// a text or number field mirrors through data-field and updated(), and a
// checkbox reports its own toggled state on the click the host listener reads.
//
// NO ORIGINAL SCREEN EXISTS for a per dealer role configuration; the visual
// references are this app's own staff surface (the register's card and table
// style) and Manager Home's matrix words, with the editor's shape following
// the original's DIB-507 screen settings page (quote-surface-settings.ts,
// items half). PROVENANCE.md carries the rows.
import { html, nothing, type TemplateResult } from "lit";
import type { ConfigItem } from "./config-logic.js";
import type { CellConfig } from "./config-api.js";
import {
  CONFIG_SURFACES,
  ITEM_KINDS,
  JOB_SHAPES,
  cellWords,
  itemRowError,
  itemSetError,
  rolesForShape,
  shapeWord,
  surfaceItemsFor,
  surfaceWord,
} from "./config-logic.js";
import type { RoleRow } from "./dealer-api.js";

// -- the models --------------------------------------------------------------------------------------

/** One grid cell: the read's answer for one surface and job shape. */
export interface ConfigCell {
  readonly config: CellConfig;
}

/** The workspace form's draft, the fields the my-workspace save carries. */
export interface WorkspaceDraft {
  readonly split_pct: number;
  readonly arrangement: string;
  readonly primary_collapsed: boolean;
  readonly secondary_collapsed: boolean;
  readonly cost_hidden: boolean;
  /** Where the row came from, as the read answered: the person or the dealer. */
  readonly source: string;
}

/** The open editor for one cell, with its three drafts and its words. */
export interface ConfigEditor {
  readonly surface: string;
  readonly profile: string;
  /** The dealer's curation draft: what a save PUTs whole. */
  readonly draft: readonly ConfigItem[];
  /** The caller's own override draft: what a save PUTs whole. */
  readonly myDraft: readonly ConfigItem[];
  /** The caller's workspace draft. */
  readonly wsDraft: WorkspaceDraft;
  /** The add row's kind and id, mirrored through updated(). */
  readonly addKind: string;
  readonly addId: string;
  readonly myAddKind: string;
  readonly myAddId: string;
  readonly error: string;
  readonly notice: string;
  readonly busy: boolean;
}

export interface RoleConfigModel {
  readonly phase: "loading" | "ready" | "refused" | "error";
  /** One answer per surface and job shape, keyed by cellKey. */
  readonly cells: ReadonlyMap<string, ConfigCell>;
  readonly loaded: number;
  readonly total: number;
  readonly words: string;
  readonly roles: readonly RoleRow[];
  readonly editor: ConfigEditor | null;
}

/** A whole cell key: surface and job shape, joined for the model's map. */
export function cellKey(surface: string, profile: string): string {
  return surface + "|" + profile;
}

export function renderRoleConfig(model: RoleConfigModel): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Role configuration</h1>
      <p class="page-sub">What each job shape sees on every surface, curated per role.</p>
    </header>
    ${model.phase === "loading"
      ? html`<p class="notice-line" data-test="config-loading">Reading what every job shape sees on every surface... (${model.loaded} of ${model.total})</p>`
      : nothing}
    ${model.phase === "refused" || model.phase === "error"
      ? html`
          <p class="error-line" data-test="config-error">${model.words}</p>
          <div class="row-actions"><button type="button" class="btn btn-secondary" data-action="reload-role-config">Try again</button></div>
        `
      : nothing}
    ${model.phase === "ready" ? renderRoleMatrix() : nothing}
    ${model.phase === "ready" ? renderGrid(model) : nothing}
    ${model.phase === "ready" ? renderCatalog(model.roles) : nothing}
    ${model.editor !== null ? renderEditor(model.editor) : nothing}
  `;
}

export interface RoleMatrixEntry {
  readonly role: string;
  readonly till: "Full" | "View" | "None";
  readonly quotes: "Full" | "View" | "None";
  readonly yard: "Full" | "View" | "None";
  readonly drive: "Full" | "View" | "None";
  readonly dispatch: "Full" | "View" | "None";
  readonly buying: "Full" | "View" | "None";
  readonly money: "Full" | "View" | "None";
  readonly manager: "Full" | "View" | "None";
  readonly messages: "Full" | "View" | "None";
  readonly admin: "Full" | "View" | "None";
}

export const ROLE_MATRIX_ENTRIES: readonly RoleMatrixEntry[] = [
  { role: "General Manager", till: "View", quotes: "View", yard: "View", drive: "View", dispatch: "Full", buying: "Full", money: "Full", manager: "Full", messages: "Full", admin: "Full" },
  { role: "Financial Controller", till: "View", quotes: "View", yard: "None", drive: "None", dispatch: "None", buying: "Full", money: "Full", manager: "View", messages: "Full", admin: "None" },
  { role: "Branch Manager", till: "Full", quotes: "Full", yard: "Full", drive: "View", dispatch: "Full", buying: "Full", money: "View", manager: "Full", messages: "Full", admin: "None" },
  { role: "Sales Manager", till: "Full", quotes: "Full", yard: "None", drive: "None", dispatch: "View", buying: "View", money: "View", manager: "View", messages: "Full", admin: "None" },
  { role: "Yard Manager", till: "None", quotes: "View", yard: "Full", drive: "View", dispatch: "Full", buying: "Full", money: "None", manager: "View", messages: "Full", admin: "None" },
  { role: "Dispatcher", till: "None", quotes: "None", yard: "View", drive: "View", dispatch: "Full", buying: "None", money: "None", manager: "None", messages: "Full", admin: "None" },
  { role: "Driver", till: "None", quotes: "None", yard: "None", drive: "Full", dispatch: "None", buying: "None", money: "None", manager: "None", messages: "Full", admin: "None" },
  { role: "Inside Sales", till: "Full", quotes: "Full", yard: "None", drive: "None", dispatch: "None", buying: "View", money: "None", manager: "None", messages: "Full", admin: "None" },
];

function renderMatrixCell(level: "Full" | "View" | "None"): TemplateResult {
  const mod = level === "Full" ? "matrix-cell-full" : level === "View" ? "matrix-cell-view" : "matrix-cell-none";
  return html`<span class="matrix-pill ${mod}">${level}</span>`;
}

function renderRoleMatrix(): TemplateResult {
  return html`
    <section class="card" data-test="role-matrix-card">
      <div class="card-head">
        <h2 class="card-title">What each job shape sees</h2>
      </div>
      <div class="tablewrap">
        <table class="role-matrix-table cardrow-table">
          <thead>
            <tr>
              <th scope="col" class="role-col">Role</th>
              <th scope="col">Till</th>
              <th scope="col">Quotes</th>
              <th scope="col">Yard</th>
              <th scope="col">Drive</th>
              <th scope="col">Dispatch</th>
              <th scope="col">Buying</th>
              <th scope="col">Money</th>
              <th scope="col">Manager</th>
              <th scope="col">Messages</th>
              <th scope="col">Admin</th>
            </tr>
          </thead>
          <tbody>
            ${ROLE_MATRIX_ENTRIES.map(
              (r) => html`
                <tr>
                  <th scope="row" class="role-col"><strong>${r.role}</strong></th>
                  <td data-label="Till">${renderMatrixCell(r.till)}</td>
                  <td data-label="Quotes">${renderMatrixCell(r.quotes)}</td>
                  <td data-label="Yard">${renderMatrixCell(r.yard)}</td>
                  <td data-label="Drive">${renderMatrixCell(r.drive)}</td>
                  <td data-label="Dispatch">${renderMatrixCell(r.dispatch)}</td>
                  <td data-label="Buying">${renderMatrixCell(r.buying)}</td>
                  <td data-label="Money">${renderMatrixCell(r.money)}</td>
                  <td data-label="Manager">${renderMatrixCell(r.manager)}</td>
                  <td data-label="Messages">${renderMatrixCell(r.messages)}</td>
                  <td data-label="Admin">${renderMatrixCell(r.admin)}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
      <p class="card-sub matrix-legend">
        Full: sees the surface and can act. View: sees it, no actions. None: the surface is not shown to this role. Roles match the Staff register.
      </p>
    </section>
  `;
}

// -- the grid -----------------------------------------------------------------------------------------

function renderGrid(model: RoleConfigModel): TemplateResult {
  return html`
    <p class="card-sub" data-test="config-caption">
      Curation hides buttons and columns; it never grants power and never moves data.
      Reading another job shape's curation is the read half of the same admin act as writing it,
      so the whole grid is the administrator's to see.
    </p>
    <section class="card">
      <div class="tablewrap">
        <table class="cardrow-table config-grid-table" data-test="config-grid">
          <thead>
            <tr>
              <th scope="col">Job shape</th>
              ${CONFIG_SURFACES.map((s) => html`<th scope="col">${s.word}</th>`)}
            </tr>
          </thead>
          <tbody>
            ${JOB_SHAPES.map((shape) => html`
              <tr data-profile=${shape.profile}>
                <th scope="row">
                  <div class="person">${shape.word}</div>
                  <div class="dim">${shape.what}</div>
                </th>
                ${CONFIG_SURFACES.map((s) => renderCell(model, s.id, shape.profile, s.word))}
              </tr>
            `)}
          </tbody>
        </table>
      </div>
    </section>
  `;
}

function renderCell(model: RoleConfigModel, surface: string, profile: string, label: string): TemplateResult {
  const cell = model.cells.get(cellKey(surface, profile));
  if (cell === undefined) {
    return html`<td data-label=${label}><span class="dim">…</span></td>`;
  }
  return html`
    <td data-label=${label}>
      <button
        type="button"
        class="btn btn-secondary btn-small cell-btn"
        data-action="open-config-editor"
        data-surface=${surface}
        data-profile=${profile}
        data-test="config-cell-${surface}-${profile}"
        aria-label=${cellAriaWords(surface, profile, cell.config)}
      >
        ${cellWords(cell.config.role_items)}
      </button>
    </td>
  `;
}

function cellAriaWords(surface: string, profile: string, config: CellConfig): string {
  return `${shapeWord(profile)} on ${surfaceWord(surface)}: ${cellWords(config.role_items)}. Edit`;
}

// -- the job shape catalog -----------------------------------------------------------------------------

function renderCatalog(roles: readonly RoleRow[]): TemplateResult {
  return html`
    <section class="card">
      <div class="card-head">
        <h2 class="card-title">The job shapes and the roles that hold them</h2>
        <span class="count" data-test="config-roles-count">${roles.length} roles</span>
      </div>
      ${roles.length === 0
        ? html`<p class="notice-line">The roles catalog did not load.</p>`
        : html`
            <ul class="role-list" data-test="config-catalog">
              ${JOB_SHAPES.map((shape) => {
                const names = rolesForShape(shape.profile, roles);
                return html`
                  <li class="role-row" data-profile=${shape.profile}>
                    <span class="role-name">${shape.word}</span>
                    ${names.map((name) => html`<span class="chip">${name}</span>`)}
                    ${names.length === 0 ? html`<span class="dim">No role in the catalog sits here yet.</span>` : nothing}
                  </li>
                `;
              })}
            </ul>
          `}
      <p class="card-sub">Each staff member gets their own shape; a role the catalog does not name answers to the counter shape.</p>
    </section>
  `;
}

// -- the editor -----------------------------------------------------------------------------------------

function renderEditor(editor: ConfigEditor): TemplateResult {
  const heading = `What the ${shapeWord(editor.profile)} sees on ${surfaceWord(editor.surface)}`;
  return html`
    <section class="card" data-test="config-editor">
      <div class="card-head">
        <h2 class="card-title">${heading}</h2>
        <button type="button" class="btn btn-secondary btn-small" data-action="close-config-editor">Close</button>
      </div>
      ${editor.notice === "" ? nothing : html`<p class="notice-line" data-test="config-notice">${editor.notice}</p>`}
      ${editor.error === "" ? nothing : html`<p class="error-line" data-test="config-save-error">${editor.error}</p>`}
      ${renderDealerDraft(editor)}
      ${renderMyDraft(editor)}
      ${renderWorkspace(editor)}
    </section>
  `;
}

// -- the dealer's curation draft ------------------------------------------------------------------------

function renderDealerDraft(editor: ConfigEditor): TemplateResult {
  const setFault = editor.draft.length === 0 ? "" : itemSetError(editor.draft);
  const rowFault = editor.draft.map((it) => itemRowError(it)).find((f) => f !== "") ?? "";
  return html`
    <div class="card-head">
      <h3 class="section-title">The dealer's curation</h3>
      <span class="count">every surface shows its standard view until a row is set</span>
    </div>
    <p class="card-sub">
      Choose from the panels, columns, chips, and actions that belong to this surface.
    </p>
    ${editor.draft.length === 0
      ? html`<p class="notice-line" data-test="config-draft-empty">Nothing curated here yet.</p>`
      : html`
          <div class="tablewrap">
            <table class="cardrow-table" data-test="config-draft-rows">
              <thead>
                <tr>
                  <th scope="col">Kind</th>
                  <th scope="col">Item</th>
                  <th scope="col">Order</th>
                  <th scope="col">Shown</th>
                  <th scope="col"></th>
                </tr>
              </thead>
              <tbody>
                ${editor.draft.map((it, index) => draftRow(it, index, "config"))}
              </tbody>
            </table>
          </div>
        `}
    ${rowFault === "" ? nothing : html`<p class="error-line" data-test="config-row-fault">${rowFault}</p>`}
    ${setFault === "" ? nothing : html`<p class="error-line" data-test="config-set-fault">${setFault}</p>`}
    <div class="editor-actions">
      <label class="field">
        <span class="field-label">Kind</span>
        <select class="select" data-action="config-add-kind" data-field="config-add-kind" aria-label="Kind of the row to add">
          ${ITEM_KINDS.map((k) => html`<option>${k}</option>`)}
        </select>
      </label>
      <label class="field">
        <span class="field-label">Item</span>
        <select class="select" data-action="config-add-id" data-field="config-add-id" aria-label="Item to add">
          <option value="">Choose an item</option>
          ${surfaceItemsFor(editor.surface, editor.addKind).map(
            (item) => html`<option data-option-id=${item.id}>${item.label}</option>`,
          )}
        </select>
      </label>
      <button type="button" class="btn btn-secondary" data-action="config-add-row">Add row</button>
      <button
        type="button"
        class="btn btn-primary"
        data-action="config-save"
        ?disabled=${editor.busy || !canSaveDraft(editor)}
      >
        ${editor.busy ? "Saving…" : "Save the curation"}
      </button>
    </div>
    <div class="editor-actions">
      <span class="field-label">Reset one kind to the level beneath the dealer:</span>
      ${ITEM_KINDS.map(
        (k) => html`
          <button type="button" class="btn btn-secondary btn-small" data-action="config-reset-kind" data-kind=${k} ?disabled=${editor.busy}>
            Reset ${k}s
          </button>
        `,
      )}
    </div>
  `;
}

/** One draft row of either draft: the action names carry which draft owns it. */
function draftRow(it: ConfigItem, index: number, scope: "config" | "my"): TemplateResult {
  return html`
    <tr data-index=${index}>
      <td data-label="Kind"><span class="chip">${it.kind}</span></td>
      <td data-label="Item"><code class="mono">${it.id}</code></td>
      <td data-label="Order">${it.position}</td>
      <td data-label="Shown">
        <button
          type="button"
          class="btn btn-secondary btn-small"
          data-action="${scope}-toggle-visible"
          data-index=${index}
        >
          ${it.visible ? "Shown" : "Hidden"}
        </button>
      </td>
      <td class="cardrow-act">
        <span class="kcontrols">
          <button type="button" class="btn btn-secondary btn-small" data-action="${scope}-row-up" data-index=${index} aria-label="Move earlier">Up</button>
          <button type="button" class="btn btn-secondary btn-small" data-action="${scope}-row-down" data-index=${index} aria-label="Move later">Down</button>
          <button type="button" class="btn btn-secondary btn-small" data-action="${scope}-remove-row" data-index=${index} aria-label="Remove row">Remove</button>
        </span>
      </td>
    </tr>
  `;
}

function canSaveDraft(editor: ConfigEditor): boolean {
  if (editor.draft.length === 0) return false;
  return itemSetError(editor.draft) === "" && editor.draft.every((it) => itemRowError(it) === "");
}

// -- the caller's own override ---------------------------------------------------------------------------

function renderMyDraft(editor: ConfigEditor): TemplateResult {
  const setFault = editor.myDraft.length === 0 ? "" : itemSetError(editor.myDraft);
  return html`
    <div class="card-head">
      <h3 class="section-title">Your own view of this surface</h3>
      <span class="count">whole or absent: your set replaces the dealer's, for you</span>
    </div>
    ${editor.myDraft.length === 0
      ? html`<p class="notice-line" data-test="config-my-empty">You follow the dealer's curation.</p>`
      : html`
          <div class="tablewrap">
            <table class="cardrow-table" data-test="config-my-rows">
              <thead>
                <tr>
                  <th scope="col">Kind</th>
                  <th scope="col">Item</th>
                  <th scope="col">Order</th>
                  <th scope="col">Shown</th>
                  <th scope="col"></th>
                </tr>
              </thead>
              <tbody>
                ${editor.myDraft.map((it, index) => draftRow(it, index, "my"))}
              </tbody>
            </table>
          </div>
        `}
    ${setFault === "" ? nothing : html`<p class="error-line" data-test="config-my-set-fault">${setFault}</p>`}
    <div class="editor-actions">
      <label class="field">
        <span class="field-label">Kind</span>
        <select class="select" data-action="config-my-add-kind" data-field="config-my-add-kind" aria-label="Kind of your own row to add">
          ${ITEM_KINDS.map((k) => html`<option>${k}</option>`)}
        </select>
      </label>
      <label class="field">
        <span class="field-label">Item</span>
        <select class="select" data-action="config-my-add-id" data-field="config-my-add-id" aria-label="Item of your own row to add">
          <option value="">Choose an item</option>
          ${surfaceItemsFor(editor.surface, editor.myAddKind).map(
            (item) => html`<option data-option-id=${item.id}>${item.label}</option>`,
          )}
        </select>
      </label>
      <button type="button" class="btn btn-secondary" data-action="config-my-add-row">Add row</button>
      <button type="button" class="btn btn-primary" data-action="config-save-my" ?disabled=${editor.busy || editor.myDraft.length === 0}>
        ${editor.busy ? "Saving…" : "Save my view"}
      </button>
      <button type="button" class="btn btn-secondary" data-action="config-reset-my" ?disabled=${editor.busy}>
        Reset my view
      </button>
    </div>
  `;
}

// -- the caller's own workspace row ------------------------------------------------------------------------

function renderWorkspace(editor: ConfigEditor): TemplateResult {
  const ws = editor.wsDraft;
  return html`
    <div class="card-head">
      <h3 class="section-title">Your workspace here</h3>
      <span class="count">${ws.source === "user" ? "your own row" : "the dealer's defaults"}</span>
    </div>
    <div class="editor-actions">
      <label class="field">
        <span class="field-label">Split (20 to 80)</span>
        <input class="text-field" type="number" min="20" max="80" data-field="configws-split" aria-label="Workspace split" />
      </label>
      <label class="field">
        <span class="field-label">Arrangement</span>
        <select class="select" data-action="configws-arrangement" data-field="configws-arrangement" aria-label="Workspace arrangement">
          <option value="PRIMARY_LEFT">Primary panel left</option>
          <option value="PRIMARY_RIGHT">Primary panel right</option>
        </select>
      </label>
      <label class="branch-choice">
        <input type="checkbox" data-action="configws-flag" data-field="primary_collapsed" />
        Primary panel starts collapsed
      </label>
      <label class="branch-choice">
        <input type="checkbox" data-action="configws-flag" data-field="secondary_collapsed" />
        Secondary panel starts collapsed
      </label>
      <label class="branch-choice">
        <input type="checkbox" data-action="configws-flag" data-field="cost_hidden" />
        Hide cost from me
      </label>
    </div>
    <div class="editor-actions">
      <button type="button" class="btn btn-primary" data-action="configws-save" ?disabled=${editor.busy}>
        ${editor.busy ? "Saving…" : "Save my workspace"}
      </button>
      <button type="button" class="btn btn-secondary" data-action="configws-reset" ?disabled=${editor.busy}>
        Reset my workspace
      </button>
    </div>
  `;
}
