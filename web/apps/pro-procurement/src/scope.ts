// The Order scope surface: one procurement project, the plan a contractor
// builds before the dealer prices it. The lines sit at the account price the
// core returns: indicative prices after the RFQ, the kit's placeholder before
// one (never a confident zero). Editing lives only in PLANNING; a quoting
// project is read only, with Recall to planning as its way back, the same
// editor shape the original portal carried (portal-procurement-project.ts):
// the catalog picker (portal's search + add), the line editor that sends only
// what changed, Submit RFQ, rename and delete. The server's refusals surface
// in its own words. Visual reference: the HH: Pro board's plan card and the
// original editor's lines table, on the design system's cards.
import { html, nothing, type TemplateResult } from "lit";
import { tenantCatalog, tenantProcurement, type TenantCatalogResult, type TenantProjectDetail, type TenantProjectLine } from "@hardscapehouse/api-client";
import { statusChip } from "@hardscapehouse/pro-kit";
import { priceWords, qtyWords } from "./figures.js";
import { backHeader, button, cancelButton, confirmModal, metaLine, modalSheet, actionFailure, type Page, type SurfaceAnswer } from "./views.js";

export const SCOPE_MISSING = "That plan is not here anymore. It may have been deleted.";
const NO_LINES_WORDS = "No lines yet. Add materials from the catalog.";

export async function scopeLoad(page: Page): Promise<SurfaceAnswer> {
  if (!page.session.can("submit_rfq")) return { kind: "refusal" };
  const answer = await tenantProcurement.tenantGetProcurementProject(page.client, page.route.projectId ?? "");
  if (answer.ok) {
    ui(page).detail = answer.value;
    return { kind: "ready", data: answer.value, heading: answer.value.name };
  }
  if (answer.status === 404) return { kind: "empty" };
  if (answer.status === 403) return { kind: "refusal" };
  return { kind: "error", status: answer.status };
}

export function scopeHeading(dataRaw: unknown): string {
  const data = dataRaw as TenantProjectDetail | null;
  return data === null || data === undefined ? "Plan" : data.name;
}

// ---- the surface's own state, per open picker, editor and confirm -------------------------------

interface ScopeUi {
  detail: TenantProjectDetail | null;
  picker: { seq: number; term: string; results: TenantCatalogResult[]; searching: boolean; busy: boolean; error: string } | null;
  edit: { lineId: string; qty: string; uom: string; notes: string; busy: boolean; error: string } | null;
  rename: { busy: boolean; error: string } | null;
  deleting: { busy: boolean } | null;
}

const uiStates = new WeakMap<Page, ScopeUi>();

function ui(page: Page): ScopeUi {
  let held = uiStates.get(page);
  if (held === undefined) {
    held = { detail: null, picker: null, edit: null, rename: null, deleting: null };
    uiStates.set(page, held);
  }
  return held;
}

// ---- the view ------------------------------------------------------------------------------------

function lineRow(line: TenantProjectLine, editable: boolean): TemplateResult {
  return html`<li>
    <div class="card" data-testid="scope-line">
      <span class="rowline">
        <span class="name">${line.description}</span>
        ${line.priced ? nothing : html`<span class="pchip" data-status="UNPRICED">Not priced</span>`}
        <span class="grow"></span>
        <span class="mono figure">${line.priced ? priceWords(line.indicative_line_total_cents) : priceWords(null)}</span>
      </span>
      <span class="rowline dim">
        <span class="mono">${line.sku}</span>
        <span>${qtyWords(line.qty_entered, line.uom)}</span>
        <span class="grow"></span>
        <span class="mono">${line.priced ? priceWords(line.indicative_unit_price_cents) : priceWords(null)}</span>
      </span>
      ${editable
        ? html`<span class="rowline line-actions">
            <button type="button" class="btn btn-secondary" data-action="edit-line" data-line="${line.id}" data-testid="line-edit">Edit</button>
            <button type="button" class="btn btn-secondary" data-action="remove-line" data-line="${line.id}" data-testid="line-remove">Remove</button>
          </span>`
        : nothing}
    </div>
  </li>`;
}

