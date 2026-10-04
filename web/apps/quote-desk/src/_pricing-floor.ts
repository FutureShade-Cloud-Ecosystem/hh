// _pricing-floor.ts: the pricing editors' floor warning (CP0-6 answer A), the pure half
// (DOM-free, unit tested on the Node runner). The editors warn and may still save: before
// a save the server names the existing products the new price would put under their
// margin floor, the editor confirms, the save runs, and the confirmation is logged in the
// pricing log against the record the save wrote. The grading is the server's (the
// resolver and its margin gate); nothing here computes a margin.
import type {
  PricingCategoryPolicyWrite,
  PricingFloorConfirmation,
  PricingFloorPreviewRequest,
  PricingOverrideWrite,
  PricingQuantityBreakWrite,
  PricingRuleWrite,
} from "@hardscapehouse/api-client";
import { formatMoney, formatPct } from "./_pricing-shared.js";
import type { FloorItem, FloorPreview } from "./_pricing-wire.js";

/** One proposed save, as the warning sees it. id is "" on a create. */
export type FloorChange =
  | { kind: "rule"; id: string; write: PricingRuleWrite }
  | { kind: "override"; id: string; customerId: string; write: PricingOverrideWrite }
  | { kind: "category_policy"; categoryId: string; write: PricingCategoryPolicyWrite }
  | { kind: "quantity_break"; id: string; write: PricingQuantityBreakWrite };

/** The request that asks the server what the change would put under the floor. */
export function previewBody(change: FloorChange): PricingFloorPreviewRequest {
  switch (change.kind) {
    case "rule":
      return {
        kind: "rule",
        ...(change.id !== "" ? { id: change.id } : {}),
        ...(change.write.branch_id !== "" ? { branch_id: change.write.branch_id } : {}),
        rule: change.write,
      };
    case "override":
      return { kind: "override", ...(change.id !== "" ? { id: change.id } : {}), customer_id: change.customerId, override: change.write };
    case "category_policy":
      return { kind: "category_policy", category_id: change.categoryId, policy: change.write };
    case "quantity_break":
      return { kind: "quantity_break", ...(change.id !== "" ? { id: change.id } : {}), quantity_break: change.write };
  }
}

/** The products the new price makes worse: newly under the floor, or under it and cheaper still. */
export function affectedItems(p: FloorPreview): FloorItem[] {
  return p.items.filter((i) => !i.alreadyUnderFloor || i.finalCents < i.previousFinalCents);
}

/** Products that were already under the floor and are no worse for the change. */
export function alreadyUnderCount(p: FloorPreview): number {
  return p.items.length - affectedItems(p).length;
}

/** Whether the editor is asked to confirm before the save runs. */
export function needsConfirm(p: FloorPreview): boolean {
  return affectedItems(p).length > 0;
}

export function warningHeadline(p: FloorPreview): string {
  const n = affectedItems(p).length;
  return n === 1 ? "This price puts 1 product under its margin floor." : `This price puts ${n} products under their margin floor.`;
}

const TYPE_WORDS: Readonly<Record<string, string>> = {
  CONTRACTOR: "Contractor",
  RETAIL: "Retail",
  BUILDER: "Builder",
  GOVERNMENT: "Government",
  INTERNAL: "Internal",
};

/** One product, with the figures the server graded. */
export function itemLine(i: FloorItem): string {
  const who = i.customerType === "" ? "" : ` for ${TYPE_WORDS[i.customerType] ?? i.customerType}`;
  return (
    `${i.sku}, ${i.name}: margin ${formatPct(i.marginBps)} against a floor of ${formatPct(i.floorBps)}${who}, ` +
    `price ${formatMoney(i.finalCents)} per ${i.priceUom} (was ${formatMoney(i.previousFinalCents)})`
  );
}

/** What the grading did not cover, said plainly. */
export function warningNotes(p: FloorPreview): string[] {
  const notes: string[] = [];
  if (p.truncated) notes.push(`Only the first ${p.evaluated} products were checked; more are reached by this price.`);
  if (p.notGraded > 0) {
    notes.push(
      p.notGraded === 1
        ? "1 product could not be checked because it has no cost on file."
        : `${p.notGraded} products could not be checked because they have no cost on file.`,
    );
  }
  const already = alreadyUnderCount(p);
  if (already > 0) {
    notes.push(already === 1 ? "1 more product was already under its floor and is no worse." : `${already} more products were already under their floor and are no worse.`);
  }
  return notes;
}

type Ref = { type: "product" | "customer" | "category"; id: string };

function refOf(change: FloorChange): Ref | null {
  switch (change.kind) {
    case "rule": {
      const w = change.write;
      if (w.scope === "CUSTOMER_PRODUCT" && w.product_id !== "") return { type: "product", id: w.product_id };
      if (w.category_id !== "") return { type: "category", id: w.category_id };
      return null;
    }
    case "override":
      return { type: "customer", id: change.customerId };
    case "category_policy":
      return { type: "category", id: change.categoryId };
    case "quantity_break": {
      const w = change.write;
      if (w.product_id !== "") return { type: "product", id: w.product_id };
      if (w.category_id !== "") return { type: "category", id: w.category_id };
      return null;
    }
  }
}

const ENTITY_OF: Record<FloorChange["kind"], PricingFloorConfirmation["entity_type"]> = {
  rule: "pricing_rule",
  override: "customer_override",
  category_policy: "category_policy",
  quantity_break: "quantity_break",
};

/** The body that logs the editor's confirmation, against the record the save wrote. */
export function confirmationBody(change: FloorChange, savedId: string, p: FloorPreview): PricingFloorConfirmation {
  const ref = refOf(change);
  return {
    entity_type: ENTITY_OF[change.kind],
    entity_id: savedId,
    product_ids: affectedItems(p).map((i) => i.productId),
    ...(ref === null ? {} : { ref_entity_type: ref.type, ref_entity_id: ref.id }),
  };
}
