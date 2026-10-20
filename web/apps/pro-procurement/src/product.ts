// The Product surface: one product of the dealer's catalog, and the one move
// it exists for: put it on a plan. The core serves no price here (pricing is
// the dealer's, at the RFQ), so the page says what the thing is, how it is
// sold, and whether the yard has it, never a price. The add names a quantity
// and a unit and lands on a plan the contractor chose (or one created on the
// spot), and the person is taken to that plan: a line that lands somewhere
// unseen is how a cart behaves, not a plan. Visual reference: the ref's
// ProductPage and its AddToPlanSheet, minus everything the core does not
// serve (no photos, no price breaks, no alternates).
import { html, nothing, type TemplateResult } from "lit";
import { tenantCatalog, tenantProcurement, type TenantCatalogProduct, type TenantProject } from "@hardscapehouse/api-client";
import { backHeader, button, cancelButton, metaLine, modalSheet, actionFailure, type Page, type SurfaceAnswer } from "./views.js";

export interface ProductData {
  product: TenantCatalogProduct;
  plans: TenantProject[];
}

export async function productLoad(page: Page): Promise<SurfaceAnswer> {
  if (!page.session.can("submit_rfq")) return { kind: "refusal" };
  const id = page.route.productId ?? "";
  const [product, projects] = await Promise.all([
    tenantCatalog.tenantGetCatalogProduct(page.client, id),
    tenantProcurement.tenantListProcurementProjects(page.client),
  ]);
  if (!product.ok) {
    if (product.status === 404) return { kind: "empty" };
    if (product.status === 403) return { kind: "refusal" };
    return { kind: "error", status: product.status };
  }
  if (!projects.ok) {
    if (projects.status === 403) return { kind: "refusal" };
    return { kind: "error", status: projects.status };
  }
  const data: ProductData = { product: product.value, plans: projects.value.projects.filter((p) => p.status === "PLANNING") };
  rememberProduct(page, data);
  return { kind: "ready", data, heading: product.value.name };
}

export function productHeading(dataRaw: unknown): string {
  const data = dataRaw as ProductData | null;
  return data === null || data === undefined ? "Product" : data.product.name;
}

function fact(label: string, value: string): TemplateResult {
  return html`<div class="rowline"><span class="dim">${label}</span><span class="grow"></span><span>${value}</span></div>`;
}

export function productView(dataRaw: unknown, page: Page): TemplateResult {
  void page;
  const data = dataRaw as ProductData;
  const p = data.product;
  const availability =
    p.availability.length > 0 && p.availability.every((a) => a.in_stock)
      ? "In stock at the yard"
      : typeof p.lead_time_days === "number" && p.lead_time_days > 0
        ? `About ${p.lead_time_days} days from the yard`
        : "Ask the yard";
  return html`${backHeader("Catalog", "open-catalog")}
    ${metaLine([html`<span class="mono">${p.sku}</span>`, html`<span>${p.manufacturer}</span>`])}
    <div class="card">
      <p class="words">${p.description}</p>
      <div class="facts">
        ${fact("Sold by", p.sold_by === "" ? p.base_uom : p.sold_by)}
        ${p.application === "" ? nothing : fact("Category", p.application)}
        ${fact("Minimum order", p.minimum_order_qty === undefined ? "1" : p.minimum_order_qty)}
        ${fact("Availability", availability)}
      </div>
    </div>
    <div class="actions">${button({ label: "Add to a plan", action: "add-to-plan-open" })}</div>`;
}

// ---- the add to a plan modal ---------------------------------------------------------------------

interface AddState {
  product: TenantCatalogProduct;
  plans: TenantProject[];
  chosen: string | null;
  busy: boolean;
  error: string;
  createdPlan: { id: string; name: string } | null;
}

const addStates = new WeakMap<Page, AddState>();
const createdPlans = new WeakMap<Page, { id: string; name: string }>();

function addModal(state: AddState): TemplateResult {
  const needsName = state.chosen === "new";
  return modalSheet(
    "Add to a plan",
    html`<label class="field">How much
        <input class="control" data-control="text" type="text" inputmode="decimal" data-field="plan-qty" data-testid="plan-qty" />
      </label>
      <label class="field">Unit
        <input class="control" data-control="text" type="text" data-field="plan-uom" data-testid="plan-uom" />
      </label>
      <p class="words">Put it on</p>
      <ul class="picker-results">
        ${state.plans.map(
          (plan) => html`<li>
            <button type="button" class="result ${state.chosen === plan.id ? "chosen" : ""}" data-action="choose-plan" data-plan="${plan.id}" data-testid="choose-plan">
              <span class="name">${plan.name}</span>
            </button>
          </li>`,
        )}
        <li>
          <button type="button" class="result ${state.chosen === "new" ? "chosen" : ""}" data-action="choose-plan" data-plan="new" data-testid="choose-new-plan">
            <span class="name">A new plan</span>
          </button>
        </li>
      </ul>
      ${needsName
        ? html`<label class="field">The new plan's name
            <input class="control" data-control="text" type="text" data-field="new-plan-name" data-testid="new-plan-name" />
          </label>`
        : nothing}
      ${state.error === "" ? nothing : html`<p class="error" role="alert" data-testid="add-plan-error">${state.error}</p>`}`,
    html`${cancelButton}${button({ label: state.busy ? "Adding" : "Add", action: "add-to-plan", disabled: state.busy })}`,
    "add-plan-modal",
  );
}