export function scopeView(dataRaw: unknown, page: Page): TemplateResult {
  void page;
  const data = dataRaw as TenantProjectDetail;
  const editable = data.status === "PLANNING";
  const fromProjects = page.route.from === "projects";
  return html`${backHeader(fromProjects ? "Projects" : "Board", fromProjects ? "open-projects" : "open-board")}
    ${metaLine([
      statusChip("project", data.status),
      html`<span>Estimated subtotal <span class="mono figure">${priceWords(data.subtotal_cents)}</span></span>`,
      html`<span>${data.line_count} lines</span>`,
    ])}
    ${editable
      ? html`<div class="actions">
          <button type="button" class="btn btn-primary" data-action="add-material" data-testid="add-material">Add material</button>
          <button type="button" class="btn btn-secondary" data-action="rename-plan" data-testid="rename-plan">Rename</button>
          <button type="button" class="btn btn-secondary" data-action="delete-plan" data-testid="delete-plan">Delete plan</button>
        </div>`
      : data.status === "QUOTING"
        ? html`<div class="actions"><button type="button" class="btn btn-secondary" data-action="recall" data-testid="recall">Recall to planning</button></div>`
        : nothing}
    ${data.lines.length === 0 ? html`<p class="emptyline" data-testid="scope-empty">${NO_LINES_WORDS}</p>` : nothing}
    <ul class="cards" data-testid="scope-lines">${data.lines.map((line) => lineRow(line, editable))}</ul>
    ${editable
      ? html`<div class="actions">
          <button type="button" class="btn btn-primary" data-action="submit-rfq" data-testid="submit-rfq" ?disabled=${data.lines.length === 0}>Request a quote</button>
        </div>`
      : nothing}`;
}

// ---- the catalog picker ---------------------------------------------------------------------------

function pickerModal(picker: NonNullable<ScopeUi["picker"]>): TemplateResult {
  const results =
    picker.searching === true
      ? html`<p class="emptyline" role="status">Searching</p>`
      : picker.term.trim() === ""
        ? html`<p class="emptyline">Type to search the catalog.</p>`
        : picker.results.length === 0
          ? html`<p class="emptyline">No matches.</p>`
          : html`<ul class="picker-results">
              ${picker.results.map(
                (item) => html`<li>
                  <button type="button" class="result" data-action="pick-product" data-product="${item.product_id}" ?disabled=${picker.busy} data-testid="picker-result">
                    <span class="name">${item.name}</span>
                    <span class="dim mono">${item.sku}</span>
                    <span class="dim">${item.base_uom}</span>
                  </button>
                </li>`,
              )}
            </ul>`;
  return modalSheet(
    "Add material",
    html`<label class="field">Search the catalog
        <input class="control" data-control="search" type="search" data-field="picker-search" data-testid="picker-search" aria-label="Search the catalog" />
      </label>
      ${picker.error === "" ? nothing : html`<p class="error" role="alert" data-testid="picker-error">${picker.error}</p>`}
      ${results}`,
    html`${cancelButton}`,
    "picker-modal",
  );
}

export function scopeInput(name: string, value: string, page: Page): void {
  if (name !== "picker-search") return;
  void runPickerSearch(page, value);
}

async function runPickerSearch(page: Page, term: string): Promise<void> {
  const picker = ui(page).picker;
  if (picker === null) return;
  const seq = (picker.seq += 1);
  picker.term = term;
  if (term.trim() === "") {
    picker.results = [];
    picker.searching = false;
    page.touch();
    return;
  }
  picker.searching = true;
  page.touch();
  const answer = await tenantCatalog.tenantSearchCatalog(page.client, { q: term.trim(), limit: 20 });
  if (seq !== picker.seq) return;
  picker.searching = false;
  picker.results = answer.ok ? answer.value.items : [];
  page.touch();
}

export async function scopeAddLine(page: Page, productId: string, qty: string, uom: string): Promise<{ ok: boolean; error?: string; status?: number }> {
  const answer = await tenantProcurement.tenantAddProcurementLine(page.client, page.route.projectId ?? "", { product_id: productId, qty_entered: qty, uom });
  return answer.ok ? { ok: true } : { ok: false, error: answer.error, status: answer.status };
}

// ---- the line editor ---------------------------------------------------------------------------------