export function productCommand(action: string, control: Element, page: Page): boolean {
  const held = addStates.get(page);
  if (action === "add-to-plan-open") {
    const data = lastProduct.get(page);
    if (data === undefined) return true;
    createdPlans.delete(page);
    addStates.set(page, { product: data.product, plans: data.plans, chosen: null, busy: false, error: "", createdPlan: null });
    const state = addStates.get(page) as AddState;
    page.openModal("add-plan-modal", () => addModal(state), { "plan-qty": "1", "plan-uom": data.product.base_uom });
    return true;
  }
  if (held === undefined) return false;
  if (action === "choose-plan") {
    held.chosen = control.getAttribute("data-plan");
    held.error = "";
    page.touch();
    return true;
  }
  if (action === "add-to-plan") {
    void addToChosenPlan(page);
    return true;
  }
  return false;
}

const lastProduct = new WeakMap<Page, ProductData>();

// The loader keeps its answer where the commands can reach it (the page's own
// data is the root's): one WeakMap entry per page, replaced on every load.
export function rememberProduct(page: Page, data: ProductData): void {
  lastProduct.set(page, data);
}

export async function productAddToPlan(
  page: Page,
  target: { planId: string } | { newPlanName: string },
  qty: string,
  uom: string,
): Promise<{ ok: boolean; error?: string; status?: number; projectId?: string; projectName?: string }> {
  const id = page.route.productId ?? "";
  const state = addStates.get(page);
  let planId = "planId" in target ? target.planId : "";
  let projectName = "newPlanName" in target ? target.newPlanName : "";
  if ("newPlanName" in target) {
    const prior = state?.createdPlan ?? createdPlans.get(page) ?? null;
    if (prior !== null && prior.name === target.newPlanName) {
      planId = prior.id;
    } else {
      const created = await tenantProcurement.tenantCreateProcurementProject(page.client, { name: target.newPlanName });
      if (!created.ok) return { ok: false, error: created.error, status: created.status };
      planId = created.value.id;
      const saved = { id: planId, name: target.newPlanName };
      if (state !== undefined) state.createdPlan = saved;
      createdPlans.set(page, saved);
    }
  } else {
    const projects = await tenantProcurement.tenantListProcurementProjects(page.client);
    const plan = projects.ok ? projects.value.projects.find((p) => p.id === target.planId) : undefined;
    projectName = plan?.name ?? "the plan";
  }
  const line = await tenantProcurement.tenantAddProcurementLine(page.client, planId, { product_id: id, qty_entered: qty, uom });
  if (!line.ok) return { ok: false, error: line.error, status: line.status, projectId: planId, projectName };
  if (state !== undefined) state.createdPlan = null;
  createdPlans.delete(page);
  return { ok: true, projectId: planId, projectName };
}

async function addToChosenPlan(page: Page): Promise<void> {
  const state = addStates.get(page);
  if (state === undefined || state.busy) return;
  if (state.chosen === null) {
    state.error = "Choose a plan first.";
    page.touch();
    return;
  }
  const qty = (page.field("plan-qty")?.value ?? "").trim();
  const uom = (page.field("plan-uom")?.value ?? "").trim();
  if (qty === "" || uom === "") {
    state.error = "Say how much, and in what unit.";
    page.touch();
    return;
  }
  if (state.chosen === "new") {
    const name = (page.field("new-plan-name")?.value ?? "").trim();
    if (name === "") {
      state.error = "Name the new plan first.";
      page.touch();
      return;
    }
  }
  state.busy = true;
  page.touch();
  const answer = await productAddToPlan(
    page,
    state.chosen === "new" ? { newPlanName: (page.field("new-plan-name")?.value ?? "").trim() } : { planId: state.chosen },
    qty,
    uom,
  );
  state.busy = false;
  if (!answer.ok) {
    state.error = await actionFailure(page, answer);
    page.touch();
    return;
  }
  page.closeModal();
  // Navigate first, then say it: a navigation clears the notice, and the words
  // must survive the move to the plan they describe.
  page.goto({ surface: "scope", projectId: answer.projectId ?? "", from: "catalog" });
  page.say("ok", `Added ${qty} ${uom} to ${answer.projectName}.`);
}