function editModal(edit: NonNullable<ScopeUi["edit"]>, line: TenantProjectLine | null): TemplateResult {
  return modalSheet(
    "Edit line",
    html`${line === null ? nothing : html`<p class="words">${line.description}</p>`}
      <label class="field">Quantity
        <input class="control" data-control="text" type="text" inputmode="decimal" data-field="line-qty" data-testid="line-qty" />
      </label>
      <label class="field">Unit
        <input class="control" data-control="text" type="text" data-field="line-uom" data-testid="line-uom" />
      </label>
      <label class="field">Notes
        <textarea class="control" data-field="line-notes" data-testid="line-notes" rows="2"></textarea>
      </label>
      ${edit.error === "" ? nothing : html`<p class="error" role="alert" data-testid="line-error">${edit.error}</p>`}`,
    html`${cancelButton}${button({ label: edit.busy ? "Saving" : "Save", action: "save-line", disabled: edit.busy })}`,
    "line-modal",
  );
}

// ---- the rename and delete confirms -------------------------------------------------------------------

function renameModal(rename: NonNullable<ScopeUi["rename"]>): TemplateResult {
  return confirmModal({
    heading: "Rename this plan",
    body: html`<label class="field">Project name
        <input class="control" data-control="text" type="text" data-field="rename-name" data-testid="rename-name" />
      </label>
      ${rename.error === "" ? nothing : html`<p class="error" role="alert">${rename.error}</p>`}`,
    confirmLabel: "Save",
    confirmAction: "save-rename",
    busy: rename.busy,
    testid: "rename-modal",
  });
}

function deleteModal(deleting: NonNullable<ScopeUi["deleting"]>): TemplateResult {
  return confirmModal({
    heading: "Delete this plan?",
    body: html`<p class="words">The plan and its lines go with it. This cannot be undone.</p>`,
    confirmLabel: "Delete",
    confirmAction: "confirm-delete",
    busy: deleting.busy,
    testid: "delete-modal",
  });
}

// ---- the commands ----------------------------------------------------------------------------------------

export function scopeCommand(action: string, control: Element, page: Page): boolean {
  const held = ui(page);
  if (action === "add-material") {
    held.picker = { seq: 0, term: "", results: [], searching: false, busy: false, error: "" };
    page.openModal("picker-modal", () => pickerModal(held.picker as NonNullable<ScopeUi["picker"]>));
    return true;
  }
  if (action === "pick-product") {
    void pickProduct(page, control.getAttribute("data-product") ?? "");
    return true;
  }
  if (action === "edit-line") {
    openEdit(page, control.getAttribute("data-line") ?? "");
    return true;
  }
  if (action === "save-line") {
    void saveLine(page);
    return true;
  }
  if (action === "remove-line") {
    void removeLine(page, control.getAttribute("data-line") ?? "");
    return true;
  }
  if (action === "submit-rfq") {
    void scopeSubmitRfq(page);
    return true;
  }
  if (action === "recall") {
    void recall(page);
    return true;
  }
  if (action === "rename-plan") {
    held.rename = { busy: false, error: "" };
    page.openModal("rename-modal", () => renameModal(held.rename as NonNullable<ScopeUi["rename"]>));
    return true;
  }
  if (action === "save-rename") {
    void saveRename(page);
    return true;
  }
  if (action === "delete-plan") {
    held.deleting = { busy: false };
    page.openModal("delete-modal", () => deleteModal(held.deleting as NonNullable<ScopeUi["deleting"]>));
    return true;
  }
  if (action === "confirm-delete") {
    void deletePlan(page);
    return true;
  }
  return false;
}

async function pickProduct(page: Page, productId: string): Promise<void> {
  const picker = ui(page).picker;
  if (picker === null || picker.busy) return;
  const item = picker.results.find((candidate) => candidate.product_id === productId);
  if (item === undefined) return;
  picker.busy = true;
  page.touch();
  const answer = await scopeAddLine(page, productId, "1", item.base_uom);
  picker.busy = false;
  if (answer.ok) {
    picker.error = "";
    page.refresh();
  } else {
    picker.error = await actionFailure(page, answer);
  }
  page.touch();
}

function openEdit(page: Page, lineId: string): void {
  const detail = ui(page).detail;
  const line = detail?.lines.find((candidate) => candidate.id === lineId);
  if (line === undefined) return;
  ui(page).edit = { lineId: line.id, qty: line.qty_entered, uom: line.uom, notes: line.notes, busy: false, error: "" };
  page.openModal(
    "line-modal",
    () => editModal(ui(page).edit as NonNullable<ScopeUi["edit"]>, line),
    { "line-qty": line.qty_entered, "line-uom": line.uom, "line-notes": line.notes },
  );
}

async function saveLine(page: Page): Promise<void> {
  const edit = ui(page).edit;
  const detail = ui(page).detail;
  if (edit === null || detail === null || edit.busy) return;
  const line = detail.lines.find((candidate) => candidate.id === edit.lineId);
  if (line === undefined) {
    page.closeModal();
    return;
  }
  // Send only what changed: the server treats a qty or uom field as a change and
  // re-prices (an edit of the notes alone must not wipe the indicative price),
  // and a body with nothing in it is refused outright.
  const body: { qty_entered?: string; uom?: string; notes?: string } = {};
  const qty = (page.field("line-qty")?.value ?? "").trim();
  const uom = (page.field("line-uom")?.value ?? "").trim();
  const notes = page.field("line-notes")?.value ?? "";
  if (qty !== line.qty_entered) body.qty_entered = qty;
  if (uom !== line.uom) body.uom = uom;
  if (notes !== line.notes) body.notes = notes;
  if (Object.keys(body).length === 0) {
    page.closeModal();
    return;
  }
  edit.busy = true;
  page.touch();
  const answer = await tenantProcurement.tenantUpdateProcurementLine(page.client, detail.id, edit.lineId, body);
  edit.busy = false;
  if (answer.ok) {
    page.closeModal();
    page.say("ok", "Line updated.");
    page.refresh();
  } else {
    edit.error = await actionFailure(page, answer);
    page.touch();
  }
}

async function removeLine(page: Page, lineId: string): Promise<void> {
  const detail = ui(page).detail;
  if (detail === null) return;
  const answer = await tenantProcurement.tenantRemoveProcurementLine(page.client, detail.id, lineId);
  if (answer.ok) {
    page.say("ok", "Line removed.");
    page.refresh();
  } else {
    page.say("alert", await actionFailure(page, answer));
  }
}

export async function scopeSubmitRfq(page: Page): Promise<void> {
  const answer = await tenantProcurement.tenantSubmitProcurementRFQ(page.client, page.route.projectId ?? "");
  if (answer.ok) {
    const anyUnpriced = answer.value.lines.some((line) => !line.priced);
    page.say("ok", anyUnpriced ? "Quote requested. Lines without a price wait for the dealer." : "Quote requested. Every line is priced as an estimate.");
    page.refresh();
  } else {
    page.say("alert", await actionFailure(page, answer));
  }
}

async function recall(page: Page): Promise<void> {
  const detail = ui(page).detail;
  if (detail === null) return;
  const answer = await tenantProcurement.tenantUpdateProcurementProject(page.client, detail.id, { status: "PLANNING" });
  if (answer.ok) {
    page.say("ok", "Recalled to planning.");
    page.refresh();
  } else {
    page.say("alert", await actionFailure(page, answer));
  }
}

async function saveRename(page: Page): Promise<void> {
  const rename = ui(page).rename;
  const detail = ui(page).detail;
  if (rename === null || detail === null || rename.busy) return;
  const name = (page.field("rename-name")?.value ?? "").trim();
  if (name === "") {
    rename.error = "Give the plan a name first.";
    page.touch();
    return;
  }
  rename.busy = true;
  page.touch();
  const answer = await tenantProcurement.tenantUpdateProcurementProject(page.client, detail.id, { name });
  rename.busy = false;
  if (answer.ok) {
    page.closeModal();
    page.say("ok", "Plan renamed.");
    page.refresh();
  } else {
    rename.error = await actionFailure(page, answer);
    page.touch();
  }
}

async function deletePlan(page: Page): Promise<void> {
  const deleting = ui(page).deleting;
  const detail = ui(page).detail;
  if (deleting === null || detail === null || deleting.busy) return;
  deleting.busy = true;
  page.touch();
  const answer = await tenantProcurement.tenantDeleteProcurementProject(page.client, detail.id);
  deleting.busy = false;
  if (answer.ok) {
    page.closeModal();
    page.goto({ surface: "board", from: page.route.from });
  } else {
    page.say("alert", await actionFailure(page, answer));
    page.closeModal();
  }
}
